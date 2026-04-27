package workbench

import (
	"fmt"
	"sort"
	"strings"

	"satis/bridge"
)

type graphRenderResult struct {
	Text             string
	Neighbors        map[string]graphNeighbors
	Boxes            map[string]graphNodeBox
	DecisionChunkIDs []string
	ParentJunctions  []graphPoint
	Parents          map[string][]string
	Children         map[string][]string
}

type graphNeighbors struct {
	Up    string
	Down  string
	Left  string
	Right string
}

type graphNodeView struct {
	ChunkID    string
	Title      string
	Port       string
	InSummary  string
	OutSummary string
	Layer      int
	Order      int
	IssueCount int
}

type graphNodeBox struct {
	X       int
	Y       int
	Width   int
	Height  int
	CenterX int
	CenterY int
	Layer   int
	Order   int
}

type graphPoint struct {
	X int
	Y int
}

func renderASCIIChunkGraph(
	plan *bridge.ChunkGraphPlan,
	selectedChunkID string,
	validationByChunk map[string][]bridge.ValidationIssue,
) graphRenderResult {
	if plan == nil || len(plan.Chunks) == 0 {
		return graphRenderResult{
			Text:             "No chunks.",
			Neighbors:        map[string]graphNeighbors{},
			Boxes:            map[string]graphNodeBox{},
			DecisionChunkIDs: nil,
			ParentJunctions:  nil,
			Parents:          map[string][]string{},
			Children:         map[string][]string{},
		}
	}
	parents, children := buildGraphAdjacency(plan)
	conditionalEdges := buildConditionalGraphEdges(plan)
	layers, layerOf := computeGraphLayers(plan, parents, children)
	views := buildGraphNodeViews(plan, layerOf, validationByChunk, parents)
	boxes, canvasWidth, canvasHeight := layoutGraphNodeBoxes(layers, views)
	if extra := conditionalEdgeCanvasPadding(conditionalEdges); extra > 0 {
		canvasWidth += extra
	}
	canvas := makeCanvas(canvasWidth, canvasHeight)
	junctions := drawGraphEdges(canvas, parents, boxes, conditionalEdges)
	drawGraphNodes(canvas, views, boxes, selectedChunkID)
	return graphRenderResult{
		Text:             canvasToText(canvas),
		Neighbors:        buildGraphNeighbors(boxes),
		Boxes:            boxes,
		DecisionChunkIDs: listDecisionChunkIDs(plan),
		ParentJunctions:  junctions,
		Parents:          parents,
		Children:         children,
	}
}

func listDecisionChunkIDs(plan *bridge.ChunkGraphPlan) []string {
	if plan == nil {
		return nil
	}
	out := make([]string, 0, len(plan.Chunks))
	for _, chunk := range plan.Chunks {
		if strings.EqualFold(strings.TrimSpace(chunk.Kind), "decision") {
			out = append(out, chunk.ChunkID)
		}
	}
	sort.Strings(out)
	return out
}

func buildGraphNodeViews(
	plan *bridge.ChunkGraphPlan,
	layerOf map[string]int,
	validationByChunk map[string][]bridge.ValidationIssue,
	parents map[string][]string,
) map[string]graphNodeView {
	views := make(map[string]graphNodeView, len(plan.Chunks))
	for _, chunk := range plan.Chunks {
		inVars := summarizeInputVars(&chunk)
		outVars := summarizeOutputVars(&chunk)
		title := graphChunkTitle(plan, chunk.ChunkID, chunk.Kind)
		if len(parents[chunk.ChunkID]) > 1 {
			title = fmt.Sprintf("%s x%d", title, len(parents[chunk.ChunkID]))
		}
		views[chunk.ChunkID] = graphNodeView{
			ChunkID:    chunk.ChunkID,
			Title:      title,
			Port:       summarizeGraphPort(&chunk),
			InSummary:  summarizeGraphVars("in", inVars),
			OutSummary: summarizeGraphVars("out", outVars),
			Layer:      layerOf[chunk.ChunkID],
			IssueCount: len(validationByChunk[chunk.ChunkID]),
		}
	}
	return views
}

