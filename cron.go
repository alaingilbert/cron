package cron

import (
	"context"
	"errors"
	"log/slog"
	"runtime"
	"runtime/debug"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alaingilbert/cron/internal/mtx"
	"github.com/alaingilbert/cron/internal/pubsub"
	isync "github.com/alaingilbert/cron/internal/sync"
	"github.com/alaingilbert/cron/internal/utils"
	"github.com/jonboulle/clockwork"
)

// Cron keeps track of any number of entries, invoking the associated func as
// specified by the schedule. It may be started, stopped, and the entries may
// be inspected while running.
type Cron struct {
	clock                clockwork.Clock                              // Clock interface (real or mock) used for timing
	runningJobsCount     atomic.Int32                                 // Count of currently running jobs
	runningJobsMap       isync.Map[EntryID, *mtx.RWMtx[jobRunsInner]] // Keep track of currently running jobs
	cond                 sync.Cond                                    // Signals when all jobs have completed
	entries              mtx.RWMtx[entries]                           // Thread-safe, sorted list of job entries
	ctx                  context.Context                              // Context to control the scheduler lifecycle
	cancel               context.CancelFunc                           // Cancels the scheduler context
	update               chan context.CancelFunc                      // Triggers update in the scheduler loop
	running              atomic.Bool                                  // Indicates if the scheduler is currently running
	location             atomic.Pointer[time.Location]                // Thread-safe time zone location
	logger               *slog.Logger                                 // Logger for scheduler events, errors, and diagnostics
	parser               ScheduleParser                               // Parses cron expressions into schedule objects
	idFactory            EntryIDFactory                               // Generates a new unique EntryID for each scheduled job
	ps                   *pubsub.PubSub[EntryID, JobEvent]            // PubSub for publishing and subscribing to job events
	jobRunCreatedCh      chan JobRun                                  // Channel for receiving notifications when new job runs are created
	jobRunCompletedCh    chan JobRun                                  // Channel for receiving notifications when job runs complete
	keepCompletedRunsDur mtx.Mtx[time.Duration]                       // Duration to keep completed job runs before cleanup (thread-safe)
	lastCleanupTS        mtx.Mtx[time.Time]                           // Timestamp of last completed job runs cleanup (thread-safe)
	hooks                mtx.RWMtx[hooksContainer]                    //
}

type hooksContainer struct {
	hooksMap      map[JobEventType][]hookStruct
	entryHooksMap map[EntryID]map[JobEventType][]hookStruct
}

type jobRunsInner struct {
	mapping     map[RunID]*jobRunStruct
	running     []*jobRunStruct
	completed   []*jobRunStruct
	completedTS []time.Time
}

type entries struct {
	heap       *entryHeap
	entriesMap map[EntryID]*Entry
}

// ErrEntryNotFound ...
var ErrEntryNotFound = errors.New("entry not found")

// ErrJobRunNotFound ...
var ErrJobRunNotFound = errors.New("job run not found")

// ErrUnsupportedJobType ...
var ErrUnsupportedJobType = errors.New("unsupported job type")

// ErrJobAlreadyRunning ...
var ErrJobAlreadyRunning = errors.New("job already running")

// ErrIDAlreadyUsed ...
var ErrIDAlreadyUsed = errors.New("id already used")

// The Schedule describes a job's duty cycle.
type Schedule interface {
	// Next return the next activation time, later than the given time.
	// Next is invoked initially, and then each time the job is run.
	Next(time.Time) time.Time
}

// ScheduleParser is an interface for schedule spec parsers that return a Schedule
type ScheduleParser interface {
	Parse(spec string) (Schedule, error)
}

type FuncEntryIDFactory func() EntryID

func (f FuncEntryIDFactory) Next() EntryID { return f() }

type EntryIDFactory interface {
	Next() EntryID
}

// UUIDEntryIDFactory generate and format UUID V4
func UUIDEntryIDFactory() EntryIDFactory {
	return FuncEntryIDFactory(func() EntryID {
		return EntryID(utils.UuidV4Str())
	})
}

//-----------------------------------------------------------------------------

