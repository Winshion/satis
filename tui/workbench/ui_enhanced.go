package workbench

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/rivo/tview"
	"satis/bridge"
)

type paletteAction struct {
	Title    string
	Keywords string
	Run      func() error
}

func (a *App) refreshStatusBar() {
	if a == nil || a.statusBar == nil || a.model == nil || a.model.Plan == nil {
		return
	}
	validateLabel := "[green]valid[-]"
	if len(a.validationIssues) > 0 {
		validateLabel = fmt.Sprintf("[red]issues=%d[-]", len(a.validationIssues))
	}
	runID := a.lastRunID
	if strings.TrimSpace(runID) == "" {
		runID = "-"
	}
	dirty := "clean"
	if a.model.Dirty {
		dirty = "[yellow]dirty[-]"
	}
	a.statusBar.SetText(fmt.Sprintf(
		" intent: %s \n plan=%s | selected=%s | state=%s | %s | last_run=%s ",
		tview.Escape(orDash(strings.TrimSpace(a.model.Plan.IntentDescription))),
		tview.Escape(a.model.ResolvedPath),
		tview.Escape(orDash(chunkDisplayLabel(a.model.Plan, a.currentChunkID))),
		dirty,
		validateLabel,
		tview.Escape(runID),
	))
}

func (a *App) refreshPlanChainView() {
	if a == nil || a.model == nil {
		return
	}
	links, err := readPlanChainLinks(a.ctx, a.backend, a.model.ResolvedPath)
	if err != nil {
		a.planChain = planChainLinks{}
		if a.prevPlanView != nil {
			a.prevPlanView.SetText(fmt.Sprintf("[red]registry error[-]: %v", err))
		}
		if a.nextPlanView != nil {
			a.nextPlanView.SetText(fmt.Sprintf("[red]registry error[-]: %v", err))
		}
		return
	}
	a.planChain = links
	if a.prevPlanView != nil {
		a.prevPlanView.SetText(a.renderPlanNavButton("prev-plan", links.Prev, a.graphNavTarget == "prev"))
	}
	if a.nextPlanView != nil {
		a.nextPlanView.SetText(a.renderPlanNavButton("next-plan", links.Next, a.graphNavTarget == "next"))
	}
	a.applyFocusColors()
}

func (a *App) renderPlanNavButton(label string, targetPath string, selected bool) string {
	color := "white"
	if pathOrEmpty(targetPath) != "" {
		color = "yellow"
	}
	if selected {
		color = "deepskyblue"
	}
	return fmt.Sprintf("[%s]%s[-]", color, tview.Escape(label))
}

func (a *App) refreshValidationState() {
	if a == nil || a.model == nil || a.model.Plan == nil {
		return
	}
	plan := a.model.Plan
	if snapshot, err := a.model.snapshotPlan(); err == nil {
		if edges, dependsOn, entryChunks, derr := buildDerivedTopology(snapshot); derr == nil {
			applyDerivedTopology(snapshot, edges, dependsOn, entryChunks)
			plan = snapshot
		} else {
			a.validationIssues = []bridge.ValidationIssue{{
				Code:    bridge.CodeValidationInputBinding,
				Message: derr.Error(),
				Field:   "edges",
			}}
			a.validationByChunk = map[string][]bridge.ValidationIssue{}
			chunkID := chunkIDFromTopologyError(a.model.Plan, derr)
			if chunkID != "" {
				a.validationByChunk[chunkID] = append(a.validationByChunk[chunkID], a.validationIssues[0])
			}
			a.refreshGraphView()
			a.refreshDiagnostics()
			a.refreshStatusBar()
			return
		}
	}
	result := bridge.ValidateChunkGraphPlan(plan)
	if result.Accepted {
		a.validationIssues = nil
		a.validationByChunk = map[string][]bridge.ValidationIssue{}
		a.refreshGraphView()
		a.refreshDiagnostics()
		a.refreshStatusBar()
		return
	}
	a.validationIssues = append([]bridge.ValidationIssue(nil), result.ValidationErrors...)
	byChunk := make(map[string][]bridge.ValidationIssue)
	for _, issue := range result.ValidationErrors {
		chunkID := chunkIDForIssue(a.model.Plan, issue)
		if chunkID == "" {
			continue
		}
		byChunk[chunkID] = append(byChunk[chunkID], issue)
	}
	a.validationByChunk = byChunk
	a.refreshGraphView()
	a.refreshDiagnostics()
	a.refreshStatusBar()
}

func chunkIDFromTopologyError(plan *bridge.ChunkGraphPlan, err error) string {
	if plan == nil || err == nil {
		return ""
	}
	msg := err.Error()
	for _, chunk := range plan.Chunks {
		if strings.Contains(msg, fmt.Sprintf("%q", chunk.ChunkID)) {
			return chunk.ChunkID
		}
	}
	return ""
}

