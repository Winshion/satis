package workbench

import (
	"context"
	"strings"
	"testing"

	"satis/bridge"
)

func testWorkbenchModel() *Model {
	return &Model{
		Plan: &bridge.ChunkGraphPlan{
			IntentID:          "intent_workbench",
			IntentDescription: "完成工作台测试意图",
			PlanDescription:   "验证工作台拓扑编辑行为",
			Chunks: []bridge.PlanChunk{
				{
					ChunkID:     "CHK_ROOT",
					Kind:        "task",
					Description: "输出当前目录",
					Source: bridge.ChunkSource{
						Format:    "satis_v1",
						SatisText: "chunk_id: CHK_ROOT\nintent_uid: intent_workbench\ndescription: 输出当前目录\nchunk_port: port_root\n\nPwd\n",
					},
				},
			},
			EntryChunks: []string{"CHK_ROOT"},
		},
		SelectedChunkID: "CHK_ROOT",
	}
}

func TestSetChunkDescriptionSyncsHeader(t *testing.T) {
	model := testWorkbenchModel()
	if err := model.SetChunkDescription("CHK_ROOT", "读取并检查当前目录"); err != nil {
		t.Fatalf("SetChunkDescription: %v", err)
	}
	chunk := model.ChunkByID("CHK_ROOT")
	if chunk.Description != "读取并检查当前目录" {
		t.Fatalf("unexpected chunk description %q", chunk.Description)
	}
	if !strings.Contains(chunk.Source.SatisText, "description: 读取并检查当前目录") {
		t.Fatalf("expected satis header description sync, got %q", chunk.Source.SatisText)
	}
}

func TestSyncEdgesFromHandoffsAcceptsValidRealtimeChildEdit(t *testing.T) {
	model := testWorkbenchModel()

	childID, _, err := model.AddChildChunk("CHK_ROOT")
	if err != nil {
		t.Fatalf("AddChildChunk: %v", err)
	}
	if err := model.SyncEdgesFromHandoffs(); err != nil {
		t.Fatalf("SyncEdgesFromHandoffs: %v", err)
	}

	child := model.ChunkByID(childID)
	if child == nil {
		t.Fatalf("missing child chunk %q", childID)
	}
	if got, want := child.DependsOn, []string{"CHK_ROOT"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("depends_on mismatch: got %#v want %#v", got, want)
	}
	if len(model.Plan.EntryChunks) != 1 || model.Plan.EntryChunks[0] != "CHK_ROOT" {
		t.Fatalf("unexpected entry chunks: %#v", model.Plan.EntryChunks)
	}
}

func TestSyncEdgesFromHandoffsRejectsSelfReferenceImmediately(t *testing.T) {
	model := testWorkbenchModel()

	childID, _, err := model.AddChildChunk("CHK_ROOT")
	if err != nil {
		t.Fatalf("AddChildChunk: %v", err)
	}
	snapshot, err := model.snapshotPlan()
	if err != nil {
		t.Fatalf("snapshotPlan: %v", err)
	}

	row := HandoffRow{Port: "port_self", FromStep: childID, VarName: "body"}
	if err := model.UpsertHandoffRow(childID, "", row); err != nil {
		t.Fatalf("UpsertHandoffRow: %v", err)
	}
	err = model.SyncEdgesFromHandoffs()
	if err == nil {
		t.Fatalf("expected self-reference error")
	}
	if !strings.Contains(err.Error(), "cannot reference itself") {
		t.Fatalf("unexpected error: %v", err)
	}

	model.Plan = snapshot
	if err := model.SyncEdgesFromHandoffs(); err != nil {
		t.Fatalf("SyncEdgesFromHandoffs after restore: %v", err)
	}
}

