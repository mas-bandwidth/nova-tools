package store

import (
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A card rises a tier by rework (nova-tools#5090; route.go, cardTier): `rework <id>
// --tier pro` writes the tier on the primary, the card is the persistent store, and
// every later deal of the card draws from that tier, the attempt the rework deals at
// once and every attempt after it, whatever its brief's line 1 says; a rework that
// finds no member with room leaves the primary ready with the tier, and the tick's deal
// draws from it. A pinned card runs on its pin whatever its tier, so --tier on it is
// refused, naming the pin.
func TestAReworkRaisesTheTierEveryLaterDealDrawsFrom(t *testing.T) {
	t.Parallel()
	routes := []sprint.Route{route("flash-a", "flash"), route("flash-b", "flash"), route("pro-a", "pro"), route("pro-b", "pro")}
	tierOf := func(name string) string {
		for _, r := range routes {
			if r.Name == name {
				return r.Tier
			}
		}
		return ""
	}
	rework := func(h *harness, tier string) []sprint.Refusal {
		h.t.Helper()
		return h.run(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Fix: "again", Tier: tier, Who: "tester"})).Refused
	}
	t.Run("dealt at once", func(t *testing.T) {
		t.Parallel()
		h := routeHarness(t, routes...)
		h.addReady("s1", 1, briefOf("flash", ""))
		h.must(DealStep(sprint.DealReq{}))
		var got []string
		for a := 1; a <= 4; a++ {
			pr := h.snap().Work.Card("s1-1")
			got = append(got, tierOf(h.workCards()[pr.F("work")].F(sprint.FieldRoute)))
			if a == 4 {
				break
			}
			h.finishAttempt("s1-1", true, "")
			tier := ""
			if a == 2 {
				tier = "pro" // a flash card that failed twice rises to pro
			}
			require.Empty(t, rework(h, tier))
		}
		assert.Equal(t, []string{"flash", "flash", "pro", "pro"}, got, "attempts 3 and 4 are drawn from pro, the tier the card keeps")
		assert.Equal(t, "pro", h.snap().Work.Card("s1-1").F(sprint.FieldTier), "the primary records its tier")
		h.clean("risen to pro")
	})
	t.Run("dealt by the tick", func(t *testing.T) {
		t.Parallel()
		h := routeHarness(t, routes...)
		h.addReady("s1", 1, briefOf("flash", ""))
		h.must(DealStep(sprint.DealReq{}))
		h.finishAttempt("s1-1", true, "")
		h.must(FleetStep(sprint.FleetReq{Op: "hold", Member: "m1"}))
		h.must(FleetStep(sprint.FleetReq{Op: "hold", Member: "m2"}))
		require.Empty(t, rework(h, "pro"))
		pr := h.snap().Work.Card("s1-1")
		require.Equal(t, sprint.Ready, pr.Col, "no member up: the rework leaves it ready")
		require.Equal(t, "pro", pr.F(sprint.FieldTier))
		h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
		h.must(DealStep(sprint.DealReq{}))
		wc := h.workCards()[h.snap().Work.Card("s1-1").F("work")]
		require.NotNil(t, wc)
		assert.Equal(t, "pro", tierOf(wc.F(sprint.FieldRoute)), "the deal reads the tier the card records: %s", wc.F(sprint.FieldRoute))
		h.clean("dealt on pro by the deal")
	})
	t.Run("a pinned card is refused", func(t *testing.T) {
		t.Parallel()
		h := routeHarness(t, routes...)
		h.addReady("s1", 1, briefOf("flash", "model: x/y\ntokens: 100\ndeadline: 60"))
		h.must(DealStep(sprint.DealReq{}))
		h.finishAttempt("s1-1", true, "")
		refused := rework(h, "pro")
		require.Len(t, refused, 1)
		assert.Contains(t, refused[0].Why, "pins model x/y")
		pr := h.snap().Work.Card("s1-1")
		assert.Equal(t, sprint.Review, pr.Col, "nothing moved")
		assert.Empty(t, pr.F(sprint.FieldTier), "nothing written")
	})
}

// boundBy ends the takes of the primary's live work card with the report until the attempt
// reaches its redeal bound (two identical takes, rule 2, or MaxRedeals redeals) and the tick
// raises the bound's judgment, as the overnight trace of 2026-10-03 shows each attempt end.
func (h *harness) boundBy(id, report string) {
	h.t.Helper()
	for range sprint.MaxRedeals + 1 {
		pr := h.snap().Work.Card(id)
		if pr.Col != sprint.Working {
			break // withdrawn at its bound: the deal places it no more
		}
		h.failTake(pr.F("work"), report)
		h.machine()
	}
	h.machine()
	require.Len(h.t, h.openOf(sprint.NBound), 1, "the attempt is at its bound")
}

