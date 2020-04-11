package cron

import (
	"fmt"
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
	time.Sleep(5 * time.Millisecond)
}

func wait(wg *sync.WaitGroup) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		wg.Wait()
		close(ch)
	}()
	return ch
}

func recvWithTimeout(t *testing.T, ch <-chan struct{}, msg ...string) {
	select {
	case <-time.After(time.Second):
		t.Fatal(msg)
	case <-ch:
	}
}

type DummyJob struct{}

func (d DummyJob) Run() {
	panic("YOLO")
}

func TestFuncPanicRecovery(t *testing.T) {
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock))
	cron.Start()
	defer cron.Stop()
	var nbCall int32
	_, _ = cron.AddFunc("* * * * *", func() { atomic.AddInt32(&nbCall, 1) })
	cycle(cron)
	clock.Advance(61 * time.Second)
	cycle(cron)
	assert.Equal(t, int32(1), atomic.LoadInt32(&nbCall))
}

func TestFuncPanicRecovery1(t *testing.T) {
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock))
	cron.Start()
	defer cron.Stop()
	var nbCall int32
	_, _ = cron.AddFunc("* * * * * *", func() { atomic.AddInt32(&nbCall, 1) })
	cycle(cron)
	clock.Advance(10 * time.Second)
	cycle(cron)
	assert.Equal(t, int32(1), atomic.LoadInt32(&nbCall))
}

func TestJobPanicRecovery(t *testing.T) {
	var job DummyJob
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock))
	cron.Start()
	defer cron.Stop()
	_, _ = cron.AddJob("* * * * * ?", job)
	cycle(cron)
	clock.Advance(time.Second)
	cycle(cron)
}

// Start and stop cron with no entries.
func TestNoEntries(t *testing.T) {
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock))
	cron.Start()
	recvWithTimeout(t, cron.Stop().Done(), "expected cron will be stopped immediately")
}

// Start, stop, then add an entry. Verify entry doesn't run.
func TestStopCausesJobsToNotRun(t *testing.T) {
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock))
	cron.Start()
	cron.Stop()
	var call int32
	_, _ = cron.AddFunc("* * * * * ?", func() { atomic.AddInt32(&call, 1) })
	cycle(cron)
	clock.Advance(time.Second)
	cycle(cron)
	assert.Equal(t, int32(0), atomic.LoadInt32(&call))
}

// Add a job, start cron, expect it runs.
func TestAddBeforeRunning(t *testing.T) {
	wg := &sync.WaitGroup{}
	wg.Add(1)

	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock))
	_, _ = cron.AddFunc("* * * * * ?", func() { wg.Done() })
	cron.Start()
	defer cron.Stop()
	cycle(cron)
	clock.Advance(time.Second)
	cycle(cron)
	recvWithTimeout(t, wait(wg), "expected job runs")
}

// Start cron, add a job, expect it runs.
func TestAddWhileRunning(t *testing.T) {
	wg := &sync.WaitGroup{}
	wg.Add(1)

	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock))
	cron.Start()
	defer cron.Stop()
	_, _ = cron.AddFunc("* * * * * ?", func() { wg.Done() })
	cycle(cron)
	clock.Advance(time.Second)
	cycle(cron)
	recvWithTimeout(t, wait(wg), "expected job runs")
}

// Test for #34. Adding a job after calling start results in multiple job invocations
func TestAddWhileRunningWithDelay(t *testing.T) {
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock))
	cron.Start()
	defer cron.Stop()
	clock.Advance(time.Second)
	cycle(cron)
	clock.Advance(time.Second)
	cycle(cron)
	clock.Advance(time.Second)
	cycle(cron)
	clock.Advance(time.Second)
	cycle(cron)
	clock.Advance(time.Second)
	cycle(cron)
	var calls int32
	_, _ = cron.AddFunc("* * * * * *", func() { atomic.AddInt32(&calls, 1) })
	clock.Advance(time.Second)
	cycle(cron)
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls))
}

