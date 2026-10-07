package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// lockStaleAfter is how long a Lock may stay untouched before another process
// may take it over. lock_timeout_seconds must stay below it, or a waiting
// process could take a Lock whose owner is still writing.
const lockStaleAfter = 30 * time.Second

// Config is the server configuration, read from a JSON file.
type Config struct {
	DataFile           string `json:"data_file"`
	Timezone           string `json:"timezone"`
	MaxTasks           int    `json:"max_tasks"`
	MaxResults         int    `json:"max_results"`
	LockTimeoutSeconds int    `json:"lock_timeout_seconds"`

	// Location is Timezone, loaded.
	Location *time.Location `json:"-"`
}

// loadConfig reads and validates the configuration file at path.
func loadConfig(path string) (Config, error) {
	cfg := Config{MaxTasks: 5000, MaxResults: 50, LockTimeoutSeconds: 5}

	data, err := os.ReadFile(path) //nolint:gosec // the configuration path is chosen by the user, by design
	if err != nil {
		return Config{}, fmt.Errorf("config: read: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("config: parse: %w", err)
	}
	if dec.More() {
		return Config{}, errors.New("config: parse: unexpected data after the JSON object")
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// validate checks the values and loads the timezone.
func (c *Config) validate() error {
	if c.DataFile == "" || !filepath.IsAbs(c.DataFile) {
		return errors.New("config: data_file is required and must be an absolute path")
	}
	if c.Timezone == "" || c.Timezone == "Local" {
		return errors.New("config: timezone is required and must be an IANA zone (not \"Local\")")
	}
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return fmt.Errorf("config: timezone: %w", err)
	}
	c.Location = loc

	for _, f := range []struct {
		name  string
		value int
	}{
		{"max_tasks", c.MaxTasks},
		{"max_results", c.MaxResults},
		{"lock_timeout_seconds", c.LockTimeoutSeconds},
	} {
		if f.value <= 0 {
			return fmt.Errorf("config: %s must be greater than 0", f.name)
		}
	}
	if time.Duration(c.LockTimeoutSeconds)*time.Second >= lockStaleAfter {
		return fmt.Errorf("config: lock_timeout_seconds must be less than %d", int(lockStaleAfter.Seconds()))
	}
	return nil
}
