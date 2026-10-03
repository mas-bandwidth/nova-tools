package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// judge is a fake backend for the judgment decision: by reads each state and says the
// verb, its probability, the fix and the reason; every ask is counted and kept.
type judge struct {
	mu     sync.Mutex
	states []string
	by     func(state string) (verb string, p float64, fix, reason string)
}

func (j *judge) Name() string { return "fake" }

func (j *judge) Ask(_ context.Context, s decide.Schema, state string) (map[string]decide.Answer, decide.Usage, error) {
	j.mu.Lock()
	j.states = append(j.states, state)
	j.mu.Unlock()
	verb, p, fix, reason := j.by(state)
	choice := func(v string, p float64) decide.Answer {
		return decide.Answer{Type: decide.Choice, Value: v, P: map[string]float64{v: p}}
	}
	return map[string]decide.Answer{"verb": choice(verb, p), "fix": choice(fix, 0.9), "reason": choice(reason, 0.9)}, decide.Usage{InputTokens: len(state) / 4}, nil
}

func (j *judge) asks() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.states)
}

// answering is a test sprint with a fake judgment backend and a record of its own.
func answering(t *testing.T, by func(state string) (string, float64, string, string)) (*testApp, *judge, string) {
	t.Helper()
	ta := newTestApp(t)
	j := &judge{by: by}
	ta.a.decider = j
	ta.ok("init --readers reader-a,reader-b --members m1")
	return ta, j, filepath.Join(t.TempDir(), "judgment.jsonl")
}

// always is a judge that answers every state the same.
func always(verb string, p float64, fix, reason string) func(string) (string, float64, string, string) {
	return func(string) (string, float64, string, string) { return verb, p, fix, reason }
}

// recorded is the judgment decisions of the record, by id.
func recorded(t *testing.T, record string) map[string]decide.Decision {
	t.Helper()
	ds, err := decide.Load(record)
	require.NoError(t, err)
	out := map[string]decide.Decision{}
	for _, d := range ds {
		out[d.ID] = d
	}
	return out
}

// A grouped judgment is answered card by card: two cards that came back failed in one
// note are each asked over their own state, each reworked by the line the inbox prints
// for that card alone, and each recorded under <note id>:<card>; the judgment is gone,
// and a second pass asks nothing.
func TestAnswerDecideReworksEachCardOfAGroupedJudgment(t *testing.T) {
	t.Parallel()
	ta, j, record := answering(t, always(decide.VerbRework, 0.93, "own", "cannot-be-done"))
	ta.ok("add --stream s1 --count 2")
	ta.deal(2)
	ta.ok("take --as m1 --limit 2")
	ta.ok("finish --as m1 s1-1.w1@1 s1-2.w1@1 --failed --report 'the tests went red'")
	g := ta.group(sprint.NWorkFailed, "s1")
	require.Equal(t, 2, g.Size)
	out := ta.ok("answer --decide --bar 0.8 --record " + record)
	assert.Contains(t, out, g.ID+"  s1-1  failed  rework  0.93  applied  nova-sprint rework s1-1")
	assert.Contains(t, out, g.ID+"  s1-2  failed  rework  0.93  applied  nova-sprint rework s1-2")
	assert.Contains(t, out, "ANSWER OK rows=2 applied=2 would_apply=0 listed=0 refused=0 failed=0 left=0 outcomes=0 bar=0.80")
	require.Len(t, j.states, 2)
	assert.Contains(t, j.states[0], "kind: work came back failed\ncard: s1-1 (one of 2 cards in this judgment)\ntext: the tests went red\n")
	assert.Contains(t, j.states[0], "ALLOWED VERBS (the answer is one of these):\nrework, drop\n")
	assert.Contains(t, j.states[0], "judgment: work came back failed", "the card's own log is its history")
	ds := recorded(t, record)
	require.Len(t, ds, 2)
	d := ds[g.Notes[0]+":s1-2"]
	assert.Equal(t, map[string]string{"judgment": g.ID, "note": g.Notes[0], "kind": "failed", "card": "s1-2", "act": "applied", "verb": "rework"}, d.Inputs)
	assert.Equal(t, decide.JudgmentSchema().Hash(), d.Schema)
	for _, g := range ta.inboxGroups() {
		assert.NotEqual(t, sprint.Judgment, g.Kind, "nothing is left open: %+v", g)
	}
	out = ta.ok("answer --decide --bar 0.8 --record " + record)
	assert.Contains(t, out, "ANSWER OK rows=0 ")
	assert.Equal(t, 2, j.asks(), "a pass with nothing open asks nothing")
}

