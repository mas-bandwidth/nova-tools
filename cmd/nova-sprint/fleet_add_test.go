package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// fakeFleetAddPlay is the play runner fleet add is driven through in the tests:
// it answers one output and error and keeps the argv it was given. onRun, when
// set, runs while the play does (a test makes the member take and finish the
// probe card the verb dealt).
type fakeFleetAddPlay struct {
	out   string
	err   error
	argv  []string
	onRun func()
}

func (f *fakeFleetAddPlay) Play(_ context.Context, argv []string) (string, error) {
	f.argv = argv
	if f.onRun != nil {
		f.onRun()
	}
	return f.out, f.err
}

// fakeProbePhase says whether a play run's argv is the probe pass
// (nova_member_probe=1): the pass that takes the card the verb dealt, run only
// once the member's own loop has genuinely beaten.
func fakeProbePhase(argv []string) bool {
	return slices.Contains(argv, "nova_member_probe=1")
}

// fleetAddPlayOK is fleet/member.yml's and fleet/tools.yml's output when every
// step printed its line, including the binaries step tools.yml prints and the
// probe step member.yml prints.
const fleetAddPlayOK = `TASK [the pinned tools] ***
ok: [bench-c] => {"msg": "FLEET-ADD step=tools host=bench-c done tools=go,sqlite3,harness,age,sops,bats"}
TASK [the nova binaries at the adopted release] ***
ok: [bench-c] => {"msg": "FLEET-ADD step=binaries host=bench-c done version=v1.2.0-dev.abc1234"}
TASK [the member and reader loop units] ***
ok: [bench-c] => {"msg": "FLEET-ADD step=units host=bench-c done member=member-bench-c reader=reader-bench-c records=2"}
TASK [the route credential through the sealed-secrets path] ***
ok: [bench-c] => {"msg": "FLEET-ADD step=credential host=bench-c done route_key=none sealed=yes"}
TASK [a mirror for every repository a live card names] ***
ok: [bench-c] => {"msg": "FLEET-ADD step=mirrors host=bench-c done repos=1"}
TASK [the probe card] ***
ok: [bench-c] => {"msg": "FLEET-ADD step=probe host=bench-c done card=probe-bench-c-1 took=1 finished=1"}
`

// fleetAddPlayNoProbe is the same play without the probe's line: the play owes
// the step and its absence refuses the add.
const fleetAddPlayNoProbe = `TASK [the pinned tools] ***
ok: [bench-c] => {"msg": "FLEET-ADD step=tools host=bench-c done tools=go,sqlite3,harness,age,sops,bats"}
TASK [the nova binaries at the adopted release] ***
ok: [bench-c] => {"msg": "FLEET-ADD step=binaries host=bench-c done version=v1.2.0-dev.abc1234"}
TASK [the member and reader loop units] ***
ok: [bench-c] => {"msg": "FLEET-ADD step=units host=bench-c done member=member-bench-c reader=reader-bench-c records=2"}
TASK [the route credential through the sealed-secrets path] ***
ok: [bench-c] => {"msg": "FLEET-ADD step=credential host=bench-c done route_key=none sealed=yes"}
TASK [a mirror for every repository a live card names] ***
ok: [bench-c] => {"msg": "FLEET-ADD step=mirrors host=bench-c done repos=1"}
`

// fleetAddPlayMissingSQLite is a refusal of the first step: a member with no
// sqlite3 gets the step's refusal and its remedy, and nothing after it runs.
const fleetAddPlayMissingSQLite = `TASK [the pinned tools] ***
fatal: [bench-c]: FAILED! => {"msg": "FLEET-ADD REFUSED step=tools host=bench-c: sqlite3 is not installed; run: apt-get install -y sqlite3 (or the machine's package manager), then run fleet add again"}
`

