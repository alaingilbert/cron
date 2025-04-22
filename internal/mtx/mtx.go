// Package mtx provides thread-safe wrappers for values and maps using read-write mutexes.
package mtx

import "sync"

// Mtx wraps a value of any type T with a sync.Mutex for safe concurrent access.
type Mtx[T any] struct {
	sync.Mutex
	v T
}

// NewMtx creates a new Mtx[T] with the initial value v.
func NewMtx[T any](v T) Mtx[T] {
	return Mtx[T]{v: v}
}

// Val returns a raw pointer to the internal value without locking.
// This is unsafe for concurrent access and should be used cautiously.
func (m *Mtx[T]) Val() *T {
	return &m.v
}

// Get returns a copy of the value, safely locking and unlocking the mutex.
func (m *Mtx[T]) Get() T {
	m.Lock()
	defer m.Unlock()
	return m.v
}

// Set sets the internal value to v, safely locking and unlocking the mutex.
func (m *Mtx[T]) Set(v T) {
	m.Lock()
	defer m.Unlock()
	m.v = v
}

// Swap set a new value and return the old value
func (m *Mtx[T]) Swap(newVal T) (old T) {
	m.With(func(v *T) {
		old = *v
		*v = newVal
	})
	return
}

// With locks the mutex and applies the given function to the value.
// It ignores errors.
func (m *Mtx[T]) With(clb func(v *T)) {
	_ = m.WithE(func(tx *T) error {
		clb(tx)
		return nil
	})
}

// WithE locks the mutex and applies the given function to the value,
// allowing the function to return an error that will be propagated.
func (m *Mtx[T]) WithE(clb func(v *T) error) error {
	m.Lock()
	defer m.Unlock()
	return clb(&m.v)
}

// RWMtx is a generic thread-safe wrapper for a value of type T using a RWMutex.
type RWMtx[T any] struct {
	sync.RWMutex
	v T
}

// NewRWMtx creates a new RWMtx instance with the given value.
func NewRWMtx[T any](v T) RWMtx[T] {
	return RWMtx[T]{v: v}
}

// Get ...
func (m *RWMtx[T]) Get() T {
	m.RLock()
	defer m.RUnlock()
	return m.v
}

// Set ...
func (m *RWMtx[T]) Set(v T) {
	m.Lock()
	defer m.Unlock()
	m.v = v
}

// RWith executes a read-only callback with the protected value (non-error version).
func (m *RWMtx[T]) RWith(clb func(v T)) {
	_ = m.RWithE(func(tx T) error {
		clb(tx)
		return nil
	})
}

// RWithE executes a read-only callback with the protected value (error-returning version).
func (m *RWMtx[T]) RWithE(clb func(v T) error) error {
	m.RLock()
	defer m.RUnlock()
	return clb(m.v)
}

// With executes a write callback with a pointer to the protected value (non-error version).
func (m *RWMtx[T]) With(clb func(v *T)) {
	_ = m.WithE(func(tx *T) error {
		clb(tx)
		return nil
	})
}

// WithE executes a write callback with a pointer to the protected value (error-returning version).
func (m *RWMtx[T]) WithE(clb func(v *T) error) error {
	m.Lock()
	defer m.Unlock()
	return clb(&m.v)
}

//----------------------

// RWMtxMap is a thread-safe map wrapper built on RWMtx.
type RWMtxMap[K comparable, V any] struct {
	RWMtx[map[K]V]
}

// NewRWMtxMap creates a new empty thread-safe map.
func NewRWMtxMap[K comparable, V any]() RWMtxMap[K, V] {
	return RWMtxMap[K, V]{RWMtx: NewRWMtx(make(map[K]V))}
}

// Store adds or updates a key-value pair in the map.
func (m *RWMtxMap[K, V]) Store(k K, v V) {
	m.With(func(m *map[K]V) { (*m)[k] = v })
}

// Load retrieves a value for a key and indicates existence.
func (m *RWMtxMap[K, V]) Load(k K) (out V, ok bool) {
	m.RWith(func(m map[K]V) { out, ok = m[k] })
	return
}

// LoadAndDelete deletes the value for a key, returning the previous value if any.
// The loaded result reports whether the key was present.
func (m *RWMtxMap[K, V]) LoadAndDelete(k K) (out V, loaded bool) {
	m.With(func(m *map[K]V) {
		out, loaded = (*m)[k]
		delete(*m, k)
	})
	return
}

// Delete removes a key-value pair from the map.
func (m *RWMtxMap[K, V]) Delete(k K) {
	m.With(func(m *map[K]V) { delete(*m, k) })
	return
}

// Len returns the number of elements in the map.
func (m *RWMtxMap[K, V]) Len() (out int) {
	m.RWith(func(m map[K]V) { out = len(m) })
	return
}

// Clear removes all elements from the map.
func (m *RWMtxMap[K, V]) Clear() {
	m.With(func(m *map[K]V) { clear(*m) })
}

//----------------------

type RWMtxSlice[T any] struct {
	RWMtx[[]T]
}

// Clear clears the slice, removing all values
func (s *RWMtxSlice[T]) Clear() {
	s.With(func(v *[]T) { *v = nil; *v = make([]T, 0) })
}

// Remove removes the element at position i within the slice,
// shifting all elements after it to the left
// Panics if index is out of bounds
func (s *RWMtxSlice[T]) Remove(i int) (out T) {
	s.With(func(v *[]T) {
		out = (*v)[i]
		*v = (*v)[:i+copy((*v)[i:], (*v)[i+1:])]
	})
	return
}

func (s *RWMtxSlice[T]) Each(clb func(T)) {
	s.RWith(func(v []T) {
		for _, e := range v {
			clb(e)
		}
	})
}

func (s *RWMtxSlice[T]) Append(els ...T) {
	s.With(func(v *[]T) { *v = append(*v, els...) })
}

func (s *RWMtxSlice[T]) Unshift(el T) {
	s.With(func(v *[]T) { *v = append([]T{el}, *v...) })
}

func (s *RWMtxSlice[T]) Clone() (out []T) {
	s.RWith(func(v []T) {
		out = make([]T, len(v))
		copy(out, v)
	})
	return
}

// Len returns the length of the slice
func (s *RWMtxSlice[T]) Len() (out int) {
	s.RWith(func(v []T) { out = len(v) })
	return
}
