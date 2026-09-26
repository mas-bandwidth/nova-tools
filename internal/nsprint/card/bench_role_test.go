package card_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
)

// nova-tools#3634: a BENCH: pin naming a bench whose registry role is friends
// is refused with one REFUSED line naming the role, exit 1, and nothing is
// written; the same pin to a fleet bench (role fleet, or no role) is stored.
func TestCardPushRefusesAFriendsBenchPin(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	seedBenches(t, ctx, client, "hulk", "studio", "vision")
	client.HSet(ctx, "bench:studio:desired", "role", "friends")
	client.HSet(ctx, "bench:vision:desired", "role", "fleet")

	f := validCard(srv.URL + "/acme/public.git")
	f.label = "pinned-studio"
	res := card.Push(ctx, client, sprint, withHeader(f, "BENCH: studio"))
	if res.Code != 1 || res.Stdout != "" || !strings.HasPrefix(res.Stderr, "REFUSED bench=studio role=friends: ") ||
		strings.Count(res.Stderr, "\n") != 1 {
		t.Fatalf("BENCH: studio: exit %d stdout %q stderr %q, want exit 1 and one REFUSED line naming role=friends", res.Code, res.Stdout, res.Stderr)
	}
	assertAbsent(t, ctx, client, f.label)

	for _, b := range []string{"hulk", "vision"} {
		f := validCard(srv.URL + "/acme/public.git")
		f.label = "pinned-" + b
		if res := card.Push(ctx, client, sprint, withHeader(f, "BENCH: "+b)); res.Code != 0 {
			t.Fatalf("BENCH: %s: exit %d stderr %q, want a fleet bench pin stored", b, res.Code, res.Stderr)
		}
	}
}
