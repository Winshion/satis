package workbench

import (
	"strings"
)

// planExecutionCWDSetter is implemented by runtimes that execute chunk graphs (e.g. SessionAdapter).
type planExecutionCWDSetter interface {
	SetPlanExecutionCWD(resolvedPlanPath string)
}

// syncPlanExecutionCWD aligns Executor/bridge InitialCWD with the workbench plan directory so relative
// paths in SatisIL (e.g. write into notes.md) resolve next to plan.json, not VFS root.
func syncPlanExecutionCWD(backend Backend, resolvedPlanPath string) {
	if backend == nil || strings.TrimSpace(resolvedPlanPath) == "" {
		return
	}
	s, ok := backend.(planExecutionCWDSetter)
	if !ok {
		return
	}
	s.SetPlanExecutionCWD(resolvedPlanPath)
}
