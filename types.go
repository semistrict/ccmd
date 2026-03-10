package main

import (
	"encoding/json"
	"time"
)

type Record struct {
	Type            string          `json:"type"`
	Subtype         string          `json:"subtype,omitempty"`
	Message         *MessageData    `json:"message,omitempty"`
	Timestamp       string          `json:"timestamp"`
	SessionID       string          `json:"sessionId"`
	CWD             string          `json:"cwd"`
	GitBranch       string          `json:"gitBranch"`
	IsSidechain     bool            `json:"isSidechain"`
	Version         string          `json:"version"`
	Data            json.RawMessage `json:"data,omitempty"`
	ParentToolUseID string          `json:"parentToolUseID"`
	ToolUseResult   json.RawMessage `json:"toolUseResult,omitempty"`
	Content         string          `json:"content,omitempty"`
	PRUrl           string          `json:"prUrl,omitempty"`
	PRRepository    string          `json:"prRepository,omitempty"`
	PRNumber        int             `json:"prNumber,omitempty"`
}

type MessageData struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	ID      string          `json:"id"`
	Model   string          `json:"model"`
}

type ContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	Source    *ImageSource    `json:"source,omitempty"`
}

type ImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type SessionInfo struct {
	Path      string
	Project   string
	Timestamp time.Time
	Preview   string
	Turns     int
}

// ConversationEntry represents a single turn in the conversation.
type ConversationEntry struct {
	Role      string // "user" or "assistant"
	Texts     []string
	Tools     []ToolCall
	Thinking  []string
	Timestamp time.Time
}

type ToolCall struct {
	Name            string
	Input           string
	Meta            string              // e.g. "69 lines", "12 files"
	Plan            string              // populated for ExitPlanMode
	Error           string              // populated for rejected/failed tool calls
	OldStr          string              // Edit tool: old_string
	NewStr          string              // Edit tool: new_string
	SubConversation []ConversationEntry // populated for Task/subagent calls
}