// New returns a new Cron job runner
func New(opts ...Option) *Cron {
	cfg := utils.BuildConfig(opts)
	clock := utils.Or(cfg.Clock, clockwork.NewRealClock())
	location := utils.Or(cfg.Location, clock.Now().Location())
	parentCtx := utils.Or(cfg.Ctx, context.Background())
	logger := utils.Or(cfg.Logger, slog.Default())
	parser := utils.Or(cfg.Parser, ScheduleParser(standardParser))
	idFactory := utils.Or(cfg.IDFactory, UUIDEntryIDFactory())
	keepCompletedRunsDur := utils.Default(cfg.KeepCompletedRunsDur, time.Minute)
	ctx, cancel := context.WithCancel(parentCtx)
	c := &Cron{
		cond:                 sync.Cond{L: &sync.Mutex{}},
		clock:                clock,
		ctx:                  ctx,
		cancel:               cancel,
		runningJobsMap:       isync.Map[EntryID, *mtx.RWMtx[jobRunsInner]]{},
		update:               make(chan context.CancelFunc),
		location:             utils.NewAtomicPtr(location),
		logger:               logger,
		parser:               parser,
		idFactory:            idFactory,
		ps:                   pubsub.NewPubSub[EntryID, JobEvent](),
		jobRunCreatedCh:      make(chan JobRun),
		jobRunCompletedCh:    make(chan JobRun),
		keepCompletedRunsDur: mtx.NewMtx(keepCompletedRunsDur),
		hooks: mtx.NewRWMtx(hooksContainer{
			hooksMap:      make(map[JobEventType][]hookStruct),
			entryHooksMap: make(map[EntryID]map[JobEventType][]hookStruct),
		}),
		entries: mtx.NewRWMtx(entries{
			heap:       newEntryHeap(),
			entriesMap: make(map[EntryID]*Entry),
		}),
	}
	if keepCompletedRunsDur > 0 {
		startCleanupThread(c)
	}
	return c
}

// Run the cron scheduler, or no-op if already running.
func (c *Cron) Run() (started bool) { return c.start() }

// Start the cron scheduler in its own go-routine, or no-op if already started.
func (c *Cron) Start() (started bool) { return c.startAsync() }

// Stop stops the cron scheduler if it is running; otherwise it does nothing.
// A context is returned so the caller can wait for running jobs to complete.
func (c *Cron) Stop() <-chan struct{} { return c.stop() }

// Wait stops the cron and waits for all running jobs to complete before returning.
func (c *Cron) Wait() { c.wait() }

// OnEvt registers a global hook function for a specific job event type.
// The hook will be called whenever the specified event occurs for any job.
// Returns the HookID for later removal.
func (c *Cron) OnEvt(evt JobEventType, clb HookFn, opts ...HookOption) HookID {
	return c.onEvt(evt, clb, opts...)
}

// OnEntryEvt registers a hook function for a specific job event type on a specific entry.
// The hook will be called only when the specified event occurs for the given entry.
// Returns the HookID for later removal.
func (c *Cron) OnEntryEvt(entryID EntryID, evt JobEventType, clb HookFn, opts ...HookOption) HookID {
	return c.onEntryEvt(entryID, evt, clb, opts...)
}

// RemoveHook removes a previously registered hook by its HookID.
// Works for both global and entry-specific hooks.
func (c *Cron) RemoveHook(id HookID) { c.removeHook(id) }

// OnJobStart registers a global hook function for job start events.
// The hook will be called whenever any job starts.
// Returns the HookID for later removal.
func (c *Cron) OnJobStart(clb HookFn, opts ...HookOption) HookID { return c.onJobStart(clb, opts...) }

// OnEntryJobStart registers a hook function for job start events on a specific entry.
// The hook will be called only when the given entry's job starts.
// Returns the HookID for later removal.
func (c *Cron) OnEntryJobStart(entryID EntryID, clb HookFn, opts ...HookOption) HookID {
	return c.onEntryJobStart(entryID, clb, opts...)
}

// OnJobCompleted registers a global hook function for job completion events.
// The hook will be called whenever any job completes.
// Returns the HookID for later removal.
func (c *Cron) OnJobCompleted(clb HookFn, opts ...HookOption) HookID {
	return c.onJobCompleted(clb, opts...)
}

