package cron

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alaingilbert/cron/internal/pubsub"
	"github.com/alaingilbert/cron/internal/utils"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
)

// run one logic cycle
func cycle(cron *Cron) {
	cron.entriesUpdated()
}

func advanceAndCycle(cron *Cron, d time.Duration) {
	advanceAndCycleNoWait(cron, d)
	cron.waitAllJobsCompleted()
}

func advanceAndCycleNoWait(cron *Cron, d time.Duration) {
	if fc, ok := cron.clock.(*clockwork.FakeClock); ok {
		fc.Advance(d)
	}
	cycle(cron)
}

func recvWithTimeout(t *testing.T, ch <-chan struct{}, msg ...string) {
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal(msg)
	}
}

type PanicJob struct{}

func (d PanicJob) Run(context.Context, EntryID) error {
	panic("YOLO")
}

func TestFuncPanicRecovery(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(1984, time.April, 4, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	cron.Start()
	defer cron.Stop()
	ch := make(chan string, 1)
	_, _ = cron.AddJob("* * * * * *", func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- fmt.Sprintf("%v", r)
			}
		}()
		panic("PANIC ERROR")
	})
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, "PANIC ERROR", <-ch)
}

func newNoOpLogger() Logger {
	return DiscardLogger
}

func newErrLogger() *slog.Logger {
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError})
	return slog.New(handler)
}

func TestJobPanicRecovery(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(1984, time.April, 4, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithLogger(newNoOpLogger()).WithParser(secondParser).Build()
	cron.Start()
	_, _ = cron.AddJob("* * * * * ?", PanicJob{})
	advanceAndCycle(cron, time.Second)
}

func TestLogError(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(1984, time.April, 4, 0, 0, 0, 0, time.UTC))
	buf := bytes.NewBuffer(nil)
	l := slog.New(slog.NewTextHandler(buf, nil))
	cron := New().WithClock(clock).WithLogger(l).WithParser(secondParser).Build()
	_, _ = cron.AddJob("* * * * * ?", func() error { return errors.New("some error") })
	cron.Start()
	advanceAndCycle(cron, time.Second)
	assert.Contains(t, buf.String(), "some error")
}

