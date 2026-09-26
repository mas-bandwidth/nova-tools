//go:build functional

package reconcile_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
)

// TestExplainReadsACardAsThePassDoes (#4399 item 5): `ready --why <id>` on a
// waiting card reads its DEPENDS-ON the way the waiting-resolve duty's next
// pass will, by the bare id or task:<id>: none is released, a landed task
// id is met, an unlanded one is on, one with no record is unknown, an empty
// blocked_on is no evidence; a card in another column says where it is and
// an id with no record is not found.
func TestExplainReadsACardAsThePassDoes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, c := wstest.Start(t)
	wrTask(t, c, "none", "waiting", 1, "blocked_on", "none")
	wrTask(t, c, "base", "landed", 2)
	wrTask(t, c, "open", "working", 3)
	wrTask(t, c, "mixed", "waiting", 4, "blocked_on", "base,open,ghost")
	wrTask(t, c, "met", "waiting", 5, "blocked_on", "task:base")
	wrTask(t, c, "bare", "waiting", 6)
	for _, tc := range []struct {
		id               string
		where            string
		released, empty  bool
		met, on, unknown string
	}{
		{"none", "waiting", true, false, "", "", ""},
		{"task:none", "waiting", true, false, "", "", ""},
		{"met", "waiting", true, false, "task:base", "", ""},
		{"mixed", "waiting", false, false, "base", "open", "ghost"},
		{"bare", "waiting", false, true, "", "", ""},
		{"open", "working", false, false, "", "", ""},
	} {
		w, found, err := reconcile.Explain(ctx, c, tc.id)
		if err != nil || !found || w.Where != tc.where || w.Released() != tc.released || w.Empty != tc.empty ||
			strings.Join(w.Met, ",") != tc.met || strings.Join(w.On, ",") != tc.on || strings.Join(w.Unknown, ",") != tc.unknown {
			t.Errorf("%s: %+v found=%v err=%v released=%v", tc.id, w, found, err, w.Released())
		}
	}
	if _, found, err := reconcile.Explain(ctx, c, "nobody"); found || err != nil {
		t.Fatalf("an id with no record: found=%v err=%v", found, err)
	}
}
