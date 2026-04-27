package workbench

import (
	"fmt"
	"sort"
	"strings"

	"satis/bridge"
)

func chunkDisplayAliases(plan *bridge.ChunkGraphPlan) map[string]string {
	aliases := make(map[string]string)
	if plan == nil {
		return aliases
	}
	parents, children := buildGraphAdjacency(plan)
	indegree := make(map[string]int, len(plan.Chunks))
	for _, chunk := range plan.Chunks {
		indegree[chunk.ChunkID] = len(parents[chunk.ChunkID])
	}
	ready := make([]string, 0, len(plan.Chunks))
	for _, chunk := range plan.Chunks {
		if indegree[chunk.ChunkID] == 0 {
			ready = append(ready, chunk.ChunkID)
		}
	}
	sort.Strings(ready)
	ordered := make([]string, 0, len(plan.Chunks))
	seen := make(map[string]struct{}, len(plan.Chunks))
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ordered = append(ordered, id)
		for _, child := range children[id] {
			indegree[child]--
			if indegree[child] == 0 {
				ready = append(ready, child)
				sort.Strings(ready)
			}
		}
	}
	remaining := make([]string, 0, len(plan.Chunks))
	for _, chunk := range plan.Chunks {
		if _, ok := seen[chunk.ChunkID]; !ok {
			remaining = append(remaining, chunk.ChunkID)
		}
	}
	sort.Strings(remaining)
	ordered = append(ordered, remaining...)
	for i, id := range ordered {
		aliases[id] = fmt.Sprintf("chk%d", i)
	}
	return aliases
}

func chunkDisplayAlias(plan *bridge.ChunkGraphPlan, chunkID string) string {
	return chunkDisplayAliases(plan)[strings.TrimSpace(chunkID)]
}

func chunkDisplayLabel(plan *bridge.ChunkGraphPlan, chunkID string) string {
	chunkID = strings.TrimSpace(chunkID)
	if chunkID == "" {
		return ""
	}
	alias := chunkDisplayAlias(plan, chunkID)
	if alias == "" {
		return chunkID
	}
	return alias + " [" + chunkID + "]"
}
