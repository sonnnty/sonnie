package voice

import "testing"

func TestQueue_FIFO(t *testing.T) {
	q := newQueue(3)
	for _, s := range []string{"a", "b", "c"} {
		if q.Push(s) {
			t.Fatalf("unexpected drop pushing %q", s)
		}
	}
	for _, want := range []string{"a", "b", "c"} {
		got, ok := q.Pop()
		if !ok || got != want {
			t.Fatalf("expected %q, got %q (ok=%v)", want, got, ok)
		}
	}
	if _, ok := q.Pop(); ok {
		t.Fatal("expected empty queue")
	}
}

func TestQueue_DropsOldestWhenFull(t *testing.T) {
	q := newQueue(2)
	q.Push("a")
	q.Push("b")
	if !q.Push("c") {
		t.Fatal("expected Push to report a drop")
	}
	if q.Len() != 2 {
		t.Fatalf("expected len 2, got %d", q.Len())
	}
	for _, want := range []string{"b", "c"} {
		if got, _ := q.Pop(); got != want {
			t.Fatalf("expected %q, got %q", want, got)
		}
	}
}

func TestQueue_MinimumSizeIsOne(t *testing.T) {
	q := newQueue(0)
	q.Push("a")
	q.Push("b")
	if got, _ := q.Pop(); got != "b" || q.Len() != 0 {
		t.Fatalf("expected only the newest item, got %q with %d left", got, q.Len())
	}
}
