package main

import (
	"encoding/json"
	"errors"
	"context"
	"io"
	"time"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/require"
)

// reachWorld keeps the tool clock and fake bus clock together. The common rig
// deliberately advances now on reads for daemon tests; reach needs a pure
// injected clock so a one-second rung is not spent by observation.
func reachWorld(r *rig) world {
	w := r.world()
	w.now = func() time.Time { return r.now }
	w.sleep = func(_ context.Context, d time.Duration) { r.now = r.now.Add(d); r.store.Advance(d) }
	return w
}

func TestReachDryRunPrintsTheLadder(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	r.cli().Do(t, "reach", "--as", "ada", "bob", "--dry-run").Exit(0).
		Out("REACH PLAN step=bus friend=bob", "REACH PLAN step=push friend=bob", "REACH PLAN step=window friend=bob", "REACH OK friend=bob from=bus dry_run=true")
}

func TestReachClimbsTheLadderOnlyUntilProof(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w := reachWorld(r); calls := 0
	w.window = func(_ context.Context, _ friend.Exec, _, _, _, _ string) error { calls++; r.answer("r4nd0m"); return nil }
	testkit.Main(func(a []string, in io.Reader, out, err io.Writer) int { return run(a, in, out, err, w) }).Do(t, "reach", "--as", "ada", "bob", "--step-timeout", "1s").Exit(0).Out("REACH OK friend=bob step=window")
	assert.Equal(t, 1, calls)
}

func TestReachWindowPermissionRefusal(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob"); w := reachWorld(r)
	w.window = func(context.Context, friend.Exec, string, string, string, string) error { return errors.New("Accessibility permission is not granted") }
	testkit.Main(func(a []string, in io.Reader, out, err io.Writer) int { return run(a, in, out, err, w) }).Do(t, "reach", "--as", "ada", "bob", "--from", "window").Exit(2).Err("Accessibility permission is not granted")
}

func TestReachFailedSaysSoOnceOnTheBus(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	state := friend.DefaultStateDir(r.home, "bob")
	require.NoError(t, friend.WriteStatus(state, friend.Status{Friend: "bob", At: r.now}))
	require.NoError(t, friend.WritePresence(state, friend.PresenceStatus{Friend: "bob", Presence: friend.PresenceUp, At: r.now}))
	w := reachWorld(r)
	w.window = func(context.Context, friend.Exec, string, string, string, string) error { return nil }
	cli := testkit.Main(func(a []string, in io.Reader, out, err io.Writer) int { return run(a, in, out, err, w) })
	cli.Do(t, "reach", "--as", "ada", "bob", "--step-timeout", "1s").Exit(1).Out("REACH STEP step=bus", "REACH STEP step=push", "REACH STEP step=window", "REACH FAILED friend=bob tried=bus,push,window")
	entries, err := r.store.Range(context.Background(), bus.StreamOf("ada"), "-", "+", 20)
	require.NoError(t, err)
	failed := 0
	for _, entry := range entries {
		m := entry.Message()
		if m.Subject == "REACH FAILED" {
			failed++
			assert.Equal(t, "ada", m.From)
			assert.Equal(t, "REACH FAILED friend=bob tried=bus,push,window", m.Body)
		}
		assert.NotEqual(t, "bob", m.From, "target never forges the coordinator failure notice")
	}
	assert.Equal(t, 1, failed)
}

func TestReachProofRejectsControlAndWrongNonceMessages(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{name, subject, body string}{
		{"wrong nonce pong", "work", friend.PongLine("wrong", 0, 0, 0)},
		{"daemon pong", friend.DaemonPongSubject, "daemon-pong r4nd0m"},
		{"keepalive", "keepalive", "still here"},
	} { t.Run(tc.name, func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "ada", "bob"); w := reachWorld(r); calls := 0
		w.window = func(context.Context, friend.Exec, string, string, string, string) error { calls++; return errors.New("window reached") }
		orig := w.sleep; w.sleep = func(ctx context.Context, d time.Duration) { _, _ = (&bus.Bus{Store:r.store}).Send(ctx, bus.Message{From:"bob", To:[]string{"ada"}, Subject:tc.subject, Body:tc.body}); orig(ctx,d) }
		testkit.Main(func(a []string,in io.Reader,out,err io.Writer) int{return run(a,in,out,err,w)}).Do(t,"reach","--as","ada","bob","--step-timeout","1s").Exit(2)
		assert.Equal(t, 1, calls)
	}) }
}

func TestReachFromStartsAtTheRequestedSuffix(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	out := r.cli().Do(t, "reach", "--as", "ada", "--friend", "bob", "--from", "push", "--dry-run").Exit(0).Stdout
	assert.NotContains(t, out, "step=bus")
	assert.Contains(t, out, "step=push")
	assert.Contains(t, out, "step=window")
}

func TestReachJSONIsOneObject(t *testing.T) {
	t.Parallel()
	out := newRig(t, "ada", "bob").cli().Do(t, "reach", "--as", "ada", "bob", "--dry-run", "--json").Exit(0).Stdout
	var got map[string]any
	assert.NoError(t, json.Unmarshal([]byte(out), &got))
}

func TestReachSkipsThePushWhenTheDaemonIsDown(t *testing.T) {
	t.Parallel()
	// Starting at push reads the target's absent daemon record and records a skip;
	// the window then refuses rather than claiming a daemon delivery happened.
	got := newRig(t, "ada", "bob").cli().Do(t, "reach", "--as", "ada", "bob", "--from", "push", "--step-timeout", "1s").Exit(2)
	got.Err("window reach")
}

func TestReachWindowRefusesWithoutAccessibilityPermission(t *testing.T) {
	t.Parallel()
	newRig(t, "ada", "bob").cli().Do(t, "reach", "--as", "ada", "bob", "--from", "window", "--step-timeout", "1s").Exit(2).Err("window reach")
}
