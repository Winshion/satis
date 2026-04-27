package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"satis/bridge"
	"satis/vfs"
)

type Backend interface {
	ListVirtualDir(ctx context.Context, virtualPath string) ([]vfs.DirEntry, error)
	ResolveVirtualPath(path string) string
	ReadVirtualText(ctx context.Context, path string) (string, error)
	WriteVirtualText(ctx context.Context, path string, text string) error
	SubmitPlan(ctx context.Context, plan *bridge.ChunkGraphPlan) (bridge.SubmitChunkGraphResult, error)
	StartPlanRun(ctx context.Context, planID string, options bridge.ExecutionOptions) (bridge.RunStatus, error)
	InspectPlanRun(ctx context.Context, runID string) (bridge.InspectRunResult, error)
	InspectRunChunk(ctx context.Context, runID string, chunkID string) (bridge.ChunkExecutionResult, error)
	StreamPlanRunEvents(ctx context.Context, runID string) (bridge.StreamRunEventsResult, error)
	ContinuePlanRun(ctx context.Context, runID string, fragment PlanFragment) (bridge.RunStatus, error)
	ContinuePlanRunLLM(ctx context.Context, runID string, prompt string) (bridge.RunStatus, error)
	FinishPlanRun(ctx context.Context, runID string) (bridge.RunStatus, error)
}

type Model struct {
	Path            string
	ResolvedPath    string
	Plan            *bridge.ChunkGraphPlan
	SelectedChunkID string
	Dirty           bool
}

const chunkDescriptionMetaKey = "description"

type HandoffRow struct {
	Key               string
	Port              string
	FromStep          string
	VarName           string
	SupplementaryJSON string
}

const defaultNewChunkKind = "task"

func LoadModel(ctx context.Context, backend Backend, path string) (*Model, error) {
	resolvedPath := backend.ResolveVirtualPath(path)
	text, err := backend.ReadVirtualText(ctx, resolvedPath)
	if err != nil {
		return nil, err
	}
	plan, err := ParsePlan(text)
	if err != nil {
		return nil, err
	}
	model := &Model{
		Path:         path,
		ResolvedPath: resolvedPath,
		Plan:         plan,
	}
	if err := validateWorkspaceChunkIDs(ctx, backend, resolvedPath, plan); err != nil {
		return nil, err
	}
	model.SelectedChunkID = defaultSelectedChunkID(plan)
	return model, nil
}

// LoadModelLenient reads a plan file and unmarshals JSON without validating the chunk graph.
// Use this for workbench editing so invalid drafts can be opened, saved, and reloaded.
func LoadModelLenient(ctx context.Context, backend Backend, path string) (*Model, error) {
	resolvedPath := backend.ResolveVirtualPath(path)
	text, err := backend.ReadVirtualText(ctx, resolvedPath)
	if err != nil {
		return nil, err
	}
	plan, err := ParsePlanDocument(text)
	if err != nil {
		return nil, err
	}
	model := &Model{
		Path:         path,
		ResolvedPath: resolvedPath,
		Plan:         plan,
	}
	if err := validateWorkspaceChunkIDs(ctx, backend, resolvedPath, plan); err != nil {
		return nil, err
	}
	model.SelectedChunkID = defaultSelectedChunkID(plan)
	return model, nil
}

// ParsePlanDocument unmarshals plan JSON only; it does not run ValidateChunkGraphPlan.
func ParsePlanDocument(text string) (*bridge.ChunkGraphPlan, error) {
	var plan bridge.ChunkGraphPlan
	if err := json.Unmarshal([]byte(text), &plan); err != nil {
		return nil, fmt.Errorf("parse plan json: %w", err)
	}
	syncChunkDescriptions(&plan)
	return &plan, nil
}

func ParsePlan(text string) (*bridge.ChunkGraphPlan, error) {
	var plan bridge.ChunkGraphPlan
	if err := json.Unmarshal([]byte(text), &plan); err != nil {
		return nil, fmt.Errorf("parse plan json: %w", err)
	}
	syncChunkDescriptions(&plan)
	result := bridge.ValidateChunkGraphPlan(&plan)
	if !result.Accepted || result.NormalizedPlan == nil {
		return nil, formatValidationIssues(result.ValidationErrors)
	}
	return result.NormalizedPlan, nil
}

func syncChunkDescriptions(plan *bridge.ChunkGraphPlan) {
	if plan == nil {
		return
	}
	for i := range plan.Chunks {
		chunk := &plan.Chunks[i]
		headerDescription := strings.TrimSpace(extractChunkHeaderValue(chunk.Source.SatisText, chunkDescriptionMetaKey))
		switch {
		case headerDescription != "":
			chunk.Description = headerDescription
		case strings.TrimSpace(chunk.Description) != "":
			chunk.Source.SatisText = setChunkHeaderValue(chunk.Source.SatisText, chunkDescriptionMetaKey, strings.TrimSpace(chunk.Description))
		}
	}
}

