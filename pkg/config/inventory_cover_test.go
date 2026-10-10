package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// snapshotInventory builds the inventory the applied state the snapshot
// helper gives, with no loops so BuildInventory is pure: no Redis, no
// network, no subprocess, no Postgres.
func snapshotInventory(t *testing.T) *AnsibleInventory {
	t.Helper()
	inv, err := BuildInventory(snapshot(nil), "")
	require.NoError(t, err)
	return inv
}

// TestInventoryCoverHas covers Has (pkg/config/inventory.go:320):
// the inventory reports a machine row by exactly its name, and refuses a
// name it does not hold. The main path is a held row; the refusal is a
// missing row.
func TestInventoryCoverHas(t *testing.T) {
	t.Parallel()
	inv := snapshotInventory(t)
	cases := []struct {
		name string
		host string
		want bool
	}{
		{"held row alpha", "bench-alpha", true},
		{"held row beta", "bench-beta", true},
		{"missing row", "bench-gamma", false},
		{"empty name", "", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, inv.Has(tc.host))
		})
	}
}

// TestInventoryCoverUnknownHostError covers *UnknownHostError.Error
// (pkg/config/inventory.go:332): its message format and the
// HostJSON refusal that raises it. The main path is the formatted
// message; the refusal is HostJSON returning the error for a host the
// inventory does not hold.
func TestInventoryCoverUnknownHostError(t *testing.T) {
	t.Parallel()
	inv := snapshotInventory(t)

	_, herr := inv.HostJSON("bench-gone")
	var unknown *UnknownHostError
	require.ErrorAs(t, herr, &unknown)
	assert.Equal(t, "bench-gone", unknown.Name)
	assert.Equal(t, []string{"bench-alpha", "bench-beta"}, unknown.Known,
		"Known is every machine name the inventory holds, sorted")
	assert.Equal(t, "no machine row named bench-gone", unknown.Error(),
		"the refused name renders verbatim, with no known list in the message")
	assert.Equal(t, "no machine row named bench-gone", herr.Error(),
		"HostJSON surfaces the same message through the error interface")

	cases := []struct {
		name string
		err  *UnknownHostError
		want string
	}{
		{"without known hosts", &UnknownHostError{Name: "ghost"}, "no machine row named ghost"},
		{"with known hosts", &UnknownHostError{Name: "ghost", Known: []string{"bench-alpha"}}, "no machine row named ghost"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.err.Error())
		})
	}
}
