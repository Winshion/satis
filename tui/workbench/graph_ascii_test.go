package workbench

import (
	"testing"

	"satis/bridge"
)

func TestBuildGraphAdjacencyIncludesHandoffEdges(t *testing.T) {
	plan := &bridge.ChunkGraphPlan{
		Chunks: []bridge.PlanChunk{
			{ChunkID: "CHK_ROOT", Kind: "task"},
			{ChunkID: "CHK_A", Kind: "task"},
			{ChunkID: "CHK_B", Kind: "task"},
			{ChunkID: "CHK_FINAL", Kind: "task"},
		},
		Edges: []bridge.PlanEdge{
			{FromChunkID: "CHK_ROOT", ToChunkID: "CHK_A", EdgeKind: "control"},
			{FromChunkID: "CHK_ROOT", ToChunkID: "CHK_B", EdgeKind: "control"},
			{FromChunkID: "CHK_A", ToChunkID: "CHK_FINAL", EdgeKind: "control"},
			// Fan-in branch that only reaches final via handoff should still be rendered.
			{FromChunkID: "CHK_B", ToChunkID: "CHK_FINAL", EdgeKind: "handoff"},
		},
	}

	parents, children := buildGraphAdjacency(plan)

	if len(parents["CHK_FINAL"]) != 2 {
		t.Fatalf("expected CHK_FINAL to have two rendered parents, got %#v", parents["CHK_FINAL"])
	}
	if !(containsString(parents["CHK_FINAL"], "CHK_A") && containsString(parents["CHK_FINAL"], "CHK_B")) {
		t.Fatalf("expected CHK_FINAL parents to include CHK_A and CHK_B, got %#v", parents["CHK_FINAL"])
	}
	if !containsString(children["CHK_B"], "CHK_FINAL") {
		t.Fatalf("expected CHK_B -> CHK_FINAL to be rendered in children map, got %#v", children["CHK_B"])
	}
}

