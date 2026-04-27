package bridge

import (
	"fmt"
	"slices"
	"strings"
	"text/template"

	"satis/satis"
)

const chunkPortMetaKey = "chunk_port"

// ValidateChunkGraphPlan performs structural validation and Satis source checks.
// If validation passes, NormalizedPlan is set; otherwise Accepted is false.
func ValidateChunkGraphPlan(p *ChunkGraphPlan) SubmitChunkGraphResult {
	if p == nil {
		return SubmitChunkGraphResult{
			Accepted: false,
			ValidationErrors: []ValidationIssue{{
				Code:    CodeValidationNilPlan,
				Message: "chunk graph plan is nil",
			}},
		}
	}
	var issues []ValidationIssue
	issues = append(issues, validateRequiredDescriptions(p)...)
	chunkIDSet := make(map[string]struct{})
	for _, c := range p.Chunks {
		if _, dup := chunkIDSet[c.ChunkID]; dup {
			issues = append(issues, ValidationIssue{
				Code:    CodeValidationDuplicateChunkID,
				Message: fmt.Sprintf("duplicate chunk_id %q", c.ChunkID),
				Field:   "chunks",
			})
			continue
		}
		chunkIDSet[c.ChunkID] = struct{}{}
	}

	if len(issues) > 0 {
		return SubmitChunkGraphResult{Accepted: false, ValidationErrors: issues}
	}

	for i, c := range p.Chunks {
		if vi := validateChunkKindAndSource(c); vi != nil {
			v := *vi
			v.Field = fmt.Sprintf("chunks[%d]", i)
			issues = append(issues, v)
		}
		if vi := validateChunkRepeat(c); vi != nil {
			v := *vi
			v.Field = fmt.Sprintf("chunks[%d].repeat", i)
			issues = append(issues, v)
		}
	}
	issues = append(issues, validateChunkPortUniqueness(p)...)

	for _, e := range p.Edges {
		if _, ok := chunkIDSet[e.FromChunkID]; !ok {
			issues = append(issues, ValidationIssue{
				Code:    CodeValidationUnknownChunkRef,
				Message: fmt.Sprintf("edge references unknown from_chunk_id %q", e.FromChunkID),
				Field:   "edges",
			})
		}
		if _, ok := chunkIDSet[e.ToChunkID]; !ok {
			issues = append(issues, ValidationIssue{
				Code:    CodeValidationUnknownChunkRef,
				Message: fmt.Sprintf("edge references unknown to_chunk_id %q", e.ToChunkID),
				Field:   "edges",
			})
		}
		if e.FromChunkID == e.ToChunkID {
			issues = append(issues, ValidationIssue{
				Code:    CodeValidationSelfLoop,
				Message: fmt.Sprintf("self-loop on chunk %q", e.FromChunkID),
				Field:   "edges",
			})
		}
		if strings.TrimSpace(e.EdgeKind) == "branch" && strings.TrimSpace(e.Branch) == "" {
			issues = append(issues, ValidationIssue{
				Code:    CodeValidationDecisionConfig,
				Message: fmt.Sprintf("branch edge %q -> %q must declare branch", e.FromChunkID, e.ToChunkID),
				Field:   "edges",
			})
		}
		if strings.EqualFold(strings.TrimSpace(e.EdgeKind), "loop_back") {
			if vi := validateLoopPolicy(e, "edges"); vi != nil {
				issues = append(issues, *vi)
			}
		}
	}
	chunkByID := make(map[string]PlanChunk, len(p.Chunks))
	for _, c := range p.Chunks {
		chunkByID[c.ChunkID] = c
	}
	for _, e := range p.Edges {
		source := chunkByID[e.FromChunkID]
		switch strings.ToLower(strings.TrimSpace(e.EdgeKind)) {
		case "branch":
			if normalizedChunkKind(source.Kind) != "decision" {
				issues = append(issues, ValidationIssue{
					Code:    CodeValidationDecisionConfig,
					Message: fmt.Sprintf("branch edge %q -> %q requires decision source node", e.FromChunkID, e.ToChunkID),
					Field:   "edges",
				})
				continue
			}
			if source.Decision == nil || !slices.Contains(source.Decision.AllowedBranches, e.Branch) {
				issues = append(issues, ValidationIssue{
					Code:    CodeValidationDecisionConfig,
					Message: fmt.Sprintf("branch edge %q -> %q references undeclared branch %q", e.FromChunkID, e.ToChunkID, e.Branch),
					Field:   "edges",
				})
			}
		case "default":
			if normalizedChunkKind(source.Kind) != "decision" {
				issues = append(issues, ValidationIssue{
					Code:    CodeValidationDecisionConfig,
					Message: fmt.Sprintf("default edge %q -> %q requires decision source node", e.FromChunkID, e.ToChunkID),
					Field:   "edges",
				})
			}
		case "planner_exit":
			issues = append(issues, ValidationIssue{
				Code:    CodeValidationUnsupportedChunkKind,
				Message: fmt.Sprintf("planner_exit edge %q -> %q is no longer supported", e.FromChunkID, e.ToChunkID),
				Field:   "edges",
			})
		}
	}
	issues = append(issues, validateBackEdgesRequireLoopPolicy(p)...)

	if len(p.EntryChunks) != 1 {
		issues = append(issues, ValidationIssue{
			Code:    CodeValidationBadEntry,
			Message: fmt.Sprintf("plan must declare exactly one entry chunk, got %d", len(p.EntryChunks)),
			Field:   "entry_chunks",
		})
	}
	for _, name := range p.EntryChunks {
		if _, ok := chunkIDSet[name]; !ok {
			issues = append(issues, ValidationIssue{
				Code:    CodeValidationBadEntry,
				Message: fmt.Sprintf("entry chunk %q not found in chunks", name),
				Field:   "entry_chunks",
			})
		}
	}

	edgeSet := make(map[string]struct{})
	for _, e := range p.Edges {
		key := e.FromChunkID + "\x00" + e.ToChunkID
		edgeSet[key] = struct{}{}
	}

	for _, c := range p.Chunks {
		for _, dep := range c.DependsOn {
			key := dep + "\x00" + c.ChunkID
			if _, ok := edgeSet[key]; !ok {
				issues = append(issues, ValidationIssue{
					Code:    CodeValidationDependsMismatch,
					Message: fmt.Sprintf("chunk %q lists depends_on %q but no matching edge from->to", c.ChunkID, dep),
					Field:   "depends_on",
				})
			}
		}
	}
	issues = append(issues, validateHandoffOutputRefs(p)...)

	if len(issues) > 0 {
		return SubmitChunkGraphResult{Accepted: false, ValidationErrors: issues}
	}

	indegree := make(map[string]int)
	for id := range chunkIDSet {
		indegree[id] = 0
	}
	adj := make(map[string][]string)
	for _, e := range p.Edges {
		if isConditionalEdgeKind(e.EdgeKind) {
			continue
		}
		adj[e.FromChunkID] = append(adj[e.FromChunkID], e.ToChunkID)
		indegree[e.ToChunkID]++
	}

	for _, e := range p.EntryChunks {
		if indegree[e] != 0 {
			issues = append(issues, ValidationIssue{
				Code:    CodeValidationBadEntry,
				Message: fmt.Sprintf("entry chunk %q must have no incoming edges", e),
				Field:   "entry_chunks",
			})
		}
	}
	if len(issues) > 0 {
		return SubmitChunkGraphResult{Accepted: false, ValidationErrors: issues}
	}

	if hasCycle(chunkIDSet, adj) {
		issues = append(issues, ValidationIssue{
			Code:    CodeValidationDAGCycle,
			Message: "chunk dependency graph contains a cycle",
			Field:   "edges",
		})
		return SubmitChunkGraphResult{Accepted: false, ValidationErrors: issues}
	}

	reachabilityAdj := make(map[string][]string, len(adj))
	for from, tos := range adj {
		reachabilityAdj[from] = append(reachabilityAdj[from], tos...)
	}
	for _, e := range p.Edges {
		if !isConditionalEdgeKind(e.EdgeKind) {
			continue
		}
		reachabilityAdj[e.FromChunkID] = append(reachabilityAdj[e.FromChunkID], e.ToChunkID)
	}
	unreachable := findUnreachable(chunkIDSet, reachabilityAdj, p.EntryChunks)
	for _, id := range unreachable {
		issues = append(issues, ValidationIssue{
			Code:    CodeValidationUnreachable,
			Message: fmt.Sprintf("chunk %q is not reachable from entry_chunks", id),
			Field:   "chunks",
		})
	}
	if len(issues) > 0 {
		return SubmitChunkGraphResult{Accepted: false, ValidationErrors: issues}
	}

	norm := NormalizeChunkGraphPlan(p)
	return SubmitChunkGraphResult{
		Accepted:         true,
		ValidationErrors: []ValidationIssue{},
		NormalizedPlan:   norm,
	}
}

