package sprint

import (
	"fmt"
	"strconv"
	"strings"
)

// A stream whose stop sentinel has landed and still has a card open is closed.
// Admitting a card then reopens it (working, a new <stream>-stop-<n>). A landing
// the stream already dealt is recorded even when the control card says landed,
// and a push whose report did not land is marked pushed-unreported <sha>.

const (
	// StreamWorking is the control state of a stream reopened past its stop.
	StreamWorking = "working"
	// StreamClosed is what where and streams show for a landed stream that still
	// has a card open. The control card stays landed until the stream reopens.
	StreamClosed = "closed"
	// FieldPushedUnreported is the sha of a batch pushed and not reported, on the
	// work card and on the merge card (the merge card is written at once; a work
	// field waits for the pump while the machine runs).
	FieldPushedUnreported = "pushed_unreported"
	FieldPushedHead       = "pushed_head"
	FieldPushedAttempt    = "pushed_attempt"
)

// PushedPin identifies the exact card revision contained in a pushed batch.
type PushedPin struct{ ID, Head, Attempt string }

const noteStreamReopened = "stream-reopened"

// ReopenLine is the NOTE line of an add that reopens a stream.
func ReopenLine(stream, id string) string {
	return "STREAM REOPENED " + stream + " stop=" + id
}

// PushedUnreportedMark is the timeline line of a push whose report did not land.
func PushedUnreportedMark(sha string) string { return "pushed-unreported " + sha }

// StopHasLanded says a sentinel of the stream has landed. That is the stop
// behind a closed stream; a landed stream with none still comes back waiting.
func StopHasLanded(s *Snapshot, stream string) bool {
	if s == nil || s.Work == nil {
		return false
	}
	for _, c := range s.Work.Cell(stream, Landed) {
		if IsSentinel(c) {
			return true
		}
	}
	return false
}

// NextStopID is the next <stream>-stop-<n>. A bare <stream>-stop counts as 1.
// extra holds ids this add is already using, so the new stop does not take one.
func NextStopID(s *Snapshot, stream string, extra []string) string {
	prefix := stream + "-stop-"
	bare := stream + "-stop"
	maxN := 0
	seen := map[string]bool{}
	consider := func(id string) {
		seen[id] = true
		if id == bare && maxN < 1 {
			maxN = 1
		}
		rest, ok := strings.CutPrefix(id, prefix)
		if !ok {
			return
		}
		v, err := strconv.Atoi(rest)
		if err != nil || v < 1 {
			return
		}
		if v > maxN {
			maxN = v
		}
	}
	if s != nil && s.Work != nil {
		for _, c := range s.Work.Cards() {
			consider(c.ID)
		}
	}
	for _, id := range extra {
		consider(id)
	}
	for {
		maxN++
		id := prefix + strconv.Itoa(maxN)
		if seen[id] || s != nil && s.Work != nil && s.Work.Card(id) != nil {
			continue
		}
		return id
	}
}

// ShownStreamState is the state where and streams show. Landed with a card
// still open is closed; a stream that is actually finished stays landed. Held
// stays held, read the same way as StreamStateText.
func ShownStreamState(fields map[string]string, open int) string {
	if fields == nil {
		fields = map[string]string{}
	}
	text := StreamStateText(fields)
	if text == StreamLanded && open > 0 {
		return StreamClosed
	}
	return text
}

// StreamOwesLanding says the stream has a queued merge card that is merging in
// work: a landing it dealt, which the merge step records even from landed.
func StreamOwesLanding(s *Snapshot, stream string) bool {
	if s == nil || s.Merge == nil {
		return false
	}
	for _, c := range s.Merge.Cell(stream, Queued) {
		if pr := s.Work.Placed(c.ID); pr != nil && pr.Col == Merging {
			return true
		}
	}
	return false
}

// PushedUnreportedSHA is the sha marked on the card, the merge card first.
func PushedUnreportedSHA(s *Snapshot, id string) string {
	if s == nil {
		return ""
	}
	if m := s.Merge.Placed(id); m != nil && m.F(FieldPushedUnreported) != "" {
		return m.F(FieldPushedUnreported)
	}
	if pr := s.Work.Placed(id); pr != nil {
		return pr.F(FieldPushedUnreported)
	}
	return ""
}

