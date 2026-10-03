package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A stream's rules by reference (nova-tools#5174 rule 6) ride in its packets: a stream that
// records none hands an empty name (the member injects its own), a recorded one hands its
// name to every card of the stream and of no other, a later record replaces it, a clear
// keeps it, an empty name removes it, and teardown removes it.
func TestAStreamsRulesByReferenceRideInItsPackets(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"s2-1"}}))
	h.must(DealStep(sprint.DealReq{}))
	assert.Empty(t, h.packetOf("s1-1.w1").Rules, "a stream that records no rules")
	require.NoError(t, h.st.SetStreamRules(h.ctx, []string{"s1"}, "child-rules.txt"))
	assert.Equal(t, "child-rules.txt", h.packetOf("s1-1.w1").Rules)
	assert.Empty(t, h.packetOf("s2-1.w1").Rules, "another stream's record is not this one's")
	require.NoError(t, h.st.SetStreamRules(h.ctx, []string{"s1", "s2"}, "child-rules.space.txt"))
	got, err := h.st.StreamRules(h.ctx)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"s1": "child-rules.space.txt", "s2": "child-rules.space.txt"}, got)
	_, err = h.st.Clear(h.ctx)
	require.NoError(t, err)
	got, err = h.st.StreamRules(h.ctx)
	require.NoError(t, err)
	assert.Equal(t, "child-rules.space.txt", got["s1"], "a clear keeps the record")
	require.NoError(t, h.st.SetStreamRules(h.ctx, []string{"s2"}, ""))
	got, err = h.st.StreamRules(h.ctx)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"s1": "child-rules.space.txt"}, got, "an empty name removes the record")
	assert.Contains(t, TeardownKeys(h.st.Names, nil, Epochs{}), h.st.Names.Key(keyStreamRules), "teardown removes the record")
	h.m.kv[keyStreamRules] = "not json"
	_, err = h.st.StreamRules(h.ctx)
	require.ErrorContains(t, err, "not a stream -> rules object")
}