// OnEntryJobCompleted registers a hook function for job completion events on a specific entry.
// The hook will be called only when the given entry's job completes.
// Returns the HookID for later removal.
func (c *Cron) OnEntryJobCompleted(entryID EntryID, clb HookFn, opts ...HookOption) HookID {
	return c.onEntryJobCompleted(entryID, clb, opts...)
}

// AddJob adds a Job to the Cron to be run on the given schedule.
func (c *Cron) AddJob(spec string, job IntoJob, opts ...EntryOption) (EntryID, error) {
	return c.addJob(spec, job, opts...)
}

// AddJob1 adds a Job to the Cron to be run on the given schedule.
func (c *Cron) AddJob1(spec string, job Job, opts ...EntryOption) (EntryID, error) {
	return c.addJob(spec, job, opts...)
}

// Schedule adds a Job to the Cron to be run on the given schedule.
func (c *Cron) Schedule(schedule Schedule, job Job, opts ...EntryOption) (EntryID, error) {
	return c.schedule(nil, schedule, job, opts...)
}

// AddEntry adds a pre-configured Entry to the Cron
func (c *Cron) AddEntry(entry Entry, opts ...EntryOption) (EntryID, error) {
	return c.addEntry(entry, opts...)
}

// UpdateSchedule updates an entry's schedule with a new Schedule
func (c *Cron) UpdateSchedule(id EntryID, schedule Schedule) error {
	return c.updateSchedule(id, nil, schedule)
}

// UpdateScheduleWithSpec updates an entry's schedule by parsing a spec string
func (c *Cron) UpdateScheduleWithSpec(id EntryID, spec string) error {
	return c.updateScheduleWithSpec(id, spec)
}

// UpdateLabel updates an entry's label
func (c *Cron) UpdateLabel(id EntryID, label string) { c.updateLabel(id, label) }

// Sub subscribes to job events for a specific entry ID
func (c *Cron) Sub(id EntryID) *pubsub.Sub[EntryID, JobEvent] {
	return c.ps.Subscribe([]EntryID{id})
}

// JobRunCreatedCh returns channel for job run creation notifications
func (c *Cron) JobRunCreatedCh() <-chan JobRun { return c.jobRunCreatedCh }

// JobRunCompletedCh returns channel for job run completion notifications
func (c *Cron) JobRunCompletedCh() <-chan JobRun { return c.jobRunCompletedCh }

// Enable activates a previously disabled cron entry by its ID
func (c *Cron) Enable(id EntryID) { c.setEntryActive(id, true) }

// Disable deactivates a previously enabled cron entry by its ID
func (c *Cron) Disable(id EntryID) { c.setEntryActive(id, false) }

// Entries returns a snapshot of the cron entries.
func (c *Cron) Entries() []Entry { return c.getEntries() }

// Entry returns a snapshot of the given entry, or nil if it couldn't be found.
func (c *Cron) Entry(id EntryID) (Entry, error) { return c.getEntry(id) }

// Remove an entry from being run in the future.
func (c *Cron) Remove(id EntryID) { c.remove(id) }

// RunNow allows the user to run a specific job now
func (c *Cron) RunNow(id EntryID) error { return c.runNow(id) }

// IsRunning either or not a specific job is currently running
func (c *Cron) IsRunning(id EntryID) bool { return c.entryIsRunning(id) }

// RunningJobs returns a list of all currently running jobs across all entries
func (c *Cron) RunningJobs() []JobRun { return c.runningJobs() }

// RunningJobsFor returns all currently running jobs for a specific entry
// // Returns ErrEntryNotFound if the entry doesn't exist.
func (c *Cron) RunningJobsFor(entryID EntryID) ([]JobRun, error) {
	return c.runningJobsFor(entryID)
}

// CompletedJobRunsFor returns all completed job runs for a specific entry.
// Returns ErrEntryNotFound if the entry doesn't exist.
func (c *Cron) CompletedJobRunsFor(entryID EntryID) ([]JobRun, error) {
	return c.completedJobRunsFor(entryID)
}

