package cli

import "testing"

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