func TestSetEntryActive(t *testing.T) {
	var calls atomic.Int32
	clock := clockwork.NewFakeClockAt(time.Date(1984, time.April, 4, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	id, _ := cron.AddJob("* * * * * ?", baseJob{&calls})
	cron.Start()
	assert.True(t, utils.First(cron.Entry(id)).Active)
	cron.Disable(id)
	assert.False(t, utils.First(cron.Entry(id)).Active)
	cron.Enable(id)
	cron.Enable(id) // unchanged
	assert.True(t, utils.First(cron.Entry(id)).Active)
	cron.Enable("not-exist") // not found
}

func TestRunNow(t *testing.T) {
	var calls atomic.Int32
	clock := clockwork.NewFakeClockAt(time.Date(1984, time.April, 4, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	id, _ := cron.AddJob("1 1 * * * *", baseJob{&calls})
	cron.Start()
	_ = cron.RunNow(id)
	_ = cron.RunNow("not-exist")
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
}

// Without using map: BenchmarkRunNow-12    	   36181	     30909 ns/op
// With map         : BenchmarkRunNow-12    	  172833	      5946 ns/op
func BenchmarkRunNow(b *testing.B) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	var id EntryID
	for i := 0; i < 1000; i++ {
		id, _ = cron.AddJob("* * * * * *", func() {})
	}
	cron.Start()
	for i := 0; i < b.N; i++ {
		_ = cron.RunNow(id)
	}
}

func TestGetNextTime(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	_, _ = cron.AddJob("1 1 * * * *", func() {})
	cron.Start()
	assert.Equal(t, clock.Now().Add(61*time.Second), cron.GetNextTime())
}

func TestOnceJob(t *testing.T) {
	var calls atomic.Int32
	clock := clockwork.NewFakeClockAt(time.Date(1984, time.April, 4, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	cron.Start()
	_, _ = cron.AddJob("* * * * * *", Once(baseJob{&calls}))
	_, _ = cron.AddJob("* * * * * *", baseJob{&calls})
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(2), calls.Load())
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(3), calls.Load())
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(4), calls.Load())
}

// Just show off that we can test crons that runs once a month
func TestOnceAMonth(t *testing.T) {
	var calls atomic.Int32
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	_, _ = cron.AddJob("@monthly", baseJob{&calls})
	cron.Start()
	advanceAndCycle(cron, 16*24*time.Hour)
	advanceAndCycle(cron, 16*24*time.Hour)
	advanceAndCycle(cron, 16*24*time.Hour)
	advanceAndCycle(cron, 16*24*time.Hour)
	advanceAndCycle(cron, 16*24*time.Hour)
	advanceAndCycle(cron, 16*24*time.Hour)
	advanceAndCycle(cron, 16*24*time.Hour)
	advanceAndCycle(cron, 16*24*time.Hour)
	assert.Equal(t, int32(4), calls.Load())
}

// Start and stop cron with no entries.
func TestNoEntries(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(1984, time.April, 4, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	cron.Start()
	c1 := make(chan struct{})
	go func() {
		<-cron.Stop()
		close(c1)
	}()
	recvWithTimeout(t, c1, "expected cron will be stopped immediately")
}

func TestStopWait(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 1, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	c1 := make(chan struct{})
	c2 := make(chan struct{})
	_, _ = cron.AddJob("1 0 1 * * *", func(jr JobRun) {
		jr.Logger()
		clock.SleepNotify(time.Minute, c1)
	})
	cron.Start()
	advanceAndCycleNoWait(cron, time.Second)
	go func() {
		<-c1
		<-cron.Stop() // wait until all ongoing jobs terminate
		close(c2)
	}()
	<-c1
	advanceAndCycle(cron, 61*time.Second)
	recvWithTimeout(t, c2, "expected cron will be stopped immediately")
}

func TestWait(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 1, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	c1 := make(chan struct{})
	c2 := make(chan struct{})
	_, _ = cron.AddJob("1 0 1 * * *", func() {
		clock.SleepNotify(time.Minute, c1)
	})
	cron.Start()
	advanceAndCycleNoWait(cron, time.Second)
	go func() {
		<-c1
		cron.Wait() // wait until all ongoing jobs terminate
		close(c2)
	}()
	<-c1
	advanceAndCycle(cron, 61*time.Second)
	recvWithTimeout(t, c2, "expected cron will be stopped immediately")
}

// Start, stop, then add an entry. Verify entry doesn't run.
func TestStopCausesJobsToNotRun(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(1984, time.April, 4, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	cron.Start()
	cron.Stop()
	var calls atomic.Int32
	_, _ = cron.AddJob("* * * * * ?", baseJob{&calls})
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(0), calls.Load())
}

// Add a job, start cron, expect it runs.
func TestAddBeforeRunning(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(1984, time.April, 4, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	var calls atomic.Int32
	_, _ = cron.AddJob("* * * * * ?", baseJob{&calls})
	cron.Start()
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
}

// Start cron, add a job, expect it runs.
func TestAddWhileRunning(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(1984, time.April, 4, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	cron.Start()
	var calls atomic.Int32
	_, _ = cron.AddJob("* * * * * ?", baseJob{&calls})
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
}

// Test for #34. Adding a job after calling start results in multiple job invocations
func TestAddWhileRunningWithDelay(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(1984, time.April, 4, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	cron.Start()
	advanceAndCycle(cron, time.Second)
	advanceAndCycle(cron, time.Second)
	advanceAndCycle(cron, time.Second)
	var calls atomic.Int32
	_, _ = cron.AddJob("* * * * * *", baseJob{&calls})
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
}

// Test timing with Entries.
func TestSnapshotEntries(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(1984, time.April, 4, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	var calls atomic.Int32
	_, _ = cron.AddJob("@every 2s", baseJob{&calls})
	cron.Start()
	advanceAndCycle(cron, time.Second)
	// Cron should fire in 2 seconds. After 1 second, call Entries.
	cron.Entries()
	advanceAndCycle(cron, time.Second)
	// Even though Entries was called, the cron should fire at the 2 second mark.
	assert.Equal(t, int32(1), calls.Load())
}

func TestEntry(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(1984, time.April, 4, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	id, _ := cron.AddJob("@every 2s", func() {})
	cron.Start()
	entry1, _ := cron.Entry(id)
	_, err := cron.Entry("do-not-exist")
	assert.Equal(t, id, entry1.ID)
	assert.ErrorIs(t, err, ErrEntryNotFound)
}

// Test that the entries are correctly sorted.
// Add a bunch of long-in-the-future entries, and an immediate entry, and ensure
// that the immediate entry runs immediately.
// Also: Test that multiple jobs run in the same instant.
func TestMultipleEntries(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(1984, time.April, 4, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	var calls atomic.Int32
	_, _ = cron.AddJob("0 0 0 1 1 ?", baseJob{&calls})
	_, _ = cron.AddJob("* * * * * ?", baseJob{&calls})
	_, _ = cron.AddJob("0 0 0 31 12 ?", baseJob{&calls})
	_, _ = cron.AddJob("* * * * * ?", baseJob{&calls})
	cron.Start()
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(2), calls.Load())
}

// Test running the same job twice.
func TestRunningJobTwice(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(1984, time.April, 4, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	var calls atomic.Int32
	_, _ = cron.AddJob("0 0 0 1 1 ?", baseJob{&calls})
	_, _ = cron.AddJob("0 0 0 31 12 ?", baseJob{&calls})
	_, _ = cron.AddJob("* * * * * ?", baseJob{&calls})
	cron.Start()
	advanceAndCycle(cron, time.Second)
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(2), calls.Load())
}

func TestRunningMultipleSchedules(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(1984, time.April, 4, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	var calls atomic.Int32
	_, _ = cron.AddJob("0 0 0 1 1 ?", baseJob{&calls})
	_, _ = cron.AddJob("0 0 0 31 12 ?", baseJob{&calls})
	_, _ = cron.AddJob("* * * * * ?", baseJob{&calls})
	_, _ = cron.Schedule(Every(time.Minute), baseJob{&calls})
	_, _ = cron.Schedule(Every(time.Second), baseJob{&calls})
	_, _ = cron.Schedule(Every(time.Hour), baseJob{&calls})
	cron.Start()
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(2), calls.Load())
}

// Test that the cron is run in the local time zone (as opposed to UTC).
func TestLocalTimezone(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.Local))
	now := clock.Now().Local()
	spec := fmt.Sprintf("%d,%d %d %d %d %d ?",
		now.Second()+1, now.Second()+2, now.Minute(), now.Hour(), now.Day(), now.Month())
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	var calls atomic.Int32
	_, _ = cron.AddJob(spec, baseJob{&calls})
	cron.Start()
	advanceAndCycle(cron, time.Second)
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(2), calls.Load())
}

// Test that the cron is run in the given time zone (as opposed to local).
func TestNonLocalTimezone(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(1984, time.April, 4, 0, 0, 0, 0, time.UTC))
	loc, err := time.LoadLocation("Atlantic/Cape_Verde")
	assert.NoError(t, err, "Failed to load time zone Atlantic/Cape_Verde")
	now := clock.Now().In(loc)
	spec := fmt.Sprintf("%d,%d %d %d %d %d ?",
		now.Second()+1, now.Second()+2, now.Minute(), now.Hour(), now.Day(), now.Month())
	cron := New().WithClock(clock).WithLocation(loc).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	var calls atomic.Int32
	_, _ = cron.AddJob(spec, baseJob{&calls})
	cron.Start()
	advanceAndCycle(cron, time.Second)
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(2), calls.Load())
}

// Test that calling stop before start silently returns without
// blocking the stop channel.
func TestStopWithoutStart(t *testing.T) {
	clock := clockwork.NewRealClock()
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	cron.Stop()
}

// Simple job that increment an atomic counter every time the job is run
type baseJob struct{ calls *atomic.Int32 }

func (j baseJob) Run(context.Context, *Cron, JobRun) error {
	j.calls.Add(1)
	return nil
}

type namedJob struct {
	calls *atomic.Int32
	name  string
}

func (t namedJob) Run(context.Context, *Cron, JobRun) error {
	t.calls.Add(1)
	return nil
}

// Test that adding an invalid job spec returns an error
func TestInvalidJobSpec(t *testing.T) {
	clock := clockwork.NewRealClock()
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	_, err := cron.AddJob("this will not parse", func() {})
	assert.Error(t, err, "expected an error with invalid spec, got nil")
}

// Test blocking run method behaves as Start()
func TestBlockingRun(t *testing.T) {
	var calls atomic.Int32
	clock := clockwork.NewFakeClockAt(time.Date(1984, time.April, 4, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	_, _ = cron.AddJob("* * * * * ?", baseJob{&calls})
	ch := make(chan struct{})
	go func() {
		cron.Run()
		calls.Add(1)
		close(ch)
	}()
	// Spinlock wait until cron is running
	for !cron.isRunning() {
	}
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
	cron.Stop()
	<-ch
	assert.Equal(t, int32(2), calls.Load())
}

// Test that double-running is a no-op
func TestBlockingRunNoop(t *testing.T) {
	var calls atomic.Int32
	clock := clockwork.NewFakeClockAt(time.Date(1984, time.April, 4, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	_, _ = cron.AddJob("* * * * * ?", baseJob{&calls})
	c1 := make(chan struct{})
	go func() {
		cron.Start()
		close(c1)
	}()
	<-c1
	assert.False(t, cron.Run())
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
}

// Test that double-running is a no-op
func TestStartNoop(t *testing.T) {
	clock := clockwork.NewRealClock()
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	started := cron.Start()
	assert.True(t, started)
	started = cron.Start()
	assert.False(t, started)
}

func TestLocationThreadSafe(t *testing.T) {
	newLoc, _ := time.LoadLocation("Atlantic/Cape_Verde")
	clock := clockwork.NewFakeClockAt(time.Date(1984, time.April, 4, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithLocation(time.UTC).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	cron.Start()
	wg := sync.WaitGroup{}
	wg.Add(2)
	go func() {
		for i := 0; i < 100; i++ {
			cron.SetLocation(newLoc)
		}
		wg.Done()
	}()
	go func() {
		for i := 0; i < 100; i++ {
			cron.getLocation()
		}
		wg.Done()
	}()
	wg.Wait()
}

// TestChangeLocationWhileRunning ...
func TestChangeLocationWhileRunning(t *testing.T) {
	newLoc, _ := time.LoadLocation("Atlantic/Cape_Verde")
	clock := clockwork.NewFakeClockAt(time.Date(1984, time.April, 4, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithLocation(time.UTC).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	cron.Start()
	_, _ = cron.AddJob("* * * * * ?", func() {})
	_, _ = cron.AddJob("0 0 1 * * ?", func() {})
	entries := cron.Entries()
	assert.Equal(t, clock.Now().Add(time.Second).In(time.UTC), entries[0].Next)
	assert.Equal(t, time.Date(1984, time.April, 4, 1, 0, 0, 0, time.UTC), entries[1].Next)
	cron.SetLocation(newLoc)
	entries = cron.Entries()
	assert.Equal(t, clock.Now().Add(time.Second).In(newLoc), entries[0].Next)
	assert.Equal(t, time.Date(1984, time.April, 4, 2, 0, 0, 0, time.UTC).In(newLoc), entries[1].Next)
	assert.Equal(t, time.Date(1984, time.April, 4, 1, 0, 0, 0, newLoc), entries[1].Next)
}

func TestChangeLocationWhileRunning2(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 2, 0, 0, 0, time.UTC))
	newLoc := time.FixedZone("TMZ", 3600)
	cron := New().WithClock(clock).WithLocation(time.UTC).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	cron.Start()
	_, _ = cron.AddJob("* * * * * ?", func() {})
	_, _ = cron.AddJob("0 0 1 * * ?", func() {})
	entries := cron.Entries()
	assert.Equal(t, clock.Now().Add(time.Second).In(time.UTC), entries[0].Next)
	assert.Equal(t, time.Date(2000, time.January, 2, 1, 0, 0, 0, time.UTC), entries[1].Next)
	cron.SetLocation(newLoc)
	entries = cron.Entries()
	assert.Equal(t, clock.Now().Add(time.Second).In(newLoc), entries[0].Next)
	assert.Equal(t, time.Date(2000, time.January, 2, 0, 0, 0, 0, time.UTC).In(newLoc), entries[1].Next)
	assert.Equal(t, time.Date(2000, time.January, 2, 1, 0, 0, 0, newLoc), entries[1].Next)
}

func TestChangeLocationWhileRunning3(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC))
	newLoc := time.FixedZone("TMZ", 2*3600)
	cron := New().WithClock(clock).WithLocation(time.UTC).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	cron.Start()
	id1, _ := cron.AddJob("0 0 1 * * *", func() {})
	id2, _ := cron.AddJob("0 0 3 * * *", func() {})
	entries := cron.Entries()
	assert.Equal(t, time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC), cron.now())
	assert.Equal(t, time.Date(2000, time.January, 1, 1, 0, 0, 0, time.UTC), entries[0].Next)
	assert.Equal(t, time.Date(2000, time.January, 1, 3, 0, 0, 0, time.UTC), entries[1].Next)
	assert.Equal(t, id1, entries[0].ID)
	assert.Equal(t, id2, entries[1].ID)
	cron.SetLocation(newLoc)
	entries = cron.Entries()
	assert.Equal(t, time.Date(2000, time.January, 1, 2, 0, 0, 0, newLoc), cron.now())
	assert.Equal(t, time.Date(2000, time.January, 1, 3, 0, 0, 0, newLoc), entries[0].Next)
	assert.Equal(t, time.Date(2000, time.January, 2, 1, 0, 0, 0, newLoc), entries[1].Next)
	assert.Equal(t, id2, entries[0].ID)
	assert.Equal(t, id1, entries[1].ID)
}

// Simple test using Runnables.
func TestJob(t *testing.T) {
	var calls atomic.Int32
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 1, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	_, _ = cron.AddJob("0 0 0 30 Feb ?", namedJob{&calls, "job5"}) // invalid spec (Next will be zero time)
	_, _ = cron.AddJob("0 0 0 1 1 ?", namedJob{&calls, "job3"})
	_, _ = cron.AddJob("* * * * * ?", namedJob{&calls, "job0"})
	_, _ = cron.AddJob("1 0 0 1 1 ?", namedJob{&calls, "job4"})
	_, _ = cron.Schedule(Every(5*time.Second+5*time.Nanosecond), namedJob{&calls, "job1"})
	_, _ = cron.Schedule(Every(5*time.Minute), namedJob{&calls, "job2"})
	cron.Start()
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
	// Ensure the entries are in the right order.
	for i, entry := range cron.Entries() {
		assert.Equal(t, fmt.Sprintf("job%d", i), entry.Job().(namedJob).name)
	}
}

type ZeroSchedule struct{}

func (*ZeroSchedule) Next(time.Time) time.Time {
	return time.Time{}
}

// Tests that job without time does not run
func TestJobWithZeroTimeDoesNotRun(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(1984, time.April, 4, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	var calls atomic.Int32
	_, _ = cron.AddJob("* * * * * *", baseJob{&calls})
	_, _ = cron.Schedule(new(ZeroSchedule), FuncJob(func(context.Context, *Cron, JobRun) error {
		t.Error("expected zero task will not run")
		return nil
	}))
	cron.Start()
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
}

func TestRemove(t *testing.T) {
	var calls atomic.Int32
	clock := clockwork.NewFakeClockAt(time.Date(1984, time.April, 4, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	id, _ := cron.AddJob("* * * * * *", baseJob{&calls})
	assert.Equal(t, int32(0), calls.Load())
	cron.Start()
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(2), calls.Load())
	advanceAndCycle(cron, 500*time.Millisecond)
	cron.Remove(id)
	assert.Equal(t, int32(2), calls.Load())
	advanceAndCycle(cron, 500*time.Millisecond)
	assert.Equal(t, int32(2), calls.Load())
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(2), calls.Load())
}

// Issue #206
// Ensure that the next run of a job after removing an entry is accurate.
func TestScheduleAfterRemoval(t *testing.T) {
	// The first time this job is run, set a timer and remove the other job
	// 750ms later. Correct behavior would be to still run the job again in
	// 250ms, but the bug would cause it to run instead 1s later.
	var calls atomic.Int32
	clock := clockwork.NewFakeClockAt(time.Date(1984, time.April, 4, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	hourJob, _ := cron.Schedule(Every(time.Hour), FuncJob(func(context.Context, *Cron, JobRun) error { return nil }))
	_, _ = cron.Schedule(Every(time.Second), FuncJob(func(context.Context, *Cron, JobRun) error {
		switch calls.Load() {
		case 0:
			calls.Add(1)
		case 1:
			calls.Add(1)
			advanceAndCycleNoWait(cron, 750*time.Millisecond)
			cron.Remove(hourJob)
		case 2:
			calls.Add(1)
		case 3:
			panic("unexpected 3rd call")
		}
		return nil
	}))
	cron.Start()
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(2), calls.Load())
	advanceAndCycle(cron, 250*time.Millisecond)
	assert.Equal(t, int32(3), calls.Load())
}

func TestTwoCrons(t *testing.T) {
	var calls atomic.Int32
	clock := clockwork.NewFakeClockAt(time.Date(1984, time.April, 4, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	_, _ = cron.AddJob("1 * * * * *", baseJob{&calls})
	_, _ = cron.AddJob("3 * * * * *", baseJob{&calls})
	assert.Equal(t, int32(0), calls.Load())
	cron.Start()
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(2), calls.Load())
}

func TestMultipleCrons(t *testing.T) {
	var calls atomic.Int32
	clock := clockwork.NewFakeClockAt(time.Date(1984, time.April, 4, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	_, _ = cron.AddJob("1 * * * * *", baseJob{&calls}) // #1
	_, _ = cron.AddJob("* * * * * *", baseJob{&calls}) // #2
	_, _ = cron.AddJob("3 * * * * *", baseJob{&calls}) // #3
	assert.Equal(t, int32(0), calls.Load())
	cron.Start()
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(2), calls.Load()) // #1 & #2
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(3), calls.Load()) // #2
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(5), calls.Load()) // #2 & #3
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(6), calls.Load()) // #2
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(7), calls.Load()) // #2
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(8), calls.Load()) // #2
}

func TestSetEntriesNext(t *testing.T) {
	var calls atomic.Int32
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 58, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	_, _ = cron.AddJob("*/5 * * * * *", baseJob{&calls})
	assert.Equal(t, int32(0), calls.Load())
	cron.Start()
	assert.Equal(t, int32(0), calls.Load())
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(0), calls.Load())
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
}

func TestWithID(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 58, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	_, err := cron.AddJob("* * * * * *", func() {}, WithID("some_id"))
	assert.NoError(t, err)
	_, err = cron.AddJob("* * * * * *", func() {}, WithID("some_id"))
	assert.ErrorIs(t, err, ErrIDAlreadyUsed)
	_, err = cron.AddJob("* * * * * *", func() {}, WithID(""))
	assert.ErrorContains(t, err, "id cannot be empty")
}

func TestNextIDIsThreadSafe(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(1984, time.April, 4, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	wg := sync.WaitGroup{}
	wg.Add(1000)
	for i := 0; i < 1000; i++ {
		go func() {
			defer wg.Done()
			_, _ = cron.AddJob("* * * * * *", func() {})
		}()
	}
	wg.Wait()
	m := make(map[EntryID]bool)
	for _, e := range cron.entries.Get().heap.entries {
		if _, ok := m[e.ID]; ok {
			t.Fatal()
		}
		m[e.ID] = true
	}
	assert.Equal(t, 1000, len(m))
}

// Without hashmap: `BenchmarkAddJob-12    	   10000	    131823 ns/op`
// With hashmap   : `BenchmarkAddJob-12    	  437586	      2766 ns/op`
func BenchmarkAddJob(b *testing.B) {
	clock := clockwork.NewFakeClockAt(time.Date(1984, time.April, 4, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	for i := 0; i < b.N; i++ {
		_, _ = cron.AddJob("* * * * * *", func() {})
	}
}

func BenchmarkSetLocation(b *testing.B) {
	clock := clockwork.NewFakeClockAt(time.Date(1984, time.April, 4, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	for i := 0; i < 1000; i++ {
		_, _ = cron.Schedule(Every(time.Duration(i)*time.Second), FuncJob(func(context.Context, *Cron, JobRun) error {
			return nil
		}))
	}
	newLoc, _ := time.LoadLocation("Atlantic/Cape_Verde")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cron.SetLocation(newLoc)
	}
}

func BenchmarkUpdateSchedule(b *testing.B) {
	clock := clockwork.NewFakeClockAt(time.Date(1984, time.April, 4, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	var ids []EntryID
	for i := 0; i < 10000; i++ {
		id, _ := cron.Schedule(Every(time.Duration(i)*time.Second), FuncJob(func(context.Context, *Cron, JobRun) error { return nil }))
		ids = append(ids, id)
	}
	cron.Start()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cron.UpdateSchedule(ids[i%1000], Every(time.Duration(i)*time.Second))
	}
}

func TestLabelEntryOption(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	_, _ = cron.AddJob("* * * * * *", func() {}, Label("#1"))
	entries := cron.Entries()
	assert.Equal(t, "#1", entries[0].Label)
}

type jw1 struct{ calls *atomic.Int32 }

func (j jw1) Run() { j.calls.Add(1) }

type jw2 struct{ calls *atomic.Int32 }

func (j jw2) Run(context.Context) { j.calls.Add(1) }

type jw3 struct{ calls *atomic.Int32 }

func (j jw3) Run(EntryID) { j.calls.Add(1) }

type jw4 struct{ calls *atomic.Int32 }

func (j jw4) Run(context.Context, EntryID) { j.calls.Add(1) }

type jw5 struct{ calls *atomic.Int32 }

func (j jw5) Run() error {
	j.calls.Add(1)
	return nil
}

type jw6 struct{ calls *atomic.Int32 }

func (j jw6) Run(context.Context) error {
	j.calls.Add(1)
	return nil
}

type jw7 struct{ calls *atomic.Int32 }

func (j jw7) Run(EntryID) error {
	j.calls.Add(1)
	return nil
}

type jw8 struct{ calls *atomic.Int32 }

func (j jw8) Run(context.Context, EntryID) error {
	j.calls.Add(1)
	return nil
}

type jw9 struct{ calls *atomic.Int32 }

func (j jw9) Run(*Cron) { j.calls.Add(1) }

type jw10 struct{ calls *atomic.Int32 }

func (j jw10) Run(*Cron) error {
	j.calls.Add(1)
	return nil
}

type jw11 struct{ calls *atomic.Int32 }

func (j jw11) Run(context.Context, *Cron) {
	j.calls.Add(1)
}

type jw12 struct{ calls *atomic.Int32 }

func (j jw12) Run(context.Context, *Cron) error {
	j.calls.Add(1)
	return nil
}

type jw13 struct{ calls *atomic.Int32 }

func (j jw13) Run(*Cron, EntryID) {
	j.calls.Add(1)
}

type jw14 struct{ calls *atomic.Int32 }

func (j jw14) Run(*Cron, EntryID) error {
	j.calls.Add(1)
	return nil
}

type jw15 struct{ calls *atomic.Int32 }

func (j jw15) Run(context.Context, *Cron, EntryID) {
	j.calls.Add(1)
}

type jw16 struct{ calls *atomic.Int32 }

func (j jw16) Run(context.Context, *Cron, EntryID) error {
	j.calls.Add(1)
	return nil
}

type jw17 struct{ calls *atomic.Int32 }

func (j jw17) Run(Entry) {
	j.calls.Add(1)
}

type jw18 struct{ calls *atomic.Int32 }

func (j jw18) Run(Entry) error {
	j.calls.Add(1)
	return nil
}

type jw19 struct{ calls *atomic.Int32 }

func (j jw19) Run(context.Context, Entry) {
	j.calls.Add(1)
}

type jw20 struct{ calls *atomic.Int32 }

func (j jw20) Run(context.Context, Entry) error {
	j.calls.Add(1)
	return nil
}

type jw21 struct{ calls *atomic.Int32 }

func (j jw21) Run(*Cron, Entry) {
	j.calls.Add(1)
}

type jw22 struct{ calls *atomic.Int32 }

func (j jw22) Run(*Cron, Entry) error {
	j.calls.Add(1)
	return nil
}

type jw23 struct{ calls *atomic.Int32 }

func (j jw23) Run(context.Context, *Cron, Entry) {
	j.calls.Add(1)
}

type jw24 struct{ calls *atomic.Int32 }

func (j jw24) Run(JobRun) {
	j.calls.Add(1)
}

type jw25 struct{ calls *atomic.Int32 }

func (j jw25) Run(JobRun) error {
	j.calls.Add(1)
	return nil
}

type jw26 struct{ calls *atomic.Int32 }

func (j jw26) Run(context.Context, JobRun) {
	j.calls.Add(1)
}

type jw27 struct{ calls *atomic.Int32 }

func (j jw27) Run(context.Context, JobRun) error {
	j.calls.Add(1)
	return nil
}

type jw28 struct{ calls *atomic.Int32 }

func (j jw28) Run(*Cron, JobRun) {
	j.calls.Add(1)
}

type jw29 struct{ calls *atomic.Int32 }

func (j jw29) Run(*Cron, JobRun) error {
	j.calls.Add(1)
	return nil
}

type jw30 struct{ calls *atomic.Int32 }

func (j jw30) Run(context.Context, *Cron, JobRun) {
	j.calls.Add(1)
}

type jw31 struct{ calls *atomic.Int32 }

func (j jw31) Run(context.Context, *Cron, Entry) error {
	j.calls.Add(1)
	return nil
}

func castEntry[T any](cron *Cron, id EntryID) bool {
	return utils.TryCast[T](utils.First(cron.Entry(id)).Job())
}

func TestWrappers(t *testing.T) {
	var calls atomic.Int32
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	wrapVoid := func(any) error { return nil }
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	fn1ID, _ := cron.AddJob("* * * * * *", func() { calls.Add(1) })
	_, _ = cron.AddJob("* * * * * *", func(context.Context) { calls.Add(1) })
	_, _ = cron.AddJob("* * * * * *", func(*Cron) { calls.Add(1) })
	_, _ = cron.AddJob("* * * * * *", func(Entry) { calls.Add(1) })
	_, _ = cron.AddJob("* * * * * *", func(EntryID) { calls.Add(1) })
	_, _ = cron.AddJob("* * * * * *", func(context.Context, EntryID) { calls.Add(1) })
	_, _ = cron.AddJob("* * * * * *", func(context.Context, Entry) { calls.Add(1) })
	_, _ = cron.AddJob("* * * * * *", func(context.Context, *Cron) { calls.Add(1) })
	_, _ = cron.AddJob("* * * * * *", func(*Cron, EntryID) { calls.Add(1) })
	_, _ = cron.AddJob("* * * * * *", func(*Cron, Entry) { calls.Add(1) })
	_, _ = cron.AddJob("* * * * * *", func(context.Context, *Cron, EntryID) { calls.Add(1) })
	_, _ = cron.AddJob("* * * * * *", func(context.Context, *Cron, Entry) { calls.Add(1) })
	_, _ = cron.AddJob("* * * * * *", func() error { return wrapVoid(calls.Add(1)) })
	_, _ = cron.AddJob("* * * * * *", func(context.Context) error { return wrapVoid(calls.Add(1)) })
	_, _ = cron.AddJob("* * * * * *", func(*Cron) error { return wrapVoid(calls.Add(1)) })
	_, _ = cron.AddJob("* * * * * *", func(Entry) error { return wrapVoid(calls.Add(1)) })
	_, _ = cron.AddJob("* * * * * *", func(EntryID) error { return wrapVoid(calls.Add(1)) })
	_, _ = cron.AddJob("* * * * * *", func(context.Context, EntryID) error { return wrapVoid(calls.Add(1)) })
	_, _ = cron.AddJob("* * * * * *", func(context.Context, Entry) error { return wrapVoid(calls.Add(1)) })
	_, _ = cron.AddJob("* * * * * *", func(context.Context, *Cron) error { return wrapVoid(calls.Add(1)) })
	_, _ = cron.AddJob("* * * * * *", func(*Cron, EntryID) error { return wrapVoid(calls.Add(1)) })
	_, _ = cron.AddJob("* * * * * *", func(*Cron, Entry) error { return wrapVoid(calls.Add(1)) })
	_, _ = cron.AddJob("* * * * * *", func(context.Context, *Cron, EntryID) error { return wrapVoid(calls.Add(1)) })
	_, _ = cron.AddJob("* * * * * *", func(context.Context, *Cron, Entry) error { return wrapVoid(calls.Add(1)) })
	_, _ = cron.AddJob("* * * * * *", func(JobRun) { calls.Add(1) })
	_, _ = cron.AddJob("* * * * * *", func(JobRun) error { return wrapVoid(calls.Add(1)) })
	_, _ = cron.AddJob("* * * * * *", func(context.Context, JobRun) { calls.Add(1) })
	_, _ = cron.AddJob("* * * * * *", func(context.Context, JobRun) error { return wrapVoid(calls.Add(1)) })
	_, _ = cron.AddJob("* * * * * *", func(*Cron, JobRun) { calls.Add(1) })
	_, _ = cron.AddJob("* * * * * *", func(*Cron, JobRun) error { return wrapVoid(calls.Add(1)) })
	_, _ = cron.AddJob("* * * * * *", func(context.Context, *Cron, JobRun) { calls.Add(1) })
	_, _ = cron.AddJob("* * * * * *", func(context.Context, *Cron, JobRun) error { return wrapVoid(calls.Add(1)) })
	jw1ID, _ := cron.AddJob("* * * * * *", jw1{&calls})
	jw2ID, _ := cron.AddJob("* * * * * *", jw2{&calls})
	jw3ID, _ := cron.AddJob("* * * * * *", jw3{&calls})
	jw4ID, _ := cron.AddJob("* * * * * *", jw4{&calls})
	jw5ID, _ := cron.AddJob("* * * * * *", jw5{&calls})
	jw6ID, _ := cron.AddJob("* * * * * *", jw6{&calls})
	jw7ID, _ := cron.AddJob("* * * * * *", jw7{&calls})
	jw8ID, _ := cron.AddJob("* * * * * *", jw8{&calls})
	jw9ID, _ := cron.AddJob("* * * * * *", jw9{&calls})
	jw10ID, _ := cron.AddJob("* * * * * *", jw10{&calls})
	jw11ID, _ := cron.AddJob("* * * * * *", jw11{&calls})
	jw12ID, _ := cron.AddJob("* * * * * *", jw12{&calls})
	jw13ID, _ := cron.AddJob("* * * * * *", jw13{&calls})
	jw14ID, _ := cron.AddJob("* * * * * *", jw14{&calls})
	jw15ID, _ := cron.AddJob("* * * * * *", jw15{&calls})
	jw16ID, _ := cron.AddJob("* * * * * *", jw16{&calls})
	jw17ID, _ := cron.AddJob("* * * * * *", jw17{&calls})
	jw18ID, _ := cron.AddJob("* * * * * *", jw18{&calls})
	jw19ID, _ := cron.AddJob("* * * * * *", jw19{&calls})
	jw20ID, _ := cron.AddJob("* * * * * *", jw20{&calls})
	jw21ID, _ := cron.AddJob("* * * * * *", jw21{&calls})
	jw22ID, _ := cron.AddJob("* * * * * *", jw22{&calls})
	jw23ID, _ := cron.AddJob("* * * * * *", jw23{&calls})
	jw24ID, _ := cron.AddJob("* * * * * *", jw24{&calls})
	jw25ID, _ := cron.AddJob("* * * * * *", jw25{&calls})
	jw26ID, _ := cron.AddJob("* * * * * *", jw26{&calls})
	jw27ID, _ := cron.AddJob("* * * * * *", jw27{&calls})
	jw28ID, _ := cron.AddJob("* * * * * *", jw28{&calls})
	jw29ID, _ := cron.AddJob("* * * * * *", jw29{&calls})
	jw30ID, _ := cron.AddJob("* * * * * *", jw30{&calls})
	jw32ID, _ := cron.AddJob("* * * * * *", jw31{&calls})
	assert.Panics(t, func() { _, _ = cron.AddJob("* * * * * *", 1) }, ErrUnsupportedJobType)
	cron.Start()
	assert.True(t, castEntry[Job](cron, fn1ID))
	assert.True(t, castEntry[jw1](cron, jw1ID))
	assert.True(t, castEntry[jw2](cron, jw2ID))
	assert.True(t, castEntry[jw3](cron, jw3ID))
	assert.True(t, castEntry[jw4](cron, jw4ID))
	assert.True(t, castEntry[jw5](cron, jw5ID))
	assert.True(t, castEntry[jw6](cron, jw6ID))
	assert.True(t, castEntry[jw7](cron, jw7ID))
	assert.True(t, castEntry[jw8](cron, jw8ID))
	assert.True(t, castEntry[jw9](cron, jw9ID))
	assert.True(t, castEntry[jw10](cron, jw10ID))
	assert.True(t, castEntry[jw11](cron, jw11ID))
	assert.True(t, castEntry[jw12](cron, jw12ID))
	assert.True(t, castEntry[jw13](cron, jw13ID))
	assert.True(t, castEntry[jw14](cron, jw14ID))
	assert.True(t, castEntry[jw15](cron, jw15ID))
	assert.True(t, castEntry[jw16](cron, jw16ID))
	assert.True(t, castEntry[jw17](cron, jw17ID))
	assert.True(t, castEntry[jw18](cron, jw18ID))
	assert.True(t, castEntry[jw19](cron, jw19ID))
	assert.True(t, castEntry[jw20](cron, jw20ID))
	assert.True(t, castEntry[jw21](cron, jw21ID))
	assert.True(t, castEntry[jw22](cron, jw22ID))
	assert.True(t, castEntry[jw23](cron, jw23ID))
	assert.True(t, castEntry[jw24](cron, jw24ID))
	assert.True(t, castEntry[jw25](cron, jw25ID))
	assert.True(t, castEntry[jw26](cron, jw26ID))
	assert.True(t, castEntry[jw27](cron, jw27ID))
	assert.True(t, castEntry[jw28](cron, jw28ID))
	assert.True(t, castEntry[jw29](cron, jw29ID))
	assert.True(t, castEntry[jw30](cron, jw30ID))
	assert.True(t, castEntry[jw31](cron, jw32ID))
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(63), calls.Load())
}

func TestEntryOption(t *testing.T) {
	var calls atomic.Int32
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	_, _ = cron.AddJob("1 1 * * * *", baseJob{&calls}, WithNext(clock.Now()))
	_, _ = cron.AddJob("1 1 * * * *", baseJob{&calls}, RunOnStart)
	disabledID, _ := cron.AddJob("1 1 * * * *", baseJob{&calls}, Disabled)
	cron.Start()
	assert.False(t, utils.First(cron.Entry(disabledID)).Active)
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(2), calls.Load())
}

func TestWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cron := New().WithContext(ctx).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	_, _ = cron.AddJob("* * * * * ?", func() {})
	cancel()
	cron.Run()
	assert.True(t, true)
}

func TestWithIDFactory(t *testing.T) {
	factory := FuncIDFactory(func() ID { return "test" })
	cron := New().WithLogger(newErrLogger()).WithIDFactory(factory).Build()
	id, _ := cron.AddJob("* * * * *", func() {})
	assert.Equal(t, EntryID("test"), id)
}

func TestWithJobRunLoggerFactory(t *testing.T) {
	l := slog.New(slog.NewTextHandler(os.Stdout, nil))
	factory := FuncJobRunLoggerFactory(func(w io.Writer) Logger { return l })
	cron := New().WithLogger(newErrLogger()).WithJobRunLoggerFactory(factory).Build()
	_, _ = cron.AddJob("* * * * *", func(jr JobRun) {
		logger := jr.Logger()
		assert.Equal(t, l, logger)
	})
	advanceAndCycle(cron, time.Second)
}

func TestAddEntry(t *testing.T) {
	var calls atomic.Int32
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	id, _ := cron.AddJob("* * * * * ?", baseJob{&calls})
	entry, _ := cron.Entry(id)
	entry.ID = "new-id"
	_, _ = cron.AddEntry(entry)
	cron.Start()
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(2), calls.Load())
}

func TestAddJob1(t *testing.T) {
	var calls atomic.Int32
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	_, _ = cron.AddJobStrict("* * * * * ?", baseJob{&calls})
	cron.Start()
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
}

func TestIsRunning(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 59, 59, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	c1 := make(chan struct{})
	id, _ := cron.AddJob("0 0 * * * *", func() {
		clock.SleepNotify(time.Minute, c1)
	})
	cron.Start()
	assert.False(t, cron.IsRunning(id))
	advanceAndCycleNoWait(cron, time.Second)
	<-c1
	assert.True(t, cron.IsRunning(id))
	advanceAndCycleNoWait(cron, time.Minute)
	cron.waitAllJobsCompleted()
	assert.False(t, cron.IsRunning(id))
}

func TestUpdateScheduleWithSpec(t *testing.T) {
	var calls atomic.Int32
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	id, _ := cron.AddJobStrict("0 0 1 * * *", baseJob{&calls})
	cron.Start()
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(0), calls.Load())
	err := cron.UpdateScheduleWithSpec(id, "* * * * * *")
	assert.NoError(t, err)
	err1 := cron.UpdateScheduleWithSpec(id, "invalid spec")
	assert.Error(t, err1)
	advanceAndCycle(cron, time.Second)
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(2), calls.Load())
}

func TestUpdateSchedule(t *testing.T) {
	var calls atomic.Int32
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	id, _ := cron.AddJobStrict("0 0 1 * * *", baseJob{&calls})
	cron.Start()
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(0), calls.Load())
	err := cron.UpdateSchedule(id, Every(time.Second))
	assert.NoError(t, err)
	err1 := cron.UpdateSchedule("do-not-exist", Every(time.Second))
	assert.ErrorIs(t, err1, ErrEntryNotFound)
	advanceAndCycle(cron, time.Second)
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(2), calls.Load())
}

func TestUpdateLabel(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	id, _ := cron.AddJob("0 0 1 * * *", func() {}, Label("original"))
	cron.Start()
	entry, _ := cron.getEntry(id)
	assert.Equal(t, "original", entry.Label)
	cron.UpdateLabel(id, "updated")
	entry, _ = cron.getEntry(id)
	assert.Equal(t, "updated", entry.Label)
}

func TestDisabledIgnoredByGetNextTime(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	_, _ = cron.AddJob("* * * * * *", func() {}, Disabled)
	cron.Start()
	assert.True(t, cron.GetNextTime().IsZero())
}

func TestWithTimeout(t *testing.T) {
	var calls atomic.Int32
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 59, 59, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	c1 := make(chan struct{})
	c2 := make(chan struct{})
	_, _ = cron.AddJob("0 0 1 * * *", WithTimeout(time.Minute, func(ctx context.Context) {
		afterCh := clock.After(time.Hour)
		close(c1)
		select {
		case <-afterCh:
		case <-ctx.Done():
		}
		calls.Add(1)
		close(c2)
	}))
	cron.Start()
	advanceAndCycleNoWait(cron, time.Second)
	<-c1
	advanceAndCycleNoWait(cron, time.Second)
	advanceAndCycleNoWait(cron, time.Second)
	assert.Equal(t, int32(0), calls.Load())
	advanceAndCycleNoWait(cron, time.Minute)
	<-c2
	assert.Equal(t, int32(1), calls.Load())
}

func TestWithDeadline(t *testing.T) {
	var calls atomic.Int32
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 59, 59, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	c1 := make(chan struct{})
	c2 := make(chan struct{})
	_, _ = cron.AddJob("0 0 1 * * *", WithDeadline(time.Date(2000, 1, 1, 1, 1, 0, 0, time.UTC), func(ctx context.Context) {
		afterCh := clock.After(time.Hour)
		close(c1)
		select {
		case <-afterCh:
		case <-ctx.Done():
		}
		calls.Add(1)
		close(c2)
	}))
	cron.Start()
	advanceAndCycleNoWait(cron, time.Second)
	<-c1
	advanceAndCycleNoWait(cron, time.Second)
	advanceAndCycleNoWait(cron, time.Second)
	assert.Equal(t, int32(0), calls.Load())
	advanceAndCycleNoWait(cron, time.Minute)
	<-c2
	assert.Equal(t, int32(1), calls.Load())
}

func TestEventsString(t *testing.T) {
	assert.Equal(t, "JobStart", JobStart.String())
	assert.Equal(t, "JobCompleted", JobCompleted.String())
	assert.Equal(t, "JobErr", JobErr.String())
	assert.Equal(t, "JobPanic", JobPanic.String())
	assert.Equal(t, "Unknown", JobEventType(-3).String())
}

type EventListener struct {
	nbJobStart     atomic.Int32
	nbJobCompleted atomic.Int32
	nbJobErr       atomic.Int32
	nbJobPanic     atomic.Int32
	jobStartCh     chan struct{}
	jobCompletedCh chan struct{}
	jobErrCh       chan struct{}
	jobPanicCh     chan struct{}
	cond           sync.Cond
}

func NewEventListener(c *Cron, id EntryID) *EventListener {
	l := &EventListener{
		jobStartCh:     make(chan struct{}, 100),
		jobCompletedCh: make(chan struct{}, 100),
		jobErrCh:       make(chan struct{}, 100),
		jobPanicCh:     make(chan struct{}, 100),
		cond:           *sync.NewCond(&sync.Mutex{}),
	}
	c1 := make(chan struct{})
	go func() {
		sub := c.Sub(id)
		close(c1)
		for {
			var payload pubsub.Payload[EntryID, JobEvent]
			select {
			case <-c.ctx.Done():
				return
			case payload = <-sub.ReceiveCh():
			}
			if payload.Msg.Typ == JobStart {
				l.nbJobStart.Add(1)
				utils.NonBlockingSend(l.jobStartCh, struct{}{})
			} else if payload.Msg.Typ == JobCompleted {
				l.nbJobCompleted.Add(1)
				utils.NonBlockingSend(l.jobCompletedCh, struct{}{})
			} else if payload.Msg.Typ == JobErr {
				l.nbJobErr.Add(1)
				utils.NonBlockingSend(l.jobErrCh, struct{}{})
			} else if payload.Msg.Typ == JobPanic {
				l.nbJobPanic.Add(1)
				utils.NonBlockingSend(l.jobPanicCh, struct{}{})
			}
			l.cond.L.Lock()
			l.cond.Signal()
			l.cond.L.Unlock()
		}
	}()
	select {
	case <-c1:
	case <-c.ctx.Done():
	}
	return l
}

func (l *EventListener) NbJobStart() int32               { return l.nbJobStart.Load() }
func (l *EventListener) NbJobCompleted() int32           { return l.nbJobCompleted.Load() }
func (l *EventListener) NbJobErr() int32                 { return l.nbJobErr.Load() }
func (l *EventListener) NbJobPanic() int32               { return l.nbJobPanic.Load() }
func (l *EventListener) JobStartCh() <-chan struct{}     { return l.jobStartCh }
func (l *EventListener) JobCompletedCh() <-chan struct{} { return l.jobCompletedCh }
func (l *EventListener) JobErrCh() <-chan struct{}       { return l.jobErrCh }
func (l *EventListener) JobPanicCh() <-chan struct{}     { return l.jobPanicCh }

func (l *EventListener) Wait(clb func() bool) {
	l.cond.L.Lock()
	for !clb() {
		l.cond.Wait()
	}
	l.cond.L.Unlock()
}

func TestEvents(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 59, 0, time.UTC))
	cron := New().WithClock(clock).WithLogger(newNoOpLogger()).Build()
	c1 := make(chan struct{})
	id, _ := cron.AddJob("* * * * *", SkipIfStillRunning(func() {
		clock.SleepNotify(150*time.Second, c1) // sleep 2m30s
	}))
	cron.Start()

	l := NewEventListener(cron, id)

	advanceAndCycleNoWait(cron, time.Second)    // after 1s, job starts
	<-c1                                        // wait until the job is started before advancing clock
	advanceAndCycleNoWait(cron, time.Minute)    // after 1m another job starts but is skipped
	<-l.JobErrCh()                              // Wait until we receive the failed event before advancing clock
	advanceAndCycleNoWait(cron, time.Minute)    // after 2m another job starts but is skipped
	<-l.JobErrCh()                              // Wait until we receive the failed event before advancing clock
	advanceAndCycleNoWait(cron, 30*time.Second) // after 2m30s job is completed

	// Wait until we receive all events (1 completed, 2 failed)
	l.Wait(func() bool { return l.NbJobErr() == 2 && l.NbJobCompleted() == 3 })
}

