package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// ISSUE #2033. slots take --kind schema is refused at admission when the remaining
// share fits only a read, and a live-until lease whose pid is gone lists as
// stranded=1 with its label.

func TestSlotsTakeRefusesASchemaKindWhenTheShareFitsOnlyARead(t *testing.T) {
	t.Parallel()

	store := slotShares(t, "capacity\t3\nreserve\t0\nswarm-space\t3\n")
	var out, errb bytes.Buffer
	rc := run([]string{"slots", "take", "--store", store, "--owner", "swarm-space",
		"--n", "1", "--for", "10m", "--label", "schema-card", "--kind", "schema"},
		strings.NewReader(""), &out, &errb, time.Now())
	if rc != 2 {
		t.Fatalf("schema take on share 3 exits 2, got %d:\n%s%s", rc, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "SLOTS REFUSED owner=swarm-space want=4 held=0 share=3") {
		t.Fatalf("the refusal names want=4 against share 3:\n%s", errb.String())
	}
	out.Reset()
	errb.Reset()
	rc = run([]string{"slots", "list", "--store", store}, strings.NewReader(""), &out, &errb, time.Now())
	if rc != 0 {
		t.Fatalf("list after a refused take: %d\n%s%s", rc, out.String(), errb.String())
	}
	if strings.Contains(out.String(), "SLOT ") {
		t.Fatalf("a refused take must publish no lease:\n%s", out.String())
	}

	out.Reset()
	errb.Reset()
	rc = run([]string{"slots", "take", "--store", store, "--owner", "swarm-space",
		"--n", "1", "--for", "10m", "--label", "read-card", "--kind", "read"},
		strings.NewReader(""), &out, &errb, time.Now())
	if rc != 0 {
		t.Fatalf("a read take on share 3 is granted, got %d:\n%s%s", rc, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "SLOTS OK owner=swarm-space granted=1 held=1 share=3") {
		t.Fatalf("read take grants one unit:\n%s", out.String())
	}
}

func TestSlotsListMarksADeadHolderStrandedWithItsLabel(t *testing.T) {
	t.Parallel()

	const deadPid = 2147483647
	if swarm.Alive(deadPid, "") {
		t.Skip("dead pid probe is alive here")
	}
	store := slotShares(t, "capacity\t1\nreserve\t0\nalice\t1\n")
	if err := swarm.MakeSlotLease(store, "dead-1", "alice", deadPid, "card-schema-7", time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	rc := run([]string{"slots", "list", "--store", store}, strings.NewReader(""), &out, &errb, time.Now())
	if rc != 0 {
		t.Fatalf("list: %d\n%s%s", rc, out.String(), errb.String())
	}
	line := out.String()
	if !strings.Contains(line, "stranded=1") {
		t.Fatalf("list marks the dead holder stranded, got:\n%s", line)
	}
	if !strings.Contains(line, "label=card-schema-7") {
		t.Fatalf("stranded lease carries its label, got:\n%s", line)
	}
}
