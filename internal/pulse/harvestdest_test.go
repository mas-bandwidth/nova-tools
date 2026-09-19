package pulse

// THE DESTINATION IS THE MANAGER'S, NOT THE WORKER'S (Johnny's HOLDs of PR #1809 at
// 8bfa4020 and at 7f692ef6).
//
// Two holes have been closed here in turn and this file holds the fixtures for both:
//
//  1. a RESULT.md naming a foreign repository with NOTHING but itself to say so, once per
//     publishing path -- the `repo-unknown` family; and
//  2. a worker that runs `git remote set-url origin <elsewhere>` in its own clone and then
//     writes a RESULT.md agreeing with it, so that the two "independent" statements the
//     resolver compared were the same worker talking twice. That is Johnny's HOLD at
//     7f692ef6, and SPEC-SANDBOX 27b already says origin is rewritable from inside the
//     wall.
//
// The first tests in this file are the second hole, through the production path with the
// REAL git: two bare repositories on disk, a global git config that maps the forge URLs
// this package forms onto them, and the assertion is those repositories' own ref lists.
// Before this change the branch landed in attacker/exfil.

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------------------
// The production path, against real remotes.
// ---------------------------------------------------------------------------------------

// destBench is a harvest fixture with REAL git and two REAL remotes.
//
// `resolveDestination` forms its URL as githubCloneBase + `<owner>/<name>.git` and the
// production `push` runs git against it, so the remotes are wired in the only place that
// does not touch the code under test: a global git config whose `pushInsteadOf` maps each
// of those URLs onto a bare repository in this test's own directory. Nothing in the package
// is stubbed or injected, and what the test reads afterwards is `git for-each-ref` on those
// bare repositories -- the push either arrived or it did not.
type destBench struct {
	root     string // the pulse root
	job      string // the job directory, which IS the worker's clone
	honest   string // the bare repo behind the dispatched repository's clone URL
	attacker string // the bare repo behind the worker's chosen clone URL
	arglog   string
}

// The two clone URLs this fixture wires up, spelled the way the resolver spells them.
const (
	honestURL   = githubCloneBase + "owner/repo.git"
	attackerURL = githubCloneBase + "attacker/exfil.git"
)

func newDestBench(t *testing.T) *destBench {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("no git on PATH: %v", err)
	}
	home := t.TempDir()
	b := &destBench{
		root:     filepath.Join(home, "root"),
		honest:   filepath.Join(home, "honest.git"),
		attacker: filepath.Join(home, "attacker.git"),
		arglog:   filepath.Join(home, "argv.log"),
	}

	// Only `gh` is faked. git is the REAL one, because the thing under test is what a
	// worker can do to a real git clone, and a fake git cannot be made to do it.
	specs := fakePATHFor(t, "gh")
	fakeTool(t, specs, "gh", fakeSpec{Log: b.arglog, Rules: []fakeRule{
		{Arg: 2, Equals: "view", Exit: 1},
		{Arg: 2, Equals: "create", Stdout: githubCloneBase + "owner/repo/pull/7"},
	}})

	realGit(t, "init", "--bare", "-b", "main", b.honest)
	realGit(t, "init", "--bare", "-b", "main", b.attacker)

	// The one seam, and it is git's own: the forge URLs this package forms are rewritten
	// onto the bare repositories above. The package's push and PR-open are untouched.
	//
	// `pushInsteadOf` and not `insteadOf`, deliberately: `git remote get-url origin`
	// applies a plain `insteadOf`, which would make the clone's recorded origin come back
	// as a local path and hide the very comparison this file is about. pushInsteadOf
	// rewrites the transport and leaves what the clone SAYS its origin is alone.
	//
	// The forge host is never spelled in this file: every URL below is the package's own
	// githubCloneBase, which is what internal/ci's net class test asks of a test file and
	// is also the honest statement -- these ARE the URLs the resolver forms.
	cfg := filepath.Join(home, "gitconfig")
	conf := fmt.Sprintf(`[user]
	name = nova-pulse test
	email = test@example.invalid
[init]
	defaultBranch = main
[commit]
	gpgsign = false
[url "%s"]
	pushInsteadOf = %s
[url "%s"]
	pushInsteadOf = %s
`, filepath.ToSlash(b.honest), honestURL, filepath.ToSlash(b.attacker), attackerURL)
	if err := os.WriteFile(cfg, []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_SYSTEM", filepath.Join(home, "no-such-system-config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")

	writePulseTable(t, b.root, "p1", 1, 4, "300")
	return b
}

