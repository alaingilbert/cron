package cron

import (
	"context"
	"errors"
	"github.com/jonboulle/clockwork"
	"sync/atomic"
	"time"

	"github.com/alaingilbert/cron/internal/utils"
)

// Job is an interface for submitted cron jobs.
type Job interface {
	Run(context.Context, *Cron, JobRun) error
}

type Job1 interface{ Run() }
type Job2 interface{ Run(context.Context) }
type Job3 interface{ Run(EntryID) }
type Job4 interface {
	Run(context.Context, EntryID)
}
type Job5 interface{ Run() error }
type Job6 interface{ Run(context.Context) error }
type Job7 interface{ Run(EntryID) error }
type Job8 interface {
	Run(context.Context, EntryID) error
}
type Job9 interface{ Run(*Cron) }
type Job10 interface{ Run(*Cron) error }
type Job11 interface{ Run(context.Context, *Cron) }
type Job12 interface {
	Run(context.Context, *Cron) error
}
type Job13 interface{ Run(*Cron, EntryID) }
type Job14 interface{ Run(*Cron, EntryID) error }
type Job15 interface {
	Run(context.Context, *Cron, EntryID)
}
type Job16 interface {
	Run(context.Context, *Cron, EntryID) error
}
type Job17 interface{ Run(Entry) }
type Job18 interface{ Run(Entry) error }
type Job19 interface{ Run(context.Context, Entry) }
type Job20 interface {
	Run(context.Context, Entry) error
}
type Job21 interface{ Run(*Cron, Entry) }
type Job22 interface{ Run(*Cron, Entry) error }
type Job23 interface {
	Run(context.Context, *Cron, Entry)
}

// FuncJob is a wrapper that turns a func() into a cron.Job
type FuncJob func(context.Context, *Cron, JobRun) error

func (f FuncJob) Run(ctx context.Context, c *Cron, r JobRun) error { return f(ctx, c, r) }

type Job1Wrapper struct{ Job1 }

func (j *Job1Wrapper) Run(context.Context, *Cron, JobRun) error {
	j.Job1.Run()
	return nil
}

type Job2Wrapper struct{ Job2 }

func (j *Job2Wrapper) Run(ctx context.Context, _ *Cron, _ JobRun) error {
	j.Job2.Run(ctx)
	return nil
}

type Job3Wrapper struct{ Job3 }

func (j *Job3Wrapper) Run(_ context.Context, _ *Cron, r JobRun) error {
	j.Job3.Run(r.Entry.ID)
	return nil
}

type Job4Wrapper struct{ Job4 }

func (j *Job4Wrapper) Run(ctx context.Context, _ *Cron, r JobRun) error {
	j.Job4.Run(ctx, r.Entry.ID)
	return nil
}

type Job5Wrapper struct{ Job5 }

func (j *Job5Wrapper) Run(context.Context, *Cron, JobRun) error { return j.Job5.Run() }

type Job6Wrapper struct{ Job6 }

func (j *Job6Wrapper) Run(ctx context.Context, _ *Cron, _ JobRun) error {
	return j.Job6.Run(ctx)
}

type Job7Wrapper struct{ Job7 }

func (j *Job7Wrapper) Run(_ context.Context, _ *Cron, r JobRun) error {
	return j.Job7.Run(r.Entry.ID)
}

type Job8Wrapper struct{ Job8 }

func (j *Job8Wrapper) Run(ctx context.Context, _ *Cron, r JobRun) error {
	return j.Job8.Run(ctx, r.Entry.ID)
}

type Job9Wrapper struct{ Job9 }

func (j *Job9Wrapper) Run(_ context.Context, cron *Cron, _ JobRun) error {
	j.Job9.Run(cron)
	return nil
}

type Job10Wrapper struct{ Job10 }

func (j *Job10Wrapper) Run(_ context.Context, cron *Cron, _ JobRun) error {
	return j.Job10.Run(cron)
}

type Job11Wrapper struct{ Job11 }

func (j *Job11Wrapper) Run(ctx context.Context, cron *Cron, _ JobRun) error {
	j.Job11.Run(ctx, cron)
	return nil
}

type Job12Wrapper struct{ Job12 }

func (j *Job12Wrapper) Run(ctx context.Context, cron *Cron, _ JobRun) error {
	return j.Job12.Run(ctx, cron)
}

type Job13Wrapper struct{ Job13 }

func (j *Job13Wrapper) Run(_ context.Context, cron *Cron, r JobRun) error {
	j.Job13.Run(cron, r.Entry.ID)
	return nil
}

type Job14Wrapper struct{ Job14 }

