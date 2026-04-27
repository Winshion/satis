package workbench

import (
	"context"
	"fmt"
	"path"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"satis/bridge"
	"satis/vfs"
)

type testWorkbenchBackend struct{}

func (testWorkbenchBackend) ResolveVirtualPath(path string) string { return path }
func (testWorkbenchBackend) ListVirtualDir(ctx context.Context, path string) ([]vfs.DirEntry, error) {
	return nil, nil
}
func (testWorkbenchBackend) ReadVirtualText(ctx context.Context, path string) (string, error) {
	return "", fmt.Errorf("missing file: %s", path)
}
func (testWorkbenchBackend) WriteVirtualText(ctx context.Context, path string, text string) error {
	return nil
}
func (testWorkbenchBackend) SubmitPlan(ctx context.Context, plan *bridge.ChunkGraphPlan) (bridge.SubmitChunkGraphResult, error) {
	return bridge.ValidateChunkGraphPlan(plan), nil
}
func (testWorkbenchBackend) StartPlanRun(ctx context.Context, planID string, options bridge.ExecutionOptions) (bridge.RunStatus, error) {
	return bridge.RunStatus{}, nil
}
func (testWorkbenchBackend) InspectPlanRun(ctx context.Context, runID string) (bridge.InspectRunResult, error) {
	return bridge.InspectRunResult{}, nil
}
func (testWorkbenchBackend) InspectRunChunk(ctx context.Context, runID string, chunkID string) (bridge.ChunkExecutionResult, error) {
	return bridge.ChunkExecutionResult{}, nil
}
func (testWorkbenchBackend) StreamPlanRunEvents(ctx context.Context, runID string) (bridge.StreamRunEventsResult, error) {
	return bridge.StreamRunEventsResult{}, nil
}
func (testWorkbenchBackend) ContinuePlanRun(ctx context.Context, runID string, fragment PlanFragment) (bridge.RunStatus, error) {
	return bridge.RunStatus{}, nil
}
func (testWorkbenchBackend) ContinuePlanRunLLM(ctx context.Context, runID string, prompt string) (bridge.RunStatus, error) {
	return bridge.RunStatus{}, nil
}
func (testWorkbenchBackend) FinishPlanRun(ctx context.Context, runID string) (bridge.RunStatus, error) {
	return bridge.RunStatus{}, nil
}

type memoryWorkbenchBackend struct {
	files map[string]string
}

func (b *memoryWorkbenchBackend) ResolveVirtualPath(path string) string { return path }
func (b *memoryWorkbenchBackend) ListVirtualDir(ctx context.Context, dir string) ([]vfs.DirEntry, error) {
	entries := make([]vfs.DirEntry, 0)
	seen := map[string]struct{}{}
	for virtualPath := range b.files {
		if path.Dir(virtualPath) != dir {
			continue
		}
		name := path.Base(virtualPath)
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		entries = append(entries, vfs.DirEntry{Name: name, VirtualPath: virtualPath, Kind: vfs.FileKindText})
	}
	return entries, nil
}
func (b *memoryWorkbenchBackend) ReadVirtualText(ctx context.Context, path string) (string, error) {
	if b.files == nil {
		return "", fmt.Errorf("missing file: %s", path)
	}
	text, ok := b.files[path]
	if !ok {
		return "", fmt.Errorf("missing file: %s", path)
	}
	return text, nil
}
func (b *memoryWorkbenchBackend) WriteVirtualText(ctx context.Context, path string, text string) error {
	if b.files == nil {
		b.files = map[string]string{}
	}
	b.files[path] = text
	return nil
}
func (b *memoryWorkbenchBackend) SubmitPlan(ctx context.Context, plan *bridge.ChunkGraphPlan) (bridge.SubmitChunkGraphResult, error) {
	return bridge.ValidateChunkGraphPlan(plan), nil
}
func (b *memoryWorkbenchBackend) StartPlanRun(ctx context.Context, planID string, options bridge.ExecutionOptions) (bridge.RunStatus, error) {
	return bridge.RunStatus{}, nil
}
func (b *memoryWorkbenchBackend) InspectPlanRun(ctx context.Context, runID string) (bridge.InspectRunResult, error) {
	return bridge.InspectRunResult{}, nil
}
func (b *memoryWorkbenchBackend) InspectRunChunk(ctx context.Context, runID string, chunkID string) (bridge.ChunkExecutionResult, error) {
	return bridge.ChunkExecutionResult{}, nil
}
func (b *memoryWorkbenchBackend) StreamPlanRunEvents(ctx context.Context, runID string) (bridge.StreamRunEventsResult, error) {
	return bridge.StreamRunEventsResult{}, nil
}
func (b *memoryWorkbenchBackend) ContinuePlanRun(ctx context.Context, runID string, fragment PlanFragment) (bridge.RunStatus, error) {
	return bridge.RunStatus{}, nil
}
func (b *memoryWorkbenchBackend) ContinuePlanRunLLM(ctx context.Context, runID string, prompt string) (bridge.RunStatus, error) {
	return bridge.RunStatus{}, nil
}
func (b *memoryWorkbenchBackend) FinishPlanRun(ctx context.Context, runID string) (bridge.RunStatus, error) {
	return bridge.RunStatus{}, nil
}

