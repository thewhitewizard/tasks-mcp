package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// newServer builds the MCP server and its tools. No tool takes a file path: the
// Data file comes from the configuration only.
func newServer(cfg Config, store Store, clock func() time.Time) *server.MCPServer {
	h := &handlers{store: store, maxResults: cfg.MaxResults, clock: clock, location: cfg.Location}
	s := server.NewMCPServer("tasks-mcp", version, server.WithRecovery())
	s.AddTool(mcp.NewTool("list_projects", readOnly(),
		mcp.WithDescription("List all projects, sorted by name. Each has an id (p_ followed by 6 characters, e.g. p_k3x9aq) "+
			"to pass as `project` to list_tasks and add_task. Example answer: {\"projects\":[{\"id\":\"p_k3x9aq\",\"name\":\"Home\",\"created_at\":\"2026-07-14T09:30:00+02:00\"}]}."),
	), h.listProjects)
	s.AddTool(mcp.NewTool("list_tasks", readOnly(),
		mcp.WithDescription("List tasks, open ones first, then by due date (earliest first, no due date last), priority (high first) and creation. "+
			"Filters combine (AND). By default done tasks are left out. Notes are not included: call get_task for them. "+
			"At most "+strconv.Itoa(cfg.MaxResults)+" tasks are returned; `truncated` is true when there were more (it is absent otherwise), so narrow the filters. "+
			"Example answer: {\"tasks\":[{\"id\":\"t_a4c7mz\",\"title\":\"Pay rent\",\"status\":\"todo\",\"priority\":\"high\",\"due\":\"2026-07-18\",\"tags\":[\"home\"],...}]}. due_before 2026-07-20 gives what is due by that day, that day included."),
		mcp.WithString("project", mcp.Description("Only this project: its id from list_projects (p_ followed by 6 characters).")),
		mcp.WithString("status", mcp.Enum(string(StatusTodo), string(StatusDoing), string(StatusDone)), mcp.Description("Only this status (lowercase). done lists done tasks even without include_done.")),
		mcp.WithString("due_before", mcp.Description("Only tasks due on or before this day, YYYY-MM-DD. Tasks without a due date are left out.")),
		mcp.WithString("tag", mcp.Description("Only tasks with this tag (not case sensitive).")),
		mcp.WithBoolean("include_done", mcp.Description("Also list done tasks. Default: false.")),
	), h.listTasks)
	s.AddTool(mcp.NewTool("get_task", readOnly(),
		mcp.WithDescription("Get one task with all its fields, notes included. Dates are YYYY-MM-DD (due) or ISO 8601 with offset (the others). "+
			"Example answer: {\"task\":{\"id\":\"t_a4c7mz\",\"title\":\"Pay rent\",\"notes\":\"Transfer before the 20th\",\"status\":\"todo\",\"priority\":\"high\",\"due\":\"2026-07-18\",\"tags\":[\"home\"],...}}."),
		mcp.WithString("id", mcp.Required(), mcp.Description("The task id from list_tasks: t_ followed by 6 characters, e.g. t_a4c7mz.")),
	), h.getTask)
	s.AddTool(mcp.NewTool("create_project", writer(),
		mcp.WithDescription("Create a project to group tasks. Names are unique, not case sensitive: if the name exists, the error gives the id of the existing project. "+
			"Returns the created project, e.g. {\"project\":{\"id\":\"p_k3x9aq\",\"name\":\"Home\",\"created_at\":\"2026-07-14T09:30:00+02:00\"}}."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Project name, at most 100 characters.")),
		mcp.WithString("description", mcp.Description("Optional description, at most 500 characters.")),
	), h.createProject)
	s.AddTool(mcp.NewTool("add_task", writer(),
		mcp.WithDescription("Add a task. It starts as todo. To put it in a project, give that project's id (from list_projects or create_project). Returns the created task with its id (t_ followed by 6 characters), "+
			"e.g. {\"task\":{\"id\":\"t_a4c7mz\",\"title\":\"Pay rent\",\"status\":\"todo\",\"priority\":\"high\",\"due\":\"2026-07-18\",\"tags\":[\"home\"],...}}. "+
			"There is no time of day: due is a day."),
		mcp.WithString("title", mcp.Required(), mcp.Description("What to do, at most 200 characters, one line.")),
		mcp.WithString("notes", mcp.Description("Optional details, at most 2000 characters.")),
		mcp.WithString("project", mcp.Description("Optional project id from list_projects (p_ followed by 6 characters).")),
		mcp.WithString("due", mcp.Description("Optional due day, YYYY-MM-DD.")),
		mcp.WithString("priority", mcp.Enum(string(PriorityLow), string(PriorityNormal), string(PriorityHigh)), mcp.Description("Optional, default normal.")),
		mcp.WithArray("tags", mcp.WithStringItems(), mcp.Description("Optional short labels (at most 10, 30 characters each), lowercased when saved.")),
	), h.addTask)
	s.AddTool(mcp.NewTool("update_task", writer(),
		mcp.WithDescription("Change some fields of a task; the fields you leave out stay as they are. An empty string clears notes, due and project; tags replace the old list (an empty list clears it). "+
			"title, status and priority cannot be cleared. Setting status to done records the completion; any other status removes it. "+
			"Returns the whole updated task, e.g. {\"task\":{\"id\":\"t_a4c7mz\",\"title\":\"Pay rent\",\"status\":\"doing\",...}}. Give at least one field."),
		mcp.WithString("id", mcp.Required(), mcp.Description("The task id from list_tasks or add_task: t_ followed by 6 characters.")),
		mcp.WithString("title", mcp.Description("New title, at most 200 characters; it cannot be empty.")),
		mcp.WithString("notes", mcp.Description("New notes, at most 2000 characters; empty to clear.")),
		mcp.WithString("status", mcp.Enum(string(StatusTodo), string(StatusDoing), string(StatusDone)), mcp.Description("New status; it cannot be empty.")),
		mcp.WithString("priority", mcp.Enum(string(PriorityLow), string(PriorityNormal), string(PriorityHigh)), mcp.Description("New priority; it cannot be empty.")),
		mcp.WithString("due", mcp.Description("New due day, e.g. 2026-07-25 (YYYY-MM-DD); empty to clear.")),
		mcp.WithString("project", mcp.Description("New project id from list_projects (p_ followed by 6 characters); empty to remove the task from its project.")),
		mcp.WithArray("tags", mcp.WithStringItems(), mcp.Description("The new tags, replacing the old ones (at most 10, 30 characters each); an empty list clears them.")),
	), h.updateTask)
	s.AddTool(mcp.NewTool("complete_task", writer(),
		mcp.WithDescription("Mark a task as done and record when. Safe to repeat: a task that is already done is returned unchanged. Returns the whole task, e.g. {\"task\":{\"id\":\"t_a4c7mz\",\"status\":\"done\",\"completed_at\":\"2026-07-14T09:30:00+02:00\",...}}."),
		mcp.WithString("id", mcp.Required(), mcp.Description("The task id from list_tasks or add_task: t_ followed by 6 characters.")),
	), h.completeTask)
	s.AddTool(mcp.NewTool("delete_task", destructive(),
		mcp.WithDescription("Delete a task for good; it cannot be undone. Use complete_task for a task that is merely finished. Returns the deleted task, e.g. {\"deleted\":{\"id\":\"t_a4c7mz\",\"title\":\"Pay rent\",...}}."),
		mcp.WithString("id", mcp.Required(), mcp.Description("The task id from list_tasks or add_task: t_ followed by 6 characters.")),
	), h.deleteTask)
	return s
}

// readOnly marks a tool as reading only, closed-world, and not destructive:
// mcp-go would otherwise default destructiveHint and openWorldHint to true.
func readOnly() mcp.ToolOption { return hints(true, false) }

// writer marks a tool as changing data, closed-world, and not destructive.
func writer() mcp.ToolOption { return hints(false, false) }

// destructive marks a tool as deleting data: only delete_task.
func destructive() mcp.ToolOption { return hints(false, true) }

func hints(readOnly, destructive bool) mcp.ToolOption {
	return func(t *mcp.Tool) {
		mcp.WithReadOnlyHintAnnotation(readOnly)(t)
		mcp.WithDestructiveHintAnnotation(destructive)(t)
		mcp.WithOpenWorldHintAnnotation(false)(t)
	}
}

// What the assistant is told when an id matches nothing.
const (
	unknownProject = "project: no project with this id; list_projects gives the ids"
	unknownTask    = "id: no task with this id; list_tasks gives the ids"
)

// handlers are the tool handlers, with what they need.
type handlers struct {
	store      Store
	maxResults int
	clock      func() time.Time
	location   *time.Location
}

// now is the time of the call, in the configured timezone.
func (h *handlers) now() time.Time { return h.clock().In(h.location) }

// jsonResult answers with compact JSON text.
func jsonResult(v any) *mcp.CallToolResult {
	data, _ := json.Marshal(v) // the results are plain structs: they always marshal
	return mcp.NewToolResultText(string(data))
}

func (h *handlers) listProjects(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	projects, err := h.store.ListProjects()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	slices.SortFunc(projects, func(a, b Project) int {
		if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return jsonResult(struct {
		Projects []Project `json:"projects"`
	}{projects}), nil
}

// Sort ranks: high priority first, done tasks last.
var (
	priorityRank = map[Priority]int{PriorityHigh: 0, PriorityNormal: 1, PriorityLow: 2}
	doneRank     = map[Status]int{StatusDone: 1}
)

func (h *handlers) listTasks(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, err := textArgs(req, "project", "status", "due_before", "tag") // project, status, due_before, tag
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	status, err := parseEnum("status", args[1], Status(""), StatusTodo, StatusDoing, StatusDone)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	dueBefore := args[2]
	if _, err := time.Parse(time.DateOnly, dueBefore); dueBefore != "" && err != nil {
		return mcp.NewToolResultError("due_before must be a real date written YYYY-MM-DD"), nil
	}
	project, tag := normalizeID(args[0]), strings.ToLower(args[3])
	includeDone := req.GetBool("include_done", false)

	if project != "" {
		projects, err := h.store.ListProjects()
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if !slices.ContainsFunc(projects, func(p Project) bool { return p.ID == project }) {
			return mcp.NewToolResultError(unknownProject), nil
		}
	}
	all, err := h.store.ListTasks()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	result := struct {
		Tasks     []Task `json:"tasks"`
		Truncated bool   `json:"truncated,omitempty"`
	}{Tasks: []Task{}}
	for _, task := range all {
		switch {
		case status != "" && task.Status != status,
			status == "" && !includeDone && task.Status == StatusDone,
			project != "" && task.Project != project,
			dueBefore != "" && (task.Due == "" || task.Due > dueBefore),
			tag != "" && !slices.ContainsFunc(task.Tags, func(t string) bool { return strings.EqualFold(t, tag) }):
			continue
		}
		task.Notes = "" // get_task gives the notes
		result.Tasks = append(result.Tasks, task)
	}
	slices.SortFunc(result.Tasks, compareTasks)
	if len(result.Tasks) > h.maxResults {
		result.Tasks, result.Truncated = result.Tasks[:h.maxResults], true
	}
	return jsonResult(result), nil
}

// compareTasks orders open tasks before done ones, then by due date (none
// last), priority (high first), creation and ID.
func compareTasks(a, b Task) int {
	return cmp.Or(
		cmp.Compare(doneRank[a.Status], doneRank[b.Status]),
		strings.Compare(dueKey(a), dueKey(b)),
		cmp.Compare(priorityRank[a.Priority], priorityRank[b.Priority]),
		a.CreatedAt.Compare(b.CreatedAt),
		strings.Compare(a.ID, b.ID),
	)
}

// dueKey sorts YYYY-MM-DD dates as text, and no date after all of them ("~" follows the digits).
func dueKey(t Task) string {
	if t.Due == "" {
		return "~"
	}
	return t.Due
}

// stringArg returns the text argument name, trimmed, or "" when it is absent.
// mcp-go's GetString would take an argument of another type for an absent one,
// and the filter would silently not apply.
func stringArg(req mcp.CallToolRequest, name string) (string, error) {
	v := req.GetArguments()[name]
	if v == nil { // absent, or null: clients often send null for what they leave out
		return "", nil
	}
	s, isText := v.(string)
	if !isText {
		return "", errors.New(name + " must be text")
	}
	return strings.TrimSpace(s), nil
}

func (h *handlers) getTask(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := taskID(req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	task, err := h.store.GetTask(id)
	if err != nil {
		return storeError(err), nil
	}
	return taskResult("task", task), nil
}

// taskResult answers with the task under key.
func taskResult(key string, task Task) *mcp.CallToolResult {
	return jsonResult(map[string]Task{key: task})
}

// taskID returns the id argument, which every task tool requires.
func taskID(req mcp.CallToolRequest) (string, error) {
	id, err := stringArg(req, "id")
	if err == nil && id == "" {
		err = errors.New("id is required")
	}
	return id, err
}

// storeError is the answer to a failed Store call: the assistant gets the
// message that tells it what to do, or else the Store's own.
func storeError(err error) *mcp.CallToolResult {
	switch {
	case errors.Is(err, ErrNotFound):
		return mcp.NewToolResultError(unknownTask)
	case errors.Is(err, ErrInvalidProject):
		return mcp.NewToolResultError(unknownProject)
	}
	return mcp.NewToolResultError(err.Error())
}

// textArgs returns the text arguments names, in that order.
func textArgs(req mcp.CallToolRequest, names ...string) ([]string, error) {
	out := make([]string, len(names))
	for i, name := range names {
		var err error
		if out[i], err = stringArg(req, name); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// stringsArg returns the list-of-text argument name, or nil when it is absent.
func stringsArg(req mcp.CallToolRequest, name string) ([]string, error) {
	v := req.GetArguments()[name]
	if v == nil {
		return nil, nil
	}
	items, isList := v.([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		s, isText := item.(string)
		if !isText {
			return nil, errors.New(name + " must be a list of text")
		}
		out = append(out, s)
	}
	if !isList {
		return nil, errors.New(name + " must be a list of text")
	}
	return out, nil
}

func (h *handlers) createProject(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, err := textArgs(req, "name", "description")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	project, err := newProject(ProjectInput{Name: args[0], Description: args[1]}, h.now())
	if err == nil {
		project, err = h.store.CreateProject(project)
	}
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(struct {
		Project Project `json:"project"`
	}{project}), nil
}

func (h *handlers) addTask(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, err := textArgs(req, "title", "notes", "project", "due", "priority")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	in := TaskInput{Title: args[0], Notes: args[1], Project: args[2], Due: args[3], Priority: args[4]}
	if in.Tags, err = stringsArg(req, "tags"); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	task, err := newTask(in, h.now())
	if err == nil {
		task, err = h.store.CreateTask(task)
	}
	if err != nil {
		return storeError(err), nil
	}
	return taskResult("task", task), nil
}

// optText returns the text argument name, trimmed, or nil when it is absent or null.
func optText(req mcp.CallToolRequest, name string) (*string, error) {
	if req.GetArguments()[name] == nil {
		return nil, nil
	}
	s, err := stringArg(req, name)
	return &s, err
}

func (h *handlers) updateTask(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := taskID(req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	var u TaskUpdate
	for _, f := range []struct {
		name string
		dst  **string
	}{{"title", &u.Title}, {"notes", &u.Notes}, {"status", &u.Status}, {"priority", &u.Priority}, {"due", &u.Due}, {"project", &u.Project}} {
		if *f.dst, err = optText(req, f.name); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
	}
	if u.Tags, err = stringsArg(req, "tags"); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if u.isEmpty() {
		return mcp.NewToolResultError("nothing to update: give at least one of title, notes, status, priority, due, project, tags"), nil
	}
	task, err := h.store.UpdateTask(id, func(t *Task) error { return u.apply(t, h.now()) })
	if err != nil {
		return storeError(err), nil
	}
	return taskResult("task", task), nil
}

func (h *handlers) completeTask(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := taskID(req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	task, err := h.store.UpdateTask(id, func(t *Task) error {
		t.setStatus(StatusDone, h.now())
		return nil
	})
	if err != nil {
		return storeError(err), nil
	}
	return taskResult("task", task), nil
}

func (h *handlers) deleteTask(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := taskID(req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	task, err := h.store.DeleteTask(id)
	if err != nil {
		return storeError(err), nil
	}
	return taskResult("deleted", task), nil
}
