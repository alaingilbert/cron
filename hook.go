package cron

import (
	"context"

	"github.com/alaingilbert/cron/internal/utils"
)

// HookFn is a function type for cron job hooks.
type HookFn func(context.Context, *Cron, HookID, JobRun)

// HookID is a unique identifier for a cron job hook.
type HookID string

func hookFunc(fn HookFn) hookStruct {
	return hookStruct{
		id:       HookID(utils.UuidV4Str()),
		runAsync: true,
		fn:       fn,
	}
}

type hookStruct struct {
	id       HookID
	runAsync bool
	fn       HookFn
}

// HookOption is a function type for configuring a hook.
type HookOption func(*hookStruct)

// HookSync makes the hook run synchronously.
func HookSync(hook *hookStruct) {
	hook.runAsync = false
}
