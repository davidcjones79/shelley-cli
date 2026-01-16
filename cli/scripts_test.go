package cli

import (
	"testing"
)

func TestSanitizeScriptName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello-world", "hello-world"},
		{"Hello World", "hello-world"},
		{"hello_world", "hello-world"},
		{"hello--world", "hello-world"},
		{"-hello-world-", "hello-world"},
		{"hello@world!", "helloworld"},
		{"parallel-reviews", "parallel-reviews"},
		{"my script 123", "my-script-123"},
		{"", ""},
		{"---", ""},
		{"a-very-long-script-name-that-exceeds-the-fifty-character-limit-for-names", "a-very-long-script-name-that-exceeds-the-fifty-cha"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := sanitizeScriptName(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeScriptName(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}
