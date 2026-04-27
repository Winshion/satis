package tuiruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"satis/bridge"
	"satis/satis"
	"satis/tui/workbench"
	"satis/vfs"
)

type ExecResult struct {
	Output  string
	Summary []string
}

type SessionAdapter struct {
	executor         *satis.Executor
	session          *satis.Session
	bridge           *bridge.Server
	chunkID          string
	hostCWD          string
	secureMode       bool
	stdout           io.Writer
	lastPlanRunID    string
	eventCursorByRun map[string]int
}

func NewSessionAdapter(ctx context.Context, executor *satis.Executor, chunkID string, hostCWD string) (*SessionAdapter, error) {
	if executor == nil {
		return nil, fmt.Errorf("tui runtime error: missing executor")
	}
	session, err := executor.NewSession(ctx, chunkID)
	if err != nil {
		return nil, err
	}
	if hostCWD == "" {
		hostCWD, _ = os.Getwd()
	}
	bridgeServer := bridge.NewServer(vfs.Config{}, executor.VFS, executor.Invoker)
	bridgeServer.SetLoadPort(executor.LoadPort)
	if executor.BatchScheduler != nil {
		bridgeServer.SetScheduler(executor.BatchScheduler)
	}
	bridgeServer.SetInitialCWD(executor.InitialCWD)
	return &SessionAdapter{
		executor:         executor,
		session:          session,
		bridge:           bridgeServer,
		chunkID:          chunkID,
		hostCWD:          hostCWD,
		eventCursorByRun: make(map[string]int),
	}, nil
}

func (a *SessionAdapter) SetStdoutMirror(w io.Writer) {
	if a == nil {
		return
	}
	a.stdout = w
}

func (a *SessionAdapter) SetHumanControlChooser(chooser bridge.HumanControlChooser) {
	if a == nil || a.bridge == nil {
		return
	}
	a.bridge.SetHumanControlChooser(chooser)
}

func (a *SessionAdapter) SetSecureMode(enabled bool) {
	if a == nil {
		return
	}
	a.secureMode = enabled
}

func (a *SessionAdapter) ExecLine(ctx context.Context, line string) (ExecResult, error) {
	if strings.TrimSpace(line) == "" {
		return ExecResult{}, nil
	}
	return a.capture(ctx, func() error {
		return a.session.ExecLine(ctx, line)
	})
}

func (a *SessionAdapter) ExecBody(ctx context.Context, body string) (ExecResult, error) {
	if strings.TrimSpace(body) == "" {
		return ExecResult{}, fmt.Errorf("empty chunk body")
	}
	return a.capture(ctx, func() error {
		return a.session.ExecBody(ctx, body)
	})
}

func (a *SessionAdapter) ExecFile(ctx context.Context, path string) (ExecResult, error) {
	targetPath, err := a.resolveHostPath(path)
	if err != nil {
		return ExecResult{}, err
	}
	src, err := os.ReadFile(targetPath)
	if err != nil {
		return ExecResult{}, err
	}
	var result *satis.ExecutionResult
	captured, err := a.capture(ctx, func() error {
		var execErr error
		result, execErr = a.executor.ParseValidateExecute(ctx, string(src))
		return execErr
	})
	if err != nil {
		return ExecResult{}, err
	}
	captured.Summary = append(captured.Summary, summarizeExecutionResult(targetPath, result)...)
	return captured, nil
}

func (a *SessionAdapter) ResetSession(ctx context.Context) error {
	if a == nil || a.executor == nil {
		return fmt.Errorf("tui runtime error: adapter is not initialized")
	}
	if a.session != nil {
		if err := a.session.Close(ctx); err != nil {
			return err
		}
	}
	session, err := a.executor.NewSession(ctx, a.chunkID)
	if err != nil {
		return err
	}
	a.session = session
	return nil
}

func (a *SessionAdapter) CommitSession(ctx context.Context) error {
	if a == nil || a.session == nil {
		return fmt.Errorf("tui runtime error: session is not initialized")
	}
	return a.session.Commit(ctx)
}

func (a *SessionAdapter) Close(ctx context.Context) error {
	if a == nil || a.session == nil {
		return nil
	}
	return a.session.Close(ctx)
}

