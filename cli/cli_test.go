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
