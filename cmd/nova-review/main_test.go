package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

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
	for _, want := range []string{"head=" + head, "base=", "@@", "+changed", "All verdicts at earlier heads\nnone recorded", "Open findings (answer with", "the author says:"} {
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

func TestWriteExclusiveCannotReplaceExistingOrSymlinkDestination(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "packet.md")
	if err := os.WriteFile(dest, []byte("first publisher"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeExclusive(dest, []byte("second publisher")); err == nil {
		t.Fatal("exclusive publication replaced an existing packet")
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != "first publisher" {
		t.Fatalf("existing packet changed: %q err=%v", got, err)
	}
	target := filepath.Join(dir, "target.md")
	if err := os.WriteFile(target, []byte("symlink target"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "symlink.md")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := writeExclusive(link, []byte("must not follow")); err == nil {
		t.Fatal("exclusive publication followed an existing destination symlink")
	}
	got, err = os.ReadFile(target)
	if err != nil || string(got) != "symlink target" {
		t.Fatalf("symlink target changed: %q err=%v", got, err)
	}
}

func TestWriteExclusiveConcurrentPublishersLeaveOneImmutablePacket(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "packet.md")
	bodies := [][]byte{[]byte("publisher A"), []byte("publisher B")}
	start := make(chan struct{})
	errs := make(chan error, len(bodies))
	for _, body := range bodies {
		body := body
		go func() {
			<-start
			errs <- writeExclusive(dest, body)
		}()
	}
	close(start)
	successes := 0
	for range bodies {
		if err := <-errs; err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent publishers succeeded %d times, want exactly one", successes)
	}
	got, err := os.ReadFile(dest)
	if err != nil || (string(got) != string(bodies[0]) && string(got) != string(bodies[1])) {
		t.Fatalf("published packet is not either complete candidate: %q err=%v", got, err)
	}
}

type packetRefusingWriter struct{}

func (packetRefusingWriter) Write([]byte) (int, error) { return 0, fmt.Errorf("broken packet stdout") }

func TestPacketReportsOutputFailureAfterImmutablePublication(t *testing.T) {
	lane, _ := packetLab(t)
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	if err := os.Chdir(lane); err != nil {
		t.Fatal(err)
	}
	var errb bytes.Buffer
	args := []string{"packet", "--lane", lane, "--branch", "feature", "--who", "emma", "--out", "published.md"}
	if code := run(args, packetRefusingWriter{}, &errb); code != 1 || !strings.Contains(errb.String(), "PACKET FAIL output") {
		t.Fatalf("published artifact stdout failure was hidden: code=%d stderr=%s", code, errb.String())
	}
	published, err := os.ReadFile("published.md")
	if err != nil || len(published) == 0 {
		t.Fatalf("output failure did not leave recovery artifact: bytes=%d err=%v", len(published), err)
	}
	var out bytes.Buffer
	errb.Reset()
	if code := run(args, &out, &errb); code != 2 {
		t.Fatalf("retry replaced published artifact: code=%d stderr=%s", code, errb.String())
	}
	after, err := os.ReadFile("published.md")
	if err != nil || string(after) != string(published) {
		t.Fatalf("retry changed published artifact: err=%v", err)
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
	hdrFirst, err := parsePacketFirstLine(strings.Split(string(first), "\n")[0])
	if err != nil {
		t.Fatal(err)
	}
	hdrSecond, err := parsePacketFirstLine(strings.Split(string(second), "\n")[0])
	if err != nil {
		t.Fatal(err)
	}
	if hdrFirst.ID != hdrSecond.ID {
		t.Errorf("reuse changed id: first=%s second=%s", hdrFirst.ID, hdrSecond.ID)
	}
	if hdrFirst.Who != "emma" || hdrSecond.Who != "stella" {
		t.Errorf("who failed: first=%s second=%s", hdrFirst.Who, hdrSecond.Who)
	}
	if hdrSecond.Built != hdrFirst.Built {
		t.Errorf("reuse changed the source evidence timestamp: first=%s second=%s", hdrFirst.Built, hdrSecond.Built)
	}
	contentFirst := string(first)
	contentSecond := string(second)
	mAllVerdicts := "## All verdicts at earlier heads\n"
	mThisHead := "## This head\n"
	mYourPrior := "## Your prior verdicts on this entry\n"
	idxAVFirst := strings.Index(contentFirst, mAllVerdicts)
	idxAVSecond := strings.Index(contentSecond, mAllVerdicts)
	if contentFirst[idxAVFirst:] != contentSecond[idxAVSecond:] {
		t.Errorf("reader-independent sections differ:\nfirst:\n%s\nsecond:\n%s", contentFirst[idxAVFirst:], contentSecond[idxAVSecond:])
	}
	idxTHFirst := strings.Index(contentFirst, mThisHead)
	idxTHSecond := strings.Index(contentSecond, mThisHead)
	idxYPFirst := strings.Index(contentFirst, mYourPrior)
	idxYPSecond := strings.Index(contentSecond, mYourPrior)
	if contentFirst[idxTHFirst:idxYPFirst] != contentSecond[idxTHSecond:idxYPSecond] {
		t.Errorf("this head section differs")
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

func packetLabTwoHeads(t *testing.T) (lane, base, h1, h2 string) {
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
	git("commit", "-qm", "base commit")
	base = git("rev-parse", "HEAD")

	git("checkout", "-qb", "feature")
	if e := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("base\nstep1\n"), 0o644); e != nil {
		t.Fatal(e)
	}
	git("add", "a.txt")
	git("commit", "-qm", "step 1")
	h1 = git("rev-parse", "HEAD")

	if e := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("base\nstep1\nstep2\n"), 0o644); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(repo, "b.txt"), []byte("second file\n"), 0o644); e != nil {
		t.Fatal(e)
	}
	git("add", "a.txt", "b.txt")
	git("commit", "-m", "feature head\n\nFix the network timeout instruction.")
	h2 = git("rev-parse", "HEAD")

	st := &merge.State{
		Version:    merge.Version,
		Repo:       "test/repo",
		Base:       base,
		LaneBranch: "lane",
		Branches: []*merge.Entry{
			{
				Branch:    "feature",
				OID:       h2,
				NeedsRead: "yes",
				Reads: []merge.Read{
					{Who: "emma", Verdict: "approve", Head: h1, At: "2026-09-12T10:00:00Z"},
				},
			},
		},
	}
	if e := st.SaveTo(lane); e != nil {
		t.Fatal(e)
	}
	return lane, base, h1, h2
}

func TestThePacketIsTheDelta(t *testing.T) {
	lane, base, h1, h2 := packetLabTwoHeads(t)
	old, _ := os.Getwd()
	defer os.Chdir(old)
	os.Chdir(lane)

	var out, errb bytes.Buffer
	// 1. emma read at H1, now at H2 -> range H1..H2
	if code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "emma", "--out", "emma.md"}, &out, &errb); code != 0 {
		t.Fatalf("emma packet code=%d err=%s", code, errb.String())
	}
	emmaBody, _ := os.ReadFile("emma.md")
	emmaText := string(emmaBody)
	wantEmmaRange := merge.Short(h1) + ".." + merge.Short(h2)
	if !strings.Contains(emmaText, "range="+wantEmmaRange) {
		t.Errorf("emma packet lacks range=%s:\n%s", wantEmmaRange, emmaText)
	}
	if !strings.Contains(emmaText, "head="+h2) {
		t.Errorf("emma packet lacks head=%s", h2)
	}

	// 2. stella has never read it -> range <base>...H2
	out.Reset()
	errb.Reset()
	if code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "stella", "--out", "stella.md"}, &out, &errb); code != 0 {
		t.Fatalf("stella packet code=%d err=%s", code, errb.String())
	}
	stellaBody, _ := os.ReadFile("stella.md")
	stellaText := string(stellaBody)
	wantStellaRange := merge.Short(base) + "..." + merge.Short(h2)
	if !strings.Contains(stellaText, "range="+wantStellaRange) {
		t.Errorf("stella packet lacks range=%s:\n%s", wantStellaRange, stellaText)
	}
	if !strings.Contains(stellaText, "none; this packet is the whole change, "+wantStellaRange) {
		t.Errorf("stella packet lacks whole change note:\n%s", stellaText)
	}

	// Author intent
	if !strings.Contains(stellaText, "the author says:\n> Fix the network timeout instruction.") {
		t.Errorf("stella packet lacks author intent:\n%s", stellaText)
	}

	// 3. emma_abstain only abstained -> same range as stella
	out.Reset()
	errb.Reset()
	if code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "emma_abstain", "--out", "abstain.md"}, &out, &errb); code != 0 {
		t.Fatalf("abstain packet code=%d err=%s", code, errb.String())
	}
	abstainBody, _ := os.ReadFile("abstain.md")
	if !strings.Contains(string(abstainBody), "range="+wantStellaRange) {
		t.Errorf("abstain packet has range %s, want %s", string(abstainBody), wantStellaRange)
	}

	// 4. Invalid --max-bytes
	for _, bad := range []string{"0", "-1"} {
		out.Reset()
		errb.Reset()
		if code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "emma", "--out", "bad.md", "--max-bytes", bad}, &out, &errb); code != 2 {
			t.Errorf("max-bytes=%s got code=%d, want 2", bad, code)
		}
	}
	// too small to hold metadata
	out.Reset()
	errb.Reset()
	if code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "emma", "--out", "bad.md", "--max-bytes", "100"}, &out, &errb); code != 2 {
		t.Errorf("max-bytes=100 got code=%d, want 2", code)
	}
}

