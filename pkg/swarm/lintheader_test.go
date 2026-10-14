package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A header key inside the header block the gate reads passes: LintHeaderStranded
// answers nothing (docs/SPEC-SPRINT.md section 2, the card header).
func TestLintHeaderStrandedAcceptsAKeyInTheHeader(t *testing.T) {
	t.Parallel()
	raw := []byte("RESULT: c1 sha=000000000000 tier: flash\n" +
		"KIND: fix\n" +
		"DEPENDS-ON: -\n" +
		"PATHS: internal/x.go\n" +
		"\n" +
		"Fix c1.\n")
	if got := LintHeaderStranded(raw); len(got) != 0 {
		t.Fatalf("a key in the header drew %+v; want no finding", got)
	}
}

// A header key written below the first prose line is one finding naming the key and
// its line: the gate reads only the header block, so the key would never be read.
func TestLintHeaderStrandedNamesAKeyBelowTheProse(t *testing.T) {
	t.Parallel()
	raw := []byte("RESULT: c1 sha=000000000000 tier: flash\n" +
		"KIND: fix\n" +
		"PATHS: internal/x.go\n" +
		"\n" +
		"Fix c1.\n" +
		"DEPENDS-ON: -\n")
	got := LintHeaderStranded(raw)
	if len(got) != 1 {
		t.Fatalf("a stranded DEPENDS-ON drew %+v; want one finding", got)
	}
	if got[0].Line != 6 {
		t.Fatalf("stranded DEPENDS-ON line=%d; want 6", got[0].Line)
	}
	if !strings.Contains(got[0].Excerpt, "DEPENDS-ON") {
		t.Fatalf("excerpt %q does not name the key", got[0].Excerpt)
	}
	if !strings.Contains(got[0].Excerpt, "6") {
		t.Fatalf("excerpt %q does not name the line", got[0].Excerpt)
	}
}

// A sentence that merely mentions a key mid-line is not a `KEY: value` line and draws
// no finding: only a line that starts with the key is a header key below the block.
func TestLintHeaderStrandedIgnoresAMentionMidLine(t *testing.T) {
	t.Parallel()
	raw := []byte("RESULT: c1 sha=000000000000 tier: flash\n" +
		"KIND: fix\n" +
		"PATHS: internal/x.go\n" +
		"\n" +
		"Fix c1: read DEPENDS-ON: for the chain, not this sentence.\n")
	if got := LintHeaderStranded(raw); len(got) != 0 {
		t.Fatalf("a mid-line mention drew %+v; want no finding", got)
	}
}

// CardHeaderValue reads one key of the typed block: the first of two lines with the same
// key, the empty value of an empty line, and nothing for a key only below the prose.
func TestCardHeaderValueReadsTheBlockOnly(t *testing.T) {
	t.Parallel()

	repeated := []byte("RESULT: c1 sha=000000000000\nKIND: fix\nKIND: bug\n\nTHE TASK.\n")
	v, ok := CardHeaderValue(repeated, "KIND")
	assert.True(t, ok)
	assert.Equal(t, "fix", v, "the first line with the key is the value")

	empty := []byte("RESULT: c1 sha=000000000000\nKIND: \n\nTHE TASK.\n")
	v, ok = CardHeaderValue(empty, "KIND")
	assert.True(t, ok, "an empty value is still a present key")
	assert.Equal(t, "", v)

	below := []byte("RESULT: c1 sha=000000000000\nKIND: fix\n\nTHE TASK.\nPATHS: pkg/x.go\n")
	_, ok = CardHeaderValue(below, "PATHS")
	assert.False(t, ok, "a key only below the header block reads as absent")

	_, ok = CardHeaderValue(below, "DEPENDS-ON")
	assert.False(t, ok)
}

