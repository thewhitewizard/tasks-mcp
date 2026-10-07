package main

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

func parisNow(t *testing.T) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Fatal(err)
	}
	return time.Date(2026, 7, 14, 9, 30, 0, 0, loc)
}

func TestNewTask_Defaults(t *testing.T) {
	t.Parallel()

	now := parisNow(t)
	got, err := newTask(TaskInput{Title: "Buy milk"}, now)
	if err != nil {
		t.Fatalf("newTask: %v", err)
	}
	want := Task{
		Title: "Buy milk", Status: StatusTodo, Priority: PriorityNormal,
		Tags: []string{}, CreatedAt: now, UpdatedAt: now,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("task = %+v, want %+v", got, want)
	}
}

func TestTask_JSON(t *testing.T) {
	t.Parallel()

	now := parisNow(t)
	done := now.Add(time.Hour)
	tests := []struct {
		name string
		task Task
		want string
	}{
		{
			name: "minimal",
			task: Task{ID: "t_abc234", Title: "Buy milk", Status: StatusTodo, Priority: PriorityNormal, Tags: []string{}, CreatedAt: now, UpdatedAt: now},
			want: `{"id":"t_abc234","title":"Buy milk","status":"todo","priority":"normal","tags":[],` +
				`"created_at":"2026-07-14T09:30:00+02:00","updated_at":"2026-07-14T09:30:00+02:00"}`,
		},
		{
			name: "complete",
			task: Task{
				ID: "t_abc234", Title: "Buy milk", Notes: "2 litres", Status: StatusDone, Priority: PriorityHigh,
				Due: "2026-07-20", Project: "p_xyz567", Tags: []string{"home"}, CreatedAt: now, UpdatedAt: done, CompletedAt: &done,
			},
			want: `{"id":"t_abc234","title":"Buy milk","notes":"2 litres","status":"done","priority":"high","due":"2026-07-20",` +
				`"project":"p_xyz567","tags":["home"],"created_at":"2026-07-14T09:30:00+02:00",` +
				`"updated_at":"2026-07-14T10:30:00+02:00","completed_at":"2026-07-14T10:30:00+02:00"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(tt.task)
			if err != nil || string(got) != tt.want {
				t.Errorf("json = %s (%v), want %s", got, err, tt.want)
			}
		})
	}
}

func TestProject_JSON(t *testing.T) {
	t.Parallel()

	now := parisNow(t)
	tests := []struct {
		name    string
		project Project
		want    string
	}{
		{"minimal", Project{ID: "p_xyz567", Name: "Home", CreatedAt: now}, `{"id":"p_xyz567","name":"Home","created_at":"2026-07-14T09:30:00+02:00"}`},
		{
			"with description", Project{ID: "p_xyz567", Name: "Home", Description: "Chores", CreatedAt: now},
			`{"id":"p_xyz567","name":"Home","description":"Chores","created_at":"2026-07-14T09:30:00+02:00"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(tt.project)
			if err != nil || string(got) != tt.want {
				t.Errorf("json = %s (%v), want %s", got, err, tt.want)
			}
		})
	}
}

const (
	rlo  = string(rune(0x202e)) // right-to-left override (Cf)
	zwsp = string(rune(0x200b)) // zero-width space (Cf)
)

