package tokens

import (
	"strings"
	"testing"
)

const frictionID = "abcdef0123456789abcdef0123456789"

func TestFrictionAddAndSummary(t *testing.T) {
	t.Run("parses every field", func(t *testing.T) {
		line := frictionID + "\t1000\t42\tburn\tclaude\tissue-240\t2026-09-11T15:29:00Z"
		e, err := ParseFrictionEntry(line)
		if err != nil {
			t.Fatalf("a well-formed friction entry does not parse: %v", err)
		}
		if e.ID != frictionID {
			t.Errorf("id = %q, want %q", e.ID, frictionID)
		}
		if e.Tokens != 1000 || e.Rough {
			t.Errorf("tokens = %d rough=%v, want 1000 exact", e.Tokens, e.Rough)
		}
		if e.WallSeconds != 42 {
			t.Errorf("wall_seconds = %d, want 42", e.WallSeconds)
		}
		if e.Gap != "burn" || e.Tool != "claude" || e.Issue != "issue-240" || e.CreatedAt != "2026-09-11T15:29:00Z" {
			t.Errorf("fields = {%q %q %q %q}, want {burn claude issue-240 2026-09-11T15:29:00Z}", e.Gap, e.Tool, e.Issue, e.CreatedAt)
		}
	})

	t.Run("a rough cost needs a wall time above zero", func(t *testing.T) {
		if _, err := ParseFrictionEntry(frictionID + "\t~1000\t0\tburn\tclaude\tissue\t2026-09-11T15:29:00Z"); err == nil {
			t.Errorf("a rough token cost with wall_seconds=0 parses, wants an error")
		}
		if _, err := ParseFrictionEntry(frictionID + "\t~1000\t-\tburn\tclaude\tissue\t2026-09-11T15:29:00Z"); err == nil {
			t.Errorf("a rough token cost with a dash wall_seconds parses, wants an error")
		}
		e, err := ParseFrictionEntry(frictionID + "\t~1000\t30\tburn\tclaude\tissue\t2026-09-11T15:29:00Z")
		if err != nil {
			t.Fatalf("a rough cost with a wall time does not parse: %v", err)
		}
		if !e.Rough || e.Tokens != 1000 || e.WallSeconds != 30 {
			t.Errorf("rough cost parsed as tokens=%d rough=%v wall=%d, want 1000 true 30", e.Tokens, e.Rough, e.WallSeconds)
		}
	})

	t.Run("an id that is not 32 hex digits refuses", func(t *testing.T) {
		if _, err := ParseFrictionEntry("xyz\t1000\t42\tburn\tclaude\tissue\t2026-09-11T15:29:00Z"); err == nil {
			t.Errorf("a non-hex id parses, wants an error")
		}
		if _, err := ParseFrictionEntry("abcd\t1000\t42\tburn\tclaude\tissue\t2026-09-11T15:29:00Z"); err == nil {
			t.Errorf("a short id parses, wants an error")
		}
	})

	t.Run("format and parse round-trip", func(t *testing.T) {
		e := FrictionEntry{ID: frictionID, Tokens: 1000, Rough: true, WallSeconds: 30, Gap: "burn", Tool: "claude", Issue: "issue-240", CreatedAt: "2026-09-11T15:29:00Z"}
		back, err := ParseFrictionEntry(FormatFrictionEntry(e))
		if err != nil {
			t.Fatalf("a formatted friction entry does not parse: %v", err)
		}
		if *back != e {
			t.Errorf("round-trip = %+v, want %+v", *back, e)
		}
		if !strings.HasPrefix(FormatFrictionEntry(e), frictionID+"\t~1000\t30\t") {
			t.Errorf("format is not `id ~1000 30 ...`: %q", FormatFrictionEntry(e))
		}
	})

	t.Run("summarizes by gap, token total descending", func(t *testing.T) {
		entries := []FrictionEntry{
			{Gap: "burn", Tokens: 500},
			{Gap: "grind", Tokens: 2000},
			{Gap: "burn", Tokens: 700},
			{Gap: "grind", Tokens: 400},
			{Gap: "burn", Tokens: 100},
		}
		sums := SummarizeFrictions(entries)
		if len(sums) != 2 {
			t.Fatalf("%d summaries, want two", len(sums))
		}
		// grind costs more (2400 vs 1300), so it sorts first even though burn was first in.
		if sums[0].Gap != "grind" || sums[0].Tokens != 2400 || sums[0].Occurrences != 2 {
			t.Errorf("sums[0] = %+v, want grind 2400 over 2 occurrences", sums[0])
		}
		if sums[1].Gap != "burn" || sums[1].Tokens != 1300 || sums[1].Occurrences != 3 {
			t.Errorf("sums[1] = %+v, want burn 1300 over 3 occurrences", sums[1])
		}
	})
}