// SetPlanExecutionCWD sets VFS initial working directory to the directory containing the workbench plan.
// Relative paths in chunk bodies (e.g. write into notes.md) resolve against this directory.
func (a *SessionAdapter) SetPlanExecutionCWD(planPath string) {
	if a == nil || a.executor == nil {
		return
	}
	planPath = strings.TrimSpace(planPath)
	if planPath == "" {
		return
	}
	dir := path.Dir(planPath)
	if dir == "." || dir == "" {
		dir = "/"
	}
	dir = path.Clean(dir)
	if !strings.HasPrefix(dir, "/") {
		dir = "/" + dir
	}
	a.executor.InitialCWD = dir
	if a.bridge != nil {
		a.bridge.SetInitialCWD(dir)
	}
}

func (a *SessionAdapter) CurrentCWD() string {
	if a == nil || a.session == nil {
		return "/"
	}
	return a.session.CurrentCWD()
}

func (a *SessionAdapter) CurrentLoadCWD() string {
	if a == nil || a.session == nil {
		return "/"
	}
	return a.session.CurrentLoadCWD()
}

func (a *SessionAdapter) CurrentSoftwareCWD() string {
	if a == nil || a.session == nil {
		return "/"
	}
	return a.session.CurrentSoftwareCWD()
}

func (a *SessionAdapter) ListVariables() []satis.SessionVariable {
	if a == nil || a.session == nil {
		return nil
	}
	return a.session.ListVariables()
}

func (a *SessionAdapter) ListVirtualDir(ctx context.Context, virtualPath string) ([]vfs.DirEntry, error) {
	if a == nil || a.session == nil {
		return nil, fmt.Errorf("tui runtime error: session is not initialized")
	}
	return a.session.ListDir(ctx, virtualPath)
}

func (a *SessionAdapter) ListLoadDir(ctx context.Context, virtualPath string) ([]satis.LoadEntry, error) {
	if a == nil || a.session == nil {
		return nil, fmt.Errorf("tui runtime error: session is not initialized")
	}
	return a.session.ListLoadDir(ctx, virtualPath)
}

func (a *SessionAdapter) ResolveVirtualPath(path string) string {
	if a == nil || a.session == nil {
		return "/"
	}
	return a.session.ResolvePath(path)
}

func (a *SessionAdapter) ReadVirtualText(ctx context.Context, path string) (string, error) {
	if a == nil || a.session == nil {
		return "", fmt.Errorf("tui runtime error: session is not initialized")
	}
	return a.session.ReadVirtualText(ctx, path)
}

func (a *SessionAdapter) WriteVirtualText(ctx context.Context, path string, text string) error {
	if a == nil || a.session == nil {
		return fmt.Errorf("tui runtime error: session is not initialized")
	}
	if _, err := a.session.WriteVirtualText(ctx, path, text); err != nil {
		return err
	}
	return a.session.Commit(ctx)
}

func (a *SessionAdapter) OpenWorkbench(ctx context.Context, path string, intent string) error {
	if a == nil || a.session == nil {
		return fmt.Errorf("tui runtime error: session is not initialized")
	}
	planPath, created, err := a.prepareWorkbenchPlanPath(ctx, path, intent)
	if err != nil {
		return err
	}
	return workbench.Open(ctx, a, planPath, created)
}

func (a *SessionAdapter) ValidatePlan(ctx context.Context, rawPath string) (ExecResult, error) {
	planPath, plan, err := a.loadExistingPlan(ctx, rawPath)
	if err != nil {
		return ExecResult{}, err
	}
	return ExecResult{
		Summary: []string{
			fmt.Sprintf("validated %s", planPath),
			fmt.Sprintf("plan_id: %s", plan.PlanID),
			fmt.Sprintf("entry_chunk: %s", plan.EntryChunks[0]),
			fmt.Sprintf("chunks: %d", len(plan.Chunks)),
		},
	}, nil
}

