package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.n, got, tt.want)
		}
	}
}

func TestAbbreviateLines(t *testing.T) {
	// Short input: no abbreviation
	short := "line1\nline2\nline3"
	if got := abbreviateLines(short, 5); got != short {
		t.Errorf("abbreviateLines should not modify short input, got %q", got)
	}

	// Long input: should abbreviate
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, "line"+strings.Repeat("x", i))
	}
	long := strings.Join(lines, "\n")
	got := abbreviateLines(long, 3)
	if !strings.Contains(got, "... (14 lines omitted)") {
		t.Errorf("abbreviateLines should contain omission notice, got %q", got)
	}
	if !strings.HasPrefix(got, "line\n") {
		t.Errorf("abbreviateLines should start with first line, got %q", got[:20])
	}
	if !strings.HasSuffix(got, "linexxxxxxxxxxxxxxxxxxx") {
		t.Errorf("abbreviateLines should end with last line")
	}
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
		if got != tt.want {
			t.Errorf("shortPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
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
		if !strings.Contains(out, want) {
			t.Fatalf("markdown missing %q:\n%s", want, out)
		}
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

	if !strings.Contains(out, "     system note\n") {
		t.Fatalf("summary should include system entries:\n%s", out)
	}
	if strings.Contains(out, "first prompt") || strings.Contains(out, "fourth answer") {
		t.Fatalf("summary should honor range filtering:\n%s", out)
	}
	for _, want := range []string{
		"  2  **Claude:** second answer (1 tools)",
		"  3  **User:** third prompt",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("summary missing %q:\n%s", want, out)
		}
	}
}

func TestRenderSessionToString(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	input := `{"type":"user","timestamp":"2025-01-01T00:00:00Z","cwd":"/tmp/project","message":{"role":"user","content":"hello"}}
{"type":"assistant","timestamp":"2025-01-01T00:00:01Z","message":{"role":"assistant","id":"msg1","content":[{"type":"text","text":"hi there"}]}}
`
	if err := os.WriteFile(path, []byte(input), 0644); err != nil {
		t.Fatal(err)
	}

	out := renderSessionToString(path)
	for _, want := range []string{
		"# Claude Code Session",
		"**Project:** `/tmp/project`",
		"## [1] User",
		"hello",
		"## [2] Claude",
		"hi there",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("renderSessionToString missing %q:\n%s", want, out)
		}
	}
}

func TestRenderSessionToStringMissingFile(t *testing.T) {
	if got := renderSessionToString("/no/such/file.jsonl"); got != "" {
		t.Fatalf("renderSessionToString missing file = %q, want empty string", got)
	}
}
