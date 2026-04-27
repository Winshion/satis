package app

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	tuiruntime "satis/tui/runtime"
)

type runtimeStub struct {
	execLineInputs   []string
	execBodyInputs   []string
	commitCalls      int
	resetCalls       int
	execBodyResult   tuiruntime.ExecResult
	execBodyErr      error
	openWorkbenchErr error
	workbenchPath    string
	workbenchIntent  string
}

func (r *runtimeStub) ExecLine(ctx context.Context, line string) (tuiruntime.ExecResult, error) {
	r.execLineInputs = append(r.execLineInputs, line)
	return tuiruntime.ExecResult{}, nil
}

func (r *runtimeStub) ExecBody(ctx context.Context, body string) (tuiruntime.ExecResult, error) {
	r.execBodyInputs = append(r.execBodyInputs, body)
	return r.execBodyResult, r.execBodyErr
}

func (r *runtimeStub) ExecFile(ctx context.Context, path string) (tuiruntime.ExecResult, error) {
	return tuiruntime.ExecResult{}, nil
}

func (r *runtimeStub) ValidatePlan(ctx context.Context, path string) (tuiruntime.ExecResult, error) {
	return tuiruntime.ExecResult{}, nil
}

func (r *runtimeStub) RunPlan(ctx context.Context, path string) (tuiruntime.ExecResult, error) {
	return tuiruntime.ExecResult{}, nil
}

func (r *runtimeStub) PlanStatus(ctx context.Context, runID string) (tuiruntime.ExecResult, error) {
	return tuiruntime.ExecResult{}, nil
}

func (r *runtimeStub) PlanInspect(ctx context.Context, runID string) (tuiruntime.ExecResult, error) {
	return tuiruntime.ExecResult{}, nil
}

func (r *runtimeStub) PlanEvents(ctx context.Context, runID string) (tuiruntime.ExecResult, error) {
	return tuiruntime.ExecResult{}, nil
}

func (r *runtimeStub) PlanContinue(ctx context.Context, runID string, fragmentPath string) (tuiruntime.ExecResult, error) {
	return tuiruntime.ExecResult{}, nil
}

func (r *runtimeStub) PlanContinueLLM(ctx context.Context, runID string, prompt string) (tuiruntime.ExecResult, error) {
	return tuiruntime.ExecResult{}, nil
}

func (r *runtimeStub) PlanFinish(ctx context.Context, runID string) (tuiruntime.ExecResult, error) {
	return tuiruntime.ExecResult{}, nil
}

func (r *runtimeStub) OpenWorkbench(ctx context.Context, path string, intent string) error {
	r.workbenchPath = path
	r.workbenchIntent = intent
	return r.openWorkbenchErr
}

func (r *runtimeStub) CommitSession(ctx context.Context) error {
	r.commitCalls++
	return nil
}

func (r *runtimeStub) ResetSession(ctx context.Context) error {
	r.resetCalls++
	return nil
}

func (r *runtimeStub) Close(ctx context.Context) error {
	return nil
}

func TestHandleInputBatchMarkersExecuteBodyInLineMode(t *testing.T) {
	ctx := context.Background()
	rt := &runtimeStub{
		execBodyResult: tuiruntime.ExecResult{
			Summary: []string{"ok"},
		},
	}
	var out bytes.Buffer
	app := New(rt, &out)

	if _, err := app.HandleInput(ctx, ">>> Pwd\nLs /\nPrint [[[@x]]] <<<"); err != nil {
		t.Fatalf("batch exec: %v", err)
	}

	if app.state.Collecting {
		t.Fatalf("expected collecting=false after <<<")
	}
	if len(rt.execBodyInputs) != 1 {
		t.Fatalf("expected one ExecBody call, got %d", len(rt.execBodyInputs))
	}
	if got, want := rt.execBodyInputs[0], "Pwd\nLs /\nPrint [[[@x]]]"; got != want {
		t.Fatalf("ExecBody body mismatch: got %q want %q", got, want)
	}
	if rt.commitCalls != 0 {
		t.Fatalf("expected no auto-commit during batch, got %d", rt.commitCalls)
	}
}

func TestHandleInputBatchMarkersSingleLineExecuteBody(t *testing.T) {
	ctx := context.Background()
	rt := &runtimeStub{}
	app := New(rt, &bytes.Buffer{})

	if _, err := app.HandleInput(ctx, ">>> Invoke [[[hello]]] <<<"); err != nil {
		t.Fatalf("single-line batch: %v", err)
	}
	if len(rt.execBodyInputs) != 1 || rt.execBodyInputs[0] != "Invoke [[[hello]]]" {
		t.Fatalf("unexpected ExecBody calls: %#v", rt.execBodyInputs)
	}
	if app.state.Collecting {
		t.Fatalf("expected collecting=false after single-line batch")
	}
}

func TestWorkbenchCommandPassesMultiWordIntent(t *testing.T) {
	ctx := context.Background()
	rt := &runtimeStub{}
	app := New(rt, &bytes.Buffer{})
	if _, err := app.HandleInput(ctx, "/workbench /plans/new 整理 输入 数据"); err != nil {
		t.Fatalf("/workbench: %v", err)
	}
	if rt.workbenchPath != "/plans/new" {
		t.Fatalf("unexpected workbench path %q", rt.workbenchPath)
	}
	if rt.workbenchIntent != "整理 输入 数据" {
		t.Fatalf("unexpected workbench intent %q", rt.workbenchIntent)
	}
}

