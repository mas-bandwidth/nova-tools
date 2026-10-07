package store

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A card's rules by reference (sprint.FieldRules, nova-tools#5174 rule 6) are written by the
// add on the primary it admits and ride in that card's packets alone: a card that names none
// hands an empty name (it carries its own rules), whatever another card of its stream names.
func TestACardsRulesByReferenceRideInItsPackets(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.must(AddStep(sprint.AddReq{Stream: "s1", Cards: []sprint.CardAdd{
		{ID: "s1-h", File: "s1-h", Brief: "the card's text", Rules: "child-rules.txt"},
		{ID: "s1-x", File: "s1-x", Brief: "a card that carries its own rules"},
	}}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"s1-y"}, Brief: "one brief for the ids", Rules: "child-rules.space.txt"}))
	h.must(DealStep(sprint.DealReq{}))
	assert.Equal(t, "child-rules.txt", h.snap().Work.Card("s1-h").F(sprint.FieldRules))
	assert.Equal(t, "child-rules.txt", h.packetOf("s1-h.w1").Rules)
	assert.Empty(t, h.packetOf("s1-x.w1").Rules, "a card that names none")
	assert.Empty(t, h.packetOf("s1-1.w1").Rules, "a card with no brief names none")
	assert.Equal(t, "child-rules.space.txt", h.packetOf("s1-y.w1").Rules)
}
