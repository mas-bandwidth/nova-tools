package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// fakeFleetAddPlay is the play runner fleet add is driven through in the tests:
// it answers one output and error and keeps the argv it was given. onRun, when
// set, runs while the play does (a test beats the loops the play would start).
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

// fleetAddPlayOK is fleet/member.yml's output when every step printed its line.
const fleetAddPlayOK = `TASK [the pinned tools] ***
ok: [bench-c] => {"msg": "FLEET-ADD step=tools host=bench-c done tools=go,sqlite3,harness,age,sops,bats"}
TASK [the nova binaries at the adopted release] ***
ok: [bench-c] => {"msg": "FLEET-ADD step=binaries host=bench-c done version=v1.2.0-dev.abc1234 bins=12"}
TASK [the member and reader loop units] ***
ok: [bench-c] => {"msg": "FLEET-ADD step=units host=bench-c done member=member-bench-c reader=reader-bench-c records=2"}
TASK [the route credential through the sealed-secrets path] ***
ok: [bench-c] => {"msg": "FLEET-ADD step=credential host=bench-c done route=pro-a keys=1"}
TASK [a mirror for every repository a live card names] ***
ok: [bench-c] => {"msg": "FLEET-ADD step=mirrors host=bench-c done repos=1"}
TASK [the probe card] ***
ok: [bench-c] => {"msg": "FLEET-ADD step=probe host=bench-c done card=probe-bench-c took=1 finished=1"}
`

// fleetAddPlayNoProbe is the same play without the probe's line: the member
// has not taken and finished a probe card, so it is not dealt work.
const fleetAddPlayNoProbe = `TASK [the pinned tools] ***
ok: [bench-c] => {"msg": "FLEET-ADD step=tools host=bench-c done tools=go,sqlite3,harness,age,sops,bats"}
TASK [the nova binaries at the adopted release] ***
ok: [bench-c] => {"msg": "FLEET-ADD step=binaries host=bench-c done version=v1.2.0-dev.abc1234 bins=12"}
TASK [the member and reader loop units] ***
ok: [bench-c] => {"msg": "FLEET-ADD step=units host=bench-c done member=member-bench-c reader=reader-bench-c records=2"}
TASK [the route credential through the sealed-secrets path] ***
ok: [bench-c] => {"msg": "FLEET-ADD step=credential host=bench-c done route=pro-a keys=1"}
TASK [a mirror for every repository a live card names] ***
ok: [bench-c] => {"msg": "FLEET-ADD step=mirrors host=bench-c done repos=1"}
`

