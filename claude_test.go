package main

import (
	"net/http"
	"net/http/httptest"
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

func TestBuildFastcompactArgs(t *testing.T) {
	args := buildFastcompactArgs("abc-123", []string{"retry"}, `{"hooks":{}}`, "fix it")
	if len(args) != 3 {
		t.Fatalf("len(args) = %d, want 3", len(args))
	}
	if args[0] != "--settings" || args[1] != `{"hooks":{}}` {
		t.Fatalf("unexpected args prefix: %v", args[:2])
	}
	if !strings.Contains(args[2], "abc-123") || !strings.Contains(args[2], "fix it") {
		t.Fatalf("prompt arg missing expected content:\n%s", args[2])
	}
}

func TestExtractUUID(t *testing.T) {
	got := extractUUID("/tmp/12345678-1234-1234-1234-123456789abc.jsonl")
	if got != "12345678-1234-1234-1234-123456789abc" {
		t.Fatalf("extractUUID = %q", got)
	}
}

func TestHandlePreToolUse(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/pretooluse", strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"go test ./..."}}`))
	w := httptest.NewRecorder()
	handlePreToolUse(w, req)
	if body := w.Body.String(); !strings.Contains(body, `"permissionDecision":"allow"`) {
		t.Fatalf("safe bash body = %q", body)
	}

	req = httptest.NewRequest(http.MethodPost, "/pretooluse", strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"rm -rf build"}}`))
	w = httptest.NewRecorder()
	handlePreToolUse(w, req)
	if body := w.Body.String(); body != "" {
		t.Fatalf("dangerous bash should return empty body, got %q", body)
	}
}

func TestHandlePostToolUse(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/posttooluse", strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"cd subdir"}}`))
	w := httptest.NewRecorder()
	handlePostToolUse(w, req)
	if body := w.Body.String(); !strings.Contains(body, "Avoid changing directory from the project root") {
		t.Fatalf("cd warning body = %q", body)
	}

	req = httptest.NewRequest(http.MethodPost, "/posttooluse", strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"pwd"}}`))
	w = httptest.NewRecorder()
	handlePostToolUse(w, req)
	if body := w.Body.String(); body != "" {
		t.Fatalf("non-cd command should not add context, got %q", body)
	}
}

func TestHandlePrecompact(t *testing.T) {
	restartCh := make(chan restartInfo, 1)
	handler := handlePrecompact(restartCh)

	req := httptest.NewRequest(http.MethodPost, "/precompact", strings.NewReader(`{"transcript_path":"/tmp/session.jsonl","session_id":"abc","prompt":"fastcompact fix the tests"}`))
	w := httptest.NewRecorder()
	handler(w, req)

	if body := w.Body.String(); !strings.Contains(body, `"decision":"block"`) {
		t.Fatalf("precompact body = %q", body)
	}
	select {
	case info := <-restartCh:
		if info.SessionID != "abc" || info.TranscriptPath != "/tmp/session.jsonl" || info.UserMessage != "fix the tests" {
			t.Fatalf("restart info = %+v", info)
		}
	default:
		t.Fatal("expected restart signal")
	}

	req = httptest.NewRequest(http.MethodPost, "/precompact", strings.NewReader(`{"transcript_path":"/tmp/session.jsonl","session_id":"abc","prompt":"hello"}`))
	w = httptest.NewRecorder()
	handler(w, req)
	select {
	case info := <-restartCh:
		t.Fatalf("unexpected restart signal: %+v", info)
	default:
	}
	if body := w.Body.String(); body != "" {
		t.Fatalf("non-fastcompact prompt should return empty body, got %q", body)
	}
}

func TestHooksSettings(t *testing.T) {
	got := hooksSettings("http://127.0.0.1:12345")
	for _, want := range []string{
		`"UserPromptSubmit"`,
		`http://127.0.0.1:12345/precompact`,
		`http://127.0.0.1:12345/pretooluse`,
		`http://127.0.0.1:12345/posttooluse`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("hooksSettings missing %q: %s", want, got)
		}
	}
}

func TestIsDangerous(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"rm -rf build", true},
		{"git push -u origin main --force", true},
		{"git commit -m \"do not rm -rf this\"", false},
		{"go test ./...", false},
		{"unterminated 'quote", true},
	}
	for _, tt := range tests {
		if got := isDangerous(tt.cmd); got != tt.want {
			t.Errorf("isDangerous(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

func TestContainsSequence(t *testing.T) {
	if !containsSequence([]string{"git", "push", "-u", "origin", "main", "--force"}, []string{"git", "push", "--force"}) {
		t.Fatal("containsSequence should match ordered subsequence with gaps")
	}
	if containsSequence([]string{"git", "push"}, []string{"git", "reset"}) {
		t.Fatal("containsSequence should not match different sequence")
	}
}
