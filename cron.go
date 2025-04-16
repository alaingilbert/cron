package cron

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"runtime/debug"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alaingilbert/clockwork"
	"github.com/alaingilbert/cron/internal/mtx"
	"github.com/alaingilbert/cron/internal/utils"
	"github.com/google/uuid"
)

// Cron keeps track of any number of entries, invoking the associated func as
// specified by the schedule. It may be started, stopped, and the entries may
// be inspected while running.
type Cron struct {
	clock            clockwork.Clock           // Clock interface (real or mock) used for timing
	runningJobsCount atomic.Int32              // Count of currently running jobs
	cond             sync.Cond                 // Signals when all jobs have completed
	entries          mtx.RWMtxSlice[*Entry]    // Thread-safe, sorted list of job entries
	ctx              context.Context           // Context to control the scheduler lifecycle
	cancel           context.CancelFunc        // Cancels the scheduler context
	update           chan context.CancelFunc   // Triggers update in the scheduler loop
	running          atomic.Bool               // Indicates if the scheduler is currently running
	location         mtx.RWMtx[*time.Location] // Thread-safe time zone location
	logger           *log.Logger               // Logger
}

type EntryID string

// ErrEntryNotFound ...
var ErrEntryNotFound = errors.New("entry not found")

// ErrUnsupportedJobType ...
var ErrUnsupportedJobType = errors.New("unsupported job type")

// ErrJobAlreadyRunning ...
var ErrJobAlreadyRunning = errors.New("job already running")

// ErrIDAlreadyUsed ...
var ErrIDAlreadyUsed = errors.New("id already used")

type JobWrapper func(IntoJob) Job

func Once(job IntoJob) Job {
	return FuncJob(func(ctx context.Context, c *Cron, id EntryID) error {
		c.Remove(id)
		return castIntoJob(job).Run(ctx, c, id)
	})
}

func NWrapper(n int) JobWrapper {
	return func(job IntoJob) Job {
		var count atomic.Int32
		return FuncJob(func(ctx context.Context, c *Cron, id EntryID) error {
			if newCount := count.Add(1); int(newCount) == n {
				c.Remove(id)
			}
			return castIntoJob(job).Run(ctx, c, id)
		})
	}
}

func ScheduledTime(job func(t time.Time)) Job {
	return FuncJob(func(ctx context.Context, c *Cron, id EntryID) error {
		entry, err := c.Entry(id)
		if err != nil {
			return err
		}
		job(entry.Prev)
		return nil
	})
}

// N runs a job "n" times
func N(n int, j IntoJob) Job {
	return NWrapper(n)(j)
}

// SkipIfStillRunning skips an invocation of the Job if a previous invocation is still running.
func SkipIfStillRunning(j IntoJob) Job {
	var running atomic.Bool
	return FuncJob(func(ctx context.Context, c *Cron, id EntryID) (err error) {
		if running.CompareAndSwap(false, true) {
			defer running.Store(false)
			err = castIntoJob(j).Run(ctx, c, id)
		} else {
			return ErrJobAlreadyRunning
		}
		return
	})
}

// JitterWrapper add some random delay before running the job
func JitterWrapper(duration time.Duration) JobWrapper {
	return func(j IntoJob) Job {
		return FuncJob(func(ctx context.Context, c *Cron, id EntryID) error {
			delay := utils.RandDuration(0, max(duration, 0))
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
			return castIntoJob(j).Run(ctx, c, id)
		})
	}
}

// WithJitter add some random delay before running the job
func WithJitter(duration time.Duration, job IntoJob) Job {
	return JitterWrapper(duration)(job)
}

// TimeoutWrapper automatically cancel the job context after a given duration
func TimeoutWrapper(duration time.Duration) JobWrapper {
	return func(j IntoJob) Job {
		return FuncJob(func(ctx context.Context, c *Cron, id EntryID) error {
			timeoutCtx, cancel := context.WithTimeout(ctx, duration)
			defer cancel()
			return castIntoJob(j).Run(timeoutCtx, c, id)
		})
	}
}

// WithTimeout ...
// `_, _ = cron.AddJob("* * * * * *", cron.WithTimeout(time.Second, func(ctx context.Context) { ... }))`
func WithTimeout(d time.Duration, job IntoJob) Job {
	return TimeoutWrapper(d)(job)
}