func (a *SessionAdapter) RunPlan(ctx context.Context, rawPath string) (ExecResult, error) {
	planPath, plan, err := a.loadExistingPlan(ctx, rawPath)
	if err != nil {
		return ExecResult{}, err
	}
	prefix := isolatedRunPrefix()
	cloned, err := clonePlanForIsolatedRun(plan, prefix)
	if err != nil {
		return ExecResult{}, err
	}
	if result := bridge.ValidateChunkGraphPlan(cloned); !result.Accepted || result.NormalizedPlan == nil {
		return ExecResult{}, formatPlanValidationErrors(result.ValidationErrors)
	} else {
		cloned = result.NormalizedPlan
	}
	isolatedSession, err := a.executor.NewSession(ctx, cloned.EntryChunks[0])
	if err != nil {
		return ExecResult{}, err
	}
	defer func() {
		_ = isolatedSession.Close(ctx)
	}()
	submit, err := a.SubmitPlan(ctx, cloned)
	if err != nil {
		return ExecResult{}, err
	}
	if !submit.Accepted || submit.NormalizedPlan == nil {
		return ExecResult{}, formatPlanValidationErrors(submit.ValidationErrors)
	}
	run, err := a.StartPlanRun(ctx, submit.NormalizedPlan.PlanID, bridge.ExecutionOptions{})
	if err != nil {
		return ExecResult{}, err
	}
	a.lastPlanRunID = run.RunID
	a.eventCursorByRun[run.RunID] = 0
	return ExecResult{
		Summary: []string{
			fmt.Sprintf("submitted isolated plan from %s", planPath),
			fmt.Sprintf("source_plan_id: %s", plan.PlanID),
			fmt.Sprintf("effective_plan_id: %s", submit.NormalizedPlan.PlanID),
			fmt.Sprintf("entry_chunk: %s", submit.NormalizedPlan.EntryChunks[0]),
			fmt.Sprintf("run_id: %s", run.RunID),
			fmt.Sprintf("status: %s", run.Status),
		},
	}, nil
}

func (a *SessionAdapter) PlanStatus(ctx context.Context, runID string) (ExecResult, error) {
	resolvedRunID, err := a.resolvePlanRunID(runID)
	if err != nil {
		return ExecResult{}, err
	}
	inspect, err := a.InspectPlanRun(ctx, resolvedRunID)
	if err != nil {
		return ExecResult{}, err
	}
	return ExecResult{
		Summary: []string{
			fmt.Sprintf("run_id: %s", inspect.Run.RunID),
			fmt.Sprintf("status: %s", inspect.Run.Status),
			fmt.Sprintf("updated_at: %s", inspect.Run.UpdatedAt.Format(time.RFC3339)),
		},
	}, nil
}

func (a *SessionAdapter) PlanInspect(ctx context.Context, runID string) (ExecResult, error) {
	if strings.TrimSpace(runID) == "" {
		if a.bridge == nil {
			return ExecResult{}, fmt.Errorf("tui runtime error: bridge is not initialized")
		}
		list := a.bridge.ListRuns()
		lines := []string{"unfinished runs:"}
		count := 0
		for _, run := range list.Runs {
			if isTerminalRunPhase(run.Status) {
				continue
			}
			lines = append(lines, fmt.Sprintf("run_id=%s plan_id=%s status=%s updated_at=%s", run.RunID, run.PlanID, run.Status, run.UpdatedAt.Format(time.RFC3339)))
			count++
		}
		if count == 0 {
			lines = append(lines, "(none)")
		}
		return ExecResult{Summary: lines}, nil
	}
	resolvedRunID, err := a.resolvePlanRunID(runID)
	if err != nil {
		return ExecResult{}, err
	}
	inspect, err := a.InspectPlanRun(ctx, resolvedRunID)
	if err != nil {
		return ExecResult{}, err
	}
	lines := []string{
		fmt.Sprintf("run_id: %s", inspect.Run.RunID),
		fmt.Sprintf("status: %s", inspect.Run.Status),
		formatRunSummary(inspect.Summary),
	}
	lines = append(lines, formatChunkStatusLines(inspect.Run.ChunkStatuses)...)
	return ExecResult{Summary: lines}, nil
}

