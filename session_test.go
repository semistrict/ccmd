package main

import "testing"

func TestIsUUID(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"967ff820-53f4-4b2c-af1d-5991b2bc22b4", true},
		{"00000000-0000-0000-0000-000000000000", true},
		{"not-a-uuid", false},
		{"967ff820-53f4-4b2c-af1d", false},
		{"967FF820-53F4-4B2C-AF1D-5991B2BC22B4", false}, // uppercase not matched
		{"", false},
		{"967ff820-53f4-4b2c-af1d-5991b2bc22b4.jsonl", false},
	}
	for _, tt := range tests {
		got := isUUID(tt.input)
		if got != tt.want {
			t.Errorf("isUUID(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestExtractProjectName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"-Users-ramon-src-ccmd", "ccmd"},
		{"-Users-ramon-src-claude-code-sandbox", "claude-code-sandbox"},
		{"-Users-ramon-Desktop-myproject", "ramon-Desktop-myproject"},
		{"simple", "simple"},
	}
	for _, tt := range tests {
		got := extractProjectName(tt.input)
		if got != tt.want {
			t.Errorf("extractProjectName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestCwdProjectDir(t *testing.T) {
	got := cwdProjectDir()
	if got == "" {
		t.Error("cwdProjectDir() should not return empty string")
	}
	if got[0] != '-' {
		t.Errorf("cwdProjectDir() should start with '-', got %q", got)
	}
}