func DeadlineWrapper(deadline time.Time) JobWrapper {
	return func(j IntoJob) Job {
		return FuncJob(func(ctx context.Context, c *Cron, id EntryID) error {
			deadlineCtx, cancel := context.WithDeadline(ctx, deadline)
			defer cancel()
			return castIntoJob(j).Run(deadlineCtx, c, id)
		})
	}
}

func WithDeadline(deadline time.Time, job IntoJob) Job {
	return DeadlineWrapper(deadline)(job)
}

// Chain `Chain(j, w1, w2, w3)` -> `w3(w2(w1(j)))`
func Chain(j IntoJob, wrappers ...JobWrapper) Job {
	job := castIntoJob(j)
	for _, w := range wrappers {
		job = w(job)
	}
	return job
}

// The Schedule describes a job's duty cycle.
type Schedule interface {
	// Next return the next activation time, later than the given time.
	// Next is invoked initially, and then each time the job is run.
	Next(time.Time) time.Time
}

type EntryOption func(*Entry)

func Label(label string) func(entry *Entry) {
	return func(entry *Entry) {
		entry.Label = label
	}
}

func WithID(id EntryID) func(entry *Entry) {
	return func(entry *Entry) {
		entry.ID = id
	}
}

func WithNext(next time.Time) func(entry *Entry) {
	return func(entry *Entry) {
		entry.Next = next
	}
}

func RunOnStart(entry *Entry) {
	entry.Next = time.Now()
}

func Disabled(entry *Entry) {
	entry.Active = false
}

// Entry consists of a schedule and the func to execute on that schedule.
type Entry struct {
	ID EntryID
	// The schedule on which this job should be run.
	Schedule Schedule
	// The next time the job will run. This is the zero time if Cron has not been started or this entry's schedule is unsatisfiable
	Next time.Time
	// The last time this job was run. This is the zero time if the job has never been run.
	Prev time.Time
	// The Job to run.
	job   Job
	Label string

	Active bool
}

// Job returns the original job as it was before it was wrapped by the cron library
func (e Entry) Job() any {
	switch j := e.job.(type) {
	case *Job1Wrapper:
		return j.Job1
	case *Job2Wrapper:
		return j.Job2
	case *Job3Wrapper:
		return j.Job3
	case *Job4Wrapper:
		return j.Job4
	case *Job5Wrapper:
		return j.Job5
	case *Job6Wrapper:
		return j.Job6
	case *Job7Wrapper:
		return j.Job7
	case *Job8Wrapper:
		return j.Job8
	case *Job9Wrapper:
		return j.Job9
	case *Job10Wrapper:
		return j.Job10
	case *Job11Wrapper:
		return j.Job11
	case *Job12Wrapper:
		return j.Job12
	case *Job13Wrapper:
		return j.Job13
	case *Job14Wrapper:
		return j.Job14
	case *Job15Wrapper:
		return j.Job15
	default:
		return e.job
	}
}

func less(e1, e2 *Entry) bool {
	if e1.Next.IsZero() || !e1.Active {
		return false
	} else if e2.Next.IsZero() || !e2.Active {
		return true
	}
	return e1.Next.Before(e2.Next)
}

// New returns a new Cron job runner, in the Local time zone.
func New(opts ...Option) *Cron {
	cfg := utils.BuildConfig(opts)
	clock := utils.Or(cfg.Clock, clockwork.NewRealClock())
	location := utils.Or(cfg.Location, clock.Location())
	parentCtx := utils.Or(cfg.Ctx, context.Background())
	logger := utils.Or(cfg.Logger, log.New(os.Stderr, "cron", log.LstdFlags))
	ctx, cancel := context.WithCancel(parentCtx)
	return &Cron{
		cond:     sync.Cond{L: &sync.Mutex{}},
		clock:    clock,
		ctx:      ctx,
		cancel:   cancel,
		update:   make(chan context.CancelFunc),
		location: mtx.NewRWMtx(location),
		logger:   logger,
	}
}

func (c *Cron) startRunning() bool {
	return c.running.CompareAndSwap(false, true)
}
func (c *Cron) stopRunning() bool {
	return c.running.CompareAndSwap(true, false)
}
func (c *Cron) isRunning() bool {
	return c.running.Load()
}

// FuncJob is a wrapper that turns a func() into a cron.Job
type FuncJob func(context.Context, *Cron, EntryID) error

func (f FuncJob) Run(ctx context.Context, c *Cron, id EntryID) error { return f(ctx, c, id) }

