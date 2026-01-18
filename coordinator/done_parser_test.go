package coordinator

import (
	"testing"
)

func TestParseDoneReport_FullFormat(t *testing.T) {
	content := `---
status: success
files_changed:
  - path: auth/login.go
    action: created
    lines_added: 45
    lines_removed: 0
  - path: auth/login_test.go
    action: created
    lines_added: 80
    lines_removed: 0
tests:
  passed: 5
  failed: 0
  skipped: 1
merge_ready: true
blockers: []
---

## Summary
Added login endpoint with JWT authentication.

## Changes Made
- Created auth/login.go with POST /login handler
- Added JWT token generation
- Created comprehensive test suite

## Testing
Ran go test ./auth/... - all tests pass.
`

	report, err := ParseDoneReport(content)
	if err != nil {
		t.Fatalf("ParseDoneReport failed: %v", err)
	}

	if report.Status != "success" {
		t.Errorf("expected status 'success', got '%s'", report.Status)
	}

	if len(report.FilesChanged) != 2 {
		t.Errorf("expected 2 files changed, got %d", len(report.FilesChanged))
	}

	if report.FilesChanged[0].Path != "auth/login.go" {
		t.Errorf("expected first file 'auth/login.go', got '%s'", report.FilesChanged[0].Path)
	}

	if report.FilesChanged[0].LinesAdded != 45 {
		t.Errorf("expected 45 lines added, got %d", report.FilesChanged[0].LinesAdded)
	}

	if report.Tests.Passed != 5 {
		t.Errorf("expected 5 tests passed, got %d", report.Tests.Passed)
	}

	if report.Tests.Skipped != 1 {
		t.Errorf("expected 1 test skipped, got %d", report.Tests.Skipped)
	}

	if !report.MergeReady {
		t.Error("expected merge_ready to be true")
	}

	if report.Summary == "" {
		t.Error("expected non-empty summary")
	}

	if !report.IsSuccess() {
		t.Error("expected IsSuccess() to return true")
	}

	if !report.TestsPassed() {
		t.Error("expected TestsPassed() to return true")
	}
}

func TestParseDoneReport_PartialWithBlockers(t *testing.T) {
	content := `---
status: partial
files_changed:
  - path: api/handler.go
    action: modified
    lines_added: 20
    lines_removed: 5
tests:
  passed: 3
  failed: 2
  skipped: 0
merge_ready: false
blockers:
  - "Need access to external API key"
  - "Database schema migration required"
---

## Summary
Partially implemented the feature but hit some blockers.
`

	report, err := ParseDoneReport(content)
	if err != nil {
		t.Fatalf("ParseDoneReport failed: %v", err)
	}

	if report.Status != "partial" {
		t.Errorf("expected status 'partial', got '%s'", report.Status)
	}

	if !report.IsPartial() {
		t.Error("expected IsPartial() to return true")
	}

	if report.MergeReady {
		t.Error("expected merge_ready to be false")
	}

	if len(report.Blockers) != 2 {
		t.Errorf("expected 2 blockers, got %d", len(report.Blockers))
	}

	if !report.HasBlockers() {
		t.Error("expected HasBlockers() to return true")
	}

	if report.Tests.Failed != 2 {
		t.Errorf("expected 2 tests failed, got %d", report.Tests.Failed)
	}

	if report.TestsPassed() {
		t.Error("expected TestsPassed() to return false")
	}
}

func TestParseDoneReport_Failed(t *testing.T) {
	content := `---
status: failed
files_changed: []
tests:
  passed: 0
  failed: 0
  skipped: 0
merge_ready: false
blockers:
  - "Could not clone repository - authentication failed"
---

## Summary
Task failed due to repository access issues.
`

	report, err := ParseDoneReport(content)
	if err != nil {
		t.Fatalf("ParseDoneReport failed: %v", err)
	}

	if !report.IsFailed() {
		t.Error("expected IsFailed() to return true")
	}

	if report.TotalFilesChanged() != 0 {
		t.Errorf("expected 0 files changed, got %d", report.TotalFilesChanged())
	}
}

func TestParseDoneReport_NoFrontmatter(t *testing.T) {
	content := `# Task Complete

I finished the task. Here's what I did:

- Fixed the bug
- Added tests
`

	report, err := ParseDoneReport(content)
	if err != nil {
		t.Fatalf("ParseDoneReport failed: %v", err)
	}

	if report.Status != "unknown" {
		t.Errorf("expected status 'unknown', got '%s'", report.Status)
	}

	if report.Summary == "" {
		t.Error("expected non-empty summary extracted from body")
	}
}

func TestParseDoneReport_Empty(t *testing.T) {
	_, err := ParseDoneReport("")
	if err == nil {
		t.Error("expected error for empty content")
	}
}

func TestParseDoneReport_TotalLines(t *testing.T) {
	content := `---
status: success
files_changed:
  - path: a.go
    action: modified
    lines_added: 10
    lines_removed: 5
  - path: b.go
    action: created
    lines_added: 50
    lines_removed: 0
  - path: c.go
    action: deleted
    lines_added: 0
    lines_removed: 100
tests:
  passed: 1
  failed: 0
  skipped: 0
merge_ready: true
blockers: []
---

## Summary
Refactored code.
`

	report, err := ParseDoneReport(content)
	if err != nil {
		t.Fatalf("ParseDoneReport failed: %v", err)
	}

	added, removed := report.TotalLinesChanged()
	if added != 60 {
		t.Errorf("expected 60 lines added, got %d", added)
	}
	if removed != 105 {
		t.Errorf("expected 105 lines removed, got %d", removed)
	}
}

func TestParseDoneReport_ToJSON(t *testing.T) {
	content := `---
status: success
files_changed:
  - path: main.go
    action: modified
    lines_added: 5
    lines_removed: 2
tests:
  passed: 3
  failed: 0
  skipped: 0
merge_ready: true
blockers: []
---

## Summary
Done.
`

	report, err := ParseDoneReport(content)
	if err != nil {
		t.Fatalf("ParseDoneReport failed: %v", err)
	}

	jsonStr, err := report.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON failed: %v", err)
	}

	if jsonStr == "" {
		t.Error("expected non-empty JSON")
	}

	// Should contain key fields
	if !contains(jsonStr, `"status":"success"`) {
		t.Error("JSON should contain status field")
	}
	if !contains(jsonStr, `"merge_ready":true`) {
		t.Error("JSON should contain merge_ready field")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