func (m *Model) Marshal() (string, error) {
	if m == nil || m.Plan == nil {
		return "", fmt.Errorf("workbench model is not initialized")
	}
	data, err := json.MarshalIndent(m.Plan, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (m *Model) Save(ctx context.Context, backend Backend) error {
	if err := validateWorkspaceChunkIDs(ctx, backend, m.ResolvedPath, m.Plan); err != nil {
		return err
	}
	text, err := m.Marshal()
	if err != nil {
		return err
	}
	if err := backend.WriteVirtualText(ctx, m.ResolvedPath, text); err != nil {
		return err
	}
	m.Dirty = false
	return nil
}

func validateWorkspaceChunkIDs(ctx context.Context, backend Backend, planPath string, plan *bridge.ChunkGraphPlan) error {
	if backend == nil || plan == nil {
		return nil
	}
	dir := path.Dir(strings.TrimSpace(planPath))
	entries, err := backend.ListVirtualDir(ctx, dir)
	if err != nil {
		return nil
	}
	currentPath := path.Clean(strings.TrimSpace(planPath))
	owners := make(map[string]string, len(plan.Chunks))
	for _, chunk := range plan.Chunks {
		chunkID := strings.TrimSpace(chunk.ChunkID)
		if chunkID == "" {
			continue
		}
		owners[chunkID] = currentPath
	}
	for _, entry := range entries {
		entryPath := path.Clean(strings.TrimSpace(entry.VirtualPath))
		name := strings.TrimSpace(entry.Name)
		if entry.Kind == vfs.FileKindDirectory || entryPath == currentPath {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(name), ".json") || strings.HasPrefix(name, ".") {
			continue
		}
		text, readErr := backend.ReadVirtualText(ctx, entryPath)
		if readErr != nil || strings.TrimSpace(text) == "" {
			continue
		}
		otherPlan, parseErr := ParsePlanDocument(text)
		if parseErr != nil || len(otherPlan.Chunks) == 0 {
			continue
		}
		for _, chunk := range otherPlan.Chunks {
			chunkID := strings.TrimSpace(chunk.ChunkID)
			if chunkID == "" {
				continue
			}
			if ownerPath, ok := owners[chunkID]; ok && ownerPath != entryPath {
				return fmt.Errorf("workspace duplicate chunk_id %q in %s and %s", chunkID, ownerPath, entryPath)
			}
			owners[chunkID] = entryPath
		}
	}
	return nil
}

func (m *Model) snapshotPlan() (*bridge.ChunkGraphPlan, error) {
	if m == nil || m.Plan == nil {
		return nil, fmt.Errorf("workbench model is not initialized")
	}
	data, err := json.Marshal(m.Plan)
	if err != nil {
		return nil, err
	}
	var clone bridge.ChunkGraphPlan
	if err := json.Unmarshal(data, &clone); err != nil {
		return nil, err
	}
	return &clone, nil
}

func (m *Model) ValidateAndNormalize() error {
	if m == nil || m.Plan == nil {
		return fmt.Errorf("workbench model is not initialized")
	}
	result := bridge.ValidateChunkGraphPlan(m.Plan)
	if !result.Accepted || result.NormalizedPlan == nil {
		return formatValidationIssues(result.ValidationErrors)
	}
	m.Plan = result.NormalizedPlan
	if m.SelectedChunkID == "" {
		m.SelectedChunkID = defaultSelectedChunkID(m.Plan)
	}
	if m.ChunkByID(m.SelectedChunkID) == nil {
		m.SelectedChunkID = defaultSelectedChunkID(m.Plan)
	}
	return nil
}

func (m *Model) ChunkByID(chunkID string) *bridge.PlanChunk {
	if m == nil || m.Plan == nil {
		return nil
	}
	for i := range m.Plan.Chunks {
		if m.Plan.Chunks[i].ChunkID == chunkID {
			return &m.Plan.Chunks[i]
		}
	}
	return nil
}

func (m *Model) SetSelectedChunk(chunkID string) {
	if m == nil {
		return
	}
	m.SelectedChunkID = chunkID
}

func (m *Model) SetChunkText(chunkID string, text string) error {
	chunk := m.ChunkByID(chunkID)
	if chunk == nil {
		return fmt.Errorf("unknown chunk %q", chunkID)
	}
	chunk.Source.SatisText = text
	if description := strings.TrimSpace(extractChunkHeaderValue(text, chunkDescriptionMetaKey)); description != "" {
		chunk.Description = description
	}
	m.Dirty = true
	return nil
}

func (m *Model) SetIntentDescription(text string) error {
	if m == nil || m.Plan == nil {
		return fmt.Errorf("workbench model is not initialized")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("intent description cannot be empty")
	}
	m.Plan.IntentDescription = text
	m.Dirty = true
	return nil
}

func (m *Model) SetPlanDescription(text string) error {
	if m == nil || m.Plan == nil {
		return fmt.Errorf("workbench model is not initialized")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("plan description cannot be empty")
	}
	m.Plan.PlanDescription = text
	m.Dirty = true
	return nil
}

func (m *Model) SetChunkDescription(chunkID string, text string) error {
	chunk := m.ChunkByID(chunkID)
	if chunk == nil {
		return fmt.Errorf("unknown chunk %q", chunkID)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("chunk description cannot be empty")
	}
	chunk.Description = text
	chunk.Source.SatisText = setChunkHeaderValue(chunk.Source.SatisText, chunkDescriptionMetaKey, text)
	m.Dirty = true
	return nil
}

func (m *Model) SetChunkKind(chunkID string, kind string) error {
	chunk := m.ChunkByID(chunkID)
	if chunk == nil {
		return fmt.Errorf("unknown chunk %q", chunkID)
	}
	currentKind := strings.ToLower(strings.TrimSpace(chunk.Kind))
	intentID := strings.TrimSpace(extractChunkHeaderValue(chunk.Source.SatisText, "intent_uid"))
	if intentID == "" {
		intentID = strings.TrimSpace(m.Plan.IntentID)
	}
	if intentID == "" {
		intentID = "intent_workbench"
	}
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "task", "satis":
		chunk.Kind = "task"
		chunk.Decision = nil
		chunk.Source.Format = "satis_v1"
		// Switching back from control nodes must always restore task headers,
		// even if the prior source keeps non-empty structured text.
		if currentKind != "task" || strings.TrimSpace(chunk.Source.SatisText) == "" {
			if strings.TrimSpace(chunk.Description) == "" {
				chunk.Description = "执行该 chunk 的默认任务"
			}
			chunk.Source.SatisText = fmt.Sprintf("chunk_id: %s\nintent_uid: %s\ndescription: %s\n\nPwd\n", chunk.ChunkID, intentID, chunk.Description)
		} else {
			chunk.Source.SatisText = setChunkHeaderValue(chunk.Source.SatisText, "chunk_id", chunk.ChunkID)
			chunk.Source.SatisText = setChunkHeaderValue(chunk.Source.SatisText, "intent_uid", intentID)
			chunk.Source.SatisText = setChunkHeaderValue(chunk.Source.SatisText, chunkDescriptionMetaKey, chunk.Description)
		}
	case "decision":
		chunk.Kind = "decision"
		chunk.Source = bridge.ChunkSource{Format: "satis_v1", SatisText: ""}
		chunk.Decision = &bridge.DecisionSpec{
			AllowedBranches: []string{"yes", "no"},
			DefaultBranch:   "yes",
			Interaction: &bridge.InteractionSpec{
				Mode: "human",
				Human: &bridge.HumanInteraction{
					Title: "Decision",
				},
			},
		}
	default:
		return fmt.Errorf("unsupported chunk kind %q in workbench; supported kinds: task, decision", kind)
	}
	m.Dirty = true
	return nil
}

func (m *Model) AddDecisionBranch(sourceChunkID string, branch string, targetChunkID string) error {
	if m == nil || m.Plan == nil {
		return fmt.Errorf("workbench model is not initialized")
	}
	sourceChunkID = strings.TrimSpace(sourceChunkID)
	targetChunkID = strings.TrimSpace(targetChunkID)
	branch = strings.TrimSpace(branch)
	if sourceChunkID == "" {
		return fmt.Errorf("missing source chunk_id")
	}
	if targetChunkID == "" {
		return fmt.Errorf("missing target chunk_id")
	}
	if branch == "" {
		return fmt.Errorf("missing branch name")
	}
	source := m.ChunkByID(sourceChunkID)
	if source == nil {
		return fmt.Errorf("unknown source chunk %q", sourceChunkID)
	}
	if normalizedKind := strings.ToLower(strings.TrimSpace(source.Kind)); normalizedKind != "decision" {
		return fmt.Errorf("source chunk %q is not a decision node", sourceChunkID)
	}
	if m.ChunkByID(targetChunkID) == nil {
		return fmt.Errorf("unknown target chunk %q", targetChunkID)
	}
	if source.Decision == nil {
		source.Decision = &bridge.DecisionSpec{}
	}
	if !containsString(source.Decision.AllowedBranches, branch) {
		source.Decision.AllowedBranches = append(source.Decision.AllowedBranches, branch)
		sort.Strings(source.Decision.AllowedBranches)
	}
	if strings.TrimSpace(source.Decision.DefaultBranch) == "" {
		source.Decision.DefaultBranch = branch
	}
	filtered := make([]bridge.PlanEdge, 0, len(m.Plan.Edges)+1)
	for _, edge := range m.Plan.Edges {
		if edge.FromChunkID == sourceChunkID &&
			(strings.EqualFold(strings.TrimSpace(edge.EdgeKind), "branch") ||
				strings.EqualFold(strings.TrimSpace(edge.EdgeKind), "loop_back")) &&
			strings.TrimSpace(edge.Branch) == branch {
			continue
		}
		filtered = append(filtered, edge)
	}
	edgeKind := classifyDecisionEdgeKind(m.Plan, sourceChunkID, targetChunkID)
	filtered = append(filtered, bridge.PlanEdge{
		FromChunkID: sourceChunkID,
		ToChunkID:   targetChunkID,
		EdgeKind:    edgeKind,
		Branch:      branch,
	})
	m.Plan.Edges = filtered
	m.Dirty = true
	return nil
}

func (m *Model) SetDecisionDefaultBranch(chunkID string, branch string) error {
	chunk := m.ChunkByID(strings.TrimSpace(chunkID))
	if chunk == nil {
		return fmt.Errorf("unknown chunk %q", chunkID)
	}
	if strings.ToLower(strings.TrimSpace(chunk.Kind)) != "decision" || chunk.Decision == nil {
		return fmt.Errorf("chunk %q is not a decision node", chunkID)
	}
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return fmt.Errorf("default branch cannot be empty")
	}
	if !containsString(chunk.Decision.AllowedBranches, branch) {
		return fmt.Errorf("default branch %q is not declared in allowed_branches", branch)
	}
	chunk.Decision.DefaultBranch = branch
	m.Dirty = true
	return nil
}

