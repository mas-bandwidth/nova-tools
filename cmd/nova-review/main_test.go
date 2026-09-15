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
	git("remote", "add", "origin", repo)
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
	git("remote", "add", "origin", repo)
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
	if !strings.Contains(stellaText, "the author says:\nFix the network timeout instruction.") {
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

func TestPacketAcceptsAbsoluteOutUnderCwd(t *testing.T) {
	lane, _ := packetLab(t)
	cwd := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if e := os.Chdir(cwd); e != nil {
		t.Fatal(e)
	}
	absOut := filepath.Join(cwd, "packet.md")
	var out, errb bytes.Buffer
	if code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "emma", "--out", absOut}, &out, &errb); code != 0 {
		t.Fatalf("absolute --out under cwd refused: code=%d stderr=%s", code, errb.String())
	}
	if _, err := os.Stat(absOut); err != nil {
		t.Fatalf("packet not written at %s: %v", absOut, err)
	}
}

func TestPacketRefetchesMovedHead(t *testing.T) {
	dir := t.TempDir()
	remote := filepath.Join(dir, "remote.git")
	seed := filepath.Join(dir, "seed")
	if e := os.MkdirAll(seed, 0o755); e != nil {
		t.Fatal(e)
	}
	gitAt := func(d string, args ...string) string {
		c := exec.Command("git", args...)
		c.Dir = d
		b, e := c.CombinedOutput()
		if e != nil {
			t.Fatalf("git %v: %v %s", args, e, b)
		}
		return strings.TrimSpace(string(b))
	}
	gitAt(dir, "init", "-q", "--bare", remote)
	gitAt(seed, "init", "-q", "-b", "main")
	gitAt(seed, "config", "user.email", "t@example.invalid")
	gitAt(seed, "config", "user.name", "t")
	if e := os.WriteFile(filepath.Join(seed, "a.txt"), []byte("base\n"), 0o644); e != nil {
		t.Fatal(e)
	}
	gitAt(seed, "add", "a.txt")
	gitAt(seed, "commit", "-qm", "base")
	base := gitAt(seed, "rev-parse", "HEAD")
	gitAt(seed, "checkout", "-qb", "feature")
	if e := os.WriteFile(filepath.Join(seed, "a.txt"), []byte("base\nv1\n"), 0o644); e != nil {
		t.Fatal(e)
	}
	gitAt(seed, "add", "a.txt")
	gitAt(seed, "commit", "-qm", "v1")
	old := gitAt(seed, "rev-parse", "HEAD")
	gitAt(seed, "push", "-q", remote, "main:refs/heads/main", "feature:refs/heads/feature")
	gitAt(remote, "symbolic-ref", "HEAD", "refs/heads/main")

	lane := filepath.Join(dir, "lane")
	if e := os.MkdirAll(lane, 0o755); e != nil {
		t.Fatal(e)
	}
	gitAt(dir, "clone", "-q", remote, filepath.Join(lane, merge.RepoDir))
	st := &merge.State{Version: merge.Version, Repo: "test/repo", Base: base, LaneBranch: "lane", Branches: []*merge.Entry{{Branch: "feature", OID: old, NeedsRead: "yes"}}}
	if e := st.SaveTo(lane); e != nil {
		t.Fatal(e)
	}

	if e := os.WriteFile(filepath.Join(seed, "a.txt"), []byte("base\nv2\n"), 0o644); e != nil {
		t.Fatal(e)
	}
	gitAt(seed, "add", "a.txt")
	gitAt(seed, "commit", "-qm", "v2")
	newHead := gitAt(seed, "rev-parse", "HEAD")
	gitAt(seed, "push", "-qf", remote, "feature:refs/heads/feature")

	var out, errb bytes.Buffer
	if code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "emma", "--out", filepath.Join(lane, "packet.md")}, &out, &errb); code != 0 {
		t.Fatalf("packet after a moved head code=%d stderr=%s", code, errb.String())
	}
	if want := "PACKET NOTE head moved " + merge.Short(old) + " -> " + merge.Short(newHead); !strings.Contains(errb.String(), want) {
		t.Fatalf("missing moved-head note %q in %q", want, errb.String())
	}
	st2, e := merge.Load(lane)
	if e != nil {
		t.Fatal(e)
	}
	if got := st2.Find("feature").OID; got != newHead {
		t.Fatalf("recorded head not updated: got %s want %s", got, newHead)
	}
}

