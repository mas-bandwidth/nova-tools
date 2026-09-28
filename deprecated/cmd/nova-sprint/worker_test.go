//go:build functional

package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

// TestWorkerPauseResumeShowCLI (#4308): worker pause|resume <kind:name>
// prints PAUSED|RESUMED <worker> and flips the desired hash's paused flag,
// bench or friend; worker show prints one line per worker, or the one
// named; an unregistered worker is a REFUSED line, exit 1; a malformed one
// a usage refusal, exit 2.
func TestWorkerPauseResumeShowCLI(t *testing.T) {
	t.Parallel()

	addr, c := benchRoleFixture(t)
	ctx := context.Background()
	c.SAdd(ctx, "benches", "hetzner")
	c.SAdd(ctx, "friends", "emma")
	c.HSet(ctx, "bench:hetzner:desired", "slots", "8", "machine", "hetzner", "tiers", "flash,pro")
	c.HSet(ctx, "friend:emma:desired", "slots", "4", "machine", "studio")

	code, out, errOut := runVerb(t, "worker", "pause", "--redis", addr, "--as", "rowan", "bench:hetzner")
	if code != 0 || out != "PAUSED bench:hetzner\n" || errOut != "" {
		t.Fatalf("pause bench = %d %q %q", code, out, errOut)
	}
	if got := c.HGet(ctx, "bench:hetzner:desired", "paused").Val(); got != "1" {
		t.Fatalf("bench paused=%q", got)
	}
	code, out, _ = runVerb(t, "worker", "pause", "--redis", addr, "friend:emma")
	if code != 0 || out != "PAUSED friend:emma\n" {
		t.Fatalf("pause friend = %d %q", code, out)
	}
	code, out, _ = runVerb(t, "worker", "show", "--redis", addr)
	want := "WORKER bench:hetzner slots=8 paused=1 tiers=flash,pro kinds=- machine=hetzner\n" +
		"WORKER friend:emma slots=4 paused=1 tiers=- kinds=- machine=studio\n"
	if code != 0 || out != want {
		t.Fatalf("show = %d\n%s\nwant:\n%s", code, out, want)
	}
	code, out, _ = runVerb(t, "worker", "resume", "--redis", addr, "friend:emma")
	if code != 0 || out != "RESUMED friend:emma\n" {
		t.Fatalf("resume friend = %d %q", code, out)
	}
	code, out, _ = runVerb(t, "worker", "show", "--redis", addr, "friend:emma")
	if code != 0 || out != "WORKER friend:emma slots=4 paused=0 tiers=- kinds=- machine=studio\n" {
		t.Fatalf("show one = %d %q", code, out)
	}
	code, out, errOut = runVerb(t, "worker", "pause", "--redis", addr, "bench:nope")
	if code != 1 || !strings.HasPrefix(out, "WORKER PAUSE REFUSED bench:nope why=\"UNKNOWN bench:nope") || errOut != "" {
		t.Fatalf("pause unknown = %d %q %q", code, out, errOut)
	}
	code, _, errOut = runVerb(t, "worker", "pause", "--redis", addr, "hetzner")
	if code != 2 || !strings.Contains(errOut, "not bench:<b> or friend:<f>") {
		t.Fatalf("pause bare name = %d %q", code, errOut)
	}
	code, _, errOut = runVerb(t, "worker", "--redis", addr)
	if code != 2 || !strings.Contains(errOut, "want pause, resume or show") {
		t.Fatalf("no subverb = %d %q", code, errOut)
	}
	// capacity bench --paused is no longer a friend-only flag (#4308)
	c.HSet(ctx, "machine:hetzner:ceiling", "slots", "64")
	code, out, errOut = runVerb(t, "capacity", "bench", "--redis", addr, "--as", "rowan", "--paused", "0", "hetzner", "8")
	if code != 0 || !strings.HasPrefix(out, "SET bench hetzner machine=hetzner slots=8") {
		t.Fatalf("capacity bench --paused 0 = %d %q %q", code, out, errOut)
	}
	if got := c.HGet(ctx, "bench:hetzner:desired", "paused").Val(); got != "0" {
		t.Fatalf("bench paused=%q after capacity bench --paused 0", got)
	}
}

// TestCardCancelEachCLI (#4309): card cancel --each prints CANCELLED <id>
// to=<where> or REFUSED <id> why=<why> per id and the count line last, exit
// 1 when any refused; without --each the same list is refused as a whole.
func TestCardCancelEachCLI(t *testing.T) {
	t.Parallel()

	addr, c := benchRoleFixture(t)
	ctx := context.Background()
	const s = "swarm: cards"
	c.HSet(ctx, "bench:b:desired", "slots", "2")
	for i := 0; i < 2; i++ {
		id := fmt.Sprintf("c%d", i)
		if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: id, Where: "waiting", Stream: s, Sprint: "s",
			Kind: "build", Ref: fmt.Sprintf("nova-tools#%d", 100+i), Origin: fmt.Sprintf("issue:nova-tools#%d", 100+i),
			Title: id, Repo: "mas-bandwidth/nova-tools", By: "rowan",
			Fields: []string{"base", "dev", "base_sha", strings.Repeat("1", 40), "paths", "a.go", "done_when", "go test ./... passes"}}); err != nil {
			t.Fatal(err)
		}
	}
	code, out, errOut := runVerb(t, "card", "deal", "--redis", addr, "--to", "bench:b", "--n", "2", "--actor", "rowan")
	if code != 0 || !strings.HasPrefix(out, "CARD DEAL to=bench:b n=2 copies=c0~1,c1~1 ms=") {
		t.Fatalf("deal = %d %q %q", code, out, errOut)
	}
	code, out, _ = runVerb(t, "card", "cancel", "--redis", addr, "--ids", "c0~1,nope,c1", "--why", "moved", "--actor", "rowan")
	if code != 1 || !strings.HasPrefix(out, "CARD CANCEL REFUSED ids=c0~1,nope,c1 why=\"NOTASK task:nope\"") {
		t.Fatalf("batch = %d %q", code, out)
	}
	code, out, _ = runVerb(t, "card", "cancel", "--redis", addr, "--ids", "c0~1,nope,c1", "--why", "moved", "--each", "--actor", "rowan")
	want := "CANCELLED c0~1 to=waiting\nREFUSED nope why=\"NOTASK task:nope\"\nCANCELLED c1 to=done\nCARD CANCEL n=2 refused=1 ms="
	if code != 1 || !strings.HasPrefix(out, want) {
		t.Fatalf("each = %d %q, want prefix %q", code, out, want)
	}
	code, out, _ = runVerb(t, "card", "cancel", "--redis", addr, "--id", "c0", "--why", "moved", "--each", "--actor", "rowan")
	if code != 0 || !strings.HasPrefix(out, "CANCELLED c0 to=done\nCARD CANCEL n=1 refused=0 ms=") {
		t.Fatalf("each, all ok = %d %q", code, out)
	}
	code, out, _ = runVerb(t, "card", "fsck", "--redis", addr)
	if code != 0 || !strings.Contains(out, " drift=0 fixed=0 ") {
		t.Fatalf("fsck = %d %q", code, out)
	}
}