// fleetAddPlayStepsText is the source text of the two plays fleet add runs:
// fleet/member.yml and the fleet/tools.yml it imports. The step test holds the
// verb's fleetAddPlaySteps to the lines these files print, so the verb and the
// play it ships cannot drift.
func fleetAddPlayStepsText(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	for _, name := range []string{"member.yml", "tools.yml"} {
		text, err := os.ReadFile(filepath.Join("..", "..", "fleet", name))
		require.NoError(t, err, name)
		b.Write(text)
	}
	return b.String()
}

// TestFleetAddPlayEmitsEveryStepByTheVerbsReading pins finding 5: every step
// fleetAddPlaySteps names has a FLEET-ADD line in the plays fleet add ships
// (fleet/member.yml and the fleet/tools.yml it imports), including the binaries
// step, which the verb refuses a run without. A hand-written fake cannot hide
// a play missing a line: this reads the plays.
func TestFleetAddPlayEmitsEveryStepByTheVerbsReading(t *testing.T) {
	t.Parallel()
	play := fleetAddPlayStepsText(t)
	for _, step := range fleetAddPlaySteps {
		assert.Contains(t, play, "FLEET-ADD step="+step+" ", "fleet/member.yml and fleet/tools.yml print no FLEET-ADD step=%s line, and the verb refuses a run without it", step)
	}
	// The content, not only the line: the credential and mirror steps the verb
	// drives must read the run's own variables, so a play whose step line stands
	// in for work the verb never supplies is refused here.
	assert.Contains(t, play, "'--only', nova_member_route_key", "the credential step must read the route key the verb passes (nova_member_route_key)")
	assert.Contains(t, play, "'--base', nova_member_mirror_base", "the mirror step must read the mirror base the verb passes (nova_member_mirror_base)")

	// the route credential reaches the member: the records name the keys and the
	// seat their units open through nova-secrets, and each member argv names
	// --pass so the card's child is handed the route's key. A play that names
	// neither leaves the member's children with no provider key and the probe
	// card cannot run on its route.
	assert.Contains(t, play, "'--keys', nova_member_route_key", "the loop records must name the keys their units open (--keys nova_member_route_key)")
	assert.Contains(t, play, "'--seat', nova_seat", "the loop records must name the seat the keys are opened from (--seat nova_seat)")
	assert.GreaterOrEqual(t, strings.Count(play, "'--pass', nova_member_route_key"), 3,
		"the member and reader argvs and the probe run must each name --pass nova_member_route_key so nova-swarm hands the route's key to the child")

	// the mirror list: the verb passes nova_member_repos as one comma-separated
	// word, group_vars holds the list, and the inventory may hold either, so the
	// step splits a string into the list mirror --repos takes; a play that joins
	// the raw value iterates a string character by character on a real run.
	assert.Contains(t, play, "nova_member_repos.split(',')", "the mirror step must split a comma-separated nova_member_repos into the list --repos takes")
	assert.Contains(t, play, "fleet_add_repos | join(',')", "the mirror step must join the normalized repository list (fleet_add_repos), not the raw value")
}

// fleetAddWidth is the member's width on the fleet table: -1 when it has no row.
func fleetAddWidth(t *testing.T, ta *testApp, member string) int {
	t.Helper()
	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(t, err)
	snap, err := st.Load(context.Background(), []string{sprint.Fleet}, nil)
	require.NoError(t, err)
	ctl := snap.MemberCtl(member)
	if ctl == nil {
		return -1
	}
	return sprint.MemberWidth(ctl)
}

