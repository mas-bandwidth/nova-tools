package sprint

import (
	"fmt"
	"strconv"
	"strings"
)

// This file holds the stream reopen: a card admitted to a stream whose stop
// sentinel landed opens it again with a new stop sentinel, and the marks and
// display words that go with it (docs/SPEC-SPRINT.md section 16).

// FieldStop is the stream control card field that records the highest stop
// sentinel number a stream opened (`<stream>-stop-<n>`). A stream with a
// recorded stop reads closed once its state is landed, so where and streams
// never say landed while the stream's stop is what ended it.
const FieldStop = "stop"

// StreamClosed is the state word a stream with a landed stop shows.
const StreamClosed = "closed"

// SentinelID is a stream's stop sentinel id: `<stream>-stop-<n>`.
func SentinelID(stream string, n int) string { return stream + "-stop-" + itoa(n) }

// SentinelName is the stream a stop sentinel id names: the text before its
// `-stop-` marker, else the id itself when it carries no marker.
func SentinelName(id string) string {
	if i := strings.LastIndex(id, "-stop-"); i >= 0 {
		return id[:i]
	}
	return id
}

// StopNumber is the stop a sentinel id numbers: n in `<stream>-stop-<n>`, 0
// when the id carries no number.
func StopNumber(id string) int {
	i := strings.LastIndex(id, "-stop-")
	if i < 0 {
		return 0
	}
	n, _ := strconv.Atoi(id[i+len("-stop-"):])
	return n
}

// IsStopSentinel says id is a stop sentinel of stream: it carries the `-stop-`
// marker and the text before that marker is the stream's name. It matches
// `stream-1-stop-1`, which ends in a number, so the marker is looked for and
// not required at the end.
func IsStopSentinel(id, stream string) bool {
	return strings.Contains(id, "-stop-") && SentinelName(id) == stream
}

// IsPushedUnreported says a value is a pushed-unreported mark: the sha of a
// commit a lander pushed and its report did not record.
func IsPushedUnreported(v string) bool { return strings.HasPrefix(v, PushedUnreportedPrefix) }

// PushedUnreportedID returns the sha a pushed-unreported mark names: the mark
// without its prefix, else the value itself.
func PushedUnreportedID(v string) string { return strings.TrimPrefix(v, PushedUnreportedPrefix) }

// StreamStateTextClosed is a stream's state cell when its stop landed: closed,
// else its own state. A stream that never recorded a stop keeps landed, so a
// sprint that simply finished every card still reads landed.
func StreamStateTextClosed(fields map[string]string) string {
	if fields["state"] == StreamLanded && fields[FieldStop] != "" {
		return StreamClosed
	}
	return fields["state"]
}

// StreamLandedWithOpen says a stream's control card reads landed while a card
// of it is still open: the state the 2026-10-07 streams were in when the
// lander refused their landing with "landed" pass after pass.
func StreamLandedWithOpen(s *Snapshot, stream string) bool {
	if s.StreamCtl(stream).F("state") != StreamLanded {
		return false
	}
	return len(Unlanded(s, stream)) > 0
}

// StreamStopLanded says a stop sentinel of the stream landed: the stop that
// closed it. A stream that landed with no stop sentinel (every card finished,
// no stop was placed) has nothing to reopen.
func StreamStopLanded(s *Snapshot, stream string) bool {
	for _, c := range s.Work.Cards() {
		if c.Placed() && c.Row == stream && c.Col == Landed && IsStopSentinel(c.ID, stream) {
			return true
		}
	}
	return false
}

// StreamReopenReq is the request to reopen a stream when a card is admitted to
// it after its stop landed.
type StreamReopenReq struct {
	Stream string
	Card   string
	Who    string
}

// StreamReopen opens a stream whose stop landed because a new card was
// admitted: it makes a new stop sentinel `<stream>-stop-<n>`, the n after the
// highest the stream recorded, and says `STREAM REOPENED <stream> stop=<id>`
// so the add prints the line. The add itself writes the sentinel and the
// stream's state; this returns what it writes. A stream that is not closed by a
// landed stop has nothing to reopen.
func StreamReopen(s *Snapshot, r StreamReopenReq) (id string, said string, err error) {
	ctl := s.StreamCtl(r.Stream)
	if ctl == nil {
		return "", "", fmt.Errorf("no stream %s", r.Stream)
	}
	if ctl.F("state") != StreamLanded {
		return "", "", fmt.Errorf("stream %s is %s, not closed", r.Stream, orDash(ctl.F("state")))
	}
	if !StreamStopLanded(s, r.Stream) {
		return "", "", fmt.Errorf("stream %s landed with no stop sentinel; nothing to reopen", r.Stream)
	}
	id = SentinelID(r.Stream, ctl.Int(FieldStop)+1)
	said = "STREAM REOPENED " + r.Stream + " stop=" + id
	return id, said, nil
}

// PushedUnreportedPrefix begins a pushed-unreported mark: the sha of a commit
// a lander pushed and the report did not record. The mark lives on the merge
// card's FieldPushedUnreported and on the card's timeline.
const PushedUnreportedPrefix = "pushed-unreported "

// FieldPushedUnreported is the merge card field holding a pushed-unreported
// mark, `<sha>` prefixed by PushedUnreportedPrefix: the merge is on the base
// and the report is the only step left (docs/SPEC-SPRINT.md section 7).
const FieldPushedUnreported = "pushed_unreported"

// NPushedUnreported is the happened note a pushed-unreported mark writes on a
// card's timeline.
const NPushedUnreported = "pushed unreported"

// PushedUnreportedReq marks the cards of a batch whose merge a lander pushed
// and whose report the store did not take. Cards and Heads are parallel: a
// card is marked only while it is still merging at the head that was pushed,
// so a card reworked under the push is left to the next merge of its new head.
type PushedUnreportedReq struct {
	Stream string
	Cards  []string
	Heads  []string
	Sha    string
	Who    string
}

// MarkPushedUnreported writes `<sha>` as a pushed-unreported mark on each
// named card's merge card and one happened note on its timeline, so the next
// land pass records it without merging it again. A card that is no longer
// merging queued at the head that was pushed (a return, a rework, a card
// already recorded) is left alone.
func MarkPushedUnreported(s *Snapshot, r PushedUnreportedReq) Plan {
	var p Plan
	p.on(s)
	mark := PushedUnreportedPrefix + r.Sha
	for i, id := range r.Cards {
		m := s.Merge.Placed(id)
		if m == nil || m.Col != Queued {
			continue
		}
		pr := s.Work.Placed(id)
		if pr == nil || pr.Col != Merging {
			continue
		}
		if i < len(r.Heads) && r.Heads[i] != "" && pr.F("head") != r.Heads[i] {
			continue
		}
		n := happened(NPushedUnreported, r.Stream, s.Now, id)
		n.Who, n.What = r.Who, mark
		u := Unit{Key: id, Stream: r.Stream,
			Changes: []Change{change(Merge, setEntry(m, map[string]string{FieldPushedUnreported: mark}))},
			Notes:   []Note{n},
			Moved:   id + " marked " + mark}
		p.Units = append(p.Units, u)
	}
	return p
}
