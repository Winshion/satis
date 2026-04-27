package workbench

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"satis/bridge"
)

const planRegistryFileName = ".plan_registry.json"

type planRegistry struct {
	Plans map[string]planRegistryEntry `json:"plans,omitempty"`
}

type planRegistryEntry struct {
	Prev string `json:"prev,omitempty"`
	Next string `json:"next,omitempty"`
}

type planChainLinks struct {
	Prev string
	Next string
}

func resolveWorkbenchPlanPath(model *Model, rawPath string) (string, error) {
	if model == nil {
		return "", fmt.Errorf("workbench model is not initialized")
	}
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return "", fmt.Errorf("missing planning json path")
	}
	if !strings.HasSuffix(strings.ToLower(rawPath), ".json") {
		rawPath += ".json"
	}
	if strings.HasPrefix(rawPath, "/") {
		return path.Clean(rawPath), nil
	}
	return path.Clean(path.Join(path.Dir(model.ResolvedPath), rawPath)), nil
}

func createContinuationPlanDocument(ctx context.Context, backend Backend, model *Model, targetPath string) (string, string, error) {
	if model == nil || model.Plan == nil {
		return "", "", fmt.Errorf("workbench model is not initialized")
	}
	chunkID := nextWorkspaceChunkID(ctx, backend, model)
	chunkPort := nextChunkPort(model.Plan)
	workspaceDir := path.Dir(targetPath)
	text, err := ScaffoldContinuationPlanJSON(workspaceDir, chunkID, model.Plan.IntentID, chunkPort)
	if err != nil {
		return "", "", err
	}
	return text, chunkID, nil
}

func loadOrCreateContinuationPlan(ctx context.Context, backend Backend, model *Model, targetPath string, createIfMissing bool) (*bridge.ChunkGraphPlan, bool, error) {
	text, err := backend.ReadVirtualText(ctx, targetPath)
	if err != nil || strings.TrimSpace(text) == "" {
		if !createIfMissing {
			if err != nil {
				return nil, false, err
			}
			return nil, false, fmt.Errorf("planning json %s is empty or missing", targetPath)
		}
		scaffold, _, scaffoldErr := createContinuationPlanDocument(ctx, backend, model, targetPath)
		if scaffoldErr != nil {
			return nil, false, scaffoldErr
		}
		if writeErr := backend.WriteVirtualText(ctx, targetPath, scaffold); writeErr != nil {
			return nil, false, writeErr
		}
		text = scaffold
	}
	plan, parseErr := ParsePlanDocument(text)
	if parseErr != nil {
		return nil, false, parseErr
	}
	return plan, true, nil
}

func loadContinuationPlanDocument(ctx context.Context, backend Backend, targetPath string) (*bridge.ChunkGraphPlan, error) {
	text, err := backend.ReadVirtualText(ctx, targetPath)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("planning json %s is empty or missing", targetPath)
	}
	plan, err := ParsePlanDocument(text)
	if err != nil {
		return nil, err
	}
	return plan, nil
}

func persistPlanDocument(ctx context.Context, backend Backend, targetPath string, plan *bridge.ChunkGraphPlan) error {
	if backend == nil || plan == nil {
		return fmt.Errorf("missing backend or plan")
	}
	result := bridge.ValidateChunkGraphPlan(plan)
	if !result.Accepted || result.NormalizedPlan == nil {
		return formatValidationIssues(result.ValidationErrors)
	}
	data, err := jsonMarshalIndent(result.NormalizedPlan)
	if err != nil {
		return err
	}
	return backend.WriteVirtualText(ctx, targetPath, string(data))
}

func persistPlanDocumentLenient(ctx context.Context, backend Backend, targetPath string, plan *bridge.ChunkGraphPlan) error {
	if backend == nil || plan == nil {
		return fmt.Errorf("missing backend or plan")
	}
	data, err := jsonMarshalIndent(plan)
	if err != nil {
		return err
	}
	return backend.WriteVirtualText(ctx, targetPath, string(data))
}

func planRegistryPath(planPath string) string {
	return path.Join(path.Dir(path.Clean(strings.TrimSpace(planPath))), planRegistryFileName)
}

func loadPlanRegistry(ctx context.Context, backend Backend, planPath string) (*planRegistry, error) {
	registryPath := planRegistryPath(planPath)
	text, err := backend.ReadVirtualText(ctx, registryPath)
	if err != nil || strings.TrimSpace(text) == "" {
		return &planRegistry{Plans: map[string]planRegistryEntry{}}, nil
	}
	var registry planRegistry
	if err := jsonUnmarshal([]byte(text), &registry); err != nil {
		return nil, fmt.Errorf("parse plan registry %s: %w", registryPath, err)
	}
	if registry.Plans == nil {
		registry.Plans = map[string]planRegistryEntry{}
	}
	return &registry, nil
}

