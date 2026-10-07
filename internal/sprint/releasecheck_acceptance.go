package sprint

import (
	"fmt"
	"path"
	"strings"
	"time"
)

// The acceptance sentinel's six checks (docs/SPEC-RELEASE.md, "release check",
// subsection release-check-acceptance-r-b.w3; docs/SPEC-SPRINT.md, section 11,
// the same subsection): the six checks the coordinator runs by hand before
// releasing a stream's acceptance sentinel, source: the coordinator's answer,
// 2026-10-06 12:50 ET. Each is a pure function over Acceptance, the
// stream's rows and log and the git facts, so a unit test builds a twin of
// those facts and opens no socket; each prints one RELEASE CHECK line and on a
// fail the evidence names the first item that did not hold.

// The six check names, in registry order.
const (
	CheckCardsSettled     = "cards-settled"
	CheckBaseGateGreen    = "base-gate-green"
	CheckTwoOKReads       = "two-ok-reads"
	CheckProseTrue        = "prose-true"
	CheckLandingsPromoted = "landings-promoted"
	CheckNoOpenJudgment   = "no-open-judgment"
)

// Acceptance is the acceptance sentinel's snapshot: everything the six checks
// may read. Empty Streams is no stream being accepted, so every check passes
// and says so; a stream with a fact missing fails naming the fact.
type Acceptance struct {
	Streams   []string         // the streams this release check named
	Cards     []AcceptCard     // the streams' primaries, placed or landed
	Reads     []AcceptRead     // the readers' reads of those primaries
	Gates     []AcceptGate     // the tree gate's runs at the stream's base
	Prose     []AcceptProse    // nova-check's prose results for the stream
	Promote   AcceptPromotion  // the promotion carrying the stream's landings
	Judgments []AcceptJudgment // the open judgments naming a stream
	Dropped   []AcceptDrop     // the stream's dropped cards and their reasons
}

// AcceptCard is one primary of a stream as the acceptance checks read it.
type AcceptCard struct {
	ID, Stream, Col, Tier, Head string
}

// AcceptRead is one reader's read of a primary at a head.
type AcceptRead struct {
	Primary, Stream, Head, Reader, Verdict string
}

// AcceptGate is one tree gate run at a base: the class it covered.
type AcceptGate struct {
	Base, Class string
	OK          bool
}

// AcceptProse is one prose check's result on a path.
type AcceptProse struct {
	Path, Check string // links or nocode
	OK          bool
}

// AcceptPromotion is the promotion carrying a stream's landings to dev.
type AcceptPromotion struct {
	Sha              string
	Queued           bool
	AfterLastLanding bool
}

// AcceptJudgment is one open judgment naming a stream.
type AcceptJudgment struct {
	ID, Stream, Kind string
}

// AcceptDrop is one card dropped off the work table and its reason.
type AcceptDrop struct {
	ID, Stream, Reason string
}

// openAcceptState is the states a card of an accepted stream may not be in:
// it must be landed or dropped.
func openAcceptState(col string) bool {
	switch col {
	case Waiting, Ready, Working, Review, Merging:
		return true
	}
	return false
}

func acceptOK(name, evidence string) ReleaseResult {
	return ReleaseResult{Name: name, OK: true, Evidence: evidence}
}

func acceptFail(name, evidence string) ReleaseResult {
	return ReleaseResult{Name: name, OK: false, Evidence: evidence}
}

// acceptFacts reads the facts and says whether any stream is being accepted;
// no stream means every acceptance check is vacuous and passes.
func acceptFacts(f ReleaseFacts) (Acceptance, bool) {
	a := f.Acceptance()
	return a, len(a.Streams) > 0
}

func streamList(a Acceptance) string { return strings.Join(a.Streams, ",") }

// CardsSettled (check 1): every card of the stream is landed or dropped with a
// reason, so none is ready, waiting, working, review or merging.
func CardsSettled(f ReleaseFacts) ReleaseResult {
	a, ok := acceptFacts(f)
	if !ok {
		return acceptOK(CheckCardsSettled, "no stream is being accepted: nothing to settle")
	}
	for _, c := range a.Cards {
		if openAcceptState(c.Col) {
			return acceptFail(CheckCardsSettled, fmt.Sprintf("card %s of stream %s is %s, not landed or dropped; look at: nova-sprint card %s", c.ID, c.Stream, c.Col, c.ID))
		}
	}
	for _, d := range a.Dropped {
		if strings.TrimSpace(d.Reason) == "" {
			return acceptFail(CheckCardsSettled, fmt.Sprintf("card %s of stream %s was dropped with no reason; look at: nova-sprint log --card %s", d.ID, d.Stream, d.ID))
		}
	}
	return acceptOK(CheckCardsSettled, fmt.Sprintf("every card of %s is landed or dropped with a reason (%d landed, %d dropped)", streamList(a), len(a.Cards), len(a.Dropped)))
}

