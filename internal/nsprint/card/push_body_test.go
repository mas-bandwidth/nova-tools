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
	t.Parallel()

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

// TestPushedCardRunsThroughCardRun (nova-tools#3809): a card pushed by card
// push carries its body at BodyKey with the retired pusher's 7-day TTL and
// runs through card run on a throwaway store, so rowan-tools'
// bin/nova-card-push (the bash EVAL SET with a 7-day PX) can be retired.
func TestPushedCardRunsThroughCardRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	client := newRedis(t)
	srv := repoServer(t)
	c := validCard(srv.URL + "/acme/public.git")
	c.label = "push-run-3809"
	body := c.render()
	if res := card.Push(ctx, client, sprint, body); res.Code != 0 {
		t.Fatalf("push: exit %d stderr %q", res.Code, res.Stderr)
	}
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	key := card.BodyKey(sprint, sha)

	// The TTL: SETNX with the retired pusher's 7-day PX, not a persistent
	// body (#3809). The expectation is the spec value, not BodyTTL, so the
	// test fails on a push that forgets the TTL or shortens it.
	ttl, err := client.PTTL(ctx, key).Result()
	if err != nil {
		t.Fatal(err)
	}
	want := 7 * 24 * time.Hour
	if ttl <= 0 || ttl > want || ttl < want-time.Second {
		t.Fatalf("body TTL %s, want within 1s of %s", ttl, want)
	}

	root := t.TempDir()
	seen := filepath.Join(root, "seen")
	cfg := card.RunConfig{
		Sprint: sprint, Label: c.label, Attempt: 1, Bench: "testbench",
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
		t.Fatalf("a pushed card refused to run: %s", rep.Line())
	}
	if data, err := os.ReadFile(filepath.Join(seen, "card.md")); err != nil || string(data) != string(body) {
		t.Fatalf("the runner saw card bytes %q (%v), want the pushed bytes", data, err)
	}
}
