package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func stageTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestStageUsesTheBenchMirrorAndTimesOut is the DONE-WHEN of #2882: a card stages from the
// bench mirror (a URL nothing can clone from still stages), a sha the mirror lacks is one
// fetch of that sha, a stage past --timeout ends the card RESULT: BLOCKED stage-timeout with
// a RESULT.md and a non-zero exit, and a bench with no mirror is refused, never cloned from
// the URL.
func TestStageUsesTheBenchMirrorAndTimesOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake git is a shell script")
	}
	root := t.TempDir()
	upstream := filepath.Join(root, "upstream")
	if err := os.MkdirAll(upstream, 0o755); err != nil {
		t.Fatal(err)
	}
	stageTestGit(t, upstream, "init", "-q", "-b", "dev")
	if err := os.WriteFile(filepath.Join(upstream, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stageTestGit(t, upstream, "add", "a.txt")
	stageTestGit(t, upstream, "commit", "-q", "-m", "one")
	inMirror := stageTestGit(t, upstream, "rev-parse", "HEAD")
	mirror := filepath.Join(root, "mirror", "repo.git")
	stageTestGit(t, root, "clone", "-q", "--mirror", upstream, mirror)
	if err := os.WriteFile(filepath.Join(upstream, "a.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stageTestGit(t, upstream, "commit", "-q", "-am", "two")
	notInMirror := stageTestGit(t, upstream, "rev-parse", "HEAD")

	stage := func(url, sha, mirror, name, timeout string) (int, string, string, time.Duration) {
		var out, errb bytes.Buffer
		t0 := time.Now()
		code := run([]string{"stage", "--url", url, "--sha", sha, "--mirror", mirror,
			"--dest", filepath.Join(root, "jobs", name, "repo"), "--job", filepath.Join(root, "jobs", name),
			"--bench", "hulk", "--label", name, "--timeout", timeout}, nil, &out, &errb, time.Now())
		return code, out.String(), errb.String(), time.Since(t0)
	}

	// 1. The URL is one nothing can clone from, so a stage that went to it would fail: the
	// mirror carries the sha, and the checkout is at it with origin pointed at the URL.
	github := filepath.Join(root, "no-such-github", "repo.git")
	code, out, errs, _ := stage(github, inMirror, mirror, "c1", "30s")
	if code != 0 || !strings.Contains(out, "STAGE OK c1 on hulk") || !strings.Contains(out, "source=mirror ") {
		t.Fatalf("a sha the mirror carries stages from the mirror alone: code=%d\n%s%s", code, out, errs)
	}
	repo := filepath.Join(root, "jobs", "c1", "repo")
	if got := stageTestGit(t, repo, "rev-parse", "HEAD"); got != inMirror {
		t.Fatalf("staged HEAD %s, want %s", got, inMirror)
	}
	if got := stageTestGit(t, repo, "remote", "get-url", "origin"); got != github {
		t.Fatalf("origin is %s, want the card's URL %s", got, github)
	}

	// 2. A sha the mirror lacks is one fetch of that sha from the card's URL.
	code, out, errs, _ = stage(upstream, notInMirror, mirror, "c2", "30s")
	if code != 0 || !strings.Contains(out, "source=mirror+fetch") {
		t.Fatalf("a sha the mirror lacks is fetched on top of the mirror: code=%d\n%s%s", code, out, errs)
	}

	// 3. A fetch that hangs past --timeout ends the card BLOCKED stage-timeout, promptly,
	// with the end record written and a non-zero exit.
	fake := filepath.Join(root, "fakegit")
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nfor a in \"$@\"; do [ \"$a\" = fetch ] && exec sleep 30; done\nexec " + real + " \"$@\"\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	stageGit = fake
	t.Cleanup(func() { stageGit = "git" })
	code, out, errs, took := stage(upstream, notInMirror, mirror, "c3", "1s")
	if code == 0 || !strings.Contains(out, "RESULT: BLOCKED stage-timeout hulk ") {
		t.Fatalf("a stage past its timeout ends the card BLOCKED stage-timeout, non-zero: code=%d\n%s%s", code, out, errs)
	}
	if took > 10*time.Second {
		t.Fatalf("the timeout was 1s but the stage took %s", took)
	}
	rec, err := os.ReadFile(filepath.Join(root, "jobs", "c3", "RESULT.md"))
	if err != nil || !strings.HasPrefix(string(rec), "RESULT: BLOCKED stage-timeout hulk ") {
		t.Fatalf("the timed-out card's RESULT.md is its end record: err=%v\n%s", err, rec)
	}
	stageGit = "git"

	// 4. No mirror on the bench: refused, and nothing is cloned from the URL.
	code, out, _, _ = stage(upstream, inMirror, filepath.Join(root, "mirror", "absent.git"), "c4", "30s")
	if code == 0 || !strings.Contains(out, "STAGE REFUSED c4 on hulk: no bench mirror") {
		t.Fatalf("a bench with no mirror is refused: code=%d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(root, "jobs", "c4", "repo")); err == nil {
		t.Fatal("a refused stage wrote a checkout")
	}

	// 5. --timeout is required.
	var o, e bytes.Buffer
	if c := run([]string{"stage", "--url", github, "--sha", inMirror, "--mirror", mirror, "--dest", filepath.Join(root, "x"),
		"--job", root, "--bench", "hulk", "--label", "c5"}, nil, &o, &e, time.Now()); c != 2 || !strings.Contains(e.String(), "--timeout is required") {
		t.Fatalf("a stage with no --timeout is refused: code=%d\n%s", c, e.String())
	}
}
