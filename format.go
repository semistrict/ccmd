package main

import (
	"bufio"
	"encoding/json"
	"io"
	"time"
)

// SessionFormat identifies which tool produced a session file.
type SessionFormat int

const (
	FormatClaudeCode SessionFormat = iota
	FormatCodex
)

// ParsedSession is the unified result of parsing any session file format.
type ParsedSession struct {
	Format    SessionFormat
	CWD       string
	GitBranch string
	SessionID string
	Version   string
	StartTime time.Time
	Entries   []ConversationEntry
}

// detectFormat examines the first few JSONL lines to determine the session format.
func detectFormat(lines [][]byte) SessionFormat {
	for _, line := range lines {
		var probe struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(line, &probe) != nil {
			continue
		}
		switch probe.Type {
		case "session_meta", "event_msg", "response_item", "turn_context", "compacted":
			return FormatCodex
		case "message", "local_shell_call", "function_call_output":
			// Old Codex format (bare items without wrapper)
			return FormatCodex
		case "user", "assistant", "system", "progress", "pr-link":
			return FormatClaudeCode
		}
		// First line with no type but has "id" => old Codex header
		// Continue to check next line
	}
	return FormatClaudeCode
}

// readAllLines reads all JSONL lines from a reader.
func readAllLines(r io.Reader) [][]byte {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	var lines [][]byte
	for scanner.Scan() {
		line := make([]byte, len(scanner.Bytes()))
		copy(line, scanner.Bytes())
		lines = append(lines, line)
	}
	return lines
}

// parseSessionFile detects the format and parses into a unified ParsedSession.
func parseSessionFile(r io.Reader, sessionPath, imagesDir string) ParsedSession {
	lines := readAllLines(r)
	if len(lines) == 0 {
		return ParsedSession{}
	}

	// Pass first few lines for format detection
	probe := lines
	if len(probe) > 5 {
		probe = probe[:5]
	}
	format := detectFormat(probe)
	switch format {
	case FormatCodex:
		return parseCodexLines(lines, sessionPath)
	default:
		return parseClaudeCodeLines(lines, sessionPath, imagesDir)
	}
}

// parseClaudeCodeLines parses Claude Code JSONL lines into a ParsedSession.
func parseClaudeCodeLines(lines [][]byte, sessionPath, imagesDir string) ParsedSession {
	var records []Record
	for _, line := range lines {
		var rec Record
		if json.Unmarshal(line, &rec) == nil {
			records = append(records, rec)
		}
	}

	entries := buildConversation(records, sessionPath, false, imagesDir)

	ps := ParsedSession{
		Format:  FormatClaudeCode,
		Entries: entries,
	}
	for _, rec := range records {
		if rec.CWD != "" && ps.CWD == "" {
			ps.CWD = rec.CWD
		}
		if rec.GitBranch != "" && ps.GitBranch == "" {
			ps.GitBranch = rec.GitBranch
		}
		if rec.SessionID != "" && ps.SessionID == "" {
			ps.SessionID = rec.SessionID
		}
		if rec.Version != "" && ps.Version == "" {
			ps.Version = rec.Version
		}
		if rec.Timestamp != "" && ps.StartTime.IsZero() {
			ps.StartTime, _ = time.Parse(time.RFC3339Nano, rec.Timestamp)
		}
		if ps.CWD != "" && ps.GitBranch != "" && ps.SessionID != "" {
			break
		}
	}
	return ps
}