func TestCompletedJobs(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).WithKeepCompletedRunsDur(time.Minute).Build()
	_, _ = cron.AddJob("* * * * * *", func() {})
	_, _ = cron.AddJob("* * * * * *", func() {})
	cron.Start()
	c1 := make(chan struct{})
	var count atomic.Int32
	cron.onJobCompleted(func(ctx context.Context, cron *Cron, id HookID, run JobRun) {
		newCount := count.Add(1)
		if newCount == 2 {
			close(c1)
		}
	})
	advanceAndCycle(cron, time.Second)
	<-c1
	jobs := cron.CompletedJobs()
	assert.Equal(t, 2, len(jobs))
}

func TestRunningJobs(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	c1 := make(chan struct{})
	c2 := make(chan struct{})
	_, _ = cron.AddJob("* * * * * *", func() { clock.SleepNotify(time.Minute, c1) })
	_, _ = cron.AddJob("* * * * * *", func() { clock.SleepNotify(time.Minute, c2) })
	cron.Start()
	advanceAndCycleNoWait(cron, time.Second)
	<-c1
	<-c2
	jobs := cron.RunningJobs()
	assert.Equal(t, 2, len(jobs))
}

func TestRunningJobsFor(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).Build()
	var calls atomic.Int32
	c1 := make(chan struct{})
	c2 := make(chan struct{})
	c3 := make(chan struct{})
	id, _ := cron.AddJob("* * * * * *", func() {
		val := calls.Add(1)
		if val == 1 {
			clock.SleepNotify(time.Minute, c1)
		} else if val == 2 {
			clock.SleepNotify(time.Minute, c2)
		} else if val == 3 {
			clock.SleepNotify(time.Minute, c3)
		}
	})
	cron.Start()
	advanceAndCycleNoWait(cron, time.Second)
	advanceAndCycleNoWait(cron, time.Second)
	advanceAndCycleNoWait(cron, time.Second)
	<-c1
	<-c2
	<-c3
	jobs, _ := cron.RunningJobsFor(id)
	assert.Equal(t, 3, len(jobs))
}

