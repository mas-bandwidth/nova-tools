package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A card accepted before the accept recorded its reads count (2026-10-07 02:00:40Z) is not
// held to its tier's rule by check rule 6 (the lowered count then is not on the card).
func TestAcceptedBeforeReadsField(t *testing.T) {
	t.Parallel()
	card := func(at string) *Card { return &Card{Fields: map[string]string{"accepted": at}} }
	assert.True(t, acceptedBeforeReadsField(card("2026-10-07T01:56:30Z")))
	assert.False(t, acceptedBeforeReadsField(card("2026-10-07T02:00:41Z")))
	assert.False(t, acceptedBeforeReadsField(card("")))
}

func rule6(s *Snapshot) bool {
	for _, v := range Check(s, nil) {
		if v.Rule == 6 {
			return true
		}
	}
	return false
}

// Through Check: a merging card with one ok read where its tier asks two is a rule 6
// violation, unless it was accepted before the accept recorded its count.
func TestCheckRule6HoldsAPreCountAcceptToOneRead(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	accepted(w, "s1-1")
	w.clean("accepted")
	s := w.s
	for _, tb := range []*Table{s.Readers, s.Fleet} {
		var drop []string
		kept := false
		for _, c := range tb.Cards() {
			if c.F("primary") == "s1-1" && c.Col == OK {
				if kept {
					drop = append(drop, c.ID)
				}
				kept = true
			}
		}
		for _, id := range drop {
			tb.Drop(id)
		}
	}
	p := s.Work.Card("s1-1")
	delete(p.Fields, FieldReadsNeeded)
	p.Fields["readers"] = ""
	p.Fields["accepted"] = "2026-10-07T02:30:00Z"
	assert.True(t, rule6(s), "accepted after the cut with one read: violation")
	p.Fields["accepted"] = "2026-10-07T01:56:30Z"
	assert.False(t, rule6(s), "accepted before the cut with one read: no violation")
}
