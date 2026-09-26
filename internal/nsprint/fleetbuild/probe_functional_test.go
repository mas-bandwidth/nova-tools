//go:build functional

package fleetbuild

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
)

// TestSprintClearThenFleetRollLeavesTheConsumerTableAtZero is the
// nova-tools#4237 DONE-WHEN, against the real library: `sprint clear`,
// then a fleet roll, then a table tick: every consumer row reads ready 0,
// working 0, done 0, ok% -, and the probe's result is on bench:<b>:beat
// probe.
func TestSprintClearThenFleetRollLeavesTheConsumerTableAtZero(t *testing.T) {
	t.Parallel()

	_, c := wstest.Start(t)
	ctx := context.Background()
	c.SAdd(ctx, BenchesKey, "hulk", "batman", "space")
	c.HSet(ctx, ConfigKey, "version", testV, "commit", testC, "builder", "space", "self", "studio",
		"platform:hulk", "linux-amd64", "platform:batman", "darwin-amd64", "platform:space", "linux-amd64",
		"platform:studio", "darwin-arm64")
	now := strconv.FormatInt(time.Now().UnixMilli(), 10)
	for _, b := range []string{"hulk", "batman", "space", "studio"} {
		c.HSet(ctx, BeatKey(b), "at", now, "load1", "0.5")
		c.Expire(ctx, BeatKey(b), time.Minute)
	}

	// consumer work the clear zeroes: a copy on hulk, given back (fail)
	hulk := taskcard.Consumer{Kind: "bench", Name: "hulk"}
	c.HSet(ctx, hulk.DesiredKey(), "slots", "2")
	if err := taskcard.Enroll(ctx, c, hulk, true); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "work-1", Where: "waiting", Stream: "swarm: cards",
		Sprint: "probe-4237", Kind: "build", Title: "work", Repo: "mas-bandwidth/nova-tools", By: "rowan",
		Fields: []string{"base", "dev", "base_sha", testC, "paths", "x.go", "done_when", "x"}}); err != nil {
		t.Fatal(err)
	}
	dealt, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: hulk, N: 1, By: "rowan"})
	if err != nil || len(dealt) != 1 {
		t.Fatalf("deal %v %v", dealt, err)
	}
	if _, err := taskcard.CancelCards(ctx, c, "rowan", "given back", dealt[0].Copy); err != nil {
		t.Fatal(err)
	}
	if n := c.ZCard(ctx, hulk.Key("fail")).Val(); n != 1 {
		t.Fatalf("%s holds %d before the clear, want 1", hulk.Key("fail"), n)
	}

	// sprint clear
	if reply, err := c.FCall(ctx, "ns_sprint_clear", nil, "rowan", "4237 done-when", "force").StringSlice(); err != nil || reply[0] != "CLEARED" {
		t.Fatalf("sprint clear: %v %v", reply, err)
	}
	// a fleet roll
	f := &fakeBench{t: t, home: t.TempDir()}
	if r, out := deploy(t, c, f); !r.OK() {
		t.Fatalf("fleet roll: %+v\n%s", r, out)
	}
	// a table tick
	snap, err := table.NewSprintReader(c, table.SprintConfig{}).Read(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Consumers) == 0 {
		t.Fatal("no consumer rows")
	}
	for _, r := range snap.Consumers {
		if r.Ready != 0 || r.Working != 0 || r.Done() != 0 || r.Unread != [4]bool{} {
			t.Errorf("%s reads %d | %d | %d after clear and roll, want 0 | 0 | 0", r.ID(), r.Ready, r.Working, r.Done())
		}
	}
	const at = "2026-09-25T13:00:00Z"
	for _, b := range []string{"hulk", "batman", "space", "studio"} {
		if got, want := c.HGet(ctx, BeatKey(b), "probe").Val(), "OK "+testV+" "+at; got != want {
			t.Errorf("%s probe = %q, want %q", BeatKey(b), got, want)
		}
	}
}