// A drop, however sure, and anything under the bar are listed, never applied: the
// judgment stays open, each decision is recorded as listed, and the next pass answers
// each from the record without asking again.
func TestAnswerDecideListsDropsAndWhatIsUnderTheBar(t *testing.T) {
	t.Parallel()
	ta, j, record := answering(t, func(state string) (string, float64, string, string) {
		if strings.Contains(state, "card: s1-1 ") {
			return decide.VerbDrop, 0.99, "own", "already-done"
		}
		return decide.VerbRework, 0.6, "own", "cannot-be-done"
	})
	ta.ok("add --stream s1 --count 2")
	ta.deal(2)
	ta.failOnce("m1", "s1-1.w1@1", "nothing to do: already applied")
	ta.failOnce("m1", "s1-2.w1@1", "the build broke")
	out := ta.ok("answer --decide --bar 0.8 --record " + record)
	assert.Contains(t, out, "s1-1  failed  drop    0.99  listed  a drop is the coordinator's: the listed edits were already made")
	assert.Contains(t, out, "s1-2  failed  rework  0.60  listed  p=0.60 is under the bar 0.80")
	assert.Contains(t, out, "applied=0 would_apply=0 listed=2")
	assert.Len(t, ta.group(sprint.NWorkFailed, "s1").Primaries, 2, "both still wait for the coordinator")
	for _, d := range recorded(t, record) {
		assert.Equal(t, "listed", d.Inputs["act"], d.ID)
	}
	out = ta.ok("answer --decide --bar 0.5 --record " + record)
	assert.Equal(t, 2, j.asks(), "a decided card is answered from the record")
	assert.Contains(t, out, "s1-2  failed  rework  0.60  applied  nova-sprint rework s1-2", "the record's answer meets the lower bar")
}

// A provider's refusal for want of payment is never answered: the card is listed, a
// payment being the owner's, the backend is not asked and nothing is recorded.
func TestAnswerDecideNeverAnswersAPaymentRefusal(t *testing.T) {
	t.Parallel()
	ta, j, record := answering(t, always(decide.VerbRework, 0.99, "retry", "cannot-be-done"))
	ta.ok("add --stream s1 --count 1")
	ta.deal(1)
	ta.failOnce("m1", "s1-1.w1@1", `provider: 402 "Insufficient account funds"`)
	out := ta.ok("answer --decide --bar 0.8 --record " + record)
	assert.Contains(t, out, "s1-1  failed  -     -  listed  a provider refused it for want of payment (402, out of credit): a payment is the owner's")
	assert.Zero(t, j.asks())
	_, err := os.Stat(record)
	assert.True(t, os.IsNotExist(err), "nothing is recorded")
	ta.group(sprint.NWorkFailed, "s1")
}

// A blocked card the decision acks is acked by the line the inbox prints, with the
// decision's reason: its dropped need is waived and the card runs.
func TestAnswerDecideAcksABlockedCard(t *testing.T) {
	t.Parallel()
	ta, _, record := answering(t, always(decide.VerbAck, 0.88, "own", "need-dropped"))
	ta.ok("add --stream s1 --count 1")
	ta.ok("add --stream s2 b --needs s1-1")
	ta.ok("drop s1-1 --reason obsolete")
	g := ta.group(sprint.NBlocked, "s2")
	out := ta.ok("answer --decide --bar 0.8 --record " + record)
	assert.Contains(t, out, g.ID+"  b     blocked  ack   0.88  applied  nova-sprint ack "+g.Notes[0]+" --reason 'nova-decide (p=0.88): the card can run without the dropped need; a conflict is handled at merge'")
	assert.Contains(t, ta.ok("card b"), "needs s1-1 (off the table (dropped)), waived")
	for _, g := range ta.inboxGroups() {
		assert.False(t, g.Kind == sprint.Judgment && g.Type == sprint.NBlocked, "the judgment is answered: %+v", g)
	}
}

