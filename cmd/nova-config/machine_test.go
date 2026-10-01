package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/stretchr/testify/require"
)

func (h *harness) machine(t *testing.T, name, slots string) {
	t.Helper()
	row := config.Row{Name: name, Fields: map[string]string{"user": "u", "seat": "s", "slots": slots, "runners": "0"}}
	_, err := h.store.Insert(context.Background(), config.KindMachine, row, "t")
	require.NoError(t, err)
}

func (h *harness) friend(t *testing.T, name, slots, host string) {
	t.Helper()
	row := config.Row{Name: name, Fields: map[string]string{"slots": slots, "tiers": "flash", "roles": "builder"}}
	_, err := h.store.Insert(context.Background(), config.KindFriend, row, "t")
	require.NoError(t, err)
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
		require.FailNow(t, fmt.Sprintf("tailnet: %d %q %q", code, out, errs))
	}
	h.tailnet = ""
	if code, out, _ := h.run(t, "machine", "self"); code != 0 || out != "other\n" {
		require.FailNow(t, fmt.Sprintf("hostname: %d %q", code, out))
	}
	h.env["NOVA_MACHINE"] = "m7"
	if code, out, _ := h.run(t, "machine", "self"); code != 0 || out != "m7\n" {
		require.FailNow(t, fmt.Sprintf("env: %d %q", code, out))
	}
	if h.opens != 0 {
		require.FailNow(t, "machine self opened the store")
	}
	if code, _, errs := h.run(t, "machine", "self", "m1"); code != 2 || !strings.Contains(errs, "self takes no name") {
		require.FailNow(t, fmt.Sprintf("a name: %d %q", code, errs))
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
		require.FailNow(t, fmt.Sprintf("no row: %d %q %q", code, out, errs))
	}
	h.machine(t, "m1", "4")
	if code, out, errs := h.run(t, "machine", "self", "--check", "--pg", dsn); code != 0 || out != "m1\n" || errs != "" {
		require.FailNow(t, fmt.Sprintf("a row: %d %q %q", code, out, errs))
	}
	if code, _, errs := h.run(t, "machine", "self", "--check", "--pg", "postgres://u@closed/nova"); code != 3 || !strings.Contains(errs, "the config cannot be read") {
		require.FailNow(t, fmt.Sprintf("store down: %d %q", code, errs))
	}
	if code, _, errs := h.run(t, "machine", "self", "--check"); code != 3 || !strings.Contains(errs, "--pg is required") {
		require.FailNow(t, fmt.Sprintf("no dsn: %d %q", code, errs))
	}
	h2 := newHarness()
	h2.hostname = ".x"
	if code, _, errs := h2.run(t, "machine", "self"); code != 3 || !strings.Contains(errs, "could not be resolved") {
		require.FailNow(t, fmt.Sprintf("no name: %d %q", code, errs))
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
		require.FailNow(t, fmt.Sprintf("m1: %d %q %q", code, out, errs))
	}
	code, out, _ = h.run(t, "machine", "width", "m2", "--pg", dsn, "--redis", "r:1", "--json")
	if code != 0 || out != `{"machine":"m2","slots":2,"charged":2,"width":0,"member":false}`+"\n" {
		require.FailNow(t, fmt.Sprintf("m2 json: %d %q", code, out))
	}
	if code, _, errs := h.run(t, "machine", "width", "m9", "--pg", dsn, "--redis", "r:1"); code != 1 || !strings.Contains(errs, "machine m9 not found") {
		require.FailNow(t, fmt.Sprintf("unknown: %d %q", code, errs))
	}
	// friends with slots and no Redis named: refused, never a wrong width
	if code, _, errs := h.run(t, "machine", "width", "m1", "--pg", dsn); code != 2 || !strings.Contains(errs, "Redis address is needed") {
		require.FailNow(t, fmt.Sprintf("no redis: %d %q", code, errs))
	}
	if code, _, _ := h.run(t, "machine", "list", "--pg", dsn); code != 0 {
		require.FailNow(t, "the plain list needs no Redis")
	}
}

// TestMachineWidthWithNoFriendsNeedsNoRedis: the width is the ceiling.
func TestMachineWidthWithNoFriendsNeedsNoRedis(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.machine(t, "m1", "6")
	code, out, errs := h.run(t, "machine", "width", "m1", "--pg", dsn)
	if code != 0 || errs != "" || !strings.Contains(out, "width=6 slots=6 charged=0 member=true") {
		require.FailNow(t, fmt.Sprintf("%d %q %q", code, out, errs))
	}
	if h.redis.opens != 0 {
		require.FailNow(t, "opened Redis with no friend to charge")
	}
}

// getDown is a store whose row reads fail.
type getDown struct{ *memStore }

func (getDown) Get(context.Context, string, string) (config.Row, bool, error) {
	return config.Row{}, false, errors.New("postgres: connection reset")
}

// TestMachineSelfCheckIsThreeWhenTheRowCannotBeRead: the read of the machine
// row failing is 3, never the 2 of "no such row".
func TestMachineSelfCheckIsThreeWhenTheRowCannotBeRead(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.tailnet = tailnetOf
	h.machine(t, "m1", "4")
	h.override = getDown{h.store}
	code, out, errs := h.run(t, "machine", "self", "--check", "--pg", dsn)
	if code != 3 || out != "" || !strings.Contains(errs, "the config cannot be read: postgres: connection reset") {
		require.FailNow(t, fmt.Sprintf("%d %q %q", code, out, errs))
	}
}