// reworkOf is `nova-sprint rework s1-1 --fix <fix> [--tier <tier>]`, its refusals.
func (h *harness) reworkOf(fix, tier string) []sprint.Refusal {
	h.t.Helper()
	return h.run(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Fix: fix, Tier: tier, Who: "rowan"})).Refused
}

// The bound holds across attempts (the coordinator's finding of 2026-10-03: an answer loop
// reworked ci-03 231 times and docsd-03 244 times overnight, each rework a fresh attempt
// whose bound started again; docs/SPEC-SPRINT.md section 5). The loop's exact sequence: the
// attempt reaches its bound (two takes that left no result), a rework with a fix is accepted,
// the next attempt reaches the same bound the same way on the same tier, and the second
// rework is refused, one line naming the attempt before and the class and offering --tier,
// drop or wait; the judgment lists rework only on another tier. A rework with --tier naming
// another tier is accepted, and that tier counts its own failures; a drop is accepted.
func TestASecondReworkAtTheSameBoundIsRefused(t *testing.T) {
	t.Parallel()
	twoBounds := func(t *testing.T) *harness {
		h := flashAndPro(t)
		h.addReady("s1", 1, briefOf("flash", ""))
		h.startMachine()
		h.machine()
		h.boundBy("s1-1", noResultLine)
		open := h.openOf(sprint.NBound)
		assert.Equal(t, []string{"rework with a fix", "drop", "wait"}, open[0].Note.Decisions, "the first bound: a rework with a fix")
		require.Empty(t, h.reworkOf("run on the pro tier", ""), "the first rework at the bound is the next attempt")
		pr := h.snap().Work.Card("s1-1")
		require.Equal(t, 2, pr.Int("attempt"))
		assert.Equal(t, []string{"no result", "1", "flash"}, []string{pr.F(sprint.FieldFailure), pr.F(sprint.FieldFailureAt), pr.F(sprint.FieldFailureTier)},
			"the bound's end is the primary's record of its failed work")
		h.boundBy("s1-1", noResultLine)
		return h
	}
	refusal := "attempt 2 reached its bound the way attempt 1 ended (no result) on tier flash, and is not reworked there a third time"
	t.Run("a fix alone is refused", func(t *testing.T) {
		t.Parallel()
		h := twoBounds(t)
		open := h.openOf(sprint.NBound)
		require.Len(t, open, 1)
		assert.Equal(t, sprint.BoundAgainDecisions, open[0].Note.Decisions, "the judgment lists rework only on another tier")
		for _, tier := range []string{"", "flash"} {
			refused := h.reworkOf("run on the pro tier", tier)
			require.Len(t, refused, 1, "--tier %q", tier)
			assert.Contains(t, refused[0].Why, refusal)
			assert.Contains(t, refused[0].Why, "--tier <another tier>")
			assert.Contains(t, refused[0].Why, "nova-sprint drop s1-1")
			assert.Contains(t, refused[0].Why, "or wait")
			assert.NotContains(t, refused[0].Why, "\n", "one line")
		}
		pr := h.snap().Work.Card("s1-1")
		assert.Equal(t, 2, pr.Int("attempt"), "no third attempt")
		assert.Equal(t, sprint.Ready, pr.Col, "it stays at its bound")
		assert.Equal(t, 1, pr.Int("reworks"), "nothing written")
		h.machine()
		assert.Len(t, h.openOf(sprint.NBound), 1, "the judgment stays open")
		res := h.run(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
		require.Len(t, res.Refused, 1)
		assert.Contains(t, res.Refused[0].Why, refusal, "the deal verb says the same")
		h.clean("a second rework at the same bound refused")
	})
	t.Run("another tier is accepted, and counts its own failures", func(t *testing.T) {
		t.Parallel()
		h := twoBounds(t)
		require.Empty(t, h.reworkOf("run on the pro tier", "pro"))
		pr := h.snap().Work.Card("s1-1")
		require.Equal(t, 3, pr.Int("attempt"))
		assert.Equal(t, "pro", pr.F(sprint.FieldTier))
		assert.Equal(t, "pro", tierOfRoute(h.workCards()[pr.F("work")].F(sprint.FieldRoute)))
		h.machine()
		assert.Empty(t, h.openOf(sprint.NBound), "the rework closed the judgment")
		h.boundBy("s1-1", noResultLine)
		require.Empty(t, h.reworkOf("again on pro", ""), "the first bound on pro: the record is flash's")
		h.boundBy("s1-1", noResultLine)
		refused := h.reworkOf("again on pro", "")
		require.Len(t, refused, 1, "the second on pro is refused")
		assert.Contains(t, refused[0].Why, "attempt 4 reached its bound the way attempt 3 ended (no result) on tier pro")
		h.clean("the bound held on pro")
	})
	t.Run("a drop is accepted", func(t *testing.T) {
		t.Parallel()
		h := twoBounds(t)
		h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Reason: "dead on arrival", Who: "rowan"}))
		h.machine()
		assert.Empty(t, h.openOf(sprint.NBound))
		h.clean("dropped at its bound")
	})
}

