package cron

import (
	"bytes"
	"context"
	"sync"
	"time"

	"github.com/alaingilbert/cron/internal/mtx"
	"github.com/jonboulle/clockwork"
)

// RunID represents a unique identifier for a job run.
type RunID ID

// JobRun represents a single execution of a scheduled job.
type JobRun struct {
	RunID       RunID      // Unique identifier for this job run
	Entry       Entry      // Job definition being executed
	CreatedAt   time.Time  // When the run was created
	StartedAt   *time.Time // When execution began (nil if not started)
	CompletedAt *time.Time // When execution finished (nil if not completed)
	Events      []JobEvent // Timeline of events during execution
	Error       error      // Final error state if any
	Panic       bool       // Whether job panicked during execution
	Logs        string     // Captured log output
	logger      Logger     // Internal logger instance
}

// Logger returns the structured logger for this job run
func (j JobRun) Logger() Logger {
	return j.logger
}

type jobRunStruct struct {
	runID     RunID
	entry     Entry
	clock     clockwork.Clock
	inner     mtx.RWMtx[jobRunInner]
	createdAt time.Time
	ctx       context.Context
	cancel    context.CancelFunc
	logsBuf   bytes.Buffer
	logger    Logger
}

type jobRunInner struct {
	startedAt   *time.Time
	completedAt *time.Time
	events      []JobEvent
	error       error
	panic       bool
}

var jobRunPool = sync.Pool{
	New: func() any {
		return &jobRunStruct{
			inner: mtx.NewRWMtx(jobRunInner{
				events: make([]JobEvent, 0, 3), // Pre-allocate event slice
			}),
		}
	},
}

func acquireJobRun(ctx context.Context, clock clockwork.Clock, entry Entry, idFactory IDFactory, loggerFactory JobRunLoggerFactory) *jobRunStruct {
	jr := jobRunPool.Get().(*jobRunStruct)
	jr.ctx, jr.cancel = context.WithCancel(ctx)
	jr.clock = clock
	jr.entry = entry
	jr.runID = RunID(idFactory.Next())
	jr.createdAt = clock.Now()
	jr.logsBuf = bytes.Buffer{}
	jr.logger = loggerFactory.New(&jr.logsBuf)
	jr.inner.With(func(inner *jobRunInner) {
		inner.events = inner.events[:0] // Reset slice
		inner.startedAt = nil
		inner.completedAt = nil
		inner.error = nil
		inner.panic = false
	})
	return jr
}

func releaseJobRun(jr *jobRunStruct) {
	jr.cancel() // Ensure context is cleaned up
	jr.logsBuf = bytes.Buffer{}
	jobRunPool.Put(jr)
}

func (j *jobRunInner) addEvent(evt JobEvent) {
	j.events = append(j.events, evt)
}

func (j *jobRunStruct) export() JobRun {
	innerCopy := j.inner.Get()
	return JobRun{
		RunID:       j.runID,
		Entry:       j.entry,
		CreatedAt:   j.createdAt,
		Logs:        j.logsBuf.String(),
		logger:      j.logger,
		Events:      innerCopy.events,
		StartedAt:   innerCopy.startedAt,
		CompletedAt: innerCopy.completedAt,
		Error:       innerCopy.error,
		Panic:       innerCopy.panic,
	}
}
