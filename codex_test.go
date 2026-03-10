package main

import (
	"strings"
	"testing"
)

func TestParseCodexNew(t *testing.T) {
	input := `{"type":"session_meta","timestamp":"2026-03-10T04:53:41.268Z","payload":{"id":"019cd60b-7fe1-7223-a1c0-6f0edbb837fc","timestamp":"2026-03-10T04:40:03.812Z","cwd":"/Users/ramon/src/codex/codex-rs","originator":"codex_cli_rs","cli_version":"0.1.0","git":{"branch":"main"}}}
{"type":"event_msg","timestamp":"2026-03-10T04:41:00Z","payload":{"type":"user_message","message":"what is file_based compact ?"}}
{"type":"response_item","timestamp":"2026-03-10T04:41:01Z","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Looking into it."}],"phase":"commentary"}}
{"type":"response_item","timestamp":"2026-03-10T04:41:02Z","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"rg -n compact .\",\"workdir\":\"/Users/ramon/src/codex\"}","call_id":"call_1"}}
{"type":"response_item","timestamp":"2026-03-10T04:41:03Z","payload":{"type":"function_call_output","call_id":"call_1","output":"found stuff"}}
{"type":"response_item","timestamp":"2026-03-10T04:41:04Z","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"It is a config flag."}],"phase":"final_answer"}}
{"type":"event_msg","timestamp":"2026-03-10T04:42:00Z","payload":{"type":"user_message","message":"thanks"}}
{"type":"response_item","timestamp":"2026-03-10T04:42:01Z","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"You're welcome!"}],"phase":"final_answer"}}
`
	ps := parseSessionFile(strings.NewReader(input), "/tmp/test.jsonl", "")

	if ps.Format != FormatCodex {
		t.Errorf("expected FormatCodex, got %d", ps.Format)
	}
	if ps.CWD != "/Users/ramon/src/codex/codex-rs" {
		t.Errorf("CWD = %q, want /Users/ramon/src/codex/codex-rs", ps.CWD)
	}
	if ps.GitBranch != "main" {
		t.Errorf("GitBranch = %q, want main", ps.GitBranch)
	}
	if ps.SessionID != "019cd60b-7fe1-7223-a1c0-6f0edbb837fc" {
		t.Errorf("SessionID = %q", ps.SessionID)
	}
	if ps.Version != "0.1.0" {
		t.Errorf("Version = %q, want 0.1.0", ps.Version)
	}

	if len(ps.Entries) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(ps.Entries))
	}

	// Entry 0: user
	if ps.Entries[0].Role != "user" {
		t.Errorf("entry 0 role = %q, want user", ps.Entries[0].Role)
	}
	if ps.Entries[0].Texts[0] != "what is file_based compact ?" {
		t.Errorf("entry 0 text = %q", ps.Entries[0].Texts[0])
	}

	// Entry 1: assistant with text + tool
	if ps.Entries[1].Role != "assistant" {
		t.Errorf("entry 1 role = %q, want assistant", ps.Entries[1].Role)
	}
	if len(ps.Entries[1].Texts) != 2 {
		t.Errorf("entry 1 texts = %d, want 2", len(ps.Entries[1].Texts))
	}
	if len(ps.Entries[1].Tools) != 1 {
		t.Errorf("entry 1 tools = %d, want 1", len(ps.Entries[1].Tools))
	}
	if ps.Entries[1].Tools[0].Name != "Bash" {
		t.Errorf("entry 1 tool name = %q, want Bash", ps.Entries[1].Tools[0].Name)
	}
	if ps.Entries[1].Tools[0].Input != "rg -n compact ." {
		t.Errorf("entry 1 tool input = %q", ps.Entries[1].Tools[0].Input)
	}

	// Entry 2: user
	if ps.Entries[2].Role != "user" || ps.Entries[2].Texts[0] != "thanks" {
		t.Errorf("entry 2: role=%q text=%q", ps.Entries[2].Role, ps.Entries[2].Texts)
	}

	// Entry 3: assistant
	if ps.Entries[3].Role != "assistant" || ps.Entries[3].Texts[0] != "You're welcome!" {
		t.Errorf("entry 3: role=%q text=%q", ps.Entries[3].Role, ps.Entries[3].Texts)
	}
}