func graphChunkTitle(plan *bridge.ChunkGraphPlan, chunkID string, kind string) string {
	chunkID = strings.TrimSpace(chunkID)
	alias := strings.TrimSpace(chunkDisplayAlias(plan, chunkID))
	kindTag := graphChunkKindTag(kind)
	switch {
	case alias == "" && chunkID == "":
		return kindTag
	case alias == "":
		return kindTag + " " + chunkID
	case chunkID == "":
		return kindTag + " " + alias
	default:
		return kindTag + " " + alias + "/" + chunkID
	}
}

func summarizeInputVars(chunk *bridge.PlanChunk) []string {
	rows := handoffRows(chunk)
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if strings.TrimSpace(row.Port) == "" || strings.TrimSpace(row.VarName) == "" {
			continue
		}
		out = append(out, row.Port+"__"+row.VarName)
	}
	sort.Strings(out)
	return out
}

func summarizeOutputVars(chunk *bridge.PlanChunk) []string {
	if chunk == nil {
		return nil
	}
	hints, err := inspectChunkIO(chunk.Source.SatisText)
	if err != nil {
		return nil
	}
	ports := make([]string, 0, len(hints.OutputVars))
	for _, row := range hints.OutputVars {
		if strings.TrimSpace(row.Port) == "" || strings.TrimSpace(row.VarName) == "" {
			continue
		}
		ports = append(ports, row.Port+"__"+row.VarName)
	}
	sort.Strings(ports)
	return ports
}

func summarizeGraphPort(chunk *bridge.PlanChunk) string {
	if chunk == nil {
		return "port:-"
	}
	if kind := strings.ToLower(strings.TrimSpace(chunk.Kind)); kind == "decision" {
		if chunk.Decision == nil || len(chunk.Decision.AllowedBranches) == 0 {
			return "branches:-"
		}
		branches := append([]string(nil), chunk.Decision.AllowedBranches...)
		sort.Strings(branches)
		return summarizeGraphVars("branches", branches)
	}
	port := strings.TrimSpace(extractChunkHeaderValue(chunk.Source.SatisText, chunkPortMetaKey))
	if port == "" {
		return "port:-"
	}
	return "port:" + port
}

func graphChunkKindTag(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "decision":
		return "D"
	default:
		return "T"
	}
}

func summarizeGraphVars(prefix string, values []string) string {
	if len(values) == 0 {
		return prefix + ":-"
	}
	display := append([]string(nil), values...)
	extra := 0
	if len(display) > 2 {
		extra = len(display) - 2
		display = display[:2]
	}
	summary := strings.Join(display, ",")
	if extra > 0 {
		summary += fmt.Sprintf(" +%d", extra)
	}
	return prefix + ":" + summary
}

func buildGraphAdjacency(plan *bridge.ChunkGraphPlan) (map[string][]string, map[string][]string) {
	parents := make(map[string][]string, len(plan.Chunks))
	children := make(map[string][]string, len(plan.Chunks))
	chunkSet := make(map[string]struct{}, len(plan.Chunks))
	for _, chunk := range plan.Chunks {
		chunkSet[chunk.ChunkID] = struct{}{}
	}
	for _, edge := range plan.Edges {
		// loop_back is intentionally excluded from forward structural layout
		// because it points to already-executed ancestors. Handoff edges should
		// be rendered so fan-in links are visible after save-time topology sync.
		if strings.EqualFold(strings.TrimSpace(edge.EdgeKind), "loop_back") {
			continue
		}
		if _, ok := chunkSet[edge.FromChunkID]; !ok {
			continue
		}
		if _, ok := chunkSet[edge.ToChunkID]; !ok {
			continue
		}
		children[edge.FromChunkID] = append(children[edge.FromChunkID], edge.ToChunkID)
		parents[edge.ToChunkID] = append(parents[edge.ToChunkID], edge.FromChunkID)
	}
	for id := range chunkSet {
		sort.Strings(children[id])
		sort.Strings(parents[id])
	}
	return parents, children
}

