//go:build functional

package land

// A store that does not answer a lookup in EvaluateUnit is a closed verdict
// naming the operation, the key and the error (FG-A fix 1): never "ci
// nopolicy", "ci missing", "card not done at head", "stack parent X ()" or
// zero holds. One test per site, each with a WRONGTYPE or a dial failure.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

const (
	storeSprint = "store-eval"
	storeRepo   = "nova-tools"
	storeBase   = "dev"
	storeHead   = "abcdefabcdefabcdefabcdefabcdefabcdefabcd"
)

var errDialRefused = errors.New("dial tcp 127.0.0.1:1: connect: connection refused")

// failOn is a go-redis hook that answers one command on one key with a
// dial failure, so a store error on one lookup is deterministic.
type failOn struct{ name, key string }

func (h failOn) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h failOn) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h failOn) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if strings.EqualFold(cmd.Name(), h.name) && len(cmd.Args()) > 1 && fmt.Sprint(cmd.Args()[1]) == h.key {
			return errDialRefused
		}
		return next(ctx, cmd)
	}
}

// storeUnit is a unit with policy and an OK CI receipt at head, one hold
// short of landable, in a throwaway store with the library loaded.
func storeUnit(t *testing.T, unit string, extra ...string) (context.Context, *redis.Client, string) {
	t.Helper()
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: testutil.Start(t)})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := c.HSet(ctx, PolicyKey(storeRepo, storeBase), "policy_id", "p", "required_set_id", "r", "runner_id", "x").Err(); err != nil {
		t.Fatal(err)
	}
	gid := GID("single", storeBase, "b0", "r", "p", "x")
	if err := c.HSet(ctx, CIKey(storeRepo, storeHead, gid), "verdict", "OK").Err(); err != nil {
		t.Fatal(err)
	}
	fields := []string{"repo", storeRepo, "base", storeBase, "head", storeHead, "base_sha", "b0",
		"state", "open", "seq", "1", "card_done", storeHead, "author", "a"}
	fields = append(fields, extra...)
	if err := c.HSet(ctx, UnitKey(storeSprint, unit), fields).Err(); err != nil {
		t.Fatal(err)
	}
	return ctx, c, gid
}

func closed(t *testing.T, ctx context.Context, c *redis.Client, unit string, wants ...string) *UnitEvalResult {
	t.Helper()
	res, err := EvaluateUnit(ctx, c, storeSprint, unit, nil)
	if err != nil {
		t.Fatalf("EvaluateUnit %s: %v", unit, err)
	}
	if res.Landable {
		t.Fatalf("%s landable with reason %q; want a closed verdict", unit, res.Reason)
	}
	for _, w := range wants {
		if !strings.Contains(res.Reason, w) {
			t.Fatalf("%s reason %q does not name %q", unit, res.Reason, w)
		}
	}
	if st, _ := c.HGet(ctx, UnitKey(storeSprint, unit), "state").Result(); st == "landable" {
		t.Fatalf("%s moved to landable on a closed verdict", unit)
	}
	return res
}

func TestEvalUnitPolicyWrongTypeIsNamed(t *testing.T) {
	t.Parallel()
	ctx, c, _ := storeUnit(t, "u-policy")
	pkey := PolicyKey(storeRepo, storeBase)
	if err := c.Del(ctx, pkey).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.Set(ctx, pkey, "not a hash", 0).Err(); err != nil {
		t.Fatal(err)
	}
	res := closed(t, ctx, c, "u-policy", "HGETALL", pkey, "WRONGTYPE")
	if res.CIStatus == "nopolicy" {
		t.Fatalf("a WRONGTYPE policy key reads as nopolicy: %+v", res)
	}
}

func TestEvalUnitCIRecordWrongTypeIsNamed(t *testing.T) {
	t.Parallel()
	ctx, c, gid := storeUnit(t, "u-ci")
	ckey := CIKey(storeRepo, storeHead, gid)
	if err := c.Del(ctx, ckey).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.Set(ctx, ckey, "not a hash", 0).Err(); err != nil {
		t.Fatal(err)
	}
	res := closed(t, ctx, c, "u-ci", "HGETALL", ckey, "WRONGTYPE")
	if res.CIStatus == "missing" || res.CIStatus == "stale" {
		t.Fatalf("a WRONGTYPE ci receipt reads as %s: %+v", res.CIStatus, res)
	}
	if n, _ := c.Exists(ctx, "land:"+storeRepo+":gates").Result(); n != 0 {
		t.Fatalf("a receipt nobody could read queued a ci single")
	}
}

