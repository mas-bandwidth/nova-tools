package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Read tiers (docs/SPEC-SPRINT.md section 6; readtier.go; the owner, 2026-10-04). The floor
// is per attempt: a card's reads run at the tier its attempt ran on at least, the stream's
// read tier a floor over that and never a cap; `stream set --read-tier` raises and refuses
// to lower below the stream's work tier. The escalation: a landed card returned by dev or
// an audit, two readers disagreeing on one attempt, or a card alternating broken and ok
// across attempts are a stream's question "raise the read tier of <stream> to <next>?",
// which the rule answers and never asks (sprint.ReadTierRule;
// a-judgment-checks-the-lane-before-it-rises.w1): kept unless two substantive findings
// disagree, when the raise is recorded on the stream row with the rule's reason; it never
// lowers.

// quiet is the judgments the last tick kept from rising, as its heartbeat says them.
func (h *harness) quieted() []sprint.Quiet {
	h.t.Helper()
	_, hb, err := h.st.Machine(h.ctx)
	require.NoError(h.t, err)
	return hb.Quiet
}

// readTiersOf is the tiers the primary's live reads were drawn on, in reader row order.
func (h *harness) readTiersOf(id string) []string {
	h.t.Helper()
	var out []string
	for _, rc := range h.snap().Readers.Of(id) {
		out = append(out, rc.F(sprint.FieldTier))
	}
	return out
}

// tiersHarness is the harness with a route of every tier.
func tiersHarness(t *testing.T) *harness {
	return routeHarness(t, route("flash-a", "flash"), route("pro-a", "pro"), route("heavy-a", "heavy"))
}

func TestTheReadTierFloorIsPerAttemptAndAStreamSettingOnlyRaises(t *testing.T) {
	t.Parallel()
	h := tiersHarness(t)
	h.must(SetStep(sprint.SetReq{Attempts: "8", Who: h.st.Actor}))
	h.addReady("s1", 1, briefOf("flash", ""))
	h.must(DealStep(sprint.DealReq{}))
	// attempt 1 on flash: read at flash
	h.finishAttempt("s1-1", false, "h1")
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	assert.Equal(t, []string{"flash"}, h.readTiersOf("s1-1"), "a flash attempt is read at flash")
	h.readOne(h.askedRead("s1-1"), "broken", "internal/x.go:1: wrong")
	h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Fix: "f", Who: "tester"}))
	h.finishAttempt("s1-1", false, "h2")
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	h.readOne(h.askedRead("s1-1"), "broken", "internal/y.go:2: wrong too")
	// attempt 3 runs on pro: its reads are asked at pro, the stream's read tier (none: flash) no cap
	h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Fix: "g", Tier: "pro", Who: "tester"}))
	h.finishAttempt("s1-1", false, "h3")
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	assert.Equal(t, []string{"pro"}, h.readTiersOf("s1-1"), "the third attempt ran on pro: pro reads")
	// a stream read tier of heavy raises everything in it
	h.addReady("s1", 1, briefOf("flash", ""))
	res := h.must(SetStep(sprint.SetReq{Streams: []string{"s1"}, ReadTier: "heavy", Reason: "audited", Who: h.st.Actor}))
	assert.Equal(t, "stream s1 read-tier heavy", res.Moved[0])
	assert.Equal(t, "audited", h.snap().StreamCtl("s1").F(sprint.FieldReadTierReason), "the reason is recorded on the stream row")
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-2"}}}))
	h.finishAttempt("s1-2", false, "h1")
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-2"}}}))
	assert.Equal(t, []string{"heavy"}, h.readTiersOf("s1-2"), "a flash attempt in a heavy-read stream is read at heavy")
	// the floor: the stream's work tier is pro (s1-1 is on pro... its brief names flash; a pro
	// brief in the stream makes the work tier pro), and a read tier below it is refused in one line
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"s1-p"}, Brief: briefOf("pro", "")}))
	assert.Equal(t, "pro", sprint.StreamWorkTier(h.snap(), "s1"))
	r := h.run(SetStep(sprint.SetReq{Streams: []string{"s1"}, ReadTier: "flash", Who: h.st.Actor}))
	require.Len(t, r.Refused, 1)
	assert.Equal(t, "s1's work tier is pro: its read tier is never below it; run: nova-sprint stream set s1 --read-tier pro", r.Refused[0].Why)
	assert.Equal(t, "heavy", h.snap().StreamCtl("s1").F(sprint.FieldReadTier), "nothing lowered")
	require.Empty(t, h.run(SetStep(sprint.SetReq{Streams: []string{"s1"}, ReadTier: "pro", Who: h.st.Actor})).Refused, "at the work tier: allowed (a setting never lowers a read below the attempt's tier)")
	h.clean("the floor")
}