// TestFleetAddSetsUpAMemberEndToEnd: fleet add runs fleet/member.yml for one
// host and does a member end to end: it prints one line per step (the machine
// steps the play printed, the store and check steps the verb did), it adds the
// member drained so it is dealt no work until its beat, its reader row are
// there and the member has taken and finished a probe card, and only then
// widens it. A play that omitted a step, or one that claimed the probe without
// the member finishing it, leaves the member drained and is refused; a second
// run changes nothing; --dry-run writes nothing.
func TestFleetAddSetsUpAMemberEndToEnd(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, "fleet"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "fleet", "member.yml"), []byte("[]\n"), 0o644))
	base := []string{"--source", src, "--inventory", "/inv/nova-inventory"}

	// A live card names a repository the member must mirror, and the store's
	// routes name the credential it must read: fleet add derives both from the
	// store, so the play's credential and mirror steps do their work.
	brief := writeBrief(t, "c: a card that names a repository\nREPO: mas-bandwidth/nova-tools\nBASE: sprint/s")
	setup := func(ta *testApp) {
		ta.ok("init --readers reader-a,reader-b")
		ta.m.SetRoutes([]sprint.Route{{Name: "flash-or", Tier: "flash", Provider: "deepseek", Model: "deepseek-v4-flash", Enabled: true}})
		ta.ok("add --stream s1 --count 4 --brief-file " + brief)
		ta.ok("start")
		ta.live = []string{"bench-c"}      // only the member being added beats
		ta.ok("reader add reader-bench-c") // its row beats with every command from here
	}
	var w whereView

	// the play's line says the probe finished, but the member took and finished
	// no card: the verb reads the store and refuses, so the member stays drained
	// and is dealt no work
	ta := newTestApp(t)
	setup(ta)
	claimed := &fakeFleetAddPlay{out: fleetAddPlayOK}
	fleetAddPlayOf.Store(ta.a, claimed)
	defer fleetAddPlayOf.Delete(ta.a)
	code, out, errs := ta.do("fleet add bench-c --width 2 " + strings.Join(base, " "))
	require.Equal(t, 1, code, "%s%s", out, errs)
	assert.Contains(t, errs, "fleet add REFUSED step=probe host=bench-c")
	assert.Contains(t, errs, "has not taken and finished the probe card")
	assert.Equal(t, 0, fleetAddWidth(t, ta, "bench-c"), "the member is added drained while the probe is unfinished")
	ta.ok("tick")
	ta.json("where", &w)
	assert.Equal(t, 0, cardsOf(w, "bench-c"), "a member whose probe has not finished is dealt no work")

	// the whole play: the member takes and finishes the probe card the verb dealt,
	// every step's line is there, the member is proved and dealt work
	okta := newTestApp(t)
	setup(okta)
	ok := &fakeFleetAddPlay{out: fleetAddPlayOK}
	ok.onRun = func() {
		if !fakeProbePhase(ok.argv) {
			return // the setup pass runs no probe: the verb deals it after this
		}
		okta.ok("take --as bench-c --limit 1")
		okta.ok("finish --as bench-c probe-bench-c-1.w1@1")
		okta.ok("tick") // the server's tick applies the member's queued take and finish
	}
	fleetAddPlayOf.Store(okta.a, ok)
	defer fleetAddPlayOf.Delete(okta.a)
	code, out, errs = okta.do("fleet add bench-c --width 2 " + strings.Join(base, " "))
	require.Equal(t, 0, code, "%s%s", out, errs)
	for _, step := range []string{"tools", "binaries", "units", "credential", "mirrors", "probe", "rows", "beat", "reader"} {
		assert.Contains(t, out, "step="+step+" host=bench-c", "the step %s printed its line", step)
	}
	assert.Contains(t, out, "step=units host=bench-c done member=member-bench-c reader=reader-bench-c", "the loop units step names the member and reader units")
	assert.Contains(t, out, "step=mirrors host=bench-c done repos=1", "the mirror for every live card's repository")
	assert.Contains(t, out, "step=probe host=bench-c done", "the probe step's line")
	assert.Contains(t, out, "FLEET-ADD OK host=bench-c width=2", "the receipt")
	assert.Equal(t, 2, fleetAddWidth(t, okta, "bench-c"), "the member is widened only after the check")
	argvLine := strings.Join(ok.argv, " ")
	assert.True(t, strings.Contains(argvLine, "nova_member=bench-c"), "the play is told the member: %v", ok.argv)
	assert.True(t, strings.Contains(argvLine, "nova_member_width=2"), "the play is told the width: %v", ok.argv)
	assert.True(t, strings.Contains(argvLine, "nova_member_reader=reader-bench-c"), "the play is told the reader: %v", ok.argv)
	assert.True(t, strings.Contains(argvLine, "--limit bench-c"), "the play is limited to the host: %v", ok.argv)
	// the run's own variables, derived from the store: the repository the live
	// card names, the base its mirror is fetched from, and the route credential
	// the store's routes name. The play's credential and mirror steps are no-ops
	// without them.
	_, wantBase := mirrorNameBase(swarm.CardRepoURL("mas-bandwidth/nova-tools"))
	assert.Contains(t, argvLine, "nova_member_repos=nova-tools", "the verb passes the repository a live card names: %v", ok.argv)
	assert.Contains(t, argvLine, "nova_member_mirror_base="+wantBase, "the verb passes the base the live card's repository is fetched from: %v", ok.argv)
	assert.Contains(t, argvLine, "nova_member_route_key=DEEPSEEK_API_KEY", "the verb passes the route credential the store's routes name: %v", ok.argv)
	require.Contains(t, ok.argv, filepath.Join(src, "fleet", "member.yml"), "the play is fleet/member.yml")

	okta.ok("tick")
	okta.json("where", &w)
	assert.Greater(t, cardsOf(w, "bench-c"), 0, "the proven member is dealt work")

	// a second run changes nothing: the same width, the probe already finished
	before := fleetAddWidth(t, okta, "bench-c")
	again := &fakeFleetAddPlay{out: fleetAddPlayOK}
	fleetAddPlayOf.Store(okta.a, again)
	code, _, errs = okta.do("fleet add bench-c --width 2 " + strings.Join(base, " "))
	require.Equal(t, 0, code, errs)
	assert.Equal(t, before, fleetAddWidth(t, okta, "bench-c"), "a second run changed the width")

	// a play that ended without the probe's line is refused, the member drained
	ta2 := newTestApp(t)
	ta2.ok("init --readers reader-a,reader-b")
	ta2.live = []string{"bench-c"}
	ta2.ok("reader add reader-bench-c") // its row beats with every command from here
	noProbe := &fakeFleetAddPlay{out: fleetAddPlayNoProbe}
	fleetAddPlayOf.Store(ta2.a, noProbe)
	defer fleetAddPlayOf.Delete(ta2.a)
	code, _, errs = ta2.do("fleet add bench-c --width 2 " + strings.Join(base, " "))
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "fleet add REFUSED step=probe host=bench-c")
	assert.Equal(t, 0, fleetAddWidth(t, ta2, "bench-c"), "a play without the probe step leaves the member drained")

	// the first step's refusal is said with its remedy, the member stays drained
	refused := &fakeFleetAddPlay{out: fleetAddPlayMissingSQLite, err: errors.New("exit status 2")}
	ta3 := newTestApp(t)
	ta3.ok("init --readers reader-a,reader-b")
	ta3.live = []string{"bench-c"}
	fleetAddPlayOf.Store(ta3.a, refused)
	defer fleetAddPlayOf.Delete(ta3.a)
	code, _, errs = ta3.do("fleet add bench-c --width 2 " + strings.Join(base, " "))
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "sqlite3 is not installed")
	assert.Contains(t, errs, "apt-get install -y sqlite3")
	assert.Equal(t, 0, fleetAddWidth(t, ta3, "bench-c"), "a refused step leaves the member drained")

	// --dry-run lists every step, runs the play's --check and writes nothing
	ta4 := newTestApp(t)
	ta4.ok("init --readers reader-a,reader-b")
	ta4.live = []string{"bench-c"}
	dry := &fakeFleetAddPlay{out: fleetAddPlayOK}
	fleetAddPlayOf.Store(ta4.a, dry)
	defer fleetAddPlayOf.Delete(ta4.a)
	code, out, errs = ta4.do("fleet add bench-c --width 2 --dry-run " + strings.Join(base, " "))
	require.Equal(t, 0, code, "%s%s", out, errs)
	assert.Contains(t, out, "FLEET-ADD WOULD-ADD host=bench-c width=2")
	for _, step := range fleetAddSteps {
		assert.Contains(t, out, "FLEET-ADD WOULD host=bench-c step="+step, "the dry run lists the %s step", step)
	}
	assert.Contains(t, dry.argv, "--check", "the dry run runs the play with --check: %v", dry.argv)
	assert.Equal(t, -1, fleetAddWidth(t, ta4, "bench-c"), "the dry run wrote the member row")

	// usage: one host and a width
	for _, bad := range [][]string{
		{"fleet add"},
		{"fleet add bench-c"},
		{"fleet add bench-c --width 0 " + strings.Join(base, " ")},
		{"fleet add bench-c --width 2048 " + strings.Join(base, " ")},
		{"fleet add bench-c! --width 2 " + strings.Join(base, " ")},
	} {
		code, _, errs = ta.do(strings.Join(bad, " "))
		assert.Equal(t, 2, code, "%v: %s", bad, errs)
		assert.Contains(t, errs, "fleet add REFUSED")
	}
}