func TestDecisionEditorTextOmitsLoopComment(t *testing.T) {
	model := testWorkbenchModel()
	if err := model.SetChunkKind("CHK_ROOT", "decision"); err != nil {
		t.Fatalf("SetChunkKind: %v", err)
	}
	text := formatDecisionEditorText(model.Plan, model.ChunkByID("CHK_ROOT"))
	if !strings.Contains(text, "loop_max_iterations: 3") {
		t.Fatalf("expected default loop_max_iterations line, got:\n%s", text)
	}
}

func TestDecisionEditorTaskPlaceholderIsASCII(t *testing.T) {
	text := formatDecisionEditorText(testWorkbenchModel().Plan, testWorkbenchModel().ChunkByID("CHK_ROOT"))
	for _, r := range text {
		if r > 127 {
			t.Fatalf("expected ASCII-only task placeholder, got %q", text)
		}
	}
}

func TestWorkbenchFocusPanelsAreGraphChunkDecisionCLI(t *testing.T) {
	app := &App{
		ctx:         context.Background(),
		backend:     testWorkbenchBackend{},
		model:       testWorkbenchModel(),
		application: tview.NewApplication(),
		uiReady:     make(chan struct{}),
	}

	app.initUI()

	if got := len(app.focusables); got != 4 {
		t.Fatalf("expected 4 focusable panels, got %d", got)
	}
	if app.focusables[0] != app.graphView {
		t.Fatalf("focusables[0] should be graph view")
	}
	if app.focusables[1] != app.chunkEditor {
		t.Fatalf("focusables[1] should be chunk editor")
	}
	if app.focusables[2] != app.decisionEditor {
		t.Fatalf("focusables[2] should be decision editor")
	}
	if app.focusables[3] != app.commandInput {
		t.Fatalf("focusables[3] should be command input")
	}
}

func TestWorkbenchShowsChunkMetaFormInCenterPanel(t *testing.T) {
	app := &App{
		ctx:         context.Background(),
		backend:     testWorkbenchBackend{},
		model:       testWorkbenchModel(),
		application: tview.NewApplication(),
		uiReady:     make(chan struct{}),
	}

	app.initUI()

	if app.chunkMetaForm == nil {
		t.Fatalf("expected chunk meta form to be initialized")
	}
	if app.chunkDescField == nil {
		t.Fatalf("expected chunk description input field to be initialized")
	}
}

func TestStatusBarShowsIntentOnTopLine(t *testing.T) {
	app := &App{
		ctx:         context.Background(),
		backend:     testWorkbenchBackend{},
		model:       testWorkbenchModel(),
		application: tview.NewApplication(),
		uiReady:     make(chan struct{}),
	}
	app.initUI()
	app.refreshStatusBar()
	text := app.statusBar.GetText(false)
	if !strings.Contains(text, "intent: 完成工作台测试意图") {
		t.Fatalf("expected intent on top status line, got %q", text)
	}
}

func TestPromptPlanDescriptionEditorMarksPromptVisible(t *testing.T) {
	app := &App{
		ctx:         context.Background(),
		backend:     testWorkbenchBackend{},
		model:       testWorkbenchModel(),
		application: tview.NewApplication(),
		uiReady:     make(chan struct{}),
	}
	app.model.Plan.PlanDescription = ""
	app.initUI()
	app.promptPlanDescriptionEditor("Edit Plan Description", true)
	if !app.promptingPlanMeta {
		t.Fatalf("expected plan metadata prompt to be active")
	}
}

func TestPlanMetadataPromptConsumesTabInsteadOfCyclingPanels(t *testing.T) {
	app := &App{
		ctx:         context.Background(),
		backend:     testWorkbenchBackend{},
		model:       testWorkbenchModel(),
		application: tview.NewApplication(),
		uiReady:     make(chan struct{}),
	}
	app.initUI()
	app.promptPlanDescriptionEditor("Edit Plan Description", false)
	before := app.focusIndex
	event := tcell.NewEventKey(tcell.KeyTAB, 0, tcell.ModNone)
	if got := app.handleGlobalKey(event); got == nil {
		t.Fatalf("expected modal Tab to stay inside the modal widgets")
	}
	if app.focusIndex != before {
		t.Fatalf("expected panel focus index unchanged, got %d want %d", app.focusIndex, before)
	}
}

func TestChunkMetaStripOnlyShowsPortAndDescription(t *testing.T) {
	app := &App{
		ctx:         context.Background(),
		backend:     testWorkbenchBackend{},
		model:       testWorkbenchModel(),
		application: tview.NewApplication(),
		uiReady:     make(chan struct{}),
	}
	app.initUI()
	if app.chunkMetaForm == nil {
		t.Fatalf("expected chunk meta strip")
	}
	if app.chunkMetaForm.GetItemCount() != 2 {
		t.Fatalf("expected only chunk_port and description fields, got %d items", app.chunkMetaForm.GetItemCount())
	}
}

func TestWorkbenchCLIPlanDescriptionUpdatesPlan(t *testing.T) {
	app := &App{
		ctx:         context.Background(),
		backend:     testWorkbenchBackend{},
		model:       testWorkbenchModel(),
		application: tview.NewApplication(),
		uiReady:     make(chan struct{}),
	}
	app.initUI()
	app.runCommand("plan-description 当前计划说明")
	if got := app.model.Plan.PlanDescription; got != "当前计划说明" {
		t.Fatalf("unexpected plan description %q", got)
	}
}

