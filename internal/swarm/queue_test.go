package swarm

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

// A RENAME THAT COLLIDES IS NOT A CARD THAT WAS TAKEN, AND A CARD THAT IS GONE IS.
//
// TakeCard takes a card by renaming queue/<name>.card into taken/, which on Windows is a
// MoveFileEx replace: while another worker's rename of the same source is delete-pending,
// this rename fails ERROR_ACCESS_DENIED (5), and a bare os.Rename read that transient as a
// fact and returned it, so the whole take failed instead of losing the race. The collision
// is waited out here through the package's forceTransientIO seam over a destination that is
// a DIRECTORY -- this package's portable stand-in for a pending replace -- and it ends a
// few polls later, far inside SteadyWindow.
func TestTakeCardWaitsOutAReplaceCollision(t *testing.T) {
	bench := t.TempDir()
	plantCard(t, bench, "only")

	dst := filepath.Join(TakenDir(bench), "w0-only"+CardExt)
	hits := collideUntilRename(t, dst)

	name, ok, err := TakeCard(bench, "w0")
	if err != nil {
		t.Fatalf("a rename collision while taking a card was read as a fact: %v", err)
	}
	if !ok || name != "only" {
		t.Fatalf("TakeCard = (%q, %t), want (only, true): the collision is waited out and the card taken", name, ok)
	}
	if hits.Load() == 0 {
		t.Error("no rename of this card ever went through the collision wait")
	}
	left, err := QueueCards(bench)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("queue still holds %v after a collision was waited out", left)
	}
}

// THE STEAL'S RENAME MEETS THE SAME WINDOWS COLLISION, and the victim's line is only
// respected once the rename LANDS: a transient refusal must not end the steal.
func TestStealWaitsOutAReplaceCollision(t *testing.T) {
	victim := t.TempDir()
	plantCard(t, victim, "only")

	dst := filepath.Join(TakenDir(victim), "thief-only"+CardExt)
	hits := collideUntilRename(t, dst)

	stolen, err := Steal(victim, "thief", 0)
	if err != nil {
		t.Fatalf("a rename collision while stealing was read as a fact: %v", err)
	}
	if len(stolen) != 1 || stolen[0] != "only" {
		t.Fatalf("stole %v, want [only]: the collision is waited out and the card taken", stolen)
	}
	if hits.Load() == 0 {
		t.Error("no rename of this card ever went through the collision wait")
	}
}

// collideUntilRename makes the first few renames onto `dst` fail and then LIFTS the
// collision, so the rule is asserted with no Windows and no race. `dst` becomes a
// non-empty DIRECTORY -- the portable stand-in for the microseconds a Windows replace is
// pending -- and the rename meets the two things a real collision has: a rename that fails,
// and a path that holds still again a few polls later, far inside SteadyWindow. The
// stand-in is lifted from INSIDE the seam on the third failure, so the rename that paid for
// the wait is by construction the one whose retry lands; the counter is narrowed to this
// path so the guard names the rename it means.
func collideUntilRename(t *testing.T, dst string) *atomic.Int64 {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dst, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int64
	forceTransientIO = func(err error) bool {
		if err == nil {
			return false
		}
		var le *os.LinkError
		if !errors.As(err, &le) || filepath.Clean(le.New) != filepath.Clean(dst) {
			return transientIO(err)
		}
		if hits.Add(1) >= 3 {
			_ = os.RemoveAll(dst)
		}
		return true
	}
	t.Cleanup(func() { forceTransientIO = nil })
	return &hits
}

// TestTwoProcessesCannotTakeOneCard proves cross-process exclusion across independent worker
// processes on the same bench. Two OS subprocesses race for one card; exactly one wins and takes
// it, while the loser observes that the card is claimed/gone and exits clean.
func TestTwoProcessesCannotTakeOneCard(t *testing.T) {
	if os.Getenv("TEST_SUBPROCESS_TAKE_CARD") == "1" {
		bench := os.Getenv("TEST_BENCH_DIR")
		worker := os.Getenv("TEST_WORKER_NAME")
		_, ok, err := TakeCard(bench, worker)
		if err != nil {
			os.Exit(2)
		}
		if ok {
			os.Exit(0)
		}
		os.Exit(1)
	}

	bench := t.TempDir()
	plantCard(t, bench, "race")

	cmd1 := exec.Command(os.Args[0], "-test.run=^TestTwoProcessesCannotTakeOneCard$")
	cmd1.Env = append(os.Environ(),
		"TEST_SUBPROCESS_TAKE_CARD=1",
		"TEST_BENCH_DIR="+bench,
		"TEST_WORKER_NAME=proc0",
	)

	cmd2 := exec.Command(os.Args[0], "-test.run=^TestTwoProcessesCannotTakeOneCard$")
	cmd2.Env = append(os.Environ(),
		"TEST_SUBPROCESS_TAKE_CARD=1",
		"TEST_BENCH_DIR="+bench,
		"TEST_WORKER_NAME=proc1",
	)

	var wg sync.WaitGroup
	var code1, code2 int
	wg.Add(2)
	go func() {
		defer wg.Done()
		err := cmd1.Run()
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				code1 = ee.ExitCode()
				return
			}
			code1 = -1
		}
	}()
	go func() {
		defer wg.Done()
		err := cmd2.Run()
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				code2 = ee.ExitCode()
				return
			}
			code2 = -1
		}
	}()
	wg.Wait()

	zeros := 0
	if code1 == 0 {
		zeros++
	}
	if code2 == 0 {
		zeros++
	}
	if zeros != 1 {
		t.Fatalf("exit codes = (%d, %d), want exactly one 0 exit: exactly one process wins", code1, code2)
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
		t.Fatalf("taken holds %d entries, want 1", len(taken))
	}
	if !strings.HasSuffix(taken[0].Name(), "-race"+CardExt) {
		t.Fatalf("ownership record = %q, want *-race.card", taken[0].Name())
	}
}

// TestInterruptedClaimRecovery asserts that a stale claim file left by a crashed or killed
// worker is cleanly broken after ClaimTimeout, allowing the waiting card to be recovered.
func TestInterruptedClaimRecovery(t *testing.T) {
	bench := t.TempDir()
	plantCard(t, bench, "crashed")

	taken := TakenDir(bench)
	if err := os.MkdirAll(taken, 0o755); err != nil {
		t.Fatal(err)
	}

	claim := filepath.Join(taken, "crashed.claim")
	oldStamp := time.Now().Unix() - 2*int64(ClaimTimeout/time.Second)
	if err := os.WriteFile(claim, []byte(fmt.Sprintf("crashed-worker 99999 %d\n", oldStamp)), 0o644); err != nil {
		t.Fatal(err)
	}

	name, ok, err := TakeCard(bench, "w1")
	if err != nil {
		t.Fatalf("take with stale claim: %v", err)
	}
	if !ok || name != "crashed" {
		t.Fatalf("TakeCard = (%q, %t), want (crashed, true)", name, ok)
	}

	left, err := QueueCards(bench)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("queue still holds %v, want empty", left)
	}

	owned, err := OwnedCards(bench, "w1")
	if err != nil {
		t.Fatal(err)
	}
	if len(owned) != 1 || owned[0] != "crashed" {
		t.Fatalf("owned = %v, want [crashed]", owned)
	}
}
