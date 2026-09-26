package ci

import (
	"context"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// ReconcileReruns is the test-side rerun pass the CI control tests drive: one
// Rerun per ended, owned, unblocked card whose verdict is FAIL or MISSING. No
// verb reaches it any more (the reconciler's pass lives in Lua), so it is a
// fixture here rather than product code.
// model decides it. It returns the results by label.
func ReconcileReruns(ctx context.Context, st *store.Store, sprint string) (map[string]Result, error) {
	cards, err := Cards(ctx, st, sprint)
	if err != nil {
		return nil, err
	}
	out := map[string]Result{}
	for _, c := range cards {
		if c.State != "ended" || !c.Owner || c.Blocked != "" {
			continue
		}
		if c.Verdict != Fail && c.Verdict != Missing {
			continue
		}
		r, err := Rerun(ctx, st, sprint, c.Label, "reconciler", "policy ci_reruns")
		if err != nil {
			return out, err
		}
		if r.Status == "SPENT" {
			continue
		}
		out[c.Label] = r
	}
	return out, nil
}
