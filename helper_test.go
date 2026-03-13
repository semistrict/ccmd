package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func setTestModTime(t *testing.T, path string, ts time.Time) {
	t.Helper()
	if err := os.Chtimes(path, ts, ts); err != nil {
		t.Fatal(err)
	}
}

func chdirForTest(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Fatal(err)
		}
	})
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = old
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func writeClaudeSessionFile(t *testing.T, path, cwd, userText, assistantID, assistantText string) {
	t.Helper()
	content := `{"type":"user","timestamp":"2025-01-01T00:00:00Z","cwd":"` + cwd + `","message":{"role":"user","content":"` + userText + `"}}
{"type":"assistant","timestamp":"2025-01-01T00:00:01Z","message":{"role":"assistant","id":"` + assistantID + `","content":[{"type":"text","text":"` + assistantText + `"}]}}
`
	writeTestFile(t, path, content)
}

func writeCodexSessionFile(t *testing.T, path, cwd, sessionID, userText, assistantText string) {
	t.Helper()
	content := `{"type":"session_meta","timestamp":"2026-03-10T04:53:41.268Z","payload":{"id":"` + sessionID + `","timestamp":"2026-03-10T04:40:03.812Z","cwd":"` + cwd + `","cli_version":"0.1.0","git":{"branch":"main"}}}
{"type":"event_msg","timestamp":"2026-03-10T04:41:00Z","payload":{"type":"user_message","message":"` + userText + `"}}
{"type":"response_item","timestamp":"2026-03-10T04:41:01Z","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"` + assistantText + `"}],"phase":"final_answer"}}
`
	writeTestFile(t, path, content)
}
