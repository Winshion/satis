package workbench

import (
	"fmt"
	"slices"
	"strings"

	"satis/bridge"
)

const chunkPortMetaKey = "chunk_port"

type chunkPortRename struct {
	ChunkID string
	OldPort string
	NewPort string
}

func (m *Model) NormalizeChunkPorts() ([]chunkPortRename, error) {
	if m == nil || m.Plan == nil {
		return nil, fmt.Errorf("workbench model is not initialized")
	}
	ordered := make([]string, 0, len(m.Plan.Chunks))
	chunksByID := make(map[string]*bridge.PlanChunk, len(m.Plan.Chunks))
	for i := range m.Plan.Chunks {
		chunk := &m.Plan.Chunks[i]
		ordered = append(ordered, chunk.ChunkID)
		chunksByID[chunk.ChunkID] = chunk
	}
	slices.Sort(ordered)

	used := make(map[string]string, len(ordered))
	renames := make([]chunkPortRename, 0)
	for _, chunkID := range ordered {
		chunk := chunksByID[chunkID]
		current := strings.TrimSpace(extractChunkHeaderValue(chunk.Source.SatisText, chunkPortMetaKey))
		base := normalizedChunkPortBase(current, chunkID, chunk.Source.SatisText)
		next := nextAvailablePortName(base, used)
		used[next] = chunkID
		rename, changed := applyChunkPortRename(m.Plan, chunkID, next)
		if changed {
			renames = append(renames, rename)
		}
	}
	if len(renames) > 0 {
		m.Dirty = true
		return renames, nil
	}
	return nil, nil
}

func (m *Model) RenameChunkPort(chunkID string, newPort string) (*chunkPortRename, error) {
	if m == nil || m.Plan == nil {
		return nil, fmt.Errorf("workbench model is not initialized")
	}
	rename, changed := applyChunkPortRename(m.Plan, strings.TrimSpace(chunkID), newPort)
	if !changed {
		return nil, nil
	}
	m.Dirty = true
	return &rename, nil
}

func extractChunkHeaderValue(text string, key string) string {
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			break
		}
		idx := strings.Index(trimmed, ":")
		if idx <= 0 {
			break
		}
		if strings.TrimSpace(trimmed[:idx]) == key {
			return strings.TrimSpace(trimmed[idx+1:])
		}
	}
	return ""
}

func setChunkHeaderValue(text string, key string, value string) string {
	lines := strings.Split(text, "\n")
	lastHeader := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if lastHeader >= 0 {
				break
			}
			continue
		}
		idx := strings.Index(trimmed, ":")
		if idx <= 0 {
			break
		}
		headerKey := strings.TrimSpace(trimmed[:idx])
		if headerKey == key {
			lines[i] = key + ": " + value
			return strings.Join(lines, "\n")
		}
		lastHeader = i
	}
	insertAt := 0
	if lastHeader >= 0 {
		insertAt = lastHeader + 1
	}
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:insertAt]...)
	out = append(out, key+": "+value)
	out = append(out, lines[insertAt:]...)
	return strings.Join(out, "\n")
}

func normalizedChunkPortBase(current string, chunkID string, text string) string {
	if normalized := normalizePortName(current); normalized != "" {
		return normalized
	}
	hints, err := inspectChunkIO(text)
	if err == nil && len(hints.OutputVars) > 0 {
		ports := make([]string, 0, len(hints.OutputVars))
		for _, row := range hints.OutputVars {
			ports = append(ports, row.Port)
		}
		slices.Sort(ports)
		if normalized := normalizePortName(ports[0]); normalized != "" {
			return normalized
		}
	}
	return normalizePortName("port_" + strings.ToLower(strings.TrimSpace(chunkID)))
}

