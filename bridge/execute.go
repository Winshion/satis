package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"text/template"
	"time"

	"satis/sandbox"
	"satis/satis"
	"satis/vfs"
)

func policiesOrDefault(plan *ChunkGraphPlan) PlanPolicies {
	if plan.Policies != nil {
		return *plan.Policies
	}
	return PlanPolicies{
		FailFast:          true,
		MaxParallelChunks: 1,
	}
}

func retryPolicyOrDefault(chunk PlanChunk) (maxAttempts int, backoff time.Duration) {
	if chunk.RetryPolicy != nil && chunk.RetryPolicy.MaxAttempts > 1 {
		bo := time.Duration(chunk.RetryPolicy.BackoffMS) * time.Millisecond
		if bo <= 0 {
			bo = 200 * time.Millisecond
		}
		return chunk.RetryPolicy.MaxAttempts, bo
	}
	return 1, 0
}

const schedulerTxnRetryAttempts = 4

type decisionLoopLimitError struct {
	DecisionChunkID string
	TargetChunkID   string
	Branch          string
	CurrentCount    int
	MaxIterations   int
}

func (e *decisionLoopLimitError) Error() string {
	return fmt.Sprintf(
		"decision chunk %q branch %q exceeded loop_back max_iterations=%d (count=%d) targeting %q",
		e.DecisionChunkID,
		e.Branch,
		e.MaxIterations,
		e.CurrentCount,
		e.TargetChunkID,
	)
}

func (s *Server) executeRun(ctx context.Context, record *runRecord, options ExecutionOptions) {
	defer func() {
		record.mu.RLock()
		done := record.done
		record.mu.RUnlock()
		if done != nil {
			close(done)
		}
	}()

	record.mu.Lock()
	record.status.Status = RunPhaseRunning
	record.status.UpdatedAt = time.Now().UTC()
	record.mu.Unlock()
	s.mu.Lock()
	s.invalidateRunsCacheLocked()
	s.mu.Unlock()

	s.appendEvent(record, "", EventRunStarted, map[string]any{
		"plan_id": record.plan.PlanID,
	})

	policies := policiesOrDefault(record.plan)

	adj := make(map[string][]string)
	conditionalAdj := make(map[string][]PlanEdge)
	incomingEdges := make(map[string][]PlanEdge)
	conditionalBackEdges := make(map[string]struct{})
	terminalLeafChunks := collectTerminalLeafChunkIDs(record.plan)
	remainingDeps := make(map[string]int, len(record.plan.Chunks))
	chunkByID := make(map[string]PlanChunk, len(record.plan.Chunks))
	for _, chunk := range record.plan.Chunks {
		chunkByID[chunk.ChunkID] = chunk
		remainingDeps[chunk.ChunkID] = 0
	}
	for i, edge := range record.plan.Edges {
		if isConditionalEdgeKind(edge.EdgeKind) && edgeCreatesBackReference(record.plan.Edges, i) {
			conditionalBackEdges[edgeIdentity(edge)] = struct{}{}
		}
	}
	for _, edge := range record.plan.Edges {
		incomingEdges[edge.ToChunkID] = append(incomingEdges[edge.ToChunkID], edge)
		if isConditionalEdgeKind(edge.EdgeKind) {
			conditionalAdj[edge.FromChunkID] = append(conditionalAdj[edge.FromChunkID], edge)
			if _, ok := conditionalBackEdges[edgeIdentity(edge)]; !ok {
				remainingDeps[edge.ToChunkID]++
			}
			continue
		}
		adj[edge.FromChunkID] = append(adj[edge.FromChunkID], edge.ToChunkID)
		remainingDeps[edge.ToChunkID]++
	}

	h := new(chunkMinHeap)
	record.mu.RLock()
	statuses := cloneChunkStatuses(record.status.ChunkStatuses)
	record.mu.RUnlock()
	for _, chunk := range record.plan.Chunks {
		phase := statuses[chunk.ChunkID]
		if phase == ChunkPhaseSucceeded || phase == ChunkPhaseFailed || phase == ChunkPhaseBlocked || phase == ChunkPhaseCancelled {
			continue
		}
		remaining := 0
		for _, edge := range incomingEdges[chunk.ChunkID] {
			if _, ok := conditionalBackEdges[edgeIdentity(edge)]; ok {
				continue
			}
			if !edgeDependencySatisfied(record, statuses, edge) {
				remaining++
			}
		}
		remainingDeps[chunk.ChunkID] = remaining
		if remaining <= 0 {
			pushChunkID(h, chunk.ChunkID)
			s.setChunkPhase(record, chunk.ChunkID, ChunkPhaseReady, true)
			s.appendEvent(record, chunk.ChunkID, EventChunkReady, map[string]any{})
		}
	}

	failedChunks := make(map[string]bool)
	hasAnyFailed := false
	scheduler := schedulerOrDefault(s.Scheduler)
	batchSize := policies.MaxParallelChunks
	if batchSize <= 0 {
		batchSize = 1
	}

	for h.Len() > 0 {
		select {
		case <-ctx.Done():
			s.failRun(record, RunPhaseCancelled, &StructuredError{
				Code:      CodeRunCancelled,
				Message:   "run cancelled",
				Stage:     StageScheduling,
				Retryable: false,
				Details: map[string]any{
					"plan_id": record.plan.PlanID,
					"run_id":  record.status.RunID,
				},
			})
			return
		default:
		}

		readyIDs := make([]string, 0, batchSize)
		for h.Len() > 0 && len(readyIDs) < batchSize {
			readyIDs = append(readyIDs, popChunkID(h))
		}
		tasks := make([]BatchTask, 0, len(readyIDs))
		for _, chunkID := range readyIDs {
			chunk := chunkByID[chunkID]
			chunkIDCopy := chunkID
			chunkCopy := chunk
			tasks = append(tasks, BatchTask{
				ID:        chunkIDCopy,
				Kind:      BatchTaskKindChunk,
				Priority:  100,
				CostLevel: estimateChunkCostLevel(chunkCopy),
				Meta: map[string]string{
					"chunk_id": chunkIDCopy,
				},
				ExecFn: func(runCtx context.Context) (any, error) {
					return nil, runWithTxnRetry(runCtx, func(retryCtx context.Context) error {
						return s.executeChunkWithRetry(retryCtx, record, chunkCopy, options)
					})
				},
			})
		}
		results := scheduler.Schedule(ctx, tasks)
		completedLeafThisBatch := ""
		for i, result := range results {
			chunkID := readyIDs[i]
			if result.Err != nil {
				failedChunks[chunkID] = true
				hasAnyFailed = true

				if policies.FailFast {
					s.failRun(record, RunPhaseFailed, &StructuredError{
						Code:    CodeChunkExecutionFailed,
						Message: result.Err.Error(),
						Stage:   StageExecution,
						Details: map[string]any{
							"chunk_id":  chunkID,
							"plan_id":   record.plan.PlanID,
							"run_id":    record.status.RunID,
							"fail_fast": true,
						},
					})
					s.appendEvent(record, chunkID, EventChunkFailed, map[string]any{
						"error": result.Err.Error(),
					})
					return
				}

				s.appendEvent(record, chunkID, EventChunkFailed, map[string]any{
					"error": result.Err.Error(),
				})
				s.blockDescendants(record, chunkID, adj, remainingDeps, chunkByID, failedChunks)
				continue
			}

			if err := s.activateConditionalSuccessors(record, chunkID, chunkByID[chunkID], conditionalAdj, incomingEdges, conditionalBackEdges, remainingDeps, h); err != nil {
				var loopLimitErr *decisionLoopLimitError
				if errors.As(err, &loopLimitErr) {
					s.handleDecisionLoopLimitExceeded(record, loopLimitErr)
					return
				}
				failedChunks[chunkID] = true
				hasAnyFailed = true
				s.appendEvent(record, chunkID, EventChunkFailed, map[string]any{
					"error": err.Error(),
				})
				if policies.FailFast {
					s.failRun(record, RunPhaseFailed, &StructuredError{
						Code:    CodeChunkExecutionFailed,
						Message: err.Error(),
						Stage:   StageExecution,
						Details: map[string]any{
							"chunk_id": chunkID,
							"plan_id":  record.plan.PlanID,
							"run_id":   record.status.RunID,
						},
					})
					return
				}
				s.blockDescendants(record, chunkID, adj, remainingDeps, chunkByID, failedChunks)
				continue
			}
			s.syncDynamicPlanState(record, chunkByID, remainingDeps, adj, conditionalAdj)

			for _, next := range adj[chunkID] {
				remainingDeps[next]--
				if remainingDeps[next] <= 0 {
					pushChunkID(h, next)
					s.setChunkPhase(record, next, ChunkPhaseReady, true)
					s.appendEvent(record, next, EventChunkReady, map[string]any{})
				}
			}
			if _, ok := terminalLeafChunks[chunkID]; ok {
				completedLeafThisBatch = chunkID
			}
		}
		// Only short-circuit when the ready heap is empty: if any chunk is still
		// queued it must execute first. This guards against a race where a newly
		// promoted chunk is both pushed onto the heap and visible as a terminal leaf
		// in Pending/Ready state, which would cause it to be auto-completed before
		// it actually runs.
		if completedLeafThisBatch != "" && !hasAnyFailed && h.Len() == 0 && activeWorkIsLimitedToTerminalLeaves(record, terminalLeafChunks) {
			s.autoCompleteTerminalLeaves(record, completedLeafThisBatch, terminalLeafChunks)
			s.transitionRunToPlanningPending(record)
			return
		}
	}

	if hasAnyFailed {
		failedIDs, blockedIDs := collectFailedAndBlockedChunkIDs(record.status.ChunkStatuses)
		s.failRun(record, RunPhaseFailed, &StructuredError{
			Code:    CodeRunPartialFailure,
			Message: "one or more chunks failed",
			Stage:   StageExecution,
			Details: map[string]any{
				"plan_id":           record.plan.PlanID,
				"run_id":            record.status.RunID,
				"fail_fast":         false,
				"failed_chunk_ids":  failedIDs,
				"blocked_chunk_ids": blockedIDs,
			},
		})
		return
	}

	s.transitionRunToPlanningPending(record)
}