func TestParseCodexOld(t *testing.T) {
	input := `{"id":"0094c5f9-8c02-4bf9-a22d-340f144ee5ee","timestamp":"2025-07-03T18:50:18.595Z"}
{"type":"message","role":"user","content":[{"type":"input_text","text":"can you tell what this repo is"}]}
{"type":"local_shell_call","id":"lsh_1","call_id":"call_1","status":"completed","action":{"type":"exec","command":["bash","-lc","ls -1"]}}
{"type":"function_call_output","call_id":"call_1","output":"{\"output\":\"Dockerfile\\napp\\n\",\"metadata\":{\"exit_code\":0}}"}
{"type":"message","role":"assistant","content":[{"type":"output_text","text":"This is a Docker project."}]}
{"type":"message","role":"user","content":[{"type":"input_text","text":"thanks"}]}
{"type":"message","role":"assistant","content":[{"type":"output_text","text":"You're welcome!"}]}
`
	ps := parseSessionFile(strings.NewReader(input), "/tmp/test.jsonl", "")

	if ps.Format != FormatCodex {
		t.Errorf("expected FormatCodex, got %d", ps.Format)
	}
	if ps.SessionID != "0094c5f9-8c02-4bf9-a22d-340f144ee5ee" {
		t.Errorf("SessionID = %q", ps.SessionID)
	}

	if len(ps.Entries) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(ps.Entries))
	}

	if ps.Entries[0].Role != "user" || ps.Entries[0].Texts[0] != "can you tell what this repo is" {
		t.Errorf("entry 0: role=%q text=%q", ps.Entries[0].Role, ps.Entries[0].Texts)
	}
	if ps.Entries[1].Role != "assistant" {
		t.Errorf("entry 1 role = %q, want assistant", ps.Entries[1].Role)
	}
	if len(ps.Entries[1].Tools) != 1 {
		t.Errorf("entry 1 tools = %d, want 1", len(ps.Entries[1].Tools))
	}
	if ps.Entries[1].Tools[0].Name != "Bash" || ps.Entries[1].Tools[0].Input != "ls -1" {
		t.Errorf("entry 1 tool: name=%q input=%q", ps.Entries[1].Tools[0].Name, ps.Entries[1].Tools[0].Input)
	}
	if ps.Entries[1].Texts[0] != "This is a Docker project." {
		t.Errorf("entry 1 text = %q", ps.Entries[1].Texts[0])
	}
}

func TestDetectFormat(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		want  SessionFormat
	}{
		{
			"claude code user",
			[]string{`{"type":"user","message":{"role":"user","content":"hi"}}`},
			FormatClaudeCode,
		},
		{
			"codex new",
			[]string{`{"type":"session_meta","payload":{"id":"abc"}}`},
			FormatCodex,
		},
		{
			"codex old",
			[]string{`{"id":"abc","timestamp":"2025-01-01T00:00:00Z"}`, `{"type":"message","role":"user","content":[]}`},
			FormatCodex,
		},
	}
	for _, tt := range tests {
		var lines [][]byte
		for _, l := range tt.lines {
			lines = append(lines, []byte(l))
		}
		got := detectFormat(lines)
		if got != tt.want {
			t.Errorf("detectFormat(%s) = %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestFormatCodexToolCall(t *testing.T) {
	// exec_command
	tc := formatCodexToolCall("exec_command", `{"cmd":"rg -n foo","workdir":"/tmp"}`)
	if tc.Name != "Bash" || tc.Input != "rg -n foo" {
		t.Errorf("exec_command: name=%q input=%q", tc.Name, tc.Input)
	}

	// update_plan
	tc = formatCodexToolCall("update_plan", `{"explanation":"doing stuff","plan":[{"step":"Step 1","status":"completed"},{"step":"Step 2","status":"in_progress"}]}`)
	if tc.Name != "Plan" {
		t.Errorf("update_plan: name=%q", tc.Name)
	}
	if !strings.Contains(tc.Plan, "[x] Step 1") || !strings.Contains(tc.Plan, "[~] Step 2") {
		t.Errorf("update_plan: plan=%q", tc.Plan)
	}
}
