package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"
)

// rpc sends one JSON-RPC request to srv and returns its result.
func rpc(t *testing.T, srv *server.MCPServer, method string, params any) json.RawMessage {
	t.Helper()
	request, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(srv.HandleMessage(context.Background(), request))
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &response); err != nil || response.Error != nil {
		t.Fatalf("%s: response %s", method, raw)
	}
	return response.Result
}

// callTool calls a tool and returns the text it answered, and whether that is an error.
func callTool(t *testing.T, srv *server.MCPServer, name string, args map[string]any) (string, bool) {
	t.Helper()
	var result struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(rpc(t, srv, "tools/call", map[string]any{"name": name, "arguments": args}), &result); err != nil || len(result.Content) != 1 {
		t.Fatalf("%s: unexpected result %+v (%v)", name, result, err)
	}
	return result.Content[0].Text, result.IsError
}

// newTestServer returns a server on a fresh jsonStore, which it also returns to seed it.
func newTestServer(t *testing.T, maxResults int) (*server.MCPServer, *jsonStore) {
	t.Helper()
	store := newJSONStore(filepath.Join(t.TempDir(), "tasks.json"), 100, time.Second)
	now := parisNow(t)
	return newServer(Config{MaxResults: maxResults, Location: now.Location()}, store, now.UTC), store
}