// blockDescendants marks all transitive descendants of a failed chunk as blocked.
func (s *Server) blockDescendants(
	record *runRecord,
	failedID string,
	adj map[string][]string,
	remainingDeps map[string]int,
	chunkByID map[string]PlanChunk,
	failedChunks map[string]bool,
) {
	queue := adj[failedID]
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if failedChunks[next] {
			continue
		}
		failedChunks[next] = true
		s.setChunkPhase(record, next, ChunkPhaseBlocked, true)
		s.appendEvent(record, next, EventChunkBlocked, map[string]any{
			"reason": fmt.Sprintf("ancestor %s failed", failedID),
		})
		queue = append(queue, adj[next]...)
	}
}

func (s *Server) executeChunkWithRetry(ctx context.Context, record *runRecord, chunk PlanChunk, options ExecutionOptions) error {
	maxAttempts, backoff := retryPolicyOrDefault(chunk)

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		lastErr = s.executeChunk(ctx, record, chunk, options, attempt)
		if lastErr == nil {
			return nil
		}
		if attempt < maxAttempts {
			s.appendEvent(record, chunk.ChunkID, EventChunkFailed, map[string]any{
				"error":   lastErr.Error(),
				"attempt": attempt,
				"retry":   true,
			})
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			s.setChunkPhase(record, chunk.ChunkID, ChunkPhaseReady, true)
		}
	}
	return lastErr
}

func (s *Server) executeChunk(ctx context.Context, record *runRecord, chunk PlanChunk, options ExecutionOptions, attempt int) error {
	now := time.Now().UTC()

	record.mu.Lock()
	result := record.chunks[chunk.ChunkID]
	result.Status = ChunkPhaseRunning
	result.StartedAt = &now
	record.chunks[chunk.ChunkID] = result
	record.status.ChunkStatuses[chunk.ChunkID] = ChunkPhaseRunning
	record.status.UpdatedAt = now
	record.mu.Unlock()
	s.mu.Lock()
	s.invalidateRunsCacheLocked()
	s.mu.Unlock()

	s.appendEvent(record, chunk.ChunkID, EventChunkStarted, map[string]any{
		"kind":    chunk.Kind,
		"attempt": attempt,
	})

	switch normalizedChunkKind(chunk.Kind) {
	case "decision":
		return s.executeDecisionChunk(ctx, record, chunk, options, attempt)
	}

	if options.DryRun {
		supplementaryInfo, err := collectChunkSupplementaryInfo(chunk)
		if err != nil {
			return err
		}
		finished := time.Now().UTC()
		record.mu.Lock()
		result := record.chunks[chunk.ChunkID]
		result.Status = ChunkPhaseSucceeded
		result.FinishedAt = &finished
		result.SupplementaryInfo = cloneAnyMap(supplementaryInfo)
		record.chunks[chunk.ChunkID] = result
		record.status.ChunkStatuses[chunk.ChunkID] = ChunkPhaseSucceeded
		record.status.UpdatedAt = finished
		record.mu.Unlock()
		s.mu.Lock()
		s.invalidateRunsCacheLocked()
		s.mu.Unlock()
		s.appendEvent(record, chunk.ChunkID, EventChunkSucceeded, map[string]any{
			"dry_run": true,
		})
		return nil
	}

	if chunk.Repeat != nil {
		switch chunk.Repeat.Mode {
		case "per_item":
			return s.executeRepeatedChunk(ctx, record, chunk, options, attempt)
		case "batch":
			return s.executeBatchRepeatChunk(ctx, record, chunk, options, attempt)
		}
	}

	supplementaryInfo, err := collectChunkSupplementaryInfo(chunk)
	if err != nil {
		return err
	}

	execCtx := ctx
	if chunk.TimeoutMS > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, time.Duration(chunk.TimeoutMS)*time.Millisecond)
		defer cancel()
	}

	executor := *s.executor
	inputBindings, err := s.resolveChunkInputBindings(record, chunk)
	var execResult *satis.ExecutionResult
	if err == nil {
		executor.InputBindings = inputBindings
		executor.Invoker = s.invoker
		execResult, err = executor.ParseValidateExecute(execCtx, chunk.Source.SatisText)
	}
	if err != nil {
		finished := time.Now().UTC()
		code := CodeChunkExecutionFailed
		if execCtx.Err() == context.DeadlineExceeded {
			code = CodeChunkTimeout
		}
		record.mu.Lock()
		result := record.chunks[chunk.ChunkID]
		result.Status = ChunkPhaseFailed
		result.FinishedAt = &finished
		result.SupplementaryInfo = cloneAnyMap(supplementaryInfo)
		result.Error = &StructuredError{
			Code:      code,
			Message:   err.Error(),
			Stage:     StageExecution,
			Retryable: code == CodeChunkTimeout,
			Details: map[string]any{
				"chunk_id":   chunk.ChunkID,
				"plan_id":    record.plan.PlanID,
				"run_id":     record.status.RunID,
				"attempt":    attempt,
				"timeout_ms": chunk.TimeoutMS,
				"dry_run":    options.DryRun,
			},
		}
		record.chunks[chunk.ChunkID] = result
		record.status.ChunkStatuses[chunk.ChunkID] = ChunkPhaseFailed
		record.status.UpdatedAt = finished
		record.mu.Unlock()
		s.mu.Lock()
		s.invalidateRunsCacheLocked()
		s.mu.Unlock()
		return err
	}

	finished := time.Now().UTC()
	objects := make(map[string]any, len(execResult.Objects))
	discoveredArtifacts := make([]ArtifactSpec, 0, len(execResult.Objects))
	for name, ref := range execResult.Objects {
		objects[name] = ref
		discoveredArtifacts = append(discoveredArtifacts, ArtifactSpec{
			Name:        name,
			VirtualPath: ref.VirtualPath,
			FileID:      string(ref.FileID),
		})
		s.appendEvent(record, chunk.ChunkID, EventObjectChanged, map[string]any{
			"name":         name,
			"file_id":      ref.FileID,
			"virtual_path": ref.VirtualPath,
		})
	}

	record.mu.Lock()
	result = record.chunks[chunk.ChunkID]
	result.Status = ChunkPhaseSucceeded
	result.FinishedAt = &finished
	result.Error = nil
	result.SupplementaryInfo = cloneAnyMap(supplementaryInfo)
	result.Values = cloneStringMap(execResult.Values)
	result.Vars = cloneRuntimeBindingMap(execResult.Vars)
	result.Notes = append([]string(nil), execResult.Notes...)
	result.Objects = objects
	result.ArtifactsEmitted = append([]ArtifactSpec(nil), discoveredArtifacts...)
	record.chunks[chunk.ChunkID] = result
	record.status.ChunkStatuses[chunk.ChunkID] = ChunkPhaseSucceeded
	record.status.UpdatedAt = finished
	record.artifacts = append(record.artifacts, discoveredArtifacts...)
	s.refreshWorkflowRegistryLocked(record, chunk.ChunkID, result.Vars)
	record.mu.Unlock()
	s.mu.Lock()
	s.invalidateRunsCacheLocked()
	s.mu.Unlock()

	for _, artifact := range discoveredArtifacts {
		s.appendEvent(record, chunk.ChunkID, EventArtifactEmitted, map[string]any{
			"name":         artifact.Name,
			"file_id":      artifact.FileID,
			"virtual_path": artifact.VirtualPath,
		})
	}
	s.appendEvent(record, chunk.ChunkID, EventChunkSucceeded, map[string]any{
		"notes": result.Notes,
	})
	return nil
}

