package decide

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dayLog is the escalation log of the 2026-09-18 routing run: twenty real units
// of work/pitstop-2026-09-18-units.lisp, thirteen of them answered by live Jev.
// It is the evidence the floor finding came from, so it is the fixture the fix
// is measured against.
func dayLog(t *testing.T) []Entry {
	t.Helper()
	entries, err := ReadEntries(filepath.Join("testdata", "pitstop-2026-09-18", "decisions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 20 {
		t.Fatalf("the day's log holds %d rows, want the 20 units", len(entries))
	}
	return entries
}

// The finding, counted: one floor of 0.90 for every kind sat above EVERY answer
// the provider gave, for every kind it answered. That is not a floor gating a
// decision, it is a floor deleting it, and the summary says so per kind.
func TestTheOneFloorWasAboveEveryProviderAnswer(t *testing.T) {
	reg := testRegistry(t)
	// The ladder as it was: no per-kind floors at all, so every kind is gated
	// on the built-in 0.9.
	bare := &Registry{Minds: reg.Minds}
	sum, err := Summarize(bare, dayLog(t))
	if err != nil {
		t.Fatal(err)
	}
	answered, defeated, below := 0, 0, 0
	for _, k := range sum.Kinds {
		if k.ProviderRows == 0 {
			continue
		}
		answered += k.ProviderRows
		below += k.BelowFloor
		if k.Defeated {
			defeated++
		}
		if k.FloorFrom != FloorFromBuiltIn {
			t.Errorf("%s: floor_from = %s, want %s on a ladder with no floors table", k.Kind, k.FloorFrom, FloorFromBuiltIn)
		}
	}
	if answered != 13 {
		t.Errorf("provider answers = %d, want the 13 of the day's run", answered)
	}
	if below != 13 {
		t.Errorf("below the floor = %d of %d; the finding is that every one of them was", below, answered)
	}
	if defeated != 5 {
		t.Errorf("defeated kinds = %d, want all 5 the provider answered for", defeated)
	}
}

// And the fix, counted on the same rows: with the floors the registry now
// ships, one answer in thirteen steps up instead of thirteen.
func TestThePerKindFloorsCutTheEscalation(t *testing.T) {
	reg := testRegistry(t)
	sum, err := Summarize(reg, dayLog(t))
	if err != nil {
		t.Fatal(err)
	}
	below, answered := 0, 0
	for _, k := range sum.Kinds {
		answered += k.ProviderRows
		below += k.BelowFloor
		if k.ProviderRows > 0 && k.Defeated {
			t.Errorf("%s: the floor is still above every answer the provider gave it (max %.2f, floor %.2f)", k.Kind, k.ConfMax, k.Floor)
		}
	}
	if answered != 13 {
		t.Fatalf("provider answers = %d, want 13", answered)
	}
	if below != 1 {
		t.Errorf("below the floor = %d of 13, want 1 (review:sharedtemp-class at 0.72 against a 0.73 floor)", below)
	}
}

// The histogram is the shape of those answers, per kind, on one line: every
// bucket named and counted, including the empty ones.
func TestTheSummaryCarriesAConfidenceHistogramPerKind(t *testing.T) {
	reg := testRegistry(t)
	sum, err := Summarize(reg, dayLog(t))
	if err != nil {
		t.Fatal(err)
	}
	byKind := map[string]KindSummary{}
	for _, k := range sum.Kinds {
		byKind[k.Kind] = k
	}
	fwrt, ok := byKind[KindFixWithRedTest]
	if !ok {
		t.Fatal("the day's log holds fix-with-red-test rows")
	}
	// Five provider answers, 0.72 0.73 0.74 0.77 0.78: all in one bucket.
	if fwrt.ProviderRows != 5 {
		t.Errorf("provider rows = %d, want 5", fwrt.ProviderRows)
	}
	if got := fwrt.HistField(); got != "0.0-0.5:0,0.5-0.6:0,0.6-0.7:0,0.7-0.8:5,0.8-0.9:0,0.9-1.0:0" {
		t.Errorf("hist = %s", got)
	}
	if fwrt.ConfMin != 0.72 || fwrt.ConfMax != 0.78 || fwrt.ConfP25 != 0.73 {
		t.Errorf("min/max/p25 = %.2f/%.2f/%.2f, want 0.72/0.78/0.73", fwrt.ConfMin, fwrt.ConfMax, fwrt.ConfP25)
	}
	// guard was answered by the machinery alone, so it has nothing to describe
	// and says so with dashes rather than with zeroes nobody measured.
	guard, ok := byKind[KindGuard]
	if !ok {
		t.Fatal("the day's log holds a guard row")
	}
	if guard.ProviderRows != 0 {
		t.Errorf("guard provider rows = %d, want 0: security is the machinery's word", guard.ProviderRows)
	}
	line := sum.Render()
	if !strings.Contains(line, "conf_min=- conf_max=- conf_p25=-") {
		t.Errorf("a kind with no provider answer prints dashes, not zeroes:\n%s", line)
	}
	if !strings.Contains(line, "hist=0.0-0.5:") || !strings.Contains(line, "below_floor=") || !strings.Contains(line, "defeated=") {
		t.Errorf("every field on every line:\n%s", line)
	}
}

// The floors the registry ships ARE what tune proposes from the day's log:
// p25 of the provider answers that stood, per kind. A measured default that
// cannot be reproduced from the rows is a number somebody chose.
func TestTheShippedFloorsAreWhatTuneProposesFromTheDaysLog(t *testing.T) {
	reg := testRegistry(t)
	proposals, err := ProposeFloors(reg, dayLog(t))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{
		KindFleetChore:     0.70,
		KindStack:          0.70,
		KindFixWithRedTest: 0.73,
		KindNewVerb:        0.75,
		KindSpec:           0.75,
	}
	got := map[string]float64{}
	for _, row := range proposals.Proposals {
		if row.Proposed {
			got[row.Kind] = row.Floor
		}
	}
	if len(got) != len(want) {
		t.Errorf("proposed %d floors, want %d: %v", len(got), len(want), got)
	}
	for kind, floor := range want {
		if got[kind] != floor {
			t.Errorf("%s: proposed %.2f, want %.2f", kind, got[kind], floor)
		}
		shipped, ok := reg.FloorFor(kind)
		if !ok {
			t.Errorf("%s: the registry ships no floor for a kind the log can propose one for", kind)
			continue
		}
		if shipped.Floor != floor {
			t.Errorf("%s: the registry ships %.2f but the rows propose %.2f", kind, shipped.Floor, floor)
		}
		if strings.TrimSpace(shipped.From) == "" {
			t.Errorf("%s: a floor nobody can trace is a feeling with a number on it", kind)
		}
	}
	// guard has no provider answer at all, so it is not proposed a floor and
	// the row says why rather than leaving a reader to infer it.
	for _, row := range proposals.Proposals {
		if row.Kind == KindGuard && row.Proposed {
			t.Error("guard was never answered by the provider; it cannot be proposed a floor")
		}
	}
	if !strings.Contains(proposals.Render(), "TUNE FLOORS OK rows=20") {
		t.Errorf("the finish counts the rows it read:\n%s", proposals.Render())
	}
}

// A kind with one provider answer is not a quartile, so no floor is proposed
// and the note says how many rows there were.
func TestOneAnswerIsNotAQuartile(t *testing.T) {
	reg := testRegistry(t)
	entries := []Entry{{Unit: "u1", Kind: KindNewVerb, Source: SourceJev, Confidence: 0.61, RungTried: "emma"}}
	proposals, err := ProposeFloors(reg, entries)
	if err != nil {
		t.Fatal(err)
	}
	if len(proposals.Proposals) != 1 {
		t.Fatalf("proposals = %d, want 1", len(proposals.Proposals))
	}
	row := proposals.Proposals[0]
	if row.Proposed {
		t.Errorf("%s: one answer proposed a floor of %.2f", row.Kind, row.Floor)
	}
	if !strings.Contains(row.Note, "quartile") {
		t.Errorf("the note must say why: %q", row.Note)
	}
	if !row.Defeated {
		t.Errorf("0.61 is below the built-in 0.90: that floor is defeated for this kind")
	}
}

// A decision the log recorded a failure against does not set the floor: the
// floor is proposed from the answers that STOOD.
func TestAFailedDecisionDoesNotSetTheFloor(t *testing.T) {
	reg := testRegistry(t)
	entries := []Entry{
		{Unit: "a", Kind: KindNewVerb, Source: SourceJev, Confidence: 0.40, Outcome: OutcomeFailed},
		{Unit: "b", Kind: KindNewVerb, Source: SourceJev, Confidence: 0.80},
		{Unit: "c", Kind: KindNewVerb, Source: SourceJev, Confidence: 0.90},
		// Not a provider answer at all: the machinery's own number.
		{Unit: "d", Kind: KindNewVerb, Source: SourceRules, Confidence: 1.00},
	}
	proposals, err := ProposeFloors(reg, entries)
	if err != nil {
		t.Fatal(err)
	}
	row := proposals.Proposals[0]
	if row.Rows != 3 || row.Stood != 2 || row.Failed != 1 {
		t.Errorf("rows/stood/failed = %d/%d/%d, want 3/2/1 (the rules row is not evidence about the provider)", row.Rows, row.Stood, row.Failed)
	}
	if row.Floor != 0.80 {
		t.Errorf("floor = %.2f, want 0.80: the p25 of what stood, not of what failed", row.Floor)
	}
}

// A floor above the provider's observed maximum for its kind is refused, with
// the number to write instead. Such a floor escalates every decision, which is
// the defect this whole change is about, and it does not get written back.
func TestAFloorAboveTheObservedMaxIsRefused(t *testing.T) {
	reg := testRegistry(t)
	proposals, err := ProposeFloors(reg, dayLog(t))
	if err != nil {
		t.Fatal(err)
	}
	err = CheckFloors([]KindFloor{{Kind: KindFixWithRedTest, Floor: 0.90}}, proposals)
	if err == nil {
		t.Fatal("a floor of 0.90 against an observed max of 0.78 must be refused")
	}
	for _, want := range []string{"0.90", KindFixWithRedTest, "0.78", "0.73"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal wants %s in it, with the remedy: %v", want, err)
		}
	}
	// At or below the observed max it stands.
	if err := CheckFloors([]KindFloor{{Kind: KindFixWithRedTest, Floor: 0.78}}, proposals); err != nil {
		t.Errorf("a floor at the observed max is meetable: %v", err)
	}
	// A kind the log says nothing about is not second-guessed.
	if err := CheckFloors([]KindFloor{{Kind: KindGuard, Floor: 0.99}}, proposals); err != nil {
		t.Errorf("a kind with no provider answer has no observed max to refuse against: %v", err)
	}
}

