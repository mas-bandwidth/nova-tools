package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A broken report without a defect is not a read (docs/SPEC-SPRINT.md section 6).
// The authoritative verb must preserve a recoverable assignment: the reader can
// return its read card and the deal cuts a replacement at the same head and attempt
// (tla/ReadCards.tla).
func TestAuthoritativeBrokenReadFindingBoundary(t *testing.T) {
	t.Parallel()
	const finding = "a.go drops the error; return it to the caller"
	for _, tc := range []struct {
		name, finding string
		valid         bool
	}{
		{"empty finding", "", false},
		{"generic finding", "Request changes.", false},
		{"single-letter filename", finding, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ta := newTestApp(t)
			ta.ok("init --readers reader-a,reader-b,reader-c --members m1")
			ta.inReview(1)
			ta.cutReads()
			const original = "s1-1.r1.reader-a"
			require.Equal(t, []string{original}, ta.askedOf("reader-a"), "a flash card's one read card")
			head := ta.primary("s1-1").F("head")

			line := "read --as reader-a --broken " + original
			if tc.finding != "" {
				line += " --finding '" + tc.finding + "'"
			}
			code, out, errs := ta.do(line)
			rc := ta.fleetCard(original)
			if tc.valid {
				assert.Zero(t, code, "%s: %s%s", line, out, errs)
				assert.False(t, rc.Placed(), "a verdict retires the read card")
				assert.Equal(t, "broken", rc.F("verdict"))
				assert.Contains(t, ta.group(sprint.NReadBroken, "s1").What, finding)
				return
			}

			// A refusal may recover the report; it may neither persist a broken verdict nor
			// ask the coordinator to judge an empty finding.
			assert.True(t, rc.Placed(), "%s: exit=%d %s%s", line, code, out, errs)
			assert.Empty(t, rc.F("verdict"), "an inadequate finding must not spend a read")
			for _, g := range ta.inboxGroups() {
				assert.NotEqual(t, sprint.NReadBroken, g.Type, "no broken-read judgment is owed: %+v", g)
			}
			pr := ta.primary("s1-1")
			assert.Equal(t, sprint.Review, pr.Col)
			assert.Equal(t, head, pr.F("head"))
			assert.Equal(t, 1, pr.Int("attempt"))
			assert.Zero(t, pr.Int("broken_reads"), "no primary broken-read bound is spent")

			// A refused report leaves the read card the reader's to hand back, and the next
			// cut deals the read to another reader at the same head and attempt.
			ta.ok("read --as reader-a --return " + original + " --reason 'no finding: name the defect'")
			rc = ta.fleetCard(original)
			assert.False(t, rc.Placed(), "the return retires the read card")
			assert.Equal(t, sprint.RetiredByReturned, rc.F("retired_by"))
			assert.Empty(t, rc.F("verdict"))
			ta.cutReads()
			const replacement = "s1-1.r1.reader-b"
			assert.Empty(t, ta.askedOf("reader-a"), "the returned read is not dealt to its old reader")
			require.Equal(t, []string{replacement}, ta.askedOf("reader-b"))
			rc = ta.fleetCard(replacement)
			assert.Equal(t, 1, rc.Int("attempt"))
			assert.Equal(t, head, rc.F("head"))
			assert.Zero(t, ta.primary("s1-1").Int("broken_reads"))
			for _, g := range ta.inboxGroups() {
				assert.NotEqual(t, sprint.NReadBroken, g.Type, "reasking is not a broken read: %+v", g)
			}
			ta.ok("read --as reader-b --broken " + replacement + " --finding '" + finding + "'")
			assert.Equal(t, "broken", ta.fleetCard(replacement).F("verdict"))
			assert.Contains(t, ta.group(sprint.NReadBroken, "s1").What, finding)
		})
	}
}
