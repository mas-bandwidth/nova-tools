package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/config"
)

func TestExtractTailnetName(t *testing.T) {
	t.Parallel()

	// 1. Valid DNSName with domain suffix and trailing dot
	data := []byte(`{"Self": {"HostName": "bench-one", "DNSName": "bench-one.tailnet-xyz.ts.net."}}`)
	name, err := ExtractTailnetName(data)
	if err != nil {
		t.Fatalf("expected valid name, got error: %v", err)
	}
	if name != "bench-one" {
		t.Fatalf("expected bench-one, got %q", name)
	}

	// 2. Valid HostName when DNSName is empty
	data2 := []byte(`{"Self": {"HostName": "bench-two", "DNSName": ""}}`)
	name2, err := ExtractTailnetName(data2)
	if err != nil {
		t.Fatalf("expected valid name, got error: %v", err)
	}
	if name2 != "bench-two" {
		t.Fatalf("expected bench-two, got %q", name2)
	}

	// 3. Invalid JSON
	if _, err := ExtractTailnetName([]byte(`{not json}`)); err == nil {
		t.Fatal("expected error on invalid JSON, got nil")
	}

	// 4. Empty name
	if _, err := ExtractTailnetName([]byte(`{"Self": {"HostName": "", "DNSName": ""}}`)); err == nil {
		t.Fatal("expected error on empty name, got nil")
	}

	// 5. Invalid name characters
	if _, err := ExtractTailnetName([]byte(`{"Self": {"HostName": "bad_name!", "DNSName": ""}}`)); err == nil {
		t.Fatal("expected error on invalid name characters, got nil")
	}
}

func TestMachineSyncWithTailscale(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.env["NOVA_PG_DSN"] = dsn
	h.env["NOVA_SPRINT_REDIS"] = "127.0.0.1:6379"
	h.env["NOVA_FRIEND"] = "friend-alpha"

	// Mock tailscale status runner
	h.tailscale = func(_ context.Context) ([]byte, error) {
		return []byte(`{"Self": {"HostName": "worker-bench", "DNSName": "worker-bench.tailnet-1234.ts.net."}}`), nil
	}

	// Add machine to Postgres store
	step := func(wantCode int, args ...string) (string, string) {
		code, out, errs := h.run(t, args...)
		if code != wantCode {
			t.Fatalf("%v: exit %d want %d;\nstdout:\n%s\nstderr:\n%s", args, code, wantCode, out, errs)
		}
		return out, errs
	}

	step(0, "machine", "add", "worker-bench", "--user", "runner", "--seat", "worker-bench", "--slots", "64", "--runners", "1")

	// Machine sync with --tailscale (identity defaulted from tailscale status)
	out, errs := step(0, "machine", "sync", "--tailscale")
	if errs != "" {
		t.Fatalf("expected no stderr, got %q", errs)
	}
	if !strings.Contains(out, "APPLY ADD kind=machine name=worker-bench\n") {
		t.Errorf("stdout missing APPLY ADD: %q", out)
	}
	if !strings.Contains(out, "CONFIG SYNC kind=machine name=worker-bench rev=1\n") {
		t.Errorf("stdout missing CONFIG SYNC: %q", out)
	}

	// Verify Redis holds the machine row
	views, _, err := h.redis.Read(context.Background(), config.KindMachine)
	if err != nil {
		t.Fatal(err)
	}
	if views["worker-bench"] == nil || views["worker-bench"]["slots"] != "64" {
		t.Fatalf("redis missing synced machine worker-bench: %v", views["worker-bench"])
	}
}

func TestMachineSyncWithExplicitName(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.env["NOVA_PG_DSN"] = dsn
	h.env["NOVA_SPRINT_REDIS"] = "127.0.0.1:6379"
	h.env["NOVA_FRIEND"] = "friend-alpha"

	code, out, errs := h.run(t, "machine", "add", "bench-two", "--user", "runner", "--seat", "seat-two", "--slots", "40")
	if code != 0 {
		t.Fatalf("machine add failed: %s %s", out, errs)
	}

	// Sync with explicit name positional argument
	code, out, errs = h.run(t, "machine", "sync", "bench-two")
	if code != 0 {
		t.Fatalf("machine sync failed: %s %s", out, errs)
	}
	if !strings.Contains(out, "APPLY ADD kind=machine name=bench-two\n") {
		t.Errorf("stdout missing APPLY ADD: %q", out)
	}
	if !strings.Contains(out, "CONFIG SYNC kind=machine name=bench-two rev=1\n") {
		t.Errorf("stdout missing CONFIG SYNC: %q", out)
	}
}

