package pulse

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// THE CONTROLS FOR THE COMMIT STEP (nova-tools #2549), against REAL git repositories under
// t.TempDir(). The bash this replaces had no test, and the way it broke each time was that
// nobody could run it anywhere but the fleet.
//
// The POSITIVE control is TestApplyCommitPlanFixtureJob: a fixture job directory goes in,
// and a branch whose two-dot diff against the base is EXACTLY the card's PATHS comes out.
// The NEGATIVE controls are beside it, one per fence.

// commitStepFixture is one job directory: a mirror repository, a clone of it as the card's
// repo/, a RESULT.md and a card.
type commitStepFixture struct {
	t      *testing.T
	root   string // the swarm root: <root>/<slot>/jobs/<label>
	mirror string // the local "mirror" the rebase target is fetched from
	jobDir string
	repo   string
	cards  string
	base   string // the sha the card was cut at
	label  string
}

func cgit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", dir,
		"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
		"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=dev"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_DATE=2026-09-22T12:00:00Z", "GIT_COMMITTER_DATE=2026-09-22T12:00:00Z")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v: %s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func cwrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newCommitFixture lays down the mirror, the job directory and the card. `paths` is what
// the card declares; the working tree carries a change to each of them.
func newCommitFixture(t *testing.T, label, line2 string, paths []string) *commitStepFixture {
	t.Helper()
	tmp := t.TempDir()
	f := &commitStepFixture{t: t, label: label,
		root:   filepath.Join(tmp, "swarm"),
		mirror: filepath.Join(tmp, "mirror"),
		cards:  filepath.Join(tmp, "cards"),
	}
	// The mirror: one commit on dev, carrying every declared path.
	if err := os.MkdirAll(f.mirror, 0o755); err != nil {
		t.Fatal(err)
	}
	cgit(t, f.mirror, "init", "-q")
	cgit(t, f.mirror, "checkout", "-q", "-B", "dev")
	cwrite(t, filepath.Join(f.mirror, "README.md"), "the fixture repository\n")
	for _, p := range paths {
		cwrite(t, filepath.Join(f.mirror, p), "package x // base\n")
	}
	cgit(t, f.mirror, "add", "-A")
	cgit(t, f.mirror, "commit", "-q", "-m", "base")
	f.base = cgit(t, f.mirror, "rev-parse", "HEAD")

	f.jobDir = filepath.Join(f.root, "slot-"+label, "jobs", label)
	f.repo = filepath.Join(f.jobDir, "repo")
	if err := os.MkdirAll(f.jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cgit(t, tmp, "clone", "-q", f.mirror, f.repo)
	cgit(t, f.repo, "checkout", "-q", "dev")

	short := strings.TrimSpace(cgit(t, f.repo, "rev-parse", "--short=12", "HEAD"))
	cwrite(t, filepath.Join(f.jobDir, "RESULT.md"), fmt.Sprintf(
		"RESULT %s sha=%s -- the card's own line 1\n%s\nSCHEMA: v2\nREPO mas-bandwidth/nova-tools\nPATHS %s\n",
		label, short, line2, strings.Join(paths, " ")))
	cwrite(t, filepath.Join(f.cards, label+".md"), fmt.Sprintf(
		"RESULT %s sha=%s -- the card\nKIND: fix\nBASE: dev\nPATHS: %s\n",
		label, short, strings.Join(paths, " ")))
	return f
}

// change writes a card's own work into the working tree.
func (f *commitStepFixture) change(rel, body string) {
	f.t.Helper()
	cwrite(f.t, filepath.Join(f.repo, rel), body)
}

func (f *commitStepFixture) scan() CommitScan {
	return CommitScan{Cards: []string{f.cards}, Base: "dev"}
}

func (f *commitStepFixture) env() CommitEnv {
	return CommitEnv{Mirrors: map[string]string{"": f.mirror}}
}

// apply is the whole step over this fixture: read, decide, act.
func (f *commitStepFixture) apply() (CommitPlan, CommitResult) {
	f.t.Helper()
	job, err := ReadCommitJob(f.jobDir, f.scan())
	if err != nil {
		f.t.Fatalf("ReadCommitJob: %v", err)
	}
	plan := CommitDecision(job)
	res, err := ApplyCommitPlan(f.jobDir, plan, f.env())
	if err != nil {
		f.t.Fatalf("ApplyCommitPlan: %v (lines: %s)", err, strings.Join(res.Lines, " | "))
	}
	return plan, res
}

// diffAgainstBase is `git diff --name-only <base>..HEAD` in the card's repo.
func (f *commitStepFixture) diffAgainstBase(base string) []string {
	f.t.Helper()
	out := cgit(f.t, f.repo, "diff", "--name-only", "--no-renames", base+"..HEAD")
	var names []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			names = append(names, l)
		}
	}
	return names
}

