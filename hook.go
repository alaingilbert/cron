package cron

import (
	"context"
)

// HookFn is a function type for cron job hooks.
type HookFn func(context.Context, *Cron, HookID, JobRun)

// HookID is a unique identifier for a cron job hook.
type HookID CronID

func hookFunc(idFactory IDFactory, fn HookFn) *hookStruct {
	return &hookStruct{
		id:       HookID(idFactory.Next()),
		runAsync: true,
		active:   true,
		fn:       fn,
	}
}

type Hook struct {
	ID        HookID
	EntryID   *EntryID
	EventType JobEventType
	Active    bool
	Async     bool
	Label     string
}

type hookStruct struct {
	id       HookID
	runAsync bool
	fn       HookFn
	label    string
	active   bool
}

func (h hookStruct) export(evt JobEventType, entryID *EntryID) Hook {
	return Hook{
		ID:        h.id,
		Active:    h.active,
		Async:     h.runAsync,
		Label:     h.label,
		EventType: evt,
		EntryID:   entryID,
	}
}

// HookOption is a function type for configuring a hook.
type HookOption func(*hookStruct)

// HookSync makes the hook run synchronously.
func HookSync(hook *hookStruct) {
	hook.runAsync = false
}

func HookLabel(label string) HookOption {
	return func(hook *hookStruct) {
		hook.label = label
	}
}

func HookDisable(hook *hookStruct) {
	hook.active = false
}