func validateSatisChunk(c PlanChunk) *ValidationIssue {
	if strings.TrimSpace(c.Description) == "" {
		return &ValidationIssue{
			Code:    CodeValidationSatisValidate,
			Message: fmt.Sprintf("chunk %q description must be non-empty", c.ChunkID),
		}
	}
	ch, err := satis.Parse(c.Source.SatisText)
	if err != nil {
		return &ValidationIssue{Code: CodeValidationSatisParse, Message: err.Error()}
	}
	if err := satis.Validate(ch); err != nil {
		return &ValidationIssue{Code: CodeValidationSatisValidate, Message: err.Error()}
	}
	if got := ch.Meta["chunk_id"]; got != c.ChunkID {
		return &ValidationIssue{
			Code:    CodeValidationChunkIDMismatch,
			Message: fmt.Sprintf("satis header chunk_id %q != plan chunk_id %q", got, c.ChunkID),
		}
	}
	headerDescription := strings.TrimSpace(ch.Meta["description"])
	if headerDescription == "" {
		return &ValidationIssue{
			Code:    CodeValidationSatisValidate,
			Message: fmt.Sprintf("chunk %q satis header must declare description", c.ChunkID),
		}
	}
	if headerDescription != strings.TrimSpace(c.Description) {
		return &ValidationIssue{
			Code:    CodeValidationSatisValidate,
			Message: fmt.Sprintf("satis header description %q != plan chunk description %q", headerDescription, c.Description),
		}
	}
	if vi := validateChunkPortMeta(c, ch); vi != nil {
		return vi
	}
	if _, err := declaredChunkSupplementaryInfo(c); err != nil {
		return &ValidationIssue{Code: CodeValidationInputBinding, Message: err.Error()}
	}
	if vi := validateInputBindings(c, ch); vi != nil {
		return vi
	}
	return nil
}

