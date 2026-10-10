package sprint

import (
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
)

// render_log.go turns a line into plain words (Render, RenderText, Timeline,
// SplitCost). These tests hold each renderer's words: every function's main
// path and one refusal, through the package's own seams, with no store, no
// subprocess and no clock.

func TestRenderLogCoverRenderTextParagraphs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		text map[string]string
		want []string
	}{
		{
			name: "the fields come in the fixed order, each with its label",
			text: map[string]string{"ci_note": "green", "return_reason": "needs the reader", "fix": "did the thing", "reason": "the cause", "brief": "the task", "finding": "one of them"},
			want: []string{"brief: the task", "fix: did the thing", "finding: one of them", "reason: the cause", "reason: needs the reader", "ci: green"},
		},
		{
			name: "blank words are left out",
			text: map[string]string{"report": "  ", "fix": "\tno blank here\n"},
			want: []string{"fix: no blank here"},
		},
		{
			name: "a line with no words has no paragraphs: nothing is printed",
			text: nil,
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, RenderText(Line{Text: tc.text}))
		})
	}
}

func TestRenderLogCoverTextLabel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		field, want string
	}{
		{field: "return_reason", want: "reason"},
		{field: "ci_note", want: "ci"},
		{field: "fix", want: "fix"},
		{field: "anything", want: "anything"}, // an unknown field keeps its own name
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, textLabel(tc.field))
		})
	}
}

func TestRenderLogCoverByWhom(t *testing.T) {
	t.Parallel()
	cases := []struct {
		actor, want string
	}{
		{actor: "", want: "by the machine"},
		{actor: MachineActor, want: "by the machine"},
		{actor: "coord", want: "by coord"},
	}
	for _, tc := range cases {
		t.Run("actor:"+tc.actor, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, byWhom(tc.actor))
		})
	}
}

func TestRenderLogCoverAttemptOf(t *testing.T) {
	t.Parallel()
	cases := []struct {
		id, want string
	}{
		{id: "s1-1.w3", want: "3"},
		{id: "s1-1.r2.1", want: "2"},
		{id: "s1-1.w1.r2", want: "1"}, // the first part that says wins
		{id: "s1-1", want: ""},        // refusal: no attempt part
		{id: "s1-1.x", want: ""},
		{id: "s1-1.w", want: ""}, // a bare letter says no attempt
	}
	for _, tc := range cases {
		t.Run("id:"+tc.id, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, attemptOf(tc.id))
		})
	}
}

func TestRenderLogCoverSetWords(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		set  map[string]string
		want string
	}{
		{name: "sorted name=value", set: map[string]string{"ver": "2", "note": "x"}, want: "note=x, ver=2"},
		{name: "nothing set says nothing", set: nil, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, setWords(tc.set))
		})
	}
}

func TestRenderLogCoverFirstLine(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{in: "one line", want: "one line"},
		{in: "  padded  \nand more", want: "padded (more below)"},
		{in: "   ", want: ""}, // refusal: blank text is no line
	}
	for _, tc := range cases {
		t.Run("in:"+tc.in, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, firstLine(tc.in))
		})
	}
}

func TestRenderLogCoverRedealOf(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		set  map[string]string
		want string
	}{
		{name: "a redeal counts against its bound", set: map[string]string{"redeals": "2"}, want: "; redeal 2 of 3"},
		{name: "no count says nothing", set: nil, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, redealOf(Line{Set: tc.set}))
		})
	}
}

func TestRenderLogCoverWhyOf(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		actor, cause string
		want         string
	}{
		{name: "a cause says why", actor: "coord", cause: "no member was up", want: "by coord: no member was up"},
		{name: "without a cause only the actor speaks", actor: "", cause: "", want: "by the machine"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, whyOf(Line{Actor: tc.actor, Cause: tc.cause}))
		})
	}
}

func TestRenderLogCoverMirrorsWork(t *testing.T) {
	t.Parallel()
	cases := []struct {
		from, to string
		want     bool
	}{
		{from: "s1:" + string(Ready), to: "s1:" + string(Working), want: true},
		{from: "s1:" + string(Working), to: "s1:" + string(Review), want: true},
		{from: "s1:" + string(Working), to: "s1:" + string(Ready), want: true},
		{from: "s1:" + string(Ready), to: "s1:" + string(Review), want: false}, // refusal: not a mirror
	}
	for _, tc := range cases {
		t.Run(tc.from+"->"+tc.to, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, mirrorsWork(Line{From: tc.from, To: tc.to}))
		})
	}
}

