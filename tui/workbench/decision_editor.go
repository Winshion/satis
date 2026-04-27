package workbench

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"satis/bridge"
)

type decisionEditorBranch struct {
	Name   string
	Target string
}

type decisionEditorConfig struct {
	Mode              string
	DefaultBranch     string
	Title             string
	Prompt            string
	LoopMaxIterations int
	Branches          []decisionEditorBranch
}

func isDecisionChunk(chunk *bridge.PlanChunk) bool {
	return chunk != nil && strings.EqualFold(strings.TrimSpace(chunk.Kind), "decision")
}

func formatDecisionEditorText(plan *bridge.ChunkGraphPlan, chunk *bridge.PlanChunk) string {
	if !isDecisionChunk(chunk) {
		return "# Decision nodes only.\n# Select a decision chunk to edit:\n# mode, default_branch, title, prompt, loop_max_iterations, branches.\n"
	}
	cfg := decisionConfigFromChunk(plan, chunk)
	lines := []string{
		"mode: " + cfg.Mode,
		"default_branch: " + cfg.DefaultBranch,
		"title: " + cfg.Title,
		"prompt: " + cfg.Prompt,
		"loop_max_iterations: " + strconv.Itoa(cfg.LoopMaxIterations),
		"",
		"branches:",
	}
	for _, branch := range cfg.Branches {
		lines = append(lines, "  "+branch.Name+" -> "+branch.Target)
	}
	if len(cfg.Branches) == 0 {
		lines = append(lines, "  yes -> ")
	}
	return strings.Join(lines, "\n")
}

func decisionConfigFromChunk(plan *bridge.ChunkGraphPlan, chunk *bridge.PlanChunk) decisionEditorConfig {
	cfg := decisionEditorConfig{
		Mode:              "human",
		DefaultBranch:     "",
		Title:             "Decision",
		Prompt:            "choose branch from {{allowed_branches}}",
		LoopMaxIterations: 3,
	}
	if chunk == nil || chunk.Decision == nil {
		return cfg
	}
	cfg.DefaultBranch = strings.TrimSpace(chunk.Decision.DefaultBranch)
	if chunk.Decision.Interaction != nil {
		if mode := strings.TrimSpace(chunk.Decision.Interaction.Mode); mode != "" {
			cfg.Mode = mode
		}
		if chunk.Decision.Interaction.Human != nil {
			if title := strings.TrimSpace(chunk.Decision.Interaction.Human.Title); title != "" {
				cfg.Title = title
			}
		}
		if chunk.Decision.Interaction.LLM != nil {
			if prompt := strings.TrimSpace(chunk.Decision.Interaction.LLM.UserPromptTemplate); prompt != "" {
				cfg.Prompt = prompt
			}
		}
	}
	branchTargets := decisionBranchTargets(plan, chunk.ChunkID)
	allowed := append([]string(nil), chunk.Decision.AllowedBranches...)
	for branch := range branchTargets {
		if !containsString(allowed, branch) {
			allowed = append(allowed, branch)
		}
	}
	sort.Strings(allowed)
	for _, name := range allowed {
		cfg.Branches = append(cfg.Branches, decisionEditorBranch{
			Name:   name,
			Target: branchTargets[name],
		})
	}
	for _, edge := range plan.Edges {
		if edge.FromChunkID != chunk.ChunkID {
			continue
		}
		if edge.LoopPolicy != nil && edge.LoopPolicy.MaxIterations > 0 {
			cfg.LoopMaxIterations = edge.LoopPolicy.MaxIterations
			break
		}
	}
	if cfg.DefaultBranch == "" && len(cfg.Branches) > 0 {
		cfg.DefaultBranch = cfg.Branches[0].Name
	}
	return cfg
}

func decisionBranchTargets(plan *bridge.ChunkGraphPlan, sourceChunkID string) map[string]string {
	targets := make(map[string]string)
	if plan == nil {
		return targets
	}
	for _, edge := range plan.Edges {
		if edge.FromChunkID != sourceChunkID || !strings.EqualFold(strings.TrimSpace(edge.EdgeKind), "branch") {
			continue
		}
		branch := strings.TrimSpace(edge.Branch)
		if branch == "" {
			continue
		}
		targets[branch] = strings.TrimSpace(edge.ToChunkID)
	}
	return targets
}

func parseDecisionEditorText(text string) (decisionEditorConfig, error) {
	cfg := decisionEditorConfig{}
	lines := strings.Split(text, "\n")
	inBranches := false
	for index, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.EqualFold(line, "branches:") {
			inBranches = true
			continue
		}
		if inBranches {
			name, target, ok := strings.Cut(line, "->")
			if !ok {
				return decisionEditorConfig{}, fmt.Errorf("decision config line %d: expected '<branch> -> <target_chunk_id>'", index+1)
			}
			branch := decisionEditorBranch{
				Name:   strings.TrimSpace(strings.TrimPrefix(name, "-")),
				Target: strings.TrimSpace(target),
			}
			if branch.Name == "" {
				return decisionEditorConfig{}, fmt.Errorf("decision config line %d: branch name cannot be empty", index+1)
			}
			cfg.Branches = append(cfg.Branches, branch)
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return decisionEditorConfig{}, fmt.Errorf("decision config line %d: expected 'key: value'", index+1)
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "mode":
			cfg.Mode = value
		case "default_branch":
			cfg.DefaultBranch = value
		case "title":
			cfg.Title = value
		case "prompt":
			cfg.Prompt = value
		case "loop_max_iterations":
			if value == "" {
				cfg.LoopMaxIterations = 0
				continue
			}
			n, err := strconv.Atoi(value)
			if err != nil {
				return decisionEditorConfig{}, fmt.Errorf("decision config line %d: loop_max_iterations must be an integer", index+1)
			}
			cfg.LoopMaxIterations = n
		default:
			return decisionEditorConfig{}, fmt.Errorf("decision config line %d: unknown key %q", index+1, key)
		}
	}
	return cfg, nil
}

