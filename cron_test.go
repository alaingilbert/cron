package cron

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/alaingilbert/cron/internal/utils"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alaingilbert/clockwork"
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
	if fc, ok := cron.clock.(clockwork.FakeClock); ok {
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
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock), WithParser(secondParser))
	cron.Start()
	defer cron.Stop()
	ch := make(chan string, 1)
	_, _ = cron.AddJob("* * * * * *", func(context.Context) {
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

func TestJobPanicRecovery(t *testing.T) {
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock), WithLogger(log.New(io.Discard, "", log.LstdFlags)), WithParser(secondParser))
	cron.Start()
	_, _ = cron.AddJob("* * * * * ?", PanicJob{})
	advanceAndCycle(cron, time.Second)
}

func TestLogError(t *testing.T) {
	clock := clockwork.NewFakeClock()
	buf := bytes.NewBuffer(nil)
	cron := New(WithClock(clock), WithLogger(log.New(buf, "", log.LstdFlags)), WithParser(secondParser))
	_, _ = cron.AddJob("* * * * * ?", func() error { return errors.New("some error") })
	cron.Start()
	advanceAndCycle(cron, time.Second)
	assert.Contains(t, buf.String(), "some error")
}

func TestSetEntryActive(t *testing.T) {
	var calls atomic.Int32
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock), WithParser(secondParser))
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
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock), WithParser(secondParser))
	id, _ := cron.AddJob("1 1 * * * *", baseJob{&calls})
	cron.Start()
	cron.RunNow(id)
	cron.RunNow("not-exist")
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
}

func TestGetNextTime(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New(WithClock(clock), WithParser(secondParser))
	_, _ = cron.AddJob("1 1 * * * *", func() {})
	cron.Start()
	assert.Equal(t, clock.Now().Add(61*time.Second), cron.GetNextTime())
}

func TestOnceJob(t *testing.T) {
	var calls atomic.Int32
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock), WithParser(secondParser))
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
	cron := New(WithClock(clock), WithParser(secondParser))
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
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock), WithParser(secondParser))
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
	cron := New(WithClock(clock), WithParser(secondParser))
	c1 := make(chan struct{})
	c2 := make(chan struct{})
	_, _ = cron.AddJob("1 0 1 * * *", func(context.Context) {
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

// Start, stop, then add an entry. Verify entry doesn't run.
func TestStopCausesJobsToNotRun(t *testing.T) {
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock), WithParser(secondParser))
	cron.Start()
	cron.Stop()
	var calls atomic.Int32
	_, _ = cron.AddJob("* * * * * ?", baseJob{&calls})
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(0), calls.Load())
}

// Add a job, start cron, expect it runs.
func TestAddBeforeRunning(t *testing.T) {
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock), WithParser(secondParser))
	var calls atomic.Int32
	_, _ = cron.AddJob("* * * * * ?", baseJob{&calls})
	cron.Start()
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
}

// Start cron, add a job, expect it runs.
func TestAddWhileRunning(t *testing.T) {
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock), WithParser(secondParser))
	cron.Start()
	var calls atomic.Int32
	_, _ = cron.AddJob("* * * * * ?", baseJob{&calls})
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
}

// Test for #34. Adding a job after calling start results in multiple job invocations
func TestAddWhileRunningWithDelay(t *testing.T) {
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock), WithParser(secondParser))
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
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock), WithParser(secondParser))
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
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock), WithParser(secondParser))
	id, _ := cron.AddJob("@every 2s", func(context.Context) {})
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
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock), WithParser(secondParser))
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
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock), WithParser(secondParser))
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
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock), WithParser(secondParser))
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
	cron := New(WithClock(clock), WithParser(secondParser))
	var calls atomic.Int32
	_, _ = cron.AddJob(spec, baseJob{&calls})
	cron.Start()
	advanceAndCycle(cron, time.Second)
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(2), calls.Load())
}

// Test that the cron is run in the given time zone (as opposed to local).
func TestNonLocalTimezone(t *testing.T) {
	clock := clockwork.NewFakeClock()
	loc, err := time.LoadLocation("Atlantic/Cape_Verde")
	assert.NoError(t, err, "Failed to load time zone Atlantic/Cape_Verde")
	now := clock.Now().In(loc)
	spec := fmt.Sprintf("%d,%d %d %d %d %d ?",
		now.Second()+1, now.Second()+2, now.Minute(), now.Hour(), now.Day(), now.Month())
	cron := New(WithClock(clock), WithLocation(loc), WithParser(secondParser))
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
	cron := New(WithClock(clock), WithParser(secondParser))
	cron.Stop()
}

