package workbench

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"satis/bridge"
	"satis/satis"
	"satis/vfs"
)

type chunkIOHints struct {
	InputVars  []HandoffRow
	OutputVars []HandoffRow
}

func nextWorkspaceChunkID(ctx context.Context, backend Backend, model *Model) string {
	used := collectWorkspaceChunkIDs(ctx, backend, model)
	return nextChunkIDFromUsed(used)
}

func collectWorkspaceChunkIDs(ctx context.Context, backend Backend, model *Model) map[string]struct{} {
	used := make(map[string]struct{})
	if model != nil && model.Plan != nil {
		for _, chunk := range model.Plan.Chunks {
			if id := strings.TrimSpace(chunk.ChunkID); id != "" {
				used[id] = struct{}{}
			}
		}
	}
	if backend == nil || model == nil {
		return used
	}
	dir := path.Dir(strings.TrimSpace(model.ResolvedPath))
	entries, err := backend.ListVirtualDir(ctx, dir)
	if err != nil {
		return used
	}
	currentPath := path.Clean(strings.TrimSpace(model.ResolvedPath))
	for _, entry := range entries {
		entryPath := path.Clean(strings.TrimSpace(entry.VirtualPath))
		if entry.Kind == vfs.FileKindDirectory || entryPath == currentPath {
			continue
		}
		name := strings.TrimSpace(entry.Name)
		if !strings.HasSuffix(strings.ToLower(name), ".json") || strings.HasPrefix(name, ".") {
			continue
		}
		text, readErr := backend.ReadVirtualText(ctx, entryPath)
		if readErr != nil || strings.TrimSpace(text) == "" {
			continue
		}
		plan, parseErr := ParsePlanDocument(text)
		if parseErr != nil || plan == nil {
			continue
		}
		for _, chunk := range plan.Chunks {
			if id := strings.TrimSpace(chunk.ChunkID); id != "" {
				used[id] = struct{}{}
			}
		}
	}
	return used
}

func renameChunkIDInPlan(plan *bridge.ChunkGraphPlan, oldID string, newID string) {
	if plan == nil || strings.TrimSpace(oldID) == "" || strings.TrimSpace(newID) == "" || oldID == newID {
		return
	}
	for i := range plan.Chunks {
		chunk := &plan.Chunks[i]
		if chunk.ChunkID == oldID {
			chunk.ChunkID = newID
			chunk.Source.SatisText = replaceChunkHeaderID(chunk.Source.SatisText, newID)
		}
		for j := range chunk.DependsOn {
			if chunk.DependsOn[j] == oldID {
				chunk.DependsOn[j] = newID
			}
		}
		if chunk.Inputs == nil {
			continue
		}
		rawInputs, _ := chunk.Inputs["handoff_inputs"].(map[string]any)
		for _, item := range rawInputs {
			spec, _ := item.(map[string]any)
			if spec == nil {
				continue
			}
			if fromStep, _ := spec["from_step"].(string); strings.TrimSpace(fromStep) == oldID {
				spec["from_step"] = newID
			}
		}
	}
	for i := range plan.EntryChunks {
		if plan.EntryChunks[i] == oldID {
			plan.EntryChunks[i] = newID
		}
	}
	for i := range plan.Edges {
		if plan.Edges[i].FromChunkID == oldID {
			plan.Edges[i].FromChunkID = newID
		}
		if plan.Edges[i].ToChunkID == oldID {
			plan.Edges[i].ToChunkID = newID
		}
	}
}