// PushedUnreportedMatches fences recovery to the revision that was pushed.
func PushedUnreportedMatches(s *Snapshot, id string) bool {
	if s == nil || s.Work == nil || s.Merge == nil {
		return false
	}
	pr, m := s.Work.Placed(id), s.Merge.Placed(id)
	if pr == nil || m == nil || pr.Col != Merging || m.Col != Queued {
		return false
	}
	sha := m.F(FieldPushedUnreported)
	return sha != "" && pr.F(FieldPushedUnreported) == sha &&
		m.F(FieldPushedHead) != "" && m.F(FieldPushedAttempt) != "" &&
		pr.F(FieldPushedHead) == m.F(FieldPushedHead) && pr.F(FieldPushedAttempt) == m.F(FieldPushedAttempt) &&
		pr.F("head") == m.F(FieldPushedHead) && pr.F("attempt") == m.F(FieldPushedAttempt)
}

// PushedRecoveryPin is the head and attempt a pushed-unreported receipt recorded
// for id (docs/SPEC-SPRINT.md section 7). ok is false when the card is no longer
// that revision, so recovery does not land a replacement head from an earlier push.
func PushedRecoveryPin(s *Snapshot, id string) (head, attempt string, ok bool) {
	if !PushedUnreportedMatches(s, id) {
		return "", "", false
	}
	m := s.Merge.Placed(id)
	return m.F(FieldPushedHead), m.F(FieldPushedAttempt), true
}

// PushedUnreportedIDs are the stream's queued cards marked pushed and not
// reported, whose work card is missing or still merging.
func PushedUnreportedIDs(s *Snapshot, stream string) []string {
	if s == nil || s.Merge == nil {
		return nil
	}
	var ids []string
	for _, c := range s.Merge.Cell(stream, Queued) {
		if !PushedUnreportedMatches(s, c.ID) {
			continue
		}
		pr := s.Work.Placed(c.ID)
		if pr == nil || pr.Col == Merging {
			ids = append(ids, c.ID)
		}
	}
	return ids
}

// ClearObsoletePushedUnreported removes receipts whose card revision changed.
func ClearObsoletePushedUnreported(s *Snapshot, stream string) Plan {
	var p Plan
	if s == nil || s.Merge == nil || s.Work == nil {
		return p
	}
	p.on(s)
	for _, m := range s.Merge.Cards() {
		if m.Row != stream || m.F(FieldPushedUnreported) == "" || PushedUnreportedMatches(s, m.ID) {
			continue
		}
		changes := []Change{change(Merge, setEntry(m, nil, FieldPushedUnreported, FieldPushedHead, FieldPushedAttempt))}
		if pr := s.Work.Card(m.ID); pr != nil && pr.F(FieldPushedUnreported) != "" {
			changes = append(changes, change(Work, setEntry(pr, nil, FieldPushedUnreported, FieldPushedHead, FieldPushedAttempt)))
		}
		p.Units = append(p.Units, Unit{Key: m.ID, Stream: stream, Changes: changes, Moved: "obsolete pushed-unreported " + m.ID + " cleared"})
	}
	return p
}

