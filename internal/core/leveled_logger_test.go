package core

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := []struct {
		input string
		want  slog.Level
	}{
		{"", slog.LevelInfo},
		{"info", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{" Debug ", slog.LevelDebug},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
	}
	for _, tc := range cases {
		got, err := ParseLevel(tc.input)
		if err != nil {
			t.Fatalf("ParseLevel(%q): %v", tc.input, err)
		}
		if got != tc.want {
			t.Fatalf("ParseLevel(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
	if _, err := ParseLevel("trace"); err == nil {
		t.Fatal("ParseLevel accepted an unknown level")
	}
}

// TestLeveledLoggerLevelChangeFiltersNextRecord is the property the SIGHUP path
// relies on: the threshold a logger applies can change without giving it a new
// handler, so components that captured the Logger keep honouring the new value.
func TestLeveledLoggerLevelChangeFiltersNextRecord(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLeveledLogger(&buf, slog.LevelInfo)

	logger.Debug("hidden-debug")
	if strings.Contains(buf.String(), "hidden-debug") {
		t.Fatalf("debug record was written at info level: %q", buf.String())
	}

	logger.SetLevel(slog.LevelDebug)
	if got := logger.Level(); got != slog.LevelDebug {
		t.Fatalf("Level() = %v, want debug", got)
	}
	logger.Debug("visible-debug")
	if !strings.Contains(buf.String(), "visible-debug") {
		t.Fatalf("debug record missing after raising verbosity: %q", buf.String())
	}

	buf.Reset()
	logger.SetLevel(slog.LevelError)
	logger.Info("hidden-info")
	if buf.Len() != 0 {
		t.Fatalf("info record was written at error level: %q", buf.String())
	}
	logger.Error("visible-error", "key", "value")
	if !strings.Contains(buf.String(), "visible-error") {
		t.Fatalf("error record missing: %q", buf.String())
	}
}

// TestLeveledLoggerNilIsSafe keeps a nil *LeveledLogger usable as a Logger:
// tests and callers that do not configure logging must not have to guard every
// call site.
func TestLeveledLoggerNilIsSafe(t *testing.T) {
	var logger *LeveledLogger
	logger.Info("no-op")
	logger.SetLevel(slog.LevelDebug)
	if got := logger.Level(); got != slog.LevelInfo {
		t.Fatalf("Level() = %v, want the info default", got)
	}
}
