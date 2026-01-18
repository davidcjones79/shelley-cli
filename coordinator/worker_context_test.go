package coordinator

import (
	"strings"
	"testing"
)

func TestRenderWorkerContext_Basic(t *testing.T) {
	data := WorkerContextData{
		TaskID:      "task-123",
		WorkerID:    "wk-1",
		TaskTimeout: "15m",
	}

	result, err := RenderWorkerContext(data)
	if err != nil {
		t.Fatalf("RenderWorkerContext failed: %v", err)
	}

	if !strings.Contains(result, "task-123") {
		t.Error("expected result to contain task ID")
	}

	if !strings.Contains(result, "wk-1") {
		t.Error("expected result to contain worker ID")
	}

	if !strings.Contains(result, "DONE.md") {
		t.Error("expected result to contain DONE.md instructions")
	}

	if !strings.Contains(result, "15m") {
		t.Error("expected result to contain task timeout")
	}
}

func TestRenderWorkerContext_WithGroup(t *testing.T) {
	data := WorkerContextData{
		TaskID:       "task-456",
		WorkerID:     "wk-2",
		GroupName:    "Auth Refactor",
		TasksInGroup: 5,
		TaskTimeout:  "30m",
	}

	result, err := RenderWorkerContext(data)
	if err != nil {
		t.Fatalf("RenderWorkerContext failed: %v", err)
	}

	if !strings.Contains(result, "Auth Refactor") {
		t.Error("expected result to contain group name")
	}

	if !strings.Contains(result, "5 tasks") {
		t.Error("expected result to contain tasks in group count")
	}
}

func TestRenderWorkerContext_WithGit(t *testing.T) {
	data := WorkerContextData{
		TaskID:      "task-789",
		WorkerID:    "wk-3",
		RepoURL:     "https://github.com/user/repo.git",
		BaseBranch:  "main",
		BranchName:  "task-789-feature",
		TaskTimeout: "15m",
	}

	result, err := RenderWorkerContext(data)
	if err != nil {
		t.Fatalf("RenderWorkerContext failed: %v", err)
	}

	if !strings.Contains(result, "github.com/user/repo.git") {
		t.Error("expected result to contain repo URL")
	}

	if !strings.Contains(result, "main") {
		t.Error("expected result to contain base branch")
	}

	if !strings.Contains(result, "task-789-feature") {
		t.Error("expected result to contain branch name")
	}
}

func TestRenderWorkerContext_WithInputDir(t *testing.T) {
	data := WorkerContextData{
		TaskID:      "task-abc",
		WorkerID:    "wk-4",
		InputDir:    "~/shared/source/task-abc",
		TaskTimeout: "15m",
	}

	result, err := RenderWorkerContext(data)
	if err != nil {
		t.Fatalf("RenderWorkerContext failed: %v", err)
	}

	if !strings.Contains(result, "Input files have been staged") {
		t.Error("expected result to mention staged input files")
	}
}

func TestRenderWorkerContext_WithFileOwnership(t *testing.T) {
	data := WorkerContextData{
		TaskID:         "task-def",
		WorkerID:       "wk-5",
		OwnsFiles:      []string{"auth/*.go", "middleware/auth.go"},
		ForbiddenFiles: []string{"config/*", "main.go"},
		TaskTimeout:    "15m",
	}

	result, err := RenderWorkerContext(data)
	if err != nil {
		t.Fatalf("RenderWorkerContext failed: %v", err)
	}

	if !strings.Contains(result, "auth/*.go") {
		t.Error("expected result to contain owned files")
	}

	if !strings.Contains(result, "config/*") {
		t.Error("expected result to contain forbidden files")
	}

	if !strings.Contains(result, "YOU OWN") {
		t.Error("expected result to contain ownership header")
	}

	if !strings.Contains(result, "DO NOT TOUCH") {
		t.Error("expected result to contain forbidden header")
	}
}
