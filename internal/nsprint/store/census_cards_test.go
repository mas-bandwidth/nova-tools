package store_test

import (
	"errors"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// TestCardCensusRefusals: a malformed request is refused as usage (never the
// family refusal), and every well-formed one, any sprint and any states, is
// ErrRetiredFamily (#4411): the card census reads nothing.
func TestCardCensusRefusals(t *testing.T) {
	t.Parallel()

	for name, req := range map[string]store.CardCensusRequest{
		"no sprint":      {},
		"sprint pattern": {Sprint: "s*"},
		"key pattern":    {Sprint: "s1", States: []string{"run*"}},
		"blank key":      {Sprint: "s1", States: []string{"queued", ""}},
		"twice":          {Sprint: "s1", States: []string{"queued", "queued"}},
	} {
		if err := store.RefuseCardCensus(req); err == nil || errors.Is(err, store.ErrRetiredFamily) {
			t.Errorf("%s: %v; want a usage refusal", name, err)
		}
	}
	for name, req := range map[string]store.CardCensusRequest{
		"sprint":        {Sprint: "probe-s"},
		"landed only":   {Sprint: "probe-s", States: []string{"landed"}},
		"every default": {Sprint: "quack-0926", States: []string{"queued", "dealt", "running", "landed"}},
	} {
		if err := store.RefuseCardCensus(req); !errors.Is(err, store.ErrRetiredFamily) {
			t.Errorf("%s: %v; want %v", name, err, store.ErrRetiredFamily)
		}
	}
}