// Test timing with Entries.
func TestSnapshotEntries(t *testing.T) {
	wg := &sync.WaitGroup{}
	wg.Add(1)

	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock))
	_, _ = cron.AddFunc("@every 2s", func() { wg.Done() })
	cron.Start()
	defer cron.Stop()
	clock.Advance(time.Second)
	cycle(cron)
	clock.Advance(time.Second)
	cycle(cron)
	// Cron should fire in 2 seconds. After 1 second, call Entries.
	cron.Entries()
	clock.Advance(time.Second)
	cycle(cron)
	// Even though Entries was called, the cron should fire at the 2 second mark.
	recvWithTimeout(t, wait(wg))
}

func TestEntry(t *testing.T) {
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock))
	id, _ := cron.AddFunc("@every 2s", func() {})
	cron.Start()
	defer cron.Stop()
	entry := cron.Entry(id)
	assert.Equal(t, id, entry.ID)
}

func TestUnexistingEntry(t *testing.T) {
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock))
	_, _ = cron.AddFunc("@every 2s", func() {})
	cron.Start()
	defer cron.Stop()
	entry := cron.Entry(EntryID(123))
	assert.Equal(t, EntryID(0), entry.ID)
}

// Test that the entries are correctly sorted.
// Add a bunch of long-in-the-future entries, and an immediate entry, and ensure
// that the immediate entry runs immediately.
// Also: Test that multiple jobs run in the same instant.
func TestMultipleEntries(t *testing.T) {
	wg := &sync.WaitGroup{}
	wg.Add(2)

	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock))
	_, _ = cron.AddFunc("0 0 0 1 1 ?", func() {})
	_, _ = cron.AddFunc("* * * * * ?", func() { wg.Done() })
	_, _ = cron.AddFunc("0 0 0 31 12 ?", func() {})
	_, _ = cron.AddFunc("* * * * * ?", func() { wg.Done() })

	cron.Start()
	defer cron.Stop()
	cycle(cron)
	clock.Advance(time.Second)
	cycle(cron)
	recvWithTimeout(t, wait(wg))
}

// Test running the same job twice.
func TestRunningJobTwice(t *testing.T) {
	wg := &sync.WaitGroup{}
	wg.Add(2)

	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock))
	_, _ = cron.AddFunc("0 0 0 1 1 ?", func() {})
	_, _ = cron.AddFunc("0 0 0 31 12 ?", func() {})
	_, _ = cron.AddFunc("* * * * * ?", func() { wg.Done() })

	cron.Start()
	defer cron.Stop()
	cycle(cron)
	clock.Advance(time.Second)
	cycle(cron)
	clock.Advance(time.Second)
	cycle(cron)

	recvWithTimeout(t, wait(wg), "expected job fires 2 times")
}

func TestRunningMultipleSchedules(t *testing.T) {
	wg := &sync.WaitGroup{}
	wg.Add(2)

	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock))
	_, _ = cron.AddFunc("0 0 0 1 1 ?", func() {})
	_, _ = cron.AddFunc("0 0 0 31 12 ?", func() {})
	_, _ = cron.AddFunc("* * * * * ?", func() { wg.Done() })
	cron.Schedule(Every(time.Minute), FuncJob(func() {}))
	cron.Schedule(Every(time.Second), FuncJob(func() { wg.Done() }))
	cron.Schedule(Every(time.Hour), FuncJob(func() {}))

	cron.Start()
	defer cron.Stop()
	cycle(cron)
	clock.Advance(time.Second)
	cycle(cron)
	recvWithTimeout(t, wait(wg), "expected job fires 2 times")
}

// Test that the cron is run in the local time zone (as opposed to UTC).
func TestLocalTimezone(t *testing.T) {
	wg := &sync.WaitGroup{}
	wg.Add(2)

	clock := clockwork.NewFakeClock()
	now := clock.Now().Local()
	spec := fmt.Sprintf("%d,%d %d %d %d %d ?",
		now.Second()+1, now.Second()+2, now.Minute(), now.Hour(), now.Day(), now.Month())

	cron := New(WithClock(clock))
	_, _ = cron.AddFunc(spec, func() { wg.Done() })
	cron.Start()
	defer cron.Stop()
	cycle(cron)
	clock.Advance(time.Second)
	cycle(cron)
	clock.Advance(time.Second)
	cycle(cron)

	recvWithTimeout(t, wait(wg), "expected job fires 2 times")
}

