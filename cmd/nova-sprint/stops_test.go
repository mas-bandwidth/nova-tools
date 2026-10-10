package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// No silent stops (internal/sprint/stops.go; the owner, 2026-10-10: "sprint doctor can check
// this, but i still dislike these silent stops/failures"): the doctor lists every automatic
// stop with its age, where --json carries what waits on the seat for the dashboard, the push
// loop tells the seat of a late tick, and a promotion ejected from the merge queue is told.

func TestTheDoctorListsEveryAutomaticStopWithItsAgeAndUndo(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1,m2,m3") // m3 never beats (ta.live is m1, m2)
	ta.ok("add --stream s1 --count 2")
	ta.ok("start")
	ta.ok("tick")
	ta.a.sleep(3 * time.Minute)
	ta.ok("tick")

	code, out, errs := ta.do("doctor")
	require.Equal(t, 1, code, "a stop holds: the doctor is RED\n%s%s", out, errs)
	assert.Contains(t, out, "DOCTOR routes GREEN: no automatic stop holds\n")
	assert.Contains(t, out, "DOCTOR machines RED: 1 automatic stops hold\n")
	assert.Contains(t, out, "  STOP member-down m3 age=")
	assert.Contains(t, out, "member m3 is down and the coordinator did not hold it: it has never beaten")
	assert.Contains(t, out, "undo: start its member loop")
	assert.Contains(t, out, "DOCTOR seat ")
	assert.Contains(t, out, "DOCTOR RED stops=1 ")

	// the judgment the pass raised for it is the push; where --json carries the seat's waits
	var w struct {
		SeatWaits *struct {
			Judgments        int   `json:"judgments"`
			OldestAgeSeconds int64 `json:"oldest_age_seconds"`
		} `json:"seat_waits"`
		Stops []sprint.Stop `json:"stops"`
	}
	ta.ok("tick")
	ta.json("where", &w)
	require.NotNil(t, w.SeatWaits, "where --json carries what waits on the seat")
	assert.GreaterOrEqual(t, w.SeatWaits.Judgments, 1, "the member-down judgment waits on the seat")
	require.NotEmpty(t, w.Stops)
	assert.Equal(t, sprint.StopKindMemberDown, w.Stops[0].Kind)

	// held by the coordinator, it is hers and no stop: the doctor is green
	ta.ok("fleet down m3")
	code, out, _ = ta.do("doctor --json")
	assert.Contains(t, out, `"stops":[]`, "a member the coordinator holds is no automatic stop: %s", out)
	assert.Contains(t, []int{0, 1}, code)
}

func TestThePushLoopTellsTheSeatOfALateTickEveryTenMinutes(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 10, 10, 4, 0, 0, 0, time.UTC)
	var w lateWatch
	assert.Empty(t, w.step("machine: running", t0), "on time: nothing")
	assert.Empty(t, w.step("machine: running (tick late 16s)", t0), "under the bound: nothing")
	text := w.step("machine: running (tick late 45s)", t0.Add(time.Minute))
	assert.Contains(t, text, "MACHINE TICK LATE: the machine's tick runs 45s late (the worst 45s, 1 looks late")
	assert.Empty(t, w.step("machine: running (tick late 90s)", t0.Add(2*time.Minute)), "pushed once a window")
	text = w.step("machine: running (tick late 31s)", t0.Add(12*time.Minute))
	assert.Contains(t, text, "runs 31s late (the worst 1m30s, 2 looks late", "again after ten minutes, with the worst since the last push")
	assert.Empty(t, w.step("machine: running", t0.Add(13*time.Minute)), "on time again: the episode ends")
	assert.Contains(t, w.step("machine: running (tick late 40s)", t0.Add(14*time.Minute)), "MACHINE TICK LATE", "a new episode pushes at once")
	assert.Empty(t, (&lateWatch{}).step("machine: STOPPED (tick late 99s)", t0), "a STOPPED machine is told by the push loop's machine line, not here")
}

func TestAPromotionEjectedFromTheMergeQueueIsToldToTheSeat(t *testing.T) {
	t.Parallel()
	f := &fakeForge{checks: []promoteCheck{{Name: "functional", Bucket: "pass"}}}
	var told []string
	p := &promoter{forge: f, base: "dev", tell: func(_ context.Context, typ, what, _ string) { told = append(told, typ+": "+what) }}
	o := promoteOutcome{Branch: "promo/2026-10-10-1"}
	var out, errb bytes.Buffer
	ctx := context.Background()
	p.watch(ctx, o, "5351", &out, &errb) // checks pending
	p.watch(ctx, o, "5351", &out, &errb) // checks pass: queued
	require.Equal(t, "5351", p.queued, "queued: %s%s", out.String(), errb.String())
	assert.Empty(t, told)

	f.entry = "" // the merge queue ejects it: neither merged nor closed
	p.watch(ctx, o, "5351", &out, &errb)
	require.Len(t, told, 1, "the ejection is told: %s", out.String())
	assert.True(t, strings.HasPrefix(told[0], NPromoteEjected+": pull request 5351 of promo/2026-10-10-1 was ejected from the merge queue (1 times this run)"), told[0])
	assert.Contains(t, out.String(), "PROMOTE EJECTED branch=promo/2026-10-10-1 pr=5351 times=1")
	assert.Equal(t, "5351", p.queued, "and queued again once its checks pass")
}
