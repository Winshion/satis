package bridge

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"satis/sandbox"
	"satis/satis"
	"satis/vfs"
)

func fillTestPlanDescriptions(plan *ChunkGraphPlan) {
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

func TestStreamRunEventsAfterIndexAndOverview(t *testing.T) {
	srv := NewServer(vfs.Config{}, &stubBridgeVFS{}, nil)
	record := &runRecord{
		status: RunStatus{RunID: "run_1", PlanID: "plan", Status: RunPhaseRunning, StartedAt: time.Now().UTC()},
		chunks: map[string]ChunkExecutionResult{},
	}
	srv.mu.Lock()
	srv.runs["run_1"] = record
	srv.invalidateRunsCacheLocked()
	srv.mu.Unlock()

	srv.appendEvent(record, "", EventRunStarted, map[string]any{"step": 1})
	srv.appendEvent(record, "chunk_a", EventChunkStarted, map[string]any{"step": 2})
	srv.appendEvent(record, "chunk_a", EventChunkSucceeded, map[string]any{"step": 3})

	full, err := srv.StreamRunEvents(RunIDParams{RunID: "run_1"})
	if err != nil {
		t.Fatalf("stream full: %v", err)
	}
	if len(full.Events) != 3 || full.NextIndex != 3 || full.EventsVersion != 3 {
		t.Fatalf("unexpected full events result: %#v", full)
	}

	incremental, err := srv.StreamRunEvents(RunIDParams{RunID: "run_1", AfterIndex: 2})
	if err != nil {
		t.Fatalf("stream incremental: %v", err)
	}
	if len(incremental.Events) != 1 || incremental.Events[0].Type != EventChunkSucceeded {
		t.Fatalf("unexpected incremental events result: %#v", incremental)
	}

	overview, err := srv.InspectRunOverview(RunIDParams{RunID: "run_1"})
	if err != nil {
		t.Fatalf("inspect overview: %v", err)
	}
	if overview.EventCount != 3 || overview.EventsVersion != 3 {
		t.Fatalf("unexpected overview result: %#v", overview)
	}
}

func TestNewServerDiscardsExecutorStdoutByDefault(t *testing.T) {
	srv := NewServer(vfs.Config{}, &stubBridgeVFS{}, nil)
	if srv.executor == nil {
		t.Fatal("expected bridge executor to be initialized")
	}
	if srv.executor.Stdout != io.Discard {
		t.Fatalf("expected bridge executor stdout to default to io.Discard, got %#v", srv.executor.Stdout)
	}
}

func TestStartRunWritesOnlyToConfiguredExecutorStdout(t *testing.T) {
	srv := NewServer(vfs.Config{}, &stubBridgeVFS{}, nil)
	var stdout bytes.Buffer
	srv.executor.Stdout = &stdout

	plan := &ChunkGraphPlan{
		PlanID:      "plan_stdout_capture",
		IntentID:    "intent",
		Goal:        "goal",
		EntryChunks: []string{"CHK_ROOT"},
		Chunks: []PlanChunk{
			{
				ChunkID: "CHK_ROOT",
				Kind:    "task",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_ROOT\nintent_uid: intent\nchunk_port: port_root\n\nPwd\n"},
			},
		},
	}
	fillTestPlanDescriptions(plan)
	submit := srv.SubmitChunkGraph(plan)
	if !submit.Accepted {
		t.Fatalf("submit plan failed: %#v", submit.ValidationErrors)
	}

	start, err := srv.StartRun(context.Background(), StartRunParams{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		inspect, err := srv.InspectRun(RunIDParams{RunID: start.RunID})
		if err != nil {
			t.Fatalf("InspectRun: %v", err)
		}
		if inspect.Chunks["CHK_ROOT"].Status == ChunkPhaseSucceeded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not reach succeeded chunk state: status=%#v chunks=%#v", inspect.Run, inspect.Chunks)
		}
		time.Sleep(10 * time.Millisecond)
	}

	if got := stdout.String(); !strings.Contains(got, "/") {
		t.Fatalf("expected configured executor stdout to capture pwd output, got %q", got)
	}
}

func TestListRunsCacheInvalidatesOnStatusChange(t *testing.T) {
	srv := NewServer(vfs.Config{}, &stubBridgeVFS{}, nil)
	now := time.Now().UTC()
	record := &runRecord{
		status: RunStatus{RunID: "run_1", PlanID: "plan", Status: RunPhaseRunning, StartedAt: now, UpdatedAt: now},
		chunks: map[string]ChunkExecutionResult{},
	}
	srv.mu.Lock()
	srv.runs["run_1"] = record
	srv.invalidateRunsCacheLocked()
	srv.mu.Unlock()

	first := srv.ListRuns()
	if len(first.Runs) != 1 || first.Runs[0].Status != RunPhaseRunning {
		t.Fatalf("unexpected first list runs result: %#v", first)
	}

	if _, err := srv.PauseRun(RunIDParams{RunID: "run_1"}); err != nil {
		t.Fatalf("pause run: %v", err)
	}
	second := srv.ListRuns()
	if second.Runs[0].Status != RunPhasePaused {
		t.Fatalf("cache was not invalidated: %#v", second)
	}
}

func BenchmarkStreamRunEvents(b *testing.B) {
	srv := NewServer(vfs.Config{}, &stubBridgeVFS{}, nil)
	record := &runRecord{
		status: RunStatus{RunID: "run_1", PlanID: "plan", Status: RunPhaseRunning},
		chunks: map[string]ChunkExecutionResult{},
	}
	for i := 0; i < 2000; i++ {
		srv.appendEvent(record, "", EventRunStarted, map[string]any{"i": i})
	}
	srv.mu.Lock()
	srv.runs["run_1"] = record
	srv.invalidateRunsCacheLocked()
	srv.mu.Unlock()

	b.Run("incremental", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = srv.StreamRunEvents(RunIDParams{RunID: "run_1", AfterIndex: len(record.events) - 1})
		}
	})
	b.Run("full", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = srv.StreamRunEvents(RunIDParams{RunID: "run_1"})
		}
	})
}