// --dry-run asks and says what it would apply, and applies nothing and writes no record.
func TestAnswerDecideDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	ta, j, record := answering(t, always(decide.VerbRework, 0.95, "own", "cannot-be-done"))
	ta.ok("add --stream s1 --count 1")
	ta.deal(1)
	ta.failOnce("m1", "s1-1.w1@1", "the tests went red")
	before := ta.applies()
	out := ta.ok("answer --decide --bar 0.8 --dry-run --record " + record)
	assert.Contains(t, out, "s1-1  failed  rework  0.95  would-apply  nova-sprint rework s1-1")
	assert.Contains(t, out, "ANSWER DRY-RUN rows=1 applied=0 would_apply=1 ")
	assert.Equal(t, 1, j.asks())
	assert.Equal(t, before, ta.applies(), "the sprint is not written")
	_, err := os.Stat(record)
	assert.True(t, os.IsNotExist(err), "no record is written")
}

// The outcome is attached once the card says it: a rework that came back failed again is
// came-back, and a listed drop the coordinator made is dropped; --json carries both.
func TestAnswerDecideAttachesTheOutcome(t *testing.T) {
	t.Parallel()
	ta, _, record := answering(t, func(state string) (string, float64, string, string) {
		if strings.Contains(state, "card: s1-2 ") {
			return decide.VerbDrop, 0.9, "own", "cannot-be-done"
		}
		return decide.VerbRework, 0.9, "own", "cannot-be-done"
	})
	ta.ok("add --stream s1 --count 2")
	ta.deal(2)
	ta.failOnce("m1", "s1-1.w1@1", "red")
	ta.failOnce("m1", "s1-2.w1@1", "the brief names lines that are not there")
	ta.ok("answer --decide --bar 0.8 --record " + record)
	ta.ok("drop s1-2 --reason 'the brief is wrong'")
	ta.ok("tick")
	ta.failOnce("m1", "s1-1.w2@1", "red again")
	var p answerPass
	require.NoError(t, json.Unmarshal([]byte(ta.ok("answer --decide --bar 0.8 --json --record "+record)), &p))
	labels := map[string]string{}
	for _, o := range p.Outcomes {
		labels[o.Card] = o.Label
	}
	assert.Equal(t, map[string]string{"s1-1": "came-back", "s1-2": "dropped"}, labels)
	for _, d := range recorded(t, record) {
		if d.Inputs["card"] == "s1-2" {
			require.NotNil(t, d.Outcome)
			assert.Equal(t, "dropped", d.Outcome.Label)
		}
	}
}

// The loop form runs a pass every --every on the injected clock and ends when the machine
// is STOPPED: a judgment raised between passes is answered by the next one.
func TestAnswerDecideLoopEndsWhenTheMachineStops(t *testing.T) {
	t.Parallel()
	ta, j, record := answering(t, always(decide.VerbRework, 0.9, "own", "cannot-be-done"))
	ta.ok("add --stream s1 --count 2")
	ta.ok("start")
	ta.deal(2)
	ta.failOnce("m1", "s1-1.w1@1", "red")
	ta.atSleep(func(n int) {
		ta.ok("tick") // the run loop's tick between passes: the machine is running
		switch n {
		case 1: // as failOnce, whose own sleep would come back here
			ta.ok("take --as m1 s1-2.w1@1")
			ta.ok("finish --as m1 s1-2.w1@1 --failed --report red")
		case 2:
			ta.ok("stop")
		}
	})
	start := ta.a.now()
	out := ta.ok("answer --decide --bar 0.8 --every 60s --record " + record)
	assert.Equal(t, 3, strings.Count(out, "ANSWER OK "), out)
	assert.Contains(t, out, "s1-1  failed  rework  0.90  applied")
	assert.Contains(t, out, "s1-2  failed  rework  0.90  applied")
	assert.True(t, strings.HasSuffix(out, "ANSWER STOPPED STOPPED: the loop ends\n"), out)
	assert.Equal(t, 2, j.asks())
	assert.GreaterOrEqual(t, ta.a.now().Sub(start), 2*time.Minute, "two sleeps of the injected clock")
}