// Test that the cron is run in the given time zone (as opposed to local).
func TestNonLocalTimezone(t *testing.T) {
	wg := &sync.WaitGroup{}
	wg.Add(2)

	clock := clockwork.NewFakeClock()
	loc, err := time.LoadLocation("Atlantic/Cape_Verde")
	if err != nil {
		fmt.Printf("Failed to load time zone Atlantic/Cape_Verde: %+v", err)
		t.Fail()
	}

	now := clock.Now().In(loc)
	spec := fmt.Sprintf("%d,%d %d %d %d %d ?",
		now.Second()+1, now.Second()+2, now.Minute(), now.Hour(), now.Day(), now.Month())

	cron := New(WithClock(clock), WithLocation(loc))
	_, _ = cron.AddFunc(spec, func() { wg.Done() })
	cron.Start()
	defer cron.Stop()
	cycle(cron)
	clock.Advance(time.Second)
	cycle(cron)
	clock.Advance(time.Second)
	cycle(cron)

	recvWithTimeout(t, wait(wg), "expected job fires 2 times")
}

// Test that calling stop before start silently returns without
// blocking the stop channel.
func TestStopWithoutStart(t *testing.T) {
	clock := clockwork.NewRealClock()
	cron := New(WithClock(clock))
	cron.Stop()
}

type testJob struct {
	wg   *sync.WaitGroup
	name string
}

func (t testJob) Run() {
	t.wg.Done()
}

// Test that adding an invalid job spec returns an error
func TestInvalidJobSpec(t *testing.T) {
	clock := clockwork.NewRealClock()
	cron := New(WithClock(clock))
	_, err := cron.AddJob("this will not parse", nil)
	if err == nil {
		t.Errorf("expected an error with invalid spec, got nil")
	}
}

// Test blocking run method behaves as Start()
func TestBlockingRun(t *testing.T) {
	wg := &sync.WaitGroup{}
	wg.Add(1)

	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock))
	_, _ = cron.AddFunc("* * * * * ?", func() { wg.Done() })

	var unblockChan = make(chan struct{})

	go func() {
		cron.Run()
		close(unblockChan)
	}()
	defer cron.Stop()
	cycle(cron)
	clock.Advance(time.Second)
	cycle(cron)
	select {
	case <-time.After(time.Second):
		t.Error("expected job fires")
	case <-unblockChan:
		t.Error("expected that Run() blocks")
	case <-wait(wg):
	}
}

// Test that double-running Run is a no-op
func TestBlockingRunNoop(t *testing.T) {
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock))
	go cron.Run()
	defer cron.Stop()
	time.Sleep(time.Millisecond)
	assert.False(t, cron.Run())
}

// Test that double-running is a no-op
func TestStartNoop(t *testing.T) {
	clock := clockwork.NewRealClock()
	cron := New(WithClock(clock))
	started := cron.Start()
	defer cron.Stop()
	assert.True(t, started)
	started = cron.Start()
	assert.False(t, started)
}

// TestChangeLocationWhileRunning ...
func TestChangeLocationWhileRunning(t *testing.T) {
	newLoc, _ := time.LoadLocation("Atlantic/Cape_Verde")
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock), WithLocation(time.UTC))
	cron.Start()
	defer cron.Stop()
	_, _ = cron.AddFunc("* * * * * ?", func() {})
	_, _ = cron.AddFunc("0 0 1 * * ?", func() {})
	assert.Equal(t, clock.Now().Add(time.Second).In(time.UTC), cron.entries[0].Next)
	assert.Equal(t, time.Date(1984, time.April, 4, 1, 0, 0, 0, time.UTC), cron.entries[1].Next)
	cron.SetLocation(newLoc)
	assert.Equal(t, clock.Now().Add(time.Second).In(newLoc), cron.entries[0].Next)
	assert.Equal(t, time.Date(1984, time.April, 4, 2, 0, 0, 0, time.UTC).In(newLoc), cron.entries[1].Next)
	assert.Equal(t, time.Date(1984, time.April, 4, 1, 0, 0, 0, newLoc), cron.entries[1].Next)
}

