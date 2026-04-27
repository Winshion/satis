package bridge

import "container/heap"

// chunkMinHeap is a min-heap of chunk_id strings (lexicographic order).
// It matches the previous scheduler behavior: always run the smallest
// chunk_id among currently runnable nodes.
type chunkMinHeap []string

func (h chunkMinHeap) Len() int           { return len(h) }
func (h chunkMinHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h chunkMinHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *chunkMinHeap) Push(x any) {
	*h = append(*h, x.(string))
}

func (h *chunkMinHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

func pushChunkID(h *chunkMinHeap, id string) {
	heap.Push(h, id)
}

func popChunkID(h *chunkMinHeap) string {
	return heap.Pop(h).(string)
}
