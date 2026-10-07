package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// lockPollInterval is how often a waiting process looks at the Lock again.
const lockPollInterval = 10 * time.Millisecond

// fileLock is the Lock: a file created with O_EXCL next to the Data file, so
// that only one process at a time can write it. It uses nothing but portable
// file operations (see docs/adr/0001-portable-lock-file.md).
type fileLock struct {
	path    string
	timeout time.Duration
}

// acquire waits up to timeout for the Lock and returns the function that
// releases it. The file holds the PID and the time, for whoever investigates a
// stuck Lock; nothing reads them back.
func (l *fileLock) acquire() (release func(), err error) {
	deadline := time.Now().Add(l.timeout)
	for {
		f, err := os.OpenFile(l.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, werr := fmt.Fprintf(f, "%d %s\n", os.Getpid(), time.Now().Format(time.RFC3339))
			if cerr := errors.Join(werr, f.Close()); cerr != nil {
				_ = os.Remove(l.path)
				return nil, fmt.Errorf("lock: %w", cerr)
			}
			return func() { _ = os.Remove(l.path) }, nil
		}
		// Windows answers "access denied", not "exists", while another process
		// is deleting the Lock: wait, and report it if it lasts.
		permission := errors.Is(err, fs.ErrPermission)
		if !permission && !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("lock: %w", err)
		}
		l.breakIfStale()
		if time.Now().After(deadline) {
			if permission {
				return nil, fmt.Errorf("lock: %w", err)
			}
			return nil, errors.New("lock: timed out waiting for another process to finish writing the data file")
		}
		time.Sleep(lockPollInterval)
	}
}

// breakIfStale removes the Lock when nobody has touched it for lockStaleAfter:
// its owner died. Processes breaking it take turns through a guard file, and
// look at the age again once they hold it, so that none removes a Lock that
// another process has just created.
func (l *fileLock) breakIfStale() {
	if !l.isStale(l.path) {
		return
	}
	guard := l.path + ".break"
	g, err := os.OpenFile(guard, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // the path comes from the configuration, never from a tool
	if err != nil {
		if l.isStale(guard) { // its breaker died too
			_ = os.Remove(guard)
		}
		return
	}
	_ = g.Close()
	defer func() { _ = os.Remove(guard) }()
	if l.isStale(l.path) {
		_ = os.Remove(l.path)
	}
}

func (l *fileLock) isStale(path string) bool {
	info, err := os.Stat(path)
	return err == nil && time.Since(info.ModTime()) > lockStaleAfter
}
