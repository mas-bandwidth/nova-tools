package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestCapacityAsActorControlReceipt exercises the specified --as control flag
// through the CLI, not merely through the Go capacity API. The actor must be
// preserved in the server-timed cap:log receipt.
func TestCapacityAsActorControlReceipt(t *testing.T) {
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := runCapacity(ctx, []string{"machine", "--redis", addr, "--as", "operator", "ctl-machine", "64"}, &out, &errOut); code != 0 {
		t.Fatalf("capacity machine code=%d stderr=%q", code, errOut.String())
	}
	client.SAdd(ctx, "friends", "alice")
	out.Reset()
	errOut.Reset()
	if code := runCapacity(ctx, []string{"friend", "--redis", addr, "--as", "operator", "--machine", "ctl-machine", "alice", "32"}, &out, &errOut); code != 0 {
		t.Fatalf("capacity friend code=%d stderr=%q", code, errOut.String())
	}
	entries, err := client.XRange(ctx, "cap:log", "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("receipts=%d want 2", len(entries))
	}
	for _, entry := range entries {
		if entry.Values["actor"] != "operator" {
			t.Fatalf("receipt actor=%v", entry.Values["actor"])
		}
	}
}

// TestL20bRunnerHooks exercises the CI runner hooks ACTIONS_RUNNER_HOOK_JOB_STARTED
// and ACTIONS_RUNNER_HOOK_JOB_COMPLETED through the CLI verb `capacity hook`.
func TestL20bRunnerHooks(t *testing.T) {
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	// Set machine with cores and mem-gb to establish budget (spec 5.1: 90%)
	// 32 cores -> 28800 cpu_milli; 64 GB -> 58982 mem_mb
	if code := runCapacity(ctx, []string{"machine", "--redis", addr, "--as", "operator", "--cores", "32", "--mem-gb", "64", "ctl-machine", "64"}, &out, &errOut); code != 0 {
		t.Fatalf("capacity machine code=%d stderr=%q", code, errOut.String())
	}

	// 1. Hook job-started takes budget for CI job
	out.Reset()
	errOut.Reset()
	code := runCapacity(ctx, []string{
		"hook", "--redis", addr,
		"--machine", "ctl-machine",
		"--consumer", "ci-runner-job-123",
		"--cpu-milli", "4000",
		"--mem-mb", "8192",
		"job-started",
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("hook job-started code=%d stderr=%q", code, errOut.String())
	}
	if !strings.Contains(out.String(), "HOOK started ci-runner-job-123") {
		t.Fatalf("unexpected hook started output: %q", out.String())
	}

	// Verify debit in Redis
	debitState := client.HGet(ctx, "machine:ctl-machine:debit:ci-runner-job-123", "state").Val()
	if debitState != "live" {
		t.Fatalf("debit state=%q want live", debitState)
	}

	// 2. An overcommitting take is refused
	out.Reset()
	errOut.Reset()
	code = runCapacity(ctx, []string{
		"take", "--redis", addr,
		"--machine", "ctl-machine",
		"--consumer", "huge-job",
		"--cpu-milli", "30000", // exceeds remaining ~24800
		"--mem-mb", "10000",
	}, &out, &errOut)
	if code != 2 {
		t.Fatalf("expected exit code 2 for overcommit, got %d", code)
	}
	if !strings.Contains(errOut.String(), "NOBUDGET") {
		t.Fatalf("expected NOBUDGET error, got %q", errOut.String())
	}

	// 3. Hook job-completed gives budget back
	out.Reset()
	errOut.Reset()
	code = runCapacity(ctx, []string{
		"hook", "--redis", addr,
		"--machine", "ctl-machine",
		"--consumer", "ci-runner-job-123",
		"job-completed",
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("hook job-completed code=%d stderr=%q", code, errOut.String())
	}
	if !strings.Contains(out.String(), "HOOK completed ci-runner-job-123") {
		t.Fatalf("unexpected hook completed output: %q", out.String())
	}

	// Verify debit removed
	exists := client.Exists(ctx, "machine:ctl-machine:debit:ci-runner-job-123").Val()
	if exists != 0 {
		t.Fatalf("debit still exists after completed hook")
	}
}