func TestMachineSyncIdempotentSameAndFieldUpdate(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.env["NOVA_PG_DSN"] = dsn
	h.env["NOVA_SPRINT_REDIS"] = "127.0.0.1:6379"
	h.env["NOVA_FRIEND"] = "friend-alpha"

	h.run(t, "machine", "add", "worker-bench", "--user", "runner", "--seat", "worker-bench", "--slots", "64")
	h.run(t, "machine", "sync", "worker-bench")

	// Running sync again when Redis matches Postgres prints APPLY SAME
	code, out, errs := h.run(t, "machine", "sync", "worker-bench")
	if code != 0 {
		t.Fatalf("second sync failed: %s %s", out, errs)
	}
	if !strings.Contains(out, "APPLY SAME kind=machine name=worker-bench\n") {
		t.Errorf("expected APPLY SAME in output: %q", out)
	}

	// Update field in Postgres: slots=128
	h.run(t, "machine", "set", "worker-bench", "--slots", "128")

	// Next sync updates Redis: prints APPLY SET
	code, out, errs = h.run(t, "machine", "sync", "worker-bench")
	if code != 0 {
		t.Fatalf("sync after update failed: %s %s", out, errs)
	}
	if !strings.Contains(out, "APPLY SET kind=machine name=worker-bench changed=slots\n") {
		t.Errorf("expected APPLY SET in output: %q", out)
	}
	if !strings.Contains(out, "CONFIG SYNC kind=machine name=worker-bench rev=2\n") {
		t.Errorf("expected CONFIG SYNC in output: %q", out)
	}

	// Redis now holds slots=128
	views, _, _ := h.redis.Read(context.Background(), config.KindMachine)
	if views["worker-bench"]["slots"] != "128" {
		t.Fatalf("expected redis slots 128, got %q", views["worker-bench"]["slots"])
	}
}

func TestMachineSyncCheckMode(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.env["NOVA_PG_DSN"] = dsn
	h.env["NOVA_SPRINT_REDIS"] = "127.0.0.1:6379"
	h.env["NOVA_FRIEND"] = "friend-alpha"

	h.run(t, "machine", "add", "worker-bench", "--user", "runner", "--seat", "worker-bench", "--slots", "64")

	code, out, errs := h.run(t, "machine", "sync", "worker-bench", "--check")
	if code != 0 {
		t.Fatalf("check failed: %s %s", out, errs)
	}
	if !strings.Contains(out, "CHECK ADD kind=machine name=worker-bench\n") {
		t.Errorf("stdout missing CHECK ADD: %q", out)
	}
	if !strings.Contains(out, "CONFIG SYNC kind=machine name=worker-bench rev=1\n") {
		t.Errorf("stdout missing CONFIG SYNC: %q", out)
	}

	// Redis was not written
	views, _, _ := h.redis.Read(context.Background(), config.KindMachine)
	if views["worker-bench"] != nil {
		t.Fatalf("check mode wrote to redis: %v", views["worker-bench"])
	}
}

func TestMachineSyncRefusals(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.env["NOVA_PG_DSN"] = dsn
	h.env["NOVA_SPRINT_REDIS"] = "127.0.0.1:6379"

	// 1. Missing name and no --tailscale
	code, _, errs := h.run(t, "machine", "sync")
	if code != 2 || !strings.Contains(errs, "want <name> or --tailscale") {
		t.Fatalf("expected code 2 with want <name> or --tailscale, got %d: %q", code, errs)
	}

	// 2. Tailscale status runner error
	h.tailscale = func(_ context.Context) ([]byte, error) {
		return nil, errors.New("connection refused")
	}
	code, _, errs = h.run(t, "machine", "sync", "--tailscale")
	if code != 2 || !strings.Contains(errs, "tailscale status: connection refused") {
		t.Fatalf("expected tailscale error refusal, got %d: %q", code, errs)
	}

	// 3. Tailscale invalid json
	h.tailscale = func(_ context.Context) ([]byte, error) {
		return []byte("invalid-json"), nil
	}
	code, _, errs = h.run(t, "machine", "sync", "--tailscale")
	if code != 2 || !strings.Contains(errs, "tailscale status: invalid json") {
		t.Fatalf("expected invalid json refusal, got %d: %q", code, errs)
	}

	// 4. Tailscale empty machine name
	h.tailscale = func(_ context.Context) ([]byte, error) {
		return []byte(`{"Self": {"HostName": "", "DNSName": ""}}`), nil
	}
	code, _, errs = h.run(t, "machine", "sync", "--tailscale")
	if code != 2 || !strings.Contains(errs, "tailscale status reported no machine name") {
		t.Fatalf("expected empty machine name refusal, got %d: %q", code, errs)
	}

	// 5. Machine does not exist in Postgres
	code, _, errs = h.run(t, "machine", "sync", "nonexistent")
	if code != 1 || !strings.Contains(errs, "machine nonexistent does not exist") {
		t.Fatalf("expected machine does not exist refusal, got %d: %q", code, errs)
	}

	// 6. Too many positional arguments
	code, _, errs = h.run(t, "machine", "sync", "foo", "bar")
	if code != 2 || !strings.Contains(errs, "takes at most one machine name") {
		t.Fatalf("expected at most one machine name refusal, got %d: %q", code, errs)
	}
}
