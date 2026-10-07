package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// newTestStore returns a jsonStore on a fresh Data file in a temporary directory.
func newTestStore(t *testing.T, maxTasks int) *jsonStore {
	t.Helper()
	return newJSONStore(filepath.Join(t.TempDir(), "tasks.json"), maxTasks, time.Second)
}

// sameJSON fails unless a and b serialise identically (time zones included).
func sameJSON(t *testing.T, got, want any) {
	t.Helper()
	g, err1 := json.Marshal(got)
	w, err2 := json.Marshal(want)
	if err1 != nil || err2 != nil || string(g) != string(w) {
		t.Errorf("got %s, want %s", g, w)
	}
}

func TestStore_Fresh(t *testing.T) {
	t.Parallel()

	s := newTestStore(t, 10)
	projects, err1 := s.ListProjects()
	tasks, err2 := s.ListTasks()
	if err1 != nil || err2 != nil || projects == nil || tasks == nil || len(projects)+len(tasks) != 0 {
		t.Errorf("lists = %v %v (%v, %v), want two empty non-nil lists", projects, tasks, err1, err2)
	}
	if _, err := os.Stat(s.path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("reading created the Data file (stat error: %v)", err)
	}
}

func TestStore_CreateProject(t *testing.T) {
	t.Parallel()

	s := newTestStore(t, 10)
	created, err := s.CreateProject(Project{Name: "Home", CreatedAt: parisNow(t)})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if !regexp.MustCompile(`^p_[a-z2-7]{6}$`).MatchString(created.ID) {
		t.Errorf("ID = %q, want p_ and 6 characters", created.ID)
	}

	// A second store on the same file sees it: it was persisted, not cached.
	listed, err := newJSONStore(s.path, s.maxTasks, time.Second).ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, listed, []Project{created})

	entries, err := os.ReadDir(filepath.Dir(s.path))
	if err != nil || len(entries) != 1 || entries[0].Name() != "tasks.json" {
		t.Errorf("directory = %v (%v), want only tasks.json (no temporary file left)", entries, err)
	}
	if info, err := os.Stat(s.path); runtime.GOOS != "windows" && (err != nil || info.Mode().Perm() != 0o600) {
		t.Errorf("Data file mode = %v (%v), want 600", info.Mode(), err)
	}
}