func TestWorkbenchCLIShowsPlanAndIntentDescriptions(t *testing.T) {
	app := &App{
		ctx:         context.Background(),
		backend:     testWorkbenchBackend{},
		model:       testWorkbenchModel(),
		application: tview.NewApplication(),
		uiReady:     make(chan struct{}),
	}
	app.initUI()
	app.runCommand("plan-description")
	app.runCommand("intent-description")
	logText := app.logView.GetText(false)
	if !strings.Contains(logText, "plan_description: 验证工作台拓扑编辑行为") {
		t.Fatalf("expected plan description in log, got %q", logText)
	}
	if !strings.Contains(logText, "intent_description: 完成工作台测试意图") {
		t.Fatalf("expected intent description in log, got %q", logText)
	}
}

func TestWorkbenchEditorsDisableWrapAndResetViewportOnLoad(t *testing.T) {
	app := &App{
		ctx:         context.Background(),
		backend:     testWorkbenchBackend{},
		model:       testWorkbenchModel(),
		application: tview.NewApplication(),
		uiReady:     make(chan struct{}),
	}
	app.initUI()

	if app.chunkEditor == nil || app.decisionEditor == nil {
		t.Fatalf("expected editors initialized")
	}
	app.chunkEditor.SetText("stale\ncontent", true)
	app.chunkEditor.SetOffset(3, 4)
	app.decisionEditor.SetText("stale decision", true)
	app.decisionEditor.SetOffset(2, 5)

	app.loadChunk("CHK_ROOT")

	if row, col := app.chunkEditor.GetOffset(); row != 0 || col != 0 {
		t.Fatalf("expected chunk editor offset reset, got row=%d col=%d", row, col)
	}
	if row, col := app.decisionEditor.GetOffset(); row != 0 || col != 0 {
		t.Fatalf("expected decision editor offset reset, got row=%d col=%d", row, col)
	}
	if app.chunkEditor.GetText() == "stale\ncontent" {
		t.Fatalf("expected chunk editor text refreshed")
	}
	if app.decisionEditor.GetText() == "stale decision" {
		t.Fatalf("expected decision editor text refreshed")
	}
	if !app.decisionEditor.GetDisabled() {
		t.Fatalf("expected decision editor disabled for task chunk")
	}
}

func TestGraphFocusUsesActivePanelStateWhenWidgetFocusDrifts(t *testing.T) {
	app := &App{
		ctx:         context.Background(),
		backend:     testWorkbenchBackend{},
		model:       testWorkbenchModel(),
		application: tview.NewApplication(),
		uiReady:     make(chan struct{}),
	}
	app.initUI()

	app.focusIndex = 0
	app.application.SetFocus(app.chunkEditor)
	if !app.isGraphFocus() {
		t.Fatalf("expected graph panel to remain active when focusIndex points to graph panel")
	}

	app.focusIndex = 1
	app.application.SetFocus(app.graphView)
	if !app.isGraphFocus() {
		t.Fatalf("expected direct graph widget focus to still count as graph focus")
	}
}

func TestCtrlNStillAddsChunkWhenGraphPanelActiveAfterFocusDrift(t *testing.T) {
	app := &App{
		ctx:         context.Background(),
		backend:     testWorkbenchBackend{},
		model:       testWorkbenchModel(),
		application: tview.NewApplication(),
		uiReady:     make(chan struct{}),
	}
	app.initUI()

	before := len(app.model.Plan.Chunks)
	app.focusIndex = 0
	app.application.SetFocus(app.chunkEditor)
	event := tcell.NewEventKey(tcell.KeyCtrlN, 0, tcell.ModCtrl)
	handled := app.handleGlobalKey(event)
	if handled != nil {
		t.Fatalf("expected Ctrl+N to be handled by graph panel shortcut")
	}
	if got := len(app.model.Plan.Chunks); got != before+1 {
		t.Fatalf("expected chunk count %d, got %d", before+1, got)
	}
}

func TestCtrlKStillRemovesLeafChunkWhenGraphPanelActiveAfterFocusDrift(t *testing.T) {
	app := &App{
		ctx:         context.Background(),
		backend:     testWorkbenchBackend{},
		model:       testWorkbenchModel(),
		application: tview.NewApplication(),
		uiReady:     make(chan struct{}),
	}
	app.initUI()

	childID, _, err := app.model.AddChildChunk("CHK_ROOT")
	if err != nil {
		t.Fatalf("AddChildChunk: %v", err)
	}
	if err := app.model.ValidateAndNormalize(); err != nil {
		t.Fatalf("ValidateAndNormalize: %v", err)
	}
	app.model.SelectedChunkID = childID
	app.currentChunkID = childID
	app.refreshGraphView()

	before := len(app.model.Plan.Chunks)
	app.focusIndex = 0
	app.application.SetFocus(app.chunkEditor)
	event := tcell.NewEventKey(tcell.KeyCtrlK, 0, tcell.ModCtrl)
	handled := app.handleGlobalKey(event)
	if handled != nil {
		t.Fatalf("expected Ctrl+K to be handled by graph panel shortcut")
	}
	if got := len(app.model.Plan.Chunks); got != before-1 {
		t.Fatalf("expected chunk count %d after delete, got %d", before-1, got)
	}
	if app.model.ChunkByID(childID) != nil {
		t.Fatalf("expected leaf chunk %q to be removed", childID)
	}
}

