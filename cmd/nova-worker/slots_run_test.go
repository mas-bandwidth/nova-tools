package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// RED TESTS FOR #880 ITEM 18: `nova-worker run` HOLDS A BENCH SLOT LEASE PER TASK.
//
// The slots verbs already exist (docs/SPEC-WORKER.md, "Bench slot leases"): a store
// with shares.tsv, take/release/list, expiry fenced by live pids. What is missing is
// the launcher's side of the contract -- `run --slots-store <dir> --owner <name>`
// takes one lease before each task starts and releases it when the task ends. A
// refused take is a wait, never a launch past the share. These tests drive the real
// binary against the fake harness on a temp store; nothing reaches the network.

// slotShares writes a shares.tsv for these tests and returns the store directory.
func slotShares(t *testing.T, body string) string {
	t.Helper()
	store := filepath.Join(t.TempDir(), "slots-store")
	require.NoError(t, os.MkdirAll(store, 0o755))
	write(t, filepath.Join(store, "shares.tsv"), body)
	return store
}

// slotLeaseCount counts the lease directories the store holds right now.
func slotLeaseCount(t *testing.T, store string) int {
	t.Helper()
	return slotLeaseCountNow(store)
}

func slotLeaseCountNow(store string) int {
	entries, err := os.ReadDir(filepath.Join(store, "slots"))
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			n++
		}
	}
	return n
}

// nativeStore is the one-seat bench slot store a `native` launch now needs: since
// nova-tools#1546 a launch without a lease is REFUSED, so every test in this package that
// runs native passes `--slots-store nativeStore(t) --owner fake-1`. It is a function
// rather than a fixture so that each test gets a store of its own under its own
// t.TempDir(), and two tests running in parallel never contend for the same seat.
func nativeStore(t *testing.T) string {
	t.Helper()
	return slotShares(t, "capacity\t1\nreserve\t0\nfake-1\t1\n")
}

