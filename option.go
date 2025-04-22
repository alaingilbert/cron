package cron

import (
	"context"
	"log/slog"
	"time"

	"github.com/jonboulle/clockwork"
)

// Config ...
type Config struct {
	Ctx                  context.Context
	Location             *time.Location
	Clock                clockwork.Clock
	Logger               *slog.Logger
	Parser               ScheduleParser
	IDFactory            EntryIDFactory
	JobRunLoggerFactory  JobRunLoggerFactory
	KeepCompletedRunsDur *time.Duration
}

// Option represents a modification to the default behavior of a Cron.
type Option func(*Config)

// WithLocation overrides the timezone of the cron instance.
func WithLocation(loc *time.Location) Option {
	return func(c *Config) {
		c.Location = loc
	}
}

// WithKeepCompletedRunsDur ...
func WithKeepCompletedRunsDur(keepCompletedRunsDur time.Duration) Option {
	return func(c *Config) {
		c.KeepCompletedRunsDur = &keepCompletedRunsDur
	}
}

// WithJobRunLoggerFactory ...
func WithJobRunLoggerFactory(factory JobRunLoggerFactory) Option {
	return func(c *Config) {
		c.JobRunLoggerFactory = factory
	}
}

// WithClock ...
func WithClock(clock clockwork.Clock) Option {
	return func(c *Config) {
		c.Clock = clock
	}
}

// WithContext ...
func WithContext(ctx context.Context) Option {
	return func(c *Config) {
		c.Ctx = ctx
	}
}

// WithLogger ...
func WithLogger(logger *slog.Logger) Option {
	return func(c *Config) {
		c.Logger = logger
	}
}

// WithParser overrides the parser used for interpreting job schedules.
func WithParser(p ScheduleParser) Option {
	return func(c *Config) {
		c.Parser = p
	}
}

// WithIDFactory ...
func WithIDFactory(idFactory EntryIDFactory) Option {
	return func(c *Config) {
		c.IDFactory = idFactory
	}
}
