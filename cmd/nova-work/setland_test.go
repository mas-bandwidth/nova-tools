package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/landed"
)

// fakeGH is the forge for these tests: one canned stdout per gh argument line, and
// a count of every line asked, so a test can see the cache hold. A line it does not
// hold is an error, the way an unreachable forge is -- nothing here dials a network.
type fakeGH struct {
	answers map[string]string
	calls   map[string]int
}

func (f *fakeGH) run(_ context.Context, args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	f.calls[key]++
	out, ok := f.answers[key]
	if !ok {
		return nil, errors.New("gh: Not Found (HTTP 404)")
	}
	return []byte(out), nil
}

func useFakeGH(t *testing.T, answers map[string]string) *fakeGH {
	t.Helper()
	f := &fakeGH{answers: answers, calls: map[string]int{}}
	old := ghRunner
	ghRunner = f.run
	t.Cleanup(func() { ghRunner = old })
	return f
}

// The forge of the landed fixture: #1 merged; #2 closed by the lander with its one
// file identical on dev; #3 closed with its file differing on dev; #4 still open;
// #5 closed by the lander, asked for by :merged :merged-at, which it fails; #6
// closed by the lander, its file edited on dev since; commit abcdef1 is behind dev,
// so reachable.
func landedForge() map[string]string {
	return map[string]string{
		"api repos/o/r/pulls/1": `{"state":"closed","merged_at":"2026-09-22T10:00:00Z","merge_commit_sha":"m1","head":{"sha":"h1"}}`,
		"api repos/o/r/pulls/2": `{"state":"closed","merged_at":null,"head":{"sha":"h2"}}`,
		"api repos/o/r/pulls/3": `{"state":"closed","merged_at":null,"head":{"sha":"h3"}}`,
		"api repos/o/r/pulls/4": `{"state":"open","merged_at":null,"head":{"sha":"h4"}}`,
		"api repos/o/r/pulls/5": `{"state":"closed","merged_at":null,"head":{"sha":"h5"}}`,

		"api --paginate repos/o/r/pulls/2/files?per_page=100": `[{"filename":"a.go","status":"modified","sha":"blobA"}]` +
			`[{"filename":"gone.go","status":"removed","sha":"old"}]`,
		"api --paginate repos/o/r/pulls/3/files?per_page=100": `[{"filename":"b.go","status":"modified","sha":"blobB-head"}]`,

		"api repos/o/r/git/trees/dev?recursive=1": `{"sha":"t","truncated":false,"tree":[` +
			`{"path":"a.go","type":"blob","sha":"blobA"},` +
			`{"path":"b.go","type":"blob","sha":"blobB-dev"},` +
			`{"path":"cmd","type":"tree","sha":"t2"}]}`,

		"api repos/o/r/compare/dev...abcdef1": `{"status":"behind"}`,

		// dev's history: #30 is not #3, and the commit that does name #3 does not
		// carry its bytes either, so #3 stays not landed; #6 moved on after its
		// landing commit c7, which carried its bytes.
		"api repos/o/r/commits?sha=dev&per_page=100": `[` +
			`{"sha":"c9","commit":{"message":"later: edit a.go and b.go (#30)"}},` +
			`{"sha":"c8","commit":{"message":"land-2: 1 approved PRs (#3)"}},` +
			`{"sha":"c7","commit":{"message":"land-1: 2 approved PRs (#6 #2)\n\nbody"}}]`,
		"api repos/o/r/git/trees/c8?recursive=1":              `{"truncated":false,"tree":[{"path":"b.go","type":"blob","sha":"blobB-other"}]}`,
		"api repos/o/r/git/trees/c7?recursive=1":              `{"truncated":false,"tree":[{"path":"b.go","type":"blob","sha":"blobB-landed"}]}`,
		"api repos/o/r/pulls/6":                               `{"state":"closed","merged_at":null,"head":{"sha":"h6"}}`,
		"api --paginate repos/o/r/pulls/6/files?per_page=100": `[{"filename":"b.go","status":"modified","sha":"blobB-landed"}]`,
	}
}

