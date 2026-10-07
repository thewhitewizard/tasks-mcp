package main

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// newServer builds the MCP server and its tools. No tool takes a file path: the
// Data file comes from the configuration only.
func newServer(cfg Config, store Store) *server.MCPServer {
	h := &handlers{store: store, maxResults: cfg.MaxResults}
	s := server.NewMCPServer("tasks-mcp", version, server.WithRecovery())
	s.AddTool(mcp.NewTool("list_projects", readOnly(),
		mcp.WithDescription("List all projects, sorted by name. Each has an id (p_ followed by 6 characters, e.g. p_k3x9aq) "+
			"to pass as `project` to list_tasks. Example answer: {\"projects\":[{\"id\":\"p_k3x9aq\",\"name\":\"Home\",\"created_at\":\"2026-07-14T09:30:00+02:00\"}]}."),
	), h.listProjects)
	s.AddTool(mcp.NewTool("list_tasks", readOnly(),
		mcp.WithDescription("List tasks, open ones first, then by due date (earliest first, no due date last), priority (high first) and creation. "+
			"Filters combine (AND). By default done tasks are left out. Tasks come without their notes. "+
			"At most "+strconv.Itoa(cfg.MaxResults)+" tasks are returned; `truncated` is true when there were more, so narrow the filters. "+
			"Example: list_tasks with due_before 2026-07-20 gives what is due by that day, that day included."),
		mcp.WithString("project", mcp.Description("Only this project: its id from list_projects (p_ followed by 6 characters).")),
		mcp.WithString("status", mcp.Enum(string(StatusTodo), string(StatusDoing), string(StatusDone)), mcp.Description("Only this status. Asking for done lists done tasks without include_done.")),
		mcp.WithString("due_before", mcp.Description("Only tasks due on or before this day, YYYY-MM-DD. Tasks without a due date are left out.")),
		mcp.WithString("tag", mcp.Description("Only tasks with this tag (not case sensitive).")),
		mcp.WithBoolean("include_done", mcp.Description("Also list done tasks. Default: false.")),
	), h.listTasks)
	return s
}

// readOnly marks a tool as reading only, closed-world, and not destructive:
// mcp-go would otherwise default destructiveHint and openWorldHint to true.
func readOnly() mcp.ToolOption {
	return func(t *mcp.Tool) {
		mcp.WithReadOnlyHintAnnotation(true)(t)
		mcp.WithDestructiveHintAnnotation(false)(t)
		mcp.WithOpenWorldHintAnnotation(false)(t)
	}
}

// handlers are the tool handlers, with what they need.
type handlers struct {
	store      Store
	maxResults int
}

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
	status, err := parseEnum("status", req.GetString("status", ""), Status(""), StatusTodo, StatusDoing, StatusDone)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	dueBefore := req.GetString("due_before", "")
	if _, err := time.Parse(time.DateOnly, dueBefore); dueBefore != "" && err != nil {
		return mcp.NewToolResultError("due_before must be a real date written YYYY-MM-DD"), nil
	}
	project := normalizeID(req.GetString("project", ""))
	tag := strings.ToLower(strings.TrimSpace(req.GetString("tag", "")))
	includeDone := req.GetBool("include_done", false)

	if project != "" {
		projects, err := h.store.ListProjects()
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if !slices.ContainsFunc(projects, func(p Project) bool { return p.ID == project }) {
			return mcp.NewToolResultError("project: no project with this id; list_projects gives the ids"), nil
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
			tag != "" && !slices.Contains(task.Tags, tag):
			continue
		}
		task.Notes = "" // the list leaves the notes out
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
