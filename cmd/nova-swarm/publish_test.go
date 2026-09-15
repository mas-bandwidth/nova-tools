package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// PUBLISH (slice 7, lesson 6). Each of these tests drives the verb against a real local git
// working tree and a real bare remote, with a stub `gh` on PATH, so the push by refspec and
// the draft PR are proved end to end without a network or a GitHub account. Nothing reaches
// outside t.TempDir().

// runGit runs git in dir ("" means the process cwd is irrelevant to --git-dir/-C use) and
// fails the test when git cannot run.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	var cmd *exec.Cmd
	if dir != "" {
		cmd = exec.Command("git", append([]string{"-C", dir}, args...)...)
	} else {
		cmd = exec.Command("git", args...)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, errb.String())
	}
	return out.String()
}

// makeGitRepo builds a working clone on main pushed to a local bare remote, so origin/main
// exists in the clone the way the publish verb expects it. It returns the clone, the bare
// remote and the sha main was on before any topic commit.
func makeGitRepo(t *testing.T) (job, bare, mainSHA string) {
	t.Helper()
	dir := t.TempDir()
	bare = filepath.Join(dir, "remote.git")
	job = filepath.Join(dir, "job")
	runGit(t, "", "init", "--bare", bare)
	runGit(t, "", "init", "-b", "main", job)
	runGit(t, job, "config", "user.name", "test")
	runGit(t, job, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(job, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, job, "add", ".")
	runGit(t, job, "commit", "-m", "base commit")
	runGit(t, job, "remote", "add", "origin", bare)
	runGit(t, job, "push", "-u", "origin", "main")
	mainSHA = strings.TrimSpace(runGit(t, job, "rev-parse", "main"))
	return job, bare, mainSHA
}

// stubGh places a stub `gh` on PATH that answers `pr create --draft` with one URL and
// records its argv, so a test can also assert the draft flag reached the CLI.
func stubGh(t *testing.T, url string) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "gh.argv")
	body := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" >> %q\necho %q\n", log, url)
	script := filepath.Join(dir, "gh")
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func publishRun(t *testing.T, args ...string) (exit int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	exit = run(append([]string{"publish"}, args...), strings.NewReader(""), &out, &errb, time.Now().UTC())
	return exit, out.String(), errb.String()
}

// TestPublishRefusesMain: a clone still on main has nothing that is safely a topic branch,
// so the verb refuses before any push.
func TestPublishRefusesMain(t *testing.T) {
	job, _, _ := makeGitRepo(t)
	stubGh(t, "https://example.com/pr/1")
	exit, _, stderr := publishRun(t, "--job", job, "--branch", "topic", "--base", "main",
		"--title", "a title", "--body-file", filepath.Join(t.TempDir(), "body.md"))
	if exit != 2 {
		t.Fatalf("a clone on main exits 2, got %d:\n%s", exit, stderr)
	}
	if !strings.Contains(stderr, "PUBLISH REFUSED") {
		t.Fatalf("the refusal is one PUBLISH REFUSED line:\n%s", stderr)
	}
}

// TestPublishRefusesUntouchedFile: a topic branch that touches a file --touched does not
// admit is refused before the push, so a change nobody admitted never reaches an inbox.
func TestPublishRefusesUntouchedFile(t *testing.T) {
	job, _, _ := makeGitRepo(t)
	runGit(t, job, "checkout", "-b", "topic")
	if err := os.WriteFile(filepath.Join(job, "stray.txt"), []byte("stray\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, job, "add", ".")
	runGit(t, job, "commit", "-m", "touches a file nobody admitted")
	stubGh(t, "https://example.com/pr/2")
	body := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(body, []byte("body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exit, _, stderr := publishRun(t, "--job", job, "--branch", "topic", "--base", "main",
		"--title", "a title", "--body-file", body, "--touched", "base.txt")
	if exit != 2 {
		t.Fatalf("a diff touching an unadmitted file exits 2, got %d:\n%s", exit, stderr)
	}
	if !strings.Contains(stderr, "PUBLISH REFUSED") || !strings.Contains(stderr, "stray.txt") {
		t.Fatalf("the refusal names the stray file:\n%s", stderr)
	}
}

// TestPublishPushesByRefspec: a topic branch with one admitted commit is pushed by the
// explicit refspec, the remote branch now exists, and main did not move.
func TestPublishPushesByRefspec(t *testing.T) {
	job, bare, mainSHA := makeGitRepo(t)
	runGit(t, job, "checkout", "-b", "topic")
	if err := os.WriteFile(filepath.Join(job, "allowed.txt"), []byte("allowed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, job, "add", ".")
	runGit(t, job, "commit", "-m", "one admitted commit")
	stubGh(t, "https://example.com/pr/3")
	body := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(body, []byte("body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exit, stdout, stderr := publishRun(t, "--job", job, "--branch", "topic", "--base", "main",
		"--title", "a title", "--body-file", body, "--touched", "allowed.txt")
	if exit != 0 {
		t.Fatalf("a publishable topic branch exits 0, got %d:\n%s%s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "PUBLISH OK") {
		t.Fatalf("stdout wants one PUBLISH OK line:\n%s", stdout)
	}
	// The remote branch exists, and it is the topic branch's new sha.
	remoteHead := strings.TrimSpace(runGit(t, "", "--git-dir", bare, "rev-parse", "--verify", "refs/heads/topic"))
	if remoteHead == "" {
		t.Fatalf("the remote branch refs/heads/topic does not exist after the push")
	}
	wantHead := strings.TrimSpace(runGit(t, job, "rev-parse", "HEAD"))
	if remoteHead != wantHead {
		t.Errorf("the remote branch points at %s, want HEAD %s", remoteHead, wantHead)
	}
	// main did not move.
	remoteMain := strings.TrimSpace(runGit(t, "", "--git-dir", bare, "rev-parse", "--verify", "refs/heads/main"))
	if remoteMain != mainSHA {
		t.Errorf("main moved from %s to %s; the push is by refspec and must not move it", mainSHA, remoteMain)
	}
}

// TestPublishPrintsPRURL: the PUBLISH OK line carries the draft PR URL the stub gh printed.
func TestPublishPrintsPRURL(t *testing.T) {
	job, _, _ := makeGitRepo(t)
	runGit(t, job, "checkout", "-b", "topic")
	if err := os.WriteFile(filepath.Join(job, "allowed.txt"), []byte("allowed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, job, "add", ".")
	runGit(t, job, "commit", "-m", "one admitted commit")
	argvLog := stubGh(t, "https://example.com/pr/42")
	body := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(body, []byte("body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exit, stdout, stderr := publishRun(t, "--job", job, "--branch", "topic", "--base", "main",
		"--title", "a title", "--body-file", body, "--touched", "allowed.txt")
	if exit != 0 {
		t.Fatalf("exit %d:\n%s%s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "pr=https://example.com/pr/42") {
		t.Fatalf("the PUBLISH OK line carries the PR url:\n%s", stdout)
	}
	raw, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "--draft") {
		t.Errorf("gh was not asked for a draft PR (argv %q)", string(raw))
	}
}
