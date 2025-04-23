package cron

import (
	"context"
)

// HookFn is a function type for cron job hooks.
type HookFn func(context.Context, *Cron, HookID, JobRun)

// HookID is a unique identifier for a cron job hook.
type HookID ID

func hookFunc(idFactory IDFactory, fn HookFn) *hookStruct {
	return &hookStruct{
		id:       HookID(idFactory.Next()),
		runAsync: true,
		active:   true,
		fn:       fn,
	}
}

// Hook represents a configured cron job hook with its properties.
type Hook struct {
	ID        HookID       // Unique identifier for the hook
	EntryID   *EntryID     // Associated job entry ID (nil if global)
	EventType JobEventType // Type of job event that triggers this hook
	Active    bool         // Whether the hook is currently active
	Async     bool         // Whether the hook runs asynchronously
	Label     string       // Human-readable label for the hook
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

// HookLabel returns a HookOption that sets the label for a hook.
func HookLabel(label string) HookOption {
	return func(hook *hookStruct) {
		hook.label = label
	}
}

// HookDisable disables a hook by setting its active status to false.
func HookDisable(hook *hookStruct) {
	hook.active = false
}