func buildConditionalGraphEdges(plan *bridge.ChunkGraphPlan) []bridge.PlanEdge {
	if plan == nil {
		return nil
	}
	chunkSet := make(map[string]struct{}, len(plan.Chunks))
	for _, chunk := range plan.Chunks {
		chunkSet[chunk.ChunkID] = struct{}{}
	}
	out := make([]bridge.PlanEdge, 0)
	for _, edge := range plan.Edges {
		if !isConditionalWorkbenchEdgeKind(edge.EdgeKind) {
			continue
		}
		if _, ok := chunkSet[edge.FromChunkID]; !ok {
			continue
		}
		if _, ok := chunkSet[edge.ToChunkID]; !ok {
			continue
		}
		out = append(out, edge)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].FromChunkID != out[j].FromChunkID {
			return out[i].FromChunkID < out[j].FromChunkID
		}
		if out[i].ToChunkID != out[j].ToChunkID {
			return out[i].ToChunkID < out[j].ToChunkID
		}
		return out[i].Branch < out[j].Branch
	})
	return out
}

func conditionalEdgeCanvasPadding(edges []bridge.PlanEdge) int {
	if len(edges) == 0 {
		return 0
	}
	return len(edges)*4 + 4
}

func computeGraphLayers(
	plan *bridge.ChunkGraphPlan,
	parents map[string][]string,
	children map[string][]string,
) ([][]string, map[string]int) {
	indegree := make(map[string]int, len(plan.Chunks))
	layerOf := make(map[string]int, len(plan.Chunks))
	chunkSet := make(map[string]struct{}, len(plan.Chunks))
	for _, chunk := range plan.Chunks {
		chunkSet[chunk.ChunkID] = struct{}{}
		indegree[chunk.ChunkID] = len(parents[chunk.ChunkID])
	}
	ready := make([]string, 0, len(plan.Chunks))
	for _, chunk := range plan.Chunks {
		if indegree[chunk.ChunkID] == 0 {
			ready = append(ready, chunk.ChunkID)
		}
	}
	sort.Strings(ready)
	order := make([]string, 0, len(plan.Chunks))
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		order = append(order, id)
		for _, child := range children[id] {
			if layerOf[child] < layerOf[id]+1 {
				layerOf[child] = layerOf[id] + 1
			}
			indegree[child]--
			if indegree[child] == 0 {
				ready = append(ready, child)
				sort.Strings(ready)
			}
		}
	}
	if len(order) < len(plan.Chunks) {
		var remaining []string
		for _, chunk := range plan.Chunks {
			if _, ok := chunkSet[chunk.ChunkID]; ok {
				found := false
				for _, done := range order {
					if done == chunk.ChunkID {
						found = true
						break
					}
				}
				if !found {
					remaining = append(remaining, chunk.ChunkID)
				}
			}
		}
		sort.Strings(remaining)
		maxLayer := 0
		for _, layer := range layerOf {
			if layer > maxLayer {
				maxLayer = layer
			}
		}
		for _, id := range remaining {
			layerOf[id] = maxLayer + 1
			order = append(order, id)
		}
	}
	maxLayer := 0
	for _, layer := range layerOf {
		if layer > maxLayer {
			maxLayer = layer
		}
	}
	layers := make([][]string, maxLayer+1)
	for _, id := range order {
		layer := layerOf[id]
		layers[layer] = append(layers[layer], id)
	}
	for i := range layers {
		sort.Strings(layers[i])
	}
	orderGraphLayers(layers, parents)
	return layers, layerOf
}

