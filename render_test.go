package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTruncate(t *testing.T) {
	tests := []struct {
		input string
		n     int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 8, "hello..."},
		{"abc", 3, "abc"},
		{"abcd", 3, "..."},
		{"", 5, ""},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.n)
		assert.Equal(t, tt.want, got, "truncate(%q, %d)", tt.input, tt.n)
	}
}

func TestAbbreviateLines(t *testing.T) {
	// Short input: no abbreviation
	short := "line1\nline2\nline3"
	assert.Equal(t, short, abbreviateLines(short, 5))

	// Long input: should abbreviate
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, "line"+strings.Repeat("x", i))
	}
	long := strings.Join(lines, "\n")
	got := abbreviateLines(long, 3)
	assert.Contains(t, got, "... (14 lines omitted)")
	assert.True(t, strings.HasPrefix(got, "line\n"))
	assert.True(t, strings.HasSuffix(got, "linexxxxxxxxxxxxxxxxxxx"))
}

func TestShortPath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/tmp/foo.go", "/tmp/foo.go"},
		{"relative/path.go", "relative/path.go"},
	}
	for _, tt := range tests {
		got := shortPath(tt.input)
		assert.Equal(t, tt.want, got, "shortPath(%q)", tt.input)
	}
}

func TestWriteMarkdownIncludesMetadataAndNestedContent(t *testing.T) {
	ps := ParsedSession{
		Format:    FormatCodex,
		CWD:       "/tmp/project",
		GitBranch: "main",
		SessionID: "session-123",
		Version:   "1.2.3",
		StartTime: time.Date(2026, 3, 13, 10, 30, 0, 0, time.UTC),
		Entries: []ConversationEntry{
			{Role: "system", Texts: []string{"*— context compacted —*"}},
			{Role: "user", Texts: []string{"hello\nworld"}},
			{
				Role:     "assistant",
				Texts:    []string{"answer"},
				Thinking: []string{"step one"},
				Tools: []ToolCall{
					{Name: "Plan", Plan: "[x] inspect\n[ ] patch"},
					{Name: "Read", Input: "src/foo.go", Meta: "12 lines"},
					{Name: "Edit", Input: "src/foo.go", OldStr: "old line", NewStr: "new line"},
					{
						Name:  "Agent",
						Input: "delegate",
						SubConversation: []ConversationEntry{
							{Role: "user", Texts: []string{"sub prompt"}},
							{Role: "assistant", Texts: []string{"sub answer"}},
						},
					},
				},
			},
		},
	}

	var buf strings.Builder
	writeMarkdown(&buf, ps, true, false, 0, 0)
	out := buf.String()

	for _, want := range []string{
		"# Codex Session",
		"**Date:** 2026-03-13 10:30",
		"**Project:** `/tmp/project`",
		"**Branch:** `main`",
		"**Session:** `session-123`",
		"**Codex:** v1.2.3",
		"*— context compacted —*",
		"## [1] User",
		"hello",
		"world",
		"## [2] Claude",
		"> *step one*",
		"### Plan",
		"[x] inspect",
		"> **Read** `src/foo.go` *(12 lines)*",
		"```diff",
		"-old line",
		"+new line",
		"> **Agent** `delegate`",
		"> **Prompt:**",
		"> sub prompt",
		"> sub answer",
	} {
		assert.Contains(t, out, want)
	}
}

func TestWriteEntriesSummaryHonorsRange(t *testing.T) {
	entries := []ConversationEntry{
		{Role: "system", Texts: []string{"system note"}},
		{Role: "user", Texts: []string{"first prompt"}},
		{Role: "assistant", Texts: []string{"second answer"}, Tools: []ToolCall{{Name: "Read", Input: "foo.go"}}},
		{Role: "user", Texts: []string{"third prompt"}},
		{Role: "assistant", Texts: []string{"fourth answer"}},
	}

	var buf strings.Builder
	writeEntries(&buf, entries, false, true, 2, 3, 0)
	out := buf.String()

	assert.Contains(t, out, "     system note\n")
	assert.NotContains(t, out, "first prompt")
	assert.NotContains(t, out, "fourth answer")
	for _, want := range []string{
		"  2  **Claude:** second answer (1 tools)",
		"  3  **User:** third prompt",
	} {
		assert.Contains(t, out, want)
	}
}

func TestRenderSessionToString(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	input := `{"type":"user","timestamp":"2025-01-01T00:00:00Z","cwd":"/tmp/project","message":{"role":"user","content":"hello"}}
{"type":"assistant","timestamp":"2025-01-01T00:00:01Z","message":{"role":"assistant","id":"msg1","content":[{"type":"text","text":"hi there"}]}}
`
	require.NoError(t, os.WriteFile(path, []byte(input), 0644))

	out := renderSessionToString(path)
	for _, want := range []string{
		"# Claude Code Session",
		"**Project:** `/tmp/project`",
		"## [1] User",
		"hello",
		"## [2] Claude",
		"hi there",
	} {
		assert.Contains(t, out, want)
	}
}

func TestRenderSessionToStringMissingFile(t *testing.T) {
	assert.Empty(t, renderSessionToString("/no/such/file.jsonl"))
}