func TestCompletedJobsFor(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newErrLogger()).WithKeepCompletedRunsDur(time.Minute).Build()
	id, _ := cron.AddJob("* * * * * *", func() {})
	cron.Start()
	advanceAndCycle(cron, time.Second)
	advanceAndCycle(cron, time.Second)
	advanceAndCycle(cron, time.Second)
	jobs, _ := cron.CompletedJobRunsFor(id)
	assert.Equal(t, 3, len(jobs))
	_, err := cron.CompletedJobRunsFor("do-not-exist")
	assert.ErrorIs(t, err, ErrEntryNotFound)
}

func TestCompletedJobsFor1(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithKeepCompletedRunsDur(0).WithLogger(newErrLogger()).Build()
	id, _ := cron.AddJob("* * * * * *", func() {})
	cron.Start()
	advanceAndCycle(cron, time.Second)
	advanceAndCycle(cron, time.Second)
	advanceAndCycle(cron, time.Second)
	jobs, _ := cron.CompletedJobRunsFor(id)
	assert.Equal(t, 0, len(jobs))
}

func TestCancelRun(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithKeepCompletedRunsDur(0).WithLogger(newErrLogger()).Build()
	c1 := make(chan struct{})
	id, _ := cron.AddJob("* * * * *", func(ctx context.Context) {
		select {
		case <-clock.AfterNotify(time.Minute, c1):
		case <-ctx.Done():
		}
	})
	cron.Start()
	advanceAndCycleNoWait(cron, time.Minute)
	<-c1
	runs, _ := cron.RunningJobsFor(id)
	runID := runs[0].RunID
	_, err := cron.GetJobRun(id, runID)
	assert.NoError(t, err)
	_, err = cron.GetJobRun("do-not-exist", runID)
	assert.ErrorIs(t, err, ErrEntryNotFound)
	_, err = cron.GetJobRun(id, "do-not-exist")
	assert.ErrorIs(t, err, ErrJobRunNotFound)
	_ = cron.CancelJobRun(id, runID)
	advanceAndCycle(cron, time.Second)
	runs1, _ := cron.RunningJobsFor(id)
	assert.Equal(t, 0, len(runs1))
}

