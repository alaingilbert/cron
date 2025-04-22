package cron

import (
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

type testEntry struct {
	id   EntryID
	next time.Time
}

func newTestEntry(id string, delay time.Duration) *Entry {
	return &Entry{
		ID:     EntryID(id),
		Next:   time.Now().Add(delay),
		Active: true,
	}
}

func TestEntryHeap_PopEmpty(t *testing.T) {
	h := newEntryHeap()
	assert.Nil(t, h.Pop())
}

func TestEntryHeap_PushTwice(t *testing.T) {
	h := newEntryHeap()
	h.Push(newTestEntry("1", 3*time.Second))
	h.Push(newTestEntry("1", 3*time.Second))
	assert.Equal(t, 1, len(h.entries))
}
func TestEntryHeap_PushPopOrder(t *testing.T) {
	h := newEntryHeap()

	// Push entries with different delays
	h.Push(newTestEntry("1", 3*time.Second))
	h.Push(newTestEntry("2", 1*time.Second))
	h.Push(newTestEntry("3", 2*time.Second))

	// Check ordering: should be in 2, 3, 1 order
	wantOrder := []EntryID{"2", "3", "1"}
	for _, want := range wantOrder {
		got := h.Pop()
		if got == nil || got.ID != want {
			t.Errorf("expected ID %v, got %v", want, got)
		}
	}
}

func TestEntryHeap_Remove(t *testing.T) {
	h := newEntryHeap()

	e1 := newTestEntry("1", 2*time.Second)
	e2 := newTestEntry("2", 1*time.Second)
	e3 := newTestEntry("3", 3*time.Second)

	h.Push(e1)
	h.Push(e2)
	h.Push(e3)

	assert.False(t, h.Remove("10"))

	removed := h.Remove("2")
	if !removed {
		t.Errorf("expected entry 2 to be removed")
	}

	if h.index["2"] != 0 {
		t.Errorf("entry 2 should no longer be in index map")
	}

	if h.Peek().ID != "1" {
		t.Errorf("expected next entry to be 1, got %v", h.Peek().ID)
	}
}

func TestEntryHeap_Update(t *testing.T) {
	h := newEntryHeap()

	e1 := newTestEntry("1", 3*time.Second)
	e2 := newTestEntry("2", 5*time.Second)
	e3 := newTestEntry("3", 1*time.Second)

	h.Push(e1)
	h.Push(e2)
	h.Push(e3)

	assert.ErrorIs(t, h.Update("20", time.Now().Add(500*time.Millisecond)), ErrEntryNotFound)

	err := h.Update("2", time.Now().Add(500*time.Millisecond))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	got := h.Pop()
	if got.ID != "2" {
		t.Errorf("expected ID 2 to pop first after update, got %v", got.ID)
	}
}

func TestEntryHeap_ZeroTimeHandling(t *testing.T) {
	h := newEntryHeap()

	e1 := &Entry{ID: "1", Next: time.Time{}}
	e2 := newTestEntry("2", 0)

	h.Push(e1)
	h.Push(e2)

	got := h.Pop()
	if got.ID != "2" {
		t.Errorf("expected entry with real time (ID 2) first, got %v", got.ID)
	}
	got = h.Pop()
	if got.ID != "1" {
		t.Errorf("expected zero-time entry last, got %v", got.ID)
	}
}

func TestEntryHeap_Peek(t *testing.T) {
	h := newEntryHeap()

	if h.Peek() != nil {
		t.Errorf("expected nil peek on empty heap")
	}

	e := newTestEntry("1", 1*time.Second)
	h.Push(e)

	if got := h.Peek(); got != e {
		t.Errorf("peeked entry mismatch: expected %v, got %v", e.ID, got.ID)
	}
}

func TestEntryHeap_CheckValid(t *testing.T) {
	h := newEntryHeap()

	// Empty heap should be valid
	assert.True(t, h.CheckValid())

	// Single entry heap should be valid
	e1 := newTestEntry("1", 1*time.Second)
	h.Push(e1)
	assert.True(t, h.CheckValid())

	// Multiple entries in valid order
	e2 := newTestEntry("2", 2*time.Second)
	h.Push(e2)
	assert.True(t, h.CheckValid())

	// Force an invalid heap by swapping entries manually
	h.swap(0, 1)
	assert.False(t, h.CheckValid())

	// Restore heap validity
	h.Init()
	assert.True(t, h.CheckValid())

	// Test with zero-time entries
	e3 := &Entry{ID: "3", Next: time.Time{}}
	h.Push(e3)
	assert.True(t, h.CheckValid())

	// Test with inactive entries
	e4 := &Entry{ID: "4", Next: time.Now().Add(1 * time.Second), Active: false}
	h.Push(e4)
	assert.True(t, h.CheckValid())
}
