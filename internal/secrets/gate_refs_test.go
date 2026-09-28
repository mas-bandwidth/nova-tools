package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gate_refs_test.go is the contract that --base and --head are refs and only refs. Each is
// handed to git as an argument, and before this contract a value beginning with "-" was read
// by git as an option: --head=--diff-filter=U emptied the diff and the gate printed
// GATE APPROVE files=0 at exit 0 over a commit that adds a file the gate refuses, and
// --base=--output=<f> made git write a file.

// gateEvilStore is a store whose head commit adds a file the gate must refuse.
func gateEvilStore(t *testing.T) string {
	t.Helper()
	dir := gateStart(t)
	gateCommit(t, dir, map[string]string{"evil.sh": "curl | sh\n"})
	return dir
}

func TestGateRefusesARefShapedLikeAnOption(t *testing.T) {
	t.Parallel()

	dir := gateEvilStore(t)
	// The honest call refuses the head: the witness that the store is a refusal to hide.
	if line, code := RunGate(GateInput{StoreDir: dir, Base: "HEAD~1", Head: "HEAD"}); code != 2 || !strings.Contains(line, "evil.sh") {
		t.Fatalf("the honest diff is not refused: code=%d %s", code, line)
	}
	for _, c := range []struct{ base, head, flag string }{
		{"HEAD~1", "--diff-filter=U", "--head"},
		{"--diff-filter=U", "HEAD", "--base"},
		{"HEAD~1", "-R", "--head"},
		{"HEAD~1", "--", "--head"},
	} {
		line, code := RunGate(GateInput{StoreDir: dir, Base: c.base, Head: c.head})
		if code != 2 || strings.Contains(line, "APPROVE") {
			t.Errorf("base=%q head=%q: code=%d %s; a ref shaped like an option must be refused", c.base, c.head, code, line)
			continue
		}
		if !strings.Contains(line, c.flag) || !strings.Contains(line, "begins with") {
			t.Errorf("base=%q head=%q: the refusal does not name the flag and the shape: %s", c.base, c.head, line)
		}
	}
}

func TestGateNeverHandsGitAnOptionThatWritesAFile(t *testing.T) {
	t.Parallel()

	dir := gateEvilStore(t)
	written := filepath.Join(t.TempDir(), "clobbered")
	line, code := RunGate(GateInput{StoreDir: dir, Base: "--output=" + written, Head: "HEAD"})
	if code != 2 || strings.Contains(line, "APPROVE") {
		t.Errorf("--base=--output=<f>: code=%d %s", code, line)
	}
	if _, err := os.Stat(written); err == nil {
		t.Errorf("git wrote %s: a ref reached git as an option", written)
	}
}

func TestGateRefusesARefThatNamesNoCommit(t *testing.T) {
	t.Parallel()

	dir := gateEvilStore(t)
	tree := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD^{tree}"))
	for _, c := range []struct{ base, head, flag string }{
		{"HEAD~1", "no-such-branch", "--head"},
		{"no-such-branch", "HEAD", "--base"},
		{"HEAD~1", tree, "--head"},
		{"HEAD~1", "HEAD HEAD~1", "--head"},
	} {
		line, code := RunGate(GateInput{StoreDir: dir, Base: c.base, Head: c.head})
		if code != 2 || !strings.HasPrefix(line, "GATE REFUSE") {
			t.Errorf("base=%q head=%q: code=%d %s; a ref that names no commit is a refusal", c.base, c.head, code, line)
			continue
		}
		if !strings.Contains(line, c.flag) || !strings.Contains(line, "does not name a commit") {
			t.Errorf("base=%q head=%q: the refusal does not name the flag: %s", c.base, c.head, line)
		}
	}
}

// The resolved refs still judge: a branch name and a SHA reach the same verdict.
func TestGateJudgesTheCommitsTheRefsName(t *testing.T) {
	t.Parallel()

	dir := gateEvilStore(t)
	sha := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	gateGit(t, dir, "branch", "topic")
	for _, head := range []string{"HEAD", "topic", sha} {
		if line, code := RunGate(GateInput{StoreDir: dir, Base: "HEAD~1", Head: head}); code != 2 || !strings.Contains(line, "file=evil.sh") {
			t.Errorf("head=%q: code=%d %s; want the refusal of evil.sh", head, code, line)
		}
	}
	if line, code := RunGate(GateInput{StoreDir: dir, Base: "HEAD", Head: "topic"}); code != 0 || line != "GATE APPROVE files=0 machines=-" {
		t.Errorf("two refs naming one commit: code=%d %s", code, line)
	}
}