func (a *App) refreshDiagnostics() {
	if a == nil || a.diagnosticsView == nil {
		return
	}
	if len(a.validationIssues) == 0 {
		a.diagnosticsView.SetText("[green]No validation issues.[-]\nUse [yellow]Ctrl+P[-] for the command palette.")
		return
	}
	var b strings.Builder
	currentIssues := a.validationByChunk[a.currentChunkID]
	if len(currentIssues) > 0 {
		fmt.Fprintf(&b, "[red]Current chunk issues (%s)[-]\n", tview.Escape(a.currentChunkID))
		for _, issue := range currentIssues {
			fmt.Fprintf(&b, "- %s\n", tview.Escape(issue.Message))
		}
	} else {
		fmt.Fprintf(&b, "[red]Plan issues (%d)[-]\n", len(a.validationIssues))
		max := len(a.validationIssues)
		if max > 5 {
			max = 5
		}
		for _, issue := range a.validationIssues[:max] {
			label := issue.Message
			if chunkID := chunkIDForIssue(a.model.Plan, issue); chunkID != "" {
				label = chunkID + ": " + label
			}
			fmt.Fprintf(&b, "- %s\n", tview.Escape(label))
		}
	}
	a.diagnosticsView.SetText(strings.TrimRight(b.String(), "\n"))
}