func TestThePacketIsTheDelta_OnePacketServesEveryReaderInRange(t *testing.T) {
	lane, _, _, _ := packetLabTwoHeads(t)
	old, _ := os.Getwd()
	defer os.Chdir(old)
	os.Chdir(lane)

	var out, errb bytes.Buffer
	// stella builds first
	if code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "stella", "--out", "stella.md"}, &out, &errb); code != 0 {
		t.Fatalf("stella code=%d %s", code, errb.String())
	}
	stellaBytes, _ := os.ReadFile("stella.md")

	// johnny reuses stella.md (both have no prior reads, same range base...h2)
	out.Reset()
	errb.Reset()
	if code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "johnny", "--out", "johnny.md", "--reuse", "stella.md"}, &out, &errb); code != 0 {
		t.Fatalf("johnny reuse code=%d %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "reused=true") {
		t.Fatalf("expected reused=true, got: %s", out.String())
	}
	johnnyBytes, _ := os.ReadFile("johnny.md")

	hdrStella, _ := parsePacketFirstLine(strings.Split(string(stellaBytes), "\n")[0])
	hdrJohnny, _ := parsePacketFirstLine(strings.Split(string(johnnyBytes), "\n")[0])
	if hdrStella.ID != hdrJohnny.ID {
		t.Fatalf("ids differ: stella=%s johnny=%s", hdrStella.ID, hdrJohnny.ID)
	}
	if hdrStella.Who != "stella" || hdrJohnny.Who != "johnny" {
		t.Fatalf("who not preserved: stella=%s johnny=%s", hdrStella.Who, hdrJohnny.Who)
	}

	// Verify six reader-independent sections are byte-for-byte identical
	sContent := string(stellaBytes)
	jContent := string(johnnyBytes)
	mAllVerdicts := "## All verdicts at earlier heads\n"
	idxAV_S := strings.Index(sContent, mAllVerdicts)
	idxAV_J := strings.Index(jContent, mAllVerdicts)
	if sContent[idxAV_S:] != jContent[idxAV_J:] {
		t.Fatalf("reader-independent sections from all-verdicts differ")
	}
	mThisHead := "## This head\n"
	mYourPrior := "## Your prior verdicts on this entry\n"
	idxTH_S := strings.Index(sContent, mThisHead)
	idxTH_J := strings.Index(jContent, mThisHead)
	idxYP_S := strings.Index(sContent, mYourPrior)
	idxYP_J := strings.Index(jContent, mYourPrior)
	if sContent[idxTH_S:idxYP_S] != jContent[idxTH_J:idxYP_J] {
		t.Fatalf("this-head sections differ")
	}

	// Mismatched reuse: emma (has prior read, range h1..h2) attempts to reuse stella.md (range base...h2)
	out.Reset()
	errb.Reset()
	if code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "emma", "--out", "emma_reuse.md", "--reuse", "stella.md"}, &out, &errb); code != 2 {
		t.Fatalf("mismatched range reuse code=%d, want 2", code)
	}
	if !strings.Contains(errb.String(), "PACKET REUSE asked=") || !strings.Contains(errb.String(), "found=") {
		t.Fatalf("expected PACKET REUSE asked=... found=..., got: %s", errb.String())
	}
	if _, err := os.Stat("emma_reuse.md"); err == nil {
		t.Fatalf("mismatched reuse wrote an output file")
	}

	// Reuse with forbidden flags
	out.Reset()
	errb.Reset()
	if code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "johnny", "--out", "j2.md", "--reuse", "stella.md", "--max-bytes", "65536"}, &out, &errb); code != 2 {
		t.Fatalf("reuse with max-bytes code=%d, want 2", code)
	}
}

