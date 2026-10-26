package voice

import "sync"

// queue is a bounded FIFO of utterances. When full, Push drops the oldest
// waiting item: chat is live, so recent messages matter more than a backlog.
// Items are text, not audio, so dropped messages never cost a synthesis.
type queue struct {
	mu    sync.Mutex
	items []string
	max   int
	// ready receives a value whenever an item is pushed; it has capacity 1
	// so a burst of pushes collapses into a single wake-up.
	ready chan struct{}
}

func newQueue(max int) *queue {
	if max < 1 {
		max = 1
	}
	return &queue{max: max, ready: make(chan struct{}, 1)}
}

// Push appends text and reports whether an older item was dropped.
func (q *queue) Push(text string) (dropped bool) {
	q.mu.Lock()
	if len(q.items) >= q.max {
		q.items = q.items[1:]
		dropped = true
	}
	q.items = append(q.items, text)
	q.mu.Unlock()

	select {
	case q.ready <- struct{}{}:
	default:
	}
	return dropped
}

// Pop removes and returns the oldest item, if any.
func (q *queue) Pop() (string, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return "", false
	}
	text := q.items[0]
	q.items = q.items[1:]
	return text, true
}

// Len returns the number of waiting items.
func (q *queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}