// acceptGateClasses are the classes the tree gate must be green on: the unit
// class and the functional class, plus the docs and ci packages.
var acceptGateClasses = []string{"unit", "functional", "docs", "ci"}

// BaseGateGreen (check 2): the tree gate is green on the base at the stream's
// last landing, covering the unit and the functional class and the docs and ci
// packages.
func BaseGateGreen(f ReleaseFacts) ReleaseResult {
	a, ok := acceptFacts(f)
	if !ok {
		return acceptOK(CheckBaseGateGreen, "no stream is being accepted: no base gate to read")
	}
	for _, class := range acceptGateClasses {
		found := false
		for _, g := range a.Gates {
			if g.Class != class {
				continue
			}
			found = true
			if !g.OK {
				return acceptFail(CheckBaseGateGreen, fmt.Sprintf("the %s gate is red at %s; look at: the %s class gate's last lines on the stream's base", class, orDash(g.Base), class))
			}
		}
		if !found {
			return acceptFail(CheckBaseGateGreen, fmt.Sprintf("no %s gate result is recorded at the base of the stream's last landing; look at: run the %s class gate on that base and record it", class, class))
		}
	}
	return acceptOK(CheckBaseGateGreen, fmt.Sprintf("the tree gate is green on the base of %s: unit, functional, docs and ci", streamList(a)))
}

// TwoOKReads (check 3): every landed card has the ok reads its tier needs at
// its final head, one for a flash card and two different readers for a heavier
// one, none accepted on the coordinator's word alone.
func TwoOKReads(f ReleaseFacts) ReleaseResult {
	a, ok := acceptFacts(f)
	if !ok {
		return acceptOK(CheckTwoOKReads, "no stream is being accepted: no read to count")
	}
	landed := 0
	for _, c := range a.Cards {
		if c.Col != Landed {
			continue
		}
		landed++
		need := 2
		if c.Tier == "flash" {
			need = 1
		}
		seen := map[string]bool{}
		for _, r := range a.Reads {
			if r.Primary == c.ID && r.Verdict == "ok" && r.Head == c.Head && r.Reader != "" {
				seen[r.Reader] = true
			}
		}
		if len(seen) < need {
			return acceptFail(CheckTwoOKReads, fmt.Sprintf("card %s of stream %s landed at head %s has %d ok read(s), its tier %s needs %d; look at: nova-sprint card %s and the read cards of that head", c.ID, c.Stream, orDash(c.Head), len(seen), orDash(c.Tier), need, c.ID))
		}
	}
	return acceptOK(CheckTwoOKReads, fmt.Sprintf("every landed card of %s has the ok reads its tier needs at its final head (%d landed)", streamList(a), landed))
}

// ProseTrue (check 4): the stream's spec sections and help text are true to
// the code: nova-check links and nocode clean, present tense, no names of
// people or machines.
func ProseTrue(f ReleaseFacts) ReleaseResult {
	a, ok := acceptFacts(f)
	if !ok {
		return acceptOK(CheckProseTrue, "no stream is being accepted: no prose to check")
	}
	for _, check := range []string{"links", "nocode"} {
		found := false
		for _, p := range a.Prose {
			if p.Check != check {
				continue
			}
			found = true
			if !p.OK {
				return acceptFail(CheckProseTrue, fmt.Sprintf("nova-check %s is not clean on %s; look at: run nova-check %s on %s and fix what it names", check, p.Path, check, p.Path))
			}
		}
		if !found {
			return acceptFail(CheckProseTrue, fmt.Sprintf("no nova-check %s result is recorded for the stream's specs and help text; look at: run nova-check %s and record its result", check, check))
		}
	}
	return acceptOK(CheckProseTrue, "the stream's specs and help are true to the code: nova-check links and nocode are clean, present tense, no names of people or machines")
}

// LandingsPromoted (check 5): the landings are in dev, or a promotion carrying
// them is queued since the stream's last landing.
func LandingsPromoted(f ReleaseFacts) ReleaseResult {
	a, ok := acceptFacts(f)
	if !ok {
		return acceptOK(CheckLandingsPromoted, "no stream is being accepted: no landing to promote")
	}
	landed := 0
	for _, c := range a.Cards {
		if c.Col == Landed {
			landed++
		}
	}
	if landed == 0 {
		return acceptOK(CheckLandingsPromoted, fmt.Sprintf("no card has landed on %s: nothing to promote", streamList(a)))
	}
	if !a.Promote.Queued && a.Promote.Sha == "" {
		return acceptFail(CheckLandingsPromoted, fmt.Sprintf("%d landing(s) on %s are not in dev and no promotion carries them; look at: nova-sprint promote --dry-run", landed, streamList(a)))
	}
	if !a.Promote.AfterLastLanding {
		return acceptFail(CheckLandingsPromoted, fmt.Sprintf("promotion %s is older than the stream's last landing; look at: nova-sprint promoted --sha <merge sha> after the last landing", orDash(a.Promote.Sha)))
	}
	return acceptOK(CheckLandingsPromoted, fmt.Sprintf("the landings on %s are in dev, or promotion %s queued after the last landing", streamList(a), orDash(a.Promote.Sha)))
}

