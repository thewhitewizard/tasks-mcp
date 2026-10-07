// Command tasks-mcp is an MCP server (stdio) that gives an AI assistant a simple
// task and project manager, persisted in a JSON file.
//
// stdout carries the MCP protocol and nothing else: every diagnostic goes to
// stderr.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/mark3labs/mcp-go/server"
)

// version is set at build time: go build -ldflags "-X main.version=v1.0.0".
var version = "dev"

const configEnv = "TASKS_MCP_CONFIG"

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stderr, func(s *server.MCPServer) error {
		return server.ServeStdio(s)
	}))
}

// run starts the server and returns the process exit code: 0 on success, 1 on
// a runtime or configuration error, 2 on a usage error. serve blocks until the
// server stops.
func run(args []string, getenv func(string) string, stderr io.Writer, serve func(*server.MCPServer) error) int {
	logger := log.New(stderr, "tasks-mcp: ", 0)

	flags := flag.NewFlagSet("tasks-mcp", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "path to the JSON configuration file (or set "+configEnv+")")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *configPath == "" {
		*configPath = getenv(configEnv)
	}
	if *configPath == "" {
		logger.Printf("no configuration file: pass --config <path> or set %s", configEnv)
		return 2
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		logger.Print(err)
		return 1
	}
	if err := prepareDataDir(filepath.Dir(cfg.DataFile)); err != nil {
		logger.Print(err)
		return 1
	}
	logger.Printf("starting %s, timezone %s", version, cfg.Timezone)

	if err := serve(newServer()); err != nil {
		logger.Printf("server stopped: %v", err)
		return 1
	}
	return 0
}

// newServer returns the MCP server. It has no tool yet.
func newServer() *server.MCPServer {
	return server.NewMCPServer("tasks-mcp", version,
		server.WithToolCapabilities(false),
		server.WithRecovery(),
	)
}

// prepareDataDir creates the directory of the Data file if needed (0700) and
// checks that it is writable by creating and removing a temporary file: a
// mount problem shows at startup, not at the first write of a conversation.
func prepareDataDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("data directory %s: %w", dir, err)
	}
	probe, err := os.CreateTemp(dir, ".write-check-*")
	if err != nil {
		return fmt.Errorf("data directory %s is not writable: %w", dir, err)
	}
	name := probe.Name()
	closeErr := probe.Close()
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("data directory %s: remove write check: %w", dir, err)
	}
	if closeErr != nil {
		return fmt.Errorf("data directory %s: close write check: %w", dir, closeErr)
	}
	return nil
}
