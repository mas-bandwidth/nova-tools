package prereview_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/prereview"
)

// The cells under testdata/cells are the pull requests two friends read
// line-by-line and classified in rowan-new reports/false-confidence-cells-2026-09-22.md.
// They are the calibration set for the symbol check and they are checked in, so
// a later widening of the patterns argues with the evidence and not with
// whoever widened them.
const cellDir = "testdata/cells"

// fixtureDir holds the ONE recorded Jev pass over all 122 cells (2026-09-22).
// A Jev call costs money, so the tests replay these and dial nothing.
const fixtureDir = "testdata/jev-2026-09-22"

const repo = "mas-bandwidth/schema"

// loadCell reads one recorded pull request the way the verb's gh fetch builds it.
func loadCell(t *testing.T, n int) prereview.PR {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(cellDir, strconv.Itoa(n)+".json"))
	if err != nil {
		t.Fatalf("cell #%d: %v", n, err)
	}
	var w struct {
		HeadRefOid string `json:"headRefOid"`
		Title      string `json:"title"`
		Body       string `json:"body"`
		Files      []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatalf("cell #%d: %v", n, err)
	}
	diff, err := os.ReadFile(filepath.Join(cellDir, strconv.Itoa(n)+".diff"))
	if err != nil {
		t.Fatalf("cell #%d diff: %v", n, err)
	}
	pr := prereview.PR{Repo: repo, Number: n, Head: w.HeadRefOid, Title: w.Title, Body: w.Body, Diff: string(diff)}
	for _, f := range w.Files {
		pr.Files = append(pr.Files, f.Path)
	}
	return pr
}

func checksFor(t *testing.T, n int) prereview.Checks {
	t.Helper()
	pr := loadCell(t, n)
	return prereview.Mechanical(pr, prereview.InferCard(pr))
}

// TestSymbolCheckOnTheClassifiedCells is the DONE-WHEN of nova-tools #2565, as a
// test rather than as a sentence in a report: the symbol check says NO for every
// pull request two friends read and called a self-check, and YES for the two
// they kept as the runtime exemplars.
func TestSymbolCheckOnTheClassifiedCells(t *testing.T) {
	selfCheck := []int{1459, 1469, 1493, 1507, 1558, 1486, 1497, 1506, 1539, 1561}
	for _, n := range selfCheck {
		if got := checksFor(t, n).Symbol; got.Result != prereview.No {
			t.Errorf("#%d is a self-check a friend read line-by-line; symbol=%s (%s), want no", n, got.Result, got.Reason)
		}
	}
	for _, n := range []int{1488, 1556} {
		if got := checksFor(t, n).Symbol; got.Result != prereview.Yes {
			t.Errorf("#%d is a runtime exemplar; symbol=%s (%s), want yes", n, got.Result, got.Reason)
		}
	}
}

// TestPathsCatchesAChangeOutsideTheCardsLeg is the scope check on a real case:
// #1569 adds a whole stub crate beside its one test file, and #1560 drops a
// notes.txt next to its cell. Neither is inside the conformance leg the cell
// names, and neither friend's read caught it.
func TestPathsCatchesAChangeOutsideTheCardsLeg(t *testing.T) {
	for _, tc := range []struct {
		pr   int
		want string
	}{{1569, "serialize-stub"}, {1560, "notes.txt"}} {
		got := checksFor(t, tc.pr).Paths
		if got.Result != prereview.No {
			t.Errorf("#%d paths=%s (%s), want no", tc.pr, got.Result, got.Reason)
			continue
		}
		if !strings.Contains(got.Reason, tc.want) {
			t.Errorf("#%d paths reason %q does not name %q", tc.pr, got.Reason, tc.want)
		}
	}
}

// TestClaimsCatchesAFileTheResultNamesAndTheDiffDoesNot: #1488's RESULT claims
// it touched test/conformance/go/go.mod; the diff has one file and that is not
// it. #1477 claims two files it never changed.
func TestClaimsCatchesAFileTheResultNamesAndTheDiffDoesNot(t *testing.T) {
	for _, n := range []int{1488, 1477} {
		got := checksFor(t, n).Claims
		if got.Result != prereview.No {
			t.Errorf("#%d claims=%s (%s), want no", n, got.Result, got.Reason)
		}
		if !strings.Contains(got.Reason, "go.mod") {
			t.Errorf("#%d claims reason %q does not name the missing file", n, got.Reason)
		}
	}
}

