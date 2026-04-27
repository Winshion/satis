package bridge

import "sort"

// collectFailedAndBlockedChunkIDs returns sorted chunk IDs in failed or blocked phase.
func collectFailedAndBlockedChunkIDs(statuses map[string]ChunkPhase) (failed, blocked []string) {
	for id, ph := range statuses {
		switch ph {
		case ChunkPhaseFailed:
			failed = append(failed, id)
		case ChunkPhaseBlocked:
			blocked = append(blocked, id)
		}
	}
	sort.Strings(failed)
	sort.Strings(blocked)
	return failed, blocked
}
