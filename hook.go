package cron

import (
	"context"

	"github.com/alaingilbert/cron/internal/utils"
)

type HookFn func(context.Context, *Cron, HookID, JobRun)

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

type HookOption func(*hookStruct)

// HookSync makes the hook run synchronously.
func HookSync(hook *hookStruct) {
	hook.runAsync = false
}
