package main

import (
	"strings"
	"testing"
)

func TestTruncate(t *testing.T) {
	tests := []struct {
		input string
		n     int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 8, "hello..."},
		{"abc", 3, "abc"},
		{"abcd", 3, "..."},
		{"", 5, ""},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.n)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.n, got, tt.want)
		}
	}
}

func TestAbbreviateLines(t *testing.T) {
	// Short input: no abbreviation
	short := "line1\nline2\nline3"
	if got := abbreviateLines(short, 5); got != short {
		t.Errorf("abbreviateLines should not modify short input, got %q", got)
	}

	// Long input: should abbreviate
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, "line"+strings.Repeat("x", i))
	}
	long := strings.Join(lines, "\n")
	got := abbreviateLines(long, 3)
	if !strings.Contains(got, "... (14 lines omitted)") {
		t.Errorf("abbreviateLines should contain omission notice, got %q", got)
	}
	if !strings.HasPrefix(got, "line\n") {
		t.Errorf("abbreviateLines should start with first line, got %q", got[:20])
	}
	if !strings.HasSuffix(got, "linexxxxxxxxxxxxxxxxxxx") {
		t.Errorf("abbreviateLines should end with last line")
	}
}

func TestShortPath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/tmp/foo.go", "/tmp/foo.go"},
		{"relative/path.go", "relative/path.go"},
	}
	for _, tt := range tests {
		got := shortPath(tt.input)
		if got != tt.want {
			t.Errorf("shortPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
