package main

import (
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"
)

// writeFixture is a server with one project and one task, both created two hours
// before the server's clock (09:30 +02:00), so that updated_at moves visibly.
type writeFixture struct {
	srv   *server.MCPServer
	store *jsonStore
	home  Project
	task  Task
}

func newWriteFixture(t *testing.T) writeFixture {
	t.Helper()
	srv, store := newTestServer(t, 50)
	earlier := parisNow(t).Add(-2 * time.Hour)
	home, err := store.CreateProject(Project{Name: "Home", CreatedAt: earlier})
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(Task{
		Title: "Pay rent", Notes: "before the 20th", Status: StatusTodo, Priority: PriorityNormal, Due: "2026-07-18",
		Project: home.ID, Tags: []string{"home"}, CreatedAt: earlier, UpdatedAt: earlier,
	})
	if err != nil {
		t.Fatal(err)
	}
	return writeFixture{srv, store, home, task}
}

// call runs a tool and decodes the task it answers.
func (f writeFixture) call(t *testing.T, tool string, args map[string]any) (Task, string) {
	t.Helper()
	text, isErr := callTool(t, f.srv, tool, args)
	var got map[string]Task
	if err := json.Unmarshal([]byte(text), &got); isErr || err != nil {
		t.Fatalf("%s(%v) = %q (error %v, %v)", tool, args, text, isErr, err)
	}
	return got["task"], text
}

const (
	createdText = `"created_at":"2026-07-14T07:30:00+02:00"`
	updatedNow  = `"updated_at":"2026-07-14T09:30:00+02:00"`
	completedAt = `"completed_at":"2026-07-14T09:30:00+02:00"`
)

func TestUpdateTask(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		args  func(f writeFixture) map[string]any
		check func(t *testing.T, f writeFixture, got Task, text string)
	}{
		{
			"every field",
			func(writeFixture) map[string]any {
				return map[string]any{"title": "Pay the rent", "notes": "paid", "status": "doing", "priority": "high", "due": "2026-07-25", "tags": []string{"Money"}}
			},
			func(t *testing.T, f writeFixture, got Task, text string) {
				if got.Title != "Pay the rent" || got.Notes != "paid" || got.Status != StatusDoing || got.Priority != PriorityHigh ||
					got.Due != "2026-07-25" || !slices.Equal(got.Tags, []string{"money"}) || got.Project != f.home.ID ||
					!strings.Contains(text, createdText+","+updatedNow) {
					t.Errorf("update_task = %s, want every field changed, the project kept, updated now and created two hours ago", text)
				}
				if saved, _ := f.store.GetTask(got.ID); saved.Title != "Pay the rent" {
					t.Errorf("saved title = %q, want the update to be saved", saved.Title)
				}
			},
		},
		{
			"absent and null fields stay",
			func(writeFixture) map[string]any {
				return map[string]any{"title": "Renamed", "notes": nil, "due": nil, "project": nil, "tags": nil, "status": nil}
			},
			func(t *testing.T, f writeFixture, got Task, _ string) {
				if got.Title != "Renamed" || got.Notes != "before the 20th" || got.Due != "2026-07-18" || got.Project != f.home.ID ||
					!slices.Equal(got.Tags, []string{"home"}) || got.Status != StatusTodo {
					t.Errorf("task = %+v, want only the title changed", got)
				}
			},
		},
		{
			"empty values clear notes, due, project and tags",
			func(writeFixture) map[string]any {
				return map[string]any{"notes": "", "due": "", "project": "", "tags": []string{}}
			},
			func(t *testing.T, _ writeFixture, got Task, text string) {
				if got.Notes != "" || got.Due != "" || got.Project != "" || !strings.Contains(text, `"tags":[]`) || !strings.Contains(text, updatedNow) {
					t.Errorf("update_task = %s, want notes, due and project gone, tags [], updated now", text)
				}
			},
		},
		{
			"id and project typed in capitals",
			func(f writeFixture) map[string]any {
				return map[string]any{"id": strings.ToUpper(f.task.ID), "project": strings.ToUpper(f.home.ID)}
			},
			func(t *testing.T, f writeFixture, got Task, text string) {
				if got.ID != f.task.ID || got.Project != f.home.ID || strings.Contains(text, updatedNow) {
					t.Errorf("update_task = %s, want the same task and project, updated_at untouched", text)
				}
			},
		},
		{
			"the same values change nothing",
			func(writeFixture) map[string]any {
				return map[string]any{"title": "Pay rent", "priority": "normal", "tags": []string{"HOME"}}
			},
			func(t *testing.T, f writeFixture, got Task, text string) {
				if strings.Contains(text, updatedNow) {
					t.Errorf("update_task = %s, want updated_at left alone", text)
				}
				sameJSON(t, got, f.task)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newWriteFixture(t)
			args := tt.args(f)
			if _, given := args["id"]; !given {
				args["id"] = f.task.ID
			}
			got, text := f.call(t, "update_task", args)
			tt.check(t, f, got, text)
		})
	}
}