func TestThePacketIsTheDelta_StaleAndCurrentArePrinted(t *testing.T) {
	lane, _, _, h2 := packetLabTwoHeads(t)
	old, _ := os.Getwd()
	defer os.Chdir(old)
	os.Chdir(lane)

	var out, errb bytes.Buffer
	staleSHA := strings.Repeat("c", 40)
	if code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "emma", "--out", "stale.md", "--head", staleSHA}, &out, &errb); code != 1 {
		t.Fatalf("expected code 1, got %d", code)
	}
	if !strings.Contains(errb.String(), "PACKET STALE entry=feature asked="+merge.Short(staleSHA)+" current="+merge.Short(h2)) {
		t.Fatalf("unexpected stale err:\n%s", errb.String())
	}
	if _, err := os.Stat("stale.md"); err == nil {
		t.Fatalf("stale run created output file")
	}

	// Without --head, writes current head
	out.Reset()
	errb.Reset()
	if code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "emma", "--out", "current.md"}, &out, &errb); code != 0 {
		t.Fatalf("code=%d %s", code, errb.String())
	}
	b, _ := os.ReadFile("current.md")
	hdr, _ := parsePacketFirstLine(strings.Split(string(b), "\n")[0])
	if hdr.Head != h2 {
		t.Fatalf("head=%s, want %s", hdr.Head, h2)
	}
}

