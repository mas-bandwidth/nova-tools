package pulse

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHarvestRefusesAReturnedBranchWhoseBaseIsStale is the failing-first case the
// issue names: a card branch cut from an older base, later files landed on the
// target, harvest must refuse and name those files, and nothing is pushed.
func TestHarvestRefusesAReturnedBranchWhoseBaseIsStale(t *testing.T) {
	b := newDestBench(t)
	label := "card-2032"
	branch := "rowan/stale-base"
	addCard(t, b.root, label, "1", "flash", "RESULT "+label+" sha=aaa",
		"RESULT "+label+" sha=aaa\nDONE\nBRANCH "+branch+"\nREPO owner/repo\nBASE dev\n")
	cardPath := filepath.Join(b.root, "cardsrc", label+".md")
	raw, err := os.ReadFile(cardPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cardPath, append(raw, []byte("PATHS: card/fix.go\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	b.job = filepath.Join(b.root, "1", "jobs", label)
	realGit(t, "init", "-b", "dev", b.job)
	realGit(t, "-C", b.job, "remote", "add", "origin", honestURL)
	if err := os.MkdirAll(filepath.Join(b.job, "card"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.job, "card", "fix.go"), []byte("package card\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	realGit(t, "-C", b.job, "add", "card/fix.go")
	realGit(t, "-C", b.job, "commit", "-m", "old target")
	realGit(t, "-C", b.job, "checkout", "-b", branch)
	if err := os.WriteFile(filepath.Join(b.job, "card", "fix.go"), []byte("package card\n\nfunc Fix() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	realGit(t, "-C", b.job, "add", "card/fix.go")
	realGit(t, "-C", b.job, "commit", "-m", "the card")
	realGit(t, "-C", b.job, "checkout", "dev")
	if err := os.MkdirAll(filepath.Join(b.job, "later"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.job, "later", "merged.go"), []byte("package later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	realGit(t, "-C", b.job, "add", "later/merged.go")
	realGit(t, "-C", b.job, "commit", "-m", "later files landed on the target")
	realGit(t, "-C", b.job, "push", "origin", "HEAD:dev")
	realGit(t, "-C", b.job, "checkout", branch)

	out, errs := runHarvest(t, b.root)

	if got := refs(t, b.honest); containsRef(got, "refs/heads/"+branch) {
		t.Fatalf("a stale-base branch became a push: %s holds %v\nstdout=%s\nstderr=%s", b.honest, got, out, errs)
	}
	if strings.Contains(out, "HARVEST PR") || strings.Contains(out, "prs=1") {
		t.Fatalf("a stale-base branch became a PR:\n%s\n%s", out, errs)
	}
	if !strings.Contains(out, "pushed=0") || !strings.Contains(out, "prs=0") {
		t.Fatalf("want pushed=0 prs=0, got:\n%s\n%s", out, errs)
	}
	if !strings.Contains(out, "refused=1") && !strings.Contains(errs, "HARVEST REFUSED") {
		t.Fatalf("want refused=1 or HARVEST REFUSED, got:\n%s\n%s", out, errs)
	}
	if !strings.Contains(out, "later/merged.go") && !strings.Contains(errs, "later/merged.go") {
		t.Fatalf("the refusal must name the later file that would be reverted:\nstdout=%s\nstderr=%s", out, errs)
	}
	for _, l := range arglogLines(t, b.arglog) {
		if strings.Contains(l, "pr create") {
			t.Fatalf("gh pr create ran for a stale-base branch: %s", l)
		}
	}
}

// TestHarvestBenchRefusesAReturnedBranchWhoseBaseIsStale is the schema14 path:
// harvest --bench, before any push or CreatePR.
func TestHarvestBenchRefusesAReturnedBranchWhoseBaseIsStale(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeTool(t, specs, "git", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 3, Equals: "diff", Stdout: "card/fix.go\nlater/merged.go\n"},
		{Arg: 3, Equals: "rev-list", Stdout: "3"},
		{Arg: 3, Equals: "rev-parse", Stdout: "abc1234"},
		originRule("mas-bandwidth/nova-tools"),
	}})
	job := "/home/gaffer/rowan-swarm-root/0/jobs/card-2032"
	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		if strings.Contains(script, "touch") {
			return "", nil
		}
		return benchJobListing(job, []string{
			"RESULT card-2032 sha=abc",
			"DONE",
			"PATHS: card/fix.go",
			"BRANCH rowan/card-2032",
			"REPO mas-bandwidth/nova-tools",
			"BASE dev",
		}), nil
	}}
	forge := &fakeForge{}
	code, out, errb := runBenchHarvest(t, benchHarvestInput(t, root, shell, forge))
	if len(forge.opened) != 0 {
		t.Fatalf("a stale-base returned branch became a PR: %+v\n%s\n%s", forge.opened, out, errb)
	}
	log, _ := os.ReadFile(arglog)
	if strings.Contains(string(log), "push origin") {
		t.Fatalf("a stale-base returned branch was pushed:\n%s", log)
	}
	if !strings.Contains(out, "later/merged.go") && !strings.Contains(errb, "later/merged.go") {
		t.Fatalf("the refusal must name the later file:\nstdout=%s\nstderr=%s", out, errb)
	}
	if code == 0 {
		t.Fatalf("exit = 0, want non-zero for a refused harvest\n%s\n%s", out, errb)
	}
}

// TestHarvestRefusesWhenTheRemoteAdvancedAfterClone is Stella's HOLD on #2117:
// the worker clone's origin/dev is left stale while a later landing is pushed to
// the authorized destination. Harvest must fetch that destination and refuse;
// comparing the cached origin/dev would clear and open a PR.
func TestHarvestRefusesWhenTheRemoteAdvancedAfterClone(t *testing.T) {
	b := newDestBench(t)
	label := "card-2032-hold"
	branch := "rowan/card"
	addCard(t, b.root, label, "1", "flash", "RESULT "+label+" sha=aaa",
		"RESULT "+label+" sha=aaa\nDONE\nBRANCH "+branch+"\nREPO owner/repo\nBASE dev\n")
	cardPath := filepath.Join(b.root, "cardsrc", label+".md")
	raw, err := os.ReadFile(cardPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cardPath, append(raw, []byte("PATHS: card.go\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	b.job = filepath.Join(b.root, "1", "jobs", label)
	realGit(t, "init", "-b", "dev", b.job)
	realGit(t, "-C", b.job, "remote", "add", "origin", honestURL)
	if err := os.WriteFile(filepath.Join(b.job, "card.go"), []byte("package card\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	realGit(t, "-C", b.job, "add", "card.go")
	realGit(t, "-C", b.job, "commit", "-m", "old target")
	realGit(t, "-C", b.job, "push", "origin", "HEAD:dev")
	// Pin the worker's origin/dev to that old SHA. Fetch the local bare repo
	// directly: dest.url is a forge URL, and this is the clone's cached tracking
	// ref, the one the HOLD says must not be treated as current.
	realGit(t, "-C", b.job, "fetch", b.honest, "+refs/heads/dev:refs/remotes/origin/dev")
	realGit(t, "-C", b.job, "checkout", "-b", branch)
	if err := os.WriteFile(filepath.Join(b.job, "card.go"), []byte("package card\n\nfunc Fix() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	realGit(t, "-C", b.job, "add", "card.go")
	realGit(t, "-C", b.job, "commit", "-m", "the card")

	advanceDestDev(t, b, "later.go", "package later\n")

	old := strings.TrimSpace(realGit(t, "-C", b.job, "rev-parse", "origin/dev"))
	live := strings.TrimSpace(realGit(t, "-C", b.honest, "rev-parse", "refs/heads/dev"))
	if old == live {
		t.Fatal("the worker origin/dev still matches the destination; the remote did not advance")
	}

	out, errs := runHarvest(t, b.root)

	if got := refs(t, b.honest); containsRef(got, "refs/heads/"+branch) {
		t.Fatalf("a stale origin/dev was treated as current: the branch was pushed\norigin/dev=%s dest/dev=%s\nstdout=%s\nstderr=%s", old, live, out, errs)
	}
	if strings.Contains(out, "HARVEST PR") || strings.Contains(out, "prs=1") {
		t.Fatalf("stale origin/dev was treated as current: a PR opened\nstdout=%s\nstderr=%s", out, errs)
	}
	if !strings.Contains(out, "later.go") && !strings.Contains(errs, "later.go") {
		t.Fatalf("the refusal must name the later landing, not clear against stale origin/dev\norigin/dev=%s dest/dev=%s\nstdout=%s\nstderr=%s", old, live, out, errs)
	}
}

func advanceDestDev(t *testing.T, b *destBench, rel, body string) {
	t.Helper()
	tmp := t.TempDir()
	realGit(t, "clone", "-b", "dev", b.honest, tmp)
	dir := filepath.Dir(filepath.Join(tmp, filepath.FromSlash(rel)))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, filepath.FromSlash(rel)), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	realGit(t, "-C", tmp, "add", rel)
	realGit(t, "-C", tmp, "commit", "-m", "later landing on the destination")
	realGit(t, "-C", tmp, "push", "origin", "HEAD:dev")
}

func containsRef(refs []string, want string) bool {
	for _, r := range refs {
		if r == want {
			return true
		}
	}
	return false
}

// The positive control: a card branch cut from the current target, two-dot inside
// PATHS, still opens. A guard that refuses everything is not a guard.
func TestHarvestOpensAPRWhenTheTwoDotDiffStaysInsidePATHS(t *testing.T) {
	b := newDestBench(t)
	label := "card-2032-ok"
	branch := "rowan/fresh-base"
	addCard(t, b.root, label, "1", "flash", "RESULT "+label+" sha=aaa",
		"RESULT "+label+" sha=aaa\nDONE\nBRANCH "+branch+"\nREPO owner/repo\nBASE dev\n")
	cardPath := filepath.Join(b.root, "cardsrc", label+".md")
	raw, err := os.ReadFile(cardPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cardPath, append(raw, []byte("PATHS: card/fix.go\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	b.job = filepath.Join(b.root, "1", "jobs", label)
	realGit(t, "init", "-b", "dev", b.job)
	realGit(t, "-C", b.job, "remote", "add", "origin", honestURL)
	if err := os.MkdirAll(filepath.Join(b.job, "card"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.job, "card", "fix.go"), []byte("package card\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	realGit(t, "-C", b.job, "add", "card/fix.go")
	realGit(t, "-C", b.job, "commit", "-m", "current target")
	realGit(t, "-C", b.job, "push", "origin", "HEAD:dev")
	realGit(t, "-C", b.job, "checkout", "-b", branch)
	if err := os.WriteFile(filepath.Join(b.job, "card", "fix.go"), []byte("package card\n\nfunc Fix() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	realGit(t, "-C", b.job, "add", "card/fix.go")
	realGit(t, "-C", b.job, "commit", "-m", "the card")

	out, errs := runHarvest(t, b.root)

	if got := refs(t, b.honest); len(got) == 0 {
		t.Fatalf("the in-path branch was not pushed:\n%s\n%s", out, errs)
	}
	if !strings.Contains(out, "pushed=1") || !strings.Contains(out, "prs=1") {
		t.Fatalf("want pushed=1 prs=1, got:\n%s\n%s", out, errs)
	}
}

// TestHarvestRefusesAnInvalidExplicitBASE is the remaining HOLD on #2117: a present
// invalid BASE (HEAD, a hex OID, malformed) must not be discarded and replaced with
// default `dev`. Omitted BASE still defaults to `dev`.
func TestHarvestRefusesAnInvalidExplicitBASE(t *testing.T) {
	for _, base := range []string{"HEAD", "0123456789abcdef0123456789abcdef01234567", "dev..main"} {
		t.Run(base, func(t *testing.T) {
			b := newDestBench(t)
			label := "card-invalid-target"
			branch := "rowan/invalid-target"
			addCard(t, b.root, label, "1", "flash", "RESULT "+label+" sha=aaa",
				"RESULT "+label+" sha=aaa\nDONE\nBRANCH "+branch+"\nREPO owner/repo\nBASE "+base+"\n")
			cardPath := filepath.Join(b.root, "cardsrc", label+".md")
			raw, err := os.ReadFile(cardPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cardPath, append(raw, []byte("PATHS: card.go\n")...), 0o644); err != nil {
				t.Fatal(err)
			}
			b.job = filepath.Join(b.root, "1", "jobs", label)
			realGit(t, "init", "-b", "dev", b.job)
			realGit(t, "-C", b.job, "remote", "add", "origin", honestURL)
			if err := os.WriteFile(filepath.Join(b.job, "card.go"), []byte("package card\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			realGit(t, "-C", b.job, "add", "card.go")
			realGit(t, "-C", b.job, "commit", "-m", "current target")
			realGit(t, "-C", b.job, "push", "origin", "HEAD:dev")
			realGit(t, "-C", b.job, "checkout", "-b", branch)
			if err := os.WriteFile(filepath.Join(b.job, "card.go"), []byte("package card\n\nfunc Fix() {}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			realGit(t, "-C", b.job, "add", "card.go")
			realGit(t, "-C", b.job, "commit", "-m", "the card")

			out, errs := runHarvest(t, b.root)

			if got := refs(t, b.honest); containsRef(got, "refs/heads/"+branch) {
				t.Fatalf("invalid explicit BASE %q was treated as omitted and the branch was pushed:\n%s\n%s", base, out, errs)
			}
			if strings.Contains(out, "HARVEST PR") || strings.Contains(out, "prs=1") {
				t.Fatalf("invalid explicit BASE %q opened a PR:\n%s\n%s", base, out, errs)
			}
			if !strings.Contains(errs, "HARVEST REFUSED") {
				t.Fatalf("want HARVEST REFUSED for BASE %q, got:\n%s\n%s", base, out, errs)
			}
			if !strings.Contains(errs, "HEAD") && !strings.Contains(errs, base) && !strings.Contains(out, base) {
				t.Fatalf("the refusal must name the invalid explicit target %q:\n%s\n%s", base, out, errs)
			}
		})
	}
}

func TestHarvestTargetBranchRejectsAPresentInvalidTarget(t *testing.T) {
	dev, err := harvestTargetBranch(HarvestInput{}, nil)
	if err != nil || dev != DefaultBase {
		t.Fatalf("omitted BASE = %q, %v, want %s", dev, err, DefaultBase)
	}
	if _, err := harvestTargetBranch(HarvestInput{}, []string{"BASE HEAD"}); err == nil {
		t.Fatal("BASE HEAD must not fall through to default dev")
	}
	if _, err := harvestTargetBranch(HarvestInput{Base: "HEAD"}, nil); err == nil {
		t.Fatal("--base HEAD must not fall through to default dev")
	}
	if _, err := harvestTargetBranch(HarvestInput{Base: "0123456789ab"}, nil); err == nil {
		t.Fatal("hex --base must not fall through to default dev")
	}
	got, err := harvestTargetBranch(HarvestInput{}, []string{"BASE main"})
	if err != nil || got != "main" {
		t.Fatalf("BASE main = %q, %v, want main", got, err)
	}
}

// ---------------------------------------------------------------------------
// Issue #2547: the stale-base refusal is a FALSE POSITIVE when the branch's
// first parent IS the pinned target tip and the two-dot diff is exactly the
// card's declared PATHS. Fourteen finished cards were refused every pass on
// 2026-09-22 with a range whose left side (af6a9fcc) was the live dev tip and a
// `files=` list that was, character for character, the card's own PATHS line.
//
// The fixtures below are real git repositories under t.TempDir(): a bare
// destination holding `dev`, a clone, a branch cut from `dev`'s tip that
// changes exactly the declared paths, and the pin `refs/harvest/target/dev`
// that `staleBaseRefusal` fetches for itself. Nothing is faked, because the
// defect was in what the declared globs MEAN, not in what git said.

// staleFixture is one fixture repository: a bare destination with `dev`, and a
// clone holding the card branch. destURL and clone are what `staleBaseRefusal`
// is called with.
type staleFixture struct {
	destURL string
	clone   string
	branch  string
}

// newStaleFixture builds the true case: `base` files exist on `dev`, the branch
// is cut from `dev`'s CURRENT tip, and it changes exactly `changed`.
func newStaleFixture(t *testing.T, base, changed []string) staleFixture {
	t.Helper()
	dir := t.TempDir()
	dest := filepath.Join(dir, "dest.git")
	realGit(t, "init", "--bare", "-b", "dev", dest)
	clone := filepath.Join(dir, "clone")
	realGit(t, "init", "-b", "dev", clone)
	writeFixtureFiles(t, clone, base, "the target")
	commitFixture(t, clone, "the target")
	realGit(t, "-C", clone, "push", dest, "HEAD:dev")
	branch := "rowan/card-2547"
	realGit(t, "-C", clone, "checkout", "-q", "-b", branch)
	writeFixtureFiles(t, clone, changed, "the card")
	commitFixture(t, clone, "the card")
	return staleFixture{destURL: dest, clone: clone, branch: branch}
}

// advanceTarget lands `landed` on the destination's `dev` AFTER the branch was
// cut, which is the real stale base of issue #2032: the two-dot diff against the
// current target shows those files as deletions, and a PR would revert them.
func advanceTarget(t *testing.T, f staleFixture, landed []string) {
	t.Helper()
	realGit(t, "-C", f.clone, "checkout", "-q", "dev")
	writeFixtureFiles(t, f.clone, landed, "landed after the cut")
	commitFixture(t, f.clone, "landed after the cut")
	realGit(t, "-C", f.clone, "push", f.destURL, "HEAD:dev")
	realGit(t, "-C", f.clone, "checkout", "-q", f.branch)
}

func writeFixtureFiles(t *testing.T, repo string, paths []string, body string) {
	t.Helper()
	for _, p := range paths {
		full := filepath.Join(repo, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("// "+body+" "+p+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func commitFixture(t *testing.T, repo, msg string) {
	t.Helper()
	realGit(t, "-C", repo, "add", "-A")
	realGit(t, "-C", repo, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
		"commit", "-q", "-m", msg)
}

// TestStaleBaseAcceptsBranchWhoseParentIsTargetTip is the card's RED-WHEN, as a
// table. Every row's branch is cut from the pinned target tip and its two-dot
// diff is exactly the row's declared paths; every row must be accepted. The rows
// are the PATHS spellings the fleet actually cut on 2026-09-22.
func TestStaleBaseAcceptsBranchWhoseParentIsTargetTip(t *testing.T) {
	for _, tc := range []struct {
		name    string
		base    []string // files already on the target
		changed []string // what the card branch changes
		paths   string   // the card's PATHS: value, verbatim
	}{
		{
			// The spec's own spelling, `PATHS: <glob>[, <glob>...]`. This row
			// passed before the fix and is here so the table proves the others
			// are about the SPELLING and not about the walk.
			name:    "comma separated",
			base:    []string{"internal/pulse/harveststale.go"},
			changed: []string{"internal/pulse/harveststale.go", "internal/pulse/harveststale_test.go"},
			paths:   "internal/pulse/harveststale.go, internal/pulse/harveststale_test.go",
		},
		{
			// LIVE, hulk 2026-09-22, card-tools-47-evaluate-github-merge-queue:
			//   stale-base files=docs/EVAL-MERGE-QUEUE.md,internal/docs/eval_merge_queue_test.go
			//   range=af6a9fccf33199b4edfc586ab3c7f0c5e1a2d71d..refs/harvest/rowan/tools-47-evaluate-github-merge-queue
			// The card's own PATHS line names exactly those two files, separated
			// by a SPACE, and af6a9fcc was the live dev tip. The value was split
			// on commas only, so the whole line became one glob that matches no
			// path at all and every declared file was reported as an offender.
			name:    "space separated, the shape cards v2 cuts",
			base:    []string{"docs/EVAL-MERGE-QUEUE.md"},
			changed: []string{"docs/EVAL-MERGE-QUEUE.md", "internal/docs/eval_merge_queue_test.go"},
			paths:   "docs/EVAL-MERGE-QUEUE.md internal/docs/eval_merge_queue_test.go",
		},
		{
			// LIVE, superman 2026-09-22, card-tools22-spec2-TestRecoveryKeyLives...:
			//   stale-base files=internal/secrets/recovery_key_test.go
			// The card declares `PATHS: internal/secrets` -- a DIRECTORY -- and a
			// file inside it was called undeclared (the A2 report's second shape).
			name:    "a directory entry covers the files inside it",
			base:    []string{"internal/secrets/seat.go"},
			changed: []string{"internal/secrets/recovery_key_test.go"},
			paths:   "internal/secrets",
		},
		{
			name:    "a directory entry with a trailing slash",
			base:    []string{"internal/secrets/seat.go"},
			changed: []string{"internal/secrets/recovery_key_test.go"},
			paths:   "internal/secrets/",
		},
		{
			// `sign/**` is the spelling docs/SPEC-TOOLWORK.md §4 rule 4 and
			// hygiene.ValidatePaths bless for a directory; it must keep working.
			name:    "the spec's ** spelling for a directory",
			base:    []string{"internal/secrets/seat.go"},
			changed: []string{"internal/secrets/recovery_key_test.go"},
			paths:   "internal/secrets/**",
		},
		{
			name:    "a segment glob",
			base:    []string{"internal/swarm/providers.go"},
			changed: []string{"internal/swarm/providers.go", "internal/swarm/providers_test.go"},
			paths:   "internal/swarm/*.go",
		},
		{
			// Mixed, because a hand-edited card carries both separators.
			name:    "commas and spaces in one line",
			base:    []string{"internal/swarm/templates.go"},
			changed: []string{"internal/swarm/templates.go", "internal/swarm/templatesunattended_test.go"},
			paths:   "internal/swarm/templates.go,  internal/swarm/templatesunattended_test.go",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newStaleFixture(t, tc.base, tc.changed)
			globs, declared := parsePATHS("PATHS: " + tc.paths)
			if !declared {
				t.Fatalf("parsePATHS did not read %q as a declaration", tc.paths)
			}
			if err := staleBaseRefusal(f.clone, f.destURL, "dev", f.branch, globs, declared); err != nil {
				t.Fatalf("a branch cut from the pinned target tip whose two-dot diff is exactly its declared PATHS was refused: %v\ndeclared=%q globs=%q changed=%v",
					err, tc.paths, globs, tc.changed)
			}
		})
	}
}

// TestStaleBaseStillRefusesAnOlderBase is the control that bites: the same
// spellings, but the branch is cut from an OLDER base and a later landing sits
// on the current target. Every row must still refuse, and the refusal must name
// the LANDED file and only it -- issue #2032 is why this check exists.
func TestStaleBaseStillRefusesAnOlderBase(t *testing.T) {
	for _, tc := range []struct {
		name  string
		paths string
	}{
		{"comma separated", "internal/pulse/harveststale.go, internal/pulse/harveststale_test.go"},
		{"space separated", "internal/pulse/harveststale.go internal/pulse/harveststale_test.go"},
		{"a directory entry", "internal/pulse"},
		{"a directory entry with a trailing slash", "internal/pulse/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newStaleFixture(t,
				[]string{"internal/pulse/harveststale.go"},
				[]string{"internal/pulse/harveststale.go", "internal/pulse/harveststale_test.go"})
			advanceTarget(t, f, []string{"later/merged.go"})
			globs, declared := parsePATHS("PATHS: " + tc.paths)
			err := staleBaseRefusal(f.clone, f.destURL, "dev", f.branch, globs, declared)
			if err == nil {
				t.Fatalf("a branch cut from an older base was accepted; opening it as a PR reverts later/merged.go (#2032)")
			}
			msg := err.Error()
			if !strings.Contains(msg, "later/merged.go") {
				t.Fatalf("the refusal must NAME the file it saw; got %q", msg)
			}
			if strings.Contains(msg, "files=internal/pulse/harveststale.go") {
				t.Fatalf("a declared path was reported as an offender: %q", msg)
			}
			if !strings.Contains(msg, "declared=") {
				t.Fatalf("the refusal must print the declared globs it judged against, so a mis-read PATHS line is visible: %q", msg)
			}
		})
	}
}

// TestStaleBaseSaysMISSINGWhenTheTargetCannotBeRead: no evidence is not negative
// evidence. A destination with no `dev` cannot be compared against, and the
// verdict must say so in those words rather than read as a DIFFER.
//
// The assertion anchors on the VERDICT and not on a substring: written as
// strings.Contains(msg, "MISSING") it passed on the unfixed code, because
// t.TempDir() names the directory after the test and the fetch error quotes that
// path. A check that its own positive control cannot fail is not a check.
func TestStaleBaseSaysMISSINGWhenTheTargetCannotBeRead(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "empty.git")
	realGit(t, "init", "--bare", "-b", "main", dest)
	clone := filepath.Join(dir, "clone")
	realGit(t, "init", "-b", "dev", clone)
	writeFixtureFiles(t, clone, []string{"a.go"}, "a")
	commitFixture(t, clone, "a")
	err := staleBaseRefusal(clone, dest, "dev", "HEAD", []string{"a.go"}, true)
	if err == nil {
		t.Fatal("an unreadable target must refuse, never clear")
	}
	if !strings.HasPrefix(err.Error(), "stale-base MISSING ") {
		t.Fatalf("an unreadable pinned target must open its verdict with `stale-base MISSING `, got %q", err)
	}
	if strings.Contains(err.Error(), "files=") {
		t.Fatalf("a target nobody could read must not read as a DIFFER over an offender list: %q", err)
	}
}

// TestStaleBaseComparesAPinnedOIDNotAMovingRef: the left side of the range the
// refusal prints is the OID the fetch pinned, not `origin/dev` or any other ref
// that can move under the check. The live refusals printed
// af6a9fccf33199b4edfc586ab3c7f0c5e1a2d71d, which is that pin.
func TestStaleBaseComparesAPinnedOIDNotAMovingRef(t *testing.T) {
	f := newStaleFixture(t,
		[]string{"internal/pulse/harveststale.go"},
		[]string{"internal/pulse/harveststale.go", "stray/extra.go"})
	// A cached, WRONG origin/dev in the clone: if the walk read this instead of
	// the pin, the range would name it.
	realGit(t, "-C", f.clone, "update-ref", "refs/remotes/origin/dev", f.branch)
	want := strings.TrimSpace(realGit(t, "-C", f.clone, "rev-parse", "dev"))
	err := staleBaseRefusal(f.clone, f.destURL, "dev", f.branch, []string{"internal/pulse/harveststale.go"}, true)
	if err == nil {
		t.Fatal("stray/extra.go is undeclared; the walk must refuse")
	}
	if !strings.Contains(err.Error(), "range="+want+"..") {
		t.Fatalf("the range must open at the pinned target OID %s, got %q", want, err)
	}
	if !strings.Contains(err.Error(), "stray/extra.go") {
		t.Fatalf("the refusal must name the offending path, got %q", err)
	}
}

// TestHarvestCallsHygieneMatchGlob verifies that internal/pulse has no local copy
// of the glob matcher: matchDeclared delegates to hygiene.MatchGlob (imported as
// pathglob), and there is no matchDeclaredSegs or matchSegments function. A
// second matcher would be a second definition (#2598 remainder, #2600).
func TestHarvestCallsHygieneMatchGlob(t *testing.T) {
	// 1. Verify matchDeclared matches through hygiene.MatchGlob by testing the
	//    same cases the hygiene package tests.
	matchCases := []struct {
		glob, path string
		want       bool
	}{
		{"internal/pulse/*.go", "internal/pulse/harveststale.go", true},
		{"internal/pulse/*.go", "internal/pulse/harvest.go", true},
		{"internal/pulse/*.go", "internal/hygiene/glob.go", false},
		{"internal/pulse/**", "internal/pulse/sub/file.go", true},
		{"internal/pulse", "internal/pulse/harveststale.go", true},
		{"internal/pulse/", "internal/pulse/harveststale.go", true},
	}
	for _, tc := range matchCases {
		if got := matchDeclared(tc.glob, tc.path); got != tc.want {
			t.Errorf("matchDeclared(%q, %q) = %v, want %v", tc.glob, tc.path, got, tc.want)
		}
	}

	// 2. Scan harveststale.go AST for a local matchDeclaredSegs or
	//    matchSegments function. The function must not exist: the glob half
	//    lives in hygiene.MatchGlob.
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "harveststale.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}
		name := fn.Name.Name
		if name == "matchDeclaredSegs" || name == "matchSegments" {
			t.Errorf("harveststale.go contains local function %s; the glob half must live in hygiene.MatchGlob", name)
		}
		return true
	})
}
