package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	stFileRead  = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))                              // blue
	stFileWrite = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))                              // green
	stFilePath  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "245", Dark: "250"})
	stFileAdd   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))                              // green
	stFileRm    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))                              // red
)

type fileInfo struct {
	path    string
	read    bool
	written bool
	added   int // lines added via Edit or Write
	removed int // lines removed via Edit
	readLen int // total lines read
}

func runFiles(args []string) {
	// Reorder so flags come before positional args
	withValue := map[string]bool{"-last": true}
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			if withValue[a] && !strings.Contains(a, "=") && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		} else {
			positional = append(positional, a)
		}
	}
	args = append(flags, positional...)

	fs := flag.NewFlagSet("files", flag.ExitOnError)
	lastN := fs.Int("last", 0, "show only the last N files")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: ccmd files [flags] [session-number-or-uuid-or-path]\n\n")
		fmt.Fprintf(os.Stderr, "List unique files read or written during a session (ordered by last access).\n\n")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	path := resolveSessionArg(fs)

	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	files := extractFiles(parseRecords(f))

	if *lastN > 0 && *lastN < len(files) {
		files = files[len(files)-*lastN:]
	}

	color := isTerminal()
	for _, fi := range files {
		printFileInfo(fi, color)
	}
}

func printFileInfo(fi fileInfo, color bool) {
	var ops []string

	if fi.read {
		s := "R"
		if fi.readLen > 0 {
			s += fmt.Sprintf(" %d lines", fi.readLen)
		}
		if color {
			s = stFileRead.Render(s)
		}
		ops = append(ops, s)
	}
	if fi.written {
		var parts []string
		if fi.added > 0 {
			p := fmt.Sprintf("+%d", fi.added)
			if color {
				p = stFileAdd.Render(p)
			}
			parts = append(parts, p)
		}
		if fi.removed > 0 {
			p := fmt.Sprintf("-%d", fi.removed)
			if color {
				p = stFileRm.Render(p)
			}
			parts = append(parts, p)
		}
		s := "W"
		if color {
			s = stFileWrite.Render(s)
		}
		if len(parts) > 0 {
			s += " " + strings.Join(parts, "/")
		}
		ops = append(ops, s)
	}

	p := shortPath(fi.path)
	if color {
		p = stFilePath.Render(p)
	}

	fmt.Printf("%s  %s\n", p, strings.Join(ops, "  "))
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n") + 1
	if strings.HasSuffix(s, "\n") {
		n--
	}
	return n
}

// extractFiles returns unique file info from Read/Edit/Write tool calls,
// ordered by last access.
func extractFiles(records []Record) []fileInfo {
	// Build tool_use_id -> read line count from user records' toolUseResult
	readLines := make(map[string]int)
	for _, rec := range records {
		if rec.Type != "user" || rec.Message == nil || len(rec.ToolUseResult) == 0 {
			continue
		}
		var result map[string]json.RawMessage
		if json.Unmarshal(rec.ToolUseResult, &result) != nil {
			continue
		}
		fileRaw, ok := result["file"]
		if !ok {
			continue
		}
		var file struct {
			TotalLines int `json:"totalLines"`
		}
		if json.Unmarshal(fileRaw, &file) != nil || file.TotalLines == 0 {
			continue
		}
		_, blocks := parseContent(rec.Message.Content)
		for _, b := range blocks {
			if b.Type == "tool_result" && b.ToolUseID != "" {
				readLines[b.ToolUseID] = file.TotalLines
			}
		}
	}

	type fileOp struct {
		path    string
		toolID  string
		op      string // "R", "E", "W"
		added   int
		removed int
		written int
	}
	var ops []fileOp

	for _, rec := range records {
		if rec.Type != "assistant" || rec.Message == nil {
			continue
		}
		_, blocks := parseContent(rec.Message.Content)
		for _, b := range blocks {
			if b.Type != "tool_use" {
				continue
			}
			var params map[string]interface{}
			if len(b.Input) == 0 || json.Unmarshal(b.Input, &params) != nil {
				continue
			}
			fp := strVal(params, "file_path")
			if fp == "" {
				continue
			}
			switch b.Name {
			case "Read":
				ops = append(ops, fileOp{path: fp, toolID: b.ID, op: "R"})
			case "Edit":
				old := strVal(params, "old_string")
				new := strVal(params, "new_string")
				ops = append(ops, fileOp{
					path:    fp,
					op:      "E",
					added:   countLines(new),
					removed: countLines(old),
				})
			case "Write":
				content := strVal(params, "content")
				ops = append(ops, fileOp{
					path:    fp,
					op:      "W",
					written: countLines(content),
				})
			}
		}
	}

	// Aggregate per file, ordered by last access
	stats := make(map[string]*fileInfo)
	var order []string
	seen := make(map[string]bool)

	// Process in reverse to track last-access order
	for i := len(ops) - 1; i >= 0; i-- {
		op := ops[i]
		if !seen[op.path] {
			seen[op.path] = true
			order = append(order, op.path)
		}
	}
	// Reverse to get chronological last-access order
	for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
		order[i], order[j] = order[j], order[i]
	}

	// Now aggregate stats
	for _, op := range ops {
		fi, ok := stats[op.path]
		if !ok {
			fi = &fileInfo{path: op.path}
			stats[op.path] = fi
		}
		switch op.op {
		case "R":
			fi.read = true
			if n, ok := readLines[op.toolID]; ok {
				fi.readLen = n
			}
		case "E":
			fi.written = true
			fi.added += op.added
			fi.removed += op.removed
		case "W":
			fi.written = true
			fi.added += op.written
		}
	}

	result := make([]fileInfo, 0, len(order))
	for _, path := range order {
		result = append(result, *stats[path])
	}
	return result
}
