package pulse

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const recutHoldLine = "DISPOSITION who=Johnny head=d080cec1d2a5afcaef2b696840389e91e769a1d1 verdict=HOLD score=4/10"

func writeHold(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "hold.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func recutDirs(t *testing.T) (out, queue string) {
	t.Helper()
	dir := t.TempDir()
	out, queue = filepath.Join(dir, "pending"), filepath.Join(dir, "queue")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}
	return out, queue
}

// cut-kind-recut-from-hold: a typed HOLD plus named remains (PATHS and the failing
// test) cuts one recut card whose header carries BASE, base-sha, PATHS, TEST, and
// the HOLD line as evidence (#2498 B2).
func TestCutKindRecutWritesACardFromAHold(t *testing.T) {
	out, queue := recutDirs(t)
	hold := writeHold(t, t.TempDir(), recutHoldLine+"\n"+
		"PATHS: internal/swarm/pullworker.go, internal/swarm/pullworker_test.go\n"+
		"TEST: ./internal/swarm TestHarvestDoesNotFollowAResultSymlink\n"+
		"BASE: dev\n"+
		"base-sha: c7104413f20c2897e6a9c4c19cc7158b0f4ea15c\n"+
		"\nHarvest RESULT.md with O_NOFOLLOW.\n")

	code, line, errs, card := cutKind(t, CutKindInput{
		Kind: "recut", Repo: "mas-bandwidth/nova-tools",
		HoldFile: hold, Out: out, Queue: queue,
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errs)
	}
	if !strings.Contains(line, "CUT CARD card=card-1.md") || !strings.Contains(line, "kind=recut") {
		t.Errorf("the one line = %q", line)
	}
	if n := len(strings.Split(strings.TrimRight(line, "\n"), "\n")); n != 1 {
		t.Errorf("stdout is %d lines, want 1: %q", n, line)
	}

	wantRE := regexp.MustCompile(`^RESULT: CARD-1 sha=[0-9a-f]{12} recut of nova-tools at d080cec1d2a5afcaef2b696840389e91e769a1d1 from HOLD$`)
	line1 := strings.SplitN(card, "\n", 2)[0]
	if !wantRE.MatchString(line1) {
		t.Errorf("line 1 = %q\nwant to match %s", line1, wantRE.String())
	}
	m := regexp.MustCompile(`^RESULT: CARD-1 sha=([0-9a-f]{12}) `).FindStringSubmatch(card)
	if m == nil {
		t.Fatalf("line 1 carries no sha=<sha12> token:\n%s", card)
	}
	sum := sha256.Sum256([]byte(strings.Join(strings.Split(card, "\n")[1:], "\n")))
	if m[1] != hex.EncodeToString(sum[:])[:12] {
		t.Errorf("sha=%s, want %s (the sha-12 of every line below line 1)", m[1], hex.EncodeToString(sum[:])[:12])
	}

	for _, want := range []string{
		"KIND: recut",
		"BASE: dev",
		"base-sha: c7104413f20c2897e6a9c4c19cc7158b0f4ea15c",
		"PATHS: internal/swarm/pullworker.go, internal/swarm/pullworker_test.go",
		"TEST: ./internal/swarm TestHarvestDoesNotFollowAResultSymlink",
		"HOLD: " + recutHoldLine,
	} {
		if !strings.Contains(card, want+"\n") && !strings.Contains(card, want+"\r\n") {
			if !strings.Contains(card, want) {
				t.Errorf("the recut card does not carry %q:\n%s", want, card)
			}
		}
	}
	if strings.Contains(strings.ToLower(card), "git apply") || strings.Contains(card, "--3way") {
		t.Errorf("mechanical apply is S1, not this cutter; the card names git apply:\n%s", card)
	}

	if matches, _ := filepath.Glob(filepath.Join(queue, "lanes", "red", "*.card")); len(matches) != 1 {
		t.Errorf("a recut card is not in the red lane: %v", matches)
	}
}

