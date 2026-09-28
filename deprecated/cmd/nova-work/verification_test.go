package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The fixture is testdata/verification/roadmap.sexp: E01-F01 verified 2/3 by the
// feature's reader-a/b/c, its third row naming its own test newly-green; E02-F01
// verified 1/2 by journal-a plus a Go test, its second row naming a test that is
// not green. The transcripts below are the suite's own line shapes.

const verificationFixture = "testdata/verification/roadmap.sexp"

const greenTranscript = `This is SBCL 2.6.8, an implementation of ANSI Common Lisp.
TEST reader-a PASS spec=docs/SPEC-WORK.md:1 x
TEST reader-b PASS spec=docs/SPEC-WORK.md:2 x
TEST reader-c PASS spec=docs/SPEC-WORK.md:3 x
TEST journal-a PASS spec=docs/SPEC-WORK.md:4 x
TEST newly-green PASS spec=docs/SPEC-WORK.md:5 x
TEST nobody-names-me PASS spec=docs/SPEC-WORK.md:6 x
NOVA-WORK SLICE1 total=6 pass=6 fail=0
`

const headSHA = "b5df1c3b0000000000000000000000000000abcd"

func verificationDeps(transcript string, dirty bool) Deps {
	return Deps{
		Now: func() time.Time { return time.Date(2026, 9, 25, 14, 0, 0, 0, time.UTC) },
		Suite: func(context.Context, string) ([]byte, error) {
			return []byte(transcript), nil
		},
		Head: func(string) (string, bool, error) { return headSHA, dirty, nil },
	}
}

// copyFixture puts the fixture where --write may rewrite it.
func copyFixture(t *testing.T) (string, []byte) {
	t.Helper()
	orig, err := os.ReadFile(verificationFixture)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "roadmap.sexp")
	if err := os.WriteFile(p, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	return p, orig
}

func runVerification(t *testing.T, deps Deps, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(append([]string{"verification"}, args...), &out, &errb, deps)
	return code, out.String(), errb.String()
}

// TestVerificationWriteRewritesTheThreeFieldsAndListsTheFlip is #3459's DONE-WHEN:
// at a green suite the verb rewrites :measured-at, :revision and :suite-result to
// what it measured at HEAD, leaves every other byte alone, and lists the criterion
// whose own tests all pass but whose :state is not verified.
func TestVerificationWriteRewritesTheThreeFieldsAndListsTheFlip(t *testing.T) {
	t.Parallel()

	p, orig := copyFixture(t)
	code, out, errs := runVerification(t, verificationDeps(greenTranscript, false),
		"--sexp", p, "--repo", ".", "--write")
	if code != 0 {
		t.Fatalf("exit %d, stdout %q stderr %q", code, out, errs)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer(
		`:measured-at "2026-09-19"`, `:measured-at "2026-09-25"`,
		`:revision "4c793b55a30160e5fe1ed45e25928f2c85dcfe5c"`, `:revision "`+headSHA+`"`,
		`:suite-result "NOVA-WORK SLICE1 total=336 pass=336 fail=0"`, `:suite-result "NOVA-WORK SLICE1 total=6 pass=6 fail=0"`,
	).Replace(string(orig))
	if string(got) != want {
		t.Fatalf("the rewrite touched more (or less) than the three strings:\n%s", got)
	}
	if !strings.Contains(out, "VERIFICATION PROPOSE E01-F01-03 state=unverified tests=newly-green\n") {
		t.Errorf("the criterion whose own test passes is not proposed:\n%s", out)
	}
	if strings.Contains(out, "E02-F01-02") {
		t.Errorf("a criterion whose own test is absent was proposed:\n%s", out)
	}
	last := strings.TrimSpace(out[strings.LastIndex(strings.TrimSpace(out), "\n")+1:])
	for _, w := range []string{"VERIFICATION OK mode=write", "measured-at=2026-09-25", "revision=" + headSHA,
		"total=6 pass=6 fail=0", "criteria=5 verified=3 stale=0 propose=1 unclaimed=1"} {
		if !strings.Contains(last, w) {
			t.Errorf("receipt %q does not carry %q", last, w)
		}
	}
}

// TestVerificationCheckWritesNothing: --check is the merge gate; it judges and
// never writes.
func TestVerificationCheckWritesNothing(t *testing.T) {
	t.Parallel()

	p, orig := copyFixture(t)
	code, out, errs := runVerification(t, verificationDeps(greenTranscript, true),
		"--sexp", p, "--repo", ".", "--check")
	if code != 0 {
		t.Fatalf("exit %d, stdout %q stderr %q", code, out, errs)
	}
	if !strings.Contains(out, "VERIFICATION OK mode=check") {
		t.Errorf("no check receipt:\n%s", out)
	}
	if got, _ := os.ReadFile(p); !bytes.Equal(got, orig) {
		t.Fatal("--check wrote the sexp")
	}
}

// TestVerificationRefusesAStaleVerifiedCriterion: a verified criterion whose named
// test is red or gone is listed STALE, and nothing is written -- a new :revision
// would claim a test passed there that did not.
func TestVerificationRefusesAStaleVerifiedCriterion(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		transcript string
		line       string
	}{
		"gone": {
			strings.Replace(greenTranscript, "TEST journal-a PASS spec=docs/SPEC-WORK.md:4 x\n", "", 1),
			"VERIFICATION STALE E02-F01-01 red=- gone=journal-a\n",
		},
		"red": {
			strings.Replace(strings.Replace(greenTranscript, "TEST reader-b PASS", "TEST reader-b FAIL", 1),
				"pass=6 fail=0", "pass=5 fail=1", 1),
			"VERIFICATION STALE E01-F01-01 red=reader-b gone=-\n",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			for _, mode := range []string{"--write", "--check"} {
				p, orig := copyFixture(t)
				code, out, errs := runVerification(t, verificationDeps(tc.transcript, false),
					"--sexp", p, "--repo", ".", mode)
				if code != 1 {
					t.Fatalf("%s: exit %d, want 1; stdout %q stderr %q", mode, code, out, errs)
				}
				if !strings.Contains(out, tc.line) {
					t.Errorf("%s: stdout lacks %q:\n%s", mode, tc.line, out)
				}
				if !strings.HasPrefix(errs, "VERIFICATION REFUSED ") || !strings.Contains(errs, "remedy:") {
					t.Errorf("%s: refusal is not one remedy line: %q", mode, errs)
				}
				if got, _ := os.ReadFile(p); !bytes.Equal(got, orig) {
					t.Fatalf("%s: a refused run wrote the sexp", mode)
				}
			}
		})
	}
}