func (s *Server) resolveChunkInputBindings(record *runRecord, chunk PlanChunk) (map[string]satis.RuntimeBinding, error) {
	specs, err := declaredHandoffInputs(chunk)
	if err != nil {
		return nil, err
	}
	if len(specs) == 0 {
		return nil, nil
	}

	bindings := make(map[string]satis.RuntimeBinding, len(specs))
	for alias, spec := range specs {
		record.mu.RLock()
		upstream, ok := record.chunks[spec.FromStep]
		record.mu.RUnlock()
		if !ok {
			return nil, fmt.Errorf("chunk %q input binding %q references unknown upstream chunk %q", chunk.ChunkID, alias, spec.FromStep)
		}
		var value satis.RuntimeBinding
		var found bool
		upstreamAlias := alias
		value, found = upstream.Vars[upstreamAlias]
		if !found {
			if text, ok := upstream.Values[upstreamAlias]; ok {
				value = satis.RuntimeBinding{Kind: "text", Text: text}
				found = true
			} else if ref, ok := anyToFileRef(upstream.Objects[upstreamAlias]); ok {
				value = satis.RuntimeBinding{Kind: "object", Object: &ref}
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("chunk %q input binding %q could not resolve upstream value from %q", chunk.ChunkID, alias, spec.FromStep)
		}
		binding := cloneRuntimeBinding(value)
		bindings[alias] = binding
	}
	return bindings, nil
}

func (s *Server) executeDecisionChunk(ctx context.Context, record *runRecord, chunk PlanChunk, options ExecutionOptions, attempt int) error {
	branch, payload, err := s.executeControlChunk(ctx, record, chunk, options, attempt, chunk.Decision.Interaction, chunk.Decision.AllowedBranches, chunk.Decision.DefaultBranch, CodeDecisionExecutionFailed)
	if err != nil {
		return err
	}
	s.storeControlResult(record, chunk.ChunkID, payload, map[string]string{
		"selected_branch": branch,
		"control_kind":    "decision",
	})
	s.appendEvent(record, chunk.ChunkID, EventChunkSucceeded, map[string]any{"selected_branch": branch, "control_kind": "decision"})
	return nil
}

func (s *Server) executeControlChunk(ctx context.Context, record *runRecord, chunk PlanChunk, options ExecutionOptions, attempt int, interaction *InteractionSpec, allowed []string, defaultBranch string, errorCode string) (string, map[string]any, error) {
	supplementaryInfo, err := collectChunkSupplementaryInfo(chunk)
	if err != nil {
		return "", nil, err
	}
	if options.DryRun {
		payload := map[string]any{"branch": fallbackChoice(allowed, defaultBranch)}
		s.markChunkSucceeded(record, chunk.ChunkID, supplementaryInfo, nil, payload)
		return payload["branch"].(string), payload, nil
	}
	if interaction == nil {
		return "", nil, fmt.Errorf("chunk %q missing interaction config", chunk.ChunkID)
	}
	inputBindings, err := s.resolveChunkInputBindings(record, chunk)
	if err != nil {
		return "", nil, s.failControlChunk(record, chunk.ChunkID, supplementaryInfo, err, errorCode, attempt)
	}
	mode := strings.TrimSpace(interaction.Mode)
	switch mode {
	case "human":
		payload, branch, err := s.executeHumanControl(ctx, record, chunk, interaction, allowed, defaultBranch)
		if err != nil {
			return "", nil, s.failControlChunk(record, chunk.ChunkID, supplementaryInfo, err, errorCode, attempt)
		}
		s.markChunkSucceeded(record, chunk.ChunkID, supplementaryInfo, nil, payload)
		return branch, payload, nil
	case "llm", "llm_then_human":
		if s.invoker == nil {
			if mode == "llm_then_human" {
				payload, branch, err := s.executeHumanControl(ctx, record, chunk, interaction, allowed, defaultBranch)
				if err != nil {
					return "", nil, s.failControlChunk(record, chunk.ChunkID, supplementaryInfo, fmt.Errorf("invoker is not configured and human fallback failed: %w", err), errorCode, attempt)
				}
				s.markChunkSucceeded(record, chunk.ChunkID, supplementaryInfo, nil, payload)
				return branch, payload, nil
			}
			return "", nil, s.failControlChunk(record, chunk.ChunkID, supplementaryInfo, fmt.Errorf("invoker is not configured"), errorCode, attempt)
		}
		prompt, err := renderControlPrompt(interaction.LLM, inputBindings, allowed)
		if err != nil {
			if mode == "llm_then_human" {
				payload, branch, humanErr := s.executeHumanControl(ctx, record, chunk, interaction, allowed, defaultBranch)
				if humanErr != nil {
					return "", nil, s.failControlChunk(record, chunk.ChunkID, supplementaryInfo, fmt.Errorf("llm prompt render failed and human fallback failed: %w", err), errorCode, attempt)
				}
				s.markChunkSucceeded(record, chunk.ChunkID, supplementaryInfo, nil, payload)
				return branch, payload, nil
			}
			return "", nil, s.failControlChunk(record, chunk.ChunkID, supplementaryInfo, err, errorCode, attempt)
		}
		raw, err := s.invoker.Invoke(ctx, prompt, "")
		if err != nil {
			if mode == "llm_then_human" {
				payload, branch, humanErr := s.executeHumanControl(ctx, record, chunk, interaction, allowed, defaultBranch)
				if humanErr != nil {
					return "", nil, s.failControlChunk(record, chunk.ChunkID, supplementaryInfo, fmt.Errorf("llm interaction failed and human fallback failed: %w", err), errorCode, attempt)
				}
				s.markChunkSucceeded(record, chunk.ChunkID, supplementaryInfo, nil, payload)
				return branch, payload, nil
			}
			return "", nil, s.failControlChunk(record, chunk.ChunkID, supplementaryInfo, err, errorCode, attempt)
		}
		payload, branch, err := parseControlOutput(raw, allowed, defaultBranch)
		if err != nil {
			if mode == "llm_then_human" {
				humanPayload, humanBranch, humanErr := s.executeHumanControl(ctx, record, chunk, interaction, allowed, defaultBranch)
				if humanErr != nil {
					return "", nil, s.failControlChunk(record, chunk.ChunkID, supplementaryInfo, fmt.Errorf("llm output parse failed and human fallback failed: %w", err), errorCode, attempt)
				}
				s.markChunkSucceeded(record, chunk.ChunkID, supplementaryInfo, nil, humanPayload)
				return humanBranch, humanPayload, nil
			}
			return "", nil, s.failControlChunk(record, chunk.ChunkID, supplementaryInfo, err, errorCode, attempt)
		}
		s.markChunkSucceeded(record, chunk.ChunkID, supplementaryInfo, nil, payload)
		return branch, payload, nil
	default:
		return "", nil, s.failControlChunk(record, chunk.ChunkID, supplementaryInfo, fmt.Errorf("unsupported interaction mode %q", mode), errorCode, attempt)
	}
}

func (s *Server) executeHumanControl(
	ctx context.Context,
	record *runRecord,
	chunk PlanChunk,
	interaction *InteractionSpec,
	allowed []string,
	defaultBranch string,
) (map[string]any, string, error) {
	inputBindings, err := s.resolveChunkInputBindings(record, chunk)
	if err != nil {
		return nil, "", err
	}
	title := "Human Decision"
	description := ""
	if interaction != nil && interaction.Human != nil {
		if v := strings.TrimSpace(interaction.Human.Title); v != "" {
			title = v
		}
		description = strings.TrimSpace(interaction.Human.Description)
	}
	setChunkSubstatus(record, chunk.ChunkID, "waiting_human")
	req := HumanControlRequest{
		RunID:           record.status.RunID,
		ChunkID:         chunk.ChunkID,
		ControlKind:     normalizedChunkKind(chunk.Kind),
		Title:           title,
		Description:     description,
		AllowedBranches: append([]string(nil), allowed...),
		DefaultBranch:   defaultBranch,
		InputBindings:   inputBindings,
	}
	pending := s.queueHumanControlRequest(ctx, req)
	defer s.clearHumanControlRequest(record.status.RunID, chunk.ChunkID, pending)
	var response humanControlResponse
	select {
	case response = <-pending.responseCh:
	case <-ctx.Done():
		clearChunkSubstatus(record, chunk.ChunkID)
		return nil, "", ctx.Err()
	}
	clearChunkSubstatus(record, chunk.ChunkID)
	choice, err := response.choice, response.err
	if err != nil {
		return nil, "", err
	}
	payload := cloneAnyMap(choice.Payload)
	if payload == nil {
		payload = make(map[string]any)
	}
	branch := strings.TrimSpace(choice.Branch)
	if branch == "" {
		if v, _ := payload["branch"].(string); strings.TrimSpace(v) != "" {
			branch = strings.TrimSpace(v)
		}
	}
	if branch == "" {
		branch = fallbackChoice(allowed, defaultBranch)
	}
	payload["branch"] = branch
	if branch != "" && !slices.Contains(allowed, branch) {
		return nil, "", fmt.Errorf("human interaction selected unsupported branch %q", branch)
	}
	payload["source"] = "human"
	return payload, branch, nil
}

func renderControlPrompt(spec *LLMInteraction, bindings map[string]satis.RuntimeBinding, allowed []string) (string, error) {
	if spec == nil {
		return "", fmt.Errorf("missing llm interaction config")
	}
	prompt := spec.UserPromptTemplate
	for _, binding := range spec.ContextBindings {
		value := controlPromptBindingText(bindings[inputAliasName(binding.FromInput, "body")])
		if value == "" {
			value = controlPromptBindingText(bindings[binding.FromInput])
		}
		prompt = strings.ReplaceAll(prompt, "{{context."+binding.Name+"}}", value)
		prompt = strings.ReplaceAll(prompt, "{{"+binding.Name+"}}", value)
	}
	if strings.Contains(prompt, "{{allowed_branches}}") {
		prompt = strings.ReplaceAll(prompt, "{{allowed_branches}}", strings.Join(allowed, ", "))
	}
	if spec.SystemPromptRef != "" {
		prompt = fmt.Sprintf("System prompt ref: %s\n%s", spec.SystemPromptRef, prompt)
	}
	if spec.DeveloperPromptRef != "" {
		prompt = fmt.Sprintf("Developer prompt ref: %s\n%s", spec.DeveloperPromptRef, prompt)
	}
	return prompt, nil
}

func controlPromptBindingText(binding satis.RuntimeBinding) string {
	switch binding.Kind {
	case "text":
		return binding.Text
	case "text_list":
		return strings.Join(binding.Texts, "\n")
	case "object":
		if binding.Object != nil {
			return binding.Object.VirtualPath
		}
	case "object_list":
		paths := make([]string, 0, len(binding.Objects))
		for _, ref := range binding.Objects {
			paths = append(paths, ref.VirtualPath)
		}
		return strings.Join(paths, "\n")
	case "conversation":
		lines := make([]string, 0, len(binding.Conversation))
		for _, msg := range binding.Conversation {
			lines = append(lines, msg.Role+": "+msg.Content)
		}
		return strings.Join(lines, "\n")
	}
	return ""
}

func parseControlOutput(raw string, allowed []string, defaultBranch string) (map[string]any, string, error) {
	trimmed := strings.TrimSpace(raw)
	payload := map[string]any{}
	if strings.HasPrefix(trimmed, "{") {
		if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
			return nil, "", fmt.Errorf("invalid control json: %w", err)
		}
	} else {
		payload["branch"] = trimmed
	}
	branch, _ := payload["branch"].(string)
	branch = strings.TrimSpace(branch)
	if branch == "" {
		if len(allowed) > 0 || strings.TrimSpace(defaultBranch) != "" {
			branch = fallbackChoice(allowed, defaultBranch)
		}
		payload["branch"] = branch
	}
	if branch != "" && !slices.Contains(allowed, branch) {
		return nil, "", fmt.Errorf("control output selected unsupported branch %q", branch)
	}
	return payload, branch, nil
}

func fallbackChoice(allowed []string, fallback string) string {
	if strings.TrimSpace(fallback) != "" {
		return fallback
	}
	if len(allowed) > 0 {
		return allowed[0]
	}
	return ""
}

func (s *Server) failControlChunk(record *runRecord, chunkID string, supplementaryInfo map[string]any, err error, code string, attempt int) error {
	finished := time.Now().UTC()
	record.mu.Lock()
	result := record.chunks[chunkID]
	result.Status = ChunkPhaseFailed
	result.FinishedAt = &finished
	result.SupplementaryInfo = cloneAnyMap(supplementaryInfo)
	result.Error = &StructuredError{
		Code:      code,
		Message:   err.Error(),
		Stage:     StageExecution,
		Retryable: false,
		Details: map[string]any{
			"chunk_id": chunkID,
			"attempt":  attempt,
		},
	}
	record.chunks[chunkID] = result
	record.status.ChunkStatuses[chunkID] = ChunkPhaseFailed
	record.status.UpdatedAt = finished
	record.mu.Unlock()
	return err
}