func (m *Model) SetDecisionMode(chunkID string, mode string) error {
	chunk := m.ChunkByID(strings.TrimSpace(chunkID))
	if chunk == nil {
		return fmt.Errorf("unknown chunk %q", chunkID)
	}
	if strings.ToLower(strings.TrimSpace(chunk.Kind)) != "decision" {
		return fmt.Errorf("chunk %q is not a decision node", chunkID)
	}
	if chunk.Decision == nil {
		chunk.Decision = &bridge.DecisionSpec{}
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "human":
		if chunk.Decision.Interaction == nil {
			chunk.Decision.Interaction = &bridge.InteractionSpec{}
		}
		chunk.Decision.Interaction.Mode = mode
		if chunk.Decision.Interaction.Human == nil {
			chunk.Decision.Interaction.Human = &bridge.HumanInteraction{Title: "Decision"}
		}
	case "llm", "llm_then_human":
		if chunk.Decision.Interaction == nil {
			chunk.Decision.Interaction = &bridge.InteractionSpec{}
		}
		chunk.Decision.Interaction.Mode = mode
		if chunk.Decision.Interaction.LLM == nil {
			chunk.Decision.Interaction.LLM = &bridge.LLMInteraction{
				UserPromptTemplate: "choose branch from {{allowed_branches}}",
			}
		}
	default:
		return fmt.Errorf("unsupported decision mode %q", mode)
	}
	m.Dirty = true
	return nil
}