// CardPaths is the typed header's PATHS and NEW globs: comma separated (a space after the
// comma is trimmed), `none` names nothing, and a key below the prose grants no scope.
func TestCardPathsReadsPATHSAndNEW(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{"comma separated", "RESULT: c1 sha=0\nPATHS: a.go,b.go\n\nT.\n", []string{"a.go", "b.go"}},
		{"comma and space separated", "RESULT: c1 sha=0\nPATHS: a.go, b.go\n\nT.\n", []string{"a.go", "b.go"}},
		{"none names nothing", "RESULT: c1 sha=0\nPATHS: none\n\nT.\n", nil},
		{"empty value names nothing", "RESULT: c1 sha=0\nPATHS: \n\nT.\n", nil},
		{"NEW joins PATHS", "RESULT: c1 sha=0\nPATHS: a.go\nNEW: b.go\n\nT.\n", []string{"a.go", "b.go"}},
		{"repeated PATHS takes the first", "RESULT: c1 sha=0\nPATHS: a.go\nPATHS: b.go\n\nT.\n", []string{"a.go"}},
		{"a key below the prose grants no scope", "RESULT: c1 sha=0\nKIND: fix\n\nT.\nPATHS: a.go\n", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, CardPaths([]byte(tt.raw)))
		})
	}
}

// LintCardHeader refuses a stranded typed key, an unknown KIND, and a required header that
// is missing, each with the token the lint prints.
func TestLintCardHeaderRefusesTypedHeaderDefects(t *testing.T) {
	t.Parallel()

	stranded := []byte("RESULT: c1 sha=000000000000\nPATHS: pkg/x.go\n\nTHE TASK.\nKIND: fix\n")
	got := LintCardHeader(stranded, nil, false)
	assert.Contains(t, checksOfFindings(got), "kind-declared", "%+v", got)

	unknown := []byte("RESULT: c1 sha=000000000000\nKIND: completely-unknown-kind\nPATHS: pkg/x.go\nTEST: ./pkg TestA\n\nTHE TASK.\n")
	got = LintCardHeader(unknown, nil, false)
	require.Len(t, findingsFor(got, "kind-declared"), 1)
	assert.Contains(t, findingsFor(got, "kind-declared")[0].Excerpt, "not a kind this toolchain declares")

	missing := LintCardHeader([]byte("RESULT: c1 sha=000000000000\n\nTHE TASK.\n"), nil, true)
	for _, want := range []string{"kind-declared", "paths-declared", "test-named", "start-named", "stop-named"} {
		assert.Contains(t, checksOfFindings(missing), want, "%+v", missing)
	}
}

func checksOfFindings(fs []CardHeaderFinding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Check)
	}
	return out
}

// UnfilledTemplateLines names a line of the card template still holding its <...>
// fill-ins, and only the template's own lines: a similar line the card writes itself is
// not this finding.
func TestUnfilledTemplateLinesNamesTemplatePlaceholders(t *testing.T) {
	t.Parallel()
	card := "RESULT: c1 sha=000000000000\n" +
		"REPO: <owner>/<name>\n" +
		"The task uses --count <n> and that is fine.\n"
	got := UnfilledTemplateLines(card)
	require.Len(t, got, 1)
	assert.Equal(t, PlaceholderCheck, got[0].Check)
	assert.Equal(t, 2, got[0].Line)
	assert.Equal(t, "REPO: <owner>/<name>", got[0].Excerpt)

	assert.Empty(t, UnfilledTemplateLines("RESULT: c1 sha=0\nREPO: owner/name\n"))
}

// CardParentPaths names the card lines that reach above the job, and not a `../` the card
// merely quotes in prose (a backtick span, a markdown link target). A walk inside a fenced
// block is still named: the wall refuses it wherever the card writes it (lintparent_test.go,
// TestAWalkInsideAFenceIsStillAFinding).
func TestCardParentPathsNamesParentPathsButNotQuotedOnes(t *testing.T) {
	t.Parallel()
	lines := []string{
		"cd ../notes",                    // 1: walked
		"a line with `cat ../x`",         // 2: inline code, quoted
		"```",                            // 3: fence opens
		"cd ../inside-the-fence",         // 4: a walk, fence or no fence (the wall refuses it)
		"```",                            // 5: fence closes
		"see [the notes](../notes/x.md)", // 6: link target, quoted
		"just prose mentioning ../up",    // 7: reached
	}
	assert.Equal(t, []int{1, 4, 7}, CardParentPaths(lines))
}

