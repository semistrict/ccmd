package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	assert.Equal(t, FormatCodex, ps.Format)
	assert.Equal(t, "/Users/ramon/src/codex/codex-rs", ps.CWD)
	assert.Equal(t, "main", ps.GitBranch)
	assert.Equal(t, "019cd60b-7fe1-7223-a1c0-6f0edbb837fc", ps.SessionID)
	assert.Equal(t, "0.1.0", ps.Version)

	require.Len(t, ps.Entries, 4)

	// Entry 0: user
	assert.Equal(t, "user", ps.Entries[0].Role)
	require.NotEmpty(t, ps.Entries[0].Texts)
	assert.Equal(t, "what is file_based compact ?", ps.Entries[0].Texts[0])

	// Entry 1: assistant with text + tool
	assert.Equal(t, "assistant", ps.Entries[1].Role)
	assert.Len(t, ps.Entries[1].Texts, 2)
	require.Len(t, ps.Entries[1].Tools, 1)
	assert.Equal(t, "Bash", ps.Entries[1].Tools[0].Name)
	assert.Equal(t, "rg -n compact .", ps.Entries[1].Tools[0].Input)

	// Entry 2: user
	assert.Equal(t, "user", ps.Entries[2].Role)
	require.NotEmpty(t, ps.Entries[2].Texts)
	assert.Equal(t, "thanks", ps.Entries[2].Texts[0])

	// Entry 3: assistant
	assert.Equal(t, "assistant", ps.Entries[3].Role)
	require.NotEmpty(t, ps.Entries[3].Texts)
	assert.Equal(t, "You're welcome!", ps.Entries[3].Texts[0])
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

	assert.Equal(t, FormatCodex, ps.Format)
	assert.Equal(t, "0094c5f9-8c02-4bf9-a22d-340f144ee5ee", ps.SessionID)

	require.Len(t, ps.Entries, 4)

	assert.Equal(t, "user", ps.Entries[0].Role)
	require.NotEmpty(t, ps.Entries[0].Texts)
	assert.Equal(t, "can you tell what this repo is", ps.Entries[0].Texts[0])
	assert.Equal(t, "assistant", ps.Entries[1].Role)
	require.Len(t, ps.Entries[1].Tools, 1)
	assert.Equal(t, "Bash", ps.Entries[1].Tools[0].Name)
	assert.Equal(t, "ls -1", ps.Entries[1].Tools[0].Input)
	require.NotEmpty(t, ps.Entries[1].Texts)
	assert.Equal(t, "This is a Docker project.", ps.Entries[1].Texts[0])
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
		assert.Equal(t, tt.want, got, "detectFormat(%s)", tt.name)
	}
}

func TestFormatCodexToolCall(t *testing.T) {
	// exec_command
	tc := formatCodexToolCall("exec_command", `{"cmd":"rg -n foo","workdir":"/tmp"}`)
	assert.Equal(t, "Bash", tc.Name)
	assert.Equal(t, "rg -n foo", tc.Input)

	// update_plan
	tc = formatCodexToolCall("update_plan", `{"explanation":"doing stuff","plan":[{"step":"Step 1","status":"completed"},{"step":"Step 2","status":"in_progress"}]}`)
	assert.Equal(t, "Plan", tc.Name)
	assert.Contains(t, tc.Plan, "[x] Step 1")
	assert.Contains(t, tc.Plan, "[~] Step 2")
}
