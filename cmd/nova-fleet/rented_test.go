package main

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// at is the tests' clock: 1000 seconds after the Unix epoch.
var at = time.Unix(1000, 0).UTC()

func stamp(d time.Duration) string { return at.Add(d).Format(time.RFC3339) }

// rentedRows is a record with one machine of each kind a listing meets.
func rentedRows() []rentedRow {
	return []rentedRow{
		{Name: "bench-a", Provider: "owned"},
		{Name: "old-b"}, // a row written before the rented columns
		{Name: "spill-1", Provider: "aws", Market: "spot", Price: "0.60", PriceFrom: "provider", Purpose: "load rows",
			RentedBy: "alice", RentedAt: stamp(-90 * time.Minute), ReturnBy: stamp(time.Hour)},
		{Name: "bench-x", Provider: "latitude", Price: "4.06", PriceFrom: "recalled", Purpose: "XDP pair",
			RentedBy: "alice", RentedAt: stamp(-3 * time.Hour), ReturnBy: stamp(-time.Minute)},
		{Name: "spill-2", Provider: "aws", Market: "on-demand", Purpose: "mutation", RentedBy: "alice", RentedAt: stamp(-30 * time.Minute)},
	}
}

// The listing names every rented machine with its age, rate, cost so far and whether it is past its return-by, and a
// total; an owned machine, or one written before the rented columns, is not listed.
func TestRentedListsCostAgeAndPast(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	require.NoError(t, listRented(rentedRows(), at, false, &out))
	assert.Equal(t, `RENTED spill-1 provider=aws market=spot age=1h30m rate=0.60/h price_from=provider cost=0.90 return_by=1970-01-01T01:16:40Z purpose="load rows"
RENTED bench-x provider=latitude market=- age=3h00m rate=4.06/h price_from=recalled cost=12.18 return_by=1970-01-01T00:15:40Z PAST purpose="XDP pair"
RENTED spill-2 provider=aws market=on-demand age=0h30m rate=unpriced price_from=- cost=0.00 return_by=- purpose="mutation"
RENTED TOTAL machines=3 rate=4.66/h cost=13.08 unpriced=1 past=1
`, out.String())
}

// --json is the same listing as one object.
func TestRentedJSON(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	require.NoError(t, listRented(rentedRows(), at, true, &out))
	var v struct {
		Machines []bill
		Total    struct {
			Machines, Unpriced, Past int
			Rate, Cost               float64
		}
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &v))
	require.Len(t, v.Machines, 3)
	assert.True(t, v.Machines[1].Past)
	assert.InDelta(t, 12.18, v.Machines[1].Cost, 0.001)
	assert.Equal(t, 3, v.Total.Machines)
	assert.Equal(t, 1, v.Total.Unpriced)
	assert.InDelta(t, 4.66, v.Total.Rate, 0.001)
}

// No rented machine is a total of none, not an empty answer.
func TestRentedNone(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	require.NoError(t, listRented(rentedRows()[:2], at, false, &out))
	assert.Equal(t, "RENTED TOTAL machines=0 rate=0.00/h cost=0.00 unpriced=0 past=0\n", out.String())
}

// The record is read through nova-config; a failed read carries its cause.
func TestRecordReadsNovaConfig(t *testing.T) {
	t.Parallel()
	var got []string
	run := func(bin string, args, env []string) (string, string, int) {
		got = append([]string{bin}, args...)
		return `{"items":[{"fields":{"name":"spill-1","provider":"aws","cores":null,"price":"0.59"}}]}`, "", 0
	}
	rows, err := record("/bin/nova-config", run)
	require.NoError(t, err)
	assert.Equal(t, []string{"/bin/nova-config", "machine", "list", "--json"}, got)
	assert.Equal(t, []rentedRow{{Name: "spill-1", Provider: "aws", Price: "0.59"}}, rows)

	_, err = record("/bin/nova-config", func(string, []string, []string) (string, string, int) {
		return "", "nova-config machine list: store has no current schema; run: nova-config schema | psql", 2
	})
	assert.EqualError(t, err, "nova-config machine list --json exit 2: nova-config machine list: store has no current schema; run: nova-config schema | psql")
	_, err = record("/bin/nova-config", func(string, []string, []string) (string, string, int) { return "MACHINE", "", 0 })
	assert.ErrorContains(t, err, "printed no JSON")
}

// An argument rented does not take is refused, naming it; -h prints the help.
func TestRentedArguments(t *testing.T) {
	t.Parallel()
	var o, e bytes.Buffer
	assert.Equal(t, 2, runRented([]string{"--all"}, &o, &e))
	assert.Equal(t, "nova-fleet rented: unexpected argument --all; run: nova-fleet rented -h\n", e.String())
	o.Reset()
	assert.Equal(t, 0, runRented([]string{"-h"}, &o, &e))
	assert.Equal(t, rentedHelp, o.String())
}
