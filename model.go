package main

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Status is where a Task stands.
type Status string

// Priority is how urgent a Task is.
type Priority string

const (
	StatusTodo  Status = "todo"
	StatusDoing Status = "doing"
	StatusDone  Status = "done"

	PriorityLow    Priority = "low"
	PriorityNormal Priority = "normal"
	PriorityHigh   Priority = "high"
)

// Task is a single thing to do. Due is a day, YYYY-MM-DD.
type Task struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Notes       string     `json:"notes,omitempty"`
	Status      Status     `json:"status"`
	Priority    Priority   `json:"priority"`
	Due         string     `json:"due,omitempty"`
	Project     string     `json:"project,omitempty"`
	Tags        []string   `json:"tags"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// Project is a named group of Tasks.
type Project struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// TaskInput is what a caller supplies to create a Task: raw, not yet validated.
type TaskInput struct {
	Title    string
	Notes    string
	Status   string
	Priority string
	Due      string
	Project  string
	Tags     []string
}

// Field limits, in characters.
const (
	maxTitleLen       = 200
	maxNotesLen       = 2000
	maxTags           = 10
	maxTagLen         = 30
	maxProjectName    = 100
	maxProjectDescLen = 500
)

// newTask validates in and returns the Task it describes, without an ID: the
// Store assigns it. now must already be in the configured timezone. Errors name
// the field and never repeat the input.
func newTask(in TaskInput, now time.Time) (Task, error) {
	title, err := cleanText("title", in.Title, maxTitleLen, false, true)
	if err != nil {
		return Task{}, err
	}
	notes, err := cleanText("notes", in.Notes, maxNotesLen, true, false)
	if err != nil {
		return Task{}, err
	}
	status, err := parseEnum("status", in.Status, StatusTodo, StatusTodo, StatusDoing, StatusDone)
	if err != nil {
		return Task{}, err
	}
	priority, err := parseEnum("priority", in.Priority, PriorityNormal, PriorityLow, PriorityNormal, PriorityHigh)
	if err != nil {
		return Task{}, err
	}
	if in.Due != "" {
		if _, err := time.Parse(time.DateOnly, in.Due); err != nil {
			return Task{}, errors.New("due must be a real date written YYYY-MM-DD")
		}
	}
	tags, err := cleanTags(in.Tags)
	if err != nil {
		return Task{}, err
	}

	task := Task{
		Title: title, Notes: notes, Status: StatusTodo, Priority: priority, Due: in.Due,
		Project: normalizeID(in.Project), Tags: tags, CreatedAt: now, UpdatedAt: now,
	}
	task.setStatus(status, now)
	return task, nil
}

// setStatus moves the Task to status. Completion sets completed_at; any other
// Status clears it. A Task that already has this Status is left untouched.
func (t *Task) setStatus(status Status, now time.Time) {
	if t.Status == status {
		return
	}
	t.Status = status
	t.UpdatedAt = now
	t.CompletedAt = nil
	if status == StatusDone {
		t.CompletedAt = &now
	}
}

// cleanText drops control and invisible characters (categories Cc and Cf),
// trims it, and checks its length. Newlines are kept when multiline, and turned
// into spaces otherwise.
func cleanText(field, s string, limit int, multiline, required bool) (string, error) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' && multiline:
			return r
		case r == '\n':
			return ' '
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if s == "" && required {
		return "", fmt.Errorf("%s is required", field)
	}
	if utf8.RuneCountInString(s) > limit {
		return "", fmt.Errorf("%s must be at most %d characters", field, limit)
	}
	return s, nil
}

// parseEnum returns s as one of the allowed values, or def when s is empty.
func parseEnum[T ~string](field, s string, def T, allowed ...T) (T, error) {
	if s == "" {
		return def, nil
	}
	if !slices.Contains(allowed, T(s)) {
		return "", fmt.Errorf("%s must be one of %v", field, allowed)
	}
	return T(s), nil
}

// cleanTags lowercases, cleans and deduplicates tags, dropping empty ones.
func cleanTags(in []string) ([]string, error) {
	tags := []string{}
	for _, raw := range in {
		tag, err := cleanText("tag", strings.ToLower(raw), maxTagLen, false, false)
		if err != nil {
			return nil, err
		}
		if tag != "" && !slices.Contains(tags, tag) {
			tags = append(tags, tag)
		}
	}
	if len(tags) > maxTags {
		return nil, fmt.Errorf("tags must be at most %d", maxTags)
	}
	return tags, nil
}

// normalizeID makes an ID typed or copied by the assistant comparable: it is
// trimmed and lowercased.
func normalizeID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

// ProjectInput is what a caller supplies to create a Project: raw, not yet
// validated.
type ProjectInput struct {
	Name        string
	Description string
}

// newProject validates in and returns the Project it describes, without an ID:
// the Store assigns it.
func newProject(in ProjectInput, now time.Time) (Project, error) {
	name, err := cleanText("name", in.Name, maxProjectName, false, true)
	if err != nil {
		return Project{}, err
	}
	description, err := cleanText("description", in.Description, maxProjectDescLen, false, false)
	if err != nil {
		return Project{}, err
	}
	return Project{Name: name, Description: description, CreatedAt: now}, nil
}

// idEncoding writes random bytes with the characters a-z and 2-7: short, easy
// to copy, and unambiguous in lower case.
var idEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// newID returns prefix ("t_" or "p_") followed by 6 random characters (30
// bits). It does not check for collisions: the Store does, under its Lock.
func newID(prefix string) string {
	b := make([]byte, 5)
	_, _ = rand.Read(b) // never fails since Go 1.24: it crashes the program instead
	return prefix + idEncoding.EncodeToString(b)[:6]
}