func TestBridgePressurePolling(t *testing.T) {
	srv := NewServer(vfs.Config{}, &stubBridgeVFS{}, nil)
	record := &runRecord{
		status: RunStatus{RunID: "run_1", PlanID: "plan", Status: RunPhaseRunning},
		chunks: map[string]ChunkExecutionResult{},
	}
	srv.mu.Lock()
	srv.runs["run_1"] = record
	srv.invalidateRunsCacheLocked()
	srv.mu.Unlock()
	for i := 0; i < 1000; i++ {
		srv.appendEvent(record, "", EventRunStarted, map[string]any{"i": i})
		if _, err := srv.StreamRunEvents(RunIDParams{RunID: "run_1", AfterIndex: i}); err != nil {
			t.Fatalf("poll iteration %d: %v", i, err)
		}
		_ = srv.ListRuns()
	}
}

func TestDecisionLoopBackRequeuesAncestorChunk(t *testing.T) {
	srv := NewServer(vfs.Config{}, &stubBridgeVFS{}, nil)
	var choiceCount int
	srv.SetHumanControlChooser(func(ctx context.Context, req HumanControlRequest) (HumanControlChoice, error) {
		choiceCount++
		switch choiceCount {
		case 1:
			return HumanControlChoice{Branch: "retry"}, nil
		case 2:
			return HumanControlChoice{Branch: "yes"}, nil
		default:
			return HumanControlChoice{}, fmt.Errorf("unexpected extra decision request %d", choiceCount)
		}
	})

	plan := &ChunkGraphPlan{
		PlanID:      "plan_loop_back",
		IntentID:    "intent",
		Goal:        "goal",
		EntryChunks: []string{"CHK_ROOT"},
		Chunks: []PlanChunk{
			{
				ChunkID: "CHK_ROOT",
				Kind:    "task",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_ROOT\nintent_uid: intent\nchunk_port: port_root\n\nconcat [[[root]]] as @port_root__msg\n"},
			},
			{
				ChunkID: "CHK_DECISION",
				Kind:    "decision",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_DECISION\nintent_uid: intent\nchunk_port: port_decision\n\nPwd\n"},
				Decision: &DecisionSpec{
					AllowedBranches: []string{"retry", "yes"},
					DefaultBranch:   "yes",
					Interaction:     &InteractionSpec{Mode: "human", Human: &HumanInteraction{Title: "review"}},
				},
			},
			{
				ChunkID: "CHK_DONE",
				Kind:    "task",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_DONE\nintent_uid: intent\nchunk_port: port_done\n\nprint [[[done]]]\n"},
			},
		},
		Edges: []PlanEdge{
			{FromChunkID: "CHK_ROOT", ToChunkID: "CHK_DECISION", EdgeKind: "control"},
			{FromChunkID: "CHK_DECISION", ToChunkID: "CHK_ROOT", EdgeKind: "loop_back", Branch: "retry", LoopPolicy: &LoopPolicy{MaxIterations: 3}},
			{FromChunkID: "CHK_DECISION", ToChunkID: "CHK_DONE", EdgeKind: "branch", Branch: "yes"},
		},
	}
	fillTestPlanDescriptions(plan)
	submit := srv.SubmitChunkGraph(plan)
	if !submit.Accepted {
		t.Fatalf("submit plan failed: %#v", submit.ValidationErrors)
	}

	status, err := srv.StartRun(context.Background(), StartRunParams{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		inspect, err := srv.InspectRun(RunIDParams{RunID: status.RunID})
		if err != nil {
			t.Fatalf("InspectRun: %v", err)
		}
		rootStarts := 0
		decisionStarts := 0
		for _, evt := range inspect.Events {
			if evt.Type != EventChunkStarted || evt.ChunkID == nil {
				continue
			}
			switch *evt.ChunkID {
			case "CHK_ROOT":
				rootStarts++
			case "CHK_DECISION":
				decisionStarts++
			}
		}
		if rootStarts >= 2 && decisionStarts >= 2 &&
			inspect.Chunks["CHK_ROOT"].Status == ChunkPhaseSucceeded &&
			inspect.Chunks["CHK_DECISION"].Status == ChunkPhaseSucceeded &&
			inspect.Chunks["CHK_DONE"].Status == ChunkPhaseSucceeded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not complete: status=%#v rootStarts=%d decisionStarts=%d choiceCount=%d events=%#v", inspect.Run, rootStarts, decisionStarts, choiceCount, inspect.Events)
		}
		time.Sleep(10 * time.Millisecond)
	}

	inspect, err := srv.InspectRun(RunIDParams{RunID: status.RunID})
	if err != nil {
		t.Fatalf("InspectRun: %v", err)
	}
	rootStarts := 0
	decisionStarts := 0
	for _, evt := range inspect.Events {
		if evt.Type != EventChunkStarted || evt.ChunkID == nil {
			continue
		}
		switch *evt.ChunkID {
		case "CHK_ROOT":
			rootStarts++
		case "CHK_DECISION":
			decisionStarts++
		}
	}
	if rootStarts != 2 {
		t.Fatalf("expected root to rerun after loop_back, got %d starts; events=%#v", rootStarts, inspect.Events)
	}
	if decisionStarts != 2 {
		t.Fatalf("expected decision to rerun after root retry, got %d starts; events=%#v", decisionStarts, inspect.Events)
	}
	if choiceCount != 2 {
		t.Fatalf("expected two human decisions, got %d", choiceCount)
	}
}

