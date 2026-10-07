package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	return newServer(Config{MaxResults: maxResults}, store), store
}

func TestTools_List(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t, 50)
	var listed struct {
		Tools []struct {
			Name        string `json:"name"`
			Annotations struct {
				ReadOnly    *bool `json:"readOnlyHint"`
				Destructive *bool `json:"destructiveHint"`
				OpenWorld   *bool `json:"openWorldHint"`
			} `json:"annotations"`
			InputSchema struct {
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
		if a.ReadOnly == nil || !*a.ReadOnly || a.Destructive == nil || *a.Destructive || a.OpenWorld == nil || *a.OpenWorld {
			t.Errorf("%s: annotations = %+v, want readOnly true, destructive false, openWorld false (all explicit)", tool.Name, a)
		}
		if enum, _ := tool.InputSchema.Properties["status"]["enum"].([]any); tool.Name == "list_tasks" && len(enum) != 3 {
			t.Errorf("list_tasks status enum = %v, want the three statuses", enum)
		}
	}
	if slices.Sort(names); !slices.Equal(names, []string{"list_projects", "list_tasks"}) {
		t.Errorf("tools = %v, want list_projects, list_tasks", names)
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