// GetJobRun retrieves a specific job run by entry ID and run ID.
// Returns ErrEntryNotFound if the entry doesn't exist or ErrJobRunNotFound if the run doesn't exist.
func (c *Cron) GetJobRun(entryID EntryID, runID RunID) (JobRun, error) {
	return c.getJobRun(entryID, runID)
}

// CancelJobRun attempts to cancel a running job by entry ID and run ID.
// Returns ErrEntryNotFound if the entry doesn't exist or ErrJobRunNotFound if the run doesn't exist.
func (c *Cron) CancelJobRun(entryID EntryID, runID RunID) error { return c.cancelRun(entryID, runID) }

// Location gets the time zone location
func (c *Cron) Location() *time.Location { return c.getLocation() }

// SetLocation sets a new location to use.
// Re-set the "Next" values for all entries.
// Re-sort entries and run due entries.
func (c *Cron) SetLocation(newLoc *time.Location) { c.setLocation(newLoc) }

// GetNextTime returns the next time a job is scheduled to be executed
// If no job is scheduled to be executed, the Zero time is returned
func (c *Cron) GetNextTime() time.Time { return c.getNextTime() }

// GetCleanupTS returns the timestamp of the last completed job runs cleanup
func (c *Cron) GetCleanupTS() time.Time { return c.lastCleanupTS.Get() }

//-----------------------------------------------------------------------------

func startCleanupThread(c *Cron) {
	go func() {
		defer c.logger.Info("cleanup thread stopped")
		for {
			keepCompletedRunsDur := c.keepCompletedRunsDur.Get()
			select {
			case <-c.clock.After(keepCompletedRunsDur):
			case <-c.ctx.Done():
				return
			}
			var totalCleaned atomic.Int32
			start := c.clock.Now()
			thresholdTime := start.Add(-keepCompletedRunsDur)
			workerCount := runtime.NumCPU()
			utils.ParallelForEach(c.ctx, c.runningJobsMap.IterValues(), workerCount, func(v *mtx.RWMtx[jobRunsInner]) {
				v.With(func(inner *jobRunsInner) {
					idx := sort.Search(len(inner.completedTS), func(i int) bool {
						return inner.completedTS[i].After(thresholdTime)
					})
					if idx > 0 {
						for i := 0; i < idx; i++ {
							jobRun := inner.completed[i]
							freeJobRun(inner, jobRun)
						}
						inner.completed = inner.completed[idx:]
						inner.completedTS = inner.completedTS[idx:]
						totalCleaned.Add(int32(idx))
					}
				})
			})
			if count := totalCleaned.Load(); count > 0 {
				c.logger.Debug("cleaned old runs", "count", count, "workers", workerCount, "duration", c.clock.Since(start))
			}
			c.lastCleanupTS.Set(c.clock.Now())
		}
	}()
}

func (c *Cron) start() (started bool) {
	return c.startWith(func() { c.run() }) // sync
}

func (c *Cron) startAsync() (started bool) {
	return c.startWith(func() { go c.run() }) // async
}

func (c *Cron) startWith(runFunc func()) (started bool) {
	if started = c.startRunning(); started {
		runFunc()
	}
	return
}