func (m *Model) SetDecisionPromptTemplate(chunkID string, prompt string) error {
	chunk := m.ChunkByID(strings.TrimSpace(chunkID))
	if chunk == nil {
		return fmt.Errorf("unknown chunk %q", chunkID)
	}
	if strings.ToLower(strings.TrimSpace(chunk.Kind)) != "decision" {
		return fmt.Errorf("chunk %q is not a decision node", chunkID)
	}
	if chunk.Decision == nil {
		chunk.Decision = &bridge.DecisionSpec{}
	}
	if chunk.Decision.Interaction == nil {
		chunk.Decision.Interaction = &bridge.InteractionSpec{Mode: "llm"}
	}
	if chunk.Decision.Interaction.LLM == nil {
		chunk.Decision.Interaction.LLM = &bridge.LLMInteraction{}
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return fmt.Errorf("decision prompt cannot be empty")
	}
	chunk.Decision.Interaction.LLM.UserPromptTemplate = prompt
	if chunk.Decision.Interaction.Mode == "" {
		chunk.Decision.Interaction.Mode = "llm"
	}
	m.Dirty = true
	return nil
}

func (m *Model) SetDecisionHumanTitle(chunkID string, title string) error {
	chunk := m.ChunkByID(strings.TrimSpace(chunkID))
	if chunk == nil {
		return fmt.Errorf("unknown chunk %q", chunkID)
	}
	if strings.ToLower(strings.TrimSpace(chunk.Kind)) != "decision" {
		return fmt.Errorf("chunk %q is not a decision node", chunkID)
	}
	if chunk.Decision == nil {
		chunk.Decision = &bridge.DecisionSpec{}
	}
	if chunk.Decision.Interaction == nil {
		chunk.Decision.Interaction = &bridge.InteractionSpec{Mode: "human"}
	}
	if chunk.Decision.Interaction.Human == nil {
		chunk.Decision.Interaction.Human = &bridge.HumanInteraction{}
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("decision title cannot be empty")
	}
	chunk.Decision.Interaction.Human.Title = title
	if chunk.Decision.Interaction.Mode == "" {
		chunk.Decision.Interaction.Mode = "human"
	}
	m.Dirty = true
	return nil
}

