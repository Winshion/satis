package workbench

import (
	"strings"
	"testing"

	"satis/bridge"
)

func fillDecisionEditorTestPlan(plan *bridge.ChunkGraphPlan) {
	if plan == nil {
		return
	}
	if strings.TrimSpace(plan.IntentDescription) == "" {
		plan.IntentDescription = "完成决策编辑测试意图"
	}
	if strings.TrimSpace(plan.PlanDescription) == "" {
		plan.PlanDescription = "验证决策编辑器行为"
	}
	for i := range plan.Chunks {
		chunk := &plan.Chunks[i]
		if strings.TrimSpace(chunk.Description) == "" {
			chunk.Description = "执行测试 chunk " + chunk.ChunkID
		}
		if !strings.Contains(chunk.Source.SatisText, "\ndescription: ") {
			chunk.Source.SatisText = setChunkHeaderValue(chunk.Source.SatisText, chunkDescriptionMetaKey, chunk.Description)
		}
	}
}

func TestReplaceDecisionConfigAllowsBranchesWithoutTargets(t *testing.T) {
	model := &Model{
		Plan: &bridge.ChunkGraphPlan{
			IntentID: "intent_workbench",
			Chunks: []bridge.PlanChunk{
				{
					ChunkID: "CHK_001",
					Kind:    "decision",
					Decision: &bridge.DecisionSpec{
						AllowedBranches: []string{"yes", "no"},
						DefaultBranch:   "yes",
						Interaction: &bridge.InteractionSpec{
							Mode:  "human",
							Human: &bridge.HumanInteraction{Title: "Decision"},
						},
					},
				},
			},
		},
	}
	fillDecisionEditorTestPlan(model.Plan)

	cfg := decisionEditorConfig{
		Mode:          "human",
		DefaultBranch: "yes",
		Title:         "Decision",
		Prompt:        "choose branch from {{allowed_branches}}",
		Branches: []decisionEditorBranch{
			{Name: "yes", Target: ""},
			{Name: "no", Target: ""},
		},
	}
	if err := model.ReplaceDecisionConfig("CHK_001", cfg); err != nil {
		t.Fatalf("ReplaceDecisionConfig returned error: %v", err)
	}

	chunk := model.ChunkByID("CHK_001")
	if chunk == nil || chunk.Decision == nil {
		t.Fatalf("expected decision chunk to remain present")
	}
	if got, want := chunk.Decision.AllowedBranches, []string{"yes", "no"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("allowed branches mismatch: got %#v want %#v", got, want)
	}
	if len(model.Plan.Edges) != 0 {
		t.Fatalf("expected no branch edges when targets are blank, got %#v", model.Plan.Edges)
	}
}

func TestSetChunkKindDecisionConfigRoundTripsThroughEditorTemplate(t *testing.T) {
	model := &Model{
		Plan: &bridge.ChunkGraphPlan{
			IntentID: "intent_workbench",
			Chunks: []bridge.PlanChunk{
				{
					ChunkID: "CHK_001",
					Kind:    "task",
					Source: bridge.ChunkSource{
						Format:    "satis_v1",
						SatisText: "chunk_id: CHK_001\nintent_uid: intent_workbench\n\nPwd\n",
					},
				},
			},
		},
	}
	fillDecisionEditorTestPlan(model.Plan)

	if err := model.SetChunkKind("CHK_001", "decision"); err != nil {
		t.Fatalf("SetChunkKind: %v", err)
	}

	chunk := model.ChunkByID("CHK_001")
	cfg, err := parseDecisionEditorText(formatDecisionEditorText(model.Plan, chunk))
	if err != nil {
		t.Fatalf("parseDecisionEditorText: %v", err)
	}
	if err := model.ReplaceDecisionConfig("CHK_001", cfg); err != nil {
		t.Fatalf("ReplaceDecisionConfig after template round-trip: %v", err)
	}
	if len(model.Plan.Edges) != 0 {
		t.Fatalf("expected no implicit branch edges after round-trip, got %#v", model.Plan.Edges)
	}
}

func TestAddChildDecisionChunkRoundTripsWithoutMissingTargetError(t *testing.T) {
	model := &Model{
		Plan: &bridge.ChunkGraphPlan{
			IntentID: "intent_workbench",
			Chunks: []bridge.PlanChunk{
				{
					ChunkID: "CHK_ROOT",
					Kind:    "task",
					Source: bridge.ChunkSource{
						Format:    "satis_v1",
						SatisText: "chunk_id: CHK_ROOT\nintent_uid: intent_workbench\nchunk_port: port_root\n\nPwd\n",
					},
				},
			},
			EntryChunks: []string{"CHK_ROOT"},
		},
		SelectedChunkID: "CHK_ROOT",
	}
	fillDecisionEditorTestPlan(model.Plan)

	childID, _, err := model.AddChildChunk("CHK_ROOT")
	if err != nil {
		t.Fatalf("AddChildChunk: %v", err)
	}
	if err := model.SetChunkKind(childID, "decision"); err != nil {
		t.Fatalf("SetChunkKind(decision): %v", err)
	}

	chunk := model.ChunkByID(childID)
	cfg, err := parseDecisionEditorText(formatDecisionEditorText(model.Plan, chunk))
	if err != nil {
		t.Fatalf("parseDecisionEditorText: %v", err)
	}
	if err := model.ReplaceDecisionConfig(childID, cfg); err != nil {
		t.Fatalf("ReplaceDecisionConfig: %v", err)
	}
	if err := model.ValidateAndNormalize(); err != nil {
		t.Fatalf("ValidateAndNormalize: %v", err)
	}
}