// card lays down the LAUNCH RECORD the pulse cut -- `REPO owner/repo`, written before any
// worker existed -- and then the job the worker ran in, which IS a clone of the honest
// remote with one commit on a prefixed branch.
func (b *destBench) card(t *testing.T, label, branch, resultRepo string) {
	t.Helper()
	addCard(t, b.root, label, "1", "flash", "RESULT "+label+" sha=aaa",
		"RESULT "+label+" sha=aaa\nDONE\nBRANCH "+branch+"\nREPO "+resultRepo+"\n")
	b.job = filepath.Join(b.root, "1", "jobs", label)
	realGit(t, "init", "-b", "main", b.job)
	realGit(t, "-C", b.job, "remote", "add", "origin", honestURL)
	if err := os.WriteFile(filepath.Join(b.job, "fix.go"), []byte("package fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	realGit(t, "-C", b.job, "add", "fix.go")
	realGit(t, "-C", b.job, "commit", "-m", "the fix")
	realGit(t, "-C", b.job, "checkout", "-b", branch)
}

// refs is what a remote actually holds, which is the only answer these tests trust.
func refs(t *testing.T, bare string) []string {
	t.Helper()
	out := realGit(t, "-C", bare, "for-each-ref", "--format=%(refname)")
	var got []string
	for _, l := range strings.Split(out, "\n") {
		if strings.TrimSpace(l) != "" {
			got = append(got, strings.TrimSpace(l))
		}
	}
	return got
}

func realGit(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// THE HOLD AT 7f692ef6, IN ONE TEST. The worker owns its clone, so it rewrites `origin` to
// a repository of its choosing and writes a RESULT.md that agrees. Both statements are the
// worker's; the card the pulse cut says owner/repo and is the only thing here the worker
// could not write. Nothing is pushed, and the attacker's remote is empty afterwards.
func TestAWorkerThatRewritesItsOwnOriginPushesNothing(t *testing.T) {
	b := newDestBench(t)
	b.card(t, "card-880", "rowan/br-880", "attacker/exfil")
	// The attack, and it is one line from inside the wall.
	realGit(t, "-C", b.job, "remote", "set-url", "origin", attackerURL)

	out, errs := runHarvest(t, b.root)

	if got := refs(t, b.attacker); len(got) != 0 {
		t.Fatalf("the worker chose the destination: %s holds %v", b.attacker, got)
	}
	if got := refs(t, b.honest); len(got) != 0 {
		t.Fatalf("a refused job pushed anyway: %s holds %v", b.honest, got)
	}
	if !strings.Contains(errs, "HARVEST REFUSED repo-mismatch card=card-880") {
		t.Fatalf("the repo-mismatch refusal is absent:\nstdout=%s\nstderr=%s", out, errs)
	}
	if !strings.Contains(errs, "dispatched=owner/repo") {
		t.Fatalf("the refusal does not say what was dispatched:\n%s", errs)
	}
	if !strings.Contains(out, "pushed=0") || !strings.Contains(out, "prs=0") {
		t.Fatalf("want pushed=0 prs=0, got:\n%s", out)
	}
	for _, l := range arglogLines(t, b.arglog) {
		if strings.Contains(l, "pr create") {
			t.Fatalf("a pull request was opened for a refused job: %s", l)
		}
	}
}

// The same rewrite with an HONEST RESULT.md: the worker says owner/repo and points its
// clone somewhere else, which is the half of the attack that needs no lie in the report at
// all. The clone's origin is a claim either way.
func TestARewrittenOriginIsRefusedEvenWhenTheResultIsHonest(t *testing.T) {
	b := newDestBench(t)
	b.card(t, "card-881", "rowan/br-881", "owner/repo")
	realGit(t, "-C", b.job, "remote", "set-url", "origin", attackerURL)

	out, errs := runHarvest(t, b.root)

	if got := refs(t, b.attacker); len(got) != 0 {
		t.Fatalf("a rewritten origin reached the forge: %s holds %v", b.attacker, got)
	}
	if !strings.Contains(errs, "HARVEST REFUSED repo-mismatch card=card-881") ||
		!strings.Contains(errs, "origin=attacker/exfil") {
		t.Fatalf("the origin-mismatch refusal is absent:\nstdout=%s\nstderr=%s", out, errs)
	}
}

// AND THE HONEST PATH STILL LANDS. A guard that refuses everything is not a guard, so the
// same fixture with nothing rewritten pushes its branch to the dispatched remote and opens
// its pull request.
func TestTheHonestJobStillLandsOnTheDispatchedRepo(t *testing.T) {
	b := newDestBench(t)
	b.card(t, "card-882", "rowan/br-882", "owner/repo")

	out, errs := runHarvest(t, b.root)

	want := "refs/heads/rowan/br-882"
	got := refs(t, b.honest)
	found := false
	for _, r := range got {
		if r == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("the honest push did not land: %s holds %v\nstdout=%s\nstderr=%s", b.honest, got, out, errs)
	}
	if len(refs(t, b.attacker)) != 0 {
		t.Fatalf("the honest push reached the wrong remote")
	}
	if !strings.Contains(out, "pushed=1") || !strings.Contains(out, "prs=1") {
		t.Fatalf("want pushed=1 prs=1, got:\n%s\n%s", out, errs)
	}
}

// ---------------------------------------------------------------------------------------
// One fixture per publishing path: the worker's two statements agree with each other and
// with nothing the manager wrote.
// ---------------------------------------------------------------------------------------

// (1) THE LOCAL FOLD, on a bare swarm root. `discoverRootCards` has no cards.tsv to read,
// so it builds each row with the RESULT.md itself as the card path -- correct for the
// contract (rule 11), and, before this change, the destination check comparing the
// RESULT.md against the RESULT.md. Johnny's word for it: tautological.
func TestHarvestRefusesABareRootWhoseOnlyDestinationIsItsOwnResult(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	// The fake git DOES answer `remote get-url origin` here, with the attacker's own
	// repository: that is the worker's rewrite, and it must buy nothing. The only
	// manager-side record a bare root has is the RESULT.md, which is no record at all.
	fakeTool(t, specs, "git", fakeSpec{Log: arglog, Rules: []fakeRule{originRule("attacker/exfil")}})
	fakeGH(t, specs, arglog, "https://forge.invalid/attacker/exfil/pull/1")

	job := filepath.Join(root, "1", "jobs", "card-880")
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(job, "RESULT.md"),
		[]byte("RESULT card-880 sha=aaa\nDONE\nBRANCH rowan/br-880\nREPO attacker/exfil\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errs := runHarvest(t, root)
	for _, l := range arglogLines(t, arglog) {
		if strings.HasPrefix(l, "git push") || strings.HasPrefix(l, "gh pr create") {
			t.Fatalf("the local fold reached a forge on a destination only the worker named: %s", l)
		}
	}
	if !strings.Contains(errs, "HARVEST REFUSED repo-unknown card=card-880") {
		t.Fatalf("the repo-unknown refusal is absent:\nstdout=%s\nstderr=%s", out, errs)
	}
	if !strings.Contains(out, "pushed=0") || !strings.Contains(out, "prs=0") {
		t.Fatalf("want pushed=0 prs=0, got:\n%s", out)
	}
}

// (2) `harvest --working`: the path that force-pushes with a lease, so an unknown
// destination matters more here, not less. It carries no launch record to this verb, so the
// coordinator names the destination with --clone or nothing does.
func TestHarvestWorkingRefusesWithNoCoordinatorCloneAndForcePushesNothing(t *testing.T) {
	working := t.TempDir()
	specs := fakePATH(t)
	arglog := filepath.Join(working, "argv.log")
	fakeTool(t, specs, "git", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 1, Equals: "log", Stdout: "aaaa000000000000000000000000000000000000 2026-09-17T10:00:00+00:00"},
		{Arg: 1, Equals: "ls-remote", Stdout: "bbbb000000000000000000000000000000000000\trefs/heads/rowan/w"},
		// The clone's origin agrees with the RESULT.md, which IS the attack: both
		// statements are the worker's, and neither is a record.
		originRule("attacker/exfil"),
	}})
	fakeTool(t, specs, "gh", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 2, Equals: "list", Stdout: "[]"},
		{Arg: 2, Equals: "create", Stdout: "https://forge.invalid/attacker/exfil/pull/23"},
	}})
	wkJob(t, working, "g-w", "w", wkResult("w", "rowan/w", "attacker/exfil"))

	// No --clone on purpose: the coordinator named no destination.
	_, errs, _ := wkRun(t, HarvestInput{Working: working, Base: "0123456789ab", Max: 20, Clones: []string{}})
	for _, l := range arglogLines(t, arglog) {
		if strings.HasPrefix(l, "git push") || strings.Contains(l, "pr create") {
			t.Fatalf("--working reached a forge with no manager-side destination: %s", l)
		}
	}
	wkHasLine(t, errs, "HARVEST REFUSED repo-unknown card=w")
}