func (m *Model) AttachPlanFragment(fragment PlanFragment) error {
	if m == nil || m.Plan == nil {
		return fmt.Errorf("workbench model is not initialized")
	}
	if len(fragment.NewNodes) > 0 {
		fragment.Chunks = append([]bridge.PlanChunk(nil), fragment.NewNodes...)
	}
	if len(fragment.NewEdges) > 0 {
		fragment.Edges = append([]bridge.PlanEdge(nil), fragment.NewEdges...)
	}
	if strings.TrimSpace(fragment.EntryNode) != "" {
		fragment.EntryChunks = []string{strings.TrimSpace(fragment.EntryNode)}
	}
	existing := make(map[string]struct{}, len(m.Plan.Chunks))
	for _, chunk := range m.Plan.Chunks {
		existing[chunk.ChunkID] = struct{}{}
	}
	for _, chunk := range fragment.Chunks {
		if _, ok := existing[chunk.ChunkID]; ok {
			return fmt.Errorf("fragment chunk_id %q already exists", chunk.ChunkID)
		}
	}
	m.Plan.Chunks = append(m.Plan.Chunks, fragment.Chunks...)
	m.Plan.Edges = append(m.Plan.Edges, fragment.Edges...)
	m.Plan.PlannerNotes = append(m.Plan.PlannerNotes, fmt.Sprintf("workbench_fragment chunks=%d", len(fragment.Chunks)))
	m.Dirty = true
	return nil
}

type PlanFragment = bridge.PlanFragment

func (m *Model) AddChunk() (string, error) {
	if m == nil || m.Plan == nil {
		return "", fmt.Errorf("workbench model is not initialized")
	}
	chunkID := nextChunkID(m.Plan)
	chunkPort := nextChunkPort(m.Plan)
	intentID := strings.TrimSpace(m.Plan.IntentID)
	if intentID == "" {
		intentID = "intent_workbench"
	}
	newChunk := bridge.PlanChunk{
		ChunkID:     chunkID,
		Kind:        defaultNewChunkKind,
		Description: "执行该 chunk 的默认任务",
		Source: bridge.ChunkSource{
			Format: "satis_v1",
			SatisText: fmt.Sprintf(
				"chunk_id: %s\nintent_uid: %s\ndescription: %s\nchunk_port: %s\n\nPwd\n",
				chunkID,
				intentID,
				"执行该 chunk 的默认任务",
				chunkPort,
			),
		},
	}
	parentChunkID := strings.TrimSpace(m.SelectedChunkID)
	if parentChunkID == "" || m.ChunkByID(parentChunkID) == nil {
		parentChunkID = defaultSelectedChunkID(m.Plan)
	}
	if parentChunkID != "" {
		newChunk.DependsOn = []string{parentChunkID}
	}
	m.Plan.Chunks = append(m.Plan.Chunks, newChunk)
	if parentChunkID == "" {
		m.Plan.EntryChunks = []string{chunkID}
	} else if !containsEdge(m.Plan.Edges, parentChunkID, chunkID) {
		m.Plan.Edges = append(m.Plan.Edges, bridge.PlanEdge{
			FromChunkID: parentChunkID,
			ToChunkID:   chunkID,
			EdgeKind:    "control",
		})
	}
	m.SelectedChunkID = chunkID
	m.Dirty = true
	return chunkID, nil
}

// RemoveChunk deletes a chunk, drops edges touching it, removes any downstream
// handoff_inputs that referenced it as from_step, then rebuilds topology via SyncEdgesFromHandoffs.
func (m *Model) RemoveChunk(chunkID string) error {
	if m == nil || m.Plan == nil {
		return fmt.Errorf("workbench model is not initialized")
	}
	chunkID = strings.TrimSpace(chunkID)
	if chunkID == "" {
		return fmt.Errorf("chunk id required")
	}
	if m.ChunkByID(chunkID) == nil {
		return fmt.Errorf("unknown chunk %q", chunkID)
	}
	if len(m.Plan.Chunks) <= 1 {
		return fmt.Errorf("cannot remove the last chunk")
	}
	out := m.Plan.Chunks[:0]
	for _, c := range m.Plan.Chunks {
		if c.ChunkID != chunkID {
			out = append(out, c)
		}
	}
	m.Plan.Chunks = out

	removeHandoffsReferencingFromStep(m.Plan, chunkID)

	filtered := m.Plan.Edges[:0]
	for _, e := range m.Plan.Edges {
		if e.FromChunkID == chunkID || e.ToChunkID == chunkID {
			continue
		}
		filtered = append(filtered, e)
	}
	m.Plan.Edges = filtered

	if err := m.SyncEdgesFromHandoffs(); err != nil {
		return err
	}
	if m.SelectedChunkID == chunkID {
		m.SelectedChunkID = ""
	}
	m.Dirty = true
	return nil
}