func (a *SessionAdapter) PlanEvents(ctx context.Context, runID string) (ExecResult, error) {
	resolvedRunID, err := a.resolvePlanRunID(runID)
	if err != nil {
		return ExecResult{}, err
	}
	result, err := a.StreamPlanRunEvents(ctx, resolvedRunID)
	if err != nil {
		return ExecResult{}, err
	}
	cursor := a.eventCursorByRun[resolvedRunID]
	if cursor > len(result.Events) {
		cursor = 0
	}
	lines := []string{fmt.Sprintf("run_id: %s", resolvedRunID)}
	if cursor == len(result.Events) {
		lines = append(lines, fmt.Sprintf("no new events for run_id=%s", resolvedRunID))
	} else {
		lines = append(lines, formatRunEvents(result.Events[cursor:])...)
	}
	a.eventCursorByRun[resolvedRunID] = len(result.Events)
	if result.Completed {
		lines = append(lines, fmt.Sprintf("run_id=%s is terminal", resolvedRunID))
	}
	return ExecResult{Summary: lines}, nil
}

func (a *SessionAdapter) PlanContinue(ctx context.Context, runID string, fragmentPath string) (ExecResult, error) {
	if a.bridge == nil {
		return ExecResult{}, fmt.Errorf("tui runtime error: bridge is not initialized")
	}
	runID, err := a.resolvePlanRunID(runID)
	if err != nil {
		return ExecResult{}, err
	}
	targetPath, err := a.resolveHostPath(fragmentPath)
	if err != nil {
		return ExecResult{}, err
	}
	src, err := os.ReadFile(targetPath)
	if err != nil {
		return ExecResult{}, err
	}
	var fragment bridge.PlanFragment
	if err := json.Unmarshal(src, &fragment); err != nil {
		return ExecResult{}, fmt.Errorf("parse fragment json: %w", err)
	}
	status, err := a.bridge.ContinueRunWithFragment(ctx, bridge.ContinueRunFragmentParams{RunID: runID, Fragment: fragment})
	if err != nil {
		return ExecResult{}, err
	}
	a.lastPlanRunID = runID
	return ExecResult{Summary: []string{
		fmt.Sprintf("continued run %s", runID),
		fmt.Sprintf("status=%s", status.Status),
		fmt.Sprintf("fragment_entry=%s", strings.Join(fragment.EntryChunks, ",")),
	}}, nil
}

func (a *SessionAdapter) PlanContinueLLM(ctx context.Context, runID string, prompt string) (ExecResult, error) {
	if a.bridge == nil {
		return ExecResult{}, fmt.Errorf("tui runtime error: bridge is not initialized")
	}
	runID, err := a.resolvePlanRunID(runID)
	if err != nil {
		return ExecResult{}, err
	}
	status, err := a.bridge.ContinueRunWithLLM(ctx, bridge.ContinueRunLLMParams{RunID: runID, Prompt: prompt})
	if err != nil {
		return ExecResult{}, err
	}
	a.lastPlanRunID = runID
	return ExecResult{Summary: []string{
		fmt.Sprintf("llm continued run %s", runID),
		fmt.Sprintf("status=%s", status.Status),
	}}, nil
}

func (a *SessionAdapter) PlanFinish(ctx context.Context, runID string) (ExecResult, error) {
	if a.bridge == nil {
		return ExecResult{}, fmt.Errorf("tui runtime error: bridge is not initialized")
	}
	runID, err := a.resolvePlanRunID(runID)
	if err != nil {
		return ExecResult{}, err
	}
	status, err := a.bridge.FinishRunPlanning(bridge.RunIDParams{RunID: runID})
	if err != nil {
		return ExecResult{}, err
	}
	return ExecResult{Summary: []string{
		fmt.Sprintf("finished planning for run %s", runID),
		fmt.Sprintf("status=%s", status.Status),
	}}, nil
}

func (a *SessionAdapter) SubmitPlan(_ context.Context, plan *bridge.ChunkGraphPlan) (bridge.SubmitChunkGraphResult, error) {
	if a == nil || a.bridge == nil {
		return bridge.SubmitChunkGraphResult{}, fmt.Errorf("tui runtime error: bridge is not initialized")
	}
	return a.bridge.SubmitChunkGraph(plan), nil
}

func (a *SessionAdapter) StartPlanRun(ctx context.Context, planID string, options bridge.ExecutionOptions) (bridge.RunStatus, error) {
	if a == nil || a.bridge == nil {
		return bridge.RunStatus{}, fmt.Errorf("tui runtime error: bridge is not initialized")
	}
	return a.bridge.StartRun(ctx, bridge.StartRunParams{
		PlanID:           planID,
		ExecutionOptions: options,
	})
}

