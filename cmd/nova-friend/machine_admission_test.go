package main

import (
	"context"
	"errors"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestNativeAdmissionReadsServerOverCachedRunningAndPreservesStopOnUnknown(t *testing.T) {
	t.Parallel()
	var words machineWords
	require.True(t, words.publish(words.begin(), &sprintMachine{state: "RUNNING", at: time.Now()}))
	ask := func(context.Context, []string) (string, error) { return `{"machine":"PAUSED"}`, nil }
	assert.False(t, admitMachineLaunch(context.Background(), &words, "bob", func(ctx context.Context, argv []string) (string, error) {
		assert.Equal(t, append(friend.ReadQueueArgv("bob"), "--packets", "0"), argv)
		return ask(ctx, argv)
	}))
	assert.Equal(t, "PAUSED", words.Load().state)
	assert.True(t, admitMachineLaunch(context.Background(), &words, "bob", func(context.Context, []string) (string, error) { return `{"machine":"RUNNING"}`, nil }))
	require.True(t, words.publish(words.begin(), &sprintMachine{state: "STOPPED", at: time.Now()}))
	assert.False(t, admitMachineLaunch(context.Background(), &words, "bob", func(context.Context, []string) (string, error) { return "", errors.New("unavailable") }))
	assert.Equal(t, "STOPPED", words.Load().state)
	assert.True(t, words.Load().at.IsZero(), "unknown authorizes no launches while STOP cancellation stays latched")
}
func TestDelayedRunningResponseCannotEraseLaterStopOrOpenNativeGate(t *testing.T) {
	t.Parallel()
	var words machineWords
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan bool, 1)
	go func() {
		done <- admitMachineLaunch(context.Background(), &words, "bob", func(context.Context, []string) (string, error) {
			close(entered)
			<-release
			return `{"machine":"RUNNING"}`, nil
		})
	}()
	<-entered
	require.True(t, words.publish(words.begin(), &sprintMachine{state: "STOPPED", at: time.Now()}))
	close(release)
	assert.False(t, <-done)
	assert.Equal(t, "STOPPED", words.Load().state)
	old := words.begin()
	latest := words.begin()
	require.True(t, words.publish(latest, &sprintMachine{state: "PAUSED", at: time.Now()}))
	assert.False(t, words.publish(old, nil))
	assert.Equal(t, "PAUSED", words.Load().state)
	assert.False(t, words.Load().at.IsZero(), "stale failure cannot invalidate a newer observation")
	require.True(t, words.publish(words.begin(), &sprintMachine{state: "STOPPED", at: time.Now()}))
	old = words.begin()
	latest = words.begin()
	assert.False(t, words.publish(latest, nil))
	assert.False(t, words.publish(old, &sprintMachine{state: "RUNNING", at: time.Now()}), "a newer unknown completion supersedes an older pending RUNNING")
	assert.Equal(t, "STOPPED", words.Load().state)
	assert.True(t, words.Load().at.IsZero(), "unknown holds admission and retains observed STOP")
}

func TestOverlappingRunningAdmissionsRetryWithoutControlTransition(t *testing.T) {
	t.Parallel()
	var words machineWords
	entered, release, first := make(chan struct{}), make(chan struct{}), make(chan bool, 1)
	go func() {
		first <- admitMachineLaunch(context.Background(), &words, "bob", func(context.Context, []string) (string, error) {
			close(entered)
			<-release
			return `{"machine":"RUNNING"}`, nil
		})
	}()
	<-entered
	running := func(context.Context, []string) (string, error) { return `{"machine":"RUNNING"}`, nil }
	require.True(t, admitMachineLaunch(context.Background(), &words, "bob", running))
	close(release)
	assert.False(t, <-first, "superseded response holds its reservation")
	assert.True(t, admitMachineLaunch(context.Background(), &words, "bob", running), "same held launch retries successfully without unpause")
	assert.Equal(t, "RUNNING", words.Load().state)
}

func TestNativeAdmissionsStayLiveWhileLaterBeatsArePending(t *testing.T) {
	t.Parallel()
	var words machineWords
	for range 20 {
		entered, release, admitted := make(chan struct{}), make(chan struct{}), make(chan bool, 1)
		go func() {
			admitted <- admitMachineLaunch(context.Background(), &words, "bob", func(context.Context, []string) (string, error) {
				close(entered)
				<-release
				return `{"machine":"RUNNING"}`, nil
			})
		}()
		<-entered
		beat := words.begin() // The later heartbeat is pending when the native query completes.
		close(release)
		require.True(t, <-admitted, "ongoing pending beats cannot starve RUNNING admissions")
		assert.Equal(t, "RUNNING", words.Load().state)
		assert.False(t, words.Load().at.IsZero())
		require.True(t, words.publish(beat, &sprintMachine{state: "RUNNING", at: time.Now()}))
	}
}