func TestRenderLogCoverPrimaryLines(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		l    Line
		want string
	}{
		{name: "added", l: Line{Card: "s1-1", Stream: "s1", Table: Work, To: "s1:" + string(Ready)}, want: "s1-1 added to s1 by the machine"},
		{name: "added waiting", l: Line{Card: "s1-1", Stream: "s1", Table: Work, To: "s1:" + string(Waiting)}, want: "s1-1 added to s1 by the machine, waiting for what it needs"},
		{name: "taken off", l: Line{Card: "s1-1", Stream: "s1", Table: Work, From: "s1:" + string(Ready), Removed: true, Actor: "coord"}, want: "s1-1 taken off the table by coord"},
		{name: "changed in place", l: Line{Card: "s1-1", Stream: "s1", Table: Work, From: "s1:" + string(Ready), To: "s1:" + string(Ready), Set: map[string]string{"ver": "2", "note": "x"}}, want: "s1-1 changed by the machine: note=x, ver=2"},
		{name: "ready from waiting", l: Line{Card: "s1-1", Stream: "s1", Table: Work, From: "s1:" + string(Waiting), To: "s1:" + string(Ready)}, want: "s1-1 is ready: what it needs has landed"},
		{name: "ready again", l: Line{Card: "s1-1", Stream: "s1", Table: Work, From: "s1:" + string(Working), To: "s1:" + string(Ready), Actor: "coord"}, want: "s1-1 is ready again by coord"},
		{name: "dealt", l: Line{Card: "s1-1", Stream: "s1", Table: Work, From: "s1:" + string(Ready), To: "s1:" + string(Working), Set: map[string]string{"attempt": "3"}}, want: "s1-1 is being worked: attempt 3 dealt"},
		{name: "reworked", l: Line{Card: "s1-1", Stream: "s1", Table: Work, From: "s1:" + string(Review), To: "s1:" + string(Working), Actor: "coord", Set: map[string]string{"attempt": "4"}}, want: "s1-1 reworked by coord: attempt 4"},
		{name: "back for review", l: Line{Card: "s1-1", Stream: "s1", Table: Work, From: "s1:" + string(Working), To: "s1:" + string(Review)}, want: "s1-1 is back for review"},
		{name: "returned to review", l: Line{Card: "s1-1", Stream: "s1", Table: Work, From: "s1:" + string(Merging), To: "s1:" + string(Review), Actor: "coord"}, want: "s1-1 returned to review by coord"},
		{name: "accepted", l: Line{Card: "s1-1", Stream: "s1", Table: Work, From: "s1:" + string(Review), To: "s1:" + string(Merging), Actor: "coord"}, want: "s1-1 accepted by coord; queued to merge in s1"},
		{name: "landed", l: Line{Card: "s1-1", Stream: "s1", Table: Work, From: "s1:" + string(Merging), To: "s1:" + string(Landed)}, want: "s1-1 landed"},
		{name: "a sentinel waits", l: Line{Card: "s1-1", Stream: "s1", Table: Work, From: "s1:" + string(Ready), To: "s1:" + string(Waiting), Actor: "coord"}, want: "s1-1 waits: a sentinel was put in front of it by coord"},
		// refusal: a column no rule names falls back to the plain words.
		{name: "an unknown column is plain", l: Line{Card: "s1-1", Stream: "s1", Table: Work, From: "s1:" + string(Ready), To: "s1:nope", Verb: "tick"}, want: "s1-1 s1:ready -> s1:nope by the machine (tick)"},
		// the multi-card line: one line for a set move, in place.
		{name: "a set of cards", l: Line{Card: "c1", Cards: []string{"c1", "c2", "c3"}, Table: Work, Stream: "s1", From: "s1:" + string(Ready), To: "s1:" + string(Ready), Set: map[string]string{"note": "x"}}, want: "3 cards: c1 changed by the machine: note=x (with c2, c3)"},
		{name: "more than four are counted", l: Line{Card: "c1", Cards: []string{"c1", "c2", "c3", "c4", "c5", "c6"}, Table: Work, Stream: "s1", From: "s1:" + string(Ready), To: "s1:" + string(Ready), Set: map[string]string{"note": "x"}}, want: "6 cards: c1 changed by the machine: note=x (with c2, c3, c4 and 2 more)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, Render(tc.l))
		})
	}
}

