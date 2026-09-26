package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func slotsRun(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	rc := run(append([]string{"slots"}, args...), strings.NewReader(""), &stdout, &stderr, time.Now())
	return rc, stdout.String(), stderr.String()
}

// The remedy a missing-store refusal prints is a verb that exists: run
// verbatim through the CLI it makes the store, and the same release then
// answers RELEASED on it.
func TestSlotsMissingStoreRemedyRuns(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "no-such-store")
	rc, stdout, stderr := slotsRun(t, "release", "--store", missing, "--owner", "bench-a", "--all")
	if rc == 0 || stdout != "" {
		t.Fatalf("release on a missing store: rc=%d stdout=%q stderr=%q, want a refusal and no RELEASED line", rc, stdout, stderr)
	}
	i := strings.Index(stderr, "nova-swarm slots init ")
	if i < 0 {
		t.Fatalf("the refusal names no slots init:\n%s", stderr)
	}
	remedy := stderr[i:]
	if j := strings.Index(remedy, " makes"); j >= 0 {
		remedy = remedy[:j]
	}
	argv := strings.Fields(remedy)
	if len(argv) < 3 || argv[0] != "nova-swarm" || argv[1] != "slots" {
		t.Fatalf("remedy %q is not a nova-swarm slots verb", remedy)
	}
	var out, errb bytes.Buffer
	if rc := run(argv[1:], strings.NewReader(""), &out, &errb, time.Now()); rc != 0 || !strings.HasPrefix(out.String(), "SLOTS INIT OK store="+missing) {
		t.Fatalf("the remedy %q run verbatim: rc=%d\n%s%s", remedy, rc, out.String(), errb.String())
	}
	rc, stdout, stderr = slotsRun(t, "release", "--store", missing, "--owner", "bench-a", "--all")
	if rc != 0 || !strings.HasPrefix(stdout, "SLOTS RELEASED owner=bench-a released=0 held=0 live=0") {
		t.Fatalf("release on the store the remedy made: rc=%d stdout=%q stderr=%q", rc, stdout, stderr)
	}
	// list writes the remedy with the owner left for the person, since it has none.
	rc, stdout, stderr = slotsRun(t, "list", "--store", filepath.Join(t.TempDir(), "also-missing"))
	if rc == 0 || stdout != "" || !strings.Contains(stderr, "--owner <name> --capacity 1 --share 1") {
		t.Fatalf("list on a missing store: rc=%d stdout=%q stderr=%q", rc, stdout, stderr)
	}
}

// `slots list` prints an unreadable lease as a CORRUPT row on stdout, beside
// the good ones, and still exits 0: the listing succeeded, and the row is
// the fact.
func TestSlotsListPrintsACorruptLeaseRow(t *testing.T) {
	t.Parallel()
	store := filepath.Join(t.TempDir(), "store")
	if rc, out, errb := slotsRun(t, "init", "--store", store, "--owner", "bench-a", "--capacity", "2", "--share", "2"); rc != 0 {
		t.Fatalf("init: rc=%d\n%s%s", rc, out, errb)
	}
	if rc, out, errb := slotsRun(t, "take", "--store", store, "--owner", "bench-a", "--n", "1", "--for", "1h", "--label", "card-1"); rc != 0 {
		t.Fatalf("take: rc=%d\n%s%s", rc, out, errb)
	}
	if err := os.MkdirAll(filepath.Join(store, "slots", "bare-lease"), 0o755); err != nil {
		t.Fatal(err)
	}
	rc, stdout, stderr := slotsRun(t, "list", "--store", store)
	if rc != 0 || stderr != "" {
		t.Fatalf("list: rc=%d stderr=%q", rc, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("list printed %d rows, want the lease and the CORRUPT row:\n%s", len(lines), stdout)
	}
	if !strings.HasPrefix(lines[0], "SLOT bare-lease state=CORRUPT reason=") || !strings.Contains(lines[0], "the next slots take reaps it") {
		t.Errorf("the corrupt row: %s", lines[0])
	}
	if !strings.Contains(lines[1], "owner=bench-a") || !strings.Contains(lines[1], "label=card-1") || strings.Contains(lines[1], "CORRUPT") {
		t.Errorf("the good row: %s", lines[1])
	}
}