func TestPerFileDiffOmissionAndRemedy(t *testing.T) {
	lane, _, _, _ := packetLabTwoHeads(t)
	old, _ := os.Getwd()
	defer os.Chdir(old)
	os.Chdir(lane)

	var out, errb bytes.Buffer
	if code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "stella", "--out", "full.md"}, &out, &errb); code != 0 {
		t.Fatal(errb.String())
	}
	fullBytes, _ := os.ReadFile("full.md")
	budget := len(fullBytes) - 80
	out.Reset()
	errb.Reset()
	if code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "stella", "--out", "cut.md", "--max-bytes", fmt.Sprint(budget)}, &out, &errb); code != 0 {
		t.Fatalf("cut run code=%d %s", code, errb.String())
	}
	cutBody, _ := os.ReadFile("cut.md")
	cutText := string(cutBody)
	hdr, err := parsePacketFirstLine(strings.Split(cutText, "\n")[0])
	if err != nil {
		t.Fatal(err)
	}
	if hdr.Cut <= 0 {
		t.Fatalf("expected cut > 0, got %d", hdr.Cut)
	}
	if len(cutBody) > budget {
		t.Fatalf("cut packet length %d exceeds budget %d", len(cutBody), budget)
	}
	if !strings.Contains(cutText, "## Not included") || !strings.Contains(cutText, "; print with: git diff") {
		t.Fatalf("lacks remedy in Not included:\n%s", cutText)
	}
	if !strings.Contains(out.String(), fmt.Sprintf("cut=%d", hdr.Cut)) {
		t.Fatalf("output receipt lacks cut count: %s", out.String())
	}
}

