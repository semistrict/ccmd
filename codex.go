package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// parseCodexLines parses Codex rollout JSONL into a ParsedSession.
// Handles both old format (bare items) and new format (wrapped in type/payload).
func parseCodexLines(lines [][]byte, sessionPath string) ParsedSession {
	// Detect old vs new format from first line
	var probe struct {
		Type string `json:"type"`
	}
	if len(lines) > 0 {
		_ = json.Unmarshal(lines[0], &probe)
	}
	isNewFormat := probe.Type == "session_meta"

	if isNewFormat {
		return parseCodexNew(lines, sessionPath)
	}
	return parseCodexOld(lines, sessionPath)
}

// --- New Codex format (session_meta / event_msg / response_item / ...) ---

type codexWrapper struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

type codexSessionMeta struct {
	ID         string `json:"id"`
	Timestamp  string `json:"timestamp"`
	CWD        string `json:"cwd"`
	CLIVersion string `json:"cli_version"`
	Git        *struct {
		Branch string `json:"branch"`
	} `json:"git"`
}

type codexEventMsg struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type codexResponseItem struct {
	Type    string              `json:"type"` // message, reasoning, function_call, function_call_output
	Role    string              `json:"role"` // user, assistant, developer
	Content []codexContentBlock `json:"content"`
	Phase   string              `json:"phase"` // commentary, final_answer (on assistant messages)

	// function_call fields
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	CallID    string `json:"call_id"`

	// function_call_output fields
	Output string `json:"output"`
}

type codexContentBlock struct {
	Type string `json:"type"` // input_text, output_text
	Text string `json:"text"`
}

func parseCodexNew(lines [][]byte, sessionPath string) ParsedSession {
	ps := ParsedSession{Format: FormatCodex}

	var raw []ConversationEntry
	var currentAssistant *ConversationEntry
	pendingCalls := make(map[string]*ToolCall) // call_id -> tool call

	flushAssistant := func() {
		if currentAssistant != nil {
			// Attach pending tool call errors/results
			raw = append(raw, *currentAssistant)
			currentAssistant = nil
		}
	}

	for _, line := range lines {
		var w codexWrapper
		if json.Unmarshal(line, &w) != nil {
			continue
		}

		ts, _ := time.Parse(time.RFC3339Nano, w.Timestamp)

		switch w.Type {
		case "session_meta":
			var meta codexSessionMeta
			if json.Unmarshal(w.Payload, &meta) == nil {
				ps.CWD = meta.CWD
				ps.SessionID = meta.ID
				ps.Version = meta.CLIVersion
				if meta.Git != nil {
					ps.GitBranch = meta.Git.Branch
				}
				if t, err := time.Parse(time.RFC3339Nano, meta.Timestamp); err == nil {
					ps.StartTime = t
				}
			}

		case "event_msg":
			var ev codexEventMsg
			if json.Unmarshal(w.Payload, &ev) != nil {
				continue
			}
			switch ev.Type {
			case "user_message":
				flushAssistant()
				raw = append(raw, ConversationEntry{
					Role:      "user",
					Texts:     []string{ev.Message},
					Timestamp: ts,
				})
			case "context_compacted":
				flushAssistant()
				raw = append(raw, ConversationEntry{
					Role:      "system",
					Texts:     []string{"*— context compacted —*"},
					Timestamp: ts,
				})
			}

		case "response_item":
			var item codexResponseItem
			if json.Unmarshal(w.Payload, &item) != nil {
				continue
			}

			switch item.Type {
			case "message":
				if item.Role == "assistant" {
					text := codexExtractText(item.Content)
					if text == "" {
						continue
					}
					if currentAssistant == nil {
						currentAssistant = &ConversationEntry{
							Role:      "assistant",
							Timestamp: ts,
						}
					}
					currentAssistant.Texts = append(currentAssistant.Texts, text)
				}
				// Skip developer/user response_items (system prompts)

			case "reasoning":
				// Codex reasoning is encrypted; we can show the summary if available
				// For now, skip as we don't have access to decrypted content

			case "function_call":
				if currentAssistant == nil {
					currentAssistant = &ConversationEntry{
						Role:      "assistant",
						Timestamp: ts,
					}
				}
				tc := formatCodexToolCall(item.Name, item.Arguments)
				currentAssistant.Tools = append(currentAssistant.Tools, tc)
				pendingCalls[item.CallID] = &currentAssistant.Tools[len(currentAssistant.Tools)-1]

			case "function_call_output":
				if tc, ok := pendingCalls[item.CallID]; ok {
					// Check for errors in output
					if strings.Contains(item.Output, "exit_code\":1") || strings.Contains(item.Output, "\"error\"") {
						tc.Error = truncate(extractCommandOutput(item.Output), 500)
					}
				}
			}
		}
	}
	flushAssistant()

	// Merge consecutive same-role entries
	ps.Entries = mergeEntries(raw)
	return ps
}