// MarkPushedUnreported sets pushed_unreported on the work card and the merge
// card and writes the timeline line. A card already marked with that sha is
// left as it is. The plan sets fields only, so it lands while the stream is landed.
func MarkPushedUnreported(s *Snapshot, stream, sha string, pins []PushedPin) Plan {
	var p Plan
	if s == nil || sha == "" || len(pins) == 0 {
		return p
	}
	p.on(s)
	var changes []Change
	var marked []string
	for _, pin := range pins {
		id := pin.ID
		pr, m := s.Work.Placed(id), s.Merge.Placed(id)
		if pr == nil || m == nil || pr.Col != Merging || m.Col != Queued || pr.Row != stream || m.Row != stream ||
			pin.Head == "" || pin.Attempt == "" || pr.F("head") != pin.Head || pr.F("attempt") != pin.Attempt {
			continue
		}
		fields := map[string]string{FieldPushedUnreported: sha, FieldPushedHead: pin.Head, FieldPushedAttempt: pin.Attempt}
		wrote := false
		if pr.F(FieldPushedUnreported) != sha || pr.F(FieldPushedHead) != pin.Head || pr.F(FieldPushedAttempt) != pin.Attempt {
			changes = append(changes, change(Work, setEntry(pr, fields)))
			wrote = true
		}
		if m.F(FieldPushedUnreported) != sha || m.F(FieldPushedHead) != pin.Head || m.F(FieldPushedAttempt) != pin.Attempt {
			changes = append(changes, change(Merge, setEntry(m, fields)))
			wrote = true
		}
		if wrote {
			marked = append(marked, id)
		}
	}
	if len(changes) == 0 {
		return p
	}
	p.Units = []Unit{{
		Key: marked[0], Stream: stream, Changes: changes,
		Notes: []Note{happened(PushedUnreportedMark(sha), stream, s.Now, marked...)},
		Moved: PushedUnreportedMark(sha),
	}}
	return p
}

// ReportRefusedReason is the land loop line when a push's report did not land:
// the store's reason, then the timeline mark when the push has a sha.
func ReportRefusedReason(base, tip, why string) string {
	msg := "the batch was pushed to " + base + " at " + tip + " and NOT reported: " + why
	if tip != "" && tip != "-" {
		msg += "; " + PushedUnreportedMark(tip)
	}
	return msg
}

// openCardIDs are the stream's primaries on the table that have not landed.
func openCardIDs(s *Snapshot, stream string) []string {
	if s == nil || s.Work == nil {
		return nil
	}
	var ids []string
	for _, c := range streamLine(s, stream) {
		if c.Col != Landed {
			ids = append(ids, c.ID)
		}
	}
	return ids
}

// planCreates says the plan places id on the work table.
func planCreates(p Plan, id string) bool {
	for _, u := range p.Units {
		for _, ch := range u.Changes {
			if ch.Table == Work && ch.Entry.ID == id && ch.Entry.Create != nil {
				return true
			}
		}
	}
	return false
}

// reopenAdd is the stop an add grows when it admits a card past a landed stop.
// stopID set means this add already admitted that sentinel, and it is the stop.
func reopenAdd(p Plan, s *Snapshot, stream, who, stopID string, admitted []string, scores []float64, extra []string) Plan {
	line := ""
	if stopID == "" {
		stopID = NextStopID(s, stream, extra)
		needs := openCardIDs(s, stream)
		have := map[string]bool{}
		for _, id := range needs {
			have[id] = true
		}
		for _, id := range admitted {
			if id == stopID || have[id] {
				continue
			}
			needs = append(needs, id)
			have[id] = true
		}
		maxScore := 0.0
		for _, sc := range scores {
			if sc > maxScore {
				maxScore = sc
			}
		}
		for _, c := range streamLine(s, stream) {
			if c.Score > maxScore {
				maxScore = c.Score
			}
		}
		score := maxScore + 1
		fields := map[string]string{"kind": "sentinel", "stream": stream, "attempt": "0", "admitted": stamp(s.Now)}
		if len(needs) > 0 {
			fields["needs"] = strings.Join(needs, ",")
		}
		line = ReopenLine(stream, stopID)
		n := happened(noteStreamReopened, stream, s.Now, stopID)
		n.Who, n.What = who, line
		p.Units = append(p.Units, Unit{
			Key: stopID, Stream: stream,
			Moved:   fmt.Sprintf("sentinel %s -> %s stream=%s score=%s; %s", stopID, Waiting, stream, fmtScore(score), line),
			Changes: []Change{change(Work, createEntry(stopID, stream, Waiting, score, fields))},
			Notes:   []Note{n},
		})
	} else {
		line = ReopenLine(stream, stopID)
		n := happened(noteStreamReopened, stream, s.Now, stopID)
		n.Who, n.What = who, line
		for i := range p.Units {
			if p.Units[i].Key != stopID {
				continue
			}
			p.Units[i].Moved += "; " + line
			p.Units[i].Notes = append(p.Units[i].Notes, n)
			break
		}
	}
	p.Said = append(p.Said, line)
	return p
}