const landedSet = `; the fixes-day shape: :pr, :status and one :acceptance criterion per unit
(work-set "landed-fixture"
 :repo "o/r"
 :base "dev"
 :units
 (
  ;; merged through the forge
  (unit "merged" :pr 1 :status "open"
   :acceptance ((:id "c1" :kind :landed :subject "pr:o/r#1" :predicate :merged-or-closed-in-base)))
  ;; closed by the lander, content in dev
  (unit "closed-in" :pr 2 :status "open"
   :acceptance ((:id "c1" :kind :landed :subject "pr:o/r#2" :predicate :landed-in)))
  ;; closed, its file differs on dev
  (unit "closed-differs" :pr 3 :status "open"
   :acceptance ((:id "c1" :kind :landed :subject "pr:o/r#3" :predicate :merged-or-closed-in-base)))
  ;; a :status nobody should trust: the PR is open
  (unit "open-pr" :pr 4 :status "landed"
   :acceptance ((:id "c1" :kind :landed :subject "pr:o/r#4" :predicate :merged-or-closed-in-base)))
  ;; no machine criterion: the document's word stands
  (unit "status-only" :status "done" :acceptance ("a person reads it"))
  ;; a named commit reachable from dev
  (unit "commit" :status "open"
   :acceptance ((:id "c1" :kind :landed :subject "commit:abcdef1" :predicate :merged-or-closed-in-base)))
  ;; closed by the lander; dev edited its file since, but the landing commit carried it
  (unit "landed-then-moved" :pr 6 :status "open"
   :acceptance ((:id "c1" :kind :landed :subject "pr:o/r#6" :predicate :merged-or-closed-in-base)))
  ;; closed by the lander but asked for as merged: :merged-at fails it (#2664's negative)
  (unit "merged-at-closed" :pr 5 :status "open" :needs ("merged")
   :acceptance ((:id "c1" :kind :merged :subject "pr:o/r#5" :predicate :merged-at)))))
`

func TestSetCheckEvaluateDerivesDoneFromCriteria(t *testing.T) {
	forge := useFakeGH(t, landedForge())
	path := write(t, "landed.sexp", landedSet)
	code, stdout, stderr := runCLI(t, "set", "check", "--file", path, "--evaluate")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	for _, want := range []string{
		"SET EVAL unit=merged criterion=c1 kind=landed subject=pr:o/r#1 holds=yes why=merged",
		"SET EVAL unit=closed-in criterion=c1 kind=landed subject=pr:o/r#2 holds=yes why=content-in-base",
		"SET EVAL unit=closed-differs criterion=c1 kind=landed subject=pr:o/r#3 holds=no why=differs:b.go",
		"SET EVAL unit=open-pr criterion=c1 kind=landed subject=pr:o/r#4 holds=no why=open",
		"SET EVAL unit=commit criterion=c1 kind=landed subject=commit:abcdef1 holds=yes why=reachable",
		"SET EVAL unit=merged-at-closed criterion=c1 kind=merged subject=pr:o/r#5 holds=no why=not-merged",
		"SET EVAL unit=landed-then-moved criterion=c1 kind=landed subject=pr:o/r#6 holds=yes why=content-in:c7",
		// done: merged, closed-in, status-only, commit, landed-then-moved -- 5 of 8
		"SET OK units=8 ready=3 blocked=0 owned=0\nSET DONE done=5 percent=62\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout does not carry %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "unit=status-only criterion") {
		t.Errorf("a sentence criterion was evaluated:\n%s", stdout)
	}
	// One run asks each question once: the tree two closed PRs are compared
	// against is fetched once, and no PR is fetched twice.
	for key, n := range forge.calls {
		if n != 1 {
			t.Errorf("gh %s was asked %d times in one run, want 1", key, n)
		}
	}
	// Without --evaluate the document's words count, and gh is never asked.
	forge.calls = map[string]int{}
	code, stdout, _ = runCLI(t, "set", "check", "--file", path)
	if code != 0 || !strings.HasSuffix(stdout, "SET DONE done=2 percent=25\n") {
		t.Errorf("without --evaluate: exit %d\n%s", code, stdout)
	}
	if len(forge.calls) != 0 {
		t.Errorf("gh was asked without --evaluate: %v", forge.calls)
	}
}

// A closed PR is done only on the lander's rule: its file differing on the base is
// not done even when the file's :status says landed.
func TestSetCheckEvaluateClosedPRWhoseFileDiffersIsNotDone(t *testing.T) {
	useFakeGH(t, landedForge())
	path := write(t, "one.sexp", `(work-set "one" :repo "o/r" :base "dev" :units (
	  (unit "u" :status "landed"
	   :acceptance ((:id "c1" :kind :landed :subject "pr:o/r#3" :predicate :merged-or-closed-in-base)))))`)
	code, stdout, _ := runCLI(t, "set", "check", "--file", path, "--evaluate")
	if code != 0 || !strings.Contains(stdout, "holds=no why=differs:b.go") ||
		!strings.HasSuffix(stdout, "SET DONE done=0 percent=0\n") {
		t.Errorf("exit %d\n%s", code, stdout)
	}
}

// A forge that cannot answer leaves the criterion unknown and the unit not done:
// no evidence is not negative evidence, and it is not positive evidence either.
func TestSetCheckEvaluateUnreachableForgeIsUnknown(t *testing.T) {
	useFakeGH(t, map[string]string{})
	path := write(t, "one.sexp", `(work-set "one" :base "dev" :units (
	  (unit "u" :status "landed"
	   :acceptance ((:id "c1" :kind :landed :subject "pr:o/r#9" :predicate :merged-or-closed-in-base)))))`)
	code, stdout, _ := runCLI(t, "set", "check", "--file", path, "--evaluate")
	if code != 0 || !strings.Contains(stdout, "holds=unknown why=error:gh:-Not-Found-(HTTP-404)") ||
		!strings.HasSuffix(stdout, "SET DONE done=0 percent=0\n") {
		t.Errorf("exit %d\n%s", code, stdout)
	}
}

