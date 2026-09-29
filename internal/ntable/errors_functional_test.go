//go:build functional

package ntable_test

// An error the table library does not turn into a refusal leaves the script as
// an error, unchanged: a denial by the store's ACL is not "malformed", and the
// script adds no prefix of its own.

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

func TestKeyACLDenialOnCellKeysIsAnErrorNotAMalformedReply(t *testing.T) {
	t.Parallel()
	perms := []string{"--user", "default", "on", "nopass", "~*", "&*", "+@all",
		"--user", "nc", "on", ">pw", "resetkeys", "resetchannels", "-@all", "+@all",
		"~table:demo", "~table:demo:rows", "~table:demo:row:*", "~table:demo:definition", "~table:demo:revision",
		"~table:demo:identity", "~table:demo:changes", "~table:demo:ops", "~table::member:*", "~tables"}
	addr, admin := live(t, perms...)
	ctx := context.Background()
	if err := ntable.Create(ctx, admin, demo(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, admin, "demo", "build", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}
	if err := ntable.MemberCreate(ctx, admin, "demo", "q"); err != nil {
		t.Fatal(err)
	}
	nc := redis.NewClient(&redis.Options{Addr: addr, Username: "nc", Password: "pw"})
	defer nc.Close()
	rev := admin.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()
	m := ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: rev, OperationID: "acl", Members: []ntable.BatchMemberEntry{
		{ID: "q", Expect: &ntable.MemberExpect{}, Move: &ntable.MemberMoveOp{Row: "build", Col: "ready"}}}}
	if _, err := ntable.ApplyBatch(ctx, nc, m); err == nil || strings.Contains(err.Error(), "malformed") || !strings.Contains(err.Error(), "ACL failure") || strings.Contains(err.Error(), "ERR ERR") {
		t.Errorf("ApplyBatch under a key ACL that denies the cell keys: %v; want the store's ACL error, once prefixed", err)
	}
	if _, err := ntable.ReadSetMembers(ctx, nc, "demo", []string{"q"}); err == nil || strings.Contains(err.Error(), "malformed") || !strings.Contains(err.Error(), "ACL failure") || strings.Contains(err.Error(), "ERR ERR") {
		t.Errorf("ReadSet: %v; want the store's ACL error, once prefixed", err)
	}
	if _, err := ntable.Check(ctx, nc, "demo"); err == nil || strings.Contains(err.Error(), "malformed") || !strings.Contains(err.Error(), "ACL failure") || strings.Contains(err.Error(), "ERR ERR") {
		t.Errorf("Check: %v; want the store's ACL error, once prefixed", err)
	}
}
