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

// TestConcurrentStaleReclaimersWithReplacement asserts that when a reclaimer observes a stale
// claim and prepares to reclaim it, but another process replaces the claim with a fresh live
// claim before the reclaimer acquires the reclaim lock, the reclaimer detects the changed token
// and does NOT unlink the replacement claim.
func TestConcurrentStaleReclaimersWithReplacement(t *testing.T) {
	bench := t.TempDir()
	plantCard(t, bench, "replaced")

	taken := TakenDir(bench)
	if err := os.MkdirAll(taken, 0o755); err != nil {
		t.Fatal(err)
	}

	claim := filepath.Join(taken, "replaced.claim")
	oldStamp := time.Now().UnixNano() - 2*int64(ClaimTimeout)
	staleContent := fmt.Sprintf("stale-worker 99999 %d stale-token-123\n", oldStamp)
	if err := os.WriteFile(claim, []byte(staleContent), 0o644); err != nil {
		t.Fatal(err)
	}

	freshToken := "fresh-token-456"
	freshContent := fmt.Sprintf("fresh-worker %d %d %s\n", os.Getpid(), time.Now().UnixNano(), freshToken)

	// Hook simulates a concurrent claimant replacing the stale claim between the reclaimer's
	// initial observation and its reclaim lock acquisition.
	beforeReclaimLockHook = func() {
		_ = os.WriteFile(claim, []byte(freshContent), 0o644)
	}
	t.Cleanup(func() { beforeReclaimLockHook = nil })

	name, ok, err := TakeCard(bench, "reclaimer")
	if err != nil {
		t.Fatalf("TakeCard failed: %v", err)
	}
	if ok || name != "" {
		t.Fatalf("TakeCard = (%q, %t), want empty/false because claim was replaced by fresh live worker", name, ok)
	}

	// Verify the fresh claim on disk was NOT removed.
	raw, err := os.ReadFile(claim)
	if err != nil {
		t.Fatalf("claim file was removed: %v", err)
	}
	if string(raw) != freshContent {
		t.Fatalf("claim content = %q, want fresh content %q", string(raw), freshContent)
	}

	// Card remains in queue because reclaimer was rejected.
	left, err := QueueCards(bench)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0] != "replaced" {
		t.Fatalf("queue cards = %v, want [replaced]", left)
	}
}

