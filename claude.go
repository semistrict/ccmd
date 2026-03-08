package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func runClaude(args []string) {
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: claude not found in PATH\n")
		os.Exit(1)
	}

	// Always run with --dangerously-skip-permissions
	args = append([]string{"--dangerously-skip-permissions"}, args...)

	// Set CCMD_PID so hooks can signal us back
	os.Setenv("CCMD_PID", strconv.Itoa(os.Getpid()))
	controlFile := fmt.Sprintf("/tmp/ccmd-%d.path", os.Getpid())
	defer os.Remove(controlFile)

	for {
		cmd := exec.Command(claudePath, args...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

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
			parts := strings.SplitN(string(pathBytes), "\n", 3)
			if len(parts) != 3 {
				fmt.Fprintf(os.Stderr, "ccmd: invalid control file\n")
				os.Exit(1)
			}
			eventType, sessionID, transcriptPath := parts[0], parts[1], parts[2]

			// If transcript_path doesn't exist, find it by session ID
			if _, err := os.Stat(transcriptPath); err != nil && sessionID != "" {
				if found := findSessionByUUID(sessionID); found != "" {
					transcriptPath = found
				}
			}

			// For PreCompact, ask the user first
			if eventType == "PreCompact" {
				if !askFastcompactConfirm() {
					continue
				}
			}

			showRestartBanner(transcriptPath)
			args = buildFastcompactArgs(transcriptPath)
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

	f, err := os.Open(arg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	records := parseRecords(f)
	entries := buildConversation(records, arg, false)

	var buf strings.Builder
	writeMarkdown(&buf, records, entries, false, false, 0, 0)
	md := buf.String()

	const maxSize = 200 * 1024
	if len(md) > maxSize {
		md = "...\n[Earlier portion of session truncated — showing last 200KB]\n...\n\n" + md[len(md)-maxSize:]
	}

	claudePath, err := exec.LookPath("claude")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: claude not found in PATH\n")
		os.Exit(1)
	}

	prompt := md + "\n\nThis session is being continued from a previous conversation that ran out of context. The summary above covers the earlier portion of the conversation.\n\nIf you need specific details from before compaction (like exact code snippets, error messages, or content you generated), read the full transcript at: " + arg + "\nContinue the conversation from where it left off without asking the user any further questions. Resume directly — do not acknowledge the summary, do not recap what was happening, do not preface with \"I'll continue\" or similar. Pick up the last task as if the break never happened."

	err = syscall.Exec(claudePath, []string{"claude", "--dangerously-skip-permissions", prompt}, os.Environ())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: exec claude: %v\n", err)
		os.Exit(1)
	}
}

func buildFastcompactArgs(transcriptPath string) []string {
	md := renderSessionToString(transcriptPath)

	if len(md) < 200 {
		projDir := filepath.Base(filepath.Dir(transcriptPath))
		if projDir != "" {
			sessions := findSessions(1, projDir)
			if len(sessions) > 0 && sessions[0].Path != transcriptPath {
				fallback := renderSessionToString(sessions[0].Path)
				if len(fallback) > len(md) {
					md = fallback
					transcriptPath = sessions[0].Path
				}
			}
		}
	}

	const maxSize = 200 * 1024
	if len(md) > maxSize {
		md = "...\n[Earlier portion of session truncated — showing last 200KB]\n...\n\n" + md[len(md)-maxSize:]
	}

	prompt := md + "\n\nThis session is being continued from a previous conversation that ran out of context. The summary above covers the earlier portion of the conversation.\n\nIf you need specific details from before compaction (like exact code snippets, error messages, or content you generated), read the full transcript at: " + transcriptPath + "\nContinue the conversation from where it left off without asking the user any further questions. Resume directly — do not acknowledge the summary, do not recap what was happening, do not preface with \"I'll continue\" or similar. Pick up the last task as if the break never happened."

	return []string{"--dangerously-skip-permissions", prompt}
}

func precompact() {
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ccmd precompact: failed to read stdin: %v\n", err)
		os.Exit(1)
	}

	var data struct {
		HookEventName  string `json:"hook_event_name"`
		TranscriptPath string `json:"transcript_path"`
		SessionID      string `json:"session_id"`
		CWD            string `json:"cwd"`
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

	if data.HookEventName == "UserPromptSubmit" {
		if strings.TrimSpace(data.Prompt) != "fastcompact" {
			os.Exit(0)
		}
		fmt.Print(`{"decision":"block","reason":"Restarting with fastcompact..."}`)
	}

	controlFile := fmt.Sprintf("/tmp/ccmd-%d.path", ccmdPid)
	os.WriteFile(controlFile, []byte(data.HookEventName+"\n"+data.SessionID+"\n"+data.TranscriptPath), 0644)

	syscall.Kill(ccmdPid, syscall.SIGUSR1)
}

func installHooks() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	settingsPath := filepath.Join(home, ".claude", "settings.json")

	settings := make(map[string]interface{})
	data, err := os.ReadFile(settingsPath)
	if err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			fmt.Fprintf(os.Stderr, "error: %s is not valid JSON: %v\n", settingsPath, err)
			os.Exit(1)
		}
	} else if !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	hooks, _ := settings["hooks"].(map[string]interface{})
	if hooks == nil {
		hooks = make(map[string]interface{})
	}

	ccmdHook := map[string]interface{}{
		"type":    "command",
		"command": "ccmd precompact",
	}
	hookEntry := []interface{}{
		map[string]interface{}{
			"hooks": []interface{}{ccmdHook},
		},
	}

	changed := false
	for _, event := range []string{"PreCompact", "UserPromptSubmit"} {
		if hasHook(hooks[event], "ccmd precompact") {
			fmt.Printf("  %s: already installed\n", event)
			continue
		}
		existing, _ := hooks[event].([]interface{})
		if existing == nil {
			hooks[event] = hookEntry
		} else {
			hooks[event] = append(existing, hookEntry[0])
		}
		fmt.Printf("  %s: installed\n", event)
		changed = true
	}

	if !changed {
		fmt.Println("\nHooks already installed, nothing to do.")
		return
	}

	settings["hooks"] = hooks

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	out = append(out, '\n')

	if err := os.WriteFile(settingsPath, out, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing %s: %v\n", settingsPath, err)
		os.Exit(1)
	}

	fmt.Printf("\nWrote %s\n", settingsPath)
}

func hasHook(eventVal interface{}, command string) bool {
	entries, ok := eventVal.([]interface{})
	if !ok {
		return false
	}
	for _, entry := range entries {
		entryMap, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		hooksList, ok := entryMap["hooks"].([]interface{})
		if !ok {
			continue
		}
		for _, h := range hooksList {
			hMap, ok := h.(map[string]interface{})
			if !ok {
				continue
			}
			if cmd, _ := hMap["command"].(string); cmd == command {
				return true
			}
		}
	}
	return false
}