func TestMaxFlagTruncatesEarlierVerdicts(t *testing.T) {
	lane, _, _, _ := packetLabTwoHeads(t)
	old, _ := os.Getwd()
	defer os.Chdir(old)
	os.Chdir(lane)

	st, _ := merge.Load(lane)
	entry := st.Find("feature")
	entry.Reads = append(entry.Reads, merge.Read{Who: "johnny", Verdict: "hold", Head: "1111222233334444555566667777888899990000", At: "2026-09-12T11:00:00Z"})
	_ = st.SaveTo(lane)

	var out, errb bytes.Buffer
	if code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "stella", "--out", "max.md", "--max", "1"}, &out, &errb); code != 0 {
		t.Fatalf("max run code=%d %s", code, errb.String())
	}
	b, _ := os.ReadFile("max.md")
	text := string(b)
	if !strings.Contains(text, "1 more of 2; print with: nova-review roster --lane") {
		t.Fatalf("lacks roster continuation line in All verdicts:\n%s", text)
	}
}

func validPacketHeaderForTest() packetHeader {
	base := strings.Repeat("a", 40)
	head := strings.Repeat("b", 40)
	rng := merge.Short(base) + "..." + merge.Short(head)
	return packetHeader{
		ID: packetID("feature", head, base, rng), Entry: "feature", Head: head, Base: base,
		Range: rng, Who: "ada", Built: "2026-09-14T00:00:00Z", Bytes: 100, Cut: 0,
	}
}

func TestPacketHeaderRejectsDuplicateUnknownAndInvalidTupleFields(t *testing.T) {
	h := validPacketHeaderForTest()
	cases := []struct {
		name string
		line string
	}{
		{"duplicate", h.String() + " head=" + h.Head},
		{"unknown", h.String() + " extra=field"},
		{"upper sha", strings.Replace(h.String(), "head="+h.Head, "head="+strings.ToUpper(h.Head), 1)},
		{"mismatched range", strings.Replace(h.String(), "range="+h.Range, "range="+merge.Short(h.Base)+".."+merge.Short(h.Head), 1)},
		{"non-UTC timestamp", strings.Replace(h.String(), "built=2026-09-14T00:00:00Z", "built=2026-09-14T01:00:00+01:00", 1)},
		{"wrong id", strings.Replace(h.String(), "id="+h.ID, "id="+strings.Repeat("c", 12), 1)},
		{"noncanonical bytes", strings.Replace(h.String(), "bytes=100", "bytes=0100", 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parsePacketFirstLine(tc.line); err == nil {
				t.Fatalf("accepted malformed header: %s", tc.line)
			}
		})
	}
}

func TestReadPacketFirstLineRejectsOverlongUnterminatedHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "packet.md")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", packetHeaderLimit+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPacketFirstLine(path); err == nil {
		t.Fatal("accepted an unterminated overlong packet header")
	}
}

func TestPacketHeaderRoundTripsTokenCharactersAndRejectsOverflow(t *testing.T) {
	h := validPacketHeaderForTest()
	h.Entry = "feature=one"
	h.Who = "Ada Vale=two"
	h.ID = packetID(h.Entry, h.Head, h.Base, h.Range)
	got, err := parsePacketFirstLine(h.String())
	if err != nil {
		t.Fatal(err)
	}
	if got.Entry != h.Entry || got.Who != h.Who {
		t.Fatalf("token fields did not round trip: %#v", got)
	}
	if _, err := parsePacketFirstLine(strings.Replace(h.String(), "bytes=100", "bytes=18446744073709551615", 1)); err == nil {
		t.Fatal("accepted bytes outside int range")
	}
}