// Every key the card header names is refused when it sits below the prose: one finding
// each, in line order, naming the key.
func TestLintHeaderStrandedNamesEveryStrandedHeaderKey(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"KIND", "PATHS", "TEST", "LEGS", "SOURCE", "START", "STOP",
		"DEPENDS-ON", "PRIORITY", "NEW", "SHARED", "REPO", "BASE", "tier", "RESULT"} {
		raw := []byte("RESULT: c1 sha=000000000000 tier: flash\n" +
			"KIND: fix\n" +
			"PATHS: internal/x.go\n" +
			"\n" +
			"Fix c1.\n" +
			key + ": x\n")
		got := LintHeaderStranded(raw)
		if len(got) != 1 {
			t.Fatalf("%s below the prose drew %+v; want one finding", key, got)
		}
		if !strings.Contains(got[0].Excerpt, key+": on line 6") {
			t.Fatalf("%s: excerpt %q does not name the key and its line", key, got[0].Excerpt)
		}
	}
}

// CardHeaderChecks is every token this file draws, in one byte-stable order.
func TestCardHeaderChecksListsTheTokens(t *testing.T) {
	t.Parallel()
	got := CardHeaderChecks()
	assert.Equal(t, []string{"kind-declared", "paths-declared", "paused", "test-named"}, got)
}

// ResolveInstructionKind is the instruction kind of a card's KIND: line, read through the
// one kinds list; a card with no KIND, or an unknown kind, is not ok.
func TestResolveInstructionKindReadsTheKindColumn(t *testing.T) {
	t.Parallel()
	work, inst, ok := ResolveInstructionKind([]byte("RESULT: c1 sha=0\nKIND: fix-red\n\nT.\n"))
	assert.True(t, ok)
	assert.Equal(t, "fix-red", work)
	assert.Equal(t, "think", inst)

	_, _, ok = ResolveInstructionKind([]byte("RESULT: c1 sha=0\n\nT.\n"))
	assert.False(t, ok)

	_, _, ok = ResolveInstructionKind([]byte("RESULT: c1 sha=0\nKIND: completely-unknown-kind\n\nT.\n"))
	assert.False(t, ok)
}

// ReadTrustFixture reads the coordinator's per-kind state from a file in the shape
// `nova-pulse trust` prints; non-TRUST rows and the TRUST OK summary are read past.
func TestReadTrustFixtureReadsTheTrustRows(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "trust.txt")
	body := "TRUST kind=fix area=core state=paused cards=1/2\n" +
		"TRUST kind=read area=core state=trial cards=3/4\n" +
		"TRUST OK kinds=2 trial=1 trusted=0 paused=1\n" +
		"some other line\n"
	require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	got, err := ReadTrustFixture(p)
	require.NoError(t, err)
	assert.Equal(t, TrustState{"fix": "paused", "read": "trial"}, got)

	_, err = ReadTrustFixture(filepath.Join(dir, "absent.txt"))
	require.Error(t, err)
}