// TestDoneReadsLineTwoOfTheResult. Every one of the 122 harvested cells carries
// a bare DONE on line 2, so the interesting cases are the ones that do not.
func TestDoneReadsLineTwoOfTheResult(t *testing.T) {
	if got := checksFor(t, 1488).Done; got.Result != prereview.Yes {
		t.Fatalf("#1488 done=%s (%s), want yes", got.Result, got.Reason)
	}
	pr := loadCell(t, 1488)
	pr.Body = strings.Replace(pr.Body, "\nDONE\n", "\nDONE (with notes)\n", 1)
	got := prereview.Mechanical(pr, prereview.InferCard(pr)).Done
	if got.Result != prereview.No {
		t.Fatalf("a qualified DONE should not pass: done=%s (%s)", got.Result, got.Reason)
	}
}

// TestMissingIsNotNo. A check with nothing to decide on answers missing, and
// missing holds the pull request exactly as no does. The bad shape this exists
// against is a row that prints `paths:no` for a pull request whose card nobody
// could find -- a check that never ran, reported as a check that failed.
func TestMissingIsNotNo(t *testing.T) {
	pr := loadCell(t, 1488)
	pr.Body = "RESULT something\nDONE\n" // no cell, no branch, no files: line
	c := prereview.Mechanical(pr, prereview.InferCard(pr))
	if c.Paths.Result != prereview.Missing {
		t.Errorf("paths=%s, want missing when nothing declares a path", c.Paths.Result)
	}
	if c.Claims.Result != prereview.Missing {
		t.Errorf("claims=%s, want missing when the RESULT names no files", c.Claims.Result)
	}
	if c.Clear() {
		t.Error("a missing check must not clear the pull request")
	}
	if v := prereview.Decide(c, 10, true); v != prereview.Hold {
		t.Errorf("verdict=%s with a missing check and a 10, want HOLD", v)
	}
	if !strings.Contains(c.Field(), "paths:missing") {
		t.Errorf("checks field %q does not carry missing", c.Field())
	}
}

// TestCardBeatsInference. A card that declares PATHS and SYMBOL is the bound;
// the pull request body is only the fallback, and the line says which answered.
func TestCardBeatsInference(t *testing.T) {
	card := prereview.ParseCard("card.md", strings.Join([]string{
		"KIND: test",
		"PATHS: test/conformance/go/rows/*.go",
		"SYMBOL: HugeFixedLoad",
		"TEST: go test ./rows/",
	}, "\n"))
	if card.PathsFrom != "card" || card.SymbolFrom != "card" {
		t.Fatalf("card origins = %s/%s, want card/card", card.PathsFrom, card.SymbolFrom)
	}
	pr := loadCell(t, 1488)
	c := prereview.Mechanical(pr, card)
	if c.Symbol.Result != prereview.Yes {
		t.Errorf("symbol=%s (%s); #1488's test does name HugeFixedLoad", c.Symbol.Result, c.Symbol.Reason)
	}
	card.Symbol = "NoSuchGeneratedSymbol"
	if got := prereview.Mechanical(pr, card).Symbol; got.Result != prereview.No {
		t.Errorf("symbol=%s for a SYMBOL the test never names, want no", got.Result)
	}
}

// TestInferenceIsNotATautology. The inferred bound comes from the cell's
// identity (`cell: go/R6`), never from the list of files the diff happens to
// touch -- otherwise the paths check would pass by construction and the row
// would say a check ran that decided nothing.
func TestInferenceIsNotATautology(t *testing.T) {
	pr := loadCell(t, 1488)
	card := prereview.InferCard(pr)
	if card.PathsFrom != "pr-body-cell" {
		t.Fatalf("paths_from=%s, want pr-body-cell", card.PathsFrom)
	}
	if len(card.Paths) != 1 || card.Paths[0] != "test/conformance/go/**" {
		t.Fatalf("inferred paths = %v, want the go conformance leg", card.Paths)
	}
	pr.Files = append(pr.Files, "internal/codegen/gotable/fixedform.go")
	if got := prereview.Mechanical(pr, card).Paths; got.Result != prereview.No {
		t.Fatalf("paths=%s for a file outside the leg, want no", got.Result)
	}
}

