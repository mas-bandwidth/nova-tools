//go:build functional

package ntable_test

// A batch reads a member with HLEN, HMGET, HSTRLEN and HEXISTS. A user missing
// one of those gets an error from the store, never an accepted batch: the count
// of the bytes a batch touches is not skipped when a read is denied.

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// batchReads are the read-only hash commands a batch reads a member with.
var batchReads = []string{"+hlen", "+hmget", "+hstrlen", "+hexists"}

func TestBatchWithAReadDeniedIsAnErrorNeverAnAcceptedBatch(t *testing.T) {
	t.Parallel()
	base := []string{"resetkeys", "resetchannels", "-@all", "+ping"}
	for _, g := range tableGrants { // the table grants, less the reads under test
		if !slices.Contains(batchReads, g) {
			base = append(base, g)
		}
	}
	extra := []string{"--user", "default", "on", "nopass", "~*", "&*", "+@all"}
	extra = append(extra, append([]string{"--user", "ns-all", "on", ">pw"}, append(append([]string{}, base...), batchReads...)...)...)
	for _, denied := range batchReads {
		name := "ns-no-" + strings.TrimPrefix(denied, "+")
		rules := append([]string{}, base...)
		for _, g := range batchReads {
			if g != denied {
				rules = append(rules, g)
			}
		}
		extra = append(extra, append([]string{"--user", name, "on", ">pw"}, rules...)...)
	}
	addr, admin := live(t, extra...)
	ctx := context.Background()
	newTable(t, admin, demo()).rows("build", "test")
	seedTwo(t, ctx, admin)
	// a field one byte over the bound a batch may touch
	require.NoError(t, admin.HSet(ctx, ntable.MemberKey("a"), "big", strings.Repeat("v", ntable.LimitBatchValueBytes+1)).Err())
	as := func(user string) *redis.Client {
		c := redis.NewClient(&redis.Options{Addr: addr, Username: user, Password: "pw"})
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	// big names a field over the bound and one that is absent (which HEXISTS
	// settles); small names only the absent field, and so passes the count and
	// goes on to read the member's head with HLEN and HMGET
	batch := func(op string, fields ...string) string {
		return manifestWith(probeRev(ctx, admin), op, `{"id":"a","expect":{},"unset":["`+strings.Join(fields, `","`)+`"]}`)
	}
	before := storeImage(t, admin)

	// every read granted: the bound refuses the big batch and the small one applies
	ans, err := rawApply(ctx, as("ns-all"), batch("all-big", "big", "gone"))
	require.True(t, replyOpens(ans, err, "REFUSED", "LIMIT", "value bytes per batch"), "with every read granted: %.200v %v; want LIMIT value bytes per batch", ans, err)
	// the count stopped where it passed the bound, and the refusal says so
	if len(ans) < 7 || ans[6] != "at least" {
		t.Errorf("the refusal from the counting pass does not say `at least`: %.200v", ans)
	}
	// One read denied: prove that command is forbidden directly, then require
	// a store ACL error from the batch, never an accepted batch or a write.
	for _, denied := range batchReads {
		user := "ns-no-" + strings.TrimPrefix(denied, "+")
		client := as(user)
		command := strings.TrimPrefix(denied, "+")
		args := []any{command, ntable.MemberKey("a")}
		if command != "hlen" {
			args = append(args, "big")
		}
		if err := client.Do(ctx, args...).Err(); !isACLPermissionError(err) {
			t.Fatalf("%s must directly deny %s on a member: %v", user, command, err)
		}
		for name, fields := range map[string][]string{"big": {"big", "gone"}, "small": {"gone"}} {
			ans, err := rawApply(ctx, client, batch("no-"+denied[1:]+"-"+name, fields...))
			// The count stops a big batch before the head is read, so missing
			// HLEN/HMGET is reached by the small batch only.
			if name == "big" && (denied == "+hlen" || denied == "+hmget") && err == nil && len(ans) > 1 && ans[0] == "REFUSED" && ans[1] == "LIMIT" {
				continue
			}
			if !isACLPermissionError(err) || strings.Contains(err.Error(), "malformed") || strings.Contains(err.Error(), "ERR ERR") {
				t.Errorf("without %s, %s batch: %.200v %v; want a store ACL denial", denied, name, ans, err)
			}
			if len(ans) > 0 && ans[0] == "OK" {
				t.Errorf("without %s, %s batch: accepted", denied, name)
			}
		}
	}
	assert.Equal(t, before, storeImage(t, admin), "a batch with a read denied changed the store")
	if ans, err := rawApply(ctx, as("ns-all"), batch("all-small", "gone")); err != nil || ans[0] != "OK" {
		t.Errorf("the small batch with every read granted: %.200v %v", ans, err)
	}
}
