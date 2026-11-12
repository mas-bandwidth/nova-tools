package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// adminWin is a WSL machine's admin runner as the record carries it (cmd/nova-fleet/README.md, "Windows runners of a
// WSL machine").
const adminWin = "https://github.com/owner-a/repo-a=verifier:verify-nightly"

// TestWindowsAdminRunnersRoundTrip: a value is stored in its canonical form, shows and lists as that, writes nothing
// when set again, and the inventory carries each entry with the nova-secrets name of its account's password; cleared
// is none, and the ordinary windows_runners field is left as it was.
func TestWindowsAdminRunnersRoundTrip(t *testing.T) {
	t.Parallel()
	h := fleet(t)
	h.ok(t, "machine", "set", "bench-b", "--windows_runners", captainWin, "--actor", "t")
	h.ok(t, "machine", "set", "bench-b", "--windows_admin_runners", " https://github.com/owner-a/repo-a/=verifier:verify-nightly+verify-nightly", "--actor", "t")
	out := h.ok(t, "machine", "show", "bench-b")
	assert.Contains(t, out, " windows_runners="+captainWin+" windows_admin_runners="+adminWin+" ")
	assert.Contains(t, h.ok(t, "machine", "list"), " windows_admin_runners="+adminWin+" ")
	assert.Contains(t, h.ok(t, "machine", "show", "bench-b", "--json"), `"windows_admin_runners":"`+adminWin+`"`)
	assert.Contains(t, h.ok(t, "machine", "set", "bench-b", "--windows_admin_runners", adminWin, "--actor", "t"), "changed=none")

	_, inv, _ := h.run(t, "inventory", "--host", "bench-b")
	flat := strings.Join(strings.Fields(inv), "")
	assert.Contains(t, flat, `"nova_windows_admin_runners":[`+
		`{"url":"https://github.com/owner-a/repo-a","account":"verifier","labels":["verify-nightly"],"secret":"NOVA_WINRUNNER_VERIFIER_PASSWORD"}]`)
	assert.Contains(t, flat, `"nova_windows_runners":[`+
		`{"url":"https://github.com/mas-bandwidth/rocketnet","count":3,"labels":["rocketnet-windows"]},`, "the ordinary runners are unchanged")
	_, inv, _ = h.run(t, "inventory", "--host", "bench-a")
	assert.NotContains(t, inv, "nova_windows_admin_runners", "an empty field is left out of the inventory")

	h.ok(t, "machine", "set", "bench-b", "--windows_admin_runners", "", "--actor", "t")
	assert.Contains(t, h.ok(t, "machine", "show", "bench-b"), " windows_admin_runners=- ")
	_, inv, _ = h.run(t, "inventory", "--host", "bench-b")
	assert.NotContains(t, inv, "nova_windows_admin_runners", "cleared is none")
	assert.Contains(t, inv, "nova_windows_runners")
}

func TestWindowsAdminRunnersCanonical(t *testing.T) {
	t.Parallel()
	f := kinds["machine"].findField("windows_admin_runners")
	for in, want := range map[string]string{
		"":       "",
		"   ":    "",
		adminWin: adminWin,
		"https://github.com/o/r=b:x,https://github.com/o/r=a:y+x+y,https://github.com/a/b=c:z": "https://github.com/a/b=c:z,https://github.com/o/r=a:x+y,https://github.com/o/r=b:x",
		"https://github.com/o/r=a2345678901234567890:l":                                        "https://github.com/o/r=a2345678901234567890:l",
	} {
		got, err := f.canonical(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, bad := range []string{
		"https://github.com/o/r=acct",                                 // no label
		"https://github.com/o/r=acct:",                                // an empty label
		"https://github.com/o/r=acct:a+",                              // an empty label after a +
		"https://github.com/o/r=acct:a;b",                             // a label outside the word
		"https://github.com/o/r:lbl",                                  // no account
		"https://github.com/o/r=:lbl",                                 // an empty account
		"https://github.com/o/r=Acct:lbl",                             // upper case
		"https://github.com/o/r=1acct:lbl",                            // not starting with a letter
		"https://github.com/o/r=a_b:lbl",                              // an underscore
		"https://github.com/o/r=a23456789012345678901:l",              // 21 characters: over Windows' 20
		"https://github.com/o=acct:lbl",                               // an organization
		"https://github.com/o/r/x=acct:lbl",                           // too deep
		"http://github.com/o/r=acct:lbl",                              // not https
		"https://gitlab.com/o/r=acct:lbl",                             // not github.com
		"https://github.com/o/r=acct:a,https://github.com/p/q=acct:b", // one account twice
		"https://github.com/o/r=acct:a,https://github.com/o/r=acct:b", // one url and account twice
	} {
		_, err := f.canonical(bad)
		assert.Error(t, err, bad)
	}
}

// The secret name is the account upper-cased with - as _, so two accounts never share one.
func TestWindowsAdminRunnersSecretName(t *testing.T) {
	t.Parallel()
	ws, err := parseAdmins("https://github.com/o/r=run-as-admin:l,https://github.com/o/r=x:l")
	require.NoError(t, err)
	require.Len(t, ws, 2)
	assert.Equal(t, "NOVA_WINRUNNER_RUN_AS_ADMIN_PASSWORD", ws[0].Secret)
	assert.Equal(t, "NOVA_WINRUNNER_X_PASSWORD", ws[1].Secret)
}

// A refused value is a malformed invocation: exit 2, nothing written, the reason and the field named.
func TestWindowsAdminRunnersRefusedSetWritesNothing(t *testing.T) {
	t.Parallel()
	h := fleet(t)
	code, _, stderr := h.run(t, "machine", "set", "bench-b", "--windows_admin_runners", "https://github.com/o=acct:l", "--actor", "t")
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "--windows_admin_runners")
	assert.Contains(t, stderr, "never an organization")
	assert.Contains(t, h.ok(t, "machine", "show", "bench-b"), " windows_admin_runners=- ")
}

// A corrupt value in the store (written by hand) refuses the inventory and names the fix.
func TestWindowsAdminRunnersCorruptRefusesTheInventory(t *testing.T) {
	t.Parallel()
	_, err := build([]Row{{Name: "m1", F: map[string]string{"windows_admin_runners": "https://github.com/o=acct:l"}}}, nil, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nova-config machine set m1 --windows_admin_runners")
}
