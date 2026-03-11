package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/google/shlex"
)

// restartInfo is sent from the precompact HTTP handler to the main loop.
type restartInfo struct {
	SessionID      string
	TranscriptPath string
}

// startHookServer starts an HTTP server for all hooks.
// Returns the base URL (e.g. "http://127.0.0.1:12345").
func startHookServer(restartCh chan<- restartInfo) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ccmd: failed to start hook server: %v\n", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/pretooluse", handlePreToolUse)
	mux.HandleFunc("/posttooluse", handlePostToolUse)
	mux.HandleFunc("/precompact", handlePrecompact(restartCh))

	go http.Serve(ln, mux)

	addr := ln.Addr().(*net.TCPAddr)
	base := fmt.Sprintf("http://127.0.0.1:%d", addr.Port)
	slog.Info("hook server started", "addr", base)
	return base
}

func handlePreToolUse(w http.ResponseWriter, r *http.Request) {
	input, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("pretooluse: read body", "err", err)
		w.WriteHeader(http.StatusOK)
		return
	}

	slog.Debug("pretooluse", "input", string(input))

	var data struct {
		ToolName  string          `json:"tool_name"`
		ToolInput json.RawMessage `json:"tool_input"`
	}
	if err := json.Unmarshal(input, &data); err != nil {
		slog.Error("pretooluse: unmarshal", "err", err)
		w.WriteHeader(http.StatusOK)
		return
	}

	// For Bash tool calls, check for dangerous commands
	if data.ToolName == "Bash" {
		var bashInput struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(data.ToolInput, &bashInput); err == nil {
			if isDangerous(bashInput.Command) {
				slog.Warn("pretooluse: blocked", "command", bashInput.Command)
				// Empty 200 = no decision = fall through to normal permission prompt
				w.WriteHeader(http.StatusOK)
				return
			}
		}
	}

	slog.Info("pretooluse: allow", "tool", data.ToolName)
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}`)
}

func handlePostToolUse(w http.ResponseWriter, r *http.Request) {
	input, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusOK)
		return
	}

	var data struct {
		ToolName  string `json:"tool_name"`
		ToolInput struct {
			Command string `json:"command"`
		} `json:"tool_input"`
	}
	if err := json.Unmarshal(input, &data); err != nil {
		w.WriteHeader(http.StatusOK)
		return
	}

	tokens, err := shlex.Split(data.ToolInput.Command)
	if err != nil || len(tokens) == 0 {
		w.WriteHeader(http.StatusOK)
		return
	}

	if tokens[0] == "cd" {
		slog.Warn("posttooluse: cd detected", "command", data.ToolInput.Command)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"WARNING: You just used cd to change the working directory. Avoid changing directory from the project root when possible."}}`)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func handlePrecompact(restartCh chan<- restartInfo) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		input, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusOK)
			return
		}

		var data struct {
			TranscriptPath string `json:"transcript_path"`
			SessionID      string `json:"session_id"`
			Prompt         string `json:"prompt"`
		}
		if err := json.Unmarshal(input, &data); err != nil || data.TranscriptPath == "" {
			w.WriteHeader(http.StatusOK)
			return
		}

		if strings.TrimSpace(data.Prompt) != "fastcompact" {
			w.WriteHeader(http.StatusOK)
			return
		}

		slog.Info("precompact: fastcompact triggered", "session", data.SessionID)

		// Signal the main loop to restart
		restartCh <- restartInfo{
			SessionID:      data.SessionID,
			TranscriptPath: data.TranscriptPath,
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"decision":"block","reason":"Restarting with fastcompact..."}`)
	}
}

func hooksSettings(hookServerURL string) string {
	return `{"hooks":{` +
		`"UserPromptSubmit":[{"hooks":[{"type":"http","url":"` + hookServerURL + `/precompact"}]}],` +
		`"PreToolUse":[{"hooks":[{"type":"http","url":"` + hookServerURL + `/pretooluse"}]}],` +
		`"PostToolUse":[{"matcher":"Bash","hooks":[{"type":"http","url":"` + hookServerURL + `/posttooluse"}]}]` +
		`}}`
}

func runClaude(args []string) {
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: claude not found in PATH\n")
		os.Exit(1)
	}

	initDebugLogOnce()

	// Start HTTP hook server
	restartCh := make(chan restartInfo, 1)
	hookServerURL := startHookServer(restartCh)
	settingsJSON := hooksSettings(hookServerURL)

	// Inject hooks for this invocation
	args = append([]string{"--settings", settingsJSON}, args...)
	slog.Info("runClaude", "args", args)

	var parentUUID string
	for {
		cmd := exec.Command(claudePath, args...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if parentUUID != "" {
			cmd.Env = append(os.Environ(), "CCMD_PARENT_UUID="+parentUUID)
		}

		cmd.Start()

		doneCh := make(chan error, 1)
		go func() { doneCh <- cmd.Wait() }()

		select {
		case err := <-doneCh:
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					os.Exit(exitErr.ExitCode())
				}
				os.Exit(1)
			}
			os.Exit(0)

		case info := <-restartCh:
			cmd.Process.Kill()
			cmd.Wait()

			// Reset terminal
			reset := exec.Command("stty", "sane")
			reset.Stdin = os.Stdin
			reset.Run()
			fmt.Print("\033[?1004l") // disable focus reporting

			transcriptPath := info.TranscriptPath
			// If transcript_path doesn't exist, find it by session ID
			if _, err := os.Stat(transcriptPath); err != nil && info.SessionID != "" {
				if found := findSessionByUUID(info.SessionID); found != "" {
					transcriptPath = found
				}
			}

			showRestartBanner(transcriptPath)
			parentUUID = extractUUID(transcriptPath)
			skills := extractSkills(transcriptPath)
			args = buildFastcompactArgs(parentUUID, skills, settingsJSON)
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

func buildFastcompactArgs(parentUUID string, skills []string, settingsJSON string) []string {
	prompt := fastcompactPrompt(os.Args[0], parentUUID, skills)
	return []string{"--settings", settingsJSON, prompt}
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
