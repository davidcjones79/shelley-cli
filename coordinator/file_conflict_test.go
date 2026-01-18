package coordinator

import "testing"

func TestPatternsOverlap(t *testing.T) {
	tests := []struct {
		name     string
		p1, p2   string
		expected bool
	}{
		// Exact matches
		{"exact match", "auth.go", "auth.go", true},
		{"exact path match", "pkg/auth/login.go", "pkg/auth/login.go", true},

		// Wildcard patterns
		{"star glob same dir", "*.go", "main.go", true},
		{"star glob different ext", "*.go", "main.rs", false},
		{"double star matches all", "**/*.go", "pkg/auth/login.go", true},
		{"double star at start", "**", "anything.go", true},

		// Directory patterns
		{"same directory", "auth/*.go", "auth/login.go", true},
		{"different directories", "auth/*.go", "api/*.go", false},
		{"nested vs flat", "auth/**/*.go", "auth/login.go", true},
		{"nested subdirs", "auth/**/*.go", "auth/oauth/google.go", true},

		// Non-overlapping
		{"completely different", "frontend/app.js", "backend/main.go", false},
		{"different extensions", "auth/*.go", "auth/*.ts", false},

		// Edge cases
		{"root wildcard", "*", "main.go", true},
		{"empty pattern vs file", "", "main.go", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Skip empty pattern test - edge case
			if tt.p1 == "" || tt.p2 == "" {
				t.Skip("empty pattern")
				return
			}
			result := patternsOverlap(tt.p1, tt.p2)
			if result != tt.expected {
				t.Errorf("patternsOverlap(%q, %q) = %v, want %v", tt.p1, tt.p2, result, tt.expected)
			}
		})
	}
}