func TestBuildDerivedTopologyRejectsMultipleEntries(t *testing.T) {
	model := testWorkbenchModel()
	model.Plan.Chunks = append(model.Plan.Chunks, bridge.PlanChunk{
		ChunkID: "CHK_002",
		Kind:    "task",
		Source: bridge.ChunkSource{
			Format:    "satis_v1",
			SatisText: "chunk_id: CHK_002\nintent_uid: intent_workbench\nchunk_port: port_2\n\nPwd\n",
		},
	})

	_, _, _, err := buildDerivedTopology(model.Plan)
	if err == nil {
		t.Fatalf("expected multiple-entry error")
	}
	if !strings.Contains(err.Error(), "exactly one entry chunk") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRemoveChunkCanRecoverFromInvalidDanglingEdgeDraft(t *testing.T) {
	model := testWorkbenchModel()
	model.Plan.Chunks = append(model.Plan.Chunks, bridge.PlanChunk{
		ChunkID: "CHK_BAD",
		Kind:    "task",
		Source: bridge.ChunkSource{
			Format:    "satis_v1",
			SatisText: "chunk_id: CHK_BAD\nintent_uid: intent_workbench\nchunk_port: port_bad\n\nPwd\n",
		},
	})
	model.Plan.Edges = append(model.Plan.Edges, bridge.PlanEdge{
		FromChunkID: "CHK_ROOT",
		ToChunkID:   "CHK_BAD",
		EdgeKind:    "control",
	})
	if err := model.UpsertHandoffRow("CHK_BAD", "", HandoffRow{Port: "input_1", FromStep: "CHK_BAD", VarName: "body"}); err != nil {
		t.Fatalf("UpsertHandoffRow: %v", err)
	}
	if err := model.SyncEdgesFromHandoffs(); err == nil {
		t.Fatalf("expected invalid draft before delete")
	}

	if err := model.RemoveChunk("CHK_BAD"); err != nil {
		t.Fatalf("RemoveChunk: %v", err)
	}
	if model.ChunkByID("CHK_BAD") != nil {
		t.Fatalf("expected bad chunk removed")
	}
	if err := model.SyncEdgesFromHandoffs(); err != nil {
		t.Fatalf("SyncEdgesFromHandoffs after delete: %v", err)
	}
	if len(model.Plan.EntryChunks) != 1 || model.Plan.EntryChunks[0] != "CHK_ROOT" {
		t.Fatalf("unexpected entry chunks after delete: %#v", model.Plan.EntryChunks)
	}
}

func TestBuildDerivedTopologySkipsRedundantReachableHandoffEdge(t *testing.T) {
	model := testWorkbenchModel()
	if err := model.SetChunkKind("CHK_ROOT", "task"); err != nil {
		t.Fatalf("SetChunkKind(root): %v", err)
	}
	decisionID, _, err := model.AddChildChunk("CHK_ROOT")
	if err != nil {
		t.Fatalf("AddChildChunk(decision): %v", err)
	}
	if err := model.SetChunkKind(decisionID, "decision"); err != nil {
		t.Fatalf("SetChunkKind(decision): %v", err)
	}
	leafID, _, err := model.AddChildChunk(decisionID)
	if err != nil {
		t.Fatalf("AddChildChunk(leaf): %v", err)
	}
	if err := model.AddDecisionBranch(decisionID, "yes", leafID); err != nil {
		t.Fatalf("AddDecisionBranch: %v", err)
	}
	if err := model.UpsertHandoffRow(leafID, "", HandoffRow{Port: "port_root", FromStep: "CHK_ROOT", VarName: "intro"}); err != nil {
		t.Fatalf("UpsertHandoffRow: %v", err)
	}
	edges, _, _, err := buildDerivedTopology(model.Plan)
	if err != nil {
		t.Fatalf("buildDerivedTopology: %v", err)
	}
	for _, edge := range edges {
		if edge.FromChunkID == "CHK_ROOT" && edge.ToChunkID == leafID && strings.EqualFold(edge.EdgeKind, "handoff") {
			t.Fatalf("expected redundant reachable handoff edge to be omitted, got %#v", edge)
		}
	}
}

func TestSyncHandoffsFromBodyIgnoresChunkLocalOutputAliases(t *testing.T) {
	model := testWorkbenchModel()
	root := model.ChunkByID("CHK_ROOT")
	root.Source.SatisText = "chunk_id: CHK_ROOT\nintent_uid: intent_workbench\nchunk_port: port_root\n\ninvoke [[[who is qianxuesen?]]] as @port_root__result\nprint @port_root__result\n"

	rows, err := model.SyncHandoffsFromBody("CHK_ROOT")
	if err != nil {
		t.Fatalf("SyncHandoffsFromBody: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected no inferred handoff rows for chunk-local output alias reuse, got %#v", rows)
	}
}

func TestBuildDerivedTopologyTreatsDecisionBackEdgeAsLoopBack(t *testing.T) {
	model := testWorkbenchModel()
	decisionID, _, err := model.AddChildChunk("CHK_ROOT")
	if err != nil {
		t.Fatalf("AddChildChunk(decision): %v", err)
	}
	if err := model.SetChunkKind(decisionID, "decision"); err != nil {
		t.Fatalf("SetChunkKind(decision): %v", err)
	}
	leafID, _, err := model.AddChildChunk(decisionID)
	if err != nil {
		t.Fatalf("AddChildChunk(leaf): %v", err)
	}
	if err := model.AddDecisionBranch(decisionID, "yes", leafID); err != nil {
		t.Fatalf("AddDecisionBranch yes: %v", err)
	}
	if err := model.AddDecisionBranch(decisionID, "no", "CHK_ROOT"); err != nil {
		t.Fatalf("AddDecisionBranch no: %v", err)
	}
	if err := model.SyncEdgesFromHandoffs(); err != nil {
		t.Fatalf("SyncEdgesFromHandoffs: %v", err)
	}

	if len(model.Plan.EntryChunks) != 1 || model.Plan.EntryChunks[0] != "CHK_ROOT" {
		t.Fatalf("unexpected entry chunks after loop back: %#v", model.Plan.EntryChunks)
	}

	foundLoopBack := false
	for _, edge := range model.Plan.Edges {
		if edge.FromChunkID == decisionID && edge.ToChunkID == "CHK_ROOT" && strings.EqualFold(edge.EdgeKind, "loop_back") {
			foundLoopBack = true
		}
	}
	if !foundLoopBack {
		t.Fatalf("expected decision back edge to be classified as loop_back, got %#v", model.Plan.Edges)
	}
}

func TestLinkPlanChainUpdatesHiddenRegistry(t *testing.T) {
	backend := &memoryWorkbenchBackend{files: map[string]string{}}
	currentPath := "/demo/a/plan.json"
	nextPath := "/demo/b/plan.json"

	if err := linkPlanChain(context.Background(), backend, currentPath, nextPath); err != nil {
		t.Fatalf("linkPlanChain: %v", err)
	}

	currentLinks, err := readPlanChainLinks(context.Background(), backend, currentPath)
	if err != nil {
		t.Fatalf("readPlanChainLinks(current): %v", err)
	}
	if currentLinks.Next != nextPath {
		t.Fatalf("expected next=%q, got %#v", nextPath, currentLinks)
	}
	nextLinks, err := readPlanChainLinks(context.Background(), backend, nextPath)
	if err != nil {
		t.Fatalf("readPlanChainLinks(next): %v", err)
	}
	if nextLinks.Prev != currentPath {
		t.Fatalf("expected prev=%q, got %#v", currentPath, nextLinks)
	}
	if _, ok := backend.files["/demo/a/.plan_registry.json"]; !ok {
		t.Fatalf("expected hidden registry file for current plan")
	}
	if _, ok := backend.files["/demo/b/.plan_registry.json"]; !ok {
		t.Fatalf("expected hidden registry file for next plan")
	}
}

func TestDetachPlanChainClearsReverseLink(t *testing.T) {
	backend := &memoryWorkbenchBackend{files: map[string]string{}}
	currentPath := "/demo/a/plan.json"
	nextPath := "/demo/b/plan.json"
	if err := linkPlanChain(context.Background(), backend, currentPath, nextPath); err != nil {
		t.Fatalf("linkPlanChain: %v", err)
	}

	detached, err := detachPlanChain(context.Background(), backend, currentPath)
	if err != nil {
		t.Fatalf("detachPlanChain: %v", err)
	}
	if detached != nextPath {
		t.Fatalf("expected detached path %q, got %q", nextPath, detached)
	}
	currentLinks, err := readPlanChainLinks(context.Background(), backend, currentPath)
	if err != nil {
		t.Fatalf("readPlanChainLinks(current): %v", err)
	}
	if currentLinks.Next != "" {
		t.Fatalf("expected current next cleared, got %#v", currentLinks)
	}
	nextLinks, err := readPlanChainLinks(context.Background(), backend, nextPath)
	if err != nil {
		t.Fatalf("readPlanChainLinks(next): %v", err)
	}
	if nextLinks.Prev != "" {
		t.Fatalf("expected next prev cleared, got %#v", nextLinks)
	}
}
