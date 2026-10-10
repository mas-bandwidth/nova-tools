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