func (s *Server) markChunkSucceeded(record *runRecord, chunkID string, supplementaryInfo map[string]any, values map[string]string, payload map[string]any) {
	finished := time.Now().UTC()
	record.mu.Lock()
	result := record.chunks[chunkID]
	result.Status = ChunkPhaseSucceeded
	result.FinishedAt = &finished
	result.Error = nil
	result.SupplementaryInfo = cloneAnyMap(supplementaryInfo)
	if len(values) > 0 {
		result.Values = cloneStringMap(values)
		result.Vars = cloneRuntimeBindingMap(result.Vars)
		if result.Vars == nil {
			result.Vars = map[string]satis.RuntimeBinding{}
		}
		for key, value := range values {
			result.Vars[key] = satis.RuntimeBinding{Kind: "text", Text: value}
		}
	}
	if len(payload) > 0 {
		for key, value := range payload {
			if result.SupplementaryInfo == nil {
				result.SupplementaryInfo = map[string]any{}
			}
			result.SupplementaryInfo[key] = value
		}
	}
	record.chunks[chunkID] = result
	record.status.ChunkStatuses[chunkID] = ChunkPhaseSucceeded
	record.status.UpdatedAt = finished
	s.refreshWorkflowRegistryLocked(record, chunkID, result.Vars)
	record.mu.Unlock()
	s.mu.Lock()
	s.invalidateRunsCacheLocked()
	s.mu.Unlock()
}

func setChunkSubstatus(record *runRecord, chunkID string, substatus string) {
	now := time.Now().UTC()
	record.mu.Lock()
	defer record.mu.Unlock()
	result := record.chunks[chunkID]
	result.Substatus = substatus
	record.chunks[chunkID] = result
	record.status.UpdatedAt = now
}

func clearChunkSubstatus(record *runRecord, chunkID string) {
	record.mu.Lock()
	defer record.mu.Unlock()
	result := record.chunks[chunkID]
	result.Substatus = ""
	record.chunks[chunkID] = result
	record.status.UpdatedAt = time.Now().UTC()
}

func (s *Server) storeControlResult(record *runRecord, chunkID string, payload map[string]any, extra map[string]string) {
	record.mu.Lock()
	defer record.mu.Unlock()
	result := record.chunks[chunkID]
	if result.Values == nil {
		result.Values = map[string]string{}
	}
	if result.Vars == nil {
		result.Vars = map[string]satis.RuntimeBinding{}
	}
	for key, value := range extra {
		result.Values[key] = value
		result.Vars[key] = satis.RuntimeBinding{Kind: "text", Text: value}
	}
	for key, value := range payload {
		if str, ok := value.(string); ok {
			result.Values[key] = str
			result.Vars[key] = satis.RuntimeBinding{Kind: "text", Text: str}
		}
	}
	mergedControl := cloneAnyMap(result.Control)
	if mergedControl == nil {
		mergedControl = map[string]any{}
	}
	for key, value := range payload {
		mergedControl[key] = cloneAnyValue(value)
	}
	result.Control = mergedControl
	record.chunks[chunkID] = result
	s.refreshWorkflowRegistryLocked(record, chunkID, result.Vars)
}

func (s *Server) activateConditionalSuccessors(record *runRecord, chunkID string, chunk PlanChunk, conditionalAdj map[string][]PlanEdge, incomingEdges map[string][]PlanEdge, conditionalBackEdges map[string]struct{}, remainingDeps map[string]int, h *chunkMinHeap) error {
	edges := conditionalAdj[chunkID]
	if len(edges) == 0 {
		return nil
	}
	record.mu.RLock()
	result := record.chunks[chunkID]
	record.mu.RUnlock()
	selected := result.Values["selected_branch"]
	switch normalizedChunkKind(chunk.Kind) {
	case "decision":
		var matchedLoopBack *PlanEdge
		for _, edge := range edges {
			switch strings.ToLower(strings.TrimSpace(edge.EdgeKind)) {
			case "branch":
				if edge.Branch == selected {
					remainingDeps[edge.ToChunkID]--
					if remainingDeps[edge.ToChunkID] <= 0 {
						pushChunkID(h, edge.ToChunkID)
						s.setChunkPhase(record, edge.ToChunkID, ChunkPhaseReady, true)
						s.appendEvent(record, edge.ToChunkID, EventChunkReady, map[string]any{"selected_by": chunkID, "branch": selected})
					}
					return nil
				}
			case "loop_back":
				if edge.Branch == selected {
					edgeCopy := edge
					matchedLoopBack = &edgeCopy
				}
			case "default":
				// use only if no branch edge matched
			}
		}
		if matchedLoopBack != nil {
			count, maxIterations, exceeded := s.bumpLoopBackIteration(record, *matchedLoopBack)
			if exceeded {
				return &decisionLoopLimitError{
					DecisionChunkID: chunkID,
					TargetChunkID:   matchedLoopBack.ToChunkID,
					Branch:          selected,
					CurrentCount:    count,
					MaxIterations:   maxIterations,
				}
			}
			resetChunkIDs := resetLoopBackCorridor(record, matchedLoopBack.ToChunkID, chunkID)
			record.mu.RLock()
			statuses := cloneChunkStatuses(record.status.ChunkStatuses)
			record.mu.RUnlock()
			for resetChunkID := range resetChunkIDs {
				if resetChunkID == matchedLoopBack.ToChunkID {
					continue
				}
				remaining := 0
				for _, edge := range incomingEdges[resetChunkID] {
					if _, ok := conditionalBackEdges[edgeIdentity(edge)]; ok {
						continue
					}
					if !edgeDependencySatisfied(record, statuses, edge) {
						remaining++
					}
				}
				remainingDeps[resetChunkID] = remaining
			}
			remainingDeps[matchedLoopBack.ToChunkID] = 0
			pushChunkID(h, matchedLoopBack.ToChunkID)
			s.setChunkPhase(record, matchedLoopBack.ToChunkID, ChunkPhaseReady, true)
			s.appendEvent(record, matchedLoopBack.ToChunkID, EventChunkReady, map[string]any{"selected_by": chunkID, "branch": selected, "loop_back": true})
			return nil
		}
		for _, edge := range edges {
			if strings.ToLower(strings.TrimSpace(edge.EdgeKind)) == "default" {
				remainingDeps[edge.ToChunkID]--
				if remainingDeps[edge.ToChunkID] <= 0 {
					pushChunkID(h, edge.ToChunkID)
					s.setChunkPhase(record, edge.ToChunkID, ChunkPhaseReady, true)
					s.appendEvent(record, edge.ToChunkID, EventChunkReady, map[string]any{"selected_by": chunkID, "branch": "default"})
				}
				return nil
			}
		}
		return fmt.Errorf("decision chunk %q selected branch %q but no matching edge exists", chunkID, selected)
	default:
		return nil
	}
}

func edgeDependencySatisfied(record *runRecord, statuses map[string]ChunkPhase, edge PlanEdge) bool {
	sourceStatus := statuses[edge.FromChunkID]
	if sourceStatus != ChunkPhaseSucceeded {
		return false
	}
	if !isConditionalEdgeKind(edge.EdgeKind) {
		return true
	}
	record.mu.RLock()
	result := record.chunks[edge.FromChunkID]
	record.mu.RUnlock()
	switch strings.ToLower(strings.TrimSpace(edge.EdgeKind)) {
	case "branch":
		return result.Values["selected_branch"] == edge.Branch
	case "default":
		return result.Values["selected_branch"] == "" || result.Values["selected_branch"] == "default"
	case "loop_back":
		return true
	default:
		return false
	}
}

func parsePlanFragmentFromPayload(payload map[string]any) (PlanFragment, bool, error) {
	raw, ok := payload["fragment"]
	if !ok || raw == nil {
		return PlanFragment{}, false, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return PlanFragment{}, false, err
	}
	var fragment PlanFragment
	if err := json.Unmarshal(data, &fragment); err != nil {
		return PlanFragment{}, false, fmt.Errorf("invalid plan fragment: %w", err)
	}
	return normalizePlanFragment(fragment)
}

func normalizePlanFragment(fragment PlanFragment) (PlanFragment, bool, error) {
	if len(fragment.NewNodes) > 0 {
		fragment.Chunks = append([]PlanChunk(nil), fragment.NewNodes...)
	}
	if len(fragment.NewEdges) > 0 {
		fragment.Edges = append([]PlanEdge(nil), fragment.NewEdges...)
	}
	if strings.TrimSpace(fragment.EntryNode) != "" {
		fragment.EntryChunks = []string{strings.TrimSpace(fragment.EntryNode)}
	}
	if len(fragment.Chunks) == 0 && len(fragment.Edges) == 0 && len(fragment.EntryChunks) == 0 {
		return PlanFragment{}, false, nil
	}
	if len(fragment.Chunks) == 0 {
		return PlanFragment{}, true, fmt.Errorf("plan fragment must add at least one chunk")
	}
	if len(fragment.EntryChunks) != 1 {
		return PlanFragment{}, true, fmt.Errorf("plan fragment must declare exactly one entry chunk")
	}
	return fragment, true, nil
}