// --- Old Codex format (bare items) ---

type codexOldRecord struct {
	// Session header (first line)
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`

	// Common
	Type string `json:"type"` // message, local_shell_call, function_call_output

	// message fields
	Role    string              `json:"role"`
	Content []codexContentBlock `json:"content"`

	// local_shell_call fields
	CallID string `json:"call_id"`
	Action *struct {
		Type    string `json:"type"`
		Command any    `json:"command"` // string or []string
	} `json:"action"`

	// function_call_output fields
	Output string `json:"output"`
}

func parseCodexOld(lines [][]byte, sessionPath string) ParsedSession {
	ps := ParsedSession{Format: FormatCodex}

	var raw []ConversationEntry
	var currentAssistant *ConversationEntry
	pendingCalls := make(map[string]*ToolCall)

	flushAssistant := func() {
		if currentAssistant != nil {
			raw = append(raw, *currentAssistant)
			currentAssistant = nil
		}
	}

	for _, line := range lines {
		var rec codexOldRecord
		if json.Unmarshal(line, &rec) != nil {
			continue
		}

		// Session header line (has id but no type)
		if rec.Type == "" && rec.ID != "" {
			ps.SessionID = rec.ID
			if t, err := time.Parse(time.RFC3339Nano, rec.Timestamp); err == nil {
				ps.StartTime = t
			}
			continue
		}

		switch rec.Type {
		case "message":
			switch rec.Role {
			case "user":
				flushAssistant()
				text := codexExtractText(rec.Content)
				if text != "" {
					raw = append(raw, ConversationEntry{
						Role:  "user",
						Texts: []string{text},
					})
				}
			case "assistant":
				if currentAssistant == nil {
					currentAssistant = &ConversationEntry{Role: "assistant"}
				}
				text := codexExtractText(rec.Content)
				if text != "" {
					currentAssistant.Texts = append(currentAssistant.Texts, text)
				}
			}

		case "local_shell_call":
			if currentAssistant == nil {
				currentAssistant = &ConversationEntry{Role: "assistant"}
			}
			cmd := extractOldShellCommand(rec.Action)
			tc := ToolCall{Name: "Bash", Input: cmd}
			currentAssistant.Tools = append(currentAssistant.Tools, tc)
			pendingCalls[rec.CallID] = &currentAssistant.Tools[len(currentAssistant.Tools)-1]

		case "function_call_output":
			if tc, ok := pendingCalls[rec.CallID]; ok {
				if strings.Contains(rec.Output, "exit_code\":1") || strings.Contains(rec.Output, "\"error\"") {
					tc.Error = truncate(extractCommandOutput(rec.Output), 500)
				}
			}
		}
	}
	flushAssistant()

	ps.Entries = mergeEntries(raw)
	return ps
}

// --- Session scanning ---

// scanCodexSessionInfo quickly scans a Codex session file for listing metadata.
func scanCodexSessionInfo(path string, modTime time.Time) *SessionInfo {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() {
		_ = f.Close()
	}()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	var preview string
	var cwd string
	var project string
	turns := 0
	lastRole := ""
	found := false

	for scanner.Scan() {
		line := scanner.Bytes()

		// Try to extract metadata from session_meta
		if bytes.Contains(line, []byte(`"session_meta"`)) {
			var w struct {
				Payload struct {
					CWD string `json:"cwd"`
				} `json:"payload"`
			}
			if json.Unmarshal(line, &w) == nil && w.Payload.CWD != "" {
				cwd = w.Payload.CWD
				project = extractProjectName(strings.ReplaceAll(cwd, "/", "-"))
			}
			continue
		}

		// Old format: first line has id + timestamp but no type
		if !found && bytes.Contains(line, []byte(`"id"`)) && !bytes.Contains(line, []byte(`"type"`)) {
			continue
		}

		// Count turns from user_message events (new format)
		if bytes.Contains(line, []byte(`"user_message"`)) {
			found = true
			if lastRole != "user" {
				turns++
				lastRole = "user"
			}
			if preview == "" {
				var w struct {
					Payload struct {
						Message string `json:"message"`
					} `json:"payload"`
				}
				if json.Unmarshal(line, &w) == nil && w.Payload.Message != "" {
					preview = w.Payload.Message
					if len(preview) > 100 {
						preview = preview[:100] + "..."
					}
				}
			}
		}

		// Old format: bare message with role user
		if bytes.Contains(line, []byte(`"role":"user"`)) && bytes.Contains(line, []byte(`"input_text"`)) {
			found = true
			if lastRole != "user" {
				turns++
				lastRole = "user"
			}
			if preview == "" {
				var rec struct {
					Content []codexContentBlock `json:"content"`
				}
				if json.Unmarshal(line, &rec) == nil {
					text := codexExtractText(rec.Content)
					if text != "" {
						preview = text
						if len(preview) > 100 {
							preview = preview[:100] + "..."
						}
					}
				}
			}
		}

		// Assistant turns
		isAssistant := bytes.Contains(line, []byte(`"role":"assistant"`))
		if isAssistant {
			found = true
			if lastRole != "assistant" {
				turns++
				lastRole = "assistant"
			}
		}
	}

	if !found {
		return nil
	}

	if project == "" {
		project = "codex"
	}

	return &SessionInfo{
		Path:      path,
		Project:   project,
		CWD:       cwd,
		Timestamp: modTime,
		Preview:   preview,
		Turns:     turns,
		Format:    FormatCodex,
	}
}

// scanCodexTokenUsage scans a Codex session for token usage from event_msg records.
func scanCodexTokenUsage(path string) (inputTokens, outputTokens int) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer func() {
		_ = f.Close()
	}()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.Contains(line, []byte(`"token_count"`)) {
			continue
		}
		var w struct {
			Payload struct {
				Info struct {
					TotalTokenUsage struct {
						InputTokens  int `json:"input_tokens"`
						OutputTokens int `json:"output_tokens"`
					} `json:"total_token_usage"`
				} `json:"info"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &w) == nil {
			u := w.Payload.Info.TotalTokenUsage
			if u.InputTokens > inputTokens {
				inputTokens = u.InputTokens
			}
			if u.OutputTokens > outputTokens {
				outputTokens = u.OutputTokens
			}
		}
	}
	return
}

