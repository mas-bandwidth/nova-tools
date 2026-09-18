package decide

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Stella's two witnesses on #1327 at df8cedaf, R5.

// (1) A successful answer is not evidence of reported usage. A 200 that carries
// a valid answer and no usage object at all reports NOTHING about what it
// spent, and nothing is not zero -- while an explicitly reported zero IS a
// measurement and must survive as one.
func TestUsagePresenceIsPerField(t *testing.T) {
	for name, tc := range map[string]struct {
		body      string
		wantIn    bool
		wantOut   bool
		wantInN   int
		wantOutN  int
		wantKnown bool
	}{
		"no usage object": {
			body: `{"answers": {"rung": {"type":"choice","choice":"rung-1","confidence":0.95}}}`,
		},
		"empty usage object": {
			body: `{"answers": {"rung": {"type":"choice","choice":"rung-1","confidence":0.95}}, "usage": {}}`,
		},
		"input only": {
			body:      `{"answers": {"rung": {"type":"choice","choice":"rung-1","confidence":0.95}}, "usage": {"input_tokens": 937}}`,
			wantIn:    true,
			wantInN:   937,
			wantKnown: true,
		},
		"an explicit zero is a measurement": {
			body:      `{"answers": {"rung": {"type":"choice","choice":"rung-1","confidence":0.95}}, "usage": {"input_tokens": 0, "output_tokens": 0}}`,
			wantIn:    true,
			wantOut:   true,
			wantKnown: true,
		},
		"both reported": {
			body:      `{"answers": {"rung": {"type":"choice","choice":"rung-1","confidence":0.95}}, "usage": {"input_tokens": 937, "output_tokens": 12}}`,
			wantIn:    true,
			wantOut:   true,
			wantInN:   937,
			wantOutN:  12,
			wantKnown: true,
		},
	} {
		_, usage, err := decodeResponse([]byte(tc.body))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if usage.HasInput != tc.wantIn || usage.HasOutput != tc.wantOut {
			t.Errorf("%s: presence = in:%v out:%v, want in:%v out:%v", name, usage.HasInput, usage.HasOutput, tc.wantIn, tc.wantOut)
		}
		if usage.InputTokens != tc.wantInN || usage.OutputTokens != tc.wantOutN {
			t.Errorf("%s: tokens = %d/%d, want %d/%d", name, usage.InputTokens, usage.OutputTokens, tc.wantInN, tc.wantOutN)
		}
		if usage.Known() != tc.wantKnown {
			t.Errorf("%s: known = %v, want %v", name, usage.Known(), tc.wantKnown)
		}
	}
}

// The presence travels through the route and into the log row: an absent
// counter is an ABSENCE there too, and an explicit zero is a zero.
func TestRouteCarriesUsagePresence(t *testing.T) {
	reg := testRegistry(t)
	u := Unit{ID: "u", Kind: KindNewVerb, Files: 3, Packages: 1, Lanes: 1}

	silent := &recorder{conf: 0.95} // a 200, an answer, and no usage at all
	res, err := RouteJev(context.Background(), silent, reg, u, DefaultFloor)
	if err != nil {
		t.Fatal(err)
	}
	if res.Usage.Calls != 1 {
		t.Fatalf("a call was made: %+v", res.Usage)
	}
	if res.Usage.HasInput || res.Usage.HasOutput || res.Usage.Known() {
		t.Errorf("the provider reported no usage, and nothing is not zero: %+v", res.Usage)
	}
	e := EntryFor(res, u, time.Unix(0, 0).UTC())
	if e.TokensIn != nil || e.TokensOut != nil {
		t.Errorf("an unreported counter is absent from the row, never a zero: %+v", e)
	}
	if e.Calls != 1 {
		t.Errorf("the call itself is still on the record: %+v", e)
	}

	zero := &recorder{conf: 0.95, usage: Usage{HasInput: true, HasOutput: true}}
	res, err = RouteJev(context.Background(), zero, reg, u, DefaultFloor)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Usage.HasInput || !res.Usage.HasOutput {
		t.Fatalf("an explicitly reported zero is a measurement: %+v", res.Usage)
	}
	e = EntryFor(res, u, time.Unix(0, 0).UTC())
	if e.TokensIn == nil || *e.TokensIn != 0 || e.TokensOut == nil || *e.TokensOut != 0 {
		t.Errorf("a reported zero must survive as a zero: %+v", e)
	}
}

// twoRungRegistry is Stella's witness: two rungs and nothing above them, so a
// choice of the top rung below the floor has nowhere to step up to.
func twoRungRegistry(t *testing.T) *Registry {
	t.Helper()
	reg, err := ParseRegistry([]byte(`{"minds":[
	  {"name":"low","lineage":"one","height":0,"availability":"available","ask":"card"},
	  {"name":"high","lineage":"two","height":1,"availability":"available","ask":"bus"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// (2) A routing refusal does not discard a completed call. The call was made,
// the tokens were spent, and the refusal that follows cannot unspend them: the
// result carries the usage and the refusal, so the caller can persist both
// before it exits.
func TestUsageSurvivesARoutingRefusal(t *testing.T) {
	reg := twoRungRegistry(t)
	u := Unit{ID: "u-ref", Kind: KindRebase, Files: 2, Packages: 1}
	// The provider picks the top rung with a confidence below the floor, so the
	// step up has nowhere to go.
	rec := &recorder{conf: 0.40, pick: 1, usage: Usage{InputTokens: 937, HasInput: true, OutputTokens: 12, HasOutput: true}}
	res, err := RouteJev(context.Background(), rec, reg, u, DefaultFloor)
	if err == nil {
		t.Fatalf("there is no rung above the top one: want a refusal, got %s", res.Rung.Name)
	}
	if res.Usage.Calls != 1 || res.Usage.InputTokens != 937 || res.Usage.OutputTokens != 12 {
		t.Errorf("the refusal discarded the call that was already made: %+v", res.Usage)
	}
	if res.Unit != u.ID {
		t.Errorf("the refused result must still name its unit, so the row can be written: %+v", res)
	}
	if strings.TrimSpace(res.Refusal) == "" {
		t.Error("the refused result must carry the refusal, so the row says why")
	}
	e := EntryFor(res, u, time.Unix(0, 0).UTC())
	if e.Refusal == "" || e.TokensIn == nil || *e.TokensIn != 937 {
		t.Errorf("the log row must carry the spend and the reason it was refused: %+v", e)
	}
}

// A refusal the rules reach on their own carries no call, and says so.
func TestARulesRefusalCarriesNoCall(t *testing.T) {
	reg := twoRungRegistry(t)
	u := Unit{ID: "u-top", Kind: KindRebase, Files: 2, Packages: 1, Attempts: []Attempt{
		{Rung: "low", Outcome: OutcomeFailed, Reason: "r"},
		{Rung: "high", Outcome: OutcomeFailed, Reason: "r"},
	}}
	res, err := RouteRules(reg, u, DefaultFloor)
	if err == nil {
		t.Fatalf("every rung has been tried: want a refusal, got %s", res.Rung.Name)
	}
	if res.Usage.Calls != 0 {
		t.Errorf("the rules alone spend nothing: %+v", res.Usage)
	}
	if res.Unit != u.ID || strings.TrimSpace(res.Refusal) == "" {
		t.Errorf("a refused result still names its unit and its refusal: %+v", res)
	}
}