func TestPacketFetchesBaseBeforeRange(t *testing.T) {
	dir := t.TempDir()
	remote := filepath.Join(dir, "remote.git")
	seed := filepath.Join(dir, "seed")
	if e := os.MkdirAll(seed, 0o755); e != nil {
		t.Fatal(e)
	}
	gitAt := func(d string, args ...string) string {
		c := exec.Command("git", args...)
		c.Dir = d
		b, e := c.CombinedOutput()
		if e != nil {
			t.Fatalf("git %v: %v %s", args, e, b)
		}
		return strings.TrimSpace(string(b))
	}
	gitAt(dir, "init", "-q", "--bare", remote)
	gitAt(seed, "init", "-q", "-b", "main")
	gitAt(seed, "config", "user.email", "t@example.invalid")
	gitAt(seed, "config", "user.name", "t")
	if e := os.WriteFile(filepath.Join(seed, "a.txt"), []byte("base\n"), 0o644); e != nil {
		t.Fatal(e)
	}
	gitAt(seed, "add", "a.txt")
	gitAt(seed, "commit", "-qm", "base")
	gitAt(seed, "push", "-q", remote, "main:refs/heads/main")
	gitAt(remote, "symbolic-ref", "HEAD", "refs/heads/main")

	lane := filepath.Join(dir, "lane")
	if e := os.MkdirAll(lane, 0o755); e != nil {
		t.Fatal(e)
	}
	gitAt(dir, "clone", "-q", remote, filepath.Join(lane, merge.RepoDir))

	// Advance origin/main past the clone's idle local main. A range built on the
	// stale local branch would count these unrelated files in the packet (#418).
	for _, f := range []string{"b.txt", "c.txt"} {
		if e := os.WriteFile(filepath.Join(seed, f), []byte(f+"\n"), 0o644); e != nil {
			t.Fatal(e)
		}
		gitAt(seed, "add", f)
	}
	gitAt(seed, "commit", "-qm", "unrelated base moves")
	gitAt(seed, "push", "-q", remote, "main:refs/heads/main")

	// The feature branches off the advanced main and touches one file.
	gitAt(seed, "checkout", "-qb", "feature")
	if e := os.WriteFile(filepath.Join(seed, "feat.txt"), []byte("one file\n"), 0o644); e != nil {
		t.Fatal(e)
	}
	gitAt(seed, "add", "feat.txt")
	gitAt(seed, "commit", "-qm", "feature")
	head := gitAt(seed, "rev-parse", "HEAD")
	gitAt(seed, "push", "-q", remote, "feature:refs/heads/feature")

	st := &merge.State{Version: merge.Version, Repo: "test/repo", Base: "main", LaneBranch: "lane", Branches: []*merge.Entry{{Branch: "feature", OID: head, NeedsRead: "yes"}}}
	if e := st.SaveTo(lane); e != nil {
		t.Fatal(e)
	}

	var out, errb bytes.Buffer
	if code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "emma", "--out", filepath.Join(lane, "packet.md")}, &out, &errb); code != 0 {
		t.Fatalf("packet code=%d stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), " files=1 ") {
		t.Fatalf("a one-file PR with a stale base must yield files=1; got %q", out.String())
	}
}

func TestLaneRefusalNamesRemedy(t *testing.T) {
	plain := t.TempDir()
	var out, errb bytes.Buffer
	if code := run([]string{"packet", "--lane", plain, "--branch", "feature", "--who", "emma", "--out", "p.md"}, &out, &errb); code != 2 {
		t.Fatalf("plain checkout code=%d, want 2", code)
	}
	want := "a lane is a directory made by nova-merge init --lane <dir> --repo <owner/name> --base <branch> --lane-branch <name>"
	if !strings.Contains(errb.String(), want) {
		t.Fatalf("refusal does not name the remedy; got %q want %q", errb.String(), want)
	}
}
