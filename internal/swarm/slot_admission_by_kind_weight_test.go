package swarm

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ISSUE #2033. captainamerica (64 cores, 54 GB) reached load 172 with ~230 schema
// cards in flight; the launcher's guard printed `CAPACITY cores=64 load=108
// allowed=-20` only then, and the bench fell off the network. A load reading is
// a brake after launch. Admission is the slot store, before any child starts:
// card kinds carry a weight (a schema card spawns make/cargo/dotnet; a read card
// does not), and a take that would pass the share in unweighted leases is still
// refused when the remaining share fits only a read.

func TestSlotAdmissionRefusesASchemaCardWhenTheShareFitsOnlyARead(t *testing.T) {
	t.Parallel()

	got, read := SlotAdmissionWeight("schema"), SlotAdmissionWeight("read")
	require.Greater(t, got, read, "a schema card must weigh more than a read card (it spawns compilers); schema=%d read=%d", got, read)
	store := writeSlotStore(t, "capacity\t3\nreserve\t0\nalice\t3\n")
	now := time.Now().UTC()
	pid := os.Getpid()

	ids, held, share, free, _, ok, err := TakeSlotLeasesKind(store, "alice", 1, "schema", time.Hour, "schema-card", now, pid)
	require.NoError(t, err, "TakeSlotLeasesKind: %v", err)
	require.False(t, ok, "a schema card (weight %d) on share 3 must be refused at take, before any child starts; granted=%d held=%d share=%d free=%d",
		SlotAdmissionWeight("schema"), len(ids), held, share, free)
	require.Equal(t, 0, held, "a refused schema take must leave held=0 share=3, got held=%d share=%d free=%d", held, share, free)
	require.Equal(t, 3, share, "a refused schema take must leave held=0 share=3, got held=%d share=%d free=%d", held, share, free)
	leases, err := ListSlotLeases(store, now)
	require.NoError(t, err)
	require.Empty(t, leases, "a refused take must publish no lease, store holds %d", len(leases))

	ids, held, _, _, _, ok, err = TakeSlotLeasesKind(store, "alice", 1, "read", time.Hour, "read-card", now, pid)
	require.NoError(t, err, "read take: %v", err)
	require.True(t, ok, "a read card (weight 1) on share 3 must be granted; ok=%v granted=%d held=%d", ok, len(ids), held)
	require.Len(t, ids, 1, "a read card (weight 1) on share 3 must be granted; ok=%v granted=%d held=%d", ok, len(ids), held)
	require.Equal(t, 1, held, "a read card (weight 1) on share 3 must be granted; ok=%v granted=%d held=%d", ok, len(ids), held)
}

func TestSlotAdmissionChargesSchemaWeightAgainstTheShare(t *testing.T) {
	t.Parallel()

	store := writeSlotStore(t, "capacity\t8\nreserve\t0\nalice\t8\n")
	now := time.Now().UTC()
	pid := os.Getpid()
	w := SlotAdmissionWeight("schema")
	require.GreaterOrEqual(t, w, 2, "schema weight must be at least 2 so two of them can fill a share of 8, got %d", w)

	_, _, _, _, _, ok, err := TakeSlotLeasesKind(store, "alice", 1, "schema", time.Hour, "one", now, pid)
	require.NoError(t, err, "first schema card must grant: ok=%v err=%v", ok, err)
	require.True(t, ok, "first schema card must grant: ok=%v err=%v", ok, err)
	_, held, share, _, _, ok, err := TakeSlotLeasesKind(store, "alice", 1, "schema", time.Hour, "two", now, pid)
	require.NoError(t, err, "second schema card must grant (2×%d = %d against share 8): ok=%v err=%v held=%d", w, 2*w, ok, err, held)
	require.True(t, ok, "second schema card must grant (2×%d = %d against share 8): ok=%v err=%v held=%d", w, 2*w, ok, err, held)
	require.Equal(t, 2*w, held, "after two schema cards held must be %d, got held=%d share=%d", 2*w, held, share)
	require.Equal(t, 8, share, "after two schema cards held must be %d, got held=%d share=%d", 2*w, held, share)
	ids, held, _, _, _, ok, err := TakeSlotLeasesKind(store, "alice", 1, "schema", time.Hour, "three", now, pid)
	require.NoError(t, err, "third take: %v", err)
	require.False(t, ok, "a third schema card must be refused at admission; granted=%d held=%d", len(ids), held)
	require.Equal(t, 2*w, held, "a refused third take must still report held=%d, got %d", 2*w, held)
}

func TestSlotDeadHolderBeforeUntilIsStrandedWithItsLabel(t *testing.T) {
	t.Parallel()

	const deadPid = 2147483647
	if Alive(deadPid, "") {
		t.Skip("dead pid probe is alive here")
	}
	store := writeSlotStore(t, "capacity\t1\nreserve\t0\nalice\t1\n")
	future := time.Now().UTC().Add(time.Hour)
	require.NoError(t, MakeSlotLease(store, "dead-1", "alice", deadPid, "card-schema-7", future))
	now := time.Now().UTC()
	leases, err := ListSlotLeases(store, now)
	require.NoError(t, err)
	require.Len(t, leases, 1, "the lease stays so a manager can re-queue it, got %d", len(leases))
	line := leases[0].Line(now)
	require.Contains(t, line, "stranded=1", "a live-until lease whose pid is gone must be marked stranded, got %q", line)
	require.Contains(t, line, "label=card-schema-7", "a stranded lease must carry its label so a manager can re-queue it, got %q", line)
	ok, _, held, _, _, _ := mustSlotTake(t, store, "alice", 1, time.Hour, "another", now, os.Getpid())
	require.False(t, ok, "a stranded lease still occupies the seat; a take must refuse, held=%d", held)
}

func TestSlotAdmissionWeightByKind(t *testing.T) {
	t.Parallel()

	got := SlotAdmissionWeight("read")
	assert.Equal(t, 1, got, "read weight = %d, want 1", got)
	got = SlotAdmissionWeight("")
	assert.Equal(t, 1, got, "untyped weight = %d, want 1 (existing launches stay one unit)", got)
	for _, kind := range []string{"schema", "schema-leg", "fix-red", "go", "lisp"} {
		got = SlotAdmissionWeight(kind)
		assert.Equal(t, 4, got, "%s weight = %d, want 4", kind, got)
	}
}

func TestCardKindFromTextReadsTypedHeaderAndPullKind(t *testing.T) {
	t.Parallel()

	got := CardKindFromText("RESULT: c1 sha=aaaaaaaaaaaa\nKIND: schema\nbody\n")
	assert.Equal(t, "schema", got, "KIND: header: got %q", got)
	got = CardKindFromText(":kind go\n:repo owner/name\n")
	assert.Equal(t, "go", got, ":kind field: got %q", got)
	got = CardKindFromText("a card with no kind\n")
	assert.Empty(t, got, "untyped: got %q", got)
}