func (s *Server) attachPlanFragment(record *runRecord, plannerChunkID string, fragment PlanFragment) error {
	normalized, ok, err := normalizePlanFragment(fragment)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("plan fragment must not be empty")
	}
	record.mu.Lock()
	defer record.mu.Unlock()
	existing := make(map[string]struct{}, len(record.plan.Chunks))
	for _, chunk := range record.plan.Chunks {
		existing[chunk.ChunkID] = struct{}{}
	}
	for _, chunk := range normalized.Chunks {
		if _, ok := existing[chunk.ChunkID]; ok {
			return fmt.Errorf("plan fragment chunk_id %q already exists", chunk.ChunkID)
		}
	}
	backEdgeCount := 0
	for _, edge := range normalized.Edges {
		if _, ok := existing[edge.FromChunkID]; !ok {
			found := false
			for _, chunk := range normalized.Chunks {
				if chunk.ChunkID == edge.FromChunkID {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("plan fragment edge references unknown from_chunk_id %q", edge.FromChunkID)
			}
		}
		if _, ok := existing[edge.ToChunkID]; !ok {
			found := false
			for _, chunk := range normalized.Chunks {
				if chunk.ChunkID == edge.ToChunkID {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("plan fragment edge references unknown to_chunk_id %q", edge.ToChunkID)
			}
		} else {
			backEdgeCount++
		}
	}
	record.plan.Chunks = append(record.plan.Chunks, normalized.Chunks...)
	record.plan.Edges = append(record.plan.Edges, normalized.Edges...)
	record.graphRevision++
	summary := fmt.Sprintf("source=%s entry=%s new_nodes=%d new_edges=%d", plannerChunkID, normalized.EntryChunks[0], len(normalized.Chunks), len(normalized.Edges))
	record.plan.PlannerNotes = append(record.plan.PlannerNotes, summary)
	record.planningHistory = append(record.planningHistory, PlanningHistoryEntry{
		Continuation:      record.continuationCount + 1,
		Source:            plannerChunkID,
		EntryNode:         normalized.EntryChunks[0],
		NewNodeIDs:        collectChunkIDs(normalized.Chunks),
		NewEdgeCount:      len(normalized.Edges),
		GraphRevision:     record.graphRevision,
		AttachedAt:        time.Now().UTC(),
		Summary:           summary,
		ImportedBackEdges: backEdgeCount,
	})
	for _, chunk := range normalized.Chunks {
		record.status.ChunkStatuses[chunk.ChunkID] = ChunkPhasePending
		record.chunks[chunk.ChunkID] = ChunkExecutionResult{
			ChunkID: chunk.ChunkID,
			Kind:    normalizedChunkKind(chunk.Kind),
			Status:  ChunkPhasePending,
		}
	}
	if plannerChunkID != "" {
		result := record.chunks[plannerChunkID]
		if result.Control == nil {
			result.Control = map[string]any{}
		}
		result.Control["fragment_attached"] = true
		result.Control["fragment_entry_chunks"] = append([]string(nil), normalized.EntryChunks...)
		result.Control["graph_revision"] = record.graphRevision
		record.chunks[plannerChunkID] = result
	}
	return nil
}

func collectChunkIDs(chunks []PlanChunk) []string {
	out := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		out = append(out, chunk.ChunkID)
	}
	slices.Sort(out)
	return out
}

