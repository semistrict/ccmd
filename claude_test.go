package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractSkills(t *testing.T) {
	path := filepath.Join("testdata", "skills.jsonl")
	skills := extractSkills(path)

	want := []string{"frontend-design", "retry", "cloudflare"}
	if len(skills) != len(want) {
		t.Fatalf("extractSkills: got %d skills %v, want %d %v", len(skills), skills, len(want), want)
	}
	for i, s := range want {
		if skills[i] != s {
			t.Errorf("skills[%d] = %q, want %q", i, skills[i], s)
		}
	}
}

func TestExtractSkillsEmpty(t *testing.T) {
	skills := extractSkills("nonexistent-file.jsonl")
	if skills != nil {
		t.Errorf("expected nil for nonexistent file, got %v", skills)
	}
}

func TestFastcompactPromptSkills(t *testing.T) {
	prompt := fastcompactPrompt("ccmd", "abc-123", []string{"retry", "cloudflare"}, "")
	if got := prompt; got == "" {
		t.Fatal("prompt is empty")
	}

	// Should contain skill lines
	for _, s := range []string{"/retry", "/cloudflare"} {
		if !strings.Contains(prompt, s) {
			t.Errorf("prompt missing skill %q", s)
		}
	}
	for _, s := range []string{`ccmd search "REGEX"`, `ccmd search "TODO"`} {
		if !strings.Contains(prompt, s) {
			t.Errorf("prompt missing search helper %q", s)
		}
	}

	// No skills → no skills section
	prompt2 := fastcompactPrompt("ccmd", "abc-123", nil, "")
	if strings.Contains(prompt2, "skills loaded") {
		t.Error("prompt with nil skills should not have skills section")
	}
}

func TestFastcompactPromptUserMessage(t *testing.T) {
	prompt := fastcompactPrompt("ccmd", "abc-123", nil, "now fix the tests")
	if !strings.Contains(prompt, "now fix the tests") {
		t.Error("prompt should contain the user message")
	}
	if !strings.Contains(prompt, "instructions after requesting the context restart") {
		t.Error("prompt should contain the user message preamble")
	}

	// Empty user message → no extra section
	prompt2 := fastcompactPrompt("ccmd", "abc-123", nil, "")
	if strings.Contains(prompt2, "instructions after requesting") {
		t.Error("prompt with empty userMessage should not have user message section")
	}
}
