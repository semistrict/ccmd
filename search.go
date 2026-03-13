package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

type matchedTurn struct {
	Number int
	Entry  ConversationEntry
}

func runSearch(args []string) {
	withValue := map[string]bool{}
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

	fs := flag.NewFlagSet("search", flag.ExitOnError)
	hideThinking := fs.Bool("no-thinking", false, "hide thinking blocks from output")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: ccmd search [flags] <regex> [session-number-or-uuid-or-path]\n\n")
		fmt.Fprintf(os.Stderr, "Print every full turn whose content matches the regex.\n\n")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	if fs.NArg() == 0 {
		fs.Usage()
		os.Exit(2)
	}

	re, err := regexp.Compile(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid regex: %v\n", err)
		os.Exit(1)
	}

	path := resolveSearchSessionArg(fs)
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	ps := parseSessionFile(f, path, "")
	matches := findMatchingTurns(ps.Entries, re)
	if len(matches) == 0 {
		fmt.Fprintln(os.Stderr, "No matching turns found.")
		os.Exit(1)
	}

	renderMatchedTurns(os.Stdout, matches, !*hideThinking)
}

func resolveSearchSessionArg(fs *flag.FlagSet) string {
	if fs.NArg() < 2 {
		empty := flag.NewFlagSet("search-session", flag.ExitOnError)
		return resolveSessionArg(empty)
	}

	arg := fs.Arg(1)
	tmp := flag.NewFlagSet("search-session", flag.ExitOnError)
	if err := tmp.Parse([]string{arg}); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	return resolveSessionArg(tmp)
}

func findMatchingTurns(entries []ConversationEntry, re *regexp.Regexp) []matchedTurn {
	var matches []matchedTurn
	turnNum := 0
	for _, entry := range entries {
		if entry.Role == "system" {
			continue
		}
		turnNum++
		if entryMatches(entry, re) {
			matches = append(matches, matchedTurn{
				Number: turnNum,
				Entry:  entry,
			})
		}
	}
	return matches
}

func entryMatches(entry ConversationEntry, re *regexp.Regexp) bool {
	for _, text := range entry.Texts {
		if re.MatchString(text) {
			return true
		}
	}
	for _, thinking := range entry.Thinking {
		if re.MatchString(thinking) {
			return true
		}
	}
	for _, tool := range entry.Tools {
		if toolMatches(tool, re) {
			return true
		}
	}
	return false
}

func toolMatches(tc ToolCall, re *regexp.Regexp) bool {
	parts := []string{tc.Name, tc.Input, tc.Meta, tc.Plan, tc.Error, tc.OldStr, tc.NewStr}
	for _, part := range parts {
		if part != "" && re.MatchString(part) {
			return true
		}
	}
	for _, sub := range tc.SubConversation {
		if entryMatches(sub, re) {
			return true
		}
	}
	return false
}

func renderMatchedTurns(w io.Writer, matches []matchedTurn, showThinking bool) {
	for i, match := range matches {
		writeSearchEntry(w, match.Entry, match.Number, showThinking, 0)
		if i < len(matches)-1 {
			fmt.Fprintln(w)
		}
	}
}

func writeSearchEntry(w io.Writer, entry ConversationEntry, turnNum int, showThinking bool, depth int) {
	prefix := strings.Repeat("> ", depth)

	switch entry.Role {
	case "user":
		if depth == 0 {
			fmt.Fprintf(w, "## [%d] User\n\n", turnNum)
		} else {
			fmt.Fprintf(w, "%s**Prompt:**\n%s\n", prefix, prefix)
		}
		for _, text := range entry.Texts {
			for _, line := range strings.Split(text, "\n") {
				fmt.Fprintf(w, "%s%s\n", prefix, line)
			}
			fmt.Fprintf(w, "%s\n", prefix)
		}

	case "assistant":
		if depth == 0 {
			fmt.Fprintf(w, "## [%d] Claude\n\n", turnNum)
		}

		if showThinking && len(entry.Thinking) > 0 {
			for _, thinking := range entry.Thinking {
				for _, line := range strings.Split(thinking, "\n") {
					if strings.TrimSpace(line) == "" {
						fmt.Fprintf(w, "%s>\n", prefix)
					} else {
						fmt.Fprintf(w, "%s> *%s*\n", prefix, line)
					}
				}
				fmt.Fprintln(w)
			}
		}

		for _, text := range entry.Texts {
			for _, line := range strings.Split(text, "\n") {
				fmt.Fprintf(w, "%s%s\n", prefix, line)
			}
			fmt.Fprintf(w, "%s\n", prefix)
		}

		if len(entry.Tools) > 0 {
			for _, tc := range entry.Tools {
				writeSearchTool(w, tc, showThinking, depth)
			}
			fmt.Fprintln(w)
		}
	}

	if depth == 0 {
		fmt.Fprintf(w, "---\n")
	}
}

func writeSearchTool(w io.Writer, tc ToolCall, showThinking bool, depth int) {
	prefix := strings.Repeat("> ", depth)

	if tc.Plan != "" {
		fmt.Fprintf(w, "%s### Plan\n%s\n", prefix, prefix)
		for _, line := range strings.Split(tc.Plan, "\n") {
			fmt.Fprintf(w, "%s%s\n", prefix, line)
		}
		fmt.Fprintf(w, "%s\n", prefix)
	} else if tc.OldStr != "" || tc.NewStr != "" {
		fmt.Fprintf(w, "%s> **%s** `%s`\n%s\n", prefix, tc.Name, tc.Input, prefix)
		if tc.OldStr != "" {
			var combined strings.Builder
			for _, line := range strings.Split(tc.OldStr, "\n") {
				combined.WriteString("-" + line + "\n")
			}
			for _, line := range strings.Split(tc.NewStr, "\n") {
				combined.WriteString("+" + line + "\n")
			}
			abbrev := abbreviateLines(strings.TrimRight(combined.String(), "\n"), 5)
			fmt.Fprintf(w, "%s```diff\n", prefix)
			for _, line := range strings.Split(abbrev, "\n") {
				fmt.Fprintf(w, "%s%s\n", prefix, line)
			}
			fmt.Fprintf(w, "%s```\n%s\n", prefix, prefix)
		} else {
			abbrev := abbreviateLines(tc.NewStr, 5)
			fmt.Fprintf(w, "%s```\n", prefix)
			for _, line := range strings.Split(abbrev, "\n") {
				fmt.Fprintf(w, "%s%s\n", prefix, line)
			}
			fmt.Fprintf(w, "%s```\n%s\n", prefix, prefix)
		}
	} else if tc.Input != "" {
		if tc.Meta != "" {
			fmt.Fprintf(w, "%s> **%s** `%s` *(%s)*\n", prefix, tc.Name, tc.Input, tc.Meta)
		} else {
			fmt.Fprintf(w, "%s> **%s** `%s`\n", prefix, tc.Name, tc.Input)
		}
	} else {
		fmt.Fprintf(w, "%s> **%s**\n", prefix, tc.Name)
	}

	if tc.Error != "" {
		fmt.Fprintf(w, "%s>\n", prefix)
		for _, line := range strings.Split(tc.Error, "\n") {
			fmt.Fprintf(w, "%s> **⚠** %s\n", prefix, line)
		}
	}
	if len(tc.SubConversation) > 0 {
		fmt.Fprintf(w, "%s\n", prefix)
		for _, sub := range tc.SubConversation {
			writeSearchEntry(w, sub, 0, showThinking, depth+1)
		}
	}
}
