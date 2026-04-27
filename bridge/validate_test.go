package bridge

import (
	"strings"
	"testing"
)

func fillValidationTestPlanDescriptions(plan *ChunkGraphPlan) {
	if plan == nil {
		return
	}
	if strings.TrimSpace(plan.IntentDescription) == "" {
		plan.IntentDescription = "test intent"
	}
	if strings.TrimSpace(plan.PlanDescription) == "" {
		plan.PlanDescription = "test plan"
	}
	for i := range plan.Chunks {
		chunk := &plan.Chunks[i]
		if strings.TrimSpace(chunk.Description) == "" {
			chunk.Description = "test chunk " + chunk.ChunkID
		}
		if strings.TrimSpace(extractHeaderValue(chunk.Source.SatisText, "description")) == "" {
			chunk.Source.SatisText = setHeaderValue(chunk.Source.SatisText, "description", chunk.Description)
		}
	}
}

func TestValidateChunkGraphPlanAllowsReachableNonAdjacentHandoffSource(t *testing.T) {
	plan := &ChunkGraphPlan{
		PlanID:      "plan",
		IntentID:    "intent",
		Goal:        "goal",
		EntryChunks: []string{"CHK_ROOT"},
		Chunks: []PlanChunk{
			{
				ChunkID: "CHK_ROOT",
				Kind:    "task",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_ROOT\nintent_uid: intent\nchunk_port: port_root\n\nconcat [[[hi]]] as @port_root__intro\n"},
			},
			{
				ChunkID: "CHK_DECISION",
				Kind:    "decision",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_DECISION\nintent_uid: intent\nchunk_port: port_decision\n\nPwd\n"},
				Decision: &DecisionSpec{
					AllowedBranches: []string{"yes"},
					DefaultBranch:   "yes",
					Interaction:     &InteractionSpec{Mode: "human", Human: &HumanInteraction{Title: "review"}},
				},
			},
			{
				ChunkID: "CHK_LEAF",
				Kind:    "task",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_LEAF\nintent_uid: intent\nchunk_port: port_leaf\n\nprint @port_root__intro\n"},
				Inputs: map[string]any{
					"handoff_inputs": map[string]any{
						"port_root__intro": map[string]any{
							"from_step": "CHK_ROOT",
							"var_name":  "intro",
						},
					},
				},
			},
		},
		Edges: []PlanEdge{
			{FromChunkID: "CHK_ROOT", ToChunkID: "CHK_DECISION", EdgeKind: "control"},
			{FromChunkID: "CHK_DECISION", ToChunkID: "CHK_LEAF", EdgeKind: "branch", Branch: "yes"},
		},
	}
	fillValidationTestPlanDescriptions(plan)
	result := ValidateChunkGraphPlan(plan)
	if !result.Accepted {
		t.Fatalf("expected reachable non-adjacent handoff to be valid, got issues: %#v", result.ValidationErrors)
	}
}

func TestValidateChunkGraphPlanRejectsUnreachableHandoffSource(t *testing.T) {
	plan := &ChunkGraphPlan{
		PlanID:      "plan",
		IntentID:    "intent",
		Goal:        "goal",
		EntryChunks: []string{"CHK_ROOT"},
		Chunks: []PlanChunk{
			{
				ChunkID: "CHK_ROOT",
				Kind:    "task",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_ROOT\nintent_uid: intent\nchunk_port: port_root\n\nconcat [[[hi]]] as @port_root__intro\n"},
			},
			{
				ChunkID: "CHK_SIDE",
				Kind:    "task",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_SIDE\nintent_uid: intent\nchunk_port: port_side\n\nPwd\n"},
			},
			{
				ChunkID: "CHK_LEAF",
				Kind:    "task",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_LEAF\nintent_uid: intent\nchunk_port: port_leaf\n\nprint @port_root__intro\n"},
				Inputs: map[string]any{
					"handoff_inputs": map[string]any{
						"port_root__intro": map[string]any{
							"from_step": "CHK_ROOT",
							"var_name":  "intro",
						},
					},
				},
			},
		},
		Edges: []PlanEdge{
			{FromChunkID: "CHK_SIDE", ToChunkID: "CHK_LEAF", EdgeKind: "control"},
		},
	}
	fillValidationTestPlanDescriptions(plan)
	result := ValidateChunkGraphPlan(plan)
	if result.Accepted {
		t.Fatalf("expected unreachable handoff source to fail validation")
	}
	found := false
	for _, issue := range result.ValidationErrors {
		if strings.Contains(issue.Message, "not a reachable upstream chunk") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected reachable-upstream validation issue, got %#v", result.ValidationErrors)
	}
}

func TestValidateChunkGraphPlanAllowsChunkLocalOutputAliasReuse(t *testing.T) {
	plan := &ChunkGraphPlan{
		PlanID:      "plan",
		IntentID:    "intent",
		Goal:        "goal",
		EntryChunks: []string{"CHK_ROOT"},
		Chunks: []PlanChunk{
			{
				ChunkID: "CHK_ROOT",
				Kind:    "task",
				Source: ChunkSource{
					SatisText: "chunk_id: CHK_ROOT\nintent_uid: intent\nchunk_port: port_root\n\ninvoke [[[who is qianxuesen?]]] as @port_root__result\nprint @port_root__result\n",
				},
			},
		},
	}
	fillValidationTestPlanDescriptions(plan)
	result := ValidateChunkGraphPlan(plan)
	if !result.Accepted {
		t.Fatalf("expected chunk-local output alias reuse to be valid, got issues: %#v", result.ValidationErrors)
	}
}