func TestStore_ProjectRules(t *testing.T) {
	t.Parallel()

	s := newTestStore(t, 10)
	first, err := s.CreateProject(Project{Name: "Home"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateProject(Project{Name: "hOME"})
	if err == nil || !strings.Contains(err.Error(), first.ID) {
		t.Errorf("duplicate name error = %v, want one giving the existing ID %s", err, first.ID)
	}

	for i := 1; i < maxProjects; i++ {
		if _, err := s.CreateProject(Project{Name: fmt.Sprintf("project %d", i)}); err != nil {
			t.Fatalf("project %d: %v", i, err)
		}
	}
	if _, err := s.CreateProject(Project{Name: "one too many"}); !errors.Is(err, ErrLimit) {
		t.Errorf("project %d error = %v, want ErrLimit", maxProjects+1, err)
	}
}

// sampleTask is a valid Task without ID, as newTask returns it.
func sampleTask(t *testing.T, title, project string) Task {
	t.Helper()
	task, err := newTask(TaskInput{Title: title, Project: project}, parisNow(t))
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// mustRead returns the Data file's bytes.
func mustRead(t *testing.T, s *jsonStore) string {
	t.Helper()
	data, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestStore_Tasks(t *testing.T) {
	t.Parallel()

	s := newTestStore(t, 3)
	project, err := s.CreateProject(Project{Name: "Home"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateTask(sampleTask(t, "Buy milk", project.ID))
	if err != nil || !regexp.MustCompile(`^t_[a-z2-7]{6}$`).MatchString(created.ID) {
		t.Fatalf("CreateTask = %+v (%v), want a t_ ID", created, err)
	}

	got, err := newJSONStore(s.path, s.maxTasks, time.Second).GetTask(strings.ToUpper(created.ID)) // another store, an ID typed in capitals
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	sameJSON(t, got, created)
	if listed, _ := s.ListTasks(); len(listed) != 1 {
		t.Errorf("ListTasks = %v, want the one Task", listed)
	}

	if _, err := s.CreateTask(sampleTask(t, "Orphan", "p_nope22")); !errors.Is(err, ErrInvalidProject) {
		t.Errorf("unknown project error = %v, want ErrInvalidProject", err)
	}
	for i := 1; i < 3; i++ {
		if _, err := s.CreateTask(sampleTask(t, "Task", "")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateTask(sampleTask(t, "One too many", "")); !errors.Is(err, ErrLimit) {
		t.Errorf("fourth task error = %v, want ErrLimit (max_tasks is 3)", err)
	}
	if _, err := s.GetTask("t_nope22"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetTask of an unknown ID = %v, want ErrNotFound", err)
	}
}

func TestStore_UnusableDataFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"empty", "", "empty or corrupt"},
		{"not JSON", "{not json", "empty or corrupt"},
		{"data after the document", `{"schema_version":1}{}`, "empty or corrupt"},
		{"closing brace after the document", `{"schema_version":1}}`, "empty or corrupt"},
		{"unknown field", `{"schema_version":1,"extra":true}`, "empty or corrupt"},
		{"no schema_version", `{"projects":[],"tasks":[]}`, "schema_version 0"},
		{"newer schema_version", `{"schema_version":2}`, "schema_version 2"},
		{"too large", `{"schema_version":1}` + strings.Repeat(" ", maxFileSize), "larger than 16 MiB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := newTestStore(t, 10)
			if err := os.WriteFile(s.path, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, readErr := s.ListTasks()
			_, writeErr := s.CreateProject(Project{Name: "Home"})
			for op, err := range map[string]error{"read": readErr, "write": writeErr} {
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Errorf("%s error = %v, want one containing %q", op, err, tt.want)
				}
			}
			if after := mustRead(t, s); after != tt.content {
				t.Error("the unusable Data file was modified")
			}
		})
	}
}

func TestStore_MinimalDataFile(t *testing.T) {
	t.Parallel()

	s := newTestStore(t, 10)
	if err := os.WriteFile(s.path, []byte(`{"schema_version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	projects, err := s.ListProjects()
	if err != nil || projects == nil || len(projects) != 0 {
		t.Errorf("ListProjects = %v (%v), want an empty non-nil list", projects, err)
	}
}

func TestStore_IDCollisions(t *testing.T) {
	t.Parallel()

	s := newTestStore(t, 10)
	taken, err := s.CreateTask(sampleTask(t, "First", ""))
	if err != nil {
		t.Fatal(err)
	}

	draws := []string{taken.ID, taken.ID, "t_free22"}
	s.newID = func(string) string { d := draws[0]; draws = draws[1:]; return d }
	got, err := s.CreateTask(sampleTask(t, "Second", ""))
	if err != nil || got.ID != "t_free22" {
		t.Errorf("CreateTask = %q (%v), want to retry past the two taken IDs", got.ID, err)
	}

	calls := 0
	s.newID = func(string) string { calls++; return taken.ID }
	if _, err := s.CreateTask(sampleTask(t, "Third", "")); err == nil {
		t.Error("CreateTask succeeded although every drawn ID is taken")
	}
	if calls != idAttempts {
		t.Errorf("drew %d IDs, want %d attempts", calls, idAttempts)
	}
}

func TestStore_WriteFailure(t *testing.T) {
	t.Parallel()

	s := newJSONStore(filepath.Join(t.TempDir(), "missing", "tasks.json"), 3, time.Second)
	got, err := s.CreateProject(Project{Name: "Home"})
	if err == nil || got.ID != "" {
		t.Errorf("CreateProject = %+v (%v), want a zero Project and an error", got, err)
	}
}
