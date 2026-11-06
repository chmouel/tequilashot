package gui

import (
	"testing"
	"time"
)

func TestTrimHistory(t *testing.T) {
	initial := trimSelection{0, 4 * time.Second, 4 * time.Second, false}
	var h trimHistory
	h.reset(initial)
	changed := trimSelection{time.Second, 3 * time.Second, 4 * time.Second, true}
	h.record(changed)
	h.record(changed)
	if len(h.states) != 2 {
		t.Fatal("unchanged selection added history")
	}
	if got, ok := h.restore(false); !ok || got != initial {
		t.Fatalf("undo: %+v %v", got, ok)
	}
	if got, ok := h.restore(true); !ok || got != changed {
		t.Fatalf("redo: %+v %v", got, ok)
	}
	h.restore(false)
	h.record(trimSelection{time.Second, 4 * time.Second, 4 * time.Second, false})
	if _, ok := h.restore(true); ok {
		t.Fatal("new edit retained redo")
	}
	for i := range 150 {
		s := initial
		s.start = time.Duration(i) * time.Millisecond
		h.record(s)
	}
	if len(h.states) != 100 || h.index != 99 {
		t.Fatalf("unbounded history: %d %d", len(h.states), h.index)
	}
	h.reset(initial)
	if _, ok := h.restore(false); ok {
		t.Fatal("source reset retained history")
	}
}
