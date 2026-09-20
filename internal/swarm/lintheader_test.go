package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE FOUR TOKENS OF SPEC-TOOLWORK §5 RULE 1 (issue #1651), RED FIRST.
//
// `cut` writes five typed lines under the contract line; internal/pulse/cardheader.go
// (T03, PR #1721 @f927bccc) parses them; this file is what `lint --card` checks before
// any spend, so the three readers agree. Each test below was red before LintCardHeader
// existed -- the package did not compile -- and each one has a negative control beside
// it: a card that is right in exactly the way the red card is wrong, drawing nothing.

// typedCard renders a card with the header lines it is handed, in order, under the
// contract line and above the prose. An empty value means the line is written with an
// empty value; a line absent from the slice is not written at all.
func typedCard(header ...string) []byte {
	lines := []string{"RESULT: CARD-0000 do the thing"}
	lines = append(lines, header...)
	lines = append(lines,
		"",
		"You are a Go engineer.",
		"STEP 1. cd repo",
		"STEP 2. Write RESULT.md.",
		"")
	return []byte(strings.Join(lines, "\n"))
}

// fullHeader is a header with every line present and every value one the parser reads.
func fullHeader() []string {
	return []string{
		"KIND: fix-red",
		"PATHS: internal/swarm/lintheader.go, internal/swarm/lintheader_test.go",
		"TEST: internal/swarm TestCardHeaderMissingKindDrawsKindDeclared",
		"LEGS: go",
		"SOURCE: mas-bandwidth/nova-tools#1651",
	}
}

// checks is the set of tokens a finding list draws, so a test names what it wants and
// what it does not want in one place.
func checks(fs []CardHeaderFinding) map[string]bool {
	out := map[string]bool{}
	for _, f := range fs {
		out[f.Check] = true
	}
	return out
}

func drew(t *testing.T, fs []CardHeaderFinding, want string) CardHeaderFinding {
	t.Helper()
	for _, f := range fs {
		if f.Check == want {
			return f
		}
	}
	t.Fatalf("no %s among %v", want, checks(fs))
	return CardHeaderFinding{}
}

// ---- kind-declared ---------------------------------------------------------------

func TestCardHeaderMissingKindDrawsKindDeclared(t *testing.T) {
	h := fullHeader()[1:] // everything but KIND:
	fs := LintCardHeader(typedCard(h...), nil, false)
	f := drew(t, fs, "kind-declared")
	if f.Line < 1 {
		t.Errorf("a finding names a 1-based line, got %d", f.Line)
	}
}

// An empty value is not a declaration: `KIND:` with nothing after it leaves the parser's
// Kind empty, which is exactly what Missing() names (cardheader.go:138-150).
func TestCardHeaderEmptyKindValueDrawsKindDeclared(t *testing.T) {
	fs := LintCardHeader(typedCard(append([]string{"KIND:"}, fullHeader()[1:]...)...), nil, false)
	f := drew(t, fs, "kind-declared")
	if f.Line != 2 {
		t.Errorf("the empty KIND: line is line 2, the finding says %d", f.Line)
	}
}

// NEGATIVE CONTROL for kind-declared: the same card with a kind draws nothing at all.
func TestCardHeaderFullHeaderDrawsNothing(t *testing.T) {
	if fs := LintCardHeader(typedCard(fullHeader()...), nil, true); len(fs) != 0 {
		t.Fatalf("a complete header is clean, drew %v", fs)
	}
}

// ---- test-named ------------------------------------------------------------------

func TestCardHeaderNoTestLineDrawsTestNamed(t *testing.T) {
	h := []string{fullHeader()[0], fullHeader()[1], fullHeader()[3], fullHeader()[4]}
	fs := LintCardHeader(typedCard(h...), nil, false)
	drew(t, fs, "test-named")
	if checks(fs)["kind-declared"] || checks(fs)["paths-declared"] {
		t.Errorf("only the TEST: line is missing, drew %v", checks(fs))
	}
}

// The parser refuses a TEST: value that is not `<package> <TestName>` and not `none`
// (cardheader.go:95-108). What it refuses, the lint refuses.
func TestCardHeaderMalformedTestDrawsTestNamed(t *testing.T) {
	for _, bad := range []string{
		"TEST: TestOnlyAName",
		"TEST: internal/swarm TestOne TestTwo",
		"TEST: internal/swarm lintTheThing",
		"TEST: ../outside TestUp",
	} {
		h := append([]string{}, fullHeader()...)
		h[2] = bad
		fs := LintCardHeader(typedCard(h...), nil, false)
		if !checks(fs)["test-named"] {
			t.Errorf("%q is not a TEST: the gate can use; drew %v", bad, checks(fs))
		}
	}
}

// NEGATIVE CONTROL for test-named: `TEST: none` is what the parser reads for a kind that
// declares no gate (cardheader.go:91-94), so it is a declaration, not a defect.
func TestCardHeaderTestNoneDrawsNothing(t *testing.T) {
	h := append([]string{}, fullHeader()...)
	h[2] = "TEST: none"
	if fs := LintCardHeader(typedCard(h...), nil, true); len(fs) != 0 {
		t.Fatalf("`TEST: none` is a declaration, drew %v", fs)
	}
}

// ---- paths-declared --------------------------------------------------------------

func TestCardHeaderBadPathsDrawPathsDeclared(t *testing.T) {
	for _, bad := range []string{
		"PATHS: ../other/**",           // above the job
		"PATHS: internal/../../etc/**", // above the job, by climbing
		"PATHS: **",                    // a bare **: every file in the repository
		"PATHS: **/*",                  // likewise, spelled the way that walked past the by-name refusal
		"PATHS: */**",                  // likewise
		// `**/*.go` and `*/*.go` WERE on this list, and they are not defects.
		// `hygiene.ValidatePaths` -- the validator `cut` and the diff judge use, and
		// which this lint now calls instead of restating -- clears them by name in its
		// own doc comment: what bounds a glob is a literal character somewhere in it,
		// and `.go` is one. The copy that used to live here refused them, so a card
		// writer was stopped on the bench by a glob the gate would have taken (#1853,
		// Emma's item-4 dogfood). A lint stricter than its gate is as wrong as a lint
		// looser than it.
		"PATHS: /etc/passwd",                 // not repository-relative
		`PATHS: C:/Windows/system32/evil.go`, // absolute on every bench, drive letter and all
		"PATHS: internal/swarm/*.go, **",     // one good glob does not excuse the other
	} {
		h := append([]string{}, fullHeader()...)
		h[1] = bad
		fs := LintCardHeader(typedCard(h...), nil, false)
		if !checks(fs)["paths-declared"] {
			t.Errorf("%q is not a PATHS: the gate can use; drew %v", bad, checks(fs))
		}
	}
}

func TestCardHeaderNoPathsLineDrawsPathsDeclared(t *testing.T) {
	h := []string{fullHeader()[0], fullHeader()[2], fullHeader()[3], fullHeader()[4]}
	fs := LintCardHeader(typedCard(h...), nil, false)
	drew(t, fs, "paths-declared")
}

// NEGATIVE CONTROL for paths-declared: globs with a literal segment and no climb, and
// `PATHS: none`, which is what a read kind carries (SPEC-TOOLWORK §5 rule 2).
func TestCardHeaderGoodPathsDrawNothing(t *testing.T) {
	for _, good := range []string{
		"PATHS: internal/swarm/lintheader.go",
		"PATHS: cmd/nova-swarm/**",
		"PATHS: internal/swarm/*.go, cmd/nova-swarm/testdata/firstrun/**",
		"PATHS: none",
	} {
		h := append([]string{}, fullHeader()...)
		h[1] = good
		if fs := LintCardHeader(typedCard(h...), nil, true); len(fs) != 0 {
			t.Errorf("%q is a PATHS: the gate can use, drew %v", good, fs)
		}
	}
}

// ---- paused ----------------------------------------------------------------------

func writeTrust(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "trust.tsv")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// The trust state does not exist yet: `nova-pulse trust` is T06a's other half and lives
// in GATE's lane. Until it does, the state is read from a fixture in the shape rule 1's
// listing prints, so the day the verb lands its own output is the fixture.
func TestCardHeaderPausedKindDrawsPausedAndTheTrialRemedy(t *testing.T) {
	p := writeTrust(t, strings.Join([]string{
		"TRUST kind=fix-red area=- state=paused cards=3/10 pass=0.33 need=0.80 run_of_fails=3/3 since=2026-09-19T14:00:00Z by=rowan",
		"TRUST kind=sweep area=- state=trial cards=0/10 pass=- need=0.80 run_of_fails=0/3 since=- by=-",
		"TRUST OK kinds=2 trial=1 trusted=0 paused=1",
		"",
	}, "\n"))
	trust, err := ReadTrustFixture(p)
	if err != nil {
		t.Fatal(err)
	}
	fs := LintCardHeader(typedCard(fullHeader()...), trust, false)
	f := drew(t, fs, "paused")
	if !strings.Contains(f.Excerpt, "fix-red") {
		t.Errorf("the finding names the paused kind: %q", f.Excerpt)
	}
	if !strings.Contains(CardHeaderRemedies["paused"], "trust --set trial") {
		t.Errorf("the paused remedy is the `trust --set trial` command: %q", CardHeaderRemedies["paused"])
	}
}

// NEGATIVE CONTROL for paused: the same fixture, a kind that is on trial in it.
func TestCardHeaderTrialKindDrawsNoPaused(t *testing.T) {
	p := writeTrust(t, "TRUST kind=fix-red area=- state=trial cards=0/10 pass=- need=0.80 run_of_fails=0/3 since=- by=-\n")
	trust, err := ReadTrustFixture(p)
	if err != nil {
		t.Fatal(err)
	}
	if fs := LintCardHeader(typedCard(fullHeader()...), trust, true); len(fs) != 0 {
		t.Fatalf("a kind on trial is cut and launched, drew %v", fs)
	}
}

// NEGATIVE CONTROL for paused: with no fixture there is no state, and the lint says
// nothing rather than guessing.
func TestCardHeaderNoTrustFixtureDrawsNoPaused(t *testing.T) {
	if fs := LintCardHeader(typedCard(fullHeader()...), nil, true); len(fs) != 0 {
		t.Fatalf("no trust state is not a paused state, drew %v", fs)
	}
}

// ---- the header block is the parser's block --------------------------------------

// The parser stops at the first non-empty line that is not `KEY: value`
// (cardheader.go:70-76), so a KIND: below the prose is not a declaration. A lint that
// read it would pass a card the gate refuses.
func TestCardHeaderStopsWhereTheParserStops(t *testing.T) {
	raw := []byte(strings.Join([]string{
		"RESULT: CARD-0000 do the thing",
		"PATHS: internal/swarm/*.go",
		"TEST: internal/swarm TestSomething",
		"You are a Go engineer.",
		"KIND: fix-red",
		"",
	}, "\n"))
	fs := LintCardHeader(raw, nil, false)
	drew(t, fs, "kind-declared")
}

// An unknown KEY: line does not end the block: the parser's switch ignores it and reads
// on (cardheader.go:78-122), so MODE: between two header lines keeps both.
func TestCardHeaderUnknownKeyDoesNotEndTheBlock(t *testing.T) {
	h := append([]string{}, fullHeader()...)
	h = append(h[:2], append([]string{"MODE: explore"}, h[2:]...)...)
	if fs := LintCardHeader(typedCard(h...), nil, true); len(fs) != 0 {
		t.Fatalf("an unknown KEY: line is read past, not stopped at; drew %v", fs)
	}
}

// NEGATIVE CONTROL for the whole set: a card with no typed header at all is a card of
// the shape that predates §5, and it is checked by the twelve older rules only -- unless
// the header is required, and then it draws all three of the missing-line tokens.
func TestCardHeaderUntypedCardIsCheckedOnlyWhenRequired(t *testing.T) {
	raw := typedCard()
	if fs := LintCardHeader(raw, nil, false); len(fs) != 0 {
		t.Fatalf("an untyped card is not header-checked unless asked, drew %v", fs)
	}
	got := checks(LintCardHeader(raw, nil, true))
	for _, want := range []string{"kind-declared", "paths-declared", "test-named"} {
		if !got[want] {
			t.Errorf("a required header draws %s on a card that has none; drew %v", want, got)
		}
	}
}

// Every token this file draws carries a remedy, the way every older token does (#1464).
func TestCardHeaderEveryTokenHasARemedy(t *testing.T) {
	for _, tok := range CardHeaderChecks() {
		if strings.TrimSpace(CardHeaderRemedies[tok]) == "" {
			t.Errorf("%s carries no remedy line", tok)
		}
	}
	if len(CardHeaderChecks()) != 4 {
		t.Errorf("§5 rule 1 names four tokens, this holds %v", CardHeaderChecks())
	}
}
