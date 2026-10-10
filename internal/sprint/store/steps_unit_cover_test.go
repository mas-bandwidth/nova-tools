package store

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RedoStep (steps.go:162) returns a Step whose Plan closure wraps sprint.Redo,
// but no unit test called this builder before. These tests drive it through
// the store on the in-memory twin.

func TestStoreStepsCoverRedoStep(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		sel        sprint.Sel
		wantRefuse string
	}{
		{"unknown card", sprint.Sel{IDs: []string{"s9-1"}}, "no such card on the table"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.setup(1)

			step := RedoStep(sprint.RedoReq{Sel: tc.sel, Who: "tester"})
			assert.Equal(t, "redo", step.Verb)
			assert.True(t, step.Named)
			assert.True(t, step.Mirrors)
			assert.True(t, step.Routes)
			assert.True(t, step.Friends)
			assert.Equal(t, tables(sprint.Work, sprint.Readers, sprint.Fleet, sprint.Merge), step.Load)

			res := h.run(step)
			require.NotEmpty(t, res.Refused)
			assert.Contains(t, res.Refused[0].Why, tc.wantRefuse)
			assert.Empty(t, res.Moved)
		})
	}
}

// LandedStep (steps.go:230) records work found on the base landed. Its Extras
// names the pins' ids on the work table.

func TestStoreStepsCoverLandedStep(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		req        sprint.LandedReq
		wantRefuse string
	}{
		{"no reason", sprint.LandedReq{Pins: []sprint.LandedPin{{ID: "s1-1", Head: "h1"}}}, "--reason"},
		{"unknown id", sprint.LandedReq{Pins: []sprint.LandedPin{{ID: "unknown", Head: "h"}}, Reason: "test reason"}, "no such card"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.setup(1)

			step := LandedStep(tc.req)
			assert.Equal(t, "landed", step.Verb)
			assert.True(t, step.Named)
			assert.True(t, step.Mirrors)
			assert.Equal(t, tables(sprint.Merge, sprint.Work), step.Load)

			extras := step.Extras(h.snap())
			assert.NotEmpty(t, extras[sprint.Work])

			res := h.run(step)
			if tc.wantRefuse != "" {
				require.NotEmpty(t, res.Refused)
				assert.Contains(t, res.Refused[0].Why, tc.wantRefuse)
				assert.Empty(t, res.Moved)
				return
			}
		})
	}
}

// MergeWindowStep (steps.go:242) opens the merge window.

func TestStoreStepsCoverMergeWindowStep(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		req        sprint.MergeWindowReq
		wantRefuse string
	}{
		{"open with duration", sprint.MergeWindowReq{For: "10m", Reason: "test"}, ""},
		{"for zero", sprint.MergeWindowReq{For: "0s", Reason: "test"}, "--for wants a duration above zero"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			step := MergeWindowStep(tc.req)
			assert.Equal(t, "merge-window open", step.Verb)
			assert.Equal(t, tables(sprint.Merge), step.Load)

			res := h.run(step)
			if tc.wantRefuse != "" {
				require.NotEmpty(t, res.Refused)
				assert.Contains(t, res.Refused[0].Why, tc.wantRefuse)
				assert.Empty(t, res.Moved)
				return
			}
			assert.Empty(t, res.Refused)
			assert.NotEmpty(t, res.Moved)
			assert.Contains(t, res.Moved[0], "merge window open until")
		})
	}
}

// CostReconcileStep (steps.go:257) reconciles provider costs.

func TestStoreStepsCoverCostReconcileStep(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	step := CostReconcileStep(sprint.CostReconcileReq{Reads: []sprint.UsageRead{{Provider: "p1", Day: "2024-01-01", Used: 100}}, Who: "tester"})
	assert.Equal(t, "cost reconcile", step.Verb)
	assert.True(t, step.Routes)
	assert.Equal(t, tables(sprint.Work, sprint.Fleet), step.Load)

	res := h.run(step)
	assert.Empty(t, res.Refused)
}

// FriendGiveStep (steps.go:348) clears the take-back mark.

func TestStoreStepsCoverFriendGiveStep(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		req        sprint.FriendGiveReq
		wantRefuse string
	}{
		{"unknown card", sprint.FriendGiveReq{Friend: "f1", IDs: []string{"unknown"}}, "no card"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.setup(1)

			step := FriendGiveStep(tc.req)
			assert.Equal(t, "friend give", step.Verb)
			assert.True(t, step.Mirrors)
			assert.Equal(t, tables(sprint.Fleet, sprint.Work), step.Load)

			res := h.run(step)
			require.NotEmpty(t, res.Refused)
			assert.Contains(t, res.Refused[0].Why, tc.wantRefuse)
			assert.Empty(t, res.Moved)
		})
	}
}

