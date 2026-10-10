package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// holdsOf is where --json --cards' holds as "<kind> <name>: <reason>".
func holdsOf(t *testing.T, ta *testApp) []string {
	t.Helper()
	var v whereView
	require.NoError(t, json.Unmarshal([]byte(ta.ok("where --json --cards")), &v))
	var held []string
	for _, h := range v.Holds {
		held = append(held, h.Kind+" "+h.Name+": "+h.Reason)
	}
	return held
}

// okLines is a verb's MOVED and OK lines, the op id taken off.
func okLines(out string) string {
	var keep []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "MOVED ") || strings.Contains(l, " OK ") {
			l, _, _ = strings.Cut(l, " op=")
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, "\n")
}

// fleet hold and fleet unhold are hold and unhold of fleet members alone (the owner,
// 2026-10-10: "you can add a fleet hold if you want"; "even if just an alias"): the same
// step and lines, the reason required of a hold, a name of another kind refused with the
// verb that holds it, nothing written.
func TestFleetHoldIsHoldOfAMemberAlone(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1,m2")
	ta.ok("add --stream s1 --count 2")

	code, _, errs := ta.do("fleet hold m1")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "nova-sprint fleet hold REFUSED: --reason <text> is required")
	code, _, errs = ta.do("fleet hold --reason r")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "wants at least one fleet member")
	code, _, _ = ta.do("fleet hold m1 --reason r --repo a/b --expect 1")
	assert.Equal(t, 2, code, "fleet hold takes no --repo: streams are hold's")

	code, _, errs = ta.do("fleet hold nobody --reason r")
	assert.Equal(t, 1, code, errs)
	assert.Contains(t, errs, "no fleet member nobody")
	code, _, errs = ta.do("fleet hold m1 reader-a --reason r")
	assert.Equal(t, 1, code, errs)
	assert.Contains(t, errs, "no fleet member reader-a: reader-a is a reader; run: nova-sprint hold reader-a --reason <text>")
	code, _, errs = ta.do("fleet hold s1 --reason r")
	assert.Equal(t, 1, code, errs)
	assert.Contains(t, errs, "s1 is a stream; run: nova-sprint hold s1 --reason <text>")
	assert.Empty(t, holdsOf(t, ta), "a refused name refuses the whole call: nothing held")

	out := ta.ok("fleet hold m1 --reason 'the cache trim'")
	assert.Regexp(t, `(?m)^HOLD OK moved=`, out)
	assert.Equal(t, []string{"member m1: the cache trim"}, holdsOf(t, ta))
	var v whereView
	require.NoError(t, json.Unmarshal([]byte(ta.ok("where --json --cards")), &v))
	assert.Equal(t, sprint.Held, v.Tables["fleet"]["m1"]["status"])
	assert.Contains(t, ta.ok("log"), "member m1 held: the cache trim")
	// the top-level hold on the other member says the same lines
	assert.Equal(t, strings.ReplaceAll(okLines(out), "m1", "m2"), okLines(ta.ok("hold m2 --reason 'the cache trim'")))
	ta.ok("unhold m2")

	code, _, errs = ta.do("fleet unhold reader-a")
	assert.Equal(t, 1, code, errs)
	assert.Contains(t, errs, "reader-a is a reader; run: nova-sprint unhold reader-a")
	out = ta.ok("fleet unhold m1 --reason trimmed")
	assert.Regexp(t, `(?m)^UNHOLD OK moved=`, out)
	assert.Empty(t, holdsOf(t, ta))
	assert.Contains(t, ta.ok("log"), "member m1 released from its hold: trimmed")

	assert.Contains(t, ta.ok("fleet hold m1 --reason r --dry-run"), "HOLD DRY-RUN names=m1")
	assert.Empty(t, holdsOf(t, ta), "a dry run writes nothing")
}

// friend hold and friend unhold are hold and unhold of friends alone: her roster's held
// record, the one top-level hold and friend down write (store.setFriendHold), her row
// held whatever she beats; a fleet member's name is refused with fleet hold.
func TestFriendHoldIsHoldOfAFriendAlone(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "amy")
	ta.ok("friend sync")

	code, _, errs := ta.do("friend hold amy")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "nova-sprint friend hold REFUSED: --reason <text> is required")
	code, _, errs = ta.do("friend hold --reason r")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "wants at least one friend")
	code, _, _ = ta.do("friend hold amy --reason r --return")
	assert.Equal(t, 2, code, "a held friend keeps no card either way: friend hold takes no --return")

	code, _, errs = ta.do("friend hold nobody --reason r")
	assert.Equal(t, 1, code, errs)
	assert.Contains(t, errs, "no friend nobody")
	code, _, errs = ta.do("friend hold m1 --reason r")
	assert.Equal(t, 1, code, errs)
	assert.Contains(t, errs, "no friend m1: m1 is a fleet member; run: nova-sprint fleet hold m1 --reason <text>")
	code, _, errs = ta.do("fleet hold amy --reason r")
	assert.Equal(t, 1, code, errs)
	assert.Contains(t, errs, "no fleet member amy: amy is a friend; run: nova-sprint friend hold amy --reason <text>")
	assert.Empty(t, holdsOf(t, ta))
	assert.NotEqual(t, sprint.Held, whereFriends(ta)["amy"].Status)

	ta.pong("amy")
	out := ta.ok("friend hold amy --reason 'rate limited'")
	assert.Regexp(t, `(?m)^HOLD OK moved=`, out)
	assert.Equal(t, []string{"friend amy: rate limited"}, holdsOf(t, ta))
	assert.Equal(t, sprint.Held, whereFriends(ta)["amy"].Status, "held whatever she beats")
	assert.Contains(t, ta.ok("log"), "friend amy held: rate limited")

	code, _, errs = ta.do("friend unhold m1")
	assert.Equal(t, 1, code, errs)
	assert.Contains(t, errs, "m1 is a fleet member; run: nova-sprint fleet unhold m1")
	out = ta.ok("friend unhold amy")
	assert.Regexp(t, `(?m)^UNHOLD OK moved=`, out)
	assert.Empty(t, holdsOf(t, ta))
	assert.NotEqual(t, sprint.Held, whereFriends(ta)["amy"].Status)
}

// fleet -h and friend -h list the new verbs, and help hold names both.
func TestHelpNamesFleetHoldAndFriendHold(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	fleet := ta.ok("help fleet")
	for _, want := range []string{"nova-sprint fleet hold <member>... --reason <text>", "nova-sprint fleet unhold <member>...", "fleet hold <member> --reason <text>"} {
		assert.Contains(t, fleet, want)
	}
	friend := ta.ok("help friend")
	for _, want := range []string{"nova-sprint friend hold <friend>... --reason <text>", "nova-sprint friend unhold <friend>...", "friend hold <friend> --reason <text>"} {
		assert.Contains(t, friend, want)
	}
	hold := ta.ok("help hold")
	assert.Contains(t, hold, "fleet hold <member>...")
	assert.Contains(t, hold, "friend hold <friend>...")
	assert.Contains(t, ta.ok("help friend hold"), "a fleet member is named with nova-sprint fleet hold")
	assert.Contains(t, ta.ok("help fleet unhold"), "a friend is named with nova-sprint friend unhold")
}
