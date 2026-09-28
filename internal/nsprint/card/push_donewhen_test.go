//go:build functional

package card_test

import (
	"context"
	"testing"
)

// TestCardPushStoresDoneWhenAndTask is #3712's push half: the card record
// holds the card's DONE-WHEN and TASK sentences, so harvest writes the PR body
// from Redis alone.
func TestCardPushStoresDoneWhenAndTask(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	f := validCard(srv.URL + "/acme/public.git")
	f.label = "card-3712"
	body := append(f.render(), []byte("TASK: open the card PR with the typed body\n")...)
	mustPush(t, ctx, client, body, "pool")
	got := client.HMGet(ctx, keyCard(f.label), "done_when", "task").Val()
	if got[0] != f.done {
		t.Fatalf("done_when = %v, want the card's DONE-WHEN %q", got[0], f.done)
	}
	if got[1] != "open the card PR with the typed body" {
		t.Fatalf("task = %v, want the card's TASK sentence", got[1])
	}

	f.label = "card-3712-notask"
	mustPush(t, ctx, client, f.render(), "pool")
	if n := client.HExists(ctx, keyCard(f.label), "task").Val(); n {
		t.Fatal("a card with no TASK line has a task field")
	}
}
