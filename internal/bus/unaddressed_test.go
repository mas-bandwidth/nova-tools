package bus

import (
	"errors"
	"strings"
	"testing"
)

// The two shapes off a real table, and the two that must NOT be reported.
//
// A note is unaddressed when its whole address resolves to nobody -- not when part of it
// does. "Bo, Team" reaches Bo and is in her inbox, and telling her it went nowhere
// would be false; "Team" alone reaches no one and was in no listing at all.
func TestUnaddressedReason(t *testing.T) {
	root := writeTable(t, map[string]string{
		// The shape that was on the table 22 times: a To line naming a group nobody
		// declared.
		"from-bo/2026-09-07T0001Z-team.md": "From: Bo\nTo: Team\nDate: Mon Sep  7 00:01:00 UTC 2026\nSubject: The wire task\n\nA note to nobody.\n",
		// The other half of the same finding: a From line whose comma is an address
		// separator, so it names two senders and resolves to none -- and whose To line is
		// the group nobody declared either.
		"from-bo/2026-09-07T0002Z-split.md": "From: Bo, Go table-wire task\nTo: Team\nDate: Mon Sep  7 00:02:00 UTC 2026\nSubject: Also the wire task\n\nAlso to nobody.\n",
		// A note with no To line at all.
		"from-bo/2026-09-07T0003Z-none.md": "From: Bo\nTo:\nDate: Mon Sep  7 00:03:00 UTC 2026\nSubject: No one\n\nNo To line worth the name.\n",
		// PARTLY unknown is not unaddressed: Ada gets this one.
		"from-bo/2026-09-07T0004Z-partly.md": "From: Bo\nTo: Ada, Team\nDate: Mon Sep  7 00:04:00 UTC 2026\nSubject: Partly\n\nThis one reaches Ada.\n",
		// And an ordinary note is not reported.
		"from-bo/2026-09-07T0005Z-fine.md": "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:05:00 UTC 2026\nSubject: Fine\n\nAn ordinary note.\n",
		// Only through the Cc is still addressed.
		"from-bo/2026-09-07T0006Z-cc.md": "From: Bo\nTo: Team\nCc: Dana\nDate: Mon Sep  7 00:06:00 UTC 2026\nSubject: Cc only\n\nDana gets this one.\n",
	})
	tab := loadTable(t, root)
	got := tab.UnaddressedOnTable()
	var paths []string
	for _, u := range got {
		paths = append(paths, u.Path)
	}
	want := []string{
		"from-bo/2026-09-07T0001Z-team.md",
		"from-bo/2026-09-07T0002Z-split.md",
		"from-bo/2026-09-07T0003Z-none.md",
	}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Fatalf("unaddressed = %v, want %v", paths, want)
	}
	// The reason names the token that reached nobody, so the writer knows what to fix.
	if !strings.Contains(got[0].Reason, `"Team"`) || !strings.Contains(got[0].Reason, "in nobody's inbox") {
		t.Fatalf("the reason does not name the token or say what it costs: %q", got[0].Reason)
	}
	if !strings.Contains(got[2].Reason, "no To line") {
		t.Fatalf("a note with no To line is reported as %q", got[2].Reason)
	}
	// A file that will not PARSE is not reported here: it has no header to read, and
	// UNREADABLE is what says so.
	broken := &Note{Path: "from-bo/x.md", Parse: &ParseError{Err: errors.New("not a header line")}}
	if _, yes := UnaddressedReason(tab.Config, broken); yes {
		t.Fatal("an unreadable file was reported as unaddressed; it has no To line to resolve")
	}
}