// AddJob adds a Job to the Cron to be run on the given schedule.
func (c *Cron) AddJob(spec string, cmd IntoJob, opts ...EntryOption) (EntryID, error) {
	schedule, err := Parse(spec)
	if err != nil {
		return "", err
	}
	return c.Schedule(schedule, cmd, opts...)
}

// Schedule adds a Job to the Cron to be run on the given schedule.
func (c *Cron) Schedule(schedule Schedule, cmd IntoJob, opts ...EntryOption) (EntryID, error) {
	entry := &Entry{
		ID:       EntryID(uuid.New().String()),
		Schedule: schedule,
		job:      castIntoJob(cmd),
		Active:   true,
		Next:     schedule.Next(c.now()),
	}
	utils.ApplyOptions(entry, opts)
	if c.entryExists(entry.ID) {
		return "", ErrIDAlreadyUsed
	}
	c.entries.With(func(entries *[]*Entry) { insertSorted(entries, entry) })
	c.entriesUpdated()
	return entry.ID, nil
}

func (c *Cron) Enable(id EntryID) { c.setEntryActive(id, true) }

func (c *Cron) Disable(id EntryID) { c.setEntryActive(id, false) }

func (c *Cron) setEntryActive(id EntryID, active bool) {
	if err := c.entries.WithE(func(entries *[]*Entry) error {
		entry := utils.Find(*entries, func(e *Entry) bool { return e.ID == id })
		if entry == nil {
			return errors.New("not found")
		}
		if (*entry).Active == active {
			return errors.New("unchanged")
		}
		(*entry).Active = active
		c.sortEntries(entries)
		return nil
	}); err != nil {
		return
	}
	c.entriesUpdated()
}

func (c *Cron) sortEntries(entries *[]*Entry) {
	sort.Slice(*entries, func(i, j int) bool { return less((*entries)[i], (*entries)[j]) })
}

func insertSorted(entries *[]*Entry, entry *Entry) {
	i := sort.Search(len(*entries), func(i int) bool { return !less((*entries)[i], entry) })
	*entries = append(*entries, nil)
	copy((*entries)[i+1:], (*entries)[i:])
	(*entries)[i] = entry
}

// Entries returns a snapshot of the cron entries.
func (c *Cron) Entries() (out []Entry) {
	return c.getEntries()
}

// Entry returns a snapshot of the given entry, or nil if it couldn't be found.
func (c *Cron) Entry(id EntryID) (out Entry, err error) {
	return c.getEntry(id)
}

func (c *Cron) getEntries() (out []Entry) {
	c.entries.RWith(func(entries []*Entry) {
		out = make([]Entry, len(entries))
		for i, e := range entries {
			out[i] = *e
		}
	})
	return
}

func (c *Cron) getEntry(id EntryID) (out Entry, err error) {
	for _, entry := range c.getEntries() {
		if entry.ID == id {
			return entry, nil
		}
	}
	return out, ErrEntryNotFound
}

func (c *Cron) entryExists(id EntryID) bool {
	return utils.Second(c.getEntry(id)) == nil
}

// Remove an entry from being run in the future.
func (c *Cron) Remove(id EntryID) {
	c.removeEntry(id)
	c.entriesUpdated()
}

// Run the cron scheduler, or no-op if already running.
func (c *Cron) Run() (started bool) {
	return c.startWith(func() { c.run() }) // sync
}

// Start the cron scheduler in its own go-routine, or no-op if already started.
func (c *Cron) Start() (started bool) {
	return c.startWith(func() { go c.run() }) // async
}

func (c *Cron) startWith(runFunc func()) (started bool) {
	if started = c.startRunning(); started {
		runFunc()
	}
	return
}

// Stop stops the cron scheduler if it is running; otherwise it does nothing.
// A context is returned so the caller can wait for running jobs to complete.
func (c *Cron) Stop() <-chan struct{} {
	if !c.stopRunning() {
		return nil
	}
	c.cancel()
	ch := make(chan struct{})
	go func() {
		c.waitAllJobsCompleted()
		close(ch)
	}()
	return ch
}

func (c *Cron) runWithRecovery(entry Entry) {
	defer func() {
		if r := recover(); r != nil {
			c.logger.Printf("%s\n", string(debug.Stack()))
		}
	}()
	if err := entry.job.Run(c.ctx, c, entry.ID); err != nil {
		msg := fmt.Sprintf("error running job %s", entry.ID)
		msg += utils.TernaryOrZero(entry.Label != "", " "+entry.Label)
		msg += " : " + err.Error()
		c.logger.Print(msg)
	}
}

