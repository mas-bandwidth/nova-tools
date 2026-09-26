package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com",
		"-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// landedOrigin is the forge's repository on disk, in the #2544 shape: pull/2 and
// pull/6 each edit one end of a.go, dev lands them COMBINED in one commit and then
// edits a.go's middle, so no dev commit carries either head's a.go byte for byte
// and merging either head into dev still changes nothing. pull/3 edits b.go, which
// dev never took. landedGitURL points the merge here and --cache keeps the bare
// repositories in the test's own temp dir.
func landedOrigin(t *testing.T) map[int]string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	put := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(msg string) string {
		gitIn(t, dir, "add", "-A")
		gitIn(t, dir, "commit", "-q", "-m", msg)
		return gitIn(t, dir, "rev-parse", "HEAD")
	}
	aGo := func(edit map[int]string) string {
		var b strings.Builder
		for i := 1; i <= 9; i++ {
			line, ok := edit[i]
			if !ok {
				line = strconv.Itoa(i)
			}
			b.WriteString(line + "\n")
		}
		return b.String()
	}
	put("a.go", aGo(nil))
	put("b.go", "x\n")
	m := commit("M")
	heads := map[int]string{}
	for n, edit := range map[int][2]string{
		2: {"a.go", aGo(map[int]string{1: "1pr"})},
		6: {"a.go", aGo(map[int]string{9: "9pr"})},
		3: {"b.go", "y\n"},
	} {
		gitIn(t, dir, "checkout", "-q", "--detach", m)
		put(edit[0], edit[1])
		heads[n] = commit("pull")
		gitIn(t, dir, "update-ref", "refs/pull/"+strconv.Itoa(n)+"/head", heads[n])
	}
	gitIn(t, dir, "checkout", "-q", "-b", "dev", m)
	put("a.go", aGo(map[int]string{1: "1pr", 9: "9pr"}))
	commit("land-1: 2 approved PRs (#2 #6)")
	put("a.go", aGo(map[int]string{1: "1pr", 5: "5later", 9: "9pr"}))
	commit("later")
	gitIn(t, dir, "checkout", "-q", "--detach")
	old := landedGitURL
	landedGitURL = func(string) string { return "file://" + dir }
	t.Cleanup(func() { landedGitURL = old })
	return heads
}

// The forge of the landed fixture: #1 merged; #2 and #6 closed by the lander,
// combined in one dev commit; #3 closed and never landed; #4 still open; #5 closed
// by the lander, asked for by :merged :merged-at, which it fails; commit abcdef1 is
// behind dev, so reachable.
func landedForge(t *testing.T) map[string]string {
	heads := landedOrigin(t)
	closed := func(n int) string {
		return `{"state":"closed","merged_at":null,"head":{"sha":"` + heads[n] + `"}}`
	}
	closedGQL := func(n int) string {
		return `{"state":"CLOSED","mergedAt":null,"closedAt":null,"headRefOid":"` + heads[n] + `","mergeCommit":null}`
	}
	return map[string]string{
		"api repos/o/r/pulls/1": `{"state":"closed","merged_at":"2026-09-22T10:00:00Z","merge_commit_sha":"m1","head":{"sha":"h1"}}`,
		"api repos/o/r/pulls/2": closed(2),
		"api repos/o/r/pulls/3": closed(3),
		"api repos/o/r/pulls/4": `{"state":"open","merged_at":null,"head":{"sha":"h4"}}`,
		"api repos/o/r/pulls/5": `{"state":"closed","merged_at":null,"head":{"sha":"h5"}}`,
		"api repos/o/r/pulls/6": closed(6),

		"api repos/o/r/compare/dev...abcdef1": `{"status":"behind"}`,

		// the same six PRs as set check reads them: one batch call (#3460)
		strings.Join(landed.BatchArgs("o/r", []int{1, 2, 3, 4, 5, 6}), " "): `{"data":{"repository":{` +
			`"p1":{"state":"MERGED","mergedAt":"2026-09-22T10:00:00Z","headRefOid":"h1","mergeCommit":{"oid":"m1"}},` +
			`"p2":` + closedGQL(2) + `,"p3":` + closedGQL(3) + `,` +
			`"p4":{"state":"OPEN","mergedAt":null,"headRefOid":"h4"},` +
			`"p5":{"state":"CLOSED","mergedAt":null,"headRefOid":"h5"},` +
			`"p6":` + closedGQL(6) + `}}}`,
	}
}

