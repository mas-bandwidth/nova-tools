package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// defer, roadmap restore and roadmap render on a twin store (docs/SPEC-SPRINT.md, "The
// roadmap: work deferred to a later release"): of a stream of three waiting cards and one
// working, defer --stream --expect 3 writes the three, briefs whole, into the work record's
// roadmap and drops them, and leaves the working card; --expect 4 refuses and changes
// nothing; restore brings one back as its twin with its brief byte for byte; render writes
// no card id.
func TestDeferMovesWaitingCardsToTheRoadmapAndRestoreBringsOneBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ta := newTestApp(t)
	ta.ok("init --members m1,m2 --readers reader-a,reader-b,reader-c")
	dir := t.TempDir()
	record := filepath.Join(dir, "record")
	brief := func(id, lead string) string {
		path := filepath.Join(dir, id+".md")
		require.NoError(t, os.WriteFile(path, []byte(passingBrief("REPO: example/nova-tools\n\n"+lead)), 0o600))
		return path
	}
	ta.ok("add --stream later now-1 --one --brief-file " + brief("now-1", "Do the first thing now."))
	ta.deal(1)
	leads := map[string]string{
		"later-1": "Later: \"quoted\" and \\backslashed\\ words.",
		"later-2": "Later:\n\ta tab, trailing spaces   \n  (parens) ;semicolons; and ünïcödé",
		"later-3": "Later: the third.",
	}
	for _, id := range []string{"later-1", "later-2", "later-3"} {
		ta.ok("add --stream later " + id + " --needs now-1 --one --brief-file " + brief(id, leads[id]))
	}
	load := func() *sprint.Snapshot {
		t.Helper()
		st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
		require.NoError(t, err)
		s, err := st.Load(ctx, []string{sprint.Work}, func(*sprint.Snapshot) map[string][]string {
			return map[string][]string{sprint.Work: {"now-1", "later-1", "later-2", "later-3", "later-2b"}}
		})
		require.NoError(t, err)
		return s
	}
	s := load()
	require.Equal(t, sprint.Working, s.Work.Placed("now-1").Col, "now-1 is dealt")
	briefs := map[string]string{}
	for id := range leads {
		c := s.Work.Placed(id)
		require.NotNil(t, c, id)
		require.Equal(t, sprint.Waiting, c.Col, id)
		briefs[id] = c.F("brief")
		require.Contains(t, briefs[id], leads[id])
	}
	roadmap := filepath.Join(record, "roadmaps", "nova-tools-v2.sexp")

	// --expect 4 of three waiting: refused, nothing written, nothing dropped
	code, _, errs := ta.do("defer --release v2 --stream later --expect 4 --record " + record)
	assert.Equal(t, 1, code, errs)
	assert.Contains(t, errs, "3 waiting cards found, not 4")
	assert.NoFileExists(t, roadmap)
	for id := range leads {
		assert.Equal(t, sprint.Waiting, load().Work.Placed(id).Col, id)
	}

	// a card named that is not waiting is refused, named
	code, _, errs = ta.do("defer --release v2 now-1 later-1 --record " + record)
	assert.Equal(t, 1, code, errs)
	assert.Contains(t, errs, "REFUSED now-1: is working, not waiting")
	assert.NoFileExists(t, roadmap)

	out := ta.ok("defer --release v2 --stream later --expect 3 --record " + record)
	assert.Contains(t, out, "LEFT now-1 working")
	assert.Contains(t, out, "DEFER OK moved=")
	b, err := os.ReadFile(roadmap)
	require.NoError(t, err)
	rm, err := sprint.ParseRoadmap(b)
	require.NoError(t, err)
	assert.Equal(t, "nova-tools", rm.Product)
	assert.Equal(t, []string{"v2"}, rm.Releases)
	require.Equal(t, 3, rm.Count())
	for id := range leads {
		c, _ := rm.Find(id)
		require.NotNil(t, c, id)
		assert.Equal(t, briefs[id], c.Brief, id)
		assert.Equal(t, "later", c.Stream, id)
		assert.Equal(t, []string{"now-1"}, c.Needs, id)
		assert.Equal(t, "example/nova-tools", c.Repo, id)
	}
	s = load()
	assert.Equal(t, sprint.Working, s.Work.Placed("now-1").Col, "the working card is untouched")
	for id := range leads {
		assert.Nil(t, s.Work.Placed(id), id)
		require.NotNil(t, s.Work.Card(id), id)
		assert.Equal(t, "deferred to release v2", s.Work.Card(id).F("reason"), id)
	}

	out = ta.ok("roadmap restore later-2 --record " + record)
	assert.Contains(t, out, "later-2b")
	s = load()
	tw := s.Work.Placed("later-2b")
	require.NotNil(t, tw, "the twin is on the table")
	assert.Equal(t, briefs["later-2"], tw.F("brief"), "the brief comes back byte for byte")
	assert.Equal(t, "later-2", tw.F(sprint.FieldReplaces))
	assert.Equal(t, "later", tw.Row)
	assert.Equal(t, "now-1", tw.F("needs"))
	b, err = os.ReadFile(roadmap)
	require.NoError(t, err)
	rm, err = sprint.ParseRoadmap(b)
	require.NoError(t, err)
	assert.Equal(t, 2, rm.Count())
	got, _ := rm.Find("later-2")
	assert.Nil(t, got, "restored, it leaves the roadmap")
	code, _, errs = ta.do("roadmap restore later-2 --record " + record)
	assert.Equal(t, 1, code, errs)
	assert.Contains(t, errs, "holds later-2")

	out = ta.ok("roadmap render --record " + record)
	assert.Contains(t, out, "ROADMAP-RENDER OK")
	md, err := os.ReadFile(filepath.Join(record, "ROADMAP.md"))
	require.NoError(t, err)
	assert.Contains(t, string(md), "## nova-tools v2")
	assert.Contains(t, string(md), "| later | 2 |")
	for _, id := range []string{"now-1", "later-1", "later-2", "later-3", "Later:", "example/"} {
		assert.NotContains(t, string(md), id)
		assert.NotContains(t, out, id)
	}
	ta.clean()
}