func orderGraphLayers(layers [][]string, parents map[string][]string) {
	if len(layers) == 0 {
		return
	}
	ranks := make(map[string]float64)
	for i, id := range layers[0] {
		ranks[id] = float64(i)
	}
	for layerIndex := 1; layerIndex < len(layers); layerIndex++ {
		ids := append([]string(nil), layers[layerIndex]...)
		sort.SliceStable(ids, func(i, j int) bool {
			leftScore, leftKnown := graphParentOrderScore(ids[i], parents, ranks)
			rightScore, rightKnown := graphParentOrderScore(ids[j], parents, ranks)
			switch {
			case leftKnown && rightKnown && leftScore != rightScore:
				return leftScore < rightScore
			case leftKnown != rightKnown:
				return leftKnown
			default:
				return ids[i] < ids[j]
			}
		})
		layers[layerIndex] = ids
		for i, id := range ids {
			ranks[id] = float64(i)
		}
	}
}

func graphParentOrderScore(id string, parents map[string][]string, ranks map[string]float64) (float64, bool) {
	parentIDs := parents[id]
	if len(parentIDs) == 0 {
		return 0, false
	}
	total := 0.0
	count := 0
	for _, parentID := range parentIDs {
		rank, ok := ranks[parentID]
		if !ok {
			continue
		}
		total += rank
		count++
	}
	if count == 0 {
		return 0, false
	}
	return total / float64(count), true
}

func layoutGraphNodeBoxes(
	layers [][]string,
	views map[string]graphNodeView,
) (map[string]graphNodeBox, int, int) {
	const (
		nodeHeight    = 6
		horizontalGap = 4
		verticalGap   = 2
		margin        = 2
		minInnerWidth = 18
		maxInnerWidth = 30
	)
	maxCount := 1
	innerWidth := minInnerWidth
	for _, layer := range layers {
		if len(layer) > maxCount {
			maxCount = len(layer)
		}
		for _, id := range layer {
			view := views[id]
			for _, text := range []string{view.Title, view.Port, view.InSummary, view.OutSummary} {
				if len(text) > innerWidth {
					innerWidth = len(text)
				}
			}
		}
	}
	if innerWidth > maxInnerWidth {
		innerWidth = maxInnerWidth
	}
	nodeWidth := innerWidth + 4
	layerSpan := nodeWidth + horizontalGap
	canvasWidth := margin*2 + maxCount*nodeWidth + (maxCount-1)*horizontalGap
	canvasHeight := margin*2 + len(layers)*nodeHeight + max(0, len(layers)-1)*verticalGap
	boxes := make(map[string]graphNodeBox, len(views))
	for layerIndex, chunkIDs := range layers {
		offset := margin
		if len(chunkIDs) < maxCount {
			offset += ((maxCount - len(chunkIDs)) * layerSpan) / 2
		}
		y := margin + layerIndex*(nodeHeight+verticalGap)
		for col, id := range chunkIDs {
			x := offset + col*layerSpan
			boxes[id] = graphNodeBox{
				X:       x,
				Y:       y,
				Width:   nodeWidth,
				Height:  nodeHeight,
				CenterX: x + nodeWidth/2,
				CenterY: y + nodeHeight/2,
				Layer:   layerIndex,
				Order:   col,
			}
			view := views[id]
			view.Order = col
			views[id] = view
		}
	}
	return boxes, canvasWidth, canvasHeight
}

func drawGraphEdges(
	canvas [][]rune,
	parents map[string][]string,
	boxes map[string]graphNodeBox,
	conditionalEdges []bridge.PlanEdge,
) []graphPoint {
	junctions := make(map[graphPoint]struct{})
	for childID, parentIDs := range parents {
		childBox, ok := boxes[childID]
		if !ok {
			continue
		}
		for _, parentID := range parentIDs {
			parentBox, ok := boxes[parentID]
			if !ok {
				continue
			}
			startY := parentBox.Y + parentBox.Height
			endY := childBox.Y - 1
			if startY > endY {
				continue
			}
			junction := graphPoint{X: parentBox.CenterX, Y: startY}
			junctions[junction] = struct{}{}
			putConnector(canvas, junction.X, junction.Y, '+')
			if parentBox.CenterX == childBox.CenterX {
				for y := startY + 1; y <= endY; y++ {
					putConnector(canvas, parentBox.CenterX, y, '|')
				}
				continue
			}
			midY := startY + (endY-startY)/2
			for y := startY + 1; y <= midY; y++ {
				putConnector(canvas, parentBox.CenterX, y, '|')
			}
			for x := min(parentBox.CenterX, childBox.CenterX); x <= max(parentBox.CenterX, childBox.CenterX); x++ {
				putConnector(canvas, x, midY, '-')
			}
			for y := midY; y <= endY; y++ {
				putConnector(canvas, childBox.CenterX, y, '|')
			}
		}
	}
	drawConditionalGraphEdges(canvas, boxes, conditionalEdges)
	out := make([]graphPoint, 0, len(junctions))
	for point := range junctions {
		out = append(out, point)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Y != out[j].Y {
			return out[i].Y < out[j].Y
		}
		return out[i].X < out[j].X
	})
	return out
}

