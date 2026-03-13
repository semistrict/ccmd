package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExtractFiles(t *testing.T) {
	f, err := os.Open("testdata/session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	files := extractFiles(parseRecords(f))

	if len(files) != 11 {
		t.Fatalf("expected 11 files, got %d", len(files))
	}

	// First file: types.go, read only
	if files[0].path != "/Users/ramon/src/ccmd/types.go" {
		t.Errorf("files[0].path = %q", files[0].path)
	}
	if !files[0].read || files[0].written {
		t.Errorf("files[0]: read=%v written=%v, want read=true written=false", files[0].read, files[0].written)
	}
	if files[0].readLen != 82 {
		t.Errorf("files[0].readLen = %d, want 82", files[0].readLen)
	}

	// claude.go: read + edited
	var claude *fileInfo
	for i := range files {
		if files[i].path == "/Users/ramon/src/ccmd/claude.go" {
			claude = &files[i]
			break
		}
	}
	if claude == nil {
		t.Fatal("claude.go not found in files")
	}
	if !claude.read || !claude.written {
		t.Errorf("claude.go: read=%v written=%v, want both true", claude.read, claude.written)
	}
	if claude.added != 4 || claude.removed != 5 {
		t.Errorf("claude.go: added=%d removed=%d, want 4/5", claude.added, claude.removed)
	}

	// files.go: read + written (Write tool)
	var filesGo *fileInfo
	for i := range files {
		if files[i].path == "/Users/ramon/src/ccmd/files.go" {
			filesGo = &files[i]
			break
		}
	}
	if filesGo == nil {
		t.Fatal("files.go not found in files")
	}
	if !filesGo.read || !filesGo.written {
		t.Errorf("files.go: read=%v written=%v, want both true", filesGo.read, filesGo.written)
	}

	// Last file should be parse_test.go (read only)
	last := files[len(files)-1]
	if last.path != "/Users/ramon/src/ccmd/parse_test.go" {
		t.Errorf("last file = %q, want parse_test.go", last.path)
	}
	if !last.read || last.written {
		t.Errorf("last file: read=%v written=%v, want read=true written=false", last.read, last.written)
	}
}

func TestExtractFilesLast(t *testing.T) {
	f, err := os.Open("testdata/session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	files := extractFiles(parseRecords(f))

	// Simulate -last 3
	last3 := files[len(files)-3:]
	if len(last3) != 3 {
		t.Fatalf("expected 3, got %d", len(last3))
	}
	if last3[0].path != "/Users/ramon/src/ccmd/main.go" {
		t.Errorf("last3[0] = %q, want main.go", last3[0].path)
	}
	if last3[1].path != "/Users/ramon/src/ccmd/files.go" {
		t.Errorf("last3[1] = %q, want files.go", last3[1].path)
	}
	if last3[2].path != "/Users/ramon/src/ccmd/parse_test.go" {
		t.Errorf("last3[2] = %q, want parse_test.go", last3[2].path)
	}
}

func TestCountLines(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"one", 1},
		{"one\ntwo", 2},
		{"one\ntwo\n", 2},
	}
	for _, tt := range tests {
		if got := countLines(tt.input); got != tt.want {
			t.Errorf("countLines(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestPrintFileInfo(t *testing.T) {
	out := captureStdout(t, func() {
		printFileInfo(fileInfo{
			path:    "/tmp/project/file.go",
			read:    true,
			written: true,
			added:   3,
			removed: 1,
			readLen: 12,
		}, false)
	})

	for _, want := range []string{"/tmp/project/file.go", "R 12 lines", "W +3/-1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("printFileInfo missing %q:\n%s", want, out)
		}
	}
}

func TestResolveSessionArg(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	projectDir := filepath.Join(home, "work", "match")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	chdirForTest(t, projectDir)
	projectFilter := cwdProjectDir()

	first := filepath.Join(home, ".claude", "projects", projectFilter, "first.jsonl")
	second := filepath.Join(home, ".claude", "projects", projectFilter, "second.jsonl")
	parentUUID := "12345678-1234-1234-1234-123456789abc"
	parentPath := filepath.Join(home, ".claude", "projects", projectFilter, parentUUID+".jsonl")
	writeClaudeSessionFile(t, first, projectDir, "first", "msg1", "one")
	writeClaudeSessionFile(t, second, projectDir, "second", "msg2", "two")
	writeClaudeSessionFile(t, parentPath, projectDir, "parent", "msg3", "three")
	setTestModTime(t, first, time.Date(2026, 3, 13, 9, 0, 0, 0, time.UTC))
	setTestModTime(t, second, time.Date(2026, 3, 13, 10, 0, 0, 0, time.UTC))
	setTestModTime(t, parentPath, time.Date(2026, 3, 13, 8, 0, 0, 0, time.UTC))

	t.Setenv("CCMD_PARENT_UUID", parentUUID)
	fs := flag.NewFlagSet("files", flag.ExitOnError)
	if got := resolveSessionArg(fs); got != parentPath {
		t.Fatalf("resolveSessionArg default parent = %q, want %q", got, parentPath)
	}

	t.Setenv("CCMD_PARENT_UUID", "")
	fs = flag.NewFlagSet("files", flag.ExitOnError)
	if err := fs.Parse([]string{"1"}); err != nil {
		t.Fatal(err)
	}
	if got := resolveSessionArg(fs); got != second {
		t.Fatalf("resolveSessionArg numeric = %q, want newest %q", got, second)
	}
}