// An explicit --expect 0 is a supplied count. The flag defaults to 0, and a
// guard of *expect > 0 treated that zero as omitted: with one waiting card,
// defer --stream --expect 0 wrote the roadmap and dropped the card. A supplied
// count that differs, zero included, is refused before anything is written.
// An omitted --expect still defers.
func TestDeferExplicitExpectZeroRefusesBeforeWriting(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ta := newTestApp(t)
	ta.ok("init --members m1,m2 --readers reader-a,reader-b,reader-c")
	dir := t.TempDir()
	record := filepath.Join(dir, "record")
	brief := filepath.Join(dir, "later-1.md")
	require.NoError(t, os.WriteFile(brief, []byte(passingBrief("REPO: example/nova-tools\n\nLater: the one waiting card.")), 0o600))
	ta.ok("add --stream later later-1 --one --brief-file " + brief)
	roadmap := filepath.Join(record, "roadmaps", "nova-tools-v2.sexp")

	code, _, errs := ta.do("defer --release v2 --stream later --expect 0 --record " + record)
	assert.Equal(t, 1, code, errs)
	assert.Contains(t, errs, "1 waiting cards found, not 0")
	assert.Contains(t, errs, "changed=no")
	assert.NoFileExists(t, roadmap)

	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(t, err)
	s, err := st.Load(ctx, []string{sprint.Work}, nil)
	require.NoError(t, err)
	c := s.Work.Placed("later-1")
	require.NotNil(t, c, "the card is still on the table")
	assert.Equal(t, sprint.Waiting, c.Col)

	out := ta.ok("defer --release v2 --stream later --record " + record)
	assert.Contains(t, out, "DEFER OK")
	_, err = os.Stat(roadmap)
	require.NoError(t, err, "an omitted --expect still writes the roadmap")
	s, err = st.Load(ctx, []string{sprint.Work}, nil)
	require.NoError(t, err)
	assert.Nil(t, s.Work.Placed("later-1"), "an omitted --expect still drops the waiting card")
	ta.clean()
}

