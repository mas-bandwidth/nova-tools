package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/sprintci"
)

// TestCIPassIsACardUnderSlotAccounting is the #2842 acceptance test at the
// verb. A pass outside the dealer's share does not run. A bench with no
// dealt slot does not start. ci cut writes the script card into the front
// tier, the bench runs that script from the mirror, and the end writes
// ci:<repo>:<sha>.
func TestCIPassIsACardUnderSlotAccounting(t *testing.T) {
	t.Parallel()
	checkShares(t)

	mirror, sha := ciFixture(t)
	if len(sha) != 40 {
		t.Fatalf("fixture sha %q", sha)
	}
	repo := "example/nova"
	const pr = 2842
	front := filepath.Join(t.TempDir(), "front")
	card := cutCI(t, pr, repo, sha, "./pkg", mirror, front)
	body := string(mustRead(t, card))
	name := fmt.Sprintf("ci-%d-%s.md", pr, sha[:8])
	if filepath.Base(card) != name {
		t.Fatalf("card name %s, want %s", filepath.Base(card), name)
	}
	for _, want := range []string{
		"KIND: script\n",
		"MODEL CALLS: 0\n",
		"PATHS: ./pkg\n",
		"zero model calls",
		"does not clone from GitHub",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("card missing %q\n%s", want, body)
		}
	}
	for _, banned := range []string{"opencode", "claude", "ollama", "curl "} {
		if strings.Contains(body, banned) {
			t.Fatalf("a script card calls a model via %q", banned)
		}
	}
	read, err := sprintci.Read(card)
	if err != nil {
		t.Fatal(err)
	}
	script := read.Script
	if !strings.Contains(script, `git clone --quiet -- "${MIRROR}"`) {
		t.Fatal("the script does not check out the mirror")
	}
	for _, line := range strings.Split(script, "\n") {
		if strings.Contains(line, "git clone") && strings.Contains(line, "github.com") {
			t.Fatalf("the script clones from GitHub: %s", line)
		}
	}
	for _, want := range []string{"gofmt -l", "go vet", "go test -p 4"} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q", want)
		}
	}
	refuseGitHub(t, script, sha)

	again := cutCI(t, pr, repo, sha, "./pkg", mirror, front)
	if again != card {
		t.Fatalf("second cut wrote %s, want the same card %s", again, card)
	}

	mr := miniredis.RunT(t)
	key, err := sprintci.VerdictKey(repo, sha)
	if err != nil {
		t.Fatal(err)
	}
	d, err := sprintci.New(8)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	idleRoot := t.TempDir()
	idle := sprintci.Bench{Name: "vision", Dealer: d, Redis: mr.Addr(), Root: idleRoot}
	res, err := idle.Run(ctx, card)
	if err != nil {
		t.Fatal(err)
	}
	if res.Started || res.Reason != sprintci.NoDealtSlot {
		t.Fatalf("a CI run started on a bench that has no dealt slot: started=%v reason=%q", res.Started, res.Reason)
	}
	if entries, _ := os.ReadDir(idleRoot); len(entries) != 0 {
		t.Fatalf("a bench with no dealt slot wrote %d entries", len(entries))
	}
	if mr.Exists(key) {
		t.Fatal("the end wrote Redis without a run")
	}

	head := sprintci.Card{Repo: repo, PR: pr, SHA: sha}
	if ok, reason := d.Deal("hulk", head); !ok {
		t.Fatalf("deal hulk: %s", reason)
	}
	res, err = idle.Run(ctx, card)
	if err != nil {
		t.Fatal(err)
	}
	if res.Started || res.Reason != sprintci.NoDealtSlot {
		t.Fatal("a CI run started on a bench that has no dealt slot")
	}
	if d.CIHeld() != 1 {
		t.Fatalf("CI held %d, want the one dealt head", d.CIHeld())
	}

	if ok, reason := d.Deal("vision", head); ok {
		t.Fatal("vision took a slot already dealt to hulk")
	} else if !strings.Contains(reason, "hulk") {
		t.Fatalf("deal vision: %s", reason)
	}

	runDealer, err := sprintci.New(8)
	if err != nil {
		t.Fatal(err)
	}
	if ok, reason := runDealer.Deal("vision", head); !ok {
		t.Fatalf("deal vision: %s", reason)
	}
	if ok, reason := runDealer.Deal("vision", head); !ok {
		t.Fatalf("same head again: %s", reason)
	}
	if runDealer.CIHeld() != 1 {
		t.Fatalf("a rerun took a second slot: CI held %d", runDealer.CIHeld())
	}
	root := t.TempDir()
	bench := sprintci.Bench{Name: "vision", Dealer: runDealer, Redis: mr.Addr(), Root: root}
	res, err = bench.Run(ctx, card)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Started || res.Value != "OK" || res.Key != key {
		t.Fatalf("OK run: %+v", res)
	}
	if got, err := mr.Get(key); err != nil || got != "OK" {
		t.Fatalf("redis %s = %q, %v; want OK", key, got, err)
	}

	failFront := filepath.Join(t.TempDir(), "front")
	failCard := cutCI(t, pr, repo, sha, "./bad", mirror, failFront)
	res, err = bench.Run(ctx, failCard)
	if err != nil {
		t.Fatal(err)
	}
	const wantFail = "FAIL example.com/ci/bad TestBad"
	if !res.Started || res.Value != wantFail {
		t.Fatalf("FAIL run: %+v", res)
	}
	if got, err := mr.Get(key); err != nil || got != wantFail {
		t.Fatalf("redis %s = %q, %v; want %s", key, got, err, wantFail)
	}
	if runDealer.CIHeld() != 1 {
		t.Fatalf("the failing rerun took a second slot: CI held %d", runDealer.CIHeld())
	}
}

