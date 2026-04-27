package workbench

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"satis/bridge"
	"satis/satis"
)

func (a *App) promptHumanControlChoice(ctx context.Context, req bridge.HumanControlRequest) (bridge.HumanControlChoice, error) {
	if a == nil || a.application == nil || a.pages == nil {
		return bridge.HumanControlChoice{}, fmt.Errorf("workbench human interaction is not available")
	}

	type promptResult struct {
		choice bridge.HumanControlChoice
		err    error
	}
	resultCh := make(chan promptResult, 1)
	var once sync.Once
	resolve := func(choice bridge.HumanControlChoice, err error) {
		once.Do(func() {
			resultCh <- promptResult{choice: choice, err: err}
		})
	}

	var previousFocus tview.Primitive
	a.application.QueueUpdateDraw(func() {
		previousFocus = a.application.GetFocus()
		body := tview.NewTextView().
			SetDynamicColors(true).
			SetWrap(true)
		body.SetBorder(true).SetTitle(" Human Decision ")
		body.SetText(formatHumanControlPrompt(req))

		list := tview.NewList().ShowSecondaryText(false)
		list.SetBorder(true).SetTitle(" Branches ")
		allowed := append([]string(nil), req.AllowedBranches...)
		sort.Strings(allowed)
		for _, branch := range allowed {
			target := branch
			label := target
			if strings.TrimSpace(req.DefaultBranch) == target {
				label += " (default)"
			}
			list.AddItem(label, "", 0, func() {
				resolve(bridge.HumanControlChoice{Branch: target}, nil)
			})
		}
		list.SetDoneFunc(func() {
			defaultBranch := strings.TrimSpace(req.DefaultBranch)
			if defaultBranch != "" {
				resolve(bridge.HumanControlChoice{Branch: defaultBranch}, nil)
				return
			}
			resolve(bridge.HumanControlChoice{}, fmt.Errorf("human interaction cancelled"))
		})
		list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
			if event.Key() == tcell.KeyEsc {
				defaultBranch := strings.TrimSpace(req.DefaultBranch)
				if defaultBranch != "" {
					resolve(bridge.HumanControlChoice{Branch: defaultBranch}, nil)
				} else {
					resolve(bridge.HumanControlChoice{}, fmt.Errorf("human interaction cancelled"))
				}
				return nil
			}
			return event
		})

		content := tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(body, 0, 3, false).
			AddItem(list, 0, 2, true)
		content.SetBorder(true).SetTitle(" Human Decision Required ")
		modal := tview.NewFlex().
			AddItem(nil, 0, 1, false).
			AddItem(content, 84, 0, true).
			AddItem(nil, 0, 1, false)
		centered := tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(modal, 22, 0, true).
			AddItem(nil, 0, 1, false)
		a.pages.AddPage("human_prompt", centered, true, true)
		a.application.SetFocus(list)
		a.application.Sync()
	})

	var result promptResult
	select {
	case result = <-resultCh:
	case <-ctx.Done():
		result.err = ctx.Err()
	}

	a.application.QueueUpdateDraw(func() {
		a.pages.RemovePage("human_prompt")
		if previousFocus != nil {
			a.application.SetFocus(previousFocus)
		}
		a.applyFocusColors()
		a.application.Sync()
		if result.err == nil {
			a.appendLog("human decision selected for %s: %s", req.ChunkID, result.choice.Branch)
		}
	})
	return result.choice, result.err
}

func (a *App) promptHumanControlChoiceWhenReady(ctx context.Context, req bridge.HumanControlRequest) (bridge.HumanControlChoice, error) {
	if a == nil {
		return bridge.HumanControlChoice{}, fmt.Errorf("workbench human interaction is not available")
	}
	if a.uiReady != nil {
		select {
		case <-a.uiReady:
		case <-ctx.Done():
			return bridge.HumanControlChoice{}, ctx.Err()
		}
	}
	return a.promptHumanControlChoice(ctx, req)
}

func formatHumanControlPrompt(req bridge.HumanControlRequest) string {
	lines := []string{
		fmt.Sprintf("[yellow]chunk:[-] %s", tview.Escape(req.ChunkID)),
		fmt.Sprintf("[yellow]kind:[-] %s", tview.Escape(req.ControlKind)),
		fmt.Sprintf("[yellow]title:[-] %s", tview.Escape(orDash(req.Title))),
	}
	if desc := strings.TrimSpace(req.Description); desc != "" {
		lines = append(lines, fmt.Sprintf("[yellow]description:[-] %s", tview.Escape(desc)))
	}
	if def := strings.TrimSpace(req.DefaultBranch); def != "" {
		lines = append(lines, fmt.Sprintf("[yellow]default:[-] %s", tview.Escape(def)))
	}
	if len(req.InputBindings) > 0 {
		lines = append(lines, "", "[yellow]inputs:[-]")
		keys := make([]string, 0, len(req.InputBindings))
		for key := range req.InputBindings {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value := formatHumanBinding(req.InputBindings[key])
			if len(value) > 80 {
				value = value[:77] + "..."
			}
			lines = append(lines, fmt.Sprintf("- %s = %s", tview.Escape(key), tview.Escape(value)))
		}
	}
	lines = append(lines, "", "Use arrows + Enter to choose. Esc picks the default branch.")
	return strings.Join(lines, "\n")
}

func formatHumanBinding(binding satis.RuntimeBinding) string {
	switch binding.Kind {
	case "text":
		return binding.Text
	case "text_list":
		return strings.Join(binding.Texts, "\\n")
	case "object":
		if binding.Object != nil {
			return binding.Object.VirtualPath
		}
	case "object_list":
		parts := make([]string, 0, len(binding.Objects))
		for _, ref := range binding.Objects {
			parts = append(parts, ref.VirtualPath)
		}
		return strings.Join(parts, ", ")
	case "conversation":
		return fmt.Sprintf("conversation(%d turns)", len(binding.Conversation))
	}
	return binding.Kind
}
