package sprint

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// costs retier (retier.go): the backfill of the records written before records carried a
// tier, the tier totals, and an api friend's usage; idempotent.

// oldLine is a record as written before records carried a tier: no on_tier word at all.
func oldLine(card, who, route, model, usage string) string {
	l := Consumer{Kind: "work", Card: card, Attempt: 1, Who: who, Route: route, Model: model, End: "ok", At: "2026-10-04T15:00:00Z", Usage: cardcost.ParseUsage(usage)}.line()
	return strings.Replace(l, " on_tier=- ", " ", 1)
}

func retierWorld() *Snapshot {
	w := NewTable(Work)
	w.SetRows([]string{"s1"})
	m := NewTable(Merge)
	m.SetRows([]string{"s1"})
	m.Put(&Card{ID: CtlID("s1"), Row: "s1", Col: "ctl", Fields: map[string]string{FieldCost: "3.5"}})
	w.Put(&Card{ID: "s1-1", Row: "s1", Col: Landed, Fields: map[string]string{
		"brief":                "RESULT: s1-1 tier: pro",
		FieldCost:              "3.5",
		FieldCostTotal:         "records=3 charged_usd=3.5 charged_of=2",
		FieldCostRecord + "a":  oldLine("s1-1.w1", "m1", "pro-grok47-openrouter", "", "input=1 actual_usd=2 actual_by=harness"),
		FieldCostRecord + "b":  oldLine("s1-1.r1", "reader-a", "", "opencode/qwen3.8-flash", "input=1 actual_usd=1.5 actual_by=harness"),
		FieldCostRecord + "fr": oldLine("s1-1.w2", "friend.freddy", "", "", "run=60s"),
	}})
	w.Put(&Card{ID: "s1-2", Row: "s1", Col: Landed, Fields: map[string]string{"brief": "RESULT: s1-2 tier: flash"}})
	return &Snapshot{Work: w, Merge: m, Routes: []Route{
		{Name: "pro-grok47-openrouter", Tier: "pro", Provider: "openrouter", Model: "x-ai/grok-4.7"},
		{Name: "flash-q", Tier: "flash", Provider: "opencode", Model: "qwen3.8-flash"},
		{Name: "flash-mercury", Tier: "flash", Provider: "inception", Model: "mercury-2.5", Prices: cardcost.Prices{Input: "0.25", Output: "1"}},
	}}
}

func TestRetierTiersEveryRecordAndPricesTheApiFriend(t *testing.T) {
	t.Parallel()
	s := retierWorld()
	freddy := "input=1000 output=100 model=inception/mercury-2.5 actual_usd=0.01 actual_by=harness"
	res := Retier(s, RetierReq{FriendUsage: map[string]string{"fr": freddy}})
	require.Len(t, res.Streams, 1)
	rs := res.Streams[0]
	assert.Equal(t, 1, rs.Cards)
	assert.Equal(t, 3, rs.Records)
	assert.Equal(t, map[string]string{NoTierWord: "$3.50"}, rs.Before, "nothing had its own tier")
	assert.Equal(t, map[string]string{"pro": "$2.00", "flash": "$1.51"}, rs.After, "the route's, the model's, the friend's model's")
	assert.Equal(t, "$3.50", rs.Was)
	assert.Equal(t, "$3.51", rs.Cost)
	assert.Empty(t, rs.Guard, "written: no read-time rule")
	assert.Equal(t, map[string]string{"fr": "0.01"}, res.Priced)
	line := RetierLine(rs)
	assert.Equal(t, "RETIER stream=s1 cards=1 records=3 cost=$3.50->$3.51 before=no_tier:$3.50 after=flash:$1.51,pro:$2.00", line)
	// the writes: every record tiered, the totals, the cost and the stream's
	var ctlSet, cardSet map[string]string
	for _, u := range res.Plan.Units {
		for _, c := range u.Changes {
			switch c.Entry.ID {
			case CtlID("s1"):
				ctlSet = c.Entry.Set
			case "s1-1":
				cardSet = c.Entry.Set
			}
		}
	}
	require.NotNil(t, cardSet)
	assert.Equal(t, "3.51", cardSet[FieldCost])
	assert.Equal(t, "3.51", ctlSet[FieldCost], "the stream's landed cost follows its card")
	assert.Equal(t, "2", cardSet[FieldCostTier+"pro"])
	assert.Equal(t, "1.51", cardSet[FieldCostTier+"flash"])
	for k, v := range cardSet {
		if strings.HasPrefix(k, FieldCostRecord) {
			assert.Contains(t, v, "on_tier=", k)
			assert.NotContains(t, v, "on_tier=-", k)
		}
	}
	// a second run on what was written writes nothing
	after := &Snapshot{Work: NewTable(Work), Merge: s.Merge, Routes: s.Routes}
	after.Work.SetRows([]string{"s1"})
	after.Work.Put(withFields(s.Work.Card("s1-1"), cardSet, nil))
	after.Work.Put(s.Work.Card("s1-2"))
	again := Retier(after, RetierReq{FriendUsage: map[string]string{"fr": freddy}})
	assert.Empty(t, again.Plan.Units, "idempotent")
	assert.Equal(t, map[string]string{"flash": "$1.51", "pro": "$2.00"}, again.Streams[0].Before)
}

// A friend record no usage was found for stays unpriced (never estimated), and --cards bounds
// the primaries one run writes.
func TestRetierBoundsItsWritesAndEstimatesNothing(t *testing.T) {
	t.Parallel()
	s := retierWorld()
	res := Retier(s, RetierReq{Max: 0})
	assert.Empty(t, res.Priced)
	assert.Equal(t, "$3.50", res.Streams[0].Cost)
	assert.Empty(t, res.Streams[0].Was)
	none := Retier(s, RetierReq{Max: 1})
	assert.Len(t, none.Plan.Units, 1)
	s.Work.Put(&Card{ID: "s1-3", Row: "s1", Col: Landed, Fields: map[string]string{"brief": "RESULT: s1-3", FieldCost: "1",
		FieldCostTotal: "records=1 charged_usd=1 charged_of=1", FieldCostRecord + "z": oldLine("s1-3.w1", "m1", "flash-q", "", "input=1 actual_usd=1 actual_by=harness")}})
	bounded := Retier(s, RetierReq{Max: 1})
	assert.Len(t, bounded.Plan.Units, 1)
	assert.Equal(t, 1, bounded.Left)
}

func TestWithTierWordFillsTheTierOfEitherShape(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "kind=work on_model=m on_tier=pro end=ok", withTierWord("kind=work on_model=m end=ok", "pro"))
	assert.Equal(t, "kind=work on_model=m on_tier=flash end=ok", withTierWord("kind=work on_model=m on_tier=- end=ok", "flash"))
	assert.Equal(t, "heavy", parseConsumer("k", withTierWord("kind=work on_route=r on_model=-", "heavy")).Tier, "read back")
}
