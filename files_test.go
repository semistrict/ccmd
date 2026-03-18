package main

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractFiles(t *testing.T) {
	f, err := os.Open("testdata/session.jsonl")
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	files := extractFiles(parseRecords(f))

	require.Len(t, files, 11)

	// First file: types.go, read only
	assert.Equal(t, "/Users/ramon/src/ccmd/types.go", files[0].path)
	assert.True(t, files[0].read)
	assert.False(t, files[0].written)
	assert.Equal(t, 82, files[0].readLen)

	// claude.go: read + edited
	var claude *fileInfo
	for i := range files {
		if files[i].path == "/Users/ramon/src/ccmd/claude.go" {
			claude = &files[i]
			break
		}
	}
	require.NotNil(t, claude)
	assert.True(t, claude.read)
	assert.True(t, claude.written)
	assert.Equal(t, 4, claude.added)
	assert.Equal(t, 5, claude.removed)

	// files.go: read + written (Write tool)
	var filesGo *fileInfo
	for i := range files {
		if files[i].path == "/Users/ramon/src/ccmd/files.go" {
			filesGo = &files[i]
			break
		}
	}
	require.NotNil(t, filesGo)
	assert.True(t, filesGo.read)
	assert.True(t, filesGo.written)

	// Last file should be parse_test.go (read only)
	last := files[len(files)-1]
	assert.Equal(t, "/Users/ramon/src/ccmd/parse_test.go", last.path)
	assert.True(t, last.read)
	assert.False(t, last.written)
}

func TestExtractFilesLast(t *testing.T) {
	f, err := os.Open("testdata/session.jsonl")
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	files := extractFiles(parseRecords(f))

	// Simulate -last 3
	last3 := files[len(files)-3:]
	require.Len(t, last3, 3)
	assert.Equal(t, "/Users/ramon/src/ccmd/main.go", last3[0].path)
	assert.Equal(t, "/Users/ramon/src/ccmd/files.go", last3[1].path)
	assert.Equal(t, "/Users/ramon/src/ccmd/parse_test.go", last3[2].path)
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
		assert.Equal(t, tt.want, countLines(tt.input), "countLines(%q)", tt.input)
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
		assert.Contains(t, out, want)
	}
}

func TestResolveSessionArg(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	projectDir := filepath.Join(home, "work", "match")
	require.NoError(t, os.MkdirAll(projectDir, 0755))
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
	assert.Equal(t, parentPath, resolveSessionArg(fs))

	t.Setenv("CCMD_PARENT_UUID", "")
	fs = flag.NewFlagSet("files", flag.ExitOnError)
	require.NoError(t, fs.Parse([]string{"1"}))
	assert.Equal(t, second, resolveSessionArg(fs))
}
