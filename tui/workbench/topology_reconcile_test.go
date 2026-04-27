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

// TestFanInTopologyValidatesAfterAutoSave reproduces the bug where creating a
// "final" chunk that consumes handoff inputs from two parallel branches
// (ROOT → CHK_A, ROOT → CHK_B, both feeding CHK_FINAL) would always fail with
// "upstream chunk not reachable" during auto-save ValidateAndNormalize, even
// though the plan topology was structurally correct.
//
// Topology:
//
//	CHK_ROOT ──control──► CHK_A ──control──► CHK_FINAL
//	CHK_ROOT ──control──► CHK_B ──handoff──► CHK_FINAL
func TestFanInTopologyValidatesAfterAutoSave(t *testing.T) {
	// Build the base model with a ROOT that outputs @port_root__output.
	model := &Model{
		Plan: &bridge.ChunkGraphPlan{
			IntentID:          "intent_fanin_wb",
			IntentDescription: "fan-in workbench test",
			PlanDescription:   "fan-in plan validation",
			Chunks: []bridge.PlanChunk{
				{
					ChunkID:     "CHK_ROOT",
					Kind:        "task",
					Description: "输出立直麻将介绍",
					Source: bridge.ChunkSource{
						Format:    "satis_v1",
						SatisText: "chunk_id: CHK_ROOT\nintent_uid: intent_fanin_wb\ndescription: 输出立直麻将介绍\nchunk_port: port_root\n\ninvoke [[[简短介绍立直麻将]]] as @port_root__output\n",
					},
				},
			},
			EntryChunks: []string{"CHK_ROOT"},
		},
		SelectedChunkID: "CHK_ROOT",
	}

	// Step 1: Add CHK_A as child of CHK_ROOT (translates to Japanese).
	chkAID, _, err := model.AddChildChunk("CHK_ROOT")
	if err != nil {
		t.Fatalf("AddChildChunk(CHK_A): %v", err)
	}
	chkA := model.ChunkByID(chkAID)
	if chkA == nil {
		t.Fatalf("missing CHK_A chunk")
	}
	chkA.Source.SatisText = "chunk_id: " + chkAID + "\nintent_uid: intent_fanin_wb\ndescription: 翻译到日文\nchunk_port: port_a\n\ninvoke [[[翻译到日文：]]] @port_root__output as @port_a__japanese\n"
	chkA.Description = "翻译到日文"

	// Step 2: Add CHK_B as child of CHK_ROOT (translates to English).
	model.SelectedChunkID = "CHK_ROOT"
	chkBID, _, err := model.AddChildChunk("CHK_ROOT")
	if err != nil {
		t.Fatalf("AddChildChunk(CHK_B): %v", err)
	}
	chkB := model.ChunkByID(chkBID)
	if chkB == nil {
		t.Fatalf("missing CHK_B chunk")
	}
	chkB.Source.SatisText = "chunk_id: " + chkBID + "\nintent_uid: intent_fanin_wb\ndescription: 翻译到英文\nchunk_port: port_b\n\ninvoke [[[翻译到英文：]]] @port_root__output as @port_b__english\n"
	chkB.Description = "翻译到英文"

	// Step 3: Add CHK_FINAL as child of CHK_A (fan-in node, reads from both A and B).
	model.SelectedChunkID = chkAID
	chkFinalID, _, err := model.AddChildChunk(chkAID)
	if err != nil {
		t.Fatalf("AddChildChunk(CHK_FINAL): %v", err)
	}
	chkFinal := model.ChunkByID(chkFinalID)
	if chkFinal == nil {
		t.Fatalf("missing CHK_FINAL chunk")
	}
	// Manually set the fan-in body: consumes @port_a__japanese AND @port_b__english.
	chkFinal.Source.SatisText = "chunk_id: " + chkFinalID + "\nintent_uid: intent_fanin_wb\ndescription: 合并翻译结果\nchunk_port: port_final\n\nconcat @port_a__japanese @port_b__english as @port_final__merged\n"
	chkFinal.Description = "合并翻译结果"
	// Declare handoff_inputs for both upstream branches.
	chkFinal.Inputs = map[string]any{
		"handoff_inputs": map[string]any{
			"port_a__japanese": map[string]any{
				"from_step": chkAID,
				"var_name":  "japanese",
			},
			"port_b__english": map[string]any{
				"from_step": chkBID,
				"var_name":  "english",
			},
		},
	}

	// Step 4: Sync edges from handoff declarations (mimics auto-save syncPlanTopology).
	if err := model.SyncEdgesFromHandoffs(); err != nil {
		t.Fatalf("SyncEdgesFromHandoffs: %v", err)
	}

	// Verify that a handoff edge CHK_B → CHK_FINAL was generated.
	foundHandoff := false
	for _, edge := range model.Plan.Edges {
		if edge.FromChunkID == chkBID && edge.ToChunkID == chkFinalID &&
			strings.EqualFold(strings.TrimSpace(edge.EdgeKind), "handoff") {
			foundHandoff = true
			break
		}
	}
	if !foundHandoff {
		t.Fatalf("expected handoff edge %s → %s to be generated, edges: %#v", chkBID, chkFinalID, model.Plan.Edges)
	}

	// Step 5: ValidateAndNormalize (this is what Workbench auto-save calls).
	// Before the fix this always returned "upstream chunk not reachable" for CHK_B.
	if err := model.ValidateAndNormalize(); err != nil {
		t.Fatalf("ValidateAndNormalize failed (fan-in handoff edge must be accepted): %v", err)
	}

	// Sanity: entry chunk is still CHK_ROOT.
	if len(model.Plan.EntryChunks) != 1 || model.Plan.EntryChunks[0] != "CHK_ROOT" {
		t.Fatalf("unexpected entry chunks: %#v", model.Plan.EntryChunks)
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