func validateChunkKindAndSource(c PlanChunk) *ValidationIssue {
	switch normalizedChunkKind(c.Kind) {
	case "task":
		return validateTaskChunk(c)
	case "decision":
		return validateDecisionChunk(c)
	default:
		return &ValidationIssue{
			Code:    CodeValidationUnsupportedChunkKind,
			Message: fmt.Sprintf("unsupported chunk kind %q", c.Kind),
		}
	}
}

func validateTaskChunk(c PlanChunk) *ValidationIssue {
	return validateSatisChunk(c)
}

func validateDecisionChunk(c PlanChunk) *ValidationIssue {
	if c.Decision == nil {
		return &ValidationIssue{Code: CodeValidationDecisionConfig, Message: "decision chunk requires decision config"}
	}
	if len(c.Decision.AllowedBranches) == 0 {
		return &ValidationIssue{Code: CodeValidationDecisionConfig, Message: "decision chunk requires allowed_branches"}
	}
	if c.Decision.Interaction == nil {
		return &ValidationIssue{Code: CodeValidationDecisionConfig, Message: "decision chunk requires interaction config"}
	}
	return validateInteractionSpec(c.Decision.Interaction, c.Decision.AllowedBranches, "decision")
}

func validateInteractionSpec(spec *InteractionSpec, choices []string, kind string) *ValidationIssue {
	mode := strings.TrimSpace(spec.Mode)
	if mode == "" {
		return &ValidationIssue{Code: codeForControlKind(kind), Message: fmt.Sprintf("%s interaction requires mode", kind)}
	}
	switch mode {
	case "human":
		if spec.Human == nil {
			return &ValidationIssue{Code: codeForControlKind(kind), Message: fmt.Sprintf("%s human mode requires human interaction", kind)}
		}
	case "llm", "llm_then_human":
		if spec.LLM == nil {
			return &ValidationIssue{Code: codeForControlKind(kind), Message: fmt.Sprintf("%s llm mode requires llm interaction", kind)}
		}
		if strings.TrimSpace(spec.LLM.UserPromptTemplate) == "" {
			return &ValidationIssue{Code: codeForControlKind(kind), Message: fmt.Sprintf("%s llm interaction requires user_prompt_template", kind)}
		}
	default:
		return &ValidationIssue{Code: codeForControlKind(kind), Message: fmt.Sprintf("unsupported %s interaction mode %q", kind, mode)}
	}
	seen := make(map[string]struct{}, len(choices))
	for _, choice := range choices {
		choice = strings.TrimSpace(choice)
		if choice == "" {
			return &ValidationIssue{Code: codeForControlKind(kind), Message: fmt.Sprintf("%s choices must be non-empty", kind)}
		}
		if _, ok := seen[choice]; ok {
			return &ValidationIssue{Code: codeForControlKind(kind), Message: fmt.Sprintf("duplicate %s choice %q", kind, choice)}
		}
		seen[choice] = struct{}{}
	}
	return nil
}

func validateRequiredDescriptions(p *ChunkGraphPlan) []ValidationIssue {
	if p == nil {
		return nil
	}
	var issues []ValidationIssue
	if strings.TrimSpace(p.IntentDescription) == "" {
		issues = append(issues, ValidationIssue{
			Code:    CodeValidationSatisValidate,
			Message: "intent_description must be non-empty",
			Field:   "intent_description",
		})
	}
	if strings.TrimSpace(p.PlanDescription) == "" {
		issues = append(issues, ValidationIssue{
			Code:    CodeValidationSatisValidate,
			Message: "plan_description must be non-empty",
			Field:   "plan_description",
		})
	}
	for i := range p.Chunks {
		chunk := &p.Chunks[i]
		headerDescription := strings.TrimSpace(extractHeaderValue(chunk.Source.SatisText, "description"))
		if strings.TrimSpace(chunk.Description) == "" {
			issues = append(issues, ValidationIssue{
				Code:    CodeValidationSatisValidate,
				Message: fmt.Sprintf("chunk %q description must be non-empty", chunk.ChunkID),
				Field:   fmt.Sprintf("chunks[%d].description", i),
			})
		}
		if normalizedChunkKind(chunk.Kind) == "task" && headerDescription == "" {
			issues = append(issues, ValidationIssue{
				Code:    CodeValidationSatisValidate,
				Message: fmt.Sprintf("chunk %q satis header must declare description", chunk.ChunkID),
				Field:   fmt.Sprintf("chunks[%d].source.satis_text", i),
			})
		}
	}
	return issues
}

func extractHeaderValue(text string, key string) string {
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			break
		}
		idx := strings.Index(trimmed, ":")
		if idx <= 0 {
			break
		}
		if strings.TrimSpace(trimmed[:idx]) == key {
			return strings.TrimSpace(trimmed[idx+1:])
		}
	}
	return ""
}

func setHeaderValue(text string, key string, value string) string {
	lines := strings.Split(text, "\n")
	lastHeader := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if lastHeader >= 0 {
				break
			}
			continue
		}
		idx := strings.Index(trimmed, ":")
		if idx <= 0 {
			break
		}
		headerKey := strings.TrimSpace(trimmed[:idx])
		if headerKey == key {
			lines[i] = key + ": " + value
			return strings.Join(lines, "\n")
		}
		lastHeader = i
	}
	insertAt := 0
	if lastHeader >= 0 {
		insertAt = lastHeader + 1
	}
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:insertAt]...)
	out = append(out, key+": "+value)
	out = append(out, lines[insertAt:]...)
	return strings.Join(out, "\n")
}

