package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func parseRecords(r io.Reader) []Record {
	var records []Record
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		var rec Record
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			continue
		}
		records = append(records, rec)
	}
	return records
}

func parseContent(raw json.RawMessage) (string, []ContentBlock) {
	if len(raw) == 0 {
		return "", nil
	}
	// Try string first (user messages)
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	// Try array of content blocks
	var blocks []ContentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		return "", blocks
	}
	return "", nil
}

func buildConversation(records []Record, sessionPath string, isSubagent bool, imagesDir string) []ConversationEntry {
	// Build tool_use_id -> agentId map from progress records
	agentMap := make(map[string]string) // tool_use_id -> agentId
	for _, rec := range records {
		if rec.Type != "progress" || len(rec.Data) == 0 {
			continue
		}
		var data struct {
			Type    string `json:"type"`
			AgentID string `json:"agentId"`
		}
		if json.Unmarshal(rec.Data, &data) == nil && data.Type == "agent_progress" && data.AgentID != "" {
			agentMap[rec.ParentToolUseID] = data.AgentID
		}
	}

	// Collect tool result errors/rejections: tool_use_id -> error message
	toolErrors := make(map[string]string)
	// Collect tool result metadata: tool_use_id -> summary string (e.g. line count)
	toolMeta := make(map[string]string)
	for _, rec := range records {
		if rec.Type != "user" || rec.Message == nil {
			continue
		}
		// Parse toolUseResult for metadata
		if len(rec.ToolUseResult) > 0 {
			var result map[string]json.RawMessage
			if json.Unmarshal(rec.ToolUseResult, &result) == nil {
				// Read: {file: {totalLines: N}}
				if fileRaw, ok := result["file"]; ok {
					var file struct {
						TotalLines int `json:"totalLines"`
					}
					if json.Unmarshal(fileRaw, &file) == nil && file.TotalLines > 0 {
						_, blocks := parseContent(rec.Message.Content)
						for _, b := range blocks {
							if b.Type == "tool_result" && b.ToolUseID != "" {
								toolMeta[b.ToolUseID] = fmt.Sprintf("%d lines", file.TotalLines)
							}
						}
					}
				}
				// Glob: {numFiles: N}
				if numRaw, ok := result["numFiles"]; ok {
					var n int
					if json.Unmarshal(numRaw, &n) == nil {
						_, blocks := parseContent(rec.Message.Content)
						for _, b := range blocks {
							if b.Type == "tool_result" && b.ToolUseID != "" {
								toolMeta[b.ToolUseID] = fmt.Sprintf("%d files", n)
							}
						}
					}
				}
			}
		}
		_, blocks := parseContent(rec.Message.Content)
		for _, b := range blocks {
			if b.Type == "tool_result" && b.IsError && b.ToolUseID != "" {
				msg := extractToolResultText(b)
				if msg != "" {
					toolErrors[b.ToolUseID] = msg
				}
			}
		}
	}

	// First pass: build raw entries, grouping by message ID
	var raw []ConversationEntry
	seenMsgID := make(map[string]int)
	imageNum := 0

	for _, rec := range records {
		if !isSubagent && rec.IsSidechain {
			continue
		}

		switch rec.Type {
		case "system":
			if rec.Subtype == "compact_boundary" {
				ts, _ := time.Parse(time.RFC3339Nano, rec.Timestamp)
				raw = append(raw, ConversationEntry{
					Role:      "system",
					Texts:     []string{"*— context compacted —*"},
					Timestamp: ts,
				})
			}
			continue

		case "pr-link":
			if rec.PRUrl != "" {
				ts, _ := time.Parse(time.RFC3339Nano, rec.Timestamp)
				text := fmt.Sprintf("PR created: [%s#%d](%s)", rec.PRRepository, rec.PRNumber, rec.PRUrl)
				raw = append(raw, ConversationEntry{
					Role:      "system",
					Texts:     []string{text},
					Timestamp: ts,
				})
			}
			continue
		}

		if rec.Message == nil {
			continue
		}

		switch rec.Type {
		case "user":
			text, blocks := parseContent(rec.Message.Content)
			var texts []string
			if text != "" {
				texts = append(texts, text)
			}
			for _, b := range blocks {
				switch b.Type {
				case "text":
					if b.Text != "" {
						texts = append(texts, b.Text)
					}
				case "image":
					if imagesDir != "" && b.Source != nil && b.Source.Data != "" {
						if path := saveImage(imagesDir, b.Source, &imageNum); path != "" {
							texts = append(texts, fmt.Sprintf("![image](%s)", path))
							continue
						}
					}
					texts = append(texts, "[image]")
				}
			}
			if len(texts) > 0 {
				ts, _ := time.Parse(time.RFC3339Nano, rec.Timestamp)
				raw = append(raw, ConversationEntry{
					Role:      "user",
					Texts:     texts,
					Timestamp: ts,
				})
			} else if len(blocks) > 0 {
				continue
			}

		case "assistant":
			if rec.Message.ID == "" {
				continue
			}
			_, blocks := parseContent(rec.Message.Content)

			idx, exists := seenMsgID[rec.Message.ID]
			if !exists {
				ts, _ := time.Parse(time.RFC3339Nano, rec.Timestamp)
				raw = append(raw, ConversationEntry{
					Role:      "assistant",
					Timestamp: ts,
				})
				idx = len(raw) - 1
				seenMsgID[rec.Message.ID] = idx
			}

			entry := &raw[idx]
			for _, b := range blocks {
				switch b.Type {
				case "text":
					if b.Text != "" {
						entry.Texts = append(entry.Texts, b.Text)
					}
				case "thinking":
					if b.Thinking != "" {
						entry.Thinking = append(entry.Thinking, b.Thinking)
					}
				case "tool_use":
					tc := formatToolCall(b)
					if errMsg, ok := toolErrors[b.ID]; ok {
						tc.Error = errMsg
					}
					if meta, ok := toolMeta[b.ID]; ok {
						tc.Meta = meta
					}
					// Resolve subagent conversation
					if b.Name == "Task" || b.Name == "Agent" {
						if agentID, ok := agentMap[b.ID]; ok {
							tc.SubConversation = loadSubagent(sessionPath, agentID)
						}
					}
					entry.Tools = append(entry.Tools, tc)
				}
			}
		}
	}

	// Second pass: merge consecutive same-role entries
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

func loadSubagent(sessionPath, agentID string) []ConversationEntry {
	sessionDir := strings.TrimSuffix(sessionPath, ".jsonl")
	agentPath := filepath.Join(sessionDir, "subagents", "agent-"+agentID+".jsonl")

	f, err := os.Open(agentPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	records := parseRecords(f)
	return buildConversation(records, agentPath, true, "")
}

func formatToolCall(b ContentBlock) ToolCall {
	var params map[string]interface{}
	if len(b.Input) > 0 {
		json.Unmarshal(b.Input, &params)
	}
	if params == nil {
		return ToolCall{Name: b.Name}
	}

	switch b.Name {
	case "Read":
		return ToolCall{Name: "Read", Input: shortPath(strVal(params, "file_path"))}
	case "Edit":
		return ToolCall{
			Name:   "Edit",
			Input:  shortPath(strVal(params, "file_path")),
			OldStr: strVal(params, "old_string"),
			NewStr: strVal(params, "new_string"),
		}
	case "Write":
		return ToolCall{
			Name:   "Write",
			Input:  shortPath(strVal(params, "file_path")),
			NewStr: strVal(params, "content"),
		}
	case "Glob":
		s := strVal(params, "pattern")
		if p := strVal(params, "path"); p != "" {
			s = shortPath(p) + "/" + s
		}
		return ToolCall{Name: "Glob", Input: s}
	case "Grep":
		s := strVal(params, "pattern")
		if p := strVal(params, "path"); p != "" {
			s += " in " + shortPath(p)
		}
		return ToolCall{Name: "Grep", Input: s}
	case "Bash":
		return ToolCall{Name: "Bash", Input: strVal(params, "command")}
	case "ExitPlanMode":
		return ToolCall{Name: "Plan", Plan: strVal(params, "plan")}
	case "Task":
		desc := strVal(params, "description")
		if desc == "" {
			desc = truncate(strVal(params, "prompt"), 120)
		}
		return ToolCall{Name: "Agent", Input: desc}
	case "Agent":
		return ToolCall{Name: "Agent", Input: truncate(strVal(params, "prompt"), 120)}
	default:
		compact, _ := json.Marshal(params)
		return ToolCall{Name: b.Name, Input: truncate(string(compact), 150)}
	}
}

func strVal(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func extractToolResultText(b ContentBlock) string {
	var s string
	if json.Unmarshal(b.Content, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(b.Content, &parts) == nil {
		var texts []string
		for _, p := range parts {
			if p.Text != "" {
				texts = append(texts, p.Text)
			}
		}
		return strings.Join(texts, "\n")
	}
	return ""
}

func saveImage(dir string, src *ImageSource, num *int) string {
	data, err := base64.StdEncoding.DecodeString(src.Data)
	if err != nil {
		return ""
	}

	*num++
	ext := ".png"
	switch src.MediaType {
	case "image/jpeg":
		ext = ".jpg"
	case "image/gif":
		ext = ".gif"
	case "image/webp":
		ext = ".webp"
	}

	os.MkdirAll(dir, 0755)
	path := filepath.Join(dir, fmt.Sprintf("image-%d%s", *num, ext))
	if err := os.WriteFile(path, data, 0644); err != nil {
		return ""
	}
	return path
}