func (m *Model) AddChildChunk(parentChunkID string) (string, *HandoffRow, error) {
	if m == nil || m.Plan == nil {
		return "", nil, fmt.Errorf("workbench model is not initialized")
	}
	parentChunkID = strings.TrimSpace(parentChunkID)
	if parentChunkID == "" {
		chunkID, err := m.AddChunk()
		return chunkID, nil, err
	}
	if m.ChunkByID(parentChunkID) == nil {
		return "", nil, fmt.Errorf("unknown parent chunk %q", parentChunkID)
	}

	chunkID := nextChunkID(m.Plan)
	chunkPort := nextChunkPort(m.Plan)
	intentID := strings.TrimSpace(m.Plan.IntentID)
	if intentID == "" {
		intentID = "intent_workbench"
	}

	var scaffoldRow *HandoffRow
	if parent := m.ChunkByID(parentChunkID); parent != nil {
		if row, ok := firstOutputAsHandoff(parentChunkID, parent); ok {
			copyRow := row
			scaffoldRow = &copyRow
		}
	}

	newChunk := bridge.PlanChunk{
		ChunkID:     chunkID,
		Kind:        defaultNewChunkKind,
		Description: strings.TrimSpace(extractChunkHeaderValue(scaffoldChildChunkText(chunkID, intentID, chunkPort, scaffoldRow), chunkDescriptionMetaKey)),
		Source: bridge.ChunkSource{
			Format:    "satis_v1",
			SatisText: scaffoldChildChunkText(chunkID, intentID, chunkPort, scaffoldRow),
		},
	}
	if scaffoldRow != nil {
		newChunk.Inputs = map[string]any{
			"handoff_inputs": map[string]any{
				handoffBindingKey(scaffoldRow.Port, scaffoldRow.VarName): map[string]any{
					"from_step": scaffoldRow.FromStep,
					"var_name":  scaffoldRow.VarName,
				},
			},
		}
	}

	m.Plan.Chunks = append(m.Plan.Chunks, newChunk)
	if !containsEdge(m.Plan.Edges, parentChunkID, chunkID) {
		m.Plan.Edges = append(m.Plan.Edges, bridge.PlanEdge{
			FromChunkID: parentChunkID,
			ToChunkID:   chunkID,
			EdgeKind:    "control",
		})
	}
	if child := m.ChunkByID(chunkID); child != nil {
		child.DependsOn = []string{parentChunkID}
	}
	if err := m.SyncEdgesFromHandoffs(); err != nil {
		return "", nil, err
	}
	m.SelectedChunkID = chunkID
	m.Dirty = true
	return chunkID, scaffoldRow, nil
}

func (m *Model) DuplicateChunk(chunkID string) (string, error) {
	if m == nil || m.Plan == nil {
		return "", fmt.Errorf("workbench model is not initialized")
	}
	chunk := m.ChunkByID(strings.TrimSpace(chunkID))
	if chunk == nil {
		return "", fmt.Errorf("unknown chunk %q", chunkID)
	}
	newID := nextChunkID(m.Plan)
	dup := *chunk
	dup.ChunkID = newID
	if dup.Inputs != nil {
		dup.Inputs = cloneJSONMap(dup.Inputs)
	}
	if dup.Outputs != nil {
		dup.Outputs = cloneJSONMap(dup.Outputs)
	}
	dup.DependsOn = nil
	dup.Source.SatisText = replaceChunkHeaderID(dup.Source.SatisText, newID)
	oldPort := strings.TrimSpace(extractChunkHeaderValue(dup.Source.SatisText, chunkPortMetaKey))
	newPort := nextChunkPort(m.Plan)
	dup.Source.SatisText = setChunkHeaderValue(dup.Source.SatisText, chunkPortMetaKey, newPort)
	dup.Source.SatisText = replaceOutputAliasPortInText(dup.Source.SatisText, oldPort, newPort)
	m.Plan.Chunks = append(m.Plan.Chunks, dup)
	if err := m.SyncEdgesFromHandoffs(); err != nil {
		return "", err
	}
	m.SelectedChunkID = newID
	m.Dirty = true
	return newID, nil
}