func TestRenderLogCoverWorkLines(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		l    Line
		want string
	}{
		{name: "dealt to a member", l: Line{Card: "s1-1.w3", Table: Fleet, To: "m1:" + string(Ready)}, want: "attempt 3 dealt to m1"},
		{name: "taken off a queue", l: Line{Card: "s1-1.w3", Table: Fleet, From: "m1:" + string(Ready), Removed: true}, want: "attempt 3 taken off m1's queue by the machine"},
		{name: "changed in place", l: Line{Card: "s1-1.w3", Table: Fleet, From: "m1:" + string(Ready), To: "m1:" + string(Ready), Set: map[string]string{"stamps": "1"}}, want: "attempt 3 changed by the machine: stamps=1"},
		{name: "taken back", l: Line{Card: "s1-1.w3", Table: Fleet, From: "m1:" + string(Working), To: "m1:" + Withdrawn, Cause: "no member was up"}, want: "attempt 3 taken back from m1 by the machine: no member was up"},
		{name: "a member takes it", l: Line{Card: "s1-1.w3", Table: Fleet, From: "m1:" + string(Ready), To: "m1:" + string(Working)}, want: "m1 took attempt 3"},
		{name: "finished ok with head and branch", l: Line{Card: "s1-1.w3", Table: Fleet, From: "m1:" + string(Working), To: "m1:" + DoneOK, Set: map[string]string{"head": "abc123", "branch": "sprint/x"}}, want: "m1 finished attempt 3: ok, head abc123 on sprint/x"},
		{name: "finished failed", l: Line{Card: "s1-1.w3", Table: Fleet, From: "m1:" + string(Working), To: "m1:" + DoneFailed}, want: "m1 finished attempt 3: FAILED"},
		{name: "re dealt in place", l: Line{Card: "s1-1.w3", Table: Fleet, From: "m1:" + Withdrawn, To: "m1:" + string(Ready), Gen: 4, Set: map[string]string{"redeals": "2"}}, want: "attempt 3 redealt to m1 (generation 4; redeal 2 of 3)"},
		{name: "moved to level the queues", l: Line{Card: "s1-1.w3", Table: Fleet, From: "m2:" + string(Ready), To: "m1:" + string(Ready), Gen: 1, Verb: "level"}, want: "attempt 3 moved from m2's queue to m1's to even the queues by the machine (generation 1)"},
		{name: "redealt to another member", l: Line{Card: "s1-1.w3", Table: Fleet, From: "m1:" + string(Ready), To: "m2:" + string(Ready), Gen: 2}, want: "attempt 3 redealt from m1 to m2 by the machine (generation 2)"},
		// refusal: no rule names the step's words; the plain line says them.
		{name: "an unruled move is plain", l: Line{Card: "s1-1.w3", Table: Fleet, From: "m1:" + string(Ready), To: "m1:nope", Verb: "tick"}, want: "s1-1.w3 m1:ready -> m1:nope by the machine (tick)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, Render(tc.l))
		})
	}
}

