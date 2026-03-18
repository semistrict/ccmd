package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseContentString(t *testing.T) {
	raw := json.RawMessage(`"hello world"`)
	text, blocks := parseContent(raw)
	assert.Equal(t, "hello world", text)
	assert.Nil(t, blocks)
}

func TestParseContentBlocks(t *testing.T) {
	raw := json.RawMessage(`[{"type":"text","text":"hello"},{"type":"tool_use","name":"Read","id":"123"}]`)
	text, blocks := parseContent(raw)
	assert.Empty(t, text)
	require.Len(t, blocks, 2)
	assert.Equal(t, "text", blocks[0].Type)
	assert.Equal(t, "hello", blocks[0].Text)
	assert.Equal(t, "tool_use", blocks[1].Type)
	assert.Equal(t, "Read", blocks[1].Name)
}

func TestParseContentEmpty(t *testing.T) {
	text, blocks := parseContent(nil)
	assert.Empty(t, text)
	assert.Nil(t, blocks)
}

func TestParseRecords(t *testing.T) {
	input := `{"type":"user","timestamp":"2025-01-01T00:00:00Z","message":{"role":"user","content":"hello"}}
{"type":"assistant","timestamp":"2025-01-01T00:00:01Z","message":{"role":"assistant","id":"msg1","content":[{"type":"text","text":"hi there"}]}}
not valid json
{"type":"user","timestamp":"2025-01-01T00:00:02Z","message":{"role":"user","content":"bye"}}
`
	records := parseRecords(strings.NewReader(input))
	require.Len(t, records, 3)
	assert.Equal(t, "user", records[0].Type)
	assert.Equal(t, "assistant", records[1].Type)
}

func TestBuildConversation(t *testing.T) {
	input := `{"type":"user","timestamp":"2025-01-01T00:00:00Z","message":{"role":"user","content":"write tests"}}
{"type":"assistant","timestamp":"2025-01-01T00:00:01Z","message":{"role":"assistant","id":"msg1","content":[{"type":"text","text":"Sure, here are some tests."}]}}
{"type":"user","timestamp":"2025-01-01T00:00:02Z","message":{"role":"user","content":"thanks"}}
`
	records := parseRecords(strings.NewReader(input))
	entries := buildConversation(records, "/tmp/test.jsonl", false, "")

	require.Len(t, entries, 3)
	assert.Equal(t, "user", entries[0].Role)
	require.NotEmpty(t, entries[0].Texts)
	assert.Equal(t, "write tests", entries[0].Texts[0])
	assert.Equal(t, "assistant", entries[1].Role)
	require.NotEmpty(t, entries[1].Texts)
	assert.Equal(t, "Sure, here are some tests.", entries[1].Texts[0])
	assert.Equal(t, "user", entries[2].Role)
	require.NotEmpty(t, entries[2].Texts)
	assert.Equal(t, "thanks", entries[2].Texts[0])
}

func TestFormatToolCall(t *testing.T) {
	// Read tool
	b := ContentBlock{
		Type:  "tool_use",
		Name:  "Read",
		Input: json.RawMessage(`{"file_path":"/tmp/foo.go"}`),
	}
	tc := formatToolCall(b)
	assert.Equal(t, "Read", tc.Name)
	assert.Equal(t, "/tmp/foo.go", tc.Input)

	// Bash tool
	b = ContentBlock{
		Type:  "tool_use",
		Name:  "Bash",
		Input: json.RawMessage(`{"command":"go build ./..."}`),
	}
	tc = formatToolCall(b)
	assert.Equal(t, "Bash", tc.Name)
	assert.Equal(t, "go build ./...", tc.Input)

	// Edit tool
	b = ContentBlock{
		Type:  "tool_use",
		Name:  "Edit",
		Input: json.RawMessage(`{"file_path":"/tmp/foo.go","old_string":"old","new_string":"new"}`),
	}
	tc = formatToolCall(b)
	assert.Equal(t, "Edit", tc.Name)
	assert.Equal(t, "old", tc.OldStr)
	assert.Equal(t, "new", tc.NewStr)

	// No input
	b = ContentBlock{Type: "tool_use", Name: "Unknown"}
	tc = formatToolCall(b)
	assert.Equal(t, "Unknown", tc.Name)
	assert.Empty(t, tc.Input)
}