func (c *Cron) stop() <-chan struct{} {
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

func (c *Cron) wait() {
	<-c.stop()
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

func (c *Cron) onEvt(evt JobEventType, clb HookFn, opts ...HookOption) HookID {
	hook := hookFunc(clb)
	utils.ApplyOptions(&hook, opts...)
	c.hooks.With(func(v *hooksContainer) {
		v.hooksMap[evt] = append(v.hooksMap[evt], hook)
	})
	return hook.id
}

func (c *Cron) onEntryEvt(entryID EntryID, evt JobEventType, clb HookFn, opts ...HookOption) HookID {
	hook := hookFunc(clb)
	utils.ApplyOptions(&hook, opts...)
	c.hooks.With(func(v *hooksContainer) {
		if v.entryHooksMap[entryID] == nil {
			v.entryHooksMap[entryID] = make(map[JobEventType][]hookStruct)
		}
		v.entryHooksMap[entryID][evt] = append(v.entryHooksMap[entryID][evt], hook)
	})
	return hook.id
}

func (c *Cron) removeHook(id HookID) {
	c.hooks.With(func(v *hooksContainer) {
		for evt, hooks := range v.hooksMap {
			if _, idx := utils.FindIdx(hooks, func(h hookStruct) bool { return h.id == id }); idx != -1 {
				v.hooksMap[evt] = slices.Delete(v.hooksMap[evt], idx, idx+1)
				return
			}
		}
		for entryID, eventMap := range v.entryHooksMap {
			for evt, hooks := range eventMap {
				if _, idx := utils.FindIdx(hooks, func(h hookStruct) bool { return h.id == id }); idx != -1 {
					eventMap[evt] = slices.Delete(eventMap[evt], idx, idx+1)
					if len(eventMap[evt]) == 0 {
						delete(eventMap, evt)
					}
					if len(eventMap) == 0 {
						delete(v.entryHooksMap, entryID)
					}
					return
				}
			}
		}
	})
}

func (c *Cron) onJobStart(clb HookFn, opts ...HookOption) HookID {
	return c.onEvt(JobStart, clb, opts...)
}

func (c *Cron) onEntryJobStart(entryID EntryID, clb HookFn, opts ...HookOption) HookID {
	return c.onEntryEvt(entryID, JobStart, clb, opts...)
}

func (c *Cron) onJobCompleted(clb HookFn, opts ...HookOption) HookID {
	return c.onEvt(JobCompleted, clb, opts...)
}

func (c *Cron) onEntryJobCompleted(entryID EntryID, clb HookFn, opts ...HookOption) HookID {
	return c.onEntryEvt(entryID, JobCompleted, clb, opts...)
}

func (c *Cron) runNow(id EntryID) error {
	if err := c.entries.WithE(func(entries *entries) error {
		if entry, ok := (*entries).entriesMap[id]; ok {
			newNext := c.now()
			(*entry).Next = newNext
			_ = entries.heap.Update(id, newNext)
			return nil
		}
		return ErrEntryNotFound
	}); err != nil {
		return err
	}
	c.entriesUpdated() // runNow
	return nil
}

func (c *Cron) addJob(spec string, job IntoJob, opts ...EntryOption) (EntryID, error) {
	return c.addJob1(spec, castIntoJob(job), opts...)
}

func (c *Cron) addJob1(spec string, job Job, opts ...EntryOption) (EntryID, error) {
	schedule, err := c.parser.Parse(spec)
	if err != nil {
		var zeroID EntryID
		return zeroID, err
	}
	return c.schedule(&spec, schedule, job, opts...)
}

func (c *Cron) schedule(spec *string, schedule Schedule, job Job, opts ...EntryOption) (EntryID, error) {
	entry := Entry{
		ID:       c.idFactory.Next(),
		job:      job,
		Spec:     spec,
		Schedule: schedule,
		Next:     schedule.Next(c.now()),
		Active:   true,
	}
	return c.addEntry(entry, opts...)
}

func (c *Cron) addEntry(entry Entry, opts ...EntryOption) (EntryID, error) {
	var zeroID EntryID
	for _, opt := range opts {
		opt(c, &entry)
	}
	if entry.ID == zeroID {
		return zeroID, errors.New("id cannot be empty")
	}
	entry.Next = utils.TernaryOrZero(entry.Active, entry.Next)
	if c.entryExists(entry.ID) {
		return zeroID, ErrIDAlreadyUsed
	}
	c.entries.With(func(entries *entries) {
		entries.entriesMap[entry.ID] = &entry
		entries.heap.Push(&entry)
	})
	c.entriesUpdated() // addEntry
	return entry.ID, nil
}

func (c *Cron) updateLabel(id EntryID, label string) {
	c.entries.With(func(entries *entries) {
		if entry, ok := entries.entriesMap[id]; ok {
			(*entry).Label = label
		}
	})
}

func (c *Cron) setEntryActive(id EntryID, active bool) {
	if err := c.entries.WithE(func(entries *entries) error {
		entry, exists := entries.entriesMap[id]
		if !exists || (*entry).Active == active {
			return errors.New("not found or unchanged")
		}
		newNext := utils.TernaryOrZero(active, (*entry).Schedule.Next(c.now()))
		(*entry).Active = active
		(*entry).Next = newNext
		_ = entries.heap.Update(id, newNext)
		return nil
	}); err != nil {
		return
	}
	c.entriesUpdated() // setEntryActive
}

func (c *Cron) updateScheduleWithSpec(id EntryID, spec string) error {
	schedule, err := c.parser.Parse(spec)
	if err != nil {
		return err
	}
	return c.updateSchedule(id, &spec, schedule)
}

func (c *Cron) updateSchedule(id EntryID, spec *string, schedule Schedule) error {
	if err := c.entries.WithE(func(entries *entries) error {
		entry, exists := entries.entriesMap[id]
		if !exists {
			return ErrEntryNotFound
		}
		newNext := utils.TernaryOrZero((*entry).Active, schedule.Next(c.now()))
		(*entry).Spec = spec
		(*entry).Schedule = schedule
		(*entry).Next = newNext
		_ = entries.heap.Update(id, newNext)
		return nil
	}); err != nil {
		return err
	}
	c.entriesUpdated() // updateSchedule
	return nil
}

func (c *Cron) getNextTime() (out time.Time) {
	c.entries.RWith(func(entries entries) {
		if e := entries.heap.Peek(); e != nil && e.Active {
			out = e.Next
		}
	})
	return
}

func (c *Cron) getNextDelay() (out time.Duration) {
	nextTime := c.getNextTime()
	if nextTime.IsZero() {
		return 100_000 * time.Hour // If there are no entries yet, just sleep - it still handles new entries and stop requests.
	} else {
		return nextTime.Sub(c.now())
	}
}

func (c *Cron) run() {
	for {
		var updated context.CancelFunc
		select {
		case <-c.clock.After(c.getNextDelay()):
		case updated = <-c.update:
		case <-c.ctx.Done():
			return
		}
		c.runDueEntries()
		if updated != nil {
			updated()
		}
	}
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
		c.entries.With(func(entries *entries) {
			for {
				entry := entries.heap.Peek()
				if entry == nil || entry.Next.After(now) || entry.Next.IsZero() || !entry.Active {
					break
				}
				entry = entries.heap.Pop()
				entry.Prev = entry.Next
				entry.Next = entry.Schedule.Next(now)
				entries.heap.Push(entry)
				c.startJob(*entry)
			}
		})
	}
}

func (c *Cron) getLocation() *time.Location {
	return c.location.Load()
}

func (c *Cron) setLocation(newLoc *time.Location) {
	c.location.Store(newLoc)
	c.setEntriesNext()
	c.entriesUpdated() // setLocation
}

// Reset all entries "Next" property.
// This is only done when the cron timezone is changed at runtime.
func (c *Cron) setEntriesNext() {
	now := c.now()
	c.entries.With(func(entries *entries) {
		for _, entry := range entries.heap.entries {
			entry.Next = utils.TernaryOrZero(entry.Active, entry.Schedule.Next(now))
		}
		entries.heap.Init()
	})
}

func (c *Cron) remove(id EntryID) {
	c.removeEntry(id)
	c.entriesUpdated() // remove
}

func (c *Cron) removeEntry(id EntryID) {
	if jobRuns, ok := c.runningJobsMap.Load(id); ok {
		jobRuns.With(func(v *jobRunsInner) {
			for _, j := range v.running {
				j.cancel()
			}
		})
	}
	c.runningJobsMap.Delete(id)
	c.entries.With(func(entries *entries) {
		delete(entries.entriesMap, id)
		entries.heap.Remove(id)
	})
}

func (c *Cron) getEntries() (out []Entry) {
	c.entries.With(func(entries *entries) {
		clone := newEntryHeap()
		l := entries.heap.Len()
		out = make([]Entry, l)
		for i := 0; i < l; i++ {
			entry := entries.heap.Pop()
			out[i] = *entry
			clone.Push(entry)
		}
		(*entries).heap = clone
	})
	return
}

func (c *Cron) getEntry(id EntryID) (Entry, error) {
	var out Entry
	err := c.entries.RWithE(func(entries entries) error {
		if entry, ok := entries.entriesMap[id]; ok {
			out = *entry
			return nil
		}
		return ErrEntryNotFound
	})
	return out, err
}

func (c *Cron) getJobRunClb(entryID EntryID, runID RunID, clb func(*jobRunStruct)) error {
	jobRunsInnerMtx, ok := c.runningJobsMap.Load(entryID)
	if !ok {
		return ErrEntryNotFound
	}
	if err := jobRunsInnerMtx.RWithE(func(jobRunsIn jobRunsInner) error {
		if run, ok := jobRunsIn.mapping[runID]; ok {
			clb(run)
			return nil
		}
		return ErrJobRunNotFound
	}); err != nil {
		return err
	}
	return nil
}

func (c *Cron) getJobRun(entryID EntryID, runID RunID) (JobRun, error) {
	var jobRunPub JobRun
	err := c.getJobRunClb(entryID, runID, func(run *jobRunStruct) {
		jobRunPub = run.export()
	})
	return jobRunPub, err
}

func (c *Cron) cancelRun(entryID EntryID, runID RunID) error {
	return c.getJobRunClb(entryID, runID, func(run *jobRunStruct) {
		(*run).cancel()
	})
}

func (c *Cron) entryIsRunning(entryID EntryID) (isRunning bool) {
	jobRuns, ok := c.runningJobsMap.Load(entryID)
	return ok && jobRunsClb(jobRuns, func(v jobRunsInner) bool { return len(v.running) > 0 })
}

func exportJobRuns(runs []*jobRunStruct) (out []JobRun) {
	for _, j := range runs {
		out = append(out, j.export())
	}
	return
}

func jobRunsClb[T any](jobRuns *mtx.RWMtx[jobRunsInner], clb func(jobRunsInner) T) (out T) {
	jobRuns.RWith(func(v jobRunsInner) { out = clb(v) })
	return
}

func jobRunsExportClb(jobRuns *mtx.RWMtx[jobRunsInner], clb func(jobRunsInner) []*jobRunStruct) []JobRun {
	return exportJobRuns(jobRunsClb(jobRuns, clb))
}

func (c *Cron) jobRunsClb(clb func(jobRunsInner) []*jobRunStruct) (out []JobRun) {
	for jobRuns := range c.runningJobsMap.IterValues() {
		out = append(out, jobRunsExportClb(jobRuns, clb)...)
	}
	sortJobRunsPublic(out)
	return
}

func (c *Cron) jobRunsForClb(entryID EntryID, clb func(jobRunsInner) []*jobRunStruct) (out []JobRun, err error) {
	if jobRuns, ok := c.runningJobsMap.Load(entryID); ok {
		out = jobRunsExportClb(jobRuns, clb)
		return
	}
	return nil, ErrEntryNotFound
}

var getRunningSliceClb = func(v jobRunsInner) []*jobRunStruct { return v.running }
var getCompletedSliceClb = func(v jobRunsInner) []*jobRunStruct { return v.completed }

func (c *Cron) runningJobs() (out []JobRun) {
	return c.jobRunsClb(getRunningSliceClb)
}

func (c *Cron) runningJobsFor(entryID EntryID) (out []JobRun, err error) {
	return c.jobRunsForClb(entryID, getRunningSliceClb)
}

func (c *Cron) completedJobRunsFor(entryID EntryID) (out []JobRun, err error) {
	return c.jobRunsForClb(entryID, getCompletedSliceClb)
}

func sortJobRunsPublic(runs []JobRun) {
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].CreatedAt.Before(runs[j].CreatedAt)
	})
}

