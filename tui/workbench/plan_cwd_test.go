package workbench

import (
	"context"
	"encoding/json"
	"testing"
)

type planCWDRecordingBackend struct {
	memoryWorkbenchBackend
	lastPlanPath string
}

func (b *planCWDRecordingBackend) SetPlanExecutionCWD(resolvedPlanPath string) {
	b.lastPlanPath = resolvedPlanPath
}

func TestNewWorkbenchSyncsPlanExecutionCWD(t *testing.T) {
	model := testWorkbenchModel()
	data, err := json.Marshal(model.Plan)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	backend := &planCWDRecordingBackend{
		memoryWorkbenchBackend: memoryWorkbenchBackend{
			files: map[string]string{
				"/demo/plan.json": string(data),
			},
		},
	}
	ctx := context.Background()
	app, err := New(ctx, backend, "/demo/plan.json", false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_ = app
	if backend.lastPlanPath != "/demo/plan.json" {
		t.Fatalf("SetPlanExecutionCWD: got %q want /demo/plan.json", backend.lastPlanPath)
	}
}
