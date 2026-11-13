package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rentArgs is a rented machine's add with every field it needs.
func rentArgs(name string, extra ...string) []string {
	return append([]string{"machine", "add", name, "--user", "nova", "--provider", "aws", "--provider_id", "i-0abc",
		"--market", "spot", "--spot_request", "sir-1", "--price", "0.59", "--price_from", "provider",
		"--purpose", "load pair rows", "--rented_by", "alice", "--rented_at", "2026-11-13T00:00:00-05:00",
		"--return_by", "2026-11-13T09:00:00Z", "--actor", "t"}, extra...)
}

// A rented machine keeps what it is for, who rented it, when, its price and where the price came from; times are
// written back in UTC.
func TestRentedRowKeepsItsRecord(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.ok(t, rentArgs("spill-1")...)
	out := h.ok(t, "machine", "show", "spill-1")
	assert.Contains(t, out, " provider=aws provider_id=i-0abc market=spot spot_request=sir-1 price=0.59 price_from=provider purpose=\"load pair rows\" rented_by=alice rented_at=2026-11-13T05:00:00Z return_by=2026-11-13T09:00:00Z ")
	h.ok(t, "machine", "add", "bench-o", "--user", "nova", "--actor", "t")
	assert.Contains(t, h.ok(t, "machine", "show", "bench-o"), rentedNone, "a machine added without a provider is owned")
}

// Every rented field that does not fit is refused, all of them in one line, and nothing is written.
func TestRentedRowRefusals(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.ok(t, "machine", "add", "bench-o", "--user", "nova", "--actor", "t")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"machine", "set", "bench-o", "--price", "1", "--price_from", "recalled", "--actor", "t"},
			"machine bench-o: --price is a rented machine's, and its --provider is owned; set --provider latitude|aws, or --price ''"},
		{[]string{"machine", "add", "r1", "--user", "nova", "--provider", "latitude", "--actor", "t"},
			"machine r1 is rented (--provider latitude) and has no --purpose; give it; machine r1 is rented (--provider latitude) and has no --rented_by; give it; machine r1 is rented (--provider latitude) and has no --rented_at; give it"},
		{rentArgs("r2", "--price_from", ""), "machine r2: --price and --price_from go together"},
		{rentArgs("r3", "--provider", "latitude"), "machine r3: --market is an aws machine's, and its --provider is latitude; set --market ''"},
		{rentArgs("r4", "--market", "on-demand"), "machine r4: --spot_request is a spot machine's, and its --market is \"on-demand\"; set --market spot or --spot_request ''"},
		{rentArgs("r5", "--price", "4,06"), `--price "4,06": want US dollars per hour, such as 4.06`},
		{rentArgs("r6", "--rented_at", "yesterday"), `--rented_at "yesterday": want an RFC 3339 time, such as 2026-11-13T04:00:00Z`},
		{rentArgs("r7", "--provider", "hetzner"), `--provider "hetzner": want one of owned|latitude|aws`},
		{rentArgs("r8", "--market", "reserved"), `--market "reserved": want one of on-demand|spot`},
		{rentArgs("r9", "--price_from", "guess"), `--price_from "guess": want one of provider|recalled`},
	} {
		code, out, errs := h.run(t, c.args...)
		assert.NotEqual(t, 0, code, "%v", c.args)
		assert.Empty(t, out, "%v", c.args)
		assert.Contains(t, errs, c.want, "%v", c.args)
	}
	assert.Len(t, h.st.rows["machine"], 1, "no refused row was written")
}

// An aws machine is never in the inventory (no play reaches it), nor is a rented machine with no address yet (asked
// for, not yet created); a rented Latitude machine with its address is, and an older row with no provider is owned.
func TestInventoryLeavesOutMachinesNoPlayReaches(t *testing.T) {
	t.Parallel()
	rented := func(name, provider, address string) Row {
		return Row{Name: name, F: map[string]string{"user": "nova", "roles": "bench", "provider": provider, "address": address}}
	}
	inv, err := build([]Row{
		rented("aws-1", "aws", "203.0.113.7"),
		rented("asked-1", "latitude", ""),
		rented("lat-1", "latitude", "lat-1.tailnet.example"),
		rented("own-1", "owned", ""),
		{Name: "old-1", F: map[string]string{"user": "nova", "roles": "bench"}},
	}, nil, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"lat-1", "own-1", "old-1"}, inv.All.Hosts)
	assert.Equal(t, []string{"lat-1", "own-1", "old-1"}, inv.Bench.Hosts)
	assert.NotContains(t, inv.Meta.Hostvars, "aws-1")
	assert.NotContains(t, inv.Meta.Hostvars, "asked-1")
}
