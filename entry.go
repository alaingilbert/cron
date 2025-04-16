package cron

import (
	"reflect"
	"time"
)

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
	val := reflect.ValueOf(e.job)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}
	if val.Kind() == reflect.Struct && val.NumField() == 1 {
		field := val.Field(0)
		if field.CanInterface() {
			return field.Interface()
		}
	}
	return e.job
}

type EntryOption func(*Cron, *Entry)

func Label(label string) func(*Cron, *Entry) {
	return func(_ *Cron, entry *Entry) {
		entry.Label = label
	}
}

func WithID(id EntryID) func(*Cron, *Entry) {
	return func(_ *Cron, entry *Entry) {
		entry.ID = id
	}
}

func WithNext(next time.Time) func(*Cron, *Entry) {
	return func(_ *Cron, entry *Entry) {
		entry.Next = next
	}
}

func RunOnStart(c *Cron, entry *Entry) {
	entry.Next = c.now()
}

func Disabled(_ *Cron, entry *Entry) {
	entry.Active = false
}
