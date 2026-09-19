package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The verb's own surface: the one HYGIENE line, the cap-and-count listing, and the
// exit codes SPEC.md's Conventions give -- 0 clean, 1 findings, 2 could not run.
// SPEC-TOOLWORK.md §3 rule 7 (PR #1637), issue #1647.

func hygGit(t *testing.T, dir string, args ...string) {
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

func hygWrite(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func hygLab(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	hygGit(t, dir, "init", "-q", "-b", "main")
	hygWrite(t, dir, "sign/sign.go", "package sign\n")
	hygGit(t, dir, "add", "-A")
	hygGit(t, dir, "commit", "-q", "-m", "base")
	hygGit(t, dir, "checkout", "-q", "-b", "card")
	return dir
}

func TestHygieneVerbPassesACleanBranch(t *testing.T) {
	dir := hygLab(t)
	hygWrite(t, dir, "sign/sign.go", "package sign\n\nfunc F() {}\n")
	hygGit(t, dir, "add", "-A")
	hygGit(t, dir, "commit", "-q", "-m", "clean")
	var out, errb bytes.Buffer
	code := run([]string{"hygiene", "--repo", dir, "--base", "main", "--head", "HEAD", "--identity", "Rowan <rowan@example.com>", "--paths", "sign/**"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d, want 0\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "HYGIENE OK base=main head=HEAD paths=sign/** findings=0") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestHygieneVerbExitsOneAndNamesTheFinding(t *testing.T) {
	dir := hygLab(t)
	hygWrite(t, dir, "sign/RESULT.md", "the worker's own report\n")
	hygGit(t, dir, "add", "-A")
	hygGit(t, dir, "commit", "-q", "-m", "ship the report")
	var out, errb bytes.Buffer
	code := run([]string{"hygiene", "--repo", dir, "--base", "main", "--head", "HEAD", "--identity", "Rowan <rowan@example.com>", "--paths", "sign/**"}, &out, &errb)
	if code != 1 {
		t.Fatalf("exit %d, want 1\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "HYGIENE FINDING reason=stray-file at=sign/RESULT.md") {
		t.Fatalf("stdout = %q, want the finding named", out.String())
	}
	if !strings.Contains(errb.String(), "HYGIENE NO ") || !strings.Contains(errb.String(), "findings=1") {
		t.Fatalf("stderr = %q, want the NO verdict line", errb.String())
	}
}

// A branch with no declared paths says paths=- and skips out-of-path. The field is
// PRINTED rather than omitted: a line that left it out would read as a bound that held.
func TestHygieneVerbSaysPathsDashWhenUnbounded(t *testing.T) {
	dir := hygLab(t)
	hygWrite(t, dir, "elsewhere/x.go", "package elsewhere\n")
	hygGit(t, dir, "add", "-A")
	hygGit(t, dir, "commit", "-q", "-m", "a friend's own branch")
	var out, errb bytes.Buffer
	code := run([]string{"hygiene", "--repo", dir, "--base", "main", "--head", "HEAD", "--identity", "Rowan <rowan@example.com>"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d, want 0\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "paths=- findings=0") {
		t.Fatalf("stdout = %q, want paths=-", out.String())
	}
}

// There is no default identity. A range checked against nobody would admit anybody, so
// the missing flag is a refusal and never a fallback to the repository's own config.
func TestHygieneVerbRefusesWithoutAnIdentity(t *testing.T) {
	dir := hygLab(t)
	for _, args := range [][]string{
		{"hygiene", "--repo", dir, "--base", "main", "--head", "HEAD"},
		{"hygiene", "--repo", dir, "--base", "main", "--head", "HEAD", "--identity", "Rowan"},
		{"hygiene", "--base", "main", "--head", "HEAD", "--identity", "Rowan <r@e.example>"},
		{"hygiene", "--repo", dir, "--base", "main", "--head", "HEAD", "--identity", "Rowan <r@e.example>", "--paths", "../out/**"},
		{"hygiene", "--repo", dir, "--base", "main", "--head", "HEAD", "--identity", "Rowan <r@e.example>", "--paths", "**"},
	} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != 2 {
			t.Fatalf("%v: exit %d, want 2 (stdout %q stderr %q)", args, code, out.String(), errb.String())
		}
	}
}

func TestHygieneUsageNamesTheVerb(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "nova-check hygiene --repo <dir> --base <ref> --head <ref>") {
		t.Fatalf("the help does not carry the hygiene line:\n%s", out.String())
	}
}
