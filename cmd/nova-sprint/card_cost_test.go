package main

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// What a card cost (internal/sprint/cost.go; docs/SPEC-SPRINT.md, "What a card cost"),
// on the twin: one producer whose first attempt failed and whose second was read by
// two readers. Each consumer keeps its record, timed and priced by the route that
// served it; `card <id>` prints a COST line for each and the totals, and --json
// carries the same value.

// costDeadline is the routes' deadline: seconds, as nova-config's route row holds it.
var costDeadline = 1800

// costRoutes are the twin's routes: the flash route the cards are dealt on, and a pro
// route of the model the first reader runs, each with a price sheet.
func costRoutes() []sprint.Route {
	return []sprint.Route{
		{Name: "flash-a", Tier: "flash", Provider: "opencode", Model: "deepseek-v4-flash", Tokens: 400000, Deadline: costDeadline, Enabled: true,
			Prices: cardcost.Prices{Input: "0.14", CacheRead: "0.028", Output: "0.28", ReasoningAsOutput: true, AsOf: "2026-10-01"}},
		{Name: "pro-a", Tier: "pro", Provider: "opencode", Model: "deepseek-v4-pro", Tokens: 400000, Deadline: costDeadline, Enabled: true,
			Prices: cardcost.Prices{Input: "0.5", CacheRead: "0.1", Output: "2", ReasoningAsOutput: true}},
	}
}

// The usage lines the member and the readers report, as cardcost.Usage spells them.
const (
	usageFailed = "wall=10.00s budget=1500/400000 input=1000 cache_read=2000 output=300 reasoning=200 requests=3 max_prompt=2500 model=opencode/deepseek-v4-flash actual_usd=0.0005 actual_by=harness"
	usageOK     = "wall=30.00s budget=4500/400000 input=4000 cache_read=6000 output=500 reasoning=0 requests=5 model=opencode/deepseek-v4-flash actual_usd=0.0009 actual_by=harness"
	usageReadA  = "wall=20.00s budget=2150/400000 input=2000 cache_read=1000 output=100 reasoning=50 requests=2 model=opencode/deepseek-v4-pro actual_usd=0.0015 actual_by=harness"
	usageReadB  = "wall=5.00s budget=110/400000 input=100 output=10 model=other/unrouted actual_usd=0.002 actual_by=harness"
)

// costCard plays the producer s1-1 to two reads: attempt 1 dealt, taken 2 s later and
// failed 10 s after that; attempt 2 taken 4 s after its deal and finished 30 s later;
// reader-a begins 3 s after the ask and reads for 20 s; reader-b reports 7 s after
// that without a begin.
func costCard(t *testing.T) *testApp {
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.m.SetRoutes(costRoutes())
	ta.ok("add --stream s1 --count 1")
	ta.deal(1)
	ta.a.sleep(2 * time.Second)
	ta.ok("take --as m1 s1-1.w1@1")
	ta.a.sleep(10 * time.Second)
	ta.ok("finish --as m1 s1-1.w1@1 --failed --report 'tests red' --usage '" + usageFailed + "'")
	ta.ok("rework s1-1 --fix 'handle the empty case'")
	ta.a.sleep(4 * time.Second)
	ta.ok("take --as m1 s1-1.w2@1")
	ta.a.sleep(30 * time.Second)
	ta.ok("finish --as m1 s1-1.w2@1 --usage '" + usageOK + "'")
	ta.ok("ask")
	ta.a.sleep(3 * time.Second)
	ta.ok("read --as reader-a --begin --limit 1")
	ta.a.sleep(20 * time.Second)
	ta.ok("read --as reader-a --ok --limit 1 --finding 'good' --usage '" + usageReadA + "'")
	ta.a.sleep(7 * time.Second)
	ta.ok("read --as reader-b --ok --limit 1 --finding 'fine' --usage '" + usageReadB + "'")
	return ta
}