// TestFleetAddReservesTheMemberWhileTheProbeRuns pins the reservation finding:
// while a member is being added and before its probe card is finished, an
// ordinary deal tick must reach it no work. The verb admits two probe cards
// (the primary and a holder) on the member's own stream and deals both, filling
// the member's room (DealAhead times width 1), so an interleaved tick leaves the
// ordinary ready cards waiting; the holder is dropped once the member is proved
// and widened. The plan must not record a quiet of the member: a quiet with no
// unquiet member left panics the deal at bench_deal.go:137 (benchRefusal), and
// this card's PATHS do not reach that file.
func TestFleetAddReservesTheMemberWhileTheProbeRuns(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, "fleet"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "fleet", "member.yml"), []byte("[]\n"), 0o644))
	base := []string{"--source", src, "--inventory", "/inv/nova-inventory"}

	brief := writeBrief(t, "c: a card that names a repository\nREPO: mas-bandwidth/nova-tools\nBASE: sprint/s")
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b")
	ta.m.SetRoutes([]sprint.Route{{Name: "flash-or", Tier: "flash", Provider: "deepseek", Model: "deepseek-v4-flash", Enabled: true}})
	ta.ok("add --stream s1 --count 4 --brief-file " + brief)
	ta.ok("start")
	ta.live = []string{"bench-c"}
	ta.ok("reader add reader-bench-c")

	tickRan := false
	play := &fakeFleetAddPlay{out: fleetAddPlayOK}
	play.onRun = func() {
		// An ordinary tick during the setup pass reaches the drained member no
		// work; during the probe pass it holds only its probe cards, up to its
		// room, and the ordinary ready cards stay waiting.
		ta.ok("tick")
		tickRan = true
		var during whereView
		ta.json("where", &during)
		if !fakeProbePhase(play.argv) {
			assert.Equal(t, 0, cardsOf(during, "bench-c"), "a drained member holds no card during setup")
			assert.Equal(t, "4", cellText(during.Tables["work"]["s1"]["ready"]), "the ordinary ready cards stay waiting during setup")
			return
		}
		assert.Equal(t, 3, cardsOf(during, "bench-c"), "the member holds the primary and both reservations while the probe runs")
		assert.Equal(t, "4", cellText(during.Tables["work"]["s1"]["ready"]), "the ordinary ready cards stay waiting during the probe")
		ta.ok("take --as bench-c --limit 1")
		ta.ok("finish --as bench-c probe-bench-c-1.w1@1")
		ta.ok("tick")
		var afterPrimary whereView
		ta.json("where", &afterPrimary)
		assert.Equal(t, "4", cellText(afterPrimary.Tables["work"]["s1"]["ready"]), "ordinary ready work remains until beat, reader and probe proofs all pass")
	}
	fleetAddPlayOf.Store(ta.a, play)
	defer fleetAddPlayOf.Delete(ta.a)

	code, out, errs := ta.do("fleet add bench-c --width 2 " + strings.Join(base, " "))
	require.Equal(t, 0, code, "%s%s", out, errs)
	require.True(t, tickRan, "the interleaved tick ran")

	// proved and widened: the holder is dropped and the next tick deals the
	// ordinary work to the member
	ta.ok("tick")
	var after whereView
	ta.json("where", &after)
	assert.Greater(t, cardsOf(after, "bench-c"), 0, "the proved member is dealt ordinary work")
}

