package fn_test

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// TestHarvestPRWritesTheBareRecordKey is #3740 on the library alone: FCALL
// ns_harvest_pr with an owner-form repo (the card's repo field,
// mas-bandwidth/nova-tools) writes the PR record under the one key
// pr:nova-tools:<n> (internal/nsprint/prkey) and never under
// pr:mas-bandwidth/nova-tools:<n>, which read post cannot find.
func TestHarvestPRWritesTheBareRecordKey(t *testing.T) {
	addr := testutil.Start(t)
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	const (
		S     = "fn-prkey-0000"
		bench = "b1"
		label = "card-a"
		repo  = "mas-bandwidth/nova-tools"
		head  = "0123456789abcdef0123456789abcdef01234567"
	)
	branch := "nova/" + S + "/" + label + "-a1"
	if err := c.HSet(ctx, "s:"+S+":card:"+label, "state", "ended", "outcome", "DONE", "bench", bench,
		"attempt", "1", "repo", repo, "pushed_sha", head, "harvest_step", "pushed", "base", "dev").Err(); err != nil {
		t.Fatal(err)
	}
	if r := c.FCall(ctx, "ns_harvest_lease", nil, bench, "holder", "tok", 60000).Val(); r != "TAKEN" {
		t.Fatalf("ns_harvest_lease = %v", r)
	}
	if r := c.FCall(ctx, "ns_harvest_pr", nil, S, bench, "holder", "tok", label, repo, branch, "3726", head).Val(); r != "PR|3726" {
		t.Fatalf("ns_harvest_pr = %v, want PR|3726", r)
	}
	if got := c.HGet(ctx, "pr:nova-tools:3726", "head").Val(); got != head {
		t.Fatalf("pr:nova-tools:3726 head = %q, want %s", got, head)
	}
	if c.Exists(ctx, "pr:mas-bandwidth/nova-tools:3726").Val() != 0 {
		t.Fatal("ns_harvest_pr wrote pr:mas-bandwidth/nova-tools:3726")
	}
}
