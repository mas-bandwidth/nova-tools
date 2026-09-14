package main

import (
	"bytes"
	"fmt"
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

func TestPacketFirstLineIsTypedTuple(t *testing.T) {
	lane, head := packetLab(t)
	old, _ := os.Getwd()
	defer os.Chdir(old)
	os.Chdir(lane)
	var out, errb bytes.Buffer
	if code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "emma", "--out", "tuple.md"}, &out, &errb); code != 0 {
		t.Fatalf("packet exit=%d stderr=%s", code, errb.String())
	}
	body, err := os.ReadFile("tuple.md")
	if err != nil {
		t.Fatal(err)
	}
	hdr, err := readPacketFirstLine("tuple.md")
	if err != nil {
		t.Fatalf("readPacketFirstLine: %v", err)
	}
	if hdr.Entry != "feature" {
		t.Errorf("entry=%q, want feature", hdr.Entry)
	}
	if hdr.Head != head {
		t.Errorf("head=%q, want %q", hdr.Head, head)
	}
	if hdr.Who != "emma" {
		t.Errorf("who=%q, want emma", hdr.Who)
	}
	if hdr.Cut != 0 {
		t.Errorf("cut=%d, want 0", hdr.Cut)
	}
	if hdr.Bytes != len(body) {
		t.Errorf("bytes=%d in header, want exact len(body)=%d", hdr.Bytes, len(body))
	}
	expectedID := packetID("feature", head, hdr.Base, hdr.Range)
	if hdr.ID != expectedID {
		t.Errorf("id=%q, want %q", hdr.ID, expectedID)
	}
}

func TestPacketReuseRejectsArbitraryLineScanning(t *testing.T) {
	lane, head := packetLab(t)
	old, _ := os.Getwd()
	defer os.Chdir(old)
	os.Chdir(lane)

	st, _ := merge.Load(lane)
	rng := merge.Short(st.Base) + ".." + merge.Short(head)
	pID := packetID("feature", head, st.Base, rng)

	// A file with arbitrary lines containing key=value fields in the body,
	// but lacking the typed first-line tuple header.
	bogus := fmt.Sprintf("# Note from another tool\n\nsome text\nid=%s\nentry=feature\nhead=%s\nbase=%s\n", pID, head, st.Base)
	if err := os.WriteFile("bogus.md", []byte(bogus), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "stella", "--out", "reused.md", "--reuse", "bogus.md"}, &out, &errb)
	if code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}
	if !strings.Contains(errb.String(), "PACKET REFUSED: --reuse file is not a valid packet") {
		t.Fatalf("expected refusal for invalid packet, got: %s", errb.String())
	}
}

func TestParsePacketFirstLineRejectsMalformed(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{"wrong prefix", "something else id=abc entry=f"},
		{"missing fields", "nova-review packet v1 id=123 entry=f"},
		{"negative bytes", "nova-review packet v1 id=123 entry=f head=h base=b range=r who=w built=s bytes=-5 cut=0"},
		{"negative cut", "nova-review packet v1 id=123 entry=f head=h base=b range=r who=w built=s bytes=10 cut=-1"},
		{"non-numeric bytes", "nova-review packet v1 id=123 entry=f head=h base=b range=r who=w built=s bytes=abc cut=0"},
		{"malformed token", "nova-review packet v1 id=123 entry=f notoken head=h base=b range=r who=w built=s bytes=10 cut=0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parsePacketFirstLine(tc.line)
			if err == nil {
				t.Errorf("expected error for %q", tc.line)
			}
		})
	}
}
