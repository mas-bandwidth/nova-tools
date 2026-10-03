//go:build functional

package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The settings on the real table layer (nova-tools#5096 items 22 and 27): `set`
// writes the work table's properties and `stream set` the stream's control card, a
// later set guarded on the value read; default is written as its word and read as
// none, and a stream's default unsets its field.
func TestRedisTheSettingsAreTheWorkTablesPropertiesAndTheStreamsControlCard(t *testing.T) {
	t.Parallel()
	st, _ := liveStore(t)
	h := &harness{t: t, st: st, ctx: context.Background(), now: time.Now(), live: []string{"m1"}}
	require.NoError(t, h.st.B.SetCoordinator(h.ctx, h.st.Actor))
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 1}))
	set := func(r sprint.SetReq) {
		h.t.Helper()
		r.Who = h.st.Actor
		h.must(SetStep(r))
	}
	set(sprint.SetReq{ReadTier: "pro", DealtMax: "90m"})
	set(sprint.SetReq{Streams: []string{"s1"}, ReadTier: "flash"})
	s := h.snap()
	assert.Equal(t, 90*time.Minute, s.DealtMax())
	v, _ := s.Work.Prop(sprint.PropReadTier)
	assert.Equal(t, "pro", v)
	assert.Equal(t, "flash", s.StreamCtl("s1").F(sprint.FieldReadTier))
	set(sprint.SetReq{ReadTier: sprint.ReadTierDefault, DealtMax: sprint.ReadTierDefault})
	set(sprint.SetReq{Streams: []string{"s1"}, ReadTier: sprint.ReadTierDefault})
	s = h.snap()
	assert.Equal(t, sprint.DealtMaxDefault, s.DealtMax(), "default is 3 times the take deadline")
	v, _ = s.Work.Prop(sprint.PropReadTier)
	assert.Equal(t, sprint.ReadTierDefault, v)
	assert.Empty(t, s.StreamCtl("s1").F(sprint.FieldReadTier), "a stream's default unsets its field")
}
