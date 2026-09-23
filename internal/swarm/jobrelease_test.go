package swarm

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func releaseLayout(t *testing.T) (root, slot, job, tmp, results string) {
	t.Helper()
	root = t.TempDir()
	slot = filepath.Join(root, "slot-1")
	job = filepath.Join(slot, "jobs", "card")
	tmp = filepath.Join(slot, "tmp", "card")
	results = filepath.Join(root, "results", "card")
	for _, d := range []string{job, tmp, filepath.Join(root, "cache", "go-mod"), filepath.Join(root, "tmp", "cache", "npm")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "cache", "go-mod", "keep"), []byte("mod\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "scratch"), []byte("tmp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, slot, job, tmp, results
}

func releaseInput(root, slot, job, tmp, results, home string) ReleaseJobInput {
	return ReleaseJobInput{
		JobDir: job, TmpDir: tmp, SlotDir: slot, Root: root,
		ResultsDir: results, Label: "card", BenchHome: home,
		NativeLog: filepath.Join(slot, "native.log"),
	}
}

// A card that published and committed is removed only after the bundle lists that
// commit. The shared cache, which is not the job, stays.
func TestReleaseJobDirRemovesTheCloneAfterTheBundleListsHEAD(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	root, slot, job, tmp, results := releaseLayout(t)
	home := t.TempDir()
	mirror := filepath.Join(home, "nova-bench", "mirror")
	if err := os.MkdirAll(mirror, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mirror, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(job, "repo")
	sha := gitCommitOnBranch(t, repo, "rowan/card", "base", "work")
	body := "RESULT card sha=" + sha + "\nDONE\nBRANCH rowan/card\n"
	if err := os.WriteFile(filepath.Join(job, "RESULT.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(job, "usage.tsv"), []byte("job\tcard\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(job, "note"), []byte("a note\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slot, "native.log"), []byte("native\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := ReleaseJobDir(releaseInput(root, slot, job, tmp, results, home))
	if err != nil {
		t.Fatal(err)
	}
	if !out.Removed || out.Head != sha {
		t.Fatalf("removed=%v head=%s, want the commit removed", out.Removed, out.Head)
	}
	if _, err := os.Stat(job); !os.IsNotExist(err) {
		t.Fatalf("job directory still present: %v", err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("sandbox tmp still present: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "cache", "go-mod", "keep")); err != nil {
		t.Fatalf("shared module cache was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(mirror, "HEAD")); err != nil {
		t.Fatalf("bench mirror was removed: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(results, "RESULT.md"))
	if err != nil || string(got) != body {
		t.Fatalf("RESULT.md in the results store = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(results, "usage.tsv")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(results, "note")); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(results, "native.log")); err != nil || string(raw) != "native\n" {
		t.Fatalf("native.log = %q, %v", raw, err)
	}
	heads, err := exec.Command("git", "bundle", "list-heads", out.Bundle).CombinedOutput()
	if err != nil {
		t.Fatalf("list-heads: %v\n%s", err, heads)
	}
	if !headsContain(string(heads), sha) {
		t.Fatalf("bundle does not list HEAD %s:\n%s", sha, heads)
	}
	if !strings.Contains(string(heads), "refs/heads/rowan/card") {
		t.Fatalf("bundle does not name the branch:\n%s", heads)
	}
}

// RESULT.md with no BRANCH line still names a commit the base does not have.
// git bundle create refuses a range of raw SHAs that names no ref, and that
// refusal used to keep the only copy in the job directory.
func TestReleaseJobDirBundlesACommitWhenResultNamesNoBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	root, slot, job, tmp, results := releaseLayout(t)
	repo := filepath.Join(job, "repo")
	sha := gitCommitOnBranch(t, repo, "rowan/card", "base", "work")
	body := "RESULT card sha=" + sha + "\nDONE\n"
	if err := os.WriteFile(filepath.Join(job, "RESULT.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := ReleaseJobDir(releaseInput(root, slot, job, tmp, results, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if !out.Removed || out.Head != sha {
		t.Fatalf("removed=%v head=%s, want the commit removed", out.Removed, out.Head)
	}
	if _, err := os.Stat(job); !os.IsNotExist(err) {
		t.Fatalf("job directory still present: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(results, "RESULT.md"))
	if err != nil || string(got) != body {
		t.Fatalf("RESULT.md in the results store = %q, %v", got, err)
	}
	heads, err := exec.Command("git", "bundle", "list-heads", out.Bundle).CombinedOutput()
	if err != nil {
		t.Fatalf("list-heads: %v\n%s", err, heads)
	}
	if !headsContain(string(heads), sha) {
		t.Fatalf("bundle does not list HEAD %s:\n%s", sha, heads)
	}
	if !strings.Contains(string(heads), " HEAD") {
		t.Fatalf("bundle does not name HEAD:\n%s", heads)
	}
}

// The loss that opened the hole: RESULT.md names a branch, and that ref is not in
// the clone and not in a bundle. The directory stays, clone and all.
func TestReleaseJobDirKeepsABranchThatResolvesNowhere(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	root, slot, job, tmp, results := releaseLayout(t)
	repo := filepath.Join(job, "repo")
	sha := gitCommitOnBranch(t, repo, "rowan/real", "base", "work")
	if err := os.WriteFile(filepath.Join(job, "RESULT.md"), []byte("RESULT card sha="+sha+"\nBRANCH rowan/missing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "only-copy"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ReleaseJobDir(releaseInput(root, slot, job, tmp, results, t.TempDir()))
	if err == nil {
		t.Fatal("a BRANCH that is not in the clone was removed")
	}
	if _, statErr := os.Stat(filepath.Join(repo, "only-copy")); statErr != nil {
		t.Fatalf("the clone was removed with an unresolved branch: %v (release: %v)", statErr, err)
	}
}

// A bundle that does not actually list HEAD is not a reason to remove the clone.
func TestReleaseJobDirKeepsTheCloneWhenTheBundleDoesNotListHEAD(t *testing.T) {
	root, slot, job, tmp, results := releaseLayout(t)
	repo := filepath.Join(job, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "only-copy"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := releaseInput(root, slot, job, tmp, results, t.TempDir())
	in.git = bundleMissesHEAD{}

	_, err := ReleaseJobDir(in)
	if err == nil {
		t.Fatal("a bundle that does not list HEAD was treated as verified")
	}
	if _, statErr := os.Stat(filepath.Join(repo, "only-copy")); statErr != nil {
		t.Fatalf("the clone was removed: %v", statErr)
	}
}

// A harness-written result with no commit has nothing to bundle. The directory
// goes at once, and RESULT.md and the harness log are what remain.
func TestReleaseJobDirRemovesAHarnessResultWithNoCommit(t *testing.T) {
	root, slot, job, tmp, results := releaseLayout(t)
	body := "RESULT: BLOCKED card\nwritten-by: nova-swarm native\n"
	if err := os.WriteFile(filepath.Join(job, "RESULT.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(job, "harness-output.log"), []byte("log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := ReleaseJobDir(releaseInput(root, slot, job, tmp, results, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if !out.Removed {
		t.Fatal("a harness result with no commit was kept")
	}
	if _, err := os.Stat(job); !os.IsNotExist(err) {
		t.Fatalf("job directory still present: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(results, "RESULT.md"))
	if err != nil || string(got) != body {
		t.Fatalf("RESULT.md = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(results, "harness-output.log")); err != nil {
		t.Fatal(err)
	}
}

// The harness died before RESULT.md. The logs are kept and the directory goes,
// because this process is the one that watched it die.
func TestReleaseJobDirRemovesASilentExitAfterCopyingLogs(t *testing.T) {
	root, slot, job, tmp, results := releaseLayout(t)
	if err := os.WriteFile(filepath.Join(job, "usage.tsv"), []byte("row\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slot, "native.log"), []byte("died\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := ReleaseJobDir(releaseInput(root, slot, job, tmp, results, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if !out.Removed {
		t.Fatal("a silent exit was kept")
	}
	if raw, err := os.ReadFile(filepath.Join(results, "native.log")); err != nil || string(raw) != "died\n" {
		t.Fatalf("native.log = %q, %v", raw, err)
	}
	if _, err := os.Stat(filepath.Join(results, "usage.tsv")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(job); !os.IsNotExist(err) {
		t.Fatalf("job directory still present: %v", err)
	}
}

func TestReleaseJobDirRefusesASharedCacheAndAMirror(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	cache := filepath.Join(root, "cache")
	mirror := filepath.Join(home, "nova-bench", "mirror")
	if err := os.MkdirAll(filepath.Join(cache, "go-mod"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(mirror, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "go-mod", "keep"), []byte("mod\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mirror, "HEAD"), []byte("ref\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReleaseJobDir(ReleaseJobInput{
		JobDir: cache, TmpDir: filepath.Join(root, "tmp", "cache"), SlotDir: root, Root: root,
		ResultsDir: filepath.Join(root, "results", "card"), Label: "card", BenchHome: home,
	})
	if err == nil {
		t.Fatal("the shared cache was an acceptable job directory")
	}
	if _, statErr := os.Stat(filepath.Join(cache, "go-mod", "keep")); statErr != nil {
		t.Fatalf("cache sentinel gone: %v", statErr)
	}
	_, err = ReleaseJobDir(ReleaseJobInput{
		JobDir: mirror, SlotDir: filepath.Join(home, "nova-bench"), Root: filepath.Join(home, "nova-bench"),
		ResultsDir: filepath.Join(home, "nova-bench", "results", "card"), Label: "card", BenchHome: home,
	})
	if err == nil {
		t.Fatal("the bench mirror was an acceptable job directory")
	}
	if _, statErr := os.Stat(filepath.Join(mirror, "HEAD")); statErr != nil {
		t.Fatalf("mirror sentinel gone: %v", statErr)
	}
}

// results/<label> is a symlink. The lexical path is under the results root and
// outside the job, which is the check ReleaseJobDir used to trust. One alias
// points at <job>/saved, so the copy lands inside the job and removing the job
// deletes the only durable copy. The other points outside the results root.
// Both must keep the job and the evidence, and neither may be written through.
func TestReleaseJobDirKeepsJobEvidenceWhenResultsDirIsAnAlias(t *testing.T) {
	t.Run("into the job", func(t *testing.T) {
		root, slot, job, tmp, results := releaseLayout(t)
		saved := filepath.Join(job, "saved")
		if err := os.MkdirAll(saved, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "RESULT: BLOCKED card\nwritten-by: nova-swarm native\n"
		if err := os.WriteFile(filepath.Join(job, "RESULT.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		const only = "only-copy\n"
		if err := os.WriteFile(filepath.Join(saved, "only-copy"), []byte(only), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(results), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(saved, results); err != nil {
			t.Skipf("symlink: %v", err)
		}

		_, err := ReleaseJobDir(releaseInput(root, slot, job, tmp, results, t.TempDir()))
		if err == nil {
			t.Fatal("a results directory that aliases the job was released")
		}
		got, readErr := os.ReadFile(filepath.Join(job, "RESULT.md"))
		if readErr != nil || string(got) != body {
			t.Fatalf("job RESULT.md = %q, %v (release: %v)", got, readErr, err)
		}
		got, readErr = os.ReadFile(filepath.Join(saved, "only-copy"))
		if readErr != nil || string(got) != only {
			t.Fatalf("aliased evidence = %q, %v (release: %v)", got, readErr, err)
		}
		if _, statErr := os.Stat(filepath.Join(saved, "release-manifest.txt")); !os.IsNotExist(statErr) {
			t.Fatalf("release wrote through the results alias: %v", statErr)
		}
	})

	t.Run("outside the results root", func(t *testing.T) {
		root, slot, job, tmp, results := releaseLayout(t)
		outside := filepath.Join(root, "elsewhere")
		if err := os.MkdirAll(outside, 0o755); err != nil {
			t.Fatal(err)
		}
		const keep = "keep-the-job\n"
		if err := os.WriteFile(filepath.Join(job, "keep"), []byte(keep), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(results), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, results); err != nil {
			t.Skipf("symlink: %v", err)
		}

		_, err := ReleaseJobDir(releaseInput(root, slot, job, tmp, results, t.TempDir()))
		if err == nil {
			t.Fatal("a results directory that resolves outside the results root was released")
		}
		got, readErr := os.ReadFile(filepath.Join(job, "keep"))
		if readErr != nil || string(got) != keep {
			t.Fatalf("job evidence = %q, %v (release: %v)", got, readErr, err)
		}
		if _, statErr := os.Stat(filepath.Join(outside, "release-manifest.txt")); !os.IsNotExist(statErr) {
			t.Fatalf("release wrote outside the results root: %v", statErr)
		}
		if _, statErr := os.Stat(filepath.Join(outside, "keep")); !os.IsNotExist(statErr) {
			t.Fatalf("release copied the job through the alias: %v", statErr)
		}
	})
}

// results/<label> aliases another directory already under results/. The target
// is inside the results root and outside the job, which is the check that used
// to accept it. The copy then replaces that directory's files, including the
// manifest. The other card's results have to stay byte for byte, and the job
// has to stay too.
func TestReleaseJobDirKeepsPeerResultsWhenResultsDirAliasesAnother(t *testing.T) {
	root, slot, job, tmp, results := releaseLayout(t)
	prior := filepath.Join(root, "results", "prior")
	if err := os.MkdirAll(prior, 0o755); err != nil {
		t.Fatal(err)
	}
	const (
		priorResult   = "prior-result\n"
		priorManifest = "prior-manifest\n"
		priorOnly     = "prior-only\n"
		jobResult     = "RESULT: BLOCKED card\nwritten-by: nova-swarm native\n"
		jobOnly       = "job-only\n"
	)
	if err := os.WriteFile(filepath.Join(prior, "RESULT.md"), []byte(priorResult), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prior, "release-manifest.txt"), []byte(priorManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prior, "untouched"), []byte(priorOnly), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(job, "RESULT.md"), []byte(jobResult), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(job, "keep"), []byte(jobOnly), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("prior", results); err != nil {
		t.Skipf("symlink: %v", err)
	}

	_, err := ReleaseJobDir(releaseInput(root, slot, job, tmp, results, t.TempDir()))
	if err == nil || !strings.Contains(err.Error(), "resolves to") {
		t.Fatalf("a results directory that aliases another results directory: %v", err)
	}
	got, readErr := os.ReadFile(filepath.Join(job, "RESULT.md"))
	if readErr != nil || string(got) != jobResult {
		t.Fatalf("job RESULT.md = %q, %v (release: %v)", got, readErr, err)
	}
	got, readErr = os.ReadFile(filepath.Join(job, "keep"))
	if readErr != nil || string(got) != jobOnly {
		t.Fatalf("job evidence = %q, %v (release: %v)", got, readErr, err)
	}
	got, readErr = os.ReadFile(filepath.Join(prior, "RESULT.md"))
	if readErr != nil || string(got) != priorResult {
		t.Fatalf("peer RESULT.md = %q, %v (release: %v)", got, readErr, err)
	}
	got, readErr = os.ReadFile(filepath.Join(prior, "release-manifest.txt"))
	if readErr != nil || string(got) != priorManifest {
		t.Fatalf("peer manifest = %q, %v (release: %v)", got, readErr, err)
	}
	got, readErr = os.ReadFile(filepath.Join(prior, "untouched"))
	if readErr != nil || string(got) != priorOnly {
		t.Fatalf("peer sentinel = %q, %v (release: %v)", got, readErr, err)
	}
	if _, statErr := os.Stat(filepath.Join(prior, "keep")); !os.IsNotExist(statErr) {
		t.Fatalf("release copied the job onto the peer results: %v", statErr)
	}
}

func TestReleaseJobDirDoesNotFollowASymlink(t *testing.T) {
	root, slot, job, tmp, results := releaseLayout(t)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "keep"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(job); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, job); err != nil {
		t.Skipf("symlink: %v", err)
	}
	_, err := ReleaseJobDir(releaseInput(root, slot, job, tmp, results, t.TempDir()))
	if err == nil {
		t.Fatal("a symlinked job directory was removed")
	}
	if _, statErr := os.Stat(filepath.Join(outside, "keep")); statErr != nil {
		t.Fatalf("the symlink target was removed: %v", statErr)
	}
}

type bundleMissesHEAD struct{}

func (bundleMissesHEAD) state(string) (gitState, error) {
	return gitState{SHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Unique: true}, nil
}
func (bundleMissesHEAD) rev(string, string) (string, error) {
	return "", errors.New("no ref")
}
func (bundleMissesHEAD) ancestor(string, string, string) (bool, error) {
	return false, nil
}
func (bundleMissesHEAD) bundle(string, string, string, string, string) (string, error) {
	return "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb HEAD\n", nil
}

func gitCommitOnBranch(t *testing.T, repo, branch, baseMsg, workMsg string) string {
	t.Helper()
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=rowan", "GIT_AUTHOR_EMAIL=rowan@example.com",
			"GIT_COMMITTER_NAME=rowan", "GIT_COMMITTER_EMAIL=rowan@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "--quiet", "--initial-branch=main")
	run("commit", "--quiet", "--allow-empty", "-m", baseMsg)
	run("update-ref", "refs/remotes/origin/main", "HEAD")
	run("checkout", "--quiet", "-b", branch)
	run("commit", "--quiet", "--allow-empty", "-m", workMsg)
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}
