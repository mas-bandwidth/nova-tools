package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// THE SLOTS OWNER IS NEVER A MACHINE FACT (SPEC-SWARM.md "Bench slot leases", the law at
// docs/SPEC-SWARM.md:2400-2446). An owner is a caller-supplied identity, and the tool's own
// code already says so: every `--owner` in scope is defined as `f.fs.String("owner", "", "")`
// and then handed to `f.want`, so the default is the empty string and it is ALWAYS required
// (cmd/nova-swarm/main.go:528,776,969,1660 and cmd/nova-swarm/slots.go:49,120,159). The
// design comment above `cmdNative`'s flags says it outright (cmd/nova-swarm/main.go:1656-1658):
// "--slots-store names the store and --owner whose share the one lease per run counts against.
// BOTH ARE REQUIRED: see swarm.NoSlotsStoreRefusal for why there is no optional mode and no
// default."
//
// The receipt that prompted this pin is operational, not in-repo: fleet/HANDOFF.md ADDENDUM 3
// §F.3 and NOTE-fleet-slots.md item 1 (2026-09-19) record a script that derived the owner from
// `hostname` on a bench whose hostname is `spacegame.losangeles`, yielding `swarm-spacegame`,
// an owner with share 0, and `held=0 share=0` on every take. This test is the tool-side half
// of "never from the hostname": it holds `slots take` to the refusal when `--owner` is omitted,
// so a future patch cannot silently invent an owner from the hostname (or anything else) and
// reach the capacity store with a name the bench never gave it.
func TestSlotsTakeNeverDefaultsTheOwnerWhenNoneIsGiven(t *testing.T) {
	store := filepath.Join(t.TempDir(), "slots-store")
	if err := os.MkdirAll(filepath.Join(store, "slots"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "shares.tsv"),
		[]byte("capacity\t8\nreserve\t0\nswarm-space\t8\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	rc := run([]string{"slots", "take", "--store", store, "--n", "1", "--for", "1h"},
		strings.NewReader(""), &stdout, &stderr, time.Now())

	if rc != 2 {
		t.Fatalf("omitting --owner is refused with exit 2, got %d:\nstdout:%s\nstderr:%s", rc, stdout.String(), stderr.String())
	}
	if stdout.String() != "" {
		t.Errorf("a refusal writes nothing to stdout, got:\n%q", stdout.String())
	}
	want := "nova-swarm slots take: --owner is required; it wants whose share the leases count against; refusing to guess\n"
	if stderr.String() != want {
		t.Errorf("omitting --owner refuses before any owner-shaped value exists:\n got %q\nwant %q", stderr.String(), want)
	}
}

// TestNoFileInThisPackageReadsHostname is the source test idiom this lane uses elsewhere: read
// the CURRENT package directory (which is where `go test` runs from), skip directories and
// `_test.go`, and count the non-test `.go` lines carrying the literal `os.Hostname(`. The
// enumeration this card is built on -- `git grep -n "Hostname\|hostname" -- internal/swarm
// cmd/nova-swarm` at its measured head -- finds none in `cmd/nova-swarm`; this test makes that
// zero permanent, so no `cmdSlotsTake` (or any other command here) can start guessing an owner
// from the machine it happens to run on.
func TestNoFileInThisPackageReadsHostname(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	hits := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if strings.Contains(line, "os.Hostname(") {
				hits++
				t.Logf("os.Hostname( at %s:%d: %s", name, i+1, strings.TrimSpace(line))
			}
		}
	}
	if hits != 0 {
		t.Errorf("no file in cmd/nova-swarm reads the hostname, and the owner is never a machine fact; found %d os.Hostname( call(s)", hits)
	}
}