func normalizePortName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var b strings.Builder
	for i, r := range raw {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case i > 0 && r >= '0' && r <= '9':
			b.WriteRune(r)
		case i > 0 && r == '_':
			b.WriteRune(r)
		default:
			if i > 0 {
				b.WriteByte('_')
			}
		}
	}
	normalized := strings.Trim(b.String(), "_")
	if normalized == "" {
		return "port"
	}
	first := normalized[0]
	if !(first >= 'a' && first <= 'z' || first >= 'A' && first <= 'Z') {
		normalized = "p_" + normalized
	}
	for strings.Contains(normalized, "__") {
		normalized = strings.ReplaceAll(normalized, "__", "_")
	}
	return normalized
}

func nextAvailablePortName(base string, used map[string]string) string {
	base = normalizePortName(base)
	if base == "" {
		base = "port"
	}
	if _, exists := used[base]; !exists {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s_%d", base, i)
		if _, exists := used[candidate]; !exists {
			return candidate
		}
	}
}

func replaceOutputAliasPortInText(text string, oldPort string, newPort string) string {
	oldPort = strings.TrimSpace(oldPort)
	newPort = strings.TrimSpace(newPort)
	if oldPort == "" || newPort == "" || oldPort == newPort {
		return text
	}
	return strings.ReplaceAll(text, "@"+oldPort+"__", "@"+newPort+"__")
}

func applyChunkPortRename(plan *bridge.ChunkGraphPlan, chunkID string, newPort string) (chunkPortRename, bool) {
	if plan == nil || strings.TrimSpace(chunkID) == "" {
		return chunkPortRename{}, false
	}
	chunk := chunkByID(plan, chunkID)
	if chunk == nil {
		return chunkPortRename{}, false
	}
	current := strings.TrimSpace(extractChunkHeaderValue(chunk.Source.SatisText, chunkPortMetaKey))
	effectiveOldPort := current
	if effectiveOldPort == "" {
		effectiveOldPort = firstOutputPort(chunk.Source.SatisText)
	}
	newPort = normalizePortName(newPort)
	if newPort == "" {
		newPort = normalizedChunkPortBase(current, chunkID, chunk.Source.SatisText)
	}
	if current == newPort && effectiveOldPort == newPort {
		return chunkPortRename{}, false
	}
	chunk.Source.SatisText = setChunkHeaderValue(chunk.Source.SatisText, chunkPortMetaKey, newPort)
	if effectiveOldPort != "" {
		chunk.Source.SatisText = replaceOutputAliasPortInText(chunk.Source.SatisText, effectiveOldPort, newPort)
	}
	updateDownstreamFromPorts(plan, chunkID, effectiveOldPort, newPort)
	return chunkPortRename{ChunkID: chunkID, OldPort: effectiveOldPort, NewPort: newPort}, true
}

func updateDownstreamFromPorts(plan *bridge.ChunkGraphPlan, chunkID string, oldPort string, newPort string) {
	_ = plan
	_ = chunkID
	_ = oldPort
	_ = newPort
}

func chunkByID(plan *bridge.ChunkGraphPlan, chunkID string) *bridge.PlanChunk {
	if plan == nil {
		return nil
	}
	for i := range plan.Chunks {
		if plan.Chunks[i].ChunkID == chunkID {
			return &plan.Chunks[i]
		}
	}
	return nil
}

func chunkPortOwners(plan *bridge.ChunkGraphPlan) map[string]string {
	owners := make(map[string]string)
	if plan == nil {
		return owners
	}
	for _, chunk := range plan.Chunks {
		port := strings.TrimSpace(extractChunkHeaderValue(chunk.Source.SatisText, chunkPortMetaKey))
		if port == "" {
			port = firstOutputPort(chunk.Source.SatisText)
		}
		if port == "" {
			continue
		}
		owners[port] = chunk.ChunkID
	}
	return owners
}

func firstOutputPort(text string) string {
	hints, err := inspectChunkIO(text)
	if err != nil || len(hints.OutputVars) == 0 {
		return ""
	}
	ports := make([]string, 0, len(hints.OutputVars))
	for _, row := range hints.OutputVars {
		ports = append(ports, row.Port)
	}
	slices.Sort(ports)
	return strings.TrimSpace(ports[0])
}
