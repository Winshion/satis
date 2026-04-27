package bridge

import (
	"time"

	"satis/satis"
)

// RunStatus is a snapshot returned by gopy.get_run / gopy.inspect_run.
type RunStatus struct {
	ProtocolVersion int                   `json:"protocol_version"`
	RunID           string                `json:"run_id"`
	PlanID          string                `json:"plan_id"`
	Status          RunPhase              `json:"status"`
	ChunkStatuses   map[string]ChunkPhase `json:"chunk_statuses"`
	StartedAt       time.Time             `json:"started_at"`
	UpdatedAt       time.Time             `json:"updated_at"`
	Error           *StructuredError      `json:"error,omitempty"`
}

// RunEvent is one event in gopy.stream_run_events.
type RunEvent struct {
	ProtocolVersion int            `json:"protocol_version"`
	EventID         string         `json:"event_id"`
	RunID           string         `json:"run_id"`
	ChunkID         *string        `json:"chunk_id,omitempty"`
	TS              time.Time      `json:"ts"`
	Type            RunEventType   `json:"type"`
	Payload         map[string]any `json:"payload"`
}

// ChunkExecutionResult is the persisted outcome of one chunk run (inspect_chunk).
type ChunkExecutionResult struct {
	ChunkID    string                          `json:"chunk_id"`
	Kind       string                          `json:"kind,omitempty"`
	Status     ChunkPhase                      `json:"status"`
	Substatus  string                          `json:"substatus,omitempty"`
	StartedAt  *time.Time                      `json:"started_at,omitempty"`
	FinishedAt *time.Time                      `json:"finished_at,omitempty"`
	Objects    map[string]any                  `json:"objects,omitempty"`
	Values     map[string]string               `json:"values,omitempty"`
	Vars       map[string]satis.RuntimeBinding `json:"vars,omitempty"`
	Notes      []string                        `json:"notes,omitempty"`
	// SupplementaryInfo captures planner-facing metadata for inspect/debug tracing.
	SupplementaryInfo map[string]any `json:"supplementary_info,omitempty"`
	// ArtifactsEmitted lists file handles produced by this chunk (from execution objects).
	ArtifactsEmitted []ArtifactSpec   `json:"artifacts_emitted,omitempty"`
	Error            *StructuredError `json:"error,omitempty"`
	// RepeatResults holds per-iteration results when chunk.Repeat is set.
	RepeatResults []map[string]any `json:"repeat_results,omitempty"`
	Control       map[string]any   `json:"control,omitempty"`
}

// InspectObjectParams is the input for gopy.inspect_object.
type InspectObjectParams struct {
	FileID      string `json:"file_id,omitempty"`
	VirtualPath string `json:"virtual_path,omitempty"`
}