func TestJobRunEvents(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithKeepCompletedRunsDur(0).WithLogger(newErrLogger()).Build()
	c0 := make(chan struct{})
	_, _ = cron.AddJob("* * * * *", func() { clock.SleepNotify(time.Second, c0) })
	cron.Start()
	createdCh := cron.JobRunCreatedCh()
	completedCh := cron.JobRunCompletedCh()
	c1 := make(chan struct{})
	c2 := make(chan struct{})
	c3 := make(chan struct{})
	go func() {
		close(c1)
		<-createdCh
		close(c2)
		<-completedCh
		close(c3)
	}()
	<-c1
	advanceAndCycleNoWait(cron, time.Minute)
	<-c2
	<-c0
	advanceAndCycleNoWait(cron, time.Second)
	<-c3
}

func TestGetCleanupTS(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithKeepCompletedRunsDur(time.Second).WithLogger(newErrLogger()).Build()
	_, _ = cron.AddJob("* * * * *", func() {})
	cron.Start()
	assert.True(t, cron.GetCleanupTS().IsZero())
}

func TestOnEvt(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newNoOpLogger()).Build()
	var calls atomic.Int32
	hookID := cron.OnEvt(JobStart, func(ctx context.Context, c *Cron, id HookID, jr JobRun) {
		calls.Add(1)
	}, HookSync)
	cron.Start()
	_, _ = cron.AddJob("* * * * * *", func() {})
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
	cron.RemoveHook(hookID)
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
}

