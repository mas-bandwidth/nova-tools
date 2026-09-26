//go:build functional

package land

// Rowan's failure-guidance audit (2026-09-26). EvaluateUnit reads holds_open
// with strconv.Atoi and drops the error (eval_unit.go:165), and after a
// supersede re-reads it with HGet(...).Val() (eval_unit.go:88), so an
// unreadable hold count -- a malformed field, or a store error on the re-read
// -- is zero holds, and ns_unit_eval does not look at holds again. The unit
// goes landable with a hold count nobody could read.

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

func TestRowanAuditUnreadableHoldCountIsNotZero(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: testutil.Start(t)})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	const S, unit, repo, base, head = "audit", "nova-tools#7", "nova-tools", "dev", "0123456789abcdef0123456789abcdef01234567"
	if err := c.HSet(ctx, PolicyKey(repo, base), "policy_id", "p", "required_set_id", "r", "runner_id", "x").Err(); err != nil {
		t.Fatal(err)
	}
	gid := GID("single", base, "b0", "r", "p", "x")
	if err := c.HSet(ctx, CIKey(repo, head, gid), "verdict", "OK").Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.HSet(ctx, UnitKey(S, unit), "repo", repo, "base", base, "head", head, "base_sha", "b0",
		"state", "open", "seq", "1", "card_done", head, "author", "a",
		"holds_open", "two").Err(); err != nil { // not a number: unreadable, not zero
		t.Fatal(err)
	}
	res, err := EvaluateUnit(ctx, c, S, unit, nil)
	if err == nil && res.Landable {
		t.Fatalf("an unreadable holds_open=two made %s landable (reason %q); want a refusal naming holds_open and the unit key", unit, res.Reason)
	}
	if err == nil && !strings.Contains(res.Reason, "holds_open") {
		t.Fatalf("reason %q does not name the unreadable field", res.Reason)
	}
}