func TestResetLoopBackCorridorClearsIntermediateState(t *testing.T) {
	plan := &ChunkGraphPlan{
		PlanID:      "plan_reset",
		EntryChunks: []string{"CHK_ROOT"},
		Chunks: []PlanChunk{
			{ChunkID: "CHK_ROOT", Kind: "task"},
			{ChunkID: "CHK_MID", Kind: "task"},
			{ChunkID: "CHK_DECISION", Kind: "decision"},
			{ChunkID: "CHK_SIDE", Kind: "task"},
		},
		Edges: []PlanEdge{
			{FromChunkID: "CHK_ROOT", ToChunkID: "CHK_MID", EdgeKind: "control"},
			{FromChunkID: "CHK_MID", ToChunkID: "CHK_DECISION", EdgeKind: "control"},
			{FromChunkID: "CHK_ROOT", ToChunkID: "CHK_SIDE", EdgeKind: "control"},
			{FromChunkID: "CHK_DECISION", ToChunkID: "CHK_ROOT", EdgeKind: "loop_back", Branch: "retry", LoopPolicy: &LoopPolicy{MaxIterations: 3}},
		},
	}
	now := time.Now().UTC()
	record := &runRecord{
		plan: plan,
		status: RunStatus{
			RunID: "run_1",
			ChunkStatuses: map[string]ChunkPhase{
				"CHK_ROOT":     ChunkPhaseSucceeded,
				"CHK_MID":      ChunkPhaseSucceeded,
				"CHK_DECISION": ChunkPhaseSucceeded,
				"CHK_SIDE":     ChunkPhaseSucceeded,
			},
			UpdatedAt: now,
		},
		chunks: map[string]ChunkExecutionResult{
			"CHK_ROOT":     {ChunkID: "CHK_ROOT", Kind: "task", Status: ChunkPhaseSucceeded, Values: map[string]string{"selected_branch": "old"}, Control: map[string]any{"stale": true}, Vars: map[string]satis.RuntimeBinding{"x": {Kind: "text", Text: "root"}}},
			"CHK_MID":      {ChunkID: "CHK_MID", Kind: "task", Status: ChunkPhaseSucceeded, Values: map[string]string{"mid": "old"}, Control: map[string]any{"stale": true}, Vars: map[string]satis.RuntimeBinding{"mid": {Kind: "text", Text: "mid"}}},
			"CHK_DECISION": {ChunkID: "CHK_DECISION", Kind: "decision", Status: ChunkPhaseSucceeded, Values: map[string]string{"selected_branch": "retry"}, Control: map[string]any{"selected_branch": "retry"}, Vars: map[string]satis.RuntimeBinding{"selected_branch": {Kind: "text", Text: "retry"}}},
			"CHK_SIDE":     {ChunkID: "CHK_SIDE", Kind: "task", Status: ChunkPhaseSucceeded, Values: map[string]string{"side": "keep"}, Vars: map[string]satis.RuntimeBinding{"side": {Kind: "text", Text: "keep"}}},
		},
		workflowRegistry: map[string]WorkflowBindingSnapshot{
			"root_var": {Name: "root_var", Source: "CHK_ROOT"},
			"mid_var":  {Name: "mid_var", Source: "CHK_MID"},
			"dec_var":  {Name: "dec_var", Source: "CHK_DECISION"},
			"side_var": {Name: "side_var", Source: "CHK_SIDE"},
		},
	}

	reset := resetLoopBackCorridor(record, "CHK_ROOT", "CHK_DECISION")
	for _, chunkID := range []string{"CHK_ROOT", "CHK_MID", "CHK_DECISION"} {
		if _, ok := reset[chunkID]; !ok {
			t.Fatalf("expected %s in reset corridor, got %#v", chunkID, reset)
		}
		if got := record.status.ChunkStatuses[chunkID]; got != ChunkPhasePending {
			t.Fatalf("expected %s status reset to pending, got %s", chunkID, got)
		}
		result := record.chunks[chunkID]
		if result.Values != nil || result.Vars != nil || result.Control != nil || result.Error != nil {
			t.Fatalf("expected %s result cleared, got %#v", chunkID, result)
		}
	}
	if got := record.status.ChunkStatuses["CHK_SIDE"]; got != ChunkPhaseSucceeded {
		t.Fatalf("expected unrelated side chunk to remain succeeded, got %s", got)
	}
	if _, ok := record.workflowRegistry["side_var"]; !ok {
		t.Fatalf("expected unrelated workflow binding to remain")
	}
	if _, ok := record.workflowRegistry["root_var"]; ok {
		t.Fatalf("expected root workflow binding removed after reset")
	}
	if _, ok := record.workflowRegistry["mid_var"]; ok {
		t.Fatalf("expected mid workflow binding removed after reset")
	}
	if _, ok := record.workflowRegistry["dec_var"]; ok {
		t.Fatalf("expected decision workflow binding removed after reset")
	}
}

