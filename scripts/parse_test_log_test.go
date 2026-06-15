package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFileWritesReadableSummary(t *testing.T) {
	path := writeJSONLog(t, "2026-05-29T12-30-30.148_unit.json", []string{
		`{"Time":"2026-05-29T12:30:30.148Z","Action":"run","Package":"github.com/X1aSheng/shark-socket/internal/runtime","Test":"TestGateway"}`,
		`{"Time":"2026-05-29T12:30:30.149Z","Action":"pass","Package":"github.com/X1aSheng/shark-socket/internal/runtime","Test":"TestGateway","Elapsed":0.01}`,
		`{"Time":"2026-05-29T12:30:30.150Z","Action":"pass","Package":"github.com/X1aSheng/shark-socket/internal/runtime","Elapsed":0.01}`,
	})

	out := captureStdout(t, func() {
		if err := parseFile(path); err != nil {
			t.Fatalf("parseFile: %v", err)
		}
	})

	if !strings.Contains(out, "unit test report") {
		t.Fatalf("report title missing:\n%s", out)
	}
	if !strings.Contains(out, "2026-05-29T12:30:30") {
		t.Fatalf("timestamp missing:\n%s", out)
	}
	if !strings.Contains(out, "summary: 1 passed, 0 failed, 0 skipped") {
		t.Fatalf("summary missing:\n%s", out)
	}
}

func TestParseFileIncludesBenchmarkRows(t *testing.T) {
	path := writeJSONLog(t, "2026-05-29T12-30-30.148_benchmark.json", []string{
		`{"Time":"2026-05-29T12:30:30.148Z","Action":"output","Package":"github.com/X1aSheng/shark-socket/internal/transport/tcp","Output":"BenchmarkLengthPrefixFramerRoundTrip-16    1000    249.4 ns/op    664 B/op    6 allocs/op\n"}`,
		`{"Time":"2026-05-29T12:30:30.149Z","Action":"output","Package":"github.com/X1aSheng/shark-socket/internal/transport/tcp","Output":"BenchmarkLineFramerRoundTrip-16    \t"}`,
		`{"Time":"2026-05-29T12:30:30.150Z","Action":"output","Package":"github.com/X1aSheng/shark-socket/internal/transport/tcp","Output":"1000    2496 ns/op    1840 B/op    12 allocs/op\n"}`,
		`{"Time":"2026-05-29T12:30:31.148Z","Action":"pass","Package":"github.com/X1aSheng/shark-socket/internal/transport/tcp","Elapsed":1}`,
	})

	out := captureStdout(t, func() {
		if err := parseFile(path); err != nil {
			t.Fatalf("parseFile: %v", err)
		}
	})

	if !strings.Contains(out, "benchmark report") ||
		!strings.Contains(out, "BenchmarkLengthPrefixFramerRoundTrip") ||
		!strings.Contains(out, "BenchmarkLineFramerRoundTrip") {
		t.Fatalf("benchmark row missing:\n%s", out)
	}
}

func writeJSONLog(t *testing.T, name string, lines []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write json log: %v", err)
	}
	return path
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	fn()
	_ = w.Close()
	os.Stdout = old
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	_ = r.Close()
	return string(out)
}