func TestGraphLayersRespectDecisionHierarchyOverReachableHandoff(t *testing.T) {
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
	chk2, _, err := model.AddChildChunk(decisionID)
	if err != nil {
		t.Fatalf("AddChildChunk(chk2): %v", err)
	}
	chk3, _, err := model.AddChildChunk(decisionID)
	if err != nil {
		t.Fatalf("AddChildChunk(chk3): %v", err)
	}
	if err := model.AddDecisionBranch(decisionID, "yes", chk2); err != nil {
		t.Fatalf("AddDecisionBranch yes: %v", err)
	}
	if err := model.AddDecisionBranch(decisionID, "no", chk3); err != nil {
		t.Fatalf("AddDecisionBranch no: %v", err)
	}
	if err := model.UpsertHandoffRow(chk2, "", HandoffRow{Port: "port_root", FromStep: "CHK_ROOT", VarName: "intro"}); err != nil {
		t.Fatalf("UpsertHandoffRow chk2: %v", err)
	}
	if err := model.UpsertHandoffRow(chk3, "", HandoffRow{Port: "port_root", FromStep: "CHK_ROOT", VarName: "intro"}); err != nil {
		t.Fatalf("UpsertHandoffRow chk3: %v", err)
	}
	if err := model.SyncEdgesFromHandoffs(); err != nil {
		t.Fatalf("SyncEdgesFromHandoffs: %v", err)
	}
	parents, children := buildGraphAdjacency(model.Plan)
	_, layerOf := computeGraphLayers(model.Plan, parents, children)
	if layerOf["CHK_ROOT"] != 0 {
		t.Fatalf("root layer mismatch: %d", layerOf["CHK_ROOT"])
	}
	if layerOf[decisionID] != 1 {
		t.Fatalf("decision layer mismatch: %d", layerOf[decisionID])
	}
	if layerOf[chk2] != 2 || layerOf[chk3] != 2 {
		t.Fatalf("leaf layers mismatch: chk2=%d chk3=%d", layerOf[chk2], layerOf[chk3])
	}
}

func TestColorizeGraphDecorationsPreservesSelectedPriority(t *testing.T) {
	text := "+---+\n| A |\n+---+"
	boxes := map[string]graphNodeBox{
		"CHK_DECISION": {X: 0, Y: 0, Width: 5, Height: 3},
	}
	got := colorizeGraphDecorations(text, boxes, []string{"CHK_DECISION"}, boxes["CHK_DECISION"], true, []graphPoint{{X: 0, Y: 0}}, map[string]struct{}{})
	if !strings.Contains(got, "[deepskyblue]") {
		t.Fatalf("expected selected highlight in output: %q", got)
	}
	if strings.Contains(got, "[yellow][deepskyblue]") || strings.Contains(got, "[deepskyblue][yellow]") {
		t.Fatalf("expected merged style rendering without nested conflicting markup: %q", got)
	}
}

func TestGraphLayersIgnoreDecisionLoopBackEdges(t *testing.T) {
	model := testWorkbenchModel()
	decisionID, _, err := model.AddChildChunk("CHK_ROOT")
	if err != nil {
		t.Fatalf("AddChildChunk(decision): %v", err)
	}
	if err := model.SetChunkKind(decisionID, "decision"); err != nil {
		t.Fatalf("SetChunkKind(decision): %v", err)
	}
	chk2, _, err := model.AddChildChunk(decisionID)
	if err != nil {
		t.Fatalf("AddChildChunk(chk2): %v", err)
	}
	if err := model.AddDecisionBranch(decisionID, "yes", chk2); err != nil {
		t.Fatalf("AddDecisionBranch yes: %v", err)
	}
	if err := model.AddDecisionBranch(decisionID, "retry", "CHK_ROOT"); err != nil {
		t.Fatalf("AddDecisionBranch retry: %v", err)
	}
	if err := model.SyncEdgesFromHandoffs(); err != nil {
		t.Fatalf("SyncEdgesFromHandoffs: %v", err)
	}

	parents, children := buildGraphAdjacency(model.Plan)
	_, layerOf := computeGraphLayers(model.Plan, parents, children)
	if layerOf["CHK_ROOT"] != 0 {
		t.Fatalf("root layer mismatch: %d", layerOf["CHK_ROOT"])
	}
	if layerOf[decisionID] != 1 {
		t.Fatalf("decision layer mismatch: %d", layerOf[decisionID])
	}
	if layerOf[chk2] != 2 {
		t.Fatalf("child layer mismatch: %d", layerOf[chk2])
	}
}