func TestPacketReuseReadsOneBoundedVerifiedRegularArtifact(t *testing.T) {
	lane, _ := packetLab(t)
	old, _ := os.Getwd()
	defer os.Chdir(old)
	os.Chdir(lane)
	var out, errb bytes.Buffer
	if code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "emma", "--out", "first.md"}, &out, &errb); code != 0 {
		t.Fatalf("first packet=%d %s", code, errb.String())
	}
	first, err := os.ReadFile("first.md")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("tampered.md", append(first, 'x'), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	if code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "stella", "--out", "bad.md", "--reuse", "tampered.md"}, &out, &errb); code != 2 || !strings.Contains(errb.String(), "does not match") {
		t.Fatalf("tampered reuse=%d %s", code, errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "stella", "--out", "dir.md", "--reuse", "."}, &out, &errb); code != 2 || !strings.Contains(errb.String(), "regular file") {
		t.Fatalf("directory reuse=%d %s", code, errb.String())
	}
}

func TestPacketCommandReuseRoundTripsNontrivialEntryAndReader(t *testing.T) {
	lane, _ := packetLab(t)
	repo := filepath.Join(lane, merge.RepoDir)
	git := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = repo
		if b, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, b)
		}
	}
	git("branch", "feature=one", "feature")
	st, err := merge.Load(lane)
	if err != nil {
		t.Fatal(err)
	}
	st.Find("feature").Branch = "feature=one"
	if err := st.SaveTo(lane); err != nil {
		t.Fatal(err)
	}
	old, _ := os.Getwd()
	defer os.Chdir(old)
	os.Chdir(lane)
	var out, errb bytes.Buffer
	who := "Ada Vale=two"
	if code := run([]string{"packet", "--lane", lane, "--branch", "feature=one", "--who", who, "--out", "first.md"}, &out, &errb); code != 0 {
		t.Fatalf("first packet=%d %s", code, errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := run([]string{"packet", "--lane", lane, "--branch", "feature=one", "--who", "next=reader", "--out", "second.md", "--reuse", "first.md"}, &out, &errb); code != 0 {
		t.Fatalf("reuse=%d %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "reused=true") {
		t.Fatalf("reuse receipt=%s", out.String())
	}
}

func TestPacketReuseRejectsNamedPipeBeforeOpen(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "packet.fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPacketReuseNamedPipeHelper$")
	cmd.Env = append(os.Environ(), "NOVA_REVIEW_REUSE_FIFO="+fifo)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("FIFO refusal hung or failed: %v %s", err, b)
	}
}

func TestPacketReuseNamedPipeHelper(t *testing.T) {
	fifo := os.Getenv("NOVA_REVIEW_REUSE_FIFO")
	if fifo == "" {
		return
	}
	if _, _, err := readReusePacket(fifo); err == nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestPacketReuseAdmitsOriginalLargeBoundAndRejectsExplicitBoundFlag(t *testing.T) {
	lane, head := packetLab(t)
	old, _ := os.Getwd()
	defer os.Chdir(old)
	os.Chdir(lane)
	st, err := merge.Load(lane)
	if err != nil {
		t.Fatal(err)
	}
	rng := merge.Short(st.Base) + "..." + merge.Short(head)
	rest := "## This head\n" + quotedPacketData(strings.Repeat("source data ", 15000)) + "\n" +
		formatYourPriorVerdicts("emma", nil, st.Base, head, rng) + "\n" +
		"## All verdicts at earlier heads\nnone recorded\n\n" +
		"## Open findings (answer with `dup <id>` if you see the same thing)\nnone recorded\n\n" +
		"## Rules touched\nNo changed files.\n\n" +
		"## Diff " + rng + "\n```diff\n\n```\n\n## Not included\nnothing\n"
	h := packetHeader{Entry: "feature", Head: head, Base: st.Base, Range: rng, Who: "emma", Built: "2026-09-14T00:00:00Z"}
	h.ID = packetID(h.Entry, h.Head, h.Base, h.Range)
	large := formatPacket(h, rest)
	if len(large) <= defaultPacketMaxBytes {
		t.Fatalf("fixture is not larger than the default bound: %d", len(large))
	}
	if err := os.WriteFile("large.md", []byte(large), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "jane", "--out", "reused.md", "--reuse", "large.md"}, &out, &errb); code != 0 {
		t.Fatalf("large reuse=%d %s", code, errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "stella", "--out", "flagged.md", "--reuse", "large.md", "--max-bytes", "131072"}, &out, &errb); code != 2 {
		t.Fatalf("explicit default max-bytes reuse=%d %s", code, errb.String())
	}
}