func TestOnEntryEvt(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newNoOpLogger()).Build()
	var calls atomic.Int32
	id, _ := cron.AddJob("* * * * * *", func() {})
	hookID := cron.OnEntryEvt(id, JobStart, func(ctx context.Context, c *Cron, id HookID, jr JobRun) {
		calls.Add(1)
	}, HookSync)
	cron.Start()
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
	cron.RemoveHook(hookID)
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
}

func TestRemoveHook(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newNoOpLogger()).Build()
	var calls atomic.Int32
	hookID := cron.OnJobStart(func(ctx context.Context, c *Cron, id HookID, jr JobRun) {
		calls.Add(1)
	}, HookSync)
	cron.Start()
	_, _ = cron.AddJob("* * * * * *", func() {})
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
	cron.RemoveHook(hookID)
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
}

func TestOnJobStart(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newNoOpLogger()).Build()
	var calls atomic.Int32
	hookID := cron.OnJobStart(func(ctx context.Context, c *Cron, id HookID, jr JobRun) {
		calls.Add(1)
	}, HookSync)
	cron.Start()
	_, _ = cron.AddJob("* * * * * *", func() {})
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
	cron.RemoveHook(hookID)
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
}

func TestOnEntryJobStart(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newNoOpLogger()).Build()
	var calls atomic.Int32
	id, _ := cron.AddJob("* * * * * *", func() {})
	hookID := cron.OnEntryJobStart(id, func(ctx context.Context, c *Cron, id HookID, jr JobRun) {
		calls.Add(1)
	}, HookSync)
	cron.Start()
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
	cron.RemoveHook(hookID)
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
}