func (m *Model) ReplaceHandoffRows(chunkID string, rows []HandoffRow) error {
	chunk := m.ChunkByID(chunkID)
	if chunk == nil {
		return fmt.Errorf("unknown chunk %q", chunkID)
	}
	if chunk.Inputs == nil {
		chunk.Inputs = make(map[string]any)
	}
	if len(rows) == 0 {
		delete(chunk.Inputs, "handoff_inputs")
		m.Dirty = true
		return nil
	}
	rawInputs := make(map[string]any, len(rows))
	for _, row := range rows {
		row.Port = strings.TrimSpace(row.Port)
		row.VarName = strings.TrimSpace(row.VarName)
		if row.Port == "" {
			return fmt.Errorf("handoff port cannot be empty")
		}
		if row.VarName == "" {
			return fmt.Errorf("handoff var cannot be empty")
		}
		spec := map[string]any{}
		if strings.TrimSpace(row.FromStep) != "" {
			spec["from_step"] = strings.TrimSpace(row.FromStep)
		}
		spec["var_name"] = row.VarName
		if strings.TrimSpace(row.SupplementaryJSON) != "" {
			var supplementary map[string]any
			if err := jsonUnmarshal([]byte(row.SupplementaryJSON), &supplementary); err != nil {
				return fmt.Errorf("invalid supplementary_info json: %w", err)
			}
			spec["supplementary_info"] = supplementary
		}
		rawInputs[handoffBindingKey(row.Port, row.VarName)] = spec
	}
	chunk.Inputs["handoff_inputs"] = rawInputs
	m.Dirty = true
	return nil
}

