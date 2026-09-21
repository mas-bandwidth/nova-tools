package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// nova-pulse cut --kind recut --hold-file writes one card from a typed HOLD
// plus named remains (#2498 B2).
func TestCutKindRecutHoldFileWritesOneCard(t *testing.T) {
	dir := t.TempDir()
	queue, out := filepath.Join(dir, "queue"), filepath.Join(dir, "queue", "pending")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}
	hold := filepath.Join(dir, "hold.md")
	body := "DISPOSITION who=Johnny head=d080cec1d2a5afcaef2b696840389e91e769a1d1 verdict=HOLD score=4/10\n" +
		"PATHS: internal/swarm/pullworker.go, internal/swarm/pullworker_test.go\n" +
		"TEST: ./internal/swarm TestHarvestDoesNotFollowAResultSymlink\n" +
		"BASE: dev\n" +
		"base-sha: c7104413f20c2897e6a9c4c19cc7158b0f4ea15c\n"
	if err := os.WriteFile(hold, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	args := []string{"cut", "--kind", "recut", "--repo", "mas-bandwidth/nova-tools",
		"--hold-file", hold, "--out", out, "--queue", queue}
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr, time.Now().UTC()); code != 0 {
		t.Fatalf("cut --kind recut exit = %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "CUT CARD card=card-1.md kind=recut") {
		t.Errorf("CUT line = %q", stdout.String())
	}
	raw, err := os.ReadFile(filepath.Join(out, "card-1.md"))
	if err != nil {
		t.Fatal(err)
	}
	card := string(raw)
	for _, want := range []string{
		"recut of nova-tools at d080cec1d2a5afcaef2b696840389e91e769a1d1 from HOLD",
		"BASE: dev",
		"base-sha: c7104413f20c2897e6a9c4c19cc7158b0f4ea15c",
		"PATHS: internal/swarm/pullworker.go, internal/swarm/pullworker_test.go",
		"HOLD: DISPOSITION who=Johnny head=d080cec1d2a5afcaef2b696840389e91e769a1d1 verdict=HOLD score=4/10",
	} {
		if !strings.Contains(card, want) {
			t.Errorf("the recut card does not carry %q:\n%s", want, card)
		}
	}
}

func TestCutKindRecutHoldFileWithoutRemainsIsARefusal(t *testing.T) {
	dir := t.TempDir()
	queue, out := filepath.Join(dir, "queue"), filepath.Join(dir, "queue", "pending")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}
	hold := filepath.Join(dir, "hold.md")
	if err := os.WriteFile(hold, []byte("DISPOSITION who=Johnny head=d080cec1d2a5afcaef2b696840389e91e769a1d1 verdict=HOLD score=4/10\nprose only\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	args := []string{"cut", "--kind", "recut", "--repo", "mas-bandwidth/nova-tools",
		"--hold-file", hold, "--out", out, "--queue", queue}
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr, time.Now().UTC()); code != 2 {
		t.Fatalf("exit = %d, want 2, stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "CUT REFUSED") || !strings.Contains(stderr.String(), "named remains") {
		t.Errorf("refusal = %q", stderr.String())
	}
	if matches, _ := filepath.Glob(filepath.Join(out, "card-*.md")); len(matches) != 0 {
		t.Errorf("a refusal wrote a card: %v", matches)
	}
}