func (a *SessionAdapter) ContinuePlanRun(ctx context.Context, runID string, fragment bridge.PlanFragment) (bridge.RunStatus, error) {
	if a == nil || a.bridge == nil {
		return bridge.RunStatus{}, fmt.Errorf("tui runtime error: bridge is not initialized")
	}
	return a.bridge.ContinueRunWithFragment(ctx, bridge.ContinueRunFragmentParams{RunID: runID, Fragment: fragment})
}

func (a *SessionAdapter) ContinuePlanRunLLM(ctx context.Context, runID string, prompt string) (bridge.RunStatus, error) {
	if a == nil || a.bridge == nil {
		return bridge.RunStatus{}, fmt.Errorf("tui runtime error: bridge is not initialized")
	}
	return a.bridge.ContinueRunWithLLM(ctx, bridge.ContinueRunLLMParams{RunID: runID, Prompt: prompt})
}

func (a *SessionAdapter) FinishPlanRun(_ context.Context, runID string) (bridge.RunStatus, error) {
	if a == nil || a.bridge == nil {
		return bridge.RunStatus{}, fmt.Errorf("tui runtime error: bridge is not initialized")
	}
	return a.bridge.FinishRunPlanning(bridge.RunIDParams{RunID: runID})
}

func (a *SessionAdapter) InspectPlanRun(_ context.Context, runID string) (bridge.InspectRunResult, error) {
	if a == nil || a.bridge == nil {
		return bridge.InspectRunResult{}, fmt.Errorf("tui runtime error: bridge is not initialized")
	}
	return a.bridge.InspectRun(bridge.RunIDParams{RunID: runID})
}

func (a *SessionAdapter) InspectRunChunk(_ context.Context, runID string, chunkID string) (bridge.ChunkExecutionResult, error) {
	if a == nil || a.bridge == nil {
		return bridge.ChunkExecutionResult{}, fmt.Errorf("tui runtime error: bridge is not initialized")
	}
	return a.bridge.InspectChunk(bridge.InspectChunkParams{
		RunID:   runID,
		ChunkID: chunkID,
	})
}

func (a *SessionAdapter) StreamPlanRunEvents(_ context.Context, runID string) (bridge.StreamRunEventsResult, error) {
	if a == nil || a.bridge == nil {
		return bridge.StreamRunEventsResult{}, fmt.Errorf("tui runtime error: bridge is not initialized")
	}
	return a.bridge.StreamRunEvents(bridge.RunIDParams{RunID: runID})
}

func (a *SessionAdapter) prepareWorkbenchPlanPath(ctx context.Context, dirPath string, intent string) (string, bool, error) {
	if a == nil || a.session == nil {
		return "", false, fmt.Errorf("tui runtime error: session is not initialized")
	}
	resolvedDir := a.session.ResolvePath(dirPath)
	if path.Ext(resolvedDir) == ".json" {
		if strings.TrimSpace(intent) != "" {
			return "", false, fmt.Errorf("cannot provide intent when opening an existing plan file")
		}
		return resolvedDir, false, nil
	}
	_, dirErr := a.session.ListDir(ctx, resolvedDir)
	dirExists := dirErr == nil
	if dirErr != nil && !errors.Is(dirErr, vfs.ErrFileNotFound) {
		return "", false, dirErr
	}
	planPath := workbench.PlanPathForWorkspace(resolvedDir)
	if _, err := a.session.ReadVirtualText(ctx, planPath); err == nil {
		if strings.TrimSpace(intent) != "" {
			return "", false, fmt.Errorf("workspace already exists; do not provide intent when opening an existing workbench")
		}
		return planPath, false, nil
	} else if !errors.Is(err, vfs.ErrFileNotFound) {
		return "", false, err
	}
	if dirExists {
		if strings.TrimSpace(intent) != "" {
			return "", false, fmt.Errorf("workspace directory already exists; intent is only allowed when creating a new workbench directory")
		}
		return "", false, fmt.Errorf("workspace directory exists but %s is missing; create a new workbench in a new directory with /workbench DIR INTENT", workbench.DefaultPlanFileName)
	}
	if strings.TrimSpace(intent) == "" {
		return "", false, fmt.Errorf("creating a new workbench requires an intent: /workbench DIR INTENT")
	}
	if err := a.session.EnsureVirtualDirectory(ctx, resolvedDir); err != nil {
		return "", false, err
	}
	if err := a.session.Commit(ctx); err != nil {
		return "", false, err
	}
	text, err := workbench.ScaffoldPlanJSON(resolvedDir, intent)
	if err != nil {
		return "", false, err
	}
	if _, err := a.session.WriteVirtualText(ctx, planPath, text); err != nil {
		return "", false, err
	}
	if err := a.session.Commit(ctx); err != nil {
		return "", false, err
	}
	return planPath, true, nil
}

