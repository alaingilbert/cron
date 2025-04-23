package utils

import (
	"context"
	cryptoRand "crypto/rand"
	"encoding/hex"
	"github.com/alaingilbert/cron/internal/core"
	"io"
	"iter"
	"math/rand/v2"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Ptr ...
func Ptr[T any](v T) *T { return &v }

// Ternary ...
func Ternary[T any](predicate bool, a, b T) T {
	if predicate {
		return a
	}
	return b
}

// TernaryOrZero ...
func TernaryOrZero[T any](predicate bool, a T) (zero T) {
	return Ternary(predicate, a, zero)
}

// Or return "a" if it is non-zero otherwise "b"
func Or[T comparable](a, b T) (zero T) {
	return Ternary(a != zero, a, b)
}

// Default return the value of "v" if not nil, otherwise return the default "d" value
func Default[T any](v *T, d T) T {
	if v == nil {
		return d
	}
	return *v
}

// First returns the first argument
func First[T any](a T, _ ...any) T { return a }

// Second returns the second argument
func Second[T any](_ any, a T, _ ...any) T { return a }

// BuildConfig ...
func BuildConfig[C any, F ~func(*C)](opts []F) *C {
	var cfg C
	return ApplyOptions(&cfg, opts...)
}

// ApplyOptions ...
func ApplyOptions[C any, F ~func(*C)](cfg *C, opts ...F) *C {
	for _, opt := range opts {
		opt(cfg)
	}
	return cfg
}

// Cast ...
func Cast[T any](origin any) (T, bool) {
	if val, ok := origin.(reflect.Value); ok {
		origin = val.Interface()
	}
	val, ok := origin.(T)
	return val, ok
}

// TryCast ...
func TryCast[T any](origin any) bool {
	_, ok := Cast[T](origin)
	return ok
}

// CastInto ...
func CastInto[T any](origin any, into *T) bool {
	originVal, ok := origin.(reflect.Value)
	if !ok {
		originVal = reflect.ValueOf(origin)
	}
	if originVal.IsValid() {
		if _, ok := originVal.Interface().(T); ok {
			rv := reflect.ValueOf(into)
			if rv.Kind() == reflect.Pointer && !rv.IsNil() {
				rv.Elem().Set(originVal)
				return true
			}
		}
	}
	return false
}

// EnsureRange ensure min is smaller or equal to max
func EnsureRange(min, max int64) (int64, int64) {
	if max < min {
		min, max = max, min
	}
	return min, max
}

func RandomFrom(r *rand.Rand, min, max int64) int64 {
	if min == max {
		return min
	}
	min, max = EnsureRange(min, max)
	return r.Int64N(max-min+1) + min
}

// Random generates a number between min and max inclusively
func Random(min, max int64) (out int64) {
	core.RandSrc.With(func(v **rand.Rand) {
		out = RandomFrom(*v, min, max)
	})
	return
}

// RandDuration generates random duration
func RandDuration(min, max time.Duration) time.Duration {
	n := Random(min.Nanoseconds(), max.Nanoseconds())
	return time.Duration(n)
}

func SliceSeq[T any](s []T) iter.Seq[T] {
	return func(yield func(T) bool) {
		for _, v := range s {
			if !yield(v) {
				return
			}
		}
	}
}

// Find looks through each value in the list, returning the first one that passes a truth test (predicate),
// or nil if no value passes the test.
// The function returns as soon as it finds an acceptable element, and doesn't traverse the entire list
func Find[T any](arr []T, predicate func(T) bool) (out *T) {
	return FindIter(SliceSeq(arr), predicate)
}

// FindIter ...
func FindIter[T any](it iter.Seq[T], predicate func(T) bool) (out *T) {
	return First(FindIdxIter(it, predicate))
}

// FindIdx ...
func FindIdx[T any](arr []T, predicate func(T) bool) (*T, int) {
	return FindIdxIter(SliceSeq(arr), predicate)
}

// FindIdxIter ...
func FindIdxIter[T any](it iter.Seq[T], predicate func(T) bool) (*T, int) {
	var i int
	for el := range it {
		if predicate(el) {
			return &el, i
		}
		i++
	}
	return nil, -1
}

// Some returns true if any of the values in the list pass the predicate truth test.
// Short-circuits and stops traversing the list if a true element is found.
func Some[T any](arr []T, predicate func(T) bool) bool {
	return SomeIter(SliceSeq(arr), predicate)
}

// SomeIter ...
func SomeIter[T any](it iter.Seq[T], predicate func(T) bool) bool {
	return FindIter(it, predicate) != nil
}

// ForEach ...
func ForEach[T any](arr []T, clb func(T)) {
	for _, el := range arr {
		clb(el)
	}
}

// NonBlockingSend ...
func NonBlockingSend[T any](c chan T, el T) {
	select {
	case c <- el:
	default:
	}
}

// ShortDur ...
func ShortDur(v any) string {
	if d, ok := v.(time.Duration); ok {
		s := d.Abs().Round(time.Second).String()
		if strings.HasSuffix(s, "m0s") || strings.HasSuffix(s, "h0m") {
			s = s[:len(s)-2]
		}
		s += Ternary(d < 0, " ago", " from now")
		return s
	} else if d, ok := v.(time.Time); ok {
		return ShortDur(time.Until(d))
	} else if d, ok := v.(uint64); ok {
		return ShortDur(time.Duration(d) * time.Second)
	} else if d, ok := v.(int64); ok {
		return ShortDur(time.Duration(d) * time.Second)
	} else if d, ok := v.(int32); ok {
		return ShortDur(time.Duration(d) * time.Second)
	} else if d, ok := v.(int64); ok {
		return ShortDur(time.Duration(d) * time.Second)
	} else if d, ok := v.(float32); ok {
		return ShortDur(time.Duration(d) * time.Second)
	} else if d, ok := v.(float64); ok {
		return ShortDur(time.Duration(d) * time.Second)
	}
	return "n/a"
}

// UuidV4 generates a new UUID v4
func UuidV4() (uuid [16]byte) {
	_, _ = io.ReadFull(cryptoRand.Reader, uuid[:])
	uuid[6] = (uuid[6] & 0x0f) | 0x40 // Version 4
	uuid[8] = (uuid[8] & 0x3f) | 0x80 // Variant is 10
	return
}

// UuidV4Str ...
func UuidV4Str() string {
	uuid := UuidV4()
	var buf [32]byte
	hex.Encode(buf[:], uuid[:4])
	hex.Encode(buf[8:12], uuid[4:6])
	hex.Encode(buf[12:16], uuid[6:8])
	hex.Encode(buf[16:20], uuid[8:10])
	hex.Encode(buf[20:], uuid[10:])
	return string(buf[:])
}

// NewAtomicPtr ...
func NewAtomicPtr[T any](v *T) atomic.Pointer[T] {
	var p atomic.Pointer[T]
	p.Store(v)
	return p
}

// ParallelForEach ...
func ParallelForEach[T any](ctx context.Context, s iter.Seq[T], workerCount int, fn func(T)) {
	if workerCount <= 0 {
		workerCount = runtime.NumCPU()
	}
	var wg sync.WaitGroup
	ch := make(chan T, workerCount*2) // Buffered channel for pipelining
	// Worker pool
	wg.Add(workerCount)
	for i := 0; i < workerCount; i++ {
		go func() {
			defer wg.Done()
			for {
				select {
				case item, ok := <-ch:
					if ok {
						fn(item)
					} else {
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	// Feed items to workers
loop:
	for item := range s {
		select {
		case ch <- item:
		case <-ctx.Done():
			break loop
		}
	}
	close(ch)
	wg.Wait()
}
