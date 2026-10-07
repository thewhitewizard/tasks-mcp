package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfig writes content to a temporary file and returns its path.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfig_Valid(t *testing.T) {
	t.Parallel()

	dataFile := filepath.ToSlash(filepath.Join(t.TempDir(), "tasks.json"))
	tests := []struct {
		name    string
		content string
		want    Config
	}{
		{
			name: "every field set",
			content: `{"data_file": "` + dataFile + `", "timezone": "Europe/Paris",
				"max_tasks": 10, "max_results": 5, "lock_timeout_seconds": 2}`,
			want: Config{DataFile: dataFile, Timezone: "Europe/Paris", MaxTasks: 10, MaxResults: 5, LockTimeoutSeconds: 2},
		},
		{
			name:    "omitted fields take their defaults",
			content: `{"data_file": "` + dataFile + `", "timezone": "Europe/Paris"}`,
			want:    Config{DataFile: dataFile, Timezone: "Europe/Paris", MaxTasks: 5000, MaxResults: 50, LockTimeoutSeconds: 5},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := loadConfig(writeConfig(t, tt.content))
			if err != nil {
				t.Fatalf("loadConfig: %v", err)
			}
			if got.Location == nil || got.Location.String() != tt.want.Timezone {
				t.Errorf("Location = %v, want %s", got.Location, tt.want.Timezone)
			}
			got.Location = nil
			if got != tt.want {
				t.Errorf("config = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestLoadConfig_Invalid(t *testing.T) {
	t.Parallel()

	abs := filepath.ToSlash(filepath.Join(t.TempDir(), "tasks.json"))
	with := func(extra string) string {
		return `{"data_file": "` + abs + `", "timezone": "Europe/Paris"` + extra + `}`
	}
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"not JSON", `nope`, "config: parse"},
		{"data after the object", with("") + `{}`, "unexpected data"},
		{"closing brace after the object", with("") + `}`, "unexpected data"},
		{"unknown field", with(`, "max_task": 3`), "max_task"},
		{"missing data_file", `{"timezone": "Europe/Paris"}`, "data_file"},
		{"relative data_file", `{"data_file": "tasks.json", "timezone": "Europe/Paris"}`, "absolute"},
		{"missing timezone", `{"data_file": "` + abs + `"}`, "timezone"},
		{"Local timezone", `{"data_file": "` + abs + `", "timezone": "Local"}`, "timezone"},
		{"unknown timezone", `{"data_file": "` + abs + `", "timezone": "Mars/Base"}`, "timezone"},
		{"zero max_tasks", with(`, "max_tasks": 0`), "max_tasks"},
		{"negative max_results", with(`, "max_results": -1`), "max_results"},
		{"zero lock timeout", with(`, "lock_timeout_seconds": 0`), "lock_timeout_seconds"},
		{"lock timeout reaches the stale delay", with(`, "lock_timeout_seconds": 30`), "lock_timeout_seconds"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := loadConfig(writeConfig(t, tt.content))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("loadConfig error = %v, want one containing %q", err, tt.want)
			}
		})
	}
}

func TestLoadConfig_AbsentFile(t *testing.T) {
	t.Parallel()

	_, err := loadConfig(filepath.Join(t.TempDir(), "absent.json"))
	if err == nil || !strings.Contains(err.Error(), "config: read") {
		t.Errorf("loadConfig error = %v, want a read error", err)
	}
}