func codeForControlKind(kind string) string { return CodeValidationDecisionConfig }

func normalizedChunkKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", "task", "satis", "read", "transform", "write", "patch", "invoke", "compound":
		return "task"
	case "decision":
		return "decision"
	default:
		return strings.ToLower(strings.TrimSpace(kind))
	}
}

func isConditionalEdgeKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "branch", "default", "loop_back":
		return true
	default:
		return false
	}
}

func validateInputBindings(c PlanChunk, ch *satis.Chunk) *ValidationIssue {
	declaredInputs, err := declaredHandoffInputs(c)
	if err != nil {
		return &ValidationIssue{
			Code:    CodeValidationInputBinding,
			Message: err.Error(),
		}
	}

	usedAliases := make(map[string]struct{})
	unknownAliases := make(map[string]struct{})
	mismatchedAliases := make(map[string]string)
	chunkPort := strings.TrimSpace(ch.Meta[chunkPortMetaKey])
	isReservedInjectedAlias := func(varName string) bool {
		port, name, ok := workflowAliasParts(varName)
		if !ok {
			return false
		}
		spec, declared := declaredInputs[workflowAliasName(port, name)]
		return declared && spec.VarName == name
	}

	recordInputUse := func(varName string) {
		port, name, ok := inputAliasParts(varName)
		if !ok {
			return
		}
		if port == chunkPort {
			return
		}
		spec, declared := declaredInputs[workflowAliasName(port, name)]
		if !declared {
			unknownAliases[varName] = struct{}{}
			return
		}
		if spec.VarName != name {
			mismatchedAliases[varName] = inputAliasName(port, spec.VarName)
			return
		}
		usedAliases[workflowAliasName(port, name)] = struct{}{}
	}
	recordInputValueUse := func(value satis.Value) {
		if value.Kind == satis.ValueKindVariable {
			recordInputUse(value.Text)
		}
	}
	recordInputValuesUse := func(values []satis.Value) {
		for _, value := range values {
			recordInputValueUse(value)
		}
	}

	for _, inst := range ch.Instructions {
		switch stmt := inst.(type) {
		case satis.ResolveStmt:
			if isReservedInjectedAlias(stmt.OutputVar) {
				return &ValidationIssue{
					Code:    CodeValidationInputBinding,
					Message: fmt.Sprintf("chunk %q reserves %q for injected handoff input; it cannot be assigned in the body", c.ChunkID, stmt.OutputVar),
					Details: map[string]any{"chunk_id": c.ChunkID, "output_var": stmt.OutputVar},
				}
			}

		case satis.ReadStmt:
			if isReservedInjectedAlias(stmt.OutputVar) {
				return &ValidationIssue{
					Code:    CodeValidationInputBinding,
					Message: fmt.Sprintf("chunk %q reserves %q for injected handoff input; it cannot be assigned in the body", c.ChunkID, stmt.OutputVar),
					Details: map[string]any{"chunk_id": c.ChunkID, "output_var": stmt.OutputVar},
				}
			}
			recordInputUse(stmt.ObjectVar)
		case satis.WriteStmt:
			recordInputValueUse(stmt.Value)
			if isReservedInjectedAlias(stmt.OutputVar) {
				return &ValidationIssue{
					Code:    CodeValidationInputBinding,
					Message: fmt.Sprintf("chunk %q reserves %q for injected handoff input; it cannot be assigned in the body", c.ChunkID, stmt.OutputVar),
					Details: map[string]any{"chunk_id": c.ChunkID, "output_var": stmt.OutputVar},
				}
			}
			recordInputUse(stmt.ObjectVar)
		case satis.PrintStmt:
			recordInputValueUse(stmt.Value)

		case satis.ConcatStmt:
			if isReservedInjectedAlias(stmt.OutputVar) {
				return &ValidationIssue{
					Code:    CodeValidationInputBinding,
					Message: fmt.Sprintf("chunk %q reserves %q for injected handoff input; it cannot be assigned in the body", c.ChunkID, stmt.OutputVar),
					Details: map[string]any{"chunk_id": c.ChunkID, "output_var": stmt.OutputVar},
				}
			}
			recordInputValuesUse(stmt.Values)

		case satis.CopyStmt:
			if isReservedInjectedAlias(stmt.OutputVar) {
				return &ValidationIssue{
					Code:    CodeValidationInputBinding,
					Message: fmt.Sprintf("chunk %q reserves %q for injected handoff input; it cannot be assigned in the body", c.ChunkID, stmt.OutputVar),
					Details: map[string]any{"chunk_id": c.ChunkID, "output_var": stmt.OutputVar},
				}
			}
			recordInputUse(stmt.ObjectVar)
		case satis.MoveStmt:
			if isReservedInjectedAlias(stmt.OutputVar) {
				return &ValidationIssue{
					Code:    CodeValidationInputBinding,
					Message: fmt.Sprintf("chunk %q reserves %q for injected handoff input; it cannot be assigned in the body", c.ChunkID, stmt.OutputVar),
					Details: map[string]any{"chunk_id": c.ChunkID, "output_var": stmt.OutputVar},
				}
			}
			recordInputUse(stmt.ObjectVar)
		case satis.PatchStmt:
			if isReservedInjectedAlias(stmt.OutputVar) {
				return &ValidationIssue{
					Code:    CodeValidationInputBinding,
					Message: fmt.Sprintf("chunk %q reserves %q for injected handoff input; it cannot be assigned in the body", c.ChunkID, stmt.OutputVar),
					Details: map[string]any{"chunk_id": c.ChunkID, "output_var": stmt.OutputVar},
				}
			}
			recordInputUse(stmt.ObjectVar)
		case satis.DeleteStmt:
			for _, source := range stmt.Sources {
				recordInputUse(source.ObjectVar)
			}
		case satis.RenameStmt:
			if isReservedInjectedAlias(stmt.OutputVar) {
				return &ValidationIssue{
					Code:    CodeValidationInputBinding,
					Message: fmt.Sprintf("chunk %q reserves %q for injected handoff input; it cannot be assigned in the body", c.ChunkID, stmt.OutputVar),
					Details: map[string]any{"chunk_id": c.ChunkID, "output_var": stmt.OutputVar},
				}
			}
			recordInputUse(stmt.ObjectVar)
		case satis.InvokeStmt:
			if isReservedInjectedAlias(stmt.OutputVar) {
				return &ValidationIssue{
					Code:    CodeValidationInputBinding,
					Message: fmt.Sprintf("chunk %q reserves %q for injected handoff input; it cannot be assigned in the body", c.ChunkID, stmt.OutputVar),
					Details: map[string]any{"chunk_id": c.ChunkID, "output_var": stmt.OutputVar},
				}
			}
			if stmt.ConversationVar != "" {
				recordInputUse(stmt.ConversationVar)
			}
			if len(stmt.PromptParts) > 0 {
				recordInputValuesUse(stmt.PromptParts)
			} else {
				recordInputValueUse(stmt.Prompt)
			}
			if stmt.HasInput {
				recordInputValueUse(stmt.Input)
			}
		case satis.BatchInvokeStmt:
			if isReservedInjectedAlias(stmt.OutputVar) {
				return &ValidationIssue{
					Code:    CodeValidationInputBinding,
					Message: fmt.Sprintf("chunk %q reserves %q for injected handoff input; it cannot be assigned in the body", c.ChunkID, stmt.OutputVar),
					Details: map[string]any{"chunk_id": c.ChunkID, "output_var": stmt.OutputVar},
				}
			}
			recordInputValueUse(stmt.Prompt)
			recordInputUse(stmt.InputList)
		}
	}

	if len(unknownAliases) > 0 {
		aliases := mapKeysSorted(unknownAliases)
		return &ValidationIssue{
			Code:    CodeValidationInputBinding,
			Message: fmt.Sprintf("chunk %q references undeclared handoff input aliases %v", c.ChunkID, aliases),
			Details: map[string]any{"chunk_id": c.ChunkID, "aliases": aliases},
		}
	}
	if len(mismatchedAliases) > 0 {
		aliases := make([]string, 0, len(mismatchedAliases))
		for alias := range mismatchedAliases {
			aliases = append(aliases, alias)
		}
		slices.Sort(aliases)
		return &ValidationIssue{
			Code: CodeValidationInputBinding,
			Message: fmt.Sprintf(
				"chunk %q uses handoff aliases with mismatched var_name: %s",
				c.ChunkID,
				strings.Join(formatAliasMismatchDetails(aliases, mismatchedAliases), ", "),
			),
			Details: map[string]any{"chunk_id": c.ChunkID, "aliases": aliases},
		}
	}

	for _, alias := range mapKeysSortedHandoff(declaredInputs) {
		spec := declaredInputs[alias]
		if _, used := usedAliases[alias]; !used {
			return &ValidationIssue{
				Code:    CodeValidationInputBinding,
				Message: fmt.Sprintf("chunk %q declares input binding %q but never consumes it in the body", c.ChunkID, alias),
				Details: map[string]any{"chunk_id": c.ChunkID, "port": spec.Port, "alias": alias},
			}
		}
	}

	return nil
}

