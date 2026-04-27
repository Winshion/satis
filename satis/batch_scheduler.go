package satis

import (
	"context"
	"sort"
	"sync"
)

type BatchTaskKind string

const (
	BatchTaskKindChunk              BatchTaskKind = "chunk"
	BatchTaskKindRepeatPerItem      BatchTaskKind = "repeat_per_item"
	BatchTaskKindSimultaneousInvoke BatchTaskKind = "invoke_simultaneous"
)

// BatchTask is a schedulable unit of independent work.
type BatchTask struct {
	ID        string
	Kind      BatchTaskKind
	ExecFn    func(context.Context) (any, error)
	Priority  int
	CostLevel int
	Meta      map[string]string
}

// BatchResult is the outcome of one scheduled task.
type BatchResult struct {
	Task  BatchTask
	Value any
	Err   error
}

// BatchScheduler decides how a batch of independent tasks is scheduled.
type BatchScheduler interface {
	Schedule(ctx context.Context, tasks []BatchTask) []BatchResult
}

// SerialScheduler keeps the historical serial execution behavior.
type SerialScheduler struct{}

func (s *SerialScheduler) Schedule(ctx context.Context, tasks []BatchTask) []BatchResult {
	results := make([]BatchResult, len(tasks))
	for _, idx := range orderedTaskIndices(tasks) {
		task := tasks[idx]
		if ctx.Err() != nil {
			results[idx] = BatchResult{Task: task, Err: ctx.Err()}
			continue
		}
		value, err := task.ExecFn(ctx)
		results[idx] = BatchResult{
			Task:  task,
			Value: value,
			Err:   err,
		}
	}
	return results
}

// ConcurrentScheduler runs tasks with bounded parallelism.
// MaxConcurrent <= 1 degrades to serial behavior.
type ConcurrentScheduler struct {
	MaxConcurrent int
}

func (s *ConcurrentScheduler) Schedule(ctx context.Context, tasks []BatchTask) []BatchResult {
	if s == nil || s.MaxConcurrent <= 1 || len(tasks) <= 1 {
		return (&SerialScheduler{}).Schedule(ctx, tasks)
	}

	type scheduledJob struct {
		index int
		task  BatchTask
	}

	ordered := orderedTaskIndices(tasks)
	jobs := make(chan scheduledJob)
	results := make([]BatchResult, len(tasks))

	var wg sync.WaitGroup
	worker := func() {
		defer wg.Done()
		for job := range jobs {
			if ctx.Err() != nil {
				results[job.index] = BatchResult{Task: job.task, Err: ctx.Err()}
				continue
			}
			value, err := job.task.ExecFn(ctx)
			results[job.index] = BatchResult{
				Task:  job.task,
				Value: value,
				Err:   err,
			}
		}
	}

	workerCount := s.MaxConcurrent
	if workerCount > len(tasks) {
		workerCount = len(tasks)
	}
	wg.Add(workerCount)
	for i := 0; i < workerCount; i++ {
		go worker()
	}

	go func() {
		defer close(jobs)
		for _, idx := range ordered {
			select {
			case <-ctx.Done():
				return
			case jobs <- scheduledJob{index: idx, task: tasks[idx]}:
			}
		}
	}()

	wg.Wait()
	for i, result := range results {
		if result.Task.ID != "" || result.Err != nil || result.Value != nil {
			continue
		}
		results[i] = BatchResult{
			Task: tasks[i],
			Err:  ctx.Err(),
		}
	}
	return results
}

// DefaultScheduler is the new built-in scheduler.
// It prefers higher priority tasks, and among same-priority tasks it prefers
// lower cost levels first so fast tasks surface earlier.
type DefaultScheduler struct {
	MaxConcurrent int
}

func (s *DefaultScheduler) Schedule(ctx context.Context, tasks []BatchTask) []BatchResult {
	maxConcurrent := s.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = 4
	}
	return (&ConcurrentScheduler{MaxConcurrent: maxConcurrent}).Schedule(ctx, tasks)
}

func (e *Executor) schedulerOrDefault() BatchScheduler {
	if e != nil && e.BatchScheduler != nil {
		return e.BatchScheduler
	}
	return &DefaultScheduler{MaxConcurrent: 4}
}

// InvokeBatch runs independent LLM calls through the injected scheduler.
// Nil scheduler keeps the historical serial behavior.
func (e *Executor) InvokeBatch(ctx context.Context, prompt string, inputs []string) ([]string, error) {
	return e.InvokeBatchWithProvider(ctx, "", prompt, inputs)
}

func orderedTaskIndices(tasks []BatchTask) []int {
	order := make([]int, len(tasks))
	for i := range tasks {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		left := tasks[order[i]]
		right := tasks[order[j]]
		if left.Priority != right.Priority {
			return left.Priority > right.Priority
		}
		if left.CostLevel != right.CostLevel {
			return left.CostLevel < right.CostLevel
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		return left.ID < right.ID
	})
	return order
}

func estimateIOCostLevel(inputSize int) int {
	switch {
	case inputSize < 1024:
		return 1
	case inputSize < 10240:
		return 2
	default:
		return 3
	}
}

func estimateLLMCostLevel(inputSize int, promptSize int) int {
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
