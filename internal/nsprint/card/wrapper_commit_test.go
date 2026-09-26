package card_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v %s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newOrigin is a bare origin with one commit on dev.
func newOrigin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	origin := filepath.Join(dir, "origin.git")
	gitIn(t, dir, "init", "-q", "--bare", origin)
	seed := filepath.Join(dir, "seed")
	gitIn(t, dir, "init", "-q", seed)
	gitIn(t, seed, "commit", "-q", "--allow-empty", "-m", "base")
	gitIn(t, seed, "push", "-q", origin, "HEAD:refs/heads/dev")
	gitIn(t, dir, "--git-dir", origin, "symbolic-ref", "HEAD", "refs/heads/dev")
	return origin
}

// TestWrapperCommitsOutputOnCardBranch (#2932 control 5): the commit step
// commits the harness's uncommitted out/repo onto nova/<S>/<label>-a<attempt>
// with the RESULT line as the message and `nova-card <bench>` as the author,
// leaves card scratch out, refuses a file over 1 MB (OVERSIZE, pushed_sha
// "-"), and the wrapper hands that commit to card end as pushed_sha.
func TestWrapperCommitsOutputOnCardBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	const branch = "nova/control-commit/card-x-a1"

	t.Run("commit-step", func(t *testing.T) {
		repo := filepath.Join(t.TempDir(), "repo")
		gitIn(t, filepath.Dir(repo), "clone", "-q", newOrigin(t), repo)
		for _, f := range []string{"work.txt", "RESULT.md", "notes.txt", "sub/REPORT.md", "sub/code.go", "usage.tsv", "repo.bundle"} {
			p := filepath.Join(repo, filepath.FromSlash(f))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(f+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		res, err := card.CommitOutput(repo, branch, "RESULT: card-x OK", "superman")
		if err != nil || res.Note != "COMMITTED" || len(res.SHA) != 40 {
			t.Fatalf("commit = %+v, %v; want a commit", res, err)
		}
		if got := gitIn(t, repo, "rev-parse", "refs/heads/"+branch); got != res.SHA {
			t.Fatalf("branch %s at %s, want the commit %s", branch, got, res.SHA)
		}
		if got := gitIn(t, repo, "log", "-1", "--format=%an <%ae>|%cn <%ce>|%s", res.SHA); got != "nova-card <superman>|nova-card <superman>|RESULT: card-x OK" {
			t.Fatalf("author|committer|subject = %q", got)
		}
		files := gitIn(t, repo, "show", "--name-only", "--format=", res.SHA)
		if files != "sub/code.go\nwork.txt" {
			t.Fatalf("committed files %q; want the work only, no card scratch", files)
		}
	})

	t.Run("oversize", func(t *testing.T) {
		repo := filepath.Join(t.TempDir(), "repo")
		gitIn(t, filepath.Dir(repo), "clone", "-q", newOrigin(t), repo)
		if err := os.WriteFile(filepath.Join(repo, "small.txt"), []byte("ok\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, "big.bin"), make([]byte, card.MaxCommitFile+1), 0o644); err != nil {
			t.Fatal(err)
		}
		res, err := card.CommitOutput(repo, branch, "RESULT: big", "superman")
		if err != nil || res.SHA != card.NoCommit || res.Note != "OVERSIZE big.bin" {
			t.Fatalf("oversize = %+v, %v; want pushed_sha - and OVERSIZE big.bin", res, err)
		}
		if out, err := exec.Command("git", "-C", repo, "rev-parse", "--verify", "-q", "refs/heads/"+branch).CombinedOutput(); err == nil {
			t.Fatalf("an oversize card made branch %s at %s", branch, out)
		}
	})

	t.Run("no-repo", func(t *testing.T) {
		res, err := card.CommitOutput(filepath.Join(t.TempDir(), "repo"), branch, "RESULT", "superman")
		if err != nil || res.SHA != card.NoCommit || res.Note != "NO-COMMIT" {
			t.Fatalf("no repo = %+v, %v; want NO-COMMIT", res, err)
		}
	})

	t.Run("wrapper-end-carries-pushed-sha", func(t *testing.T) {
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		st, client := newSprint(t)
		id := card.Identity{Sprint: "control-commit", Label: "card-x", BaseSHA: "0123abcd", Bench: "wrap-bench", Attempt: 1}
		token := attemptToken(1, strings.Repeat("c", 32))
		seedCard(t, ctx, client, id, "dealt", token)
		gate := filepath.Join(t.TempDir(), "gate")
		t.Setenv(fakeHarnessEnv, "repo")
		t.Setenv(fakeGateEnv, gate)
		t.Setenv(fakeOriginEnv, newOrigin(t))
		h := newHarnessRun(t, id, self)
		ledger := &observed{inner: &card.RedisLedger{Store: st, Sprint: id.Sprint, Label: id.Label, Token: token}, events: make(chan string, 64)}
		// #4227: the end names the checkout the commit lives in, and that
		// checkout is still on disk when the ledger ends (a copy's ledger
		// pushes from it); the job dir goes only after the end.
		wantRepo := filepath.Join(card.WrapperJobDir(h.cfg.JobsRoot, id.Sprint, id.Label, 1), "out", "repo")
		ledger.onEnd = func(end card.WrapperEnd) {
			if end.RepoDir != wantRepo {
				t.Errorf("end.RepoDir %q, want the job's checkout %q", end.RepoDir, wantRepo)
			}
			if _, err := os.Stat(filepath.Join(end.RepoDir, ".git")); err != nil {
				t.Errorf("the checkout is gone at the end: %v", err)
			}
		}
		got := make(chan card.WrapperReport, 1)
		go func() { got <- card.RunWrapper(ctx, h.cfg, ledger) }()
		h.waitFor(ledger, "launched")
		h.waitFor(ledger, "beat")
		if err := os.WriteFile(gate, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		rep := h.report(got)
		if rep.Code != card.WrapperExitEnded || rep.Outcome != "DONE" {
			t.Fatalf("report %s why=%q; want DONE ended", rep.Line(), rep.Why)
		}
		if _, err := os.Stat(wantRepo); err == nil {
			t.Fatalf("the job dir %s is still there after the end", wantRepo)
		}
		rec, err := card.ReadEndRecord(h.results)
		if err != nil {
			t.Fatal(err)
		}
		repo := filepath.Join(h.results, "repo")
		if tip := gitIn(t, repo, "rev-parse", "refs/heads/"+card.WrapperBranch(id.Sprint, id.Label, 1)); rec.PushedSHA != tip {
			t.Fatalf("end.record pushed_sha %s, want the card branch commit %s in results/repo", rec.PushedSHA, tip)
		}
		if h := hashOf(t, ctx, client, id.Sprint, id.Label); h["pushed_sha"] != rec.PushedSHA {
			t.Fatalf("card hash pushed_sha %q, want %s", h["pushed_sha"], rec.PushedSHA)
		}
		if files := gitIn(t, repo, "show", "--name-only", "--format=", rec.PushedSHA); files != "work.txt" {
			t.Fatalf("committed %q, want work.txt only (notes.txt is scratch)", files)
		}
		line, _ := os.ReadFile(filepath.Join(h.results, "wrapper.line"))
		if !strings.Contains(string(line), `commit="COMMITTED"`) {
			t.Fatalf("wrapper.line %q lacks the commit note", line)
		}
	})
}

// newBaseOrigin is a bare origin whose dev holds one committed base.txt.
func newBaseOrigin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	origin := filepath.Join(dir, "origin.git")
	gitIn(t, dir, "init", "-q", "--bare", origin)
	seed := filepath.Join(dir, "seed")
	gitIn(t, dir, "init", "-q", seed)
	if err := os.WriteFile(filepath.Join(seed, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, seed, "add", "base.txt")
	gitIn(t, seed, "commit", "-q", "-m", "base")
	gitIn(t, seed, "push", "-q", origin, "HEAD:refs/heads/dev")
	gitIn(t, dir, "--git-dir", origin, "symbolic-ref", "HEAD", "refs/heads/dev")
	return origin
}

// TestWrapperCommitsNativeJobRepo is the quack-test defect of 2026-09-24 (space,
// nc-flash-0924): nova-swarm native ran the card in <slot>/jobs/<label>, its clone at
// <slot>/jobs/<label>/repo and its RESULT.md beside it, nothing under out, and the
// wrapper line said commit="NO-COMMIT" for a DONE card with a real fix. The fake
// harness reproduces that layout (a slot outside the wrapper's job, one committed base
// and one changed file). Without the hand-off the wrapper still ends NO-COMMIT (the
// defect); with the hand-off native now makes under NOVA_CARD_OUT, the wrapper commits
// the fix on the attempt branch, end.record and the card hash carry pushed_sha, and the
// wrapper line says COMMITTED with the card's RESULT line.
func TestWrapperCommitsNativeJobRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	for _, tc := range []struct {
		mode      string
		committed bool
	}{
		{"native-nohandoff", false},
		{"native", true},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			self, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			st, client := newSprint(t)
			id := card.Identity{Sprint: "flash-0924", Label: "quack-fix", BaseSHA: "0123abcd", Bench: "wrap-bench", Attempt: 1}
			token := attemptToken(1, strings.Repeat("d", 32))
			seedCard(t, ctx, client, id, "dealt", token)
			slot := filepath.Join(t.TempDir(), "nc-flash-0924-quack-fix-1")
			gate := filepath.Join(t.TempDir(), "gate")
			t.Setenv(fakeHarnessEnv, tc.mode)
			t.Setenv(fakeGateEnv, gate)
			t.Setenv(fakeOriginEnv, newBaseOrigin(t))
			t.Setenv(fakeSlotEnv, slot)
			h := newHarnessRun(t, id, self)
			ledger := &observed{inner: &card.RedisLedger{Store: st, Sprint: id.Sprint, Label: id.Label, Token: token}, events: make(chan string, 64)}
			got := make(chan card.WrapperReport, 1)
			go func() { got <- card.RunWrapper(ctx, h.cfg, ledger) }()
			h.waitFor(ledger, "launched")
			h.waitFor(ledger, "beat")
			if err := os.WriteFile(gate, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			rep := h.report(got)
			if rep.Code != card.WrapperExitEnded || rep.Outcome != "DONE" {
				t.Fatalf("report %s why=%q; want DONE ended", rep.Line(), rep.Why)
			}
			if _, err := os.Stat(filepath.Join(slot, "jobs", id.Label)); err != nil {
				t.Fatalf("the fake native job is not in the slot: %v", err)
			}
			rec, err := card.ReadEndRecord(h.results)
			if err != nil {
				t.Fatal(err)
			}
			line, _ := os.ReadFile(filepath.Join(h.results, "wrapper.line"))
			if !tc.committed {
				if rec.PushedSHA != card.NoCommit || !strings.Contains(string(line), `commit="NO-COMMIT"`) {
					t.Fatalf("without the hand-off: pushed_sha %q, wrapper.line %q; want the NO-COMMIT defect", rec.PushedSHA, line)
				}
				return
			}
			repo := filepath.Join(h.results, "repo")
			branch := card.WrapperBranch(id.Sprint, id.Label, 1)
			tip := gitIn(t, repo, "rev-parse", "refs/heads/"+branch)
			if len(rec.PushedSHA) != 40 || rec.PushedSHA != tip {
				t.Fatalf("end.record pushed_sha %q, want the %s commit %s", rec.PushedSHA, branch, tip)
			}
			if hs := hashOf(t, ctx, client, id.Sprint, id.Label); hs["pushed_sha"] != rec.PushedSHA {
				t.Fatalf("card hash pushed_sha %q, want %s", hs["pushed_sha"], rec.PushedSHA)
			}
			if files := gitIn(t, repo, "show", "--name-only", "--format=", rec.PushedSHA); files != "base.txt" {
				t.Fatalf("committed %q, want the changed base.txt only", files)
			}
			if parent := gitIn(t, repo, "log", "-1", "--format=%s", rec.PushedSHA+"^"); parent != "base" {
				t.Fatalf("the commit's parent is %q, want the committed base", parent)
			}
			want := "RESULT: " + id.Sprint + "/" + id.Label + "/1 sha=000000000000"
			if !strings.Contains(string(line), `commit="COMMITTED"`) || !strings.Contains(string(line), `result="`+want+`"`) {
				t.Fatalf("wrapper.line %q; want COMMITTED and result %q", line, want)
			}
			if subject := gitIn(t, repo, "log", "-1", "--format=%s", rec.PushedSHA); subject != want {
				t.Fatalf("commit message %q, want the RESULT line %q", subject, want)
			}
		})
	}
}