// Simple test using Runnables.
func TestJob(t *testing.T) {
	wg := &sync.WaitGroup{}
	wg.Add(1)

	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock))
	_, _ = cron.AddJob("0 0 0 30 Feb ?", testJob{wg, "job0"})
	_, _ = cron.AddJob("0 0 0 1 1 ?", testJob{wg, "job1"})
	_, _ = cron.AddJob("* * * * * ?", testJob{wg, "job2"})
	_, _ = cron.AddJob("1 0 0 1 1 ?", testJob{wg, "job3"})
	cron.Schedule(Every(5*time.Second+5*time.Nanosecond), testJob{wg, "job4"})
	cron.Schedule(Every(5*time.Minute), testJob{wg, "job5"})

	cron.Start()
	defer cron.Stop()
	cycle(cron)
	clock.Advance(time.Second)
	cycle(cron)
	recvWithTimeout(t, wait(wg))

	// Ensure the entries are in the right order.
	expecteds := []string{"job2", "job4", "job5", "job1", "job3", "job0"}

	actuals := make([]string, 0)
	for _, entry := range cron.Entries() {
		actuals = append(actuals, entry.Job.(testJob).name)
	}

	for i, expected := range expecteds {
		if actuals[i] != expected {
			t.Fatalf("Jobs not in the right order.  (expected) %s != %s (actual)", expecteds, actuals)
		}
	}
}

type ZeroSchedule struct{}

func (*ZeroSchedule) Next(time.Time) time.Time {
	return time.Time{}
}

// Tests that job without time does not run
func TestJobWithZeroTimeDoesNotRun(t *testing.T) {
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock))
	var calls int32
	_, _ = cron.AddFunc("* * * * * *", func() { atomic.AddInt32(&calls, 1) })
	cron.Schedule(new(ZeroSchedule), FuncJob(func() { t.Error("expected zero task will not run") }))
	cron.Start()
	defer cron.Stop()
	cycle(cron)
	clock.Advance(time.Second)
	cycle(cron)
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls))
}

func TestRemove(t *testing.T) {
	var calls int32
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock))
	id, _ := cron.AddFunc("* * * * * *", func() { atomic.AddInt32(&calls, 1) })
	assert.Equal(t, int32(0), atomic.LoadInt32(&calls))
	cron.Start()
	defer cron.Stop()
	cycle(cron)
	clock.Advance(time.Second)
	cycle(cron)
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls))
	clock.Advance(time.Second)
	cycle(cron)
	assert.Equal(t, int32(2), atomic.LoadInt32(&calls))
	clock.Advance(500 * time.Millisecond)
	cycle(cron)
	cron.Remove(id)
	assert.Equal(t, int32(2), atomic.LoadInt32(&calls))
	clock.Advance(500 * time.Millisecond)
	cycle(cron)
	assert.Equal(t, int32(2), atomic.LoadInt32(&calls))
	clock.Advance(time.Second)
	cycle(cron)
	assert.Equal(t, int32(2), atomic.LoadInt32(&calls))
}

// Issue #206
// Ensure that the next run of a job after removing an entry is accurate.
func TestScheduleAfterRemoval(t *testing.T) {
	var wg1 sync.WaitGroup
	var wg2 sync.WaitGroup
	wg1.Add(1)
	wg2.Add(1)

	// The first time this job is run, set a timer and remove the other job
	// 750ms later. Correct behavior would be to still run the job again in
	// 250ms, but the bug would cause it to run instead 1s later.

	var calls int32
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock))
	hourJob := cron.Schedule(Every(time.Hour), FuncJob(func() {}))
	cron.Schedule(Every(time.Second), FuncJob(func() {
		switch atomic.LoadInt32(&calls) {
		case 0:
			wg1.Done()
			atomic.AddInt32(&calls, 1)
		case 1:
			clock.Advance(750 * time.Millisecond)
			cron.Remove(hourJob)
			cycle(cron)
			atomic.AddInt32(&calls, 1)
		case 2:
			atomic.AddInt32(&calls, 1)
			wg2.Done()
		case 3:
			panic("unexpected 3rd call")
		}
	}))

	cron.Start()
	defer cron.Stop()
	cycle(cron)
	clock.Advance(time.Second)
	cycle(cron)

	// the first run might be any length of time 0 - 1s, since the schedule
	// rounds to the second. wait for the first run to true up.
	wg1.Wait()

	clock.Advance(time.Second)
	cycle(cron)
	clock.Advance(250 * time.Millisecond)
	cycle(cron)

	recvWithTimeout(t, wait(&wg2), "\"expected job fires 2 times\"")
}

