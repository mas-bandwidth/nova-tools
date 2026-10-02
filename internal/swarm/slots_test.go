package swarm

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func writeSlotStore(t *testing.T, shares string) string {
	t.Helper()
	store := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(store, "shares.tsv"), []byte(shares), 0o644))
	return store
}

func mustSlotTake(t *testing.T, store, owner string, k int, d time.Duration, label string, now time.Time, pid int) (bool, int, int, int, int, string) {
	t.Helper()
	ids, held, share, free, holders, ok, err := TakeSlotLeases(store, owner, k, d, label, now, pid)
	require.NoError(t, err, "TakeSlotLeases: %v", err)
	return ok, len(ids), held, share, free, holders
}

// Two owners at share 2 each with capacity 4 reserve 0 cannot take a fifth.
func TestSlotSharesRefuseFifth(t *testing.T) {
	t.Parallel()

	store := writeSlotStore(t, "capacity\t4\nreserve\t0\nalice\t2\nbob\t2\n")
	now := time.Now().UTC()
	pid := os.Getpid()
	ok, _, _, _, _, _ := mustSlotTake(t, store, "alice", 2, time.Hour, "", now, pid)
	require.True(t, ok, "alice take 2 should grant")
	ok, _, _, _, _, _ = mustSlotTake(t, store, "bob", 2, time.Hour, "", now, pid)
	require.True(t, ok, "bob take 2 should grant")
	ok, _, held, share, free, holders := mustSlotTake(t, store, "alice", 1, time.Hour, "", now, pid)
	require.False(t, ok, "fifth lease must refuse: held=%d share=%d free=%d holders=%s", held, share, free, holders)
	require.Equal(t, 2, held, "refused counts wrong: held=%d share=%d free=%d holders=%s", held, share, free, holders)
	require.Equal(t, 2, share, "refused counts wrong: held=%d share=%d free=%d holders=%s", held, share, free, holders)
	require.Equal(t, 0, free, "refused counts wrong: held=%d share=%d free=%d holders=%s", held, share, free, holders)
	require.Equal(t, "alice:2,bob:2", holders, "holders must name both owners, got %q", holders)
}

// An expired lease with a dead pid is reaped and its slot granted.
func TestSlotExpiredDeadPidReaped(t *testing.T) {
	t.Parallel()

	const deadPid = 2147483647
	if Alive(deadPid, "") {
		t.Skip("dead pid probe is alive here")
	}
	store := writeSlotStore(t, "capacity\t2\nreserve\t0\nalice\t2\n")
	past := time.Now().UTC().Add(-time.Hour)
	require.NoError(t, MakeSlotLease(store, "old-1", "alice", deadPid, "", past))
	now := time.Now().UTC()
	ok, granted, _, _, _, _ := mustSlotTake(t, store, "alice", 1, time.Hour, "", now, os.Getpid())
	require.True(t, ok, "expired dead lease must be reaped and granted: ok=%v granted=%d", ok, granted)
	require.Equal(t, 1, granted, "expired dead lease must be reaped and granted: ok=%v granted=%d", ok, granted)
	_, err := os.Stat(filepath.Join(store, "slots", "old-1"))
	require.True(t, os.IsNotExist(err), "reaped lease dir must be gone")
	leases, err := ListSlotLeases(store, now)
	require.NoError(t, err)
	require.Len(t, leases, 1, "one fresh lease must remain: %+v", leases)
	require.Equal(t, "alice", leases[0].Owner, "one fresh lease must remain: %+v", leases)
}

// An expired lease with the test's live pid stays and is printed DRIFT.
func TestSlotExpiredLivePidDrift(t *testing.T) {
	t.Parallel()

	store := writeSlotStore(t, "capacity\t2\nreserve\t0\nalice\t1\n")
	past := time.Now().UTC().Add(-time.Hour)
	require.NoError(t, MakeSlotLease(store, "drift-1", "alice", os.Getpid(), "", past))
	now := time.Now().UTC()
	leases, err := ListSlotLeases(store, now)
	require.NoError(t, err)
	require.Len(t, leases, 1, "drift lease must stay: %+v", leases)
	got := leases[0].State(now)
	require.Equal(t, "DRIFT", got, "expired live lease state must be DRIFT, got %q", got)
	ok, _, held, _, _, _ := mustSlotTake(t, store, "alice", 1, time.Hour, "", now, os.Getpid())
	require.False(t, ok, "DRIFT lease counts as held and must refuse past share")
	require.Equal(t, 1, held, "held must count DRIFT, got %d", held)
	var line string
	for _, l := range leases {
		line = l.Line(now)
	}
	require.Contains(t, line, "state=DRIFT", "list line must print DRIFT, got %q", line)
}

// Take past capacity-reserve is refused naming holders.
func TestSlotCapacityReserveRefused(t *testing.T) {
	t.Parallel()

	store := writeSlotStore(t, "capacity\t4\nreserve\t1\nalice\t10\nbob\t10\n")
	now := time.Now().UTC()
	pid := os.Getpid()
	ok, _, _, _, _, _ := mustSlotTake(t, store, "alice", 2, time.Hour, "", now, pid)
	require.True(t, ok, "alice take 2 should grant")
	ok, _, _, _, _, _ = mustSlotTake(t, store, "bob", 1, time.Hour, "", now, pid)
	require.True(t, ok, "bob take 1 should grant (total 3 = capacity-reserve)")
	ok, _, _, _, free, holders := mustSlotTake(t, store, "bob", 1, time.Hour, "", now, pid)
	require.False(t, ok, "take past capacity-reserve must refuse")
	require.Equal(t, 0, free, "free must be 0 at capacity-reserve, got %d", free)
	require.Equal(t, "alice:2,bob:1", holders, "refusal must name holders, got %q", holders)
}
