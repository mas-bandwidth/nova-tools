package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

func packetLab(t *testing.T) (lane, head string) {
	t.Helper()
	lane = t.TempDir()
	repo := filepath.Join(lane, merge.RepoDir)
	git := func(args ...string) string {
		c := exec.Command("git", args...)
		c.Dir = repo
		b, e := c.CombinedOutput()
		if e != nil {
			t.Fatalf("git %v: %v %s", args, e, b)
		}
		return strings.TrimSpace(string(b))
	}
	if e := os.MkdirAll(repo, 0o755); e != nil {
		t.Fatal(e)
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "test@example.invalid")
	git("config", "user.name", "test")
	if e := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("base\n"), 0o644); e != nil {
		t.Fatal(e)
	}
	git("add", "a.txt")
	git("commit", "-qm", "base")
	base := git("rev-parse", "HEAD")
	git("checkout", "-qb", "feature")
	if e := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("base\nchanged\n"), 0o644); e != nil {
		t.Fatal(e)
	}
	git("add", "a.txt")
	git("commit", "-qm", "change")
	head = git("rev-parse", "HEAD")
	st := &merge.State{Version: merge.Version, Repo: "test/repo", Base: base, LaneBranch: "lane", Branches: []*merge.Entry{{Branch: "feature", OID: head, NeedsRead: "yes"}}}
	if e := st.SaveTo(lane); e != nil {
		t.Fatal(e)
	}
	return lane, head
}

func TestPacketWritesTheSelectedDiffAndHonestBound(t *testing.T) {
	lane, head := packetLab(t)
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if e := os.Chdir(lane); e != nil {
		t.Fatal(e)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "emma", "--out", "packet.md", "--max-bytes", "4096"}, &out, &errb); code != 0 {
		t.Fatalf("packet exit=%d stderr=%s", code, errb.String())
	}
	body, e := os.ReadFile("packet.md")
	if e != nil {
		t.Fatal(e)
	}
	got := string(body)
	for _, want := range []string{"head=" + head, "base=", "@@", "+changed", "Prior verdicts\nunknown", "Open findings\nunknown"} {
		if !strings.Contains(got, want) {
			t.Errorf("packet lacks %q:\n%s", want, got)
		}
	}
	if !strings.Contains(out.String(), "files=1") || !strings.Contains(out.String(), "hunks=1") || !strings.Contains(out.String(), "cut=0") {
		t.Errorf("untruthful receipt %s", out.String())
	}
}

func TestPacketRefusesStaleHeadAndOverwrite(t *testing.T) {
	lane, _ := packetLab(t)
	old, _ := os.Getwd()
	defer os.Chdir(old)
	os.Chdir(lane)
	var out, errb bytes.Buffer
	if code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "emma", "--out", "p.md", "--head", strings.Repeat("a", 40)}, &out, &errb); code != 1 || !strings.Contains(errb.String(), "PACKET STALE") {
		t.Fatalf("stale code=%d stderr=%s", code, errb.String())
	}
	if e := os.WriteFile("p.md", []byte("keep"), 0o644); e != nil {
		t.Fatal(e)
	}
	out.Reset()
	errb.Reset()
	if code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "emma", "--out", "p.md"}, &out, &errb); code != 2 {
		t.Fatalf("overwrite code=%d", code)
	}
	b, _ := os.ReadFile("p.md")
	if string(b) != "keep" {
		t.Fatal("packet overwrote an existing artifact")
	}
}

func TestPacketReuseCopiesOnlyAnExactTuple(t *testing.T) {
	lane, _ := packetLab(t)
	old, _ := os.Getwd()
	defer os.Chdir(old)
	os.Chdir(lane)
	var out, errb bytes.Buffer
	args := []string{"packet", "--lane", lane, "--branch", "feature", "--who", "emma", "--out", "first.md"}
	if code := run(args, &out, &errb); code != 0 {
		t.Fatalf("first packet=%d %s", code, errb.String())
	}
	first, _ := os.ReadFile("first.md")
	out.Reset()
	errb.Reset()
	if code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "stella", "--out", "second.md", "--reuse", "first.md"}, &out, &errb); code != 0 {
		t.Fatalf("reuse=%d %s", code, errb.String())
	}
	second, _ := os.ReadFile("second.md")
	if !bytes.Equal(first, second) {
		t.Fatal("reuse changed the cached packet bytes")
	}
	if !strings.Contains(out.String(), "reused=true") {
		t.Fatalf("receipt=%s", out.String())
	}
	if code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "stella", "--out", "third.md", "--head", strings.Repeat("b", 40), "--reuse", "first.md"}, &out, &errb); code != 1 {
		t.Fatalf("mismatched head did not refuse stale: %d", code)
	}
}