type handoffInputSpec struct {
	Alias             string
	Port              string
	FromStep          string
	VarName           string
	SupplementaryInfo map[string]any
}

func declaredHandoffInputs(c PlanChunk) (map[string]handoffInputSpec, error) {
	out := make(map[string]handoffInputSpec)
	if c.Inputs == nil {
		return out, nil
	}
	raw := c.Inputs["handoff_inputs"]
	if raw == nil {
		return out, nil
	}
	handoffs, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("chunk %q inputs.handoff_inputs must be an object", c.ChunkID)
	}
	for port, item := range handoffs {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("chunk %q inputs.handoff_inputs[%q] must be an object", c.ChunkID, port)
		}
		portName, varFromKey, ok := workflowAliasParts("@" + strings.TrimSpace(port))
		if !ok {
			return nil, fmt.Errorf("chunk %q inputs.handoff_inputs[%q] must use key <port>__<var>", c.ChunkID, port)
		}
		spec := handoffInputSpec{
			Alias:    workflowAliasName(portName, varFromKey),
			Port:     portName,
			FromStep: strings.TrimSpace(asString(entry["from_step"])),
			VarName:  strings.TrimSpace(asString(entry["var_name"])),
		}
		if spec.FromStep == "" || spec.VarName == "" {
			return nil, fmt.Errorf("chunk %q inputs.handoff_inputs[%q] requires from_step and var_name", c.ChunkID, port)
		}
		if !isValidHandoffName(portName) || !isValidHandoffName(spec.VarName) || spec.VarName != varFromKey {
			return nil, fmt.Errorf("chunk %q inputs.handoff_inputs[%q] contains invalid handoff names", c.ChunkID, port)
		}
		if raw := entry["supplementary_info"]; raw != nil {
			info, ok := raw.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("chunk %q inputs.handoff_inputs[%q].supplementary_info must be an object", c.ChunkID, port)
			}
			spec.SupplementaryInfo = cloneAnyMap(info)
		}
		out[spec.Alias] = spec
	}
	return out, nil
}

