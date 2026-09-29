package sprint

import (
	"sort"
	"strings"
	"time"
)

// Notification kinds (docs/SPEC-SPRINT.md section 8).
const (
	Happened = "happened" // no decision
	Judgment = "judgment" // needs the coordinator
	Decided  = "decided"  // a judgment answered
)

// Notification types. A type is what happened; judgment types name the
// decisions open to the coordinator.
const (
	NStartedMerging = "stream started merging"
	NBatchLanded    = "batch landed"
	NStreamLanded   = "stream landed"
	NReadyToAccept  = "two readers said ok"
	NWorkOK         = "work came back ok"
	NMemberUp       = "fleet member up"
	NMemberDown     = "fleet member down"
	NWithdrawn      = "cards returned to ready because no member is up"
	NCIGreen        = "ci green"

	NWorkFailed   = "work came back failed"
	NReadBroken   = "a reader found it broken"
	NConflict     = "stream stopped: conflict on a card"
	NRed          = "stream stopped: stream branch red"
	NCross        = "stream stopped: needs a card of another stream first"
	NRejected     = "stream stopped: the merge queue rejected"
	NBlocked      = "a primary is blocked on something dropped"
	NCIRed        = "ci red"
	NOverdue      = "a judgment notification has waited past its deadline"
	NStreamStale  = "a stream has not changed state or count past its deadline"
	NRepeatSuffix = "; a second time for the same cause"
)

// Decisions open to each judgment type.
var Decisions = map[string][]string{
	NWorkFailed:  {"rework with a fix", "drop"},
	NReadBroken:  {"rework with the finding", "ask another reader", "drop"},
	NConflict:    {"resolve and resume", "rework", "drop"},
	NRed:         {"take the suspect off and resume", "rework the suspect"},
	NCross:       {"rank that card first", "wait", "look at both", "return", "drop"},
	NRejected:    {"resume", "return", "drop"},
	NBlocked:     {"replace", "drop"},
	NCIRed:       {"rework with a fix", "return", "drop", "look"},
	NOverdue:     {"act"},
	NStreamStale: {"look"},
}

// RepeatDecision is added to a judgment for a primary that came back a second
// time for the same cause.
const RepeatDecision = "stop and look"

// MaxListed bounds the primaries a notification lists; Count carries the total.
const MaxListed = 50

// Note is one notification.
type Note struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Type      string    `json:"type"`
	Stream    string    `json:"stream,omitempty"`
	Primaries []string  `json:"primaries,omitempty"`
	Count     int       `json:"count"`
	What      string    `json:"what,omitempty"`
	Who       string    `json:"who,omitempty"`
	Attempt   int       `json:"attempt,omitempty"`
	Before    int       `json:"before,omitempty"` // how many times before, for this cause
	At        time.Time `json:"at"`
	Decisions []string  `json:"decisions,omitempty"`
	Marked    bool      `json:"marked,omitempty"`  // a repeat: sorts first
	Answers   string    `json:"answers,omitempty"` // a decided note: the judgment it answers
	// StreamLevel says the judgment is about its stream as a whole (a stopped
	// stream): it stays open until the stream resumes.
	StreamLevel bool `json:"stream_level,omitempty"`
	// Review is the next review time the coordinator set with wait; the
	// judgment stays open and shown, and is due then.
	Review time.Time `json:"review,omitempty"`
}

// Due is when the judgment is overdue: its review time when one is set, else
// its time plus the deadline.
func (n Note) Due(deadline time.Duration) time.Time {
	if !n.Review.IsZero() {
		return n.Review
	}
	return n.At.Add(deadline)
}

// Open is one open judgment on one of its subjects (a primary, or a whole
// stream). A judgment is open while any of its subjects is.
type Open struct {
	Key  string // <note id>|<primary> or <note id>|stream:<stream>
	Note Note
}

// OpenKey is the open-judgment key of a note on one subject.
func OpenKey(noteID, subject string) string { return noteID + "|" + subject }

// StreamSubject is the subject of a stream-level judgment.
func StreamSubject(stream string) string { return "stream:" + stream }

// Subject is the subject half of an open key.
func (o Open) Subject() string {
	_, s, _ := strings.Cut(o.Key, "|")
	return s
}

// happened is a notification with no decision.
func happened(typ, stream string, now time.Time, primaries ...string) Note {
	return Note{Kind: Happened, Type: typ, Stream: stream, Primaries: primaries, Count: len(primaries), At: now}
}

// judgment is a notification that needs the coordinator. before is how many
// times this cause came before for the primary: from one, the note is marked.
func judgment(typ, stream string, now time.Time, before int, primaries ...string) Note {
	n := Note{Kind: Judgment, Type: typ, Stream: stream, Primaries: primaries, Count: len(primaries), At: now,
		Decisions: append([]string(nil), Decisions[typ]...), Before: before}
	if before >= 1 {
		n.Marked = true
		n.Decisions = append(n.Decisions, RepeatDecision)
	}
	return n
}

// Subjects are the open keys' subjects a judgment note is open on.
func (n Note) Subjects() []string {
	if n.StreamLevel {
		return []string{StreamSubject(n.Stream)}
	}
	return n.Primaries
}

// Merge groups notes of one kind, type, stream and cause into one note with
// the primaries together; the input order is kept for the first of each.
// Judgment notes merge too: each primary stays an open subject of its own.
func MergeNotes(notes []Note) []Note {
	var out []Note
	at := map[string]int{}
	for _, n := range notes {
		k := strings.Join([]string{n.Kind, n.Type, n.Stream, n.What, n.Who, n.Answers, boolWord(n.Marked), boolWord(n.StreamLevel)}, "\x00")
		i, ok := at[k]
		if !ok || n.StreamLevel {
			at[k] = len(out)
			n.Primaries = append([]string(nil), n.Primaries...)
			out = append(out, n)
			continue
		}
		out[i].Primaries = append(out[i].Primaries, n.Primaries...)
		out[i].Count += n.Count
		if n.Before > out[i].Before {
			out[i].Before = n.Before
		}
	}
	for i := range out {
		sort.Strings(out[i].Primaries)
	}
	return out
}

func boolWord(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// Bound cuts the listed primaries to MaxListed; Count keeps the total.
func (n Note) Bound() Note {
	if len(n.Primaries) > MaxListed {
		n.Primaries = append([]string(nil), n.Primaries[:MaxListed]...)
	}
	return n
}