func TestNewTask_Valid(t *testing.T) {
	t.Parallel()

	now := parisNow(t)
	tests := []struct {
		name string
		in   TaskInput
		edit func(*Task) // applied to the defaults to build the want
	}{
		{"title is trimmed and cleaned", TaskInput{Title: "  Buy" + rlo + " milk" + zwsp + "\x00 "}, func(k *Task) { k.Title = "Buy milk" }},
		{"newlines in a title become spaces", TaskInput{Title: "Buy\nmilk\r\ntoday"}, func(k *Task) { k.Title = "Buy milk today" }},
		{"title of 200 characters", TaskInput{Title: strings.Repeat("é", 200)}, func(k *Task) { k.Title = strings.Repeat("é", 200) }},
		{"notes keep their lines", TaskInput{Title: "t", Notes: "a\r\nb" + rlo + "\x00\tc"}, func(k *Task) { k.Notes = "a\nbc" }},
		{"notes of 2000 characters", TaskInput{Title: "t", Notes: strings.Repeat("n", 2000)}, func(k *Task) { k.Notes = strings.Repeat("n", 2000) }},
		{"explicit status and priority", TaskInput{Title: "t", Status: "doing", Priority: "high"}, func(k *Task) { k.Status, k.Priority = StatusDoing, PriorityHigh }},
		{"created already done", TaskInput{Title: "t", Status: "done"}, func(k *Task) { k.Status, k.CompletedAt = StatusDone, &now }},
		{"due date", TaskInput{Title: "t", Due: "2026-02-28"}, func(k *Task) { k.Due = "2026-02-28" }},
		{"project id is normalised", TaskInput{Title: "t", Project: " P_ABC234 "}, func(k *Task) { k.Project = "p_abc234" }},
		{
			"tags are lowercased, deduplicated, empty ones dropped", TaskInput{Title: "t", Tags: []string{" Home", "home", "", "URGENT" + zwsp}},
			func(k *Task) { k.Tags = []string{"home", "urgent"} },
		},
		{"ten tags of thirty characters", TaskInput{Title: "t", Tags: tagsOf(10, 30)}, func(k *Task) { k.Tags = tagsOf(10, 30) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			want := Task{Status: StatusTodo, Priority: PriorityNormal, Tags: []string{}, CreatedAt: now, UpdatedAt: now}
			want.Title = "t"
			tt.edit(&want)
			got, err := newTask(tt.in, now)
			if err != nil {
				t.Fatalf("newTask: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("task = %+v, want %+v", got, want)
			}
		})
	}
}

func tagsOf(n, size int) []string {
	tags := make([]string, n)
	for i := range tags {
		tags[i] = string(rune('a'+i)) + strings.Repeat("x", size-1)
	}
	return tags
}

func TestNewTask_Invalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		in    TaskInput
		field string
		leak  string // text from the input the error must not repeat
	}{
		{"empty title", TaskInput{}, "title", ""},
		{"title only made of invisible characters", TaskInput{Title: zwsp + " \n"}, "title", ""},
		{"title too long", TaskInput{Title: strings.Repeat("é", 201)}, "title", "éé"},
		{"notes too long", TaskInput{Title: "t", Notes: strings.Repeat("n", 2001)}, "notes", "nnn"},
		{"unknown status", TaskInput{Title: "t", Status: "secret-status"}, "status", "secret-status"},
		{"unknown priority", TaskInput{Title: "t", Priority: "secret-priority"}, "priority", "secret-priority"},
		{"due in another format", TaskInput{Title: "t", Due: "14/07/2026"}, "due", "14/07/2026"},
		{"due that does not exist", TaskInput{Title: "t", Due: "2026-02-30"}, "due", "2026-02-30"},
		{"too many tags", TaskInput{Title: "t", Tags: tagsOf(11, 3)}, "tags", ""},
		{"tag too long", TaskInput{Title: "t", Tags: []string{strings.Repeat("g", 31)}}, "tag", "ggg"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := newTask(tt.in, parisNow(t))
			if err == nil || !strings.Contains(err.Error(), tt.field) {
				t.Fatalf("newTask error = %v, want one naming %q", err, tt.field)
			}
			if tt.leak != "" && strings.Contains(err.Error(), tt.leak) {
				t.Errorf("error %q repeats the input", err)
			}
		})
	}
}

func TestNewProject(t *testing.T) {
	t.Parallel()

	now := parisNow(t)
	tests := []struct {
		name    string
		in      ProjectInput
		want    Project
		wantErr string
	}{
		{"name only", ProjectInput{Name: " Home" + zwsp + " "}, Project{Name: "Home", CreatedAt: now}, ""},
		{"with description", ProjectInput{Name: "Home", Description: "Chores\nand repairs"}, Project{Name: "Home", Description: "Chores and repairs", CreatedAt: now}, ""},
		{"longest name and description", ProjectInput{Name: strings.Repeat("n", 100), Description: strings.Repeat("d", 500)},
			Project{Name: strings.Repeat("n", 100), Description: strings.Repeat("d", 500), CreatedAt: now}, ""},
		{"empty name", ProjectInput{Name: zwsp}, Project{}, "name"},
		{"name too long", ProjectInput{Name: strings.Repeat("n", 101)}, Project{}, "name"},
		{"description too long", ProjectInput{Name: "Home", Description: strings.Repeat("d", 501)}, Project{}, "description"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := newProject(tt.in, now)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("newProject error = %v, want one naming %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("project = %+v (%v), want %+v", got, err, tt.want)
			}
		})
	}
}

func TestNewID(t *testing.T) {
	t.Parallel()

	pattern := regexp.MustCompile(`^[tp]_[a-z2-7]{6}$`)
	seen := map[string]bool{}
	for range 200 {
		for _, prefix := range []string{"t_", "p_"} {
			id := newID(prefix)
			if !pattern.MatchString(id) || !strings.HasPrefix(id, prefix) {
				t.Fatalf("newID(%q) = %q, want %s followed by 6 characters of a-z2-7", prefix, id, prefix)
			}
			seen[id] = true
		}
	}
	if len(seen) < 300 { // 400 draws: a few repeats at most
		t.Errorf("only %d distinct IDs out of 400 draws", len(seen))
	}
}

func TestTask_SetStatus(t *testing.T) {
	t.Parallel()

	created := parisNow(t)
	later := created.Add(time.Hour)
	latest := later.Add(time.Hour)

	task, err := newTask(TaskInput{Title: "t"}, created)
	if err != nil {
		t.Fatal(err)
	}

	task.setStatus(StatusDone, later)
	if task.Status != StatusDone || task.CompletedAt == nil || !task.CompletedAt.Equal(later) || !task.UpdatedAt.Equal(later) {
		t.Fatalf("after done: %+v, want completed and updated at %v", task, later)
	}

	task.setStatus(StatusDone, latest)
	if !task.CompletedAt.Equal(later) || !task.UpdatedAt.Equal(later) {
		t.Errorf("done again: completed %v, updated %v, want both unchanged at %v", task.CompletedAt, task.UpdatedAt, later)
	}

	task.setStatus(StatusDoing, latest)
	if task.Status != StatusDoing || task.CompletedAt != nil || !task.UpdatedAt.Equal(latest) {
		t.Errorf("reopened: %+v, want doing, no completion, updated at %v", task, latest)
	}
}