// PATHS is copied when the HOLD names it, and omitted when it does not.
func TestCutKindRecutOmitsPathsWhenTheHoldHasNone(t *testing.T) {
	out, queue := recutDirs(t)
	hold := writeHold(t, t.TempDir(), recutHoldLine+"\n"+
		"TEST: ./internal/swarm TestHarvestDoesNotFollowAResultSymlink\n"+
		"BASE: dev@c7104413f20c2897e6a9c4c19cc7158b0f4ea15c\n")

	code, _, errs, card := cutKind(t, CutKindInput{
		Kind: "recut", Repo: "mas-bandwidth/nova-tools",
		HoldFile: hold, Out: out, Queue: queue,
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errs)
	}
	if strings.Contains(card, "PATHS:") {
		t.Errorf("PATHS was not on the HOLD; the card invented it:\n%s", card)
	}
	if !strings.Contains(card, "BASE: dev") || !strings.Contains(card, "base-sha: c7104413f20c2897e6a9c4c19cc7158b0f4ea15c") {
		t.Errorf("BASE: dev@sha on the HOLD did not split into BASE and base-sha:\n%s", card)
	}
	if !strings.Contains(card, recutHoldLine) {
		t.Errorf("the HOLD line is not on the card as evidence:\n%s", card)
	}
}

// a HOLD without a named remains (PATHS, TEST, or REMAINS) is a refusal, and
// no card is written.
func TestCutKindRecutRefusesAHoldWithoutNamedRemains(t *testing.T) {
	out, queue := recutDirs(t)
	hold := writeHold(t, t.TempDir(), recutHoldLine+"\n\nHOLD. The wall this card claims is not in the running code.\n")

	code, line, errs, card := cutKind(t, CutKindInput{
		Kind: "recut", Repo: "mas-bandwidth/nova-tools",
		HoldFile: hold, Out: out, Queue: queue,
	})
	if code != 2 {
		t.Fatalf("exit = %d, want 2, stdout=%s stderr=%s", code, line, errs)
	}
	if !strings.Contains(errs, "CUT REFUSED") || !strings.Contains(errs, "named remains") {
		t.Errorf("refusal = %q, want CUT REFUSED naming named remains", errs)
	}
	if !strings.Contains(errs, "(") {
		t.Errorf("the refusal names no remedy: %q", errs)
	}
	if card != "" {
		t.Errorf("a refused recut still wrote a card:\n%s", card)
	}
	if matches, _ := filepath.Glob(filepath.Join(out, "card-*.md")); len(matches) != 0 {
		t.Errorf("a refused recut left files in --out: %v", matches)
	}
}

func TestCutKindRecutRefusesANonHoldDisposition(t *testing.T) {
	out, queue := recutDirs(t)
	hold := writeHold(t, t.TempDir(),
		"DISPOSITION who=Johnny head=d080cec1d2a5afcaef2b696840389e91e769a1d1 verdict=APPROVE score=8/10\n"+
			"PATHS: internal/swarm/pullworker.go\n"+
			"TEST: ./internal/swarm TestHarvestDoesNotFollowAResultSymlink\n")

	code, _, errs, card := cutKind(t, CutKindInput{
		Kind: "recut", Repo: "mas-bandwidth/nova-tools",
		HoldFile: hold, Out: out, Queue: queue,
	})
	if code != 2 {
		t.Fatalf("exit = %d, want 2, stderr=%s", code, errs)
	}
	if !strings.Contains(errs, "CUT REFUSED") || !strings.Contains(errs, "HOLD") {
		t.Errorf("refusal = %q, want CUT REFUSED naming HOLD", errs)
	}
	if card != "" {
		t.Errorf("an APPROVE still cut a recut card:\n%s", card)
	}
}

func TestCutKindRecutRefusesAQuotedHoldAsEvidence(t *testing.T) {
	out, queue := recutDirs(t)
	hold := writeHold(t, t.TempDir(),
		"> "+recutHoldLine+"\n"+
			"PATHS: internal/swarm/pullworker.go\n"+
			"TEST: ./internal/swarm TestHarvestDoesNotFollowAResultSymlink\n")

	code, _, errs, _ := cutKind(t, CutKindInput{
		Kind: "recut", Repo: "mas-bandwidth/nova-tools",
		HoldFile: hold, Out: out, Queue: queue,
	})
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (a quoted DISPOSITION is not a HOLD), stderr=%s", code, errs)
	}
	if !strings.Contains(errs, "CUT REFUSED") || !strings.Contains(errs, "DISPOSITION") {
		t.Errorf("refusal = %q, want CUT REFUSED naming DISPOSITION", errs)
	}
}

