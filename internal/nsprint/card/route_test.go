package card_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
)

// withHeader renders the card with extra header lines above KIND.
func withHeader(f cardFix, lines ...string) []byte {
	body := string(f.render())
	if len(lines) == 0 {
		return []byte(body)
	}
	return []byte(strings.Replace(body, "\nKIND:", "\n"+strings.Join(lines, "\n")+"\nKIND:", 1))
}

func TestCardPushStoresRoute(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	for _, tc := range []struct {
		label string
		lines []string
		want  string
	}{
		{"route-pro", []string{"ROUTE: pro"}, "pro"},
		{"route-flash", []string{"ROUTE: flash"}, "flash"},
		{"route-absent", nil, "flash"},
	} {
		f := validCard(srv.URL + "/acme/public.git")
		f.label = tc.label
		res := card.Push(ctx, client, sprint, withHeader(f, tc.lines...))
		if res.Code != 0 {
			t.Fatalf("%s: exit %d stderr %q, want exit 0", tc.label, res.Code, res.Stderr)
		}
		got, err := client.HGet(ctx, keyCard(tc.label), "route").Result()
		if err != nil || got != tc.want {
			t.Fatalf("%s: route %q (%v), want %q", tc.label, got, err, tc.want)
		}
	}
}

func TestCardLintRefusesBadRoute(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	for i, line := range []string{"ROUTE: turbo", "ROUTE: PRO", "ROUTE: pro flash", "ROUTE: ocflash", "ROUTE:"} {
		f := validCard(srv.URL + "/acme/public.git")
		f.label = "bad-route-" + string(rune('a'+i))
		res := card.Push(ctx, client, sprint, withHeader(f, line))
		if res.Code != 2 || res.Stdout != "" || !strings.Contains(res.Stderr, "ROUTE") {
			t.Fatalf("%q: exit %d stdout %q stderr %q, want exit 2 naming ROUTE", line, res.Code, res.Stdout, res.Stderr)
		}
		assertAbsent(t, ctx, client, f.label)
	}
}

// TestCardPushPriorityIsTheRecordField: PRIORITY: is the record's priority
// field, the dealer's order; the pool is scored by the card's created_at like
// every view (#3692: age order, uniformly).
func TestCardPushPriorityIsTheRecordField(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	for _, tc := range []struct {
		label string
		lines []string
		field string
	}{
		{"prio-seven", []string{"PRIORITY: 7"}, "7"},
		{"prio-negative", []string{"PRIORITY: -3"}, "-3"},
		{"prio-absent", nil, "0"},
	} {
		f := validCard(srv.URL + "/acme/public.git")
		f.label = tc.label
		res := card.Push(ctx, client, sprint, withHeader(f, tc.lines...))
		if res.Code != 0 || !strings.Contains(res.Stdout, "place=pool") {
			t.Fatalf("%s: exit %d stdout %q stderr %q, want pool", tc.label, res.Code, res.Stdout, res.Stderr)
		}
		score, err := client.ZScore(ctx, keyPool(), tc.label).Result()
		created, _ := strconv.ParseFloat(client.HGet(ctx, keyCard(tc.label), "created_at").Val(), 64)
		if err != nil || score != created || created == 0 {
			t.Fatalf("%s: pool score %v (%v), want the card's created_at %v", tc.label, score, err, created)
		}
		if got, _ := client.HGet(ctx, keyCard(tc.label), "priority").Result(); got != tc.field {
			t.Fatalf("%s: priority field %q, want %q", tc.label, got, tc.field)
		}
	}
	f := validCard(srv.URL + "/acme/public.git")
	f.label = "prio-bad"
	res := card.Push(ctx, client, sprint, withHeader(f, "PRIORITY: high"))
	if res.Code != 2 || !strings.Contains(res.Stderr, "PRIORITY") {
		t.Fatalf("PRIORITY: high: exit %d stderr %q, want exit 2 naming PRIORITY", res.Code, res.Stderr)
	}
	assertAbsent(t, ctx, client, f.label)
}