func TestOnJobCompleted(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newNoOpLogger()).Build()
	var calls atomic.Int32
	hookID := cron.OnJobCompleted(func(ctx context.Context, c *Cron, id HookID, jr JobRun) {
		calls.Add(1)
	}, HookSync)
	cron.Start()
	_, _ = cron.AddJob("* * * * * *", func() {})
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
	cron.RemoveHook(hookID)
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
}

func TestOnEntryJobCompleted(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newNoOpLogger()).Build()
	var calls atomic.Int32
	id, _ := cron.AddJob("* * * * * *", func() {})
	hookID := cron.OnEntryJobCompleted(id, func(ctx context.Context, c *Cron, id HookID, jr JobRun) {
		calls.Add(1)
	}, HookSync)
	cron.Start()
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
	cron.RemoveHook(hookID)
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
}

func TestOnEntryJobStartAsync(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newNoOpLogger()).Build()
	var calls atomic.Int32
	id, _ := cron.AddJob("* * * * * *", func() {})
	c1 := make(chan struct{})
	hookID := cron.OnEntryJobStart(id, func(ctx context.Context, c *Cron, id HookID, jr JobRun) {
		calls.Add(1)
		close(c1)
	})
	cron.Start()
	advanceAndCycle(cron, time.Second)
	<-c1
	assert.Equal(t, int32(1), calls.Load())
	cron.RemoveHook(hookID)
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
}