// LintCardHeader refuses a repeated typed key, an empty value, a TEST: none on a gated
// kind, a paused kind, and a brief missing its START/STOP lines.
func TestLintCardHeaderRefusesMoreTypedHeaderDefects(t *testing.T) {
	t.Parallel()

	repeated := []byte("RESULT: c1 sha=0\nKIND: fix-red\nKIND: read\nPATHS: pkg/x.go\nTEST: ./pkg TestA\n\nT.\n")
	got := LintCardHeader(repeated, nil, false)
	assert.Contains(t, checksOfFindings(got), "kind-declared", "%+v", got)

	emptyKind := []byte("RESULT: c1 sha=0\nKIND: \nPATHS: pkg/x.go\nTEST: ./pkg TestA\n\nT.\n")
	got = LintCardHeader(emptyKind, nil, false)
	assert.Contains(t, checksOfFindings(got), "kind-declared", "%+v", got)

	emptyPaths := []byte("RESULT: c1 sha=0\nKIND: fix-red\nPATHS: \nTEST: ./pkg TestA\n\nT.\n")
	got = LintCardHeader(emptyPaths, nil, false)
	assert.Contains(t, checksOfFindings(got), "paths-declared", "%+v", got)

	emptyTest := []byte("RESULT: c1 sha=0\nKIND: fix-red\nPATHS: pkg/x.go\nTEST: \n\nT.\n")
	got = LintCardHeader(emptyTest, nil, false)
	assert.Contains(t, checksOfFindings(got), "test-named", "%+v", got)

	noneGated := []byte("RESULT: c1 sha=0\nKIND: fix-red\nPATHS: pkg/x.go\nTEST: none no test needed\n\nT.\n")
	got = LintCardHeader(noneGated, nil, false)
	assert.Contains(t, checksOfFindings(got), "test-named", "%+v", got)

	paused := []byte("RESULT: c1 sha=0\nKIND: fix-red\nPATHS: pkg/x.go\nTEST: ./pkg TestA\n\nT.\n")
	got = LintCardHeader(paused, TrustState{"fix-red": "paused"}, false)
	assert.Contains(t, checksOfFindings(got), "paused", "%+v", got)

	brief := []byte("RESULT: c1 sha=0 tier: pro\nKIND: fix-red\nPATHS: pkg/x.go\nTEST: ./pkg TestA\n\nT.\n")
	got = LintCardHeader(brief, nil, false)
	assert.Contains(t, checksOfFindings(got), "start-named", "%+v", got)
	assert.Contains(t, checksOfFindings(got), "stop-named", "%+v", got)

	sentinelMissing := []byte("RESULT: s1 sha=0\nKIND: sentinel\nPATHS: docs/roadmap.sexp\nTEST: none reason\n\nT.\n")
	got = LintCardHeader(sentinelMissing, nil, false)
	assert.Contains(t, checksOfFindings(got), "consumers-named", "%+v", got)

	sentinelOK := []byte("RESULT: s1 sha=0\nKIND: sentinel\nCONSUMERS: c1\nPATHS: docs/roadmap.sexp\nTEST: none reason\n\nT.\n")
	got = LintCardHeader(sentinelOK, nil, false)
	assert.NotContains(t, checksOfFindings(got), "consumers-named", "%+v", got)
}

// A declared KIND whose row of the one kinds list names no instruction kind is refused as
// kind-declared, through the lintCardHeader seam.
func TestLintCardHeaderRefusesAKindWithNoInstructionKind(t *testing.T) {
	t.Parallel()
	raw := []byte("RESULT: c1 sha=0\nKIND: fix-red\nPATHS: pkg/x.go\nTEST: ./pkg TestA\n\nT.\n")
	got := lintCardHeader(raw, nil, false, func(string) (string, bool) { return "", false })
	require.Len(t, findingsFor(got, "kind-declared"), 1)
	assert.Contains(t, findingsFor(got, "kind-declared")[0].Excerpt, "names no instruction kind")
}

// THE FOUR TOKENS OF SPEC-TOOLWORK §5 RULE 1 (issue #1651), RED FIRST.
//
// `cut` writes five typed lines under the contract line; pulse/cardheader.go at
// f927bccc (T03, PR #1721) parses them; this file is what `lint --card` checks before
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
		"PATHS: pkg/swarm/lintheader.go, pkg/swarm/lintheader_test.go",
		"TEST: pkg/swarm TestCardHeaderMissingKindDrawsKindDeclared",
		"LEGS: go",
		"SOURCE: mas-bandwidth/nova-tools#1651",
		"START: pkg/swarm/lintheader.go",
		"STOP: the gate prints its last line",
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
	t.Parallel()

	h := fullHeader()[1:] // everything but KIND:
	fs := LintCardHeader(typedCard(h...), nil, false)
	f := drew(t, fs, "kind-declared")
	assert.GreaterOrEqual(t, f.Line, 1, "a finding names a 1-based line, got %d", f.Line)
}

// An empty value is not a declaration: `KIND:` with nothing after it leaves the parser's
// Kind empty, which is exactly what Missing() names (cardheader.go:138-150).
func TestCardHeaderEmptyKindValueDrawsKindDeclared(t *testing.T) {
	t.Parallel()

	fs := LintCardHeader(typedCard(append([]string{"KIND:"}, fullHeader()[1:]...)...), nil, false)
	f := drew(t, fs, "kind-declared")
	assert.Equal(t, 2, f.Line, "the empty KIND: line is line 2, the finding says %d", f.Line)
}

