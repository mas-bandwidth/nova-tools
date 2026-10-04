package store

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bars nova-config's apply gives the store: the landed score's (docs/SPEC-SPRINT.md
// section 7) and the judgment decision's (section 8, read by answer through routes
// --json). Mem.SetScoreBar and Mem.SetJudgmentBar apply them as the live apply does;
// Store.JudgmentBar is the read's way back, with the routes. These
// are the routes.go bar functions no other store test reaches: each row pins the
// bar that arrives and its one refusal, the read of a store that does not answer.

func TestRoutesCoverSetScoreBar(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		bars []string
		want string
	}{
		{"one apply", []string{"0.7"}, "0.7"},
		{"the last apply holds", []string{"0.5", "0.75"}, "0.75"},
		{"turned off", []string{"0.7", ""}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.m.SetJudgmentBar("0.9")
			for _, bar := range tc.bars {
				h.m.SetScoreBar(bar)
			}
			set, err := h.st.routes(h.ctx)
			require.NoError(t, err)
			assert.Equal(t, tc.want, set.Bars.Score, "the routes read carries the bar the last apply gave")
			assert.Equal(t, "0.9", set.Bars.Judgment, "no other bar moves: none is ever read as another")
		})
	}
}

func TestRoutesCoverSetJudgmentBar(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		bars []string
		want string
	}{
		{"one apply", []string{"0.9"}, "0.9"},
		{"the last apply holds", []string{"0.7", "0.95"}, "0.95"},
		{"turned off", []string{"0.9", ""}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.m.SetScoreBar("0.5")
			for _, bar := range tc.bars {
				h.m.SetJudgmentBar(bar)
			}
			set, err := h.st.routes(h.ctx)
			require.NoError(t, err)
			assert.Equal(t, tc.want, set.Bars.Judgment, "the routes read carries the bar the last apply gave")
			assert.Equal(t, "0.5", set.Bars.Score, "no other bar moves: none is ever read as another")
		})
	}
}

// Store.JudgmentBar is what routes --json hands nova-sprint answer: the applied
// judgment bar, "" when none is applied; a store that does not answer the routes
// read is a refusal, never a silent no bar.
func TestRoutesCoverJudgmentBar(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		bar    string
		refuse bool
	}{
		{"the applied bar", "0.9", false},
		{"no bar applied", "", false},
		{"a refused read", "0.9", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.m.SetScoreBar("0.5")
			h.m.SetJudgmentBar(tc.bar)
			if tc.refuse {
				h.m.Fail = func(point string) error {
					if point == "routes" {
						return errors.New("cut")
					}
					return nil
				}
				bar, err := h.st.JudgmentBar(h.ctx)
				require.Error(t, err)
				assert.ErrorContains(t, err, "the store did not answer")
				assert.Empty(t, bar, "a refusal is not a silent no bar")
				h.m.Fail = nil
			}
			bar, err := h.st.JudgmentBar(h.ctx)
			require.NoError(t, err)
			assert.Equal(t, tc.bar, bar)
		})
	}
}
