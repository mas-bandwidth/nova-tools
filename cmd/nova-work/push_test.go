package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/mas-bandwidth/nova-tools/internal/redisq"
)

// pushClock is a fixed clock, so the `pushed-at` field a card carries is the one this test
// wrote rather than the moment it ran.
var pushClock = func() time.Time { return time.Date(2026, 9, 19, 2, 0, 0, 0, time.UTC) }

// cardFile writes a card whose first line is the RESULT: line every worker card opens with,
// and returns its path. The base name is the label, which is what reaches a bench.
func cardFile(t *testing.T, label, first string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), label+".md")
	body := first + "\n\nRULES — the card body.\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func runPush(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := cmdPush(args, &out, &errb, pushClock)
	return code, out.String(), errb.String()
}

// TestPushThenPullReturnsTheSameCard is the point of the verb, and it is red without it:
// `nova-swarm pull` has read nova:queue:<kind>:<lane> since #1436 and NOTHING wrote to it.
// This drives the write and the read over one miniredis, through the same internal/redisq
// that nova-swarm uses, and asserts the card that comes back is the card that went in.
func TestPushThenPullReturnsTheSameCard(t *testing.T) {
	mr := miniredis.RunT(t)
	path := cardFile(t, "card-9346", "RESULT: CARD-9346 the ready set has a writer")

	code, out, errb := runPush(t, "--stream", "rebase", "--lane", "next", "--card", path,
		"--redis", mr.Addr(), "--priority", "3", "--needs", "1269,1270")
	if code != 0 {
		t.Fatalf("push exit = %d, stderr=%s", code, errb)
	}
	const stream = "nova:queue:rebase:next"
	if !strings.HasPrefix(out, "PUSH stream="+stream+" card=") {
		t.Fatalf("push printed %q; it must name the stream it wrote and the id it got", out)
	}
	if !strings.Contains(out, "label=card-9346") {
		t.Errorf("push printed %q; it must name the label a bench will make a slot for", out)
	}

	q, err := redisq.Open(mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	ctx := context.Background()
	card, err := q.Pull(ctx, stream, "space", 0)
	if err != nil {
		t.Fatal(err)
	}
	if card == nil {
		t.Fatal("pull found nothing on the stream push just wrote; the producer and the consumer do not agree on the name or the group")
	}
	for field, want := range map[string]string{
		"card":      "card-9346",
		"priority":  "3",
		"needs":     "1269,1270",
		"pushed-at": "2026-09-19T02:00:00Z",
	} {
		if got := card.Fields[field]; got != want {
			t.Errorf("the pulled card's %s is %q, want %q", field, got, want)
		}
	}
	if !strings.HasPrefix(card.Fields["body"], "RESULT: CARD-9346") {
		t.Errorf("the pulled card's body does not open with the RESULT: line: %q", card.Fields["body"])
	}
}

// TestPushMakesTheGroupSoNothingIsPushedBehindTheCursor is the failure this verb is most
// likely to have and the hardest to see: a card added to a stream with no consumer group is
// invisible to XREADGROUP's `>` until somebody makes a group from `$`, and by then the card
// is behind the cursor. Pushed, acknowledged, never delivered, never missed. So push makes
// the group itself, and this pulls WITHOUT calling EnsureGroup first to prove it.
func TestPushMakesTheGroupSoNothingIsPushedBehindTheCursor(t *testing.T) {
	mr := miniredis.RunT(t)
	path := cardFile(t, "card-1", "RESULT: CARD-1 a card pushed before any bench woke up")

	if code, _, errb := runPush(t, "--stream", "fix", "--lane", "red", "--card", path, "--redis", mr.Addr()); code != 0 {
		t.Fatalf("push exit = %d, stderr=%s", code, errb)
	}
	q, err := redisq.Open(mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	card, err := q.Pull(context.Background(), "nova:queue:fix:red", "space", 0)
	if err != nil {
		t.Fatalf("pulling from a stream push made the group on: %v", err)
	}
	if card == nil {
		t.Fatal("the card is behind the cursor: push wrote it without making the group, so the first bench to arrive makes one from $ and never sees it")
	}
}

// TestPushDirectoryModeIsTheSameContract: pull has two modes and so does push. A card in the
// directory queue is read by the same reader nova-swarm falls back to.
func TestPushDirectoryModeIsTheSameContract(t *testing.T) {
	root := t.TempDir()
	path := cardFile(t, "card-dir", "RESULT: CARD-DIR the fallback has a writer too")

	code, out, errb := runPush(t, "--stream", "chore", "--lane", "small", "--card", path, "--dir", root)
	if code != 0 {
		t.Fatalf("push --dir exit = %d, stderr=%s", code, errb)
	}
	if !strings.Contains(out, "PUSH stream=nova:queue:chore:small card=") {
		t.Fatalf("push --dir printed %q", out)
	}
	// The id on the PUSH line is the id `nova-swarm pull` prints on its PULL line, so
	// anything reading both can join them. It is NOT DirQueue.Add's return, which is the
	// path it wrote: running the two binaries by hand is what found that, and nothing in
	// this package would have.
	id := strings.TrimSuffix(strings.TrimPrefix(strings.Fields(out)[2], "card="), "\n")
	if strings.Contains(id, "/") || strings.HasSuffix(id, ".card") {
		t.Errorf("push printed card=%s, which is a path; pull prints an id, and a line nobody can join to the other is a line that does not do its one job", id)
	}
	card, err := (&redisq.DirQueue{Root: root}).Pull("nova:queue:chore:small")
	if err != nil {
		t.Fatal(err)
	}
	if card == nil {
		t.Fatal("the directory queue returned nothing; push and pull disagree about the fallback")
	}
	if card.ID != id {
		t.Errorf("push said card=%s and pull found id %s; the two lines must name the same thing", id, card.ID)
	}
	if card.Fields["card"] != "card-dir" {
		t.Fatalf("the directory queue returned card %+v; its `card` field is not the label push wrote", card)
	}
}

// TestPushRefusesInOneRunNamingEveryProblem: a coordinator pushing a batch by hand finds out
// about all of it at once. Each refusal is one line on stderr, exit 2, nothing written.
func TestPushRefusesInOneRunNamingEveryProblem(t *testing.T) {
	code, out, errb := runPush(t, "--priority", "-1")
	if code != 2 {
		t.Fatalf("a push with nothing named exits %d, want 2", code)
	}
	if out != "" {
		t.Errorf("push wrote to stdout while refusing: %q", out)
	}
	for _, want := range []string{"--stream is required", "--lane is required", "--card is required", "--redis or --dir is required", "--priority is 0 or more"} {
		if !strings.Contains(errb, want) {
			t.Errorf("the refusal does not name %q; every problem of one run is named in that run:\n%s", want, errb)
		}
	}
}

// TestPushRefusesALaneNobodyReads is the refusal worth the most. nova-swarm pull walks red,
// green, small and next and ONLY those. A card in a fifth lane is written, acknowledged and
// never taken, which is the worst shape a queue can have: it looks like it worked.
func TestPushRefusesALaneNobodyReads(t *testing.T) {
	mr := miniredis.RunT(t)
	path := cardFile(t, "card-2", "RESULT: CARD-2 a card for a lane nobody reads")

	code, _, errb := runPush(t, "--stream", "fix", "--lane", "urgent", "--card", path, "--redis", mr.Addr())
	if code != 2 {
		t.Fatalf("a push to an unread lane exits %d, want 2", code)
	}
	if !strings.Contains(errb, "is read by nobody") {
		t.Errorf("the refusal does not say the lane is unread: %s", errb)
	}
	q, err := redisq.Open(mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	if err := q.EnsureGroup(context.Background(), "nova:queue:fix:urgent"); err != nil {
		t.Fatal(err)
	}
	card, err := q.Pull(context.Background(), "nova:queue:fix:urgent", "space", 0)
	if err != nil {
		t.Fatal(err)
	}
	if card != nil {
		t.Error("the refused push wrote the card anyway; a refusal writes nothing")
	}
}

// TestPushRefusesACardWithNoResultLine and a label that cannot name a slot directory. Both
// are refused at the ONE write rather than on the bench that takes it twenty minutes later.
func TestPushRefusesACardWithNoResultLine(t *testing.T) {
	mr := miniredis.RunT(t)

	noResult := cardFile(t, "card-3", "TODO: I meant to write this one")
	code, _, errb := runPush(t, "--stream", "fix", "--lane", "red", "--card", noResult, "--redis", mr.Addr())
	if code != 2 {
		t.Fatalf("a card with no RESULT: line exits %d, want 2", code)
	}
	if !strings.Contains(errb, "does not open with a RESULT: line") {
		t.Errorf("the refusal does not say what is wrong with the card: %s", errb)
	}

	odd := cardFile(t, "card 4", "RESULT: CARD-4 a label with a space in it")
	code, _, errb = runPush(t, "--stream", "fix", "--lane", "red", "--card", odd, "--redis", mr.Addr())
	if code != 2 {
		t.Fatalf("a label that is not [A-Za-z0-9._-]+ exits %d, want 2", code)
	}
	if !strings.Contains(errb, "cannot name a slot directory") {
		t.Errorf("the refusal does not say why the label is refused: %s", errb)
	}

	missing := filepath.Join(t.TempDir(), "card-5.md")
	code, _, errb = runPush(t, "--stream", "fix", "--lane", "red", "--card", missing, "--redis", mr.Addr())
	if code != 2 {
		t.Fatalf("an unreadable --card exits %d, want 2", code)
	}
	if !strings.Contains(errb, "--card wants a readable file") {
		t.Errorf("the refusal does not name the unreadable file: %s", errb)
	}
}

// TestPushRefusesTwoStoresAtOnce: one mode per call. A card written to both is a card two
// benches take, and the fence neither token sees.
func TestPushRefusesTwoStoresAtOnce(t *testing.T) {
	mr := miniredis.RunT(t)
	path := cardFile(t, "card-6", "RESULT: CARD-6 two stores")
	code, _, errb := runPush(t, "--stream", "fix", "--lane", "red", "--card", path,
		"--redis", mr.Addr(), "--dir", t.TempDir())
	if code != 2 {
		t.Fatalf("--redis and --dir together exit %d, want 2", code)
	}
	if !strings.Contains(errb, "name two stores") {
		t.Errorf("the refusal does not say why both is wrong: %s", errb)
	}
}
