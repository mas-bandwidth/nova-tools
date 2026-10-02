package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/stretchr/testify/require"
)

// machine makes a machine row with this width; its slots are a number unlike
// any width, so a width read from them would be seen.
func (h *harness) machine(t *testing.T, name, width string) {
	t.Helper()
	row := config.Row{Name: name, Fields: map[string]string{"user": "u", "seat": "s", "slots": "160", "runners": "0", "width": width}}
	_, err := h.store.Insert(context.Background(), config.KindMachine, row, "t")
	require.NoError(t, err)
}

func (h *harness) friend(t *testing.T, name, slots string) {
	t.Helper()
	row := config.Row{Name: name, Fields: map[string]string{"slots": slots, "tiers": "flash", "roles": "builder"}}
	_, err := h.store.Insert(context.Background(), config.KindFriend, row, "t")
	require.NoError(t, err)
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
	codeCheck39, outCheck39, errsCheck39 := h.run(t, "machine", "self")
	require.Equal(t, 0, codeCheck39, "tailnet: %d %q %q", codeCheck39, outCheck39, errsCheck39)
	require.Equal(t, "m1\n", outCheck39, "tailnet: %d %q %q", codeCheck39, outCheck39, errsCheck39)
	require.Equal(t, "", errsCheck39, "tailnet: %d %q %q", codeCheck39, outCheck39, errsCheck39)
	h.tailnet = ""
	codeCheck43, outCheck43, _ := h.run(t, "machine", "self")
	require.Equal(t, 0, codeCheck43, "hostname: %d %q", codeCheck43, outCheck43)
	require.Equal(t, "other\n", outCheck43, "hostname: %d %q", codeCheck43, outCheck43)
	h.env["NOVA_MACHINE"] = "m7"
	codeCheck47, outCheck47, _ := h.run(t, "machine", "self")
	require.Equal(t, 0, codeCheck47, "env: %d %q", codeCheck47, outCheck47)
	require.Equal(t, "m7\n", outCheck47, "env: %d %q", codeCheck47, outCheck47)
	require.Equal(t, 0, h.opens, "machine self opened the store")
	codeCheck53, _, errsCheck53 := h.run(t, "machine", "self", "m1")
	require.Equal(t, 2, codeCheck53, "a name: %d %q", codeCheck53, errsCheck53)
	require.Contains(t, errsCheck53, "self takes no name", "a name: %d %q", codeCheck53, errsCheck53)
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
	require.Equal(t, 2, code, "no row: %d %q %q", code, out, errs)
	require.Equal(t, "", out, "no row: %d %q %q", code, out, errs)
	require.Contains(t, errs, `"m1" is no machine row`, "no row: %d %q %q", code, out, errs)
	require.Equal(t, 1, strings.Count(errs, "\n"), "no row: %d %q %q", code, out, errs)
	h.machine(t, "m1", "4")
	codeCheck71, outCheck71, errsCheck71 := h.run(t, "machine", "self", "--check", "--pg", dsn)
	require.Equal(t, 0, codeCheck71, "a row: %d %q %q", codeCheck71, outCheck71, errsCheck71)
	require.Equal(t, "m1\n", outCheck71, "a row: %d %q %q", codeCheck71, outCheck71, errsCheck71)
	require.Equal(t, "", errsCheck71, "a row: %d %q %q", codeCheck71, outCheck71, errsCheck71)
	codeCheck74, _, errsCheck74 := h.run(t, "machine", "self", "--check", "--pg", "postgres://u@closed/nova")
	require.Equal(t, 3, codeCheck74, "store down: %d %q", codeCheck74, errsCheck74)
	require.Contains(t, errsCheck74, "the config cannot be read", "store down: %d %q", codeCheck74, errsCheck74)
	codeCheck77, _, errsCheck77 := h.run(t, "machine", "self", "--check")
	require.Equal(t, 3, codeCheck77, "no dsn: %d %q", codeCheck77, errsCheck77)
	require.Contains(t, errsCheck77, "--pg is required", "no dsn: %d %q", codeCheck77, errsCheck77)
	h2 := newHarness()
	h2.hostname = ".x"
	codeCheck82, _, errsCheck82 := h2.run(t, "machine", "self")
	require.Equal(t, 3, codeCheck82, "no name: %d %q", codeCheck82, errsCheck82)
	require.Contains(t, errsCheck82, "could not be resolved", "no name: %d %q", codeCheck82, errsCheck82)
}

// TestMachineWidthPrintsTheRowsWidth: the query prints the width the row
// carries, whatever its slots and whatever friend rows carry slots; width 0
// is no member; it opens no Redis, even with one named in the environment.
func TestMachineWidthPrintsTheRowsWidth(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.env["NOVA_SPRINT_REDIS"] = "r:1"
	h.machine(t, "m1", "32")
	h.machine(t, "m2", "0")
	h.friend(t, "f1", "32")
	h.friend(t, "f2", "32")
	code, out, errs := h.run(t, "machine", "width", "m1", "--pg", dsn)
	require.Equal(t, 0, code, "m1: %d %q %q", code, out, errs)
	require.Equal(t, "", errs, "m1: %d %q %q", code, out, errs)
	require.Equal(t, "CONFIG WIDTH machine=m1 width=32 member=true\n", out, "m1: %d %q %q", code, out, errs)
	code, out, _ = h.run(t, "machine", "width", "m2", "--pg", dsn, "--json")
	require.Equal(t, 0, code, "m2 json: %d %q", code, out)
	require.Equal(t, `{"machine":"m2","width":0,"member":false}`+"\n", out, "m2 json: %d %q", code, out)
	codeCheck103, _, errsCheck103 := h.run(t, "machine", "width", "m9", "--pg", dsn)
	require.Equal(t, 1, codeCheck103, "unknown: %d %q", codeCheck103, errsCheck103)
	require.Contains(t, errsCheck103, "machine m9 not found", "unknown: %d %q", codeCheck103, errsCheck103)
	require.Equal(t, 0, h.redis.opens, "machine width opened Redis")
}

// TestMachineSetWidthIsWhatWidthPrints: a width set with machine set is the
// width machine width prints, and it moves no other machine's.
func TestMachineSetWidthIsWhatWidthPrints(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.machine(t, "m1", "16")
	h.machine(t, "m2", "16")
	code, out, errs := h.run(t, "machine", "set", "m1", "--width", "32", "--pg", dsn, "--as", "t")
	require.Equal(t, 0, code, "set: %d %q %q", code, out, errs)
	_, out, _ = h.run(t, "machine", "width", "m1", "--pg", dsn)
	require.Equal(t, "CONFIG WIDTH machine=m1 width=32 member=true\n", out)
	_, out, _ = h.run(t, "machine", "width", "m2", "--pg", dsn)
	require.Equal(t, "CONFIG WIDTH machine=m2 width=16 member=true\n", out)
	_, out, _ = h.run(t, "machine", "show", "m1", "--pg", dsn)
	require.Contains(t, out, "width=32", "show: %q", out)
	_, out, _ = h.run(t, "machine", "list", "--pg", dsn)
	require.Contains(t, out, "width=32", "list: %q", out)
	require.Contains(t, out, "width=16", "list: %q", out)
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
	require.Equal(t, 3, code, "%d %q %q", code, out, errs)
	require.Equal(t, "", out, "%d %q %q", code, out, errs)
	require.Contains(t, errs, "the config cannot be read: postgres: connection reset", "%d %q %q", code, out, errs)
}