func declaredChunkSupplementaryInfo(c PlanChunk) (map[string]any, error) {
	if c.Inputs == nil {
		return nil, nil
	}
	raw := c.Inputs["supplementary_info"]
	if raw == nil {
		return nil, nil
	}
	info, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("chunk %q inputs.supplementary_info must be an object", c.ChunkID)
	}
	return cloneAnyMap(info), nil
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func validateHandoffOutputRefs(p *ChunkGraphPlan) []ValidationIssue {
	if p == nil {
		return nil
	}
	outputsByChunk := make(map[string]map[string]struct{}, len(p.Chunks))
	chunkIDSet := make(map[string]struct{}, len(p.Chunks))
	for _, chunk := range p.Chunks {
		chunkIDSet[chunk.ChunkID] = struct{}{}
		ch, err := satis.Parse(chunk.Source.SatisText)
		if err != nil {
			continue
		}
		outputsByChunk[chunk.ChunkID] = collectOutputAliases(ch, true)
	}
	reachabilityAdj := make(map[string][]string, len(p.Chunks))
	for _, edge := range p.Edges {
		if strings.EqualFold(strings.TrimSpace(edge.EdgeKind), "handoff") ||
			strings.EqualFold(strings.TrimSpace(edge.EdgeKind), "loop_back") {
			continue
		}
		if _, ok := chunkIDSet[edge.FromChunkID]; !ok {
			continue
		}
		if _, ok := chunkIDSet[edge.ToChunkID]; !ok {
			continue
		}
		reachabilityAdj[edge.FromChunkID] = append(reachabilityAdj[edge.FromChunkID], edge.ToChunkID)
	}
	reachable := computeReachableChunks(chunkIDSet, reachabilityAdj)
	var issues []ValidationIssue
	for _, chunk := range p.Chunks {
		specs, err := declaredHandoffInputs(chunk)
		if err != nil {
			continue
		}
		for alias, spec := range specs {
			required := alias
			upstream := outputsByChunk[spec.FromStep]
			if spec.FromStep != chunk.ChunkID && !reachable[spec.FromStep][chunk.ChunkID] {
				issues = append(issues, ValidationIssue{
					Code:    CodeValidationInputBinding,
					Message: fmt.Sprintf("chunk %q input binding %q references %q, but %q is not a reachable upstream chunk", chunk.ChunkID, alias, spec.FromStep, spec.FromStep),
					Field:   "inputs",
					Details: map[string]any{"chunk_id": chunk.ChunkID, "port": spec.Port, "from_step": spec.FromStep, "expected_alias": required},
				})
				continue
			}
			if workflowAliasDeclared(upstream, spec.Port, spec.VarName) {
				continue
			}
			issues = append(issues, ValidationIssue{
				Code:    CodeValidationInputBinding,
				Message: fmt.Sprintf("chunk %q input binding %q expects upstream alias %q from %q, but it is not declared as an output alias", chunk.ChunkID, alias, required, spec.FromStep),
				Field:   "inputs",
				Details: map[string]any{"chunk_id": chunk.ChunkID, "port": spec.Port, "from_step": spec.FromStep, "expected_alias": required},
			})
		}
	}
	return issues
}

func computeReachableChunks(ids map[string]struct{}, adj map[string][]string) map[string]map[string]bool {
	out := make(map[string]map[string]bool, len(ids))
	for id := range ids {
		seen := make(map[string]bool)
		queue := append([]string(nil), adj[id]...)
		for len(queue) > 0 {
			next := queue[0]
			queue = queue[1:]
			if seen[next] {
				continue
			}
			seen[next] = true
			queue = append(queue, adj[next]...)
		}
		out[id] = seen
	}
	return out
}

func collectTextOutputAliases(ch *satis.Chunk) map[string]struct{} {
	return collectOutputAliases(ch, false)
}

func collectOutputAliases(ch *satis.Chunk, includeConversation bool) map[string]struct{} {
	out := make(map[string]struct{})
	if ch == nil {
		return out
	}
	record := func(varName string) {
		if _, _, ok := workflowAliasParts(varName); ok {
			out[varName] = struct{}{}
		}
	}
	for _, inst := range ch.Instructions {
		switch stmt := inst.(type) {
		case satis.ResolveStmt:
			record(stmt.OutputVar)
		case satis.ReadStmt:
			record(stmt.OutputVar)
		case satis.CreateStmt:
			record(stmt.OutputVar)
		case satis.ConcatStmt:
			record(stmt.OutputVar)
		case satis.WriteStmt:
			record(stmt.OutputVar)
		case satis.CopyStmt:
			record(stmt.OutputVar)
		case satis.MoveStmt:
			record(stmt.OutputVar)
		case satis.PatchStmt:
			record(stmt.OutputVar)
		case satis.RenameStmt:
			record(stmt.OutputVar)
		case satis.InvokeStmt:
			record(stmt.OutputVar)
			if includeConversation {
				record(stmt.ConversationVar)
			}
		case satis.BatchInvokeStmt:
			record(stmt.OutputVar)
		}
	}
	return out
}

