package sprint

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/card"
	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// The producer card carries what it cost (cost.go): one record per consumer key,
// set once, the total kept exactly with it, and past MaxCostRecords the total still
// exact and the list cut.

// book adds the consumers to the primary as steps would, one step each.
func book(pr *Card, cons ...Consumer) {
	for _, c := range cons {
		set := map[string]string{}
		addConsumer(pr, set, c)
		for k, v := range set {
			pr.Fields[k] = v
		}
	}
}

func consumerOf(key, at, usage string) Consumer {
	return Consumer{Kind: "read", Card: "s1-1.r1.reader-a", Attempt: 1, Who: "reader-a", End: "ok", At: at, Key: key, Usage: cardcost.ParseUsage(usage)}
}

func TestTheProducerKeepsOneRecordPerConsumerAndAnExactTotal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		cons    []Consumer
		records int
		input   int64
		charged string
	}{
		{name: "two consumers", cons: []Consumer{consumerOf("a#v", "2026-10-01T12:00:01Z", "input=10 actual_usd=0.1"), consumerOf("b#v", "2026-10-01T12:00:00Z", "input=20 predicted_usd=0.2")},
			records: 2, input: 30, charged: "0.3"},
		{name: "a consumer added twice counts once", cons: []Consumer{consumerOf("a#v", "2026-10-01T12:00:00Z", "input=10 actual_usd=0.1"), consumerOf("a#v", "2026-10-01T12:00:00Z", "input=10 actual_usd=0.1")},
			records: 1, input: 10, charged: "0.1"},
		{name: "a consumer with no cost is in the history and not in charged", cons: []Consumer{consumerOf("a#v", "2026-10-01T12:00:00Z", "input=5")},
			records: 1, input: 5, charged: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pr := &Card{ID: "s1-1", Fields: map[string]string{}}
			book(pr, tc.cons...)
			v := CardCostOf(pr)
			assert.Len(t, v.Consumers, tc.records)
			assert.Equal(t, tc.records, v.Total.Records)
			assert.Equal(t, tc.input, v.Total.Tokens.Input)
			assert.Equal(t, tc.charged, v.Total.Charged)
			for i := 1; i < len(v.Consumers); i++ {
				assert.LessOrEqual(t, v.Consumers[i-1].At, v.Consumers[i].At, "the history is in the order the consumers ended")
			}
		})
	}
}

func TestPastTheBoundTheTotalStaysExactAndTheListIsCut(t *testing.T) {
	t.Parallel()
	pr := &Card{ID: "s1-1", Fields: map[string]string{}}
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < MaxCostRecords+3; i++ {
		book(pr, consumerOf(fmt.Sprintf("c%03d#v", i), t0.Add(time.Duration(i)*time.Second).Format(time.RFC3339), "input=1 actual_usd=0.001"))
	}
	v := CardCostOf(pr)
	require.Len(t, v.Consumers, MaxCostRecords)
	assert.Equal(t, 3, v.Cut)
	assert.Equal(t, MaxCostRecords+3, v.Total.Records, "the total holds every consumer")
	assert.Equal(t, "0.067", v.Total.Charged)
	assert.Contains(t, v.CostLines()[len(v.CostLines())-1], " cut=3", "the total line says the list was cut")
}

func TestARecordReadsBackAsTheConsumer(t *testing.T) {
	t.Parallel()
	c := Consumer{Kind: "work", Card: "s1-1.w2", Attempt: 2, Take: 1, Gen: 3, Who: "m1", Route: "flash-a", Model: "opencode/m", End: "provider failure",
		At: "2026-10-01T12:00:00Z", Key: "s1-1.w2#g3", Usage: cardcost.ParseUsage("input=10 actual_usd=0.1 actual_by=harness wait=2s run=9s")}
	got := parseConsumer(c.Key, c.line())
	assert.Equal(t, c, got)
}

// A take's cost record carries the token count of the brief it was dealt (cost.go,
// Consumer.BriefTokens; docs/SPEC-CARD-CONTRACT.md section 7), read back from the primary
// with the rest of the record; a read's carries none.
func TestATakesCostRecordCarriesItsBriefsTokens(t *testing.T) {
	t.Parallel()
	brief := "RESULT: s1-1 sha=0123456789ab tier: pro\nREPO: o/r\nBASE: dev\nContract: docs/SPEC-CARD-CONTRACT.md v1\n"
	pr := &Card{ID: "s1-1", Fields: map[string]string{"brief": brief}}
	work := Consumer{Kind: "work", Card: "s1-1.w1", Attempt: 1, Who: "m1", End: "done", At: "2026-10-01T12:00:00Z", Key: "s1-1.w1#g1", Usage: cardcost.ParseUsage("input=10")}
	book(pr, work, consumerOf("s1-1.r1#v", "2026-10-01T12:00:01Z", "input=5"))
	v := CardCostOf(pr)
	require.Len(t, v.Consumers, 2)
	assert.Equal(t, card.Tokens(brief), v.Consumers[0].BriefTokens, "the take's record carries its brief's tokens")
	assert.Equal(t, (len(brief)+3)/4, v.Consumers[0].BriefTokens)
	assert.Equal(t, int64(10), v.Consumers[0].Usage.Tokens.Input, "the usage reads back beside it")
	assert.Zero(t, v.Consumers[1].BriefTokens, "a read carries none")
	assert.Contains(t, pr.F(FieldCostRecord+"s1-1.w1#g1"), " brief_tokens="+fmt.Sprint(card.Tokens(brief))+" ")
}