// TestSlotsRunSerializesAndReleases proves two concurrent runners have maximum
// critical-section occupancy one, and proves failure and cancellation leave the
// slot reusable (docs/SPEC-WORKER.md, "Bench slot leases").
func TestSlotsRunSerializesAndReleases(t *testing.T) {
	t.Parallel()

	store := slotShares(t, "capacity\t1\nreserve\t0\ngate-owner\t1\n")
	dir := t.TempDir()
	logPath := filepath.Join(dir, "occupancy.log")

	scriptPath := filepath.Join(dir, "section.sh")
	scriptContent := `#!/bin/sh
echo "enter $1" >> "$2"
sleep 0.02
echo "exit $1" >> "$2"
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(scriptContent), 0o755))

	var wg sync.WaitGroup
	codes := make([]int, 2)
	errOutputs := make([]string, 2)

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			var out, errs bytes.Buffer
			tag := strconv.Itoa(id + 1)
			codes[id] = run([]string{
				"slots", "run",
				"--store", store,
				"--owner", "gate-owner",
				"--wait", "5s",
				"--", scriptPath, tag, logPath,
			}, strings.NewReader(""), &out, &errs, time.Now())
			errOutputs[id] = errs.String()
		}(i)
	}
	wg.Wait()

	require.Equal(t, 0, codes[0], "runner 1 must exit 0: %s", errOutputs[0])
	require.Equal(t, 0, codes[1], "runner 2 must exit 0: %s", errOutputs[1])

	raw, err := os.ReadFile(logPath)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	require.Len(t, lines, 4, "must have 2 enters and 2 exits: %v", lines)

	occupancy := 0
	maxOccupancy := 0
	for _, l := range lines {
		fields := strings.Fields(l)
		require.Len(t, fields, 2)
		switch fields[0] {
		case "enter":
			occupancy++
			if occupancy > maxOccupancy {
				maxOccupancy = occupancy
			}
		case "exit":
			occupancy--
		}
	}
	require.Equal(t, 1, maxOccupancy, "maximum critical-section occupancy must be 1, got %d", maxOccupancy)
	require.Equal(t, 0, occupancy, "final occupancy must be 0")
	require.Equal(t, 0, slotLeaseCount(t, store), "store must hold no leases after normal exit")

	// Failure leaves the slot reusable.
	var failOut, failErrs bytes.Buffer
	failCode := run([]string{
		"slots", "run",
		"--store", store,
		"--owner", "gate-owner",
		"--", "/bin/sh", "-c", "exit 42",
	}, strings.NewReader(""), &failOut, &failErrs, time.Now())
	require.Equal(t, 42, failCode, "failed command exit code must be propagated")
	require.Equal(t, 0, slotLeaseCount(t, store), "store must hold no leases after command failure")

	var reuseOut, reuseErrs bytes.Buffer
	reuseCode := run([]string{
		"slots", "run",
		"--store", store,
		"--owner", "gate-owner",
		"--", "/bin/sh", "-c", "exit 0",
	}, strings.NewReader(""), &reuseOut, &reuseErrs, time.Now())
	require.Equal(t, 0, reuseCode, "runner after failure must succeed: %s", reuseErrs.String())
	require.Equal(t, 0, slotLeaseCount(t, store), "store must hold no leases after reuse exit")

	// Cancellation leaves the slot reusable.
	cancelCtx, cancel := context.WithCancel(t.Context())
	var cancelOut, cancelErrs bytes.Buffer
	cancelDone := make(chan int, 1)
	go func() {
		cancelDone <- cmdSlotsRunContext(cancelCtx, []string{
			"--store", store,
			"--owner", "gate-owner",
			"--", "sleep", "5",
		}, &cancelOut, &cancelErrs)
	}()

	require.Eventually(t, func() bool {
		return slotLeaseCount(t, store) == 1
	}, 2*time.Second, 10*time.Millisecond, "lease must be acquired before cancellation")

	cancel()
	cCode := <-cancelDone
	require.NotEqual(t, 0, cCode, "cancelled command must not exit 0")
	require.Equal(t, 0, slotLeaseCount(t, store), "store must hold no leases after cancellation")

	var postCancelOut, postCancelErrs bytes.Buffer
	postCancelCode := run([]string{
		"slots", "run",
		"--store", store,
		"--owner", "gate-owner",
		"--", "/bin/sh", "-c", "exit 0",
	}, strings.NewReader(""), &postCancelOut, &postCancelErrs, time.Now())
	require.Equal(t, 0, postCancelCode, "runner after cancellation must succeed: %s", postCancelErrs.String())
	require.Equal(t, 0, slotLeaseCount(t, store), "store must hold no leases after post-cancel exit")

	// Refusal when capacity is occupied names the holder and remedy without deleting the live lease.
	sentinel := filepath.Join(dir, "holder.release")
	holderDone := make(chan int, 1)
	go func() {
		var hOut, hErrs bytes.Buffer
		holderDone <- run([]string{
			"slots", "run",
			"--store", store,
			"--owner", "gate-owner",
			"--", "/bin/sh", "-c", `while [ ! -f "$1" ]; do sleep 0.01; done`, "sh", sentinel,
		}, strings.NewReader(""), &hOut, &hErrs, time.Now())
	}()

	require.Eventually(t, func() bool {
		return slotLeaseCount(t, store) == 1
	}, 2*time.Second, 10*time.Millisecond, "holder lease must be recorded")

	var refOut, refErrs bytes.Buffer
	refCode := run([]string{
		"slots", "run",
		"--store", store,
		"--owner", "gate-owner",
		"--wait", "0s",
		"--", "/bin/sh", "-c", "exit 0",
	}, strings.NewReader(""), &refOut, &refErrs, time.Now())

	require.Equal(t, 2, refCode, "refused run must exit 2")
	require.Contains(t, refErrs.String(), "SLOTS REFUSED", "refusal line must start with SLOTS REFUSED")
	require.Contains(t, refErrs.String(), "holders=gate-owner:1", "refusal must name current holder")
	require.Contains(t, refErrs.String(), `remedy="nova-worker slots list --store `+store+`"`, "refusal must provide concrete remedy")
	require.Equal(t, 1, slotLeaseCount(t, store), "refusal must not delete the live holder lease")

	require.NoError(t, os.WriteFile(sentinel, []byte("ok"), 0o644))
	hCode := <-holderDone
	require.Equal(t, 0, hCode, "holder command must exit 0")
	require.Equal(t, 0, slotLeaseCount(t, store), "store must hold no leases after holder finishes")
}