func validateChunkPortMeta(c PlanChunk, ch *satis.Chunk) *ValidationIssue {
	if ch == nil {
		return nil
	}
	port := strings.TrimSpace(ch.Meta[chunkPortMetaKey])
	if port == "" {
		return &ValidationIssue{
			Code:    CodeValidationInputBinding,
			Message: fmt.Sprintf("chunk %q must declare header %q", c.ChunkID, chunkPortMetaKey),
			Details: map[string]any{"chunk_id": c.ChunkID, "header": chunkPortMetaKey},
		}
	}
	if !isValidHandoffName(port) {
		return &ValidationIssue{
			Code:    CodeValidationInputBinding,
			Message: fmt.Sprintf("chunk %q header %q has invalid port name %q", c.ChunkID, chunkPortMetaKey, port),
			Details: map[string]any{"chunk_id": c.ChunkID, "header": chunkPortMetaKey, "port": port},
		}
	}
	for alias := range collectTextOutputAliases(ch) {
		aliasPort, _, ok := outputAliasParts(alias)
		if !ok {
			continue
		}
		if aliasPort != port {
			return &ValidationIssue{
				Code:    CodeValidationInputBinding,
				Message: fmt.Sprintf("chunk %q declares %q=%q, but body outputs %q", c.ChunkID, chunkPortMetaKey, port, alias),
				Details: map[string]any{"chunk_id": c.ChunkID, "declared_port": port, "output_alias": alias},
			}
		}
	}
	return nil
}

func validateChunkPortUniqueness(p *ChunkGraphPlan) []ValidationIssue {
	if p == nil {
		return nil
	}
	ownerByPort := make(map[string]string)
	var issues []ValidationIssue
	for _, chunk := range p.Chunks {
		ch, err := satis.Parse(chunk.Source.SatisText)
		if err != nil {
			continue
		}
		port := strings.TrimSpace(ch.Meta[chunkPortMetaKey])
		if port == "" {
			continue
		}
		if previous, exists := ownerByPort[port]; exists && previous != chunk.ChunkID {
			issues = append(issues, ValidationIssue{
				Code:    CodeValidationInputBinding,
				Message: fmt.Sprintf("duplicate %q %q on chunks %q and %q; ports must be globally unique", chunkPortMetaKey, port, previous, chunk.ChunkID),
				Field:   "chunks",
				Details: map[string]any{"port": port, "chunks": []string{previous, chunk.ChunkID}},
			})
			continue
		}
		ownerByPort[port] = chunk.ChunkID
	}
	return issues
}

func inputAliasParts(varName string) (string, string, bool) {
	return workflowAliasParts(varName)
}

func outputAliasParts(varName string) (string, string, bool) {
	return workflowAliasParts(varName)
}

func workflowAliasParts(varName string) (string, string, bool) {
	trimmed := strings.TrimSpace(varName)
	if !strings.HasPrefix(trimmed, "@") {
		return "", "", false
	}
	trimmed = strings.TrimPrefix(trimmed, "@")
	idx := strings.Index(trimmed, "__")
	if idx <= 0 || idx >= len(trimmed)-2 {
		return "", "", false
	}
	port := trimmed[:idx]
	name := trimmed[idx+2:]
	if !isValidHandoffName(port) || !isValidHandoffName(name) {
		return "", "", false
	}
	return port, name, true
}

func inputAliasName(port string, name string) string {
	return workflowAliasName(port, name)
}

func outputAliasName(port string, name string) string {
	return workflowAliasName(port, name)
}

func workflowAliasName(port string, name string) string {
	return "@" + port + "__" + name
}

func legacyInputAliasName(port string, name string) string {
	return "@in_" + port + "__" + name
}

func legacyOutputAliasName(port string, name string) string {
	return "@out_" + port + "__" + name
}

func workflowAliasDeclared(aliases map[string]struct{}, port string, name string) bool {
	if aliases == nil {
		return false
	}
	_, ok := aliases[workflowAliasName(port, name)]
	return ok
}

func isValidHandoffName(value string) bool {
	if value == "" || strings.Contains(value, "__") {
		return false
	}
	for i, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case i > 0 && r >= '0' && r <= '9':
		case i > 0 && r == '_':
		default:
			return false
		}
	}
	return true
}

func mapKeysSorted(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func mapKeysSortedHandoff(m map[string]handoffInputSpec) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func formatAliasMismatchDetails(aliases []string, expected map[string]string) []string {
	out := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		out = append(out, fmt.Sprintf("%s (expected %s)", alias, expected[alias]))
	}
	return out
}

func hasCycle(ids map[string]struct{}, adj map[string][]string) bool {
	color := make(map[string]int)
	var dfs func(string) bool
	dfs = func(u string) bool {
		switch color[u] {
		case 1:
			return true
		case 2:
			return false
		}
		color[u] = 1
		for _, v := range adj[u] {
			if dfs(v) {
				return true
			}
		}
		color[u] = 2
		return false
	}
	for id := range ids {
		if color[id] == 0 && dfs(id) {
			return true
		}
	}
	return false
}