func TestSyncPlanTopologySaveAllowsLocalAliasAndDecisionLoopBack(t *testing.T) {
	app := &App{
		ctx:         context.Background(),
		backend:     testWorkbenchBackend{},
		model:       testWorkbenchModel(),
		application: tview.NewApplication(),
		uiReady:     make(chan struct{}),
	}
	app.initUI()

	root := app.model.ChunkByID("CHK_ROOT")
	root.Source.SatisText = "chunk_id: CHK_ROOT\nintent_uid: intent_workbench\nchunk_port: port_root\n\ninvoke [[[who is qianxuesen?]]] as @port_root__result\nprint @port_root__result\n"

	decisionID, _, err := app.model.AddChildChunk("CHK_ROOT")
	if err != nil {
		t.Fatalf("AddChildChunk(decision): %v", err)
	}
	if err := app.model.SetChunkKind(decisionID, "decision"); err != nil {
		t.Fatalf("SetChunkKind(decision): %v", err)
	}
	chk2, _, err := app.model.AddChildChunk(decisionID)
	if err != nil {
		t.Fatalf("AddChildChunk(chk2): %v", err)
	}
	if err := app.model.UpsertHandoffRow(chk2, "", HandoffRow{Port: "port_root", FromStep: "CHK_ROOT", VarName: "result"}); err != nil {
		t.Fatalf("UpsertHandoffRow(chk2): %v", err)
	}
	if err := app.model.AddDecisionBranch(decisionID, "yes", chk2); err != nil {
		t.Fatalf("AddDecisionBranch yes: %v", err)
	}
	if err := app.model.AddDecisionBranch(decisionID, "retry", "CHK_ROOT"); err != nil {
		t.Fatalf("AddDecisionBranch retry: %v", err)
	}

	if err := app.syncPlanTopology("save"); err != nil {
		t.Fatalf("syncPlanTopology(save): %v", err)
	}
	if len(app.model.Plan.EntryChunks) != 1 || app.model.Plan.EntryChunks[0] != "CHK_ROOT" {
		t.Fatalf("unexpected entry chunks after save sync: %#v", app.model.Plan.EntryChunks)
	}
}

func BenchmarkRenderASCIIChunkGraphDecisionHierarchy(b *testing.B) {
	model := testWorkbenchModel()
	decisionID, _, _ := model.AddChildChunk("CHK_ROOT")
	_ = model.SetChunkKind(decisionID, "decision")
	chk2, _, _ := model.AddChildChunk(decisionID)
	chk3, _, _ := model.AddChildChunk(decisionID)
	_ = model.AddDecisionBranch(decisionID, "yes", chk2)
	_ = model.AddDecisionBranch(decisionID, "no", chk3)
	_ = model.UpsertHandoffRow(chk2, "", HandoffRow{Port: "port_root", FromStep: "CHK_ROOT", VarName: "intro"})
	_ = model.UpsertHandoffRow(chk3, "", HandoffRow{Port: "port_root", FromStep: "CHK_ROOT", VarName: "intro"})
	_ = model.SyncEdgesFromHandoffs()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = renderASCIIChunkGraph(model.Plan, chk2, nil)
	}
}

func TestPlanChainViewShowsPrevAndNextAboveAndBelowGraph(t *testing.T) {
	backend := &memoryWorkbenchBackend{files: map[string]string{}}
	currentPath := "/demo/current/plan.json"
	prevPath := "/demo/prev/plan.json"
	nextPath := "/demo/next/plan.json"
	currentText, err := ScaffoldPlanJSON("/demo/current", "当前计划意图")
	if err != nil {
		t.Fatalf("ScaffoldPlanJSON(current): %v", err)
	}
	backend.files[currentPath] = currentText
	if err := linkPlanChain(context.Background(), backend, prevPath, currentPath); err != nil {
		t.Fatalf("link prev->current: %v", err)
	}
	if err := linkPlanChain(context.Background(), backend, currentPath, nextPath); err != nil {
		t.Fatalf("link current->next: %v", err)
	}

	app := &App{
		ctx:         context.Background(),
		backend:     backend,
		model:       testWorkbenchModel(),
		application: tview.NewApplication(),
		uiReady:     make(chan struct{}),
	}
	app.model.ResolvedPath = currentPath
	app.initUI()
	app.refreshPlanChainView()

	if app.prevPlanView == nil || app.nextPlanView == nil {
		t.Fatalf("expected prev/next plan views initialized")
	}
	prevText := app.prevPlanView.GetText(false)
	nextText := app.nextPlanView.GetText(false)
	if !strings.Contains(prevText, "prev-plan") {
		t.Fatalf("expected prev plan above graph, got:\n%s", prevText)
	}
	if !strings.Contains(nextText, "next-plan") {
		t.Fatalf("expected next plan below graph, got:\n%s", nextText)
	}
	rendered := renderASCIIChunkGraph(app.model.Plan, app.model.SelectedChunkID, nil).Text
	if strings.Contains(rendered, "Next Plan") || strings.Contains(rendered, "Return Plan") {
		t.Fatalf("expected graph to contain only current plan DAG, got:\n%s", rendered)
	}
}

func TestPlanNavButtonsUseWhiteWhenMissingAndYellowWhenPresent(t *testing.T) {
	app := &App{
		ctx:         context.Background(),
		backend:     &memoryWorkbenchBackend{files: map[string]string{}},
		model:       testWorkbenchModel(),
		application: tview.NewApplication(),
		uiReady:     make(chan struct{}),
	}
	app.model.ResolvedPath = "/demo/current/plan.json"
	app.initUI()

	app.planChain = planChainLinks{}
	if got := app.renderPlanNavButton("prev-plan", "", false); !strings.Contains(got, "[white]prev-plan[-]") {
		t.Fatalf("expected missing button white, got %q", got)
	}
	if got := app.renderPlanNavButton("next-plan", "/demo/next/plan.json", false); !strings.Contains(got, "[yellow]next-plan[-]") {
		t.Fatalf("expected linked button yellow, got %q", got)
	}
	if got := app.renderPlanNavButton("next-plan", "/demo/next/plan.json", true); !strings.Contains(got, "[deepskyblue]next-plan[-]") {
		t.Fatalf("expected selected button deep blue, got %q", got)
	}
}