func TestRenderLogCoverReadLines(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		l    Line
		want string
	}{
		{name: "asked to read", l: Line{Card: "s1-1.r1.reader-a", Table: Readers, To: "reader-a:" + Asked}, want: "reader-a asked to read attempt 1 by the machine"},
		{name: "returned with no verdict", l: Line{Card: "s1-1.r1.reader-a", Table: Readers, From: "reader-a:" + Reading, To: "reader-a:" + Asked, Set: map[string]string{FieldReturned: "t"}}, want: "reader-a returned its read of attempt 1 with no verdict"},
		{name: "a returned read taken back", l: Line{Card: "s1-1.r1.reader-b", Table: Readers, From: "reader-b:" + Asked, Removed: true, Set: map[string]string{"retired_by": "returned"}}, want: "the read of attempt 1 taken back from reader-b by the machine"},
		{name: "asked again in place", l: Line{Card: "s1-1.r1.reader-a", Table: Readers, From: "reader-a:" + Asked, To: "reader-a:" + Asked, Set: map[string]string{"asked": "t"}}, want: "reader-a asked again to read attempt 1 by the machine"},
		{name: "taken off", l: Line{Card: "s1-1.r1.reader-b", Table: Readers, From: "reader-b:" + Asked, Removed: true, Actor: "coord"}, want: "the read of attempt 1 by reader-b taken off by coord"},
		{name: "changed in place", l: Line{Card: "s1-1.r1.reader-a", Table: Readers, From: "reader-a:" + Reading, To: "reader-a:" + Reading, Set: map[string]string{"stamps": "1"}}, want: "the read of attempt 1 by reader-a changed: stamps=1"},
		{name: "began reading", l: Line{Card: "s1-1.r1.reader-a", Table: Readers, From: "reader-a:" + Asked, To: "reader-a:" + Reading}, want: "reader-a began reading attempt 1"},
		{name: "read ok", l: Line{Card: "s1-1.r1.reader-a", Table: Readers, From: "reader-a:" + Reading, To: "reader-a:" + OK}, want: "reader-a read attempt 1: ok"},
		{name: "read broken", l: Line{Card: "s1-1.r1.reader-a", Table: Readers, From: "reader-a:" + Reading, To: "reader-a:" + Broken}, want: "reader-a read attempt 1: broken"},
		{name: "asked again by move", l: Line{Card: "s1-1.r1.reader-a", Table: Readers, From: "reader-a:" + OK, To: "reader-a:" + Asked}, want: "reader-a asked again to read attempt 1 by the machine"},
		// refusal: an unknown column falls back to the plain words.
		{name: "an unruled move is plain", l: Line{Card: "s1-1.r1.reader-a", Table: Readers, From: "reader-a:" + Asked, To: "reader-a:" + Withdrawn, Verb: "tick"}, want: "s1-1.r1.reader-a reader-a:asked -> reader-a:withdrawn by the machine (tick)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, Render(tc.l))
		})
	}
}

func TestRenderLogCoverMergeLines(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		l    Line
		want string
	}{
		{name: "queued", l: Line{Card: "s1-1", Stream: "s1", Table: Merge, To: "s1:" + Queued}, want: "queued to merge in s1"},
		{name: "off the queue", l: Line{Card: "s1-1", Stream: "s1", Table: Merge, From: "s1:" + Queued, To: "s1:" + Queued, Removed: true}, want: "off the merge queue of s1 by the machine"},
		{name: "changed in place", l: Line{Card: "s1-1", Stream: "s1", Table: Merge, From: "s1:" + Queued, To: "s1:" + Queued, Set: map[string]string{"state": "merging"}}, want: "its merge card changed by the machine: state=merging"},
		{name: "merged", l: Line{Card: "s1-1", Stream: "s1", Table: Merge, From: "s1:" + Queued, To: "s1:" + Merged, Actor: "coord"}, want: "merged into s1 and landed by coord"},
		{name: "stuck", l: Line{Card: "s1-1", Stream: "s1", Table: Merge, From: "s1:" + Queued, To: "s1:" + Stuck, Cause: "a conflict"}, want: "stuck in the merge of s1 by the machine: a conflict"},
		{name: "returned", l: Line{Card: "s1-1", Stream: "s1", Table: Merge, From: "s1:" + Stuck, To: "s1:" + Returned}, want: "off the merge queue of s1: returned by the machine"},
		{name: "queued again", l: Line{Card: "s1-1", Stream: "s1", Table: Merge, From: "s1:" + Returned, To: "s1:" + Queued, Actor: "coord"}, want: "queued to merge in s1 again by coord"},
		// refusal: an unknown column falls back to the plain words.
		{name: "an unruled move is plain", l: Line{Card: "s1-1", Stream: "s1", Table: Merge, From: "s1:" + Queued, To: "s1:" + DoneOK, Verb: "tick"}, want: "s1-1 s1:queued -> s1:ok by the machine (tick)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, Render(tc.l))
		})
	}
}