func (a *SessionAdapter) loadExistingPlan(ctx context.Context, rawPath string) (string, *bridge.ChunkGraphPlan, error) {
	if a == nil || a.session == nil {
		return "", nil, fmt.Errorf("tui runtime error: session is not initialized")
	}
	planPath := a.session.ResolvePath(strings.TrimSpace(rawPath))
	if path.Ext(planPath) != ".json" {
		planPath = workbench.PlanPathForWorkspace(planPath)
	}
	text, err := a.session.ReadVirtualText(ctx, planPath)
	if err != nil {
		return "", nil, err
	}
	plan, err := workbench.ParsePlan(text)
	if err != nil {
		return "", nil, err
	}
	return planPath, plan, nil
}

func (a *SessionAdapter) capture(ctx context.Context, run func() error) (ExecResult, error) {
	if a == nil || a.executor == nil {
		return ExecResult{}, fmt.Errorf("tui runtime error: adapter is not initialized")
	}
	var stdout bytes.Buffer
	previous := a.executor.Stdout
	writer := io.Writer(&stdout)
	liveMirror := a.stdout != nil
	var mirror *thinkingStreamWriter
	if liveMirror {
		mirror = newThinkingStreamWriter(a.stdout, 6, 120)
		writer = io.MultiWriter(&stdout, mirror)
	}
	a.executor.Stdout = writer
	defer func() {
		a.executor.Stdout = previous
	}()
	if mirror != nil {
		defer func() {
			_ = mirror.Close()
		}()
	}
	if err := run(); err != nil {
		return ExecResult{}, err
	}
	if mirror != nil {
		if err := mirror.Close(); err != nil {
			return ExecResult{}, err
		}
	}
	output := strings.TrimRight(stdout.String(), "\n")
	if liveMirror {
		output = ""
	}
	return ExecResult{
		Output: output,
	}, nil
}

func (a *SessionAdapter) resolveHostPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("missing .satis path")
	}
	if a != nil && a.secureMode {
		return "", fmt.Errorf("host-path access is disabled in secure mode")
	}
	if filepath.IsAbs(path) {
		return path, nil
	}
	if a != nil && a.hostCWD != "" {
		return filepath.Clean(filepath.Join(a.hostCWD, path)), nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Clean(filepath.Join(cwd, path)), nil
}

func summarizeExecutionResult(path string, result *satis.ExecutionResult) []string {
	if result == nil {
		return []string{fmt.Sprintf("executed %s", path)}
	}
	lines := []string{
		fmt.Sprintf("executed %s", path),
		fmt.Sprintf("chunk_id: %s", result.ChunkID),
	}
	if len(result.Objects) > 0 {
		objectNames := sortedKeys(result.Objects)
		lines = append(lines, fmt.Sprintf("objects: %s", strings.Join(objectNames, ", ")))
	}
	if len(result.Values) > 0 {
		valueNames := sortedKeys(result.Values)
		lines = append(lines, fmt.Sprintf("values: %s", strings.Join(valueNames, ", ")))
	}
	for _, note := range result.Notes {
		lines = append(lines, "note: "+note)
	}
	return lines
}

func sortedKeys[T any](items map[string]T) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (a *SessionAdapter) resolvePlanRunID(runID string) (string, error) {
	runID = strings.TrimSpace(runID)
	if runID != "" {
		return runID, nil
	}
	if strings.TrimSpace(a.lastPlanRunID) == "" {
		return "", fmt.Errorf("no plan run yet; use /plan run first")
	}
	return a.lastPlanRunID, nil
}

