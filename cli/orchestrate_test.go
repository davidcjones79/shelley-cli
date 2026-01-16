package cli

import (
	"testing"
)

func TestParsePlanIntoTasks(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{
			name:     "pipe-separated",
			input:    `"Create auth module here" | "Create API routes here" | "Create DB schema here"`,
			expected: 3,
		},
		{
			name:     "numbered list",
			input:    "1. Create auth module\n2. Create API routes\n3. Create database",
			expected: 3,
		},
		{
			name:     "bullet points",
			input:    "- Create auth module\n- Create API routes\n- Create database",
			expected: 3,
		},
		{
			name:     "mixed format",
			input:    "# Plan\n1. First task is here\n2. Second task follows\n* Third with bullet",
			expected: 3,
		},
		{
			name:     "short tasks filtered",
			input:    "1. OK\n2. This is a valid task\n3. X",
			expected: 1,
		},
		{
			name:     "empty input",
			input:    "",
			expected: 0,
		},
		{
			name:     "only headers",
			input:    "# Header\n## Subheader",
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parsePlanIntoTasks(tt.input)
			if len(result) != tt.expected {
				t.Errorf("parsePlanIntoTasks() returned %d tasks, want %d\nInput: %q\nGot: %v",
					len(result), tt.expected, tt.input, result)
			}
		})
	}
}

func TestTruncateString(t *testing.T) {
	tests := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"hello", 10, "hello"},
		{"hello world", 8, "hello..."},
		{"hi", 2, "hi"},
		{"hello", 5, "hello"},
		{"hello", 4, "h..."},
	}

	for _, tt := range tests {
		result := truncateString(tt.input, tt.maxLen)
		if result != tt.expected {
			t.Errorf("truncateString(%q, %d) = %q, want %q",
				tt.input, tt.maxLen, result, tt.expected)
		}
	}
}