func (m *Model) ReplaceDecisionConfig(chunkID string, cfg decisionEditorConfig) error {
	chunk := m.ChunkByID(strings.TrimSpace(chunkID))
	if chunk == nil {
		return fmt.Errorf("unknown chunk %q", chunkID)
	}
	if !isDecisionChunk(chunk) {
		return nil
	}

	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode == "" {
		mode = "human"
	}
	loopMaxIterations := cfg.LoopMaxIterations
	if loopMaxIterations <= 0 {
		loopMaxIterations = 3
	}
	switch mode {
	case "human", "llm", "llm_then_human":
	default:
		return fmt.Errorf("unsupported decision mode %q", cfg.Mode)
	}

	seenBranches := make(map[string]struct{}, len(cfg.Branches))
	allowed := make([]string, 0, len(cfg.Branches))
	filteredBranches := make([]decisionEditorBranch, 0, len(cfg.Branches))
	for _, branch := range cfg.Branches {
		name := strings.TrimSpace(branch.Name)
		target := strings.TrimSpace(branch.Target)
		if name == "" {
			return fmt.Errorf("decision branch name cannot be empty")
		}
		if target != "" && m.ChunkByID(target) == nil {
			return fmt.Errorf("decision branch %q references unknown target chunk %q", name, target)
		}
		if _, exists := seenBranches[name]; exists {
			return fmt.Errorf("duplicate decision branch %q", name)
		}
		seenBranches[name] = struct{}{}
		allowed = append(allowed, name)
		filteredBranches = append(filteredBranches, decisionEditorBranch{Name: name, Target: target})
	}
	if chunk.Decision == nil {
		chunk.Decision = &bridge.DecisionSpec{}
	}
	if chunk.Decision.Interaction == nil {
		chunk.Decision.Interaction = &bridge.InteractionSpec{}
	}
	chunk.Decision.AllowedBranches = allowed
	chunk.Decision.DefaultBranch = strings.TrimSpace(cfg.DefaultBranch)
	if chunk.Decision.DefaultBranch == "" && len(allowed) > 0 {
		chunk.Decision.DefaultBranch = allowed[0]
	}
	if chunk.Decision.DefaultBranch != "" && !containsString(allowed, chunk.Decision.DefaultBranch) {
		return fmt.Errorf("default branch %q is not present in branches", chunk.Decision.DefaultBranch)
	}

	chunk.Decision.Interaction.Mode = mode
	if chunk.Decision.Interaction.Human == nil {
		chunk.Decision.Interaction.Human = &bridge.HumanInteraction{}
	}
	if chunk.Decision.Interaction.LLM == nil {
		chunk.Decision.Interaction.LLM = &bridge.LLMInteraction{}
	}
	title := strings.TrimSpace(cfg.Title)
	if title == "" {
		title = "Decision"
	}
	prompt := strings.TrimSpace(cfg.Prompt)
	if prompt == "" {
		prompt = "choose branch from {{allowed_branches}}"
	}
	chunk.Decision.Interaction.Human.Title = title
	chunk.Decision.Interaction.LLM.UserPromptTemplate = prompt

	edges := make([]bridge.PlanEdge, 0, len(m.Plan.Edges)+len(filteredBranches))
	for _, edge := range m.Plan.Edges {
		if edge.FromChunkID == chunk.ChunkID &&
			(strings.EqualFold(strings.TrimSpace(edge.EdgeKind), "branch") ||
				strings.EqualFold(strings.TrimSpace(edge.EdgeKind), "loop_back")) {
			continue
		}
		edges = append(edges, edge)
	}
	sort.SliceStable(filteredBranches, func(i, j int) bool {
		if filteredBranches[i].Name != filteredBranches[j].Name {
			return filteredBranches[i].Name < filteredBranches[j].Name
		}
		return filteredBranches[i].Target < filteredBranches[j].Target
	})
	for _, branch := range filteredBranches {
		if strings.TrimSpace(branch.Target) == "" {
			continue
		}
		edgeKind := classifyDecisionEdgeKind(m.Plan, chunk.ChunkID, branch.Target)
		edge := bridge.PlanEdge{
			FromChunkID: chunk.ChunkID,
			ToChunkID:   branch.Target,
			EdgeKind:    edgeKind,
			Branch:      branch.Name,
		}
		if branch.Target != "" {
			edge.LoopPolicy = &bridge.LoopPolicy{MaxIterations: loopMaxIterations}
		}
		edges = append(edges, edge)
	}
	m.Plan.Edges = edges
	m.Dirty = true
	return nil
}
