package main

import (
	"strings"
	"testing"
)

// platform_budget_test.go is the contract for THE BUDGET A PLATFORM IS JUDGED
// AGAINST.
//
// The record grew a second bench in #1411, and the rule that came with it was
// that only the `[budget]`-marked section is enforced -- for a good reason: a
// change cannot be answerable for whichever machine somebody happened to
// measure on. The consequence, though, is that darwin has NO ceiling at all.
// `cmd/nova-wake` is recorded at 62.9 s on the Air, over a minute, over the
// two-minute rule's target, and nothing reads it. An unenforced number is a
// number that drifts.
//
// The answer is not one budget for every machine; it is one budget PER
// PLATFORM. A section may state a `budget-factor:` in its heading -- what the
// same suite costs on that platform relative to the budget bench -- and its
// ceiling is `60 s x factor`. darwin/arm64's factor is 2.2, the whole-suite
// ratio #1411 measured (about 380 s against 170 s). A platform with no section
// falls back to the budget bench's plain sixty, so a new platform is never
// silently unbudgeted.
//
// Every case below is a record written out here rather than the real file: what
// the parser does with a heading the file does not happen to carry today is
// exactly what cannot be proved by reading the file.

// A bench's package table is the one under its own heading. The record carries
// other tables inside a bench's section whose second column is a number too,
// and reading those as package measurements is a second, contradictory row for
// the same package on the same bench -- silently.
func TestOnlyTheTableUnderTheHeadingIsTheMeasurement(t *testing.T) {
	const record = `## Bench: the Air, darwin/arm64, 8 cores, budget-factor: 2.2

| package | total seconds | slowest test |
| --- | --- | --- |
| cmd/nova-wake | 62.9 | - |

### The five biggest, against Space

| package | darwin/arm64 | linux/amd64 | ratio |
| --- | --- | --- | --- |
| cmd/nova-wake | 62.9 | 12.4 | 5.1x |
| cmd/nova-merge | 51.4 | 16.7 | 3.1x |
`
	benches, loose, err := parseRecord(record)
	if err != nil {
		t.Fatalf("parseRecord: %v", err)
	}
	if len(loose) != 0 {
		t.Errorf("rows inside a bench's sub-table read as loose: %v", loose)
	}
	if len(benches) != 1 {
		t.Fatalf("parsed %d sections, want 1", len(benches))
	}
	if len(benches[0].rows) != 1 || benches[0].rows[0].pkg != "cmd/nova-wake" {
		t.Errorf("the Air's package rows = %v, want the one row under its heading", benches[0].rows)
	}
}

// A record with the two benches the file holds, plus rows chosen so each
// platform's verdict differs from the other's.
const twoBenchRecord = `# Test durations

Prose above the first heading, and no table in it.

## Bench: Space, linux/amd64, 16 cores [budget]

| package | total seconds | slowest test |
| --- | --- | --- |
| cmd/nova-bus | 30.4 | - |
| cmd/nova-wake | 12.4 | - |

### Tests over five seconds

| package | test | seconds |
| --- | --- | --- |
| internal/ci | TestEveryToolPrintsTheOneVersionLine | 5.3 |

## Bench: the Air, darwin/arm64, 8 cores, budget-factor: 2.2

| package | total seconds | slowest test |
| --- | --- | --- |
| cmd/nova-wake | 62.9 | TestTheRecoveryReadsTheCountAndNeverAConstant 7.5 |
| cmd/nova-bus | 47.2 | - |
`

// The platform's own section is what its packages are judged against, at its
// own factor; a platform the record does not name falls back to the budget
// bench.
func TestTheBudgetIsThePlatformsWhenTheRecordHasOne(t *testing.T) {
	benches, loose, err := parseRecord(twoBenchRecord)
	if err != nil {
		t.Fatalf("parseRecord: %v", err)
	}
	if len(loose) != 0 {
		t.Fatalf("rows outside a bench section: %v", loose)
	}

	cases := []struct {
		platform string
		want     float64
		bench    string
	}{
		// The budget bench: factor 1, so the plain sixty.
		{"linux/amd64", 60.0, "Space"},
		// The Air: 60 x 2.2. A real ceiling where there was none.
		{"darwin/arm64", 132.0, "the Air"},
		// Not in the record. It gets the budget bench's sixty rather than
		// nothing at all, so a platform is never silently unbudgeted.
		{"windows/amd64", 60.0, "Space"},
		// Same OS, different arch, is a different platform and is not the
		// Air: an arm64 Linux bench does not inherit a Mac's factor.
		{"linux/arm64", 60.0, "Space"},
	}
	for _, c := range cases {
		t.Run(c.platform, func(t *testing.T) {
			got, bench, err := budgetFor(benches, c.platform)
			if err != nil {
				t.Fatalf("budgetFor(%s): %v", c.platform, err)
			}
			if got != c.want {
				t.Errorf("budgetFor(%s) = %.1fs, want %.1fs", c.platform, got, c.want)
			}
			if !strings.Contains(bench.heading, c.bench) {
				t.Errorf("budgetFor(%s) came from %q, want the %s section", c.platform, bench.heading, c.bench)
			}
		})
	}
}

