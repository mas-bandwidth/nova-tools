package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/config"
)

func (h *harness) machine(t *testing.T, name, slots string) {
	t.Helper()
	row := config.Row{Name: name, Fields: map[string]string{"user": "u", "seat": "s", "slots": slots, "runners": "0"}}
	if _, err := h.store.Insert(context.Background(), config.KindMachine, row, "t"); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) friend(t *testing.T, name, slots, host string) {
	t.Helper()
	row := config.Row{Name: name, Fields: map[string]string{"slots": slots, "tiers": "flash", "roles": "builder"}}
	if _, err := h.store.Insert(context.Background(), config.KindFriend, row, "t"); err != nil {
		t.Fatal(err)
	}
	h.redis.hosts[name] = host
}

const tailnetOf = `{"BackendState":"Running","Self":{"HostName":"m1","DNSName":"m1.tail1234.ts.net."}}`

// TestMachineSelfPrintsTheTailnetNameAndOpensNoStore: the name is the
// tailnet's first label when a tailnet runs, else the hostname's; plain self
// reads no store.
func TestMachineSelfPrintsTheTailnetNameAndOpensNoStore(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.tailnet = tailnetOf
	h.hostname = "other.local"
	if code, out, errs := h.run(t, "machine", "self"); code != 0 || out != "m1\n" || errs != "" {
		t.Fatalf("tailnet: %d %q %q", code, out, errs)
	}
	h.tailnet = ""
	if code, out, _ := h.run(t, "machine", "self"); code != 0 || out != "other\n" {
		t.Fatalf("hostname: %d %q", code, out)
	}
	h.env["NOVA_MACHINE"] = "m7"
	if code, out, _ := h.run(t, "machine", "self"); code != 0 || out != "m7\n" {
		t.Fatalf("env: %d %q", code, out)
	}
	if h.opens != 0 {
		t.Fatal("machine self opened the store")
	}
	if code, _, errs := h.run(t, "machine", "self", "m1"); code != 2 || !strings.Contains(errs, "self takes no name") {
		t.Fatalf("a name: %d %q", code, errs)
	}
}

// TestMachineSelfCheckIsTwoWhenTheNameIsNoRowAndThreeWhenUnreadable: --check
// exits 0 for a row, 2 for no row, 3 when the rows or the name cannot be
// read.
func TestMachineSelfCheckIsTwoWhenTheNameIsNoRowAndThreeWhenUnreadable(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.tailnet = tailnetOf
	h.machine(t, "m2", "4")
	code, out, errs := h.run(t, "machine", "self", "--check", "--pg", dsn)
	if code != 2 || out != "" || !strings.Contains(errs, `"m1" is no machine row`) || strings.Count(errs, "\n") != 1 {
		t.Fatalf("no row: %d %q %q", code, out, errs)
	}
	h.machine(t, "m1", "4")
	if code, out, errs := h.run(t, "machine", "self", "--check", "--pg", dsn); code != 0 || out != "m1\n" || errs != "" {
		t.Fatalf("a row: %d %q %q", code, out, errs)
	}
	if code, _, errs := h.run(t, "machine", "self", "--check", "--pg", "postgres://u@closed/nova"); code != 3 || !strings.Contains(errs, "the config cannot be read") {
		t.Fatalf("store down: %d %q", code, errs)
	}
	if code, _, errs := h.run(t, "machine", "self", "--check"); code != 3 || !strings.Contains(errs, "--pg is required") {
		t.Fatalf("no dsn: %d %q", code, errs)
	}
	h2 := newHarness()
	h2.hostname = ".x"
	if code, _, errs := h2.run(t, "machine", "self"); code != 3 || !strings.Contains(errs, "could not be resolved") {
		t.Fatalf("no name: %d %q", code, errs)
	}
}

// TestMachineWidthIsSlotsLessTheFriendsCharged: the query; a machine with no room is no member.
func TestMachineWidthIsSlotsLessTheFriendsCharged(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.machine(t, "m1", "8")
	h.machine(t, "m2", "2")
	h.friend(t, "f1", "3", "m1")
	h.friend(t, "f2", "2", "m2")
	code, out, errs := h.run(t, "machine", "width", "m1", "--pg", dsn, "--redis", "r:1")
	if code != 0 || errs != "" || out != "CONFIG WIDTH machine=m1 width=5 slots=8 charged=3 member=true\n" {
		t.Fatalf("m1: %d %q %q", code, out, errs)
	}
	code, out, _ = h.run(t, "machine", "width", "m2", "--pg", dsn, "--redis", "r:1", "--json")
	if code != 0 || out != `{"machine":"m2","slots":2,"charged":2,"width":0,"member":false}`+"\n" {
		t.Fatalf("m2 json: %d %q", code, out)
	}
	if code, _, errs := h.run(t, "machine", "width", "m9", "--pg", dsn, "--redis", "r:1"); code != 1 || !strings.Contains(errs, "machine m9 not found") {
		t.Fatalf("unknown: %d %q", code, errs)
	}
	// friends with slots and no Redis named: refused, never a wrong width
	if code, _, errs := h.run(t, "machine", "width", "m1", "--pg", dsn); code != 2 || !strings.Contains(errs, "Redis address is needed") {
		t.Fatalf("no redis: %d %q", code, errs)
	}
	if code, _, _ := h.run(t, "machine", "list", "--pg", dsn); code != 0 {
		t.Fatal("the plain list needs no Redis")
	}
}

// TestMachineWidthWithNoFriendsNeedsNoRedis: the width is the ceiling.
func TestMachineWidthWithNoFriendsNeedsNoRedis(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.machine(t, "m1", "6")
	code, out, errs := h.run(t, "machine", "width", "m1", "--pg", dsn)
	if code != 0 || errs != "" || !strings.Contains(out, "width=6 slots=6 charged=0 member=true") {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	if h.redis.opens != 0 {
		t.Fatal("opened Redis with no friend to charge")
	}
}
