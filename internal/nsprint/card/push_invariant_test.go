//go:build functional

package card_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
)

// TestCardPushRefusesNotOneInvariant (#4396): card push (and card cut, which
// pushes through PushBatch) refuses a card that is not one invariant before
// any write, one REFUSED card-lint line per rule, naming the card file.
func TestCardPushRefusesNotOneInvariant(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	f := validCard(srv.URL + "/acme/public.git")
	f.label = "card-4396"
	f.omit = map[string]bool{"INVARIANT": true}
	body := append(f.render(), []byte("\nBUILD:\n- the parser\n- the linter\n")...)
	res := card.PushBatch(ctx, client, sprint, []card.CardFile{{Name: "list.md", Body: body}}, card.PushOptions{})[0]
	want := `REFUSED card-lint rule=invariant-missing line="" remedy="add INVARIANT: <the one sentence the class test proves>" card="list.md"` + "\n" +
		`REFUSED card-lint rule=build-list line="BUILD:" remedy="cut as a parent with children: card cut --parent" card="list.md"` + "\n"
	if res.Code != 2 || res.Stderr != want || res.Stdout != "" {
		t.Fatalf("exit %d stdout %q stderr\n%s\nwant exit 2 and\n%s", res.Code, res.Stdout, res.Stderr, want)
	}
	if n := client.Exists(ctx, keyCard(f.label)).Val(); n != 0 {
		t.Fatal("a refused card was written")
	}
	if keys := client.Keys(ctx, "*").Val(); len(keys) != 0 {
		t.Fatalf("a refused push wrote %v", keys)
	}
	f.omit = nil
	mustPush(t, ctx, client, f.render(), "pool")
	if !strings.Contains(string(f.render()), "\nINVARIANT: ") {
		t.Fatal("the fixture carries no INVARIANT line")
	}
}
