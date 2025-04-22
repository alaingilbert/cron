package cron

import (
	"bytes"
	"context"
	"github.com/alaingilbert/cron/internal/mtx"
	"github.com/alaingilbert/cron/internal/utils"
	"github.com/jonboulle/clockwork"
	"log/slog"
	"sync"
	"time"
)

// RunID ...
type RunID string

type JobRun struct {
	RunID       RunID
	Entry       Entry
	CreatedAt   time.Time
	StartedAt   *time.Time
	CompletedAt *time.Time
	Events      []JobEvent
	Error       error
	Panic       bool
	Logs        string
	logger      *slog.Logger
}

func (j JobRun) Logger() *slog.Logger {
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
	logger    *slog.Logger
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

func acquireJobRun(ctx context.Context, clock clockwork.Clock, entry Entry, loggerFactory JobRunLoggerFactory) *jobRunStruct {
	jr := jobRunPool.Get().(*jobRunStruct)
	jr.ctx, jr.cancel = context.WithCancel(ctx)
	jr.clock = clock
	jr.entry = entry
	jr.runID = RunID(utils.UuidV4Str())
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