// The heading is where a section says what it is. Both things read out of it
// are pinned, because both decide a verdict.
func TestABenchHeadingNamesItsPlatformAndItsFactor(t *testing.T) {
	benches, _, err := parseRecord(twoBenchRecord)
	if err != nil {
		t.Fatalf("parseRecord: %v", err)
	}
	if len(benches) != 2 {
		t.Fatalf("parsed %d bench sections, want 2", len(benches))
	}
	space, air := benches[0], benches[1]
	if space.platform != "linux/amd64" {
		t.Errorf("the Space heading's platform = %q, want linux/amd64", space.platform)
	}
	if !space.budget {
		t.Errorf("the Space heading is marked %s and did not read as the budget bench", budgetMark)
	}
	if space.factor != 1 {
		t.Errorf("a heading stating no factor = %v, want 1", space.factor)
	}
	if air.platform != "darwin/arm64" {
		t.Errorf("the Air heading's platform = %q, want darwin/arm64", air.platform)
	}
	if air.budget {
		t.Errorf("the Air heading is not marked %s and read as the budget bench", budgetMark)
	}
	if air.factor != 2.2 {
		t.Errorf("the Air heading's factor = %v, want 2.2", air.factor)
	}
}

// A factor that cannot be read is a REFUSAL, never a silent 1. A budget that
// quietly falls back to the wrong number is the failure this whole file exists
// to stop, and it would be invisible.
func TestAnUnreadableFactorIsRefused(t *testing.T) {
	for _, bad := range []string{
		"## Bench: b, darwin/arm64, budget-factor: two",
		"## Bench: b, darwin/arm64, budget-factor:",
		"## Bench: b, darwin/arm64, budget-factor: 0",
		"## Bench: b, darwin/arm64, budget-factor: -1",
	} {
		t.Run(bad, func(t *testing.T) {
			_, _, err := parseRecord(bad + "\n\n| pkg | 1.0 | - |\n")
			if err == nil {
				t.Fatalf("%q parsed without an error; an unreadable factor must refuse", bad)
			}
			if !strings.Contains(err.Error(), "budget-factor") {
				t.Errorf("error = %q, want it to name budget-factor", err)
			}
		})
	}
}

// And the whole point, stated as the verdict it produces: with the factor in
// place, a darwin row over its own ceiling is found. Before this, no darwin row
// was ever over anything.
func TestADarwinRowOverItsOwnCeilingIsFound(t *testing.T) {
	record := strings.Replace(twoBenchRecord, "| cmd/nova-wake | 62.9 |", "| cmd/nova-wake | 140.0 |", 1)
	benches, _, err := parseRecord(record)
	if err != nil {
		t.Fatalf("parseRecord: %v", err)
	}
	budget, bench, err := budgetFor(benches, "darwin/arm64")
	if err != nil {
		t.Fatalf("budgetFor: %v", err)
	}
	over := []string{}
	for _, r := range bench.rows {
		if r.secs > budget {
			over = append(over, r.pkg)
		}
	}
	if len(over) != 1 || over[0] != "cmd/nova-wake" {
		t.Errorf("over the darwin ceiling of %.1fs: %v, want [cmd/nova-wake]", budget, over)
	}
	// And 62.9 -- the number actually recorded -- is UNDER that ceiling, so
	// this change is a mechanism and not a red on somebody else's package.
	benches, _, err = parseRecord(twoBenchRecord)
	if err != nil {
		t.Fatalf("parseRecord: %v", err)
	}
	_, bench, _ = budgetFor(benches, "darwin/arm64")
	for _, r := range bench.rows {
		if r.secs > budget {
			t.Errorf("%s at %.1fs is over the darwin ceiling of %.1fs as recorded", r.pkg, r.secs, budget)
		}
	}
}
