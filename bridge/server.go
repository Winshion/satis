package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"satis/satis"
	"satis/vfs"
)

// StartRunParams is the RPC payload for gopy.start_run.
type StartRunParams struct {
	PlanID           string           `json:"plan_id"`
	ExecutionOptions ExecutionOptions `json:"execution_options,omitempty"`
}

type ContinueRunFragmentParams struct {
	RunID    string       `json:"run_id"`
	Fragment PlanFragment `json:"fragment"`
}

type ContinueRunLLMParams struct {
	RunID  string `json:"run_id"`
	Prompt string `json:"prompt"`
}

type WorkflowBindingSnapshot struct {
	Name      string               `json:"name"`
	Kind      string               `json:"kind"`
	Source    string               `json:"source,omitempty"`
	Version   int                  `json:"version"`
	Binding   satis.RuntimeBinding `json:"binding"`
	UpdatedAt time.Time            `json:"updated_at"`
}

type PlanningHistoryEntry struct {
	Continuation      int       `json:"continuation"`
	Source            string    `json:"source"`
	EntryNode         string    `json:"entry_node"`
	NewNodeIDs        []string  `json:"new_node_ids,omitempty"`
	NewEdgeCount      int       `json:"new_edge_count"`
	GraphRevision     int       `json:"graph_revision"`
	AttachedAt        time.Time `json:"attached_at"`
	Summary           string    `json:"summary,omitempty"`
	ImportedBackEdges int       `json:"imported_back_edges,omitempty"`
}

// RunIDParams identifies a run.
type RunIDParams struct {
	RunID      string `json:"run_id"`
	AfterIndex int    `json:"after_index,omitempty"`
}

// InspectChunkParams identifies a chunk result inside a run.
type InspectChunkParams struct {
	RunID   string `json:"run_id"`
	ChunkID string `json:"chunk_id"`
}

// StreamRunEventsResult is the polling-friendly v1 response shape.
type StreamRunEventsResult struct {
	RunID         string     `json:"run_id"`
	Events        []RunEvent `json:"events"`
	Completed     bool       `json:"completed"`
	NextIndex     int        `json:"next_index,omitempty"`
	EventsVersion uint64     `json:"events_version,omitempty"`
}

type ListRunsResult struct {
	Runs []RunStatus `json:"runs"`
}

// InspectRunResult returns the run snapshot plus per-chunk outcomes.
type InspectRunResult struct {
	Run             RunStatus                       `json:"run"`
	Chunks          map[string]ChunkExecutionResult `json:"chunks"`
	Events          []RunEvent                      `json:"events"`
	Summary         InspectRunSummary               `json:"summary"`
	PlanningHistory []PlanningHistoryEntry          `json:"planning_history,omitempty"`
}

// InspectRunSummary aggregates observation-friendly fields for clients (UI, scripts, tests).
type InspectRunSummary struct {
	PlanID                   string   `json:"plan_id,omitempty"`
	GraphRevision            int      `json:"graph_revision,omitempty"`
	PlanningPending          bool     `json:"planning_pending,omitempty"`
	ContinuationCount        int      `json:"continuation_count,omitempty"`
	TotalChunks              int      `json:"total_chunks"`
	Terminal                 bool     `json:"terminal"`
	TaskChunkIDs             []string `json:"task_chunk_ids,omitempty"`
	DecisionChunkIDs         []string `json:"decision_chunk_ids,omitempty"`
	FailedChunkIDs           []string `json:"failed_chunk_ids,omitempty"`
	BlockedChunkIDs          []string `json:"blocked_chunk_ids,omitempty"`
	SucceededChunkIDs        []string `json:"succeeded_chunk_ids,omitempty"`
	RunningChunkIDs          []string `json:"running_chunk_ids,omitempty"`
	ReadyChunkIDs            []string `json:"ready_chunk_ids,omitempty"`
	PendingChunkIDs          []string `json:"pending_chunk_ids,omitempty"`
	CancelledChunkIDs        []string `json:"cancelled_chunk_ids,omitempty"`
	WaitingHumanIDs          []string `json:"waiting_human_ids,omitempty"`
	PlanningIDs              []string `json:"planning_ids,omitempty"`
	ArtifactTotal            int      `json:"artifact_total"`
	RunErrorCode             string   `json:"run_error_code,omitempty"`
	RunErrorMessage          string   `json:"run_error_message,omitempty"`
	PrimaryFailureChunkID    string   `json:"primary_failure_chunk_id,omitempty"`
	PrimaryFailureCode       string   `json:"primary_failure_code,omitempty"`
	PrimaryFailureStage      string   `json:"primary_failure_stage,omitempty"`
	FailureLayer             string   `json:"failure_layer,omitempty"`
	InheritedVariableSummary []string `json:"inherited_variable_summary,omitempty"`
	LatestPlanningSummary    string   `json:"latest_planning_summary,omitempty"`
}

