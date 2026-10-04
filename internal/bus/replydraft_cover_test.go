package bus

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// OffTheListing at replydraft.go:151 is why a note on the bus is on none of this reader's
// listing, answered by the same two rules the listing itself applies -- so there is one
// spelling of each: is the note addressed to this reader, and does its day fall behind this
// reader's switch-day line. It is pure logic over a Config, a Note, a Participant and a
// LegacyLine, so a test builds those directly and injects nothing real: no clock, no disk
// read of its own, no store.
//
// docs/SPEC-BUS-REPLY.md, the OffTheListing rule: a note is in a reader's listing exactly
// when addressed is true AND behindTheLine is false; either false sends it off the listing,
// which is the whole question the function answers.

// TestReplydraftCoverOffTheListing pins the two booleans OffTheListing returns against the
// four cells of the matrix, plus the two zero-moment guards in covers and the day read from
// the filename rather than the Date header.
func TestReplydraftCoverOffTheListing(t *testing.T) {
	t.Parallel()
	c, err := LoadConfig(writeBus(t, nil))
	require.NoError(t, err, "LoadConfig: %v", err)
	ada := mustParticipant(t, c, "Ada")
	// A switch-day line drawn at midnight on 2026-09-09: a note whose day is before it is off
	// the open list and counted, not listed.
	line := LegacyLine{Before: at("2026-09-09T00:00:00Z"), Text: "2026-09-09"}

	cases := []struct {
		name      string
		note      Note
		me        Participant
		legacy    LegacyLine
		addressed bool
		behind    bool
	}{
		{
			// MAIN PATH: the note is for me and on the line side -- it is in the listing.
			name:      "addressed and on the line side is in the listing",
			note:      Note{Path: "from-ada/2026-09-10T0900Z-reply.md", Header: Header{Date: "2026-09-10T09:00:00Z", To: "Ada"}},
			me:        ada,
			legacy:    line,
			addressed: true,
			behind:    false,
		},
		{
			// Addressed only through Cc resolves the same as To does, so a Cc to me is in.
			name:      "addressed only in Cc is in the listing",
			note:      Note{Path: "from-bo/2026-09-10T0900Z-reply.md", Header: Header{To: "Bo", Cc: "Ada"}},
			me:        ada,
			legacy:    line,
			addressed: true,
			behind:    false,
		},
		{
			// REFUSAL: not addressed to me at all -- off the listing by the address rule.
			name:      "not addressed to me is off the listing",
			note:      Note{Path: "from-bo/2026-09-10T0900Z-reply.md", Header: Header{Date: "2026-09-10T09:00:00Z", To: "Bo"}},
			me:        ada,
			legacy:    line,
			addressed: false,
			behind:    false,
		},
		{
			// REFUSAL: addressed to me but dated behind the line -- off the listing by the
			// legacy rule, which is the same one the inbox uses.
			name:      "addressed but behind the line is off the listing",
			note:      Note{Path: "from-ada/2026-09-08T0900Z-old.md", Header: Header{Date: "2026-09-08T09:00:00Z", To: "Ada"}},
			me:        ada,
			legacy:    line,
			addressed: true,
			behind:    true,
		},
		{
			// Both reasons hold at once and both booleans report them.
			name:      "not addressed and behind the line is off the listing",
			note:      Note{Path: "from-bo/2026-09-08T0900Z-old.md", Header: Header{Date: "2026-09-08T09:00:00Z", To: "Bo"}},
			me:        ada,
			legacy:    line,
			addressed: false,
			behind:    true,
		},
		{
			// covers guards on a zero Before: a line drawn nowhere never puts a note behind it,
			// even one dated well before where the line would have been.
			name:      "a zero legacy line never covers",
			note:      Note{Path: "from-ada/2026-09-08T0900Z-old.md", Header: Header{Date: "2026-09-08T09:00:00Z", To: "Ada"}},
			me:        ada,
			legacy:    LegacyLine{},
			addressed: true,
			behind:    false,
		},
		{
			// legacyDay falls back to the filename when the Date header parses to nothing, and
			// the fallback still faces the line the same way When would.
			name:      "a day read from the filename still faces the line",
			note:      Note{Path: "from-ada/2026-09-08-old.md", Header: Header{To: "Ada"}},
			me:        ada,
			legacy:    line,
			addressed: true,
			behind:    true,
		},
		{
			// covers guards on a zero day: a note that cannot say when it was written cannot
			// claim to predate the line, so it stays in the open list.
			name:      "a filename with no day is never behind the line",
			note:      Note{Path: "from-ada/untitled.md", Header: Header{To: "Ada"}},
			me:        ada,
			legacy:    line,
			addressed: true,
			behind:    false,
		},
		{
			// A reader named both directly and through Cc is still just addressed once: the
			// resolved recipients carry them and addressedTo sees them in either list.
			name:      "addressed in both To and Cc is still addressed",
			note:      Note{Path: "from-bo/2026-09-10T0900Z-reply.md", Header: Header{To: "Ada; Bo", Cc: "Ada"}},
			me:        ada,
			legacy:    line,
			addressed: true,
			behind:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			addressed, behind := OffTheListing(c, &tc.note, tc.me, tc.legacy)
			assert.Equal(t, tc.addressed, addressed, "addressed for %q", tc.name)
			assert.Equal(t, tc.behind, behind, "behindTheLine for %q", tc.name)
		})
	}
}