// The registry validates its own floors: a kind nobody routes, a floor that is
// not a confidence, and one kind with two floors are all refusals on the way
// in, never a row sitting in a file being silently ignored.
func TestTheFloorTableIsValidated(t *testing.T) {
	minds := `"minds":[{"name":"flash","lineage":"deepseek","height":0,"availability":"available","ask":"card"}]`
	for name, floors := range map[string]string{
		"unknown kind": `"floors":[{"kind":"vibes","floor":0.7}]`,
		"no kind":      `"floors":[{"kind":"","floor":0.7}]`,
		"not a floor":  `"floors":[{"kind":"rebase","floor":1.5}]`,
		"twice":        `"floors":[{"kind":"rebase","floor":0.7},{"kind":"rebase","floor":0.8}]`,
	} {
		if _, err := ParseRegistry([]byte("{" + minds + "," + floors + "}")); err == nil {
			t.Errorf("%s: want a refusal", name)
		}
	}
	reg, err := ParseRegistry([]byte("{" + minds + `,"floors":[{"kind":"rebase","floor":0.7,"from":"measured"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	f, ok := reg.FloorFor(KindRebase)
	if !ok || f.Floor != 0.7 || f.From != "measured" {
		t.Errorf("FloorFor = %+v, %v", f, ok)
	}
	if _, ok := reg.FloorFor(KindSpec); ok {
		t.Error("a kind with no row has no floor, not a floor of zero")
	}
}

// ResolveFloor: the flag wins, then the kind's measured row, then the built-in
// default -- and every answer says which it was.
func TestResolveFloorPrefersTheFlagThenTheKind(t *testing.T) {
	reg := testRegistry(t)
	if got, from := ResolveFloor(reg, KindFixWithRedTest, 0.42, true); got != 0.42 || from != FloorFromFlag {
		t.Errorf("an explicit --floor wins: %.2f from %s", got, from)
	}
	if got, from := ResolveFloor(reg, KindFixWithRedTest, 0, false); got != 0.73 || from != FloorFromKind {
		t.Errorf("the kind's measured floor answers: %.2f from %s", got, from)
	}
	if got, from := ResolveFloor(reg, KindGuard, 0, false); got != DefaultFloor || from != FloorFromBuiltIn {
		t.Errorf("a kind with no row keeps the built-in: %.2f from %s", got, from)
	}
}

// The rate table prices a usage row, and a model it does not hold has no price
// here -- the caller writes a dash and a NOTE rather than a number nobody
// published.
func TestTheRateTablePricesTokens(t *testing.T) {
	reg := testRegistry(t)
	if _, ok := reg.RateFor(DefaultModel); ok {
		t.Errorf("the shipped table holds a rate for %s; TypeSafe has published none to us", DefaultModel)
	}
	priced, err := ParseRegistry([]byte(`{"minds":[{"name":"flash","lineage":"deepseek","height":0,"availability":"available","ask":"card"}],
	  "rates":[{"model":"jev-latest","provider":"typesafe","input_usd_per_mtok":3,"output_usd_per_mtok":15}]}`))
	if err != nil {
		t.Fatal(err)
	}
	rate, ok := priced.RateFor("jev-latest")
	if !ok {
		t.Fatal("the table holds jev-latest")
	}
	// 511 in at $3/Mtok and 61 out at $15/Mtok: the day's first call.
	if got := rate.USD(511, 61); math.Abs(got-0.002448) > 1e-9 {
		t.Errorf("usd = %.6f, want 0.002448", got)
	}
	if _, err := ParseRegistry([]byte(`{"minds":[{"name":"f","lineage":"d","height":0,"availability":"available","ask":"card"}],
	  "rates":[{"model":"m","input_usd_per_mtok":-1,"output_usd_per_mtok":1}]}`)); err == nil {
		t.Error("a negative rate is not a discount")
	}
}

// Merging floors into a registry document leaves every other field of the file
// exactly as it was -- the comments above all, which are where the ladder
// explains itself -- and what is written parses back as a registry.
func TestMergeFloorsKeepsTheRestOfTheFile(t *testing.T) {
	source := DefaultRegistryJSON()
	out, err := MergeFloors(source, []KindFloor{{Kind: KindRebase, Floor: 0.55, From: "a test"}})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"comment", "minds", "floors_comment", "rates_comment", "rates"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("the merge dropped %q from the document", key)
		}
	}
	reg, err := ParseRegistry(out)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := reg.FloorFor(KindRebase)
	if !ok || f.Floor != 0.55 {
		t.Errorf("the merged floor is %+v, %v", f, ok)
	}
	if _, ok := reg.FloorFor(KindSpec); ok {
		t.Error("the merge replaces the floors table; it does not add to it")
	}
	// It is a file, so it round-trips through one.
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRegistry(path); err != nil {
		t.Errorf("the written registry does not load: %v", err)
	}
}
