package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"satis/bridge"
)

type App struct {
	ctx                  context.Context
	backend              Backend
	model                *Model
	application          *tview.Application
	pages                *tview.Pages
	layout               *tview.Flex
	statusBar            *tview.TextView
	prevPlanView         *tview.TextView
	nextPlanView         *tview.TextView
	graphView            *tview.TextView
	chunkEditor          *tview.TextArea
	decisionEditor       *tview.TextArea
	chunkMetaForm        *tview.Flex
	chunkDescField       *tview.InputField
	chunkPortField       *tview.InputField
	handoffTable         *tview.Table
	portField            *tview.InputField
	fromStepField        *tview.InputField
	varNameField         *tview.InputField
	supplementaryTable   *tview.Table
	handoffPreview       *tview.TextView
	logView              *tview.TextView
	diagnosticsView      *tview.TextView
	planningView         *tview.TextView
	commandInput         *tview.InputField
	paletteInput         *tview.InputField
	paletteList          *tview.List
	paletteVisible       bool
	focusables           []tview.Primitive
	focusBorders         []*tview.Box
	focusIndex           int
	currentChunkID       string
	currentHandoffKey    string
	suspendEditorSync    bool
	suspendDecisionSync  bool
	suspendFormSync      bool
	supplementaryRows    []supplementaryRow
	supplementaryActive  int
	supplementaryEditing bool
	supplementaryEditRow int
	supplementaryEditCol int
	lastSubmittedPlanID  string
	lastRunID            string
	eventCursorByRun     map[string]int
	graphNeighbors       map[string]graphNeighbors
	graphBoxes           map[string]graphNodeBox
	graphParents         map[string][]string
	graphChildren        map[string][]string
	graphParentJunctions []graphPoint
	graphVerticalUp      map[string]string
	graphVerticalDown    map[string]string
	validationIssues     []bridge.ValidationIssue
	validationByChunk    map[string][]bridge.ValidationIssue
	recentAddedChunkIDs  map[string]struct{}
	planChain            planChainLinks
	graphNavTarget       string
	uiReady              chan struct{}
	uiReadyOnce          sync.Once
	promptingPlanMeta    bool
}

type supplementaryRow struct {
	Key   string
	Value string
}

type humanControlChooserSetter interface {
	SetHumanControlChooser(bridge.HumanControlChooser)
}

func Open(ctx context.Context, backend Backend, path string, promptPlanDescription bool) error {
	app, err := New(ctx, backend, path, promptPlanDescription)
	if err != nil {
		return err
	}
	if setter, ok := backend.(humanControlChooserSetter); ok {
		setter.SetHumanControlChooser(app.promptHumanControlChoiceWhenReady)
		defer setter.SetHumanControlChooser(nil)
	}
	return app.Run()
}

func New(ctx context.Context, backend Backend, path string, promptPlanDescription bool) (*App, error) {
	model, err := LoadModelLenient(ctx, backend, path)
	if err != nil {
		return nil, err
	}
	wb := &App{
		ctx:                 ctx,
		backend:             backend,
		model:               model,
		application:         tview.NewApplication(),
		eventCursorByRun:    make(map[string]int),
		graphVerticalUp:     make(map[string]string),
		graphVerticalDown:   make(map[string]string),
		graphParents:        make(map[string][]string),
		graphChildren:       make(map[string][]string),
		recentAddedChunkIDs: make(map[string]struct{}),
		uiReady:             make(chan struct{}),
	}
	wb.initUI()
	wb.loadChunk(model.SelectedChunkID)
	if promptPlanDescription || strings.TrimSpace(model.Plan.PlanDescription) == "" {
		wb.promptPlanDescriptionEditor("New Plan Description", true)
	}
	if err := wb.restorePersistedRunState(); err != nil {
		wb.appendError("restore run state failed: %v", err)
	}
	wb.appendLog("workbench opened: %s", model.ResolvedPath)
	wb.appendLog("commands: render, validate, save, submit, start, run, status, inspect, events, chunk, reload, quit, quit!, help")
	wb.appendLog("planning: plan-continue <json> | plan-change <json> | plan-detach | plan-prev | plan-next | plan-draft <prompt> | plan-finish")
	wb.appendLog("shortcuts: Option+1..4 focus DAG/chunk/decision/cli (macOS) | Ctrl+Shift+R render | Ctrl+N add task chunk (DAG) | Shift+D add decision chunk (DAG) | Ctrl+T toggle task/decision (DAG) | Ctrl+K remove chunk (DAG)")
	return wb, nil
}

func (a *App) Run() error {
	root := tview.Primitive(a.layout)
	if a.pages != nil {
		root = a.pages
	}
	a.uiReadyOnce.Do(func() { close(a.uiReady) })
	return a.application.SetRoot(root, true).Run()
}

func (a *App) initUI() {
	a.graphView = tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(false)
	a.graphView.SetBorder(true).SetTitle(" Chunk Graph ")
	a.prevPlanView = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter)
	a.prevPlanView.SetBorder(true)
	a.nextPlanView = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter)
	a.nextPlanView.SetBorder(true)

	a.chunkEditor = tview.NewTextArea()
	a.chunkEditor.SetWrap(false)
	a.chunkEditor.SetBorder(true).SetTitle(" Chunk ")
	a.chunkEditor.SetChangedFunc(func() {
		if a.suspendEditorSync || a.currentChunkID == "" {
			return
		}
		if err := a.model.SetChunkText(a.currentChunkID, a.chunkEditor.GetText()); err != nil {
			a.appendLog("chunk update failed: %v", err)
			return
		}
		a.refreshTitles()
		a.refreshStatusBar()
	})
	a.decisionEditor = tview.NewTextArea()
	a.decisionEditor.SetWrap(false)
	a.decisionEditor.SetBorder(true).SetTitle(" Decision Config ")
	a.decisionEditor.SetChangedFunc(func() {
		if a.suspendDecisionSync || a.currentChunkID == "" {
			return
		}
		chunk := a.model.ChunkByID(a.currentChunkID)
		if !isDecisionChunk(chunk) {
			return
		}
		a.model.Dirty = true
		a.refreshTitles()
	})

	a.chunkPortField = tview.NewInputField().SetLabel("chunk port: ")
	a.chunkDescField = tview.NewInputField().SetLabel("chunk desc: ")
	a.chunkDescField.SetChangedFunc(func(text string) {
		if a.suspendFormSync || a.currentChunkID == "" {
			return
		}
		if err := a.model.SetChunkDescription(a.currentChunkID, text); err != nil && strings.TrimSpace(text) != "" {
			a.appendLog("chunk description update failed: %v", err)
			return
		}
		a.setChunkEditorText(a.model.ChunkByID(a.currentChunkID).Source.SatisText)
		a.refreshTitles()
		a.refreshStatusBar()
	})

	a.handoffTable = tview.NewTable().
		SetBorders(false).
		SetSelectable(true, false).
		SetFixed(1, 0)
	a.handoffTable.SetBorder(true).SetTitle(" Handoff ")
	a.handoffTable.SetSelectionChangedFunc(func(row, _ int) {
		if row <= 0 {
			return
		}
		rows, err := a.model.HandoffRows(a.currentChunkID)
		if err != nil || row-1 >= len(rows) {
			return
		}
		a.loadHandoffKey(rows[row-1].Key)
	})

	a.portField = tview.NewInputField().SetLabel("port: ")
	a.fromStepField = tview.NewInputField().SetLabel("fstep: ")
	a.varNameField = tview.NewInputField().SetLabel("var: ")
	a.supplementaryTable = tview.NewTable().
		SetBorders(true).
		SetSelectable(true, true).
		SetFixed(1, 0)
	a.supplementaryTable.SetBordersColor(tcell.ColorGray)
	a.supplementaryTable.SetBorder(true).SetTitle(" supplementary_info ")
	a.supplementaryTable.SetSelectionChangedFunc(func(row, _ int) {
		if a.suspendFormSync {
			return
		}
		if row <= 0 || row > len(a.supplementaryRows) {
			a.supplementaryActive = -1
			return
		}
		a.supplementaryActive = row - 1
	})
	a.supplementaryTable.SetSelectedFunc(func(row, column int) {
		if row <= 0 {
			return
		}
		if row > len(a.supplementaryRows) {
			a.addSupplementaryRow()
			a.refreshHandoffPreview()
			return
		}
		a.beginSupplementaryEdit(row, column)
	})
	a.supplementaryTable.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEnter:
			if a.supplementaryEditing {
				a.stopSupplementaryEditing()
				return nil
			}
			return event
		case tcell.KeyEsc:
			if a.supplementaryEditing {
				a.stopSupplementaryEditing()
				return nil
			}
			return event
		case tcell.KeyBackspace, tcell.KeyBackspace2:
			if a.supplementaryEditing {
				a.backspaceSupplementaryCell()
				return nil
			}
			return event
		case tcell.KeyUp, tcell.KeyDown, tcell.KeyLeft, tcell.KeyRight:
			if a.supplementaryEditing {
				a.stopSupplementaryEditing()
			}
			return event
		case tcell.KeyTAB:
			if a.supplementaryEditing {
				a.advanceSupplementaryEdit()
				return nil
			}
			return event
		case tcell.KeyBacktab:
			if a.supplementaryEditing {
				a.reverseSupplementaryEdit()
				return nil
			}
			return event
		case tcell.KeyCtrlK:
			if a.supplementaryEditing {
				a.stopSupplementaryEditing()
			}
			if a.removeActiveSupplementaryRow() {
				return nil
			}
			return event
		case tcell.KeyRune:
			if !a.supplementaryEditing {
				row, column := a.supplementaryTable.GetSelection()
				if row <= 0 {
					return event
				}
				if row > len(a.supplementaryRows) {
					a.addSupplementaryRow()
					row = len(a.supplementaryRows)
					column = 0
				}
				a.beginSupplementaryEdit(row, column)
			}
			a.appendSupplementaryRune(event.Rune())
			return nil
		default:
			return event
		}
	})
	a.handoffPreview = tview.NewTextView().
		SetDynamicColors(true)
	a.handoffPreview.SetBorder(true).SetTitle(" Handoff Preview ")

	onFormChanged := func() {
		if a.suspendFormSync {
			return
		}
		a.refreshHandoffPreview()
	}
	a.portField.SetChangedFunc(func(_ string) { onFormChanged() })
	a.fromStepField.SetChangedFunc(func(_ string) { onFormChanged() })
	a.varNameField.SetChangedFunc(func(_ string) { onFormChanged() })

	form := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.chunkPortField, 1, 0, false).
		AddItem(a.chunkDescField, 1, 0, false)
	a.chunkMetaForm = form

	center := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.chunkEditor, 0, 3, true).
		AddItem(form, 2, 0, false).
		AddItem(a.decisionEditor, 10, 0, false)
	center.SetBorder(true).SetTitle(" Chunk Detail ")

	a.logView = tview.NewTextView().
		SetDynamicColors(true)
	a.logView.SetChangedFunc(func() {
		// Keep the latest output visible when the log overflows.
		a.logView.ScrollToEnd()
	})
	a.logView.SetBorder(true).SetTitle(" CLI Log ")
	a.diagnosticsView = tview.NewTextView().
		SetDynamicColors(true)
	a.diagnosticsView.SetBorder(true).SetTitle(" Diagnostics ")
	a.planningView = tview.NewTextView().
		SetDynamicColors(true)
	a.planningView.SetBorder(true).SetTitle(" Planning ")
	a.commandInput = tview.NewInputField().
		SetLabel("> ").
		SetDoneFunc(func(key tcell.Key) {
			if key != tcell.KeyEnter {
				return
			}
			command := strings.TrimSpace(a.commandInput.GetText())
			a.commandInput.SetText("")
			if command == "" {
				return
			}
			a.runCommand(command)
		})

	right := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.logView, 0, 3, false).
		AddItem(a.diagnosticsView, 7, 0, false).
		AddItem(a.planningView, 9, 0, false).
		AddItem(a.commandInput, 1, 0, true)
	right.SetBorder(true).SetTitle(" CLI ")

	left := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.prevPlanView, 3, 0, false).
		AddItem(a.graphView, 0, 1, true).
		AddItem(a.nextPlanView, 3, 0, false)
	left.SetBorder(true).SetTitle(" Graph ")

	a.layout = tview.NewFlex().
		AddItem(left, 54, 0, true).
		AddItem(center, 0, 2, false).
		AddItem(right, 0, 1, false)

	a.statusBar = tview.NewTextView().
		SetDynamicColors(true)
	a.statusBar.SetBorder(true).SetTitle(" Status ")
	mainRoot := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.statusBar, 2, 0, false).
		AddItem(a.layout, 0, 1, true)

	a.paletteInput = tview.NewInputField().
		SetLabel("palette> ").
		SetChangedFunc(func(text string) {
			a.refreshPalette(text)
		})
	a.paletteInput.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyDown:
			if a.paletteList != nil && a.paletteList.GetItemCount() > 0 {
				a.paletteList.SetCurrentItem(0)
				a.application.SetFocus(a.paletteList)
				return nil
			}
		case tcell.KeyTAB:
			a.application.SetFocus(a.paletteList)
			return nil
		}
		return event
	})
	a.paletteInput.
		SetDoneFunc(func(key tcell.Key) {
			switch key {
			case tcell.KeyEnter:
				a.application.SetFocus(a.paletteList)
			case tcell.KeyEsc:
				a.closePalette()
			}
		})
	a.paletteList = tview.NewList().
		ShowSecondaryText(false)
	a.paletteList.SetBorder(true).SetTitle(" Commands ")
	a.paletteList.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyUp:
			if a.paletteList.GetCurrentItem() <= 0 {
				a.application.SetFocus(a.paletteInput)
				return nil
			}
		case tcell.KeyTAB:
			a.application.SetFocus(a.paletteInput)
			return nil
		}
		return event
	})
	a.paletteList.SetDoneFunc(func() {
		a.closePalette()
	})
	paletteBox := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.paletteInput, 1, 0, true).
		AddItem(a.paletteList, 0, 1, false)
	paletteBox.SetBorder(true).SetTitle(" Palette ")
	paletteModal := tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(paletteBox, 80, 0, true).
		AddItem(nil, 0, 1, false)
	paletteCentered := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(nil, 0, 1, false).
		AddItem(paletteModal, 18, 0, true).
		AddItem(nil, 0, 1, false)
	a.pages = tview.NewPages().
		AddPage("main", mainRoot, true, true).
		AddPage("palette", paletteCentered, true, false)

	a.focusables = []tview.Primitive{
		a.graphView,
		a.chunkEditor,
		a.decisionEditor,
		a.commandInput,
	}
	a.focusBorders = []*tview.Box{
		left.Box,
		center.Box,
		a.decisionEditor.Box,
		right.Box,
	}
	a.focusIndex = 0
	a.application.SetInputCapture(a.handleGlobalKey)

	a.refreshGraphView()
	a.refreshValidationState()
	a.refreshStatusBar()
	a.refreshPlanChainView()
	a.refreshPlanningPanel()
	a.applyFocusColors()
}

