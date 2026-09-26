package jev

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// PassScore is the read score that moves a primary to merging (TM.PASS).
const PassScore = 8

// Event is one ws:log entry: its stream id and fields (id, from, to, by, why).
type Event struct {
	ID     string
	Fields map[string]string
}

// Snapshot is what sync read for one batch of events: the task records the
// events and their read copies name (by id), the rows that exist (by key),
// and the read decisions each landing primary still waits on.
type Snapshot struct {
	Records map[string]map[string]string
	Rows    map[string]bool
	Reads   map[string]map[string]string
}

// Plan is one batch's writes: the decisions, the outcomes, the read
// decisions that wait for their head's fate, the primaries whose wait is
// over, and the cursor after the batch.
type Plan struct {
	Decisions []Decision
	Outcomes  []Outcome
	Reads     []ReadWait
	Settled   []string
	Cursor    string
}

// ReadWait is one read decision waiting on its primary's head.
type ReadWait struct {
	Primary, Copy, Score, Head string
}

// The kinds of move a decision point is.
const (
	kCut     = "cut"
	kReview  = "review"
	kVerdict = "verdict"
	kMerging = "merging"
	kRead    = "read"
	kLanded  = "landed"
	kClosed  = "closed"
	kCopyCut = "copycut"
)

// move is one event read as a decision point.
type move struct {
	kind, id, by, why string
}

var (
	copyRx    = regexp.MustCompile(`^\S+~\d+$`)
	verdictRx = regexp.MustCompile(`^review (recut|redeal|reassign|drop): ?(.*)$`)
	suggestRx = regexp.MustCompile(`\bsuggest=([a-z]+)`)
	findingRx = regexp.MustCompile(`\bfinding=(.*)$`)
)

// IsCopy is id a consumer copy's (<primary>~<n>).
func IsCopy(id string) bool { return copyRx.MatchString(id) }

// classify reads one event as a decision point, from the event alone: the
// records it needs are read after (Needs), and MakePlan judges with them.
func classify(e Event) (move, bool) {
	f := e.Fields
	id, from, to, why := f["id"], f["from"], f["to"], f["why"]
	m := move{id: id, by: f["by"], why: why}
	if id == "" {
		return m, false
	}
	if IsCopy(id) {
		switch {
		case strings.HasSuffix(to, ":ok"):
			m.kind = kRead
		case from == "" && strings.HasSuffix(to, ":ready"):
			m.kind = kCopyCut
		default:
			return m, false
		}
		return m, true
	}
	switch {
	case from == "" && (to == "waiting" || to == "ready"):
		m.kind = kCut
	case from == "review" && to != "review" && verdictRx.MatchString(why):
		m.kind = kVerdict
	case to == "review":
		m.kind = kReview
	case to == "merging" && from != "merging":
		m.kind = kMerging
	case to == "landed" && from != "landed" && !strings.HasPrefix(why, "review drop"):
		m.kind = kLanded
	case from == "merging" && to == "done/fail":
		m.kind = kClosed
	default:
		return m, false
	}
	return m, true
}

// PrimaryOf is a copy's primary (the id before ~<n>); a primary is its own.
func PrimaryOf(id string) string {
	if !IsCopy(id) {
		return id
	}
	return id[:strings.LastIndexByte(id, '~')]
}

// Needs is what a batch reads before it is planned: every event's record,
// and a copy's primary's.
func Needs(events []Event) []string {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, e := range events {
		if m, ok := classify(e); ok {
			add(m.id)
			add(PrimaryOf(m.id))
		}
	}
	return out
}