// TestFencedWorkerCannotRenameOrRelease simulates a slow or suspended worker whose claim
// expired or was reclaimed. When the worker resumes, the owner-fencing check prevents it
// from renaming the card, and releaseClaim refuses to remove the replacement claim.
func TestFencedWorkerCannotRenameOrRelease(t *testing.T) {
	bench := t.TempDir()
	plantCard(t, bench, "fenced")

	taken := TakenDir(bench)
	if err := os.MkdirAll(taken, 0o755); err != nil {
		t.Fatal(err)
	}

	// Simulate slow worker w_slow claiming the card.
	tokenSlow := "token-slow-111"
	claim := filepath.Join(taken, "fenced.claim")
	slowContent := fmt.Sprintf("w_slow %d %d %s\n", os.Getpid(), time.Now().UnixNano(), tokenSlow)
	if err := os.WriteFile(claim, []byte(slowContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// While w_slow is paused, w_fast reclaims the card with tokenFast.
	tokenFast := "token-fast-222"
	fastContent := fmt.Sprintf("w_fast %d %d %s\n", os.Getpid(), time.Now().UnixNano(), tokenFast)
	if err := os.WriteFile(claim, []byte(fastContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// When w_slow wakes up, holdsClaim must return false (fenced).
	if holdsClaim(taken, "fenced", tokenSlow) {
		t.Fatalf("holdsClaim(tokenSlow) = true, want false (fenced by tokenFast)")
	}

	// w_slow calls releaseClaim with its old token; it must NOT unlink the claim held by tokenFast.
	releaseClaim(taken, "fenced", tokenSlow)
	raw, err := os.ReadFile(claim)
	if err != nil {
		t.Fatalf("claim was unlinked by slow worker: %v", err)
	}
	if string(raw) != fastContent {
		t.Fatalf("claim content = %q, want %q", string(raw), fastContent)
	}

	// When w_fast releases its matching token, the claim is cleanly removed.
	releaseClaim(taken, "fenced", tokenFast)
	if _, err := os.Stat(claim); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("claim was not unlinked by owner: err=%v", err)
	}
}

// TestEmptyOrTruncatedClaimRecovery asserts that an empty (0-byte) or truncated claim file
// left by a crashed process is safely recovered without stalling the card or crashing.
func TestEmptyOrTruncatedClaimRecovery(t *testing.T) {
	bench := t.TempDir()
	taken := TakenDir(bench)
	if err := os.MkdirAll(taken, 0o755); err != nil {
		t.Fatal(err)
	}

	// 1. Zero-byte empty claim file
	plantCard(t, bench, "empty")
	emptyClaim := filepath.Join(taken, "empty.claim")
	if err := os.WriteFile(emptyClaim, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	name, ok, err := TakeCard(bench, "w1")
	if err != nil {
		t.Fatalf("TakeCard with empty claim failed: %v", err)
	}
	if !ok || name != "empty" {
		t.Fatalf("TakeCard = (%q, %t), want (empty, true)", name, ok)
	}

	// 2. Truncated malformed claim file (dead PID)
	plantCard(t, bench, "truncated")
	truncClaim := filepath.Join(taken, "truncated.claim")
	if err := os.WriteFile(truncClaim, []byte("crashed-worker 99999\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	name2, ok2, err2 := TakeCard(bench, "w2")
	if err2 != nil {
		t.Fatalf("TakeCard with truncated claim failed: %v", err2)
	}
	if !ok2 || name2 != "truncated" {
		t.Fatalf("TakeCard = (%q, %t), want (truncated, true)", name2, ok2)
	}
}

// TestLiveAgedOwnerNotReclaimed proves that a claim older than ClaimTimeout whose
// process is still alive is NEVER reclaimed by another worker.
func TestLiveAgedOwnerNotReclaimed(t *testing.T) {
	bench := t.TempDir()
	plantCard(t, bench, "live-aged")

	taken := TakenDir(bench)
	if err := os.MkdirAll(taken, 0o755); err != nil {
		t.Fatal(err)
	}

	// Claim file is aged well beyond ClaimTimeout, but the owning PID is os.Getpid() (alive).
	claim := filepath.Join(taken, "live-aged.claim")
	agedTime := time.Now().Add(-5 * ClaimTimeout).UnixNano()
	tokenLive := "token-live-worker-1"
	content := fmt.Sprintf("w_live %d %d %s\n", os.Getpid(), agedTime, tokenLive)
	if err := os.WriteFile(claim, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// Another worker attempts to take the card; it must not reclaim the live owner.
	name, ok, err := TakeCard(bench, "w_other")
	if err != nil {
		t.Fatalf("TakeCard failed: %v", err)
	}
	if ok {
		t.Fatalf("TakeCard = (%q, true), want false: live owner must not be reclaimed regardless of age", name)
	}

	// The original claim must remain unmolested.
	raw, err := os.ReadFile(claim)
	if err != nil {
		t.Fatalf("claim file missing: %v", err)
	}
	if string(raw) != content {
		t.Fatalf("claim content = %q, want %q", string(raw), content)
	}

	// Once the owner is dead (e.g. deadPid), TakeCard succeeds and reclaims.
	const deadPid = 2147483647
	if Alive(deadPid, "") {
		t.Skip("dead pid probe is alive here")
	}
	deadContent := fmt.Sprintf("w_dead %d %d %s\n", deadPid, agedTime, "token-dead-worker-2")
	if err := os.WriteFile(claim, []byte(deadContent), 0o644); err != nil {
		t.Fatal(err)
	}

	name2, ok2, err2 := TakeCard(bench, "w_other")
	if err2 != nil {
		t.Fatalf("TakeCard failed: %v", err2)
	}
	if !ok2 || name2 != "live-aged" {
		t.Fatalf("TakeCard = (%q, %t), want (live-aged, true)", name2, ok2)
	}
}

// TestCardLockExcludesConcurrentTakeCard asserts that while takeCardLock is held on a card,
// any concurrent worker attempting TakeCard is excluded and cannot claim or take the card.
func TestCardLockExcludesConcurrentTakeCard(t *testing.T) {
	bench := t.TempDir()
	plantCard(t, bench, "locked-card")

	taken := TakenDir(bench)
	if err := os.MkdirAll(taken, 0o755); err != nil {
		t.Fatal(err)
	}

	origWait := cardLockWait
	cardLockWait = 50 * time.Millisecond
	t.Cleanup(func() { cardLockWait = origWait })

	// Acquire card lock externally to simulate an active critical section.
	unlock, err := takeCardLock(taken, "locked-card", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("takeCardLock failed: %v", err)
	}

	// Concurrent TakeCard attempt times out waiting for lock and returns ok=false.
	name, ok, err := TakeCard(bench, "w_concurrent")
	if err != nil {
		t.Fatalf("TakeCard during locked state returned unexpected err: %v", err)
	}
	if ok {
		t.Fatalf("TakeCard = (%q, true), want false while card lock is held", name)
	}

	// After unlocking, TakeCard succeeds immediately.
	unlock()

	name2, ok2, err2 := TakeCard(bench, "w_concurrent")
	if err2 != nil {
		t.Fatalf("TakeCard after unlock failed: %v", err2)
	}
	if !ok2 || name2 != "locked-card" {
		t.Fatalf("TakeCard after unlock = (%q, %t), want (locked-card, true)", name2, ok2)
	}
}

// TestCheckToActionReplacementBlocked proves that between claim validation and rename,
// takeCardLock excludes concurrent callers from replacing or stealing the claim.
func TestCheckToActionReplacementBlocked(t *testing.T) {
	bench := t.TempDir()
	plantCard(t, bench, "fenced-action")

	taken := TakenDir(bench)
	if err := os.MkdirAll(taken, 0o755); err != nil {
		t.Fatal(err)
	}

	origWait := cardLockWait
	cardLockWait = 50 * time.Millisecond
	t.Cleanup(func() { cardLockWait = origWait })

	hookCalled := false
	interloperBlocked := false

	betweenClaimAndRenameHook = func() {
		hookCalled = true
		// Interloper attempts to steal or take the card while winner is between claim and rename.
		name, ok, err := TakeCard(bench, "interloper")
		if err == nil && !ok && name == "" {
			interloperBlocked = true
		}
	}
	t.Cleanup(func() { betweenClaimAndRenameHook = nil })

	name, ok, err := TakeCard(bench, "winner")
	if err != nil {
		t.Fatalf("TakeCard winner failed: %v", err)
	}
	if !ok || name != "fenced-action" {
		t.Fatalf("TakeCard winner = (%q, %t), want (fenced-action, true)", name, ok)
	}
	if !hookCalled {
		t.Fatalf("betweenClaimAndRenameHook was not called")
	}
	if !interloperBlocked {
		t.Fatalf("interloper was not blocked by takeCardLock during check-to-action window")
	}
}

// TestTakeCardFailsOnLockDirError verifies that if .locks is a regular file (or cannot be
// initialized), TakeCard returns the filesystem error rather than swallowing it as contention.
func TestTakeCardFailsOnLockDirError(t *testing.T) {
	bench := t.TempDir()
	plantCard(t, bench, "one")

	// Make victim/.locks a regular file
	locksPath := filepath.Join(bench, ".locks")
	if err := os.WriteFile(locksPath, []byte("regular file"), 0o644); err != nil {
		t.Fatal(err)
	}

	name, ok, err := TakeCard(bench, "stella-review")
	if err == nil {
		t.Fatalf("TakeCard on corrupt lock dir returned err=nil, want error (name=%q, ok=%t)", name, ok)
	}
	if ok || name != "" {
		t.Fatalf("TakeCard on corrupt lock dir took card (%q, %t), want empty/false", name, ok)
	}

	// Card remains queued
	cards, qErr := QueueCards(bench)
	if qErr != nil || len(cards) != 1 || cards[0] != "one" {
		t.Fatalf("queue cards = %v, qErr = %v, want ['one'] preserved", cards, qErr)
	}
}

// TestStealFailsOnLockDirError verifies that if victim/.locks is a regular file,
// Steal propagates the error immediately without hanging or retrying indefinitely.
func TestStealFailsOnLockDirError(t *testing.T) {
	victim := t.TempDir()
	plantCard(t, victim, "one")

	locksPath := filepath.Join(victim, ".locks")
	if err := os.WriteFile(locksPath, []byte("regular file"), 0o644); err != nil {
		t.Fatal(err)
	}

	stolen, err := Steal(victim, "stella-review", 0)
	if err == nil {
		t.Fatalf("Steal on corrupt lock dir returned err=nil, want error (stolen=%v)", stolen)
	}
	if len(stolen) != 0 {
		t.Fatalf("Steal on corrupt lock dir returned stolen=%v, want none", stolen)
	}

	// Card remains queued
	cards, qErr := QueueCards(victim)
	if qErr != nil || len(cards) != 1 || cards[0] != "one" {
		t.Fatalf("queue cards = %v, qErr = %v, want ['one'] preserved", cards, qErr)
	}
}

// TestStealAdvancesPastContinuouslyHeldCardLock proves that if the first card is continuously
// locked by another process, Steal advances to steal subsequent candidate cards boundedly
// without retrying the first card in place.
func TestStealAdvancesPastContinuouslyHeldCardLock(t *testing.T) {
	victim := t.TempDir()
	plantCard(t, victim, "first")
	plantCard(t, victim, "second")

	origWait := cardLockWait
	cardLockWait = 50 * time.Millisecond
	t.Cleanup(func() { cardLockWait = origWait })

	// Pre-create taken/ and continuously hold the lock on "first"
	taken := TakenDir(victim)
	if err := os.MkdirAll(taken, 0o755); err != nil {
		t.Fatal(err)
	}
	unlockFirst, err := takeCardLock(taken, "first", cardLockWait)
	if err != nil {
		t.Fatalf("takeCardLock for first failed: %v", err)
	}
	t.Cleanup(unlockFirst)

	// Steal with capacity 0: victim has 2 cards, so 2 cards are wanted.
	// Because "first" is continuously held, Steal advances past it, steals "second",
	// and finishes boundedly.
	stolen, err := Steal(victim, "thief", 0)
	if err != nil {
		t.Fatalf("Steal failed: %v", err)
	}
	if len(stolen) != 1 || stolen[0] != "second" {
		t.Fatalf("Steal = %v, want ['second'] (first was skipped due to lock contention)", stolen)
	}

	// "first" remains queued in victim
	cards, qErr := QueueCards(victim)
	if qErr != nil || len(cards) != 1 || cards[0] != "first" {
		t.Fatalf("victim queue = %v, want ['first'] preserved", cards)
	}
}

// TestStealRefreshesCapacityBetweenTakes verifies Stella's regression witness:
// with cards a, b, c, d at capacity 2, holding a.lock, launching steal in a goroutine,
// concurrently renaming d into taken, and then releasing a.lock:
// Steal must take only 'a' and leave 'b' and 'c' queued (2 queued cards, respecting capacity 2).
func TestStealRefreshesCapacityBetweenTakes(t *testing.T) {
	victim := t.TempDir()
	plantCard(t, victim, "a")
	plantCard(t, victim, "b")
	plantCard(t, victim, "c")
	plantCard(t, victim, "d")

	taken := TakenDir(victim)
	if err := os.MkdirAll(taken, 0o755); err != nil {
		t.Fatal(err)
	}

	origWait := cardLockWait
	cardLockWait = 500 * time.Millisecond
	t.Cleanup(func() { cardLockWait = origWait })

	// Pre-lock 'a'
	unlockA, err := takeCardLock(taken, "a", cardLockWait)
	if err != nil {
		t.Fatalf("takeCardLock for 'a' failed: %v", err)
	}

	type stealResult struct {
		stolen []string
		err    error
	}
	done := make(chan stealResult, 1)

	go func() {
		stolen, sErr := Steal(victim, "thief", 2)
		done <- stealResult{stolen: stolen, err: sErr}
	}()

	// Small pause so steal starts and begins waiting on 'a' lock
	time.Sleep(50 * time.Millisecond)

	// Concurrently rename 'd' into taken/
	queue := QueueDir(victim)
	if err := os.Rename(filepath.Join(queue, "d"+CardExt), filepath.Join(taken, "other-d"+CardExt)); err != nil {
		t.Fatalf("concurrent rename of d failed: %v", err)
	}

	// Release 'a' lock so steal can acquire 'a'
	unlockA()

	res := <-done
	if res.err != nil {
		t.Fatalf("Steal failed: %v", res.err)
	}
	if len(res.stolen) != 1 || res.stolen[0] != "a" {
		t.Fatalf("Steal = %v, want ['a'] (capacity reduced by concurrent rename of d)", res.stolen)
	}

	cards, qErr := QueueCards(victim)
	if qErr != nil {
		t.Fatalf("QueueCards failed: %v", qErr)
	}
	if len(cards) != 2 || cards[0] != "b" || cards[1] != "c" {
		t.Fatalf("remaining queue = %v, want ['b', 'c'] (exactly capacity 2 preserved)", cards)
	}
}