func (a *App) cycleFocus(delta int) {
	if len(a.focusables) == 0 {
		return
	}
	a.focusIndex = (a.focusIndex + delta + len(a.focusables)) % len(a.focusables)
	a.application.SetFocus(a.focusables[a.focusIndex])
	a.applyFocusColors()
}

func (a *App) promptPlanDescriptionEditor(title string, required bool) {
	if a == nil || a.application == nil || a.pages == nil || a.model == nil || a.model.Plan == nil || a.promptingPlanMeta {
		return
	}
	a.promptingPlanMeta = true
	previousFocus := a.application.GetFocus()
	input := tview.NewInputField().
		SetLabel("plan: ").
		SetText(a.model.Plan.PlanDescription)
	var form *tview.Form
	var closeModal func()
	saveAction := func() {
		if err := a.updateAndPersistPlanDescription(input.GetText()); err != nil {
			a.appendError("%v", err)
			return
		}
		closeModal()
	}
	cancelAction := func() {
		if required && strings.TrimSpace(a.model.Plan.PlanDescription) == "" {
			a.appendError("plan description is required")
			return
		}
		closeModal()
	}
	focusOrder := []tview.Primitive{input}
	focusIndex := 0
	setModalFocus := func(index int) {
		if len(focusOrder) == 0 {
			return
		}
		focusIndex = (index + len(focusOrder)) % len(focusOrder)
		a.application.SetFocus(focusOrder[focusIndex])
	}
	closeModal = func() {
		a.pages.RemovePage("plan_meta")
		a.promptingPlanMeta = false
		if previousFocus != nil {
			a.application.SetFocus(previousFocus)
		}
	}
	body := tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(true)
	body.SetText("Fill the current plan description. Esc cancels unless this field is required.")
	form = tview.NewForm().
		AddFormItem(input).
		AddButton("Save", saveAction)
	form.AddButton("Cancel", cancelAction)
	focusOrder = append(focusOrder, form.GetButton(0), form.GetButton(1))
	input.SetDoneFunc(func(key tcell.Key) {
		switch key {
		case tcell.KeyTab:
			setModalFocus(focusIndex + 1)
		case tcell.KeyBacktab:
			setModalFocus(focusIndex - 1)
		case tcell.KeyEnter:
			setModalFocus(1)
		}
	})
	form.SetBorder(true).SetTitle(title)
	form.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyTAB:
			setModalFocus(focusIndex + 1)
			return nil
		case tcell.KeyBacktab:
			setModalFocus(focusIndex - 1)
			return nil
		case tcell.KeyEsc:
			if required && strings.TrimSpace(a.model.Plan.PlanDescription) == "" {
				a.appendError("plan description is required")
				return nil
			}
			cancelAction()
			return nil
		case tcell.KeyEnter:
			if focusIndex == 1 {
				saveAction()
				return nil
			}
			if focusIndex == 2 {
				cancelAction()
				return nil
			}
		}
		return event
	})
	content := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(body, 3, 0, false).
		AddItem(form, 0, 1, true)
	content.SetBorder(true).SetTitle(" Plan Metadata ")
	modal := tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(content, 90, 0, true).
		AddItem(nil, 0, 1, false)
	centered := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(nil, 0, 1, false).
		AddItem(modal, 12, 0, true).
		AddItem(nil, 0, 1, false)
	a.pages.AddPage("plan_meta", centered, true, true)
	a.application.SetFocus(input)
}

func (a *App) updateAndPersistPlanDescription(text string) error {
	if a == nil || a.model == nil {
		return fmt.Errorf("workbench model is not initialized")
	}
	if err := a.model.SetPlanDescription(text); err != nil {
		return err
	}
	if a.backend != nil {
		if err := a.model.Save(a.ctx, a.backend); err != nil {
			return err
		}
	}
	a.refreshStatusBar()
	a.refreshTitles()
	a.refreshValidationState()
	return nil
}

func (a *App) handleGlobalKey(event *tcell.EventKey) *tcell.EventKey {
	if a.paletteVisible {
		switch event.Key() {
		case tcell.KeyEsc:
			a.closePalette()
			return nil
		case tcell.KeyDown:
			if a.paletteList != nil && a.paletteList.GetItemCount() > 0 {
				a.paletteList.SetCurrentItem(0)
				a.application.SetFocus(a.paletteList)
			}
			return nil
		}
	}
	if a.promptingPlanMeta {
		switch event.Key() {
		case tcell.KeyTAB, tcell.KeyBacktab, tcell.KeyEsc, tcell.KeyEnter:
			return event
		}
	}
	if event.Key() == tcell.KeyCtrlP && event.Modifiers()&tcell.ModShift != 0 {
		a.promptPlanDescriptionEditor("Edit Plan Description", false)
		return nil
	}
	if event.Key() == tcell.KeyRune {
		mods := event.Modifiers()
		if mods&tcell.ModAlt != 0 && mods&tcell.ModCtrl == 0 {
			// Linux/iTerm2 "Use Option as Meta key" mode: ESC+digit → ModAlt+digit
			switch event.Rune() {
			case '1':
				a.setFocus(a.graphView)
				return nil
			case '2':
				a.setFocus(a.chunkEditor)
				return nil
			case '3':
				a.setFocus(a.decisionEditor)
				return nil
			case '4':
				a.setFocus(a.commandInput)
				return nil
			}
		}
		if mods == 0 {
			// macOS Terminal / iTerm2 default: Option+digit produces Unicode chars
			// US keyboard layout: ¡ ™ £ ¢
			switch event.Rune() {
			case '\u00a1': // Option+1 → ¡
				a.setFocus(a.graphView)
				return nil
			case '\u2122': // Option+2 → ™
				a.setFocus(a.chunkEditor)
				return nil
			case '\u00a3': // Option+3 → £
				a.setFocus(a.decisionEditor)
				return nil
			case '\u00a2': // Option+4 → ¢
				a.setFocus(a.commandInput)
				return nil
			}
		}
		if mods&(tcell.ModCtrl|tcell.ModShift) == (tcell.ModCtrl | tcell.ModShift) {
			r := event.Rune()
			if r == 'r' || r == 'R' {
				a.runCommand("render")
				return nil
			}
		}
		if mods == tcell.ModShift && a.isGraphFocus() {
			switch event.Rune() {
			case 'D':
				if err := a.addDecisionChunk(); err != nil {
					a.appendError("%v", err)
				}
				return nil
			}
		}
	}
	switch event.Key() {
	case tcell.KeyUp:
		if a.isGraphFocus() {
			a.moveGraphSelection("up")
			return nil
		}
	case tcell.KeyDown:
		if a.isGraphFocus() {
			a.moveGraphSelection("down")
			return nil
		}
	case tcell.KeyLeft:
		if a.isGraphFocus() {
			a.moveGraphSelection("left")
			return nil
		}
	case tcell.KeyRight:
		if a.isGraphFocus() {
			a.moveGraphSelection("right")
			return nil
		}
	case tcell.KeyEnter:
		if a.isGraphFocus() {
			switch a.graphNavTarget {
			case "prev":
				if err := a.openPrevPlan(); err != nil {
					a.appendError("%v", err)
				}
				return nil
			case "next":
				if err := a.openNextPlan(); err != nil {
					a.appendError("%v", err)
				}
				return nil
			}
			selected := a.selectedGraphChunkID()
			a.loadChunk(selected)
			return nil
		}
	case tcell.KeyTAB:
		a.cycleFocus(1)
		return nil
	case tcell.KeyBacktab:
		a.cycleFocus(-1)
		return nil
	case tcell.KeyCtrlP:
		a.openPalette()
		return nil
	case tcell.KeyCtrlA:
		a.cycleFocus(-1)
		return nil
	case tcell.KeyCtrlD:
		a.cycleFocus(1)
		return nil
	case tcell.KeyCtrlN:
		if a.isGraphFocus() {
			if err := a.addChunk(); err != nil {
				a.appendError("%v", err)
			}
			return nil
		}
		if a.isHandoffTableFocus() {
			a.appendLog("handoff ports are auto-synced from chunk body workflow vars (@<port>__<var>)")
			return nil
		}
		if a.isSupplementaryFocus() {
			a.addSupplementaryRow()
			a.refreshHandoffPreview()
			return nil
		}
		return event
	case tcell.KeyCtrlT:
		if a.isGraphFocus() {
			if err := a.cycleSelectedChunkKind(); err != nil {
				a.appendError("%v", err)
			}
			return nil
		}
		return event
	case tcell.KeyCtrlK:
		if a.isGraphFocus() {
			if err := a.removeSelectedChunk(); err != nil {
				a.appendError("%v", err)
			}
			return nil
		}
		if a.isHandoffTableFocus() {
			a.appendLog("handoff ports are auto-synced from chunk body workflow vars (@<port>__<var>)")
			return nil
		}
		if a.isSupplementaryFocus() && a.removeActiveSupplementaryRow() {
			a.refreshHandoffPreview()
			return nil
		}
		return event
	case tcell.KeyCtrlS:
		a.runCommand("save")
		return nil
	case tcell.KeyCtrlSpace:
		a.openPalette()
		return nil
	case tcell.KeyEsc:
		if a.model.Dirty {
			a.appendLog("unsaved changes; use save or quit! to discard")
			return nil
		}
		a.syncRunStateOnExit()
		a.application.Stop()
		return nil
	}
	return event
}

