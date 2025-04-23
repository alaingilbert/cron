Changes:  
Job runs has a context, so each "run" can be individually cancelled  
Jobs recover panic by default  
Jobs can have a description (label)  
Jobs ID are string and can have user specified ID  
Job run have a dedicated logger that can be use to keep logs for a specific run  
Can add hooks callback to global JobEvent or entry specific events  
Can have a job that only run once using cron.Once  
Can change the *time.Location while running  
Do not sort all entries at every iteration  
Use Heap data structure to store entries  
Can ask the cron object if a job is currently running with `isRunning := c.IsRunning(entryID)`  
Tests runs in under a second (instead of over a minute)  
Comes with a complete admin web interface built-in  

Built in job wrapper:  
```go
func Once(job IntoJob) Job
func N(n int, j IntoJob) Job
func WithRetry(maxRetry int, job IntoJob) Job
func WithTimeout(time.Duration, IntoJob) Job
func WithDeadline(time.Time, IntoJob) Job
func WithJitter(time.Duration, IntoJob) Job
func SkipIfStillRunning(IntoJob) Job
func DelayIfStillRunning(IntoJob) Job
func ThresholdClb(threshold time.Duration, j IntoJob, clb ThresholdCallback) Job
func ThresholdClbAsync(threshold time.Duration, j IntoJob, clb ThresholdCallbackAsync) Job
```

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/alaingilbert/cron"
	"github.com/alaingilbert/cron/webadmin"
	"log/slog"
	"net/http"
	"os"
	"time"
)

type SomeJob struct{}

// Run implements the Job interface for SomeJob
func (s SomeJob) Run(context.Context, *cron.Cron, cron.JobRun) error {
	fmt.Println("Some job")
	return nil
}

