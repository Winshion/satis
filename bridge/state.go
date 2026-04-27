package bridge

// RunPhase is the scheduler-level status of a single plan execution.
type RunPhase string

const (
	RunPhasePending   RunPhase = "pending"
	RunPhaseRunning   RunPhase = "running"
	RunPhasePaused    RunPhase = "paused"
	RunPhasePlanningPending RunPhase = "planning_pending"
	RunPhaseCompleted RunPhase = "completed"
	RunPhaseFailed    RunPhase = "failed"
	RunPhaseCancelled RunPhase = "cancelled"
)

// ChunkPhase is the status of one node inside a run.
type ChunkPhase string

const (
	ChunkPhasePending   ChunkPhase = "pending"
	ChunkPhaseReady     ChunkPhase = "ready"
	ChunkPhaseRunning   ChunkPhase = "running"
	ChunkPhaseSucceeded ChunkPhase = "succeeded"
	ChunkPhaseFailed    ChunkPhase = "failed"
	ChunkPhaseBlocked   ChunkPhase = "blocked"
	ChunkPhaseCancelled ChunkPhase = "cancelled"
)

// ErrorStage classifies where a StructuredError originated.
type ErrorStage string

const (
	StagePlanning   ErrorStage = "planning"
	StageValidation ErrorStage = "validation"
	StageScheduling ErrorStage = "scheduling"
	StageExecution  ErrorStage = "execution"
	StageVFS        ErrorStage = "vfs"
	StageInvoke     ErrorStage = "invoke"
)

// RunEventType is the v1 fixed set of event.type values.
type RunEventType string

const (
	EventRunStarted      RunEventType = "run_started"
	EventChunkReady      RunEventType = "chunk_ready"
	EventChunkStarted    RunEventType = "chunk_started"
	EventChunkSucceeded  RunEventType = "chunk_succeeded"
	EventChunkFailed     RunEventType = "chunk_failed"
	EventChunkBlocked    RunEventType = "chunk_blocked"
	EventArtifactEmitted RunEventType = "artifact_emitted"
	EventObjectChanged   RunEventType = "object_changed"
	EventRunPlanningPending RunEventType = "run_planning_pending"
	EventRunCompleted    RunEventType = "run_completed"
	EventRunFailed       RunEventType = "run_failed"
)
