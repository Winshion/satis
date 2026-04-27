package tuiruntime

import (
	"context"
	"strings"
	"testing"

	"satis/satis"
	"satis/vfs"
)

func newSessionAdapterForTest(t *testing.T) *SessionAdapter {
	t.Helper()
	svc := vfs.NewMemoryService()
	executor := &satis.Executor{VFS: svc, InitialCWD: "/"}
	adapter, err := NewSessionAdapter(context.Background(), executor, "TUI_TEST", "")
	if err != nil {
		t.Fatalf("NewSessionAdapter: %v", err)
	}
	return adapter
}

func TestPrepareWorkbenchPlanPathRequiresIntentForNewDirectory(t *testing.T) {
	adapter := newSessionAdapterForTest(t)
	_, _, err := adapter.prepareWorkbenchPlanPath(context.Background(), "/plans/new_one", "")
	if err == nil || !strings.Contains(err.Error(), "requires an intent") {
		t.Fatalf("expected missing intent error, got %v", err)
	}
}

func TestPrepareWorkbenchPlanPathCreatesNewWorkbenchWithIntent(t *testing.T) {
	adapter := newSessionAdapterForTest(t)
	planPath, created, err := adapter.prepareWorkbenchPlanPath(context.Background(), "/plans/new_one", "整理新目录的执行意图")
	if err != nil {
		t.Fatalf("prepareWorkbenchPlanPath: %v", err)
	}
	if !created {
		t.Fatalf("expected new workbench creation")
	}
	text, err := adapter.ReadVirtualText(context.Background(), planPath)
	if err != nil {
		t.Fatalf("ReadVirtualText: %v", err)
	}
	if !strings.Contains(text, "\"intent_description\": \"整理新目录的执行意图\"") {
		t.Fatalf("expected scaffolded intent description, got %s", text)
	}
}

func TestPrepareWorkbenchPlanPathRejectsIntentForExistingDirectory(t *testing.T) {
	adapter := newSessionAdapterForTest(t)
	if err := adapter.session.EnsureVirtualDirectory(context.Background(), "/plans/existing"); err != nil {
		t.Fatalf("EnsureVirtualDirectory: %v", err)
	}
	if err := adapter.session.Commit(context.Background()); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	_, _, err := adapter.prepareWorkbenchPlanPath(context.Background(), "/plans/existing", "不允许")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected existing directory error, got %v", err)
	}
}
