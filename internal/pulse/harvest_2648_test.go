package pulse

// Issue #2648, requirements 1-3, on the Go harvest commit step.
//
// (1) The rebase target is the remote at the moment of rebase. A mirror refreshed
// earlier in the pass is not that target: dev can move after the check.
// (2) A FINDING-RED card rebases the same way a DONE card does, and the PR body
// carries that verdict.
// (3) unstick reads the typed stale-base refusal. It does not grep the pre-#2598
// sentence (no declared=, `: git diff` glued to the range).

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// pre2598UnstickWording is the sed unstick_stale_base used before the refusal
// grew a declared= field. A match is the old grep. The remedy must not need one.
var pre2598UnstickWording = regexp.MustCompile(`^[a-z]+ HARVEST REFUSED stale-base bench=[a-z]+ label=([^:]+): .*range=[0-9a-f]+\.\.refs/harvest/(.+): git diff`)

type recordingGit struct {
	inner GitRunner
	calls []string
	on    func(dir string, args []string)
}

func (r *recordingGit) Run(dir string, args ...string) (string, error) {
	r.calls = append(r.calls, dir+"\x00"+strings.Join(args, "\x00"))
	if r.on != nil {
		r.on(dir, args)
	}
	return r.inner.Run(dir, args...)
}

func (r *recordingGit) fetched(remote string) bool {
	for _, c := range r.calls {
		parts := strings.Split(c, "\x00")
		args := parts[1:]
		if len(args) == 0 || args[0] != "fetch" {
			continue
		}
		for _, a := range args[1:] {
			if a == remote {
				return true
			}
		}
	}
	return false
}