func (a *App) refreshPlanningPanel() {
	if a == nil || a.planningView == nil {
		return
	}
	if strings.TrimSpace(a.lastRunID) == "" {
		a.recentAddedChunkIDs = map[string]struct{}{}
		a.planningView.SetText("No active run.\nUse `run` / `start` to begin, then continue planning here.")
		return
	}
	inspect, err := a.backend.InspectPlanRun(a.ctx, a.lastRunID)
	if err != nil {
		a.planningView.SetText(fmt.Sprintf("[red]inspect failed:[-] %v", err))
		return
	}
	if strings.TrimSpace(inspect.Run.PlanID) != "" {
		a.lastSubmittedPlanID = inspect.Run.PlanID
	}
	a.recentAddedChunkIDs = map[string]struct{}{}
	if n := len(inspect.PlanningHistory); n > 0 {
		for _, chunkID := range inspect.PlanningHistory[n-1].NewNodeIDs {
			a.recentAddedChunkIDs[chunkID] = struct{}{}
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "run=%s\nstatus=%s\nrevision=%d continuations=%d\n",
		tview.Escape(inspect.Run.RunID),
		tview.Escape(string(inspect.Run.Status)),
		inspect.Summary.GraphRevision,
		inspect.Summary.ContinuationCount,
	)
	if inspect.Summary.PlanningPending {
		fmt.Fprintf(&b, "[yellow]planning_pending[-]\n")
		fmt.Fprintf(&b, "actions: plan-continue | plan-change | plan-detach | plan-prev | plan-next | plan-draft | plan-finish\n")
	}
	if a.planChain.Prev != "" || a.planChain.Next != "" {
		fmt.Fprintf(&b, "prev_plan: %s\n", tview.Escape(planNavLabel(a.planChain.Prev)))
		fmt.Fprintf(&b, "next_plan: %s\n", tview.Escape(planNavLabel(a.planChain.Next)))
	}
	if inspect.Summary.LatestPlanningSummary != "" {
		fmt.Fprintf(&b, "latest: %s\n", tview.Escape(inspect.Summary.LatestPlanningSummary))
	}
	if len(inspect.Summary.InheritedVariableSummary) > 0 {
		limit := len(inspect.Summary.InheritedVariableSummary)
		if limit > 6 {
			limit = 6
		}
		fmt.Fprintf(&b, "vars: %s", tview.Escape(strings.Join(inspect.Summary.InheritedVariableSummary[:limit], ", ")))
		if limit < len(inspect.Summary.InheritedVariableSummary) {
			fmt.Fprintf(&b, " +%d", len(inspect.Summary.InheritedVariableSummary)-limit)
		}
		fmt.Fprintln(&b)
	}
	if len(inspect.PlanningHistory) > 0 {
		fmt.Fprintf(&b, "history=%d\n", len(inspect.PlanningHistory))
		last := inspect.PlanningHistory[len(inspect.PlanningHistory)-1]
		if len(last.NewNodeIDs) > 0 {
			fmt.Fprintf(&b, "new_nodes: %s\n", tview.Escape(strings.Join(last.NewNodeIDs, ", ")))
		}
		if last.ImportedBackEdges > 0 {
			fmt.Fprintf(&b, "back_edges: %d\n", last.ImportedBackEdges)
		}
	}
	a.planningView.SetText(strings.TrimRight(b.String(), "\n"))
	a.refreshGraphView()
}

func chunkIDForIssue(plan *bridge.ChunkGraphPlan, issue bridge.ValidationIssue) string {
	if plan == nil {
		return ""
	}
	if issue.Details != nil {
		if chunkID, _ := issue.Details["chunk_id"].(string); strings.TrimSpace(chunkID) != "" {
			return strings.TrimSpace(chunkID)
		}
	}
	field := strings.TrimSpace(issue.Field)
	if strings.HasPrefix(field, "chunks[") {
		rest := strings.TrimPrefix(field, "chunks[")
		idxEnd := strings.Index(rest, "]")
		if idxEnd > 0 {
			if idx, err := strconv.Atoi(rest[:idxEnd]); err == nil && idx >= 0 && idx < len(plan.Chunks) {
				return plan.Chunks[idx].ChunkID
			}
		}
	}
	return ""
}

func (a *App) openPalette() {
	if a == nil || a.pages == nil || a.paletteInput == nil {
		return
	}
	a.paletteVisible = true
	a.refreshPalette("")
	a.paletteInput.SetText("")
	a.pages.ShowPage("palette")
	a.application.SetFocus(a.paletteInput)
}

func (a *App) closePalette() {
	if a == nil || a.pages == nil {
		return
	}
	a.paletteVisible = false
	a.pages.HidePage("palette")
	a.applyFocusColors()
}

func (a *App) refreshPalette(query string) {
	if a == nil || a.paletteList == nil {
		return
	}
	a.paletteList.Clear()
	query = strings.ToLower(strings.TrimSpace(query))
	actions := a.paletteActions()
	for _, action := range actions {
		searchText := strings.ToLower(action.Title + " " + action.Keywords)
		if query != "" && !strings.Contains(searchText, query) {
			continue
		}
		runFn := action.Run
		a.paletteList.AddItem(action.Title, "", 0, func() {
			a.closePalette()
			if runFn != nil {
				if err := runFn(); err != nil {
					a.appendError("%v", err)
				}
			}
		})
	}
	if a.paletteList.GetItemCount() == 0 {
		a.paletteList.AddItem("No matching actions", "", 0, nil)
	}
}

func (a *App) paletteActions() []paletteAction {
	currentChunkID := strings.TrimSpace(a.currentChunkID)
	actions := []paletteAction{
		{Title: "Render DAG", Keywords: "render validate topology", Run: func() error { a.runCommand("render"); return nil }},
		{Title: "Validate Plan", Keywords: "validate check lint", Run: func() error { a.runCommand("validate"); return nil }},
		{Title: "Save Plan", Keywords: "save write persist", Run: func() error { a.runCommand("save"); return nil }},
		{Title: "Run Plan", Keywords: "run execute submit", Run: func() error { a.runCommand("run"); return nil }},
		{Title: "Run Selected Chunk + Dependencies", Keywords: "focus runselected run chunk deps", Run: func() error { a.runCommand("runselected"); return nil }},
		{Title: "Sync Handoff From Body", Keywords: "sync handoff parse body io", Run: func() error { a.runCommand("synchandoff"); return nil }},
		{Title: "Sync Body Prelude From Handoff", Keywords: "sync body prelude input io", Run: func() error { a.runCommand("syncbody"); return nil }},
		{Title: "Duplicate Current Chunk", Keywords: "duplicate clone chunk copy", Run: func() error { a.runCommand("duplicate"); return nil }},
		{Title: "Focus DAG", Keywords: "focus left graph", Run: func() error { a.setFocus(a.graphView); return nil }},
		{Title: "Focus Chunk Editor", Keywords: "focus chunk editor", Run: func() error { a.setFocus(a.chunkEditor); return nil }},
		{Title: "Focus Decision Config", Keywords: "focus decision config", Run: func() error { a.setFocus(a.decisionEditor); return nil }},
		{Title: "Focus CLI", Keywords: "focus cli", Run: func() error { a.setFocus(a.commandInput); return nil }},
	}
	if currentChunkID != "" {
		if chunk := a.model.ChunkByID(currentChunkID); chunk != nil && strings.EqualFold(strings.TrimSpace(chunk.Kind), "decision") {
			actions = append(actions,
				paletteAction{
					Title:    "Decision Mode: human",
					Keywords: "decision mode human",
					Run:      func() error { a.runCommand("decisionmode human"); return nil },
				},
				paletteAction{
					Title:    "Decision Mode: llm",
					Keywords: "decision mode llm",
					Run:      func() error { a.runCommand("decisionmode llm"); return nil },
				},
				paletteAction{
					Title:    "Decision Mode: llm_then_human",
					Keywords: "decision mode llm fallback",
					Run:      func() error { a.runCommand("decisionmode llm_then_human"); return nil },
				},
			)
		}
		actions = append(actions, paletteAction{
			Title:    fmt.Sprintf("Inspect Current Chunk (%s)", currentChunkID),
			Keywords: "inspect chunk runtime",
			Run: func() error {
				a.runCommand("chunk")
				return nil
			},
		})
	}
	chunkIDs := make([]string, 0, len(a.model.Plan.Chunks))
	for _, chunk := range a.model.Plan.Chunks {
		chunkIDs = append(chunkIDs, chunk.ChunkID)
	}
	sort.Strings(chunkIDs)
	for _, chunkID := range chunkIDs {
		target := chunkID
		actions = append(actions, paletteAction{
			Title:    "Jump to " + target,
			Keywords: "jump select chunk " + target,
			Run: func() error {
				a.loadChunk(target)
				a.setFocus(a.graphView)
				return nil
			},
		})
	}
	return actions
}

func orDash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}
