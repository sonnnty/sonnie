package voice

import "sync"

// item is one queued utterance and the hook to call when it leaves the player.
type item struct {
	text  string
	voice string
	done  func(spoken bool)
}

// queue is a bounded FIFO of utterances. When full, Push drops the oldest
// waiting item: chat is live, so recent messages matter more than a backlog.
// Items are text, not audio, so dropped messages never cost a synthesis.
type queue struct {
	mu    sync.Mutex
	items []item
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

// Push appends it and returns the older item it displaced, if any.
func (q *queue) Push(it item) (dropped item, wasDropped bool) {
	q.mu.Lock()
	if len(q.items) >= q.max {
		dropped, wasDropped = q.items[0], true
		q.items = q.items[1:]
	}
	q.items = append(q.items, it)
	q.mu.Unlock()

	select {
	case q.ready <- struct{}{}:
	default:
	}
	return dropped, wasDropped
}

// Pop removes and returns the oldest item, if any.
func (q *queue) Pop() (item, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return item{}, false
	}
	it := q.items[0]
	q.items = q.items[1:]
	return it, true
}

// Len returns the number of waiting items.
func (q *queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}
