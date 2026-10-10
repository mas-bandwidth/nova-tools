package main

// acl_rotate_test.go pins acl apply's password rotation, over the tool's ACL
// server interface: --rotate adds the password the variable names beside the
// old one, a second run with --drop-old removes it, and neither value is
// printed (nova-tools#5096 item 15).

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/redisacl"
)

// aclLiveStore is a fake store holding the four rendered users, as an earlier
// apply created them.
func aclLiveStore(t *testing.T) *fakeACL {
	t.Helper()
	f := &fakeACL{live: map[string]redisacl.Live{}, cat: redisacl.Catalog{"read": {"get"}, "write": {"set"}}}
	code, _, errs := aclRun(t, f, append(append([]string{"apply"}, login4...), sourced...)...)
	require.Equal(t, 0, code, errs)
	f.set = nil
	f.saves = 0
	return f
}

// TestACLRotateAddsTheNewPasswordBesideTheOld: --rotate sets one user's new
// password with a > rule, reports the rotation, and prints no password.
func TestACLRotateAddsTheNewPasswordBesideTheOld(t *testing.T) {
	t.Parallel()
	f := aclLiveStore(t)
	code, out, _ := aclRun(t, f, append(append([]string{"apply", "--rotate", "bench"}, login4...), "--password-env-for", "bench=NEW_PW")...)
	require.Equal(t, 0, code, out)
	assert.Equal(t, []string{"bench"}, f.set, "only the rotated user is set")
	assert.Equal(t, []string{">new-secret"}, f.setRules["bench"], "the new password is added, not the rendered rules")
	assert.Contains(t, out, "ACL ROTATE user=bench drop-old=false", out)
	assert.Contains(t, out, "rotated=1", out)
	assert.Equal(t, 1, f.saves, "a rotation is saved")
	assert.NotContains(t, out, "new-secret")
}

// TestACLRotateDropOldRemovesTheOld: the second run removes the old password
// with a < rule, and prints no password.
func TestACLRotateDropOldRemovesTheOld(t *testing.T) {
	t.Parallel()
	f := aclLiveStore(t)
	code, out, _ := aclRun(t, f, append(append([]string{"apply", "--rotate", "bench", "--drop-old"}, login4...), "--password-env-for", "bench=NEW_PW")...)
	require.Equal(t, 0, code, out)
	assert.Equal(t, []string{"bench"}, f.set, "only the rotated user is set")
	assert.Equal(t, []string{"<new-secret"}, f.setRules["bench"], "the named password is removed")
	assert.Contains(t, out, "ACL ROTATE user=bench drop-old=true", out)
	assert.Contains(t, out, "rotated=1", out)
	assert.NotContains(t, out, "new-secret")
}

// TestACLRotateRefusesAUserTheStoreLacks: a password is rotated only on a user
// the store already has.
func TestACLRotateRefusesAUserTheStoreLacks(t *testing.T) {
	t.Parallel()
	f := aclLiveStore(t)
	code, out, _ := aclRun(t, f, append(append([]string{"apply", "--rotate", "legacy"}, login4...), "--password-env-for", "legacy=NEW_PW")...)
	require.Equal(t, 1, code, out)
	assert.Contains(t, out, "ACL APPLY REFUSED users=4 rotate=legacy", out)
	assert.Empty(t, f.set, "a refused rotation wrote")
}

// TestACLRotateRefusesWithoutASource: a rotation reads its password from the
// variable --password-env-for names, and refuses without one.
func TestACLRotateRefusesWithoutASource(t *testing.T) {
	t.Parallel()
	f := aclLiveStore(t)
	code, out, _ := aclRun(t, f, append([]string{"apply", "--rotate", "bench"}, login4...)...)
	require.Equal(t, 1, code, out)
	assert.Contains(t, out, "ACL APPLY REFUSED users=4 rotate=bench", out)
	assert.Contains(t, out, "--password-env-for bench=<VARIABLE>", out)
	assert.Empty(t, f.set, "a refused rotation wrote")
}

// TestACLRotateDryRunNeverDials: the rotation plan prints from the flags alone
// and dials nothing.
func TestACLRotateDryRunNeverDials(t *testing.T) {
	t.Parallel()
	f := &fakeACL{live: map[string]redisacl.Live{}, cat: redisacl.Catalog{"read": {"get"}, "write": {"set"}}}
	code, out, errs := aclRun(t, f, append(append([]string{"apply", "--dry-run", "--rotate", "bench"}, login4...), "--password-env-for", "bench=NEW_PW")...)
	require.Equal(t, 0, code, errs)
	assert.Zero(t, f.opened, "a dry run dialled the store")
	assert.Contains(t, out, "ACL WOULD-ROTATE user=bench drop-old=false", out)
	assert.Contains(t, out, "rotated=1", out)
	assert.NotContains(t, out, "new-secret")
}