func (m *Model) SyncHandoffsFromBody(chunkID string) ([]HandoffRow, error) {
	chunk := m.ChunkByID(chunkID)
	if chunk == nil {
		return nil, fmt.Errorf("unknown chunk %q", chunkID)
	}
	hints, err := inspectChunkIO(chunk.Source.SatisText)
	if err != nil {
		return nil, err
	}
	chunkPort := strings.TrimSpace(extractChunkHeaderValue(chunk.Source.SatisText, chunkPortMetaKey))
	portOwners := chunkPortOwners(m.Plan)
	existing, err := m.HandoffRows(chunkID)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]HandoffRow, len(existing))
	for _, row := range existing {
		byKey[row.Key] = row
	}
	rows := make([]HandoffRow, 0, len(hints.InputVars))
	for _, inferred := range hints.InputVars {
		if inferred.Port == chunkPort {
			continue
		}
		row := byKey[inferred.Key]
		row.Key = inferred.Key
		row.Port = inferred.Port
		row.VarName = inferred.VarName
		if fromStep := strings.TrimSpace(portOwners[row.Port]); fromStep != "" {
			if fromStep == chunkID {
				continue
			}
			row.FromStep = fromStep
		} else if strings.TrimSpace(row.FromStep) == "" {
			return nil, fmt.Errorf("chunk body input port %q does not match any declared chunk_port", row.Port)
		}
		rows = append(rows, row)
	}
	if err := m.ReplaceHandoffRows(chunkID, rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (m *Model) SyncBodyPreludeFromHandoffs(chunkID string) error {
	chunk := m.ChunkByID(chunkID)
	if chunk == nil {
		return fmt.Errorf("unknown chunk %q", chunkID)
	}
	rows, err := m.HandoffRows(chunkID)
	if err != nil {
		return err
	}
	chunk.Source.SatisText = rewriteBodyPrelude(chunk.Source.SatisText, rows)
	m.Dirty = true
	return nil
}

func (m *Model) BuildFocusedRunPlan(chunkID string) (*bridge.ChunkGraphPlan, []string, error) {
	if m == nil || m.Plan == nil {
		return nil, nil, fmt.Errorf("workbench model is not initialized")
	}
	chunkID = strings.TrimSpace(chunkID)
	if chunkID == "" {
		return nil, nil, fmt.Errorf("chunk id required")
	}
	if m.ChunkByID(chunkID) == nil {
		return nil, nil, fmt.Errorf("unknown chunk %q", chunkID)
	}
	include := collectAncestors(m.Plan, chunkID)
	include[chunkID] = struct{}{}

	chunkIDs := make([]string, 0, len(include))
	filtered := &bridge.ChunkGraphPlan{
		ProtocolVersion: m.Plan.ProtocolVersion,
		PlanID:          fmt.Sprintf("%s__focus_%s_%d", slugPlanID(m.Plan.PlanID), strings.ToLower(chunkID), time.Now().UnixNano()),
		IntentID:        m.Plan.IntentID,
		Goal:            fmt.Sprintf("%s (focus %s)", m.Plan.Goal, chunkID),
		Policies:        m.Plan.Policies,
	}
	for _, chunk := range m.Plan.Chunks {
		if _, ok := include[chunk.ChunkID]; !ok {
			continue
		}
		copyChunk := chunk
		if copyChunk.Inputs != nil {
			copyChunk.Inputs = cloneJSONMap(copyChunk.Inputs)
		}
		if copyChunk.Outputs != nil {
			copyChunk.Outputs = cloneJSONMap(copyChunk.Outputs)
		}
		filtered.Chunks = append(filtered.Chunks, copyChunk)
		chunkIDs = append(chunkIDs, chunk.ChunkID)
	}
	for _, edge := range m.Plan.Edges {
		if _, ok := include[edge.FromChunkID]; !ok {
			continue
		}
		if _, ok := include[edge.ToChunkID]; !ok {
			continue
		}
		filtered.Edges = append(filtered.Edges, edge)
	}
	inDegree := make(map[string]int, len(filtered.Chunks))
	for _, chunk := range filtered.Chunks {
		inDegree[chunk.ChunkID] = 0
	}
	for _, edge := range filtered.Edges {
		inDegree[edge.ToChunkID]++
	}
	for i := range filtered.Chunks {
		deps := filtered.Chunks[i].DependsOn[:0]
		for _, dep := range filtered.Chunks[i].DependsOn {
			if _, ok := include[dep]; ok {
				deps = append(deps, dep)
			}
		}
		filtered.Chunks[i].DependsOn = deps
	}
	for _, chunk := range filtered.Chunks {
		if inDegree[chunk.ChunkID] == 0 {
			filtered.EntryChunks = append(filtered.EntryChunks, chunk.ChunkID)
		}
	}
	sort.Strings(chunkIDs)
	sort.Strings(filtered.EntryChunks)
	return filtered, chunkIDs, nil
}

func inspectChunkIO(text string) (chunkIOHints, error) {
	hints := chunkIOHints{
		InputVars:  make([]HandoffRow, 0),
		OutputVars: make([]HandoffRow, 0),
	}
	recordWorkflowVar := func(target *[]HandoffRow, port string, varName string) {
		key := handoffBindingKey(port, varName)
		for _, row := range *target {
			if row.Key == key {
				return
			}
		}
		*target = append(*target, HandoffRow{Key: key, Port: port, VarName: varName})
	}
	recordInputValue := func(value satis.Value) error {
		if value.Kind != satis.ValueKindVariable {
			return nil
		}
		if port, name, ok := inputAliasParts(value.Text); ok {
			recordWorkflowVar(&hints.InputVars, port, name)
		}
		return nil
	}
	recordInputValues := func(values []satis.Value) error {
		for _, value := range values {
			if err := recordInputValue(value); err != nil {
				return err
			}
		}
		return nil
	}
	ch, err := satis.Parse(text)
	if err != nil {
		return hints, err
	}
	for _, inst := range ch.Instructions {
		switch stmt := inst.(type) {
		case satis.ReadStmt:
			if port, name, ok := inputAliasParts(stmt.ObjectVar); ok {
				recordWorkflowVar(&hints.InputVars, port, name)
			}
			if port, name, ok := outputAliasParts(stmt.OutputVar); ok {
				recordWorkflowVar(&hints.OutputVars, port, name)
			}
		case satis.WriteStmt:
			if err := recordInputValue(stmt.Value); err != nil {
				return hints, err
			}
		case satis.PrintStmt:
			if err := recordInputValue(stmt.Value); err != nil {
				return hints, err
			}
		case satis.ConcatStmt:
			if port, name, ok := outputAliasParts(stmt.OutputVar); ok {
				recordWorkflowVar(&hints.OutputVars, port, name)
			}
			if err := recordInputValues(stmt.Values); err != nil {
				return hints, err
			}
		case satis.InvokeStmt:
			if port, name, ok := outputAliasParts(stmt.OutputVar); ok {
				recordWorkflowVar(&hints.OutputVars, port, name)
			}
			if port, name, ok := inputAliasParts(stmt.ConversationVar); ok {
				recordWorkflowVar(&hints.InputVars, port, name)
			}
			if len(stmt.PromptParts) > 0 {
				if err := recordInputValues(stmt.PromptParts); err != nil {
					return hints, err
				}
			} else if err := recordInputValue(stmt.Prompt); err != nil {
				return hints, err
			}
			if stmt.HasInput {
				if err := recordInputValue(stmt.Input); err != nil {
					return hints, err
				}
			}
		case satis.BatchInvokeStmt:
			if port, name, ok := outputAliasParts(stmt.OutputVar); ok {
				recordWorkflowVar(&hints.OutputVars, port, name)
			}
			if err := recordInputValue(stmt.Prompt); err != nil {
				return hints, err
			}
			if port, name, ok := inputAliasParts(stmt.InputList); ok {
				recordWorkflowVar(&hints.InputVars, port, name)
			}
		}
	}
	sort.Slice(hints.InputVars, func(i, j int) bool { return hints.InputVars[i].Key < hints.InputVars[j].Key })
	sort.Slice(hints.OutputVars, func(i, j int) bool { return hints.OutputVars[i].Key < hints.OutputVars[j].Key })
	return hints, nil
}

func scaffoldChildChunkText(chunkID string, intentID string, chunkPort string, row *HandoffRow) string {
	body := "Pwd\n"
	description := "执行该子 chunk 的默认任务"
	if row != nil && strings.TrimSpace(row.Port) != "" && strings.TrimSpace(row.VarName) != "" {
		port := row.Port
		body = fmt.Sprintf("Print %s\n", workflowAliasName(port, row.VarName))
		description = fmt.Sprintf("消费来自 %s 的 handoff 输入并输出检查结果", port)
	}
	return fmt.Sprintf("chunk_id: %s\nintent_uid: %s\ndescription: %s\nchunk_port: %s\n\n%s", chunkID, intentID, description, chunkPort, body)
}

func firstOutputAsHandoff(parentChunkID string, parent *bridge.PlanChunk) (HandoffRow, bool) {
	if parent == nil {
		return HandoffRow{}, false
	}
	hints, err := inspectChunkIO(parent.Source.SatisText)
	if err != nil || len(hints.OutputVars) == 0 {
		return HandoffRow{}, false
	}
	row := hints.OutputVars[0]
	row.FromStep = parentChunkID
	return row, true
}

func rewriteBodyPrelude(text string, rows []HandoffRow) string {
	lines := strings.Split(text, "\n")
	metaEnd := 0
	for metaEnd < len(lines) {
		line := strings.TrimSpace(lines[metaEnd])
		if line == "" {
			metaEnd++
			break
		}
		if strings.Contains(line, ":") {
			metaEnd++
			continue
		}
		break
	}
	preludeEnd := metaEnd
	for preludeEnd < len(lines) {
		line := strings.TrimSpace(lines[preludeEnd])
		if line == "" {
			preludeEnd++
			continue
		}
		if !isGeneratedPreludeLine(line) {
			break
		}
		preludeEnd++
	}
	out := make([]string, 0, len(lines))
	out = append(out, lines[:metaEnd]...)
	out = append(out, lines[preludeEnd:]...)
	return strings.Join(trimTrailingBlankLines(out), "\n")
}

func isGeneratedPreludeLine(line string) bool {
	if port, kind, ok := parseResolveLineForInputPort(line); ok && kind == "file" && port != "" {
		return true
	}
	if port, ok := parseReadLineForInputPort(line); ok && port != "" {
		return true
	}
	return false
}

func parseResolveLineForInputPort(line string) (string, string, bool) {
	const prefix = "Resolve file "
	const marker = " as @"
	if !strings.HasPrefix(line, prefix) || !strings.Contains(line, marker) || !strings.HasSuffix(line, "_file") {
		return "", "", false
	}
	idx := strings.LastIndex(line, marker)
	if idx < 0 {
		return "", "", false
	}
	port := strings.TrimSuffix(strings.TrimPrefix(line[idx+len(" as @"):], ""), "_file")
	port = strings.TrimPrefix(port, "in_")
	port = strings.TrimPrefix(port, "out_")
	if port == "" {
		return "", "", false
	}
	return port, "file", true
}

func parseReadLineForInputPort(line string) (string, bool) {
	const prefix = "Read @"
	const middle = "_file as @"
	if !strings.HasPrefix(line, prefix) || !strings.Contains(line, middle) || !strings.HasSuffix(line, "_text") {
		return "", false
	}
	left := strings.TrimPrefix(line, prefix)
	idx := strings.Index(left, middle)
	if idx <= 0 {
		return "", false
	}
	port := left[:idx]
	right := strings.TrimSuffix(strings.TrimPrefix(left[idx+len(middle):], ""), "_text")
	if port == "" || port != right {
		return "", false
	}
	return port, true
}

func trimTrailingBlankLines(lines []string) []string {
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	if end == 0 {
		return []string{""}
	}
	return lines[:end]
}

func replaceChunkHeaderID(text string, chunkID string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "chunk_id:") {
			lines[i] = "chunk_id: " + chunkID
			return strings.Join(lines, "\n")
		}
	}
	return fmt.Sprintf("chunk_id: %s\n%s", chunkID, text)
}