// TestFleetAddRefusesAMemberThatHasNotReallyBeaten pins the fresh-host finding:
// the setup's own steps are never current availability. A member with no beat at
// all is dealt no probe and is not widened; so is one whose only beat is
// expired; only a member whose own loop beats during the setup run is proved.
// The play's setup pass beats the member, as the machine's loop does, and the
// probe pass takes the card the verb dealt.
func TestFleetAddRefusesAMemberThatHasNotReallyBeaten(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, "fleet"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "fleet", "member.yml"), []byte("[]\n"), 0o644))
	base := []string{"--source", src, "--inventory", "/inv/nova-inventory"}
	line := "fleet add bench-c --width 2 " + strings.Join(base, " ")

	// no beat at all: the member's loop never runs, so the add refuses it and
	// leaves it drained, and starts no probe run
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b")
	ta.live = nil
	none := &fakeFleetAddPlay{out: fleetAddPlayOK}
	fleetAddPlayOf.Store(ta.a, none)
	defer fleetAddPlayOf.Delete(ta.a)
	code, _, errs := ta.do(line)
	require.Equal(t, 1, code, errs)
	assert.Contains(t, errs, "fleet add REFUSED step=beat host=bench-c")
	assert.Equal(t, 0, fleetAddWidth(t, ta, "bench-c"), "a member with no beat is left drained")
	assert.False(t, fakeProbePhase(none.argv), "no probe run is started for a member that has not beaten")

	// an expired beat: the member beat once, long ago, and its loop is not
	// running now, so the stale beat is not current availability
	old := newTestApp(t)
	old.ok("init --readers reader-a,reader-b")
	old.live = []string{"bench-c"}
	old.ok("tick") // one command beats it, at this clock's now
	old.mu.Lock()
	old.live = nil
	old.now = old.now.Add(2 * fleetAddBeatBound)
	old.mu.Unlock()
	stale := &fakeFleetAddPlay{out: fleetAddPlayOK}
	fleetAddPlayOf.Store(old.a, stale)
	defer fleetAddPlayOf.Delete(old.a)
	code, _, errs = old.do(line)
	require.Equal(t, 1, code, errs)
	assert.Contains(t, errs, "fleet add REFUSED step=beat host=bench-c")
	assert.Equal(t, 0, fleetAddWidth(t, old, "bench-c"), "a stale beat leaves the member drained")

	// a genuine beat: the machine's loop beats the member during the setup run,
	// and the probe run takes the card, so the member is proved and dealt work
	fresh := newTestApp(t)
	fresh.ok("init --readers reader-a,reader-b")
	fresh.live = nil
	live := &fakeFleetAddPlay{out: fleetAddPlayOK}
	live.onRun = func() {
		if !fakeProbePhase(live.argv) {
			fresh.live = []string{"bench-c"} // the machine's member loop is up
			fresh.beat()
			return
		}
		fresh.ok("take --as bench-c --limit 1")
		fresh.ok("finish --as bench-c probe-bench-c-1.w1@1")
		fresh.ok("tick")
	}
	fleetAddPlayOf.Store(fresh.a, live)
	defer fleetAddPlayOf.Delete(fresh.a)
	code, out, errs := fresh.do(line)
	require.Equal(t, 0, code, "%s%s", out, errs)
	assert.Equal(t, 2, fleetAddWidth(t, fresh, "bench-c"), "a member whose loop has beaten is widened")
}