func packetFakeCommand(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-command")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func withPacketSourceBinaries(t *testing.T, git, gh string) {
	t.Helper()
	oldGit, oldGH := packetGitBinary, packetGHBinary
	packetGitBinary, packetGHBinary = git, gh
	t.Cleanup(func() { packetGitBinary, packetGHBinary = oldGit, oldGH })
}

func TestPacketSourceCommandsUseSuccessFailureAndTimeoutBudgets(t *testing.T) {
	t.Run("git success", func(t *testing.T) {
		fake := packetFakeCommand(t, "printf 'source-ok\\n'")
		withPacketSourceBinaries(t, fake, packetGHBinary)
		got, err := gitOut(time.Second, t.TempDir(), "rev-parse", "HEAD")
		if err != nil || got != "source-ok\n" {
			t.Fatalf("git success got=%q err=%v", got, err)
		}
	})
	t.Run("git failure preserves diagnostic", func(t *testing.T) {
		fake := packetFakeCommand(t, "printf 'fake git failure' >&2; exit 7")
		withPacketSourceBinaries(t, fake, packetGHBinary)
		got, err := gitOut(time.Second, t.TempDir(), "diff")
		if err == nil || !strings.Contains(err.Error(), "failed") || !strings.Contains(got, "fake git failure") {
			t.Fatalf("git failure got=%q err=%v", got, err)
		}
	})
	t.Run("git timeout", func(t *testing.T) {
		fake := packetFakeCommand(t, "exec sleep 2")
		withPacketSourceBinaries(t, fake, packetGHBinary)
		_, err := gitOut(25*time.Millisecond, t.TempDir(), "diff")
		if err == nil || !strings.Contains(err.Error(), "timed out") || !strings.Contains(err.Error(), "--timeout") {
			t.Fatalf("git timeout err=%v", err)
		}
	})
	t.Run("gh success", func(t *testing.T) {
		sha := strings.Repeat("a", 40)
		fake := packetFakeCommand(t, "printf '{\\\"headRefOid\\\":\\\""+sha+"\\\"}'")
		withPacketSourceBinaries(t, packetGitBinary, fake)
		got, err := hostPRHead(time.Second, "owner/repo", 7)
		if err != nil || got != sha {
			t.Fatalf("gh success got=%q err=%v", got, err)
		}
	})
}

func TestPacketTimeoutFlagBoundsSourceCommand(t *testing.T) {
	lane, _ := packetLab(t)
	fake := packetFakeCommand(t, "exec sleep 2")
	withPacketSourceBinaries(t, fake, packetGHBinary)
	old, _ := os.Getwd()
	defer os.Chdir(old)
	os.Chdir(lane)
	var out, errb bytes.Buffer
	if code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "emma", "--out", "timed.md", "--timeout", "1"}, &out, &errb); code != 2 || !strings.Contains(errb.String(), "timed out") {
		t.Fatalf("timeout code=%d stderr=%s", code, errb.String())
	}
	if _, err := os.Stat("timed.md"); !os.IsNotExist(err) {
		t.Fatalf("timeout wrote artifact: %v", err)
	}
}