func (c *Cron) entryExists(id EntryID) bool {
	return utils.Second(c.getEntry(id)) == nil
}

func (c *Cron) updateJobRuns(entryID EntryID, jobRun *jobRunStruct, isStarting bool) {
	jobRuns, _ := c.runningJobsMap.LoadOrStore(entryID, utils.Ptr(mtx.NewRWMtx(jobRunsInner{mapping: make(map[RunID]*jobRunStruct)})))
	notifyCh := utils.Ternary(isStarting, c.jobRunCreatedCh, c.jobRunCompletedCh)
	utils.NonBlockingSend(notifyCh, jobRun.export())
	jobRuns.With(func(v *jobRunsInner) {
		if isStarting {
			(*v).mapping[jobRun.runID] = jobRun
			(*v).running = append((*v).running, jobRun)
		} else if idx := slices.Index((*v).running, jobRun); idx != -1 {
			(*v).running = slices.Delete((*v).running, idx, idx+1)
			if c.keepCompletedRunsDur.Get() > 0 {
				completedAt := jobRun.inner.Get().completedAt
				(*v).completed = append((*v).completed, jobRun)
				(*v).completedTS = append((*v).completedTS, *completedAt)
			} else {
				freeJobRun(v, jobRun)
			}
		}
	})
}

