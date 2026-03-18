package main

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindMatchingTurnsMatchesFullTurnNumbers(t *testing.T) {
	entries := []ConversationEntry{
		{Role: "user", Texts: []string{"first prompt"}},
		{Role: "assistant", Texts: []string{"all good"}},
		{Role: "user", Texts: []string{"needle here"}},
		{Role: "assistant", Texts: []string{"done"}},
	}

	matches := findMatchingTurns(entries, regexp.MustCompile("needle"))
	require.Len(t, matches, 1)
	assert.Equal(t, 3, matches[0].Number)
	assert.Equal(t, "needle here", strings.Join(matches[0].Entry.Texts, "\n"))
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
	require.Len(t, matches, 1)
	assert.Equal(t, 2, matches[0].Number)
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
		assert.Contains(t, out, want)
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
		assert.Contains(t, out, want)
	}

	buf.Reset()
	writeSearchTool(&buf, ToolCall{Name: "Plan", Plan: "[x] done"}, false, 0)
	out = buf.String()
	assert.Contains(t, out, "### Plan")
	assert.Contains(t, out, "[x] done")

	buf.Reset()
	writeSearchTool(&buf, ToolCall{Name: "Read", Input: "file.go", Meta: "12 lines"}, false, 0)
	assert.Contains(t, buf.String(), "> **Read** `file.go` *(12 lines)*")
}

func TestResolveSearchSessionArg(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	projectDir := filepath.Join(home, "work", "match")
	require.NoError(t, os.MkdirAll(projectDir, 0755))
	chdirForTest(t, projectDir)
	projectFilter := cwdProjectDir()

	sessionPath := filepath.Join(home, ".claude", "projects", projectFilter, "session.jsonl")
	writeClaudeSessionFile(t, sessionPath, projectDir, "hello", "msg1", "hi")

	fs := flag.NewFlagSet("search", flag.ExitOnError)
	require.NoError(t, fs.Parse([]string{"needle"}))
	assert.Equal(t, sessionPath, resolveSearchSessionArg(fs))

	fs = flag.NewFlagSet("search", flag.ExitOnError)
	explicit := filepath.Join(home, "explicit.jsonl")
	writeClaudeSessionFile(t, explicit, projectDir, "hello", "msg2", "hi")
	require.NoError(t, fs.Parse([]string{"needle", explicit}))
	assert.Equal(t, explicit, resolveSearchSessionArg(fs))
}
