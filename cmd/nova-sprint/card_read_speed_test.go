package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// logReads is the test's store as a card read sees it, counting the log it
// reads and the tables a hold would load. whole is a read of the log from its
// start. holdAll is a shapes or cells read that names every table, which is
// how Held loads the sprint (store.Load of All): one card's hold must not.
type logReads struct {
	*store.Mem
	n *logCount
}

type logCount struct {
	mu           sync.Mutex
	whole, lines int
	holdAll      int
	tables       [][]string
}

func (b logReads) LogSince(ctx context.Context, after string, max int) ([]sprint.Line, []string, error) {
	lines, ids, err := b.Mem.LogSince(ctx, after, max)
	b.n.mu.Lock()
	defer b.n.mu.Unlock()
	if after == "" {
		b.n.whole++
	}
	b.n.lines += len(lines)
	return lines, ids, err
}

func (b logReads) Shapes(ctx context.Context, tables []string) ([]ntable.Table, error) {
	b.n.note(tables)
	return b.Mem.Shapes(ctx, tables)
}

func (b logReads) CellIDs(ctx context.Context, shapes []ntable.Table) (map[string][]string, error) {
	names := make([]string, len(shapes))
	for i, s := range shapes {
		names[i] = s.Name
	}
	b.n.note(names)
	return b.Mem.CellIDs(ctx, shapes)
}

func (b logReads) AtEpoch(epoch uint64, old bool) store.Backend {
	return logReads{Mem: b.Mem.AtEpoch(epoch, old).(*store.Mem), n: b.n}
}

func (c *logCount) note(tables []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tables = append(c.tables, append([]string(nil), tables...))
	if tablesCoverAll(tables) {
		c.holdAll++
	}
}

func (c *logCount) reset() (whole, lines, holdAll int, tables [][]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	whole, lines, holdAll, tables = c.whole, c.lines, c.holdAll, c.tables
	c.whole, c.lines, c.holdAll = 0, 0, 0
	c.tables = nil
	return whole, lines, holdAll, tables
}

// tablesCoverAll says the read named every sprint table, as Load of All does
// in one exchange. A card's own rows are one table, or the work table and the
// readers table's shape; fleet and merge together with those two is the hold
// that still loads every table.
func tablesCoverAll(tables []string) bool {
	got := map[string]bool{}
	for _, t := range tables {
		switch {
		case strings.HasSuffix(t, sprint.Work):
			got[sprint.Work] = true
		case strings.HasSuffix(t, sprint.Readers):
			got[sprint.Readers] = true
		case strings.HasSuffix(t, sprint.Merge):
			got[sprint.Merge] = true
		case strings.HasSuffix(t, sprint.Fleet):
			got[sprint.Fleet] = true
		}
	}
	return got[sprint.Work] && got[sprint.Readers] && got[sprint.Merge] && got[sprint.Fleet]
}

// noIndex is the same store with no card log index and no machine records:
// a card read there tells its story from the whole log and its needs from the
// whole work table, and its hold from Held, the read as it was before the index.
type noIndex struct{ store.Backend }

func (b noIndex) AtEpoch(epoch uint64, old bool) store.Backend {
	return noIndex{b.Backend.AtEpoch(epoch, old)}
}

// cardStory is what of `card <id> --json` the index and the card facts answer:
// the story and the needs, and the card's own records. The hold is compared
// on its own, because a table that moved since the tick keeps the tick's hold
// and does not load every table to recompute it.
func cardStory(t *testing.T, out string) map[string]json.RawMessage {
	t.Helper()
	var v map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(out), &v), out)
	keep := map[string]json.RawMessage{}
	for _, k := range []string{"primary", "timeline", "texts", "needs", "needed_by", "work_cards", "read_cards", "merge", "open"} {
		keep[k] = v[k]
	}
	return keep
}

