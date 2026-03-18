package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

// abbreviateLines returns first/last N lines with a "... (X lines omitted)" gap if needed.
func abbreviateLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n*2+1 {
		return s
	}
	var b strings.Builder
	for _, l := range lines[:n] {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	_, _ = fmt.Fprintf(&b, "... (%d lines omitted)\n", len(lines)-n*2)
	for _, l := range lines[len(lines)-n:] {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

func writef(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func writeln(w io.Writer) {
	_, _ = fmt.Fprintln(w)
}

func shortPath(p string) string {
	home, _ := os.UserHomeDir()
	if home != "" {
		p = strings.TrimPrefix(p, home+"/")
	}
	if parts := strings.SplitN(p, "/", 4); len(parts) >= 4 && parts[0] == "src" {
		return parts[1] + "/.../" + parts[len(parts)-1]
	}
	return p
}

// countTurns returns the number of non-system entries (matching writeEntries turn numbering).
func countTurns(entries []ConversationEntry) int {
	n := 0
	for _, e := range entries {
		if e.Role != "system" {
			n++
		}
	}
	return n
}

func isTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func renderSession(path, outputFile, imagesDir string, showThinking, summary bool, fromTurn, toTurn, lastTurns int) {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = f.Close() }()

	ps := parseSessionFile(f, path, imagesDir)

	if lastTurns > 0 && fromTurn == 0 {
		total := countTurns(ps.Entries)
		if lastTurns < total {
			fromTurn = total - lastTurns + 1
		}
	}

	if outputFile != "" {
		of, err := os.Create(outputFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		defer func() { _ = of.Close() }()
		writeMarkdown(of, ps, showThinking, summary, fromTurn, toTurn)
		fmt.Fprintf(os.Stderr, "Wrote %s\n", outputFile)
		return
	}

	glowPath, glowErr := exec.LookPath("glow")
	if isTerminal() && glowErr == nil {
		glow := exec.Command(glowPath, "-p", "-")
		glow.Stdout = os.Stdout
		glow.Stderr = os.Stderr

		glowIn, err := glow.StdinPipe()
		if err != nil {
			writeMarkdown(os.Stdout, ps, showThinking, summary, fromTurn, toTurn)
			return
		}

		if err := glow.Start(); err != nil {
			_ = glowIn.Close()
			writeMarkdown(os.Stdout, ps, showThinking, summary, fromTurn, toTurn)
			return
		}
		writeMarkdown(glowIn, ps, showThinking, summary, fromTurn, toTurn)
		_ = glowIn.Close()
		_ = glow.Wait()
		return
	}

	writeMarkdown(os.Stdout, ps, showThinking, summary, fromTurn, toTurn)
}

var _ = renderSessionToString

func renderSessionToString(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()

	ps := parseSessionFile(f, path, "")

	var buf strings.Builder
	writeMarkdown(&buf, ps, false, false, 0, 0)
	return buf.String()
}

func writeMarkdown(w io.Writer, ps ParsedSession, showThinking, summary bool, fromTurn, toTurn int) {
	title := "Claude Code Session"
	versionLabel := "Claude Code"
	if ps.Format == FormatCodex {
		title = "Codex Session"
		versionLabel = "Codex"
	}

	writef(w, "# %s\n\n", title)
	if !ps.StartTime.IsZero() {
		writef(w, "**Date:** %s  \n", ps.StartTime.Format("2006-01-02 15:04"))
	}
	if ps.CWD != "" {
		writef(w, "**Project:** `%s`  \n", ps.CWD)
	}
	if ps.GitBranch != "" {
		writef(w, "**Branch:** `%s`  \n", ps.GitBranch)
	}
	if ps.SessionID != "" {
		writef(w, "**Session:** `%s`  \n", ps.SessionID)
	}
	if ps.Version != "" {
		writef(w, "**%s:** v%s  \n", versionLabel, ps.Version)
	}
	writef(w, "\n---\n\n")

	writeEntries(w, ps.Entries, showThinking, summary, fromTurn, toTurn, 0)
}

func writeEntries(w io.Writer, entries []ConversationEntry, showThinking, summary bool, fromTurn, toTurn, depth int) {
	prefix := strings.Repeat("> ", depth)
	turnNum := 0

	for _, entry := range entries {
		// System entries (compact boundary, PR links) don't count as turns
		if entry.Role == "system" {
			if summary && depth == 0 {
				writef(w, "     %s\n", strings.Join(entry.Texts, " "))
			} else {
				writef(w, "%s%s\n\n", prefix, strings.Join(entry.Texts, " "))
			}
			continue
		}

		turnNum++

		if depth == 0 && fromTurn > 0 && turnNum < fromTurn {
			continue
		}
		if depth == 0 && toTurn > 0 && turnNum > toTurn {
			break
		}

		if summary && depth == 0 {
			switch entry.Role {
			case "user":
				preview := strings.Join(entry.Texts, " ")
				preview = strings.ReplaceAll(preview, "\n", " ")
				writef(w, "%3d  **User:** %s\n", turnNum, truncate(preview, 120))
			case "assistant":
				preview := strings.Join(entry.Texts, " ")
				preview = strings.ReplaceAll(preview, "\n", " ")
				toolCount := len(entry.Tools)
				if preview != "" && toolCount > 0 {
					writef(w, "%3d  **Claude:** %s (%d tools)\n", turnNum, truncate(preview, 100), toolCount)
				} else if preview != "" {
					writef(w, "%3d  **Claude:** %s\n", turnNum, truncate(preview, 120))
				} else if toolCount > 0 {
					writef(w, "%3d  **Claude:** (%d tools)\n", turnNum, toolCount)
				}
			}
			continue
		}

		switch entry.Role {
		case "user":
			if depth == 0 {
				writef(w, "## [%d] User\n\n", turnNum)
			} else {
				writef(w, "%s**Prompt:**\n%s\n", prefix, prefix)
			}
			for _, text := range entry.Texts {
				for _, line := range strings.Split(text, "\n") {
					writef(w, "%s%s\n", prefix, line)
				}
				writef(w, "%s\n", prefix)
			}

		case "assistant":
			if depth == 0 {
				writef(w, "## [%d] Claude\n\n", turnNum)
			}

			if showThinking && len(entry.Thinking) > 0 {
				for _, t := range entry.Thinking {
					for _, line := range strings.Split(t, "\n") {
						if strings.TrimSpace(line) == "" {
							writef(w, "%s>\n", prefix)
						} else {
							writef(w, "%s> *%s*\n", prefix, line)
						}
					}
					writeln(w)
				}
			}

			for _, text := range entry.Texts {
				for _, line := range strings.Split(text, "\n") {
					writef(w, "%s%s\n", prefix, line)
				}
				writef(w, "%s\n", prefix)
			}

			if len(entry.Tools) > 0 {
				for _, tc := range entry.Tools {
					if tc.Plan != "" {
						writef(w, "%s### Plan\n%s\n", prefix, prefix)
						for _, line := range strings.Split(tc.Plan, "\n") {
							writef(w, "%s%s\n", prefix, line)
						}
						writef(w, "%s\n", prefix)
					} else if tc.OldStr != "" || tc.NewStr != "" {
						writef(w, "%s> **%s** `%s`\n%s\n", prefix, tc.Name, tc.Input, prefix)
						if tc.OldStr != "" {
							var combined strings.Builder
							for _, line := range strings.Split(tc.OldStr, "\n") {
								combined.WriteString("-" + line + "\n")
							}
							for _, line := range strings.Split(tc.NewStr, "\n") {
								combined.WriteString("+" + line + "\n")
							}
							abbrev := abbreviateLines(strings.TrimRight(combined.String(), "\n"), 5)
							writef(w, "%s```diff\n", prefix)
							for _, line := range strings.Split(abbrev, "\n") {
								writef(w, "%s%s\n", prefix, line)
							}
							writef(w, "%s```\n%s\n", prefix, prefix)
						} else {
							abbrev := abbreviateLines(tc.NewStr, 5)
							writef(w, "%s```\n", prefix)
							for _, line := range strings.Split(abbrev, "\n") {
								writef(w, "%s%s\n", prefix, line)
							}
							writef(w, "%s```\n%s\n", prefix, prefix)
						}
					} else if tc.Input != "" {
						if tc.Meta != "" {
							writef(w, "%s> **%s** `%s` *(%s)*\n", prefix, tc.Name, tc.Input, tc.Meta)
						} else {
							writef(w, "%s> **%s** `%s`\n", prefix, tc.Name, tc.Input)
						}
					} else {
						writef(w, "%s> **%s**\n", prefix, tc.Name)
					}
					if tc.Error != "" {
						writef(w, "%s>\n", prefix)
						for _, line := range strings.Split(tc.Error, "\n") {
							writef(w, "%s> **⚠** %s\n", prefix, line)
						}
					}
					if len(tc.SubConversation) > 0 {
						writef(w, "%s\n", prefix)
						writeEntries(w, tc.SubConversation, showThinking, false, 0, 0, depth+1)
					}
				}
				writeln(w)
			}
		}
		if depth == 0 {
			writef(w, "---\n\n")
		}
	}
}

func listSessions(n int, projectFilter string) {
	sessions := findSessions(n, projectFilter)
	if len(sessions) == 0 {
		fmt.Println("No sessions found.")
		return
	}

	fmt.Printf(" %3s  %-12s  %5s  %-6s  %-30s  %s\n", "#", "Date", "Turns", "Agent", "Project", "Preview")
	fmt.Printf(" %3s  %-12s  %5s  %-6s  %-30s  %s\n", "---", "------------", "-----", "------", "------------------------------", strings.Repeat("-", 40))
	for i, s := range sessions {
		preview := strings.ReplaceAll(s.Preview, "\n", " ")
		agent := "claude"
		if s.Format == FormatCodex {
			agent = "codex"
		}
		fmt.Printf(" %3d  %-12s  %5d  %-6s  %-30s  %s\n",
			i+1,
			s.Timestamp.Format("2006-01-02"),
			s.Turns,
			agent,
			truncate(s.Project, 30),
			truncate(preview, 80),
		)
	}
	fmt.Printf("\nRender a session: ccmd <number>\n")
}
