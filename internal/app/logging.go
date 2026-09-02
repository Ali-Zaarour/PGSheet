package app

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Logs go to a file beside the user's application data, structured, one file
// per day, with old files pruned (spec §14).
//
// What is never written here: cell values and credentials. The log exists to
// answer "what did this run do and where did it fail", not to reproduce the
// data — a log file is exactly the artifact that gets attached to a ticket and
// mailed around.

const (
	logRetentionDays = 14
	logDirName       = "pgsheet"
)

// LogDir is where log files are written: %APPDATA%\pgsheet\logs on Windows,
// the user config directory elsewhere.
func LogDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, logDirName, "logs"), nil
}

// setupLogging opens today's log file and returns a logger writing to both the
// file and stderr.
//
// A logging failure is never fatal: the application is more useful without a
// log than not running at all, so the error is reported once to stderr and the
// logger falls back to stderr alone.
func setupLogging(version string) (*slog.Logger, func()) {
	stderr := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})

	dir, err := LogDir()
	if err != nil {
		return slog.New(stderr), func() {}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return slog.New(stderr), func() {}
	}

	pruneLogs(dir, logRetentionDays)

	path := filepath.Join(dir, "pgsheet-"+time.Now().Format("2006-01-02")+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return slog.New(stderr), func() {}
	}

	handler := slog.NewJSONHandler(io.MultiWriter(f, os.Stderr), &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	logger := slog.New(handler).With("version", version)

	return logger, func() { _ = f.Close() }
}

// pruneLogs keeps the most recent files and deletes the rest, so the directory
// does not grow without bound on a machine that is never cleaned up.
func pruneLogs(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	var logs []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() && strings.HasPrefix(name, "pgsheet-") && strings.HasSuffix(name, ".log") {
			logs = append(logs, name)
		}
	}
	// The names sort chronologically because the date is written as ISO.
	sort.Strings(logs)

	for i := 0; i < len(logs)-keep; i++ {
		_ = os.Remove(filepath.Join(dir, logs[i]))
	}
}
