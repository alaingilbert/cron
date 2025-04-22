package mtx

import (
	"errors"
	"fmt"
	"github.com/stretchr/testify/assert"
	"sync"
	"testing"
)

func TestNewMtx(t *testing.T) {
	m := NewMtx(42)
	assert.Equal(t, 42, m.Get())
}

func TestValUnsafeAccess(t *testing.T) {
	m := NewMtx("hello")
	*m.Val() = "world" // direct, unsafe access
	assert.Equal(t, "world", m.Get())
}

func TestSetAndGet(t *testing.T) {
	m := NewMtx(10)
	m.Set(20)
	assert.Equal(t, 20, m.Get())

	m1 := NewRWMtx(10)
	m1.Set(20)
	assert.Equal(t, 20, m1.Get())
}

func TestMtx_Swap(t *testing.T) {
	m := NewMtx("old")
	old := m.Swap("new")
	assert.Equal(t, "old", old)
	assert.Equal(t, "new", m.Get())
}

func TestWith(t *testing.T) {
	m := NewMtx(5)
	m.With(func(v *int) {
		*v += 10
	})
	assert.Equal(t, 15, m.Get())
}

func TestWithE_Success(t *testing.T) {
	m := NewMtx("a")
	err := m.WithE(func(v *string) error {
		*v += "b"
		return nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "ab", m.Get())
}

func TestWithE_Error(t *testing.T) {
	m := NewMtx(100)
	err := m.WithE(func(v *int) error {
		return errors.New("some error")
	})
	assert.Error(t, err)
	assert.Equal(t, 100, m.Get()) // value should remain unchanged
}

func TestRWMtx(t *testing.T) {
	mtx := NewRWMtx(42)

	mtx.RWith(func(v int) {
		if v != 42 {
			t.Errorf("expected 42, got %d", v)
		}
	})

	mtx.With(func(v *int) {
		*v = 100
	})

	mtx.RWith(func(v int) {
		if v != 100 {
			t.Errorf("expected 100, got %d", v)
		}
	})
}

func TestRWMtxMap(t *testing.T) {
	m := NewRWMtxMap[string, int]()

	m.Store("a", 1)
	v, ok := m.Load("a")
	if !ok || v != 1 {
		t.Errorf("expected to load 1, got %d, ok=%v", v, ok)
	}

	m.Store("b", 2)
	m.Store("c", 3)
	if m.Len() != 3 {
		t.Errorf("expected length 3, got %d", m.Len())
	}

	m.Delete("a")
	_, ok = m.Load("a")
	if ok {
		t.Errorf("expected key 'a' to be deleted")
	}

	val, ok := m.LoadAndDelete("b")
	if !ok || val != 2 {
		t.Errorf("expected to load 2, got %d, ok=%v", v, ok)
	}
	if m.Len() != 1 {
		t.Errorf("expected length 1, got %d", m.Len())
	}

	_, ok = m.LoadAndDelete("non-existent")
	if ok {
		t.Errorf("expected to not load non-existent key")
	}

	m.Clear()
	if m.Len() != 0 {
		t.Errorf("expected map to be cleared")
	}
}

func TestRWMtxMap_ConcurrentAccess(t *testing.T) {
	m := NewRWMtxMap[int, int]()
	const n = 100

	var wg sync.WaitGroup
	wg.Add(n * 2)

	// Writers
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			m.Store(i, i*10)
		}(i)
	}

	// Readers
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_ = m.Len()
			m.Load(i)
		}(i)
	}

	wg.Wait()
}

func TestRWMtx_WithE(t *testing.T) {
	mtx := NewRWMtx(1)

	err := mtx.WithE(func(v *int) error {
		return fmt.Errorf("fail")
	})

	if err == nil || err.Error() != "fail" {
		t.Errorf("expected error")
	}
}

func TestRWMtxSlice_Append(t *testing.T) {
	var s RWMtxSlice[int]
	s.Append(1, 2, 3)
	assert.Equal(t, []int{1, 2, 3}, s.Clone())
}

func TestRWMtxSlice_Unshift(t *testing.T) {
	var s RWMtxSlice[int]
	s.Append(2, 3)
	s.Unshift(1)
	assert.Equal(t, []int{1, 2, 3}, s.Clone())
}

func TestRWMtxSlice_Remove(t *testing.T) {
	var s RWMtxSlice[int]
	s.Append(1, 2, 3)
	v := s.Remove(1)
	assert.Equal(t, 2, v)
	assert.Equal(t, []int{1, 3}, s.Clone())
}

func TestRWMtxSlice_RemoveOutOfBounds(t *testing.T) {
	var s RWMtxSlice[int]
	s.Append(1, 2, 3)

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on out-of-bounds Remove, got none")
		}
	}()
	_ = s.Remove(5)
}

func TestRWMtxSlice_Clear(t *testing.T) {
	var s RWMtxSlice[int]
	s.Append(1, 2, 3)
	s.Clear()
	assert.Equal(t, 0, s.Len())
	assert.Equal(t, []int{}, s.Clone())
}

func TestRWMtxSlice_Clone(t *testing.T) {
	var s RWMtxSlice[int]
	s.Append(1, 2, 3)

	clone := s.Clone()
	assert.Equal(t, []int{1, 2, 3}, clone)

	// Ensure it's a deep copy
	clone[0] = 99
	assert.Equal(t, []int{1, 2, 3}, s.Clone())
}

func TestRWMtxSlice_Each(t *testing.T) {
	var s RWMtxSlice[int]
	s.Append(1, 2, 3)

	var out []int
	s.Each(func(v int) {
		out = append(out, v)
	})

	assert.Equal(t, []int{1, 2, 3}, out)
}

func TestRWMtxSlice_Len(t *testing.T) {
	var s RWMtxSlice[string]
	s.Append("a", "b", "c")
	assert.Equal(t, 3, s.Len())
}
