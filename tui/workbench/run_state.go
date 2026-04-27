package workbench

import (
	"fmt"
	"path"
	"strings"

	"satis/bridge"
)

type persistedRunState struct {
	LastSubmittedPlanID string `json:"last_submitted_plan_id,omitempty"`
	LastRunID           string `json:"last_run_id,omitempty"`
}

func workbenchRunStatePath(planPath string) string {
	planPath = strings.TrimSpace(planPath)
	if planPath == "" {
		return ".workbench_run_state.json"
	}
	dir := path.Dir(planPath)
	base := path.Base(planPath)
	return path.Join(dir, "."+base+".run_state.json")
}

func isTerminalRunPhase(status bridge.RunPhase) bool {
	switch status {
	case bridge.RunPhaseCompleted, bridge.RunPhaseFailed, bridge.RunPhaseCancelled:
		return true
	default:
		return false
	}
}

func (a *App) restorePersistedRunState() error {
	if a == nil || a.backend == nil || a.model == nil {
		return nil
	}
	statePath := workbenchRunStatePath(a.model.ResolvedPath)
	text, err := a.backend.ReadVirtualText(a.ctx, statePath)
	if err != nil || strings.TrimSpace(text) == "" {
		return nil
	}
	var state persistedRunState
	if err := jsonUnmarshal([]byte(text), &state); err != nil {
		return fmt.Errorf("parse persisted run state: %w", err)
	}
	a.lastSubmittedPlanID = strings.TrimSpace(state.LastSubmittedPlanID)
	a.lastRunID = strings.TrimSpace(state.LastRunID)
	if a.lastRunID == "" {
		return nil
	}
	inspect, err := a.backend.InspectPlanRun(a.ctx, a.lastRunID)
	if err != nil {
		return nil
	}
	if strings.TrimSpace(inspect.Run.PlanID) != "" {
		a.lastSubmittedPlanID = inspect.Run.PlanID
	}
	if isTerminalRunPhase(inspect.Run.Status) || inspect.Summary.Terminal {
		a.lastRunID = ""
		a.lastSubmittedPlanID = ""
		return a.clearPersistedRunState()
	}
	return nil
}

func (a *App) persistRunState() error {
	if a == nil || a.backend == nil || a.model == nil {
		return nil
	}
	if strings.TrimSpace(a.lastRunID) == "" {
		return a.clearPersistedRunState()
	}
	state := persistedRunState{
		LastSubmittedPlanID: strings.TrimSpace(a.lastSubmittedPlanID),
		LastRunID:           strings.TrimSpace(a.lastRunID),
	}
	data, err := jsonMarshalIndent(state)
	if err != nil {
		return err
	}
	return a.backend.WriteVirtualText(a.ctx, workbenchRunStatePath(a.model.ResolvedPath), string(data))
}

func (a *App) clearPersistedRunState() error {
	if a == nil || a.backend == nil || a.model == nil {
		return nil
	}
	return a.backend.WriteVirtualText(a.ctx, workbenchRunStatePath(a.model.ResolvedPath), "")
}

func (a *App) syncRunStateOnExit() {
	if a == nil || strings.TrimSpace(a.lastRunID) == "" {
		return
	}
	inspect, err := a.backend.InspectPlanRun(a.ctx, a.lastRunID)
	if err != nil {
		return
	}
	if isTerminalRunPhase(inspect.Run.Status) || inspect.Summary.Terminal {
		_ = a.clearPersistedRunState()
		return
	}
	if strings.TrimSpace(inspect.Run.PlanID) != "" {
		a.lastSubmittedPlanID = inspect.Run.PlanID
	}
	_ = a.persistRunState()
}
