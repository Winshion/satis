package bridge

import "satis/satis"

type BatchTaskKind = satis.BatchTaskKind

const (
	BatchTaskKindChunk         = satis.BatchTaskKindChunk
	BatchTaskKindRepeatPerItem = satis.BatchTaskKindRepeatPerItem
	BatchTaskKindSimultaneousInvoke = satis.BatchTaskKindSimultaneousInvoke
)

type BatchTask = satis.BatchTask
type BatchResult = satis.BatchResult
type BatchScheduler = satis.BatchScheduler

type SerialScheduler = satis.SerialScheduler
type ConcurrentScheduler = satis.ConcurrentScheduler
type DefaultScheduler = satis.DefaultScheduler

func schedulerOrDefault(scheduler BatchScheduler) BatchScheduler {
	if scheduler != nil {
		return scheduler
	}
	return &DefaultScheduler{MaxConcurrent: 4}
}