func TestDecisionConfigBackEdgeGetsDefaultLoopMaxIterations(t *testing.T) {
	model := &Model{
		Plan: &bridge.ChunkGraphPlan{
			IntentID: "intent_workbench",
			Chunks: []bridge.PlanChunk{
				{
					ChunkID: "CHK_ROOT",
					Kind:    "task",
					Source: bridge.ChunkSource{
						Format:    "satis_v1",
						SatisText: "chunk_id: CHK_ROOT\nintent_uid: intent_workbench\nchunk_port: port_root\n\nPwd\n",
					},
				},
				{
					ChunkID: "CHK_001",
					Kind:    "decision",
					Decision: &bridge.DecisionSpec{
						AllowedBranches: []string{"yes"},
						DefaultBranch:   "yes",
						Interaction: &bridge.InteractionSpec{
							Mode:  "human",
							Human: &bridge.HumanInteraction{Title: "Decision"},
						},
					},
				},
			},
			Edges: []bridge.PlanEdge{{
				FromChunkID: "CHK_ROOT",
				ToChunkID:   "CHK_001",
				EdgeKind:    "control",
			}},
			EntryChunks: []string{"CHK_ROOT"},
		},
	}
	fillDecisionEditorTestPlan(model.Plan)

	cfg := decisionEditorConfig{
		Mode:          "human",
		DefaultBranch: "yes",
		Title:         "Decision",
		Prompt:        "choose branch from {{allowed_branches}}",
		Branches: []decisionEditorBranch{
			{Name: "yes", Target: "CHK_ROOT"},
		},
	}
	if err := model.ReplaceDecisionConfig("CHK_001", cfg); err != nil {
		t.Fatalf("ReplaceDecisionConfig: %v", err)
	}
	if err := model.ValidateAndNormalize(); err != nil {
		t.Fatalf("ValidateAndNormalize: %v", err)
	}

	var backEdge *bridge.PlanEdge
	for i := range model.Plan.Edges {
		edge := &model.Plan.Edges[i]
		if edge.FromChunkID == "CHK_001" && edge.ToChunkID == "CHK_ROOT" {
			backEdge = edge
			break
		}
	}
	if backEdge == nil {
		t.Fatalf("expected back edge to CHK_ROOT")
	}
	if backEdge.LoopPolicy == nil || backEdge.LoopPolicy.MaxIterations != 3 {
		t.Fatalf("expected default loop max_iterations=3, got %#v", backEdge.LoopPolicy)
	}
}

func TestDecisionConfigRoundTripsCustomLoopMaxIterations(t *testing.T) {
	model := &Model{
		Plan: &bridge.ChunkGraphPlan{
			IntentID: "intent_workbench",
			Chunks: []bridge.PlanChunk{
				{
					ChunkID: "CHK_ROOT",
					Kind:    "task",
					Source: bridge.ChunkSource{
						Format:    "satis_v1",
						SatisText: "chunk_id: CHK_ROOT\nintent_uid: intent_workbench\nchunk_port: port_root\n\nPwd\n",
					},
				},
				{
					ChunkID: "CHK_001",
					Kind:    "decision",
					Decision: &bridge.DecisionSpec{
						AllowedBranches: []string{"retry"},
						DefaultBranch:   "retry",
						Interaction: &bridge.InteractionSpec{
							Mode:  "human",
							Human: &bridge.HumanInteraction{Title: "Decision"},
						},
					},
				},
			},
			Edges: []bridge.PlanEdge{
				{
					FromChunkID: "CHK_ROOT",
					ToChunkID:   "CHK_001",
					EdgeKind:    "control",
				},
				{
					FromChunkID: "CHK_001",
					ToChunkID:   "CHK_ROOT",
					EdgeKind:    "branch",
					Branch:      "retry",
					LoopPolicy:  &bridge.LoopPolicy{MaxIterations: 7},
				},
			},
			EntryChunks: []string{"CHK_ROOT"},
		},
	}
	fillDecisionEditorTestPlan(model.Plan)

	text := formatDecisionEditorText(model.Plan, model.ChunkByID("CHK_001"))
	if want := "loop_max_iterations: 7"; !strings.Contains(text, want) {
		t.Fatalf("expected formatted config to contain %q, got:\n%s", want, text)
	}
	cfg, err := parseDecisionEditorText(text)
	if err != nil {
		t.Fatalf("parseDecisionEditorText: %v", err)
	}
	if cfg.LoopMaxIterations != 7 {
		t.Fatalf("expected parsed loop max_iterations 7, got %d", cfg.LoopMaxIterations)
	}
	cfg.LoopMaxIterations = 9
	if err := model.ReplaceDecisionConfig("CHK_001", cfg); err != nil {
		t.Fatalf("ReplaceDecisionConfig: %v", err)
	}
	if err := model.ValidateAndNormalize(); err != nil {
		t.Fatalf("ValidateAndNormalize: %v", err)
	}
	for _, edge := range model.Plan.Edges {
		if edge.FromChunkID == "CHK_001" && edge.ToChunkID == "CHK_ROOT" {
			if edge.LoopPolicy == nil || edge.LoopPolicy.MaxIterations != 9 {
				t.Fatalf("expected updated loop max_iterations=9, got %#v", edge.LoopPolicy)
			}
			return
		}
	}
	t.Fatalf("expected branch edge after replace")
}