// Simple job that increment an atomic counter every time the job is run
type baseJob struct{ calls *atomic.Int32 }

func (j baseJob) Run(context.Context, EntryID) error {
	j.calls.Add(1)
	return nil
}

type namedJob struct {
	calls *atomic.Int32
	name  string
}

func (t namedJob) Run() {
	t.calls.Add(1)
}

// Test that adding an invalid job spec returns an error
func TestInvalidJobSpec(t *testing.T) {
	clock := clockwork.NewRealClock()
	cron := New(WithClock(clock), WithParser(secondParser))
	_, err := cron.AddJob("this will not parse", func() {})
	assert.Error(t, err, "expected an error with invalid spec, got nil")
}

// Test blocking run method behaves as Start()
func TestBlockingRun(t *testing.T) {
	var calls atomic.Int32
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock), WithParser(secondParser))
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
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock), WithParser(secondParser))
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
	cron := New(WithClock(clock), WithParser(secondParser))
	started := cron.Start()
	assert.True(t, started)
	started = cron.Start()
	assert.False(t, started)
}

// TestChangeLocationWhileRunning ...
func TestChangeLocationWhileRunning(t *testing.T) {
	newLoc, _ := time.LoadLocation("Atlantic/Cape_Verde")
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock), WithLocation(time.UTC), WithParser(secondParser))
	cron.Start()
	_, _ = cron.AddJob("* * * * * ?", func(context.Context) {})
	_, _ = cron.AddJob("0 0 1 * * ?", func(context.Context) {})
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
	cron := New(WithClock(clock), WithLocation(time.UTC), WithParser(secondParser))
	cron.Start()
	_, _ = cron.AddJob("* * * * * ?", func(context.Context) {})
	_, _ = cron.AddJob("0 0 1 * * ?", func(context.Context) {})
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
	cron := New(WithClock(clock), WithLocation(time.UTC), WithParser(secondParser))
	cron.Start()
	id1, _ := cron.AddJob("0 0 1 * * *", func(context.Context) {})
	id2, _ := cron.AddJob("0 0 3 * * *", func(context.Context) {})
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
	cron := New(WithClock(clock), WithParser(secondParser))
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
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock), WithParser(secondParser))
	var calls atomic.Int32
	_, _ = cron.AddJob("* * * * * *", baseJob{&calls})
	_, _ = cron.Schedule(new(ZeroSchedule), func() { t.Error("expected zero task will not run") })
	cron.Start()
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(1), calls.Load())
}

func TestRemove(t *testing.T) {
	var calls atomic.Int32
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock), WithParser(secondParser))
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
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock), WithParser(secondParser))
	hourJob, _ := cron.Schedule(Every(time.Hour), func() {})
	_, _ = cron.Schedule(Every(time.Second), func() {
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
	})
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
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock), WithParser(secondParser))
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
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock), WithParser(secondParser))
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
	cron := New(WithClock(clock), WithParser(secondParser))
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
	cron := New(WithClock(clock), WithParser(secondParser))
	_, err := cron.AddJob("* * * * * *", func() {}, WithID("some_id"))
	assert.NoError(t, err)
	_, err = cron.AddJob("* * * * * *", func() {}, WithID("some_id"))
	assert.ErrorIs(t, err, ErrIDAlreadyUsed)
}

func TestNextIDIsThreadSafe(t *testing.T) {
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock), WithParser(secondParser))
	wg := sync.WaitGroup{}
	wg.Add(1000)
	for i := 0; i < 1000; i++ {
		go func() {
			defer wg.Done()
			_, _ = cron.AddJob("* * * * * *", func(context.Context) {})
		}()
	}
	wg.Wait()
	m := make(map[EntryID]bool)
	for _, e := range cron.entries.Get() {
		if _, ok := m[e.ID]; ok {
			t.Fatal()
		}
		m[e.ID] = true
	}
	assert.Equal(t, 1000, len(m))
}