func findUnreachable(ids map[string]struct{}, adj map[string][]string, entries []string) []string {
	seen := make(map[string]struct{})
	var q []string
	for _, e := range entries {
		if _, ok := ids[e]; !ok {
			continue
		}
		if _, ok := seen[e]; ok {
			continue
		}
		seen[e] = struct{}{}
		q = append(q, e)
	}
	for head := 0; head < len(q); head++ {
		u := q[head]
		for _, v := range adj[u] {
			if _, ok := seen[v]; ok {
				continue
			}
			seen[v] = struct{}{}
			q = append(q, v)
		}
	}
	var out []string
	for id := range ids {
		if _, ok := seen[id]; !ok {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

func validateChunkRepeat(c PlanChunk) *ValidationIssue {
	if c.Repeat == nil {
		return nil
	}
	if c.Repeat.Mode != "per_item" && c.Repeat.Mode != "batch" {
		return &ValidationIssue{
			Code:    CodeValidationRepeat,
			Message: fmt.Sprintf("chunk %q repeat mode must be per_item or batch, got %q", c.ChunkID, c.Repeat.Mode),
		}
	}
	if len(c.Repeat.InputPaths) == 0 {
		return &ValidationIssue{
			Code:    CodeValidationRepeat,
			Message: fmt.Sprintf("chunk %q repeat.input_paths must not be empty", c.ChunkID),
		}
	}
	if c.Repeat.OutputTmpl == "" {
		return &ValidationIssue{
			Code:    CodeValidationRepeat,
			Message: fmt.Sprintf("chunk %q repeat.output_template must not be empty", c.ChunkID),
		}
	}
	if _, err := template.New("repeat_output_validate").Parse(c.Repeat.OutputTmpl); err != nil {
		return &ValidationIssue{
			Code:    CodeValidationRepeat,
			Message: fmt.Sprintf("chunk %q repeat.output_template must be valid Go text/template: %v", c.ChunkID, err),
		}
	}
	if c.Repeat.Mode == "batch" && c.Repeat.Prompt == "" {
		return &ValidationIssue{
			Code:    CodeValidationRepeat,
			Message: fmt.Sprintf("chunk %q repeat.prompt is required for batch mode", c.ChunkID),
		}
	}
	if c.Repeat.Mode == "per_item" {
		if !strings.Contains(c.Source.SatisText, "__INPUT_PATH__") {
			return &ValidationIssue{
				Code:    CodeValidationRepeat,
				Message: fmt.Sprintf("chunk %q satis_text must contain __INPUT_PATH__ placeholder for per_item mode", c.ChunkID),
			}
		}
		if !strings.Contains(c.Source.SatisText, "__OUTPUT_PATH__") {
			return &ValidationIssue{
				Code:    CodeValidationRepeat,
				Message: fmt.Sprintf("chunk %q satis_text must contain __OUTPUT_PATH__ placeholder for per_item mode", c.ChunkID),
			}
		}
	}
	return nil
}

func validateLoopPolicy(edge PlanEdge, field string) *ValidationIssue {
	if edge.LoopPolicy == nil {
		return &ValidationIssue{
			Code:    CodeValidationLoopPolicy,
			Message: fmt.Sprintf("edge %q -> %q must declare loop_policy", edge.FromChunkID, edge.ToChunkID),
			Field:   field,
		}
	}
	if edge.LoopPolicy.MaxIterations <= 0 {
		return &ValidationIssue{
			Code:    CodeValidationLoopPolicy,
			Message: fmt.Sprintf("edge %q -> %q loop_policy.max_iterations must be > 0", edge.FromChunkID, edge.ToChunkID),
			Field:   field,
		}
	}
	return nil
}

func validateBackEdgesRequireLoopPolicy(p *ChunkGraphPlan) []ValidationIssue {
	if p == nil || len(p.Edges) == 0 {
		return nil
	}
	issues := make([]ValidationIssue, 0)
	for i, edge := range p.Edges {
		if !isConditionalEdgeKind(edge.EdgeKind) {
			continue
		}
		if !edgeCreatesBackReference(p.Edges, i) {
			continue
		}
		if edge.LoopPolicy == nil || edge.LoopPolicy.MaxIterations <= 0 {
			issues = append(issues, ValidationIssue{
				Code:    CodeValidationLoopPolicy,
				Message: fmt.Sprintf("back edge %q -> %q must declare loop_policy.max_iterations", edge.FromChunkID, edge.ToChunkID),
				Field:   "edges",
				Details: map[string]any{
					"from_chunk_id": edge.FromChunkID,
					"to_chunk_id":   edge.ToChunkID,
				},
			})
		}
	}
	return issues
}

func edgeCreatesBackReference(edges []PlanEdge, skip int) bool {
	if skip < 0 || skip >= len(edges) {
		return false
	}
	target := edges[skip]
	if target.FromChunkID == "" || target.ToChunkID == "" {
		return false
	}
	queue := []string{target.ToChunkID}
	seen := map[string]struct{}{target.ToChunkID: {}}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == target.FromChunkID {
			return true
		}
		for idx, edge := range edges {
			if idx == skip {
				continue
			}
			if edge.FromChunkID != current {
				continue
			}
			if _, ok := seen[edge.ToChunkID]; ok {
				continue
			}
			seen[edge.ToChunkID] = struct{}{}
			queue = append(queue, edge.ToChunkID)
		}
	}
	return false
}