// TestFoldedResultIsStillRead. Two of the 122 fold every RESULT fact onto one
// semicolon-joined line. A start-of-line rule read those as having no cell at
// all, which is how a check quietly stops checking.
func TestFoldedResultIsStillRead(t *testing.T) {
	pr := prereview.PR{
		Repo: repo, Number: 1, Head: "0",
		Body:  "RESULT cell-rust-r30\nDONE\nBRANCH rowan/cell-rust-r30\nlaw: x; cell: rust/R30; files: test/conformance/rust/rows/R30.rs; run: rustc\n",
		Files: []string{"test/conformance/rust/rows/R30.rs"},
		Diff:  "+++ b/test/conformance/rust/rows/R30.rs\n+use build/tables-generated-rust;\n",
	}
	card := prereview.InferCard(pr)
	if len(card.Paths) != 1 || card.Paths[0] != "test/conformance/rust/**" {
		t.Fatalf("inferred paths = %v from a folded RESULT", card.Paths)
	}
	c := prereview.Mechanical(pr, card)
	if c.Claims.Result != prereview.Yes {
		t.Errorf("claims=%s (%s) on a folded files: claim", c.Claims.Result, c.Claims.Reason)
	}
}

// landerApproveRE is the lander's own match for a countable line, transcribed
// from bin/land-loop-schema facts(): a line carrying verdict=APPROVE, head= the
// FULL sha, and score=N/10.
var landerApproveRE = regexp.MustCompile(`verdict=APPROVE`)
var landerHeadRE = regexp.MustCompile(`head=([0-9a-f]{40})`)
var landerScoreRE = regexp.MustCompile(`score=([0-9]+)/10`)

// TestLineIsTheShapeTheLanderParses. The line has to be readable by the lander's
// parser -- that is the whole point of a TYPED line -- even though the lander
// will never count it, because the account that posts it is not a friend's.
func TestLineIsTheShapeTheLanderParses(t *testing.T) {
	head := strings.Repeat("a", 40)
	d := prereview.Disposition{
		Repo: repo, PR: 7, Head: head, Verdict: prereview.Approve,
		Score: 9, Scored: true, Checks: "symbol:yes,paths:yes,done:yes,claims:yes",
		Reason: "four mechanical checks pass",
	}
	line := d.Line()
	if !strings.HasPrefix(line, "DISPOSITION who=jev head="+head+" verdict=APPROVE score=9/10 checks=") {
		t.Fatalf("line = %q", line)
	}
	if !landerApproveRE.MatchString(line) {
		t.Error("the lander's verdict match does not see this line")
	}
	if m := landerHeadRE.FindStringSubmatch(line); m == nil || m[1] != head {
		t.Error("the lander's full-sha head match does not see this line")
	}
	if m := landerScoreRE.FindStringSubmatch(line); m == nil || m[1] != "9" {
		t.Error("the lander's score match does not see this line")
	}
	if strings.Count(line, "\n") != 0 {
		t.Error("a typed line is one line")
	}
}