func TestLabelEntryOption(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New(WithClock(clock), WithParser(secondParser))
	_, _ = cron.AddJob("* * * * * *", func(_ context.Context) {}, Label("#1"))
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

func TestWrappers(t *testing.T) {
	var calls atomic.Int32
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	wrapVoid := func(any) error { return nil }
	cron := New(WithClock(clock), WithParser(secondParser))
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
	assert.Panics(t, func() { _, _ = cron.AddJob("* * * * * *", 1) }, ErrUnsupportedJobType)
	cron.Start()
	_, okFn1 := utils.First(cron.Entry(fn1ID)).Job().(Job)
	_, ok1 := utils.First(cron.Entry(jw1ID)).Job().(jw1)
	_, ok2 := utils.First(cron.Entry(jw2ID)).Job().(jw2)
	_, ok3 := utils.First(cron.Entry(jw3ID)).Job().(jw3)
	_, ok4 := utils.First(cron.Entry(jw4ID)).Job().(jw4)
	_, ok5 := utils.First(cron.Entry(jw5ID)).Job().(jw5)
	_, ok6 := utils.First(cron.Entry(jw6ID)).Job().(jw6)
	_, ok7 := utils.First(cron.Entry(jw7ID)).Job().(jw7)
	_, ok8 := utils.First(cron.Entry(jw8ID)).Job().(jw8)
	_, ok9 := utils.First(cron.Entry(jw9ID)).Job().(jw9)
	_, ok10 := utils.First(cron.Entry(jw10ID)).Job().(jw10)
	_, ok11 := utils.First(cron.Entry(jw11ID)).Job().(jw11)
	_, ok12 := utils.First(cron.Entry(jw12ID)).Job().(jw12)
	_, ok13 := utils.First(cron.Entry(jw13ID)).Job().(jw13)
	_, ok14 := utils.First(cron.Entry(jw14ID)).Job().(jw14)
	_, ok15 := utils.First(cron.Entry(jw15ID)).Job().(jw15)
	_, ok16 := utils.First(cron.Entry(jw16ID)).Job().(jw16)
	_, ok17 := utils.First(cron.Entry(jw17ID)).Job().(jw17)
	_, ok18 := utils.First(cron.Entry(jw18ID)).Job().(jw18)
	_, ok19 := utils.First(cron.Entry(jw19ID)).Job().(jw19)
	_, ok20 := utils.First(cron.Entry(jw20ID)).Job().(jw20)
	_, ok21 := utils.First(cron.Entry(jw21ID)).Job().(jw21)
	_, ok22 := utils.First(cron.Entry(jw22ID)).Job().(jw22)
	_, ok23 := utils.First(cron.Entry(jw23ID)).Job().(jw23)
	assert.True(t, okFn1)
	assert.True(t, ok1)
	assert.True(t, ok2)
	assert.True(t, ok3)
	assert.True(t, ok4)
	assert.True(t, ok5)
	assert.True(t, ok6)
	assert.True(t, ok7)
	assert.True(t, ok8)
	assert.True(t, ok9)
	assert.True(t, ok10)
	assert.True(t, ok11)
	assert.True(t, ok12)
	assert.True(t, ok13)
	assert.True(t, ok14)
	assert.True(t, ok15)
	assert.True(t, ok16)
	assert.True(t, ok17)
	assert.True(t, ok18)
	assert.True(t, ok19)
	assert.True(t, ok20)
	assert.True(t, ok21)
	assert.True(t, ok22)
	assert.True(t, ok23)
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(47), calls.Load())
}

func TestEntryOption(t *testing.T) {
	var calls atomic.Int32
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New(WithClock(clock), WithParser(secondParser))
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
	cron := New(WithContext(ctx), WithParser(secondParser))
	_, _ = cron.AddJob("* * * * * ?", func() {})
	cancel()
	cron.Run()
	assert.True(t, true)
}

func TestAddEntry(t *testing.T) {
	var calls atomic.Int32
	clock := clockwork.NewFakeClockAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	cron := New(WithClock(clock), WithParser(secondParser))
	id, _ := cron.AddJob("* * * * * ?", baseJob{&calls})
	entry, _ := cron.Entry(id)
	entry.ID = "new-id"
	_, _ = cron.AddEntry(entry)
	cron.Start()
	advanceAndCycle(cron, time.Second)
	assert.Equal(t, int32(2), calls.Load())
}
