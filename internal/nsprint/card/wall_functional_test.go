//go:build functional

package card_test

import (
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/redis/go-redis/v9"
)

// TestCardPushStoresEst: lint reads EST: into the card hash's est field in
// minutes; prose or an absent line stores nothing and is not refused.
func TestCardPushStoresEst(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	for _, tc := range []struct {
		label string
		lines []string
		want  string
	}{
		{"est-plain", []string{"EST: 20"}, "20"},
		{"est-min", []string{"EST: 60 min"}, "60"},
		{"est-m", []string{"EST: 90m"}, "90"},
		{"est-hours", []string{"EST: 5 h"}, "300"},
		{"est-prose", []string{"EST: 1 read, ~15 min"}, ""},
		{"est-absent", nil, ""},
	} {
		f := validCard(srv.URL + "/acme/public.git")
		f.label = tc.label
		res := card.Push(ctx, client, sprint, withHeader(f, tc.lines...))
		if res.Code != 0 {
			t.Fatalf("%s: exit %d stderr %q, want exit 0", tc.label, res.Code, res.Stderr)
		}
		got, err := client.HGet(ctx, keyCard(tc.label), "est").Result()
		if tc.want == "" {
			if err != redis.Nil {
				t.Fatalf("%s: est %q (%v), want no field", tc.label, got, err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Fatalf("%s: est %q (%v), want %q", tc.label, got, err, tc.want)
		}
	}
}