func anyToStringSlice(v any) []string {
	switch x := v.(type) {
	case []string:
		return append([]string(nil), x...)
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func anyToFileRef(v any) (vfs.FileRef, bool) {
	switch x := v.(type) {
	case vfs.FileRef:
		return x, true
	case *vfs.FileRef:
		if x == nil {
			return vfs.FileRef{}, false
		}
		return *x, true
	default:
		return vfs.FileRef{}, false
	}
}

func cloneRuntimeBinding(binding satis.RuntimeBinding) satis.RuntimeBinding {
	cloned := binding
	if binding.Texts != nil {
		cloned.Texts = append([]string(nil), binding.Texts...)
	}
	if binding.Object != nil {
		ref := *binding.Object
		cloned.Object = &ref
	}
	if binding.Objects != nil {
		cloned.Objects = append([]vfs.FileRef(nil), binding.Objects...)
	}
	if binding.Conversation != nil {
		cloned.Conversation = append([]satis.ConversationMessage(nil), binding.Conversation...)
	}
	return cloned
}

func cloneRuntimeBindingMap(src map[string]satis.RuntimeBinding) map[string]satis.RuntimeBinding {
	if src == nil {
		return nil
	}
	dst := make(map[string]satis.RuntimeBinding, len(src))
	for key, value := range src {
		dst[key] = cloneRuntimeBinding(value)
	}
	return dst
}

func (s *Server) refreshWorkflowRegistryLocked(record *runRecord, chunkID string, vars map[string]satis.RuntimeBinding) {
	if record.workflowRegistry == nil {
		record.workflowRegistry = make(map[string]WorkflowBindingSnapshot)
	}
	now := time.Now().UTC()
	for key, binding := range vars {
		version := 1
		if previous, ok := record.workflowRegistry[key]; ok {
			version = previous.Version + 1
		}
		record.workflowRegistry[key] = WorkflowBindingSnapshot{
			Name:      key,
			Kind:      binding.Kind,
			Source:    chunkID,
			Version:   version,
			Binding:   cloneRuntimeBinding(binding),
			UpdatedAt: now,
		}
	}
}

func resetLoopBackCorridor(record *runRecord, targetChunkID string, sourceChunkID string) map[string]struct{} {
	resetChunkIDs := collectLoopBackCorridorChunkIDs(record.plan, targetChunkID, sourceChunkID)
	if len(resetChunkIDs) == 0 {
		return resetChunkIDs
	}
	record.mu.Lock()
	defer record.mu.Unlock()
	if record.workflowRegistry != nil {
		for key, snapshot := range record.workflowRegistry {
			if _, ok := resetChunkIDs[snapshot.Source]; ok {
				delete(record.workflowRegistry, key)
			}
		}
	}
	for chunkID := range resetChunkIDs {
		result := record.chunks[chunkID]
		result.Status = ChunkPhasePending
		result.Substatus = ""
		result.StartedAt = nil
		result.FinishedAt = nil
		result.Objects = nil
		result.Values = nil
		result.Vars = nil
		result.Notes = nil
		result.SupplementaryInfo = nil
		result.ArtifactsEmitted = nil
		result.Error = nil
		result.RepeatResults = nil
		result.Control = nil
		record.chunks[chunkID] = result
		record.status.ChunkStatuses[chunkID] = ChunkPhasePending
	}
	record.status.UpdatedAt = time.Now().UTC()
	return resetChunkIDs
}

func collectLoopBackCorridorChunkIDs(plan *ChunkGraphPlan, targetChunkID string, sourceChunkID string) map[string]struct{} {
	targetChunkID = strings.TrimSpace(targetChunkID)
	sourceChunkID = strings.TrimSpace(sourceChunkID)
	if plan == nil || targetChunkID == "" || sourceChunkID == "" {
		return nil
	}
	forward := make(map[string][]string)
	reverse := make(map[string][]string)
	for _, edge := range plan.Edges {
		if strings.EqualFold(strings.TrimSpace(edge.EdgeKind), "handoff") || strings.EqualFold(strings.TrimSpace(edge.EdgeKind), "loop_back") {
			continue
		}
		fromID := strings.TrimSpace(edge.FromChunkID)
		toID := strings.TrimSpace(edge.ToChunkID)
		if fromID == "" || toID == "" {
			continue
		}
		forward[fromID] = append(forward[fromID], toID)
		reverse[toID] = append(reverse[toID], fromID)
	}
	reachableFromTarget := bfsChunkIDs(targetChunkID, forward)
	canReachSource := bfsChunkIDs(sourceChunkID, reverse)
	out := make(map[string]struct{})
	if _, ok := reachableFromTarget[sourceChunkID]; !ok && targetChunkID != sourceChunkID {
		return out
	}
	for chunkID := range reachableFromTarget {
		if _, ok := canReachSource[chunkID]; ok {
			out[chunkID] = struct{}{}
		}
	}
	out[targetChunkID] = struct{}{}
	out[sourceChunkID] = struct{}{}
	return out
}

func bfsChunkIDs(start string, adj map[string][]string) map[string]struct{} {
	seen := map[string]struct{}{start: {}}
	queue := []string{start}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range adj[current] {
			if _, ok := seen[next]; ok {
				continue
			}
			seen[next] = struct{}{}
			queue = append(queue, next)
		}
	}
	return seen
}

func collectTerminalLeafChunkIDs(plan *ChunkGraphPlan) map[string]struct{} {
	out := make(map[string]struct{})
	if plan == nil {
		return out
	}
	kindByChunkID := make(map[string]string, len(plan.Chunks))
	outgoing := make(map[string]int, len(plan.Chunks))
	for _, chunk := range plan.Chunks {
		kindByChunkID[chunk.ChunkID] = normalizedChunkKind(chunk.Kind)
		outgoing[chunk.ChunkID] = 0
	}
	for _, edge := range plan.Edges {
		kind := strings.ToLower(strings.TrimSpace(edge.EdgeKind))
		// loop_back is a back-edge that revisits an upstream node; excluding it keeps
		// the leaf classification stable. handoff edges however represent real data
		// flow between task chunks, so they must count as outgoing edges — otherwise
		// a chunk whose only downstream consumers are connected via handoff is
		// misclassified as a terminal leaf and may be auto-completed without executing.
		if kind == "loop_back" {
			continue
		}
		outgoing[edge.FromChunkID]++
	}
	for _, chunk := range plan.Chunks {
		if kindByChunkID[chunk.ChunkID] == "decision" {
			continue
		}
		if outgoing[chunk.ChunkID] == 0 {
			out[chunk.ChunkID] = struct{}{}
		}
	}
	return out
}

func (s *Server) autoCompleteTerminalLeaves(record *runRecord, completedLeafID string, terminalLeafChunks map[string]struct{}) {
	finished := time.Now().UTC()
	record.mu.Lock()
	autoCompleted := make([]string, 0, len(terminalLeafChunks))
	for chunkID := range terminalLeafChunks {
		if chunkID == completedLeafID {
			continue
		}
		phase := record.status.ChunkStatuses[chunkID]
		if phase != ChunkPhasePending && phase != ChunkPhaseReady {
			continue
		}
		result := record.chunks[chunkID]
		result.Status = ChunkPhaseSucceeded
		result.Substatus = ""
		result.Error = nil
		result.StartedAt = nil
		result.FinishedAt = &finished
		result.Objects = nil
		result.Values = nil
		result.Vars = nil
		result.Notes = nil
		result.SupplementaryInfo = nil
		result.ArtifactsEmitted = nil
		result.RepeatResults = nil
		if result.Control == nil {
			result.Control = map[string]any{}
		} else {
			result.Control = cloneAnyMap(result.Control)
		}
		result.Control["auto_completed"] = true
		result.Control["completed_by_leaf"] = completedLeafID
		record.chunks[chunkID] = result
		record.status.ChunkStatuses[chunkID] = ChunkPhaseSucceeded
		autoCompleted = append(autoCompleted, chunkID)
	}
	if record.workflowRegistry != nil {
		for key, snapshot := range record.workflowRegistry {
			if snapshot.Source == completedLeafID {
				continue
			}
			if _, ok := terminalLeafChunks[snapshot.Source]; ok {
				delete(record.workflowRegistry, key)
			}
		}
	}
	record.status.UpdatedAt = finished
	record.mu.Unlock()
	s.mu.Lock()
	s.invalidateRunsCacheLocked()
	s.mu.Unlock()
	for _, chunkID := range autoCompleted {
		s.appendEvent(record, chunkID, EventChunkSucceeded, map[string]any{
			"auto_completed":    true,
			"completed_by_leaf": completedLeafID,
		})
	}
}

func activeWorkIsLimitedToTerminalLeaves(record *runRecord, terminalLeafChunks map[string]struct{}) bool {
	if record == nil {
		return false
	}
	record.mu.RLock()
	defer record.mu.RUnlock()
	for chunkID, phase := range record.status.ChunkStatuses {
		switch phase {
		case ChunkPhasePending, ChunkPhaseReady, ChunkPhaseRunning:
			if _, ok := terminalLeafChunks[chunkID]; !ok {
				return false
			}
		}
	}
	return true
}

func (s *Server) transitionRunToPlanningPending(record *runRecord) {
	record.mu.Lock()
	record.status.Status = RunPhasePlanningPending
	record.status.UpdatedAt = time.Now().UTC()
	record.mu.Unlock()
	s.mu.Lock()
	s.invalidateRunsCacheLocked()
	s.mu.Unlock()
	s.appendEvent(record, "", EventRunPlanningPending, map[string]any{
		"plan_id": record.plan.PlanID,
	})
}

func (s *Server) syncDynamicPlanState(record *runRecord, chunkByID map[string]PlanChunk, remainingDeps map[string]int, adj map[string][]string, conditionalAdj map[string][]PlanEdge) {
	record.mu.RLock()
	defer record.mu.RUnlock()
	if record.plan == nil {
		return
	}
	conditionalBackEdges := make(map[string]struct{})
	for i, edge := range record.plan.Edges {
		if isConditionalEdgeKind(edge.EdgeKind) && edgeCreatesBackReference(record.plan.Edges, i) {
			conditionalBackEdges[edgeIdentity(edge)] = struct{}{}
		}
	}
	for _, chunk := range record.plan.Chunks {
		if _, ok := chunkByID[chunk.ChunkID]; ok {
			continue
		}
		chunkByID[chunk.ChunkID] = chunk
		remainingDeps[chunk.ChunkID] = 0
	}
	for _, edge := range record.plan.Edges {
		if isConditionalEdgeKind(edge.EdgeKind) {
			if !containsConditionalEdge(conditionalAdj[edge.FromChunkID], edge) {
				conditionalAdj[edge.FromChunkID] = append(conditionalAdj[edge.FromChunkID], edge)
				if _, ok := conditionalBackEdges[edgeIdentity(edge)]; !ok {
					remainingDeps[edge.ToChunkID]++
				}
			}
			continue
		}
		if !slices.Contains(adj[edge.FromChunkID], edge.ToChunkID) {
			adj[edge.FromChunkID] = append(adj[edge.FromChunkID], edge.ToChunkID)
			remainingDeps[edge.ToChunkID]++
		}
	}
}

func containsConditionalEdge(edges []PlanEdge, target PlanEdge) bool {
	for _, edge := range edges {
		if edge.FromChunkID == target.FromChunkID && edge.ToChunkID == target.ToChunkID && edge.EdgeKind == target.EdgeKind && edge.Branch == target.Branch {
			return true
		}
	}
	return false
}

func edgeIdentity(edge PlanEdge) string {
	return edge.FromChunkID + "\x00" + edge.ToChunkID + "\x00" + strings.ToLower(strings.TrimSpace(edge.EdgeKind)) + "\x00" + strings.TrimSpace(edge.Branch)
}

func collectChunkSupplementaryInfo(chunk PlanChunk) (map[string]any, error) {
	payload := make(map[string]any)

	if chunkInfo, err := declaredChunkSupplementaryInfo(chunk); err != nil {
		return nil, err
	} else if len(chunkInfo) > 0 {
		payload["chunk"] = chunkInfo
	}

	handoffSpecs, err := declaredHandoffInputs(chunk)
	if err != nil {
		return nil, err
	}
	if len(handoffSpecs) > 0 {
		handoffInfo := make(map[string]any)
		for port, spec := range handoffSpecs {
			if len(spec.SupplementaryInfo) == 0 {
				continue
			}
			handoffInfo[port] = cloneAnyMap(spec.SupplementaryInfo)
		}
		if len(handoffInfo) > 0 {
			payload["handoff_inputs"] = handoffInfo
		}
	}

	if len(payload) == 0 {
		return nil, nil
	}
	return payload, nil
}

func (s *Server) failRun(record *runRecord, phase RunPhase, err *StructuredError) {
	record.mu.Lock()
	record.status.Status = phase
	record.status.Error = err
	record.status.UpdatedAt = time.Now().UTC()
	record.mu.Unlock()
	s.mu.Lock()
	s.invalidateRunsCacheLocked()
	s.mu.Unlock()
	if phase == RunPhaseFailed {
		s.appendEvent(record, "", EventRunFailed, map[string]any{
			"error": err.Message,
		})
	}
}

func (s *Server) setChunkPhase(record *runRecord, chunkID string, phase ChunkPhase, touchResult bool) {
	record.mu.Lock()
	defer record.mu.Unlock()
	record.status.ChunkStatuses[chunkID] = phase
	record.status.UpdatedAt = time.Now().UTC()
	if touchResult {
		result := record.chunks[chunkID]
		result.Status = phase
		record.chunks[chunkID] = result
	}
	s.mu.Lock()
	s.invalidateRunsCacheLocked()
	s.mu.Unlock()
}

func (s *Server) appendEvent(record *runRecord, chunkID string, typ RunEventType, payload map[string]any) {
	record.mu.Lock()
	defer record.mu.Unlock()
	var chunkPtr *string
	if chunkID != "" {
		chunkCopy := chunkID
		chunkPtr = &chunkCopy
	}
	record.events = append(record.events, RunEvent{
		ProtocolVersion: 1,
		EventID:         s.nextEventID(),
		RunID:           record.status.RunID,
		ChunkID:         chunkPtr,
		TS:              time.Now().UTC(),
		Type:            typ,
		Payload:         payload,
	})
	record.eventsVersion++
}

func cloneStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	out := make(map[string]string, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

func cloneChunkStatuses(src map[string]ChunkPhase) map[string]ChunkPhase {
	if src == nil {
		return nil
	}
	out := make(map[string]ChunkPhase, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

func (s *Server) bumpLoopBackIteration(record *runRecord, edge PlanEdge) (int, int, bool) {
	maxIterations := 1
	if edge.LoopPolicy != nil && edge.LoopPolicy.MaxIterations > 0 {
		maxIterations = edge.LoopPolicy.MaxIterations
	}
	key := edgeIdentity(edge)
	record.mu.Lock()
	if record.loopBackCounts == nil {
		record.loopBackCounts = make(map[string]int)
	}
	nextCount := record.loopBackCounts[key] + 1
	record.loopBackCounts[key] = nextCount
	record.mu.Unlock()
	return nextCount, maxIterations, nextCount > maxIterations
}

func (s *Server) handleDecisionLoopLimitExceeded(record *runRecord, loopErr *decisionLoopLimitError) {
	payload := map[string]any{
		"error":          loopErr.Error(),
		"branch":         loopErr.Branch,
		"loop_back":      true,
		"loop_count":     loopErr.CurrentCount,
		"max_iterations": loopErr.MaxIterations,
		"target_chunk":   loopErr.TargetChunkID,
	}
	record.mu.Lock()
	stable := record.lastStableState
	initialStable := record.initialStableState
	isIncremental := record.continuationCount > 0 && stable != nil
	record.mu.Unlock()
	if isIncremental {
		if err := s.restoreVFSSnapshot(stable.vfsSnapshotDir); err != nil {
			payload["rollback_restore_error"] = err.Error()
		}
		record.mu.Lock()
		record.plan = stable.plan
		record.status = cloneRunStatus(stable.status)
		record.chunks = make(map[string]ChunkExecutionResult, len(stable.chunks))
		for chunkID, result := range stable.chunks {
			record.chunks[chunkID] = cloneChunkExecutionResult(result)
		}
		record.artifacts = append([]ArtifactSpec(nil), stable.artifacts...)
		record.graphRevision = stable.graphRevision
		record.continuationCount = stable.continuationCount
		record.planningHistory = append([]PlanningHistoryEntry(nil), stable.planningHistory...)
		record.workflowRegistry = cloneWorkflowRegistry(stable.workflowRegistry)
		record.loopBackCounts = make(map[string]int)
		record.lastStableState = nil
		record.status.UpdatedAt = time.Now().UTC()
		record.mu.Unlock()
		s.mu.Lock()
		s.invalidateRunsCacheLocked()
		s.mu.Unlock()
		payload["rollback"] = true
		s.appendEvent(record, loopErr.DecisionChunkID, EventChunkFailed, payload)
		s.appendEvent(record, "", EventRunPlanningPending, map[string]any{
			"plan_id":          record.plan.PlanID,
			"rollback":         true,
			"rollback_reason":  "decision_loop_limit_exceeded",
			"decision_chunk":   loopErr.DecisionChunkID,
			"max_iterations":   loopErr.MaxIterations,
			"observed_retries": loopErr.CurrentCount,
		})
		return
	}
	if initialStable != nil {
		if err := s.restoreVFSSnapshot(initialStable.vfsSnapshotDir); err != nil {
			payload["rollback_restore_error"] = err.Error()
		}
	}
	now := time.Now().UTC()
	structured := &StructuredError{
		Code:      CodeDecisionLoopLimitExceeded,
		Message:   loopErr.Error(),
		Stage:     StageExecution,
		Retryable: false,
		Details: map[string]any{
			"chunk_id":       loopErr.DecisionChunkID,
			"branch":         loopErr.Branch,
			"target_chunk":   loopErr.TargetChunkID,
			"max_iterations": loopErr.MaxIterations,
			"loop_count":     loopErr.CurrentCount,
		},
	}
	record.mu.Lock()
	for chunkID, result := range record.chunks {
		result.Status = ChunkPhaseFailed
		result.Substatus = ""
		if result.StartedAt == nil {
			result.StartedAt = &now
		}
		result.FinishedAt = &now
		result.Error = structured
		record.chunks[chunkID] = result
		record.status.ChunkStatuses[chunkID] = ChunkPhaseFailed
	}
	record.status.UpdatedAt = now
	record.mu.Unlock()
	payload["rollback"] = false
	record.mu.RLock()
	failedChunkIDs := make([]string, 0, len(record.chunks))
	for chunkID := range record.chunks {
		failedChunkIDs = append(failedChunkIDs, chunkID)
	}
	record.mu.RUnlock()
	for _, chunkID := range failedChunkIDs {
		s.appendEvent(record, chunkID, EventChunkFailed, payload)
	}
	s.failRun(record, RunPhaseFailed, structured)
}

func runWithTxnRetry(ctx context.Context, fn func(context.Context) error) error {
	var lastErr error
	for attempt := 0; attempt < schedulerTxnRetryAttempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := fn(ctx)
		if err == nil {
			return nil
		}
		if !errors.Is(err, vfs.ErrTxnMismatch) {
			return err
		}
		lastErr = err
		if attempt < schedulerTxnRetryAttempts-1 {
			time.Sleep(time.Duration(attempt+1) * 5 * time.Millisecond)
		}
	}
	return lastErr
}

func estimateChunkCostLevel(chunk PlanChunk) int {
	sourceSize := len(chunk.Source.SatisText)
	if chunk.Repeat != nil && chunk.Repeat.Mode == "batch" {
		return 5
	}
	if strings.Contains(strings.ToLower(chunk.Source.SatisText), "invoke") {
		return estimateBridgeLLMCostLevel(sourceSize, sourceSize/4)
	}
	return estimateBridgeIOCostLevel(sourceSize)
}

func estimateRepeatPerItemCostLevel(expandedText string) int {
	lower := strings.ToLower(expandedText)
	if strings.Contains(lower, "invoke") {
		return estimateBridgeLLMCostLevel(len(expandedText), len(expandedText)/4)
	}
	return estimateBridgeIOCostLevel(len(expandedText))
}

func estimateBridgeIOCostLevel(inputSize int) int {
	switch {
	case inputSize < 1024:
		return 1
	case inputSize < 10240:
		return 2
	default:
		return 3
	}
}

func estimateBridgeLLMCostLevel(inputSize int, promptSize int) int {
	total := inputSize + promptSize
	switch {
	case total < 500:
		return 3
	case total < 5000:
		return 4
	default:
		return 5
	}
}

// InspectObject reuses the disk-backed VFS inspection path.
func (s *Server) InspectObject(params InspectObjectParams) (InspectObjectResult, error) {
	if s.cfg.Backend != "disk" {
		return InspectObjectResult{}, fmt.Errorf("gopy inspect_object error: disk backend required")
	}
	snapshot, err := sandbox.LoadRuntimeSnapshot(s.cfg)
	if err != nil {
		return InspectObjectResult{}, err
	}

	var meta *vfs.FileMeta
	if params.FileID != "" {
		meta = snapshot.FindFileByID(vfs.FileID(params.FileID))
	} else if params.VirtualPath != "" {
		meta = snapshot.FindFileByPath(normalizeVirtualPath(params.VirtualPath))
	}
	if meta == nil {
		return InspectObjectResult{}, fmt.Errorf("gopy inspect_object error: target not found")
	}

	payload := InspectObjectResult{
		Backend:  fmt.Sprintf("disk mount=%s", s.cfg.MountDir),
		StateDir: s.cfg.StateDir,
		File:     *meta,
		Events:   snapshot.FilterEventsByFileID(meta.FileID),
	}
	if versions, err := sandbox.LoadVersionEntries(s.cfg, meta.FileID); err == nil {
		payload.Versions = versions
	}
	if content, err := sandbox.LoadCurrentContent(s.cfg, *meta); err == nil {
		payload.Content = summarizeContent(meta.Kind, content)
	}
	return payload, nil
}

func normalizeVirtualPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	cleaned := filepath.Clean(path)
	if cleaned == "." {
		return "/"
	}
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}
	return cleaned
}

func summarizeContent(kind vfs.FileKind, data []byte) *ContentSummary {
	if data == nil && kind == vfs.FileKindDirectory {
		return &ContentSummary{
			Kind:     string(kind),
			ByteSize: 0,
		}
	}

	summary := &ContentSummary{
		Kind:     string(kind),
		ByteSize: len(data),
	}
	if kind == vfs.FileKindBinary {
		return summary
	}

	text := string(data)
	if len(text) > 240 {
		summary.Preview = text[:240]
		summary.Truncated = true
		return summary
	}
	summary.Preview = text
	return summary
}

// repeatOutputTmplData is passed to text/template for repeat.output_template.
type repeatOutputTmplData struct {
	Index       int
	IndexPadded string
	Basename    string
}

type repeatChunkSourceToken uint8

const (
	repeatChunkSourceLiteral repeatChunkSourceToken = iota
	repeatChunkSourceInputPath
	repeatChunkSourceOutputPath
)

type repeatChunkSourceSegment struct {
	kind    repeatChunkSourceToken
	literal string
}

type repeatChunkSourceTemplate struct {
	segments     []repeatChunkSourceSegment
	literalBytes int
	inputRefs    int
	outputRefs   int
}

func compileRepeatChunkSourceTemplate(src string) repeatChunkSourceTemplate {
	const (
		inputToken  = "__INPUT_PATH__"
		outputToken = "__OUTPUT_PATH__"
	)
	tmpl := repeatChunkSourceTemplate{}
	for len(src) > 0 {
		inputIdx := strings.Index(src, inputToken)
		outputIdx := strings.Index(src, outputToken)
		nextIdx := -1
		nextKind := repeatChunkSourceLiteral
		nextToken := ""
		switch {
		case inputIdx >= 0 && (outputIdx < 0 || inputIdx < outputIdx):
			nextIdx = inputIdx
			nextKind = repeatChunkSourceInputPath
			nextToken = inputToken
		case outputIdx >= 0:
			nextIdx = outputIdx
			nextKind = repeatChunkSourceOutputPath
			nextToken = outputToken
		}
		if nextIdx < 0 {
			tmpl.segments = append(tmpl.segments, repeatChunkSourceSegment{
				kind:    repeatChunkSourceLiteral,
				literal: src,
			})
			tmpl.literalBytes += len(src)
			break
		}
		if nextIdx > 0 {
			literal := src[:nextIdx]
			tmpl.segments = append(tmpl.segments, repeatChunkSourceSegment{
				kind:    repeatChunkSourceLiteral,
				literal: literal,
			})
			tmpl.literalBytes += len(literal)
		}
		tmpl.segments = append(tmpl.segments, repeatChunkSourceSegment{kind: nextKind})
		if nextKind == repeatChunkSourceInputPath {
			tmpl.inputRefs++
		} else {
			tmpl.outputRefs++
		}
		src = src[nextIdx+len(nextToken):]
	}
	return tmpl
}

func (t repeatChunkSourceTemplate) render(inputPath string, outputPath string) string {
	if len(t.segments) == 1 && t.segments[0].kind == repeatChunkSourceLiteral {
		return t.segments[0].literal
	}
	var buf strings.Builder
	buf.Grow(t.literalBytes + t.inputRefs*len(inputPath) + t.outputRefs*len(outputPath))
	for _, segment := range t.segments {
		switch segment.kind {
		case repeatChunkSourceInputPath:
			buf.WriteString(inputPath)
		case repeatChunkSourceOutputPath:
			buf.WriteString(outputPath)
		default:
			buf.WriteString(segment.literal)
		}
	}
	return buf.String()
}

// renderRepeatOutputPaths renders one path per input using a single parsed template.
// Templates use Go text/template syntax, e.g. "/out/{{.Index}}.txt" or "/out/{{.IndexPadded}}_{{.Basename}}.txt".
func renderRepeatOutputPaths(repeat *ChunkRepeat, inputPaths []string) ([]string, error) {
	if repeat == nil || repeat.OutputTmpl == "" {
		return nil, fmt.Errorf("empty output template")
	}
	tmpl, err := template.New("repeat_output").Parse(repeat.OutputTmpl)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(inputPaths))
	for i, inputPath := range inputPaths {
		var buf strings.Builder
		if err := tmpl.Execute(&buf, repeatOutputTmplData{
			Index:       i,
			IndexPadded: fmt.Sprintf("%04d", i),
			Basename:    filepath.Base(inputPath),
		}); err != nil {
			return nil, err
		}
		out[i] = buf.String()
	}
	return out, nil
}