func TestUpdateTask_Completion(t *testing.T) {
	t.Parallel()

	f := newWriteFixture(t)
	done, text := f.call(t, "update_task", map[string]any{"id": f.task.ID, "status": "done"})
	if done.Status != StatusDone || !strings.Contains(text, completedAt) {
		t.Errorf("update_task done = %s, want completed_at set", text)
	}
	reopened, text := f.call(t, "update_task", map[string]any{"id": f.task.ID, "status": "doing"})
	if reopened.Status != StatusDoing || strings.Contains(text, "completed_at") {
		t.Errorf("update_task doing = %s, want completed_at gone", text)
	}
}

func TestUpdateTask_Refused(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args map[string]any
		want string
		leak string // part of the input the error must not repeat
	}{
		{"unknown id", map[string]any{"id": "t_secret2", "title": "x"}, "list_tasks", "secret2"},
		{"nothing to update", map[string]any{}, "nothing to update", ""},
		{"only nulls", map[string]any{"title": nil, "tags": nil}, "nothing to update", ""},
		{"empty title", map[string]any{"title": " "}, "title is required", ""},
		{"empty status", map[string]any{"status": ""}, "status cannot be cleared", ""},
		{"empty priority", map[string]any{"priority": ""}, "priority cannot be cleared", ""},
		{"unknown status", map[string]any{"status": "secret-state"}, "status", "secret-state"},
		{"unknown priority", map[string]any{"priority": "secret-level"}, "priority", "secret-level"},
		{"impossible due date", map[string]any{"due": "2026-02-30"}, "due", "2026-02-30"},
		{"unknown project", map[string]any{"project": "p_secret2"}, "list_projects", "secret2"},
		{"notes too long", map[string]any{"notes": strings.Repeat("n", 2001)}, "notes must be at most 2000", ""},
		{"too many tags", map[string]any{"tags": tagsOf(11, 3)}, "tags", ""},
		{"tags that are not a list", map[string]any{"tags": "home"}, "tags must be a list of text", ""},
		{"title of a wrong type", map[string]any{"title": 5}, "title must be text", ""},
	}
	f := newWriteFixture(t)
	before := mustRead(t, f.store)
	for _, tt := range tests {
		args := maps.Clone(tt.args)
		if _, given := args["id"]; !given {
			args["id"] = f.task.ID
		}
		text, isErr := callTool(t, f.srv, "update_task", args)
		if !isErr || !strings.Contains(text, tt.want) || (tt.leak != "" && strings.Contains(text, tt.leak)) {
			t.Errorf("%s: update_task = %q (error %v), want an error with %q, not repeating the input", tt.name, text, isErr, tt.want)
		}
	}
	if text, isErr := callTool(t, f.srv, "update_task", map[string]any{"title": "x"}); !isErr || !strings.Contains(text, "id is required") {
		t.Errorf("update_task without id = %q (error %v), want id is required", text, isErr)
	}
	if after := mustRead(t, f.store); after != before {
		t.Error("a refused update modified the Data file")
	}
}