// Then is the second read, once the records are in: the rows the decisions
// and outcomes would touch, and the read decisions of the primaries whose
// head's fate is known.
func Then(events []Event, recs map[string]map[string]string) (rows, reads []string) {
	seenR, seenW := map[string]bool{}, map[string]bool{}
	row := func(k string) {
		if !seenR[k] {
			seenR[k] = true
			rows = append(rows, k)
		}
	}
	for _, e := range events {
		m, ok := classify(e)
		if !ok {
			continue
		}
		rec := recs[m.id]
		switch m.kind {
		case kRead:
			row(RowKey(TypeReadSane, m.id))
		case kCopyCut:
			if at := recs[PrimaryOf(m.id)]["review_at"]; at != "" {
				row(RowKey(TypeReview, PrimaryOf(m.id)+"@"+at))
			}
		case kCut:
			row(RowKey(TypeTier, m.id))
			row(RowKey(TypeWorkType, m.id))
		case kReview, kVerdict:
			if at := rec["review_at"]; at != "" {
				row(RowKey(TypeReview, m.id+"@"+at))
			}
		case kMerging:
			row(RowKey(TypeTier, m.id))
		case kLanded, kClosed:
			if !seenW[m.id] {
				seenW[m.id] = true
				reads = append(reads, m.id)
			}
		}
	}
	return rows, reads
}

// MakePlan judges one batch: each decision point's row (when the row is not
// there yet) and each outcome (when its row is, or was made earlier in the
// batch). cursor is the batch's last event id.
func MakePlan(events []Event, s Snapshot) Plan {
	var pl Plan
	rows := map[string]bool{}
	for k, v := range s.Rows {
		rows[k] = v
	}
	reads := map[string]map[string]string{}
	for p, m := range s.Reads {
		reads[p] = map[string]string{}
		for c, v := range m {
			reads[p][c] = v
		}
	}
	decide := func(d Decision) {
		if rows[RowKey(d.Type, d.Subject)] {
			return
		}
		if d.Check() != nil {
			return
		}
		rows[RowKey(d.Type, d.Subject)] = true
		pl.Decisions = append(pl.Decisions, d)
	}
	join := func(o Outcome) {
		if rows[RowKey(o.Type, o.Subject)] && o.Outcome != "" {
			pl.Outcomes = append(pl.Outcomes, o)
		}
	}
	for _, e := range events {
		pl.Cursor = e.ID
		m, ok := classify(e)
		if !ok {
			continue
		}
		rec := s.Records[m.id]
		if rec == nil {
			continue
		}
		switch m.kind {
		case kCut:
			if rec["friend"] != "" || rec["owner"] != "" {
				continue // a friend-queue task, not a primary of the copy model
			}
			state := CutState(m.id, rec)
			decide(Decision{Type: TypeTier, Subject: m.id, State: state, Rules: DeclaredTier(rec), Ask: true})
			decide(Decision{Type: TypeWorkType, Subject: m.id, State: state, Ask: true})
			if t := DeclaredType(rec); t != "" {
				join(Outcome{Type: TypeWorkType, Subject: m.id, Outcome: t, By: "card", Why: "the card's TYPE line"})
			}
		case kReview:
			at, suggest := rec["review_at"], suggestOf(rec["review_jev"])
			if at == "" || suggest == "" {
				continue
			}
			decide(Decision{Type: TypeReview, Subject: m.id + "@" + at, State: ReviewState(m.id, rec), Rules: suggest,
				Ask: true})
		case kCopyCut:
			// a reassign of a read cuts a read copy and moves no primary: the
			// verdict is on the primary's record, not on a move
			p := s.Records[PrimaryOf(m.id)]
			at := p["review_at"]
			if rec["leg"] != "read" || p["review_verdict"] != "reassign" || at == "" || !atOrAfter(p["reviewed_at"], at) ||
				!sameCall(e.ID, p["reviewed_at"]) {
				continue
			}
			join(Outcome{Type: TypeReview, Subject: PrimaryOf(m.id) + "@" + at, Outcome: "reassign", By: p["reviewed_by"],
				Why: reviewWhy(p["review"])})
		case kVerdict:
			if at := rec["review_at"]; at != "" {
				v := verdictRx.FindStringSubmatch(m.why)
				join(Outcome{Type: TypeReview, Subject: m.id + "@" + at, Outcome: v[1], By: m.by, Why: v[2]})
			}
		case kMerging:
			if t := RanTier(rec); t != "" {
				join(Outcome{Type: TypeTier, Subject: m.id, Outcome: t, By: "merging",
					Why: "its work read " + strconv.Itoa(PassScore) + "+ at tier " + t})
			}
		case kRead:
			p := rec["primary"]
			score, err := strconv.Atoi(rec["score"])
			if rec["leg"] != "read" || p == "" || err != nil || score < 1 || score > 10 {
				continue
			}
			finding := ReadFinding(rec)
			rules, _ := ReadRules(score, rec["gates"], finding)
			decide(Decision{Type: TypeReadSane, Subject: m.id, State: ReadState(m.id, rec, s.Records[p]), Rules: rules,
				Ask: true, Fields: map[string]string{"primary": p, "score": rec["score"], "head": rec["head"]}})
			if reads[p] == nil {
				reads[p] = map[string]string{}
			}
			if _, seen := reads[p][m.id]; !seen {
				reads[p][m.id] = rec["score"] + " " + rec["head"]
				pl.Reads = append(pl.Reads, ReadWait{Primary: p, Copy: m.id, Score: rec["score"], Head: rec["head"]})
			}
		case kLanded, kClosed:
			w := reads[m.id]
			if len(w) == 0 {
				continue
			}
			for _, c := range sortedKeys(w) {
				score, head, _ := strings.Cut(w[c], " ")
				if o, ok := ReadFate(m.kind == kLanded, rec["head"], score, head); ok {
					o.Type, o.Subject, o.By = TypeReadSane, c, m.kind
					join(o)
				}
			}
			delete(reads, m.id)
			pl.Settled = append(pl.Settled, m.id)
		}
	}
	return pl
}

