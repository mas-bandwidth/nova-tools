package decide

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// `--default` is a CALCULATION over a decisions log, and what it says about a
// log is a statement about that log (Stella, 2026-09-19, repair 3 of the #1925
// hold: keep observations separate from truth).
//
// The arithmetic it adds is real and the old arithmetic could not express it.
// Tune counts a row below the floor as `escalated` and caps the escalation
// rate. That is right where escalation is the expensive, careful direction --
// the rung question, where below the floor the answer steps UP a rung. It says
// nothing at all where the escalation is a fallback to ONE configured default,
// because there the interesting number is how often that default DISAGREED
// with the row's label, and no field held it.
//
// The fixture is testdata/reader-observations-2026-09-19.jsonl: 47 who-reads
// answers from one day, joined to later HOLDs. Its own README says what it is
// and is not -- every row carries `adjudicated: false`, every row was asked
// with the previous question version, and the stopping classes are sparse. It
// is here to exercise the arithmetic. **It sets no floor**, and neither test
// below asserts one.

const readerObservations = "testdata/reader-observations-2026-09-19.jsonl"

func TestTheDefaultArithmeticCountsWhatEscalationAloneCannot(t *testing.T) {
	// Observations: true is what admits this fixture at all now -- the
	// adjudication boundary refuses it otherwise -- and what it buys is the
	// ARITHMETIC. Neither read below recommends a floor: Render prints
	// best_floor=none for both, and what is compared here is the calculation.
	opts := TuneOptions{Floors: []float64{0.5, 0.65, 0.8, 0.9}, MaxEscalation: 0.7, Observations: true}
	plain := tuneFixture(t, opts)
	if plain.Recommends {
		t.Error("a log of observations recommended a floor")
	}
	if plain.Observations != 47 {
		t.Errorf("counted %d observation rows, want 47", plain.Observations)
	}
	if plain.Labeled != 47 {
		t.Fatalf("fixture has %d labeled rows, want 47", plain.Labeled)
	}
	for _, s := range plain.Floors {
		if s.Defaulted != 0 || s.DefaultAgree != 0 || s.Missed() != 0 {
			t.Errorf("floor %g: with no default named there is no default to score, got %+v", s.Floor, s)
		}
	}

	opts.Default = "opus-child"
	withDefault := tuneFixture(t, opts)
	// Every escalated row is a defaulted row: the two counts describe the same
	// rows from the two sides.
	for _, s := range withDefault.Floors {
		if s.Defaulted != s.Escalated {
			t.Errorf("floor %g: defaulted=%d escalated=%d; a named default answers every escalated row", s.Floor, s.Defaulted, s.Escalated)
		}
		if s.DefaultAgree > s.Defaulted {
			t.Errorf("floor %g: default agreed on %d of %d", s.Floor, s.DefaultAgree, s.Defaulted)
		}
	}
	// The arithmetic over THIS log, stated as a fact about this log.
	want := map[float64]int{0.5: 0, 0.65: 1, 0.8: 3, 0.9: 4}
	for _, s := range withDefault.Floors {
		if got, ok := want[s.Floor]; ok && s.Missed() != got {
			t.Errorf("floor %g: the default disagreed on %d rows of this log, want %d (defaulted=%d agreed=%d)",
				s.Floor, s.Missed(), got, s.Defaulted, s.DefaultAgree)
		}
	}
	// The two readings of the same log disagree, which is the point: one
	// maximises agreement on the rows it kept, the other minimises the rows the
	// default answered differently. Neither is a calibrated floor for any
	// reading, and this test asserts only that the arithmetic is distinguishable.
	if plain.BestFloor == withDefault.BestFloor {
		t.Errorf("both readings answered %g; the default arithmetic adds nothing", plain.BestFloor)
	}
}

// A default that is not one of the answers the log holds is a refusal, never a
// silent zero: arithmetic against a default nothing ever returns is arithmetic
// over an empty set.
func TestADefaultNoRowEverAnsweredIsARefusal(t *testing.T) {
	_, err := Tune(fixtureBytes(t), TuneOptions{
		Floors:        []float64{0.5, 0.9},
		MaxEscalation: 0.7,
		Default:       "a-reader-nobody-named",
		Observations:  true,
	})
	if err == nil {
		t.Fatal("a default no labeled row ever names must be refused")
	}
}

// And the boundary itself, at the package: this fixture cannot bless a floor
// without the flag that says out loud no floor comes out of it, and the
// refusal is the sentinel a caller can name in one word.
func TestAnObservationLogCannotSetAFloorWithoutSayingSo(t *testing.T) {
	_, err := Tune(fixtureBytes(t), TuneOptions{Floors: []float64{0.5, 0.9}, MaxEscalation: 0.7})
	if err == nil {
		t.Fatal("the observation fixture set a floor")
	}
	if !errors.Is(err, ErrNotAdjudicated) {
		t.Errorf("the refusal is not ErrNotAdjudicated: %v", err)
	}
	// Adjudicated truth is untouched, and so is a log from before the field
	// existed: a historical format is not silently reinterpreted.
	for _, suffix := range []string{`,"adjudicated":true`, ``} {
		var b strings.Builder
		for i := 0; i < MinLabeled+2; i++ {
			fmt.Fprintf(&b, `{"decision":"a","label":"a","confidence":0.8%s}`+"\n", suffix)
		}
		res, err := Tune([]byte(b.String()), TuneOptions{Floors: []float64{0.5, 0.9}, MaxEscalation: 0.7})
		if err != nil {
			t.Fatalf("suffix %q: %v", suffix, err)
		}
		if !res.Recommends || res.Observations != 0 {
			t.Errorf("suffix %q: recommends=%v observations=%d", suffix, res.Recommends, res.Observations)
		}
		if !strings.Contains(res.Render(), "TUNE OK") {
			t.Errorf("suffix %q: the OK line is gone:\n%s", suffix, res.Render())
		}
	}
}

// The fixture says what it is. A row that claims to be adjudicated truth, or
// that carries no provider binding, does not belong in it.
func TestTheObservationFixtureIsLabelledAsObservations(t *testing.T) {
	readme, err := os.ReadFile("testdata/README-reader-observations.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"observations, not adjudicated truth", "tune nothing"} {
		if !contains(string(readme), want) {
			t.Errorf("the fixture README does not say %q", want)
		}
	}
	rows := fixtureBytes(t)
	for _, field := range []string{`"adjudicated":false`, `"label_source":"observed-later-hold"`, `"model":"jev-latest"`, `"criteria_version":"pre-2026-09-19.1`} {
		if !contains(string(rows), field) {
			t.Errorf("the observation rows do not carry %s", field)
		}
	}
	if contains(string(rows), `"adjudicated":true`) {
		t.Error("no row in this file is adjudicated")
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

func fixtureBytes(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(readerObservations)
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