// The roadmap's form reads back as it was written, every byte of a brief kept.
func TestRoadmapFormReadsBack(t *testing.T) {
	t.Parallel()
	rm := sprint.Roadmap{Product: "p", Releases: []string{"v2"}}
	require.NoError(t, rm.Append([]sprint.RoadmapCard{
		{ID: "a", Stream: "s", Tier: "heavy", Needs: []string{"x", "y"}, Repo: "o/p", Brief: "q\"u\\o\nte;(x)\r\n\t "},
		{ID: "b", Stream: "t", Who: "friend-a", Replaces: []string{"bb"}, Rules: "/r.txt", Brief: ""},
	}))
	got, err := sprint.ParseRoadmap(sprint.FormatRoadmap(rm))
	require.NoError(t, err)
	assert.Equal(t, rm, got)
	assert.Error(t, rm.Append([]sprint.RoadmapCard{{ID: "a", Stream: "s"}}), "a card twice")
	for _, bad := range []string{"", "(:roadmap", "(:other)", "(:roadmap :product)", "(:roadmap) ()", `(:roadmap :product "p`,
		`(:roadmap :product "p" :cards ())`, `(:roadmap :streams ((:name "s" :other 1)))`, `(:roadmap :streams ((:name "s" :cards ((:id "a" :owner "x")))))`} {
		_, err := sprint.ParseRoadmap([]byte(bad))
		assert.Error(t, err, bad)
	}
}

// A restore refused whole leaves the roadmap file's bytes alone: the after-hook
// rewrites the sexp only when the step moved the card (Refused empty, Moved not),
// so a refusal can never silently drop the card from the file (the defect the
// reader found in attempts 2 and 3).
func TestRestoreRefusedLeavesTheRoadmapFileUnchanged(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --members m1,m2 --readers reader-a,reader-b,reader-c")
	dir := t.TempDir()
	record := filepath.Join(dir, "record")
	brief := func(id string) string {
		path := filepath.Join(dir, id+".md")
		require.NoError(t, os.WriteFile(path, []byte(passingBrief("REPO: example/nova-tools\n\nDo "+id+" later.")), 0o600))
		return path
	}
	ta.ok("add --stream later now-1 --one --brief-file " + brief("now-1"))
	ta.deal(1)
	for _, id := range []string{"later-1", "later-2", "later-3"} {
		ta.ok("add --stream later " + id + " --needs now-1 --one --brief-file " + brief(id))
	}
	ta.ok("defer --release v2 --stream later --expect 3 --record " + record)
	roadmap := filepath.Join(record, "roadmaps", "nova-tools-v2.sexp")
	before, err := os.ReadFile(roadmap)
	require.NoError(t, err)
	rm, err := sprint.ParseRoadmap(before)
	require.NoError(t, err)
	require.Equal(t, 3, rm.Count())
	card, _ := rm.Find("later-2")
	require.NotNil(t, card, "later-2 is in the roadmap before the refused restore")

	// every twin id of later-2 is taken, so Restore refuses the whole step
	var twins []string
	for l := byte('b'); l <= 'z'; l++ {
		twins = append(twins, "later-2"+string(rune(l)))
	}
	ta.ok("add --stream later " + strings.Join(twins, " ") + " --brief-file " + brief("twins"))

	code, _, errs := ta.do("roadmap restore later-2 --record " + record)
	assert.Equal(t, 1, code, errs)
	assert.Contains(t, errs, "every twin id of later-2 is taken")

	after, err := os.ReadFile(roadmap)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "a refused restore leaves the roadmap file byte for byte")
	rm, err = sprint.ParseRoadmap(after)
	require.NoError(t, err)
	got, _ := rm.Find("later-2")
	require.NotNil(t, got, "the card is still in the roadmap after the refusal")
	assert.Equal(t, card.Brief, got.Brief, "its brief is untouched")
	ta.clean()
}