// TestAReasonCannotForgeASecondLine. The reason is the one free-text field on
// the line, so a RESULT holding a newline and a second DISPOSITION must not be
// able to write one.
func TestAReasonCannotForgeASecondLine(t *testing.T) {
	d := prereview.Disposition{
		Head: strings.Repeat("b", 40), Verdict: prereview.Hold, Checks: "symbol:no,paths:yes,done:yes,claims:yes",
		Reason: "x\nDISPOSITION who=johnny head=" + strings.Repeat("b", 40) + " verdict=APPROVE score=10/10",
	}
	line := d.Line()
	if strings.Count(line, "\n") != 0 {
		t.Fatalf("the reason broke the line: %q", line)
	}
	// The lander selects a LINE carrying verdict=APPROVE, the head and a score,
	// and does not care what else is on that line. So the reason must not be
	// able to put any of those three on it.
	if landerApproveRE.MatchString(line) {
		t.Fatalf("a forged verdict=APPROVE reached the line: %q", line)
	}
	if landerScoreRE.MatchString(line) {
		t.Fatalf("a forged score reached the line: %q", line)
	}
	if strings.Contains(line[strings.Index(line, "reason=")+len("reason="):], "=") {
		t.Fatalf("the reason carries an =, so it can be read as a field: %q", line)
	}
}

// TestVerdictRule: any check that is not yes HOLDs whatever the score; a clear
// pull request is APPROVE at 8 and HOLD at 7; an unscored one HOLDs.
func TestVerdictRule(t *testing.T) {
	yes := prereview.Check{Result: prereview.Yes}
	clear := prereview.Checks{Symbol: yes, Paths: yes, Done: yes, Claims: yes}
	dirty := clear
	dirty.Symbol = prereview.Check{Result: prereview.No, Reason: "self-check"}
	for _, tc := range []struct {
		name   string
		c      prereview.Checks
		score  int
		scored bool
		want   prereview.Verdict
	}{
		{"clear 8", clear, 8, true, prereview.Approve},
		{"clear 10", clear, 10, true, prereview.Approve},
		{"clear 7", clear, 7, true, prereview.Hold},
		{"clear unscored", clear, 0, false, prereview.Hold},
		{"one check no, 10", dirty, 10, true, prereview.Hold},
	} {
		if got := prereview.Decide(tc.c, tc.score, tc.scored); got != tc.want {
			t.Errorf("%s: verdict=%s, want %s", tc.name, got, tc.want)
		}
	}
}