func drawConditionalGraphEdges(canvas [][]rune, boxes map[string]graphNodeBox, edges []bridge.PlanEdge) {
	if len(edges) == 0 {
		return
	}
	maxRight := 0
	for _, box := range boxes {
		if right := box.X + box.Width; right > maxRight {
			maxRight = right
		}
	}
	for index, edge := range edges {
		sourceBox, ok := boxes[edge.FromChunkID]
		if !ok {
			continue
		}
		targetBox, ok := boxes[edge.ToChunkID]
		if !ok {
			continue
		}
		if sourceBox.Layer < targetBox.Layer && abs(sourceBox.CenterX-targetBox.CenterX) <= sourceBox.Width {
			// Downstream branch already has a readable vertical structural placement;
			// reserve explicit side-routing for loop-backs / cross-layer jumps.
			continue
		}
		laneX := maxRight + 2 + index*3
		startX := sourceBox.X + sourceBox.Width
		startY := sourceBox.CenterY
		endX := targetBox.X + targetBox.Width
		endY := targetBox.CenterY
		for x := startX; x <= laneX; x++ {
			putBranchConnector(canvas, x, startY, '=')
		}
		step := 1
		if startY > endY {
			step = -1
		}
		for y := startY; y != endY; y += step {
			putBranchConnector(canvas, laneX, y, ':')
		}
		putBranchConnector(canvas, laneX, endY, ':')
		for x := min(laneX, endX); x <= max(laneX, endX); x++ {
			putBranchConnector(canvas, x, endY, '=')
		}
	}
}

func drawGraphNodes(
	canvas [][]rune,
	views map[string]graphNodeView,
	boxes map[string]graphNodeBox,
	selectedChunkID string,
) {
	for id, box := range boxes {
		view := views[id]
		selected := id == selectedChunkID
		lines := formatGraphNodeLines(view, box.Width-4, selected)
		for dy, line := range lines {
			for dx, r := range line {
				canvas[box.Y+dy][box.X+dx] = r
			}
		}
	}
}

func formatGraphNodeLines(view graphNodeView, innerWidth int, selected bool) []string {
	_ = selected
	statusSuffix := " "
	if view.IssueCount > 0 {
		statusSuffix = "!"
	}
	title := fitGraphText(view.Title, innerWidth)
	header := padGraphText(title+" "+statusSuffix, innerWidth+2)
	return []string{
		"+" + strings.Repeat("-", innerWidth+2) + "+",
		"|" + header + "|",
		"| " + padGraphText(fitGraphText(view.Port, innerWidth), innerWidth) + " |",
		"| " + padGraphText(fitGraphText(view.InSummary, innerWidth), innerWidth) + " |",
		"| " + padGraphText(fitGraphText(view.OutSummary, innerWidth), innerWidth) + " |",
		"+" + strings.Repeat("-", innerWidth+2) + "+",
	}
}

func fitGraphText(text string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= width {
		return text
	}
	if width <= 3 {
		return string(runes[:width])
	}
	return string(runes[:width-3]) + "..."
}

func padGraphText(text string, width int) string {
	runes := []rune(text)
	if len(runes) >= width {
		return string(runes[:width])
	}
	return text + strings.Repeat(" ", width-len(runes))
}

