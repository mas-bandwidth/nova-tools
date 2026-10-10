package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDeliver is a harness's deliver command in a unit test: it keeps every turn handed to it and
// answers what the test set.
type fakeDeliver struct {
	texts []string
	err   error
	exit  int
}

func (f *fakeDeliver) Deliver(_ context.Context, text string) (int, error) {
	f.texts = append(f.texts, text)
	return f.exit, f.err
}

// TestTheContractGoesInThroughTheHarnessDeliverCommand pins the session contract's channel: the
// contract's push and a deferred check are one turn of the harness's own deliver command, the
// same path a card's deal takes, never a message on her bus stream (the finding of 2026-10-07:
// the daemon wrote "the session runs no monitor" only into its own log, and the session was
// never told).
func TestTheContractGoesInThroughTheHarnessDeliverCommand(t *testing.T) {
	t.Parallel()
	d := &fakeDeliver{}
	push := deliverPush(d)
	require.NotNil(t, push)
	require.NoError(t, push(context.Background(), friend.ContractTitle, friend.ContractTitle+" (nova-friend, bob): what the machine expects of this session"))
	require.Len(t, d.texts, 1)
	assert.True(t, strings.HasPrefix(d.texts[0], friend.ContractTitle), d.texts[0])

	// a subject the body does not open with is said first, so a deferred check's turn names the
	// check it answers before the contract it carries
	require.NoError(t, push(context.Background(), friend.SessionCheckPrefix+"n1", "answer n1: nova-friend pong --as bob --nonce n1\n"))
	require.Len(t, d.texts, 2)
	assert.True(t, strings.HasPrefix(d.texts[1], friend.SessionCheckPrefix+"n1\n\nanswer n1:"), d.texts[1])

	// a deliver that defers is returned, so the record says the push did not go in
	err := deliverPush(&fakeDeliver{err: friend.Deferred{Reason: "no monitor"}})(context.Background(), friend.ContractTitle, friend.ContractTitle+" x")
	var deferred friend.Deferred
	require.ErrorAs(t, err, &deferred)

	// a non-zero exit is a failed push, never a silent one
	require.ErrorContains(t, deliverPush(&fakeDeliver{exit: 1})(context.Background(), friend.ContractTitle, friend.ContractTitle+" x"), "exited 1")

	// no deliver command is no push path: the checks still carry the contract
	assert.Nil(t, deliverPush(nil))
}

// TestTheContractChannelIsTheMonitorAGrokSessionActuallyRuns pins grok's channel: the contract
// goes to the file a monitor tails (Wake ""), because a wake path that changed leaves the
// session's monitor on the old file until the contract tells it the new one, and a push to the
// configured path alone would defer — the exact session that was never told. Every other harness
// keeps its own deliver command.
func TestTheContractChannelIsTheMonitorAGrokSessionActuallyRuns(t *testing.T) {
	t.Parallel()
	inner := &fakeDeliver{}
	ch := contractChannel("grok", "", "/w/bob", inner, nil, nil)
	g, ok := ch.(*friend.Grok)
	require.True(t, ok, "grok's channel is the grok deliver command")
	assert.Equal(t, "/w/bob", g.Dir)
	assert.Empty(t, g.Wake, "the deliver goes to the file a monitor tails, not the configured path")

	for _, harness := range []string{"opencode", "codex", "claude", "antigravity", "dsh", "gemini", "tmux"} {
		assert.Equal(t, inner, contractChannel(harness, "ses_named", "/w/bob", inner, nil, nil), harness)
	}
}

// TestALaneHarnessWithNoSessionNamedPushesNothing pins the one session a laned harness has: with
// no session named the adapter would pick the newest, a lane's, so the contract rides the check
// (nil) rather than opening a lane with it. Every other harness, and a named session, keeps the
// deliver command.
func TestALaneHarnessWithNoSessionNamedPushesNothing(t *testing.T) {
	t.Parallel()
	lanes := &fakeLaneDeliver{fakeDeliver: fakeDeliver{}}
	inner := &fakeDeliver{}
	assert.Nil(t, contractChannel("opencode", "", "/w/bob", lanes, nil, nil), "no named session: the check carries the contract")
	assert.Equal(t, lanes, contractChannel("opencode", "ses_main", "/w/bob", lanes, nil, nil), "a named session is the channel")
	assert.Equal(t, inner, contractChannel("codex", "", "/w/bob", inner, nil, nil), "a harness with one session keeps its deliver command")
}

// fakeLaneDeliver is a harness that opens a session per lane (the opencode adapter's shape).
type fakeLaneDeliver struct {
	fakeDeliver
}

func (*fakeLaneDeliver) OpenSession(context.Context, string) (string, error) { return "ses_lane", nil }
func (*fakeLaneDeliver) DeliverTo(context.Context, string, string) (friend.LaneTurn, error) {
	return friend.LaneTurn{}, nil
}
