package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeBriefDir writes n passing briefs a00.md, a01.md ... into a new directory.
func writeBriefDir(t *testing.T, n int) string {
	t.Helper()
	dir := t.TempDir()
	for i := 0; i < n; i++ {
		writeNeedsBrief(t, dir, fmt.Sprintf("a%02d", i), fmt.Sprintf("Fix a%02d.", i), "")
	}
	return dir
}

// resultLine is the result line of a verb: the one that starts with its token and a status.
func resultLine(out, errs, token string) string {
	for _, l := range strings.Split(out+errs, "\n") {
		if strings.HasPrefix(l, token+" OK ") || strings.HasPrefix(l, token+" FAIL ") {
			return l
		}
	}
	return ""
}

// add --brief-dir of 10 briefs adds the 10 cards, and says ADD OK moved=10.
func TestAddBriefDirOfTenAddsTen(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := writeBriefDir(t, 10)
	code, out, errs := ta.do("add --stream s1 --brief-dir " + dir)
	require.Equal(t, 0, code, out+errs)
	require.Contains(t, resultLine(out, errs, "ADD"), "ADD OK moved=10 ")
	var w whereView
	ta.json("where", &w)
	assert.Equal(t, int64(10), w.All)
	ta.clean()
}

// The write committed and the display cells then failed to sync (the 2026-10-01
// 07:45 failure: ADD FAIL moved=10 changed=no over ten cards that were in the
// table): the result line says what the table holds, ADD OK moved=10, and the
// exit is 0; the sync's own error is still said, on its own line.
func TestAddResultLineMatchesTheTableWhenTheDisplaySyncFails(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := writeBriefDir(t, 10)
	var committed atomic.Bool
	ta.m.Fail = func(point string) error {
		if point == "release" {
			committed.Store(true)
		}
		if committed.Load() && strings.HasPrefix(point, "readset") {
			return errors.New("the display cells cannot be read")
		}
		return nil
	}
	code, out, errs := ta.do("add --stream s1 --brief-dir " + dir)
	ta.m.Fail = nil
	line := resultLine(out, errs, "ADD")
	var w whereView
	ta.json("where", &w)
	require.Equal(t, int64(10), w.All, "the table holds the cards")
	assert.NotContains(t, line, "FAIL", "a FAIL over cards in the table: %s", line)
	assert.Contains(t, line, "ADD OK moved=10 ")
	assert.NotContains(t, line, "changed=no")
	assert.Equal(t, 0, code, out+errs)
	assert.Contains(t, errs, "display cells")
}

// Every verb that syncs the display cells after its write (the steps with Mirrors
// set: add, drop, release, finish, rework, merge, resume and the fleet verbs; deal
// is the tick's, which has no verb) reports a committed write whose sync failed as
// OK, exit 0, with the sync's own error on its own line: the table holds what the
// step wrote, so the result line never says FAIL or changed=no over it. Each row
// runs its verb once clean (the control) and once with the display read failing
// after the commit.
func TestAVerbWhoseDisplaySyncFailsAfterItsWriteReportsOK(t *testing.T) {
	t.Parallel()
	cards := func(n int) func(*testApp) {
		return func(ta *testApp) { ta.ok(fmt.Sprintf("add --stream s1 --count %d", n)) }
	}
	for _, tc := range []struct {
		name, token, line string
		setup             func(*testApp)
	}{
		{"add", "ADD", "add --stream s1 --count 2", func(*testApp) {}},
		{"drop", "DROP", "drop s1-1 --reason obsolete", cards(2)},
		{"fleet down", "FLEET-DOWN", "fleet down m1", func(*testApp) {}},
		{"fleet up", "FLEET-UP", "fleet up m1", func(ta *testApp) { ta.ok("fleet down m1") }},
		{"finish", "FINISH", "finish --as m1 s1-1.w1@1", func(ta *testApp) {
			cards(1)(ta)
			ta.deal(1)
			ta.ok("take --as m1 s1-1.w1@1")
		}},
		{"rework", "REWORK", "rework s1-1 --fix 'try again'", func(ta *testApp) {
			cards(1)(ta)
			ta.deal(1)
			ta.failOnce("m1", "s1-1.w1@1", "tests red")
		}},
		{"merge", "MERGE", "merge --stream s1 --batch 3", func(ta *testApp) { ta.toMergingAdded("s1") }},
		{"resume", "RESUME", "resume --stream s1 --did x", func(ta *testApp) {
			ta.toMergingAdded("s1")
			ta.ok("merge --stream s1 --red --suspect s1-2 s1-3")
		}},
		{"release", "RELEASE", "release stop --reason ok", func(ta *testApp) {
			cards(1)(ta)
			ta.ok("add --stream s1 --sentinel stop")
			ta.deal(1)
			ta.ok("take --as m1 s1-1.w1@1")
			ta.ok("finish --as m1 s1-1.w1@1")
			ta.ok("ask")
			ta.ok("read --as reader-a --ok s1-1.r1.reader-a")
			ta.ok("read --as reader-b --ok s1-1.r1.reader-b")
			ta.ok("accept s1-1")
			ta.ok("merge --stream s1")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, armed := range []bool{false, true} {
				ta := newTestApp(t)
				ta.ok("init --readers reader-a,reader-b --members m1")
				tc.setup(ta)
				var committed atomic.Bool
				if armed {
					ta.m.Fail = func(point string) error {
						if point == "release" {
							committed.Store(true)
						}
						if committed.Load() && strings.HasPrefix(point, "readset") {
							return errors.New("the display cells cannot be read")
						}
						return nil
					}
				}
				code, out, errs := ta.do(tc.line)
				ta.m.Fail = nil
				line := resultLine(out, errs, tc.token)
				require.Equal(t, 0, code, "armed=%v: %s%s", armed, out, errs)
				assert.Contains(t, line, tc.token+" OK ", "armed=%v", armed)
				assert.NotContains(t, line, "changed=")
				if armed {
					require.True(t, committed.Load(), "the verb never committed")
					assert.Contains(t, errs, "display cells did not sync")
				}
			}
		})
	}
}

// toMergingAdded is toMerging on an app that already ran init.
func (ta *testApp) toMergingAdded(stream string) {
	ta.t.Helper()
	ta.ok("add --stream " + stream + " --count 3")
	ta.deal(100)
	ta.ok("take --as m1 --limit 100")
	var q struct{ Cards []queueCard }
	ta.json("queue --as m1", &q)
	var words []string
	for _, c := range q.Cards {
		words = append(words, c.ID+"@"+strconv.Itoa(c.Gen))
	}
	ta.ok("finish --as m1 " + strings.Join(words, " "))
	ta.ok("ask --limit 100")
	ta.ok("read --as reader-a --ok --limit 100")
	ta.ok("read --as reader-b --ok --limit 100")
	ta.ok("accept --read-ok")
}