func (s *Server) readRepeatInputText(ctx context.Context, chunkID string, inputPath string) (string, error) {
	txn, err := s.vfs.BeginChunkTxn(ctx, vfs.ChunkID(chunkID))
	if err != nil {
		return "", err
	}
	defer func() {
		_ = s.vfs.RollbackChunkTxn(ctx, txn)
	}()

	ref, err := s.vfs.Resolve(ctx, vfs.ResolveInput{VirtualPath: normalizeVirtualPath(inputPath)})
	if err != nil {
		return "", err
	}
	readResult, err := s.vfs.Read(ctx, txn, vfs.ReadInput{
		Target: vfs.ResolveInput{FileID: ref.FileID},
	})
	if err != nil {
		return "", err
	}
	if readResult.Blob != nil {
		return string(readResult.Blob), nil
	}
	return readResult.Text, nil
}

func (s *Server) writeRepeatOutput(ctx context.Context, chunkID string, outputPath string, content string) (vfs.FileRef, error) {
	txn, err := s.vfs.BeginChunkTxn(ctx, vfs.ChunkID(chunkID))
	if err != nil {
		return vfs.FileRef{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = s.vfs.RollbackChunkTxn(ctx, txn)
		}
	}()

	targetPath := normalizeVirtualPath(outputPath)
	ref, resolveErr := s.vfs.Resolve(ctx, vfs.ResolveInput{VirtualPath: targetPath})
	switch {
	case resolveErr == nil:
		ref, err = s.vfs.Write(ctx, txn, vfs.WriteInput{
			Target:        vfs.ResolveInput{FileID: ref.FileID},
			WriterChunkID: txn.ChunkID,
			Mode:          vfs.WriteModeReplaceFull,
			Text:          content,
		})
	case resolveErr == vfs.ErrFileNotFound:
		ref, err = s.vfs.Create(ctx, txn, vfs.CreateInput{
			VirtualPath:    targetPath,
			Kind:           vfs.FileKindText,
			CreatorChunkID: txn.ChunkID,
			InitialText:    content,
		})
	default:
		err = resolveErr
	}
	if err != nil {
		return vfs.FileRef{}, err
	}
	if err := s.vfs.CommitChunkTxn(ctx, txn); err != nil {
		return vfs.FileRef{}, err
	}
	committed = true
	return ref, nil
}

