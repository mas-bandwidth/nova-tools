package card_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// TestPushStoresTheBodyTheWrapperRuns (nova-tools#4101): every push path
// stores the card body at BodyKey(sprint, payload_sha) in the push pipeline,
// so card run never refuses "no body" for a card the store lists. The probe
// sprint quack-0925-1350 failed 12 of 12 that way.
func TestPushStoresTheBodyTheWrapperRuns(t *testing.T) {
	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	c := validCard(srv.URL + "/acme/public.git")
	c.label = "body-4101"
	body := []byte(c.render())
	mustPush(t, ctx, client, body, "pool")
	sum := sha256.Sum256(body)
	got, err := client.Get(ctx, card.BodyKey(sprint, hex.EncodeToString(sum[:]))).Bytes()
	if err != nil {
		t.Fatalf("no body at the payload key after push: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("stored body differs from the pushed body")
	}
}

// TestCardPushStoresTheBodyWithTTL is the DONE-WHEN of #3809: the body card
// push stores at BodyKey(sprint, payload_sha) carries the retired bash
// nova-card-push's 7-day PX, so a body no card ever ran leaves the store on
// its own instead of sitting there for ever.
func TestCardPushStoresTheBodyWithTTL(t *testing.T) {
	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	c := validCard(srv.URL + "/acme/public.git")
	c.label = "body-ttl-3809"
	body := []byte(c.render())
	mustPush(t, ctx, client, body, "pool")
	sum := sha256.Sum256(body)
	key := card.BodyKey(sprint, hex.EncodeToString(sum[:]))
	ttl, err := client.PTTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("PTTL: %v", err)
	}
	if ttl <= 0 || ttl > 7*24*time.Hour {
		t.Fatalf("body TTL %s, want within (0, 7 days]", ttl)
	}
}

// TestPushedCardRunsThroughCardRun is the second half of #3809's DONE-WHEN: a
// card stored by card push (body content-addressed under BodyTTL) runs through
// card.Run on the same throwaway store, and the runner receives the exact
// bytes the push stored, so the retired bash nova-card-push can go away.
func TestPushedCardRunsThroughCardRun(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	f := validCard(srv.URL + "/acme/public.git")
	f.label = "push-run-3809"
	body := []byte(f.render())
	mustPush(t, ctx, client, body, "pool")

	root := t.TempDir()
	seen := filepath.Join(root, "seen")
	cfg := card.RunConfig{
		Sprint: sprint, Label: f.label, Attempt: 1, Bench: "push-run-bench",
		OutDir: filepath.Join(root, "job", "out"), JobDir: filepath.Join(root, "job"), Home: filepath.Join(root, "home"),
		Runner: self, HarnessBin: "/opt/harness/opencode", Deadline: "60", Tokens: "1000",
		Env: []string{
			fakeRunnerEnv + "=1", fakeRunnerSeen + "=" + seen, fakeRunnerResult + "=1",
			"DEEPSEEK_API_KEY=ds-val", "INCEPTION_API_KEY=in-val", "OPENCODE_API_KEY=oc-val", "OPENROUTER_API_KEY=or-val",
			"PATH=" + os.Getenv("PATH"),
		},
	}
	rep := card.Run(ctx, store.New(client), cfg)
	if rep.Code != 0 || rep.RC != 0 {
		t.Fatalf("a pushed card did not run through card run: %s", rep.Line())
	}
	got, err := os.ReadFile(filepath.Join(seen, "card.md"))
	if err != nil {
		t.Fatalf("runner recorded no card bytes: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("runner got %q, want the exact bytes the push stored", got)
	}
}
