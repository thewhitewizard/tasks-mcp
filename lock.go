package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"sync"
	"time"
)

// lockPollInterval is how often a waiting process looks at the Lock again.
const lockPollInterval = 10 * time.Millisecond

// ErrLockTimeout is returned when the Lock stays held until the timeout.
var ErrLockTimeout = errors.New("lock: timed out waiting for another process to finish writing the data file")

// fileLock is the Lock: a file created with O_EXCL next to the Data file, so
// that only one process at a time can write it. It uses nothing but portable
// file operations (see docs/adr/0001-portable-lock-file.md).
type fileLock struct {
	path    string
	timeout time.Duration
}

// acquire waits up to timeout for the Lock and returns the function that
// releases it; calling that function again does nothing. The file holds the
// PID and the time, for whoever investigates a stuck Lock; nothing reads them
// back.
func (l *fileLock) acquire() (func(), error) {
	deadline := time.Now().Add(l.timeout)
	for {
		f, err := createExclusive(l.path)
		if err == nil {
			_, werr := fmt.Fprintf(f, "%d %s\n", os.Getpid(), time.Now().Format(time.RFC3339))
			if cerr := errors.Join(werr, f.Close()); cerr != nil {
				_ = os.Remove(l.path)
				return nil, fmt.Errorf("lock: %w", cerr)
			}
			var once sync.Once
			return func() { once.Do(func() { _ = os.Remove(l.path) }) }, nil
		}
		// Windows answers "access denied", not "exists", while another process
		// is deleting the Lock: wait, and report it if it lasts.
		denied := runtime.GOOS == "windows" && errors.Is(err, fs.ErrPermission)
		if !denied && !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("lock: %w", err)
		}
		l.breakIfStale()
		if time.Now().After(deadline) {
			if denied {
				return nil, fmt.Errorf("lock: %w", err)
			}
			return nil, ErrLockTimeout
		}
		time.Sleep(lockPollInterval)
	}
}

// createExclusive creates the file at path, failing if it already exists.
func createExclusive(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // the path comes from the configuration, never from a tool
}

// breakIfStale removes the Lock when nobody has touched it for lockStaleAfter:
// its owner died. Processes breaking it take turns through a guard file and
// look at the age again once they hold it. That keeps two breakers from
// removing a Lock that one of them has just seen replaced; it does not cover an
// owner that wakes up after lockStaleAfter and releases at that very moment
// (see the ADR).
func (l *fileLock) breakIfStale() {
	if !isStale(l.path) {
		return
	}
	guard := l.path + ".break"
	g, err := createExclusive(guard)
	if err != nil {
		if isStale(guard) { // its breaker died too
			_ = os.Remove(guard)
		}
		return
	}
	_ = g.Close()
	defer func() { _ = os.Remove(guard) }()
	if isStale(l.path) {
		_ = os.Remove(l.path)
	}
}

// isStale reports whether the file at path exists and was last modified more
// than lockStaleAfter ago.
func isStale(path string) bool {
	info, err := os.Stat(path)
	return err == nil && time.Since(info.ModTime()) > lockStaleAfter
}
