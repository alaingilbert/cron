package cron

import "time"

// EntryID ...
type EntryID string

// Entry consists of a schedule and the func to execute on that schedule.
type Entry struct {
	ID EntryID
	// The schedule on which this job should be run.
	Schedule Schedule
	// The next time the job will run. This is the zero time if Cron has not been started or this entry's schedule is unsatisfiable
	Next time.Time
	// The last time this job was run. This is the zero time if the job has never been run.
	Prev time.Time
	// Label to describe the job
	Label string
	// Either or not the job is currently active
	Active bool
	// The Job to run.
	job Job
}

func less(e1, e2 *Entry) bool {
	if e1.Next.IsZero() || !e1.Active {
		return false
	} else if e2.Next.IsZero() || !e2.Active {
		return true
	}
	return e1.Next.Before(e2.Next)
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
	case *Job16Wrapper:
		return j.Job16
	default:
		return e.job
	}
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
