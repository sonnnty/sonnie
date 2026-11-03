package voice

import "testing"

// push queues plain text and reports whether an older item was dropped.
func push(q *queue, text string) bool {
	_, dropped := q.Push(item{text: text})
	return dropped
}

func TestQueue_FIFO(t *testing.T) {
	q := newQueue(3)
	for _, s := range []string{"a", "b", "c"} {
		if push(q, s) {
			t.Fatalf("unexpected drop pushing %q", s)
		}
	}
	for _, want := range []string{"a", "b", "c"} {
		it, ok := q.Pop()
		if got := it.text; !ok || got != want {
			t.Fatalf("expected %q, got %q (ok=%v)", want, got, ok)
		}
	}
	if _, ok := q.Pop(); ok {
		t.Fatal("expected empty queue")
	}
}

func TestQueue_DropsOldestWhenFull(t *testing.T) {
	q := newQueue(2)
	push(q, "a")
	push(q, "b")
	if !push(q, "c") {
		t.Fatal("expected Push to report a drop")
	}
	if q.Len() != 2 {
		t.Fatalf("expected len 2, got %d", q.Len())
	}
	for _, want := range []string{"b", "c"} {
		if it, _ := q.Pop(); it.text != want {
			t.Fatalf("expected %q, got %q", want, it.text)
		}
	}
}

func TestQueue_MinimumSizeIsOne(t *testing.T) {
	q := newQueue(0)
	push(q, "a")
	push(q, "b")
	if it, _ := q.Pop(); it.text != "b" || q.Len() != 0 {
		t.Fatalf("expected only the newest item, got %q with %d left", it.text, q.Len())
	}
}

func TestQueue_PushReturnsTheDroppedItem(t *testing.T) {
	q := newQueue(1)
	q.Push(item{text: "a"})
	old, dropped := q.Push(item{text: "b"})
	if !dropped || old.text != "a" {
		t.Fatalf("dropped %q (%v), want a", old.text, dropped)
	}
}
