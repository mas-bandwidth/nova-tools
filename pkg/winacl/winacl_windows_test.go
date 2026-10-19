//go:build windows

package winacl

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// setACL replaces path's DACL with exactly the given grants (inheritance off).
func setACL(t *testing.T, path string, sids ...*windows.SID) {
	t.Helper()
	var entries []windows.EXPLICIT_ACCESS
	for _, sid := range sids {
		entries = append(entries, windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.NO_INHERITANCE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_UNKNOWN,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		})
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	require.NoError(t, err)
	require.NoError(t, windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil))
}

// TestOwnerOnlyReadsTheAccessListNotTheMode: a key file whose access list names only this
// user (and SYSTEM, Administrators) passes; one that also grants Everyone or Users is
// refused, naming the account and the icacls remedy. No process, no time, no socket.
func TestOwnerOnlyReadsTheAccessListNotTheMode(t *testing.T) {
	t.Parallel()
	me, err := currentUser()
	require.NoError(t, err)
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	require.NoError(t, err)
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	require.NoError(t, err)
	everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	require.NoError(t, err)
	users, err := windows.CreateWellKnownSid(windows.WinBuiltinUsersSid)
	require.NoError(t, err)

	key := filepath.Join(t.TempDir(), "seat.key")
	require.NoError(t, os.WriteFile(key, []byte("AGE-SECRET-KEY-TEST"), 0o600))
	fi, err := os.Stat(key)
	require.NoError(t, err)
	assert.NotEqual(t, os.FileMode(0o600), fi.Mode().Perm(), "Windows reports no POSIX mode, which is why the ACL is read")

	setACL(t, key, me)
	assert.NoError(t, OwnerOnly(key), "this user alone")
	setACL(t, key, me, system, admins)
	assert.NoError(t, OwnerOnly(key), "this user, SYSTEM and Administrators, as OpenSSH allows a private key")

	setACL(t, key, me, everyone)
	err = OwnerOnly(key)
	require.Error(t, err, "a key Everyone can read is refused")
	assert.Contains(t, err.Error(), everyone.String())
	assert.Contains(t, err.Error(), "icacls")

	setACL(t, key, me, users)
	assert.Error(t, OwnerOnly(key), "a key the Users group can read is refused")

	assert.Error(t, OwnerOnly(filepath.Join(t.TempDir(), "absent")), "a file that is not there is an error, never a pass")
}
