package cron

import (
	"time"
)

func (h *entryHeap) Init() {
	n := h.Len()
	for i := n/2 - 1; i >= 0; i-- {
		h.down(i, n)
	}
}

func (h *entryHeap) CheckValid() bool {
	for i := 1; i < len(h.entries); i++ {
		if h.less(i, h.parent(i)) {
			return false
		}
	}
	return true
}

func (h *entryHeap) Remove(id EntryID) bool {
	idx, ok := h.index[id]
	if !ok {
		return false
	}

	n := h.Len() - 1
	if idx != n {
		h.swap(idx, n)
	}
	h.entries = h.entries[:n]
	delete(h.index, id)

	if idx != n {
		if !h.down(idx, n) {
			h.up(idx)
		}
	}
	return true
}

type entryHeap struct {
	entries []*Entry
	index   map[EntryID]int
}

func newEntryHeap() *entryHeap {
	return &entryHeap{
		index: make(map[EntryID]int),
	}
}

func (h *entryHeap) Clone() *entryHeap {
	newHeap := newEntryHeap()
	newHeap.entries = make([]*Entry, len(h.entries))
	copy(newHeap.entries, h.entries)
	newHeap.index = make(map[EntryID]int)
	for k, v := range h.index {
		newHeap.index[k] = v
	}
	return newHeap
}

func (h *entryHeap) Entries() []Entry {
	clone := h.Clone()
	out := make([]Entry, 0, clone.Len())
	for clone.Len() > 0 {
		out = append(out, *clone.Pop())
	}
	return out
}

// Less function compares two entries considering zero times
func (h *entryHeap) less(i, j int) bool {
	a, b := h.entries[i], h.entries[j]
	if a.Next.IsZero() || !a.Active {
		return false
	} else if b.Next.IsZero() || !b.Active {
		return true
	}
	return a.Next.Before(b.Next)
}

func (h *entryHeap) Len() int { return len(h.entries) }

func (h *entryHeap) Peek() *Entry {
	if len(h.entries) == 0 {
		return nil
	}
	return h.entries[0]
}

func (h *entryHeap) Push(entry *Entry) {
	if _, ok := h.index[entry.ID]; ok {
		return
	}
	h.entries = append(h.entries, entry)
	h.index[entry.ID] = len(h.entries) - 1
	h.up(len(h.entries) - 1)
}

func (h *entryHeap) Pop() *Entry {
	if len(h.entries) == 0 {
		return nil
	}

	n := h.Len() - 1
	h.swap(0, n)
	h.down(0, n)

	entry := h.entries[n]
	h.entries = h.entries[:n]
	delete(h.index, entry.ID)
	return entry
}

func (h *entryHeap) Update(id EntryID, newNext time.Time) error {
	idx, ok := h.index[id]
	if !ok {
		return ErrEntryNotFound
	}

	entry := h.entries[idx]
	entry.Next = newNext

	if h.less(idx, h.parent(idx)) {
		h.up(idx)
	} else {
		h.down(idx, len(h.entries))
	}
	return nil
}

func (h *entryHeap) parent(i int) int {
	return (i - 1) / 2
}

func (h *entryHeap) up(j int) {
	for {
		i := (j - 1) / 2 // parent
		if i == j || !h.less(j, i) {
			break
		}
		h.swap(i, j)
		j = i
	}
}

func (h *entryHeap) down(i, n int) bool {
	i0 := i
	for {
		j1 := 2*i + 1
		if j1 >= n || j1 < 0 {
			break
		}
		j := j1
		if j2 := j1 + 1; j2 < n && h.less(j2, j1) {
			j = j2
		}
		if !h.less(j, i) {
			break
		}
		h.swap(i, j)
		i = j
	}
	return i > i0
}

func (h *entryHeap) swap(i, j int) {
	h.entries[i], h.entries[j] = h.entries[j], h.entries[i]
	h.index[h.entries[i].ID] = i
	h.index[h.entries[j].ID] = j
}