func TestTools_List(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t, 50)
	var listed struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Annotations struct {
				ReadOnly    *bool `json:"readOnlyHint"`
				Destructive *bool `json:"destructiveHint"`
				OpenWorld   *bool `json:"openWorldHint"`
			} `json:"annotations"`
			InputSchema struct {
				Required   []string                  `json:"required"`
				Properties map[string]map[string]any `json:"properties"`
			} `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(rpc(t, srv, "tools/list", nil), &listed); err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
		a := tool.Annotations
		writes := tool.Name == "create_project" || tool.Name == "add_task"
		if a.ReadOnly == nil || *a.ReadOnly == writes || a.Destructive == nil || *a.Destructive || a.OpenWorld == nil || *a.OpenWorld {
			t.Errorf("%s: annotations = %+v, want readOnly %v, destructive false, openWorld false (all explicit)", tool.Name, a, !writes)
		}
		if want := map[string][]string{"create_project": {"name"}, "add_task": {"title"}}[tool.Name]; want != nil && !slices.Equal(tool.InputSchema.Required, want) {
			t.Errorf("%s required = %v, want %v", tool.Name, tool.InputSchema.Required, want)
		}
		if tool.Name == "add_task" {
			if enum, _ := tool.InputSchema.Properties["priority"]["enum"].([]any); len(enum) != 3 || tool.InputSchema.Properties["tags"]["type"] != "array" {
				t.Errorf("add_task priority enum = %v, tags = %v, want three priorities and a list", enum, tool.InputSchema.Properties["tags"])
			}
		}
		if enum, _ := tool.InputSchema.Properties["status"]["enum"].([]any); tool.Name == "list_tasks" && len(enum) != 3 {
			t.Errorf("list_tasks status enum = %v, want the three statuses", enum)
		}
		if tool.Name == "get_task" && !slices.Equal(tool.InputSchema.Required, []string{"id"}) {
			t.Errorf("get_task required = %v, want [id]", tool.InputSchema.Required)
		}
		if tool.Name == "list_tasks" && !strings.Contains(tool.Description, "get_task") {
			t.Error("list_tasks does not point to get_task for the notes")
		}
	}
	if slices.Sort(names); !slices.Equal(names, []string{"add_task", "create_project", "get_task", "list_projects", "list_tasks"}) {
		t.Errorf("tools = %v, want the five tools", names)
	}
}

func TestListProjects(t *testing.T) {
	t.Parallel()

	srv, store := newTestServer(t, 50)
	if text, isErr := callTool(t, srv, "list_projects", nil); isErr || text != `{"projects":[]}` {
		t.Errorf("empty list_projects = %s (error %v), want {\"projects\":[]}", text, isErr)
	}

	now := parisNow(t)
	ids := map[string]string{}
	for _, name := range []string{"work", "Garden", "Home"} {
		p, err := store.CreateProject(Project{Name: name, CreatedAt: now})
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = p.ID
	}
	want := `{"projects":[` +
		`{"id":"` + ids["Garden"] + `","name":"Garden","created_at":"2026-07-14T09:30:00+02:00"},` +
		`{"id":"` + ids["Home"] + `","name":"Home","created_at":"2026-07-14T09:30:00+02:00"},` +
		`{"id":"` + ids["work"] + `","name":"work","created_at":"2026-07-14T09:30:00+02:00"}]}`
	if text, isErr := callTool(t, srv, "list_projects", nil); isErr || text != want {
		t.Errorf("list_projects = %s (error %v), want %s", text, isErr, want)
	}
}

// seedTasks fills store with two projects and six tasks, and returns the
// projects' IDs by name. The default order of the open tasks is: pay rent,
// plan trip, call mom, write report, buy milk.
func seedTasks(t *testing.T, store *jsonStore) map[string]string {
	t.Helper()
	created := parisNow(t)
	ids := map[string]string{}
	for _, name := range []string{"Work", "Home"} {
		p, err := store.CreateProject(Project{Name: name, CreatedAt: created})
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = p.ID
	}
	for i, task := range []Task{
		{Title: "write report", Notes: "quarterly", Status: StatusTodo, Priority: PriorityHigh, Due: "2026-07-20", Project: ids["Work"], Tags: []string{"work", "urgent"}},
		{Title: "buy milk", Status: StatusTodo, Priority: PriorityNormal, Project: ids["Home"], Tags: []string{"home"}},
		{Title: "call mom", Status: StatusDoing, Priority: PriorityLow, Due: "2026-07-18", Tags: []string{}},
		{Title: "pay rent", Status: StatusTodo, Priority: PriorityHigh, Due: "2026-07-18", Project: ids["Home"], Tags: []string{"home", "money"}},
		{Title: "old chore", Status: StatusDone, Priority: PriorityNormal, Due: "2026-07-01", Project: ids["Home"], Tags: []string{"home"}},
		{Title: "plan trip", Status: StatusTodo, Priority: PriorityNormal, Due: "2026-07-18", Tags: []string{}},
	} {
		task.CreatedAt = created.Add(time.Duration(i) * time.Minute)
		task.UpdatedAt = task.CreatedAt
		if _, err := store.CreateTask(task); err != nil {
			t.Fatal(err)
		}
	}
	return ids
}

// listed is the answer of list_tasks.
type listed struct {
	Tasks     []Task `json:"tasks"`
	Truncated bool   `json:"truncated"`
}

func listTasks(t *testing.T, srv *server.MCPServer, args map[string]any) (listed, string) {
	t.Helper()
	text, isErr := callTool(t, srv, "list_tasks", args)
	if isErr {
		t.Fatalf("list_tasks(%v) failed: %s", args, text)
	}
	var out listed
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("list_tasks answered %q: %v", text, err)
	}
	return out, text
}

func titles(tasks []Task) []string {
	out := []string{}
	for _, task := range tasks {
		out = append(out, task.Title)
	}
	return out
}

func TestListTasks_Filters(t *testing.T) {
	t.Parallel()

	srv, store := newTestServer(t, 50)
	ids := seedTasks(t, store)
	open := []string{"pay rent", "plan trip", "call mom", "write report", "buy milk"}

	tests := []struct {
		name string
		args map[string]any
		want []string
	}{
		{"default: open tasks, due first, then priority", nil, open},
		{"include_done puts done tasks last", map[string]any{"include_done": true}, append(slices.Clone(open), "old chore")},
		{"status done works alone", map[string]any{"status": "done"}, []string{"old chore"}},
		{"status doing", map[string]any{"status": "doing"}, []string{"call mom"}},
		{"project, typed in capitals", map[string]any{"project": strings.ToUpper(ids["Home"])}, []string{"pay rent", "buy milk"}},
		{"due_before is inclusive and drops tasks without due", map[string]any{"due_before": "2026-07-18"}, []string{"pay rent", "plan trip", "call mom"}},
		{"due_before with include_done", map[string]any{"due_before": "2026-07-18", "include_done": true}, []string{"pay rent", "plan trip", "call mom", "old chore"}},
		{"tag ignores case", map[string]any{"tag": "HOME"}, []string{"pay rent", "buy milk"}},
		{"filters combine", map[string]any{"project": ids["Home"], "tag": "money", "status": "todo"}, []string{"pay rent"}},
		{"no match", map[string]any{"tag": "nothing"}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, _ := listTasks(t, srv, tt.args)
			if !slices.Equal(titles(got.Tasks), tt.want) {
				t.Errorf("titles = %v, want %v", titles(got.Tasks), tt.want)
			}
		})
	}
}

func TestListTasks_Shape(t *testing.T) {
	t.Parallel()

	srv, store := newTestServer(t, 50)
	if _, text := listTasks(t, srv, nil); text != `{"tasks":[]}` {
		t.Errorf("empty list_tasks = %s, want {\"tasks\":[]}", text)
	}
	seedTasks(t, store)
	got, text := listTasks(t, srv, map[string]any{"tag": "work"})
	if len(got.Tasks) != 1 || strings.Contains(text, "notes") || strings.Contains(text, "truncated") {
		t.Errorf("list_tasks = %s, want one task without its notes and without truncated", text)
	}
}

func TestListTasks_Truncated(t *testing.T) {
	t.Parallel()

	srv, store := newTestServer(t, 2)
	seedTasks(t, store)
	got, _ := listTasks(t, srv, nil)
	if !got.Truncated || !slices.Equal(titles(got.Tasks), []string{"pay rent", "plan trip"}) {
		t.Errorf("list_tasks = %v (truncated %v), want the first two, truncated", titles(got.Tasks), got.Truncated)
	}

	exact, store2 := newTestServer(t, 5)
	seedTasks(t, store2)
	if got, text := listTasks(t, exact, nil); got.Truncated || strings.Contains(text, "truncated") {
		t.Errorf("list_tasks = %s, want no truncated when everything fits", text)
	}
}

func TestListTasks_Errors(t *testing.T) {
	t.Parallel()

	srv, store := newTestServer(t, 50)
	seedTasks(t, store)
	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{"unknown status", map[string]any{"status": "secret-status"}, "status"},
		{"badly written date", map[string]any{"due_before": "18/07/2026"}, "due_before"},
		{"unknown project", map[string]any{"project": "p_nope22"}, "project"},
		{"status of the wrong type", map[string]any{"status": 5}, "status"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			text, isErr := callTool(t, srv, "list_tasks", tt.args)
			if !isErr || !strings.Contains(text, tt.want) || strings.Contains(text, "secret-status") {
				t.Errorf("list_tasks = %q (error %v), want an error naming %q without repeating the input", text, isErr, tt.want)
			}
		})
	}
}

func TestTools_UnusableDataFile(t *testing.T) {
	t.Parallel()

	srv, store := newTestServer(t, 50)
	if err := os.WriteFile(store.path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"list_projects", "list_tasks"} {
		if text, isErr := callTool(t, srv, name, nil); !isErr || !strings.Contains(text, "corrupt") {
			t.Errorf("%s = %q (error %v), want an error about the corrupt data file", name, text, isErr)
		}
	}
}

func TestGetTask(t *testing.T) {
	t.Parallel()

	srv, store := newTestServer(t, 50)
	seedTasks(t, store)
	all, err := store.ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	report := all[slices.IndexFunc(all, func(task Task) bool { return task.Title == "write report" })] // the only seeded task with notes

	text, isErr := callTool(t, srv, "get_task", map[string]any{"id": strings.ToUpper(report.ID)})
	var got struct {
		Task Task `json:"task"`
	}
	if err := json.Unmarshal([]byte(text), &got); isErr || err != nil {
		t.Fatalf("get_task = %q (error %v, %v)", text, isErr, err)
	}
	sameJSON(t, got.Task, report)
	if got.Task.Notes != "quarterly" {
		t.Errorf("notes = %q, want them in get_task", got.Task.Notes)
	}

	for name, tt := range map[string]struct {
		args map[string]any
		want string
	}{
		"no id":              {nil, "id is required"},
		"unknown id":         {map[string]any{"id": "t_secret2"}, "list_tasks"},
		"id of a wrong type": {map[string]any{"id": 5}, "id must be text"},
	} {
		if text, isErr := callTool(t, srv, "get_task", tt.args); !isErr || !strings.Contains(text, tt.want) || strings.Contains(text, "secret2") {
			t.Errorf("%s: get_task = %q (error %v), want an error with %q, not repeating the id", name, text, isErr, tt.want)
		}
	}

	if err := os.WriteFile(store.path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if text, isErr := callTool(t, srv, "get_task", map[string]any{"id": report.ID}); !isErr || !strings.Contains(text, "corrupt") {
		t.Errorf("get_task on a corrupt file = %q (error %v), want the store's error", text, isErr)
	}
}

func TestCreateProject(t *testing.T) {
	t.Parallel()

	srv, store := newTestServer(t, 50)
	text, isErr := callTool(t, srv, "create_project", map[string]any{"name": " Home" + zwsp, "description": "Chores"})
	var got struct {
		Project Project `json:"project"`
	}
	if err := json.Unmarshal([]byte(text), &got); isErr || err != nil {
		t.Fatalf("create_project = %q (error %v, %v)", text, isErr, err)
	}
	if got.Project.Name != "Home" || got.Project.Description != "Chores" || !strings.Contains(text, `"created_at":"2026-07-14T09:30:00+02:00"`) {
		t.Errorf("create_project = %s, want the cleaned project created at 09:30 +02:00", text)
	}
	if listed, _ := store.ListProjects(); len(listed) != 1 || listed[0].ID != got.Project.ID {
		t.Errorf("projects = %v, want the created one to be saved", listed)
	}

	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{"same name in other letters", map[string]any{"name": "HOME"}, got.Project.ID},
		{"no name", nil, "name is required"},
		{"name too long", map[string]any{"name": strings.Repeat("n", 101)}, "name must be at most 100"},
		{"name of a wrong type", map[string]any{"name": 5}, "name must be text"},
		{"description of a wrong type", map[string]any{"name": "Work", "description": 5}, "description must be text"},
		{"description too long", map[string]any{"name": "Work", "description": strings.Repeat("d", 501)}, "description must be at most 500"},
	}
	for _, tt := range tests {
		if text, isErr := callTool(t, srv, "create_project", tt.args); !isErr || !strings.Contains(text, tt.want) {
			t.Errorf("%s: create_project = %q (error %v), want an error with %q", tt.name, text, isErr, tt.want)
		}
	}
}

func TestAddTask(t *testing.T) {
	t.Parallel()

	srv, store := newTestServer(t, 50)
	home, err := store.CreateProject(Project{Name: "Home"})
	if err != nil {
		t.Fatal(err)
	}
	add := func(args map[string]any) (Task, string) {
		t.Helper()
		text, isErr := callTool(t, srv, "add_task", args)
		var got struct {
			Task Task `json:"task"`
		}
		if err := json.Unmarshal([]byte(text), &got); isErr || err != nil {
			t.Fatalf("add_task(%v) = %q (error %v, %v)", args, text, isErr, err)
		}
		return got.Task, text
	}

	minimal, text := add(map[string]any{"title": "Buy milk"})
	if !regexp.MustCompile(`^t_[a-z2-7]{6}$`).MatchString(minimal.ID) || minimal.Status != StatusTodo || minimal.Priority != PriorityNormal ||
		!strings.Contains(text, `"tags":[]`) || !strings.Contains(text, `"created_at":"2026-07-14T09:30:00+02:00","updated_at":"2026-07-14T09:30:00+02:00"`) {
		t.Errorf("minimal add_task = %s, want a todo/normal task with no tags, created and updated at 09:30 +02:00", text)
	}

	full, _ := add(map[string]any{
		"title": "Pay rent", "notes": "before the 20th", "project": strings.ToUpper(home.ID), "due": "2026-07-18",
		"priority": "high", "tags": []string{"Home", " urgent", "home"},
	})
	if full.Project != home.ID || full.Due != "2026-07-18" || full.Priority != PriorityHigh || full.Notes != "before the 20th" || !slices.Equal(full.Tags, []string{"home", "urgent"}) {
		t.Errorf("full add_task = %+v, want every field kept, the project id lowercased and the tags cleaned", full)
	}

	for _, task := range []Task{minimal, full} {
		if saved, err := store.GetTask(task.ID); err != nil {
			t.Errorf("task %s was not saved: %v", task.ID, err)
		} else {
			sameJSON(t, saved, task)
		}
	}
	if listedTasks, _ := listTasks(t, srv, nil); len(listedTasks.Tasks) != 2 {
		t.Errorf("list_tasks = %v, want both added tasks", titles(listedTasks.Tasks))
	}
}

func TestAddTask_Errors(t *testing.T) {
	t.Parallel()

	srv, store := newTestServer(t, 50)
	tests := []struct {
		name string
		args map[string]any
		want string
		leak string // part of the input the error must not repeat
	}{
		{"no title", nil, "title is required", ""},
		{"notes too long", map[string]any{"title": "t", "notes": strings.Repeat("n", 2001)}, "notes must be at most 2000", ""},
		{"blank title", map[string]any{"title": " "}, "title is required", ""},
		{"title of a wrong type", map[string]any{"title": 5}, "title must be text", ""},
		{"impossible due date", map[string]any{"title": "t", "due": "2026-02-30"}, "due", "2026-02-30"},
		{"unknown priority", map[string]any{"title": "t", "priority": "secret-level"}, "priority", "secret-level"},
		{"unknown project", map[string]any{"title": "t", "project": "p_secret2"}, "list_projects", "secret2"},
		{"tags that are not a list", map[string]any{"title": "t", "tags": "home"}, "tags must be a list of text", ""},
		{"tags with a number", map[string]any{"title": "t", "tags": []any{"home", 5}}, "tags must be a list of text", ""},
		{"too many tags", map[string]any{"title": "t", "tags": tagsOf(11, 3)}, "tags", ""},
	}
	for _, tt := range tests {
		text, isErr := callTool(t, srv, "add_task", tt.args)
		if !isErr || !strings.Contains(text, tt.want) || (tt.leak != "" && strings.Contains(text, tt.leak)) {
			t.Errorf("%s: add_task = %q (error %v), want an error with %q, not repeating the input", tt.name, text, isErr, tt.want)
		}
	}
	if saved, _ := store.ListTasks(); len(saved) != 0 {
		t.Errorf("%d tasks were saved by refused calls", len(saved))
	}
}

func TestAddTask_LimitReached(t *testing.T) {
	t.Parallel()

	now := parisNow(t)
	store := newJSONStore(filepath.Join(t.TempDir(), "tasks.json"), 1, time.Second)
	srv := newServer(Config{MaxResults: 50, Location: now.Location()}, store, func() time.Time { return now })
	if _, isErr := callTool(t, srv, "add_task", map[string]any{"title": "first"}); isErr {
		t.Fatal("the first task was refused")
	}
	if text, isErr := callTool(t, srv, "add_task", map[string]any{"title": "second"}); !isErr || !strings.Contains(text, "limit") {
		t.Errorf("add_task over max_tasks = %q (error %v), want a limit error", text, isErr)
	}
}

func TestWriteTools_NullsAreAbsent(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t, 50)
	args := map[string]any{"title": "t", "notes": nil, "project": nil, "due": nil, "priority": nil, "tags": nil}
	if text, isErr := callTool(t, srv, "add_task", args); isErr {
		t.Errorf("add_task with null optional arguments = %q, want it to be accepted", text)
	}
	if text, isErr := callTool(t, srv, "create_project", map[string]any{"name": "Home", "description": nil}); isErr {
		t.Errorf("create_project with a null description = %q, want it to be accepted", text)
	}
}

func TestWriteTools_NoFilePathInErrors(t *testing.T) {
	t.Parallel()

	now := parisNow(t)
	store := newJSONStore(filepath.Join(t.TempDir(), "secret-dir", "tasks.json"), 10, time.Second) // its directory does not exist
	srv := newServer(Config{MaxResults: 50, Location: now.Location()}, store, now.UTC)
	for name, args := range map[string]map[string]any{"add_task": {"title": "t"}, "create_project": {"name": "Home"}} {
		if text, isErr := callTool(t, srv, name, args); !isErr || strings.Contains(text, "secret-dir") {
			t.Errorf("%s = %q (error %v), want an error that does not give the path of the Data file", name, text, isErr)
		}
	}
}
