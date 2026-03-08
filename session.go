package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

func claudeProjectsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	return filepath.Join(home, ".claude", "projects")
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
	projectsDir := claudeProjectsDir()
	filename := uuid + ".jsonl"
	projectDirs, err := os.ReadDir(projectsDir)
	if err != nil {
		return ""
	}
	for _, pd := range projectDirs {
		if !pd.IsDir() {
			continue
		}
		candidate := filepath.Join(projectsDir, pd.Name(), filename)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func findSessions(limit int, projectFilter string) []SessionInfo {
	projectsDir := claudeProjectsDir()

	type candidate struct {
		path       string
		projectDir string
		modTime    time.Time
	}
	var candidates []candidate

	projectDirs, err := os.ReadDir(projectsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading %s: %v\n", projectsDir, err)
		os.Exit(1)
	}

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
			})
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].modTime.After(candidates[j].modTime)
	})

	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}

	var sessions []SessionInfo
	for _, c := range candidates {
		info := scanSessionInfo(c.path, c.projectDir)
		if info != nil {
			sessions = append(sessions, *info)
		}
	}
	return sessions
}

func scanSessionInfo(path, projectDir string) *SessionInfo {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	project := extractProjectName(projectDir)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	var ts time.Time
	var preview string

	for scanner.Scan() {
		var rec Record
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			continue
		}
		if rec.Type == "file-history-snapshot" || rec.Type == "progress" {
			continue
		}
		if rec.Timestamp != "" && ts.IsZero() {
			ts, _ = time.Parse(time.RFC3339Nano, rec.Timestamp)
		}
		if rec.Type == "user" && rec.Message != nil && preview == "" {
			text, _ := parseContent(rec.Message.Content)
			if text != "" {
				preview = text
				if len(preview) > 100 {
					preview = preview[:100] + "..."
				}
				break
			}
		}
	}

	if ts.IsZero() {
		return nil
	}
	return &SessionInfo{
		Path:      path,
		Project:   project,
		Timestamp: ts,
		Preview:   preview,
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