// (3b) `harvest --bench` with a --clone that names nothing git can read: no coordinator
// record, so no destination, so nothing is published.
func TestHarvestBenchRefusesWhenTheCoordinatorCloneNamesNothing(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeTool(t, specs, "git", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 3, Equals: "rev-list", Stdout: "3"},
		{Arg: 3, Equals: "rev-parse", Stdout: "abc1234"},
		// no `remote get-url` rule: the coordinator's own clone answers nothing
	}})
	mine := "/home/gaffer/rowan-swarm-root/0/jobs/card-1"
	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		if strings.Contains(script, "touch") {
			return "", nil
		}
		return benchJobListing(mine, []string{
			"RESULT card-1 sha=abc", "DONE",
			"BRANCH rowan/card-1", "REPO attacker/exfil",
		}), nil
	}}
	forge := &fakeForge{}
	in := benchHarvestInput(t, root, shell, forge)
	in.BranchPrefix = DefaultBranchPrefix
	_, out, _ := runBenchHarvest(t, in)
	if len(forge.opened) != 0 {
		t.Fatalf("--bench opened a PR with no coordinator record: %+v", forge.opened)
	}
	if !strings.Contains(out, "HARVEST REFUSED repo-unknown card=card-1") {
		t.Fatalf("the repo-unknown refusal line is absent:\n%s", out)
	}
}

