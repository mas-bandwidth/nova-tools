package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// TestFleetBeatWritesTheLoadGivenOrMeasured: fleet beat prints the load it
// wrote, given or measured by the machine's meter, and refuses a load that
// is not a percent.
func TestFleetBeatWritesTheLoadGivenOrMeasured(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.live = nil
	out := ta.ok("fleet beat m1 --load 12.5")
	require.Contains(t, out, "FLEET-BEAT OK m1 at=2030-01-02T03:04:05Z load=12.5% last=12.5% how=given", "given")
	// the test meter: a load average of 1 on 4 cores
	out = ta.ok("fleet beat m9")
	require.Contains(t, out, "load=25.0% last=25.0% how=load1", "measured")
	var b beatReport
	ta.json("fleet beat m1 --load 40%", &b)
	require.Equal(t, "m1", b.Member, "--json: %+v", b)
	require.Equal(t, 40.0, b.Load, "--json: %+v", b)
	require.Equal(t, 40.0, b.Last, "--json: %+v", b)
	require.Equal(t, sprint.HowGiven, b.How, "--json: %+v", b)
	code, _, errs := ta.do("fleet beat m1 --load lots")
	require.Equal(t, 2, code, "a load that is not a number: exit %d %q", code, errs)
	require.Contains(t, errs, "--load wants a percent", "a load that is not a number: exit %d %q", code, errs)
	code, _, errs = ta.do("fleet beat m1 --load 5000")
	require.Equal(t, 1, code, "a load out of range: exit %d %q", code, errs)
	require.Contains(t, errs, "a load is a percent", "a load out of range: exit %d %q", code, errs)
	code, _, _ = ta.do("fleet beat")
	require.Equal(t, 2, code, "no member: exit %d, want 2", code)
}

// TestFleetDownHoldsAndFleetUpReleases: the coordinator's fleet down holds a
// beating member down (its row says held); fleet up releases it and it is up
// at once; a member that never beat is added down with no load.
func TestFleetDownHoldsAndFleetUpReleases(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1,m2")
	ta.ok("start")
	ta.ok("fleet down m1")
	ta.ok("tick")
	var w whereView
	ta.json("where", &w)
	row := w.Tables["fleet"]["m1"]
	require.Equal(t, sprint.Held, row["status"], "m1 held: %v", row)
	require.Equal(t, "0.0%", row["load"], "m1 held: %v", row)
	ta.a.sleep(20 * time.Second)
	ta.ok("tick")
	ta.json("where", &w)
	row = w.Tables["fleet"]["m1"]
	require.Equal(t, sprint.Held, row["status"], "m1 held after 20 s of beats: %v", row)
	ta.ok("fleet up m1")
	ta.json("where", &w)
	row = w.Tables["fleet"]["m1"]
	require.Equal(t, sprint.Up, row["status"], "m1 released: %v, want up at once", row)
	ta.ok("fleet up m3")
	ta.json("where", &w)
	row = w.Tables["fleet"]["m3"]
	require.Equal(t, sprint.Down, row["status"], "m3 never beat: %v, want down with no load", row)
	require.Empty(t, row["load"], "m3 never beat: %v, want down with no load", row)
}

// TestFleetHelpSaysHowStatusComesAbout: help fleet lists the beat and says
// what up, down and held are.
func TestFleetHelpSaysHowStatusComesAbout(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	out := ta.ok("help fleet")
	for _, want := range []string{"nova-sprint fleet beat <member> [--load <percent>]", "A beat window is 15s", "missed 3 windows", "status held", "CPU busy percent", "highest of the last 10s"} {
		assert.Contains(t, out, want, "help fleet lacks %q", want)
	}
	assert.Contains(t, ta.ok("help fleet beat"), "-load", "help fleet beat lacks --load")
}