func (j *Job14Wrapper) Run(_ context.Context, cron *Cron, r JobRun) error {
	return j.Job14.Run(cron, r.Entry.ID)
}

type Job15Wrapper struct{ Job15 }

func (j *Job15Wrapper) Run(ctx context.Context, cron *Cron, r JobRun) error {
	j.Job15.Run(ctx, cron, r.Entry.ID)
	return nil
}

type Job16Wrapper struct{ Job16 }

func (j *Job16Wrapper) Run(ctx context.Context, cron *Cron, r JobRun) error {
	return j.Job16.Run(ctx, cron, r.Entry.ID)
}

type Job17Wrapper struct{ Job17 }

func (j *Job17Wrapper) Run(_ context.Context, _ *Cron, r JobRun) error {
	j.Job17.Run(r.Entry)
	return nil
}

type Job18Wrapper struct{ Job18 }

func (j *Job18Wrapper) Run(_ context.Context, _ *Cron, r JobRun) error {
	return j.Job18.Run(r.Entry)
}

type Job19Wrapper struct{ Job19 }

func (j *Job19Wrapper) Run(ctx context.Context, _ *Cron, r JobRun) error {
	j.Job19.Run(ctx, r.Entry)
	return nil
}

type Job20Wrapper struct{ Job20 }

func (j *Job20Wrapper) Run(ctx context.Context, _ *Cron, r JobRun) error {
	return j.Job20.Run(ctx, r.Entry)
}

type Job21Wrapper struct{ Job21 }

func (j *Job21Wrapper) Run(_ context.Context, c *Cron, r JobRun) error {
	j.Job21.Run(c, r.Entry)
	return nil
}

type Job22Wrapper struct{ Job22 }

func (j *Job22Wrapper) Run(_ context.Context, c *Cron, r JobRun) error {
	return j.Job22.Run(c, r.Entry)
}

type Job23Wrapper struct{ Job23 }

func (j *Job23Wrapper) Run(ctx context.Context, c *Cron, r JobRun) error {
	j.Job23.Run(ctx, c, r.Entry)
	return nil
}

type IntoJob any

// J is a helper to turn a IntoJob into a Job
// Any of these functions, or anything that have a "Run" method
// with one of these signatures can be casted into a Job.
// func()
// func() error
// func(context.Context)
// func(context.Context) error
// func(cron.EntryID)
// func(cron.EntryID) error
// func(cron.Entry)
// func(cron.Entry) error
// func(*cron.Cron)
// func(*cron.Cron) error
// func(context.Context, cron.EntryID)
// func(context.Context, cron.EntryID) error
// func(context.Context, cron.Entry)
// func(context.Context, cron.Entry) error
// func(context.Context, *cron.Cron)
// func(context.Context, *cron.Cron) error
// func(*cron.Cron, cron.EntryID)
// func(*cron.Cron, cron.EntryID) error
// func(*cron.Cron, cron.Entry)
// func(*cron.Cron, cron.Entry) error
// func(context.Context, *cron.Cron, cron.EntryID)
// func(context.Context, *cron.Cron, cron.EntryID) error
// func(context.Context, *cron.Cron, cron.Entry)
// func(context.Context, *cron.Cron, cron.Entry) error
func J(v IntoJob) Job { return castIntoJob(v) }