// NEGATIVE CONTROL for kind-declared: the same card with a kind draws nothing at all.
func TestCardHeaderFullHeaderDrawsNothing(t *testing.T) {
	t.Parallel()

	fs := LintCardHeader(typedCard(fullHeader()...), nil, true)
	require.Empty(t, fs, "a complete header is clean, drew %v", fs)
}

// ---- test-named ------------------------------------------------------------------

func TestCardHeaderNoTestLineDrawsTestNamed(t *testing.T) {
	t.Parallel()

	h := []string{fullHeader()[0], fullHeader()[1], fullHeader()[3], fullHeader()[4]}
	fs := LintCardHeader(typedCard(h...), nil, false)
	drew(t, fs, "test-named")
	assert.False(t, checks(fs)["kind-declared"], "only the TEST: line is missing, drew %v", checks(fs))
	assert.False(t, checks(fs)["paths-declared"], "only the TEST: line is missing, drew %v", checks(fs))
}

// The parser refuses a TEST: value that is not `<package> <TestName>` and not `none`
// (cardheader.go:95-108). What it refuses, the lint refuses.
func TestCardHeaderMalformedTestDrawsTestNamed(t *testing.T) {
	t.Parallel()

	for _, bad := range []string{
		"TEST: TestOnlyAName",
		"TEST: pkg/swarm TestOne TestTwo",
		"TEST: pkg/swarm lintTheThing",
		"TEST: ../outside TestUp",
	} {
		h := append([]string{}, fullHeader()...)
		h[2] = bad
		fs := LintCardHeader(typedCard(h...), nil, false)
		assert.True(t, checks(fs)["test-named"], "%q is not a TEST: the gate can use; drew %v", bad, checks(fs))
	}
}

// NEGATIVE CONTROL for test-named: `TEST: none <why>` is what cardhdr.ParseTest reads for
// a kind that declares no gate, so it is a declaration, not a defect. The kind is an
// ungated one (read): on a gated kind TEST: none is refused (TestIssue1853). A bare
// `none` is refused on any kind (the reader must see why), and a tagged TEST is read
// as the gate runs it (nova-tools#4401 fix round, item 4).
func TestCardHeaderTestNoneDrawsNothing(t *testing.T) {
	t.Parallel()

	h := append([]string{}, fullHeader()...)
	h[0] = "KIND: read"
	h[2] = "TEST: none the read changes no code"
	fs := LintCardHeader(typedCard(h...), nil, true)
	require.Empty(t, fs, "`TEST: none <why>` is a declaration, drew %v", fs)
	h[2] = "TEST: none"
	fs = LintCardHeader(typedCard(h...), nil, true)
	require.True(t, checks(fs)["test-named"], "a bare `TEST: none` drew %v, want test-named: says no why", fs)
	require.Contains(t, fs[0].Excerpt, "says no why", "a bare `TEST: none` drew %v, want test-named: says no why", fs)
	h = append([]string{}, fullHeader()...)
	h[2] = "TEST: -tags functional pkg/swarm TestA"
	fs = LintCardHeader(typedCard(h...), nil, true)
	require.Empty(t, fs, "a tagged TEST drew %v", fs)
}

// ---- paths-declared --------------------------------------------------------------

func TestCardHeaderBadPathsDrawPathsDeclared(t *testing.T) {
	t.Parallel()

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
		"PATHS: pkg/swarm/*.go, **",          // one good glob does not excuse the other
	} {
		h := append([]string{}, fullHeader()...)
		h[1] = bad
		fs := LintCardHeader(typedCard(h...), nil, false)
		assert.True(t, checks(fs)["paths-declared"], "%q is not a PATHS: the gate can use; drew %v", bad, checks(fs))
	}
}

func TestCardHeaderNoPathsLineDrawsPathsDeclared(t *testing.T) {
	t.Parallel()

	h := []string{fullHeader()[0], fullHeader()[2], fullHeader()[3], fullHeader()[4]}
	fs := LintCardHeader(typedCard(h...), nil, false)
	drew(t, fs, "paths-declared")
}

