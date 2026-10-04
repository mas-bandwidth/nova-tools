package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
	require.Equal(t, 2, rc, "schema take on share 3 exits 2, got %d:\n%s%s", rc, out.String(), errb.String())
	require.Contains(t, errb.String(), "SLOTS REFUSED owner=swarm-space want=4 held=0 share=3", "the refusal names want=4 against share 3:\n%s", errb.String())
	out.Reset()
	errb.Reset()
	rc = run([]string{"slots", "list", "--store", store}, strings.NewReader(""), &out, &errb, time.Now())
	require.Equal(t, 0, rc, "list after a refused take: %d\n%s%s", rc, out.String(), errb.String())
	require.NotContains(t, out.String(), "SLOT ", "a refused take must publish no lease:\n%s", out.String())

	out.Reset()
	errb.Reset()
	rc = run([]string{"slots", "take", "--store", store, "--owner", "swarm-space",
		"--n", "1", "--for", "10m", "--label", "read-card", "--kind", "read"},
		strings.NewReader(""), &out, &errb, time.Now())
	require.Equal(t, 0, rc, "a read take on share 3 is granted, got %d:\n%s%s", rc, out.String(), errb.String())
	require.Contains(t, out.String(), "SLOTS OK owner=swarm-space granted=1 held=1 share=3", "read take grants one unit:\n%s", out.String())
}

func TestSlotsListMarksADeadHolderStrandedWithItsLabel(t *testing.T) {
	t.Parallel()

	const deadPid = 2147483647
	if swarm.Alive(deadPid, "") {
		t.Skip("dead pid probe is alive here")
	}
	store := slotShares(t, "capacity\t1\nreserve\t0\nalice\t1\n")
	require.NoError(t, swarm.MakeSlotLease(store, "dead-1", "alice", deadPid, "card-schema-7", time.Now().UTC().Add(time.Hour)))
	var out, errb bytes.Buffer
	rc := run([]string{"slots", "list", "--store", store}, strings.NewReader(""), &out, &errb, time.Now())
	require.Equal(t, 0, rc, "list: %d\n%s%s", rc, out.String(), errb.String())
	line := out.String()
	require.Contains(t, line, "stranded=1", "list marks the dead holder stranded, got:\n%s", line)
	require.Contains(t, line, "label=card-schema-7", "stranded lease carries its label, got:\n%s", line)
}
