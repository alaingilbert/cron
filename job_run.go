package cron

import (
	"context"
	"github.com/alaingilbert/cron/internal/mtx"
	"github.com/alaingilbert/cron/internal/utils"
	"github.com/jonboulle/clockwork"
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
}

type jobRunStruct struct {
	runID     RunID
	entry     Entry
	clock     clockwork.Clock
	inner     mtx.RWMtx[jobRunInner]
	createdAt time.Time
	ctx       context.Context
	cancel    context.CancelFunc
}

type jobRunInner struct {
	startedAt   *time.Time
	completedAt *time.Time
	events      []JobEvent
	error       error
	panic       bool
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
		Events:      innerCopy.events,
		StartedAt:   innerCopy.startedAt,
		CompletedAt: innerCopy.completedAt,
		Error:       innerCopy.error,
		Panic:       innerCopy.panic,
	}
}

func newJobRun(ctx context.Context, clock clockwork.Clock, entry Entry) *jobRunStruct {
	ctx, cancel := context.WithCancel(ctx)
	return &jobRunStruct{
		runID:     RunID(utils.UuidV4Str()),
		entry:     entry,
		clock:     clock,
		createdAt: clock.Now(),
		ctx:       ctx,
		cancel:    cancel,
	}
}