func removeHandoffsReferencingFromStep(plan *bridge.ChunkGraphPlan, deletedFromStep string) {
	if plan == nil {
		return
	}
	deletedFromStep = strings.TrimSpace(deletedFromStep)
	for i := range plan.Chunks {
		c := &plan.Chunks[i]
		if c.Inputs == nil {
			continue
		}
		rawInputs, _ := c.Inputs["handoff_inputs"].(map[string]any)
		if len(rawInputs) == 0 {
			continue
		}
		for port, item := range rawInputs {
			spec, ok := item.(map[string]any)
			if !ok {
				continue
			}
			fs, _ := spec["from_step"].(string)
			if strings.TrimSpace(fs) == deletedFromStep {
				delete(rawInputs, port)
			}
		}
		if len(rawInputs) == 0 {
			delete(c.Inputs, "handoff_inputs")
		}
	}
}

func (m *Model) HandoffRows(chunkID string) ([]HandoffRow, error) {
	chunk := m.ChunkByID(chunkID)
	if chunk == nil {
		return nil, fmt.Errorf("unknown chunk %q", chunkID)
	}
	return handoffRows(chunk), nil
}

func (m *Model) SetHandoffRow(chunkID string, row HandoffRow) error {
	return m.UpsertHandoffRow(chunkID, row.Key, row)
}

func handoffBindingKey(port string, varName string) string {
	return strings.TrimSpace(port) + "__" + strings.TrimSpace(varName)
}

func (m *Model) UpsertHandoffRow(chunkID string, previousKey string, row HandoffRow) error {
	chunk := m.ChunkByID(chunkID)
	if chunk == nil {
		return fmt.Errorf("unknown chunk %q", chunkID)
	}
	row.Port = strings.TrimSpace(row.Port)
	row.VarName = strings.TrimSpace(row.VarName)
	previousKey = strings.TrimSpace(previousKey)
	if row.Port == "" {
		return fmt.Errorf("handoff port cannot be empty")
	}
	if row.VarName == "" {
		return fmt.Errorf("handoff var cannot be empty")
	}
	row.Key = handoffBindingKey(row.Port, row.VarName)
	if chunk.Inputs == nil {
		chunk.Inputs = make(map[string]any)
	}
	rawInputs, _ := chunk.Inputs["handoff_inputs"].(map[string]any)
	if rawInputs == nil {
		rawInputs = make(map[string]any)
		chunk.Inputs["handoff_inputs"] = rawInputs
	}
	if previousKey != "" && previousKey != row.Key {
		if _, exists := rawInputs[row.Key]; exists {
			return fmt.Errorf("handoff binding %q already exists", row.Key)
		}
		delete(rawInputs, previousKey)
	}
	spec := map[string]any{}
	if strings.TrimSpace(row.FromStep) != "" {
		spec["from_step"] = strings.TrimSpace(row.FromStep)
	}
	spec["var_name"] = row.VarName
	if strings.TrimSpace(row.SupplementaryJSON) != "" {
		var supplementary map[string]any
		if err := json.Unmarshal([]byte(row.SupplementaryJSON), &supplementary); err != nil {
			return fmt.Errorf("invalid supplementary_info json: %w", err)
		}
		spec["supplementary_info"] = supplementary
	}
	rawInputs[row.Key] = spec
	m.Dirty = true
	return nil
}

func (m *Model) AddHandoffRow(chunkID string) (HandoffRow, error) {
	chunk := m.ChunkByID(chunkID)
	if chunk == nil {
		return HandoffRow{}, fmt.Errorf("unknown chunk %q", chunkID)
	}
	port := nextHandoffPort(chunk)
	row := HandoffRow{Port: port, VarName: "body", Key: handoffBindingKey(port, "body")}
	if err := m.UpsertHandoffRow(chunkID, "", row); err != nil {
		return HandoffRow{}, err
	}
	return row, nil
}

func (m *Model) RemoveHandoffRow(chunkID string, key string) error {
	chunk := m.ChunkByID(chunkID)
	if chunk == nil {
		return fmt.Errorf("unknown chunk %q", chunkID)
	}
	rawInputs, _ := chunk.Inputs["handoff_inputs"].(map[string]any)
	if len(rawInputs) == 0 {
		return nil
	}
	delete(rawInputs, strings.TrimSpace(key))
	if len(rawInputs) == 0 {
		delete(chunk.Inputs, "handoff_inputs")
	}
	m.Dirty = true
	return nil
}

