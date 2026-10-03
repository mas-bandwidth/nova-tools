package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// landScorer is a score backend with no network: every class low, but stranded_fragment
// high over a state (the card and its diff) that names the file high.
type landScorer struct {
	high string
	asks atomic.Int32
}

func (f *landScorer) Name() string { return "fake" }

func (f *landScorer) Ask(ctx context.Context, s decide.Schema, state string) (map[string]decide.Answer, decide.Usage, error) {
	f.asks.Add(1)
	p := func(v float64) *float64 { return &v }
	table := map[string]decide.FixedAnswer{"verdict": {Choice: decide.Land, P: map[string]float64{decide.Land: 0.8}}}
	for name := range s.Questions {
		if name != "verdict" {
			table[name] = decide.FixedAnswer{Noul: p(0.1)}
		}
	}
	table["inside_paths"] = decide.FixedAnswer{Noul: p(0.95)}
	if strings.Contains(state, f.high) {
		table["stranded_fragment"] = decide.FixedAnswer{Noul: p(0.9)}
	}
	return decide.Fixed{Table: table}.Ask(ctx, s, state)
}

// A batch landed, land scores each card's merge diff against its brief, records each
// decision under <card>@landed@<head> in the land root's score record, writes the top
// class and p on each landed card, and raises ONE "landed work scored low" judgment
// listing the card at the bar; the coordinator answers it with ack. No socket, no key:
// the backend is the test's.
func TestLandScoresEveryLandedHeadAndJudgesTheBatchOnce(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	scorer := &landScorer{high: "s1-2.txt"}
	r.a.scoreBackend = scorer
	r.m.SetScoreBar("0.5")
	r.ok("add --stream s1 --count 3")
	heads := map[string]string{}
	for _, id := range []string{"s1-1", "s1-2", "s1-3"} {
		heads[id] = r.head(id, "main", id+".txt", id+"\n")
	}
	r.queued(heads, "s1-1", "s1-2", "s1-3")
	out := r.ok("land --repo-dir " + r.clone + " --base main")
	assert.Contains(t, out, "LAND OK stream=s1 cards=3")
	assert.Contains(t, out, " scored=3 judged=yes")
	assert.Equal(t, int32(3), scorer.asks.Load(), "one score a landed head")

	ds, err := decide.Load(filepath.Join(r.dir, "land", "decide", "score.jsonl"))
	require.NoError(t, err)
	require.Len(t, ds, 3)
	for i, id := range []string{"s1-1", "s1-2", "s1-3"} {
		assert.Equal(t, decide.ScoreOp(id, heads[id]), ds[i].ID)
		assert.Contains(t, ds[i].State, "+"+id+"\n", "the score is asked over the card's own merge diff")
	}

	var v cardView
	r.json("card s1-2", &v)
	assert.Equal(t, []string{"stranded_fragment", "0.900", decide.ScoreOp("s1-2", heads["s1-2"])},
		[]string{v.Primary.F(sprint.FieldLandedClass), v.Primary.F(sprint.FieldLandedP), v.Primary.F(sprint.FieldLandedOp)})
	var in struct{ Judgments []inboxJudgment }
	r.json("inbox", &in)
	require.Len(t, in.Judgments, 1, "one judgment for the batch: %+v", in)
	j := in.Judgments[0]
	assert.Equal(t, sprint.NScoredLow, j.Type)
	assert.Equal(t, []string{"s1-2"}, j.Cards, "only the card at the bar")
	inbox := r.ok("inbox")
	assert.Contains(t, inbox, "s1-2 stranded_fragment 0.90")
	assert.Contains(t, inbox, "nova-sprint add --stream s1 '<fix id>'", "the repair card is a decision it prints")
	r.ok("ack " + j.ID + " --reason 'the fragment is a list item, read whole it stands'")
	r.json("inbox", &in)
	assert.Empty(t, in.Judgments, "ack answers it")
	r.clean()
}