// NEGATIVE CONTROL for paths-declared: globs with a literal segment and no climb, and
// `PATHS: none`, which is what a read kind carries (SPEC-TOOLWORK §5 rule 2).
func TestCardHeaderGoodPathsDrawNothing(t *testing.T) {
	t.Parallel()

	for _, good := range []string{
		"PATHS: pkg/swarm/lintheader.go",
		"PATHS: cmd/nova-swarm/**",
		"PATHS: pkg/swarm/*.go, cmd/nova-swarm/testdata/firstrun/**",
		"PATHS: none",
	} {
		h := append([]string{}, fullHeader()...)
		h[1] = good
		fs := LintCardHeader(typedCard(h...), nil, true)
		assert.Empty(t, fs, "%q is a PATHS: the gate can use, drew %v", good, fs)
	}
}

// ---- paused ----------------------------------------------------------------------

func writeTrust(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "trust.tsv")
	require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	return p
}

// The trust state does not exist yet: `nova-pulse trust` is T06a's other half and lives
// in GATE's lane. Until it does, the state is read from a fixture in the shape rule 1's
// listing prints, so the day the verb lands its own output is the fixture.
func TestCardHeaderPausedKindDrawsPausedAndTheTrialRemedy(t *testing.T) {
	t.Parallel()

	p := writeTrust(t, strings.Join([]string{
		"TRUST kind=fix-red area=- state=paused cards=3/10 pass=0.33 need=0.80 run_of_fails=3/3 since=2026-09-19T14:00:00Z by=rowan",
		"TRUST kind=sweep area=- state=trial cards=0/10 pass=- need=0.80 run_of_fails=0/3 since=- by=-",
		"TRUST OK kinds=2 trial=1 trusted=0 paused=1",
		"",
	}, "\n"))
	trust, err := ReadTrustFixture(p)
	require.NoError(t, err)
	fs := LintCardHeader(typedCard(fullHeader()...), trust, false)
	f := drew(t, fs, "paused")
	assert.Contains(t, f.Excerpt, "fix-red", "the finding names the paused kind: %q", f.Excerpt)
	assert.Contains(t, CardHeaderRemedies["paused"], "trust --set trial", "the paused remedy is the `trust --set trial` command: %q", CardHeaderRemedies["paused"])
}

// NEGATIVE CONTROL for paused: the same fixture, a kind that is on trial in it.
func TestCardHeaderTrialKindDrawsNoPaused(t *testing.T) {
	t.Parallel()

	p := writeTrust(t, "TRUST kind=fix-red area=- state=trial cards=0/10 pass=- need=0.80 run_of_fails=0/3 since=- by=-\n")
	trust, err := ReadTrustFixture(p)
	require.NoError(t, err)
	fs := LintCardHeader(typedCard(fullHeader()...), trust, true)
	require.Empty(t, fs, "a kind on trial is cut and launched, drew %v", fs)
}

// NEGATIVE CONTROL for paused: with no fixture there is no state, and the lint says
// nothing rather than guessing.
func TestCardHeaderNoTrustFixtureDrawsNoPaused(t *testing.T) {
	t.Parallel()

	fs := LintCardHeader(typedCard(fullHeader()...), nil, true)
	require.Empty(t, fs, "no trust state is not a paused state, drew %v", fs)
}

// ---- the header block is the parser's block --------------------------------------

