package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// rentArgs is a rented box's add with every field it needs.
func rentArgs(name string, extra ...string) []string {
	return append([]string{"rented", "add", name, "--provider", "aws", "--provider_id", "i-0abc",
		"--market", "spot", "--spot_request", "sir-1", "--address", "203.0.113.7", "--user", "ubuntu",
		"--price", "0.59", "--price_from", "provider", "--purpose", "load pair rows", "--rented_by", "alice",
		"--rented_at", "2026-11-13T00:00:00-05:00", "--return_by", "2026-11-13T09:00:00Z", "--actor", "t"}, extra...)
}

// A rented box keeps what it is for, who rented it, when, its price and where the price came from; times are written
// back in UTC. It is no machine: it is in its own table.
func TestRentedRowKeepsItsRecord(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.ok(t, rentArgs("spill-1")...)
	out := h.ok(t, "rented", "show", "spill-1")
	assert.Contains(t, out, "RENTED name=spill-1 provider=aws provider_id=i-0abc market=spot spot_request=sir-1 address=203.0.113.7 user=ubuntu price=0.59 price_from=provider purpose=\"load pair rows\" rented_by=alice rented_at=2026-11-13T05:00:00Z return_by=2026-11-13T09:00:00Z ")
	assert.Empty(t, h.st.rows["machine"], "a rented box is no machine row")
}

// Every rented field that does not fit is refused, all of them in one line, and nothing is written.
func TestRentedRowRefusals(t *testing.T) {
	t.Parallel()
	h := newHarness()
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"rented", "add", "r1", "--provider", "latitude", "--actor", "t"}, "missing required flag --purpose, --rented_by, --rented_at"},
		{rentArgs("r2", "--price_from", ""), "rented r2: --price and --price_from go together"},
		{rentArgs("r3", "--provider", "latitude"), "rented r3: --market is an aws box's, and its --provider is latitude; set --market ''"},
		{rentArgs("r4", "--market", "on-demand"), "rented r4: --spot_request is a spot box's, and its --market is \"on-demand\"; set --market spot or --spot_request ''"},
		{rentArgs("r5", "--price", "4,06"), `--price "4,06": want US dollars per hour, such as 4.06`},
		{rentArgs("r6", "--rented_at", "yesterday"), `--rented_at "yesterday": want an RFC 3339 time, such as 2026-11-13T04:00:00Z`},
		{rentArgs("r7", "--provider", "owned"), `--provider "owned": want one of latitude|aws`},
		{rentArgs("r8", "--market", "reserved"), `--market "reserved": want one of on-demand|spot`},
		{rentArgs("r9", "--price_from", "guess"), `--price_from "guess": want one of provider|recalled`},
		{rentArgs("r10", "--return_by", "2026-11-12T00:00:00Z"), "rented r10: --return_by 2026-11-12T00:00:00Z is before --rented_at 2026-11-13T05:00:00Z; give a later --return_by"},
		{rentArgs("r11", "--market", "", "--spot_request", ""), "rented r11 is at aws and has no --market; give --market on-demand|spot"},
		{rentArgs("r12", "--purpose", ""), "rented r12 has no --purpose; give --purpose <value>"},
	} {
		code, out, errs := h.run(t, c.args...)
		assert.NotEqual(t, 0, code, "%v", c.args)
		assert.Empty(t, out, "%v", c.args)
		assert.Contains(t, errs, c.want, "%v", c.args)
	}
	assert.Empty(t, h.st.rows["rented"], "no refused row was written")
}

// A rented box is in its own table, which no release before it reads: the machine kind (all an inventory is built
// from) keeps exactly the columns it had, so a box no play may reach is in no older binary's inventory either.
func TestRentedBoxesAreInNoTableAnOlderReleaseReads(t *testing.T) {
	t.Parallel()
	older := []string{"machines", "services", "friends", "history"} // v2.0.61's tables
	assert.NotContains(t, older, kinds["rented"].table)
	var machine []string
	for _, f := range kinds["machine"].fields {
		machine = append(machine, f.name)
	}
	assert.Equal(t, "address public user os arch cores roles runners runner_scopes windows_runners windows_admin_runners runner_env note", strings.Join(machine, " "), "v2.0.61's machine columns, unchanged")
	assert.False(t, slices.ContainsFunc(kinds["machine"].fields, func(f field) bool { return f.name == "provider" }))
}

// A rented record is not removed while it holds either handle at the provider: a box id, or a spot request id (a
// request with no box yet, or one left after its box went, can still fulfil a billing box). Only with both cleared
// does it go.
func TestRentedRowWithAHandleIsNotRemoved(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.ok(t, rentArgs("spill-1")...)
	for _, c := range []struct {
		clear []string
		still string
	}{
		{nil, "box i-0abc and spot request sir-1"},
		{[]string{"--provider_id", ""}, "spot request sir-1"},
		{[]string{"--provider_id", "i-0abc", "--spot_request", ""}, "box i-0abc"},
	} {
		if c.clear != nil {
			h.ok(t, append([]string{"rented", "set", "spill-1", "--actor", "t"}, c.clear...)...)
		}
		code, out, errs := h.run(t, "rented", "remove", "spill-1", "--actor", "t")
		assert.Equal(t, 1, code, c.still)
		assert.Empty(t, out)
		assert.Contains(t, errs, "rented spill-1 still has "+c.still+" at aws, which may bill;")
		assert.Contains(t, errs, "; run: nova-fleet rented\n")
	}
	h.ok(t, "rented", "set", "spill-1", "--provider_id", "", "--actor", "t", "--reason", "the box and the request are gone")
	h.ok(t, "rented", "remove", "spill-1", "--actor", "t")
	assert.Empty(t, h.st.rows["rented"])
}

// A handle at the provider is cleared once its thing is gone, never replaced by another: the old box or request may
// still bill, and its id would be lost.
func TestRentedHandleIsNotReplaced(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.ok(t, rentArgs("spill-1")...)
	for _, f := range []string{"provider_id", "spot_request"} {
		code, out, errs := h.run(t, "rented", "set", "spill-1", "--"+f, "x-new", "--actor", "t")
		assert.Equal(t, 1, code, f)
		assert.Empty(t, out)
		assert.Contains(t, errs, "rented spill-1: --"+f+" is ")
		assert.Contains(t, errs, "which may still bill; return it, or once it is gone at the provider clear it (--"+f+" '') before giving another")
	}
	h.ok(t, "rented", "set", "spill-1", "--provider_id", "", "--actor", "t")
	h.ok(t, "rented", "set", "spill-1", "--provider_id", "i-0new", "--actor", "t")
}
