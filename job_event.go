package cron

import (
	"github.com/jonboulle/clockwork"
	"time"
)

type JobEventType int

const (
	Start JobEventType = iota + 1
	Completed
	CompletedNoErr
	CompletedErr
	CompletedPanic
)

func (e JobEventType) String() string {
	switch e {
	case Start:
		return "Start"
	case Completed:
		return "Completed"
	case CompletedNoErr:
		return "CompletedNoErr"
	case CompletedErr:
		return "CompletedErr"
	case CompletedPanic:
		return "CompletedPanic"
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
