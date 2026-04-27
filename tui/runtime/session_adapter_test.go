package tuiruntime

import (
	"context"
	"strings"
	"testing"

	"satis/bridge"
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

func TestSetPlanExecutionCWDUsesPlanDirectory(t *testing.T) {
	adapter := newSessionAdapterForTest(t)
	adapter.SetPlanExecutionCWD("/demo/plan.json")
	if got := adapter.executor.InitialCWD; got != "/demo" {
		t.Fatalf("executor InitialCWD: got %q want /demo", got)
	}
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

// TestClonePlanForIsolatedRunPreservesDescriptions verifies that IntentDescription and
// PlanDescription are copied into the cloned plan. Previously both fields were omitted,
// causing ValidateChunkGraphPlan to reject the clone with "must be non-empty" errors.
func TestClonePlanForIsolatedRunPreservesDescriptions(t *testing.T) {
	src := &bridge.ChunkGraphPlan{
		ProtocolVersion:   1,
		PlanID:            "plan_test",
		IntentID:          "intent_test",
		IntentDescription: "用户的执行意图",
		PlanDescription:   "当前计划说明",
		Goal:              "test goal",
		EntryChunks:       []string{"CHK_A"},
		Chunks: []bridge.PlanChunk{
			{
				ChunkID:     "CHK_A",
				Kind:        "task",
				Description: "test chunk",
				Source: bridge.ChunkSource{
					Format:    "satis_v1",
					SatisText: "chunk_id: CHK_A\nintent_uid: intent_test\ndescription: test chunk\nchunk_port: port_a\n\nPwd\n",
				},
			},
		},
		Edges: []bridge.PlanEdge{},
	}
	cloned, err := clonePlanForIsolatedRun(src, "RUN__")
	if err != nil {
		t.Fatalf("clonePlanForIsolatedRun: %v", err)
	}
	if cloned.IntentDescription != src.IntentDescription {
		t.Errorf("IntentDescription: got %q want %q", cloned.IntentDescription, src.IntentDescription)
	}
	if cloned.PlanDescription != src.PlanDescription {
		t.Errorf("PlanDescription: got %q want %q", cloned.PlanDescription, src.PlanDescription)
	}
	result := bridge.ValidateChunkGraphPlan(cloned)
	if !result.Accepted {
		var msgs []string
		for _, iss := range result.ValidationErrors {
			msgs = append(msgs, iss.Message)
		}
		t.Errorf("ValidateChunkGraphPlan rejected cloned plan: %s", strings.Join(msgs, "; "))
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