func TestHandleInputBatchMarkersAcceptMultilineInlineMarkers(t *testing.T) {
	ctx := context.Background()
	rt := &runtimeStub{}
	app := New(rt, &bytes.Buffer{})

	if _, err := app.HandleInput(ctx, ">>> Pwd\nLs /<<<"); err != nil {
		t.Fatalf("multiline inline batch: %v", err)
	}
	if len(rt.execBodyInputs) != 1 || rt.execBodyInputs[0] != "Pwd\nLs /" {
		t.Fatalf("unexpected ExecBody calls: %#v", rt.execBodyInputs)
	}
}

func TestHandleInputBatchMarkersAcceptStandaloneMarkersWithEmbeddedNewlines(t *testing.T) {
	ctx := context.Background()
	rt := &runtimeStub{}
	app := New(rt, &bytes.Buffer{})

	if _, err := app.HandleInput(ctx, ">>>\nPwd\nLs /\n<<<"); err != nil {
		t.Fatalf("standalone multiline batch: %v", err)
	}
	if len(rt.execBodyInputs) != 1 || rt.execBodyInputs[0] != "Pwd\nLs /" {
		t.Fatalf("unexpected ExecBody calls: %#v", rt.execBodyInputs)
	}
}

func TestHandleInputBatchMarkersRejectSemicolonSeparatedCommands(t *testing.T) {
	ctx := context.Background()
	app := New(&runtimeStub{}, &bytes.Buffer{})

	_, err := app.HandleInput(ctx, ">>> Pwd; Ls / <<<")
	if err == nil || err.Error() != "batch commands must be separated by newlines; semicolons are not allowed" {
		t.Fatalf("unexpected error: %v", err)
	}
	if app.state.Collecting {
		t.Fatalf("expected batch parse failure to leave collecting=false")
	}
}

func TestHandleInputBatchEndWithoutStartFails(t *testing.T) {
	ctx := context.Background()
	app := New(&runtimeStub{}, &bytes.Buffer{})

	_, err := app.HandleInput(ctx, "<<<")
	if err == nil || err.Error() != "<<< is only valid as the end of a >>> ... <<< batch" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleInputBatchStartRejectedInChunkMode(t *testing.T) {
	ctx := context.Background()
	app := New(&runtimeStub{}, &bytes.Buffer{})
	app.state.Mode = ModeChunk

	_, err := app.HandleInput(ctx, ">>>")
	if err == nil || err.Error() != ">>> is only available in line mode; chunk mode already buffers input" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleInputNestedBatchStartFails(t *testing.T) {
	ctx := context.Background()
	app := New(&runtimeStub{}, &bytes.Buffer{})

	if _, err := app.HandleInput(ctx, "/begin"); err != nil {
		t.Fatalf("start collecting chunk: %v", err)
	}
	_, err := app.HandleInput(ctx, ">>> Pwd")
	if err == nil || err.Error() != "already collecting a chunk" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleInputBatchWithoutEndFails(t *testing.T) {
	ctx := context.Background()
	app := New(&runtimeStub{}, &bytes.Buffer{})

	_, err := app.HandleInput(ctx, ">>> Pwd")
	if err == nil || err.Error() != "batch input must end with <<<" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSlashBeginCommitStillWorks(t *testing.T) {
	ctx := context.Background()
	rt := &runtimeStub{
		execBodyResult: tuiruntime.ExecResult{
			Output: "body-output",
		},
	}
	var out bytes.Buffer
	app := New(rt, &out)

	if _, err := app.HandleInput(ctx, "/begin"); err != nil {
		t.Fatalf("/begin: %v", err)
	}
	if _, err := app.HandleInput(ctx, "Pwd"); err != nil {
		t.Fatalf("buffer line: %v", err)
	}
	if _, err := app.HandleInput(ctx, "/commit"); err != nil {
		t.Fatalf("/commit: %v", err)
	}
	if len(rt.execBodyInputs) != 1 || rt.execBodyInputs[0] != "Pwd" {
		t.Fatalf("unexpected ExecBody calls: %#v", rt.execBodyInputs)
	}
	if got := out.String(); got == "" {
		t.Fatalf("expected rendered output")
	}
}

func TestHandleInputLineModeStillExecutesSingleLine(t *testing.T) {
	ctx := context.Background()
	rt := &runtimeStub{}
	app := New(rt, &bytes.Buffer{})

	if _, err := app.HandleInput(ctx, "Pwd"); err != nil {
		t.Fatalf("line exec: %v", err)
	}
	if len(rt.execLineInputs) != 1 || rt.execLineInputs[0] != "Pwd" {
		t.Fatalf("unexpected ExecLine calls: %#v", rt.execLineInputs)
	}
	if rt.commitCalls != 1 {
		t.Fatalf("expected one auto-commit, got %d", rt.commitCalls)
	}
}

func TestHandleInputBatchPropagatesExecBodyError(t *testing.T) {
	ctx := context.Background()
	rt := &runtimeStub{execBodyErr: fmt.Errorf("boom")}
	app := New(rt, &bytes.Buffer{})

	_, err := app.HandleInput(ctx, ">>> Pwd <<<")
	if err == nil || err.Error() != "boom" {
		t.Fatalf("unexpected error: %v", err)
	}
	if app.state.Collecting {
		t.Fatalf("expected collecting=false after batch ExecBody failure")
	}
}