func formatRunSummary(summary bridge.InspectRunSummary) string {
	parts := []string{
		fmt.Sprintf("plan_id=%s", summary.PlanID),
		fmt.Sprintf("graph_revision=%d", summary.GraphRevision),
		fmt.Sprintf("continuations=%d", summary.ContinuationCount),
		fmt.Sprintf("total=%d", summary.TotalChunks),
	}
	if summary.PlanningPending {
		parts = append(parts, "planning_pending=true")
	}
	if len(summary.TaskChunkIDs) > 0 {
		parts = append(parts, fmt.Sprintf("task=%d", len(summary.TaskChunkIDs)))
	}
	if len(summary.DecisionChunkIDs) > 0 {
		parts = append(parts, fmt.Sprintf("decision=%d", len(summary.DecisionChunkIDs)))
	}
	if len(summary.SucceededChunkIDs) > 0 {
		parts = append(parts, fmt.Sprintf("succeeded=%d", len(summary.SucceededChunkIDs)))
	}
	if len(summary.FailedChunkIDs) > 0 {
		parts = append(parts, fmt.Sprintf("failed=%d", len(summary.FailedChunkIDs)))
	}
	if len(summary.RunningChunkIDs) > 0 {
		parts = append(parts, fmt.Sprintf("running=%d", len(summary.RunningChunkIDs)))
	}
	if len(summary.PendingChunkIDs) > 0 {
		parts = append(parts, fmt.Sprintf("pending=%d", len(summary.PendingChunkIDs)))
	}
	if len(summary.BlockedChunkIDs) > 0 {
		parts = append(parts, fmt.Sprintf("blocked=%d", len(summary.BlockedChunkIDs)))
	}
	if len(summary.CancelledChunkIDs) > 0 {
		parts = append(parts, fmt.Sprintf("cancelled=%d", len(summary.CancelledChunkIDs)))
	}
	if summary.ArtifactTotal > 0 {
		parts = append(parts, fmt.Sprintf("artifacts=%d", summary.ArtifactTotal))
	}
	if summary.PrimaryFailureChunkID != "" {
		parts = append(parts, fmt.Sprintf("primary_failure=%s", summary.PrimaryFailureChunkID))
	}
	if summary.LatestPlanningSummary != "" {
		parts = append(parts, fmt.Sprintf("latest_planning=%s", summary.LatestPlanningSummary))
	}
	return strings.Join(parts, " ")
}

func formatChunkStatusLines(statuses map[string]bridge.ChunkPhase) []string {
	if len(statuses) == 0 {
		return nil
	}
	chunkIDs := make([]string, 0, len(statuses))
	for chunkID := range statuses {
		chunkIDs = append(chunkIDs, chunkID)
	}
	sort.Strings(chunkIDs)
	lines := make([]string, 0, len(chunkIDs))
	for _, chunkID := range chunkIDs {
		lines = append(lines, fmt.Sprintf("chunk %s: %s", chunkID, statuses[chunkID]))
	}
	return lines
}

func formatRunEvents(events []bridge.RunEvent) []string {
	lines := make([]string, 0, len(events))
	for _, event := range events {
		chunkLabel := ""
		if event.ChunkID != nil && *event.ChunkID != "" {
			chunkLabel = " chunk=" + *event.ChunkID
		}
		lines = append(lines, fmt.Sprintf("event %s %s%s", event.EventID, event.Type, chunkLabel))
	}
	return lines
}

func isTerminalRunPhase(phase bridge.RunPhase) bool {
	switch phase {
	case bridge.RunPhaseCompleted, bridge.RunPhaseFailed, bridge.RunPhaseCancelled:
		return true
	default:
		return false
	}
}

func isolatedRunPrefix() string {
	return fmt.Sprintf("LINE_RUN_%d__", time.Now().UnixNano())
}