// --base names the branch; without it the set's :base does, and a set with
// neither is refused rather than evaluated against a guessed branch.
func TestSetCheckEvaluateBase(t *testing.T) {
	forge := useFakeGH(t, map[string]string{
		"api repos/o/r/compare/main...abcdef1": `{"status":"diverged"}`,
	})
	body := `(work-set "b" :repo "o/r" :units (
	  (unit "u" :acceptance ((:id "c1" :kind :landed :subject "commit:abcdef1" :predicate :landed-in)))))`
	path := write(t, "b.sexp", body)
	code, _, stderr := runCLI(t, "set", "check", "--file", path, "--evaluate")
	if code != 2 || !strings.Contains(stderr, "--base") {
		t.Errorf("no base anywhere: exit %d, stderr %q; want 2 naming --base", code, stderr)
	}
	code, stdout, _ := runCLI(t, "set", "check", "--file", path, "--evaluate", "--base", "main")
	if code != 0 || !strings.Contains(stdout, "holds=no why=not-in-base") || forge.calls["api repos/o/r/compare/main...abcdef1"] != 1 {
		t.Errorf("--base main: exit %d\n%s", code, stdout)
	}
	code, _, stderr = runCLI(t, "set", "check", "--file", path, "--evaluate", "--base", "dev?x=1")
	if code != 2 {
		t.Errorf("a base that is not a branch name: exit %d, stderr %q", code, stderr)
	}
}

func TestSetDonePercentRoundsDown(t *testing.T) {
	for _, c := range []struct{ done, units, want int }{
		{26, 42, 61}, {0, 0, 0}, {0, 5, 0}, {1, 3, 33}, {2, 3, 66}, {3, 3, 100}, {4, 7, 57},
	} {
		if got := percent(c.done, c.units); got != c.want {
			t.Errorf("percent(%d, %d) = %d, want %d", c.done, c.units, got, c.want)
		}
	}
}

// --write-status rewrites :status "open" to "landed" for exactly the units whose
// criteria all hold, and not one other byte: comments, spacing and order survive,
// and a second run finds nothing to write.
func TestSetCheckWriteStatusIsByteStable(t *testing.T) {
	useFakeGH(t, landedForge())
	path := write(t, "landed.sexp", landedSet)
	code, stdout, stderr := runCLI(t, "set", "check", "--file", path, "--write-status")
	if code != 0 {
		t.Fatalf("exit = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := landedSet
	for _, unit := range []string{"merged", "closed-in", "commit", "landed-then-moved"} {
		before := `(unit "` + unit + `"`
		i := strings.Index(want, before)
		j := i + strings.Index(want[i:], `:status "open"`)
		want = want[:j] + `:status "landed"` + want[j+len(`:status "open"`):]
	}
	if string(got) != want {
		t.Errorf("the file moved past the four status values:\ngot:\n%s\nwant:\n%s", got, want)
	}
	for _, line := range []string{"SET WROTE unit=merged status=landed", "SET WROTE unit=closed-in status=landed",
		"SET WROTE unit=commit status=landed", "SET WROTE unit=landed-then-moved status=landed"} {
		if !strings.Contains(stdout, line) {
			t.Errorf("stdout does not carry %q:\n%s", line, stdout)
		}
	}
	// closed-differs and merged-at-closed stay open; open-pr keeps its (wrong)
	// landed, because --write-status only ever writes a holding criterion's word.
	if strings.Count(stdout, "SET WROTE") != 4 {
		t.Errorf("wrote %d units, want 4:\n%s", strings.Count(stdout, "SET WROTE"), stdout)
	}
	// Changed lines: one per flipped unit.
	a, b := strings.Split(landedSet, "\n"), strings.Split(string(got), "\n")
	if len(a) != len(b) {
		t.Fatalf("line count moved: %d -> %d", len(a), len(b))
	}
	changed := 0
	for i := range a {
		if a[i] != b[i] {
			changed++
		}
	}
	if changed != 4 {
		t.Errorf("%d lines changed, want 4", changed)
	}
	// Idempotent: the second run writes nothing and the bytes stay put.
	code, stdout, _ = runCLI(t, "set", "check", "--file", path, "--write-status")
	again, _ := os.ReadFile(path)
	if code != 0 || strings.Contains(stdout, "SET WROTE") || string(again) != string(got) {
		t.Errorf("second run: exit %d, rewrote:\n%s", code, stdout)
	}
}

// The evaluator is the same one the CLI drives; this pins the seam's type so a
// test fake and landed.GH stay interchangeable.
var _ landed.Runner = (&fakeGH{}).run