// fleetAddPlayMissingSQLite is a refusal of the first step: a member with no
// sqlite3 gets the step's refusal and its remedy, and nothing after it runs.
const fleetAddPlayMissingSQLite = `TASK [the pinned tools] ***
fatal: [bench-c]: FAILED! => {"msg": "FLEET-ADD REFUSED step=tools host=bench-c: sqlite3 is not installed; run: apt-get install -y sqlite3 (or the machine's package manager), then run fleet add again"}
`

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
// there and the play has finished a probe card, and only then widens it. A
// play that omitted a step, or one that refused, leaves the member drained and
// is refused; a second run changes nothing; --dry-run writes nothing.
func TestFleetAddSetsUpAMemberEndToEnd(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, "fleet"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "fleet", "member.yml"), []byte("[]\n"), 0o644))
	base := []string{"--source", src, "--inventory", "/inv/nova-inventory"}

	// one member, no others: the new member alone is dealt the ready cards
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b")
	ta.ok("add --stream s1 --count 4")
	ta.ok("start")
	ta.live = []string{"bench-c"}      // only the member being added beats
	ta.ok("reader add reader-bench-c") // its row beats with every command from here

	// the play ended without the probe's line: the member is not dealt work
	noProbe := &fakeFleetAddPlay{out: fleetAddPlayNoProbe}
	fleetAddPlayOf.Store(ta.a, noProbe)
	defer fleetAddPlayOf.Delete(ta.a)
	code, out, errs := ta.do("fleet add bench-c --width 2 " + strings.Join(base, " "))
	require.Equal(t, 1, code, "%s%s", out, errs)
	assert.Contains(t, errs, "fleet add REFUSED step=probe host=bench-c")
	assert.Equal(t, 0, fleetAddWidth(t, ta, "bench-c"), "the member is added drained while the probe is unfinished")
	ta.ok("tick")
	var w whereView
	ta.json("where", &w)
	assert.Equal(t, 0, cardsOf(w, "bench-c"), "a member whose probe has not finished is dealt no work")

	// the whole play: every step's line, the member proven, the member dealt work
	ok := &fakeFleetAddPlay{out: fleetAddPlayOK}
	fleetAddPlayOf.Store(ta.a, ok)
	code, out, errs = ta.do("fleet add bench-c --width 2 " + strings.Join(base, " "))
	require.Equal(t, 0, code, "%s%s", out, errs)
	for _, step := range []string{"tools", "binaries", "units", "credential", "mirrors", "probe", "rows", "beat", "reader"} {
		assert.Contains(t, out, "step="+step+" host=bench-c", "the step %s printed its line", step)
	}
	assert.Contains(t, out, "step=units host=bench-c done member=member-bench-c reader=reader-bench-c", "the loop units step names the member and reader units")
	assert.Contains(t, out, "step=mirrors host=bench-c done repos=1", "the mirror for every live card's repository")
	assert.Contains(t, out, "FLEET-ADD OK host=bench-c width=2", "the receipt")
	assert.Equal(t, 2, fleetAddWidth(t, ta, "bench-c"), "the member is widened only after the check")
	assert.True(t, strings.Contains(strings.Join(ok.argv, " "), "nova_member=bench-c"), "the play is told the member: %v", ok.argv)
	assert.True(t, strings.Contains(strings.Join(ok.argv, " "), "nova_member_width=2"), "the play is told the width: %v", ok.argv)
	assert.True(t, strings.Contains(strings.Join(ok.argv, " "), "nova_member_reader=reader-bench-c"), "the play is told the reader: %v", ok.argv)
	assert.True(t, strings.Contains(strings.Join(ok.argv, " "), "--limit bench-c"), "the play is limited to the host: %v", ok.argv)
	require.Contains(t, ok.argv, filepath.Join(src, "fleet", "member.yml"), "the play is fleet/member.yml")

	ta.ok("tick")
	ta.json("where", &w)
	assert.Greater(t, cardsOf(w, "bench-c"), 0, "the proven member is dealt work")

	// a second run changes nothing: the same width, the same rows
	before := fleetAddWidth(t, ta, "bench-c")
	again := &fakeFleetAddPlay{out: fleetAddPlayOK}
	fleetAddPlayOf.Store(ta.a, again)
	code, _, errs = ta.do("fleet add bench-c --width 2 " + strings.Join(base, " "))
	require.Equal(t, 0, code, errs)
	assert.Equal(t, before, fleetAddWidth(t, ta, "bench-c"), "a second run changed the width")

	// the first step's refusal is said with its remedy, the member stays drained
	refused := &fakeFleetAddPlay{out: fleetAddPlayMissingSQLite, err: errors.New("exit status 2")}
	ta2 := newTestApp(t)
	ta2.ok("init --readers reader-a,reader-b")
	ta2.live = []string{"bench-c"}
	fleetAddPlayOf.Store(ta2.a, refused)
	defer fleetAddPlayOf.Delete(ta2.a)
	code, _, errs = ta2.do("fleet add bench-c --width 2 " + strings.Join(base, " "))
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "sqlite3 is not installed")
	assert.Contains(t, errs, "apt-get install -y sqlite3")
	assert.Equal(t, 0, fleetAddWidth(t, ta2, "bench-c"), "a refused step leaves the member drained")

	// --dry-run lists every step, runs the play's --check and writes nothing
	ta3 := newTestApp(t)
	ta3.ok("init --readers reader-a,reader-b")
	ta3.live = []string{"bench-c"}
	dry := &fakeFleetAddPlay{out: fleetAddPlayOK}
	fleetAddPlayOf.Store(ta3.a, dry)
	defer fleetAddPlayOf.Delete(ta3.a)
	code, out, errs = ta3.do("fleet add bench-c --width 2 --dry-run " + strings.Join(base, " "))
	require.Equal(t, 0, code, "%s%s", out, errs)
	assert.Contains(t, out, "FLEET-ADD WOULD-ADD host=bench-c width=2")
	for _, step := range []string{"rows", "beat", "reader"} {
		assert.Contains(t, out, "FLEET-ADD WOULD host=bench-c step="+step, "the dry run lists the %s step", step)
	}
	assert.Contains(t, dry.argv, "--check", "the dry run runs the play with --check: %v", dry.argv)
	assert.Equal(t, -1, fleetAddWidth(t, ta3, "bench-c"), "the dry run wrote the member row")

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
