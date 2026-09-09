package bus

import (
	"fmt"
	"sort"
	"strings"
)

// The notes that are addressed to NOBODY, and why they needed a line of their own.
//
// THE FAILURE, from the scenario run over a copy of a real bus: 22 notes were
// in no inbox and were not reported as unreadable either, so nobody on the bus was ever
// told they existed. They parsed perfectly. What they were was addressed to nobody -- `To:
// Team`, a name no roster held; and `From: Bo, Go bus-wire task ...`, where the comma
// after the name is an address separator, so the From line named two senders and resolved
// to none. A note like that falls out of every listing for a reason each half of the tool
// thinks is somebody else's business: `inbox` skips it because it is not addressed to ME,
// and UNREADABLE is about a file that will not parse, which this one does.
//
// That is the exact shape of the failure this whole tool exists to end -- somebody wrote it,
// it is on the bus, and no reader is ever shown it -- with the quietest cause of the lot.
// `send` already refuses an unknown recipient, so nothing this tool writes can be one of
// these: they are the legacy notes and the ones typed by hand in a browser, which is
// precisely the writing the bus's form is meant to allow.
//
// So they are reported:
//
//   - on `--full`, to EVERY reader, because a full read is the one that says what is on the
//     bus rather than what is new for me, and one of these is on the bus and in nobody's
//     inbox;
//   - ALWAYS in the reader's OWN LANE, on every run whatever its mode, because that is the
//     one place where the reader is the person who can fix it. A line sees its own
//     unaddressed notes every run, and goes on seeing them until the header is repaired.
//
// It is a report and never a failure: `check` is the gate and says the same thing about the
// header in its own words. This is the listing telling a reader that a note they wrote went
// nowhere.

// Unaddressed is one note on the bus that resolves to no recipient at all.
type Unaddressed struct {
	// Path is repo-relative.
	Path string
	// Reason says which line named nobody, in the words the reader needs to fix it.
	Reason string
}

// UnaddressedReason reports whether a parsed note is addressed to nobody, and why.
//
// The test is the one the inbox itself uses: the note's To and Cc, resolved against the
// roster, name NOBODY. Not "names somebody the roster does not know" -- a note to
// "Bo, Team" reaches Bo and is in her inbox, and telling her it went nowhere would
// be false. Only a note whose whole address resolves to an empty list has no reader.
func UnaddressedReason(c *Config, n *Note) (string, bool) {
	if n == nil || n.Parse != nil {
		return "", false
	}
	to, unknownTo := c.ResolveList(n.Header.To)
	cc, unknownCc := c.ResolveList(n.Header.Cc)
	if len(to)+len(cc) > 0 {
		return "", false
	}
	unknown := UnknownNames(append(append([]string{}, unknownTo...), unknownCc...))
	if len(unknown) == 0 {
		return fmt.Sprintf("no %s line, so this note is in nobody's inbox", KeyTo), true
	}
	return fmt.Sprintf("%s: %s names no one on this bus, so this note is in nobody's inbox (known: %s)",
		KeyTo, quoteAll(unknown), strings.Join(c.KnownNames(), "; ")), true
}

// UnaddressedOnTable is every note on the bus that reaches no reader, which is what a
// full read reports. It is the same list for everybody, deliberately: the fact is about the
// note and not about who is looking.
func (t *Bus) UnaddressedOnBus() []Unaddressed {
	var out []Unaddressed
	for i := range t.Notes {
		n := &t.Notes[i]
		if reason, yes := UnaddressedReason(t.Config, n); yes {
			out = append(out, Unaddressed{Path: n.Path, Reason: reason})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
