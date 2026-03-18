package main

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractChanges(t *testing.T) {
	f, err := os.Open("testdata/session.jsonl")
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	ps := parseSessionFile(f, "testdata/session.jsonl", "")
	changes := extractChanges(ps.Entries, 0)

	require.NotEmpty(t, changes)

	// First change should be a Write to files.go
	assert.Equal(t, "Write", changes[0].op)
	assert.Equal(t, "src/ccmd/files.go", changes[0].path)

	// Should have Edit operations for claude.go
	var hasClaudeEdit bool
	for _, ch := range changes {
		if ch.op == "Edit" && ch.path == "src/ccmd/claude.go" {
			hasClaudeEdit = true
			break
		}
	}
	assert.True(t, hasClaudeEdit)
}

func TestExtractChangesLast(t *testing.T) {
	f, err := os.Open("testdata/session.jsonl")
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	ps := parseSessionFile(f, "testdata/session.jsonl", "")

	// -last 2 should have no changes (last 2 turns are user+assistant with no edits)
	changes := extractChanges(ps.Entries, 2)
	assert.Len(t, changes, 0)

	// -last 5 should have some changes
	changes = extractChanges(ps.Entries, 5)
	assert.NotEmpty(t, changes)

	// All changes should be fewer than all changes
	allChanges := extractChanges(ps.Entries, 0)
	assert.Less(t, len(changes), len(allChanges))
}

func TestChangesFromToolsIncludesSubagent(t *testing.T) {
	tools := []ToolCall{
		{Name: "Edit", Input: "a.go", OldStr: "old", NewStr: "new"},
		{
			Name: "Agent",
			SubConversation: []ConversationEntry{
				{
					Role: "assistant",
					Tools: []ToolCall{
						{Name: "Write", Input: "b.go", NewStr: "content"},
					},
				},
			},
		},
	}

	changes := changesFromTools(tools)
	require.Len(t, changes, 2)
	assert.Equal(t, "a.go", changes[0].path)
	assert.Equal(t, "Edit", changes[0].op)
	assert.Equal(t, "b.go", changes[1].path)
	assert.Equal(t, "Write", changes[1].op)
}

func TestPrintChange(t *testing.T) {
	editOut := captureStdout(t, func() {
		printChange(fileChange{
			path:   "/tmp/project/file.go",
			op:     "Edit",
			oldStr: "old line",
			newStr: "new line",
		}, false)
	})
	for _, want := range []string{"--- /tmp/project/file.go (Edit)", "-old line", "+new line"} {
		assert.Contains(t, editOut, want)
	}

	var lines []string
	for i := 0; i < 45; i++ {
		lines = append(lines, "line")
	}
	writeOut := captureStdout(t, func() {
		printChange(fileChange{
			path:   "/tmp/project/file.go",
			op:     "Write",
			newStr: strings.Join(lines, "\n"),
		}, false)
	})
	assert.Contains(t, writeOut, "... (15 lines omitted)")
}