func TestRenderLogCoverCtlAndPlainLines(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		l    Line
		want string
	}{
		{name: "a control card's state with its cause", l: Line{Table: Merge, To: "s1:" + Ctl, Actor: "coord", Set: map[string]string{"state": "paused", "cause": "the owner held it"}}, want: "s1 paused by coord (the owner held it)"},
		{name: "a control card changed", l: Line{Table: Fleet, From: "m1:" + Ctl, To: "m1:" + Ctl, Set: map[string]string{"width": "2"}}, want: "m1 changed by the machine: width=2"},
		// refusal: a line of no known table is plain, and removal says so.
		{name: "an unknown table is plain", l: Line{Card: "c", Table: "nonsense", From: "a:" + string(Ready), To: "a:" + string(Working), Verb: "deal"}, want: "c a:ready -> a:working by the machine (deal)"},
		{name: "a plain removal", l: Line{Card: "c", Verb: "forget", Removed: true}, want: "c - -> off the table by the machine (forget)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, Render(tc.l))
		})
	}
}

func TestRenderLogCoverNoteLines(t *testing.T) {
	t.Parallel()
	rev := time.Date(2026, 1, 2, 8, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		l    Line
		want string
	}{
		{name: "a judgment lists its decisions", l: Line{Verb: "add", Note: &Note{Kind: Judgment, Type: "cards are done", What: "all landed\nwith more", Decisions: []string{"ship", "stop"}}}, want: "judgment: cards are done: all landed (more below) (decisions: ship, stop)"},
		{name: "an updated judgment", l: Line{Verb: "updated", Note: &Note{Kind: Judgment, Type: "a conflict", What: "twice"}}},
		{name: "a judgment answered", l: Line{Note: &Note{Kind: Decided, Type: "cards are done", Who: "coord", What: "coord: ship it"}}, want: "coord answered \"cards are done\": coord: ship it"},
		{name: "held until a review", l: Line{Note: &Note{Kind: Acknowledged, Type: "no member", Who: "coord", Review: rev}}, want: "\"no member\" held by coord until 08:00"},
		{name: "acknowledged without a review", l: Line{Note: &Note{Kind: Acknowledged, Type: "no member"}}, want: "\"no member\" acknowledged by the machine"},
		// refusal: a note of no kind says its type and its first line only.
		{name: "a happened note", l: Line{Note: &Note{Kind: Happened, Type: "the sprint is done", What: "all landed"}}, want: "the sprint is done: all landed"},
		{name: "a note with no words", l: Line{Note: &Note{Kind: Happened, Type: "a beat"}}, want: "a beat"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Render(tc.l)
			if tc.want == "" {
				assert.Equal(t, "judgment updated: a conflict: twice", got)
				return
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestRenderLogCoverSplitCost(t *testing.T) {
	t.Parallel()
	rec := func(key, card, who string, run int64, usd string) (string, string) {
		c := Consumer{Kind: "read", Card: card, Attempt: 1, Who: who, End: "ok", At: "2026-10-01T12:00:0" + string(rune('0'+run%10)) + "Z", Key: key,
			Usage: cardcost.Usage{Tokens: cardcost.Tokens{Input: 10}, Actual: usd, Run: run}}
		return FieldCostRecord + key, c.line()
	}
	k1, v1 := rec("a#v", "s1-1.r1.reader-a", "reader-a", 7, "0.1")
	k2, v2 := rec("b#v", "s1-1.r1.reader-b", "reader-b", 9, "0.2")
	t.Run("records come out as sorted lines, the rest stays", func(t *testing.T) {
		t.Parallel()
		l := Line{Card: "s1-1", Set: map[string]string{k1: v1, k2: v2, "head": "abc", FieldCostTotal: "0.3", FieldCostCut: "2"}}
		rest, words := SplitCost(l)
		require.Len(t, words, 2)
		require.True(t, slices.IsSorted(words), "the cost lines are sorted")
		assert.Equal(t,
			[]string{"s1-1 cost: read s1-1.r1.reader-a by reader-a, ok, ran 7s, cost $0.10",
				"s1-1 cost: read s1-1.r1.reader-b by reader-b, ok, ran 9s, cost $0.20"},
			words)
		assert.Equal(t, map[string]string{"head": "abc"}, rest.Set)
		assert.Equal(t, "s1-1", rest.Card)
	})
	cases := []struct {
		name string
		l    Line
	}{
		{name: "a note is returned as it is", l: Line{Set: map[string]string{k1: v1}, Note: &Note{Kind: Happened, Type: "t"}}},
		{name: "a line with no fields is returned as it is", l: Line{Card: "s1-1"}},
		{name: "a change with no records is returned as it is", l: Line{Card: "s1-1", Set: map[string]string{"head": "abc"}}},
	}
	for _, tc := range cases {
		t.Run("refusal: "+tc.name, func(t *testing.T) {
			t.Parallel()
			rest, words := SplitCost(tc.l)
			assert.Nil(t, words)
			assert.Equal(t, tc.l, rest)
		})
	}
}

func TestRenderLogCoverTimelineOrderAndDrops(t *testing.T) {
	t.Parallel()
	mk := func(table, card, from, to string, op string) Line {
		return Line{Table: table, Card: card, Primary: "s1-1", Op: op, From: from, To: to, Text: map[string]string{"note": "kept"}}
	}
	// one step's lines, as written: the primary's dealt line mirrors the work
	// card's and the queue line is the accept's own: both left out.
	work1 := mk(Work, "s1-1", "s1:"+string(Ready), "s1:"+string(Working), "op1")
	fleet1 := mk(Fleet, "s1-1.w3", "", "m1:"+string(Ready), "op1")
	read1 := mk(Readers, "s1-1.r1.reader-a", "reader-a:"+Asked, "reader-a:"+Reading, "op1")
	mergeQueued := mk(Merge, "s1-1", "", "s1:"+Queued, "op1")
	merge1 := mk(Merge, "s1-1", "s1:"+Queued, "s1:"+Merged, "op1")
	note1 := Line{Op: "op1", Note: &Note{Kind: Happened, Type: "a beat"}}
	passed := Line{Table: Fleet, Card: "s1-1.w3", Primary: "s1-1", Op: "op1", From: "m1:" + string(Ready), To: "m1:" + string(Ready)} // a field set in passing
	// a second step, written out of rank order: the story puts the primary's first.
	read2 := mk(Readers, "s1-1.r1.reader-a", "reader-a:"+Asked, "reader-a:"+Reading, "op2")
	fleet2 := mk(Fleet, "s1-1.w3", "", "m1:"+string(Ready), "op2")
	// a third step, kept small: its landed line belongs to the merge card.
	landed := mk(Work, "s1-1", "s1:"+string(Review), "s1:"+string(Landed), "op3")
	merge3 := mk(Merge, "s1-1", "s1:"+Queued, "s1:"+Merged, "op3")

	got := Timeline([]Line{work1, fleet1, read1, mergeQueued, merge1, note1, passed, read2, fleet2, landed, merge3}, "s1-1")

	kind := func(l Line) string {
		if l.Note != nil {
			return "note"
		}
		return l.Table
	}
	require.Len(t, got, 7)
	var gotTables []string
	for _, l := range got {
		gotTables = append(gotTables, kind(l))
	}
	// op1: the primary's dealt line is left to the work card's, the queue
	// line to the accept's, the passing field to the lines around it: fleet,
	// readers, merge, note stand, in rank order; op2's two lines sort with
	// the fleet card before the readers'; op3's landed line is the merge's.
	assert.Equal(t, []string{Fleet, Readers, Merge, "note", Fleet, Readers, Merge}, gotTables)
	for _, l := range got {
		assert.NotEqual(t, work1, l, "a dealt line mirrored by the work card is left out")
		assert.NotEqual(t, mergeQueued, l, "a queue line made at the accept is left out")
		assert.NotEqual(t, passed, l, "a field set in passing is left out")
		assert.NotEqual(t, landed, l, "a landed line mirrored by the merge card is left out")
	}
}

func TestRenderLogCoverTimelineLeavesOtherPrimaries(t *testing.T) {
	t.Parallel()
	// refusal: a line of another primary is not the story of this card: it stays
	// where it was written, only within its own op is the order held.
	foreign := Line{Table: Work, Card: "s2-1", Primary: "s2-1", Op: "op1", From: "s2:" + string(Ready), To: "s2:" + string(Working), Text: map[string]string{"note": "kept"}}
	mine := Line{Table: Fleet, Card: "s1-1.w3", Primary: "s1-1", Op: "op1", To: "m1:" + string(Ready), Text: map[string]string{"note": "kept"}}
	got := Timeline([]Line{foreign, mine}, "s1-1")
	require.Len(t, got, 2)
	assert.Equal(t, "s2-1", got[0].Card)
}