// FriendLevelStep (steps.go:354) evens friends' ready queues.

func TestStoreStepsCoverFriendLevelStep(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	step := FriendLevelStep(sprint.FriendLevelReq{})
	assert.Equal(t, "friend level", step.Verb)
	assert.True(t, step.Mirrors)
	assert.Equal(t, tables(sprint.Fleet, sprint.Work), step.Load)

	res := h.run(step)
	assert.Empty(t, res.Refused)
}

// NoteStep (steps.go:361) writes one happened note.

func TestStoreStepsCoverNoteStep(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		verb string
		note sprint.Note
	}{
		{"friend sync note", "friend sync", sprint.Note{Kind: sprint.Happened, Type: "friend sync", Stream: "s1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.setup(1)

			step := NoteStep(tc.verb, tc.note)
			assert.Equal(t, tc.verb, step.Verb)

			res := h.must(step)
			assert.NotEmpty(t, res.Notes)
		})
	}
}

// FriendSyncStateStep (steps.go:371) records sync loop standing failure.

func TestStoreStepsCoverFriendSyncStateStep(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		req      sprint.FriendSyncStateReq
		wantPlan bool
	}{
		{"failing", sprint.FriendSyncStateReq{Failing: true}, true},
		{"not failing", sprint.FriendSyncStateReq{Failing: false}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.setup(1)

			step := FriendSyncStateStep(tc.req)
			assert.Equal(t, "friend sync", step.Verb)
			assert.Equal(t, tables(sprint.Fleet), step.Load)

			res := h.run(step)
			if tc.wantPlan {
				assert.Empty(t, res.Refused)
			}
		})
	}
}

// PriorityStep (steps.go:385) sets a card's priority.

func TestStoreStepsCoverPriorityStep(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		req        sprint.PriorityReq
		wantRefuse string
	}{
		{"no ids no stream", sprint.PriorityReq{}, "run: nova-sprint help priority"},
		{"unknown stream", sprint.PriorityReq{Stream: "unknown", Level: "high", Reason: "test"}, "no such stream"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.setup(1)

			step := PriorityStep(tc.req)
			assert.Equal(t, "priority", step.Verb)
			assert.Equal(t, tables(sprint.Work, sprint.Merge), step.Load)

			res := h.run(step)
			require.NotEmpty(t, res.Refused)
			assert.Contains(t, res.Refused[0].Why, tc.wantRefuse)
			assert.Empty(t, res.Moved)
		})
	}
}

// RecutStep (steps.go:403) re-cuts a card as its twin.

func TestStoreStepsCoverRecutStep(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		req        sprint.RecutReq
		wantRefuse string
	}{
		{"unknown id", sprint.RecutReq{ID: "unknown"}, ""},
		{"neither tier nor brief", sprint.RecutReq{ID: "s1-1", Tier: "", Brief: ""}, "recut wants --tier"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.setup(1)

			step := RecutStep(tc.req)
			assert.Equal(t, "recut", step.Verb)
			assert.True(t, step.Named)
			assert.True(t, step.Mirrors)
			assert.Equal(t, All, step.Load)

			extras := step.Extras(h.snap())
			assert.NotEmpty(t, extras[sprint.Work])

			res := h.run(step)
			if tc.wantRefuse != "" {
				require.NotEmpty(t, res.Refused)
				assert.Contains(t, res.Refused[0].Why, tc.wantRefuse)
				assert.Empty(t, res.Moved)
				return
			}
		})
	}
}

// FriendReturnStep (steps.go:426) returns abandoned cards to ready.

func TestStoreStepsCoverFriendReturnStep(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		req        sprint.FriendReturnReq
		wantRefuse string
	}{
		{"unknown", sprint.FriendReturnReq{Friend: "f1", Cards: []sprint.FriendReturnCard{{ID: "unknown", Gen: 1}}}, "no such work card"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.setup(1)
			step := FriendReturnStep(tc.req)
			assert.Equal(t, "friend reconcile", step.Verb)
			assert.True(t, step.Named)
			assert.True(t, step.Mirrors)
			assert.Equal(t, tables(sprint.Fleet, sprint.Work), step.Load)

			extras := step.Extras(h.snap())
			assert.NotEmpty(t, extras[sprint.Fleet])

			res := h.run(step)
			require.NotEmpty(t, res.Refused)
			assert.Contains(t, res.Refused[0].Why, tc.wantRefuse)
			assert.Empty(t, res.Moved)
		})
	}
}

// ServerRestartStep (steps.go:439) is the server restart plan.

func TestStoreStepsCoverServerRestartStep(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	step := ServerRestartStep()
	assert.Equal(t, "restart", step.Verb)
	assert.Equal(t, tables(sprint.Readers, sprint.Work), step.Load)

	res := h.run(step)
	assert.Empty(t, res.Refused)
}