// --- Helpers ---

func codexExtractText(blocks []codexContentBlock) string {
	var parts []string
	for _, b := range blocks {
		if b.Text != "" && (b.Type == "output_text" || b.Type == "input_text") {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func formatCodexToolCall(name, argsJSON string) ToolCall {
	switch name {
	case "exec_command", "shell_command":
		var args struct {
			Cmd     string `json:"cmd"`
			Workdir string `json:"workdir"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil {
			input := args.Cmd
			if args.Workdir != "" {
				input = args.Cmd
			}
			return ToolCall{Name: "Bash", Input: input}
		}
	case "update_plan":
		var args struct {
			Explanation string `json:"explanation"`
			Plan        []struct {
				Step   string `json:"step"`
				Status string `json:"status"`
			} `json:"plan"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil {
			var steps []string
			for _, p := range args.Plan {
				marker := "[ ]"
				switch p.Status {
				case "completed":
					marker = "[x]"
				case "in_progress":
					marker = "[~]"
				}
				steps = append(steps, marker+" "+p.Step)
			}
			return ToolCall{Name: "Plan", Plan: strings.Join(steps, "\n")}
		}
	case "request_user_input":
		var args struct {
			Prompt string `json:"prompt"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil {
			return ToolCall{Name: "Ask", Input: args.Prompt}
		}
	}
	return ToolCall{Name: name, Input: truncate(argsJSON, 150)}
}

func extractOldShellCommand(action *struct {
	Type    string `json:"type"`
	Command any    `json:"command"`
}) string {
	if action == nil {
		return ""
	}
	switch cmd := action.Command.(type) {
	case string:
		return cmd
	case []any:
		// Usually ["bash", "-lc", "actual command"]
		if len(cmd) >= 3 {
			if s, ok := cmd[2].(string); ok {
				return s
			}
		}
		var parts []string
		for _, c := range cmd {
			if s, ok := c.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, " ")
	}
	return ""
}

// extractCommandOutput extracts the user-visible output from a Codex command result JSON.
func extractCommandOutput(raw string) string {
	var result struct {
		Output string `json:"output"`
	}
	if json.Unmarshal([]byte(raw), &result) == nil && result.Output != "" {
		return result.Output
	}
	// Sometimes the output is the raw string itself (new format)
	if strings.HasPrefix(raw, "Chunk ID:") || strings.HasPrefix(raw, "Wall time:") {
		// New format: plain text output
		return raw
	}
	return raw
}

// mergeEntries merges consecutive same-role entries.
func mergeEntries(raw []ConversationEntry) []ConversationEntry {
	var entries []ConversationEntry
	for _, e := range raw {
		if len(entries) > 0 && entries[len(entries)-1].Role == e.Role {
			last := &entries[len(entries)-1]
			last.Texts = append(last.Texts, e.Texts...)
			last.Thinking = append(last.Thinking, e.Thinking...)
			last.Tools = append(last.Tools, e.Tools...)
		} else {
			entries = append(entries, e)
		}
	}
	return entries
}

func runCodex(args []string) {
	codexPath, err := exec.LookPath("codex")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: codex not found in PATH\n")
		os.Exit(1)
	}

	args = append([]string{"--dangerously-bypass-approvals-and-sandbox"}, args...)

	cmd := exec.Command(codexPath, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
