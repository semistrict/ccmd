package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		assert.Equal(t, tt.want, got, "isUUID(%q)", tt.input)
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
		assert.Equal(t, tt.want, got, "extractProjectName(%q)", tt.input)
	}
}

func TestCwdProjectDir(t *testing.T) {
	got := cwdProjectDir()
	require.NotEmpty(t, got)
	assert.Equal(t, byte('-'), got[0])
}

func TestScanSessionInfoSkipsToolOnlySidechainAndDuplicateAssistant(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	content := `{"type":"user","timestamp":"2025-01-01T00:00:00Z","cwd":"/tmp/project","message":{"role":"user","content":"hello world"}}
{"type":"user","timestamp":"2025-01-01T00:00:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool1","content":"ignored"}]}}
{"type":"assistant","timestamp":"2025-01-01T00:00:02Z","message":{"role":"assistant","id":"msg1","content":[{"type":"text","text":"answer"}]}}
{"type":"assistant","timestamp":"2025-01-01T00:00:03Z","message":{"role":"assistant","id":"msg1","content":[{"type":"text","text":"duplicate"}]}}
{"type":"user","timestamp":"2025-01-01T00:00:04Z","isSidechain":true,"message":{"role":"user","content":"ignored sidechain"}}
`
	writeTestFile(t, path, content)

	modTime := time.Date(2026, 3, 13, 12, 0, 0, 0, time.UTC)
	info := scanSessionInfo(path, "-Users-ramon-src-demo", modTime)
	require.NotNil(t, info)
	assert.Equal(t, "demo", info.Project)
	assert.Equal(t, "/tmp/project", info.CWD)
	assert.Equal(t, "hello world", info.Preview)
	assert.Equal(t, 2, info.Turns)
	assert.True(t, info.Timestamp.Equal(modTime))
}

func TestFindCodexSessionByUUID(t *testing.T) {
	root := t.TempDir()
	uuid := "11111111-2222-3333-4444-555555555555"
	path := filepath.Join(root, "2026", "03", "13", "session-"+uuid+".jsonl")
	writeCodexSessionFile(t, path, "/tmp/project", uuid, "hello", "hi")

	got := findCodexSessionByUUID(root, uuid)
	assert.Equal(t, path, got)
}

func TestFindSessionByUUIDPrefersClaudeThenFallsBackToCodex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	uuid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	claudePath := filepath.Join(home, ".claude", "projects", "-tmp-proj", uuid+".jsonl")
	codexPath := filepath.Join(home, ".codex", "sessions", "2026", "03", "13", "session-"+uuid+".jsonl")
	writeClaudeSessionFile(t, claudePath, "/tmp/project", "hello", "msg1", "hi")
	writeCodexSessionFile(t, codexPath, "/tmp/project", uuid, "hello", "hi")

	assert.Equal(t, claudePath, findSessionByUUID(uuid))

	require.NoError(t, os.Remove(claudePath))
	assert.Equal(t, codexPath, findSessionByUUID(uuid))
}

func TestFindSessionsFiltersSortsAndLimits(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	projectDir := filepath.Join(home, "work", "match")
	require.NoError(t, os.MkdirAll(projectDir, 0755))
	chdirForTest(t, projectDir)
	projectFilter := strings.ReplaceAll(projectDir, "/", "-")

	matchingCodexPath := filepath.Join(home, ".codex", "sessions", "2026", "03", "13", "codex-match.jsonl")
	otherCodexPath := filepath.Join(home, ".codex", "sessions", "2026", "03", "13", "codex-other.jsonl")

	writeCodexSessionFile(t, matchingCodexPath, projectDir, "019cd60b-7fe1-7223-a1c0-6f0edbb837fc", "codex prompt", "codex answer")
	writeCodexSessionFile(t, otherCodexPath, filepath.Join(home, "work", "other"), "119cd60b-7fe1-7223-a1c0-6f0edbb837fc", "other prompt", "other answer")

	setTestModTime(t, matchingCodexPath, time.Date(2026, 3, 13, 10, 0, 0, 0, time.UTC))
	setTestModTime(t, otherCodexPath, time.Date(2026, 3, 13, 11, 0, 0, 0, time.UTC))

	sessions := findSessions(0, projectFilter)
	require.Len(t, sessions, 1)
	assert.Equal(t, matchingCodexPath, sessions[0].Path)
	assert.Equal(t, FormatCodex, sessions[0].Format)

	limited := findSessions(1, "")
	require.Len(t, limited, 1)
	assert.Equal(t, otherCodexPath, limited[0].Path)
}
