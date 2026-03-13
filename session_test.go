package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	if info == nil {
		t.Fatal("scanSessionInfo returned nil")
	}
	if info.Project != "demo" {
		t.Fatalf("Project = %q, want demo", info.Project)
	}
	if info.CWD != "/tmp/project" {
		t.Fatalf("CWD = %q, want /tmp/project", info.CWD)
	}
	if info.Preview != "hello world" {
		t.Fatalf("Preview = %q, want hello world", info.Preview)
	}
	if info.Turns != 2 {
		t.Fatalf("Turns = %d, want 2", info.Turns)
	}
	if !info.Timestamp.Equal(modTime) {
		t.Fatalf("Timestamp = %v, want %v", info.Timestamp, modTime)
	}
}

func TestFindCodexSessionByUUID(t *testing.T) {
	root := t.TempDir()
	uuid := "11111111-2222-3333-4444-555555555555"
	path := filepath.Join(root, "2026", "03", "13", "session-"+uuid+".jsonl")
	writeCodexSessionFile(t, path, "/tmp/project", uuid, "hello", "hi")

	got := findCodexSessionByUUID(root, uuid)
	if got != path {
		t.Fatalf("findCodexSessionByUUID = %q, want %q", got, path)
	}
}

func TestFindSessionByUUIDPrefersClaudeThenFallsBackToCodex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	uuid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	claudePath := filepath.Join(home, ".claude", "projects", "-tmp-proj", uuid+".jsonl")
	codexPath := filepath.Join(home, ".codex", "sessions", "2026", "03", "13", "session-"+uuid+".jsonl")
	writeClaudeSessionFile(t, claudePath, "/tmp/project", "hello", "msg1", "hi")
	writeCodexSessionFile(t, codexPath, "/tmp/project", uuid, "hello", "hi")

	if got := findSessionByUUID(uuid); got != claudePath {
		t.Fatalf("findSessionByUUID Claude preference = %q, want %q", got, claudePath)
	}

	if err := os.Remove(claudePath); err != nil {
		t.Fatal(err)
	}
	if got := findSessionByUUID(uuid); got != codexPath {
		t.Fatalf("findSessionByUUID Codex fallback = %q, want %q", got, codexPath)
	}
}

func TestFindSessionsFiltersSortsAndLimits(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	projectDir := filepath.Join(home, "work", "match")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	chdirForTest(t, projectDir)
	projectFilter := strings.ReplaceAll(projectDir, "/", "-")

	matchingCodexPath := filepath.Join(home, ".codex", "sessions", "2026", "03", "13", "codex-match.jsonl")
	otherCodexPath := filepath.Join(home, ".codex", "sessions", "2026", "03", "13", "codex-other.jsonl")

	writeCodexSessionFile(t, matchingCodexPath, projectDir, "019cd60b-7fe1-7223-a1c0-6f0edbb837fc", "codex prompt", "codex answer")
	writeCodexSessionFile(t, otherCodexPath, filepath.Join(home, "work", "other"), "119cd60b-7fe1-7223-a1c0-6f0edbb837fc", "other prompt", "other answer")

	setTestModTime(t, matchingCodexPath, time.Date(2026, 3, 13, 10, 0, 0, 0, time.UTC))
	setTestModTime(t, otherCodexPath, time.Date(2026, 3, 13, 11, 0, 0, 0, time.UTC))

	sessions := findSessions(0, projectFilter)
	if len(sessions) != 1 {
		t.Fatalf("findSessions filtered len = %d, want 1", len(sessions))
	}
	if sessions[0].Path != matchingCodexPath || sessions[0].Format != FormatCodex {
		t.Fatalf("first session = %+v, want matching codex %q", sessions[0], matchingCodexPath)
	}

	limited := findSessions(1, "")
	if len(limited) != 1 {
		t.Fatalf("limited sessions len = %d, want 1", len(limited))
	}
	if limited[0].Path != otherCodexPath {
		t.Fatalf("limited[0].Path = %q, want newest %q", limited[0].Path, otherCodexPath)
	}
}
