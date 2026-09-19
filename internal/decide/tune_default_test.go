package decide

import (
	"os"
	"testing"
)

// The floor a decisions log tunes to depends on what a BELOW-FLOOR row actually
// gets, and until now Tune did not know.
//
// Tune counts a row below the floor as `escalated` and caps the escalation
// rate. That is right where escalation is the expensive, careful direction --
// the rung question, where below the floor the answer steps UP a rung. It is
// exactly backwards for the who-reads question, where below the floor the
// answer falls back to the CHEAP default, `opus-child`: there, escalating is
// the risky move, and the floor that "escalates" most is the floor that hands
// the most cards to the default reader.
//
// The fixture is the day's real rows: testdata/reader-2026-09-19.jsonl, 47
// who-reads answers from six manager lanes on 2026-09-19, joined to what the
// friends actually did on the message bus. Nine of those pull requests were
// later HELD by a friend (#1815 and #1871 by Johnny, #1776 #1785 #1830 #1833
// #1856 #1860 by Stella, #1832 by Emma), so the label is the friend who held
// it, and `opus-child` where no friend held it.
//
// Without a default named, Tune answers 0.9 -- the floor at which THREE of
// FOUR of those holds are handed to the default reader that would not have
// caught them.
func TestTheBestFloorWithNoDefaultNamedIsTheOneThatMissesMostHolds(t *testing.T) {
	res := tuneFixture(t, TuneOptions{Floors: []float64{0.5, 0.65, 0.8, 0.9}, MaxEscalation: 0.7})
	if res.Labeled != 47 {
		t.Fatalf("fixture has %d labeled rows, want 47", res.Labeled)
	}
	if res.BestFloor != 0.9 {
		t.Fatalf("with no default named the verb answers %g; this test records today's behaviour, not the wanted one", res.BestFloor)
	}
}

// With the default named, each floor also reports how many below-floor rows the
// default got RIGHT and how many it MISSED, and the best floor is the one that
// misses fewest -- a missed hold lands a defect, and a needless friend read
// costs minutes, so a defect outranks minutes and ties go to the higher agree
// rate.
func TestNamingTheDefaultMakesTheFloorAnswerTheCostThatMatters(t *testing.T) {
	res := tuneFixture(t, TuneOptions{
		Floors:        []float64{0.5, 0.65, 0.8, 0.9},
		MaxEscalation: 0.7,
		Default:       "opus-child",
	})
	want := map[float64]int{0.5: 0, 0.65: 1, 0.8: 3, 0.9: 4}
	for _, s := range res.Floors {
		missed := s.Defaulted - s.DefaultAgree
		if got, ok := want[s.Floor]; ok && missed != got {
			t.Errorf("floor %g missed %d holds through the default, want %d (defaulted=%d agreed=%d)",
				s.Floor, missed, got, s.Defaulted, s.DefaultAgree)
		}
	}
	if res.BestFloor != 0.5 {
		t.Errorf("best floor is %g; the day's rows say 0.5, the only floor that hands no friend hold to the default", res.BestFloor)
	}
}

// A default that is not one of the answers the log holds is a refusal, never a
// silent zero: a floor tuned against a default nothing ever returns is untuned.
func TestADefaultNoRowEverAnsweredIsARefusal(t *testing.T) {
	_, err := Tune(fixtureBytes(t), TuneOptions{
		Floors:        []float64{0.5, 0.9},
		MaxEscalation: 0.7,
		Default:       "sol",
	})
	if err == nil {
		t.Fatal("a default no labeled row ever names must be refused")
	}
}

func fixtureBytes(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/reader-2026-09-19.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func tuneFixture(t *testing.T, opts TuneOptions) TuneResult {
	t.Helper()
	res, err := Tune(fixtureBytes(t), opts)
	if err != nil {
		t.Fatal(err)
	}
	return res
}
