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

func factsRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	writeMainFile(t, dir, "sign/sign.go", "package sign\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "base")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Rowan", "GIT_AUTHOR_EMAIL=rowan@example.com",
		"GIT_COMMITTER_NAME=Rowan", "GIT_COMMITTER_EMAIL=rowan@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func TestGateFactsVerbConflictExits2(t *testing.T) {
	dir := factsRepo(t)
	runGit(t, dir, "checkout", "-q", "-b", "card")
	writeMainFile(t, dir, "sign/sign.go", "package sign\n// card\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "card")
	runGit(t, dir, "checkout", "-q", "main")
	writeMainFile(t, dir, "sign/sign.go", "package sign\n// main\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "main")
	runGit(t, dir, "checkout", "-q", "card")
	rollup := writeMainFile(t, dir, "rollup.json", `{"ci-ok":"success"}`)

	var out, errb bytes.Buffer
	code := run([]string{"gate-facts", "--dir", dir, "--base", "main", "--head", "HEAD", "--rollup", rollup, "--paths", "sign/**"}, &out, &errb, time.Now().UTC())
	if code != 2 {
		t.Fatalf("conflict exit = %d, want 2; out=%q err=%q", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "merge-tree=conflict") {
		t.Fatalf("want merge-tree=conflict, got %q", out.String())
	}
}

func TestGateFactsVerbCleanExits0(t *testing.T) {
	dir := factsRepo(t)
	runGit(t, dir, "checkout", "-q", "-b", "card")
	writeMainFile(t, dir, "sign/sign.go", "package sign\nfunc Sign() {}\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "head")
	rollup := writeMainFile(t, dir, "rollup.json", `{"ci-ok":"success"}`)
	receipt := filepath.Join(dir, "receipt.txt")

	var out, errb bytes.Buffer
	code := run([]string{
		"gate-facts", "--dir", dir, "--base", "main", "--head", "HEAD",
		"--rollup", rollup, "--paths", "sign/**", "--receipt-file", receipt,
	}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("clean exit = %d, want 0; out=%q err=%q", code, out.String(), errb.String())
	}
	line := strings.TrimSpace(out.String())
	if !strings.Contains(line, "merge-tree=clean") || !strings.Contains(line, "ci-ok=success") || !strings.Contains(line, "paths=ok") {
		t.Fatalf("want a clean G1/G2/G3 receipt, got %q", line)
	}
	raw, err := os.ReadFile(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(raw)) != line {
		t.Fatalf("receipt file %q != stdout %q", strings.TrimSpace(string(raw)), line)
	}
}

func TestGateFactsVerbRefusesGuess(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"gate-facts"}, &out, &errb, time.Now().UTC()); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	got := errb.String()
	for _, flag := range []string{"--dir", "--base", "--head"} {
		if !strings.Contains(got, flag) {
			t.Fatalf("stderr = %q, want %s named", got, flag)
		}
	}
}

func TestHelpListsGateFacts(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, &out, &errb, time.Now().UTC()); code != 0 {
		t.Fatalf("help exit = %d", code)
	}
	found := false
	for _, line := range strings.Split(out.String(), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "nova-pulse" && f[1] == "gate-facts" {
			found = true
			if strings.Contains(line, "not yet implemented") {
				t.Errorf("gate-facts is shipped and help marks it unshipped: %q", line)
			}
		}
	}
	if !found {
		t.Fatal("help does not list gate-facts")
	}
}

func TestGateFactsVerbDoesNotCallGhWithRollup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the PATH gh fake is a shell script")
	}
	dir := factsRepo(t)
	called := filepath.Join(dir, "gh-called")
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho called > '" + called + "'\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	rollup := writeMainFile(t, dir, "rollup.json", `{"ci-ok":"success"}`)
	var out, errb bytes.Buffer
	code := run([]string{
		"gate-facts", "--dir", dir, "--base", "main", "--head", "HEAD",
		"--rollup", rollup, "--paths", "none", "--pr", "99", "--repo", "owner/name",
	}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("exit = %d out=%q err=%q", code, out.String(), errb.String())
	}
	if _, err := os.Stat(called); err == nil {
		t.Fatal("gh was invoked; --rollup must be the whole G1 source")
	}
}
