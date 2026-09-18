package swarm

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// plantCard puts one <name>.card in a bench's queue/, making the directory when it is
// not there yet.
func plantCard(t *testing.T, benchDir, name string) {
	t.Helper()
	queue := filepath.Join(benchDir, QueueName)
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatalf("mkdir queue: %v", err)
	}
	if err := os.WriteFile(filepath.Join(queue, name+CardExt), []byte("card "+name+"\n"), 0o644); err != nil {
		t.Fatalf("write card %s: %v", name, err)
	}
}

// two-workers-cannot-take-one-card (docs/SPEC-JOBS.md section 2, line 57): a worker
// takes a card by rename(<name>.card, taken/<worker>-<name>.card), which is atomic
// within the directory, so two workers racing for one card cannot both take it --
// exactly one wins and the card is on exactly one worker.
func TestTwoWorkersCannotTakeOneCard(t *testing.T) {
	bench := t.TempDir()
	plantCard(t, bench, "only")

	const workers = 16
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		wins  []string
		start = make(chan struct{})
	)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // every worker reaches the rename with the queue unchanged
			name, ok, err := TakeCard(bench, "w"+strconv.Itoa(i))
			if err != nil {
				t.Errorf("take: %v", err)
				return
			}
			if ok {
				mu.Lock()
				wins = append(wins, name)
				mu.Unlock()
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if len(wins) != 1 || wins[0] != "only" {
		t.Fatalf("wins = %v, want exactly [only]: two workers cannot take one card", wins)
	}

	left, err := QueueCards(bench)
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	if len(left) != 0 {
		t.Fatalf("queue still holds %v, want empty", left)
	}

	taken, err := os.ReadDir(TakenDir(bench))
	if err != nil {
		t.Fatalf("taken: %v", err)
	}
	if len(taken) != 1 {
		t.Fatalf("taken holds %d entries, want one ownership record", len(taken))
	}
	if name := taken[0].Name(); !strings.HasSuffix(name, "-only"+CardExt) {
		t.Fatalf("ownership record = %q, want <worker>-only.card", name)
	}
}

// a-steal-never-starves-the-victim (docs/SPEC-JOBS.md section 2, line 57): an idle
// worker steals from the fullest bench, but a steal leaves the victim at or above its
// own capacity line, and a victim already at the line is left alone.
func TestAStealNeverStarvesTheVictim(t *testing.T) {
	// The capacity line is min(cores*1.5 - load1, (free_gb-25)/2, memfree_gb/2),
	// floored to a whole worker and never negative.
	if got := CapacityLine(8, 2, 125, 64); got != 10 {
		t.Fatalf("CapacityLine(8, 2, 125, 64) = %d, want 10 (the load term is smallest)", got)
	}
	if got := CapacityLine(4, 5, 45, 20); got != 1 {
		t.Fatalf("CapacityLine(4, 5, 45, 20) = %d, want 1", got)
	}
	if got := CapacityLine(2, 10, 30, 10); got != 0 {
		t.Fatalf("CapacityLine(2, 10, 30, 10) = %d, want 0 (floored, never negative)", got)
	}

	// StealCount is everything above the line: a victim at or below its line keeps
	// every card.
	if got := StealCount(10, 4); got != 6 {
		t.Fatalf("StealCount(10, 4) = %d, want 6", got)
	}
	if got := StealCount(4, 4); got != 0 {
		t.Fatalf("StealCount(4, 4) = %d, want 0 (at the line, nothing to steal)", got)
	}
	if got := StealCount(3, 4); got != 0 {
		t.Fatalf("StealCount(3, 4) = %d, want 0 (below the line)", got)
	}

	// A real steal from a victim of ten with a line of four leaves exactly the line.
	victim := t.TempDir()
	const capacity = 4
	for i := 0; i < 10; i++ {
		plantCard(t, victim, "c"+strconv.Itoa(i))
	}
	stolen, err := Steal(victim, "thief", capacity)
	if err != nil {
		t.Fatalf("steal: %v", err)
	}
	if len(stolen) != 10-capacity {
		t.Fatalf("stole %d cards, want %d (only what is above the line)", len(stolen), 10-capacity)
	}
	left, err := QueueCards(victim)
	if err != nil {
		t.Fatalf("queue after steal: %v", err)
	}
	if len(left) != capacity {
		t.Fatalf("victim left with %d cards, want exactly its capacity line %d", len(left), capacity)
	}

	// Stealing from a victim at its line takes nothing.
	atLine := t.TempDir()
	for i := 0; i < capacity; i++ {
		plantCard(t, atLine, "d"+strconv.Itoa(i))
	}
	stolen, err = Steal(atLine, "thief", capacity)
	if err != nil {
		t.Fatalf("steal at the line: %v", err)
	}
	if len(stolen) != 0 {
		t.Fatalf("stole %d cards from a victim at its line, want 0", len(stolen))
	}
}

// The idle worker reaches for the FULLEST bench, and only on the mirror's five-minute
// timer.
func TestStealPicksTheFullestBenchOnTheMirrorTimer(t *testing.T) {
	small, full := t.TempDir(), t.TempDir()
	plantCard(t, small, "s0")
	for i := 0; i < 3; i++ {
		plantCard(t, full, "f"+strconv.Itoa(i))
	}
	name, queued, ok, err := FullestBench([]string{small, full})
	if err != nil {
		t.Fatalf("fullest bench: %v", err)
	}
	if !ok || name != full || queued != 3 {
		t.Fatalf("FullestBench = (%q, %d, %t), want (%q, 3, true)", name, queued, ok, full)
	}

	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	if MirrorDue(now, now) {
		t.Fatalf("a steal is due on the instant it last happened, want the mirror timer to hold")
	}
	if !MirrorDue(now.Add(-MirrorTimer), now) {
		t.Fatalf("a steal is not due after the mirror's %v timer", MirrorTimer)
	}
	if !MirrorDue(time.Time{}, now) {
		t.Fatalf("a worker that has never stolen is due")
	}
}