func TestCompleteTask(t *testing.T) {
	t.Parallel()

	f := newWriteFixture(t)
	done, text := f.call(t, "complete_task", map[string]any{"id": strings.ToUpper(f.task.ID)})
	if done.Status != StatusDone || !strings.Contains(text, completedAt) || !strings.Contains(text, updatedNow) {
		t.Errorf("complete_task = %s, want done, completed and updated at 09:30", text)
	}

	// An hour later, as an assistant retrying a call it thinks failed: nothing moves.
	now := parisNow(t)
	later := newServer(Config{MaxResults: 50, Location: now.Location()}, f.store, now.Add(time.Hour).UTC)
	text, isErr := callTool(t, later, "complete_task", map[string]any{"id": f.task.ID})
	if isErr || !strings.Contains(text, completedAt) || !strings.Contains(text, updatedNow) {
		t.Errorf("second complete_task = %q (error %v), want completed_at and updated_at unchanged", text, isErr)
	}

	for name, args := range map[string]map[string]any{"unknown id": {"id": "t_secret2"}, "no id": nil} {
		if text, isErr := callTool(t, f.srv, "complete_task", args); !isErr || strings.Contains(text, "secret2") {
			t.Errorf("%s: complete_task = %q (error %v), want an error without the id", name, text, isErr)
		}
	}
}

func TestUpdateTask_NoTagsAtAllIsNotAChange(t *testing.T) {
	t.Parallel()

	f := newWriteFixture(t)
	earlier := parisNow(t).Add(-2 * time.Hour)
	bare, err := f.store.CreateTask(Task{Title: "Bare", Status: StatusTodo, Priority: PriorityNormal, CreatedAt: earlier, UpdatedAt: earlier}) // Tags is nil
	if err != nil {
		t.Fatal(err)
	}
	_, text := f.call(t, "update_task", map[string]any{"id": bare.ID, "tags": []string{}})
	if strings.Contains(text, updatedNow) || !strings.Contains(text, `"tags":[]`) {
		t.Errorf("update_task = %s, want tags [] and updated_at left alone", text)
	}
}

func TestDeleteTask(t *testing.T) {
	t.Parallel()

	f := newWriteFixture(t)
	if _, err := f.store.CreateTask(sampleTask(t, "Keep me", "")); err != nil {
		t.Fatal(err)
	}

	text, isErr := callTool(t, f.srv, "delete_task", map[string]any{"id": strings.ToUpper(f.task.ID)})
	var gone struct {
		Deleted Task `json:"deleted"`
	}
	if err := json.Unmarshal([]byte(text), &gone); isErr || err != nil || gone.Deleted.ID != f.task.ID || gone.Deleted.Title != "Pay rent" {
		t.Errorf("delete_task = %q (error %v, %v), want the deleted task back under \"deleted\"", text, isErr, err)
	}
	if left, _ := listTasks(t, f.srv, nil); !slices.Equal(titles(left.Tasks), []string{"Keep me"}) {
		t.Errorf("tasks left = %v, want only Keep me", titles(left.Tasks))
	}
	if gone.Deleted.Notes != "before the 20th" {
		t.Errorf("deleted notes = %q, want the whole task back", gone.Deleted.Notes)
	}

	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{"already deleted", map[string]any{"id": f.task.ID}, "list_tasks"},
		{"unknown id", map[string]any{"id": "t_secret2"}, "list_tasks"},
		{"no id", nil, "id is required"},
		{"blank id", map[string]any{"id": "  "}, "id is required"},
		{"id of a wrong type", map[string]any{"id": 5}, "id must be text"},
	}
	for _, tt := range tests {
		if text, isErr := callTool(t, f.srv, "delete_task", tt.args); !isErr || !strings.Contains(text, tt.want) || strings.Contains(text, "secret2") {
			t.Errorf("%s: delete_task = %q (error %v), want an error with %q, without the id", tt.name, text, isErr, tt.want)
		}
	}
}

func TestDeleteTask_TwoProcessesOneWins(t *testing.T) {
	t.Parallel()

	f := newWriteFixture(t)
	results := make(chan error, 2)
	for range 2 {
		go func() { // each with its own store on the same file, like two processes
			_, err := newJSONStore(f.store.path, 10, 20*time.Second).DeleteTask(f.task.ID)
			results <- err
		}()
	}
	first, second := <-results, <-results
	if (first == nil) == (second == nil) || (first != nil && !errors.Is(first, ErrNotFound)) || (second != nil && !errors.Is(second, ErrNotFound)) {
		t.Errorf("deletions answered %v and %v, want exactly one success and one ErrNotFound", first, second)
	}
}