// TestVerificationWriteRefusesADirtyTreeAndAMissingSummary: --write records only a
// measurement of HEAD, and only a suite that finished.
func TestVerificationWriteRefusesADirtyTreeAndAMissingSummary(t *testing.T) {
	t.Parallel()

	for name, deps := range map[string]Deps{
		"dirty":      verificationDeps(greenTranscript, true),
		"no-summary": verificationDeps(strings.Replace(greenTranscript, "NOVA-WORK SLICE1", "(crashed)", 1), false),
	} {
		t.Run(name, func(t *testing.T) {
			p, orig := copyFixture(t)
			code, out, errs := runVerification(t, deps, "--sexp", p, "--repo", ".", "--write")
			if code != 1 || !strings.HasPrefix(errs, "VERIFICATION REFUSED ") {
				t.Fatalf("exit %d, stdout %q stderr %q", code, out, errs)
			}
			if got, _ := os.ReadFile(p); !bytes.Equal(got, orig) {
				t.Fatal("a refused run wrote the sexp")
			}
		})
	}
}

// TestVerificationUsage: the flags are required and --check/--write exclusive.
func TestVerificationUsage(t *testing.T) {
	t.Parallel()

	deps := verificationDeps(greenTranscript, false)
	for _, args := range [][]string{
		{"--repo", ".", "--check"},
		{"--sexp", verificationFixture, "--check"},
		{"--sexp", verificationFixture, "--repo", "."},
		{"--sexp", verificationFixture, "--repo", ".", "--check", "--write"},
	} {
		if code, _, _ := runVerification(t, deps, args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}

// TestVerificationReadsTheRealRoadmap: the reader walks docs/roadmaps/nova-work.sexp
// and every verified criterion there names at least one test.
func TestVerificationReadsTheRealRoadmap(t *testing.T) {
	t.Parallel()

	doc, err := readVerificationDoc(filepath.Join("..", "..", defaultRoadmapSexp))
	if err != nil {
		t.Fatal(err)
	}
	all := suiteResult{verdict: map[string]bool{}, summary: "x", fail: "0"}
	for _, c := range doc.criteria {
		lisp, _ := namedTests(c.featureTest)
		for _, n := range lisp {
			all.verdict[n] = true
		}
	}
	stale, _, verified, _ := judge(doc, all)
	if len(doc.criteria) == 0 || verified == 0 {
		t.Fatalf("read %d criteria, %d verified", len(doc.criteria), verified)
	}
	for _, s := range stale {
		t.Errorf("verified criterion %s names no test this suite runs (gone=%v)", s.id, s.gone)
	}
}
