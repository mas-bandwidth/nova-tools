package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// coverUnit is the plan's unit for a key, the zero Unit when it has none.
func coverUnit(p Plan, key string) Unit {
	for _, u := range p.Units {
		if u.Key == key {
			return u
		}
	}
	return Unit{}
}

// HeldBack counts every waiting primary no tick moves on its own: a sentinel,
// a held card, and, through a need or a place in line, everything waiting on
// one of them; a cycle holds nothing back by itself.
func TestStepsSentinelCoverHeldBack(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		build func(t *testing.T) *world
		want  int
	}{
		{
			name: "a waiting sentinel is held back",
			build: func(t *testing.T) *world {
				w := newWorld(t)
				w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"stop"}, Sentinel: true}))
				return w
			},
			want: 1,
		},
		{
			name: "a primary admitted held is held back",
			build: func(t *testing.T) *world {
				w := newWorld(t)
				w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"h"}, Held: true}))
				return w
			},
			want: 1,
		},
		{
			name: "a card behind a held card is held back with it",
			build: func(t *testing.T) *world {
				w := newWorld(t)
				w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"h"}, Held: true}))
				w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"m"}, Needs: []string{"h"}}))
				require.Equal(t, Waiting, w.state("m"))
				return w
			},
			want: 2,
		},
		{
			name: "a card waiting behind a sentinel is held back with it",
			build: func(t *testing.T) *world {
				w := newWorld(t)
				w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"stop"}, Sentinel: true}))
				w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"after"}}))
				require.Equal(t, Waiting, w.state("after"))
				require.Equal(t, []string{"stop"}, WaitsFor(w.s, w.s.Work.Card("after"), nil))
				return w
			},
			want: 2,
		},
		{
			name: "the whole chain behind a held card is held back",
			build: func(t *testing.T) *world {
				w := newWorld(t)
				w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"h"}, Held: true}))
				w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"m"}, Needs: []string{"h"}}))
				w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"z"}, Needs: []string{"m"}}))
				return w
			},
			want: 3,
		},
		{
			name: "a card whose unmet need is ready is not held back",
			build: func(t *testing.T) *world {
				w := newWorld(t)
				w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"n"}}))
				w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"waiting"}, Needs: []string{"n"}}))
				require.Equal(t, Waiting, w.state("waiting"))
				require.Equal(t, Ready, w.state("n"))
				return w
			},
			want: 0,
		},
		{
			name: "a cycle of needs holds nothing back",
			build: func(t *testing.T) *world {
				w := newWorld(t)
				w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"y0"}}))
				w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"x"}, Needs: []string{"y0"}}))
				y0 := w.s.Work.Card("y0")
				y0.Col, y0.Fields["needs"] = Waiting, "x"
				w.s.Work.Put(y0)
				return w
			},
			want: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, HeldBack(tc.build(t).s))
		})
	}
}

// releaseHeld clears a held primary's hold, with who and why: waiting -> ready
// when it waits for nothing else, and left waiting when it does. A release
// that names a primary that is neither a sentinel nor held is refused.
func TestStepsSentinelCoverReleaseHeld(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		build func(t *testing.T) *world
		req   ReleaseReq
		check func(t *testing.T, w *world, p Plan)
	}{
		{
			name: "a held primary that waits for nothing moves to ready",
			build: func(t *testing.T) *world {
				w := newWorld(t)
				w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"h"}, Held: true}))
				require.Equal(t, Waiting, w.state("h"))
				return w
			},
			req: ReleaseReq{IDs: []string{"h"}, Reason: "the wave clears", Coordinator: "coordinator", Who: "coordinator"},
			check: func(t *testing.T, w *world, p Plan) {
				require.Empty(t, p.Refused)
				require.Equal(t, "h waiting -> ready (released by coordinator)", coverUnit(p, "h").Moved)
				w.must(p)
				c := w.s.Work.Card("h")
				assert.Equal(t, Ready, c.Col)
				assert.Empty(t, c.F(FieldHeld))
				assert.Equal(t, "coordinator", c.F("released_by"))
				assert.Equal(t, "the wave clears", c.F("release_reason"))
			},
		},
		{
			name: "a held primary that still waits has its hold cleared and stays waiting",
			build: func(t *testing.T) *world {
				w := newWorld(t)
				w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"n"}}))
				w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"h"}, Held: true, Needs: []string{"n"}}))
				require.Equal(t, Waiting, w.state("h"))
				require.Equal(t, []string{"n"}, WaitsFor(w.s, w.s.Work.Card("h"), nil))
				return w
			},
			req: ReleaseReq{IDs: []string{"h"}, Reason: "the wave clears", Coordinator: "coordinator", Who: "coordinator"},
			check: func(t *testing.T, w *world, p Plan) {
				require.Empty(t, p.Refused)
				require.Equal(t, "h released by coordinator; it waits for n", coverUnit(p, "h").Moved)
				w.must(p)
				c := w.s.Work.Card("h")
				assert.Equal(t, Waiting, c.Col)
				assert.Empty(t, c.F(FieldHeld))
				assert.Equal(t, "coordinator", c.F("released_by"))
			},
		},
		{
			name: "a primary that is neither a sentinel nor held is refused",
			build: func(t *testing.T) *world {
				w := newWorld(t)
				w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"p"}}))
				require.Equal(t, Ready, w.state("p"))
				return w
			},
			req: ReleaseReq{IDs: []string{"p"}, Reason: "the wave clears", Coordinator: "coordinator", Who: "coordinator"},
			check: func(t *testing.T, w *world, p Plan) {
				require.Empty(t, p.Units)
				require.Len(t, p.Refused, 1)
				assert.Equal(t, "not a sentinel or a held card: a primary lands by merging", p.Refused[0].Why)
				assert.Equal(t, Ready, w.state("p"))
			},
		},
		{
			name: "a release by any other actor is refused",
			build: func(t *testing.T) *world {
				w := newWorld(t)
				w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"h"}, Held: true}))
				return w
			},
			req: ReleaseReq{IDs: []string{"h"}, Reason: "the wave clears", Coordinator: "coordinator", Who: "someone"},
			check: func(t *testing.T, w *world, p Plan) {
				require.Empty(t, p.Units)
				require.Len(t, p.Refused, 1)
				assert.Equal(t, "release is the coordinator's alone: coordinator, not someone", p.Refused[0].Why)
				assert.Equal(t, Waiting, w.state("h"))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := tc.build(t)
			tc.check(t, w, Release(w.s, tc.req))
		})
	}
}
