package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/google/shlex"
)

func runClaude(args []string) {
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: claude not found in PATH\n")
		os.Exit(1)
	}

	// Inject hooks for this invocation
	hooksJSON := `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"ccmd precompact"}]}],"PreToolUse":[{"hooks":[{"type":"command","command":"ccmd pretooluse"}]}]}}`
	args = append([]string{"--settings", hooksJSON}, args...)
	slog("runClaude: args=%v", args)

	// Set CCMD_PID so hooks can signal us back
	os.Setenv("CCMD_PID", strconv.Itoa(os.Getpid()))
	controlFile := fmt.Sprintf("/tmp/ccmd-%d.path", os.Getpid())
	defer os.Remove(controlFile)

	var parentUUID string
	for {
		cmd := exec.Command(claudePath, args...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if parentUUID != "" {
			cmd.Env = append(os.Environ(), "CCMD_PARENT_UUID="+parentUUID)
		}

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGUSR1)

		cmd.Start()

		doneCh := make(chan error, 1)
		go func() { doneCh <- cmd.Wait() }()

		select {
		case err := <-doneCh:
			signal.Stop(sigCh)
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					os.Exit(exitErr.ExitCode())
				}
				os.Exit(1)
			}
			os.Exit(0)

		case <-sigCh:
			signal.Stop(sigCh)
			cmd.Process.Kill()
			cmd.Wait()

			// Reset terminal
			reset := exec.Command("stty", "sane")
			reset.Stdin = os.Stdin
			reset.Run()
			fmt.Print("\033[?1004l") // disable focus reporting

			// Read event type + session ID + transcript path
			pathBytes, err := os.ReadFile(controlFile)
			os.Remove(controlFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "ccmd: failed to read control file: %v\n", err)
				os.Exit(1)
			}
			parts := strings.SplitN(string(pathBytes), "\n", 2)
			if len(parts) != 2 {
				fmt.Fprintf(os.Stderr, "ccmd: invalid control file\n")
				os.Exit(1)
			}
			sessionID, transcriptPath := parts[0], parts[1]

			// If transcript_path doesn't exist, find it by session ID
			if _, err := os.Stat(transcriptPath); err != nil && sessionID != "" {
				if found := findSessionByUUID(sessionID); found != "" {
					transcriptPath = found
				}
			}

			showRestartBanner(transcriptPath)
			parentUUID = extractUUID(transcriptPath)
			skills := extractSkills(transcriptPath)
			args = buildFastcompactArgs(parentUUID, skills)
		}
	}
}

func fastcompact(args []string) {
	var arg string
	if len(args) == 0 {
		sessions := findSessions(1, cwdProjectDir())
		if len(sessions) == 0 {
			fmt.Fprintf(os.Stderr, "error: no sessions found for current project\n")
			os.Exit(1)
		}
		arg = sessions[0].Path
	} else if n, err := strconv.Atoi(args[0]); err == nil {
		sessions := findSessions(0, "")
		if n < 1 || n > len(sessions) {
			fmt.Fprintf(os.Stderr, "error: session %d not found (have %d sessions)\n", n, len(sessions))
			os.Exit(1)
		}
		arg = sessions[n-1].Path
	} else if isUUID(args[0]) {
		found := findSessionByUUID(args[0])
		if found == "" {
			fmt.Fprintf(os.Stderr, "error: no session found for UUID %s\n", args[0])
			os.Exit(1)
		}
		arg = found
	} else {
		arg = args[0]
	}

	uuid := extractUUID(arg)

	claudePath, err := exec.LookPath("claude")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: claude not found in PATH\n")
		os.Exit(1)
	}

	skills := extractSkills(arg)
	prompt := fastcompactPrompt(os.Args[0], uuid, skills)

	env := append(os.Environ(), "CCMD_PARENT_UUID="+uuid)
	err = syscall.Exec(claudePath, []string{"claude", prompt}, env)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: exec claude: %v\n", err)
		os.Exit(1)
	}
}

func buildFastcompactArgs(parentUUID string, skills []string) []string {
	prompt := fastcompactPrompt(os.Args[0], parentUUID, skills)
	return []string{prompt}
}

func extractUUID(transcriptPath string) string {
	return sessionUUID(transcriptPath)
}

func fastcompactPrompt(ccmdBin, parentUUID string, skills []string) string {
	prompt := "This session is being continued from a previous conversation that ran out of context. " +
		"To get the full conversation history, run: " + ccmdBin + "\n" +
		"Parent session UUID: " + parentUUID + "\n\n" +
		"Useful flags:\n" +
		"  " + ccmdBin + " -s           # one-line summary per turn (good for getting an overview first)\n" +
		"  " + ccmdBin + " -s=N:M       # summary of turns N to M\n" +
		"  " + ccmdBin + " -s=N:        # summary from turn N onwards\n" +
		"  " + ccmdBin + " -last N      # show only the last N turns\n" +
		"  " + ccmdBin + " -no-thinking # hide thinking blocks\n" +
		"  " + ccmdBin + " files          # list all files read/written\n" +
		"  " + ccmdBin + " files -last 20 # list last 20 files\n" +
		"  " + ccmdBin + " diff           # show all file changes (Edit/Write)\n" +
		"  " + ccmdBin + " diff -last 5   # show changes from last 5 turns\n\n" +
		"Read the output to understand what was being worked on, then continue the conversation from where it left off without asking the user any further questions. " +
		"Resume directly — do not acknowledge the summary, do not recap what was happening, do not preface with \"I'll continue\" or similar. " +
		"Pick up the last task as if the break never happened."

	if len(skills) > 0 {
		prompt += "\n\nThe previous session had these skills loaded. Consider reloading them if relevant:\n"
		for _, s := range skills {
			prompt += "  /" + s + "\n"
		}
	}

	return prompt
}

