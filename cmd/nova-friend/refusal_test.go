package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestASingleProviderRefusalIsNotYetAFault: one lane whose turn the provider
// refused (a content filter) is no fault yet and retries nothing new: the
// second identical refusal is the one that marks the lane faulted.
func TestASingleProviderRefusalIsNotYetAFault(t *testing.T) {
	t.Parallel()

	out := `{"type":"error","error":{"type":"content_filter_error","message":"I'm sorry, but I can't help with that request."}}`
	reason, ok := friend.ProviderRefusal(out)
	require.True(t, ok, "a content filter is a provider refusal")

	w := &friend.LaneFaultWatch{}
	fault, yes := w.Observe(reason)
	assert.False(t, yes, "one provider refusal is not yet a fault")
	assert.Empty(t, fault)
}

// TestASecondIdenticalProviderRefusalFaultsTheLane: the second identical
// refusal on a lane marks the lane faulted with the refusal line, reported as
// lane-fault=<n> refusal="<first line>", and writes nothing more: the denial is
// terminal, never a retry each minute.
func TestASecondIdenticalProviderRefusalFaultsTheLane(t *testing.T) {
	t.Parallel()

	out := `{"type":"error","error":{"type":"content_filter_error","message":"I'm sorry, but I can't help with that request."}}`
	reason, ok := friend.ProviderRefusal(out)
	require.True(t, ok)

	w := &friend.LaneFaultWatch{}
	for i := 0; i < 2; i++ {
		w.Observe(reason)
	}
	require.True(t, w.Faulted(), "the second identical provider refusal faults the lane")
	report := w.Report()
	assert.Equal(t, `lane-fault=2 refusal="content_filter_error: I'm sorry, but I can't help with that request."`, report)

	w.Observe(reason)
	assert.Equal(t, report, w.Report(), "a third identical refusal is no second fault")
}

// TestAPolicyRefusalAlsoFaultsTheLane: a policy refusal (the guard's denial)
// faults the lane the same way, after the second identical one.
func TestAPolicyRefusalAlsoFaultsTheLane(t *testing.T) {
	t.Parallel()

	out := `{"type":"error","error":{"type":"policy_error","message":"the request is not allowed"}}`
	reason, ok := friend.ProviderRefusal(out)
	require.True(t, ok, "a policy refusal is a provider refusal")

	w := &friend.LaneFaultWatch{}
	for i := 0; i < 2; i++ {
		w.Observe(reason)
	}
	require.True(t, w.Faulted())
	assert.Contains(t, w.Report(), "lane-fault=2")
}

// TestTheLaneFaultNoticeNamesTheHoldVerb: the one notice to the coordinator
// carries the fault and the hold verb that returns the friend's cards.
func TestTheLaneFaultNoticeNamesTheHoldVerb(t *testing.T) {
	t.Parallel()

	subject, body := friend.LaneFaultText("bob", friend.DefaultPronouns, `lane-fault=2 refusal="content_filter_error: no"`)
	assert.Contains(t, subject, "bob")
	assert.Contains(t, body, "lane-fault=2")
	assert.Contains(t, body, "nova-sprint hold bob --return")
	assert.Contains(t, body, "nova-friend check --as bob")
}

// TestAFaultedLaneHoldsUntilAClear: the lane that faulted is Faulted (its
// card is held) and the clear releases it for a fresh run.
func TestAFaultedLaneHoldsUntilAClear(t *testing.T) {
	t.Parallel()

	out := `{"type":"error","error":{"type":"content_filter_error","message":"I'm sorry, but I can't help with that request."}}`
	reason, ok := friend.ProviderRefusal(out)
	require.True(t, ok)

	w := &friend.LaneFaultWatch{}
	for i := 0; i < 2; i++ {
		w.Observe(reason)
	}
	require.True(t, w.Faulted(), "a faulted lane holds its card")

	w.Clear()
	assert.False(t, w.Faulted(), "the clear releases the lane")
	assert.Equal(t, "", w.Report(), "the clear empties the report")
	w.Observe(reason)
	assert.False(t, w.Faulted(), "after the clear one refusal is not yet a fault again")
}

// TestATransientProviderErrorIsNotAFault: a rate limit or overload passes by
// itself; it is never a lane fault.
func TestATransientProviderErrorIsNotAFault(t *testing.T) {
	t.Parallel()

	out := `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`
	_, ok := friend.ProviderRefusal(out)
	assert.False(t, ok, "a transient rate limit is never a provider refusal")
}

// TestADifferentRefusalStartsTheCountAgain: a different refusal line starts
// the lane-fault count again, never the second of two different lines.
func TestADifferentRefusalStartsTheCountAgain(t *testing.T) {
	t.Parallel()

	w := &friend.LaneFaultWatch{}
	w.Observe("content_filter_error: one")
	w.Observe("policy_error: two")
	assert.False(t, w.Faulted(), "two different refusals are not alike")

	w.Observe("policy_error: two")
	require.True(t, w.Faulted())
	assert.Contains(t, w.Report(), "policy_error: two")
}

// TestTheLaneFaultFileRoundTripsTheReportAndTheClear: the daemon's fault file
// in the state directory records the report a clear removes.
func TestTheLaneFaultFileRoundTripsTheReportAndTheClear(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	report := `lane-fault=2 refusal="content_filter_error: no"`
	require.NoError(t, friend.WriteLaneFault(dir, report))
	assert.Equal(t, report, friend.ReadLaneFault(dir))

	cleared, err := friend.ClearLaneFault(dir)
	require.NoError(t, err)
	assert.True(t, cleared)
	assert.Equal(t, "", friend.ReadLaneFault(dir))
}
