package cron

import (
	"context"
	"errors"
	"github.com/alaingilbert/cron/internal/mtx"
	"github.com/alaingilbert/cron/internal/utils"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alaingilbert/clockwork"
)

// Cron keeps track of any number of entries, invoking the associated func as
// specified by the schedule. It may be started, stopped, and the entries may
// be inspected while running.
type Cron struct {
	clock            clockwork.Clock           // Clock interface (real or mock) used for timing
	nextID           atomic.Int32              // Auto-incrementing ID for new jobs
	runningJobsCount atomic.Int32              // Count of currently running jobs
	cond             sync.Cond                 // Signals when all jobs have completed
	entries          mtx.RWMtxSlice[*Entry]    // Thread-safe, sorted list of job entries
	ctx              context.Context           // Context to control the scheduler lifecycle
	cancel           context.CancelFunc        // Cancels the scheduler context
	update           chan context.CancelFunc   // Triggers update in the scheduler loop
	running          atomic.Bool               // Indicates if the scheduler is currently running
	location         mtx.RWMtx[*time.Location] // Thread-safe time zone location
}

type EntryID int32

// ErrEntryNotFound ...
var ErrEntryNotFound = errors.New("entry not found")

// ErrUnsupportedJobType ...
var ErrUnsupportedJobType = errors.New("unsupported job type")

// Job is an interface for submitted cron jobs.
type Job interface {
	Run(context.Context)
}

type JobWrapper func(Job) Job

type OnceJob struct{ Job }

func (j *OnceJob) Run(ctx context.Context) { j.Job.Run(ctx) }

// Once creates a Job that will remove itself from the entries once executed
func Once(job Job) Job { return &OnceJob{job} }

// SkipIfStillRunning skips an invocation of the Job if a previous invocation is still running.
func SkipIfStillRunning(j Job) Job {
	var running atomic.Bool
	return FuncJob(func(ctx context.Context) {
		if running.CompareAndSwap(false, true) {
			defer running.Store(false)
			j.Run(ctx)
		}
	})
}

// TimeoutWrapper automatically cancel the job context after a given duration
func TimeoutWrapper(duration time.Duration) func(j Job) Job {
	return func(j Job) Job {
		return FuncJob(func(ctx context.Context) {
			timeoutCtx, cancel := context.WithTimeout(ctx, duration)
			defer cancel()
			j.Run(timeoutCtx)
		})
	}
}

// WithTimeout ...
// `_, _ = cron.AddJob("* * * * * *", cron.WithTimeout(time.Second, func(ctx context.Context) { ... }))`
func WithTimeout(d time.Duration, job IntoJob) Job {
	j, err := castIntoJob(job)
	if err != nil {
		panic(err)
	}
	return TimeoutWrapper(d)(j)
}

type IntoJob any

func castIntoJob(v IntoJob) (Job, error) {
	switch j := v.(type) {
	case func(ctx context.Context):
		return FuncJob(j), nil
	case Job:
		return j, nil
	default:
		return nil, ErrUnsupportedJobType
	}
}

// Chain `Chain(j, w1, w2, w3)` -> `w3(w2(w1(j)))`
func Chain(j Job, wrappers ...JobWrapper) Job {
	for _, w := range wrappers {
		j = w(j)
	}
	return j
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
	Job   Job
	Label string
}

func less(e1, e2 *Entry) bool {
	if e1.Next.IsZero() {
		return false
	} else if e2.Next.IsZero() {
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
	ctx, cancel := context.WithCancel(parentCtx)
	return &Cron{
		cond:     sync.Cond{L: &sync.Mutex{}},
		clock:    clock,
		ctx:      ctx,
		cancel:   cancel,
		update:   make(chan context.CancelFunc),
		location: mtx.NewRWMtx(location),
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
type FuncJob func(context.Context)

func (f FuncJob) Run(ctx context.Context) { f(ctx) }

// AddFunc adds a func to the Cron to be run on the given schedule.
func (c *Cron) AddFunc(spec string, cmd func(context.Context), opts ...EntryOption) (EntryID, error) {
	return c.AddJob(spec, FuncJob(cmd), opts...)
}

// AddJob adds a Job to the Cron to be run on the given schedule.
func (c *Cron) AddJob(spec string, cmd Job, opts ...EntryOption) (EntryID, error) {
	schedule, err := Parse(spec)
	if err != nil {
		return 0, err
	}
	return c.Schedule(schedule, cmd, opts...), nil
}

// Schedule adds a Job to the Cron to be run on the given schedule.
func (c *Cron) Schedule(schedule Schedule, cmd Job, opts ...EntryOption) EntryID {
	newID := c.nextID.Add(1)
	entry := &Entry{
		ID:       EntryID(newID),
		Schedule: schedule,
		Job:      cmd,
	}
	utils.ApplyOptions(entry, opts)
	entry.Next = entry.Schedule.Next(c.now())
	c.entries.With(func(entries *[]*Entry) { insertSorted(entries, entry) })
	c.entriesUpdated()
	return entry.ID
}

func insertSorted(entries *[]*Entry, entry *Entry) {
	i := sort.Search(len(*entries), func(i int) bool { return !less((*entries)[i], entry) })
	*entries = append(*entries, nil)
	copy((*entries)[i+1:], (*entries)[i:])
	(*entries)[i] = entry
}

// Entries returns a snapshot of the cron entries.
func (c *Cron) Entries() (out []Entry) {
	c.entries.RWith(func(entries []*Entry) {
		out = make([]Entry, len(entries))
		for i, e := range entries {
			out[i] = *e
		}
	})
	return
}

// Entry returns a snapshot of the given entry, or nil if it couldn't be found.
func (c *Cron) Entry(id EntryID) (out Entry, err error) {
	for _, entry := range c.Entries() {
		if id == entry.ID {
			return entry, nil
		}
	}
	return out, ErrEntryNotFound
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

func (c *Cron) runWithRecovery(j Job) {
	defer func() { recover() }()
	j.Run(c.ctx)
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
				if entry.Next.After(now) || entry.Next.IsZero() {
					break
				}
				c.startJob(entry.Job)
				if _, ok := entry.Job.(*OnceJob); ok {
					removeEntry(entries, entry.ID)
				} else {
					entry.Prev = entry.Next
					entry.Next = entry.Schedule.Next(now) // Compute new Next property for the Entry
					toSortCount++
				}
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
		sort.Slice(*entries, func(i, j int) bool { return less((*entries)[i], (*entries)[j]) })
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
func (c *Cron) startJob(j Job) {
	c.runningJobsCount.Add(1)
	go func() {
		defer func() {
			c.runningJobsCount.Add(-1)
			c.signalJobCompleted()
		}()
		c.runWithRecovery(j)
	}()
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