func TestEnableHook(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newNoOpLogger()).Build()
	var calls atomic.Int32
	hookID := cron.OnJobStart(func(ctx context.Context, c *Cron, id HookID, jr JobRun) {
		calls.Add(1)
	}, HookSync, HookDisable)
	cron.Start()
	_, _ = cron.AddJob("* * * * * *", func() {})
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(0), calls.Load()) // Hook is disabled, should not be called
	cron.EnableHook(hookID)
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load()) // Hook is enabled, should be called
}

func TestDisableHook(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithParser(secondParser).WithLogger(newNoOpLogger()).Build()
	var calls atomic.Int32
	hookID := cron.OnJobStart(func(ctx context.Context, c *Cron, id HookID, jr JobRun) {
		calls.Add(1)
	}, HookSync)
	cron.Start()
	_, _ = cron.AddJob("* * * * * *", func() {})
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load()) // Hook is enabled, should be called
	cron.DisableHook(hookID)
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load()) // Hook is disabled, should not be called
}

func TestGetHooks(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithLogger(newNoOpLogger()).Build()
	eid, _ := cron.AddJob("* * * * * *", func() {})
	hookID1 := cron.OnJobStart(func(ctx context.Context, c *Cron, id HookID, jr JobRun) {}, HookSync)
	hookID2 := cron.OnJobCompleted(func(ctx context.Context, c *Cron, id HookID, jr JobRun) {}, HookSync)
	hookID3 := cron.OnEntryJobCompleted(eid, func(ctx context.Context, c *Cron, id HookID, jr JobRun) {}, HookSync)
	hooks := cron.GetHooks()
	assert.Equal(t, 3, len(hooks))
	assert.True(t, hooks[0].ID == hookID1 || hooks[0].ID == hookID2 || hooks[0].ID == hookID3)
	assert.True(t, hooks[1].ID == hookID1 || hooks[1].ID == hookID2 || hooks[1].ID == hookID3)
	assert.True(t, hooks[2].ID == hookID1 || hooks[2].ID == hookID2 || hooks[2].ID == hookID3)
}

func TestGetHook(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithLogger(newNoOpLogger()).Build()
	hookID := cron.OnJobStart(func(ctx context.Context, c *Cron, id HookID, jr JobRun) {}, HookSync)
	hook, err := cron.GetHook(hookID)
	assert.NoError(t, err)
	assert.Equal(t, hookID, hook.ID)
	_, err = cron.GetHook("nonexistent-hook")
	assert.ErrorIs(t, err, ErrHookNotFound)
}

func TestSetHookLabel(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithLogger(newNoOpLogger()).Build()
	hookID := cron.OnJobStart(func(ctx context.Context, c *Cron, id HookID, jr JobRun) {}, HookSync, HookLabel("label"))
	hook, err := cron.GetHook(hookID)
	assert.NoError(t, err)
	assert.Equal(t, "label", hook.Label)
	cron.SetHookLabel(hookID, "test-label")
	hook, err = cron.GetHook(hookID)
	assert.NoError(t, err)
	assert.Equal(t, "test-label", hook.Label)
}

func TestHooksContainerIterHooksEarlyReturns(t *testing.T) {
	hc := hooksContainer{
		hooksMap: make(map[HookID]hookMeta),
		globalHooksMap: map[JobEventType][]*hookStruct{
			JobStart: {
				&hookStruct{id: "hook1"},
				&hookStruct{id: "hook2"},
			},
		},
		entryHooksMap: map[EntryID]map[JobEventType][]*hookStruct{
			"entry1": {
				JobStart: {
					&hookStruct{id: "hook3"},
					&hookStruct{id: "hook4"},
				},
			},
		},
	}

	count := 0
	for _ = range hc.iterHooks() {
		count++
		if count == 2 {
			break
		}
	}

	if count != 2 {
		t.Errorf("Expected iteration to stop after 2 hooks, got %d", count)
	}

	count = 0
	for _ = range hc.iterHooks() {
		count++
		if count == 3 {
			break
		}
	}

	if count != 3 {
		t.Errorf("Expected iteration to stop after 3 hooks, got %d", count)
	}
}

func TestCleanupNow(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithLogger(newNoOpLogger()).Build()
	cron.Start()
	cron.CleanupNow()
}

func TestSetCleanupInterval(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New().WithClock(clock).WithKeepCompletedRunsDur(0).WithLogger(newNoOpLogger()).Build()
	cron.Start()
	cron.SetCleanupInterval(1)
	cron.SetCleanupInterval(0)
}

func TestWithLocation(t *testing.T) {
	c1 := New().Build()
	assert.Equal(t, time.Local, c1.Location())
	c2 := New().WithLocation(time.UTC).Build()
	assert.Equal(t, time.UTC, c2.Location())
}
