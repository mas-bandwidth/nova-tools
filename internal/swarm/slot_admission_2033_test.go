package swarm

import (
	"os"
	"strings"
	"testing"
	"time"
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

	if got, read := SlotAdmissionWeight("schema"), SlotAdmissionWeight("read"); got <= read {
		t.Fatalf("a schema card must weigh more than a read card (it spawns compilers); schema=%d read=%d", got, read)
	}
	store := writeSlotStore(t, "capacity\t3\nreserve\t0\nalice\t3\n")
	now := time.Now().UTC()
	pid := os.Getpid()

	ids, held, share, free, _, ok, err := TakeSlotLeasesKind(store, "alice", 1, "schema", time.Hour, "schema-card", now, pid)
	if err != nil {
		t.Fatalf("TakeSlotLeasesKind: %v", err)
	}
	if ok {
		t.Fatalf("a schema card (weight %d) on share 3 must be refused at take, before any child starts; granted=%d held=%d share=%d free=%d",
			SlotAdmissionWeight("schema"), len(ids), held, share, free)
	}
	if held != 0 || share != 3 {
		t.Fatalf("a refused schema take must leave held=0 share=3, got held=%d share=%d free=%d", held, share, free)
	}
	leases, err := ListSlotLeases(store, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 0 {
		t.Fatalf("a refused take must publish no lease, store holds %d", len(leases))
	}

	ids, held, _, _, _, ok, err = TakeSlotLeasesKind(store, "alice", 1, "read", time.Hour, "read-card", now, pid)
	if err != nil {
		t.Fatalf("read take: %v", err)
	}
	if !ok || len(ids) != 1 || held != 1 {
		t.Fatalf("a read card (weight 1) on share 3 must be granted; ok=%v granted=%d held=%d", ok, len(ids), held)
	}
}

func TestSlotAdmissionChargesSchemaWeightAgainstTheShare(t *testing.T) {
	t.Parallel()

	store := writeSlotStore(t, "capacity\t8\nreserve\t0\nalice\t8\n")
	now := time.Now().UTC()
	pid := os.Getpid()
	w := SlotAdmissionWeight("schema")
	if w < 2 {
		t.Fatalf("schema weight must be at least 2 so two of them can fill a share of 8, got %d", w)
	}

	if _, _, _, _, _, ok, err := TakeSlotLeasesKind(store, "alice", 1, "schema", time.Hour, "one", now, pid); err != nil || !ok {
		t.Fatalf("first schema card must grant: ok=%v err=%v", ok, err)
	}
	ids, held, share, _, _, ok, err := TakeSlotLeasesKind(store, "alice", 1, "schema", time.Hour, "two", now, pid)
	if err != nil || !ok {
		t.Fatalf("second schema card must grant (2×%d = %d against share 8): ok=%v err=%v held=%d", w, 2*w, ok, err, held)
	}
	if held != 2*w || share != 8 {
		t.Fatalf("after two schema cards held must be %d, got held=%d share=%d", 2*w, held, share)
	}
	ids, held, _, _, _, ok, err = TakeSlotLeasesKind(store, "alice", 1, "schema", time.Hour, "three", now, pid)
	if err != nil {
		t.Fatalf("third take: %v", err)
	}
	if ok {
		t.Fatalf("a third schema card must be refused at admission; granted=%d held=%d", len(ids), held)
	}
	if held != 2*w {
		t.Fatalf("a refused third take must still report held=%d, got %d", 2*w, held)
	}
}

func TestSlotDeadHolderBeforeUntilIsStrandedWithItsLabel(t *testing.T) {
	t.Parallel()

	const deadPid = 2147483647
	if Alive(deadPid, "") {
		t.Skip("dead pid probe is alive here")
	}
	store := writeSlotStore(t, "capacity\t1\nreserve\t0\nalice\t1\n")
	future := time.Now().UTC().Add(time.Hour)
	if err := MakeSlotLease(store, "dead-1", "alice", deadPid, "card-schema-7", future); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	leases, err := ListSlotLeases(store, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 1 {
		t.Fatalf("the lease stays so a manager can re-queue it, got %d", len(leases))
	}
	line := leases[0].Line(now)
	if !strings.Contains(line, "stranded=1") {
		t.Fatalf("a live-until lease whose pid is gone must be marked stranded, got %q", line)
	}
	if !strings.Contains(line, "label=card-schema-7") {
		t.Fatalf("a stranded lease must carry its label so a manager can re-queue it, got %q", line)
	}
	ok, _, held, _, _, _ := mustSlotTake(t, store, "alice", 1, time.Hour, "another", now, os.Getpid())
	if ok {
		t.Fatalf("a stranded lease still occupies the seat; a take must refuse, held=%d", held)
	}
}

func TestSlotAdmissionWeightByKind(t *testing.T) {
	t.Parallel()

	if got := SlotAdmissionWeight("read"); got != 1 {
		t.Errorf("read weight = %d, want 1", got)
	}
	if got := SlotAdmissionWeight(""); got != 1 {
		t.Errorf("untyped weight = %d, want 1 (existing launches stay one unit)", got)
	}
	for _, kind := range []string{"schema", "schema-leg", "fix-red", "go", "lisp"} {
		if got := SlotAdmissionWeight(kind); got != 4 {
			t.Errorf("%s weight = %d, want 4", kind, got)
		}
	}
}

func TestCardKindFromTextReadsTypedHeaderAndPullKind(t *testing.T) {
	t.Parallel()

	if got := CardKindFromText("RESULT: c1 sha=aaaaaaaaaaaa\nKIND: schema\nbody\n"); got != "schema" {
		t.Errorf("KIND: header: got %q", got)
	}
	if got := CardKindFromText(":kind go\n:repo owner/name\n"); got != "go" {
		t.Errorf(":kind field: got %q", got)
	}
	if got := CardKindFromText("a card with no kind\n"); got != "" {
		t.Errorf("untyped: got %q", got)
	}
}