// One card read touches that card (docs/SPEC-SPRINT.md, card-read-speedb.w1):
// on 2026-10-04 `card <id> --json` took 3.4 s on the live store, reading the
// whole log (240,119 lines) for one card's story and every table for its hold.
// On a 3,000-card twin with a log as long as its cards, once the tick has run,
// a card read never reads the log from its start and reads no more of it than
// the tail the tick has not indexed, tells the same story and the same needs
// as the whole log and the whole table, and its hold loads no table: a shapes
// or cells read that names work, readers, merge and fleet is Held's Load of
// All, and that count is zero. A release after the tick is in the story from
// the tail, and that read's hold still does not load every table. Every card
// is readable in one call: card --all --json prints each, one JSON object a line.
func TestCardReadTouchesOnlyItsOwnLogAndRows(t *testing.T) {
	t.Parallel()
	ta := bigSprint(t)
	// the log as long as the live store's is for its cards: 20,000 lines about
	// other cards, written as a step writes its lines (the fence taken and
	// released with them), before the tick indexes it; one tick indexes
	// 20,000 lines at most, so it takes two
	ctx := context.Background()
	f, err := ta.m.ReadFence(ctx)
	require.NoError(t, err)
	pad := store.OpRecord{ID: "pad", Verb: "pad", At: ta.a.now()}
	for i := range 20000 {
		pad.Log = append(pad.Log, sprint.Line{Kind: sprint.LineMove, At: ta.a.now(), Op: "pad", Card: fmt.Sprintf("pad-%d.w1", i), Primary: fmt.Sprintf("pad-%d", i), Table: sprint.Fleet, To: "m1:working"})
	}
	ok, err := ta.m.Acquire(ctx, f.Gen, pad)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, ta.m.Release(ctx, pad, true))
	ta.ok("tick")
	ta.ok("tick")
	n := &logCount{}
	ta.a.backend = func(context.Context, string, sprint.Names) (store.Backend, error) {
		return logReads{Mem: ta.m, n: n}, nil
	}
	plain := newTestApp(t)
	plain.m = ta.m
	plain.now = ta.a.now()
	plain.a.backend = func(context.Context, string, sprint.Names) (store.Backend, error) { return noIndex{ta.m}, nil }

	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(t, err)
	log, err := st.Log(ctx)
	require.NoError(t, err)
	require.Greater(t, len(log), 20000, "a long log")

	// every card in one call, one object a line
	var all []cardLine
	for _, line := range strings.Split(strings.TrimSpace(ta.ok("card --all --json")), "\n") {
		var c cardLine
		require.NoError(t, json.Unmarshal([]byte(line), &c), line)
		all = append(all, c)
	}
	require.Len(t, all, 3000, "every card on the table")
	byID := map[string]cardLine{}
	for _, c := range all {
		byID[c.ID] = c
		require.NotContains(t, c.Fields, "brief", "the brief is its length, not its text")
	}
	require.Equal(t, "waiting", byID["gate1"].Column)
	require.Positive(t, byID["gate1"].BriefLen+1)
	behind := ""
	for _, c := range all {
		if c.Stream == "w1" && c.ID != "gate1" {
			behind = c.ID
			break
		}
	}
	require.NotEmpty(t, behind)
	stream := strings.Split(strings.TrimSpace(ta.ok("card --stream w1 --json")), "\n")
	assert.Len(t, stream, 500, "one stream's cards: its sentinel and its 499")

	indexed := len(log) // the ticks indexed it all
	read := func(id string, sameHold bool) {
		t.Helper()
		now, err := st.Log(ctx)
		require.NoError(t, err)
		tail := len(now) - indexed
		n.reset()
		cells := ta.m.Calls["cells"]
		out := ta.ok("card " + id + " --json")
		whole, lines, holdAll, tables := n.reset()
		require.Zero(t, whole, "card %s read the log from its start", id)
		require.LessOrEqual(t, lines, tail, "card %s read %d lines of a %d-line log: more than the %d-line tail the tick has not indexed", id, lines, len(now), tail)
		require.Zero(t, holdAll, "card %s: the hold loaded every table in %d reads %v; Held's Load of All is one shapes of work, readers, merge and fleet", id, holdAll, tables)
		if sameHold {
			require.Equal(t, cells, ta.m.Calls["cells"], "card %s: a settled read, hold included, read a table whole", id)
		}
		var got cardView
		require.NoError(t, json.Unmarshal([]byte(out), &got))
		require.NotNil(t, got.Held, "card %s: the tick's hold is on the card", id)
		want := plain.ok("card " + id + " --json")
		assert.Equal(t, cardStory(t, want), cardStory(t, out), "card %s: the index and the card facts tell the story the whole log and the whole table tell", id)
		if sameHold {
			var live cardView
			require.NoError(t, json.Unmarshal([]byte(want), &live))
			assert.Equal(t, live.Held, got.Held, "card %s: the tick's hold is what Held says, and the card read did not load every table to get it", id)
		}
	}
	read("gate1", true)
	read(behind, true)
	var v cardView
	require.NoError(t, json.Unmarshal([]byte(ta.ok("card "+behind+" --json")), &v))
	n.reset()
	require.NotEmpty(t, v.Timeline, "a story")
	require.NotEmpty(t, v.Needs, "it waits on its sentinel")
	assert.Equal(t, "gate1", v.Needs[0].ID)

	ta.ok("release gate1 --reason 'the wave is loaded' --actor lead")
	read("gate1", false)
	out := ta.ok("card gate1 --json")
	n.reset()
	assert.Contains(t, out, "the wave is loaded", "the release after the tick is told from the log's tail")
}