func TestTheProducerCardCarriesWhatEachConsumerCostAndTheTotal(t *testing.T) {
	t.Parallel()
	ta := costCard(t)
	out := ta.ok("card s1-1")
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "COST ") {
			lines = append(lines, l)
		}
	}
	want := []string{
		// predicted: 1000*0.14 + 2000*0.028 + (300+200)*0.28, over a million
		"COST kind=work card=s1-1.w1 attempt=1 who=m1 route=flash-a model=opencode/deepseek-v4-flash end=failed input=1000 cache_read=2000 cache_write=- output=300 reasoning=200 requests=3 wait=2s run=10s predicted_usd=0.000336 actual_usd=0.0005 actual_by=harness cost=both",
		// 4000*0.14 + 6000*0.028 + 500*0.28
		"COST kind=work card=s1-1.w2 attempt=2 who=m1 route=flash-a model=opencode/deepseek-v4-flash end=ok input=4000 cache_read=6000 cache_write=- output=500 reasoning=0 requests=5 wait=4s run=30s predicted_usd=0.000868 actual_usd=0.0009 actual_by=harness cost=both",
		// a read is priced by the enabled route of the model its harness reported: 2000*0.5 + 1000*0.1 + (100+50)*2
		"COST kind=read card=s1-1.r2.reader-a attempt=2 who=reader-a route=pro-a model=opencode/deepseek-v4-pro end=ok input=2000 cache_read=1000 cache_write=- output=100 reasoning=50 requests=2 wait=3s run=20s predicted_usd=0.0014 actual_usd=0.0015 actual_by=harness cost=both",
		// no route runs its model: no prediction, never a zero; the harness's cost stands
		"COST kind=read card=s1-1.r2.reader-b attempt=2 who=reader-b route=- model=other/unrouted end=ok input=100 cache_read=- cache_write=- output=10 reasoning=- requests=- wait=30s run=0s predicted_usd=- actual_usd=0.002 actual_by=harness cost=actual",
		"COST TOTAL consumers=4 input=7100 cache_read=9000 cache_write=- output=910 reasoning=250 requests=10 wait=39s run=60s predicted_usd=0.002604 predicted_of=3/4 actual_usd=0.0049 actual_by=harness actual_of=4/4 charged_usd=0.0049",
	}
	assert.Equal(t, want, lines, out)

	// the record on the card copies the sheet it was priced by, so a later price change rewrites nothing
	ta.m.SetRoutes(nil)
	again := ta.ok("card s1-1")
	assert.Contains(t, again, want[0], "the record keeps its prediction when the routes change")
	assert.Contains(t, again, "prices=in:0.14,cr:0.028,out:0.28,ro:true,asof:2026-10-01", "the sheet used is on the card")

	var v cardView
	ta.json("card s1-1", &v)
	require.Len(t, v.Cost.Consumers, 4)
	assert.Equal(t, "work", v.Cost.Consumers[0].Kind)
	assert.Equal(t, "failed", v.Cost.Consumers[0].End)
	assert.Equal(t, "flash-a", v.Cost.Consumers[0].Route)
	assert.Equal(t, int64(2000), v.Cost.Consumers[0].Usage.Tokens.CacheRead)
	assert.Equal(t, cardcost.Unreported, v.Cost.Consumers[0].Usage.Tokens.CacheWrite, "a class not reported is an absence, never 0")
	assert.Equal(t, "0.000336", v.Cost.Consumers[0].Usage.Predicted)
	assert.Equal(t, "read", v.Cost.Consumers[3].Kind)
	assert.Equal(t, "", v.Cost.Consumers[3].Usage.Predicted)
	assert.Equal(t, cardcost.WhyNoRoute, v.Cost.Consumers[3].Usage.Unpriced)
	assert.Equal(t, "0.002604", v.Cost.Total.Predicted)
	assert.Equal(t, 3, v.Cost.Total.PredOf)
	assert.Equal(t, "0.0049", v.Cost.Total.Actual)
	assert.Equal(t, cardcost.ActualByHarness, v.Cost.Total.ActualBy, "the actual is the harness's figure, never an invoice")
	assert.Equal(t, int64(7100), v.Cost.Total.Tokens.Input)
	assert.Equal(t, int64(39), v.Cost.Total.Wait)
	assert.Equal(t, int64(60), v.Cost.Total.Run)
}

// A card no consumer reported for still prints its total line, every figure unknown:
// a dash, never a zero.
func TestACardWithNoConsumerRecordPrintsDashes(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1")
	out := ta.ok("card s1-1")
	assert.Contains(t, out, "COST TOTAL consumers=0 input=- cache_read=- cache_write=- output=- reasoning=- requests=- wait=- run=- predicted_usd=- predicted_of=0/0 actual_usd=- actual_by=- actual_of=0/0 charged_usd=-\n")
}