func TestArrowDownFromLeafSelectsNextPlanButton(t *testing.T) {
	backend := &memoryWorkbenchBackend{files: map[string]string{}}
	currentPath := "/demo/current/plan.json"
	currentModel := testWorkbenchModel()
	currentModel.ResolvedPath = currentPath
	currentText, err := currentModel.Marshal()
	if err != nil {
		t.Fatalf("Marshal(current): %v", err)
	}
	backend.files[currentPath] = currentText
	if err := linkPlanChain(context.Background(), backend, currentPath, "/demo/next/plan.json"); err != nil {
		t.Fatalf("linkPlanChain: %v", err)
	}
	app := &App{
		ctx:         context.Background(),
		backend:     backend,
		model:       testWorkbenchModel(),
		application: tview.NewApplication(),
		uiReady:     make(chan struct{}),
	}
	app.model.ResolvedPath = currentPath
	app.initUI()
	app.refreshPlanChainView()

	app.moveGraphSelection("down")

	if app.graphNavTarget != "next" {
		t.Fatalf("expected next button selected, got %q", app.graphNavTarget)
	}
	if got := app.nextPlanView.GetText(false); !strings.Contains(got, "next-plan") {
		t.Fatalf("expected next button label, got %q", got)
	}
	rendered := app.graphView.GetText(false)
	if strings.Contains(rendered, "deepskyblue") {
		t.Fatalf("expected graph selection highlight cleared when next button selected, got:\n%s", rendered)
	}
}

func TestArrowUpFromRootSelectsPrevPlanButton(t *testing.T) {
	backend := &memoryWorkbenchBackend{files: map[string]string{}}
	currentPath := "/demo/current/plan.json"
	currentText, err := ScaffoldPlanJSON("/demo/current", "当前计划意图")
	if err != nil {
		t.Fatalf("ScaffoldPlanJSON(current): %v", err)
	}
	backend.files[currentPath] = currentText
	if err := linkPlanChain(context.Background(), backend, "/demo/prev/plan.json", currentPath); err != nil {
		t.Fatalf("linkPlanChain: %v", err)
	}
	app := &App{
		ctx:         context.Background(),
		backend:     backend,
		model:       testWorkbenchModel(),
		application: tview.NewApplication(),
		uiReady:     make(chan struct{}),
	}
	app.model.ResolvedPath = currentPath
	app.initUI()
	app.refreshPlanChainView()

	app.moveGraphSelection("up")

	if app.graphNavTarget != "prev" {
		t.Fatalf("expected prev button selected, got %q", app.graphNavTarget)
	}
	if got := app.prevPlanView.GetText(false); !strings.Contains(got, "prev-plan") {
		t.Fatalf("expected prev button label, got %q", got)
	}
	rendered := app.graphView.GetText(false)
	if strings.Contains(rendered, "deepskyblue") {
		t.Fatalf("expected graph selection highlight cleared when prev button selected, got:\n%s", rendered)
	}
}

func TestArrowBackFromPlanButtonRestoresImmediateChunkHighlight(t *testing.T) {
	backend := &memoryWorkbenchBackend{files: map[string]string{}}
	currentPath := "/demo/current/plan.json"
	currentText, err := ScaffoldPlanJSON("/demo/current", "当前计划意图")
	if err != nil {
		t.Fatalf("ScaffoldPlanJSON(current): %v", err)
	}
	backend.files[currentPath] = currentText
	if err := linkPlanChain(context.Background(), backend, "/demo/prev/plan.json", currentPath); err != nil {
		t.Fatalf("linkPlanChain: %v", err)
	}
	app := &App{
		ctx:         context.Background(),
		backend:     backend,
		model:       testWorkbenchModel(),
		application: tview.NewApplication(),
		uiReady:     make(chan struct{}),
	}
	app.model.ResolvedPath = currentPath
	app.initUI()
	app.refreshPlanChainView()

	app.moveGraphSelection("up")
	if app.graphNavTarget != "prev" {
		t.Fatalf("expected prev button selected, got %q", app.graphNavTarget)
	}

	app.moveGraphSelection("down")
	if app.graphNavTarget != "" {
		t.Fatalf("expected graph focus restored, got %q", app.graphNavTarget)
	}
	rendered := app.graphView.GetText(false)
	if !strings.Contains(rendered, "deepskyblue") {
		t.Fatalf("expected root chunk highlight restored immediately, got:\n%s", rendered)
	}
}