// The answers are the coordinator's alone, and answer wants --decide; a bad bar is refused.
func TestAnswerDecideRefusals(t *testing.T) {
	t.Parallel()
	ta, _, record := answering(t, always(decide.VerbRework, 0.9, "own", "cannot-be-done"))
	for line, want := range map[string]string{
		"answer --decide --actor intruder --record " + record: "nova-sprint answer REFUSED: answer is the coordinator's alone: coordinator, not intruder; nothing was changed",
		"answer --record " + record:                           "answer wants --decide",
		"answer --decide --bar 1.5 --record " + record:        "--bar: decide_judgment_bar \"1.5\" is not a probability",
		"answer --decide --backend fixed --record " + record:  "--answers <file> goes with --backend fixed",
	} {
		code, out, errs := ta.do(line)
		assert.Equal(t, 2, code, line)
		assert.Empty(t, out, line)
		assert.Contains(t, errs, want, line)
	}
}

// The bar is the sprint row's when --bar is not given, as nova-config applied it.
func TestAnswerDecideReadsTheSprintRowsBar(t *testing.T) {
	t.Parallel()
	ta, _, record := answering(t, always(decide.VerbRework, 0.85, "own", "cannot-be-done"))
	ta.m.SetJudgmentBar("0.9")
	ta.m.SetRoutes([]sprint.Route{{Name: "r1", Tier: "flash", Model: "m", Enabled: true}})
	ta.ok("add --stream s1 --count 1")
	ta.deal(1)
	ta.failOnce("m1", "s1-1.w1@1", "red")
	out := ta.ok("answer --decide --record " + record)
	assert.Contains(t, out, "p=0.85 is under the bar 0.90")
	assert.Contains(t, out, "bar=0.90")
}

// Through the sprint's server, answer opens no store: every read and every answer is a
// verb sent to the server, as the coordinator's shell sends them.
func TestAnswerDecideRunsThroughTheServer(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, "init --readers reader-a,reader-b --members m1:2", "add --stream s1 --count 1", "start", "tick", "tick")
	r.one("take", "--as", "m1", "--limit", "1", "--epoch", "0")
	r.one("finish", "--as", "m1", "s1-1.w1@1", "--failed", "--report", "red", "--epoch", "0")
	r.boss("tick")
	var sent [][]string
	c, boss := clientOf(t, r, "boss", &sent)
	c.decider = &judge{by: always(decide.VerbRework, 0.9, "own", "cannot-be-done")}
	code, out, errs := boss("answer", "--decide", "--bar", "0.8", "--record", filepath.Join(t.TempDir(), "j.jsonl"))
	require.Equal(t, 0, code, "%s%s", out, errs)
	assert.Contains(t, out, "s1-1  failed  rework  0.90  applied  nova-sprint rework s1-1")
	var verbs []string
	for _, v := range sent {
		verbs = append(verbs, v[0])
	}
	assert.Subset(t, verbs, []string{"inbox", "card", "log", "rework"})
	assert.Contains(t, r.boss("log --card s1-1"), "reworked by boss")
}

// One card of a grouped judgment is answered by the lines the inbox prints for a group of
// that card alone, the card named where a group's form would name the note: the bound's
// rework carries the chosen fix, its wait names the note, and an ack of a note of several
// cards, or a fix the decision did not give, is not applied.
func TestAGroupedJudgmentsCardHasItsOwnLines(t *testing.T) {
	t.Parallel()
	n := sprint.Note{ID: "tick-bound-1.1", Kind: sprint.Judgment, Type: sprint.NBound, Stream: "s1", Primaries: []string{"s1-1", "s1-2"},
		Decisions: sprint.TickDecisions[sprint.NBound]}
	cmds := cardCommands(n, "s1-2")
	assert.Equal(t, []string{decide.VerbRework, decide.VerbDrop, decide.VerbWait}, allowedVerbs(cmds))
	lines, why := fill(commandOf(cmds, decide.VerbRework), decide.Chosen{Verb: decide.VerbRework, Fix: "pro-tier"})
	require.Empty(t, why)
	assert.Equal(t, [][]string{{"rework", "s1-2", "--fix", decide.Fixes["pro-tier"]}}, lines)
	lines, why = fill(commandOf(cmds, decide.VerbWait), decide.Chosen{Verb: decide.VerbWait})
	require.Empty(t, why)
	assert.Equal(t, [][]string{{"wait", "tick-bound-1.1", "--for", "30m"}}, lines)
	_, why = fill(commandOf(cmds, decide.VerbRework), decide.Chosen{Verb: decide.VerbRework, Fix: "own"})
	assert.Equal(t, "rework wants <fix>, which the decision did not give (fix=own)", why)
	ready := sprint.Note{ID: "accept-1.1", Kind: sprint.Judgment, Type: sprint.NReadyToAccept, Stream: "s1", Primaries: []string{"s1-1", "s1-3"}, Decisions: sprint.Decisions[sprint.NReadyToAccept]}
	assert.Equal(t, []string{"nova-sprint accept s1-3 --answers accept-1.1"}, commandOf(cardCommands(ready, "s1-3"), decide.VerbAccept))
	ws, err := words("nova-sprint drop s1-1 --reason '<why>' --answers x")
	require.NoError(t, err)
	assert.Equal(t, []string{"nova-sprint", "drop", "s1-1", "--reason", "<why>", "--answers", "x"}, ws)
	assert.Equal(t, "rework s1-1 --fix 'it'\\''s red'", quoteLine([]string{"rework", "s1-1", "--fix", "it's red"}))
}