func buildDerivedTopology(plan *bridge.ChunkGraphPlan) ([]bridge.PlanEdge, map[string][]string, []string, error) {
	if plan == nil {
		return nil, nil, nil, fmt.Errorf("workbench model is not initialized")
	}
	chunkIDSet := make(map[string]struct{}, len(plan.Chunks))
	for _, chunk := range plan.Chunks {
		chunkIDSet[chunk.ChunkID] = struct{}{}
	}

	edgeMap := make(map[string]bridge.PlanEdge)
	for _, edge := range plan.Edges {
		if isConditionalWorkbenchEdgeKind(edge.EdgeKind) {
			edgeMap[edgeIdentity(edge)] = edge
			continue
		}
		key := edgeKey(edge.FromChunkID, edge.ToChunkID)
		edgeMap[key] = edge
	}
	controlReachability := buildPlanControlReachability(plan, chunkIDSet)

	for _, chunk := range plan.Chunks {
		rows := handoffRows(&chunk)
		for _, row := range rows {
			fromStep := strings.TrimSpace(row.FromStep)
			if fromStep == "" {
				continue
			}
			if fromStep == chunk.ChunkID {
				return nil, nil, nil, fmt.Errorf("chunk %q handoff port %q cannot reference itself", chunk.ChunkID, row.Port)
			}
			if _, ok := chunkIDSet[fromStep]; !ok {
				return nil, nil, nil, fmt.Errorf("chunk %q handoff port %q references unknown FSTEP %q", chunk.ChunkID, row.Port, fromStep)
			}
			if controlReachability[fromStep][chunk.ChunkID] {
				continue
			}
			key := edgeKey(fromStep, chunk.ChunkID)
			edgeMap[key] = bridge.PlanEdge{
				FromChunkID: fromStep,
				ToChunkID:   chunk.ChunkID,
				EdgeKind:    "handoff",
			}
		}
	}

	inDegree := make(map[string]int, len(plan.Chunks))
	dependsOn := make(map[string][]string, len(plan.Chunks))
	newEdges := make([]bridge.PlanEdge, 0, len(edgeMap))
	for _, chunk := range plan.Chunks {
		inDegree[chunk.ChunkID] = 0
	}
	for _, edge := range edgeMap {
		newEdges = append(newEdges, edge)
		if isHandoffWorkbenchEdgeKind(edge.EdgeKind) || strings.EqualFold(strings.TrimSpace(edge.EdgeKind), "loop_back") {
			continue
		}
		inDegree[edge.ToChunkID]++
		dependsOn[edge.ToChunkID] = append(dependsOn[edge.ToChunkID], edge.FromChunkID)
	}
	entryChunks := make([]string, 0, len(plan.Chunks))
	for _, chunk := range plan.Chunks {
		if inDegree[chunk.ChunkID] == 0 {
			entryChunks = append(entryChunks, chunk.ChunkID)
		}
	}
	sort.Strings(entryChunks)
	if len(entryChunks) != 1 {
		return nil, nil, nil, fmt.Errorf("plan must contain exactly one entry chunk after topology sync, got %d (%s)", len(entryChunks), strings.Join(entryChunks, ", "))
	}
	for chunkID := range dependsOn {
		sort.Strings(dependsOn[chunkID])
	}
	return newEdges, dependsOn, entryChunks, nil
}

func applyDerivedTopology(plan *bridge.ChunkGraphPlan, edges []bridge.PlanEdge, dependsOn map[string][]string, entryChunks []string) {
	plan.Edges = edges
	plan.EntryChunks = append([]string(nil), entryChunks...)
	for i := range plan.Chunks {
		chunkID := plan.Chunks[i].ChunkID
		deps := append([]string(nil), dependsOn[chunkID]...)
		plan.Chunks[i].DependsOn = deps
	}
}

func (m *Model) SyncEdgesFromHandoffs() error {
	if m == nil || m.Plan == nil {
		return fmt.Errorf("workbench model is not initialized")
	}
	newEdges, dependsOn, entryChunks, err := buildDerivedTopology(m.Plan)
	if err != nil {
		return err
	}
	applyDerivedTopology(m.Plan, newEdges, dependsOn, entryChunks)
	m.Dirty = true
	return nil
}

