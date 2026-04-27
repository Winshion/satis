package bridge

import (
	"strings"
	"testing"
)

func TestValidateChunkGraphPlanRejectsMissingDescriptions(t *testing.T) {
	plan := &ChunkGraphPlan{
		PlanID:      "plan_desc",
		IntentID:    "intent_desc",
		Goal:        "Do useful work",
		EntryChunks: []string{"CHK_ROOT"},
		Chunks: []PlanChunk{
			{
				ChunkID: "CHK_ROOT",
				Kind:    "task",
				Source: ChunkSource{
					SatisText: "chunk_id: CHK_ROOT\nintent_uid: intent_desc\nchunk_port: port_root\n\nPwd\n",
				},
			},
		},
	}
	result := ValidateChunkGraphPlan(plan)
	if result.Accepted {
		t.Fatalf("expected validation failure")
	}
	joined := ""
	for _, issue := range result.ValidationErrors {
		joined += issue.Message + "\n"
	}
	if !strings.Contains(joined, "intent_description") {
		t.Fatalf("expected intent_description error, got %#v", result.ValidationErrors)
	}
	if !strings.Contains(joined, "plan_description") {
		t.Fatalf("expected plan_description error, got %#v", result.ValidationErrors)
	}
	if !strings.Contains(joined, "description must be non-empty") {
		t.Fatalf("expected chunk description error, got %#v", result.ValidationErrors)
	}
}