func TestTwoCrons(t *testing.T) {
	var calls int32
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock))
	_, _ = cron.AddFunc("1 * * * * *", func() { atomic.AddInt32(&calls, 1) })
	_, _ = cron.AddFunc("3 * * * * *", func() { atomic.AddInt32(&calls, 1) })
	assert.Equal(t, int32(0), atomic.LoadInt32(&calls))
	cron.Start()
	defer cron.Stop()
	cycle(cron)
	clock.Advance(time.Second)
	cycle(cron)
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls))
	clock.Advance(time.Second)
	cycle(cron)
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls))
	clock.Advance(time.Second)
	cycle(cron)
	assert.Equal(t, int32(2), atomic.LoadInt32(&calls))
}

func TestMultipleCrons(t *testing.T) {
	var calls int32
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock))
	_, _ = cron.AddFunc("1 * * * * *", func() { atomic.AddInt32(&calls, 1) })
	_, _ = cron.AddFunc("* * * * * *", func() { atomic.AddInt32(&calls, 1) })
	_, _ = cron.AddFunc("3 * * * * *", func() { atomic.AddInt32(&calls, 1) })
	assert.Equal(t, int32(0), atomic.LoadInt32(&calls))
	cron.Start()
	defer cron.Stop()
	cycle(cron)
	clock.Advance(time.Second)
	cycle(cron)
	assert.Equal(t, int32(2), atomic.LoadInt32(&calls))
	clock.Advance(time.Second)
	cycle(cron)
	assert.Equal(t, int32(3), atomic.LoadInt32(&calls))
	clock.Advance(time.Second)
	cycle(cron)
	assert.Equal(t, int32(5), atomic.LoadInt32(&calls))
	clock.Advance(time.Second)
	cycle(cron)
	assert.Equal(t, int32(6), atomic.LoadInt32(&calls))
	clock.Advance(time.Second)
	cycle(cron)
	assert.Equal(t, int32(7), atomic.LoadInt32(&calls))
	clock.Advance(time.Second)
	cycle(cron)
	assert.Equal(t, int32(8), atomic.LoadInt32(&calls))
}

func TestSetEntriesNext(t *testing.T) {
	var calls int32
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock))
	_, _ = cron.AddFunc("*/5 * * * * *", func() { atomic.AddInt32(&calls, 1) })
	assert.Equal(t, int32(0), atomic.LoadInt32(&calls))
	clock.Advance(58 * time.Second)
	cycle(cron)
	cron.Start()
	defer cron.Stop()
	assert.Equal(t, int32(0), atomic.LoadInt32(&calls))
	cycle(cron)
	clock.Advance(time.Second)
	cycle(cron)
	assert.Equal(t, int32(0), atomic.LoadInt32(&calls))
	clock.Advance(time.Second)
	cycle(cron)
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls))
}

func TestNextIDIsThreadSafe(t *testing.T) {
	var calls int32
	clock := clockwork.NewFakeClock()
	cron := New(WithClock(clock))
	wg := sync.WaitGroup{}
	wg.Add(1000)
	for i := 0; i < 1000; i++ {
		go func() {
			defer wg.Done()
			_, _ = cron.AddFunc("*/5 * * * * *", func() { atomic.AddInt32(&calls, 1) })
		}()
	}
	wg.Wait()
	m := make(map[EntryID]bool)
	for _, e := range cron.entries {
		if _, ok := m[e.ID]; ok {
			t.Fail()
			return
		}
		m[e.ID] = true
	}
}