func (a *App) refreshGraphView() {
	if a.graphView == nil {
		return
	}
	result := renderASCIIChunkGraph(a.model.Plan, a.model.SelectedChunkID, a.validationByChunk)
	a.graphNeighbors = result.Neighbors
	a.graphBoxes = result.Boxes
	a.graphParents = result.Parents
	a.graphChildren = result.Children
	a.graphParentJunctions = result.ParentJunctions
	text := result.Text
	selectedBox, hasSelected := result.Boxes[a.model.SelectedChunkID]
	if a.graphNavTarget == "prev" || a.graphNavTarget == "next" {
		hasSelected = false
	}
	text = colorizeGraphDecorations(text, result.Boxes, result.DecisionChunkIDs, selectedBox, hasSelected, result.ParentJunctions, a.recentAddedChunkIDs)
	a.graphView.SetText(text)
	a.centerGraphSelection()
	a.refreshTitles()
}

func colorizeGraphDecorations(
	text string,
	boxes map[string]graphNodeBox,
	decisionChunkIDs []string,
	box graphNodeBox,
	hasSelected bool,
	junctions []graphPoint,
	recentAdded map[string]struct{},
) string {
	lines := strings.Split(text, "\n")
	type styleSpan struct {
		start    int
		end      int
		color    string
		priority int
	}
	spansByLine := make(map[int][]styleSpan)
	addSpan := func(y, start, end int, color string, priority int) {
		if y < 0 || y >= len(lines) {
			return
		}
		if start < 0 {
			start = 0
		}
		if end > len(lines[y]) {
			end = len(lines[y])
		}
		if end <= start {
			return
		}
		spansByLine[y] = append(spansByLine[y], styleSpan{start: start, end: end, color: color, priority: priority})
	}
	for _, point := range junctions {
		addSpan(point.Y, point.X, point.X+1, "yellow", 1)
	}
	for _, chunkID := range decisionChunkIDs {
		nodeBox, ok := boxes[chunkID]
		if !ok {
			continue
		}
		// Selected node already uses deepskyblue full-box highlight.
		if hasSelected && nodeBox == box {
			continue
		}
		for y := nodeBox.Y; y < nodeBox.Y+nodeBox.Height && y >= 0 && y < len(lines); y++ {
			if y == nodeBox.Y || y == nodeBox.Y+nodeBox.Height-1 {
				addSpan(y, nodeBox.X, nodeBox.X+nodeBox.Width, "yellow", 2)
				continue
			}
			addSpan(y, nodeBox.X, nodeBox.X+1, "yellow", 2)
			addSpan(y, nodeBox.X+nodeBox.Width-1, nodeBox.X+nodeBox.Width, "yellow", 2)
		}
	}
	for chunkID := range recentAdded {
		nodeBox, ok := boxes[chunkID]
		if !ok {
			continue
		}
		if hasSelected && nodeBox == box {
			continue
		}
		for y := nodeBox.Y; y < nodeBox.Y+nodeBox.Height && y >= 0 && y < len(lines); y++ {
			addSpan(y, nodeBox.X, nodeBox.X+nodeBox.Width, "green", 3)
		}
	}
	if hasSelected {
		for y := box.Y; y < box.Y+box.Height && y >= 0 && y < len(lines); y++ {
			addSpan(y, box.X, box.X+box.Width, "deepskyblue", 4)
		}
	}
	for y, line := range lines {
		if len(spansByLine[y]) == 0 || len(line) == 0 {
			continue
		}
		styles := make([]string, len(line))
		priorities := make([]int, len(line))
		for _, span := range spansByLine[y] {
			for x := span.start; x < span.end && x < len(line); x++ {
				if span.priority >= priorities[x] {
					priorities[x] = span.priority
					styles[x] = span.color
				}
			}
		}
		var b strings.Builder
		current := ""
		for i := 0; i < len(line); i++ {
			if styles[i] != current {
				if current != "" {
					b.WriteString("[-]")
				}
				current = styles[i]
				if current != "" {
					b.WriteString("[")
					b.WriteString(current)
					b.WriteString("]")
				}
			}
			b.WriteByte(line[i])
		}
		if current != "" {
			b.WriteString("[-]")
		}
		lines[y] = b.String()
	}
	return strings.Join(lines, "\n")
}

func (a *App) centerGraphSelection() {
	if a.graphView == nil {
		return
	}
	box, ok := a.graphBoxes[a.model.SelectedChunkID]
	if !ok {
		return
	}
	_, _, width, height := a.graphView.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}
	row := max(0, box.CenterY-height/2)
	col := max(0, box.CenterX-width/2)
	a.graphView.ScrollTo(row, col)
}

func (a *App) moveGraphSelection(direction string) {
	if a.graphNavTarget == "prev" {
		switch direction {
		case "down":
			a.graphNavTarget = ""
			a.focusGraphChunk(defaultSelectedChunkID(a.model.Plan))
			return
		case "up":
			return
		}
	}
	if a.graphNavTarget == "next" {
		switch direction {
		case "up":
			a.graphNavTarget = ""
			a.focusGraphChunk(a.bottomMostSelectedChunkID())
			return
		case "down":
			return
		}
	}
	current := a.selectedGraphChunkID()
	if current == "" {
		current = defaultSelectedChunkID(a.model.Plan)
	}
	next := ""
	switch direction {
	case "up":
		next = a.parentNeighborForUp(current)
	case "down":
		next = a.childNeighborForDown(current)
	case "left":
		next = a.graphNeighbors[current].Left
	case "right":
		next = a.graphNeighbors[current].Right
	}
	if strings.TrimSpace(next) == "" {
		switch direction {
		case "up":
			if len(a.graphParents[current]) == 0 {
				a.graphNavTarget = "prev"
				a.refreshGraphView()
				a.refreshPlanChainView()
			}
		case "down":
			if len(a.graphChildren[current]) == 0 {
				a.graphNavTarget = "next"
				a.refreshGraphView()
				a.refreshPlanChainView()
			}
		}
		return
	}
	a.graphNavTarget = ""
	a.rememberVerticalNavigation(current, next, direction)
	a.loadChunk(next)
}

func (a *App) focusGraphChunk(chunkID string) {
	chunkID = strings.TrimSpace(chunkID)
	if chunkID == "" {
		return
	}
	if chunkID == a.currentChunkID {
		a.model.SetSelectedChunk(chunkID)
		a.refreshGraphView()
		a.refreshPlanChainView()
		return
	}
	a.loadChunk(chunkID)
}

func (a *App) bottomMostSelectedChunkID() string {
	current := a.selectedGraphChunkID()
	if current == "" {
		current = defaultSelectedChunkID(a.model.Plan)
	}
	for {
		next := a.childNeighborForDown(current)
		if strings.TrimSpace(next) == "" {
			return current
		}
		current = next
	}
}

func (a *App) parentNeighborForUp(current string) string {
	if strings.TrimSpace(current) == "" {
		return ""
	}
	parentIDs := a.graphParents[current]
	if len(parentIDs) == 0 {
		return ""
	}
	cached := strings.TrimSpace(a.graphVerticalUp[current])
	if cached != "" {
		for _, id := range parentIDs {
			if id == cached {
				return id
			}
		}
	}
	currentBox, hasCurrentBox := a.graphBoxes[current]
	bestID := ""
	bestX := 1 << 30
	bestLayer := 1 << 30
	for _, id := range parentIDs {
		parentBox, ok := a.graphBoxes[id]
		if !ok || !hasCurrentBox {
			if bestID == "" || id < bestID {
				bestID = id
			}
			continue
		}
		xDelta := abs(parentBox.CenterX - currentBox.CenterX)
		layerDelta := abs(currentBox.Layer - parentBox.Layer)
		if xDelta < bestX ||
			(xDelta == bestX && layerDelta < bestLayer) ||
			(xDelta == bestX && layerDelta == bestLayer && id < bestID) {
			bestID = id
			bestX = xDelta
			bestLayer = layerDelta
		}
	}
	return bestID
}

func (a *App) childNeighborForDown(current string) string {
	if strings.TrimSpace(current) == "" {
		return ""
	}
	childIDs := a.graphChildren[current]
	if len(childIDs) == 0 {
		return ""
	}
	cached := strings.TrimSpace(a.graphVerticalDown[current])
	if cached != "" {
		for _, id := range childIDs {
			if id == cached {
				return id
			}
		}
	}
	currentBox, hasCurrentBox := a.graphBoxes[current]
	bestID := ""
	bestX := 1 << 30
	bestLayer := 1 << 30
	for _, id := range childIDs {
		childBox, ok := a.graphBoxes[id]
		if !ok || !hasCurrentBox {
			if bestID == "" || id < bestID {
				bestID = id
			}
			continue
		}
		xDelta := abs(childBox.CenterX - currentBox.CenterX)
		layerDelta := abs(childBox.Layer - currentBox.Layer)
		if xDelta < bestX ||
			(xDelta == bestX && layerDelta < bestLayer) ||
			(xDelta == bestX && layerDelta == bestLayer && id < bestID) {
			bestID = id
			bestX = xDelta
			bestLayer = layerDelta
		}
	}
	return bestID
}

func (a *App) rememberVerticalNavigation(current string, next string, direction string) {
	switch direction {
	case "up":
		if !containsString(a.graphParents[current], next) {
			return
		}
		a.graphVerticalUp[current] = next
		a.graphVerticalDown[next] = current
	case "down":
		if !containsString(a.graphChildren[current], next) {
			return
		}
		a.graphVerticalDown[current] = next
		a.graphVerticalUp[next] = current
	}
}

func (a *App) loadChunk(chunkID string) {
	if chunkID == "" || chunkID == a.currentChunkID {
		return
	}
	if err := a.applyCurrentChunkPortForm(); err != nil {
		a.appendError("chunk_port apply warning: %v", err)
	}
	if err := a.applyCurrentDecisionForm(); err != nil {
		a.appendError("decision apply warning: %v", err)
	}
	if err := a.applyCurrentHandoffForm(); err != nil {
		a.appendError("handoff apply warning: %v", err)
	}
	chunk := a.model.ChunkByID(chunkID)
	if chunk == nil {
		return
	}
	a.currentChunkID = chunkID
	a.graphNavTarget = ""
	a.model.SetSelectedChunk(chunkID)
	a.setChunkEditorText(chunk.Source.SatisText)
	a.setDecisionEditorText(formatDecisionEditorText(a.model.Plan, chunk))
	a.decisionEditor.SetDisabled(!isDecisionChunk(chunk))
	a.suspendFormSync = true
	a.chunkDescField.SetText(chunk.Description)
	a.chunkPortField.SetText(extractChunkHeaderValue(chunk.Source.SatisText, chunkPortMetaKey))
	a.suspendFormSync = false
	a.refreshGraphView()
	a.refreshHandoffTable()
	a.refreshTitles()
	a.refreshDiagnostics()
	a.refreshStatusBar()
	a.refreshPlanChainView()
	a.refreshPlanningPanel()
}