// Run the scheduler. this is private just due to the need to synchronize
// access to the 'running' state variable.
func (c *Cron) run() {
	for {
		// Determine the next entry to run.
		delay := c.getNextDelay()
		var updated context.CancelFunc
		select {
		case <-c.clock.After(delay):
		case updated = <-c.update:
		case <-c.ctx.Done():
			return
		}
		c.runDueEntries()
		if updated != nil {
			updated()
		}
		if c.ctx.Err() != nil {
			return
		}
	}
}

func (c *Cron) getNextDelay() (out time.Duration) {
	c.entries.RWith(func(entries []*Entry) {
		if len(entries) == 0 || entries[0].Next.IsZero() {
			out = 100_000 * time.Hour // If there are no entries yet, just sleep - it still handles new entries and stop requests.
		} else {
			out = entries[0].Next.Sub(c.now())
		}
	})
	return
}

// trigger an update of the entries in the run loop
func (c *Cron) entriesUpdated() {
	if c.isRunning() { // If the cron is not running, no need to notify the main loop about updating entries
		ctx, cancel := context.WithCancel(c.ctx)
		select { // Wait until the main loop pickup our update task (or cron exiting)
		case c.update <- cancel:
		case <-ctx.Done():
		}
		<-ctx.Done() // Wait until "runDueEntries" is done running (or cron exiting)
	}
}

// Run every entry whose next time was less than now
func (c *Cron) runDueEntries() {
	if c.isRunning() {
		now := c.now()
		c.entries.With(func(entries *[]*Entry) {
			var toSortCount int
			for _, entry := range *entries {
				if entry.Next.After(now) || entry.Next.IsZero() || !entry.Active {
					break
				}
				c.startJob(*entry)
				entry.Prev = entry.Next
				entry.Next = entry.Schedule.Next(now) // Compute new Next property for the Entry
				toSortCount++
			}
			utils.InsertionSortPartial(*entries, toSortCount, less)
		})
	}
}

func (c *Cron) setEntriesNext() {
	now := c.now()
	c.entries.With(func(entries *[]*Entry) {
		for _, entry := range *entries {
			entry.Next = entry.Schedule.Next(now)
		}
		c.sortEntries(entries)
	})
}

func (c *Cron) removeEntry(id EntryID) {
	c.entries.With(func(entries *[]*Entry) {
		removeEntry(entries, id)
	})
}

func removeEntry(entries *[]*Entry, id EntryID) {
	for i, entry := range *entries {
		if entry.ID == id {
			*entries = slices.Delete(*entries, i, i+1)
			break
		}
	}
}

// startJob runs the given job in a new goroutine.
func (c *Cron) startJob(entry Entry) {
	c.runningJobsCount.Add(1)
	go func() {
		defer func() {
			c.runningJobsCount.Add(-1)
			c.signalJobCompleted()
		}()
		c.runWithRecovery(entry)
	}()
}

// RunNow allows the user to run a specific job now
func (c *Cron) RunNow(id EntryID) {
	if err := c.entries.WithE(func(entries *[]*Entry) error {
		entry := utils.Find(*entries, func(e *Entry) bool { return e.ID == id })
		if entry == nil {
			return errors.New("not found")
		}
		(*entry).Next = c.now()
		c.sortEntries(entries)
		return nil
	}); err != nil {
		return
	}
	c.entriesUpdated()
}

func (c *Cron) signalJobCompleted() {
	c.cond.L.Lock()
	c.cond.Broadcast()
	c.cond.L.Unlock()
}

func (c *Cron) waitAllJobsCompleted() {
	c.cond.L.Lock()
	for c.runningJobsCount.Load() != 0 {
		c.cond.Wait()
	}
	c.cond.L.Unlock()
}

// Location gets the time zone location
func (c *Cron) Location() *time.Location {
	return c.location.Get()
}

// SetLocation sets a new location to use.
// Re-set the "Next" values for all entries.
// Re-sort entries and run due entries.
func (c *Cron) SetLocation(newLoc *time.Location) {
	c.location.Set(newLoc)
	c.setEntriesNext()
	c.entriesUpdated()
}

// now returns current time in c location
func (c *Cron) now() time.Time {
	return c.clock.Now().In(c.Location())
}
