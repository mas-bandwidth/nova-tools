//go:build functional

package ntable_test

// A batch reads a member with HLEN, HMGET, HSTRLEN and HEXISTS. A user missing
// one of those gets an error from the store, never an accepted batch: the count
// of the bytes a batch touches is not skipped when a read is denied.

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

// batchReads are the read-only hash commands a batch reads a member with.
var batchReads = []string{"+hlen", "+hmget", "+hstrlen", "+hexists"}

func TestBatchWithAReadDeniedIsAnErrorNeverAnAcceptedBatch(t *testing.T) {
	t.Parallel()
	base := []string{"resetkeys", "resetchannels", "-@all", "+ping"}
	base = append(base, tableGrants...)
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
	if err := ntable.Create(ctx, admin, demo(), now); err != nil {
		t.Fatal(err)
	}
	for _, row := range []string{"build", "test"} {
		if _, err := ntable.RowAdd(ctx, admin, "demo", row, ntable.RowSpec{}); err != nil {
			t.Fatal(err)
		}
	}
	seedTwo(t, ctx, admin)
	// a field one byte over the bound a batch may touch
	if err := admin.HSet(ctx, ntable.MemberKey("a"), "big", strings.Repeat("v", ntable.LimitBatchValueBytes+1)).Err(); err != nil {
		t.Fatal(err)
	}
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
	if err != nil || len(ans) < 5 || ans[0] != "REFUSED" || ans[1] != "LIMIT" || ans[2] != "value bytes per batch" {
		t.Fatalf("with every read granted: %.200v %v; want LIMIT value bytes per batch", ans, err)
	}
	// the count stopped where it passed the bound, and the refusal says so
	if len(ans) < 7 || ans[6] != "at least" {
		t.Errorf("the refusal from the counting pass does not say `at least`: %.200v", ans)
	}
	// one read denied: an error naming the command, never an accepted batch, and
	// nothing written
	for _, denied := range batchReads {
		user := "ns-no-" + strings.TrimPrefix(denied, "+")
		for name, fields := range map[string][]string{"big": {"big", "gone"}, "small": {"gone"}} {
			ans, err := rawApply(ctx, as(user), batch("no-"+denied[1:]+"-"+name, fields...))
			if err == nil || !strings.Contains(err.Error(), denied[1:]) {
				// the count stops the big batch before the head is read, so hlen and
				// hmget are met by the small batch only
				if name == "big" && (denied == "+hlen" || denied == "+hmget") && err == nil && len(ans) > 1 && ans[1] == "LIMIT" {
					continue
				}
				t.Errorf("without %s, %s batch: %.200v %v; want an error naming the command", denied, name, ans, err)
			}
			if err == nil && len(ans) > 0 && ans[0] == "OK" {
				t.Errorf("without %s, %s batch: accepted", denied, name)
			}
		}
	}
	if after := storeImage(t, admin); !reflect.DeepEqual(before, after) {
		t.Errorf("a batch with a read denied changed the store")
	}
	if ans, err := rawApply(ctx, as("ns-all"), batch("all-small", "gone")); err != nil || ans[0] != "OK" {
		t.Errorf("the small batch with every read granted: %.200v %v", ans, err)
	}
}