func castIntoJob(v IntoJob) Job {
	switch j := v.(type) {
	case func():
		return FuncJob(func(context.Context, *Cron, JobRun) error {
			j()
			return nil
		})
	case func() error:
		return FuncJob(func(context.Context, *Cron, JobRun) error {
			return j()
		})
	case func(context.Context):
		return FuncJob(func(ctx context.Context, _ *Cron, _ JobRun) error {
			j(ctx)
			return nil
		})
	case func(context.Context) error:
		return FuncJob(func(ctx context.Context, _ *Cron, _ JobRun) error { return j(ctx) })
	case func(EntryID):
		return FuncJob(func(_ context.Context, _ *Cron, r JobRun) error {
			j(r.Entry.ID)
			return nil
		})
	case func(EntryID) error:
		return FuncJob(func(_ context.Context, _ *Cron, r JobRun) error {
			return j(r.Entry.ID)
		})
	case func(Entry):
		return FuncJob(func(_ context.Context, _ *Cron, r JobRun) error {
			j(r.Entry)
			return nil
		})
	case func(Entry) error:
		return FuncJob(func(_ context.Context, _ *Cron, r JobRun) error {
			return j(r.Entry)
		})
	case func(*Cron):
		return FuncJob(func(_ context.Context, c *Cron, _ JobRun) error {
			j(c)
			return nil
		})
	case func(*Cron) error:
		return FuncJob(func(_ context.Context, c *Cron, _ JobRun) error {
			return j(c)
		})
	case func(context.Context, EntryID):
		return FuncJob(func(ctx context.Context, _ *Cron, r JobRun) error {
			j(ctx, r.Entry.ID)
			return nil
		})
	case func(context.Context, EntryID) error:
		return FuncJob(func(ctx context.Context, _ *Cron, r JobRun) error {
			return j(ctx, r.Entry.ID)
		})
	case func(context.Context, Entry):
		return FuncJob(func(ctx context.Context, _ *Cron, r JobRun) error {
			j(ctx, r.Entry)
			return nil
		})
	case func(context.Context, Entry) error:
		return FuncJob(func(ctx context.Context, _ *Cron, r JobRun) error {
			return j(ctx, r.Entry)
		})
	case func(context.Context, *Cron):
		return FuncJob(func(ctx context.Context, c *Cron, _ JobRun) error {
			j(ctx, c)
			return nil
		})
	case func(context.Context, *Cron) error:
		return FuncJob(func(ctx context.Context, c *Cron, r JobRun) error {
			return j(ctx, c)
		})
	case func(*Cron, EntryID):
		return FuncJob(func(_ context.Context, c *Cron, r JobRun) error {
			j(c, r.Entry.ID)
			return nil
		})
	case func(*Cron, EntryID) error:
		return FuncJob(func(_ context.Context, c *Cron, r JobRun) error {
			return j(c, r.Entry.ID)
		})
	case func(*Cron, Entry):
		return FuncJob(func(_ context.Context, c *Cron, r JobRun) error {
			j(c, r.Entry)
			return nil
		})
	case func(*Cron, Entry) error:
		return FuncJob(func(_ context.Context, c *Cron, r JobRun) error {
			return j(c, r.Entry)
		})
	case func(context.Context, *Cron, Entry):
		return FuncJob(func(ctx context.Context, c *Cron, r JobRun) error {
			j(ctx, c, r.Entry)
			return nil
		})
	case func(context.Context, *Cron, Entry) error:
		return FuncJob(func(ctx context.Context, c *Cron, r JobRun) error {
			return j(ctx, c, r.Entry)
		})
	case func(context.Context, *Cron, EntryID):
		return FuncJob(func(ctx context.Context, c *Cron, r JobRun) error {
			j(ctx, c, r.Entry.ID)
			return nil
		})
	case func(context.Context, *Cron, EntryID) error:
		return FuncJob(func(ctx context.Context, c *Cron, r JobRun) error {
			return j(ctx, c, r.Entry.ID)
		})
	case Job:
		return j
	case Job1:
		return &Job1Wrapper{j}
	case Job2:
		return &Job2Wrapper{j}
	case Job3:
		return &Job3Wrapper{j}
	case Job4:
		return &Job4Wrapper{j}
	case Job5:
		return &Job5Wrapper{j}
	case Job6:
		return &Job6Wrapper{j}
	case Job7:
		return &Job7Wrapper{j}
	case Job8:
		return &Job8Wrapper{j}
	case Job9:
		return &Job9Wrapper{j}
	case Job10:
		return &Job10Wrapper{j}
	case Job11:
		return &Job11Wrapper{j}
	case Job12:
		return &Job12Wrapper{j}
	case Job13:
		return &Job13Wrapper{j}
	case Job14:
		return &Job14Wrapper{j}
	case Job15:
		return &Job15Wrapper{j}
	case Job16:
		return &Job16Wrapper{j}
	case Job17:
		return &Job17Wrapper{j}
	case Job18:
		return &Job18Wrapper{j}
	case Job19:
		return &Job19Wrapper{j}
	case Job20:
		return &Job20Wrapper{j}
	case Job21:
		return &Job21Wrapper{j}
	case Job22:
		return &Job22Wrapper{j}
	case Job23:
		return &Job23Wrapper{j}
	default:
		panic(ErrUnsupportedJobType)
	}
}

type JobWrapper func(IntoJob) Job

func Once(job IntoJob) Job {
	return FuncJob(func(ctx context.Context, c *Cron, r JobRun) error {
		c.Remove(r.Entry.ID)
		return J(job).Run(ctx, c, r)
	})
}

func NWrapper(n int) JobWrapper {
	return func(job IntoJob) Job {
		var count atomic.Int32
		return FuncJob(func(ctx context.Context, c *Cron, r JobRun) error {
			if newCount := count.Add(1); int(newCount) == n {
				c.Remove(r.Entry.ID)
			}
			return J(job).Run(ctx, c, r)
		})
	}
}

