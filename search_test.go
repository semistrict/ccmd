package main

import (
	"bytes"
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
