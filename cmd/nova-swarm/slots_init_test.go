package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `nova-swarm slots init` (nova-tools#1546). It exists because `native` now refuses to
// launch without a bench slot store, and a remedy of "pass --slots-store <dir>" is not a
// remedy on a bench that has never had one. These tests hold it to three promises: it
// MAKES a store a person can read, it REFUSES to touch one that is already there, and the
// store it writes is one `slots take` actually honours -- which is the only promise that
// matters, because a store the lease code cannot read is a store that grants everything.

func slotsInit(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	rc := run(append([]string{"slots", "init"}, args...), strings.NewReader(""), &stdout, &stderr, time.Now())
	return rc, stdout.String(), stderr.String()
}

func TestSlotsInitMakesAStoreTheLeaseCodeCanRead(t *testing.T) {
	t.Parallel()

	store := filepath.Join(t.TempDir(), "slots-store")

	rc, stdout, stderr := slotsInit(t, "--store", store, "--owner", "swarm-space", "--capacity", "1", "--share", "1")
	require.Equal(t, 0, rc, "init exits %d:\n%s%s", rc, stdout, stderr)
	assert.True(t, strings.HasPrefix(stdout, "SLOTS INIT OK store="), "init prints one SLOTS INIT OK line, got:\n%s", stdout)
	for _, field := range []string{"owner=swarm-space", "capacity=1", "reserve=0", "share=1"} {
		assert.Contains(t, stdout, field, "the OK line does not carry %s:\n%s", field, stdout)
	}

	// The file is compared BYTE FOR BYTE, because its format is a contract with
	// pkg/swarm.loadSlotShares and a stray space is a store that refuses to load.
	raw, err := os.ReadFile(filepath.Join(store, "shares.tsv"))
	require.NoError(t, err)
	got, want := string(raw), "capacity\t1\nreserve\t0\nswarm-space\t1\n"
	assert.Equal(t, want, got, "shares.tsv is\n%q\nwant\n%q", got, want)
	fi, err := os.Stat(filepath.Join(store, "slots"))
	if assert.NoError(t, err, "init makes the slots/ directory a lease is written into: %v", err) {
		assert.True(t, fi.IsDir(), "init makes the slots/ directory a lease is written into: %v", err)
	}
}

// The store init writes is honoured by the lease code: one seat means one lease, and the
// second ask is refused. This is the test that would have caught a shares.tsv written in
// a format loadSlotShares does not read -- a bench that then grants every take.
func TestTheStoreSlotsInitWritesGrantsOneLeaseAndRefusesTheSecond(t *testing.T) {
	t.Parallel()

	store := filepath.Join(t.TempDir(), "slots-store")
	rc, stdout, stderr := slotsInit(t, "--store", store, "--owner", "swarm-space", "--capacity", "1", "--share", "1")
	require.Equal(t, 0, rc, "init exits %d:\n%s%s", rc, stdout, stderr)

	var out, errb bytes.Buffer
	rc = run([]string{"slots", "take", "--store", store, "--owner", "swarm-space", "--n", "1", "--for", "10m", "--label", "first"},
		strings.NewReader(""), &out, &errb, time.Now())
	require.Equal(t, 0, rc, "the first take on a one-seat store is granted, got %d:\n%s%s", rc, out.String(), errb.String())
	assert.Contains(t, out.String(), "SLOTS OK owner=swarm-space granted=1", "the first take says so:\n%s", out.String())

	out.Reset()
	errb.Reset()
	rc = run([]string{"slots", "take", "--store", store, "--owner", "swarm-space", "--n", "1", "--for", "10m", "--label", "second"},
		strings.NewReader(""), &out, &errb, time.Now())
	require.NotEqual(t, 0, rc, "a second lease at capacity 1 is refused, got exit 0:\n%s%s", out.String(), errb.String())
	assert.Contains(t, errb.String(), "SLOTS REFUSED owner=swarm-space want=1 held=1 share=1", "the refusal names the share that is full:\n%s", errb.String())
}

// init creates and never updates: the leases under a live store belong to processes that
// are running right now, and a capacity edited underneath them is a bench that overcommits
// without saying so.
func TestSlotsInitRefusesToOverwriteAStore(t *testing.T) {
	t.Parallel()

	store := filepath.Join(t.TempDir(), "slots-store")
	rc, stdout, stderr := slotsInit(t, "--store", store, "--owner", "swarm-space", "--capacity", "1", "--share", "1")
	require.Equal(t, 0, rc, "init exits %d:\n%s%s", rc, stdout, stderr)
	before, err := os.ReadFile(filepath.Join(store, "shares.tsv"))
	require.NoError(t, err)

	rc, stdout, stderr = slotsInit(t, "--store", store, "--owner", "someone-else", "--capacity", "99", "--share", "99")
	require.Equal(t, 2, rc, "a second init is refused with exit 2, got %d:\n%s%s", rc, stdout, stderr)
	assert.Contains(t, stderr, "SLOTS REFUSED reason=store_exists store=", "the refusal says the store is already there:\n%s", stderr)
	assert.Empty(t, stdout, "a refusal writes nothing to stdout: %q", stdout)
	after, err := os.ReadFile(filepath.Join(store, "shares.tsv"))
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "the refused init changed the store:\nbefore %q\nafter  %q", before, after)
}

// Every flag is required, and one run names every one that is missing: the onboarding
// standard's rule, and the difference between one trip to the shell and four.
func TestSlotsInitWantsAllFourFlags(t *testing.T) {
	t.Parallel()

	rc, stdout, stderr := slotsInit(t)
	require.Equal(t, 2, rc, "init with no flags is refused with exit 2, got %d:\n%s%s", rc, stdout, stderr)
	for _, flag := range []string{"--store", "--owner", "--capacity", "--share"} {
		assert.Contains(t, stderr, flag, "one run names every missing flag; %s is not in:\n%s", flag, stderr)
	}
}

// An owner spelled as one of shares.tsv's two reserved keys would be read back as the
// bench's own capacity or reserve, and the owner would simply not be there.
func TestSlotsInitRefusesAReservedOwnerName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"capacity", "reserve"} {
		store := filepath.Join(t.TempDir(), "slots-store")
		rc, _, stderr := slotsInit(t, "--store", store, "--owner", name, "--capacity", "1", "--share", "1")
		assert.Equal(t, 2, rc, "--owner %q is refused, got exit %d", name, rc)
		assert.Contains(t, stderr, "reserved shares.tsv key", "the refusal for --owner %q says why:\n%s", name, stderr)
		_, err := os.Stat(filepath.Join(store, "shares.tsv"))
		assert.True(t, os.IsNotExist(err), "a refused init writes no shares.tsv: %v", err)
	}
}

// A share wider than the bench can never be met.
func TestSlotsInitRefusesAShareWiderThanTheCapacity(t *testing.T) {
	t.Parallel()

	store := filepath.Join(t.TempDir(), "slots-store")
	rc, _, stderr := slotsInit(t, "--store", store, "--owner", "swarm-space", "--capacity", "2", "--share", "3")
	require.Equal(t, 2, rc, "a share wider than the capacity is refused, got exit %d", rc)
	assert.Contains(t, stderr, "is more than --capacity", "the refusal compares the two numbers:\n%s", stderr)
}