// A withdrawn take's end counts toward rule 2 across attempts, not only a failed finish
// (the coordinator's finding of 2026-10-03: 240 bounds in a row were never the second
// identical failure, because only a failed finish wrote the primary's record). An attempt
// bounded by takes the provider failed ends with the provider's class; the next attempt
// bounded the same way is the second identical failure, and its rework on the same tier is
// refused; a bound that ended another way is not, and the rework is its next attempt.
func TestAWithdrawnTakeCountsTowardTheIdenticalFailure(t *testing.T) {
	t.Parallel()
	credit := cardhdr.EndProvider + ": provider: class=out-of-credit status=402 msg=Insufficient credits"
	start := func(t *testing.T) *harness {
		h := flashAndPro(t)
		h.addReady("s1", 1, briefOf("flash", ""))
		h.startMachine()
		h.machine()
		h.boundBy("s1-1", credit)
		require.Empty(t, h.reworkOf("again", ""))
		pr := h.snap().Work.Card("s1-1")
		assert.Equal(t, []string{"provider failure: class=out-of-credit", "1", "flash"},
			[]string{pr.F(sprint.FieldFailure), pr.F(sprint.FieldFailureAt), pr.F(sprint.FieldFailureTier)}, "the provider's class is the attempt's end")
		return h
	}
	t.Run("the same provider failure", func(t *testing.T) {
		t.Parallel()
		h := start(t)
		h.boundBy("s1-1", credit)
		refused := h.reworkOf("again", "")
		require.Len(t, refused, 1)
		assert.Contains(t, refused[0].Why, "attempt 2 reached its bound the way attempt 1 ended (provider failure: class=out-of-credit) on tier flash")
		assert.Equal(t, sprint.BoundAgainDecisions, h.openOf(sprint.NBound)[0].Note.Decisions)
	})
	t.Run("another end", func(t *testing.T) {
		t.Parallel()
		h := start(t)
		h.boundBy("s1-1", noResultLine)
		assert.Equal(t, []string{"rework with a fix", "drop", "wait"}, h.openOf(sprint.NBound)[0].Note.Decisions)
		require.Empty(t, h.reworkOf("again", ""), "no result after the provider: not the same failure")
		assert.Equal(t, 3, h.snap().Work.Card("s1-1").Int("attempt"))
	})
	for _, tc := range []struct {
		name    string
		redeals int
		takes   map[int]string
		class   string
	}{
		{"two takes with no result", 1, map[int]string{1: noResultLine, 2: noResultLine}, "no result"},
		{"the provider's class word", 3, map[int]string{4: credit}, "provider failure: class=out-of-credit"},
		{"a provider line with no class word", 3, map[int]string{4: providerLine}, "provider failure"},
		{"the last take, not the one before", 3, map[int]string{3: credit, 4: noResultLine}, "no result"},
		{"a last take with no record", 3, map[int]string{3: credit}, ""},
	} {
		t.Run("the class of "+tc.name, func(t *testing.T) {
			t.Parallel()
			f := map[string]string{"redeals": strconv.Itoa(tc.redeals), sprint.FieldTakeEnded: "t"}
			for n, line := range tc.takes {
				kind, rest, _ := strings.Cut(line, ": ")
				if kind == cardhdr.EndNoResult {
					rest = line // a no-result take keeps its kind in its record
				}
				f[sprint.FieldProviderTake+strconv.Itoa(n)] = sprint.ProviderTake{Route: "r", Error: rest}.String()
			}
			assert.Equal(t, tc.class, sprint.BoundClass(&sprint.Card{ID: "s1-1.w1", Fields: f}))
		})
	}
}
