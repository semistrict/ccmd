package main

import (
	"testing"
	"time"
)

func TestRelativeTime(t *testing.T) {
	// Just test that it doesn't panic on zero value
	got := relativeTime(time.Time{})
	if got == "" {
		t.Error("relativeTime should return something for zero time")
	}
}

func TestFormatTokens(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{0, "0"},
		{500, "500"},
		{1000, "1k"},
		{1500, "2k"},
		{50000, "50k"},
		{1000000, "1.0M"},
		{1500000, "1.5M"},
	}
	for _, tt := range tests {
		got := formatTokens(tt.input)
		if got != tt.want {
			t.Errorf("formatTokens(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSessionUUID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/home/user/.claude/projects/foo/abc-123.jsonl", "abc-123"},
		{"/tmp/967ff820-53f4-4b2c-af1d-5991b2bc22b4.jsonl", "967ff820-53f4-4b2c-af1d-5991b2bc22b4"},
	}
	for _, tt := range tests {
		got := sessionUUID(tt.input)
		if got != tt.want {
			t.Errorf("sessionUUID(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
