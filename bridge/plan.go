package bridge

// ChunkGraphPlan is the Python planner output for Go scheduling (v1).
type ChunkGraphPlan struct {
	ProtocolVersion   int            `json:"protocol_version"`
	PlanID            string         `json:"plan_id"`
	IntentID          string         `json:"intent_id"`
	IntentDescription string         `json:"intent_description"`
	PlanDescription   string         `json:"plan_description"`
	Goal              string         `json:"goal"`
	Chunks            []PlanChunk    `json:"chunks"`
	Edges             []PlanEdge     `json:"edges"`
	EntryChunks       []string       `json:"entry_chunks"`
	Artifacts         []ArtifactSpec `json:"artifacts,omitempty"`
	PlannerNotes      []string       `json:"planner_notes,omitempty"`
	Policies          *PlanPolicies  `json:"policies,omitempty"`
}

// PlanChunk is one node; execution body is SatisIL v0 text for v1.
type PlanChunk struct {
	ChunkID        string         `json:"chunk_id"`
	Kind           string         `json:"kind"`
	Description    string         `json:"description"`
	Source         ChunkSource    `json:"source"`
	Decision       *DecisionSpec  `json:"decision,omitempty"`
	Inputs         map[string]any `json:"inputs,omitempty"`
	Outputs        map[string]any `json:"outputs,omitempty"`
	DependsOn      []string       `json:"depends_on,omitempty"`
	RetryPolicy    *RetryPolicy   `json:"retry_policy,omitempty"`
	TimeoutMS      int            `json:"timeout_ms,omitempty"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
	ApprovalMode   string         `json:"approval_mode,omitempty"`
	Repeat         *ChunkRepeat   `json:"repeat,omitempty"`
}

// ChunkRepeat configures template-based repeated execution of a single chunk.
// Instead of storing N identical chunks, one chunk template is expanded at runtime
// over input_paths, producing one output per item.
type ChunkRepeat struct {
	// Mode controls expansion strategy:
	//   "per_item"   – one SatisIL execution per input_path, template placeholders replaced
	//   "batch"      – single execution, concurrent invoke handles parallelism internally
	Mode string `json:"mode"`
	// InputPaths lists the files to process. Each path becomes a separate iteration.
	InputPaths []string `json:"input_paths"`
	// OutputTmpl is a Go text/template string for the output virtual path.
	// Fields: .Index (int), .IndexPadded (4-digit string), .Basename (input file name).
	// Example: "/out/{{.Index}}.txt" or "/out/{{.IndexPadded}}_{{.Basename}}.txt".
	OutputTmpl string `json:"output_template"`
	// Prompt is the uniform prompt used for repeat batch mode (concurrent invoke over input list).
	Prompt string `json:"prompt,omitempty"`
}

// ChunkSource carries executable source; v1 uses satis_text only.
type ChunkSource struct {
	SatisText string `json:"satis_text"`
	Format    string `json:"format,omitempty"`
}

// PlanEdge means To must run after From (To depends on From).
type PlanEdge struct {
	FromChunkID string      `json:"from_chunk_id"`
	ToChunkID   string      `json:"to_chunk_id"`
	EdgeKind    string      `json:"edge_kind,omitempty"`
	Branch      string      `json:"branch,omitempty"`
	LoopPolicy  *LoopPolicy `json:"loop_policy,omitempty"`
}

type InteractionContextBinding struct {
	Name      string `json:"name"`
	FromInput string `json:"from_input,omitempty"`
}

type LLMInteraction struct {
	SystemPromptRef    string                      `json:"system_prompt_ref,omitempty"`
	DeveloperPromptRef string                      `json:"developer_prompt_ref,omitempty"`
	UserPromptTemplate string                      `json:"user_prompt_template,omitempty"`
	ContextBindings    []InteractionContextBinding `json:"context_bindings,omitempty"`
	Temperature        float64                     `json:"temperature,omitempty"`
	MaxTokens          int                         `json:"max_tokens,omitempty"`
}

type HumanInteraction struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

type InteractionSpec struct {
	Mode  string            `json:"mode,omitempty"`
	LLM   *LLMInteraction   `json:"llm,omitempty"`
	Human *HumanInteraction `json:"human,omitempty"`
}

type DecisionSpec struct {
	AllowedBranches []string         `json:"allowed_branches,omitempty"`
	DefaultBranch   string           `json:"default_branch,omitempty"`
	Interaction     *InteractionSpec `json:"interaction,omitempty"`
}

type LoopPolicy struct {
	MaxIterations int    `json:"max_iterations,omitempty"`
	Notes         string `json:"notes,omitempty"`
}

type FragmentAttachRules struct {
	AllowBackEdges bool `json:"allow_back_edges,omitempty"`
}

type PlanFragment struct {
	Chunks      []PlanChunk          `json:"chunks,omitempty"`
	Edges       []PlanEdge           `json:"edges,omitempty"`
	EntryChunks []string             `json:"entry_chunks,omitempty"`
	EntryNode   string               `json:"entry_node,omitempty"`
	NewNodes    []PlanChunk          `json:"new_nodes,omitempty"`
	NewEdges    []PlanEdge           `json:"new_edges,omitempty"`
	AttachRules *FragmentAttachRules `json:"attach_rules,omitempty"`
}

// ArtifactSpec is an optional declared output handle for planning / UI.
type ArtifactSpec struct {
	Name        string `json:"name,omitempty"`
	VirtualPath string `json:"virtual_path,omitempty"`
	FileID      string `json:"file_id,omitempty"`
}

// PlanPolicies holds cross-chunk execution policy.
type PlanPolicies struct {
	MaxParallelChunks      int  `json:"max_parallel_chunks,omitempty"`
	FailFast               bool `json:"fail_fast,omitempty"`
	RequireApprovalForPlan bool `json:"require_approval_for_plan,omitempty"`
}

// SubmitChunkGraphResult is returned by gopy.submit_chunk_graph.
type SubmitChunkGraphResult struct {
	Accepted         bool              `json:"accepted"`
	ValidationErrors []ValidationIssue `json:"validation_errors"`
	NormalizedPlan   *ChunkGraphPlan   `json:"normalized_plan,omitempty"`
}

// ValidationIssue is a structural validation problem (not a runtime failure).
type ValidationIssue struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Field   string         `json:"field,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

// StructuredError is returned on failures that should be machine-readable.
type StructuredError struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Stage     ErrorStage     `json:"stage"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
}

// RetryPolicy is optional per-chunk retry hints for the scheduler.
type RetryPolicy struct {
	MaxAttempts int `json:"max_attempts,omitempty"`
	BackoffMS   int `json:"backoff_ms,omitempty"`
}

// ExecutionOptions is passed to gopy.start_run (v1 minimal).
type ExecutionOptions struct {
	DryRun bool `json:"dry_run,omitempty"`
}