func buildGraphNeighbors(boxes map[string]graphNodeBox) map[string]graphNeighbors {
	neighbors := make(map[string]graphNeighbors, len(boxes))
	layerMap := make(map[int][]string)
	for id, box := range boxes {
		layerMap[box.Layer] = append(layerMap[box.Layer], id)
	}
	for layer, ids := range layerMap {
		sort.Slice(ids, func(i, j int) bool { return boxes[ids[i]].CenterX < boxes[ids[j]].CenterX })
		layerMap[layer] = ids
	}
	for id, box := range boxes {
		nav := graphNeighbors{
			Left:  nearestLayerNeighbor(layerMap[box.Layer], boxes, id, -1),
			Right: nearestLayerNeighbor(layerMap[box.Layer], boxes, id, 1),
		}
		nav.Up = nearestVerticalNeighbor(boxes, id, -1)
		nav.Down = nearestVerticalNeighbor(boxes, id, 1)
		neighbors[id] = nav
	}
	return neighbors
}

func nearestLayerNeighbor(ids []string, boxes map[string]graphNodeBox, currentID string, direction int) string {
	for i, id := range ids {
		if id != currentID {
			continue
		}
		next := i + direction
		if next >= 0 && next < len(ids) {
			return ids[next]
		}
		break
	}
	return ""
}

func nearestVerticalNeighbor(boxes map[string]graphNodeBox, currentID string, direction int) string {
	current, ok := boxes[currentID]
	if !ok {
		return ""
	}
	bestID := ""
	bestLayerDelta := 1 << 30
	bestXDelta := 1 << 30
	for id, box := range boxes {
		if id == currentID {
			continue
		}
		layerDelta := box.Layer - current.Layer
		if direction < 0 && layerDelta >= 0 {
			continue
		}
		if direction > 0 && layerDelta <= 0 {
			continue
		}
		layerDistance := abs(layerDelta)
		xDistance := abs(box.CenterX - current.CenterX)
		if layerDistance < bestLayerDelta ||
			(layerDistance == bestLayerDelta && xDistance < bestXDelta) ||
			(layerDistance == bestLayerDelta && xDistance == bestXDelta && box.CenterX < boxes[bestID].CenterX) ||
			(layerDistance == bestLayerDelta && xDistance == bestXDelta && box.CenterX == boxes[bestID].CenterX && id < bestID) {
			bestID = id
			bestLayerDelta = layerDistance
			bestXDelta = xDistance
		}
	}
	return bestID
}

func makeCanvas(width int, height int) [][]rune {
	canvas := make([][]rune, height)
	for y := 0; y < height; y++ {
		canvas[y] = make([]rune, width)
		for x := 0; x < width; x++ {
			canvas[y][x] = ' '
		}
	}
	return canvas
}

func putConnector(canvas [][]rune, x int, y int, ch rune) {
	if y < 0 || y >= len(canvas) || x < 0 || x >= len(canvas[y]) {
		return
	}
	existing := canvas[y][x]
	switch {
	case existing == ' ':
		canvas[y][x] = ch
	case existing == ch:
		return
	case existing == '+':
		return
	case (existing == '-' && ch == '|') || (existing == '|' && ch == '-'):
		canvas[y][x] = '+'
	default:
		canvas[y][x] = '+'
	}
}

func putBranchConnector(canvas [][]rune, x int, y int, ch rune) {
	if y < 0 || y >= len(canvas) || x < 0 || x >= len(canvas[y]) {
		return
	}
	existing := canvas[y][x]
	switch {
	case existing == ' ':
		canvas[y][x] = ch
	case existing == ch:
		return
	case existing == '+':
		return
	default:
		canvas[y][x] = '+'
	}
}

func canvasToText(canvas [][]rune) string {
	lines := make([]string, len(canvas))
	for i, row := range canvas {
		line := strings.TrimRight(string(row), " ")
		lines[i] = line
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

func min(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
