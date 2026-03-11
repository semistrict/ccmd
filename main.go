package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// summaryFlag implements flag.Value for -s, acting as a bool flag that also accepts
// an optional range: -s, -s=5, -s=5:10, -s=:10, -s=5:
type summaryFlag struct {
	enabled bool
	from    int
	to      int
}

func (f *summaryFlag) String() string { return "" }

func (f *summaryFlag) Set(s string) error {
	if s == "true" || s == "" {
		f.enabled = true
		return nil
	}
	if s == "false" {
		f.enabled = false
		return nil
	}
	f.enabled = true
	if before, after, ok := strings.Cut(s, ":"); ok {
		if before != "" {
			n, err := strconv.Atoi(before)
			if err != nil {
				return fmt.Errorf("invalid range: %s", s)
			}
			f.from = n
		}
		if after != "" {
			n, err := strconv.Atoi(after)
			if err != nil {
				return fmt.Errorf("invalid range: %s", s)
			}
			f.to = n
		}
	} else {
		n, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("invalid range: %s", s)
		}
		f.from = n
	}
	return nil
}

func (f *summaryFlag) IsBoolFlag() bool { return true }

// reorderArgs moves flags before positional arguments in os.Args[1:],
// so Go's flag package (which stops at the first non-flag) parses them all.
func reorderArgs() {
	// Flags that take a following argument (not using =)
	withValue := map[string]bool{
		"-o": true, "-n": true, "-from": true, "-to": true, "-last": true, "-images": true,
	}
	var flags, positional []string
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			// If this flag takes a separate value and doesn't use =, consume next arg too
			if withValue[a] && !strings.Contains(a, "=") && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		} else {
			positional = append(positional, a)
		}
	}
	copy(os.Args[1:], append(flags, positional...))
}

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "claude":
			runClaude(os.Args[2:])
			return
		case "fastcompact":
			fastcompact(os.Args[2:])
			return
		case "files":
			runFiles(os.Args[2:])
			return
		case "precompact":
			precompact()
			return
		}
	}

	// Reorder os.Args so flags come before positional args.
	// Go's flag package stops parsing at the first non-flag argument,
	// so "ccmd 1 -s -last 20" would fail without this.
	reorderArgs()

	outputFile := flag.String("o", "", "write output to file instead of stdout")
	numSessions := flag.Int("n", 0, "limit number of sessions to list (0 = all)")
	hideThinking := flag.Bool("no-thinking", false, "hide thinking blocks from output")
	var summary summaryFlag
	flag.Var(&summary, "s", "summary mode: -s, -s=FROM:TO, -s=FROM:, -s=:TO, -s=N")
	fromTurn := flag.Int("from", 0, "start turn number (inclusive)")
	toTurn := flag.Int("to", 0, "end turn number (inclusive)")
	lastTurns := flag.Int("last", 0, "show only the last N turns")
	imagesDir := flag.String("images", "", "extract images to this directory")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: ccmd [flags] [session-file-or-number-or-uuid]\n\n")
		fmt.Fprintf(os.Stderr, "Parse Claude Code JSONL session files into Markdown.\n\n")
		fmt.Fprintf(os.Stderr, "Commands:\n")
		fmt.Fprintf(os.Stderr, "  ccmd claude ...  launch claude with fastcompact support\n")
		fmt.Fprintf(os.Stderr, "  ccmd fastcompact render & restart the most recent session\n")
		fmt.Fprintf(os.Stderr, "  ccmd files ...   list files read/written in a session\n\n")
		fmt.Fprintf(os.Stderr, "Session browser:\n")
		fmt.Fprintf(os.Stderr, "  No arguments     interactive session browser (plain list if piped)\n")
		fmt.Fprintf(os.Stderr, "  <number>         render the Nth most recent session\n")
		fmt.Fprintf(os.Stderr, "  <uuid>           render the session with that UUID\n")
		fmt.Fprintf(os.Stderr, "  <path>           render the session at that path\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	// -s=FROM:TO overrides -from/-to
	if summary.from > 0 && *fromTurn == 0 {
		*fromTurn = summary.from
	}
	if summary.to > 0 && *toTurn == 0 {
		*toTurn = summary.to
	}

	if flag.NArg() == 0 {
		if isTerminal() {
			runTUI(*numSessions, !*hideThinking, *fromTurn, *toTurn)
		} else {
			listSessions(*numSessions, "")
		}
		return
	}

	arg := flag.Arg(0)

	// "parent" resolves to the UUID from CCMD_PARENT_UUID env var
	if arg == "parent" {
		parentUUID := os.Getenv("CCMD_PARENT_UUID")
		if parentUUID == "" {
			fmt.Fprintf(os.Stderr, "error: CCMD_PARENT_UUID not set\n")
			os.Exit(1)
		}
		arg = parentUUID
	}

	// If the argument is a number, look it up from the session list
	if n, err := strconv.Atoi(arg); err == nil {
		sessions := findSessions(0, cwdProjectDir())
		if n < 1 || n > len(sessions) {
			fmt.Fprintf(os.Stderr, "error: session %d not found (have %d sessions)\n", n, len(sessions))
			os.Exit(1)
		}
		arg = sessions[n-1].Path
	} else if isUUID(arg) {
		found := findSessionByUUID(arg)
		if found == "" {
			fmt.Fprintf(os.Stderr, "error: no session found for UUID %s\n", arg)
			os.Exit(1)
		}
		arg = found
	}

	renderSession(arg, *outputFile, *imagesDir, !*hideThinking, summary.enabled, *fromTurn, *toTurn, *lastTurns)
}