// A caller --head that is not the HOLD revision must not win RESULT/SOURCE
// while HOLD: still names the typed head (#2500).
func TestCutKindRecutRefusesAHeadThatDiffersFromTheHold(t *testing.T) {
	out, queue := recutDirs(t)
	hold := writeHold(t, t.TempDir(), recutHoldLine+"\n"+
		"PATHS: internal/swarm/pullworker.go\n"+
		"TEST: ./internal/swarm TestHarvestDoesNotFollowAResultSymlink\n")
	const other = "0123456789abcdef0123456789abcdef01234567"

	code, line, errs, card := cutKind(t, CutKindInput{
		Kind: "recut", Repo: "mas-bandwidth/nova-tools", Head: other,
		HoldFile: hold, Out: out, Queue: queue,
	})
	if code != 2 {
		t.Fatalf("exit = %d, want 2, stdout=%s stderr=%s", code, line, errs)
	}
	if !strings.Contains(errs, "CUT REFUSED") || !strings.Contains(errs, other) || !strings.Contains(errs, "d080cec1d2a5afcaef2b696840389e91e769a1d1") || !strings.Contains(errs, "(") {
		t.Errorf("refusal = %q, want CUT REFUSED naming both heads and a remedy", errs)
	}
	if strings.Contains(line, "CUT CARD") || card != "" {
		t.Errorf("a mismatched --head still cut a card:\nstdout=%s\n%s", line, card)
	}
	if matches, _ := filepath.Glob(filepath.Join(out, "card-*.md")); len(matches) != 0 {
		t.Errorf("a refused recut left files in --out: %v", matches)
	}
	if matches, _ := filepath.Glob(filepath.Join(queue, "lanes", "red", "*.card")); len(matches) != 0 {
		t.Errorf("a refused recut left a red-lane card: %v", matches)
	}
}

// The same revision, however the caller spells it, still cuts, and RESULT and
// SOURCE use the HOLD head rather than the caller's spelling.
func TestCutKindRecutBindsToTheHoldHeadWhenTheCallerPassesIt(t *testing.T) {
	out, queue := recutDirs(t)
	hold := writeHold(t, t.TempDir(), recutHoldLine+"\n"+
		"PATHS: internal/swarm/pullworker.go\n"+
		"TEST: ./internal/swarm TestHarvestDoesNotFollowAResultSymlink\n")
	const holdHead = "d080cec1d2a5afcaef2b696840389e91e769a1d1"

	code, _, errs, card := cutKind(t, CutKindInput{
		Kind: "recut", Repo: "mas-bandwidth/nova-tools", Head: strings.ToUpper(holdHead),
		HoldFile: hold, Out: out, Queue: queue,
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errs)
	}
	for _, want := range []string{
		"recut of nova-tools at " + holdHead + " from HOLD",
		"SOURCE: mas-bandwidth/nova-tools HOLD " + holdHead,
		"HOLD: " + recutHoldLine,
	} {
		if !strings.Contains(card, want) {
			t.Errorf("the recut card does not carry %q:\n%s", want, card)
		}
	}
	if strings.Contains(card, strings.ToUpper(holdHead)) {
		t.Errorf("the caller's spelling of the head leaked onto the card:\n%s", card)
	}
}

func TestCutKindRecutRefusesWithoutHoldFile(t *testing.T) {
	dir := t.TempDir()
	var out, errs bytes.Buffer
	code := CutKind(CutKindInput{
		Kind: "recut", Repo: "o/n", Out: dir, Queue: dir,
		Stdout: &out, Stderr: &errs,
	})
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errs.String(), "CUT REFUSED") || !strings.Contains(errs.String(), "--hold-file") || !strings.Contains(errs.String(), "(") {
		t.Errorf("refusal = %q, want CUT REFUSED naming --hold-file and a remedy", errs.String())
	}
}