// N runs a job "n" times
func N(n int, j IntoJob) Job {
	return NWrapper(n)(j)
}

type ThresholdCallback func(ctx context.Context, c *Cron, r JobRun, threshold, dur time.Duration, err error)
type ThresholdCallback2 func(ctx context.Context, c *Cron, r JobRun, threshold time.Duration)

// ThresholdClb2Wrapper wraps a job and starts a timer for the given threshold.
// If the job runs longer than the threshold, the callback is triggered.
// The callback is invoked asynchronously and does not block job execution.
func ThresholdClb2Wrapper(threshold time.Duration, clb ThresholdCallback2) JobWrapper {
	return func(job IntoJob) Job {
		return FuncJob(func(ctx context.Context, c *Cron, r JobRun) error {
			go func() {
				ctx2, cancel := clockwork.WithTimeout(ctx, c.clock, threshold)
				defer cancel()
				select {
				case <-c.clock.After(threshold):
				case <-ctx2.Done():
					if errors.Is(ctx2.Err(), context.DeadlineExceeded) {
						clb(ctx, c, r, threshold)
					}
				}
			}()
			return J(job).Run(ctx, c, r)
		})
	}
}

// ThresholdClb2 ...
func ThresholdClb2(threshold time.Duration, j IntoJob, clb ThresholdCallback2) Job {
	return ThresholdClb2Wrapper(threshold, clb)(j)
}

func ThresholdClbWrapper(threshold time.Duration, clb ThresholdCallback) JobWrapper {
	return func(job IntoJob) Job {
		return FuncJob(func(ctx context.Context, c *Cron, r JobRun) error {
			start := c.clock.Now()
			err := J(job).Run(ctx, c, r)
			dur := c.clock.Since(start)
			if dur > threshold {
				clb(ctx, c, r, threshold, dur, err)
			}
			return err
		})
	}
}

// ThresholdClb execute a callback if the job runs longer than the specified threshold.
func ThresholdClb(threshold time.Duration, j IntoJob, clb ThresholdCallback) Job {
	return ThresholdClbWrapper(threshold, clb)(j)
}

// SkipIfStillRunning skips an invocation of the Job if a previous invocation is still running.
func SkipIfStillRunning(j IntoJob) Job {
	var running atomic.Bool
	return FuncJob(func(ctx context.Context, c *Cron, r JobRun) (err error) {
		if running.CompareAndSwap(false, true) {
			defer running.Store(false)
			err = J(j).Run(ctx, c, r)
		} else {
			return ErrJobAlreadyRunning
		}
		return
	})
}

// JitterWrapper add some random delay before running the job
func JitterWrapper(duration time.Duration) JobWrapper {
	return func(j IntoJob) Job {
		return FuncJob(func(ctx context.Context, c *Cron, r JobRun) error {
			delay := utils.RandDuration(0, max(duration, 0))
			select {
			case <-c.clock.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
			return J(j).Run(ctx, c, r)
		})
	}
}

// WithJitter add some random delay before running the job
func WithJitter(duration time.Duration, job IntoJob) Job {
	return JitterWrapper(duration)(job)
}

// TimeoutWrapper automatically cancel the job context after a given duration
func TimeoutWrapper(duration time.Duration) JobWrapper {
	return func(j IntoJob) Job {
		return FuncJob(func(ctx context.Context, c *Cron, r JobRun) error {
			timeoutCtx, cancel := clockwork.WithTimeout(ctx, c.clock, duration)
			defer cancel()
			return J(j).Run(timeoutCtx, c, r)
		})
	}
}

// WithTimeout ...
// `_, _ = cron.AddJob("* * * * * *", cron.WithTimeout(time.Second, func(ctx context.Context) { ... }))`
func WithTimeout(d time.Duration, job IntoJob) Job {
	return TimeoutWrapper(d)(job)
}

func DeadlineWrapper(deadline time.Time) JobWrapper {
	return func(j IntoJob) Job {
		return FuncJob(func(ctx context.Context, c *Cron, r JobRun) error {
			deadlineCtx, cancel := clockwork.WithDeadline(ctx, c.clock, deadline)
			defer cancel()
			return J(j).Run(deadlineCtx, c, r)
		})
	}
}

func WithDeadline(deadline time.Time, job IntoJob) Job {
	return DeadlineWrapper(deadline)(job)
}

// Chain `Chain(j, w1, w2, w3)` -> `w3(w2(w1(j)))`
func Chain(j IntoJob, wrappers ...JobWrapper) Job {
	job := J(j)
	for _, w := range wrappers {
		job = w(job)
	}
	return job
}