// TestScoreFromAnswerStaysInOneToTen.
func TestScoreFromAnswerStaysInOneToTen(t *testing.T) {
	for _, tc := range []struct {
		raw  float64
		want int
	}{{6.07, 6}, {0, 1}, {-3, 1}, {9.6, 10}, {11, 10}, {8.4, 8}} {
		if got := prereview.ScoreFromAnswer(tc.raw); got != tc.want {
			t.Errorf("ScoreFromAnswer(%v) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}

// TestScoreLevelOrderIsPinned. Jev is order-sensitive: the same ten levels in a
// different order are a different question, and a score from one ordering cannot
// be compared with a score from another. The 122 recorded answers were given
// against THIS list in THIS order, so a change to it must break a test and force
// a new calibration run rather than silently invalidate the ledger.
func TestScoreLevelOrderIsPinned(t *testing.T) {
	sum := sha256.Sum256([]byte(strings.Join(prereview.ScoreLevels, "\x00")))
	const want = "816c4381d33705e87d6f08bf8de0981368b61aa5e030a6e0e89352442503ce86"
	if got := hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("the score levels changed (sha256 %s).\n"+
			"Every answer in %s and in the ledger was given against the previous ordering.\n"+
			"If the change is intended, re-run the pass and update this constant to %s.", got, fixtureDir, got)
	}
}

// TestEveryRecordedAnswerReplays. All 122 fixtures load, validate against the
// question that was asked, and map into 1-10.
func TestEveryRecordedAnswerReplays(t *testing.T) {
	entries, err := os.ReadDir(fixtureDir)
	if err != nil {
		t.Fatalf("fixtures: %v", err)
	}
	n := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		num := strings.TrimSuffix(strings.TrimPrefix(e.Name(), "mas-bandwidth-schema-"), ".json")
		pr, err := strconv.Atoi(num)
		if err != nil {
			t.Fatalf("fixture %s is not named for a pull request", e.Name())
		}
		answers, err := prereview.FixtureAsker{Dir: fixtureDir, Repo: repo, PR: pr}.
			Ask(context.Background(), "", prereview.ScoreQuestion())
		if err != nil {
			t.Fatalf("#%d: %v", pr, err)
		}
		a := answers["score"]
		if s := prereview.ScoreFromAnswer(a.Score); s < 1 || s > 10 {
			t.Errorf("#%d recorded score %v maps to %d", pr, a.Score, s)
		}
		n++
	}
	if n != 122 {
		t.Fatalf("the recorded pass has %d answers, want the 122 cells", n)
	}
}

// TestFixtureAskerFindsThePullRequestInTheState. The verb hands one asker to a
// whole batch, so the asker has to tell the pull requests apart by the state it
// is given and must refuse rather than guess when it cannot.
func TestFixtureAskerFindsThePullRequestInTheState(t *testing.T) {
	pr := loadCell(t, 1488)
	card := prereview.InferCard(pr)
	raw, conf, err := prereview.Score(context.Background(),
		prereview.FixtureAsker{Dir: fixtureDir, Repo: repo}, pr, card)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	want, err := prereview.LoadFixture(fixtureDir, repo, 1488)
	if err != nil {
		t.Fatal(err)
	}
	if raw != want.Score || conf != want.Conf {
		t.Fatalf("replayed %v/%v, recorded %v/%v", raw, conf, want.Score, want.Conf)
	}
	if _, _, err := prereview.Score(context.Background(),
		prereview.FixtureAsker{Dir: fixtureDir, Repo: repo},
		prereview.PR{Repo: repo, Number: 999999}, card); err == nil {
		t.Fatal("a pull request with no recorded answer must refuse, not invent one")
	}
}

// TestStateCarriesTheDiffAndTheCardAndNoSecret.
func TestStateCarriesTheDiffAndTheCardAndNoSecret(t *testing.T) {
	pr := loadCell(t, 1488)
	state := prereview.State(pr, prereview.InferCard(pr))
	for _, want := range []string{"pull request " + repo + "#1488", "--- RESULT ---", "--- DIFF ---", "TestRowR6"} {
		if !strings.Contains(state, want) {
			t.Errorf("state does not carry %q", want)
		}
	}
	if strings.Contains(strings.ToLower(state), "api_key") || strings.Contains(state, "JEV_") {
		t.Error("the state must never carry a key")
	}
}

// TestStateSaysWhenTheDiffWasTruncated. A score given over part of a change must
// never be mistaken for one given over all of it.
func TestStateSaysWhenTheDiffWasTruncated(t *testing.T) {
	pr := loadCell(t, 1488)
	pr.Diff = strings.Repeat("+x\n", prereview.DiffCap)
	state := prereview.State(pr, prereview.InferCard(pr))
	if !strings.Contains(state, "diff truncated at") {
		t.Fatal("a truncated diff must say so in the state")
	}
}

// TestLedgerRowIsOneJSONLineWithBothScores. The raw provider answer travels
// beside the 1-10 the line printed: if the provider ever answers in level
// indexes rather than in the numbering the levels carry, the only way anyone can
// tell is that both numbers are on the record.
func TestLedgerRowIsOneJSONLineWithBothScores(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	d := prereview.Disposition{Repo: repo, PR: 1488, Head: "abc", Verdict: prereview.Hold,
		Score: 6, RawScore: 6.07, Conf: 0.21, Scored: true, Checks: "symbol:yes,paths:yes,done:yes,claims:no",
		Reason: "claims: a file the RESULT names is not in the diff"}
	if err := prereview.AppendLedger(path, d); err != nil {
		t.Fatal(err)
	}
	if err := prereview.AppendLedger(path, d); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("ledger has %d lines, want 2", len(lines))
	}
	var back prereview.Disposition
	if err := json.Unmarshal([]byte(lines[0]), &back); err != nil {
		t.Fatal(err)
	}
	if back.Score != 6 || back.RawScore != 6.07 || !back.Scored {
		t.Fatalf("ledger row = %+v", back)
	}
	if back.At == "" {
		t.Error("a ledger row with no time is not a record")
	}
}

// TestCommentSaysItLandsNothing. The posted body is read by people, and the one
// thing it must never let a reader assume is that a machine's line is a friend's.
func TestCommentSaysItLandsNothing(t *testing.T) {
	body := prereview.Disposition{Head: "abc", Verdict: prereview.Hold, PathsFrom: "pr-body-cell"}.Comment()
	for _, want := range []string{"LANDS NOTHING", "no friend has read this yet", "inferred from the pull request body"} {
		if !strings.Contains(body, want) {
			t.Errorf("the comment does not say %q", want)
		}
	}
}

// TestPathishRecognizesRootFilesAndPaths verifies that pathish correctly
// recognizes root files with extensions, extensionless paths, and paths with
// both slashes and dots, while rejecting bare prose words.
func TestPathishRecognizesRootFilesAndPaths(t *testing.T) {
	cases := []struct {
		tok  string
		want bool
	}{
		// Root files with extensions
		{"go.mod", true},
		{"Cargo.toml", true},
		{"package.json", true},
		{"README.md", true},
		{".gitignore", true},
		// Extensionless paths
		{"scripts/deploy", true},
		{"bin/test", true},
		// Paths with both slashes and extensions
		{"internal/foo.go", true},
		{"test/conformance/go/rows/R1.go", true},
		// Wrapped tokens (quotes, backticks, brackets)
		{"`go.mod`", true},
		{"\"Cargo.toml\"", true},
		{"'package.json'", true},
		{"(internal/foo.go)", true},
		{"[scripts/deploy]", true},
		// Prose words (neither slash nor dot)
		{"and", false},
		{"plus", false},
		{"fixed", false},
		{"changes", false},
		{"to", false},
		// Empty / punctuation only
		{"", false},
		{",", false},
		{"`", false},
		{"()", false},
	}
	for _, tc := range cases {
		got := prereview.Pathish(tc.tok)
		if got != tc.want {
			t.Errorf("Pathish(%q) = %v, want %v", tc.tok, got, tc.want)
		}
	}
}

// TestClaimsCheckWithGoMod tests claimsCheck with a PR claiming go.mod:
// - Positive control: claims go.mod and diff matches -> claims:yes
// - Negative control 1: claims go.mod but missing from diff -> claims:no
// - Negative control 2: claims line has only prose -> claims:missing
// - Negative control 3: no files line -> claims:missing
func TestClaimsCheckWithGoMod(t *testing.T) {
	// Positive control: claims go.mod and diff matches
	prMatch := prereview.PR{
		Repo:   repo,
		Number: 1,
		Head:   "deadbeef",
		Body:   "RESULT test-cell\nDONE\nfiles: go.mod\n",
		Files:  []string{"go.mod"},
	}
	cardMatch := prereview.InferCard(prMatch)
	gotMatch := prereview.ClaimsCheck(prMatch, cardMatch)
	if gotMatch.Result != prereview.Yes {
		t.Fatalf("positive control: claims=%s (%s), want yes", gotMatch.Result, gotMatch.Reason)
	}
	if !strings.Contains(gotMatch.Reason, "all 1 files") {
		t.Errorf("positive control reason %q does not match expected", gotMatch.Reason)
	}
	// Also check through Mechanical
	if m := prereview.Mechanical(prMatch, cardMatch).Claims; m.Result != prereview.Yes {
		t.Errorf("Mechanical claims=%s, want yes", m.Result)
	}

	// Negative control 1: claims go.mod but missing from diff -> claims:no
	prMissing := prereview.PR{
		Repo:   repo,
		Number: 2,
		Head:   "deadbeef",
		Body:   "RESULT test-cell\nDONE\nfiles: go.mod\n",
		Files:  []string{"other.go"},
	}
	cardMissing := prereview.InferCard(prMissing)
	gotMissing := prereview.ClaimsCheck(prMissing, cardMissing)
	if gotMissing.Result != prereview.No {
		t.Fatalf("negative control (missing from diff): claims=%s (%s), want no", gotMissing.Result, gotMissing.Reason)
	}
	if !strings.Contains(gotMissing.Reason, "go.mod") {
		t.Errorf("negative control reason %q does not name missing go.mod", gotMissing.Reason)
	}

	// Negative control 2: claims line has only prose words -> claims:missing
	prProse := prereview.PR{
		Repo:   repo,
		Number: 3,
		Head:   "deadbeef",
		Body:   "RESULT test-cell\nDONE\nfiles: and plus\n",
		Files:  []string{"go.mod"},
	}
	cardProse := prereview.InferCard(prProse)
	gotProse := prereview.ClaimsCheck(prProse, cardProse)
	if gotProse.Result != prereview.Missing {
		t.Fatalf("negative control (prose only): claims=%s (%s), want missing", gotProse.Result, gotProse.Reason)
	}

	// Negative control 3: no files: line -> claims:missing
	prNoLine := prereview.PR{
		Repo:   repo,
		Number: 4,
		Head:   "deadbeef",
		Body:   "RESULT test-cell\nDONE\n",
		Files:  []string{"go.mod"},
	}
	cardNoLine := prereview.InferCard(prNoLine)
	gotNoLine := prereview.ClaimsCheck(prNoLine, cardNoLine)
	if gotNoLine.Result != prereview.Missing {
		t.Fatalf("negative control (no files line): claims=%s (%s), want missing", gotNoLine.Result, gotNoLine.Reason)
	}
}

// TestInferCardWithPreambleBeforeResult tests that InferCard locates RESULT
// in pr.Body even when there is introductory text before RESULT, ensuring
// that card.Result begins at RESULT and doneCheck checks line 2 of the RESULT
// block rather than line 2 of the entire PR description.
func TestInferCardWithPreambleBeforeResult(t *testing.T) {
	pr := prereview.PR{
		Repo:   repo,
		Number: 999,
		Head:   "1234567890abcdef1234567890abcdef12345678",
		Title:  "Fix table and add conformance test",
		Body: strings.Join([]string{
			"Here is some pull request description.",
			"This line explains the PR motivation in detail.",
			"Another introductory paragraph before the machine block.",
			"",
			"RESULT cell-go-r42",
			"DONE",
			"cell: go/R42",
			"files: test/conformance/go/rows/R42.go",
		}, "\n"),
		Files: []string{"test/conformance/go/rows/R42.go"},
		Diff:  "+++ b/test/conformance/go/rows/R42.go\n+func TestR42() {}\n",
	}

	card := prereview.InferCard(pr)
	if !strings.HasPrefix(card.Result, "RESULT") {
		t.Fatalf("card.Result does not start with RESULT: %q", card.Result)
	}
	if card.PathsFrom != "pr-body-cell" {
		t.Errorf("PathsFrom = %q, want pr-body-cell", card.PathsFrom)
	}
	if len(card.Paths) != 1 || card.Paths[0] != "test/conformance/go/**" {
		t.Errorf("Paths = %v, want test/conformance/go/**", card.Paths)
	}

	// Mechanical checks must see DONE as line 2 of RESULT (not line 2 of body)
	checks := prereview.Mechanical(pr, card)
	if checks.Done.Result != prereview.Yes {
		t.Errorf("done = %s (%s), want yes", checks.Done.Result, checks.Done.Reason)
	}
	if checks.Claims.Result != prereview.Yes {
		t.Errorf("claims = %s (%s), want yes", checks.Claims.Result, checks.Claims.Reason)
	}
	if checks.Paths.Result != prereview.Yes {
		t.Errorf("paths = %s (%s), want yes", checks.Paths.Result, checks.Paths.Reason)
	}
}

// TestAppendLedgerCreatesParentDirectories verifies that AppendLedger ensures
// parent directories exist with 0755 permissions before opening the ledger file.
func TestAppendLedgerCreatesParentDirectories(t *testing.T) {
	dir := t.TempDir()
	nestedPath := filepath.Join(dir, "new-session", "deep", "dir", "ledger.jsonl")
	d := prereview.Disposition{
		Repo:    repo,
		PR:      100,
		Head:    "abc",
		Verdict: prereview.Approve,
	}
	if err := prereview.AppendLedger(nestedPath, d); err != nil {
		t.Fatalf("AppendLedger failed to create directories: %v", err)
	}
	if _, err := os.Stat(nestedPath); err != nil {
		t.Fatalf("ledger file was not created: %v", err)
	}
}