func handoffRows(chunk *bridge.PlanChunk) []HandoffRow {
	if chunk == nil || len(chunk.Inputs) == 0 {
		return nil
	}
	rawInputs, _ := chunk.Inputs["handoff_inputs"].(map[string]any)
	if len(rawInputs) == 0 {
		return nil
	}
	ports := make([]string, 0, len(rawInputs))
	for key := range rawInputs {
		ports = append(ports, key)
	}
	sort.Strings(ports)
	rows := make([]HandoffRow, 0, len(ports))
	for _, key := range ports {
		row := HandoffRow{Key: key}
		spec, _ := rawInputs[key].(map[string]any)
		if spec == nil {
			rows = append(rows, row)
			continue
		}
		if port, varName, ok := workflowAliasParts("@" + key); ok {
			row.Port = port
			row.VarName = varName
		}
		row.FromStep, _ = spec["from_step"].(string)
		if row.VarName == "" {
			row.VarName, _ = spec["var_name"].(string)
		}
		if supplementary, ok := spec["supplementary_info"]; ok {
			if data, err := json.MarshalIndent(supplementary, "", "  "); err == nil {
				row.SupplementaryJSON = string(data)
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func defaultSelectedChunkID(plan *bridge.ChunkGraphPlan) string {
	if plan == nil {
		return ""
	}
	if len(plan.EntryChunks) > 0 {
		return plan.EntryChunks[0]
	}
	if len(plan.Chunks) > 0 {
		return plan.Chunks[0].ChunkID
	}
	return ""
}

func formatValidationIssues(issues []bridge.ValidationIssue) error {
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

func nextChunkID(plan *bridge.ChunkGraphPlan) string {
	used := make(map[string]struct{}, len(plan.Chunks))
	for _, chunk := range plan.Chunks {
		used[chunk.ChunkID] = struct{}{}
	}
	return nextChunkIDFromUsed(used)
}

func nextChunkIDFromUsed(used map[string]struct{}) string {
	maxNumeric := 0
	for chunkID := range used {
		const prefix = "CHK_"
		if !strings.HasPrefix(chunkID, prefix) {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(chunkID, prefix))
		if err == nil && n > maxNumeric {
			maxNumeric = n
		}
	}
	for i := maxNumeric + 1; ; i++ {
		candidate := fmt.Sprintf("CHK_%03d", i)
		if _, exists := used[candidate]; !exists {
			return candidate
		}
	}
}

func nextHandoffPort(chunk *bridge.PlanChunk) string {
	used := make(map[string]struct{})
	for _, row := range handoffRows(chunk) {
		used[row.Port] = struct{}{}
	}
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("input_%d", i)
		if _, exists := used[candidate]; !exists {
			return candidate
		}
	}
}

func nextChunkPort(plan *bridge.ChunkGraphPlan) string {
	used := make(map[string]string)
	for _, chunk := range plan.Chunks {
		port := normalizePortName(extractChunkHeaderValue(chunk.Source.SatisText, chunkPortMetaKey))
		if port == "" {
			continue
		}
		used[port] = chunk.ChunkID
	}
	return nextAvailablePortName("port", used)
}

func hasHandoffInputs(chunk *bridge.PlanChunk) bool {
	if chunk == nil || chunk.Inputs == nil {
		return false
	}
	rawInputs, _ := chunk.Inputs["handoff_inputs"].(map[string]any)
	return len(rawInputs) > 0
}

func edgeKey(fromChunkID string, toChunkID string) string {
	return fromChunkID + "\x00" + toChunkID
}

func edgeIdentity(edge bridge.PlanEdge) string {
	return edge.FromChunkID + "\x00" + edge.ToChunkID + "\x00" + strings.ToLower(strings.TrimSpace(edge.EdgeKind)) + "\x00" + strings.TrimSpace(edge.Branch)
}

func buildPlanControlReachability(plan *bridge.ChunkGraphPlan, chunkIDSet map[string]struct{}) map[string]map[string]bool {
	adj := make(map[string][]string, len(plan.Chunks))
	for _, edge := range plan.Edges {
		if isHandoffWorkbenchEdgeKind(edge.EdgeKind) || strings.EqualFold(strings.TrimSpace(edge.EdgeKind), "loop_back") {
			continue
		}
		if _, ok := chunkIDSet[edge.FromChunkID]; !ok {
			continue
		}
		if _, ok := chunkIDSet[edge.ToChunkID]; !ok {
			continue
		}
		adj[edge.FromChunkID] = append(adj[edge.FromChunkID], edge.ToChunkID)
	}
	out := make(map[string]map[string]bool, len(plan.Chunks))
	for _, chunk := range plan.Chunks {
		seen := make(map[string]bool)
		queue := append([]string(nil), adj[chunk.ChunkID]...)
		for len(queue) > 0 {
			id := queue[0]
			queue = queue[1:]
			if seen[id] {
				continue
			}
			seen[id] = true
			queue = append(queue, adj[id]...)
		}
		out[chunk.ChunkID] = seen
	}
	return out
}

func classifyDecisionEdgeKind(plan *bridge.ChunkGraphPlan, sourceChunkID string, targetChunkID string) string {
	sourceChunkID = strings.TrimSpace(sourceChunkID)
	targetChunkID = strings.TrimSpace(targetChunkID)
	if plan == nil || sourceChunkID == "" || targetChunkID == "" {
		return "branch"
	}
	chunkIDSet := make(map[string]struct{}, len(plan.Chunks))
	for _, chunk := range plan.Chunks {
		chunkIDSet[chunk.ChunkID] = struct{}{}
	}
	if _, ok := chunkIDSet[sourceChunkID]; !ok {
		return "branch"
	}
	if _, ok := chunkIDSet[targetChunkID]; !ok {
		return "branch"
	}
	controlReachability := buildPlanControlReachability(plan, chunkIDSet)
	if controlReachability[targetChunkID][sourceChunkID] {
		return "loop_back"
	}
	return "branch"
}

func isConditionalWorkbenchEdgeKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "branch", "default", "loop_back":
		return true
	default:
		return false
	}
}

func isHandoffWorkbenchEdgeKind(kind string) bool {
	return strings.EqualFold(strings.TrimSpace(kind), "handoff")
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
