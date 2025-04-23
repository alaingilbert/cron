package cron

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestWithLocation(t *testing.T) {
	c1 := New().Build()
	assert.Equal(t, time.Local, c1.Location())
	c2 := New().WithLocation(time.UTC).Build()
	assert.Equal(t, time.UTC, c2.Location())
}
