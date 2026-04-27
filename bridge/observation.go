package bridge

import (
	"fmt"
	"sort"
)

func buildInspectRunSummary(record *runRecord) InspectRunSummary {
	if record == nil {
		return InspectRunSummary{}
	}
	plan := record.plan
	if plan == nil {
		plan = &ChunkGraphPlan{}
	}
	st := record.status
	chunks := record.chunks
	s := InspectRunSummary{
		PlanID:                   plan.PlanID,
		GraphRevision:            record.graphRevision,
		PlanningPending:          st.Status == RunPhasePlanningPending,
		ContinuationCount:        record.continuationCount,
		TotalChunks:              len(plan.Chunks),
		Terminal:                 isTerminalRunPhase(st.Status),
		ArtifactTotal:            len(record.artifacts),
		InheritedVariableSummary: summarizeInheritedVariables(record.workflowRegistry),
	}
	if n := len(record.planningHistory); n > 0 {
		s.LatestPlanningSummary = record.planningHistory[n-1].Summary
	}
	for _, chunk := range plan.Chunks {
		switch normalizedChunkKind(chunk.Kind) {
		case "decision":
			s.DecisionChunkIDs = append(s.DecisionChunkIDs, chunk.ChunkID)
		default:
			s.TaskChunkIDs = append(s.TaskChunkIDs, chunk.ChunkID)
		}
	}
	if st.Error != nil {
		s.RunErrorCode = st.Error.Code
		s.RunErrorMessage = st.Error.Message
		s.PrimaryFailureChunkID = chunkIDFromDetails(st.Error.Details)
	}

	for id, phase := range st.ChunkStatuses {
		switch phase {
		case ChunkPhaseFailed:
			s.FailedChunkIDs = append(s.FailedChunkIDs, id)
		case ChunkPhaseBlocked:
			s.BlockedChunkIDs = append(s.BlockedChunkIDs, id)
		case ChunkPhaseSucceeded:
			s.SucceededChunkIDs = append(s.SucceededChunkIDs, id)
		case ChunkPhaseRunning:
			if chunk, ok := chunks[id]; ok && chunk.Substatus == "waiting_human" {
				s.WaitingHumanIDs = append(s.WaitingHumanIDs, id)
			} else {
				s.RunningChunkIDs = append(s.RunningChunkIDs, id)
			}
		case ChunkPhaseReady:
			s.ReadyChunkIDs = append(s.ReadyChunkIDs, id)
		case ChunkPhasePending:
			s.PendingChunkIDs = append(s.PendingChunkIDs, id)
		case ChunkPhaseCancelled:
			s.CancelledChunkIDs = append(s.CancelledChunkIDs, id)
		}
	}
	sort.Strings(s.FailedChunkIDs)
	sort.Strings(s.BlockedChunkIDs)
	sort.Strings(s.SucceededChunkIDs)
	sort.Strings(s.RunningChunkIDs)
	sort.Strings(s.WaitingHumanIDs)
	sort.Strings(s.ReadyChunkIDs)
	sort.Strings(s.PendingChunkIDs)
	sort.Strings(s.CancelledChunkIDs)
	sort.Strings(s.TaskChunkIDs)
	sort.Strings(s.DecisionChunkIDs)
	primaryErr := primaryFailureError(s.PrimaryFailureChunkID, chunks, st.Error)
	if primaryErr != nil {
		s.PrimaryFailureCode = primaryErr.Code
		s.PrimaryFailureStage = string(primaryErr.Stage)
		s.FailureLayer = FailureLayerForStructuredError(primaryErr)
	}
	return s
}

func summarizeInheritedVariables(registry map[string]WorkflowBindingSnapshot) []string {
	if len(registry) == 0 {
		return nil
	}
	keys := make([]string, 0, len(registry))
	for key := range registry {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		snapshot := registry[key]
		out = append(out, fmt.Sprintf("%s:%s@v%d", snapshot.Name, snapshot.Kind, snapshot.Version))
	}
	return out
}

func primaryFailureError(
	chunkID string,
	chunks map[string]ChunkExecutionResult,
	runErr *StructuredError,
) *StructuredError {
	if chunkID != "" && chunks != nil {
		if result, ok := chunks[chunkID]; ok && result.Error != nil {
			return result.Error
		}
	}
	return runErr
}

func chunkIDFromDetails(details map[string]any) string {
	if details == nil {
		return ""
	}
	if v, ok := details["chunk_id"]; ok && v != nil {
		switch t := v.(type) {
		case string:
			if t != "" {
				return t
			}
		default:
			s := fmt.Sprint(t)
			if s != "" {
				return s
			}
		}
	}
	// RUN_PARTIAL_FAILURE: prefer first failed chunk when no single chunk_id.
	if v, ok := details["failed_chunk_ids"]; ok && v != nil {
		switch x := v.(type) {
		case []string:
			if len(x) > 0 {
				return x[0]
			}
		case []any:
			if len(x) > 0 {
				if s, ok := x[0].(string); ok {
					return s
				}
			}
		}
	}
	return ""
}
