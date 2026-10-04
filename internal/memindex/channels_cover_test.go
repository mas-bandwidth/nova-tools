package memindex

import (
	"container/heap"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// channels_cover_test.go reaches scoreHeap's Push and Pop (channels.go:156-162),
// the heap.Interface methods that exist to satisfy container/heap: topK uses
// only heap.Init and heap.Fix, and neither calls them, so the seam that runs
// them is heap.Push and heap.Pop on the same type. Push's refusal is the type
// assertion pinning its payload to Scored; Pop's is the panic naming a caller
// that asks an empty heap for an element, because Pop must only be reached
// when one exists.

func TestChannelsCoverScoreHeapPushPopDrainWorstFirst(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		push []Scored
		// wantDrain is the heap's contract under Less: the worst retained hit
		// sits at the root, so the drain is lowest score first, ties by
		// larger chunk id first.
		wantDrain []Scored
	}{
		{
			name:      "scores decide and ties break by larger chunk id",
			push:      []Scored{{Chunk: 0, Score: 5}, {Chunk: 1, Score: 3}, {Chunk: 2, Score: 7}, {Chunk: 3, Score: 3}},
			wantDrain: []Scored{{Chunk: 3, Score: 3}, {Chunk: 1, Score: 3}, {Chunk: 0, Score: 5}, {Chunk: 2, Score: 7}},
		},
		{
			name:      "all equal scores drain by chunk id descending",
			push:      []Scored{{Chunk: 0, Score: 4}, {Chunk: 1, Score: 4}, {Chunk: 2, Score: 4}},
			wantDrain: []Scored{{Chunk: 2, Score: 4}, {Chunk: 1, Score: 4}, {Chunk: 0, Score: 4}},
		},
		{
			name:      "one element round-trips whole, Rank included",
			push:      []Scored{{Chunk: 7, Rank: 2, Score: 1.5}},
			wantDrain: []Scored{{Chunk: 7, Rank: 2, Score: 1.5}},
		},
		{
			name:      "zero and negative scores order like any others",
			push:      []Scored{{Chunk: 4, Score: -2}, {Chunk: 5, Score: 0}},
			wantDrain: []Scored{{Chunk: 4, Score: -2}, {Chunk: 5, Score: 0}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &scoreHeap{}
			for _, s := range tc.push {
				heap.Push(h, s) // runs (*scoreHeap).Push, then restores the order
			}
			require.Equal(t, len(tc.push), h.Len(), "heap.Push must grow the heap by exactly one per element")
			got := make([]Scored, 0, len(tc.push))
			for h.Len() > 0 {
				got = append(got, heap.Pop(h).(Scored)) // runs (*scoreHeap).Pop
			}
			assert.Equal(t, tc.wantDrain, got, "the drain must yield the worst hit first under Less")
		})
	}
}

func TestChannelsCoverScoreHeapPushRefusesNonScoredValue(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		val  any
	}{
		{"int", 7},
		{"string", "scored"},
		{"pointer to Scored", &Scored{}},
		{"nil", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &scoreHeap{{Chunk: 0, Score: 1}}
			assert.Panics(t, func() { h.Push(tc.val) }, "Push must refuse a %s payload, not store it", tc.name)
			assert.Equal(t, 1, h.Len(), "a refused Push must leave the heap untouched")
		})
	}
}

func TestChannelsCoverScoreHeapPopRefusesEmptyHeap(t *testing.T) {
	t.Parallel()

	h := &scoreHeap{}
	assert.Panics(t, func() { h.Pop() }, "Pop on an empty heap must refuse loudly: there is no last element")
	assert.Equal(t, 0, h.Len(), "a refused Pop must leave the heap empty")
}
