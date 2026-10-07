package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/server"
)

// runResult is what run did: its exit code, what it wrote on stderr, and the
// server it handed to serve (nil if serve was not called).
type runResult struct {
	code   int
	stderr string
	served *server.MCPServer
}

func runWith(t *testing.T, args []string, env map[string]string, serveErr error) runResult {
	t.Helper()

	var res runResult
	var stderr bytes.Buffer
	res.code = run(args, func(k string) string { return env[k] }, &stderr,
		func(s *server.MCPServer) error {
			res.served = s
			return serveErr
		})
	res.stderr = stderr.String()
	return res
}

// configFor writes a configuration whose Data file is dataFile.
func configFor(t *testing.T, dataFile string) string {
	t.Helper()
	return writeConfig(t, `{"data_file": "`+filepath.ToSlash(dataFile)+`", "timezone": "Europe/Paris"}`)
}

func TestRun(t *testing.T) {
	t.Parallel()

	valid := configFor(t, filepath.Join(t.TempDir(), "tasks.json"))
	invalid := writeConfig(t, `{"timezone": "Europe/Paris"}`)
	absent := filepath.Join(t.TempDir(), "absent.json")

	tests := []struct {
		name       string
		args       []string
		env        map[string]string
		serveErr   error
		wantCode   int
		wantServed bool
		wantStderr string
	}{
		{name: "config from the flag", args: []string{"--config", valid}, wantServed: true},
		{name: "config from the environment", env: map[string]string{"TASKS_MCP_CONFIG": valid}, wantServed: true},
		{
			name:       "the flag wins over the environment",
			args:       []string{"--config", valid},
			env:        map[string]string{"TASKS_MCP_CONFIG": absent},
			wantServed: true,
		},
		{name: "no config path", wantCode: 2, wantStderr: "TASKS_MCP_CONFIG"},
		{name: "unknown flag", args: []string{"--nope"}, wantCode: 2, wantStderr: "nope"},
		{name: "help", args: []string{"-h"}, wantStderr: "-config"},
		{name: "absent config file", args: []string{"--config", absent}, wantCode: 1, wantStderr: "read config"},
		{name: "invalid config", args: []string{"--config", invalid}, wantCode: 1, wantStderr: "data_file"},
		{
			name:       "server failure",
			args:       []string{"--config", valid},
			serveErr:   errors.New("boom"),
			wantCode:   1,
			wantServed: true,
			wantStderr: "boom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			res := runWith(t, tt.args, tt.env, tt.serveErr)
			if res.code != tt.wantCode {
				t.Errorf("exit code = %d, want %d (stderr: %q)", res.code, tt.wantCode, res.stderr)
			}
			if (res.served != nil) != tt.wantServed {
				t.Errorf("serve called = %v, want %v", res.served != nil, tt.wantServed)
			}
			if !strings.Contains(res.stderr, tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", res.stderr, tt.wantStderr)
			}
		})
	}
}

func TestRun_PreparesTheDataDirectory(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "nested", "data")
	dataFile := filepath.Join(dir, "tasks.json")

	res := runWith(t, []string{"--config", configFor(t, dataFile)}, nil, nil)
	if res.code != 0 {
		t.Fatalf("exit code = %d, stderr: %q", res.code, res.stderr)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("the data directory was not created: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("data directory holds %d entries, want none (no Data file, no probe left)", len(entries))
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Errorf("data directory mode = %o, want 700", perm)
		}
	}
}

func TestRun_DataDirectoryNotWritable(t *testing.T) {
	t.Parallel()

	// A regular file where the directory should be: portable on Windows and Linux.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	res := runWith(t, []string{"--config", configFor(t, filepath.Join(blocker, "tasks.json"))}, nil, nil)
	if res.code != 1 {
		t.Errorf("exit code = %d, want 1", res.code)
	}
	if res.served != nil {
		t.Error("serve was called although the data directory is unusable")
	}
	if !strings.Contains(res.stderr, "blocker") {
		t.Errorf("stderr = %q, want it to name the directory", res.stderr)
	}
}

func TestRun_ServesAnEmptyToolList(t *testing.T) {
	t.Parallel()

	res := runWith(t, []string{"--config", configFor(t, filepath.Join(t.TempDir(), "tasks.json"))}, nil, nil)
	if res.served == nil {
		t.Fatalf("no server was served (stderr: %q)", res.stderr)
	}
	request := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	raw, err := json.Marshal(res.served.HandleMessage(context.Background(), []byte(request)))
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Result struct {
			Tools []json.RawMessage `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &response); err != nil || response.Result.Tools == nil || len(response.Result.Tools) != 0 {
		t.Errorf("tools/list = %s, want an empty tools array", raw)
	}
}
