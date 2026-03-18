package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractSkills(t *testing.T) {
	path := filepath.Join("testdata", "skills.jsonl")
	skills := extractSkills(path)

	want := []string{"frontend-design", "retry", "cloudflare"}
	require.Len(t, skills, len(want))
	for i, s := range want {
		assert.Equal(t, s, skills[i], "skills[%d]", i)
	}
}

func TestExtractSkillsEmpty(t *testing.T) {
	skills := extractSkills("nonexistent-file.jsonl")
	assert.Nil(t, skills)
}

func TestFastcompactPromptSkills(t *testing.T) {
	prompt := fastcompactPrompt("ccmd", "abc-123", []string{"retry", "cloudflare"}, "")
	require.NotEmpty(t, prompt)

	// Should contain skill lines
	for _, s := range []string{"/retry", "/cloudflare"} {
		assert.Contains(t, prompt, s)
	}
	for _, s := range []string{`ccmd search "REGEX"`, `ccmd search "TODO"`} {
		assert.Contains(t, prompt, s)
	}

	// No skills → no skills section
	prompt2 := fastcompactPrompt("ccmd", "abc-123", nil, "")
	assert.NotContains(t, prompt2, "skills loaded")
}

func TestFastcompactPromptUserMessage(t *testing.T) {
	prompt := fastcompactPrompt("ccmd", "abc-123", nil, "now fix the tests")
	assert.Contains(t, prompt, "now fix the tests")
	assert.Contains(t, prompt, "instructions after requesting the context restart")

	// Empty user message → no extra section
	prompt2 := fastcompactPrompt("ccmd", "abc-123", nil, "")
	assert.NotContains(t, prompt2, "instructions after requesting")
}

func TestBuildFastcompactArgs(t *testing.T) {
	args := buildFastcompactArgs("abc-123", []string{"retry"}, `{"hooks":{}}`, "fix it")
	require.Len(t, args, 3)
	assert.Equal(t, "--settings", args[0])
	assert.Equal(t, `{"hooks":{}}`, args[1])
	assert.Contains(t, args[2], "abc-123")
	assert.Contains(t, args[2], "fix it")
}

func TestExtractUUID(t *testing.T) {
	got := extractUUID("/tmp/12345678-1234-1234-1234-123456789abc.jsonl")
	assert.Equal(t, "12345678-1234-1234-1234-123456789abc", got)
}

func TestHandlePreToolUse(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/pretooluse", strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"go test ./..."}}`))
	w := httptest.NewRecorder()
	handlePreToolUse(w, req)
	assert.Contains(t, w.Body.String(), `"permissionDecision":"allow"`)

	req = httptest.NewRequest(http.MethodPost, "/pretooluse", strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"rm -rf build"}}`))
	w = httptest.NewRecorder()
	handlePreToolUse(w, req)
	assert.Empty(t, w.Body.String())
}

func TestHandlePostToolUse(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/posttooluse", strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"cd subdir"}}`))
	w := httptest.NewRecorder()
	handlePostToolUse(w, req)
	assert.Contains(t, w.Body.String(), "Avoid changing directory from the project root")

	req = httptest.NewRequest(http.MethodPost, "/posttooluse", strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"pwd"}}`))
	w = httptest.NewRecorder()
	handlePostToolUse(w, req)
	assert.Empty(t, w.Body.String())
}

func TestHandlePrecompact(t *testing.T) {
	restartCh := make(chan restartInfo, 1)
	handler := handlePrecompact(restartCh)

	req := httptest.NewRequest(http.MethodPost, "/precompact", strings.NewReader(`{"transcript_path":"/tmp/session.jsonl","session_id":"abc","prompt":"fastcompact fix the tests"}`))
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Contains(t, w.Body.String(), `"decision":"block"`)
	select {
	case info := <-restartCh:
		assert.Equal(t, "abc", info.SessionID)
		assert.Equal(t, "/tmp/session.jsonl", info.TranscriptPath)
		assert.Equal(t, "fix the tests", info.UserMessage)
	default:
		require.FailNow(t, "expected restart signal")
	}

	req = httptest.NewRequest(http.MethodPost, "/precompact", strings.NewReader(`{"transcript_path":"/tmp/session.jsonl","session_id":"abc","prompt":"hello"}`))
	w = httptest.NewRecorder()
	handler(w, req)
	select {
	case info := <-restartCh:
		require.FailNowf(t, "unexpected restart signal", "%+v", info)
	default:
	}
	assert.Empty(t, w.Body.String())
}

func TestHooksSettings(t *testing.T) {
	got := hooksSettings("http://127.0.0.1:12345")
	for _, want := range []string{
		`"UserPromptSubmit"`,
		`http://127.0.0.1:12345/precompact`,
		`http://127.0.0.1:12345/pretooluse`,
		`http://127.0.0.1:12345/posttooluse`,
	} {
		assert.Contains(t, got, want)
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
		assert.Equal(t, tt.want, isDangerous(tt.cmd), "isDangerous(%q)", tt.cmd)
	}
}

func TestContainsSequence(t *testing.T) {
	assert.True(t, containsSequence([]string{"git", "push", "-u", "origin", "main", "--force"}, []string{"git", "push", "--force"}))
	assert.False(t, containsSequence([]string{"git", "push"}, []string{"git", "reset"}))
}