// ReadFate is a read's outcome once its primary's head has a fate: landed at
// head, or closed unmerged from merging. A read at the fate's head is trusted
// when its pass or fail matched the fate; a low read at an older head asked
// for the fix that came (trusted); a pass at an older head says nothing.
func ReadFate(landed bool, head, score, readHead string) (Outcome, bool) {
	n, err := strconv.Atoi(score)
	if err != nil {
		return Outcome{}, false
	}
	pass := n >= PassScore
	same := readHead != "" && head != "" && (strings.HasPrefix(head, readHead) || strings.HasPrefix(readHead, head))
	short := head
	if len(short) > 12 {
		short = short[:12]
	}
	switch {
	case same && pass == landed:
		return Outcome{Outcome: "trust", Why: fmt.Sprintf("read %s/10; the head %s %s", score, short, fate(landed))}, true
	case same:
		return Outcome{Outcome: "suspect", Why: fmt.Sprintf("read %s/10; the head %s %s", score, short, fate(landed))}, true
	case landed && !pass:
		return Outcome{Outcome: "trust", Why: fmt.Sprintf("read %s/10 at an older head; a fix landed at %s", score, short)}, true
	}
	return Outcome{}, false
}

func fate(landed bool) string {
	if landed {
		return "landed"
	}
	return "closed unmerged"
}

// ReadRules is the rules' answer on a read's score, before Jev: a pass with a
// gate that is not green/ok is suspect, and so is a score under 10 that names
// no work to 10 (a-score-under-ten-names-the-work-to-ten).
func ReadRules(score int, gates, finding string) (string, string) {
	if score >= PassScore {
		for _, bad := range []string{"ci:red", "base:behind", "scope:over"} {
			if strings.Contains(gates, bad) {
				return "suspect", "a pass with " + bad
			}
		}
	}
	if score < 10 && strings.TrimSpace(finding) == "" {
		return "suspect", "a score under 10 names no work to 10"
	}
	return "trust", "the score follows from its gates and finding"
}