// With no backend and no JEV_API_KEY the batch lands as before and its line says, in one
// NOTE, that nothing was scored; with an empty bar every card is scored and no judgment
// is raised.
func TestLandWithoutAKeyOrABarLandsAndJudgesNothing(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		scorer bool
		says   string
	}{
		{"no key", false, "NOTE JEV_API_KEY is absent, so no landed diff was scored"},
		{"no bar", true, " scored=2\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newLandRig(t)
			if tc.scorer {
				r.a.scoreBackend = &landScorer{high: "s1-1.txt"}
			}
			r.ok("add --stream s1 --count 2")
			heads := map[string]string{"s1-1": r.head("s1-1", "main", "s1-1.txt", "one\n"), "s1-2": r.head("s1-2", "main", "s1-2.txt", "two\n")}
			r.queued(heads, "s1-1", "s1-2")
			code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
			assert.Equal(t, 0, code, errs)
			assert.Contains(t, out, "LAND OK stream=s1 cards=2")
			assert.Contains(t, out+errs, tc.says)
			assert.NotContains(t, out, "judged=yes")
			assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged"}, r.places("s1-1", "s1-2"))
			assert.NotContains(t, r.ok("inbox"), sprint.NScoredLow)
			r.clean()
		})
	}
}

// stalling is a score backend that fails: at its first ask it records whether every
// stream of the run had landed by then, then either fails at once or, like a hung
// backend, waits until its context ends (the land loop's, cancelled here as a shutdown or
// the pass's deadline would end it).
type stalling struct {
	hang    bool
	stop    context.CancelFunc
	landed  func() bool
	asks    atomic.Int32
	allDone atomic.Bool
}

func (f *stalling) Name() string { return "fake" }

func (f *stalling) Ask(ctx context.Context, _ decide.Schema, _ string) (map[string]decide.Answer, decide.Usage, error) {
	if f.asks.Add(1) == 1 {
		f.allDone.Store(f.landed())
	}
	if !f.hang {
		return nil, decide.Usage{}, errors.New("the backend answered HTTP 402")
	}
	f.stop()
	<-ctx.Done()
	return nil, decide.Usage{}, ctx.Err()
}

// The scoring runs after the whole land pass, never between streams: a backend that
// fails, or one that hangs until its context ends, is asked once, after both streams
// have landed; the pass stops there, the land exits 0, and the NOTE names the card that
// failed and every card not scored after it.
func TestLandScoresAfterThePassAndStopsAtTheFirstFailure(t *testing.T) {
	t.Parallel()
	for _, hang := range []bool{false, true} {
		t.Run(map[bool]string{false: "failing", true: "hanging"}[hang], func(t *testing.T) {
			t.Parallel()
			r := newLandRig(t)
			r.ok("add --stream s1 --count 2")
			r.ok("add --stream s2 --count 1")
			heads := map[string]string{}
			for _, id := range []string{"s1-1", "s1-2", "s2-1"} {
				heads[id] = r.head(id, "main", id+".txt", id+"\n")
			}
			r.queued(heads, "s1-1", "s1-2", "s2-1")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := &stalling{hang: hang, stop: cancel}
			f.landed = func() bool {
				return r.places("s2-1")["s2-1"] == "landed/merged" && r.places("s1-2")["s1-2"] == "landed/merged"
			}
			r.a.scoreBackend, r.a.landCtx = f, ctx
			code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
			assert.Equal(t, 0, code, errs)
			assert.Contains(t, out, "LAND OK stream=s1 cards=2")
			assert.Contains(t, out, "LAND OK stream=s2 cards=1")
			assert.True(t, f.allDone.Load(), "the first score is asked after every stream landed")
			assert.Equal(t, int32(1), f.asks.Load(), "the pass stops at the first failure: no ask after it")
			assert.Contains(t, errs, "NOTE s1-1 was not scored: ")
			assert.Contains(t, errs, "the scoring pass stopped at the first failure; not scored: s1-2")
			assert.Contains(t, errs, "the scoring pass stopped at the first failure; not scored: s2-1")
			assert.NotContains(t, out, "scored=")
			r.clean()
		})
	}
}
