package sprint

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// Notification kinds (docs/SPEC-SPRINT.md section 8).
const (
	Happened = "happened" // no decision
	Judgment = "judgment" // needs the coordinator
	Decided  = "decided"  // a judgment answered
	// Acknowledged is a judgment of the tick the coordinator acknowledged
	// while its condition held: kept on the condition's subjects, it is no
	// judgment and shows in no inbox; it stops the tick from writing the
	// judgment again until the condition has cleared (the tick then closes
	// it) and come back.
	Acknowledged = "acknowledged"
)

// SplitOpen is the open set as judgments and acknowledged conditions.
func SplitOpen(all []Open) (judgments, acked []Open) {
	for _, o := range all {
		if o.Note.Kind == Acknowledged {
			acked = append(acked, o)
		} else {
			judgments = append(judgments, o)
		}
	}
	return judgments, acked
}

// Notification types. A type is what happened; judgment types name the
// decisions open to the coordinator.
const (
	NStartedMerging = "stream started merging"
	NBatchLanded    = "batch landed"
	NStreamLanded   = "stream landed"
	NWorkOK         = "work came back ok"
	NMemberUp       = "fleet member up"
	NMemberDown     = "fleet member down"
	NUnknownMachine = "an unknown machine is beating"

	// The tick's own failure, noted once for each distinct error text it
	// keeps failing with, and its recovery, noted once with the count of
	// failed ticks (docs/SPEC-SPRINT.md section 14): both are happened notes
	// addressed to the coordinator, so the tick end wakes it.
	NTickFailed    = "the tick failed"
	NTickRecovered = "the tick recovered"

	// Computed by inbox from the machine's record, as the stale line is: no
	// notification holds them.
	NMachineSilent  = "the machine is not ticking"
	NTickFailing    = "the tick keeps failing"
	NStoppedWithDue = "the machine is STOPPED and moves are due"
	NWithdrawn      = "cards returned to ready because no member is up"
	NCIGreen        = "ci green"
	NAbandoned      = "an operation was abandoned"
	NSentinelLanded = "sentinel landed" // released by the coordinator

	NReadyToAccept   = "ready to accept"    // two different readers said ok at its head
	NReturned        = "returned to review" // sent back from merging: the coordinator decides again
	NWorkFailed      = "work came back failed"
	NReadBroken      = "a reader found it broken"
	NReadReturned    = "a reader returned a read" // no verdict: asked of another reader
	NConflict        = "stream stopped: conflict on a card"
	NRed             = "stream stopped: stream branch red"
	NCross           = "stream stopped: needs a card of another stream first"
	NRejected        = "stream stopped: the merge queue rejected"
	NBlocked         = "a primary is blocked on something dropped"
	NMissingNeed     = "a primary is blocked on something missing"
	NCIRed           = "ci red"
	NReadsExhausted  = "reads exhausted"
	NStranded        = "stranded in review" // failed work acknowledged, or never asked, and nothing open
	NRepairSkipped   = "repair skipped changes the store refused as recorded"
	NOpStuck         = "an operation was stuck"
	NOverdue         = "a judgment notification has waited past its deadline"
	NStreamStale     = "a stream has not changed state or count past its deadline"
	NSprintDone      = "the sprint is done"
	NSentinelReached = "sentinel reached" // a stop: the coordinator decides before going on
	NRepeatSuffix    = "; a second time for the same cause"
)

// Decisions open to each judgment type.
var Decisions = map[string][]string{
	NReadyToAccept:   {"accept", "rework", "drop"},
	NReturned:        {"rework", "accept", "drop"}, // accept only while its reads stand at its head
	NWorkFailed:      {"rework with a fix", "drop"},
	NReadBroken:      {"rework with the finding", "ask another reader", "drop"},
	NConflict:        {"resolve and resume", "rework", "drop"},
	NRed:             {"take the suspect off and resume", "rework the suspect"},
	NCross:           {"rank that card first", "wait", "look at both", "return", "drop"},
	NRejected:        {"resume", "return", "drop"},
	NBlocked:         {"drop", "ack"},
	NMissingNeed:     {"drop", "ack"},
	NCIRed:           {"rework with a fix", "return", "drop", "look", "ack"},
	NReadsExhausted:  {"ask another reader", "rework", "drop"},
	NStranded:        {"ask", "rework", "drop"},
	NRepairSkipped:   {"look at the card", "return", "drop", "rework", "ack"},
	NOpStuck:         {"check", "ack"},
	NOverdue:         {"act"},
	NStreamStale:     {"look"},
	NSprintDone:      {"clear", "add"},
	NSentinelReached: {"release", "do more before going on", "drop"},
	NStalled:         {"look at the card", "wait"}, // each stall names its own
}

