package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func lockPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "tasks.json.lock")
}

func TestFileLock_Exclusion(t *testing.T) {
	t.Parallel()

	path := lockPath(t)
	release, err := (&fileLock{path: path, timeout: time.Second}).acquire()
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if content, _ := os.ReadFile(path); !strings.Contains(string(content), strconv.Itoa(os.Getpid())) { //nolint:gosec // a test file
		t.Errorf("lock file = %q, want it to hold this process's PID", content)
	}

	if _, err := (&fileLock{path: path, timeout: 50 * time.Millisecond}).acquire(); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("acquire while held: error = %v, want a timeout", err)
	}

	go func() { time.Sleep(100 * time.Millisecond); release() }()
	second, err := (&fileLock{path: path, timeout: 5 * time.Second}).acquire()
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	second()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("lock file still there after release (stat error: %v)", err)
	}
}

// leftoverLock creates a Lock file that nobody will release, last touched age ago.
func leftoverLock(t *testing.T, path string, age time.Duration) {
	t.Helper()
	if err := os.WriteFile(path, []byte("99999 long ago\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-age)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

func TestFileLock_Stale(t *testing.T) {
	t.Parallel()

	t.Run("a Lock untouched for 31 seconds is taken over", func(t *testing.T) {
		t.Parallel()

		path := lockPath(t)
		leftoverLock(t, path, 31*time.Second)
		release, err := (&fileLock{path: path, timeout: time.Second}).acquire()
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		release()
		if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 0 {
			t.Errorf("directory = %v, want it empty (no broken Lock left behind)", entries)
		}
	})

	t.Run("a Lock of 5 seconds is still held", func(t *testing.T) {
		t.Parallel()

		path := lockPath(t)
		leftoverLock(t, path, 5*time.Second)
		if _, err := (&fileLock{path: path, timeout: 50 * time.Millisecond}).acquire(); err == nil {
			t.Error("acquire took over a Lock that is not stale")
		}
	})

	t.Run("several processes finding it stale take turns", func(t *testing.T) {
		t.Parallel()

		path := lockPath(t)
		leftoverLock(t, path, time.Minute)
		var inside, overlaps atomic.Int32
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				release, err := (&fileLock{path: path, timeout: 10 * time.Second}).acquire()
				if err != nil {
					t.Errorf("acquire: %v", err)
					return
				}
				if inside.Add(1) > 1 {
					overlaps.Add(1)
				}
				time.Sleep(5 * time.Millisecond)
				inside.Add(-1)
				release()
			}()
		}
		wg.Wait()
		if overlaps.Load() != 0 {
			t.Errorf("%d holders were in the critical section at the same time", overlaps.Load())
		}
	})
}

func TestStore_ConcurrentWriters(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "tasks.json")
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { // each writer has its own store, like separate processes
			defer wg.Done()
			s := newJSONStore(path, 100, 20*time.Second)
			for range 10 {
				if _, err := s.CreateTask(sampleTask(t, "Task", "")); err != nil {
					t.Errorf("CreateTask: %v", err)
				}
			}
		}()
	}
	wg.Wait()
	assertTaskCount(t, path, 80)
}

// assertTaskCount fails unless the Data file holds exactly want Tasks, with distinct IDs.
func assertTaskCount(t *testing.T, path string, want int) {
	t.Helper()
	tasks, err := newJSONStore(path, 100, time.Second).ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, task := range tasks {
		ids[task.ID] = true
	}
	if len(tasks) != want || len(ids) != want {
		t.Errorf("the Data file holds %d Tasks (%d distinct IDs), want %d: a write was lost", len(tasks), len(ids), want)
	}
}

// TestHelperWriter is the body of the processes TestStore_ConcurrentProcesses
// starts; run alone, it does nothing.
func TestHelperWriter(t *testing.T) {
	path := os.Getenv("TASKS_TEST_DATA_FILE")
	if path == "" {
		t.Skip("only meaningful as a child process")
	}
	s := newJSONStore(path, 100, 20*time.Second)
	for range 10 {
		if _, err := s.CreateTask(sampleTask(t, "Task", "")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStore_ConcurrentProcesses(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "tasks.json")
	var children []*exec.Cmd
	for range 3 {
		cmd := exec.Command(os.Args[0], "-test.run=^TestHelperWriter$") //nolint:gosec // re-runs this test binary
		cmd.Env = append(os.Environ(), "TASKS_TEST_DATA_FILE="+path)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		children = append(children, cmd)
	}
	for _, cmd := range children {
		if err := cmd.Wait(); err != nil {
			t.Errorf("writer process: %v", err)
		}
	}
	assertTaskCount(t, path, 30)
}

func TestStore_WriteWhileLocked(t *testing.T) {
	t.Parallel()

	s := newJSONStore(filepath.Join(t.TempDir(), "tasks.json"), 10, 50*time.Millisecond)
	release, err := (&fileLock{path: s.path + ".lock", timeout: time.Second}).acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	if _, err := s.CreateTask(sampleTask(t, "Task", "")); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("CreateTask while another process writes: error = %v, want a timeout", err)
	}
	if _, err := os.Stat(s.path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the timed-out write created the Data file (stat error: %v)", err)
	}
	if _, err := s.ListTasks(); err != nil {
		t.Errorf("a read was blocked by the Lock: %v", err)
	}
}
