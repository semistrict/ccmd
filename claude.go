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

	// Inject hooks and permissions for this invocation
	hooksJSON := `{"hooks":{"PreCompact":[{"hooks":[{"type":"command","command":"ccmd precompact"}]}],"UserPromptSubmit":[{"hooks":[{"type":"command","command":"ccmd precompact"}]}]}}`
	args = append([]string{"--dangerously-skip-permissions", "--settings", hooksJSON}, args...)

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

	uuid := extractUUID(arg)

	claudePath, err := exec.LookPath("claude")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: claude not found in PATH\n")
		os.Exit(1)
	}

	prompt := fastcompactPrompt(os.Args[0], uuid, arg)

	err = syscall.Exec(claudePath, []string{"claude", "--dangerously-skip-permissions", prompt}, os.Environ())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: exec claude: %v\n", err)
		os.Exit(1)
	}
}

func buildFastcompactArgs(transcriptPath string) []string {
	uuid := extractUUID(transcriptPath)
	prompt := fastcompactPrompt(os.Args[0], uuid, transcriptPath)
	return []string{"--dangerously-skip-permissions", prompt}
}

func extractUUID(transcriptPath string) string {
	base := filepath.Base(transcriptPath)
	return strings.TrimSuffix(base, ".jsonl")
}

func fastcompactPrompt(ccmdBin, uuid, transcriptPath string) string {
	return "This session is being continued from a previous conversation that ran out of context. " +
		"To get the full conversation history, run: " + ccmdBin + " " + uuid + "\n\n" +
		"Read the output to understand what was being worked on, then continue the conversation from where it left off without asking the user any further questions. " +
		"Resume directly — do not acknowledge the summary, do not recap what was happening, do not preface with \"I'll continue\" or similar. " +
		"Pick up the last task as if the break never happened."
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