// RepeatDecision is added to a judgment for a primary that came back a second
// time for the same cause.
const RepeatDecision = "stop and look"

// MaxListed bounds the primaries a notification lists; Count carries the total.
const MaxListed = 50

// PreviewLen is how many ids a rendered list of ids shows before it is cut:
// the card, the inbox and the notices name the first eight and count the rest,
// as the inbox's group line does, so a card with a thousand needs is one short
// line and never every id.
const PreviewLen = 8

// Preview is a list of items as a person reads it, joined by sep: all of them
// when there are PreviewLen or fewer, else the first PreviewLen and
// "... and k more".
func Preview(items []string, sep string) string {
	if len(items) <= PreviewLen {
		return strings.Join(items, sep)
	}
	return strings.Join(items[:PreviewLen], sep) + sep + "... and " + strconv.Itoa(len(items)-PreviewLen) + " more"
}

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
	Marked    bool      `json:"marked,omitempty"`   // a repeat: sorts first
	Answers   string    `json:"answers,omitempty"`  // a decided note: the judgment it answers
	Suspects  []string  `json:"suspects,omitempty"` // a red branch: the cards of the batch the caller suspects
	// Card and Other name the cards a judgment is about by what they are,
	// for the commands the inbox prints: the card a stream stopped on (a
	// conflict, a cross stop) or the sentinel reached, and for a cross stop
	// the card it needs, of OtherStream. Primaries is a set: nothing reads a
	// card from its position there.
	Card        string `json:"card,omitempty"`
	Other       string `json:"other,omitempty"`
	OtherStream string `json:"other_stream,omitempty"`
	// StreamLevel says the judgment is about its stream as a whole (a stopped
	// stream): it stays open until the stream resumes.
	StreamLevel bool `json:"stream_level,omitempty"`
	// SprintLevel says the judgment is about the whole sprint (it is done):
	// its one subject is SprintSubject.
	SprintLevel bool `json:"sprint_level,omitempty"`
	// Needs is a blocked judgment's dropped needs: acknowledging it waives
	// these, and only these.
	Needs []string `json:"needs,omitempty"`
	// Review is the next review time the coordinator set with wait; the
	// judgment stays open and shown, and is due then.
	Review time.Time `json:"review,omitempty"`
	// ReviewSet is when wait set Review: the review time counts running time
	// from it, as every deadline does.
	ReviewSet time.Time `json:"review_set,omitempty"`
	// To is who a happened note is addressed to: the coordinator, for "the
	// sprint is done" (errata 3 amendment 6). The inbox shows a note
	// addressed to someone first, above the judgments; empty is no one.
	To string `json:"to,omitempty"`
	// Hint is what to do next, in words, for a note addressed to someone.
	Hint string `json:"hint,omitempty"`
}

// NTickEnd is the tick's end note (errata 3 amendment 8): written once at the
// end of a tick that addressed the coordinator something, "judgments=N";
// inbox --wait wakes on it, and the inbox does not list it.
const NTickEnd = "tick end"

// TickEndCounts says a note is an item for the coordinator that a tick-end
// counts: a judgment, or a happened note addressed to someone (the sprint is
// done). The tick-end note itself is not.
func TickEndCounts(n Note) bool {
	return n.Type != NTickEnd && (n.Kind == Judgment || (n.Kind == Happened && n.To != ""))
}

// Due is when the judgment is overdue: its review time when one is set, else
// its time plus the deadline. The sprint is done has no due time (zero).
func (n Note) Due(deadline time.Duration) time.Time {
	if n.Type == NSprintDone {
		return time.Time{}
	}
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

// SprintSubject is the subject of a judgment about the whole sprint; no
// primary or stream id has a colon.
const SprintSubject = "sprint:done"

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
	if n.SprintLevel {
		return []string{SprintSubject}
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
		if len(n.Primaries) == 0 {
			out[i].Count += n.Count
		}
		for _, p := range n.Primaries {
			if !contains(out[i].Primaries, p) {
				out[i].Primaries = append(out[i].Primaries, p)
				out[i].Count++
			}
		}
		if n.Before > out[i].Before {
			out[i].Before = n.Before
		}
		for _, x := range n.Needs {
			if !contains(out[i].Needs, x) {
				out[i].Needs = append(out[i].Needs, x)
			}
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