// NoOpenJudgment (check 6): no open judgment names the stream: no stale, no
// brief-defect, no conflict, no returned-to-review.
func NoOpenJudgment(f ReleaseFacts) ReleaseResult {
	a, ok := acceptFacts(f)
	if !ok {
		return acceptOK(CheckNoOpenJudgment, "no stream is being accepted: no judgment to read")
	}
	inStream := map[string]bool{}
	for _, s := range a.Streams {
		inStream[s] = true
	}
	for _, j := range a.Judgments {
		if j.Stream != "" && !inStream[j.Stream] {
			continue
		}
		kind := j.Kind
		if kind == "" {
			kind = "judgment"
		}
		return acceptFail(CheckNoOpenJudgment, fmt.Sprintf("open judgment %s (%s) names stream %s; look at: nova-sprint inbox --open %s", orDash(j.ID), kind, orDash(j.Stream), orDash(j.ID)))
	}
	return acceptOK(CheckNoOpenJudgment, "no open judgment names "+streamList(a))
}

// AcceptanceOf is the acceptance sentinel's facts read from a snapshot, the
// log and the streams glob: the verb binds it; a test builds a twin of it.
func AcceptanceOf(s *Snapshot, lines []Line, glob string) Acceptance {
	a := Acceptance{}
	if s == nil {
		return a
	}
	keep := func(stream string) bool {
		if glob == "" {
			return true
		}
		ok, _ := path.Match(glob, stream)
		return ok
	}
	for _, stream := range s.Streams() {
		if keep(stream) {
			a.Streams = append(a.Streams, stream)
		}
	}
	rows := CardRows(s, CardsFilter{})
	streamOf := map[string]string{}
	for _, r := range rows {
		streamOf[r.ID] = r.Stream
	}
	for _, r := range rows {
		if !keep(r.Stream) {
			continue
		}
		c := s.Work.Card(r.ID)
		a.Cards = append(a.Cards, AcceptCard{ID: r.ID, Stream: r.Stream, Col: r.Col, Tier: r.Tier, Head: c.F("head")})
	}
	if s.Readers != nil {
		for _, c := range s.Readers.Cards() {
			primary := c.F("primary")
			stream := c.F("stream")
			if stream == "" {
				stream = streamOf[primary]
			}
			if !keep(stream) {
				continue
			}
			a.Reads = append(a.Reads, AcceptRead{Primary: primary, Stream: stream, Head: c.F("head"), Reader: c.F("reader"), Verdict: c.F("verdict")})
		}
	}
	for _, o := range s.Open {
		stream := o.Note.Stream
		if stream == "" {
			if sub, ok := strings.CutPrefix(o.Subject(), "stream:"); ok {
				stream = sub
			}
		}
		if stream == "" {
			for _, p := range o.Note.Primaries {
				if st := streamOf[p]; st != "" {
					stream = st
					break
				}
			}
		}
		if stream == "" || !keep(stream) {
			continue
		}
		a.Judgments = append(a.Judgments, AcceptJudgment{ID: o.Note.ID, Stream: stream, Kind: o.Note.Type})
	}
	for _, l := range lines {
		if l.Table != Work || !l.Removed || l.Card == "" {
			continue
		}
		if !keep(l.Stream) {
			continue
		}
		reason := l.Cause
		if reason == "" && l.Text != nil {
			reason = l.Text["reason"]
		}
		a.Dropped = append(a.Dropped, AcceptDrop{ID: l.Card, Stream: l.Stream, Reason: reason})
	}
	sha, _ := s.Work.Prop(PropPromotedSha)
	at, _ := s.Work.Prop(PropPromotedAt)
	var last time.Time
	for _, l := range lines {
		if l.Kind == LineMove && l.Table == Work && strings.HasSuffix(l.To, ":"+Landed) && l.At.After(last) {
			last = l.At
		}
	}
	a.Promote = AcceptPromotion{Sha: sha, Queued: sha != ""}
	if last.IsZero() {
		a.Promote.AfterLastLanding = sha != ""
	} else if t, err := time.Parse(time.RFC3339, at); err == nil {
		a.Promote.AfterLastLanding = !t.Before(last)
	}
	return a
}
