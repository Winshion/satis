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

// TestValidateChunkGraphPlanAllowsFanInHandoffEdge verifies that a chunk
// collecting outputs from two parallel branches is accepted when one branch
// reaches the fan-in node via a control edge and the other via a handoff edge.
// This is the concrete topology reported as broken:
//
//	ROOT ──control──► CHK_A ──control──► FINAL
//	ROOT ──control──► CHK_B ──handoff──► FINAL
func TestValidateChunkGraphPlanAllowsFanInHandoffEdge(t *testing.T) {
	plan := &ChunkGraphPlan{
		PlanID:      "plan_fanin",
		IntentID:    "intent_fanin",
		Goal:        "goal",
		EntryChunks: []string{"CHK_ROOT"},
		Chunks: []PlanChunk{
			{
				ChunkID: "CHK_ROOT",
				Kind:    "task",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_ROOT\nintent_uid: intent_fanin\nchunk_port: port_root\n\ninvoke [[[介绍立直麻将]]] as @port_root__output\n"},
			},
			{
				ChunkID: "CHK_A",
				Kind:    "task",
				Source: ChunkSource{SatisText: "chunk_id: CHK_A\nintent_uid: intent_fanin\nchunk_port: port_a\n\ninvoke [[[翻译到日文：]]] @port_root__output as @port_a__japanese\n"},
				Inputs: map[string]any{
					"handoff_inputs": map[string]any{
						"port_root__output": map[string]any{
							"from_step": "CHK_ROOT",
							"var_name":  "output",
						},
					},
				},
			},
			{
				ChunkID: "CHK_B",
				Kind:    "task",
				Source: ChunkSource{SatisText: "chunk_id: CHK_B\nintent_uid: intent_fanin\nchunk_port: port_b\n\ninvoke [[[翻译到英文：]]] @port_root__output as @port_b__english\n"},
				Inputs: map[string]any{
					"handoff_inputs": map[string]any{
						"port_root__output": map[string]any{
							"from_step": "CHK_ROOT",
							"var_name":  "output",
						},
					},
				},
			},
			{
				ChunkID: "CHK_FINAL",
				Kind:    "task",
				Source: ChunkSource{SatisText: "chunk_id: CHK_FINAL\nintent_uid: intent_fanin\nchunk_port: port_final\n\nconcat @port_a__japanese @port_b__english as @port_final__merged\n"},
				Inputs: map[string]any{
					"handoff_inputs": map[string]any{
						"port_a__japanese": map[string]any{
							"from_step": "CHK_A",
							"var_name":  "japanese",
						},
						"port_b__english": map[string]any{
							"from_step": "CHK_B",
							"var_name":  "english",
						},
					},
				},
			},
		},
		Edges: []PlanEdge{
			{FromChunkID: "CHK_ROOT", ToChunkID: "CHK_A", EdgeKind: "control"},
			{FromChunkID: "CHK_ROOT", ToChunkID: "CHK_B", EdgeKind: "control"},
			{FromChunkID: "CHK_A", ToChunkID: "CHK_FINAL", EdgeKind: "control"},
			// CHK_B reaches CHK_FINAL only via a handoff edge; this is the
			// fan-in case that previously caused a false "not reachable" error.
			{FromChunkID: "CHK_B", ToChunkID: "CHK_FINAL", EdgeKind: "handoff"},
		},
	}
	fillValidationTestPlanDescriptions(plan)
	result := ValidateChunkGraphPlan(plan)
	if !result.Accepted {
		t.Fatalf("expected fan-in plan with handoff edge to be valid, got issues: %#v", result.ValidationErrors)
	}
}

// TestValidateChunkGraphPlanRejectsTrulyUnrelatedHandoffSource ensures that a
// chunk referencing a from_step that has NO path to it (neither control nor
// handoff) is still rejected after the fan-in fix.
func TestValidateChunkGraphPlanRejectsTrulyUnrelatedHandoffSource(t *testing.T) {
	plan := &ChunkGraphPlan{
		PlanID:      "plan_unrelated",
		IntentID:    "intent_unrelated",
		Goal:        "goal",
		EntryChunks: []string{"CHK_ROOT"},
		Chunks: []PlanChunk{
			{
				ChunkID: "CHK_ROOT",
				Kind:    "task",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_ROOT\nintent_uid: intent_unrelated\nchunk_port: port_root\n\nconcat [[[hi]]] as @port_root__intro\n"},
			},
			{
				ChunkID: "CHK_ORPHAN",
				Kind:    "task",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_ORPHAN\nintent_uid: intent_unrelated\nchunk_port: port_orphan\n\nPwd\n"},
			},
			{
				ChunkID: "CHK_LEAF",
				Kind:    "task",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_LEAF\nintent_uid: intent_unrelated\nchunk_port: port_leaf\n\nprint @port_root__intro\n"},
				Inputs: map[string]any{
					"handoff_inputs": map[string]any{
						"port_root__intro": map[string]any{
							// from_step references CHK_ROOT, but there is no path
							// (control or handoff) from CHK_ROOT to CHK_LEAF.
							"from_step": "CHK_ROOT",
							"var_name":  "intro",
						},
					},
				},
			},
		},
		Edges: []PlanEdge{
			// Only edge connects CHK_ORPHAN → CHK_LEAF; CHK_ROOT is isolated.
			{FromChunkID: "CHK_ORPHAN", ToChunkID: "CHK_LEAF", EdgeKind: "control"},
		},
	}
	fillValidationTestPlanDescriptions(plan)
	result := ValidateChunkGraphPlan(plan)
	if result.Accepted {
		t.Fatalf("expected truly-unrelated handoff source to fail validation")
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