// ReadFinding is a read copy's finding: the finding field, else the finding=
// of its SCORE line (line2).
func ReadFinding(rec map[string]string) string {
	if f := strings.TrimSpace(rec["finding"]); f != "" {
		return f
	}
	if m := findingRx.FindStringSubmatch(rec["line2"]); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// DeclaredTier is the tier the card declares (its tier, else its ROUTE when
// that is a model type), else flash: a card with no ROUTE line is flash.
func DeclaredTier(rec map[string]string) string {
	if IsTier(rec["tier"]) {
		return rec["tier"]
	}
	if IsTier(rec["route"]) {
		return rec["route"]
	}
	return "flash"
}

// RanTier is the tier the primary's work ran at ("" when nothing says).
func RanTier(rec map[string]string) string {
	if IsTier(rec["tier"]) {
		return rec["tier"]
	}
	if IsTier(rec["route"]) {
		return rec["route"]
	}
	return ""
}

// DeclaredType is the card's TYPE when it is one of Jev's work types.
func DeclaredType(rec map[string]string) string {
	p, _ := PromptFor(TypeWorkType)
	for _, k := range []string{"type", "card_type"} {
		if t := strings.ToLower(strings.TrimSpace(rec[k])); p.Has(t) {
			return t
		}
	}
	return ""
}

// atOrAfter is ms a at or after ms b.
func atOrAfter(a, b string) bool {
	x, err1 := strconv.ParseInt(a, 10, 64)
	y, err2 := strconv.ParseInt(b, 10, 64)
	return err1 == nil && err2 == nil && x >= y
}

// sameCall is the log entry's ms within a second of ms at: the copy cut in
// the verdict's own call, not a later read cut of the same review.
func sameCall(entry, at string) bool {
	ms, _, _ := strings.Cut(entry, "-")
	x, err1 := strconv.ParseInt(ms, 10, 64)
	y, err2 := strconv.ParseInt(at, 10, 64)
	return err1 == nil && err2 == nil && x-y < 1000 && y-x < 1000
}

// reviewWhy is the why of a REVIEW line (REVIEW verdict=<v> by=<b>: <why>).
func reviewWhy(line string) string {
	if _, why, ok := strings.Cut(line, ": "); ok {
		return why
	}
	return ""
}

func suggestOf(line string) string {
	if m := suggestRx.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	return ""
}

// CutState is what Jev reads for a card's work type and tier.
func CutState(id string, rec map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "CARD %s: %s\n", id, rec["title"])
	for _, kv := range [][2]string{{"KIND", "kind"}, {"ROUTE", "route"}, {"TIER", "tier"}, {"TYPE", "type"},
		{"REPO", "repo"}, {"PATHS", "paths"}, {"DONE-WHEN", "done_when"}, {"STREAM", "stream"}} {
		if v := strings.TrimSpace(rec[kv[1]]); v != "" {
			fmt.Fprintf(&b, "%s: %s\n", kv[0], v)
		}
	}
	if body := strings.TrimSpace(rec["body"]); body != "" {
		b.WriteString("\nISSUE:\n" + body + "\n")
	}
	return b.String()
}

// ReviewState is what Jev reads for a failed copy's verdict.
func ReviewState(id string, rec map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "CARD %s in review: %s\n", id, rec["title"])
	for _, kv := range [][2]string{{"SHAPE", "review_shape"}, {"SAME-SHAPE ON THIS CARD", "same_shape"},
		{"SAME-SHAPE ON THIS CONSUMER", "same_shape_consumer"}, {"CONSUMER", "review_consumer"},
		{"LEG", "review_leg"}, {"MODEL", "review_model"}, {"EXIT", "review_exit"}, {"WALL", "review_wall"},
		{"WHY", "review_why"}, {"LAST LINE", "review_line"}, {"PR", "review_pr"}, {"READ", "review_read"},
		{"ATTEMPTS", "attempts"}, {"PATHS", "paths"}, {"DONE-WHEN", "done_when"}, {"PREVIOUS REVIEW", "review"}} {
		if v := strings.TrimSpace(rec[kv[1]]); v != "" {
			fmt.Fprintf(&b, "%s: %s\n", kv[0], v)
		}
	}
	return b.String()
}

// ReadState is what Jev reads for a read's sanity: the read and the card it
// judges.
func ReadState(id string, rec, primary map[string]string) string {
	var b strings.Builder
	title, done := rec["title"], rec["done_when"]
	if primary != nil {
		if title == "" {
			title = primary["title"]
		}
		if done == "" {
			done = primary["done_when"]
		}
	}
	fmt.Fprintf(&b, "READ %s of %s: %s\n", id, rec["primary"], title)
	for _, kv := range [][2]string{{"HEAD", rec["head"]}, {"DONE-WHEN", done}, {"SCORE", rec["score"] + "/10"},
		{"GATES", rec["gates"]}, {"FINDING", ReadFinding(rec)}, {"READER", rec["consumer"]}} {
		if v := strings.TrimSpace(kv[1]); v != "" {
			fmt.Fprintf(&b, "%s: %s\n", kv[0], v)
		}
	}
	return b.String()
}
