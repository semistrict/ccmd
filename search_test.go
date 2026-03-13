package main

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestFindMatchingTurnsMatchesFullTurnNumbers(t *testing.T) {
	entries := []ConversationEntry{
		{Role: "user", Texts: []string{"first prompt"}},
		{Role: "assistant", Texts: []string{"all good"}},
		{Role: "user", Texts: []string{"needle here"}},
		{Role: "assistant", Texts: []string{"done"}},
	}

	matches := findMatchingTurns(entries, regexp.MustCompile("needle"))
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].Number != 3 {
		t.Fatalf("match number = %d, want 3", matches[0].Number)
	}
	if got := strings.Join(matches[0].Entry.Texts, "\n"); got != "needle here" {
		t.Fatalf("match text = %q, want %q", got, "needle here")
	}
}

func TestFindMatchingTurnsMatchesAssistantToolsAndSubagents(t *testing.T) {
	entries := []ConversationEntry{
		{Role: "user", Texts: []string{"prompt"}},
		{
			Role: "assistant",
			Tools: []ToolCall{
				{
					Name:  "Agent",
					Input: "delegate",
					SubConversation: []ConversationEntry{
						{Role: "assistant", Texts: []string{"subagent found needle"}},
					},
				},
			},
		},
	}

	matches := findMatchingTurns(entries, regexp.MustCompile("needle"))
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].Number != 2 {
		t.Fatalf("match number = %d, want 2", matches[0].Number)
	}
}

func TestRenderMatchedTurnsPrintsFullTurns(t *testing.T) {
	matches := []matchedTurn{
		{
			Number: 2,
			Entry: ConversationEntry{
				Role:     "assistant",
				Texts:    []string{"matched answer"},
				Thinking: []string{"reasoning"},
				Tools: []ToolCall{
					{Name: "Read", Input: "/tmp/file.txt"},
				},
			},
		},
		{
			Number: 4,
			Entry: ConversationEntry{
				Role:  "user",
				Texts: []string{"another match"},
			},
		},
	}

	var buf bytes.Buffer
	renderMatchedTurns(&buf, matches, true)
	out := buf.String()

	for _, want := range []string{
		"## [2] Claude",
		"matched answer",
		"> *reasoning*",
		"> **Read** `/tmp/file.txt`",
		"## [4] User",
		"another match",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestWriteSearchToolCoversBranches(t *testing.T) {
	tc := ToolCall{
		Name:   "Edit",
		Input:  "src/file.go",
		OldStr: "old line",
		NewStr: "new line",
		Error:  "line one\nline two",
		SubConversation: []ConversationEntry{
			{Role: "user", Texts: []string{"nested prompt"}},
			{Role: "assistant", Texts: []string{"nested answer"}},
		},
	}

	var buf bytes.Buffer
	writeSearchTool(&buf, tc, true, 0)
	out := buf.String()
	for _, want := range []string{
		"> **Edit** `src/file.go`",
		"```diff",
		"-old line",
		"+new line",
		"> **⚠** line one",
		"> **⚠** line two",
		"> **Prompt:**",
		"> nested prompt",
		"> nested answer",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("writeSearchTool output missing %q:\n%s", want, out)
		}
	}

	buf.Reset()
	writeSearchTool(&buf, ToolCall{Name: "Plan", Plan: "[x] done"}, false, 0)
	if out := buf.String(); !strings.Contains(out, "### Plan") || !strings.Contains(out, "[x] done") {
		t.Fatalf("plan output missing content:\n%s", out)
	}

	buf.Reset()
	writeSearchTool(&buf, ToolCall{Name: "Read", Input: "file.go", Meta: "12 lines"}, false, 0)
	if out := buf.String(); !strings.Contains(out, "> **Read** `file.go` *(12 lines)*") {
		t.Fatalf("meta output missing content:\n%s", out)
	}
}

func TestResolveSearchSessionArg(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	projectDir := filepath.Join(home, "work", "match")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	chdirForTest(t, projectDir)
	projectFilter := cwdProjectDir()

	sessionPath := filepath.Join(home, ".claude", "projects", projectFilter, "session.jsonl")
	writeClaudeSessionFile(t, sessionPath, projectDir, "hello", "msg1", "hi")

	fs := flag.NewFlagSet("search", flag.ExitOnError)
	if err := fs.Parse([]string{"needle"}); err != nil {
		t.Fatal(err)
	}
	if got := resolveSearchSessionArg(fs); got != sessionPath {
		t.Fatalf("default resolveSearchSessionArg = %q, want %q", got, sessionPath)
	}

	fs = flag.NewFlagSet("search", flag.ExitOnError)
	explicit := filepath.Join(home, "explicit.jsonl")
	writeClaudeSessionFile(t, explicit, projectDir, "hello", "msg2", "hi")
	if err := fs.Parse([]string{"needle", explicit}); err != nil {
		t.Fatal(err)
	}
	if got := resolveSearchSessionArg(fs); got != explicit {
		t.Fatalf("explicit resolveSearchSessionArg = %q, want %q", got, explicit)
	}
}
