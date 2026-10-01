//go:build functional

package ntable_test

// An error the table library does not turn into a refusal leaves the script as
// an error, unchanged: a denial by the store's ACL is not "malformed", and the
// script adds no prefix of its own.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Redis releases use different words for the same ACL denial. Require a
// typed store error with one of Redis's ACL prefixes, not arbitrary prose.
func isACLPermissionError(err error) bool {
	var storeErr redis.Error
	if !errors.As(err, &storeErr) {
		return false
	}
	s := strings.ToLower(storeErr.Error())
	return strings.HasPrefix(s, "noperm ") ||
		strings.HasPrefix(s, "err acl failure in script:") ||
		strings.HasPrefix(s, "err the user executing the script can't run this command or subcommand") ||
		strings.HasPrefix(s, "err the user executing the script can't access at least one of the keys mentioned")
}

// The adapter may add location and recovery text, but must carry Redis's own
// ACL error exactly once, without converting it into a malformed reply.
func assertACLPassThrough(t *testing.T, action string, wrapped, raw error) {
	t.Helper()
	if !assert.True(t, isACLPermissionError(raw), "%s: raw=%v wrapped=%v; want store ACL denials", action, raw, wrapped) || !assert.True(t, isACLPermissionError(wrapped), "%s: raw=%v wrapped=%v; want store ACL denials", action, raw, wrapped) {
		return
	}
	assert.NotContains(t, wrapped.Error(), "malformed", "%s: raw=%v wrapped=%v; want the store error once, not a malformed reply", action, raw, wrapped)
	assert.NotContains(t, wrapped.Error(), "ERR ERR", "%s: raw=%v wrapped=%v; want the store error once, not a malformed reply", action, raw, wrapped)
	assert.Equal(t, 1, strings.Count(wrapped.Error(), raw.Error()), "%s: raw=%v wrapped=%v; want the store error once, not a malformed reply", action, raw, wrapped)
}

func TestKeyACLDenialOnCellKeysIsAnErrorNotAMalformedReply(t *testing.T) {
	t.Parallel()
	perms := []string{"--user", "default", "on", "nopass", "~*", "&*", "+@all",
		"--user", "nc", "on", ">pw", "resetkeys", "resetchannels", "-@all", "+@all",
		"~table:demo", "~table:demo:rows", "~table:demo:row:*", "~table:demo:definition", "~table:demo:revision",
		"~table:demo:identity", "~table:demo:changes", "~table:demo:ops", "~table::member:*", "~tables"}
	addr, admin := live(t, perms...)
	ctx := context.Background()
	newTable(t, admin, demo()).rows("build")
	require.NoError(t, ntable.MemberCreate(ctx, admin, "demo", "q"))
	nc := redis.NewClient(&redis.Options{Addr: addr, Username: "nc", Password: "pw"})
	defer nc.Close()
	rev := admin.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()
	m := ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: rev, OperationID: "acl", Members: []ntable.BatchMemberEntry{
		{ID: "q", Expect: &ntable.MemberExpect{}, Move: &ntable.MemberMoveOp{Row: "build", Col: "ready"}}}}
	before := storeImage(t, admin)
	err := nc.ZCard(ctx, ntable.CellKey("demo", "build", "ready")).Err()
	require.True(t, isACLPermissionError(err), "the cell key must be denied directly: %v", err)
	body, err := json.Marshal(m)
	require.NoError(t, err)
	raw := nc.FCall(ctx, ntable.FnApply, []string{ntable.DefKey("demo")}, "demo", string(body)).Err()
	_, err = ntable.ApplyBatch(ctx, nc, m)
	assertACLPassThrough(t, "ApplyBatch", err, raw)
	assert.ErrorIs(t, err, ntable.ErrUnknownOutcome, "ApplyBatch ACL error must be an unknown outcome, not a refusal: %v", err)
	assert.False(t, ntable.IsRefusal(err), "ApplyBatch ACL error must be an unknown outcome, not a refusal: %v", err)
	raw = nc.FCallRO(ctx, ntable.FnReadSet, []string{ntable.DefKey("demo")}, "demo", `{"members":["q"]}`).Err()
	_, err = ntable.ReadSetMembers(ctx, nc, "demo", []string{"q"})
	assertACLPassThrough(t, "ReadSet", err, raw)
	raw = nc.FCallRO(ctx, ntable.FnCheck, []string{ntable.DefKey("demo")}, "demo").Err()
	_, err = ntable.Check(ctx, nc, "demo")
	assertACLPassThrough(t, "Check", err, raw)
	assert.Equal(t, before, storeImage(t, admin), "key-ACL denials wrote to the store")
}

// The store's own errors (memory, a read-only replica, a missing permission)
// leave the script as the store wrote them, once prefixed.
func TestStoreErrorsPassThroughTheScriptUnchanged(t *testing.T) {
	t.Parallel()
	var grants []string
	for _, g := range tableGrants {
		if g != "+type" {
			grants = append(grants, g)
		}
	}
	extra := append([]string{"--user", "default", "on", "nopass", "~*", "&*", "+@all", "--user", "nt", "on", ">pw", "resetkeys", "resetchannels", "-@all", "+ping"}, grants...)
	addr, admin := live(t, extra...)
	ctx := context.Background()
	newTable(t, admin, demo()).rows("r")
	nt := redis.NewClient(&redis.Options{Addr: addr, Username: "nt", Password: "pw"})
	defer nt.Close()
	before := storeImage(t, admin)
	_, err := ntable.CellAdd(ctx, nt, "demo", "r", "ready", "x", 1)
	raw := nt.FCall(ctx, ntable.FnCellAdd, []string{ntable.DefKey("demo")}, "demo", "r", "ready", "1", "x", `{"epoch":"0","actor":"","fence":"","idem":""}`).Err()
	assertACLPassThrough(t, "missing +type", err, raw)
	assert.Equal(t, before, storeImage(t, admin), "a denied +type write changed the store")
	require.NoError(t, admin.ConfigSet(ctx, "maxmemory-policy", "noeviction").Err())
	require.NoError(t, admin.ConfigSet(ctx, "maxmemory", "1").Err())
	_, err = ntable.RowAdd(ctx, admin, "demo", "r9", ntable.RowSpec{})
	require.Error(t, err, "out of memory")
	assert.Contains(t, err.Error(), "OOM command not allowed", "out of memory: %v", err)
	assert.NotContains(t, err.Error(), "ERR ERR", "out of memory: %v", err)
	require.NoError(t, admin.ConfigSet(ctx, "maxmemory", "0").Err())
}
