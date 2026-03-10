package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseContentString(t *testing.T) {
	raw := json.RawMessage(`"hello world"`)
	text, blocks := parseContent(raw)
	if text != "hello world" {
		t.Errorf("parseContent string: got %q, want %q", text, "hello world")
	}
	if blocks != nil {
		t.Errorf("parseContent string: expected nil blocks, got %v", blocks)
	}
}

func TestParseContentBlocks(t *testing.T) {
	raw := json.RawMessage(`[{"type":"text","text":"hello"},{"type":"tool_use","name":"Read","id":"123"}]`)
	text, blocks := parseContent(raw)
	if text != "" {
		t.Errorf("parseContent blocks: expected empty text, got %q", text)
	}
	if len(blocks) != 2 {
		t.Fatalf("parseContent blocks: expected 2 blocks, got %d", len(blocks))
	}
	if blocks[0].Type != "text" || blocks[0].Text != "hello" {
		t.Errorf("block 0: got type=%q text=%q", blocks[0].Type, blocks[0].Text)
	}
	if blocks[1].Type != "tool_use" || blocks[1].Name != "Read" {
		t.Errorf("block 1: got type=%q name=%q", blocks[1].Type, blocks[1].Name)
	}
}

func TestParseContentEmpty(t *testing.T) {
	text, blocks := parseContent(nil)
	if text != "" || blocks != nil {
		t.Errorf("parseContent nil: expected empty, got text=%q blocks=%v", text, blocks)
	}
}

func TestParseRecords(t *testing.T) {
	input := `{"type":"user","timestamp":"2025-01-01T00:00:00Z","message":{"role":"user","content":"hello"}}
{"type":"assistant","timestamp":"2025-01-01T00:00:01Z","message":{"role":"assistant","id":"msg1","content":[{"type":"text","text":"hi there"}]}}
not valid json
{"type":"user","timestamp":"2025-01-01T00:00:02Z","message":{"role":"user","content":"bye"}}
`
	records := parseRecords(strings.NewReader(input))
	if len(records) != 3 {
		t.Fatalf("expected 3 records (invalid line skipped), got %d", len(records))
	}
	if records[0].Type != "user" {
		t.Errorf("record 0: type=%q, want user", records[0].Type)
	}
	if records[1].Type != "assistant" {
		t.Errorf("record 1: type=%q, want assistant", records[1].Type)
	}
}

func TestBuildConversation(t *testing.T) {
	input := `{"type":"user","timestamp":"2025-01-01T00:00:00Z","message":{"role":"user","content":"write tests"}}
{"type":"assistant","timestamp":"2025-01-01T00:00:01Z","message":{"role":"assistant","id":"msg1","content":[{"type":"text","text":"Sure, here are some tests."}]}}
{"type":"user","timestamp":"2025-01-01T00:00:02Z","message":{"role":"user","content":"thanks"}}
`
	records := parseRecords(strings.NewReader(input))
	entries := buildConversation(records, "/tmp/test.jsonl", false, "")

	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].Role != "user" || entries[0].Texts[0] != "write tests" {
		t.Errorf("entry 0: role=%q text=%q", entries[0].Role, entries[0].Texts)
	}
	if entries[1].Role != "assistant" || entries[1].Texts[0] != "Sure, here are some tests." {
		t.Errorf("entry 1: role=%q text=%q", entries[1].Role, entries[1].Texts)
	}
	if entries[2].Role != "user" || entries[2].Texts[0] != "thanks" {
		t.Errorf("entry 2: role=%q text=%q", entries[2].Role, entries[2].Texts)
	}
}

func TestFormatToolCall(t *testing.T) {
	// Read tool
	b := ContentBlock{
		Type:  "tool_use",
		Name:  "Read",
		Input: json.RawMessage(`{"file_path":"/tmp/foo.go"}`),
	}
	tc := formatToolCall(b)
	if tc.Name != "Read" || tc.Input != "/tmp/foo.go" {
		t.Errorf("Read tool: name=%q input=%q", tc.Name, tc.Input)
	}

	// Bash tool
	b = ContentBlock{
		Type:  "tool_use",
		Name:  "Bash",
		Input: json.RawMessage(`{"command":"go build ./..."}`),
	}
	tc = formatToolCall(b)
	if tc.Name != "Bash" || tc.Input != "go build ./..." {
		t.Errorf("Bash tool: name=%q input=%q", tc.Name, tc.Input)
	}

	// Edit tool
	b = ContentBlock{
		Type:  "tool_use",
		Name:  "Edit",
		Input: json.RawMessage(`{"file_path":"/tmp/foo.go","old_string":"old","new_string":"new"}`),
	}
	tc = formatToolCall(b)
	if tc.Name != "Edit" || tc.OldStr != "old" || tc.NewStr != "new" {
		t.Errorf("Edit tool: name=%q old=%q new=%q", tc.Name, tc.OldStr, tc.NewStr)
	}

	// No input
	b = ContentBlock{Type: "tool_use", Name: "Unknown"}
	tc = formatToolCall(b)
	if tc.Name != "Unknown" || tc.Input != "" {
		t.Errorf("empty tool: name=%q input=%q", tc.Name, tc.Input)
	}
}