func savePlanRegistry(ctx context.Context, backend Backend, planPath string, registry *planRegistry) error {
	if registry == nil {
		registry = &planRegistry{}
	}
	if registry.Plans == nil {
		registry.Plans = map[string]planRegistryEntry{}
	}
	keys := make([]string, 0, len(registry.Plans))
	for key, entry := range registry.Plans {
		key = path.Clean(strings.TrimSpace(key))
		if key == "" {
			continue
		}
		entry.Prev = strings.TrimSpace(entry.Prev)
		entry.Next = strings.TrimSpace(entry.Next)
		if entry.Prev == "" && entry.Next == "" {
			delete(registry.Plans, key)
			continue
		}
		registry.Plans[key] = entry
		keys = append(keys, key)
	}
	sort.Strings(keys)
	data, err := jsonMarshalIndent(registry)
	if err != nil {
		return err
	}
	return backend.WriteVirtualText(ctx, planRegistryPath(planPath), string(data))
}

func readPlanChainLinks(ctx context.Context, backend Backend, planPath string) (planChainLinks, error) {
	registry, err := loadPlanRegistry(ctx, backend, planPath)
	if err != nil {
		return planChainLinks{}, err
	}
	entry := registry.Plans[path.Clean(strings.TrimSpace(planPath))]
	return planChainLinks{
		Prev: strings.TrimSpace(entry.Prev),
		Next: strings.TrimSpace(entry.Next),
	}, nil
}

func setPlanChainLink(ctx context.Context, backend Backend, planPath string, entry planRegistryEntry) error {
	registry, err := loadPlanRegistry(ctx, backend, planPath)
	if err != nil {
		return err
	}
	key := path.Clean(strings.TrimSpace(planPath))
	entry.Prev = pathOrEmpty(entry.Prev)
	entry.Next = pathOrEmpty(entry.Next)
	if entry.Prev == "" && entry.Next == "" {
		delete(registry.Plans, key)
	} else {
		registry.Plans[key] = entry
	}
	return savePlanRegistry(ctx, backend, planPath, registry)
}

func updatePlanLink(ctx context.Context, backend Backend, planPath string, mutate func(planRegistryEntry) planRegistryEntry) error {
	registry, err := loadPlanRegistry(ctx, backend, planPath)
	if err != nil {
		return err
	}
	key := path.Clean(strings.TrimSpace(planPath))
	entry := registry.Plans[key]
	entry = mutate(entry)
	entry.Prev = pathOrEmpty(entry.Prev)
	entry.Next = pathOrEmpty(entry.Next)
	if entry.Prev == "" && entry.Next == "" {
		delete(registry.Plans, key)
	} else {
		registry.Plans[key] = entry
	}
	return savePlanRegistry(ctx, backend, planPath, registry)
}

func linkPlanChain(ctx context.Context, backend Backend, currentPath string, nextPath string) error {
	currentPath = pathOrEmpty(currentPath)
	nextPath = pathOrEmpty(nextPath)
	if currentPath == "" || nextPath == "" {
		return fmt.Errorf("missing current or next plan path")
	}
	if currentPath == nextPath {
		return fmt.Errorf("next plan cannot be the current plan")
	}
	currentLinks, err := readPlanChainLinks(ctx, backend, currentPath)
	if err != nil {
		return err
	}
	nextLinks, err := readPlanChainLinks(ctx, backend, nextPath)
	if err != nil {
		return err
	}
	if currentLinks.Next != "" && currentLinks.Next != nextPath {
		_ = updatePlanLink(ctx, backend, currentLinks.Next, func(entry planRegistryEntry) planRegistryEntry {
			if pathOrEmpty(entry.Prev) == currentPath {
				entry.Prev = ""
			}
			return entry
		})
	}
	if nextLinks.Prev != "" && nextLinks.Prev != currentPath {
		_ = updatePlanLink(ctx, backend, nextLinks.Prev, func(entry planRegistryEntry) planRegistryEntry {
			if pathOrEmpty(entry.Next) == nextPath {
				entry.Next = ""
			}
			return entry
		})
	}
	if err := updatePlanLink(ctx, backend, currentPath, func(entry planRegistryEntry) planRegistryEntry {
		entry.Next = nextPath
		return entry
	}); err != nil {
		return err
	}
	return updatePlanLink(ctx, backend, nextPath, func(entry planRegistryEntry) planRegistryEntry {
		entry.Prev = currentPath
		return entry
	})
}

func detachPlanChain(ctx context.Context, backend Backend, currentPath string) (string, error) {
	currentPath = pathOrEmpty(currentPath)
	if currentPath == "" {
		return "", fmt.Errorf("missing current plan path")
	}
	links, err := readPlanChainLinks(ctx, backend, currentPath)
	if err != nil {
		return "", err
	}
	if links.Next == "" {
		return "", nil
	}
	nextPath := links.Next
	if err := updatePlanLink(ctx, backend, currentPath, func(entry planRegistryEntry) planRegistryEntry {
		entry.Next = ""
		return entry
	}); err != nil {
		return "", err
	}
	if err := updatePlanLink(ctx, backend, nextPath, func(entry planRegistryEntry) planRegistryEntry {
		if pathOrEmpty(entry.Prev) == currentPath {
			entry.Prev = ""
		}
		return entry
	}); err != nil {
		return "", err
	}
	return nextPath, nil
}

func pathOrEmpty(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	return path.Clean(raw)
}

func planNavLabel(planPath string) string {
	planPath = pathOrEmpty(planPath)
	if planPath == "" {
		return "-"
	}
	dir := path.Base(path.Dir(planPath))
	file := path.Base(planPath)
	if dir == "." || dir == "/" || dir == "" {
		return file
	}
	return dir + "/" + file
}