func main() {
	appCtx := context.Background()

	l := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	c := cron.New().
		WithSeconds().
		WithContext(appCtx). // Can provide a custom context
		WithLogger(l).       // Can provide a custom logger
		Build()

	// A simple function can be used as a job
	_, _ = c.AddJob("* * * * * *", func() {
		fmt.Println("hello world")
	})

	// If your function returns an error, it will be logged by the cron logger
	_, _ = c.AddJob("* * * * * *", func() error {
		return errors.New("this error will be logged by the cron logger")
	})

	// You can give a description to your jobs using the cron.Label helper
	// The label will be part of the log if the job returns an error
	_, _ = c.AddJob("* * * * * *", func() error {
		return errors.New("this error will be logged by the cron logger")
	}, cron.Label("some description"))

	// A job can also receive a context if you want to handle graceful shutdown
	_, _ = c.AddJob("* * * * * *", func(ctx context.Context) {
		// ...
	})

	// This is also valid
	_, _ = c.AddJob("* * * * * *", func(ctx context.Context) error {
		return nil
	})

	// WithTimeout will ensure that the context get cancelled after the given duration
	_, _ = c.AddJob("*/3 * * * * *", cron.WithTimeout(2*time.Second, func(ctx context.Context) {
		select {
		case <-time.After(time.Minute):
		case <-ctx.Done():
			return
		}
	}))

	// This is also valid
	_, _ = c.AddJob("*/3 * * * * *", cron.WithTimeout(2*time.Second, func(ctx context.Context) error {
		select {
		case <-time.After(time.Minute):
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	}))

	// Anything that implements the Job interface can be used for job
	// Or any of:
	// Run()
	// Run() error
	// Run(context.Context)
	// Run(context.Context) error
	// Run(cron.EntryID)
	// Run(cron.EntryID) error
	// Run(cron.Entry)
	// Run(cron.Entry) error
	// Run(*cron.Cron)
	// Run(*cron.Cron) error
	// Run(cron.JobRun)
	// Run(cron.JobRun) error
	// Run(context.Context, cron.EntryID)
	// Run(context.Context, cron.EntryID) error
	// Run(context.Context, cron.Entry)
	// Run(context.Context, cron.Entry) error
	// Run(context.Context, *cron.Cron)
	// Run(context.Context, *cron.Cron) error
	// Run(context.Context, cron.JobRun)
	// Run(context.Context, cron.JobRun) error
	// Run(*cron.Cron, cron.EntryID)
	// Run(*cron.Cron, cron.EntryID) error
	// Run(*cron.Cron, cron.Entry)
	// Run(*cron.Cron, cron.Entry) error
	// Run(*cron.Cron, cron.JobRun)
	// Run(*cron.Cron, cron.JobRun) error
	// Run(context.Context, *cron.Cron, cron.EntryID)
	// Run(context.Context, *cron.Cron, cron.EntryID) error
	// Run(context.Context, *cron.Cron, cron.Entry)
	// Run(context.Context, *cron.Cron, cron.Entry) error
	// Run(context.Context, *cron.Cron, cron.JobRun)
	// Run(context.Context, *cron.Cron, cron.JobRun) error
	_, _ = c.AddJob("* * * * * *", SomeJob{})

	// When using cron.Once, the job will remove itself from the cron entries
	// after being executed once
	_, _ = c.AddJob("* * * * * *", cron.Once(func() {
		fmt.Println("Will only be executed once")
	}))

	// When using cron.N, the job will remove itself from the cron entries
	// after being executed "n" times
	_, _ = c.AddJob("* * * * * *", cron.N(2, func() {
		fmt.Println("Will be executed 2 times")
	}))

	// cron.SkipIfStillRunning will ensure that the job is skipped if a previous
	// invocation is still running
	_, _ = c.AddJob("* * * * * *", cron.SkipIfStillRunning(func() {
		fmt.Println("Slow job")
		time.Sleep(3 * time.Second)
	}))

	// You can use cron.Chain to chain wrappers to your job
	_, _ = c.AddJob("* * * * * *", cron.Chain(func() {
		fmt.Println("job with chained wrappers")
	}, cron.TimeoutWrapper(time.Second), cron.Once))

	// Jitter adds some random delay before running the job
	_, _ = c.AddJob("0 */5 * * * *", cron.WithJitter(time.Minute, func() {
		fmt.Println("Runs every 5min, but wait 0-1 minute before starting")
	}))

	// This job is disabled by default
	disabledID, _ := c.AddJob("* * * * * *", func() {
		fmt.Println("this job is disabled by default")
	}, cron.Disabled)

	// Job can be enabled/disabled using their ID
	c.Enable(disabledID)
	c.Disable(disabledID)

	// You can specify the ID to use for a job
	customJobIDEntryID, _ := c.AddJob("* * * * * *", func(id cron.EntryID) {
		fmt.Printf("this job has a custom ID: %s\n", id)
	}, cron.WithID("my-job-id"))

	// You can run a job as soon as the cron is started with cron.RunOnStart
	_, _ = c.AddJob("*/10 * * * * *", func() {
		fmt.Println("this job runs as soon as cron is started, then at every 10th seconds", time.Now())
	}, cron.RunOnStart)

	// This is an example of how you can get the time
	// the job was scheduled to run at
	_, _ = c.AddJob("*/5 * * * * *", func(entry cron.Entry) {
		fmt.Println(entry.Prev.UnixNano(), time.Now().UnixNano())
	})

	// A job can use the logger in JobRun to keep logs specific
	// to this run which can be retrieved once completed.
	useLoggerID, _ := c.AddJob("*/5 * * * * *", func(ctx context.Context, jr cron.JobRun) error {
		logger := jr.Logger()
		logger.Debug("debug log")
		logger.Info("info log")
		logger.Warn("warn log")
		logger.Error("error log")
		for i := 0; i < 10; i++ {
			logger.Info("info log", slog.Int("i", i))
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
				logger.Error(ctx.Err().Error())
				return ctx.Err()
			}
		}
		return nil
	}, cron.Label("JobRun logger"), cron.WithID("logger-job"))

	// Hooking
	// By default hooks are running asynchronously.
	// It is possible to add hooks for global job events
	c.OnJobStart(func(ctx context.Context, c *cron.Cron, id cron.HookID, run cron.JobRun) {
		fmt.Println("job started", run.Entry.ID, run.RunID)
	}, cron.HookLabel("global job start hook"))
	// You can also make a hook for a specific entry
	// See also OnJobCompleted / OnEntryJobCompleted
	c.OnEntryJobStart(customJobIDEntryID, func(ctx context.Context, c *cron.Cron, id cron.HookID, run cron.JobRun) {
		fmt.Println("job started", run.Entry.ID, run.RunID)
	})
	c.OnEntryJobCompleted(useLoggerID, func(ctx context.Context, c *cron.Cron, id cron.HookID, run cron.JobRun) {
		fmt.Println("get back job logs", run.Logs)
	}, cron.HookLabel("logger job completed hook"))
	// You can make hook for all JobEventType (JobStart, JobCompleted, JobErr, JobPanic)
	// using OnEvt / OnEntryEvt
	c.OnEvt(cron.JobPanic, func(ctx context.Context, c *cron.Cron, id cron.HookID, run cron.JobRun) {
		fmt.Println("job panic", run.Entry.ID, run.RunID)
	})
	
	c.Start()

	// This library comes with a complete web interface administration tool (optional)
	mux := webadmin.GetMux(c)
	if err := http.ListenAndServe(":8080", mux); err != nil {
		panic(err)
	}
}

```

```go
go test -v ./...
go clean -testcache && go test -race ./...
```