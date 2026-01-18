package coordinator

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// DoneReport represents the structured output from a worker's DONE.md file.
type DoneReport struct {
	Status       string        `json:"status" yaml:"status"`               // success, partial, failed
	FilesChanged []FileChange  `json:"files_changed" yaml:"files_changed"` // list of changed files
	Tests        TestResults   `json:"tests" yaml:"tests"`                 // test execution results
	MergeReady   bool          `json:"merge_ready" yaml:"merge_ready"`     // true if ready for merge
	Blockers     []string      `json:"blockers" yaml:"blockers"`           // issues preventing completion
	Summary      string        `json:"summary"`                            // parsed from markdown body
	RawMarkdown  string        `json:"raw_markdown,omitempty"`             // full DONE.md content
}

// FileChange represents a single file modification.
type FileChange struct {
	Path         string `json:"path" yaml:"path"`
	Action       string `json:"action" yaml:"action"` // created, modified, deleted
	LinesAdded   int    `json:"lines_added" yaml:"lines_added"`
	LinesRemoved int    `json:"lines_removed" yaml:"lines_removed"`
}

// TestResults represents test execution summary.
type TestResults struct {
	Passed  int `json:"passed" yaml:"passed"`
	Failed  int `json:"failed" yaml:"failed"`
	Skipped int `json:"skipped" yaml:"skipped"`
}

// ParseDoneReport parses a DONE.md file content into a structured DoneReport.
// It expects YAML frontmatter between --- delimiters, followed by markdown.
func ParseDoneReport(content string) (*DoneReport, error) {
	if content == "" {
		return nil, fmt.Errorf("empty content")
	}

	report := &DoneReport{
		RawMarkdown: content,
	}

	// Check for YAML frontmatter
	if !strings.HasPrefix(strings.TrimSpace(content), "---") {
		// No frontmatter, try to extract what we can from markdown
		report.Summary = extractSummary(content)
		report.Status = "unknown"
		return report, nil
	}

	// Parse YAML frontmatter
	scanner := bufio.NewScanner(strings.NewReader(content))
	var yamlLines []string
	var markdownLines []string
	inFrontmatter := false
	frontmatterDone := false

	for scanner.Scan() {
		line := scanner.Text()

		if !inFrontmatter && strings.TrimSpace(line) == "---" {
			inFrontmatter = true
			continue
		}

		if inFrontmatter && strings.TrimSpace(line) == "---" {
			inFrontmatter = false
			frontmatterDone = true
			continue
		}

		if inFrontmatter {
			yamlLines = append(yamlLines, line)
		} else if frontmatterDone {
			markdownLines = append(markdownLines, line)
		}
	}

	// Parse YAML frontmatter
	if len(yamlLines) > 0 {
		yamlContent := strings.Join(yamlLines, "\n")
		if err := yaml.Unmarshal([]byte(yamlContent), report); err != nil {
			return nil, fmt.Errorf("failed to parse YAML frontmatter: %w", err)
		}
	}

	// Extract summary from markdown body
	if len(markdownLines) > 0 {
		report.Summary = extractSummary(strings.Join(markdownLines, "\n"))
	}

	return report, nil
}

// extractSummary extracts the first paragraph or summary section from markdown.
func extractSummary(markdown string) string {
	lines := strings.Split(markdown, "\n")
	var summaryLines []string
	inSummary := false
	foundSummaryHeader := false

	// First pass: look for explicit ## Summary section
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Look for ## Summary header
		if strings.HasPrefix(trimmed, "## Summary") || strings.HasPrefix(trimmed, "# Summary") {
			inSummary = true
			foundSummaryHeader = true
			continue
		}

		// Stop at next header
		if inSummary && strings.HasPrefix(trimmed, "#") {
			break
		}

		if inSummary && trimmed != "" {
			summaryLines = append(summaryLines, trimmed)
		}
	}

	if foundSummaryHeader && len(summaryLines) > 0 {
		return strings.Join(summaryLines, " ")
	}

	// Second pass: take first non-header paragraph
	summaryLines = nil
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip headers
		if strings.HasPrefix(trimmed, "#") {
			if len(summaryLines) > 0 {
				// Already collected some content, stop
				break
			}
			continue
		}

		// Empty line ends the paragraph if we've started collecting
		if trimmed == "" {
			if len(summaryLines) > 0 {
				break
			}
			continue
		}

		summaryLines = append(summaryLines, trimmed)
	}

	return strings.Join(summaryLines, " ")
}

// ToJSON serializes the DoneReport to JSON.
func (r *DoneReport) ToJSON() (string, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// IsSuccess returns true if the task completed successfully.
func (r *DoneReport) IsSuccess() bool {
	return r.Status == "success"
}

// IsPartial returns true if the task partially completed.
func (r *DoneReport) IsPartial() bool {
	return r.Status == "partial"
}

// IsFailed returns true if the task failed.
func (r *DoneReport) IsFailed() bool {
	return r.Status == "failed"
}

// HasBlockers returns true if there are unresolved blockers.
func (r *DoneReport) HasBlockers() bool {
	return len(r.Blockers) > 0
}

// TotalFilesChanged returns the count of changed files.
func (r *DoneReport) TotalFilesChanged() int {
	return len(r.FilesChanged)
}

// TotalLinesChanged returns the sum of added and removed lines.
func (r *DoneReport) TotalLinesChanged() (added, removed int) {
	for _, f := range r.FilesChanged {
		added += f.LinesAdded
		removed += f.LinesRemoved
	}
	return
}

// TestsPassed returns true if all tests passed (none failed).
func (r *DoneReport) TestsPassed() bool {
	return r.Tests.Failed == 0
}