func TestCompletingOneLeafAutoCompletesOtherPendingLeaves(t *testing.T) {
	srv := NewServer(vfs.Config{}, &stubBridgeVFS{}, nil)
	srv.SetHumanControlChooser(func(ctx context.Context, req HumanControlRequest) (HumanControlChoice, error) {
		return HumanControlChoice{Branch: "left"}, nil
	})

	plan := &ChunkGraphPlan{
		PlanID:      "plan_multi_leaf",
		IntentID:    "intent",
		Goal:        "goal",
		EntryChunks: []string{"CHK_ROOT"},
		Chunks: []PlanChunk{
			{
				ChunkID: "CHK_ROOT",
				Kind:    "task",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_ROOT\nintent_uid: intent\nchunk_port: port_root\n\nconcat [[[root]]] as @port_root__msg\n"},
			},
			{
				ChunkID: "CHK_DECISION",
				Kind:    "decision",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_DECISION\nintent_uid: intent\nchunk_port: port_decision\n\nPwd\n"},
				Decision: &DecisionSpec{
					AllowedBranches: []string{"left", "right"},
					DefaultBranch:   "left",
					Interaction:     &InteractionSpec{Mode: "human", Human: &HumanInteraction{Title: "review"}},
				},
			},
			{
				ChunkID: "CHK_LEFT",
				Kind:    "task",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_LEFT\nintent_uid: intent\nchunk_port: port_left\n\nprint [[[left]]]\n"},
			},
			{
				ChunkID: "CHK_RIGHT",
				Kind:    "task",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_RIGHT\nintent_uid: intent\nchunk_port: port_right\n\nprint [[[right]]]\n"},
			},
		},
		Edges: []PlanEdge{
			{FromChunkID: "CHK_ROOT", ToChunkID: "CHK_DECISION", EdgeKind: "control"},
			{FromChunkID: "CHK_DECISION", ToChunkID: "CHK_LEFT", EdgeKind: "branch", Branch: "left"},
			{FromChunkID: "CHK_DECISION", ToChunkID: "CHK_RIGHT", EdgeKind: "branch", Branch: "right"},
		},
	}
	fillTestPlanDescriptions(plan)
	submit := srv.SubmitChunkGraph(plan)
	if !submit.Accepted {
		t.Fatalf("submit plan failed: %#v", submit.ValidationErrors)
	}

	status, err := srv.StartRun(context.Background(), StartRunParams{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		inspect, err := srv.InspectRun(RunIDParams{RunID: status.RunID})
		if err != nil {
			t.Fatalf("InspectRun: %v", err)
		}
		if inspect.Run.Status == RunPhasePlanningPending &&
			inspect.Chunks["CHK_LEFT"].Status == ChunkPhaseSucceeded &&
			inspect.Chunks["CHK_RIGHT"].Status == ChunkPhaseSucceeded {
			if auto := inspect.Chunks["CHK_RIGHT"].Control["auto_completed"]; auto != true {
				t.Fatalf("expected right leaf to be auto completed, got %#v", inspect.Chunks["CHK_RIGHT"])
			}
			foundAutoSuccess := false
			for _, evt := range inspect.Events {
				if evt.Type == EventChunkSucceeded && evt.ChunkID != nil && *evt.ChunkID == "CHK_RIGHT" {
					if flag, _ := evt.Payload["auto_completed"].(bool); flag {
						foundAutoSuccess = true
					}
				}
			}
			if !foundAutoSuccess {
				t.Fatalf("expected auto-complete success event for right leaf, got %#v", inspect.Events)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not reach planning_pending with auto-completed sibling leaf")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAutoCompleteTerminalLeavesClearsSiblingLeafBindings(t *testing.T) {
	srv := NewServer(vfs.Config{}, &stubBridgeVFS{}, nil)
	record := &runRecord{
		plan: &ChunkGraphPlan{
			Chunks: []PlanChunk{
				{ChunkID: "CHK_LEFT", Kind: "task"},
				{ChunkID: "CHK_RIGHT", Kind: "task"},
			},
		},
		status: RunStatus{
			RunID: "run_1",
			ChunkStatuses: map[string]ChunkPhase{
				"CHK_LEFT":  ChunkPhaseSucceeded,
				"CHK_RIGHT": ChunkPhaseReady,
			},
		},
		chunks: map[string]ChunkExecutionResult{
			"CHK_LEFT": {
				ChunkID: "CHK_LEFT",
				Status:  ChunkPhaseSucceeded,
				Vars: map[string]satis.RuntimeBinding{
					"left_var": {Kind: "text", Text: "chosen"},
				},
			},
			"CHK_RIGHT": {
				ChunkID: "CHK_RIGHT",
				Status:  ChunkPhaseReady,
				Values:  map[string]string{"right_value": "stale"},
				Vars: map[string]satis.RuntimeBinding{
					"right_var": {Kind: "text", Text: "stale"},
				},
				Control: map[string]any{"old": true},
			},
		},
		workflowRegistry: map[string]WorkflowBindingSnapshot{
			"left_var":  {Name: "left_var", Source: "CHK_LEFT", Version: 1},
			"right_var": {Name: "right_var", Source: "CHK_RIGHT", Version: 2},
		},
	}

	srv.autoCompleteTerminalLeaves(record, "CHK_LEFT", map[string]struct{}{
		"CHK_LEFT":  {},
		"CHK_RIGHT": {},
	})

	right := record.chunks["CHK_RIGHT"]
	if right.Status != ChunkPhaseSucceeded {
		t.Fatalf("expected right leaf to be auto-completed as succeeded, got %#v", right)
	}
	if right.Values != nil || right.Vars != nil {
		t.Fatalf("expected right leaf bindings cleared, got %#v", right)
	}
	if right.Control["auto_completed"] != true || right.Control["completed_by_leaf"] != "CHK_LEFT" {
		t.Fatalf("expected right leaf control to record auto-completion, got %#v", right.Control)
	}
	if _, ok := record.workflowRegistry["right_var"]; ok {
		t.Fatalf("expected auto-completed sibling leaf binding removed from workflow registry")
	}
	if _, ok := record.workflowRegistry["left_var"]; !ok {
		t.Fatalf("expected completed leaf binding to remain in workflow registry")
	}
}

func TestActiveWorkIsLimitedToTerminalLeavesRequiresNoPendingDecision(t *testing.T) {
	record := &runRecord{
		status: RunStatus{
			ChunkStatuses: map[string]ChunkPhase{
				"CHK_LEAF":     ChunkPhaseSucceeded,
				"CHK_OTHER":    ChunkPhasePending,
				"CHK_DECISION": ChunkPhaseReady,
			},
		},
	}
	if activeWorkIsLimitedToTerminalLeaves(record, map[string]struct{}{
		"CHK_LEAF":  {},
		"CHK_OTHER": {},
	}) {
		t.Fatalf("expected pending decision work to prevent terminal leaf auto-complete")
	}
	if !activeWorkIsLimitedToTerminalLeaves(record, map[string]struct{}{
		"CHK_LEAF":     {},
		"CHK_OTHER":    {},
		"CHK_DECISION": {},
	}) {
		t.Fatalf("expected helper to allow auto-complete when all active work is terminal")
	}
}

func TestCollectTerminalLeafChunkIDsExcludesDecisionLoopNodes(t *testing.T) {
	plan := &ChunkGraphPlan{
		Chunks: []PlanChunk{
			{ChunkID: "CHK_ROOT", Kind: "task"},
			{ChunkID: "CHK_DECISION", Kind: "decision"},
		},
		Edges: []PlanEdge{
			{FromChunkID: "CHK_ROOT", ToChunkID: "CHK_DECISION", EdgeKind: "control"},
			{FromChunkID: "CHK_DECISION", ToChunkID: "CHK_ROOT", EdgeKind: "loop_back", Branch: "retry", LoopPolicy: &LoopPolicy{MaxIterations: 3}},
		},
	}
	leaves := collectTerminalLeafChunkIDs(plan)
	if _, ok := leaves["CHK_DECISION"]; ok {
		t.Fatalf("decision loop node must not be treated as terminal leaf: %#v", leaves)
	}
}

func TestDecisionLoopBackExceedsMaxIterationsFailsCurrentPlanning(t *testing.T) {
	srv := NewServer(vfs.Config{}, &stubBridgeVFS{}, nil)
	var choiceCount int
	srv.SetHumanControlChooser(func(ctx context.Context, req HumanControlRequest) (HumanControlChoice, error) {
		choiceCount++
		return HumanControlChoice{Branch: "retry"}, nil
	})
	plan := &ChunkGraphPlan{
		PlanID:      "plan_loop_limit_fail",
		IntentID:    "intent",
		Goal:        "goal",
		EntryChunks: []string{"CHK_ROOT"},
		Chunks: []PlanChunk{
			{
				ChunkID: "CHK_ROOT",
				Kind:    "task",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_ROOT\nintent_uid: intent\nchunk_port: port_root\n\nconcat [[[root]]] as @port_root__msg\n"},
			},
			{
				ChunkID: "CHK_DECISION",
				Kind:    "decision",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_DECISION\nintent_uid: intent\nchunk_port: port_decision\n\nPwd\n"},
				Decision: &DecisionSpec{
					AllowedBranches: []string{"retry", "yes"},
					DefaultBranch:   "yes",
					Interaction:     &InteractionSpec{Mode: "human", Human: &HumanInteraction{Title: "review"}},
				},
			},
			{
				ChunkID: "CHK_DONE",
				Kind:    "task",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_DONE\nintent_uid: intent\nchunk_port: port_done\n\nprint [[[done]]]\n"},
			},
		},
		Edges: []PlanEdge{
			{FromChunkID: "CHK_ROOT", ToChunkID: "CHK_DECISION", EdgeKind: "control"},
			{FromChunkID: "CHK_DECISION", ToChunkID: "CHK_ROOT", EdgeKind: "loop_back", Branch: "retry", LoopPolicy: &LoopPolicy{MaxIterations: 2}},
			{FromChunkID: "CHK_DECISION", ToChunkID: "CHK_DONE", EdgeKind: "branch", Branch: "yes"},
		},
	}
	fillTestPlanDescriptions(plan)
	submit := srv.SubmitChunkGraph(plan)
	if !submit.Accepted {
		t.Fatalf("submit plan failed: %#v", submit.ValidationErrors)
	}
	start, err := srv.StartRun(context.Background(), StartRunParams{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		inspect, err := srv.InspectRun(RunIDParams{RunID: start.RunID})
		if err != nil {
			t.Fatalf("InspectRun: %v", err)
		}
		if inspect.Run.Status == RunPhaseFailed {
			if inspect.Run.Error == nil || inspect.Run.Error.Code != CodeDecisionLoopLimitExceeded {
				t.Fatalf("expected decision loop limit run error, got %#v", inspect.Run.Error)
			}
			if inspect.Chunks["CHK_DECISION"].Status != ChunkPhaseFailed {
				t.Fatalf("expected decision chunk failed on loop limit, got %#v", inspect.Chunks["CHK_DECISION"])
			}
			if inspect.Chunks["CHK_DONE"].Status != ChunkPhaseFailed {
				t.Fatalf("expected child chunk failed with the rest of current planning, got %#v", inspect.Chunks["CHK_DONE"])
			}
			if choiceCount != 3 {
				t.Fatalf("expected three retry decisions before fail, got %d", choiceCount)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not fail on loop limit")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDecisionLoopBackExceedsLimitRollsBackIncrementalPlanning(t *testing.T) {
	srv := NewServer(vfs.Config{}, &stubBridgeVFS{}, nil)
	var choiceCount int
	srv.SetHumanControlChooser(func(ctx context.Context, req HumanControlRequest) (HumanControlChoice, error) {
		choiceCount++
		return HumanControlChoice{Branch: "retry"}, nil
	})
	basePlan := &ChunkGraphPlan{
		PlanID:      "plan_incremental_rollback",
		IntentID:    "intent",
		Goal:        "goal",
		EntryChunks: []string{"CHK_ROOT"},
		Chunks: []PlanChunk{
			{
				ChunkID: "CHK_ROOT",
				Kind:    "task",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_ROOT\nintent_uid: intent\nchunk_port: port_root\n\nconcat [[[root]]] as @port_root__msg\n"},
			},
			{
				ChunkID: "CHK_LEAF",
				Kind:    "task",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_LEAF\nintent_uid: intent\nchunk_port: port_leaf\n\nprint [[[leaf]]]\n"},
			},
		},
		Edges: []PlanEdge{
			{FromChunkID: "CHK_ROOT", ToChunkID: "CHK_LEAF", EdgeKind: "control"},
		},
	}
	fillTestPlanDescriptions(basePlan)
	submit := srv.SubmitChunkGraph(basePlan)
	if !submit.Accepted {
		t.Fatalf("submit plan failed: %#v", submit.ValidationErrors)
	}
	start, err := srv.StartRun(context.Background(), StartRunParams{PlanID: basePlan.PlanID})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		inspect, err := srv.InspectRun(RunIDParams{RunID: start.RunID})
		if err != nil {
			t.Fatalf("InspectRun: %v", err)
		}
		if inspect.Run.Status == RunPhasePlanningPending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("base run did not reach planning_pending")
		}
		time.Sleep(10 * time.Millisecond)
	}
	fragment := PlanFragment{
		Chunks: []PlanChunk{
			{
				ChunkID: "CHK_DECISION_INC",
				Kind:    "decision",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_DECISION_INC\nintent_uid: intent\nchunk_port: port_decision\n\nPwd\n"},
				Decision: &DecisionSpec{
					AllowedBranches: []string{"retry", "yes"},
					DefaultBranch:   "yes",
					Interaction:     &InteractionSpec{Mode: "human", Human: &HumanInteraction{Title: "retry?"}},
				},
			},
			{
				ChunkID: "CHK_DONE_INC",
				Kind:    "task",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_DONE_INC\nintent_uid: intent\nchunk_port: port_done\n\nprint [[[done]]]\n"},
			},
		},
		Edges: []PlanEdge{
			{FromChunkID: "CHK_ROOT", ToChunkID: "CHK_DECISION_INC", EdgeKind: "control"},
			{FromChunkID: "CHK_DECISION_INC", ToChunkID: "CHK_ROOT", EdgeKind: "loop_back", Branch: "retry", LoopPolicy: &LoopPolicy{MaxIterations: 2}},
			{FromChunkID: "CHK_DECISION_INC", ToChunkID: "CHK_DONE_INC", EdgeKind: "branch", Branch: "yes"},
		},
		EntryChunks: []string{"CHK_DECISION_INC"},
	}
	if _, err := srv.ContinueRunWithFragment(context.Background(), ContinueRunFragmentParams{
		RunID:    start.RunID,
		Fragment: fragment,
	}); err != nil {
		t.Fatalf("ContinueRunWithFragment: %v", err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		inspect, err := srv.InspectRun(RunIDParams{RunID: start.RunID})
		if err != nil {
			t.Fatalf("InspectRun: %v", err)
		}
		if inspect.Run.Status == RunPhasePlanningPending {
			if inspect.Run.Error != nil {
				t.Fatalf("expected rollback to keep run alive, got error %#v", inspect.Run.Error)
			}
			if _, ok := inspect.Chunks["CHK_DECISION_INC"]; ok {
				t.Fatalf("expected continuation decision chunk rolled back, got chunks=%#v", inspect.Chunks)
			}
			if _, ok := inspect.Chunks["CHK_DONE_INC"]; ok {
				t.Fatalf("expected continuation child chunk rolled back, got chunks=%#v", inspect.Chunks)
			}
			if inspect.Summary.ContinuationCount != 0 {
				t.Fatalf("expected continuation count restored after rollback, got %#v", inspect.Summary)
			}
			foundRollbackEvent := false
			for _, evt := range inspect.Events {
				if evt.Type == EventRunPlanningPending {
					if rollback, _ := evt.Payload["rollback"].(bool); rollback {
						foundRollbackEvent = true
						break
					}
				}
			}
			if !foundRollbackEvent {
				t.Fatalf("expected rollback planning pending event, events=%#v", inspect.Events)
			}
			if choiceCount != 3 {
				t.Fatalf("expected three retry decisions before rollback, got %d", choiceCount)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("incremental run did not roll back to planning_pending")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDecisionLoopBackExceedsMaxIterationsFailsAllChunksAndRestoresFiles(t *testing.T) {
	ctx := context.Background()
	srv, mountDir := newDiskBackedTestServer(t)
	var choiceCount int
	srv.SetHumanControlChooser(func(ctx context.Context, req HumanControlRequest) (HumanControlChoice, error) {
		choiceCount++
		return HumanControlChoice{Branch: "retry"}, nil
	})

	plan := &ChunkGraphPlan{
		PlanID:      "plan_loop_limit_file_restore",
		IntentID:    "intent",
		Goal:        "goal",
		EntryChunks: []string{"CHK_ROOT"},
		Chunks: []PlanChunk{
			{
				ChunkID: "CHK_ROOT",
				Kind:    "task",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_ROOT\nintent_uid: intent\nchunk_port: port_root\n\nWrite [[[from-run]]] into /rollback.txt\n"},
			},
			{
				ChunkID: "CHK_DECISION",
				Kind:    "decision",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_DECISION\nintent_uid: intent\nchunk_port: port_decision\n\nPwd\n"},
				Decision: &DecisionSpec{
					AllowedBranches: []string{"retry", "yes"},
					DefaultBranch:   "yes",
					Interaction:     &InteractionSpec{Mode: "human", Human: &HumanInteraction{Title: "retry?"}},
				},
			},
			{
				ChunkID: "CHK_DONE",
				Kind:    "task",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_DONE\nintent_uid: intent\nchunk_port: port_done\n\nCreate file /done.txt with [[[done]]]\n"},
			},
		},
		Edges: []PlanEdge{
			{FromChunkID: "CHK_ROOT", ToChunkID: "CHK_DECISION", EdgeKind: "control"},
			{FromChunkID: "CHK_DECISION", ToChunkID: "CHK_ROOT", EdgeKind: "loop_back", Branch: "retry", LoopPolicy: &LoopPolicy{MaxIterations: 2}},
			{FromChunkID: "CHK_DECISION", ToChunkID: "CHK_DONE", EdgeKind: "branch", Branch: "yes"},
		},
	}
	fillTestPlanDescriptions(plan)
	submit := srv.SubmitChunkGraph(plan)
	if !submit.Accepted {
		t.Fatalf("submit plan failed: %#v", submit.ValidationErrors)
	}
	start, err := srv.StartRun(ctx, StartRunParams{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	inspect := waitForRunStatus(t, srv, start.RunID, func(inspect InspectRunResult) bool {
		return inspect.Run.Status == RunPhaseFailed
	})
	if inspect.Run.Error == nil || inspect.Run.Error.Code != CodeDecisionLoopLimitExceeded {
		t.Fatalf("expected decision loop limit error, got %#v", inspect.Run.Error)
	}
	if choiceCount != 3 {
		t.Fatalf("expected three retry decisions before run fail, got %d", choiceCount)
	}
	for chunkID, result := range inspect.Chunks {
		if result.Status != ChunkPhaseFailed {
			t.Fatalf("expected chunk %s failed after loop limit, got %#v", chunkID, result)
		}
	}
	if _, err := os.Stat(filepath.Join(mountDir, "rollback.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected rollback.txt removed by VFS rollback, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(mountDir, "done.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected done.txt absent after failure rollback, err=%v", err)
	}
}

func TestDecisionLoopBackExceedsLimitRollsBackIncrementalPlanningFiles(t *testing.T) {
	ctx := context.Background()
	srv, mountDir := newDiskBackedTestServer(t)
	srv.SetHumanControlChooser(func(ctx context.Context, req HumanControlRequest) (HumanControlChoice, error) {
		return HumanControlChoice{Branch: "retry"}, nil
	})

	basePlan := &ChunkGraphPlan{
		PlanID:      "plan_incremental_file_restore",
		IntentID:    "intent",
		Goal:        "goal",
		EntryChunks: []string{"CHK_ROOT"},
		Chunks: []PlanChunk{
			{
				ChunkID: "CHK_ROOT",
				Kind:    "task",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_ROOT\nintent_uid: intent\nchunk_port: port_root\n\nWrite [[[stable]]] into /shared.txt\n"},
			},
			{
				ChunkID: "CHK_LEAF",
				Kind:    "task",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_LEAF\nintent_uid: intent\nchunk_port: port_leaf\n\nprint [[[leaf]]]\n"},
			},
		},
		Edges: []PlanEdge{
			{FromChunkID: "CHK_ROOT", ToChunkID: "CHK_LEAF", EdgeKind: "control"},
		},
	}
	fillTestPlanDescriptions(basePlan)
	submit := srv.SubmitChunkGraph(basePlan)
	if !submit.Accepted {
		t.Fatalf("submit base plan failed: %#v", submit.ValidationErrors)
	}
	start, err := srv.StartRun(ctx, StartRunParams{PlanID: basePlan.PlanID})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForRunStatus(t, srv, start.RunID, func(inspect InspectRunResult) bool {
		return inspect.Run.Status == RunPhasePlanningPending
	})
	if got := mustReadFile(t, filepath.Join(mountDir, "shared.txt")); got != "stable" {
		t.Fatalf("expected stable base file, got %q", got)
	}

	fragment := PlanFragment{
		Chunks: []PlanChunk{
			{
				ChunkID: "CHK_MUTATE_INC",
				Kind:    "task",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_MUTATE_INC\nintent_uid: intent\nchunk_port: port_mutate\n\nWrite [[[mutated]]] into /shared.txt\nWrite [[[temp]]] into /temp.txt\n"},
			},
			{
				ChunkID: "CHK_DECISION_INC",
				Kind:    "decision",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_DECISION_INC\nintent_uid: intent\nchunk_port: port_decision\n\nPwd\n"},
				Decision: &DecisionSpec{
					AllowedBranches: []string{"retry", "yes"},
					DefaultBranch:   "yes",
					Interaction:     &InteractionSpec{Mode: "human", Human: &HumanInteraction{Title: "retry?"}},
				},
			},
			{
				ChunkID: "CHK_DONE_INC",
				Kind:    "task",
				Source:  ChunkSource{SatisText: "chunk_id: CHK_DONE_INC\nintent_uid: intent\nchunk_port: port_done\n\nCreate file /done_inc.txt with [[[done]]]\n"},
			},
		},
		Edges: []PlanEdge{
			{FromChunkID: "CHK_ROOT", ToChunkID: "CHK_MUTATE_INC", EdgeKind: "control"},
			{FromChunkID: "CHK_MUTATE_INC", ToChunkID: "CHK_DECISION_INC", EdgeKind: "control"},
			{FromChunkID: "CHK_DECISION_INC", ToChunkID: "CHK_ROOT", EdgeKind: "loop_back", Branch: "retry", LoopPolicy: &LoopPolicy{MaxIterations: 2}},
			{FromChunkID: "CHK_DECISION_INC", ToChunkID: "CHK_DONE_INC", EdgeKind: "branch", Branch: "yes"},
		},
		EntryChunks: []string{"CHK_MUTATE_INC"},
	}
	if _, err := srv.ContinueRunWithFragment(ctx, ContinueRunFragmentParams{RunID: start.RunID, Fragment: fragment}); err != nil {
		t.Fatalf("ContinueRunWithFragment: %v", err)
	}
	inspect := waitForRunStatus(t, srv, start.RunID, func(inspect InspectRunResult) bool {
		return inspect.Run.Status == RunPhasePlanningPending && inspect.Summary.ContinuationCount == 0
	})
	if inspect.Run.Error != nil {
		t.Fatalf("expected rollback to preserve run without error, got %#v", inspect.Run.Error)
	}
	if got := mustReadFile(t, filepath.Join(mountDir, "shared.txt")); got != "stable" {
		t.Fatalf("expected shared.txt restored to stable, got %q", got)
	}
	if _, err := os.Stat(filepath.Join(mountDir, "temp.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected temp.txt removed after incremental rollback, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(mountDir, "done_inc.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected done_inc.txt absent after incremental rollback, err=%v", err)
	}
}

func newDiskBackedTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	mountDir := filepath.Join(root, "mount")
	stateDir := filepath.Join(root, "state")
	cfg := vfs.Config{Backend: "disk", MountDir: mountDir, StateDir: stateDir}
	svc, err := sandbox.NewDiskService(cfg)
	if err != nil {
		t.Fatalf("NewDiskService: %v", err)
	}
	return NewServer(cfg, svc, nil), mountDir
}

func waitForRunStatus(t *testing.T, srv *Server, runID string, done func(InspectRunResult) bool) InspectRunResult {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		inspect, err := srv.InspectRun(RunIDParams{RunID: runID})
		if err != nil {
			t.Fatalf("InspectRun: %v", err)
		}
		if done(inspect) {
			return inspect
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s did not reach expected state; inspect=%#v", runID, inspect)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file %s: %v", path, err)
	}
	return string(data)
}

type stubBridgeVFS struct{}

func (s *stubBridgeVFS) BeginChunkTxn(context.Context, vfs.ChunkID) (vfs.Txn, error) {
	return vfs.Txn{}, nil
}
func (s *stubBridgeVFS) CommitChunkTxn(context.Context, vfs.Txn) error   { return nil }
func (s *stubBridgeVFS) RollbackChunkTxn(context.Context, vfs.Txn) error { return nil }
func (s *stubBridgeVFS) Create(context.Context, vfs.Txn, vfs.CreateInput) (vfs.FileRef, error) {
	return vfs.FileRef{}, nil
}
func (s *stubBridgeVFS) Resolve(context.Context, vfs.ResolveInput) (vfs.FileRef, error) {
	return vfs.FileRef{}, nil
}
func (s *stubBridgeVFS) Read(context.Context, vfs.Txn, vfs.ReadInput) (vfs.ReadResult, error) {
	return vfs.ReadResult{}, nil
}
func (s *stubBridgeVFS) ListDir(context.Context, vfs.Txn, string) ([]vfs.DirEntry, error) {
	return nil, nil
}
func (s *stubBridgeVFS) Write(context.Context, vfs.Txn, vfs.WriteInput) (vfs.FileRef, error) {
	return vfs.FileRef{}, nil
}
func (s *stubBridgeVFS) Delete(context.Context, vfs.Txn, vfs.DeleteInput) (vfs.FileRef, error) {
	return vfs.FileRef{}, nil
}
func (s *stubBridgeVFS) Rename(context.Context, vfs.Txn, vfs.RenameInput) (vfs.FileRef, error) {
	return vfs.FileRef{}, nil
}
func (s *stubBridgeVFS) Glob(context.Context, string) ([]string, error) { return nil, nil }