func hasVerdict(lines []string, token string) bool {
	for _, l := range lines {
		if strings.HasPrefix(l, token+" ") || l == token {
			return true
		}
	}
	return false
}

func verdictLine(lines []string, token string) string {
	for _, l := range lines {
		if strings.HasPrefix(l, token+" ") {
			return l
		}
	}
	return ""
}

// TestApplyCommitPlanFixtureJob IS THE POSITIVE CONTROL. A fixture job goes in; a branch
// carrying exactly the card's declared PATHS comes out, and the stray notes.txt the card
// left in the working tree is not in it.
func TestApplyCommitPlanFixtureJob(t *testing.T) {
	paths := []string{"internal/pulse/work.go"}
	f := newCommitFixture(t, "fix-1", "DONE (both runs green)", paths)
	f.change("internal/pulse/work.go", "package x // the card's work\n")
	// Two strays the branch must not carry, for two different reasons: `notes.txt` is
	// the card's OWN scratch and is set aside, and `stray/extra.go` is an ordinary file
	// the card never declared, which the A4 staging rule simply does not stage.
	f.change("notes.txt", "scratch the card left behind\n")
	f.change("stray/extra.go", "package stray // undeclared\n")

	plan, res := f.apply()
	if plan.Action != CommitCommit {
		t.Fatalf("action = %s, want COMMIT (%s)", plan.Action, plan.Detail)
	}
	if !res.Committed {
		t.Fatalf("nothing was committed; lines: %s", strings.Join(res.Lines, " | "))
	}
	if got := cgit(t, f.repo, "rev-parse", "--abbrev-ref", "HEAD"); got != "rowan/fix-1" {
		t.Errorf("branch = %q, want rowan/fix-1", got)
	}
	if got := cgit(t, f.repo, "log", "-1", "--pretty=%s"); !strings.HasPrefix(got, "RESULT fix-1 sha=") {
		t.Errorf("commit subject = %q, want RESULT.md line 1", got)
	}
	if got := strings.Join(f.diffAgainstBase(f.base), ","); got != strings.Join(paths, ",") {
		t.Errorf("two-dot diff = %q, want EXACTLY the card's PATHS %q", got, strings.Join(paths, ","))
	}
	if !hasVerdict(res.Lines, "COMMITTED") {
		t.Errorf("no COMMITTED verdict line; got: %s", strings.Join(res.Lines, " | "))
	}
	// The BRANCH line was absent from the RESULT.md and must have been WRITTEN, which
	// is the fact `sed "s#^BRANCH[: ].*#...#"` could never establish (#2549).
	if !hasVerdict(res.Lines, "BRANCH-LINE") {
		t.Errorf("no BRANCH-LINE verdict; got: %s", strings.Join(res.Lines, " | "))
	}
	raw, err := os.ReadFile(filepath.Join(f.jobDir, "RESULT.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "\nBRANCH rowan/fix-1\n") {
		t.Errorf("the RESULT.md has no BRANCH line after the step:\n%s", raw)
	}
	if !strings.Contains(string(raw), "\nBASE dev\n") {
		t.Errorf("the RESULT.md has no BASE line after the step:\n%s", raw)
	}
	// Nothing was DELETED. The card's own notes.txt is set aside inside the job
	// directory and the undeclared file is still in the working tree, untracked.
	if _, err := os.Stat(filepath.Join(f.jobDir, "scratch-from-repo", "notes.txt")); err != nil {
		t.Errorf("the card's notes.txt was not set aside in the job directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.repo, "stray/extra.go")); err != nil {
		t.Errorf("the undeclared file was removed from the working tree: %v", err)
	}
}

// TestApplyCommitPlanRebasesOntoAMovedTarget: the target moved under a finished card, which
// is the whole of the stale-base refusal pile. The branch is replayed onto the live tip and
// its two-dot diff is STILL exactly the card's PATHS.
func TestApplyCommitPlanRebasesOntoAMovedTarget(t *testing.T) {
	paths := []string{"internal/pulse/work.go"}
	f := newCommitFixture(t, "fix-2", "DONE", paths)
	f.change("internal/pulse/work.go", "package x // the card's work\n")

	// dev moves one commit ahead in the mirror, in a file the card never touched.
	cwrite(t, filepath.Join(f.mirror, "docs/LATER.md"), "a later landing\n")
	cgit(t, f.mirror, "add", "-A")
	cgit(t, f.mirror, "commit", "-q", "-m", "a later landing on dev")
	moved := cgit(t, f.mirror, "rev-parse", "HEAD")

	_, res := f.apply()
	if !res.Rebased {
		t.Fatalf("the branch was not rebased onto the moved target; lines: %s", strings.Join(res.Lines, " | "))
	}
	if !hasVerdict(res.Lines, "REBASED") {
		t.Errorf("no REBASED verdict line; got: %s", strings.Join(res.Lines, " | "))
	}
	if got := strings.Join(f.diffAgainstBase(moved), ","); got != strings.Join(paths, ",") {
		t.Errorf("two-dot diff against the MOVED target = %q, want exactly %q", got, strings.Join(paths, ","))
	}
}

// TestApplyCommitPlanRebaseConflictIsPrintedAndAborted: a replay that conflicts is author
// work. It aborts, prints REBASE-CONFLICT and leaves the branch where it was.
func TestApplyCommitPlanRebaseConflictIsPrintedAndAborted(t *testing.T) {
	paths := []string{"internal/pulse/work.go"}
	f := newCommitFixture(t, "fix-3", "DONE", paths)
	f.change("internal/pulse/work.go", "package x // the card's work\n")

	cwrite(t, filepath.Join(f.mirror, "internal/pulse/work.go"), "package x // somebody else got there first\n")
	cgit(t, f.mirror, "add", "-A")
	cgit(t, f.mirror, "commit", "-q", "-m", "a conflicting landing on dev")

	_, res := f.apply()
	if !hasVerdict(res.Lines, "REBASE-CONFLICT") {
		t.Fatalf("no REBASE-CONFLICT verdict; got: %s", strings.Join(res.Lines, " | "))
	}
	if res.Rebased {
		t.Error("Rebased is true although the replay conflicted")
	}
	if out := cgit(t, f.repo, "status", "--porcelain"); strings.Contains(out, "UU ") {
		t.Errorf("the rebase was not aborted; the tree is left in conflict:\n%s", out)
	}
	if got := cgit(t, f.repo, "rev-parse", "--abbrev-ref", "HEAD"); got != "rowan/fix-3" {
		t.Errorf("branch = %q after an aborted rebase, want rowan/fix-3", got)
	}
}

// TestApplyCommitPlanSkipsDONEish IS A NEGATIVE CONTROL: the prefix rule, on a real job.
// `DONEish` produces a SKIP line, no branch and no commit.
func TestApplyCommitPlanSkipsDONEish(t *testing.T) {
	f := newCommitFixture(t, "fix-4", "DONEish", []string{"internal/pulse/work.go"})
	f.change("internal/pulse/work.go", "package x // the card's work\n")

	plan, res := f.apply()
	if plan.Action != CommitSkip || plan.Why != CommitWhyNotDone {
		t.Fatalf("action/why = %s/%s, want SKIP/not-done", plan.Action, plan.Why)
	}
	if !hasVerdict(res.Lines, "SKIP") {
		t.Errorf("no SKIP verdict line; got: %s", strings.Join(res.Lines, " | "))
	}
	if out := cgit(t, f.repo, "branch", "--list", "rowan/fix-4"); strings.TrimSpace(out) != "" {
		t.Errorf("a branch was cut for a DONEish card: %q", out)
	}
	if got := cgit(t, f.repo, "rev-parse", "HEAD"); got != f.base {
		t.Errorf("HEAD moved for a DONEish card: %q", got)
	}
}

// TestApplyCommitPlanRefusesAFileOverAMegabyte IS A NEGATIVE CONTROL: 1.1 MB, refused and
// named, nothing committed.
func TestApplyCommitPlanRefusesAFileOverAMegabyte(t *testing.T) {
	f := newCommitFixture(t, "fix-5", "DONE", []string{"internal/pulse/work.go"})
	f.change("internal/pulse/work.go", "package x // the card's work\n")
	f.change("testdata/dump.bin", string(bytes.Repeat([]byte("x"), 1_100_000)))

	plan, res := f.apply()
	if plan.Action != CommitRefuse || plan.Why != CommitWhyTooBig {
		t.Fatalf("action/why = %s/%s, want REFUSED/too-big", plan.Action, plan.Why)
	}
	line := verdictLine(res.Lines, "REFUSED")
	if !strings.Contains(line, "testdata/dump.bin") {
		t.Errorf("the refusal does not name the file: %q", line)
	}
	if got := cgit(t, f.repo, "rev-parse", "HEAD"); got != f.base {
		t.Error("something was committed although the job was refused")
	}
}

// TestApplyCommitPlanRefusesAKeyFile IS A NEGATIVE CONTROL for the exfiltration fence
// (#2609), both halves: the PATH shape, decided without opening the file, and the CONTENT
// shape, caught on the staged bytes with an ordinary path. Neither prints what it matched.
func TestApplyCommitPlanRefusesAKeyFile(t *testing.T) {
	t.Run("by path shape", func(t *testing.T) {
		f := newCommitFixture(t, "fix-6", "DONE", []string{"internal/pulse/work.go"})
		f.change("internal/pulse/work.go", "package x // the card's work\n")
		f.change("infra/deploy.key", "AGE-SECRET-KEY-1QQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQ\n")

		plan, res := f.apply()
		if plan.Action != CommitRefuse || plan.Why != CommitWhySecretPath {
			t.Fatalf("action/why = %s/%s, want REFUSED/secret-path", plan.Action, plan.Why)
		}
		line := verdictLine(res.Lines, "REFUSED")
		if !strings.Contains(line, "infra/deploy.key") || !strings.Contains(line, "key-file") {
			t.Errorf("the refusal must name the path and the shape: %q", line)
		}
		if strings.Contains(line, "AGE-SECRET-KEY") {
			t.Errorf("the refusal printed the matched text: %q", line)
		}
		if got := cgit(t, f.repo, "rev-parse", "HEAD"); got != f.base {
			t.Error("something was committed although a credential-shaped path was changed")
		}
	})

	t.Run("by content shape under an ordinary path", func(t *testing.T) {
		paths := []string{"internal/pulse/work.go"}
		f := newCommitFixture(t, "fix-7", "DONE", paths)
		f.change("internal/pulse/work.go",
			"package x\n\nconst leaked = \"AGE-SECRET-KEY-1QQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQ\"\n")

		plan, res := f.apply()
		if plan.Action != CommitCommit {
			t.Fatalf("action = %s, want the DECISION to allow it: the path is ordinary", plan.Action)
		}
		if res.Committed {
			t.Fatalf("the commit went through although a staged file carries a key shape; lines: %s",
				strings.Join(res.Lines, " | "))
		}
		line := verdictLine(res.Lines, "REFUSED")
		if !strings.Contains(line, "secret-shape") || !strings.Contains(line, "age-secret-key") {
			t.Errorf("the refusal must name the shape: %q", line)
		}
		if strings.Contains(line, "AGE-SECRET-KEY-1Q") {
			t.Errorf("the refusal printed the matched text: %q", line)
		}
		if out := cgit(t, f.repo, "diff", "--cached", "--name-only"); strings.TrimSpace(out) != "" {
			t.Errorf("the index was left staged after a refusal: %q", out)
		}
	})
}

// TestApplyCommitPlanDropsCardScratch IS A NEGATIVE CONTROL for the follow-up commit: the
// card itself committed a notes.txt and a build artefact, and the branch must not carry
// them. The two-dot diff against the base ends as exactly the card's PATHS.
func TestApplyCommitPlanDropsCardScratch(t *testing.T) {
	paths := []string{"internal/pulse/work.go"}
	f := newCommitFixture(t, "fix-8", "DONE", paths)
	// The card's OWN earlier commit, carrying scratch beside the work.
	f.change("internal/pulse/work.go", "package x // the card's work\n")
	f.change("notes.txt", "the card's own notes\n")
	f.change("build/out.o", "an object file\n")
	cgit(t, f.repo, "checkout", "-q", "-B", "rowan/fix-8")
	cgit(t, f.repo, "add", "-A")
	cgit(t, f.repo, "commit", "-q", "-m", "the card's own commit, scratch and all")

	plan, res := f.apply()
	if plan.Action != CommitBranchOnly {
		t.Fatalf("action = %s, want BRANCH-ONLY: the work is already committed", plan.Action)
	}
	if !hasVerdict(res.Lines, "SCRATCH-DROPPED") {
		t.Fatalf("no SCRATCH-DROPPED verdict; got: %s", strings.Join(res.Lines, " | "))
	}
	line := verdictLine(res.Lines, "SCRATCH-DROPPED")
	if !strings.Contains(line, "notes.txt") || !strings.Contains(line, "build/out.o") {
		t.Errorf("the drop line does not name what it dropped: %q", line)
	}
	if got := strings.Join(f.diffAgainstBase(f.base), ","); got != strings.Join(paths, ",") {
		t.Errorf("two-dot diff = %q, want exactly the card's PATHS %q after the drop", got, strings.Join(paths, ","))
	}
}

// TestApplyCommitPlanSetsCardScratchAside: the card's own files in the working tree are
// MOVED into the job directory, never committed and never deleted. Refusing the whole job
// over one of these left 88 DONE cells unharvested on 2026-09-21.
func TestApplyCommitPlanSetsCardScratchAside(t *testing.T) {
	paths := []string{"internal/pulse/work.go"}
	f := newCommitFixture(t, "fix-9", "DONE", paths)
	f.change("internal/pulse/work.go", "package x // the card's work\n")
	f.change("RESULT.md", "a RESULT.md the worker wrote in the wrong directory\n")
	f.change("usage.tsv", "tokens\t100\n")

	_, res := f.apply()
	if !hasVerdict(res.Lines, "SCRATCH-MOVED") {
		t.Fatalf("no SCRATCH-MOVED verdict; got: %s", strings.Join(res.Lines, " | "))
	}
	for _, n := range []string{"RESULT.md", "usage.tsv"} {
		if _, err := os.Stat(filepath.Join(f.repo, n)); err == nil {
			t.Errorf("%s is still in the repository", n)
		}
		aside := filepath.Join(f.jobDir, "scratch-from-repo", n)
		if _, err := os.Stat(aside); err != nil {
			t.Errorf("%s was not set aside in the job directory: %v", n, err)
		}
	}
	// The job's OWN RESULT.md, one level up, is untouched: the one that moved is the
	// stray copy inside repo/.
	raw, err := os.ReadFile(filepath.Join(f.jobDir, "RESULT.md"))
	if err != nil || !strings.HasPrefix(string(raw), "RESULT fix-9 ") {
		t.Errorf("the job's own RESULT.md was disturbed: %v %q", err, string(raw))
	}
	if got := strings.Join(f.diffAgainstBase(f.base), ","); got != strings.Join(paths, ",") {
		t.Errorf("two-dot diff = %q, want exactly %q", got, strings.Join(paths, ","))
	}
}

// TestApplyCommitPlanDraftOnlyFromAFile: the nine prefixes that lived inside a
// single-quoted shell string inside an ssh are a FILE, and a card matching one is skipped.
func TestApplyCommitPlanDraftOnlyFromAFile(t *testing.T) {
	f := newCommitFixture(t, "fix-nova-tools-2454-r1", "DONE", []string{"internal/pulse/work.go"})
	f.change("internal/pulse/work.go", "package x // the card's work\n")
	list := filepath.Join(t.TempDir(), "DRAFT-ONLY")
	cwrite(t, list, "# Glenn 2026-09-21: friends build the tooling\nfix-nova-tools-2454-*\nfix-nova-tools-2416-*\n")

	draft, err := ReadDraftOnly(list)
	if err != nil {
		t.Fatal(err)
	}
	cfg := f.scan()
	cfg.DraftOnly = draft
	job, err := ReadCommitJob(f.jobDir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	plan := CommitDecision(job)
	if plan.Action != CommitSkip || plan.Why != CommitWhyDraftOnly {
		t.Fatalf("action/why = %s/%s, want SKIP/draft-only", plan.Action, plan.Why)
	}
}

// TestApplyCommitPlanDryRunChangesNothing: a rehearsal prints and touches nothing.
func TestApplyCommitPlanDryRunChangesNothing(t *testing.T) {
	f := newCommitFixture(t, "fix-10", "DONE", []string{"internal/pulse/work.go"})
	f.change("internal/pulse/work.go", "package x // the card's work\n")

	job, err := ReadCommitJob(f.jobDir, f.scan())
	if err != nil {
		t.Fatal(err)
	}
	env := f.env()
	env.DryRun = true
	res, err := ApplyCommitPlan(f.jobDir, CommitDecision(job), env)
	if err != nil {
		t.Fatal(err)
	}
	if !hasVerdict(res.Lines, "DRY-RUN") {
		t.Errorf("no DRY-RUN line; got: %s", strings.Join(res.Lines, " | "))
	}
	if got := cgit(t, f.repo, "rev-parse", "HEAD"); got != f.base {
		t.Error("a dry run moved HEAD")
	}
	if got := cgit(t, f.repo, "rev-parse", "--abbrev-ref", "HEAD"); got != "dev" {
		t.Errorf("a dry run changed the branch to %q", got)
	}
}

// TestReadChangedFilesHandlesAPathWithASpace: `git status --porcelain | awk '{print $2}'`
// loses the tail of a path with a space in it, which is one of the ways the bash silently
// staged or refused the wrong thing. `-z` and no awk.
func TestReadChangedFilesHandlesAPathWithASpace(t *testing.T) {
	f := newCommitFixture(t, "fix-11", "DONE", []string{"internal/pulse/work.go"})
	f.change("docs/a file with spaces.md", "hello\n")

	files, err := readChangedFiles(f.repo)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range files {
		if c.Path == "docs/a file with spaces.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("the path with spaces was not read whole: %+v", files)
	}
}

// TestEnsureResultLineWritesAnAbsentLine is the unit under the BRANCH-LINE fix (#2549):
// `sed "s#^BRANCH[: ].*#...#"` substitutes NOTHING when the line is absent and exits 0, so
// the verdict was printed 94 times, once per pass, and never once took.
func TestEnsureResultLineWritesAnAbsentLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "RESULT.md")
	cwrite(t, path, "RESULT report-nova-tools-2537-r1 -- a report card\nDONE\nREPO mas-bandwidth/nova-tools\n")

	changed, err := ensureResultLine(path, "BRANCH", "rowan/report-nova-tools-2537-r1")
	if err != nil || !changed {
		t.Fatalf("ensureResultLine on an absent line = %v,%v; want it written", changed, err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "\nBRANCH rowan/report-nova-tools-2537-r1\n") {
		t.Fatalf("the line was not written:\n%s", raw)
	}
	// A second call is a no-op and reports so: the verdict is printed from the WRITE,
	// never from an exit status.
	changed, err = ensureResultLine(path, "BRANCH", "rowan/report-nova-tools-2537-r1")
	if err != nil || changed {
		t.Fatalf("a second call = %v,%v; want no change", changed, err)
	}
	// A line that is there and names something else is rewritten IN PLACE.
	changed, err = ensureResultLine(path, "BRANCH", "rowan/other")
	if err != nil || !changed {
		t.Fatalf("rewriting a present line = %v,%v; want it changed", changed, err)
	}
	raw, _ = os.ReadFile(path)
	if strings.Count(string(raw), "BRANCH ") != 1 {
		t.Fatalf("the rewrite left two BRANCH lines:\n%s", raw)
	}
}

// TestCommitJobDirsTakesEveryJobUnderASlot: the bash took `ls -d $d/jobs/* | head -1`, so a
// slot holding two jobs lost one of them with no line saying so.
func TestCommitJobDirsTakesEveryJobUnderASlot(t *testing.T) {
	root := t.TempDir()
	for _, j := range []string{"card-00-a", "card-00-b"} {
		if err := os.MkdirAll(filepath.Join(root, "slot-card-x", "jobs", j), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A directory with no `jobs/` under it is not a slot and is passed over.
	if err := os.MkdirAll(filepath.Join(root, "not-a-slot", "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	dirs, err := CommitJobDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 2 {
		t.Fatalf("job dirs = %v, want BOTH jobs under the slot (the bash took only the first)", dirs)
	}
}
