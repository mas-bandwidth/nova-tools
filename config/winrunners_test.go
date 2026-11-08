package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captainWin is captain's Windows side as the record carries it (fleet/README.md, "Windows runners of a WSL machine").
const captainWin = "https://github.com/mas-bandwidth/rocketnet=3:rocketnet-windows,https://github.com/robotgamedevelopers=2:fleet"

// TestWindowsRunnersRoundTrip: a value is stored in its canonical form (targets by url, labels sorted and unique, no
// trailing slash), shows and lists as that, writes nothing when set again, and the inventory carries it.
func TestWindowsRunnersRoundTrip(t *testing.T) {
	t.Parallel()
	h := fleet(t)
	h.ok(t, "machine", "set", "bench-b", "--windows_runners",
		"https://github.com/robotgamedevelopers/=2:fleet+fleet, https://github.com/mas-bandwidth/rocketnet=3:rocketnet-windows", "--actor", "t")
	out := h.ok(t, "machine", "show", "bench-b")
	assert.Contains(t, out, " windows_runners="+captainWin+" ")
	assert.Contains(t, h.ok(t, "machine", "list"), " windows_runners="+captainWin+" ")
	assert.Contains(t, h.ok(t, "machine", "show", "bench-b", "--json"), `"windows_runners":"`+captainWin+`"`)
	assert.Contains(t, h.ok(t, "machine", "set", "bench-b", "--windows_runners", captainWin, "--actor", "t"), "changed=none")

	_, inv, _ := h.run(t, "inventory", "--host", "bench-b")
	assert.Contains(t, strings.Join(strings.Fields(inv), ""), `"nova_windows_runners":[`+
		`{"url":"https://github.com/mas-bandwidth/rocketnet","count":3,"labels":["rocketnet-windows"]},`+
		`{"url":"https://github.com/robotgamedevelopers","count":2,"labels":["fleet"]}]`)
	_, inv, _ = h.run(t, "inventory", "--host", "bench-a")
	assert.NotContains(t, inv, "nova_windows_runners", "an empty field is left out of the inventory")

	h.ok(t, "machine", "set", "bench-b", "--windows_runners", "", "--actor", "t")
	assert.Contains(t, h.ok(t, "machine", "show", "bench-b"), " windows_runners=- ")
	_, inv, _ = h.run(t, "inventory", "--host", "bench-b")
	assert.NotContains(t, inv, "nova_windows_runners", "cleared is none")
}

func TestWindowsRunnersCanonical(t *testing.T) {
	t.Parallel()
	f := kinds["machine"].findField("windows_runners")
	for in, want := range map[string]string{
		"":                         "",
		"   ":                      "",
		captainWin:                 captainWin,
		"https://github.com/o/r=1": "https://github.com/o/r=1",
		"https://github.com/o=1:b+a+b,https://github.com/a/b=99": "https://github.com/a/b=99,https://github.com/o=1:a+b",
	} {
		got, err := f.canonical(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, bad := range []string{
		"https://github.com/o/r",                            // no count
		"https://github.com/o/r=0",                          // a target with no runners is no target
		"https://github.com/o/r=100",                        // above 99
		"https://github.com/o/r=x",                          // not a number
		"http://github.com/o/r=1",                           // not https
		"https://gitlab.com/o/r=1",                          // not github.com
		"https://github.com/o/r/x=1",                        // too deep
		"https://github.com/o/r=1:a b",                      // a label with a blank splits into a bad entry
		"https://github.com/o/r=1:a;b",                      // a label outside the word
		"https://github.com/o/r=1:",                         // an empty label list after the colon is a bad label
		"https://github.com/o/r=1,https://github.com/O/R=2", // one url twice
	} {
		_, err := f.canonical(bad)
		assert.Error(t, err, bad)
	}
}

// A corrupt value in the store (written by hand) refuses the inventory and names the fix.
func TestWindowsRunnersCorruptRefusesTheInventory(t *testing.T) {
	t.Parallel()
	_, err := build([]Row{{Name: "m1", F: map[string]string{"windows_runners": "nope"}}}, nil, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nova-config machine set m1 --windows_runners")
}
