package pulse

// Red tests for CARD-8362: nova-pulse status utilisation per bench and the
// STARVED alarm. Every slot working all the time: status prints utilisation
// per bench (leases held per owner against share) and a STARVED line when
// work is pending and slots are free for two consecutive ticks.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

func writeSlotShares(t *testing.T, store, body string) {
	t.Helper()
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "shares.tsv"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writePendingCard(t *testing.T, queue, name string) {
	t.Helper()
	dir := filepath.Join(queue, "pending")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("card\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runStatusSlots(t *testing.T, queue, store, roots string, now time.Time) (string, string, int) {
	t.Helper()
	var out, errs bytes.Buffer
	code := Status(StatusInput{
		Queue: queue, Roots: roots, Day: "2026-09-15",
		SlotsStores: store,
		Stdout:      &out, Stderr: &errs, Now: func() time.Time { return now },
	})
	return out.String(), errs.String(), code
}

// Full utilisation prints no STARVED.
func TestStatusSlotsFullUtilisationNoStarved(t *testing.T) {
	base := t.TempDir()
	queue := filepath.Join(base, "queue")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}
	rootsArg := queue
	specs := fakePATH(t)
	fakeGh(t, specs, "[]")
	store := filepath.Join(base, "bench-a")
	writeSlotShares(t, store, "capacity\t2\nreserve\t0\nalice\t2\n")
	now := time.Now().UTC()
	pid := os.Getpid()
	future := now.Add(time.Hour)
	if err := swarm.MakeSlotLease(store, "l1", "alice", pid, "", future); err != nil {
		t.Fatal(err)
	}
	if err := swarm.MakeSlotLease(store, "l2", "alice", pid, "", future); err != nil {
		t.Fatal(err)
	}
	out, _, code := runStatusSlots(t, queue, store, rootsArg, now)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 at full utilisation, out:\n%s", code, out)
	}
	if !strings.Contains(out, "STATUS SLOTS bench=bench-a capacity=2 reserve=0 held=2 free=0") {
		t.Fatalf("SLOTS line wants full utilisation, got:\n%s", out)
	}
	if !strings.Contains(out, "owners=alice:2/2") {
		t.Fatalf("SLOTS owners wants alice:2/2, got:\n%s", out)
	}
	if strings.Contains(out, "STARVED") {
		t.Fatalf("full utilisation must print no STARVED, got:\n%s", out)
	}
}

// Free slots with pending cards on two ticks print STARVED and exit 2.
func TestStatusSlotsStarvedOnTwoTicks(t *testing.T) {
	base := t.TempDir()
	queue := filepath.Join(base, "queue")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}
	rootsArg := queue
	specs := fakePATH(t)
	fakeGh(t, specs, "[]")
	store := filepath.Join(base, "bench-a")
	writeSlotShares(t, store, "capacity\t4\nreserve\t0\nalice\t2\nbob\t2\n")
	now := time.Now().UTC()
	pid := os.Getpid()
	future := now.Add(time.Hour)
	if err := swarm.MakeSlotLease(store, "l1", "alice", pid, "", future); err != nil {
		t.Fatal(err)
	}
	writePendingCard(t, queue, "card-1.md")
	out1, _, code1 := runStatusSlots(t, queue, store, rootsArg, now)
	if strings.Contains(out1, "STARVED") {
		t.Fatalf("first tick must not STARVE, got:\n%s", out1)
	}
	if code1 != 0 {
		t.Fatalf("first tick exit = %d, want 0, out:\n%s", code1, out1)
	}
	out2, _, code2 := runStatusSlots(t, queue, store, rootsArg, now)
	if !strings.Contains(out2, "STATUS STARVED bench=bench-a free=3 pending=1") {
		t.Fatalf("second tick wants STARVED bench=bench-a free=3 pending=1, got:\n%s", out2)
	}
	if code2 != 2 {
		t.Fatalf("second tick exit = %d, want 2, out:\n%s", code2, out2)
	}
}

// One tick alone does not STARVE.
func TestStatusSlotsOneTickAloneDoesNotStarve(t *testing.T) {
	base := t.TempDir()
	queue := filepath.Join(base, "queue")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}
	rootsArg := queue
	specs := fakePATH(t)
	fakeGh(t, specs, "[]")
	store := filepath.Join(base, "bench-a")
	writeSlotShares(t, store, "capacity\t4\nreserve\t0\nalice\t2\nbob\t2\n")
	now := time.Now().UTC()
	pid := os.Getpid()
	future := now.Add(time.Hour)
	if err := swarm.MakeSlotLease(store, "l1", "alice", pid, "", future); err != nil {
		t.Fatal(err)
	}
	writePendingCard(t, queue, "card-1.md")
	out, _, code := runStatusSlots(t, queue, store, rootsArg, now)
	if strings.Contains(out, "STARVED") {
		t.Fatalf("one tick alone must not STARVE, got:\n%s", out)
	}
	if code != 0 {
		t.Fatalf("one tick exit = %d, want 0, out:\n%s", code, out)
	}
	if !strings.Contains(out, "STATUS SLOTS bench=bench-a capacity=4 reserve=0 held=1 free=3") {
		t.Fatalf("SLOTS line wants held=1 free=3, got:\n%s", out)
	}
}