// ---------------------------------------------------------------------------------------
// The resolver, and the class test that keeps it the only reader of a clone's origin.
// ---------------------------------------------------------------------------------------

func TestResolveDestinationRefusals(t *testing.T) {
	dir := t.TempDir()
	card := filepath.Join(dir, "card-1.md")
	if err := os.WriteFile(card, []byte("RESULT card-1\nREPO owner/repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := filepath.Join(dir, "RESULT.md")
	if err := os.WriteFile(result, []byte("RESULT card-1\nREPO attacker/exfil\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The launch record ANSWERS, and the URL is formed from IT.
	got, err := resolveDestination("card-1", dispatchFromLaunchRecord(card), "", "owner/repo")
	if err != nil {
		t.Fatalf("the launch record must answer: %v", err)
	}
	if got.repo != "owner/repo" || got.url != githubCloneBase+"owner/repo.git" || got.from != "launch-record" {
		t.Fatalf("destination = %+v", got)
	}

	// A claim that disagrees is refused by name, and the line says what was dispatched.
	if _, err := resolveDestination("card-1", dispatchFromLaunchRecord(card), "", "attacker/exfil"); err == nil ||
		!strings.Contains(err.Error(), "HARVEST REFUSED repo-mismatch card=card-1 dispatched=owner/repo claimed=attacker/exfil") {
		t.Fatalf("the mismatch refusal: %v", err)
	}

	// A RESULT.md is not a launch record at any position, so it answers nothing.
	if _, err := resolveDestination("card-1", dispatchFromLaunchRecord(result), "", "attacker/exfil"); err == nil ||
		!strings.Contains(err.Error(), "HARVEST REFUSED repo-unknown card=card-1") {
		t.Fatalf("a RESULT.md must never be the record: %v", err)
	}

	// Nothing at all: refused, never guessed.
	if _, err := resolveDestination("card-1", dispatch{}, "", "attacker/exfil"); err == nil ||
		!strings.Contains(err.Error(), "HARVEST REFUSED repo-unknown card=card-1") {
		t.Fatalf("an empty dispatch must refuse: %v", err)
	}
}

// The coordinator's own spelling, and the one ambiguity it refuses rather than resolving
// out of a RESULT.md.
func TestTheOneCoordinatorDispatch(t *testing.T) {
	got, err := theOneCoordinatorDispatch([]string{"owner/repo=/somewhere"})
	if err != nil || got.repo != "owner/repo" || got.from != "coordinator-clone" {
		t.Fatalf("one named clone: %+v %v", got, err)
	}
	if _, err := theOneCoordinatorDispatch([]string{"owner/repo=/a", "other/repo=/b"}); err == nil ||
		!strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("two named clones must be ambiguous, not resolved by the worker: %v", err)
	}
	got, err = theOneCoordinatorDispatch(nil)
	if err != nil || got.repo != "" {
		t.Fatalf("no clone names nothing: %+v %v", got, err)
	}
}

// TestNoPushURLFromAResultRemains: `pushURL` formed the push destination FROM the worker's
// claim, which is the defect itself and not a detail of it. It is gone, and this test says
// so from the package's own source so it cannot quietly come back.
func TestNoPushURLFromAResultRemains(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		raw, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte("pushURL(")) {
			t.Errorf("%s still calls pushURL(): the push destination is resolveDestination's answer, never a URL a caller formed", e.Name())
		}
	}
}

// THE CLASS TEST FOR JOHNNY'S HOLD AT 7f692ef6. The cut before this one read the
// destination from `cloneOrigin(clone)` on the first line of the resolver, and the whole
// defect was that one call. A publishing path that reads a job clone's origin for itself
// would be the same defect with a different spelling, so `cloneOrigin` is callable from ONE
// file -- harvestdest.go, where it is only ever compared against the manager's record --
// and this walk fails on the commit that adds a second caller.
func TestCloneOriginIsOnlyEverComparedNeverRead(t *testing.T) {
	t.Parallel()

	const theOrigin = "cloneOrigin"
	const theOnlyFile = "harvestdest.go"

	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	callers := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Name.Name == theOrigin {
				continue
			}
			if !callsGuard(fn, theOrigin) {
				continue
			}
			callers++
			if name != theOnlyFile {
				t.Errorf("%s:%d %s() calls %s: a job clone's origin is the WORKER's -- one `git remote set-url` rewrites it -- so it is compared against the manager's dispatch record in %s and read nowhere else (Johnny's HOLD of #1809 at 7f692ef6)",
					filepath.Join("internal/pulse", name), fset.Position(fn.Pos()).Line, fn.Name.Name, theOrigin, theOnlyFile)
			}
		}
	}
	if callers == 0 {
		t.Fatalf("this walk found no caller of %s at all; the detector has gone blind, which is worse than no test", theOrigin)
	}
}
