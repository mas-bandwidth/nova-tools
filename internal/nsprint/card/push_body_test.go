package card_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
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
