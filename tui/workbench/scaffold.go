package workbench

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"path"
	"strings"

	"satis/bridge"
)

const (
	DefaultPlanFileName = "plan.json"
	DefaultRootChunkID  = "CHK_ROOT"
)

// PlanPathForWorkspace returns the default plan file path under a workbench directory.
func PlanPathForWorkspace(dir string) string {
	cleaned := path.Clean(strings.TrimSpace(dir))
	if cleaned == "." || cleaned == "" {
		cleaned = "/"
	}
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}
	return path.Join(cleaned, DefaultPlanFileName)
}

// ScaffoldPlanJSON returns a minimal editable bridge plan for a new workbench directory.
func ScaffoldPlanJSON(dir string, intent string) (string, error) {
	plan := ScaffoldPlan(dir, intent)
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func ScaffoldContinuationPlanJSON(dir string, chunkID string, intentID string, chunkPort string) (string, error) {
	plan := ScaffoldContinuationPlan(dir, chunkID, intentID, chunkPort)
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ScaffoldPlan builds a minimal single-root plan draft for a new workbench directory.
func ScaffoldPlan(dir string, intent string) *bridge.ChunkGraphPlan {
	cleaned := path.Clean(strings.TrimSpace(dir))
	if cleaned == "." || cleaned == "" {
		cleaned = "/"
	}
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}

	slug := slugify(path.Base(cleaned))
	if slug == "" {
		slug = "workspace"
	}
	hash := shortPathHash(cleaned)
	planID := fmt.Sprintf("plan_%s_%s", slug, hash)
	intentID := fmt.Sprintf("intent_%s_%s", slug, hash)

	return &bridge.ChunkGraphPlan{
		ProtocolVersion:   1,
		PlanID:            planID,
		IntentID:          intentID,
		IntentDescription: strings.TrimSpace(intent),
		PlanDescription:   "",
		Goal:              fmt.Sprintf("New workbench scaffold for %s", cleaned),
		EntryChunks:       []string{DefaultRootChunkID},
		Chunks: []bridge.PlanChunk{
			{
				ChunkID:     DefaultRootChunkID,
				Kind:        "task",
				Description: "确认当前工作目录并输出基础检查结果",
				Source: bridge.ChunkSource{
					Format: "satis_v1",
					SatisText: fmt.Sprintf(
						"chunk_id: %s\nintent_uid: %s\ndescription: %s\nchunk_port: %s\n\nPwd\n",
						DefaultRootChunkID,
						intentID,
						"确认当前工作目录并输出基础检查结果",
						"port_root",
					),
				},
			},
		},
		Edges: []bridge.PlanEdge{},
	}
}

func ScaffoldContinuationPlan(dir string, chunkID string, intentID string, chunkPort string) *bridge.ChunkGraphPlan {
	cleaned := path.Clean(strings.TrimSpace(dir))
	if cleaned == "." || cleaned == "" {
		cleaned = "/"
	}
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}
	if strings.TrimSpace(chunkID) == "" {
		chunkID = DefaultRootChunkID
	}
	if strings.TrimSpace(intentID) == "" {
		slug := slugify(path.Base(cleaned))
		if slug == "" {
			slug = "workspace"
		}
		intentID = fmt.Sprintf("intent_%s_%s", slug, shortPathHash(cleaned))
	}
	if strings.TrimSpace(chunkPort) == "" {
		chunkPort = "port_root"
	}
	slug := slugify(path.Base(cleaned))
	if slug == "" {
		slug = "workspace"
	}
	hash := shortPathHash(cleaned + ":" + chunkID)
	return &bridge.ChunkGraphPlan{
		ProtocolVersion:   1,
		PlanID:            fmt.Sprintf("plan_%s_%s", slug, hash),
		IntentID:          intentID,
		IntentDescription: fmt.Sprintf("完成 %s 工作区对应的用户意图", cleaned),
		PlanDescription:   "",
		Goal:              fmt.Sprintf("Continuation workbench scaffold for %s", cleaned),
		EntryChunks:       []string{chunkID},
		Chunks: []bridge.PlanChunk{
			{
				ChunkID:     chunkID,
				Kind:        "task",
				Description: "确认当前工作目录并输出基础检查结果",
				Source: bridge.ChunkSource{
					Format: "satis_v1",
					SatisText: fmt.Sprintf(
						"chunk_id: %s\nintent_uid: %s\ndescription: %s\nchunk_port: %s\n\nPwd\n",
						chunkID,
						intentID,
						"确认当前工作目录并输出基础检查结果",
						chunkPort,
					),
				},
			},
		},
		Edges: []bridge.PlanEdge{},
	}
}

func slugify(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" || name == "." || name == "/" {
		return ""
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range name {
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

func shortPathHash(input string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(input))
	return fmt.Sprintf("%08x", h.Sum32())
}