func collectAncestors(plan *bridge.ChunkGraphPlan, target string) map[string]struct{} {
	reverse := make(map[string][]string)
	for _, edge := range plan.Edges {
		reverse[edge.ToChunkID] = append(reverse[edge.ToChunkID], edge.FromChunkID)
	}
	out := make(map[string]struct{})
	queue := []string{target}
	seen := map[string]struct{}{target: {}}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, parent := range reverse[current] {
			if _, ok := seen[parent]; ok {
				continue
			}
			seen[parent] = struct{}{}
			out[parent] = struct{}{}
			queue = append(queue, parent)
		}
	}
	return out
}

func slugPlanID(planID string) string {
	planID = strings.TrimSpace(planID)
	if planID == "" {
		return "plan"
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToLower(planID) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	return strings.Trim(b.String(), "_")
}

func workflowAliasParts(varName string) (string, string, bool) {
	trimmed := strings.TrimSpace(varName)
	if !strings.HasPrefix(trimmed, "@") {
		return "", "", false
	}
	trimmed = strings.TrimPrefix(trimmed, "@")
	idx := strings.Index(trimmed, "__")
	if idx <= 0 || idx >= len(trimmed)-2 {
		return "", "", false
	}
	return trimmed[:idx], trimmed[idx+2:], true
}

func workflowAliasName(port string, varName string) string {
	return "@" + port + "__" + varName
}

func inputAliasParts(varName string) (string, string, bool) {
	return workflowAliasParts(varName)
}

func outputAliasParts(varName string) (string, string, bool) {
	return workflowAliasParts(varName)
}

func containsEdge(edges []bridge.PlanEdge, fromChunkID string, toChunkID string) bool {
	for _, edge := range edges {
		if edge.FromChunkID == fromChunkID && edge.ToChunkID == toChunkID {
			return true
		}
	}
	return false
}

func cloneJSONMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = cloneJSONValue(value)
	}
	return out
}

func cloneJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneJSONMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = cloneJSONValue(item)
		}
		return out
	default:
		return value
	}
}