func TestReadersDisagreeingRaiseTheReadTierByRule(t *testing.T) {
	t.Parallel()
	h := tiersHarness(t)
	h.addReady("s1", 2, briefOf("pro", ""))
	for _, id := range []string{"s1-1", "s1-2"} {
		h.setPrimary(id, map[string]string{sprint.FieldTierNow: "pro"})
	}
	h.startMachine()
	h.must(DealStep(sprint.DealReq{}))
	for _, id := range []string{"s1-1", "s1-2"} {
		h.finishAttempt(id, false, "h-"+id)
		rc := h.pairAsked(id)
		h.readOne(rc[0], "ok", "")
		h.readOne(rc[1], "broken", "internal/z.go:3: wrong")
	}
	h.machine()
	assert.Empty(t, h.openOf(sprint.NRaiseReadTier), "never asked")
	assert.Zero(t, h.written(sprint.NRaiseReadTier))
	// two substantive findings disagree: the rule raises it, with its reason on the stream row
	why := "answered by rule read-tier: raised to heavy: two substantive findings disagree on s1-1 attempt 1 (reader-a ok, reader-b broken with a finding)"
	ctl := h.snap().StreamCtl("s1")
	assert.Equal(t, "heavy", ctl.F(sprint.FieldReadTier))
	assert.Equal(t, why, ctl.F(sprint.FieldReadTierReason))
	assert.Equal(t, 1, h.written(sprint.NRuleAnswered), "the rule's answer is a note")
	assert.Contains(t, h.quieted(), sprint.Quiet{Type: sprint.NRaiseReadTier, Subject: sprint.StreamSubject("s1"), Why: why}, "counted beside the judgments that rose")
	h.machine()
	assert.Empty(t, h.openOf(sprint.NRaiseReadTier), "heavy is the top: no further raise")
	assert.Equal(t, 1, h.written(sprint.NRuleAnswered), "raised once")
	assert.Empty(t, h.quieted(), "nothing left to answer")
	h.clean("raised")
}

func TestALandedCardReturnedByDevKeepsTheReadTierByRule(t *testing.T) {
	t.Parallel()
	h := tiersHarness(t)
	h.addReady("s1", 1, briefOf("flash", ""))
	h.startMachine()
	h.must(DealStep(sprint.DealReq{}))
	h.finishAttempt("s1-1", false, "h1")
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	h.readAllOK("s1-1")
	h.must(AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	h.must(MergeStep(sprint.MergeReq{Stream: "s1", Batch: 10}))
	require.Equal(t, sprint.Landed, h.state("s1-1"))
	r := h.run(PromotedStep(sprint.PromotedReq{Sha: "abc1234", Returned: []string{"s1-2"}, Who: h.st.Actor}))
	require.Len(t, r.Refused, 1, "a card that is not on the table")
	res := h.must(PromotedStep(sprint.PromotedReq{Sha: "abc1234", Returned: []string{"s1-1"}, Who: h.st.Actor}))
	assert.Contains(t, res.Moved[0], "returned by dev: s1-1")
	h.machine() // the promotion's work-table change drains, and the tick reads it
	h.machine()
	pr := h.snap().Work.Card("s1-1")
	assert.NotEmpty(t, pr.F(sprint.FieldReturnedByDev))
	assert.Equal(t, "flash", pr.F(sprint.FieldReturnedByDevTier))
	assert.Equal(t, "abc1234", pr.F(sprint.FieldReturnedByDevSha))
	// no two findings disagree: the rule keeps the read tier, and the question is never asked
	assert.Empty(t, h.openOf(sprint.NRaiseReadTier))
	assert.Zero(t, h.written(sprint.NRaiseReadTier), "never asked")
	assert.Empty(t, h.snap().StreamCtl("s1").F(sprint.FieldReadTier), "kept")
	quiet := h.quieted()
	require.Len(t, quiet, 1)
	assert.Equal(t, sprint.NRaiseReadTier, quiet[0].Type)
	assert.Contains(t, quiet[0].Why, "answered by rule read-tier: kept at its tier: raise the read tier of s1 to pro? s1-1 landed and was returned by dev or an audit at ")
	h.machine()
	assert.Zero(t, h.written(sprint.NRaiseReadTier), "never asked")
	h.clean("returned by dev, kept")
}

func TestACardAlternatingBrokenAndOkKeepsTheReadTierByRule(t *testing.T) {
	t.Parallel()
	h := tiersHarness(t)
	h.addReady("s1", 1, briefOf("flash", ""))
	h.startMachine()
	h.must(DealStep(sprint.DealReq{}))
	// attempt 1 read ok, accepted, then returned to review by the coordinator and reworked:
	// the rework keeps the head a reader passed (FieldPassedHead)
	h.finishAttempt("s1-1", false, "h1")
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	h.readAllOK("s1-1")
	h.must(AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	h.must(ReturnStep(sprint.ReturnReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Reason: "an audit found a hole"}))
	h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Fix: "close the hole", Who: "tester"}))
	require.Equal(t, "h1", h.snap().Work.Card("s1-1").F(sprint.FieldPassedHead))
	// attempt 2 found broken: ok then broken across attempts
	h.finishAttempt("s1-1", false, "h2")
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	h.readOne(h.askedRead("s1-1"), "broken", "internal/w.go:4: the hole is open")
	h.machine()
	// ok and broken on different attempts are no two findings disagreeing on one: kept
	assert.Empty(t, h.openOf(sprint.NRaiseReadTier))
	assert.Zero(t, h.written(sprint.NRaiseReadTier), "never asked")
	assert.Empty(t, h.snap().StreamCtl("s1").F(sprint.FieldReadTier), "kept")
	assert.Contains(t, h.quieted(), sprint.Quiet{Type: sprint.NRaiseReadTier, Subject: sprint.StreamSubject("s1"),
		Why: "answered by rule read-tier: kept at its tier: raise the read tier of s1 to pro? s1-1 alternates broken and ok across attempts: a reader passed it at h1 and attempt 2 was found broken by reader-b; no two substantive findings disagree"})
	h.clean("alternating")
}