// HOLD 7 on #2688: a pinned subject asks about that head. #7's newer head merged;
// the unit pinned to its older head is not done, the one pinned to the merged
// head is.
func TestSetCheckEvaluatePinnedHead(t *testing.T) {
	useFakeGH(t, map[string]string{
		"api repos/o/r/pulls/7": `{"state":"closed","merged_at":"2026-09-22T10:00:00Z","head":{"sha":"2222222222222222222222222222222222222222"}}`,
	})
	path := write(t, "pin.sexp", `(work-set "pin" :repo "o/r" :base "dev" :units (
	  (unit "older-head" :status "open"
	   :acceptance ((:id "c1" :kind :landed :subject "pr:o/r#7@1111111111111111111111111111111111111111" :predicate :merged-or-closed-in-base)))
	  (unit "merged-head" :status "open"
	   :acceptance ((:id "c1" :kind :landed :subject "pr:o/r#7@2222222" :predicate :merged-or-closed-in-base)))))`)
	code, stdout, _ := runCLI(t, "set", "check", "--file", path, "--evaluate", "--cache", t.TempDir())
	for _, want := range []string{
		"SET EVAL unit=older-head criterion=c1 kind=landed subject=pr:o/r#7@1111111111111111111111111111111111111111 holds=no why=pinned-head-superseded",
		"SET EVAL unit=merged-head criterion=c1 kind=landed subject=pr:o/r#7@2222222 holds=yes why=merged",
		"SET DONE done=1 percent=50",
	} {
		if code != 0 || !strings.Contains(stdout, want) {
			t.Errorf("exit %d; stdout does not carry %q:\n%s", code, want, stdout)
		}
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
  ;; closed, never landed: merging it would change dev
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
  ;; the #2544 control: combined with #2 by the lander, dev edited since
  (unit "combined-2544" :pr 6 :status "open"
   :acceptance ((:id "c1" :kind :landed :subject "pr:o/r#6" :predicate :merged-or-closed-in-base)))
  ;; closed by the lander but asked for as merged: :merged-at fails it (#2664's negative)
  (unit "merged-at-closed" :pr 5 :status "open" :needs ("merged")
   :acceptance ((:id "c1" :kind :merged :subject "pr:o/r#5" :predicate :merged-at)))))
`

func TestSetCheckEvaluateDerivesDoneFromCriteria(t *testing.T) {
	// SLEEPS: this test waits on the wall clock (measured over 5 s on the 2026-09-25 PR run). Skipped 2026-09-25
	// by Glenn's rule ("unit tests must not have real sleeps or waits"): it becomes a
	// mocked-clock unit test or a functional program (nova-tools #4221).
	t.Skip("SLEEPS: needs a mocked clock or a functional test (nova-tools #4221)")
	forge := useFakeGH(t, landedForge(t))
	path := write(t, "landed.sexp", landedSet)
	code, stdout, stderr := runCLI(t, "set", "check", "--file", path, "--evaluate", "--cache", t.TempDir())
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	for _, want := range []string{
		"SET EVAL unit=merged criterion=c1 kind=landed subject=pr:o/r#1 holds=yes why=merged",
		"SET EVAL unit=closed-in criterion=c1 kind=landed subject=pr:o/r#2 holds=yes why=merge-changes-nothing",
		"SET EVAL unit=closed-differs criterion=c1 kind=landed subject=pr:o/r#3 holds=no why=merge-changes-base",
		"SET EVAL unit=open-pr criterion=c1 kind=landed subject=pr:o/r#4 holds=no why=open",
		"SET EVAL unit=commit criterion=c1 kind=landed subject=commit:abcdef1 holds=yes why=reachable",
		"SET EVAL unit=merged-at-closed criterion=c1 kind=merged subject=pr:o/r#5 holds=no why=not-merged",
		"SET EVAL unit=combined-2544 criterion=c1 kind=landed subject=pr:o/r#6 holds=yes why=merge-changes-nothing",
		// done: merged, closed-in, status-only, commit, combined-2544 -- 5 of 8
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
	// #3460: the six PRs are read in one batch call, and none again by REST; the
	// one other call is the commit's compare.
	for key := range forge.calls {
		if strings.HasPrefix(key, "api repos/o/r/pulls/") {
			t.Errorf("gh %s: a PR was read by REST after the batch", key)
		}
	}
	if len(forge.calls) != 2 {
		t.Errorf("one run made %d distinct gh calls, want 2 (one batch, one compare): %v", len(forge.calls), forge.calls)
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

// A closed PR is done only on the lander's rule: one whose merge would change the
// base is not done even when the file's :status says landed.
func TestSetCheckEvaluateClosedPRWhoseFileDiffersIsNotDone(t *testing.T) {
	useFakeGH(t, landedForge(t))
	path := write(t, "one.sexp", `(work-set "one" :repo "o/r" :base "dev" :units (
	  (unit "u" :status "landed"
	   :acceptance ((:id "c1" :kind :landed :subject "pr:o/r#3" :predicate :merged-or-closed-in-base)))))`)
	code, stdout, _ := runCLI(t, "set", "check", "--file", path, "--evaluate", "--cache", t.TempDir())
	if code != 0 || !strings.Contains(stdout, "holds=no why=merge-changes-base") ||
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
	code, stdout, _ := runCLI(t, "set", "check", "--file", path, "--evaluate", "--cache", t.TempDir())
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
	code, _, stderr := runCLI(t, "set", "check", "--file", path, "--evaluate", "--cache", t.TempDir())
	if code != 2 || !strings.Contains(stderr, "--base") {
		t.Errorf("no base anywhere: exit %d, stderr %q; want 2 naming --base", code, stderr)
	}
	code, stdout, _ := runCLI(t, "set", "check", "--file", path, "--evaluate", "--base", "main", "--cache", t.TempDir())
	if code != 0 || !strings.Contains(stdout, "holds=no why=not-in-base") || forge.calls["api repos/o/r/compare/main...abcdef1"] != 1 {
		t.Errorf("--base main: exit %d\n%s", code, stdout)
	}
	code, _, stderr = runCLI(t, "set", "check", "--file", path, "--evaluate", "--base", "dev?x=1")
	if code != 2 {
		t.Errorf("a base that is not a branch name: exit %d, stderr %q", code, stderr)
	}
}

func TestSetDonePercentRoundsDown(t *testing.T) {
	t.Parallel()

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
	useFakeGH(t, landedForge(t))
	path := write(t, "landed.sexp", landedSet)
	cache := t.TempDir()
	code, stdout, stderr := runCLI(t, "set", "check", "--file", path, "--write-status", "--cache", cache)
	if code != 0 {
		t.Fatalf("exit = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := landedSet
	for _, unit := range []string{"merged", "closed-in", "commit", "combined-2544"} {
		before := `(unit "` + unit + `"`
		i := strings.Index(want, before)
		j := i + strings.Index(want[i:], `:status "open"`)
		want = want[:j] + `:status "landed"` + want[j+len(`:status "open"`):]
	}
	if string(got) != want {
		t.Errorf("the file moved past the four status values:\ngot:\n%s\nwant:\n%s", got, want)
	}
	for _, line := range []string{"SET WROTE unit=merged status=landed", "SET WROTE unit=closed-in status=landed",
		"SET WROTE unit=commit status=landed", "SET WROTE unit=combined-2544 status=landed"} {
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
	code, stdout, _ = runCLI(t, "set", "check", "--file", path, "--write-status", "--cache", cache)
	again, _ := os.ReadFile(path)
	if code != 0 || strings.Contains(stdout, "SET WROTE") || string(again) != string(got) {
		t.Errorf("second run: exit %d, rewrote:\n%s", code, stdout)
	}
}

// The evaluator is the same one the CLI drives; this pins the seam's type so a
// test fake and landed.GH stay interchangeable.
var _ landed.Runner = (&fakeGH{}).run