func extractSkills(transcriptPath string) []string {
	f, err := os.Open(transcriptPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	seen := make(map[string]bool)
	var skills []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		// Only look at user messages — assistant messages may contain
		// source code with <command-name> literals that aren't real skills.
		if !strings.Contains(line, `"type":"user"`) && !strings.Contains(line, `"type": "user"`) {
			continue
		}
		for {
			start := strings.Index(line, "<command-name>/")
			if start < 0 {
				break
			}
			rest := line[start+len("<command-name>/"):]
			end := strings.Index(rest, "</command-name>")
			if end < 0 {
				break
			}
			name := rest[:end]
			if name != "" && !strings.ContainsAny(name, " \t\n\\\"{}()") && !seen[name] {
				seen[name] = true
				skills = append(skills, name)
			}
			line = rest[end:]
		}
	}
	return skills
}

func preToolUse() {
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		slog("pretooluse: failed to read stdin: %v", err)
		os.Exit(0)
	}

	slog("pretooluse: input=%s", string(input))

	var data struct {
		ToolName  string          `json:"tool_name"`
		ToolInput json.RawMessage `json:"tool_input"`
	}
	if err := json.Unmarshal(input, &data); err != nil {
		slog("pretooluse: failed to unmarshal: %v", err)
		os.Exit(0)
	}

	// For Bash tool calls, check for dangerous commands
	if data.ToolName == "Bash" {
		var bashInput struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(data.ToolInput, &bashInput); err == nil {
			if isDangerous(bashInput.Command) {
				slog("pretooluse: BLOCKED dangerous command: %s", bashInput.Command)
				os.Exit(0)
			}
		}
	}

	slog("pretooluse: ALLOW tool=%s", data.ToolName)
	// Auto-approve everything else
	fmt.Print(`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}`)
}

// isDangerous checks if a shell command contains dangerous operations
// by tokenizing with shlex so quoted strings (like commit messages) are
// treated as single tokens and don't trigger false positives.
func isDangerous(cmd string) bool {
	tokens, err := shlex.Split(cmd)
	if err != nil {
		// If we can't parse it, let the normal prompt handle it
		return true
	}

	dangerous := [][]string{
		{"rm", "-rf"},
		{"rm", "-Rf"},
		{"git", "push", "--force"},
		{"git", "push", "-f"},
		{"git", "reset", "--hard"},
		{"git", "checkout", "--", "."},
		{"git", "clean", "-f"},
		{"git", "stash"},
	}

	for _, pattern := range dangerous {
		if containsSequence(tokens, pattern) {
			return true
		}
	}
	return false
}

// containsSequence checks if tokens contains pattern as a subsequence
// in order, allowing gaps (e.g. ["git", "push", "-u", "--force"] matches
// ["git", "push", "--force"]).
func containsSequence(tokens, pattern []string) bool {
	pi := 0
	for _, tok := range tokens {
		if tok == pattern[pi] {
			pi++
			if pi == len(pattern) {
				return true
			}
		}
	}
	return false
}

func precompact() {
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ccmd precompact: failed to read stdin: %v\n", err)
		os.Exit(1)
	}

	var data struct {
		TranscriptPath string `json:"transcript_path"`
		SessionID      string `json:"session_id"`
		Prompt         string `json:"prompt"`
	}
	if err := json.Unmarshal(input, &data); err != nil || data.TranscriptPath == "" {
		fmt.Fprintf(os.Stderr, "ccmd precompact: invalid hook input\n")
		os.Exit(1)
	}

	ccmdPidStr := os.Getenv("CCMD_PID")
	if ccmdPidStr == "" {
		os.Exit(0)
	}
	ccmdPid, err := strconv.Atoi(ccmdPidStr)
	if err != nil {
		os.Exit(0)
	}

	if strings.TrimSpace(data.Prompt) != "fastcompact" {
		os.Exit(0)
	}
	fmt.Print(`{"decision":"block","reason":"Restarting with fastcompact..."}`)

	controlFile := fmt.Sprintf("/tmp/ccmd-%d.path", ccmdPid)
	os.WriteFile(controlFile, []byte(data.SessionID+"\n"+data.TranscriptPath), 0644)

	syscall.Kill(ccmdPid, syscall.SIGUSR1)
}

