package cron

import (
	"github.com/jonboulle/clockwork"
	"time"
)

type JobEventType int

const (
	JobStart JobEventType = iota + 1
	JobCompleted
	JobErr
	JobPanic
)

func (e JobEventType) String() string {
	switch e {
	case JobStart:
		return "JobStart"
	case JobCompleted:
		return "JobCompleted"
	case JobErr:
		return "JobErr"
	case JobPanic:
		return "JobPanic"
	default:
		return "Unknown"
	}
}

type JobEvent struct {
	Typ       JobEventType
	CreatedAt time.Time
}

func newJobEvent(typ JobEventType, clock clockwork.Clock) JobEvent {
	return JobEvent{
		Typ:       typ,
		CreatedAt: clock.Now(),
	}
}
