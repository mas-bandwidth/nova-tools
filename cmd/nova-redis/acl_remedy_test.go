package main

// acl_remedy_test.go pins acl apply's refusal when users need a password the
// run did not name a source for: the one refusal carries a
// --password-env-for <user>=<VARIABLE> for EVERY user it names, so a reader
// fixes the whole call in one turn (docs/STANDARD.md section 2, "recovery
// takes one turn").

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/redisacl"
)

// TestACLApplyRefusalNamesEveryMissingUser: an apply that finds four rendered
// users missing and no password source for them names each one in its remedy.
func TestACLApplyRefusalNamesEveryMissingUser(t *testing.T) {
	t.Parallel()
	f := &fakeACL{live: map[string]redisacl.Live{}, cat: redisacl.Catalog{"read": {"get"}, "write": {"set"}}}
	code, out, _ := aclRun(t, f, append([]string{"apply"}, login4...)...)
	require.Equal(t, 1, code, out)
	assert.Contains(t, out, "ACL APPLY REFUSED users=4 missing=coordinator,bench,ns-table,ns-friend")
	for _, u := range []string{"coordinator", "bench", "ns-table", "ns-friend"} {
		assert.Contains(t, out, "--password-env-for "+u+"=<VARIABLE>", "the refusal does not name %s's source: %s", u, out)
	}
	assert.Equal(t, 4, strings.Count(out, "=<VARIABLE>"), out)
	assert.Empty(t, f.set, "a refused apply wrote")
}

// TestACLApplyNopassRefusalNamesEveryUser: the same one-refusal rule for the
// live users that carry nopass, when a run names no source for two of them.
func TestACLApplyNopassRefusalNamesEveryUser(t *testing.T) {
	t.Parallel()
	f := &fakeACL{live: map[string]redisacl.Live{}, cat: redisacl.Catalog{"read": {"get"}, "write": {"set"}}}
	code, _, errs := aclRun(t, f, append(append([]string{"apply"}, login4...), sourced...)...)
	require.Equal(t, 0, code, errs)

	for _, u := range []string{"bench", "ns-table"} {
		l := f.live[u]
		l.NoPass = true
		f.live[u] = l
	}
	f.set = nil
	code, out, _ := aclRun(t, f, append([]string{"apply"}, login4...)...)
	require.Equal(t, 1, code, out)
	assert.Contains(t, out, "ACL APPLY REFUSED users=4 nopass=bench,ns-table")
	for _, u := range []string{"bench", "ns-table"} {
		assert.Contains(t, out, "--password-env-for "+u+"=<VARIABLE>", "the refusal does not name %s's source: %s", u, out)
	}
	assert.Equal(t, 2, strings.Count(out, "=<VARIABLE>"), out)
	assert.Empty(t, f.set, "a refused apply wrote")
}
