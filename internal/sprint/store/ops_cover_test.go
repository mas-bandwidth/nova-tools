package store

import (
	"errors"
	"testing"

	"github.com/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOpsCoverHeldMainPath drives Held over a live in-memory sprint (no
// store, no wall clock): a ready card under a STOPPED machine is held by the
// next tick, a landed card is done, and an id off the table stalls with its
// why. The whole read (fence, tables, machine, the no-stall rule's answer) is
// what these rows pin.
func TestOpsCoverHeldMainPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		id      string
		arrange func(t *testing.T, h *harness)
		wantBy  string
		wantIn  string // a substring of the Hold's place
		wantWhy string // a substring of the Hold's why; "" pins a hold with none
	}{
		{
			name: "a ready card under a stopped machine is held by the next tick",
			id:   "s1-1",
			arrange: func(t *testing.T, h *harness) {
				h.setup(1)
			},
			wantBy:  sprint.HeldByStopped,
			wantIn:  "s1-1 ready",
			wantWhy: "the machine is STOPPED",
		},
		{
			name: "a landed card is done",
			id:   "s1-1",
			arrange: func(t *testing.T, h *harness) {
				h.setup(1)
				h.through("s1-1")
				h.must(MergeStep(sprint.MergeReq{Stream: "s1", Batch: 2}))
			},
			wantBy:  sprint.HeldDone,
			wantIn:  "landed",
			wantWhy: "",
		},
		{
			name: "an id off the table stalls and says so",
			id:   "s1-9",
			arrange: func(t *testing.T, h *harness) {
				h.setup(1)
			},
			wantBy:  "",
			wantIn:  "s1-9",
			wantWhy: "not on the table",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			tc.arrange(t, h)
			hd, err := h.st.Held(h.ctx, tc.id)
			require.NoError(t, err)
			assert.Equal(t, tc.id, hd.ID)
			assert.Equal(t, tc.wantBy, hd.By)
			assert.Contains(t, hd.Place, tc.wantIn)
			if tc.wantWhy == "" {
				assert.Empty(t, hd.Why, "a done hold carries no why")
			} else {
				assert.Contains(t, hd.Why, tc.wantWhy)
			}
		})
	}
}

// TestOpsCoverHeldRefusesWhilePendingAndWhenTheFenceIsLost covers Held's two
// early answers: with an operation pending, the tables are a partial state of
// it and the hold names the remedy (repair); with the fence unreadable, the
// read fails.
func TestOpsCoverHeldRefusesWhilePendingAndWhenTheFenceIsLost(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		failAt string // the store call Mem fails once, "" for none
		want   func(t *testing.T, h *harness, hd sprint.Hold)
	}{
		{
			name:   "an operation pending holds the answer for repair",
			failAt: "apply t-work before",
			want: func(t *testing.T, h *harness, hd sprint.Hold) {
				pend := h.m.Pending()
				require.NotNil(t, pend, "the fence holds the cut deal")
				assert.Equal(t, "s1-1", hd.ID)
				assert.Equal(t, "s1-1", hd.Place)
				assert.Contains(t, hd.Why, "operation "+pend.ID+" ("+pend.Verb+") is pending")
				assert.Contains(t, hd.Why, "run: nova-sprint repair")
			},
		},
		{
			name:   "a fence that does not answer is an error",
			failAt: "fence",
			want: func(t *testing.T, h *harness, hd sprint.Hold) {
				assert.Empty(t, hd.ID, "no hold comes back with the fence lost")
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.setup(1)
			h.m.Fail = func(p string) error {
				if p == tc.failAt {
					return errors.New("connection reset")
				}
				return nil
			}
			if tc.failAt == "apply t-work before" {
				_, err := h.st.Run(h.ctx, DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
				require.ErrorIs(t, err, ErrUnknown)
				require.NotNil(t, h.m.Pending())
			}
			hd, err := h.st.Held(h.ctx, "s1-1")
			if tc.failAt == "fence" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			h.m.Fail = nil
			tc.want(t, h, hd)
		})
	}
}