// A card the decision reworked is not reworked again within the hour: back at once, it is
// listed for the coordinator; back an hour on, it is the decision's again.
func TestAnswerDecideReworksACardAtMostOnceAnHour(t *testing.T) {
	t.Parallel()
	ta, _, record := answering(t, always(decide.VerbRework, 0.95, "retry", "cannot-be-done"))
	ta.ok("add --stream s1 --count 1")
	ta.deal(1)
	ta.failOnce("m1", "s1-1.w1@1", "red")
	assert.Contains(t, ta.ok("answer --decide --bar 0.8 --record "+record), "s1-1  failed  rework  0.95  applied")
	ta.failOnce("m1", "s1-1.w2@1", "the build broke")
	assert.Contains(t, ta.ok("answer --decide --bar 0.8 --record "+record), "s1-1  failed  rework  0.95  listed  nova-decide reworked it at ")
	ta.ok("rework s1-1 --fix 'the coordinator looked'")
	ta.a.sleep(time.Hour)
	ta.failOnce("m1", "s1-1.w3@1", "the gate timed out")
	assert.Contains(t, ta.ok("answer --decide --bar 0.8 --record "+record), "s1-1  failed  rework  0.95  applied")
}

// The sprint row's bar ships empty (the owner, 2026-10-03: "same rule as the other
// layers"): with no bar set and none given, answer applies nothing; it asks, records every
// decision as listed, and says what a bar would apply. The same decisions are applied from
// the record, asking nothing, once a bar is given.
func TestAnswerDecideAppliesNothingWithNoBar(t *testing.T) {
	t.Parallel()
	ta, j, record := answering(t, always(decide.VerbRework, 0.91, "own", "cannot-be-done"))
	ta.ok("add --stream s1 --count 1")
	ta.deal(1)
	ta.failOnce("m1", "s1-1.w1@1", "red")
	before := ta.applies()
	out := ta.ok("answer --decide --record " + record)
	assert.Contains(t, out, "s1-1  failed  rework  0.91  listed  no decide_judgment_bar is set, so nothing is applied; at a bar at or under 0.91 it would apply: nova-sprint rework s1-1")
	assert.Contains(t, out, "applied=0 would_apply=0 listed=1 refused=0 failed=0 left=0 outcomes=0 bar=- ")
	assert.Equal(t, before, ta.applies(), "the sprint is not written")
	var p answerPass
	require.NoError(t, json.Unmarshal([]byte(ta.ok("answer --decide --json --record "+record)), &p))
	assert.Nil(t, p.Bar, "--json says no bar as null")
	for _, d := range recorded(t, record) {
		assert.Equal(t, "listed", d.Inputs["act"], "recorded: %s", d.ID)
	}
	assert.Contains(t, ta.ok("answer --decide --bar 0.9 --record "+record), "s1-1  failed  rework  0.91  applied  nova-sprint rework s1-1")
	assert.Equal(t, 1, j.asks(), "the bar applies the recorded decision; nothing is asked again")
}