func freeJobRun(v *jobRunsInner, jobRun *jobRunStruct) {
	delete((*v).mapping, jobRun.runID)
	releaseJobRun(jobRun)
}

// startJob runs the given job in a new goroutine.
func (c *Cron) startJob(entry Entry) {
	jobRun := acquireJobRun(c.ctx, c.clock, entry)
	c.runningJobsCount.Add(1)
	go func() {
		defer func() {
			c.updateJobRuns(entry.ID, jobRun, false)
			c.runningJobsCount.Add(-1)
			c.signalJobCompleted()
		}()
		c.updateJobRuns(entry.ID, jobRun, true)
		c.runWithRecovery(jobRun)
	}()
}

func (c *Cron) runWithRecovery(jobRun *jobRunStruct) {
	entry := jobRun.entry
	runID := jobRun.runID
	clock := c.clock
	logger := c.logger
	start := clock.Now()
	defer func() {
		if r := recover(); r != nil {
			logger.Error("job panic", "label", entry.Label, "entryID", entry.ID, "runID", runID, "error", r, "stack", string(debug.Stack()))
			makeEvent(c, entry, jobRun, JobPanic)
		}
		logger.Info("job completed", "label", entry.Label, "entryID", entry.ID, "runID", runID, "duration", clock.Since(start))
		makeEvent(c, entry, jobRun, JobCompleted)
	}()
	logger.Info("job start", "label", entry.Label, "entryID", entry.ID, "runID", runID)
	makeEvent(c, entry, jobRun, JobStart)
	if err := entry.job.Run(jobRun.ctx, c, jobRun.export()); err != nil {
		logger.Error("job error", "label", entry.Label, "entryID", entry.ID, "runID", runID, "error", err)
		makeEventErr(c, entry, jobRun, JobErr, err)
	}
}