func checkShares(t *testing.T) {
	t.Helper()
	const width = 8
	d, err := sprintci.New(width)
	if err != nil {
		t.Fatal(err)
	}
	if d.CIShare() != width/2 || d.ModelReserve() != width-width/2 {
		t.Fatalf("share=%d reserve=%d", d.CIShare(), d.ModelReserve())
	}
	for i := 0; i < 9; i++ {
		c := sprintci.Card{Repo: "example/nova", PR: 2800 + i, SHA: sha40(i)}
		ran, reason := d.RunCI(c)
		if i < d.CIShare() {
			if !ran {
				t.Fatalf("card inside the share did not run: %s", reason)
			}
			continue
		}
		if ran || reason != sprintci.OutsideShares {
			t.Fatalf("a CI pass ran outside the dealer's shares: ran=%v reason=%q", ran, reason)
		}
	}
	if d.CIHeld() != width/2 {
		t.Fatalf("CI held %d, want %d", d.CIHeld(), width/2)
	}
	again := sprintci.Card{Repo: "example/nova", PR: 2800, SHA: sha40(0)}
	if ran, reason := d.RunCI(again); !ran {
		t.Fatalf("the same head is the same card: %s", reason)
	}
	if d.CIHeld() != width/2 {
		t.Fatalf("a rerun took a second slot: CI held %d", d.CIHeld())
	}
	if ran, _ := d.RunCI(sprintci.Card{Repo: "example/nova", PR: 1, SHA: "not-a-head"}); ran {
		t.Fatal("a CI pass ran outside the dealer's shares")
	}
	open, err := sprintci.New(4)
	if err != nil {
		t.Fatal(err)
	}
	c := sprintci.Card{Repo: "example/nova", PR: 2842, SHA: sha40(3)}
	if ran, _ := open.Start("vision", c); ran {
		t.Fatal("a CI run started on a bench that has no dealt slot")
	}
}

func cutCI(t *testing.T, pr int, repo, sha, paths, mirror, front string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"ci", "cut",
		"--pr", strconv.Itoa(pr),
		"--repo", repo,
		"--sha", sha,
		"--paths", paths,
		"--mirror", mirror,
		"--front", front,
	}, &stdout, &stderr, time.Now().UTC())
	if code != 0 {
		t.Fatalf("ci cut exit %d\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}
	name := fmt.Sprintf("ci-%d-%s.md", pr, sha[:8])
	line := strings.TrimSpace(stdout.String())
	if !strings.Contains(line, "card="+name) || !strings.Contains(line, "front="+front) {
		t.Fatalf("stdout %q", line)
	}
	path := filepath.Join(front, name)
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	return path
}

func refuseGitHub(t *testing.T, script, sha string) {
	t.Helper()
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "run.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(dir, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	result := filepath.Join(dir, "RESULT.md")
	cmd := exec.Command("/bin/sh", scriptPath)
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"MIRROR=https://github.com/mas-bandwidth/nova-tools.git",
		"SHA=" + sha,
		"WORK=" + work,
		"RESULT=" + result,
		"PATHS=./pkg",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("the script cloned from GitHub\n%s", out)
	}
	if _, statErr := os.Stat(filepath.Join(work, "src")); !os.IsNotExist(statErr) {
		t.Fatal("the script checked out a GitHub clone")
	}
	got := string(mustRead(t, result))
	if strings.Contains(got, "RESULT: OK") {
		t.Fatalf("github refusal wrote %q", got)
	}
}

func ciFixture(t *testing.T) (mirror, sha string) {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join(root, "src")
	mirror = filepath.Join(root, "mirror.git")
	writeFile(t, filepath.Join(src, "go.mod"), "module example.com/ci\n\ngo 1.26\n")
	writeFile(t, filepath.Join(src, "pkg", "pass_test.go"), "package pkg\n\nimport \"testing\"\n\nfunc TestPass(t *testing.T) {}\n")
	writeFile(t, filepath.Join(src, "bad", "bad_test.go"), "package bad\n\nimport \"testing\"\n\nfunc TestBad(t *testing.T) {\n\tt.Fatal(\"no\")\n}\n")
	gofmtDir(t, src)
	git(t, src, "init", "-b", "main")
	git(t, src, "add", ".")
	git(t, src, "-c", "user.name=ci", "-c", "user.email=ci@example.com", "commit", "-m", "fixture")
	sha = strings.TrimSpace(gitOut(t, src, "rev-parse", "HEAD"))
	git(t, root, "clone", "--bare", src, mirror)
	return mirror, sha
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func gofmtDir(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("gofmt", "-w", "pkg/pass_test.go", "bad/bad_test.go")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gofmt: %v\n%s", err, out)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	_ = gitOut(t, dir, args...)
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=ci",
		"GIT_AUTHOR_EMAIL=ci@example.com",
		"GIT_COMMITTER_NAME=ci",
		"GIT_COMMITTER_EMAIL=ci@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func sha40(n int) string {
	return fmt.Sprintf("%08x%032d", n, 0)
}
