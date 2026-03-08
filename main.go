package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
)

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "claude":
			runClaude(os.Args[2:])
			return
		case "fastcompact":
			fastcompact(os.Args[2:])
			return
		case "precompact":
			precompact()
			return
		}
	}

	outputFile := flag.String("o", "", "write output to file instead of stdout")
	numSessions := flag.Int("n", 0, "limit number of sessions to list (0 = all)")
	hideThinking := flag.Bool("no-thinking", false, "hide thinking blocks from output")
	summary := flag.Bool("s", false, "summary mode: one line per turn")
	fromTurn := flag.Int("from", 0, "start turn number (inclusive)")
	toTurn := flag.Int("to", 0, "end turn number (inclusive)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: ccmd [flags] [session-file-or-number-or-uuid]\n\n")
		fmt.Fprintf(os.Stderr, "Parse Claude Code JSONL session files into Markdown.\n\n")
		fmt.Fprintf(os.Stderr, "Commands:\n")
		fmt.Fprintf(os.Stderr, "  ccmd claude ...  launch claude with fastcompact support\n")
		fmt.Fprintf(os.Stderr, "  ccmd fastcompact render & restart the most recent session\n\n")
		fmt.Fprintf(os.Stderr, "Session browser:\n")
		fmt.Fprintf(os.Stderr, "  No arguments     interactive session browser (plain list if piped)\n")
		fmt.Fprintf(os.Stderr, "  <number>         render the Nth most recent session\n")
		fmt.Fprintf(os.Stderr, "  <uuid>           render the session with that UUID\n")
		fmt.Fprintf(os.Stderr, "  <path>           render the session at that path\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() == 0 {
		if isTerminal() {
			runTUI(*numSessions, !*hideThinking, *fromTurn, *toTurn)
		} else {
			listSessions(*numSessions, "")
		}
		return
	}

	arg := flag.Arg(0)

	// If the argument is a number, look it up from the session list
	if n, err := strconv.Atoi(arg); err == nil {
		sessions := findSessions(0, "")
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

	renderSession(arg, *outputFile, !*hideThinking, *summary, *fromTurn, *toTurn)
}
