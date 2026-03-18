package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRelativeTime(t *testing.T) {
	// Just test that it doesn't panic on zero value
	got := relativeTime(time.Time{})
	assert.NotEmpty(t, got)
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
		assert.Equal(t, tt.want, got, "formatTokens(%d)", tt.input)
	}
}

func TestSessionUUID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/home/user/.claude/projects/foo/abc-123.jsonl", "abc-123"},
		{"/tmp/967ff820-53f4-4b2c-af1d-5991b2bc22b4.jsonl", "967ff820-53f4-4b2c-af1d-5991b2bc22b4"},
		{"/Users/user/.codex/sessions/2026/03/09/rollout-2026-03-09T21-40-03-019cd60b-7fe1-7223-a1c0-6f0edbb837fc.jsonl", "019cd60b-7fe1-7223-a1c0-6f0edbb837fc"},
	}
	for _, tt := range tests {
		got := sessionUUID(tt.input)
		assert.Equal(t, tt.want, got, "sessionUUID(%q)", tt.input)
	}
}

func TestLaunchArgs(t *testing.T) {
	tests := []struct {
		name   string
		format SessionFormat
		action string
		uuid   string
		want   []string
	}{
		{
			name:   "claude continue",
			format: FormatClaudeCode,
			action: "continue",
			uuid:   "abc",
			want:   []string{"--resume", "abc"},
		},
		{
			name:   "claude fork",
			format: FormatClaudeCode,
			action: "fork",
			uuid:   "abc",
			want:   []string{"--resume", "abc", "--fork-session"},
		},
		{
			name:   "codex continue",
			format: FormatCodex,
			action: "continue",
			uuid:   "abc",
			want:   []string{"resume", "abc"},
		},
		{
			name:   "codex fork",
			format: FormatCodex,
			action: "fork",
			uuid:   "abc",
			want:   []string{"fork", "abc"},
		},
	}

	for _, tt := range tests {
		got := launchArgs(tt.format, tt.action, tt.uuid)
		assert.Equal(t, tt.want, got, tt.name)
	}
}