func TestOpenNextPlanFailsWhenRegistryTargetMissing(t *testing.T) {
	backend := &memoryWorkbenchBackend{files: map[string]string{}}
	currentPath := "/demo/current/plan.json"
	currentText, err := ScaffoldPlanJSON("/demo/current", "当前计划意图")
	if err != nil {
		t.Fatalf("ScaffoldPlanJSON(current): %v", err)
	}
	backend.files[currentPath] = currentText
	if err := linkPlanChain(context.Background(), backend, currentPath, "/demo/missing/plan.json"); err != nil {
		t.Fatalf("linkPlanChain: %v", err)
	}

	app := &App{
		ctx:         context.Background(),
		backend:     backend,
		model:       testWorkbenchModel(),
		application: tview.NewApplication(),
		uiReady:     make(chan struct{}),
	}
	app.model.ResolvedPath = currentPath
	app.initUI()
	app.refreshPlanChainView()

	err = app.openNextPlan()
	if err == nil {
		t.Fatalf("expected openNextPlan to fail for missing target")
	}
	if !strings.Contains(err.Error(), "missing file") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPlanContinueCreatesNextPlanWithoutPlanDescription(t *testing.T) {
	backend := &memoryWorkbenchBackend{files: map[string]string{}}
	currentPath := "/demo/current/plan.json"
	currentText, err := ScaffoldPlanJSON("/demo/current", "当前计划意图")
	if err != nil {
		t.Fatalf("ScaffoldPlanJSON(current): %v", err)
	}
	backend.files[currentPath] = currentText

	app := &App{
		ctx:         context.Background(),
		backend:     backend,
		model:       testWorkbenchModel(),
		application: tview.NewApplication(),
		uiReady:     make(chan struct{}),
	}
	app.model.ResolvedPath = currentPath
	app.initUI()

	if err := app.planContinueToJSON("next_plan.json"); err != nil {
		t.Fatalf("planContinueToJSON: %v", err)
	}
	nextText, ok := backend.files["/demo/current/next_plan.json"]
	if !ok {
		t.Fatalf("expected next plan file to be written")
	}
	nextPlan, err := ParsePlanDocument(nextText)
	if err != nil {
		t.Fatalf("ParsePlanDocument(next): %v", err)
	}
	if strings.TrimSpace(nextPlan.PlanDescription) != "" {
		t.Fatalf("expected empty next plan description, got %q", nextPlan.PlanDescription)
	}
}

func TestOpenNextPlanPromptsWhenPlanDescriptionMissing(t *testing.T) {
	backend := &memoryWorkbenchBackend{files: map[string]string{}}
	currentPath := "/demo/current/plan.json"
	currentText, err := ScaffoldPlanJSON("/demo/current", "当前计划意图")
	if err != nil {
		t.Fatalf("ScaffoldPlanJSON(current): %v", err)
	}
	nextText, err := ScaffoldContinuationPlanJSON("/demo/current", "CHK_NEXT", "intent_current", "port_next")
	if err != nil {
		t.Fatalf("ScaffoldContinuationPlanJSON(next): %v", err)
	}
	backend.files[currentPath] = currentText
	backend.files["/demo/current/next_plan.json"] = nextText
	if err := linkPlanChain(context.Background(), backend, currentPath, "/demo/current/next_plan.json"); err != nil {
		t.Fatalf("linkPlanChain: %v", err)
	}

	app := &App{
		ctx:         context.Background(),
		backend:     backend,
		model:       testWorkbenchModel(),
		application: tview.NewApplication(),
		uiReady:     make(chan struct{}),
	}
	app.model.ResolvedPath = currentPath
	app.initUI()
	app.refreshPlanChainView()

	if err := app.openNextPlan(); err != nil {
		t.Fatalf("openNextPlan: %v", err)
	}
	if !app.promptingPlanMeta {
		t.Fatalf("expected plan description prompt for next plan without description")
	}
}

func TestOpenNextPlanDoesNotPromptAfterDescriptionPersisted(t *testing.T) {
	backend := &memoryWorkbenchBackend{files: map[string]string{}}
	currentPath := "/demo/current/plan.json"
	currentText, err := ScaffoldPlanJSON("/demo/current", "当前计划意图")
	if err != nil {
		t.Fatalf("ScaffoldPlanJSON(current): %v", err)
	}
	nextText, err := ScaffoldContinuationPlanJSON("/demo/current", "CHK_NEXT", "intent_current", "port_next")
	if err != nil {
		t.Fatalf("ScaffoldContinuationPlanJSON(next): %v", err)
	}
	backend.files[currentPath] = currentText
	backend.files["/demo/current/next_plan.json"] = nextText
	if err := linkPlanChain(context.Background(), backend, currentPath, "/demo/current/next_plan.json"); err != nil {
		t.Fatalf("linkPlanChain: %v", err)
	}

	app := &App{
		ctx:         context.Background(),
		backend:     backend,
		model:       testWorkbenchModel(),
		application: tview.NewApplication(),
		uiReady:     make(chan struct{}),
	}
	app.model.ResolvedPath = currentPath
	app.initUI()
	app.refreshPlanChainView()

	if err := app.openNextPlan(); err != nil {
		t.Fatalf("openNextPlan first: %v", err)
	}
	if !app.promptingPlanMeta {
		t.Fatalf("expected initial prompt for missing plan description")
	}
	if err := app.updateAndPersistPlanDescription("已经填写好的续接计划说明"); err != nil {
		t.Fatalf("updateAndPersistPlanDescription: %v", err)
	}
	if !strings.Contains(backend.files["/demo/current/next_plan.json"], "已经填写好的续接计划说明") {
		t.Fatalf("expected persisted next plan description, got %s", backend.files["/demo/current/next_plan.json"])
	}
	app.promptingPlanMeta = false

	if err := app.openPrevPlan(); err != nil {
		t.Fatalf("openPrevPlan: %v", err)
	}
	if err := app.openNextPlan(); err != nil {
		t.Fatalf("openNextPlan second: %v", err)
	}
	if got := app.model.Plan.PlanDescription; got != "已经填写好的续接计划说明" {
		t.Fatalf("unexpected reopened next plan description %q", got)
	}
	if app.promptingPlanMeta {
		t.Fatalf("did not expect prompt after persisted plan description")
	}
}

func TestLoadModelRejectsDuplicateChunkIDsAcrossWorkspacePlans(t *testing.T) {
	backend := &memoryWorkbenchBackend{files: map[string]string{}}
	first, err := ScaffoldPlanJSON("/demo/current", "当前计划意图")
	if err != nil {
		t.Fatalf("ScaffoldPlanJSON(first): %v", err)
	}
	second, err := ScaffoldPlanJSON("/demo/other", "其他计划意图")
	if err != nil {
		t.Fatalf("ScaffoldPlanJSON(second): %v", err)
	}
	second = strings.Replace(second, "\"CHK_ROOT\"", "\"CHK_ROOT\"", 1)
	backend.files["/demo/ws/plan.json"] = first
	backend.files["/demo/ws/other.json"] = second

	_, err = LoadModelLenient(context.Background(), backend, "/demo/ws/plan.json")
	if err == nil {
		t.Fatalf("expected duplicate chunk_id error")
	}
	if !strings.Contains(err.Error(), "workspace duplicate chunk_id") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestModelSaveRejectsDuplicateChunkIDsAcrossWorkspacePlans(t *testing.T) {
	backend := &memoryWorkbenchBackend{files: map[string]string{}}
	current, err := ScaffoldPlanJSON("/demo/current", "当前计划意图")
	if err != nil {
		t.Fatalf("ScaffoldPlanJSON(current): %v", err)
	}
	other, err := ScaffoldPlanJSON("/demo/other", "其他计划意图")
	if err != nil {
		t.Fatalf("ScaffoldPlanJSON(other): %v", err)
	}
	backend.files["/demo/ws/plan.json"] = current
	backend.files["/demo/ws/other.json"] = other

	model := testWorkbenchModel()
	model.ResolvedPath = "/demo/ws/plan.json"
	if err := model.Save(context.Background(), backend); err == nil {
		t.Fatalf("expected save to reject duplicate chunk ids")
	}
}

func TestAddChildChunkSkipsChunkIDsUsedByDetachedSiblingPlan(t *testing.T) {
	backend := &memoryWorkbenchBackend{files: map[string]string{}}
	currentPath := "/demo/ws/plan.json"
	currentModel := testWorkbenchModel()
	currentModel.ResolvedPath = currentPath
	currentText, err := currentModel.Marshal()
	if err != nil {
		t.Fatalf("Marshal(current): %v", err)
	}
	siblingText, err := ScaffoldContinuationPlanJSON("/demo/ws", "CHK_001", "intent_workbench", "port_001")
	if err != nil {
		t.Fatalf("ScaffoldContinuationPlanJSON(sibling): %v", err)
	}
	backend.files[currentPath] = currentText
	backend.files["/demo/ws/detached_next.json"] = siblingText

	app := &App{
		ctx:         context.Background(),
		backend:     backend,
		model:       currentModel,
		application: tview.NewApplication(),
		uiReady:     make(chan struct{}),
	}
	app.initUI()

	if err := app.addChunkOfKind("task"); err != nil {
		t.Fatalf("addChunkOfKind: %v", err)
	}
	if app.model.SelectedChunkID != "CHK_002" {
		t.Fatalf("expected CHK_002 to avoid sibling CHK_001, got %q", app.model.SelectedChunkID)
	}
}

func TestPlanContinueSkipsChunkIDsUsedByDetachedSiblingPlan(t *testing.T) {
	backend := &memoryWorkbenchBackend{files: map[string]string{}}
	currentPath := "/demo/ws/plan.json"
	currentModel := testWorkbenchModel()
	currentModel.ResolvedPath = currentPath
	currentText, err := currentModel.Marshal()
	if err != nil {
		t.Fatalf("Marshal(current): %v", err)
	}
	siblingText, err := ScaffoldContinuationPlanJSON("/demo/ws", "CHK_001", "intent_workbench", "port_001")
	if err != nil {
		t.Fatalf("ScaffoldContinuationPlanJSON(sibling): %v", err)
	}
	backend.files[currentPath] = currentText
	backend.files["/demo/ws/detached_next.json"] = siblingText

	app := &App{
		ctx:         context.Background(),
		backend:     backend,
		model:       currentModel,
		application: tview.NewApplication(),
		uiReady:     make(chan struct{}),
	}
	app.initUI()

	if err := app.planContinueToJSON("fresh_next.json"); err != nil {
		t.Fatalf("planContinueToJSON: %v", err)
	}
	nextText, ok := backend.files["/demo/ws/fresh_next.json"]
	if !ok {
		t.Fatalf("expected fresh next plan file")
	}
	nextPlan, err := ParsePlanDocument(nextText)
	if err != nil {
		t.Fatalf("ParsePlanDocument(fresh next): %v", err)
	}
	if len(nextPlan.Chunks) != 1 || nextPlan.Chunks[0].ChunkID != "CHK_002" {
		t.Fatalf("expected next plan root chunk CHK_002, got %#v", nextPlan.Chunks)
	}
}
