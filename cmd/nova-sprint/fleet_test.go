package main

import (
	"strings"
	"testing"
	"time"

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
	if want := "FLEET-BEAT OK m1 at=2030-01-02T03:04:05Z load=12.5% last=12.5% how=given"; !strings.Contains(out, want) {
		t.Fatalf("given: %q, want %q", out, want)
	}
	// the test meter: a load average of 1 on 4 cores
	out = ta.ok("fleet beat m9")
	if !strings.Contains(out, "load=25.0% last=25.0% how=load1") {
		t.Fatalf("measured: %q", out)
	}
	var b beatReport
	ta.json("fleet beat m1 --load 40%", &b)
	if b.Member != "m1" || b.Load != 40 || b.Last != 40 || b.How != sprint.HowGiven {
		t.Fatalf("--json: %+v", b)
	}
	if code, _, errs := ta.do("fleet beat m1 --load lots"); code != 2 || !strings.Contains(errs, "--load wants a percent") {
		t.Fatalf("a load that is not a number: exit %d %q", code, errs)
	}
	if code, _, errs := ta.do("fleet beat m1 --load 5000"); code != 1 || !strings.Contains(errs, "a load is a percent") {
		t.Fatalf("a load out of range: exit %d %q", code, errs)
	}
	if code, _, _ := ta.do("fleet beat"); code != 2 {
		t.Fatalf("no member: exit %d, want 2", code)
	}
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
	if row := w.Tables["fleet"]["m1"]; row["status"] != sprint.Held || row["load"] != "0.0%" {
		t.Fatalf("m1 held: %v", row)
	}
	ta.a.sleep(20 * time.Second)
	ta.ok("tick")
	ta.json("where", &w)
	if row := w.Tables["fleet"]["m1"]; row["status"] != sprint.Held {
		t.Fatalf("m1 held after 20 s of beats: %v", row)
	}
	ta.ok("fleet up m1")
	ta.json("where", &w)
	if row := w.Tables["fleet"]["m1"]; row["status"] != sprint.Up {
		t.Fatalf("m1 released: %v, want up at once", row)
	}
	ta.ok("fleet up m3")
	ta.json("where", &w)
	if row := w.Tables["fleet"]["m3"]; row["status"] != sprint.Down || row["load"] != "" {
		t.Fatalf("m3 never beat: %v, want down with no load", row)
	}
}

// TestFleetHelpSaysHowStatusComesAbout: help fleet lists the beat and says
// what up, down and held are.
func TestFleetHelpSaysHowStatusComesAbout(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	out := ta.ok("help fleet")
	for _, want := range []string{"nova-sprint fleet beat <member> [--load <percent>]", "under 15s old", "status held", "CPU busy percent", "highest of the last 10s"} {
		if !strings.Contains(out, want) {
			t.Errorf("help fleet lacks %q:\n%s", want, out)
		}
	}
	if out := ta.ok("help fleet beat"); !strings.Contains(out, "-load") {
		t.Errorf("help fleet beat lacks --load:\n%s", out)
	}
}