// costLines are the COST lines of a card's story.
func costLines(out string) []string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "COST ") {
			lines = append(lines, l)
		}
	}
	return lines
}

// workGen is the generation of the member's ready work card, as its queue says it.
func (ta *testApp) workGen(member, card string) string {
	ta.t.Helper()
	var q struct{ Cards []queueCard }
	ta.json("queue --as "+member, &q)
	for _, c := range q.Cards {
		if c.ID == card {
			return strconv.Itoa(c.Gen)
		}
	}
	ta.t.Fatalf("%s holds no %s: %+v", member, card, q.Cards)
	return ""
}

// A take the provider failed is counted once: take 1 fails at the provider with its
// usage, the card is dealt again, and take 2 is a failed launch that reports no usage.
// The first take's record is in its provider_take_1 alone, so the card's own take
// shows nothing spent and the total is the first take's.
func TestAProviderFailedTakeCountsOnceAfterARedeal(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.m.SetRoutes(costRoutes())
	ta.ok("add --stream s1 --count 1")
	ta.deal(1)
	ta.ok("take --as m1 s1-1.w1@1")
	ta.a.sleep(5 * time.Second)
	ta.ok("finish --as m1 s1-1.w1@1 --failed --report 'provider failure: 529 overloaded' --usage '" + usageFailed + "'")
	ta.deal(1)
	gen := ta.workGen("m1", "s1-1.w1")
	ta.ok("take --as m1 s1-1.w1@" + gen)
	ta.a.sleep(2 * time.Second)
	ta.ok("finish --as m1 s1-1.w1@" + gen + " --failed --report 'launch refused: no route for the model'")
	out := ta.ok("card s1-1")
	lines := costLines(out)
	require.Len(t, lines, 3, out)
	assert.Contains(t, lines[0], "COST kind=work card=s1-1.w1 attempt=1 take=1 who=m1 route=flash-a model=opencode/deepseek-v4-flash end=provider-failure input=1000 ")
	assert.Contains(t, lines[1], "COST kind=work card=s1-1.w1 attempt=1 who=m1 route=flash-a model=opencode/deepseek-v4-flash end=failed input=- cache_read=- ", "the second take spent nothing it reported")
	assert.Contains(t, lines[2], "COST TOTAL consumers=2 input=1000 cache_read=2000 cache_write=- output=300 reasoning=200 requests=3 ")
	assert.Contains(t, lines[2], "predicted_usd=0.000336 predicted_of=1/2 actual_usd=0.0005 actual_by=harness actual_of=1/2", "the first take counted once")
}

// A read handed back without a verdict keeps its run's record (read_take_<n>), the
// other reader's verdict run its own, and both count in the total.
func TestAReturnedReadKeepsItsRunInTheTotal(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1")
	ta.m.SetRoutes(costRoutes())
	ta.ok("add --stream s1 --count 1")
	ta.deal(1)
	ta.ok("take --as m1 s1-1.w1@1")
	ta.ok("finish --as m1 s1-1.w1@1")
	ta.ok("ask")
	ta.ok("read --as reader-a --begin --limit 1")
	ta.a.sleep(9 * time.Second)
	ta.ok("read --as reader-a --return s1-1.r1.reader-a --reason 'no verdict' --usage '" + usageReadA + "'")
	ta.ok("read --as reader-b --ok --limit 1 --usage '" + usageReadB + "'")
	out := ta.ok("card s1-1")
	lines := costLines(out)
	require.Len(t, lines, 4, out) // the work card's take (it reported nothing), two reads, the total
	assert.Contains(t, lines[1], "COST kind=read card=s1-1.r1.reader-a attempt=1 take=1 who=reader-a route=pro-a model=opencode/deepseek-v4-pro end=returned input=2000 ")
	assert.Contains(t, lines[1], "wait=0s run=9s predicted_usd=0.0014 actual_usd=0.0015 actual_by=harness cost=both")
	assert.Contains(t, lines[2], "COST kind=read card=s1-1.r1.reader-b attempt=1 who=reader-b ")
	assert.Contains(t, lines[3], "COST TOTAL consumers=3 input=2100 ")
	assert.Contains(t, lines[3], "actual_usd=0.0035 actual_by=harness actual_of=2/3", "the returned run counts")
}
