package bridge

import (
	"cmp"
	"slices"
)

// NormalizeChunkGraphPlan returns a deterministic copy suitable for storage and hashing.
// It does not validate; callers should run ValidateChunkGraphPlan first.
func NormalizeChunkGraphPlan(p *ChunkGraphPlan) *ChunkGraphPlan {
	if p == nil {
		return nil
	}
	out := *p
	if out.ProtocolVersion < 1 {
		out.ProtocolVersion = 1
	}
	out.Chunks = slices.Clone(p.Chunks)
	for i := range out.Chunks {
		c := out.Chunks[i]
		if c.Decision != nil {
			decision := *c.Decision
			if decision.Interaction != nil {
				interaction := *decision.Interaction
				if interaction.LLM != nil {
					llm := *interaction.LLM
					llm.ContextBindings = slices.Clone(llm.ContextBindings)
					interaction.LLM = &llm
				}
				if interaction.Human != nil {
					human := *interaction.Human
					interaction.Human = &human
				}
				decision.Interaction = &interaction
			}
			decision.AllowedBranches = slices.Clone(decision.AllowedBranches)
			c.Decision = &decision
		}
		if c.Inputs != nil {
			c.Inputs = cloneAnyMap(c.Inputs)
		}
		if c.Outputs != nil {
			c.Outputs = cloneAnyMap(c.Outputs)
		}
		c.DependsOn = slices.Clone(c.DependsOn)
		out.Chunks[i] = c
	}
	slices.SortFunc(out.Chunks, func(a, b PlanChunk) int {
		return cmp.Compare(a.ChunkID, b.ChunkID)
	})
	out.Edges = slices.Clone(p.Edges)
	slices.SortFunc(out.Edges, func(a, b PlanEdge) int {
		if x := cmp.Compare(a.FromChunkID, b.FromChunkID); x != 0 {
			return x
		}
		if x := cmp.Compare(a.ToChunkID, b.ToChunkID); x != 0 {
			return x
		}
		if x := cmp.Compare(a.EdgeKind, b.EdgeKind); x != 0 {
			return x
		}
		return cmp.Compare(a.Branch, b.Branch)
	})
	out.EntryChunks = slices.Clone(p.EntryChunks)
	slices.Sort(out.EntryChunks)
	out.Artifacts = slices.Clone(p.Artifacts)
	out.PlannerNotes = slices.Clone(p.PlannerNotes)
	if p.Policies != nil {
		pol := *p.Policies
		out.Policies = &pol
	}
	return &out
}

func cloneAnyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = cloneAnyValue(v)
	}
	return out
}

func cloneAnyValue(v any) any {
	switch typed := v.(type) {
	case map[string]any:
		return cloneAnyMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = cloneAnyValue(item)
		}
		return out
	default:
		return v
	}
}
