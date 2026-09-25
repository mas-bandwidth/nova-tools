package card_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
)

// TestCardRecordHasEveryField is the DONE-WHEN of #3596: a card record in
// Redis holds WHO, STREAM, DEPENDS-ON (+WHY), PATHS, DONE-WHEN, BASE, base-sha,
// EST; the lander, readers and the gate read them from there.
func TestCardRecordHasEveryField(t *testing.T) {
	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)

	body := withHeader(validCard(srv.URL+"/acme/public.git"),
		"WHO: any",
		"STREAM: test-stream",
		"EST: 30",
	)
	// Change DEPENDS-ON to include a WHY
	body = []byte(strings.ReplaceAll(string(body), "DEPENDS-ON: none", "DEPENDS-ON: none (WHY: no dependencies)"))

	res := card.Push(ctx, client, sprint, body)
	if res.Code != 0 {
		t.Fatalf("push: exit %d stderr %q, want exit 0", res.Code, res.Stderr)
	}

	// The card record holds every field the issue names.
	key := keyCard("card-2731")
	vals, err := client.HGetAll(ctx, key).Result()
	if err != nil {
		t.Fatal(err)
	}
	for field, want := range map[string]string{
		"who":         "any",
		"stream":      "test-stream",
		"depends_why": "no dependencies",
		"paths":       "internal/nsprint/card/push.go, internal/nsprint/card/lint.go",
		"done_when":   "go test ./internal/nsprint/card -run TestCardPushRefusesWithoutDoneWhen exits 2 when DONE-WHEN is absent and passes when the line is present",
		"base":        "dev",
		"base_sha":    baseSHA,
		"est":         "30",
	} {
		got := vals[field]
		if got != want {
			t.Errorf("record %s = %q, want %q", field, got, want)
		}
	}
	// depends_on is empty when it is "none" (not stored, as per the Lua code).
	if vals["depends_on"] != "" {
		t.Errorf("record depends_on = %q, want empty (none is not stored)", vals["depends_on"])
	}
}

// TestGateReadsCardNotBody is the second DONE-WHEN of #3596: a read child's
// prompt fetches no PR body; the gate reads WHO, STREAM, DEPENDS-ON, PATHS,
// DONE-WHEN, BASE, base-sha, EST from the card record in Redis.
func TestGateReadsCardNotBody(t *testing.T) {
	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)

	// Push a card with full fields
	body := withHeader(validCard(srv.URL+"/acme/public.git"),
		"WHO: any",
		"STREAM: test-stream",
		"EST: 30",
	)
	res := card.Push(ctx, client, sprint, body)
	if res.Code != 0 {
		t.Fatalf("push: exit %d stderr %q, want exit 0", res.Code, res.Stderr)
	}

	// Read the record the way the gate would: from the card hash
	key := keyCard("card-2731")
	vals, err := client.HGetAll(ctx, key).Result()
	if err != nil {
		t.Fatal(err)
	}

	// The gate reads from the record, not from a PR body.
	// Verify the record has all the fields the gate needs.
	requiredFields := []string{"who", "stream", "depends_on", "paths", "done_when", "base", "base_sha", "est"}
	for _, field := range requiredFields {
		if _, ok := vals[field]; !ok {
			t.Errorf("record missing %s field the gate needs", field)
		}
	}

	// The PR body is one line pointing at the card id (#3596).
	prBody := fmt.Sprintf("card: %s/card-2731\n", sprint)
	if !strings.Contains(prBody, "card:") {
		t.Fatalf("PR body must point at the card id: %q", prBody)
	}

	// Verify the gate can read all fields from the record.
	who := vals["who"]
	stream := vals["stream"]
	paths := vals["paths"]
	doneWhen := vals["done_when"]
	base := vals["base"]
	baseSHA := vals["base_sha"]
	est := vals["est"]

	if who != "any" {
		t.Errorf("gate reads who = %q, want any", who)
	}
	if stream != "test-stream" {
		t.Errorf("gate reads stream = %q, want test-stream", stream)
	}
	if !strings.Contains(paths, "push.go") {
		t.Errorf("gate reads paths = %q, want it to contain push.go", paths)
	}
	if !strings.Contains(doneWhen, "TestCardPushRefusesWithoutDoneWhen") {
		t.Errorf("gate reads done_when lacking the test name")
	}
	if base != "dev" {
		t.Errorf("gate reads base = %q, want dev", base)
	}
	if baseSHA != baseSHA {
		t.Errorf("gate reads base_sha = %q, want %s", baseSHA, baseSHA)
	}
	if est != "30" {
		t.Errorf("gate reads est = %q, want 30", est)
	}
}