func TestEvalUnitCIGIDsWrongTypeIsNamed(t *testing.T) {
	t.Parallel()
	ctx, c, gid := storeUnit(t, "u-gids")
	if err := c.Del(ctx, CIKey(storeRepo, storeHead, gid)).Err(); err != nil {
		t.Fatal(err)
	}
	gkey := CIGIDsKey(storeRepo, storeHead)
	if err := c.Set(ctx, gkey, "not a set", 0).Err(); err != nil {
		t.Fatal(err)
	}
	res := closed(t, ctx, c, "u-gids", "SMEMBERS", gkey, "WRONGTYPE")
	if res.CIStatus == "missing" || res.CIStatus == "stale" {
		t.Fatalf("a WRONGTYPE gids set reads as %s: %+v", res.CIStatus, res)
	}
}

func TestEvalUnitCardDoneDialFailureIsNamed(t *testing.T) {
	t.Parallel()
	ctx, c, _ := storeUnit(t, "u-done", "card_done", "other")
	doneKey := "card:u-done:done"
	c.AddHook(failOn{name: "exists", key: doneKey})
	closed(t, ctx, c, "u-done", "EXISTS", doneKey, errDialRefused.Error())
}

func TestEvalUnitStackParentPRLookupDialFailureIsNamed(t *testing.T) {
	t.Parallel()
	ctx, c, _ := storeUnit(t, "u-parent-pr", "stack_parent", "#41")
	puKey := PRUnitKey(storeSprint, storeRepo, 41)
	c.AddHook(failOn{name: "get", key: puKey})
	closed(t, ctx, c, "u-parent-pr", "GET", puKey, errDialRefused.Error())
}

func TestEvalUnitStackParentStateDialFailureIsNamed(t *testing.T) {
	t.Parallel()
	ctx, c, _ := storeUnit(t, "u-parent-state", "stack_parent", "nova-tools#40")
	parentKey := UnitKey(storeSprint, "nova-tools#40")
	c.AddHook(failOn{name: "hget", key: parentKey})
	closed(t, ctx, c, "u-parent-state", "HGET", parentKey, errDialRefused.Error())
}

// A parent with no unit record is absent, and the verdict says so instead of
// "stack parent X ()".
func TestEvalUnitStackParentAbsentIsNamed(t *testing.T) {
	t.Parallel()
	ctx, c, _ := storeUnit(t, "u-parent-absent", "stack_parent", "nova-tools#39")
	closed(t, ctx, c, "u-parent-absent", "stack parent nova-tools#39 (no unit record "+UnitKey(storeSprint, "nova-tools#39")+")")
}

func TestEvalUnitTierUnreadableIsNamed(t *testing.T) {
	t.Parallel()
	ctx, c, _ := storeUnit(t, "u-tier", "tier", "gold")
	closed(t, ctx, c, "u-tier", "tier unreadable", UnitKey(storeSprint, "u-tier"), `"gold"`)
}

// The re-read of holds_open after a supersede: a store that does not answer
// is a closed verdict naming HGET and the key, not zero holds.
func TestEvalUnitHoldsOpenRereadDialFailureIsNamed(t *testing.T) {
	t.Parallel()
	ctx, c, _ := storeUnit(t, "u-reread")
	const unit = "u-reread"
	h1 := "1010101010101010101010101010101010101010"
	if _, err := CallUnitHead(ctx, c, UnitHeadParams{Sprint: storeSprint, Unit: unit, Repo: storeRepo, Base: storeBase, Head: h1}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := CallHold(ctx, c, storeSprint, unit, "emma", h1, "objection", "TestX red", "", "", "", "verb"); err != nil {
		t.Fatal(err)
	}
	if _, err := CallUnitHead(ctx, c, UnitHeadParams{Sprint: storeSprint, Unit: unit, Repo: storeRepo, Base: storeBase, Head: storeHead}); err != nil {
		t.Fatal(err)
	}
	if _, err := CallRead(ctx, c, storeSprint, unit, "emma", storeHead, "APPROVE", "10", "substance", "", ""); err != nil {
		t.Fatal(err)
	}
	ukey := UnitKey(storeSprint, unit)
	c.AddHook(failOn{name: "hget", key: ukey})
	closed(t, ctx, c, unit, "HGET", ukey+" holds_open", errDialRefused.Error())
}