// InspectObjectResult wraps disk-backed object inspection data.
type InspectObjectResult struct {
	Backend  string             `json:"backend"`
	StateDir string             `json:"state_dir"`
	File     vfs.FileMeta       `json:"file"`
	Events   []vfs.Event        `json:"events,omitempty"`
	Versions []vfs.VersionEntry `json:"versions,omitempty"`
	Content  *ContentSummary    `json:"content,omitempty"`
}

// ContentSummary mirrors the current inspect CLI JSON shape.
type ContentSummary struct {
	Kind      string `json:"kind"`
	ByteSize  int    `json:"byte_size"`
	Preview   string `json:"preview,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// Server is the in-process Go scheduler facade behind gopyd.
type Server struct {
	mu        sync.RWMutex
	vfs       vfs.Service
	cfg       vfs.Config
	invoker   satis.Invoker
	human     HumanControlChooser
	pending   map[string]*pendingHumanControl
	loadPort  satis.LoadPort
	Scheduler BatchScheduler
	executor  *satis.Executor
	plans     map[string]*ChunkGraphPlan
	runs      map[string]*runRecord
	runSeq    uint64
	eventSeq  uint64
	runsCache []RunStatus
	runsDirty bool
}

type runRecord struct {
	mu                 sync.RWMutex
	plan               *ChunkGraphPlan
	status             RunStatus
	chunks             map[string]ChunkExecutionResult
	events             []RunEvent
	artifacts          []ArtifactSpec
	cancel             context.CancelFunc
	done               chan struct{}
	lastOptions        ExecutionOptions
	continuationCount  int
	graphRevision      int
	planningHistory    []PlanningHistoryEntry
	workflowRegistry   map[string]WorkflowBindingSnapshot
	loopBackCounts     map[string]int
	initialStableState *runStableStateSnapshot
	lastStableState    *runStableStateSnapshot
	eventsVersion      uint64
}

type runStableStateSnapshot struct {
	plan              *ChunkGraphPlan
	status            RunStatus
	chunks            map[string]ChunkExecutionResult
	artifacts         []ArtifactSpec
	graphRevision     int
	continuationCount int
	planningHistory   []PlanningHistoryEntry
	workflowRegistry  map[string]WorkflowBindingSnapshot
	vfsSnapshotDir    string
}

type InspectRunOverviewResult struct {
	Run                  RunStatus         `json:"run"`
	Summary              InspectRunSummary `json:"summary"`
	Completed            bool              `json:"completed"`
	EventCount           int               `json:"event_count"`
	EventsVersion        uint64            `json:"events_version"`
	PlanningHistoryCount int               `json:"planning_history_count"`
}

// NewServer creates a scheduler backed by the provided VFS service.
func NewServer(cfg vfs.Config, svc vfs.Service, invoker satis.Invoker) *Server {
	return &Server{
		vfs:       svc,
		cfg:       cfg,
		invoker:   invoker,
		executor:  newBridgeExecutor(svc, invoker),
		pending:   make(map[string]*pendingHumanControl),
		plans:     make(map[string]*ChunkGraphPlan),
		runs:      make(map[string]*runRecord),
		runsDirty: true,
	}
}

func newBridgeExecutor(svc vfs.Service, invoker satis.Invoker) *satis.Executor {
	return &satis.Executor{
		VFS:     svc,
		Invoker: invoker,
		// Bridge runs back interactive UIs like Workbench. Writing chunk output or
		// preview escape sequences to the real terminal corrupts the tview screen.
		Stdout: io.Discard,
	}
}

func (s *Server) invalidateRunsCacheLocked() {
	s.runsDirty = true
	s.runsCache = nil
}

func (s *Server) SetHumanControlChooser(chooser HumanControlChooser) {
	s.mu.Lock()
	s.human = chooser
	if chooser == nil {
		for _, req := range s.pending {
			if req == nil {
				continue
			}
			if req.dispatchCancel != nil {
				req.dispatchCancel()
				req.dispatchCancel = nil
			}
			req.dispatched = false
			req.dispatchSeq++
		}
	}
	pending := s.collectDispatchableHumanRequestsLocked()
	s.mu.Unlock()
	for _, req := range pending {
		s.dispatchPendingHumanRequest(req, chooser)
	}
}

func pendingHumanKey(runID, chunkID string) string {
	return runID + "\x00" + chunkID
}

func (s *Server) collectDispatchableHumanRequestsLocked() []*pendingHumanControl {
	if s.human == nil {
		return nil
	}
	pending := make([]*pendingHumanControl, 0, len(s.pending))
	for _, req := range s.pending {
		if req == nil {
			continue
		}
		if req.dispatchCancel != nil {
			req.dispatchCancel()
			req.dispatchCancel = nil
		}
		req.dispatchSeq++
		req.dispatched = true
		pending = append(pending, req)
	}
	return pending
}

func (s *Server) dispatchPendingHumanRequest(req *pendingHumanControl, chooser HumanControlChooser) {
	if req == nil || chooser == nil {
		return
	}
	s.mu.Lock()
	seq := req.dispatchSeq
	dispatchCtx, cancel := context.WithCancel(req.ctx)
	req.dispatchCancel = cancel
	s.mu.Unlock()
	go func() {
		choice, err := chooser(dispatchCtx, req.request)
		s.mu.Lock()
		active := req.dispatched && req.dispatchSeq == seq
		if req.dispatchSeq == seq {
			req.dispatchCancel = nil
		}
		s.mu.Unlock()
		if !active {
			return
		}
		select {
		case req.responseCh <- humanControlResponse{choice: choice, err: err}:
		default:
		}
	}()
}

func (s *Server) queueHumanControlRequest(ctx context.Context, req HumanControlRequest) *pendingHumanControl {
	pending := &pendingHumanControl{
		ctx:        ctx,
		request:    req,
		responseCh: make(chan humanControlResponse, 1),
	}
	s.mu.Lock()
	s.pending[pendingHumanKey(req.RunID, req.ChunkID)] = pending
	chooser := s.human
	if chooser != nil {
		pending.dispatched = true
	}
	s.mu.Unlock()
	if chooser != nil {
		s.dispatchPendingHumanRequest(pending, chooser)
	}
	return pending
}

func (s *Server) clearHumanControlRequest(runID, chunkID string, pending *pendingHumanControl) {
	key := pendingHumanKey(runID, chunkID)
	s.mu.Lock()
	if current, ok := s.pending[key]; ok && current == pending {
		if current.dispatchCancel != nil {
			current.dispatchCancel()
			current.dispatchCancel = nil
		}
		delete(s.pending, key)
	}
	s.mu.Unlock()
}

func (s *Server) SetLoadPort(loadPort satis.LoadPort) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadPort = loadPort
	if s.executor == nil {
		s.executor = newBridgeExecutor(s.vfs, s.invoker)
		s.executor.LoadPort = loadPort
		return
	}
	s.executor.LoadPort = loadPort
}

// SetScheduler injects the unified scheduler used by chunk, repeat, and batch invoke paths.
func (s *Server) SetScheduler(scheduler BatchScheduler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Scheduler = scheduler
	if s.executor == nil {
		s.executor = newBridgeExecutor(s.vfs, s.invoker)
		s.executor.LoadPort = s.loadPort
		s.executor.BatchScheduler = scheduler
		return
	}
	s.executor.BatchScheduler = scheduler
}

// SetInitialCWD keeps bridge-driven chunk execution aligned with the caller's runtime.
func (s *Server) SetInitialCWD(initialCWD string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.executor == nil {
		s.executor = newBridgeExecutor(s.vfs, s.invoker)
		s.executor.LoadPort = s.loadPort
	}
	s.executor.InitialCWD = initialCWD
}

func (s *Server) nextEventID() string {
	return fmt.Sprintf("evt_%06d", atomic.AddUint64(&s.eventSeq, 1))
}

// SubmitChunkGraph validates and stores a normalized plan.
func (s *Server) SubmitChunkGraph(plan *ChunkGraphPlan) SubmitChunkGraphResult {
	result := ValidateChunkGraphPlan(plan)
	if !result.Accepted || result.NormalizedPlan == nil {
		return result
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.plans[result.NormalizedPlan.PlanID] = result.NormalizedPlan
	return result
}

// StartRun registers a run and begins asynchronous execution.
func (s *Server) StartRun(ctx context.Context, params StartRunParams) (RunStatus, error) {
	s.mu.RLock()
	plan := s.plans[params.PlanID]
	s.mu.RUnlock()
	if plan == nil {
		return RunStatus{}, fmt.Errorf("gopy start_run error: unknown plan_id %q", params.PlanID)
	}

	runID := fmt.Sprintf("run_%06d", atomic.AddUint64(&s.runSeq, 1))
	now := time.Now().UTC()
	status := RunStatus{
		ProtocolVersion: 1,
		RunID:           runID,
		PlanID:          plan.PlanID,
		Status:          RunPhasePending,
		ChunkStatuses:   make(map[string]ChunkPhase, len(plan.Chunks)),
		StartedAt:       now,
		UpdatedAt:       now,
	}
	chunks := make(map[string]ChunkExecutionResult, len(plan.Chunks))
	for _, chunk := range plan.Chunks {
		status.ChunkStatuses[chunk.ChunkID] = ChunkPhasePending
		chunks[chunk.ChunkID] = ChunkExecutionResult{
			ChunkID: chunk.ChunkID,
			Kind:    normalizedChunkKind(chunk.Kind),
			Status:  ChunkPhasePending,
		}
	}

	runCtx, cancel := context.WithCancel(ctx)
	record := &runRecord{
		plan:             NormalizeChunkGraphPlan(plan),
		status:           status,
		chunks:           chunks,
		artifacts:        append([]ArtifactSpec(nil), plan.Artifacts...),
		cancel:           cancel,
		done:             make(chan struct{}),
		lastOptions:      params.ExecutionOptions,
		graphRevision:    1,
		workflowRegistry: make(map[string]WorkflowBindingSnapshot),
	}
	record.initialStableState = s.snapshotRunStableState(record)

	s.mu.Lock()
	s.runs[runID] = record
	s.invalidateRunsCacheLocked()
	s.mu.Unlock()

	go s.executeRun(runCtx, record, params.ExecutionOptions)
	return status, nil
}

// PauseRun marks a run as paused for protocol compatibility.
func (s *Server) PauseRun(params RunIDParams) (RunStatus, error) {
	record, err := s.lookupRun(params.RunID)
	if err != nil {
		return RunStatus{}, err
	}
	record.mu.Lock()
	defer record.mu.Unlock()
	if record.status.Status == RunPhaseRunning {
		record.status.Status = RunPhasePaused
		record.status.UpdatedAt = time.Now().UTC()
		s.mu.Lock()
		s.invalidateRunsCacheLocked()
		s.mu.Unlock()
	}
	return cloneRunStatus(record.status), nil
}

// ResumeRun marks a paused run as running.
func (s *Server) ResumeRun(params RunIDParams) (RunStatus, error) {
	record, err := s.lookupRun(params.RunID)
	if err != nil {
		return RunStatus{}, err
	}
	record.mu.Lock()
	defer record.mu.Unlock()
	if record.status.Status == RunPhasePaused {
		record.status.Status = RunPhaseRunning
		record.status.UpdatedAt = time.Now().UTC()
		s.mu.Lock()
		s.invalidateRunsCacheLocked()
		s.mu.Unlock()
	}
	return cloneRunStatus(record.status), nil
}

// CancelRun cancels a running run.
func (s *Server) CancelRun(params RunIDParams) (RunStatus, error) {
	record, err := s.lookupRun(params.RunID)
	if err != nil {
		return RunStatus{}, err
	}
	record.cancel()
	record.mu.Lock()
	record.status.Status = RunPhaseCancelled
	record.status.UpdatedAt = time.Now().UTC()
	record.mu.Unlock()
	s.mu.Lock()
	s.invalidateRunsCacheLocked()
	s.mu.Unlock()
	return s.GetRun(params)
}

// GetRun returns the run snapshot.
func (s *Server) GetRun(params RunIDParams) (RunStatus, error) {
	record, err := s.lookupRun(params.RunID)
	if err != nil {
		return RunStatus{}, err
	}
	record.mu.RLock()
	defer record.mu.RUnlock()
	return cloneRunStatus(record.status), nil
}

// InspectRun returns the run snapshot, chunk results, and collected events.
func (s *Server) InspectRun(params RunIDParams) (InspectRunResult, error) {
	record, err := s.lookupRun(params.RunID)
	if err != nil {
		return InspectRunResult{}, err
	}
	record.mu.RLock()
	defer record.mu.RUnlock()

	chunks := make(map[string]ChunkExecutionResult, len(record.chunks))
	for id, result := range record.chunks {
		chunks[id] = cloneChunkExecutionResult(result)
	}
	events := append([]RunEvent(nil), record.events...)

	return InspectRunResult{
		Run:             cloneRunStatus(record.status),
		Chunks:          chunks,
		Events:          events,
		Summary:         buildInspectRunSummary(record),
		PlanningHistory: append([]PlanningHistoryEntry(nil), record.planningHistory...),
	}, nil
}

func (s *Server) InspectRunOverview(params RunIDParams) (InspectRunOverviewResult, error) {
	record, err := s.lookupRun(params.RunID)
	if err != nil {
		return InspectRunOverviewResult{}, err
	}
	record.mu.RLock()
	defer record.mu.RUnlock()
	return InspectRunOverviewResult{
		Run:                  cloneRunStatus(record.status),
		Summary:              buildInspectRunSummary(record),
		Completed:            isTerminalRunPhase(record.status.Status),
		EventCount:           len(record.events),
		EventsVersion:        record.eventsVersion,
		PlanningHistoryCount: len(record.planningHistory),
	}, nil
}

// InspectChunk returns one chunk outcome.
func (s *Server) InspectChunk(params InspectChunkParams) (ChunkExecutionResult, error) {
	record, err := s.lookupRun(params.RunID)
	if err != nil {
		return ChunkExecutionResult{}, err
	}
	record.mu.RLock()
	defer record.mu.RUnlock()
	result, ok := record.chunks[params.ChunkID]
	if !ok {
		return ChunkExecutionResult{}, fmt.Errorf("gopy inspect_chunk error: unknown chunk_id %q", params.ChunkID)
	}
	return cloneChunkExecutionResult(result), nil
}

// StreamRunEvents returns the currently collected events for polling clients.
func (s *Server) StreamRunEvents(params RunIDParams) (StreamRunEventsResult, error) {
	record, err := s.lookupRun(params.RunID)
	if err != nil {
		return StreamRunEventsResult{}, err
	}
	record.mu.RLock()
	defer record.mu.RUnlock()
	completed := isTerminalRunPhase(record.status.Status)
	afterIndex := params.AfterIndex
	if afterIndex < 0 {
		afterIndex = 0
	}
	if afterIndex > len(record.events) {
		afterIndex = len(record.events)
	}
	return StreamRunEventsResult{
		RunID:         params.RunID,
		Events:        append([]RunEvent(nil), record.events[afterIndex:]...),
		Completed:     completed,
		NextIndex:     len(record.events),
		EventsVersion: record.eventsVersion,
	}, nil
}

func (s *Server) ListRuns() ListRunsResult {
	s.mu.RLock()
	if !s.runsDirty {
		cached := append([]RunStatus(nil), s.runsCache...)
		s.mu.RUnlock()
		return ListRunsResult{Runs: cached}
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runsDirty {
		runs := make([]RunStatus, 0, len(s.runs))
		for _, record := range s.runs {
			record.mu.RLock()
			runs = append(runs, cloneRunStatus(record.status))
			record.mu.RUnlock()
		}
		sort.Slice(runs, func(i, j int) bool {
			return runs[i].StartedAt.After(runs[j].StartedAt)
		})
		s.runsCache = runs
		s.runsDirty = false
	}
	return ListRunsResult{Runs: append([]RunStatus(nil), s.runsCache...)}
}

// ListArtifacts returns declared plan artifacts plus any discovered file handles.
func (s *Server) ListArtifacts(params RunIDParams) ([]ArtifactSpec, error) {
	record, err := s.lookupRun(params.RunID)
	if err != nil {
		return nil, err
	}
	record.mu.RLock()
	defer record.mu.RUnlock()
	return append([]ArtifactSpec(nil), record.artifacts...), nil
}

func (s *Server) lookupRun(runID string) (*runRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record := s.runs[runID]
	if record == nil {
		return nil, fmt.Errorf("gopy run error: unknown run_id %q", runID)
	}
	return record, nil
}

func isTerminalRunPhase(phase RunPhase) bool {
	switch phase {
	case RunPhaseCompleted, RunPhaseFailed, RunPhaseCancelled:
		return true
	default:
		return false
	}
}

func (s *Server) ContinueRunWithFragment(ctx context.Context, params ContinueRunFragmentParams) (RunStatus, error) {
	record, err := s.lookupRun(params.RunID)
	if err != nil {
		return RunStatus{}, err
	}
	record.mu.RLock()
	if record.status.Status != RunPhasePlanningPending {
		record.mu.RUnlock()
		return RunStatus{}, fmt.Errorf("run %q is not waiting for planning", params.RunID)
	}
	source := fmt.Sprintf("continuation_%d", record.continuationCount+1)
	stable := s.snapshotRunStableState(record)
	record.mu.RUnlock()
	if err := s.attachPlanFragment(record, source, params.Fragment); err != nil {
		return RunStatus{}, err
	}
	record.mu.Lock()
	record.status.Status = RunPhasePending
	record.status.UpdatedAt = time.Now().UTC()
	record.continuationCount++
	record.done = make(chan struct{})
	record.loopBackCounts = make(map[string]int)
	record.lastStableState = stable
	options := record.lastOptions
	record.mu.Unlock()
	s.mu.Lock()
	s.invalidateRunsCacheLocked()
	s.mu.Unlock()
	go s.executeRun(ctx, record, options)
	return s.GetRun(RunIDParams{RunID: params.RunID})
}

func (s *Server) ContinueRunWithLLM(ctx context.Context, params ContinueRunLLMParams) (RunStatus, error) {
	if s.invoker == nil {
		return RunStatus{}, fmt.Errorf("invoker is not configured")
	}
	record, err := s.lookupRun(params.RunID)
	if err != nil {
		return RunStatus{}, err
	}
	record.mu.RLock()
	if record.status.Status != RunPhasePlanningPending {
		record.mu.RUnlock()
		return RunStatus{}, fmt.Errorf("run %q is not waiting for planning", params.RunID)
	}
	summary := buildInspectRunSummary(record)
	record.mu.RUnlock()
	prompt := strings.TrimSpace(params.Prompt)
	if prompt == "" {
		prompt = "Return JSON with a top-level fragment field containing a single-entry controlled graph fragment."
	}
	fullPrompt := fmt.Sprintf("%s\nCurrent run summary: %s", prompt, formatPlanningSummary(summary))
	raw, err := s.invoker.Invoke(ctx, fullPrompt, "")
	if err != nil {
		return RunStatus{}, err
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return RunStatus{}, fmt.Errorf("llm returned invalid json: %w", err)
	}
	fragment, ok, err := parsePlanFragmentFromPayload(payload)
	if err != nil {
		return RunStatus{}, err
	}
	if !ok {
		return RunStatus{}, fmt.Errorf("llm response did not contain fragment")
	}
	return s.ContinueRunWithFragment(ctx, ContinueRunFragmentParams{RunID: params.RunID, Fragment: fragment})
}

func (s *Server) FinishRunPlanning(params RunIDParams) (RunStatus, error) {
	record, err := s.lookupRun(params.RunID)
	if err != nil {
		return RunStatus{}, err
	}
	record.mu.Lock()
	if record.status.Status != RunPhasePlanningPending {
		status := cloneRunStatus(record.status)
		record.mu.Unlock()
		return status, nil
	}
	record.status.Status = RunPhaseCompleted
	record.status.UpdatedAt = time.Now().UTC()
	status := cloneRunStatus(record.status)
	planID := record.plan.PlanID
	graphRevision := record.graphRevision
	record.mu.Unlock()
	s.mu.Lock()
	s.invalidateRunsCacheLocked()
	s.mu.Unlock()
	s.appendEvent(record, "", EventRunCompleted, map[string]any{
		"plan_id":        planID,
		"graph_revision": graphRevision,
	})
	return status, nil
}

func formatPlanningSummary(summary InspectRunSummary) string {
	parts := []string{
		fmt.Sprintf("graph_revision=%d", summary.GraphRevision),
		fmt.Sprintf("continuations=%d", summary.ContinuationCount),
		fmt.Sprintf("total_chunks=%d", summary.TotalChunks),
		fmt.Sprintf("pending=%d", len(summary.PendingChunkIDs)),
		fmt.Sprintf("succeeded=%d", len(summary.SucceededChunkIDs)),
	}
	if len(summary.DecisionChunkIDs) > 0 {
		parts = append(parts, fmt.Sprintf("decision=%d", len(summary.DecisionChunkIDs)))
	}
	if len(summary.FailedChunkIDs) > 0 {
		parts = append(parts, fmt.Sprintf("failed=%d", len(summary.FailedChunkIDs)))
	}
	return strings.Join(parts, " ")
}

func cloneRunStatus(status RunStatus) RunStatus {
	out := status
	if status.ChunkStatuses != nil {
		out.ChunkStatuses = make(map[string]ChunkPhase, len(status.ChunkStatuses))
		for key, value := range status.ChunkStatuses {
			out.ChunkStatuses[key] = value
		}
	}
	if status.Error != nil {
		errCopy := *status.Error
		if status.Error.Details != nil {
			errCopy.Details = cloneAnyMap(status.Error.Details)
		}
		out.Error = &errCopy
	}
	return out
}

func cloneChunkExecutionResult(result ChunkExecutionResult) ChunkExecutionResult {
	out := result
	out.Notes = append([]string(nil), result.Notes...)
	if result.Values != nil {
		out.Values = make(map[string]string, len(result.Values))
		for key, value := range result.Values {
			out.Values[key] = value
		}
	}
	if result.Vars != nil {
		out.Vars = make(map[string]satis.RuntimeBinding, len(result.Vars))
		for key, value := range result.Vars {
			cloned := value
			if value.Texts != nil {
				cloned.Texts = append([]string(nil), value.Texts...)
			}
			if value.Object != nil {
				ref := *value.Object
				cloned.Object = &ref
			}
			if value.Objects != nil {
				cloned.Objects = append([]vfs.FileRef(nil), value.Objects...)
			}
			if value.Conversation != nil {
				cloned.Conversation = append([]satis.ConversationMessage(nil), value.Conversation...)
			}
			out.Vars[key] = cloned
		}
	}
	out.Objects = cloneAnyMap(result.Objects)
	out.SupplementaryInfo = cloneAnyMap(result.SupplementaryInfo)
	if result.Error != nil {
		errCopy := *result.Error
		if result.Error.Details != nil {
			errCopy.Details = cloneAnyMap(result.Error.Details)
		}
		out.Error = &errCopy
	}
	if len(result.ArtifactsEmitted) > 0 {
		out.ArtifactsEmitted = append([]ArtifactSpec(nil), result.ArtifactsEmitted...)
	}
	out.Control = cloneAnyMap(result.Control)
	return out
}

func cloneWorkflowRegistry(src map[string]WorkflowBindingSnapshot) map[string]WorkflowBindingSnapshot {
	if src == nil {
		return nil
	}
	out := make(map[string]WorkflowBindingSnapshot, len(src))
	for key, value := range src {
		out[key] = WorkflowBindingSnapshot{
			Name:      value.Name,
			Kind:      value.Kind,
			Source:    value.Source,
			Version:   value.Version,
			Binding:   cloneRuntimeBinding(value.Binding),
			UpdatedAt: value.UpdatedAt,
		}
	}
	return out
}

func snapshotRunStableState(record *runRecord) *runStableStateSnapshot {
	if record == nil {
		return nil
	}
	return nil
}