func clonePlanForIsolatedRun(plan *bridge.ChunkGraphPlan, prefix string) (*bridge.ChunkGraphPlan, error) {
	if plan == nil {
		return nil, fmt.Errorf("plan is nil")
	}
	if len(plan.EntryChunks) != 1 {
		return nil, fmt.Errorf("plan must declare exactly one entry chunk, got %d", len(plan.EntryChunks))
	}
	idMap := make(map[string]string, len(plan.Chunks))
	for _, chunk := range plan.Chunks {
		idMap[chunk.ChunkID] = prefix + chunk.ChunkID
	}
	cloned := &bridge.ChunkGraphPlan{
		ProtocolVersion: plan.ProtocolVersion,
		PlanID:          fmt.Sprintf("%s__line_run_%d", slugPlanID(plan.PlanID), time.Now().UnixNano()),
		IntentID:        plan.IntentID,
		Goal:            plan.Goal + " (line run)",
		Artifacts:       append([]bridge.ArtifactSpec(nil), plan.Artifacts...),
		PlannerNotes:    append([]string(nil), plan.PlannerNotes...),
	}
	if plan.Policies != nil {
		policies := *plan.Policies
		cloned.Policies = &policies
	}
	for _, entry := range plan.EntryChunks {
		cloned.EntryChunks = append(cloned.EntryChunks, idMap[entry])
	}
	for _, edge := range plan.Edges {
		cloned.Edges = append(cloned.Edges, bridge.PlanEdge{
			FromChunkID: idMap[edge.FromChunkID],
			ToChunkID:   idMap[edge.ToChunkID],
			EdgeKind:    edge.EdgeKind,
		})
	}
	for _, chunk := range plan.Chunks {
		copyChunk := chunk
		copyChunk.ChunkID = idMap[chunk.ChunkID]
		copyChunk.Source.SatisText = replaceChunkHeaderID(chunk.Source.SatisText, copyChunk.ChunkID)
		copyChunk.DependsOn = rewriteChunkIDSlice(chunk.DependsOn, idMap)
		if copyChunk.Inputs != nil {
			copyChunk.Inputs = cloneJSONMap(copyChunk.Inputs)
			rewriteHandoffFromSteps(copyChunk.Inputs, idMap)
		}
		if copyChunk.Outputs != nil {
			copyChunk.Outputs = cloneJSONMap(copyChunk.Outputs)
		}
		if copyChunk.IdempotencyKey != "" {
			copyChunk.IdempotencyKey = prefix + copyChunk.IdempotencyKey
		}
		cloned.Chunks = append(cloned.Chunks, copyChunk)
	}
	return cloned, nil
}

func rewriteHandoffFromSteps(inputs map[string]any, idMap map[string]string) {
	if inputs == nil {
		return
	}
	raw, _ := inputs["handoff_inputs"].(map[string]any)
	for _, item := range raw {
		spec, ok := item.(map[string]any)
		if !ok {
			continue
		}
		fromStep, _ := spec["from_step"].(string)
		if fromStep == "" {
			continue
		}
		if mapped, ok := idMap[fromStep]; ok {
			spec["from_step"] = mapped
		}
	}
}

func rewriteChunkIDSlice(values []string, idMap map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if mapped, ok := idMap[value]; ok {
			out = append(out, mapped)
			continue
		}
		out = append(out, value)
	}
	return out
}

func replaceChunkHeaderID(text string, chunkID string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "chunk_id:") {
			lines[i] = "chunk_id: " + chunkID
			return strings.Join(lines, "\n")
		}
	}
	return fmt.Sprintf("chunk_id: %s\n%s", chunkID, text)
}

func formatPlanValidationErrors(issues []bridge.ValidationIssue) error {
	if len(issues) == 0 {
		return fmt.Errorf("plan validation failed")
	}
	parts := make([]string, 0, len(issues))
	for _, issue := range issues {
		if issue.Field != "" {
			parts = append(parts, fmt.Sprintf("%s: %s", issue.Field, issue.Message))
		} else {
			parts = append(parts, issue.Message)
		}
	}
	return fmt.Errorf("%s", strings.Join(parts, "; "))
}

func slugPlanID(planID string) string {
	planID = strings.TrimSpace(planID)
	if planID == "" {
		return "plan"
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToLower(planID) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	slug := strings.Trim(b.String(), "_")
	if slug == "" {
		return "plan"
	}
	return slug
}

func cloneJSONMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = cloneJSONValue(value)
	}
	return out
}

func cloneJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneJSONMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = cloneJSONValue(item)
		}
		return out
	default:
		return value
	}
}
