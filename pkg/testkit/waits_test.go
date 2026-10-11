package testkit_test

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
)

func TestWaitsRecordEveryWaitAndHoldNoneByDefault(t *testing.T) {
	t.Parallel()
	w := testkit.NewWaits()
	assert.True(t, w.Wait(make(chan struct{}), time.Second))
	assert.True(t, w.Wait(make(chan struct{}), time.Minute))
	assert.Equal(t, []time.Duration{time.Second, time.Minute}, w.Asked())
}

func TestAnAlreadyClosedStopEndsAnUnheldWait(t *testing.T) {
	t.Parallel()
	w := testkit.NewWaits()
	stop := make(chan struct{})
	close(stop)
	assert.False(t, w.Wait(stop, time.Second), "an already-closed stop passed an unheld wait")
}

func TestAHeldWaitEndsOnReleaseOrOnStop(t *testing.T) {
	t.Parallel()
	w := testkit.NewWaits().Hold(-1)
	stop := make(chan struct{})
	ended := make(chan bool, 2)
	go func() { ended <- w.Wait(stop, time.Second) }()
	go func() { ended <- w.Wait(make(chan struct{}), time.Second) }()
	w.Holding(2)
	assert.Empty(t, ended, "a held wait ended before the test let it")
	close(stop)
	assert.False(t, <-ended, "a wait its stop ended reported the time passed")
	w.Release()
	assert.True(t, <-ended, "a released wait reported stop")
	assert.True(t, w.Wait(make(chan struct{}), time.Second), "a wait after Release was held")
}

func TestAClosedStopWinsWhenReleaseIsReadyToo(t *testing.T) {
	t.Parallel()
	w := testkit.NewWaits().Hold(-1)
	stop := make(chan struct{})
	close(stop)
	w.Release()
	assert.False(t, w.Wait(stop, time.Second), "a closed stop lost when release was ready too")
}

func TestHoldFirstHoldsOnlyTheFirstWaits(t *testing.T) {
	t.Parallel()
	w := testkit.NewWaits().Hold(1)
	ended := make(chan bool, 1)
	go func() { ended <- w.Wait(make(chan struct{}), time.Second) }()
	w.Holding(1)
	assert.True(t, w.Wait(make(chan struct{}), time.Second), "the second wait, of the same length, was held behind the first")
	assert.Empty(t, ended, "the first wait ended with the second")
	w.Release()
	assert.True(t, <-ended)
	assert.Equal(t, []time.Duration{time.Second, time.Second}, w.Asked())
}