func (s *Server) executeBatchRepeatChunk(ctx context.Context, record *runRecord, chunk PlanChunk, options ExecutionOptions, attempt int) error {
	repeat := chunk.Repeat
	if repeat == nil || len(repeat.InputPaths) == 0 {
		return fmt.Errorf("chunk %q repeat has no input_paths", chunk.ChunkID)
	}
	if s.invoker == nil {
		return fmt.Errorf("chunk %q repeat mode batch requires invoker", chunk.ChunkID)
	}
	supplementaryInfo, err := collectChunkSupplementaryInfo(chunk)
	if err != nil {
		return err
	}

	outputPaths, err := renderRepeatOutputPaths(repeat, repeat.InputPaths)
	if err != nil {
		return fmt.Errorf("chunk %q: failed to render output_template: %w", chunk.ChunkID, err)
	}

	inputTexts := make([]string, len(repeat.InputPaths))
	for i, inputPath := range repeat.InputPaths {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		inputText, err := s.readRepeatInputText(ctx, chunk.ChunkID, inputPath)
		if err != nil {
			return fmt.Errorf("chunk %q iteration %d (%s): read input: %w", chunk.ChunkID, i, inputPath, err)
		}
		inputTexts[i] = inputText
	}

	batchExec := &satis.Executor{
		Invoker: satis.InvokerFunc(func(_ context.Context, prompt string, input string) (string, error) {
			invokeCtx := ctx
			if chunk.TimeoutMS > 0 {
				var cancel context.CancelFunc
				invokeCtx, cancel = context.WithTimeout(ctx, time.Duration(chunk.TimeoutMS)*time.Millisecond)
				defer cancel()
			}
			return s.invoker.Invoke(invokeCtx, prompt, input)
		}),
		Stdout: io.Discard,
	}
	batchExec.BatchScheduler = s.Scheduler
	outputTexts, err := batchExec.InvokeBatch(ctx, repeat.Prompt, inputTexts)
	if err != nil {
		return fmt.Errorf("chunk %q concurrent invoke failed: %w", chunk.ChunkID, err)
	}

	var (
		repeatResults []map[string]any
		discovered    []ArtifactSpec
		values        = make(map[string]string, len(repeat.InputPaths))
	)
	for i, inputPath := range repeat.InputPaths {
		outputText := outputTexts[i]
		outputPath := outputPaths[i]
		ref, err := s.writeRepeatOutput(ctx, chunk.ChunkID, outputPath, outputText)
		if err != nil {
			return fmt.Errorf("chunk %q iteration %d (%s): write output: %w", chunk.ChunkID, i, inputPath, err)
		}

		name := fmt.Sprintf("@repeat_%04d", i)
		values[name] = outputText
		discovered = append(discovered, ArtifactSpec{
			Name:        name,
			VirtualPath: ref.VirtualPath,
			FileID:      string(ref.FileID),
		})
		repeatResults = append(repeatResults, map[string]any{
			"index":          i,
			"input_path":     inputPath,
			"output_path":    outputPath,
			"status":         "succeeded",
			"output_var":     name,
			"output_text":    outputText,
			"output_file":    ref.VirtualPath,
			"output_file_id": string(ref.FileID),
		})
		s.appendEvent(record, chunk.ChunkID, EventObjectChanged, map[string]any{
			"repeat_index": i,
			"name":         name,
			"file_id":      ref.FileID,
			"virtual_path": ref.VirtualPath,
		})
		s.appendEvent(record, chunk.ChunkID, EventArtifactEmitted, map[string]any{
			"name":         name,
			"file_id":      ref.FileID,
			"virtual_path": ref.VirtualPath,
		})
	}

	finished := time.Now().UTC()
	record.mu.Lock()
	result := record.chunks[chunk.ChunkID]
	result.Status = ChunkPhaseSucceeded
	result.FinishedAt = &finished
	result.SupplementaryInfo = cloneAnyMap(supplementaryInfo)
	result.Values = values
	result.Vars = cloneRuntimeBindingMap(result.Vars)
	if result.Vars == nil {
		result.Vars = map[string]satis.RuntimeBinding{}
	}
	for key, value := range values {
		result.Vars[key] = satis.RuntimeBinding{Kind: "text", Text: value}
	}
	result.RepeatResults = repeatResults
	result.ArtifactsEmitted = append([]ArtifactSpec(nil), discovered...)
	record.chunks[chunk.ChunkID] = result
	record.status.ChunkStatuses[chunk.ChunkID] = ChunkPhaseSucceeded
	record.status.UpdatedAt = finished
	record.artifacts = append(record.artifacts, discovered...)
	s.refreshWorkflowRegistryLocked(record, chunk.ChunkID, result.Vars)
	record.mu.Unlock()

	s.appendEvent(record, chunk.ChunkID, EventChunkSucceeded, map[string]any{
		"repeat_count": len(repeat.InputPaths),
	})
	return nil
}

// executeRepeatedChunk expands a template chunk over input_paths and executes each iteration.
func (s *Server) executeRepeatedChunk(ctx context.Context, record *runRecord, chunk PlanChunk, options ExecutionOptions, attempt int) error {
	repeat := chunk.Repeat
	if repeat == nil || len(repeat.InputPaths) == 0 {
		return fmt.Errorf("chunk %q repeat has no input_paths", chunk.ChunkID)
	}
	supplementaryInfo, err := collectChunkSupplementaryInfo(chunk)
	if err != nil {
		return err
	}
	inputBindings, err := s.resolveChunkInputBindings(record, chunk)
	if err != nil {
		return err
	}
	outputPaths, err := renderRepeatOutputPaths(repeat, repeat.InputPaths)
	if err != nil {
		return fmt.Errorf("chunk %q: failed to render output_template: %w", chunk.ChunkID, err)
	}

	sourceTemplate := compileRepeatChunkSourceTemplate(chunk.Source.SatisText)
	tasks := make([]BatchTask, 0, len(repeat.InputPaths))
	for i, inputPath := range repeat.InputPaths {
		outputPath := outputPaths[i]

		idx := i
		path := inputPath
		expandedText := sourceTemplate.render(inputPath, outputPath)
		tasks = append(tasks, BatchTask{
			ID:        fmt.Sprintf("%s_repeat_%04d", chunk.ChunkID, idx),
			Kind:      BatchTaskKindRepeatPerItem,
			Priority:  80,
			CostLevel: estimateRepeatPerItemCostLevel(expandedText),
			Meta: map[string]string{
				"chunk_id":   chunk.ChunkID,
				"input_path": path,
			},
			ExecFn: func(runCtx context.Context) (any, error) {
				var execResult *satis.ExecutionResult
				err := runWithTxnRetry(runCtx, func(retryCtx context.Context) error {
					execCtx := retryCtx
					if chunk.TimeoutMS > 0 {
						var cancel context.CancelFunc
						execCtx, cancel = context.WithTimeout(retryCtx, time.Duration(chunk.TimeoutMS)*time.Millisecond)
						defer cancel()
					}
					executor := *s.executor
					executor.InputBindings = inputBindings
					executor.Invoker = s.invoker
					var err error
					execResult, err = executor.ParseValidateExecute(execCtx, expandedText)
					return err
				})
				if err != nil {
					return nil, fmt.Errorf("chunk %q iteration %d (%s): %w", chunk.ChunkID, idx, path, err)
				}
				return execResult, nil
			},
		})
	}

	results := schedulerOrDefault(s.Scheduler).Schedule(ctx, tasks)
	var (
		repeatResults []map[string]any
		discovered    []ArtifactSpec
	)
	for i, result := range results {
		inputPath := repeat.InputPaths[i]
		outputPath := outputPaths[i]
		if result.Err != nil {
			repeatResults = append(repeatResults, map[string]any{
				"index":      i,
				"input_path": inputPath,
				"status":     "failed",
				"error":      result.Err.Error(),
			})
			s.appendEvent(record, chunk.ChunkID, EventChunkFailed, map[string]any{
				"repeat_index": i,
				"input_path":   inputPath,
				"error":        result.Err.Error(),
			})
			return result.Err
		}

		execResult, ok := result.Value.(*satis.ExecutionResult)
		if !ok {
			return fmt.Errorf("chunk %q iteration %d returned unexpected result type %T", chunk.ChunkID, i, result.Value)
		}
		resultEntry := map[string]any{
			"index":       i,
			"input_path":  inputPath,
			"output_path": outputPath,
			"status":      "succeeded",
		}
		for name, ref := range execResult.Objects {
			resultEntry[name+"_file_id"] = string(ref.FileID)
			resultEntry[name+"_virtual_path"] = ref.VirtualPath
			discovered = append(discovered, ArtifactSpec{
				Name:        fmt.Sprintf("%s[%04d]", name, i),
				VirtualPath: ref.VirtualPath,
				FileID:      string(ref.FileID),
			})
			s.appendEvent(record, chunk.ChunkID, EventObjectChanged, map[string]any{
				"repeat_index": i,
				"name":         name,
				"file_id":      ref.FileID,
				"virtual_path": ref.VirtualPath,
			})
			s.appendEvent(record, chunk.ChunkID, EventArtifactEmitted, map[string]any{
				"name":         name,
				"file_id":      ref.FileID,
				"virtual_path": ref.VirtualPath,
			})
		}
		repeatResults = append(repeatResults, resultEntry)
	}

	finished := time.Now().UTC()
	record.mu.Lock()
	result := record.chunks[chunk.ChunkID]
	result.Status = ChunkPhaseSucceeded
	result.FinishedAt = &finished
	result.SupplementaryInfo = cloneAnyMap(supplementaryInfo)
	result.RepeatResults = repeatResults
	result.ArtifactsEmitted = append([]ArtifactSpec(nil), discovered...)
	record.chunks[chunk.ChunkID] = result
	record.status.ChunkStatuses[chunk.ChunkID] = ChunkPhaseSucceeded
	record.status.UpdatedAt = finished
	record.artifacts = append(record.artifacts, discovered...)
	record.mu.Unlock()

	s.appendEvent(record, chunk.ChunkID, EventChunkSucceeded, map[string]any{
		"repeat_count": len(repeat.InputPaths),
	})
	return nil
}
