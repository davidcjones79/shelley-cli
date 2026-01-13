package cli

import (
	"strings"
	"testing"
)

func TestSanitizeSlug(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello world", "hello-world"},
		{"Hello World", "hello-world"},
		{"hello_world", "hello-world"},
		{"hello--world", "hello-world"},
		{"hello---world", "hello-world"},
		{"-hello-world-", "hello-world"},
		{"hello@world!", "helloworld"},
		{"hello123", "hello123"},
		{"UPPERCASE", "uppercase"},
		{"mix_of-things here", "mix-of-things-here"},
		{"", ""},
		{"---", ""},
		{"a very long slug that exceeds sixty characters in total length should be truncated", "a-very-long-slug-that-exceeds-sixty-characters-in-total-leng"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := sanitizeSlug(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeSlug(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestFormatTokens(t *testing.T) {
	tests := []struct {
		tokens   uint64
		expected string
	}{
		{0, "0"},
		{500, "500"},
		{999, "999"},
		{1000, "1.0k"},
		{1500, "1.5k"},
		{10000, "10.0k"},
		{100000, "100.0k"},
		{999999, "1000.0k"},
		{1000000, "1.0M"},
		{1500000, "1.5M"},
		{200000000, "200.0M"},
	}

	for _, tt := range tests {
		result := formatTokens(tt.tokens)
		if result != tt.expected {
			t.Errorf("formatTokens(%d) = %q, want %q", tt.tokens, result, tt.expected)
		}
	}
}

func TestOsc8Link(t *testing.T) {
	result := osc8Link("file:///tmp/test.txt", "/tmp/test.txt")
	expected := "\033]8;;file:///tmp/test.txt\033\\/tmp/test.txt\033]8;;\033\\"
	if result != expected {
		t.Errorf("osc8Link() = %q, want %q", result, expected)
	}
}

func TestParseNumstat(t *testing.T) {
	tests := []struct {
		input    string
		expected gitStats
	}{
		{"", gitStats{0, 0, 0}},
		{"10\t5\tfile.go", gitStats{1, 10, 5}},
		{"10\t5\tfile1.go\n20\t3\tfile2.go", gitStats{2, 30, 8}},
		{"-\t-\tbinary.png", gitStats{1, 0, 0}},
		{"10\t-\tfile.go", gitStats{1, 10, 0}},
	}

	for _, tt := range tests {
		result := parseNumstat(tt.input)
		if result != tt.expected {
			t.Errorf("parseNumstat(%q) = %+v, want %+v", tt.input, result, tt.expected)
		}
	}
}

func TestColorizeDiff(t *testing.T) {
	styles := DarkStyles()
	diff := `--- a/file.go
+++ b/file.go
@@ -1,3 +1,4 @@
 package main
+import "fmt"
-import "os"
 func main() {}`

	result := colorizeDiff(diff, styles)
	// Just check it doesn't panic and returns something
	if result == "" {
		t.Error("colorizeDiff returned empty string")
	}
	// Check that the output contains the original lines
	if !strings.Contains(result, "package main") {
		t.Error("colorizeDiff should contain original content")
	}
	if !strings.Contains(result, "+import") {
		t.Error("colorizeDiff should contain added lines")
	}
}

func TestStripAnsiCodes(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"\x1b[31mred\x1b[0m", "red"},
		{"\x1b[1;32mbold green\x1b[0m normal", "bold green normal"},
		{"no codes here", "no codes here"},
		{"", ""},
	}

	for _, tt := range tests {
		result := stripAnsiCodes(tt.input)
		if result != tt.expected {
			t.Errorf("stripAnsiCodes(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}
