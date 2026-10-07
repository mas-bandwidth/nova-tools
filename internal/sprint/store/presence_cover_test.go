package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests here reach TouchBeat, which no unit test did before (the stream
// coverage card of 2026-10-04): the touch releasing a hold gives a member
// that has beaten moves its last beat to the store's clock with its load and
// samples as they were, and says no for a member with no beat to touch.

// TestPresenceCoverTouchBeatCarriesTheBeatToTheClock pins the main path: the
// record's beat becomes the store's clock, to the second, while its load,
// samples, cores and meter carry from beat to beat untouched.
func TestPresenceCoverTouchBeatCarriesTheBeatToTheClock(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	first := h.beatAt("m1", 42)
	require.True(t, first.At.Equal(t0), "the setup's beat: %+v", first)
	at := t0.Add(2 * time.Second)
	h.mu.Lock()
	h.now = at
	h.mu.Unlock()
	wrote, err := h.st.TouchBeat(h.ctx, "m1")
	require.NoError(t, err)
	assert.True(t, wrote, "a beaten member's touch writes")
	kv, err := h.st.rootKV()
	require.NoError(t, err)
	raw, ok, err := kv.GetKey(h.ctx, beatKey("m1"))
	require.NoError(t, err)
	require.True(t, ok, "the touched record")
	var b sprint.Beat
	require.NoError(t, json.Unmarshal([]byte(raw), &b))
	assert.True(t, b.At.Equal(at), "the touched record's beat: %s, want %s", b.At, at)
	assert.Equal(t, first.Load, b.Load, "the touched record's load")
	assert.Equal(t, first.Cores, b.Cores, "the touched record's cores")
	assert.Equal(t, first.Meter, b.Meter, "the touched record's meter")
	require.Len(t, b.Samples, 1, "the touched record's samples as they were: %+v", b.Samples)
	assert.True(t, b.Samples[0].At.Equal(t0), "the touched record's samples as they were: %+v", b.Samples)
	assert.Equal(t, first.Samples[0].Pct, b.Samples[0].Pct, "the touched record's samples as they were: %+v", b.Samples)
}

// TestPresenceCoverTouchBeatRefusesTheBeatless pins the refusals: a member
// that never beat, an unreadable record, a record with no beat yet and a
// store that keeps no beats say no, and the touchless ones write nothing.
func TestPresenceCoverTouchBeatRefusesTheBeatless(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		store  func(t *testing.T) (*Store, context.Context)
		member string
		seed   func(t *testing.T, st *Store, ctx context.Context)
		errKw  string
	}{
		{"a member that never beat has no record", func(t *testing.T) (*Store, context.Context) {
			h := newHarness(t)
			return h.st, h.ctx
		}, "m3", nil, ""},
		{"an unreadable record is no beat", func(t *testing.T) (*Store, context.Context) {
			h := newHarness(t)
			return h.st, h.ctx
		}, "m4", func(t *testing.T, st *Store, ctx context.Context) {
			kv, err := st.rootKV()
			require.NoError(t, err)
			require.NoError(t, kv.SetKey(ctx, beatKey("m4"), "{not a beat"))
		}, ""},
		{"a record with no beat yet is no beat", func(t *testing.T) (*Store, context.Context) {
			h := newHarness(t)
			return h.st, h.ctx
		}, "m5", func(t *testing.T, st *Store, ctx context.Context) {
			raw, err := json.Marshal(sprint.Beat{})
			require.NoError(t, err)
			kv, err := st.rootKV()
			require.NoError(t, err)
			require.NoError(t, kv.SetKey(ctx, beatKey("m5"), string(raw)))
		}, ""},
		{"a store that keeps no beats refuses", func(t *testing.T) (*Store, context.Context) {
			return &Store{B: kvless{NewMem()}}, context.Background()
		}, "m1", nil, "this store keeps no beats"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, ctx := tt.store(t)
			if tt.seed != nil {
				tt.seed(t, st, ctx)
			}
			var before string
			var had bool
			if tt.errKw == "" {
				kv, err := st.rootKV()
				require.NoError(t, err)
				before, had, err = kv.GetKey(ctx, beatKey(tt.member))
				require.NoError(t, err)
			}
			wrote, err := st.TouchBeat(ctx, tt.member)
			if tt.errKw != "" {
				assert.ErrorContains(t, err, tt.errKw)
				assert.False(t, wrote, "the refusal writes nothing")
				return
			}
			require.NoError(t, err)
			assert.False(t, wrote, "the refusal writes nothing")
			kv, err := st.rootKV()
			require.NoError(t, err)
			after, ok, err := kv.GetKey(ctx, beatKey(tt.member))
			require.NoError(t, err)
			assert.Equal(t, had, ok, "the refusal kept the key's presence")
			assert.Equal(t, before, after, "the refusal wrote nothing")
		})
	}
}