func (a *App) openRegisteredPlan(targetPath string, label string) error {
	targetPath = pathOrEmpty(targetPath)
	if targetPath == "" {
		return fmt.Errorf("no %s plan registered", label)
	}
	if a.pages != nil {
		a.pages.RemovePage("plan_meta")
	}
	a.promptingPlanMeta = false
	model, err := LoadModelLenient(a.ctx, a.backend, targetPath)
	if err != nil {
		return fmt.Errorf("open %s plan %s failed: %w", label, targetPath, err)
	}
	a.model = model
	a.currentChunkID = ""
	a.currentHandoffKey = ""
	a.lastSubmittedPlanID = ""
	a.lastRunID = ""
	a.eventCursorByRun = make(map[string]int)
	a.recentAddedChunkIDs = map[string]struct{}{}
	a.refreshGraphView()
	a.loadChunk(defaultSelectedChunkID(a.model.Plan))
	a.refreshValidationState()
	a.refreshPlanChainView()
	a.refreshPlanningPanel()
	if strings.TrimSpace(a.model.Plan.PlanDescription) == "" {
		a.promptPlanDescriptionEditor("New Plan Description", true)
	}
	a.appendLog("opened %s plan: %s", label, targetPath)
	return nil
}

func (a *App) openNextPlan() error {
	return a.openRegisteredPlan(a.planChain.Next, "next")
}

func (a *App) openPrevPlan() error {
	return a.openRegisteredPlan(a.planChain.Prev, "previous")
}

func (a *App) refreshHandoffTable() {
	a.handoffTable.Clear()
	headers := []string{"port", "fstep", "var"}
	for col, header := range headers {
		cell := tview.NewTableCell(strings.ToUpper(header)).
			SetTextColor(tcell.ColorYellow).
			SetSelectable(false)
		a.handoffTable.SetCell(0, col, cell)
	}
	rows, err := a.model.HandoffRows(a.currentChunkID)
	if err != nil {
		a.appendError("handoff load failed: %v", err)
		return
	}
	for rowIndex, row := range rows {
		a.handoffTable.SetCell(rowIndex+1, 0, tview.NewTableCell(row.Port))
		a.handoffTable.SetCell(rowIndex+1, 1, tview.NewTableCell(row.FromStep))
		a.handoffTable.SetCell(rowIndex+1, 2, tview.NewTableCell(row.VarName))
	}
	if len(rows) == 0 {
		a.currentHandoffKey = ""
		a.loadHandoffRow(HandoffRow{})
		return
	}
	a.handoffTable.Select(1, 0)
	a.loadHandoffKey(rows[0].Key)
}

func (a *App) loadHandoffKey(key string) {
	rows, err := a.model.HandoffRows(a.currentChunkID)
	if err != nil {
		a.appendError("handoff load failed: %v", err)
		return
	}
	for _, row := range rows {
		if row.Key == key {
			a.currentHandoffKey = key
			a.loadHandoffRow(row)
			return
		}
	}
}

func (a *App) loadHandoffRow(row HandoffRow) {
	a.suspendFormSync = true
	a.portField.SetText(row.Port)
	a.fromStepField.SetText(row.FromStep)
	a.varNameField.SetText(row.VarName)
	a.supplementaryRows = parseSupplementaryRows(row.SupplementaryJSON)
	a.supplementaryActive = -1
	a.stopSupplementaryEditing()
	a.refreshSupplementaryTable()
	a.suspendFormSync = false
	a.refreshHandoffPreview()
}

func (a *App) currentHandoffRowFromForm() HandoffRow {
	supplementaryJSON := marshalSupplementaryRows(a.supplementaryRows)
	return HandoffRow{
		Port:              a.portField.GetText(),
		FromStep:          a.fromStepField.GetText(),
		VarName:           a.varNameField.GetText(),
		SupplementaryJSON: supplementaryJSON,
	}
}

func (a *App) applyCurrentHandoffForm() error {
	if a.currentChunkID == "" {
		return nil
	}
	if strings.TrimSpace(a.currentHandoffKey) == "" {
		// Handoff rows are body-driven; avoid creating ad-hoc rows from the form.
		return nil
	}
	row := a.currentHandoffRowFromForm()
	rows, err := a.model.HandoffRows(a.currentChunkID)
	if err != nil {
		return err
	}
	var existing *HandoffRow
	for i := range rows {
		if strings.TrimSpace(rows[i].Key) == strings.TrimSpace(a.currentHandoffKey) {
			existing = &rows[i]
			break
		}
	}
	if existing == nil {
		return nil
	}
	row.Key = existing.Key
	if strings.TrimSpace(row.Port) == "" &&
		strings.TrimSpace(row.FromStep) == "" &&
		strings.TrimSpace(row.VarName) == "" &&
		strings.TrimSpace(row.SupplementaryJSON) == "" {
		return nil
	}
	if err := a.model.UpsertHandoffRow(a.currentChunkID, existing.Key, row); err != nil {
		return err
	}
	a.currentHandoffKey = handoffBindingKey(strings.TrimSpace(row.Port), strings.TrimSpace(row.VarName))
	return nil
}

func (a *App) applyCurrentChunkPortForm() error {
	if a.currentChunkID == "" {
		return nil
	}
	rename, err := a.model.RenameChunkPort(a.currentChunkID, a.chunkPortField.GetText())
	if err != nil {
		return err
	}
	if rename == nil {
		return nil
	}
	oldPort := rename.OldPort
	if strings.TrimSpace(oldPort) == "" {
		oldPort = "(missing)"
	}
	a.appendLog("updated chunk_port for %s: %s -> %s", chunkDisplayLabel(a.model.Plan, rename.ChunkID), oldPort, rename.NewPort)
	if chunk := a.model.ChunkByID(a.currentChunkID); chunk != nil {
		a.setChunkEditorText(chunk.Source.SatisText)
	}
	a.suspendFormSync = true
	a.chunkPortField.SetText(rename.NewPort)
	a.suspendFormSync = false
	return nil
}

func (a *App) applyCurrentDecisionForm() error {
	if a.currentChunkID == "" || a.decisionEditor == nil {
		return nil
	}
	chunk := a.model.ChunkByID(a.currentChunkID)
	if !isDecisionChunk(chunk) {
		return nil
	}
	cfg, err := parseDecisionEditorText(a.decisionEditor.GetText())
	if err != nil {
		return err
	}
	return a.model.ReplaceDecisionConfig(a.currentChunkID, cfg)
}

func (a *App) refreshHandoffPreview() {
	row := a.currentHandoffRowFromForm()
	if strings.TrimSpace(row.Port) == "" {
		a.handoffPreview.SetText("No handoff input on selected chunk.")
		return
	}
	if strings.TrimSpace(row.SupplementaryJSON) != "" {
		var payload map[string]any
		if err := jsonUnmarshal([]byte(row.SupplementaryJSON), &payload); err != nil {
			a.handoffPreview.SetText(fmt.Sprintf("[red]invalid supplementary_info JSON:[-] %v", err))
			return
		}
	}
	spec := map[string]any{}
	if strings.TrimSpace(row.FromStep) != "" {
		spec["from_step"] = strings.TrimSpace(row.FromStep)
	}
	if strings.TrimSpace(row.VarName) != "" {
		spec["var_name"] = strings.TrimSpace(row.VarName)
	}
	if strings.TrimSpace(row.SupplementaryJSON) != "" {
		var supplementary map[string]any
		_ = jsonUnmarshal([]byte(row.SupplementaryJSON), &supplementary)
		spec["supplementary_info"] = supplementary
	}
	formatted, err := jsonMarshalIndent(spec)
	if err != nil {
		a.handoffPreview.SetText(fmt.Sprintf("[red]preview error:[-] %v", err))
		return
	}
	a.handoffPreview.SetText(string(formatted))
}

func (a *App) refreshSupplementaryTable() {
	wasSuspended := a.suspendFormSync
	a.suspendFormSync = true
	defer func() { a.suspendFormSync = wasSuspended }()

	selectedRow, selectedCol := a.supplementaryTable.GetSelection()
	a.supplementaryTable.Clear()
	headers := []string{"KEY", "VALUE"}
	for col, header := range headers {
		a.supplementaryTable.SetCell(0, col, tview.NewTableCell(header).SetTextColor(tcell.ColorYellow).SetSelectable(false))
	}
	for i, row := range a.supplementaryRows {
		a.supplementaryTable.SetCell(i+1, 0, tview.NewTableCell(row.Key))
		a.supplementaryTable.SetCell(i+1, 1, tview.NewTableCell(row.Value))
	}
	newRow := len(a.supplementaryRows) + 1
	a.supplementaryTable.SetCell(newRow, 0, tview.NewTableCell("+").SetTextColor(tcell.ColorGreen))
	a.supplementaryTable.SetCell(newRow, 1, tview.NewTableCell(""))
	if a.supplementaryEditing && a.supplementaryEditRow > 0 && a.supplementaryEditRow <= len(a.supplementaryRows) {
		a.supplementaryTable.Select(a.supplementaryEditRow, clampSupplementaryColumn(a.supplementaryEditCol))
		return
	}
	if a.supplementaryActive >= 0 && a.supplementaryActive < len(a.supplementaryRows) {
		a.supplementaryTable.Select(a.supplementaryActive+1, clampSupplementaryColumn(selectedCol))
		return
	}
	if selectedRow == newRow {
		a.supplementaryTable.Select(newRow, 0)
		return
	}
	a.supplementaryTable.Select(newRow, 0)
}

func (a *App) addSupplementaryRow() {
	row := supplementaryRow{}
	a.supplementaryRows = append(a.supplementaryRows, row)
	a.supplementaryActive = len(a.supplementaryRows) - 1
	a.refreshSupplementaryTable()
	a.setFocus(a.supplementaryTable)
	a.beginSupplementaryEdit(a.supplementaryActive+1, 0)
}

func (a *App) removeActiveSupplementaryRow() bool {
	if a.supplementaryTable == nil {
		return false
	}
	row, _ := a.supplementaryTable.GetSelection()
	if row <= 0 {
		return false
	}
	if row > len(a.supplementaryRows) {
		return false
	}
	idx := row - 1
	a.supplementaryRows = append(a.supplementaryRows[:idx], a.supplementaryRows[idx+1:]...)
	a.stopSupplementaryEditing()
	if len(a.supplementaryRows) == 0 {
		a.supplementaryActive = -1
	} else if idx >= len(a.supplementaryRows) {
		a.supplementaryActive = len(a.supplementaryRows) - 1
	} else {
		a.supplementaryActive = idx
	}
	a.refreshSupplementaryTable()
	a.setFocus(a.supplementaryTable)
	a.refreshHandoffPreview()
	return true
}