// The parser stops at the first non-empty line that is not `KEY: value`
// (cardheader.go:70-76), so a KIND: below the prose is not a declaration. A lint that
// read it would pass a card the gate refuses.
func TestCardHeaderStopsWhereTheParserStops(t *testing.T) {
	t.Parallel()

	raw := []byte(strings.Join([]string{
		"RESULT: CARD-0000 do the thing",
		"PATHS: pkg/swarm/*.go",
		"TEST: pkg/swarm TestSomething",
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
	t.Parallel()

	h := append([]string{}, fullHeader()...)
	h = append(h[:2], append([]string{"MODE: explore"}, h[2:]...)...)
	fs := LintCardHeader(typedCard(h...), nil, true)
	require.Empty(t, fs, "an unknown KEY: line is read past, not stopped at; drew %v", fs)
}

// NEGATIVE CONTROL for the whole set: a card with no typed header at all is a card of
// the shape that predates §5, and it is checked by the twelve older rules only -- unless
// the header is required, and then it draws all three of the missing-line tokens.
func TestCardHeaderUntypedCardIsCheckedOnlyWhenRequired(t *testing.T) {
	t.Parallel()

	raw := typedCard()
	fs := LintCardHeader(raw, nil, false)
	require.Empty(t, fs, "an untyped card is not header-checked unless asked, drew %v", fs)
	got := checks(LintCardHeader(raw, nil, true))
	for _, want := range []string{"kind-declared", "paths-declared", "test-named"} {
		assert.True(t, got[want], "a required header draws %s on a card that has none; drew %v", want, got)
	}
}

// Every token this file draws carries a remedy, the way every older token does (#1464).
func TestCardHeaderEveryTokenHasARemedy(t *testing.T) {
	t.Parallel()

	for _, tok := range CardHeaderChecks() {
		assert.NotEmpty(t, strings.TrimSpace(CardHeaderRemedies[tok]), "%s carries no remedy line", tok)
	}
	assert.Len(t, CardHeaderChecks(), 4, "§5 rule 1 names four tokens, this holds %v", CardHeaderChecks())
}

// ---- start-named and stop-named ---------------------------------------------------

// tierCard renders a card whose contract line names the tier as the sprint writes it
// (`tier: flash` or `tier: pro`), with the header lines it is handed.
func tierCard(tier string, header ...string) []byte {
	lines := []string{"RESULT: CARD-0000 do the thing tier: " + tier}
	lines = append(lines, header...)
	lines = append(lines,
		"",
		"You are a Go engineer.",
		"STEP 1. cd repo",
		"STEP 2. Write RESULT.md.",
		"")
	return []byte(strings.Join(lines, "\n"))
}

// A tier flash brief whose header carries START: but no STOP: line is refused: the
// finding names the missing line (docs/SPEC-CARD-CONTRACT.md §2, the brief's header
// grammar; the check sits beside the TEST: rules in LintCardHeader). A tier pro brief
// without it draws the same finding, which cmd/nova-swarm/lint.go prints as a note, so
// its verdict stands. RED BEFORE: on base the flash brief passes the header lint with
// no stop-named finding, and the pro brief draws no finding at all.
func TestAFlashBriefWithoutStopIsRefusedAndAProBriefIsNoted(t *testing.T) {
	t.Parallel()

	// the brief carries every typed line and START:, but no STOP: line.
	h := []string{
		"KIND: fix-red",
		"PATHS: pkg/swarm/lintheader.go, pkg/swarm/lintheader_test.go",
		"TEST: pkg/swarm TestAFlashBriefWithoutStopIsRefusedAndAProBriefIsNoted",
		"START: pkg/swarm/lintheader.go",
	}

	// the flash brief is refused, and the finding names the missing line.
	fs := LintCardHeader(tierCard("flash", h...), nil, true)
	f := drew(t, fs, "stop-named")
	assert.Contains(t, f.Excerpt, "STOP:", "the finding names the missing line: %q", f.Excerpt)
	assert.GreaterOrEqual(t, f.Line, 1, "the finding names a 1-based line: %d", f.Line)
	assert.False(t, checks(fs)["start-named"], "START: is present, drew %v", checks(fs))

	// NEGATIVE CONTROL for the flash case: the same brief with the STOP: line draws
	// nothing at all.
	withStop := append(append([]string{}, h...), "STOP: the gate prints its last line")
	fs = LintCardHeader(tierCard("flash", withStop...), nil, true)
	require.Empty(t, fs, "a flash brief with both lines is clean, drew %v", fs)

	// the pro brief draws the same finding; cmd/nova-swarm/lint.go prints it as a note,
	// so the verdict stands.
	fs = LintCardHeader(tierCard("pro", h...), nil, true)
	pf := drew(t, fs, "stop-named")
	assert.Contains(t, pf.Excerpt, "STOP:", "the note names the missing line: %q", pf.Excerpt)
}