func makeEvent(c *Cron, entry Entry, jobRun *jobRunStruct, typ JobEventType) {
	makeEventErr(c, entry, jobRun, typ, nil)
}

func makeEventErr(c *Cron, entry Entry, jobRun *jobRunStruct, typ JobEventType, err error) {
	clock := c.clock
	now := clock.Now()
	var opt func(*jobRunInner)
	switch typ {
	case JobStart:
		opt = func(inner *jobRunInner) { (*inner).startedAt = utils.Ptr(now) }
	case JobCompleted:
		opt = func(inner *jobRunInner) { (*inner).completedAt = utils.Ptr(now) }
	case JobPanic:
		opt = func(inner *jobRunInner) { (*inner).panic = true }
	case JobErr:
		opt = func(inner *jobRunInner) { (*inner).error = err }
	}
	evt := newJobEvent(typ, clock)
	jobRun.inner.With(func(inner *jobRunInner) {
		utils.ApplyOptions(inner, opt)
		inner.addEvent(evt)
	})
	c.ps.Pub(entry.ID, evt)
	triggerHooks(c, jobRun.ctx, jobRun.export(), typ)
}

func triggerHooks(c *Cron, ctx context.Context, jr JobRun, evtType JobEventType) {
	c.hooks.RWith(func(v hooksContainer) {
		entryID := jr.Entry.ID
		runHook := func(hook hookStruct) {
			fn := func() { hook.fn(ctx, c, hook.id, jr) }
			if hook.runAsync {
				go fn()
			} else {
				fn()
			}
		}
		for _, hook := range v.hooksMap[evtType] {
			runHook(hook)
		}
		for _, hook := range v.entryHooksMap[entryID][evtType] {
			runHook(hook)
		}
	})
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

// now returns current time in c location
func (c *Cron) now() time.Time {
	return c.clock.Now().In(c.getLocation())
}
