//go:build functional

package tset

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// WRONGTYPE and NOPERM are faults in Redis's physical key types and ACLs.
// Mem models valid logical state, so these two gates exercise the installed
// writer on a real Redis server rather than adding synthetic Mem error hooks.
func TestRefuseWRONGTYPE(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	profileDefineWork(t, fx)
	cell := fixtureCellKey(fx.Space, "work", "0", "r", "ready")
	if err := fx.Client.Set(context.Background(), cell, "not-a-zset", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if kind, err := fx.Client.Type(context.Background(), cell).Result(); err != nil || kind != "string" {
		t.Fatalf("corrupt cell TYPE = %q, %v; want string", kind, err)
	}
	fx.Activate(t)
	raw, err := EncodeStep(profileCreateStep(fx.Space))
	if err != nil {
		t.Fatal(err)
	}
	profileRefusalUnchanged(t, fx, "WRONGTYPE", nil, Version, string(raw))
	commitProbeNoKeys(t, fx.Client, []string{fixtureRecordKey(fx.Space, "work", "card-1")})
}

func TestRefuseNOPERM(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		user  string
		rules []string
		prove func(*testing.T, *redis.Client, *tsetFixture)
	}{
		{
			name: "command denied", user: "tset_no_hset",
			rules: []string{"~l1:*", "+@all", "-hset"},
			prove: func(t *testing.T, client *redis.Client, fx *tsetFixture) {
				t.Helper()
				err := client.HSet(context.Background(), fx.Space+"acl:probe", "field", "value").Err()
				storeFaultRequireNOPERM(t, err)
			},
		},
		{
			name: "key denied", user: "tset_no_cell",
			rules: []string{
				"~l1:sprint:*", "~l1:table:work", "~l1:table:work:definition",
				"~l1:table:work:rows", "~l1:member:work:*", "+@all",
			},
			prove: func(t *testing.T, client *redis.Client, fx *tsetFixture) {
				t.Helper()
				cell := fixtureCellKey(fx.Space, "work", "0", "r", "ready")
				err := client.ZAdd(context.Background(), cell, redis.Z{Score: 1, Member: "probe"}).Err()
				storeFaultRequireNOPERM(t, err)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fx := newTSetFixture(t)
			profileDefineWork(t, fx)
			ctx := context.Background()
			arguments := []any{"ACL", "SETUSER", tc.user, "reset", "on", ">tset-test-pass"}
			for _, rule := range tc.rules {
				arguments = append(arguments, rule)
			}
			if err := fx.Client.Do(ctx, arguments...).Err(); err != nil {
				t.Fatalf("set restricted ACL user: %v", err)
			}
			fx.Activate(t)
			client := redis.NewClient(&redis.Options{
				Addr: fx.Client.Options().Addr, Username: tc.user,
				Password: "tset-test-pass", MaxRetries: -1,
			})
			t.Cleanup(func() { _ = client.Close() })
			if err := client.Ping(ctx).Err(); err != nil {
				t.Fatalf("restricted ACL user cannot connect: %v", err)
			}
			tc.prove(t, client, fx)
			before := commitProbeImage(t, fx.Client)
			raw, err := EncodeStep(profileCreateStep(fx.Space))
			if err != nil {
				t.Fatal(err)
			}
			reply := profileCall(t, client, "ns_tset_step", nil, Version, string(raw))
			if reply.Status != "refused" || reply.Code != "NOPERM" ||
				!strings.HasSuffix(reply.Message, "; nothing was changed") {
				t.Fatalf("restricted FCALL reply = %+v, want NOPERM no-change refusal", reply)
			}
			if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
				t.Fatal("NOPERM refusal changed the whole Redis key image")
			}
			commitProbeNoKeys(t, fx.Client, []string{
				fixtureRecordKey(fx.Space, "work", "card-1"),
				fixtureCellKey(fx.Space, "work", "0", "r", "ready"),
			})
		})
	}
}

func storeFaultRequireNOPERM(t *testing.T, err error) {
	t.Helper()
	if err == nil || !strings.Contains(strings.ToUpper(err.Error()), "NOPERM") {
		t.Fatalf("ACL probe error = %v, want real Redis NOPERM", err)
	}
}
