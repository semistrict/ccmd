package main

import (
	"os"
	"testing"
)

func TestExtractChanges(t *testing.T) {
	f, err := os.Open("testdata/session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	ps := parseSessionFile(f, "testdata/session.jsonl", "")
	changes := extractChanges(ps.Entries, 0)

	if len(changes) == 0 {
		t.Fatal("expected changes, got none")
	}

	// First change should be a Write to files.go
	if changes[0].op != "Write" {
		t.Errorf("changes[0].op = %q, want Write", changes[0].op)
	}
	if changes[0].path != "src/ccmd/files.go" {
		t.Errorf("changes[0].path = %q, want src/ccmd/files.go", changes[0].path)
	}

	// Should have Edit operations for claude.go
	var hasClaudeEdit bool
	for _, ch := range changes {
		if ch.op == "Edit" && ch.path == "src/ccmd/claude.go" {
			hasClaudeEdit = true
			break
		}
	}
	if !hasClaudeEdit {
		t.Error("expected Edit for claude.go")
	}
}

func TestExtractChangesLast(t *testing.T) {
	f, err := os.Open("testdata/session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	ps := parseSessionFile(f, "testdata/session.jsonl", "")

	// -last 2 should have no changes (last 2 turns are user+assistant with no edits)
	changes := extractChanges(ps.Entries, 2)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes with -last 2, got %d", len(changes))
	}

	// -last 5 should have some changes
	changes = extractChanges(ps.Entries, 5)
	if len(changes) == 0 {
		t.Error("expected changes with -last 5, got none")
	}

	// All changes should be fewer than all changes
	allChanges := extractChanges(ps.Entries, 0)
	if len(changes) >= len(allChanges) {
		t.Errorf("-last 5 changes (%d) should be fewer than all changes (%d)", len(changes), len(allChanges))
	}
}