func parseSupplementaryRows(raw string) []supplementaryRow {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var obj map[string]any
	if err := jsonUnmarshal([]byte(raw), &obj); err != nil {
		return nil
	}
	keys := make([]string, 0, len(obj))
	for key := range obj {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	rows := make([]supplementaryRow, 0, len(keys))
	for _, key := range keys {
		rows = append(rows, supplementaryRow{
			Key:   key,
			Value: fmt.Sprintf("%v", obj[key]),
		})
	}
	return rows
}

func marshalSupplementaryRows(rows []supplementaryRow) string {
	if len(rows) == 0 {
		return ""
	}
	obj := make(map[string]any, len(rows))
	for _, row := range rows {
		key := strings.TrimSpace(row.Key)
		if key == "" {
			continue
		}
		obj[key] = row.Value
	}
	if len(obj) == 0 {
		return ""
	}
	data, err := jsonMarshalIndent(obj)
	if err != nil {
		return ""
	}
	return string(data)
}

func (a *App) isSupplementaryFocus() bool {
	if a == nil || a.application == nil {
		return false
	}
	switch a.application.GetFocus() {
	case a.supplementaryTable:
		return true
	default:
		return false
	}
}

func (a *App) isGraphFocus() bool {
	if a == nil {
		return false
	}
	if a.focusIndex == 0 {
		return true
	}
	if a.application == nil {
		return false
	}
	return a.application.GetFocus() == a.graphView
}

func (a *App) isHandoffTableFocus() bool {
	if a == nil || a.application == nil {
		return false
	}
	return a.application.GetFocus() == a.handoffTable
}

func clampSupplementaryColumn(col int) int {
	if col <= 0 {
		return 0
	}
	return 1
}

func (a *App) beginSupplementaryEdit(row, col int) {
	if row <= 0 || row > len(a.supplementaryRows) {
		return
	}
	a.supplementaryEditing = true
	a.supplementaryEditRow = row
	a.supplementaryEditCol = clampSupplementaryColumn(col)
	a.supplementaryActive = row - 1
	a.supplementaryTable.Select(row, a.supplementaryEditCol)
}

func (a *App) stopSupplementaryEditing() {
	a.supplementaryEditing = false
	a.supplementaryEditRow = 0
	a.supplementaryEditCol = 0
}

func (a *App) appendSupplementaryRune(r rune) {
	row, col, ok := a.currentSupplementaryEditCell()
	if !ok {
		return
	}
	a.setSupplementaryCellText(row, col, a.supplementaryCellText(row, col)+string(r))
}

func (a *App) backspaceSupplementaryCell() {
	row, col, ok := a.currentSupplementaryEditCell()
	if !ok {
		return
	}
	current := a.supplementaryCellText(row, col)
	if current == "" {
		return
	}
	runes := []rune(current)
	a.setSupplementaryCellText(row, col, string(runes[:len(runes)-1]))
}

func (a *App) advanceSupplementaryEdit() {
	if !a.supplementaryEditing {
		return
	}
	row := a.supplementaryEditRow
	col := a.supplementaryEditCol
	if col == 0 {
		a.beginSupplementaryEdit(row, 1)
		return
	}
	if row == len(a.supplementaryRows) {
		a.addSupplementaryRow()
		return
	}
	a.beginSupplementaryEdit(row+1, 0)
}

func (a *App) reverseSupplementaryEdit() {
	if !a.supplementaryEditing {
		return
	}
	row := a.supplementaryEditRow
	col := a.supplementaryEditCol
	if col == 1 {
		a.beginSupplementaryEdit(row, 0)
		return
	}
	if row <= 1 {
		return
	}
	a.beginSupplementaryEdit(row-1, 1)
}

func (a *App) currentSupplementaryEditCell() (int, int, bool) {
	if !a.supplementaryEditing {
		return 0, 0, false
	}
	row := a.supplementaryEditRow
	if row <= 0 || row > len(a.supplementaryRows) {
		return 0, 0, false
	}
	return row, clampSupplementaryColumn(a.supplementaryEditCol), true
}

func (a *App) supplementaryCellText(row, col int) string {
	if row <= 0 || row > len(a.supplementaryRows) {
		return ""
	}
	if clampSupplementaryColumn(col) == 0 {
		return a.supplementaryRows[row-1].Key
	}
	return a.supplementaryRows[row-1].Value
}

func (a *App) setSupplementaryCellText(row, col int, text string) {
	if row <= 0 || row > len(a.supplementaryRows) {
		return
	}
	switch clampSupplementaryColumn(col) {
	case 0:
		a.supplementaryRows[row-1].Key = text
	case 1:
		a.supplementaryRows[row-1].Value = text
	}
	a.supplementaryActive = row - 1
	a.refreshSupplementaryTable()
	a.refreshHandoffPreview()
}

func (a *App) setFocus(target tview.Primitive) {
	if a == nil || target == nil {
		return
	}
	for i, primitive := range a.focusables {
		if primitive == target {
			a.focusIndex = i
			break
		}
	}
	a.application.SetFocus(target)
	a.applyFocusColors()
}

func (a *App) applyFocusColors() {
	for i, box := range a.focusBorders {
		if box == nil {
			continue
		}
		if i == a.focusIndex {
			box.SetBorderColor(tcell.ColorBlue)
			box.SetTitleColor(tcell.ColorBlue)
		} else {
			box.SetBorderColor(tcell.ColorWhite)
			box.SetTitleColor(tcell.ColorWhite)
		}
	}
	if a.prevPlanView != nil {
		if a.graphNavTarget == "prev" && a.isGraphFocus() {
			a.prevPlanView.SetBorderColor(tcell.ColorDarkBlue)
			a.prevPlanView.SetTitleColor(tcell.ColorDarkBlue)
		} else {
			a.prevPlanView.SetBorderColor(tcell.ColorWhite)
			a.prevPlanView.SetTitleColor(tcell.ColorWhite)
		}
	}
	if a.nextPlanView != nil {
		if a.graphNavTarget == "next" && a.isGraphFocus() {
			a.nextPlanView.SetBorderColor(tcell.ColorDarkBlue)
			a.nextPlanView.SetTitleColor(tcell.ColorDarkBlue)
		} else {
			a.nextPlanView.SetBorderColor(tcell.ColorWhite)
			a.nextPlanView.SetTitleColor(tcell.ColorWhite)
		}
	}
}

func (a *App) refreshTitles() {
	dirtySuffix := ""
	if a.model.Dirty {
		dirtySuffix = " *"
	}
	if a.chunkEditor != nil {
		a.chunkEditor.SetTitle(fmt.Sprintf(" Chunk %s%s ", chunkDisplayLabel(a.model.Plan, a.currentChunkID), dirtySuffix))
	}
	if a.decisionEditor != nil {
		title := fmt.Sprintf(" Decision Config %s%s ", chunkDisplayLabel(a.model.Plan, a.currentChunkID), dirtySuffix)
		if chunk := a.model.ChunkByID(a.currentChunkID); !isDecisionChunk(chunk) {
			title = fmt.Sprintf(" Decision Config %s (task node)%s ", chunkDisplayLabel(a.model.Plan, a.currentChunkID), dirtySuffix)
		}
		a.decisionEditor.SetTitle(title)
	}
	if a.graphView != nil {
		a.graphView.SetTitle(fmt.Sprintf(" Chunk Graph %s ", orDash(chunkDisplayLabel(a.model.Plan, a.model.SelectedChunkID))))
	}
	if a.layout != nil {
		a.layout.SetTitle(fmt.Sprintf(" Workbench %s%s ", a.model.ResolvedPath, dirtySuffix))
	}
	if a.statusBar != nil {
		a.refreshStatusBar()
	}
}

func (a *App) setChunkEditorText(text string) {
	if a == nil || a.chunkEditor == nil {
		return
	}
	a.suspendEditorSync = true
	a.chunkEditor.SetText("", false)
	a.chunkEditor.SetText(text, false)
	a.chunkEditor.Select(0, 0)
	a.chunkEditor.SetOffset(0, 0)
	a.suspendEditorSync = false
}

func (a *App) setDecisionEditorText(text string) {
	if a == nil || a.decisionEditor == nil {
		return
	}
	a.suspendDecisionSync = true
	a.decisionEditor.SetText("", false)
	a.decisionEditor.SetText(text, false)
	a.decisionEditor.Select(0, 0)
	a.decisionEditor.SetOffset(0, 0)
	a.suspendDecisionSync = false
}

func (a *App) appendLog(format string, args ...any) {
	a.appendLogWithColor("", format, args...)
}

func (a *App) appendError(format string, args ...any) {
	a.appendLogWithColor("red", format, args...)
}

func (a *App) appendLogWithColor(color string, format string, args ...any) {
	line := tview.Escape(fmt.Sprintf(format, args...))
	if color != "" {
		line = "[" + color + "]" + line + "[-]"
	}
	fmt.Fprintln(a.logView, colorizeWorkbenchLogLine(line))
	a.logView.ScrollToEnd()
}

func colorizeWorkbenchLogLine(line string) string {
	type rule struct {
		prefix string
	}
	rules := []rule{
		{prefix: "workbench opened:"},
		{prefix: "commands:"},
		{prefix: "chunk_id:"},
		{prefix: "intent_id:"},
		{prefix: "intent_uid:"},
	}
	trimmed := strings.TrimLeft(line, " \t")
	leading := line[:len(line)-len(trimmed)]
	for _, r := range rules {
		if strings.HasPrefix(trimmed, r.prefix) {
			return leading + "[yellow]" + r.prefix + "[-]" + strings.TrimPrefix(trimmed, r.prefix)
		}
	}
	return line
}

func (a *App) preparePlanForBridge(action string) error {
	if err := a.syncPlanTopology(action); err != nil {
		return fmt.Errorf("%s failed: %w", action, err)
	}
	if err := a.model.ValidateAndNormalize(); err != nil {
		return fmt.Errorf("%s failed: %w", action, err)
	}
	if err := a.model.Save(a.ctx, a.backend); err != nil {
		return fmt.Errorf("%s failed: %w", action, err)
	}
	a.refreshGraphView()
	a.refreshTitles()
	a.refreshValidationState()
	return nil
}

func (a *App) reconcileTopologyAfterEdit(action string) error {
	if a == nil || a.model == nil || a.model.Plan == nil {
		return fmt.Errorf("workbench model is not initialized")
	}
	snapshot, err := a.model.snapshotPlan()
	if err != nil {
		return err
	}
	selectedChunkID := a.model.SelectedChunkID
	dirty := a.model.Dirty
	if err := a.syncPlanTopology(action); err != nil {
		a.model.Plan = snapshot
		a.model.SelectedChunkID = selectedChunkID
		a.model.Dirty = dirty
		a.refreshValidationState()
		return err
	}
	a.refreshValidationState()
	return nil
}

func (a *App) syncPlanTopology(action string) error {
	if strings.EqualFold(strings.TrimSpace(action), "save") {
		if err := a.syncAllHandoffsFromBodyForSave(); err != nil {
			return fmt.Errorf("%s failed: %w", action, err)
		}
	}
	if err := a.applyCurrentChunkPortForm(); err != nil {
		return fmt.Errorf("%s failed: %w", action, err)
	}
	if err := a.applyCurrentDecisionForm(); err != nil {
		return fmt.Errorf("%s failed: %w", action, err)
	}
	if err := a.applyCurrentHandoffForm(); err != nil {
		return fmt.Errorf("%s failed: %w", action, err)
	}
	renames, err := a.model.NormalizeChunkPorts()
	if err != nil {
		return fmt.Errorf("%s failed: %w", action, err)
	}
	for _, rename := range renames {
		oldPort := strings.TrimSpace(rename.OldPort)
		if oldPort == "" {
			oldPort = "(missing)"
		}
		a.appendLog("normalized chunk_port for %s: %s -> %s", rename.ChunkID, oldPort, rename.NewPort)
	}
	if err := a.model.SyncEdgesFromHandoffs(); err != nil {
		return fmt.Errorf("%s failed: %w", action, err)
	}
	return nil
}

func (a *App) addChunk() error {
	return a.addChunkOfKind("task")
}

func (a *App) addDecisionChunk() error {
	return a.addChunkOfKind("decision")
}

func (a *App) addChunkOfKind(kind string) error {
	usedBefore := collectWorkspaceChunkIDs(a.ctx, a.backend, a.model)
	parentChunkID := a.selectedGraphChunkID()
	chunkID, row, err := a.model.AddChildChunk(parentChunkID)
	if err != nil {
		return err
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		kind = "task"
	}
	if kind != "task" {
		if err := a.model.SetChunkKind(chunkID, kind); err != nil {
			return err
		}
	}
	if _, exists := usedBefore[chunkID]; exists {
		newID := nextChunkIDFromUsed(usedBefore)
		renameChunkIDInPlan(a.model.Plan, chunkID, newID)
		chunkID = newID
	}
	if err := a.reconcileTopologyAfterEdit("add chunk"); err != nil {
		return err
	}
	if err := a.model.ValidateAndNormalize(); err != nil {
		return err
	}
	if err := a.model.Save(a.ctx, a.backend); err != nil {
		return err
	}
	a.currentChunkID = ""
	a.refreshGraphView()
	a.loadChunk(chunkID)
	a.setFocus(a.graphView)
	label := "chunk"
	if kind == "decision" {
		label = "decision"
	}
	if row != nil {
		a.appendLog("added %s chunk_id: %s linked from %s via %s", label, chunkID, row.FromStep, row.Port)
	} else {
		a.appendLog("added %s chunk_id: %s", label, chunkID)
	}
	a.refreshValidationState()
	return nil
}

func (a *App) cycleSelectedChunkKind() error {
	id := a.selectedGraphChunkID()
	if id == "" {
		return fmt.Errorf("select a chunk node in the graph first")
	}
	chunk := a.model.ChunkByID(id)
	if chunk == nil {
		return fmt.Errorf("unknown chunk %q", id)
	}
	current := strings.ToLower(strings.TrimSpace(chunk.Kind))
	next := "task"
	switch current {
	case "task", "satis", "":
		next = "decision"
	case "decision":
		next = "task"
	}
	if err := a.applyCurrentDecisionForm(); err != nil {
		return err
	}
	if err := a.model.SetChunkKind(id, next); err != nil {
		return err
	}
	if err := a.reconcileTopologyAfterEdit("change chunk kind"); err != nil {
		return err
	}
	a.currentChunkID = ""
	a.refreshGraphView()
	a.loadChunk(id)
	a.refreshValidationState()
	a.appendLog("changed chunk %s kind to %s", id, next)
	return nil
}

func (a *App) attachFragmentFromFile(hostPath string) error {
	data, err := os.ReadFile(hostPath)
	if err != nil {
		return err
	}
	var fragment PlanFragment
	if err := json.Unmarshal(data, &fragment); err != nil {
		return err
	}
	if err := a.model.AttachPlanFragment(fragment); err != nil {
		return err
	}
	if err := a.model.ValidateAndNormalize(); err != nil {
		return err
	}
	a.refreshGraphView()
	a.refreshValidationState()
	a.appendLog("attached fragment from %s with %d chunks", hostPath, len(fragment.Chunks))
	return nil
}

func (a *App) planContinueToJSON(rawPath string) error {
	if a.model == nil || a.model.Plan == nil {
		return fmt.Errorf("workbench model is not initialized")
	}
	targetPath, err := resolveWorkbenchPlanPath(a.model, rawPath)
	if err != nil {
		return err
	}
	plan, _, err := loadOrCreateContinuationPlan(a.ctx, a.backend, a.model, targetPath, true)
	if err != nil {
		return err
	}
	if err := persistPlanDocumentLenient(a.ctx, a.backend, targetPath, plan); err != nil {
		return err
	}
	if err := linkPlanChain(a.ctx, a.backend, a.model.ResolvedPath, targetPath); err != nil {
		return err
	}
	a.refreshPlanChainView()
	a.refreshPlanningPanel()
	a.appendLog("registered next plan: %s", targetPath)
	return nil
}

func (a *App) planChangeToJSON(rawPath string) error {
	if a.model == nil || a.model.Plan == nil {
		return fmt.Errorf("workbench model is not initialized")
	}
	return a.planContinueToJSON(rawPath)
}

func (a *App) planDetachSelectedNextPlan() error {
	if a.model == nil || a.model.Plan == nil {
		return fmt.Errorf("workbench model is not initialized")
	}
	targetPath, err := detachPlanChain(a.ctx, a.backend, a.model.ResolvedPath)
	if err != nil {
		return err
	}
	a.refreshPlanChainView()
	a.refreshPlanningPanel()
	if targetPath == "" {
		a.appendLog("no next plan registered")
		return nil
	}
	a.appendLog("detached next plan: %s", targetPath)
	return nil
}

func (a *App) continueRunWithPrompt(prompt string) error {
	if strings.TrimSpace(a.lastRunID) == "" {
		return fmt.Errorf("no run yet; use run first")
	}
	status, err := a.backend.ContinuePlanRunLLM(a.ctx, a.lastRunID, prompt)
	if err != nil {
		return err
	}
	inspect, err := a.backend.InspectPlanRun(a.ctx, a.lastRunID)
	if err == nil {
		a.lastSubmittedPlanID = inspect.Run.PlanID
	}
	a.appendLog("llm continuation requested for %s status=%s", a.lastRunID, status.Status)
	a.refreshPlanningPanel()
	return nil
}

func (a *App) finishPlanningRun() error {
	if strings.TrimSpace(a.lastRunID) == "" {
		return fmt.Errorf("no run yet; use run first")
	}
	status, err := a.backend.FinishPlanRun(a.ctx, a.lastRunID)
	if err != nil {
		return err
	}
	a.appendLog("run %s marked %s", a.lastRunID, status.Status)
	a.refreshPlanningPanel()
	return nil
}

func (a *App) duplicateCurrentChunk() error {
	sourceID := strings.TrimSpace(a.currentChunkID)
	if sourceID == "" {
		return fmt.Errorf("no selected chunk")
	}
	usedBefore := collectWorkspaceChunkIDs(a.ctx, a.backend, a.model)
	newID, err := a.model.DuplicateChunk(sourceID)
	if err != nil {
		return err
	}
	if _, exists := usedBefore[newID]; exists {
		renamedID := nextChunkIDFromUsed(usedBefore)
		renameChunkIDInPlan(a.model.Plan, newID, renamedID)
		newID = renamedID
	}
	if err := a.model.ValidateAndNormalize(); err != nil {
		return err
	}
	a.currentChunkID = ""
	a.refreshGraphView()
	a.loadChunk(newID)
	a.setFocus(a.graphView)
	a.refreshValidationState()
	a.appendLog("duplicated chunk %s to %s", sourceID, newID)
	return nil
}

func (a *App) syncCurrentHandoffsFromBody() error {
	if strings.TrimSpace(a.currentChunkID) == "" {
		return fmt.Errorf("no selected chunk")
	}
	rows, err := a.model.SyncHandoffsFromBody(a.currentChunkID)
	if err != nil {
		return err
	}
	a.refreshHandoffTable()
	a.refreshValidationState()
	a.appendLog("synced %d handoff rows from chunk body", len(rows))
	return nil
}

func (a *App) syncCurrentBodyFromHandoffs() error {
	if strings.TrimSpace(a.currentChunkID) == "" {
		return fmt.Errorf("no selected chunk")
	}
	if err := a.applyCurrentHandoffForm(); err != nil {
		return err
	}
	if err := a.model.SyncBodyPreludeFromHandoffs(a.currentChunkID); err != nil {
		return err
	}
	chunk := a.model.ChunkByID(a.currentChunkID)
	if chunk == nil {
		return fmt.Errorf("unknown chunk %q", a.currentChunkID)
	}
	a.suspendEditorSync = true
	a.setChunkEditorText(chunk.Source.SatisText)
	a.suspendEditorSync = false
	a.refreshValidationState()
	a.appendLog("synced chunk body input prelude from handoff rows")
	return nil
}

func (a *App) syncAllHandoffsFromBodyForSave() error {
	if a.model == nil || a.model.Plan == nil {
		return fmt.Errorf("workbench model is not initialized")
	}
	for _, chunk := range a.model.Plan.Chunks {
		kind := strings.ToLower(strings.TrimSpace(chunk.Kind))
		if kind == "decision" {
			// Decision nodes are control-flow nodes; their body is not
			// treated as task handoff source for save-time body parsing.
			continue
		}
		if _, err := a.model.SyncHandoffsFromBody(chunk.ChunkID); err != nil {
			a.appendError("save handoff parse failed on %s: %v", chunkDisplayLabel(a.model.Plan, chunk.ChunkID), err)
			return err
		}
	}
	a.refreshHandoffTable()
	a.refreshValidationState()
	return nil
}

func (a *App) selectedGraphChunkID() string {
	return strings.TrimSpace(a.model.SelectedChunkID)
}

func (a *App) removeSelectedChunk() error {
	if err := a.applyCurrentHandoffForm(); err != nil {
		return err
	}
	id := a.selectedGraphChunkID()
	if id == "" {
		return fmt.Errorf("select a chunk node in the graph first")
	}
	if a.model.ChunkByID(id) == nil {
		return fmt.Errorf("unknown chunk %q", id)
	}
	if a.isGraphRootChunk(id) {
		return fmt.Errorf("cannot remove root chunk %q", id)
	}
	if !a.isGraphLeafChunk(id) {
		return fmt.Errorf("cannot remove chunk %q: only leaf chunks can be removed", id)
	}
	if err := a.model.RemoveChunk(id); err != nil {
		return err
	}
	if err := a.model.ValidateAndNormalize(); err != nil {
		restored, err2 := LoadModelLenient(a.ctx, a.backend, a.model.ResolvedPath)
		if err2 == nil {
			a.model = restored
			a.currentChunkID = ""
			a.currentHandoffKey = ""
			a.refreshGraphView()
			a.loadChunk(a.model.SelectedChunkID)
		}
		return fmt.Errorf("remove chunk: invalid plan after delete: %w", err)
	}
	if err := a.model.Save(a.ctx, a.backend); err != nil {
		return err
	}
	removed := id
	a.currentHandoffKey = ""
	a.currentChunkID = ""
	next := a.preferredLeafAfterDelete(removed)
	if next == "" || a.model.ChunkByID(next) == nil {
		next = strings.TrimSpace(a.model.SelectedChunkID)
	}
	if next == "" || a.model.ChunkByID(next) == nil {
		next = defaultSelectedChunkID(a.model.Plan)
	}
	a.model.SelectedChunkID = next
	a.refreshGraphView()
	a.loadChunk(next)
	a.setFocus(a.graphView)
	a.appendLog("removed chunk_id: %s", removed)
	a.refreshValidationState()
	return nil
}

func (a *App) isGraphRootChunk(chunkID string) bool {
	chunkID = strings.TrimSpace(chunkID)
	if chunkID == "" || a.model == nil || a.model.Plan == nil {
		return false
	}
	if containsString(a.model.Plan.EntryChunks, chunkID) {
		return true
	}
	return len(a.graphParents[chunkID]) == 0
}

func (a *App) isGraphLeafChunk(chunkID string) bool {
	chunkID = strings.TrimSpace(chunkID)
	if chunkID == "" || a.model == nil || a.model.Plan == nil {
		return false
	}
	return len(a.graphChildren[chunkID]) == 0
}

func (a *App) preferredLeafAfterDelete(removedChunkID string) string {
	if a.model == nil || a.model.Plan == nil {
		return ""
	}
	parents, children := buildGraphAdjacency(a.model.Plan)
	_, layerOf := computeGraphLayers(a.model.Plan, parents, children)
	leafIDs := make([]string, 0, len(a.model.Plan.Chunks))
	for _, chunk := range a.model.Plan.Chunks {
		id := chunk.ChunkID
		if id == removedChunkID {
			continue
		}
		if len(children[id]) == 0 {
			leafIDs = append(leafIDs, id)
		}
	}
	if len(leafIDs) == 0 {
		return ""
	}
	sort.Slice(leafIDs, func(i, j int) bool {
		left := leafIDs[i]
		right := leafIDs[j]
		if layerOf[left] != layerOf[right] {
			return layerOf[left] > layerOf[right]
		}
		return left < right
	})
	return leafIDs[0]
}

func (a *App) runSelectedChunkPlan() error {
	if strings.TrimSpace(a.currentChunkID) == "" {
		return fmt.Errorf("no selected chunk")
	}
	if err := a.syncPlanTopology("runselected"); err != nil {
		return err
	}
	if err := a.model.ValidateAndNormalize(); err != nil {
		return err
	}
	plan, chunkIDs, err := a.model.BuildFocusedRunPlan(a.currentChunkID)
	if err != nil {
		return err
	}
	result, err := a.backend.SubmitPlan(a.ctx, plan)
	if err != nil {
		return fmt.Errorf("runselected submit failed: %w", err)
	}
	if !result.Accepted || result.NormalizedPlan == nil {
		return formatValidationIssues(result.ValidationErrors)
	}
	run, err := a.backend.StartPlanRun(a.ctx, result.NormalizedPlan.PlanID, bridge.ExecutionOptions{})
	if err != nil {
		return fmt.Errorf("runselected start failed: %w", err)
	}
	a.lastSubmittedPlanID = result.NormalizedPlan.PlanID
	a.lastRunID = run.RunID
	a.eventCursorByRun[run.RunID] = 0
	if !isTerminalRunPhase(run.Status) {
		_ = a.persistRunState()
	} else {
		_ = a.clearPersistedRunState()
	}
	a.appendLog("started focused run_id=%s chunks=%s", run.RunID, strings.Join(chunkIDs, ","))
	a.refreshStatusBar()
	a.refreshPlanningPanel()
	return nil
}

func (a *App) submitCurrentPlan() error {
	if err := a.preparePlanForBridge("submit"); err != nil {
		return err
	}
	result, err := a.backend.SubmitPlan(a.ctx, a.model.Plan)
	if err != nil {
		return fmt.Errorf("submit failed: %w", err)
	}
	if !result.Accepted || result.NormalizedPlan == nil {
		return formatValidationIssues(result.ValidationErrors)
	}
	a.model.Plan = result.NormalizedPlan
	a.lastSubmittedPlanID = result.NormalizedPlan.PlanID
	if strings.TrimSpace(a.lastRunID) != "" {
		_ = a.persistRunState()
	}
	a.refreshGraphView()
	a.appendLog("submitted plan_id=%s chunks=%d", a.lastSubmittedPlanID, len(a.model.Plan.Chunks))
	return nil
}

func (a *App) ensureSubmittedPlan() error {
	if a.model.Dirty || a.lastSubmittedPlanID == "" || a.lastSubmittedPlanID != a.model.Plan.PlanID {
		return a.submitCurrentPlan()
	}
	return nil
}

func (a *App) inspectLatestRun() (bridge.InspectRunResult, error) {
	if strings.TrimSpace(a.lastRunID) == "" {
		return bridge.InspectRunResult{}, fmt.Errorf("no run yet; use run or start first")
	}
	return a.backend.InspectPlanRun(a.ctx, a.lastRunID)
}

func (a *App) formatRunSummary(summary bridge.InspectRunSummary) string {
	parts := []string{
		fmt.Sprintf("plan_id=%s", summary.PlanID),
		fmt.Sprintf("graph_revision=%d", summary.GraphRevision),
		fmt.Sprintf("continuations=%d", summary.ContinuationCount),
		fmt.Sprintf("total=%d", summary.TotalChunks),
	}
	if summary.PlanningPending {
		parts = append(parts, "planning_pending=true")
	}
	if len(summary.SucceededChunkIDs) > 0 {
		parts = append(parts, fmt.Sprintf("succeeded=%d", len(summary.SucceededChunkIDs)))
	}
	if len(summary.FailedChunkIDs) > 0 {
		parts = append(parts, fmt.Sprintf("failed=%d", len(summary.FailedChunkIDs)))
	}
	if len(summary.RunningChunkIDs) > 0 {
		parts = append(parts, fmt.Sprintf("running=%d", len(summary.RunningChunkIDs)))
	}
	if len(summary.PendingChunkIDs) > 0 {
		parts = append(parts, fmt.Sprintf("pending=%d", len(summary.PendingChunkIDs)))
	}
	if len(summary.BlockedChunkIDs) > 0 {
		parts = append(parts, fmt.Sprintf("blocked=%d", len(summary.BlockedChunkIDs)))
	}
	if len(summary.CancelledChunkIDs) > 0 {
		parts = append(parts, fmt.Sprintf("cancelled=%d", len(summary.CancelledChunkIDs)))
	}
	if summary.ArtifactTotal > 0 {
		parts = append(parts, fmt.Sprintf("artifacts=%d", summary.ArtifactTotal))
	}
	if summary.PrimaryFailureChunkID != "" {
		parts = append(parts, fmt.Sprintf("primary_failure=%s", summary.PrimaryFailureChunkID))
	}
	if summary.LatestPlanningSummary != "" {
		parts = append(parts, fmt.Sprintf("latest_planning=%s", summary.LatestPlanningSummary))
	}
	return strings.Join(parts, " ")
}

func (a *App) appendChunkStatusLines(statuses map[string]bridge.ChunkPhase) {
	if len(statuses) == 0 {
		return
	}
	chunkIDs := make([]string, 0, len(statuses))
	for chunkID := range statuses {
		chunkIDs = append(chunkIDs, chunkID)
	}
	sort.Strings(chunkIDs)
	for _, chunkID := range chunkIDs {
		a.appendLog("chunk %s: %s", chunkID, statuses[chunkID])
	}
}

func (a *App) appendNewEvents(runID string, result bridge.StreamRunEventsResult) {
	cursor := a.eventCursorByRun[runID]
	if cursor > len(result.Events) {
		cursor = 0
	}
	if cursor == len(result.Events) {
		a.appendLog("no new events for run_id=%s", runID)
		return
	}
	for _, event := range result.Events[cursor:] {
		chunkLabel := ""
		if event.ChunkID != nil && *event.ChunkID != "" {
			chunkLabel = " chunk=" + *event.ChunkID
		}
		a.appendLog("event %s %s%s", event.EventID, event.Type, chunkLabel)
	}
	a.eventCursorByRun[runID] = len(result.Events)
}

func (a *App) runCommand(raw string) {
	command := strings.TrimSpace(strings.TrimPrefix(raw, "/"))
	if command == "" {
		return
	}
	a.appendLog("> %s", raw)
	fields := strings.Fields(command)
	name := strings.ToLower(fields[0])
	switch name {
	case "p":
		name = "palette"
	case "r":
		name = "render"
	case "v":
		name = "validate"
	case "s":
		name = "save"
	case "dup":
		name = "duplicate"
	case "rs":
		name = "runselected"
	case "sh":
		name = "synchandoff"
	case "sb":
		name = "syncbody"
	}
	switch name {
	case "help":
		a.appendLog("render | validate | save | submit | start | run | runselected | status | inspect | events | chunk <id> | reload | quit | quit! | focus left|chunk|decision|cli")
		a.appendLog("syncbody | synchandoff | duplicate | adddecision | branch <name> <target_chunk_id> | decisiondefault <branch> | decisionmode <human|llm|llm_then_human>")
		a.appendLog("plan-description [text] | intent-description")
		a.appendLog("plan-continue <json_file> | plan-change <json_file> | plan-detach | plan-prev | plan-next | plan-draft <prompt> | plan-finish")
		a.appendLog("decisionprompt <template> | decisiontitle <title> | attachfragment <file> | palette")
		a.appendLog("aliases: r v s dup rs sh sb p")
		a.appendLog("shortcuts: Option+1..4 focus panels (macOS); Ctrl+Shift+R render; Ctrl+P palette; Ctrl+N add task chunk; Shift+D add decision chunk; Ctrl+T toggle task/decision; Ctrl+K remove chunk; handoff rows sync from chunk body on save")
	case "palette":
		a.openPalette()
	case "adddecision":
		if err := a.addDecisionChunk(); err != nil {
			a.appendError("adddecision failed: %v", err)
			return
		}
	case "branch":
		if len(fields) != 3 {
			a.appendError("usage: branch <name> <target_chunk_id>")
			return
		}
		sourceID := a.selectedGraphChunkID()
		if sourceID == "" {
			a.appendError("branch failed: select a decision node in the graph first")
			return
		}
		if err := a.addDecisionBranch(sourceID, fields[1], fields[2]); err != nil {
			a.appendError("branch failed: %v", err)
			return
		}
	case "decisiondefault":
		if len(fields) != 2 {
			a.appendError("usage: decisiondefault <branch>")
			return
		}
		if err := a.updateDecisionDefault(fields[1]); err != nil {
			a.appendError("decisiondefault failed: %v", err)
			return
		}
	case "decisionmode":
		if len(fields) != 2 {
			a.appendError("usage: decisionmode <human|llm|llm_then_human>")
			return
		}
		if err := a.updateDecisionMode(fields[1]); err != nil {
			a.appendError("decisionmode failed: %v", err)
			return
		}
	case "decisionprompt":
		prompt := strings.TrimSpace(strings.TrimPrefix(command, fields[0]))
		if prompt == "" {
			a.appendError("usage: decisionprompt <template>")
			return
		}
		if err := a.updateDecisionPrompt(prompt); err != nil {
			a.appendError("decisionprompt failed: %v", err)
			return
		}
	case "decisiontitle":
		title := strings.TrimSpace(strings.TrimPrefix(command, fields[0]))
		if title == "" {
			a.appendError("usage: decisiontitle <title>")
			return
		}
		if err := a.updateDecisionTitle(title); err != nil {
			a.appendError("decisiontitle failed: %v", err)
			return
		}
	case "plan-description":
		if len(fields) == 1 {
			a.appendLog("plan_description: %s", a.model.Plan.PlanDescription)
			return
		}
		description := strings.TrimSpace(strings.TrimPrefix(command, fields[0]))
		if description == "" {
			a.appendError("usage: plan-description <plan_desc>")
			return
		}
		if err := a.updateAndPersistPlanDescription(description); err != nil {
			a.appendError("plan-description failed: %v", err)
			return
		}
		a.appendLog("updated plan_description")
	case "intent-description":
		if len(fields) != 1 {
			a.appendError("usage: intent-description")
			return
		}
		a.appendLog("intent_description: %s", a.model.Plan.IntentDescription)
	case "attachfragment":
		if len(fields) != 2 {
			a.appendError("usage: attachfragment <host_json_file>")
			return
		}
		if err := a.attachFragmentFromFile(fields[1]); err != nil {
			a.appendError("attachfragment failed: %v", err)
			return
		}
	case "plan-continue":
		if len(fields) != 2 {
			a.appendError("usage: plan-continue <json_file>")
			return
		}
		if err := a.planContinueToJSON(fields[1]); err != nil {
			a.appendError("plan-continue failed: %v", err)
			return
		}
	case "plan-change":
		if len(fields) != 2 {
			a.appendError("usage: plan-change <json_file>")
			return
		}
		if err := a.planChangeToJSON(fields[1]); err != nil {
			a.appendError("plan-change failed: %v", err)
			return
		}
	case "plan-detach":
		if len(fields) != 1 {
			a.appendError("usage: plan-detach")
			return
		}
		if err := a.planDetachSelectedNextPlan(); err != nil {
			a.appendError("plan-detach failed: %v", err)
			return
		}
	case "plan-prev":
		if len(fields) != 1 {
			a.appendError("usage: plan-prev")
			return
		}
		if err := a.openPrevPlan(); err != nil {
			a.appendError("plan-prev failed: %v", err)
			return
		}
	case "plan-next":
		if len(fields) != 1 {
			a.appendError("usage: plan-next")
			return
		}
		if err := a.openNextPlan(); err != nil {
			a.appendError("plan-next failed: %v", err)
			return
		}
	case "plan-draft":
		if len(fields) < 2 {
			a.appendError("usage: plan-draft <prompt>")
			return
		}
		if err := a.continueRunWithPrompt(strings.TrimSpace(raw[len(fields[0]):])); err != nil {
			a.appendError("plan-draft failed: %v", err)
			return
		}
	case "plan-finish":
		if err := a.finishPlanningRun(); err != nil {
			a.appendError("plan-finish failed: %v", err)
			return
		}
	case "render":
		if err := a.syncPlanTopology("render"); err != nil {
			a.appendError("%v", err)
			return
		}
		if err := a.model.ValidateAndNormalize(); err != nil {
			a.appendError("render failed: %v", err)
			return
		}
		if err := a.model.Save(a.ctx, a.backend); err != nil {
			a.appendError("render failed: %v", err)
			return
		}
		a.refreshGraphView()
		a.refreshHandoffTable()
		a.refreshValidationState()
		a.appendLog("rendered DAG for %s", a.model.ResolvedPath)
	case "validate":
		if err := a.syncPlanTopology("validate"); err != nil {
			a.appendError("%v", err)
			return
		}
		if err := a.model.ValidateAndNormalize(); err != nil {
			a.appendError("validate failed: %v", err)
			a.refreshValidationState()
			return
		}
		a.refreshValidationState()
		a.appendLog("plan is valid")
	case "save":
		if err := a.syncPlanTopology("save"); err != nil {
			a.appendError("%v", err)
			return
		}
		if err := a.model.Save(a.ctx, a.backend); err != nil {
			a.appendError("save failed: %v", err)
			return
		}
		a.refreshGraphView()
		a.refreshTitles()
		a.refreshValidationState()
		a.appendLog("saved %s", a.model.ResolvedPath)
	case "duplicate":
		if err := a.duplicateCurrentChunk(); err != nil {
			a.appendError("%v", err)
		}
	case "synchandoff":
		if err := a.syncCurrentHandoffsFromBody(); err != nil {
			a.appendError("%v", err)
		}
	case "syncbody":
		if err := a.syncCurrentBodyFromHandoffs(); err != nil {
			a.appendError("%v", err)
		}
	case "submit":
		if err := a.submitCurrentPlan(); err != nil {
			a.appendError("%v", err)
		}
	case "start":
		if err := a.ensureSubmittedPlan(); err != nil {
			a.appendError("%v", err)
			return
		}
		run, err := a.backend.StartPlanRun(a.ctx, a.lastSubmittedPlanID, bridge.ExecutionOptions{})
		if err != nil {
			a.appendError("start failed: %v", err)
			return
		}
		a.lastRunID = run.RunID
		a.eventCursorByRun[run.RunID] = 0
		if !isTerminalRunPhase(run.Status) {
			_ = a.persistRunState()
		} else {
			_ = a.clearPersistedRunState()
		}
		a.appendLog("started run_id=%s plan_id=%s status=%s", run.RunID, run.PlanID, run.Status)
		a.refreshPlanningPanel()
	case "run":
		if err := a.submitCurrentPlan(); err != nil {
			a.appendError("%v", err)
			return
		}
		run, err := a.backend.StartPlanRun(a.ctx, a.lastSubmittedPlanID, bridge.ExecutionOptions{})
		if err != nil {
			a.appendError("run failed: %v", err)
			return
		}
		a.lastRunID = run.RunID
		a.eventCursorByRun[run.RunID] = 0
		if !isTerminalRunPhase(run.Status) {
			_ = a.persistRunState()
		} else {
			_ = a.clearPersistedRunState()
		}
		a.appendLog("started run_id=%s plan_id=%s status=%s", run.RunID, run.PlanID, run.Status)
		a.refreshStatusBar()
		a.refreshPlanningPanel()
	case "runselected":
		if err := a.runSelectedChunkPlan(); err != nil {
			a.appendError("%v", err)
		}
	case "status":
		inspect, err := a.inspectLatestRun()
		if err != nil {
			a.appendError("status failed: %v", err)
			return
		}
		a.appendLog("run_id=%s status=%s updated=%s", inspect.Run.RunID, inspect.Run.Status, inspect.Run.UpdatedAt.Format("15:04:05"))
		if strings.TrimSpace(inspect.Run.PlanID) != "" {
			a.lastSubmittedPlanID = inspect.Run.PlanID
		}
		if isTerminalRunPhase(inspect.Run.Status) || inspect.Summary.Terminal {
			_ = a.clearPersistedRunState()
		} else {
			_ = a.persistRunState()
		}
		a.refreshPlanningPanel()
	case "inspect":
		inspect, err := a.inspectLatestRun()
		if err != nil {
			a.appendError("inspect failed: %v", err)
			return
		}
		a.appendLog("inspect run_id=%s status=%s", inspect.Run.RunID, inspect.Run.Status)
		a.appendLog("%s", a.formatRunSummary(inspect.Summary))
		a.appendChunkStatusLines(inspect.Run.ChunkStatuses)
		if strings.TrimSpace(inspect.Run.PlanID) != "" {
			a.lastSubmittedPlanID = inspect.Run.PlanID
		}
		if isTerminalRunPhase(inspect.Run.Status) || inspect.Summary.Terminal {
			_ = a.clearPersistedRunState()
		} else {
			_ = a.persistRunState()
		}
		a.refreshPlanningPanel()
	case "events":
		if strings.TrimSpace(a.lastRunID) == "" {
			a.appendError("events failed: no run yet; use run or start first")
			return
		}
		result, err := a.backend.StreamPlanRunEvents(a.ctx, a.lastRunID)
		if err != nil {
			a.appendError("events failed: %v", err)
			return
		}
		a.appendNewEvents(a.lastRunID, result)
		if result.Completed {
			a.appendLog("run_id=%s is terminal", a.lastRunID)
			_ = a.clearPersistedRunState()
		} else {
			_ = a.persistRunState()
		}
		a.refreshPlanningPanel()
	case "chunk":
		chunkID := a.currentChunkID
		if len(fields) > 2 {
			a.appendError("usage: chunk <chunk_id>")
			return
		}
		if len(fields) == 2 {
			chunkID = fields[1]
		}
		if strings.TrimSpace(chunkID) == "" {
			a.appendError("chunk failed: no selected chunk")
			return
		}
		if strings.TrimSpace(a.lastRunID) == "" {
			a.appendError("chunk failed: no run yet; use run or start first")
			return
		}
		result, err := a.backend.InspectRunChunk(a.ctx, a.lastRunID, chunkID)
		if err != nil {
			a.appendError("chunk failed: %v", err)
			return
		}
		a.appendLog("chunk %s status=%s", result.ChunkID, result.Status)
		if result.Error != nil {
			a.appendError("chunk %s error=%s", result.ChunkID, result.Error.Message)
		}
	case "reload":
		if a.model.Dirty {
			a.appendError("reload blocked: save changes first or use quit! to discard")
			return
		}
		model, err := LoadModelLenient(a.ctx, a.backend, a.model.ResolvedPath)
		if err != nil {
			a.appendError("reload failed: %v", err)
			return
		}
		a.model = model
		a.currentChunkID = ""
		a.refreshGraphView()
		a.loadChunk(a.model.SelectedChunkID)
		a.refreshValidationState()
		a.appendLog("reloaded %s", a.model.ResolvedPath)
		a.refreshPlanningPanel()
	case "focus":
		if len(fields) != 2 {
			a.appendError("usage: focus left|chunk|decision|cli")
			return
		}
		switch strings.ToLower(fields[1]) {
		case "left":
			a.setFocus(a.graphView)
		case "chunk":
			a.setFocus(a.chunkEditor)
		case "decision":
			a.setFocus(a.decisionEditor)
		case "cli":
			a.setFocus(a.commandInput)
		default:
			a.appendError("unknown focus target %q", fields[1])
			return
		}
	case "quit":
		if a.model.Dirty {
			a.appendError("unsaved changes; use save or quit! to discard")
			return
		}
		a.syncRunStateOnExit()
		a.application.Stop()
	case "quit!":
		a.syncRunStateOnExit()
		a.application.Stop()
	default:
		a.appendError("unknown command %q", fields[0])
	}
}

func (a *App) addDecisionBranch(sourceChunkID string, branch string, targetChunkID string) error {
	if err := a.applyCurrentDecisionForm(); err != nil {
		return err
	}
	if err := a.model.AddDecisionBranch(sourceChunkID, branch, targetChunkID); err != nil {
		return err
	}
	if err := a.reconcileTopologyAfterEdit("add decision branch"); err != nil {
		return err
	}
	if err := a.model.ValidateAndNormalize(); err != nil {
		return err
	}
	if err := a.model.Save(a.ctx, a.backend); err != nil {
		return err
	}
	a.currentChunkID = ""
	a.refreshGraphView()
	a.refreshValidationState()
	a.loadChunk(sourceChunkID)
	a.appendLog("added decision branch %s: %s -> %s", branch, sourceChunkID, targetChunkID)
	return nil
}

func (a *App) updateDecisionDefault(branch string) error {
	if err := a.applyCurrentDecisionForm(); err != nil {
		return err
	}
	chunkID := a.selectedGraphChunkID()
	if chunkID == "" {
		return fmt.Errorf("select a decision node in the graph first")
	}
	if err := a.model.SetDecisionDefaultBranch(chunkID, branch); err != nil {
		return err
	}
	return a.persistDecisionEdit(chunkID, "updated decision %s default branch to %s", chunkID, branch)
}

func (a *App) updateDecisionMode(mode string) error {
	if err := a.applyCurrentDecisionForm(); err != nil {
		return err
	}
	chunkID := a.selectedGraphChunkID()
	if chunkID == "" {
		return fmt.Errorf("select a decision node in the graph first")
	}
	if err := a.model.SetDecisionMode(chunkID, mode); err != nil {
		return err
	}
	return a.persistDecisionEdit(chunkID, "updated decision %s mode to %s", chunkID, mode)
}

func (a *App) updateDecisionPrompt(prompt string) error {
	if err := a.applyCurrentDecisionForm(); err != nil {
		return err
	}
	chunkID := a.selectedGraphChunkID()
	if chunkID == "" {
		return fmt.Errorf("select a decision node in the graph first")
	}
	if err := a.model.SetDecisionPromptTemplate(chunkID, prompt); err != nil {
		return err
	}
	return a.persistDecisionEdit(chunkID, "updated decision %s prompt template", chunkID)
}

func (a *App) updateDecisionTitle(title string) error {
	if err := a.applyCurrentDecisionForm(); err != nil {
		return err
	}
	chunkID := a.selectedGraphChunkID()
	if chunkID == "" {
		return fmt.Errorf("select a decision node in the graph first")
	}
	if err := a.model.SetDecisionHumanTitle(chunkID, title); err != nil {
		return err
	}
	return a.persistDecisionEdit(chunkID, "updated decision %s human title", chunkID)
}

func (a *App) persistDecisionEdit(chunkID string, format string, args ...any) error {
	if err := a.reconcileTopologyAfterEdit("edit decision"); err != nil {
		return err
	}
	if err := a.model.ValidateAndNormalize(); err != nil {
		return err
	}
	if err := a.model.Save(a.ctx, a.backend); err != nil {
		return err
	}
	a.currentChunkID = ""
	a.refreshGraphView()
	a.refreshValidationState()
	a.loadChunk(chunkID)
	a.appendLog(format, args...)
	return nil
}

func jsonMarshalIndent(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }

func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
