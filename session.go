package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

func claudeProjectsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	return filepath.Join(home, ".claude", "projects")
}

func codexSessionsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	return filepath.Join(home, ".codex", "sessions")
}

// cwdProjectDir returns the project directory name for the current working directory.
// e.g., /Users/ramon/src/ccmd -> -Users-ramon-src-ccmd
func cwdProjectDir() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return strings.ReplaceAll(cwd, "/", "-")
}

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func isUUID(s string) bool {
	return uuidRe.MatchString(s)
}

func findSessionByUUID(uuid string) string {
	// Search Claude Code projects
	projectsDir := claudeProjectsDir()
	filename := uuid + ".jsonl"
	projectDirs, err := os.ReadDir(projectsDir)
	if err == nil {
		for _, pd := range projectDirs {
			if !pd.IsDir() {
				continue
			}
			candidate := filepath.Join(projectsDir, pd.Name(), filename)
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
	}

	// Search Codex sessions (files contain UUID in the name)
	codexDir := codexSessionsDir()
	if found := findCodexSessionByUUID(codexDir, uuid); found != "" {
		return found
	}
	return ""
}

// findCodexSessionByUUID walks the Codex sessions directory for a file containing the UUID.
func findCodexSessionByUUID(root, uuid string) string {
	var result string
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".jsonl") && strings.Contains(filepath.Base(path), uuid) {
			result = path
			return filepath.SkipAll
		}
		return nil
	})
	return result
}

func findSessions(limit int, projectFilter string) []SessionInfo {
	type candidate struct {
		path       string
		projectDir string
		modTime    time.Time
		format     SessionFormat
	}
	var candidates []candidate

	// Discover Claude Code sessions
	projectsDir := claudeProjectsDir()
	projectDirs, err := os.ReadDir(projectsDir)
	if err == nil {
		for _, pd := range projectDirs {
			if !pd.IsDir() {
				continue
			}
			if projectFilter != "" && pd.Name() != projectFilter {
				continue
			}
			projPath := filepath.Join(projectsDir, pd.Name())
			entries, err := os.ReadDir(projPath)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
					continue
				}
				fi, err := e.Info()
				if err != nil {
					continue
				}
				candidates = append(candidates, candidate{
					path:       filepath.Join(projPath, e.Name()),
					projectDir: pd.Name(),
					modTime:    fi.ModTime(),
					format:     FormatClaudeCode,
				})
			}
		}
	}

	// Discover Codex sessions
	codexDir := codexSessionsDir()
	filepath.Walk(codexDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		candidates = append(candidates, candidate{
			path:    path,
			modTime: info.ModTime(),
			format:  FormatCodex,
		})
		return nil
	})

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].modTime.After(candidates[j].modTime)
	})

	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}

	results := make([]*SessionInfo, len(candidates))
	g := new(errgroup.Group)
	g.SetLimit(32)
	for i, c := range candidates {
		g.Go(func() error {
			switch c.format {
			case FormatCodex:
				results[i] = scanCodexSessionInfo(c.path, c.modTime)
			default:
				results[i] = scanSessionInfo(c.path, c.projectDir, c.modTime)
			}
			return nil
		})
	}
	g.Wait()

	var sessions []SessionInfo
	for _, info := range results {
		if info != nil {
			sessions = append(sessions, *info)
		}
	}

	// Apply project filter for Codex sessions (by CWD)
	if projectFilter != "" {
		var filtered []SessionInfo
		for _, s := range sessions {
			if s.Format == FormatCodex {
				// Match Codex sessions by CWD-derived project dir
				cwdProjectDir := strings.ReplaceAll(s.CWD, "/", "-")
				if cwdProjectDir == projectFilter {
					filtered = append(filtered, s)
				}
			} else {
				filtered = append(filtered, s)
			}
		}
		sessions = filtered
	}

	return sessions
}

func scanSessionInfo(path, projectDir string, modTime time.Time) *SessionInfo {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	project := extractProjectName(projectDir)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	var preview string
	var cwd string
	found := false
	turns := 0
	lastRole := ""

	for scanner.Scan() {
		line := scanner.Bytes()

		// Fast string checks — avoid JSON parsing for turn counting
		if bytes.Contains(line, []byte(`"isSidechain":true`)) {
			continue
		}

		isUser := bytes.Contains(line, []byte(`"type":"user"`))
		isAsst := bytes.Contains(line, []byte(`"type":"assistant"`))

		if !found && (isUser || isAsst) {
			found = true
		}

		if isUser && lastRole != "user" {
			turns++
			lastRole = "user"
		} else if isAsst && lastRole != "assistant" {
			turns++
			lastRole = "assistant"
		}

		// Only JSON-parse to extract preview+cwd from first user message
		if isUser && preview == "" {
			var rec Record
			if json.Unmarshal(line, &rec) == nil && rec.Message != nil {
				if rec.CWD != "" {
					cwd = rec.CWD
				}
				text, _ := parseContent(rec.Message.Content)
				if text != "" {
					preview = text
					if len(preview) > 100 {
						preview = preview[:100] + "..."
					}
				}
			}
		}
	}

	if !found {
		return nil
	}
	return &SessionInfo{
		Path:      path,
		Project:   project,
		CWD:       cwd,
		Timestamp: modTime,
		Preview:   preview,
		Turns:     turns,
	}
}

func extractProjectName(dirName string) string {
	// Directory names like "-Users-ramon-src-claude-code-sandbox"
	// Extract just the last component
	parts := strings.Split(dirName, "-")
	// Find the last meaningful segment (after "src" or similar)
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] == "src" && i+1 < len(parts) {
			return strings.Join(parts[i+1:], "-")
		}
	}
	// Fallback: last 2-3 components
	if len(parts) > 3 {
		return strings.Join(parts[len(parts)-3:], "-")
	}
	return dirName
}