// TestRebaseReadsLiveBaseWhenDevMovesAfterTheMirrorCheck is requirement (1).
// The mirror is the check: it still names the old dev. The control advances the
// live remote after that check and before the rebase fetch. The card lands on
// the live tip, and the harvest's stale-base decision against that tip is clear.
func TestRebaseReadsLiveBaseWhenDevMovesAfterTheMirrorCheck(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	live := filepath.Join(root, "live.git")
	runCmd(t, root, "git", "init", "--bare", live)

	seed := filepath.Join(root, "seed")
	if err := os.MkdirAll(seed, 0o755); err != nil {
		t.Fatal(err)
	}
	initTestGitRepo(t, seed)
	runCmd(t, seed, "git", "remote", "add", "origin", live)
	runCmd(t, seed, "git", "push", "origin", "main:dev")
	checked := runCmd(t, live, "git", "rev-parse", "refs/heads/dev")

	mirrorDir := filepath.Join(root, "mirror")
	mirror := filepath.Join(mirrorDir, "nova-tools.git")
	runCmd(t, root, "git", "clone", "--bare", live, mirror)
	if got := runCmd(t, mirror, "git", "rev-parse", "refs/heads/dev"); got != checked {
		t.Fatalf("mirror check = %s, live dev = %s", got, checked)
	}

	jobDir := filepath.Join(root, "card-00-job-live")
	repoDir := filepath.Join(jobDir, "repo")
	runCmd(t, root, "git", "clone", "-b", "dev", live, repoDir)
	runCmd(t, repoDir, "git", "config", "user.name", "Rowan")
	runCmd(t, repoDir, "git", "config", "user.email", "rowan@mas-bandwidth.com")
	if err := os.WriteFile(filepath.Join(repoDir, "card_work.txt"), []byte("worker work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := "RESULT job-live sha=012345678901\nDONE\nBRANCH rowan/job-live\nPATHS: card_work.txt\n"
	if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(res), 0o644); err != nil {
		t.Fatal(err)
	}

	moved := false
	rec := &recordingGit{inner: defaultGitRunner{}, on: func(dir string, args []string) {
		if moved || len(args) == 0 || args[0] != "fetch" {
			return
		}
		// The control: dev moves after the mirror was checked and before this
		// fetch's bytes are read. A rebase that already captured the mirror tip
		// lands on the old base.
		moved = true
		if err := os.WriteFile(filepath.Join(seed, "other_pr.txt"), []byte("landed after the check\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runCmd(t, seed, "git", "add", "other_pr.txt")
		runCmd(t, seed, "git", "commit", "-m", "dev moved during the run")
		runCmd(t, seed, "git", "push", "origin", "main:dev")
	}}

	lines, err := CommitJob(CommitJobInput{
		JobDir:    jobDir,
		MirrorDir: mirrorDir,
		Runner:    rec,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !moved {
		t.Fatal("the rebase never fetched, so the control never moved dev")
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "REBASED job-live onto dev@") {
		t.Fatalf("expected REBASED onto the live base, got:\n%s", joined)
	}
	if rec.fetched(mirror) {
		t.Fatalf("rebase fetched the mirror %s, which the pass checked earlier:\n%s", mirror, strings.Join(rec.calls, "\n"))
	}
	liveTip := runCmd(t, live, "git", "rev-parse", "refs/heads/dev")
	if liveTip == checked {
		t.Fatal("the control did not move the live base")
	}
	if got := runCmd(t, mirror, "git", "rev-parse", "refs/heads/dev"); got != checked {
		t.Fatalf("the mirror was refreshed to %s; the rebase must not need that", got)
	}
	files := runCmd(t, repoDir, "git", "ls-tree", "-r", "--name-only", "HEAD")
	if !strings.Contains(files, "other_pr.txt") || !strings.Contains(files, "card_work.txt") {
		t.Fatalf("HEAD is not the live tip plus the card:\n%s", files)
	}
	if err := staleBaseRefusal(repoDir, live, "dev", "HEAD", []string{"card_work.txt"}, true); err != nil {
		t.Fatalf("harvest still refuses after the live rebase: %v", err)
	}
}

// TestFindingRedCardRebasesAndOpensPRCarryingVerdict is requirement (2).
// FINDING-RED is a finished card. It rebases onto the live base, and the PR
// body is the RESULT, verdict included. DONE already rebases; this is the card
// the commit step used to skip.
func TestFindingRedCardRebasesAndOpensPRCarryingVerdict(t *testing.T) {
	t.Run("rebase", func(t *testing.T) {
		root := t.TempDir()
		remote := filepath.Join(root, "remote.git")
		runCmd(t, root, "git", "init", "--bare", remote)
		seed := filepath.Join(root, "seed")
		if err := os.MkdirAll(seed, 0o755); err != nil {
			t.Fatal(err)
		}
		initTestGitRepo(t, seed)
		runCmd(t, seed, "git", "remote", "add", "origin", remote)
		runCmd(t, seed, "git", "push", "origin", "main:dev")

		jobDir := filepath.Join(root, "card-00-job-finding")
		repoDir := filepath.Join(jobDir, "repo")
		runCmd(t, root, "git", "clone", "-b", "dev", remote, repoDir)
		runCmd(t, repoDir, "git", "config", "user.name", "Rowan")
		runCmd(t, repoDir, "git", "config", "user.email", "rowan@mas-bandwidth.com")
		if err := os.WriteFile(filepath.Join(repoDir, "card_work.txt"), []byte("finding\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(seed, "other_pr.txt"), []byte("moved\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runCmd(t, seed, "git", "add", "other_pr.txt")
		runCmd(t, seed, "git", "commit", "-m", "dev moved")
		runCmd(t, seed, "git", "push", "origin", "main:dev")

		res := "RESULT job-finding sha=012345678901\nFINDING-RED\nBRANCH rowan/job-finding\n"
		if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(res), 0o644); err != nil {
			t.Fatal(err)
		}
		lines, err := CommitJob(CommitJobInput{JobDir: jobDir})
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(lines, "\n")
		if strings.Contains(joined, "reason=not-done") {
			t.Fatalf("FINDING-RED was skipped:\n%s", joined)
		}
		if !strings.Contains(joined, "REBASED job-finding onto dev@") {
			t.Fatalf("FINDING-RED was not rebased:\n%s", joined)
		}
		files := runCmd(t, repoDir, "git", "ls-tree", "-r", "--name-only", "HEAD")
		if !strings.Contains(files, "other_pr.txt") || !strings.Contains(files, "card_work.txt") {
			t.Fatalf("FINDING-RED HEAD is not the moved base plus the card:\n%s", files)
		}
	})

	t.Run("pr body", func(t *testing.T) {
		root, specs, arglog := setupPulse(t)
		benchGit(t, specs, arglog, nil)
		verdict := "FINDING-RED"
		job := "/home/gaffer/rowan-swarm-root/0/jobs/card-finding"
		shell := &fakeShell{answer: func(bench, script string) (string, error) {
			if strings.Contains(script, "touch") {
				return "", nil
			}
			return benchJobListing(job, []string{
				"RESULT card-finding sha=abc",
				verdict,
				"BRANCH rowan/card-finding",
				"REPO mas-bandwidth/nova-tools",
			}), nil
		}}
		forge := &fakeForge{}
		code, out, errb := runBenchHarvest(t, benchHarvestInput(t, root, shell, forge))
		if code != 0 {
			t.Fatalf("exit = %d, want 0\nstdout=%s\nstderr=%s", code, out, errb)
		}
		if len(forge.opened) != 1 {
			t.Fatalf("PRs opened = %d, want 1\n%s\n%s", len(forge.opened), out, errb)
		}
		if !strings.Contains(forge.opened[0].body, verdict) {
			t.Fatalf("the PR body does not carry the verdict %q:\n%s", verdict, forge.opened[0].body)
		}
	})
}

// TestUnstickStaleBaseMatchesTypedRefusalNotWording is requirement (3).
// The refusal sentence names a decoy branch and does not match the pre-#2598
// sed. The Branch field names the card. Unstick follows the field.
func TestUnstickStaleBaseMatchesTypedRefusalNotWording(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	runCmd(t, repo, "git", "init", "-b", "dev")
	runCmd(t, repo, "git", "config", "user.name", "Rowan")
	runCmd(t, repo, "git", "config", "user.email", "rowan@mas-bandwidth.com")
	if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCmd(t, repo, "git", "add", "a.go")
	runCmd(t, repo, "git", "commit", "-m", "base")
	runCmd(t, repo, "git", "update-ref", "refs/harvest/rowan/typed", "HEAD")
	runCmd(t, repo, "git", "update-ref", "refs/harvest/rowan/decoy", "HEAD")

	// Range is what the old sentence carried. It names the decoy on purpose:
	// a grep of the sentence, or of this field re-parsed as a sentence, unsticks
	// the wrong branch. Branch is the typed answer.
	r := &StaleBaseRefusal{
		Label:    "card-typed",
		Branch:   "rowan/typed",
		Files:    []string{"later.go"},
		Declared: "a.go",
		Range:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa..refs/harvest/rowan/decoy",
	}
	logLine := "vision HARVEST REFUSED stale-base bench=vision label=" + r.Label + ": " + r.Error()
	if pre2598UnstickWording.MatchString(logLine) {
		t.Fatalf("the refusal sentence still matches the old unstick sed:\n%s", logLine)
	}
	if !strings.Contains(r.Error(), "declared=") {
		t.Fatalf("the typed refusal's sentence lost declared=: %s", r.Error())
	}
	if !strings.Contains(logLine, "refs/harvest/rowan/decoy") {
		t.Fatalf("the sentence no longer names the decoy, so a grep would not pick it:\n%s", logLine)
	}

	var deleted []string
	lines, err := UnstickStaleBase(r, UnstickOptions{
		Clones:   []string{repo},
		SeenPath: filepath.Join(t.TempDir(), "seen"),
		Repos:    []string{"owner/nova-tools"},
		RemoteSHA: func(repoName, branch string) (string, error) {
			return "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", nil
		},
		PRCount: func(repoName, branch string) (int, error) {
			return 0, nil
		},
		PushDelete: func(repoName, branch string) error {
			deleted = append(deleted, repoName+" "+branch)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "UNSTICK-CACHE card-typed rowan/typed") {
		t.Fatalf("typed branch was not unstuck:\n%s", joined)
	}
	if gitRefExists(repo, "refs/harvest/rowan/typed") {
		t.Fatal("refs/harvest/rowan/typed still present")
	}
	if !gitRefExists(repo, "refs/harvest/rowan/decoy") {
		t.Fatal("the decoy the sentence names was removed")
	}
	if len(deleted) != 1 || deleted[0] != "owner/nova-tools rowan/typed" {
		t.Fatalf("orphan delete = %v, want the typed branch only", deleted)
	}
}

func gitRefExists(dir, ref string) bool {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "-q", "--verify", ref)
	return cmd.Run() == nil
}

// TestHarvestWorkingStaleBaseDropsTheTypedHarvestRef is the --working path's
// control for the same remedy. It reaches the drop only through HarvestWorking.
// The diff head is HEAD, which is what the refusal sentence's range ends with.
// The typed Branch field the remedy reads is the card branch. A decoy ref at
// refs/harvest/HEAD stays; only refs/harvest/<branch> is dropped.
func TestHarvestWorkingStaleBaseDropsTheTypedHarvestRef(t *testing.T) {
	b := newDestBench(t)
	fakeTool(t, os.Getenv("NOVA_PULSE_FAKE_DIR"), "gh", fakeSpec{
		Log:   b.arglog,
		Rules: []fakeRule{{Arg: 2, Equals: "list", Stdout: "[]"}},
	})

	working := t.TempDir()
	label := "wstale"
	branch := "rowan/wstale"
	job := wkJob(t, working, "guid-wstale", label,
		"RESULT "+label+" sha=aaa\nDONE\nBRANCH "+branch+"\nREPO owner/repo\nBASE dev\nPATHS: card/fix.go\n")
	realGit(t, "init", "-b", "dev", job)
	realGit(t, "-C", job, "remote", "add", "origin", honestURL)
	if err := os.MkdirAll(filepath.Join(job, "card"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(job, "card", "fix.go"), []byte("package card\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	realGit(t, "-C", job, "add", "card/fix.go")
	realGit(t, "-C", job, "commit", "-m", "old target")
	realGit(t, "-C", job, "checkout", "-b", branch)
	if err := os.WriteFile(filepath.Join(job, "card", "fix.go"), []byte("package card\n\nfunc Fix() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	realGit(t, "-C", job, "add", "card/fix.go")
	realGit(t, "-C", job, "commit", "-m", "the card")
	realGit(t, "-C", job, "checkout", "dev")
	if err := os.MkdirAll(filepath.Join(job, "later"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(job, "later", "merged.go"), []byte("package later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	realGit(t, "-C", job, "add", "later/merged.go")
	realGit(t, "-C", job, "commit", "-m", "later landing")
	realGit(t, "-C", job, "push", "origin", "HEAD:dev")
	realGit(t, "-C", job, "checkout", branch)

	// The typed Branch field this path remedies is the card branch. refs/harvest/HEAD
	// is the decoy a reader of the diff head (or of the sentence's `..HEAD`) would drop.
	typed := "refs/harvest/" + branch
	decoy := "refs/harvest/" + branchFromHead("HEAD")
	realGit(t, "-C", job, "update-ref", typed, "HEAD")
	realGit(t, "-C", job, "update-ref", decoy, "HEAD")

	out, errs, code := wkRun(t, HarvestInput{
		Working: working,
		Base:    "dev",
		Max:     20,
		Clones:  []string{"owner/repo=" + b.honest},
	})
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (the stale base is still a refusal)\n%s\n%s", code, out, errs)
	}
	if strings.Contains(out, "class=fixed") || strings.Contains(out, "pushed=1") {
		t.Fatalf("a stale-base working job was published:\n%s\n%s", out, errs)
	}
	if containsRef(refs(t, b.honest), "refs/heads/"+branch) {
		t.Fatalf("the branch was pushed: %v", refs(t, b.honest))
	}
	if gitRefExists(job, typed) {
		t.Fatalf("the ref named by the typed Branch field is still present: %s", typed)
	}
	if !gitRefExists(job, decoy) {
		t.Fatalf("the card-branch ref was removed; the remedy followed something other than the typed Branch field")
	}
}
