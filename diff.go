package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	stDiffFile = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("4")) // blue
	stDiffAdd  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))            // green
	stDiffDel  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))            // red
	stDiffCtx  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "245", Dark: "250"})
)

type fileChange struct {
	path   string
	op     string // "Edit" or "Write"
	oldStr string
	newStr string
}

func runDiff(args []string) {
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

	fs := flag.NewFlagSet("diff", flag.ExitOnError)
	lastTurns := fs.Int("last", 0, "show changes from only the last N turns")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: ccmd diff [flags] [session-number-or-uuid-or-path]\n\n")
		fmt.Fprintf(os.Stderr, "Show file changes (Edit/Write) made during a session.\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	path := resolveSessionArg(fs)

	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = f.Close() }()

	ps := parseSessionFile(f, path, "")
	changes := extractChanges(ps.Entries, *lastTurns)

	if len(changes) == 0 {
		fmt.Fprintf(os.Stderr, "No file changes found.\n")
		return
	}

	color := isTerminal()
	for _, ch := range changes {
		printChange(ch, color)
	}
}

// resolveSessionArg resolves a session argument from a FlagSet.
// Shared between files and diff subcommands.
func resolveSessionArg(fs *flag.FlagSet) string {
	if fs.NArg() == 0 {
		// Default to parent session if CCMD_PARENT_UUID is set
		if parentUUID := os.Getenv("CCMD_PARENT_UUID"); parentUUID != "" {
			found := findSessionByUUID(parentUUID)
			if found == "" {
				fmt.Fprintf(os.Stderr, "error: no session found for UUID %s\n", parentUUID)
				os.Exit(1)
			}
			return found
		}
		sessions := findSessions(0, cwdProjectDir())
		if len(sessions) == 0 {
			fmt.Fprintf(os.Stderr, "error: no sessions found\n")
			os.Exit(1)
		}
		return sessions[0].Path
	}

	arg := fs.Arg(0)
	if arg == "parent" {
		parentUUID := os.Getenv("CCMD_PARENT_UUID")
		if parentUUID == "" {
			fmt.Fprintf(os.Stderr, "error: CCMD_PARENT_UUID not set\n")
			os.Exit(1)
		}
		arg = parentUUID
	}
	if n, err := strconv.Atoi(arg); err == nil {
		sessions := findSessions(0, cwdProjectDir())
		if n < 1 || n > len(sessions) {
			fmt.Fprintf(os.Stderr, "error: session %d not found (have %d sessions)\n", n, len(sessions))
			os.Exit(1)
		}
		return sessions[n-1].Path
	}
	if isUUID(arg) {
		found := findSessionByUUID(arg)
		if found == "" {
			fmt.Fprintf(os.Stderr, "error: no session found for UUID %s\n", arg)
			os.Exit(1)
		}
		return found
	}
	return arg
}

func extractChanges(entries []ConversationEntry, lastTurns int) []fileChange {
	fromTurn := 0
	if lastTurns > 0 {
		total := countTurns(entries)
		if lastTurns < total {
			fromTurn = total - lastTurns + 1
		}
	}

	turnNum := 0
	var changes []fileChange
	for _, entry := range entries {
		if entry.Role == "system" {
			continue
		}
		turnNum++
		if fromTurn > 0 && turnNum < fromTurn {
			continue
		}
		if entry.Role != "assistant" {
			continue
		}
		changes = append(changes, changesFromTools(entry.Tools)...)
	}
	return changes
}

func changesFromTools(tools []ToolCall) []fileChange {
	var changes []fileChange
	for _, tc := range tools {
		switch tc.Name {
		case "Edit":
			if tc.Input != "" {
				changes = append(changes, fileChange{
					path:   tc.Input,
					op:     "Edit",
					oldStr: tc.OldStr,
					newStr: tc.NewStr,
				})
			}
		case "Write":
			if tc.Input != "" {
				changes = append(changes, fileChange{
					path:   tc.Input,
					op:     "Write",
					newStr: tc.NewStr,
				})
			}
		case "Agent":
			// Recurse into subagent conversations
			for _, sub := range tc.SubConversation {
				if sub.Role == "assistant" {
					changes = append(changes, changesFromTools(sub.Tools)...)
				}
			}
		}
	}
	return changes
}

func printChange(ch fileChange, color bool) {
	path := shortPath(ch.path)

	// Header
	hdr := fmt.Sprintf("--- %s (%s)", path, ch.op)
	if color {
		fmt.Println(stDiffFile.Render(hdr))
	} else {
		fmt.Println(hdr)
	}

	if ch.op == "Write" {
		lines := strings.Split(ch.newStr, "\n")
		if len(lines) > 40 {
			printLines(lines[:15], "+", color, stDiffAdd)
			omit := fmt.Sprintf("... (%d lines omitted)", len(lines)-30)
			if color {
				fmt.Println(stDiffCtx.Render(omit))
			} else {
				fmt.Println(omit)
			}
			printLines(lines[len(lines)-15:], "+", color, stDiffAdd)
		} else {
			printLines(lines, "+", color, stDiffAdd)
		}
	} else {
		// Edit: show removed then added
		if ch.oldStr != "" {
			for _, line := range strings.Split(ch.oldStr, "\n") {
				s := "-" + line
				if color {
					fmt.Println(stDiffDel.Render(s))
				} else {
					fmt.Println(s)
				}
			}
		}
		if ch.newStr != "" {
			for _, line := range strings.Split(ch.newStr, "\n") {
				s := "+" + line
				if color {
					fmt.Println(stDiffAdd.Render(s))
				} else {
					fmt.Println(s)
				}
			}
		}
	}
	fmt.Println()
}

func printLines(lines []string, prefix string, color bool, style lipgloss.Style) {
	for _, line := range lines {
		s := prefix + line
		if color {
			fmt.Println(style.Render(s))
		} else {
			fmt.Println(s)
		}
	}
}
