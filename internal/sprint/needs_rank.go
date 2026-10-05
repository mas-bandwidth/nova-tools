package sprint

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// NeedsRank is the coordinator's inbox as one ranked list (docs/SPEC-SPRINT.md, section
// "view-coordinator-needs.w1"; the comfort lens: what the coordinator does by hand, or waits
// on, becomes the tool's own behaviour). It is computed from the snapshot alone (its tables
// and its open judgments), no store and no git: every decision waiting on the coordinator,
// each with the number of cards blocked behind it, transitively through needs and stream
// order (WaitsFor), heaviest first, ties by age, the older first.

// The kinds of a Need.
const (
	NeedJudgment = "judgment" // an open judgment of the inbox
	NeedSentinel = "sentinel" // a sentinel reached, or held, that waits for its release
	NeedStream   = "stream"   // a stopped stream with no judgment open on it
	NeedHeld     = "held"     // a card admitted held (add --held) that waits for its release
)

// needRank orders needs of equal weight and age.
var needRank = map[string]int{NeedJudgment: 0, NeedSentinel: 1, NeedStream: 2, NeedHeld: 3}

// needCardsShown is the cards Evidence names before it counts the rest.
const needCardsShown = 8

// Need is one decision waiting on the coordinator.
type Need struct {
	Kind  string   `json:"kind"`
	ID    string   `json:"id"`              // the judgment's id, the sentinel's or card's id, the stream's name
	Alias string   `json:"alias,omitempty"` // a judgment's alias, j<n>
	Type  string   `json:"type,omitempty"`  // a judgment's type, a stream's cause, a sentinel's or card's stream
	Cards []string `json:"cards"`           // the cards the decision is about
	// Behind is the cards not landed that wait on those cards, through needs and stream
	// order, the cards themselves not counted.
	Behind int           `json:"behind"`
	Age    time.Duration `json:"age_ns"`
	Paths  []string      `json:"paths,omitempty"` // the evidence paths the judgment names: reports, findings
	What   string        `json:"what,omitempty"`
}

// Evidence is the need's one line: its id, its cards and its evidence paths.
func (n Need) Evidence() string {
	cards, more := n.Cards, ""
	if len(cards) > needCardsShown {
		cards, more = cards[:needCardsShown], " +"+strconv.Itoa(len(n.Cards)-needCardsShown)
	}
	l := n.Kind + " " + n.ID + ": cards " + strings.Join(cards, ",") + more
	if len(n.Paths) > 0 {
		l += "; evidence " + strings.Join(n.Paths, ", ")
	}
	return l
}

// NeedsRank is the ranked needs of the coordinator at s.Now.
func NeedsRank(s *Snapshot) []Need {
	if s == nil || s.Work == nil {
		return nil
	}
	open := s.Work.Column(Waiting, Ready, Working, Review, Merging)
	dependents := map[string][]string{} // a card -> the cards that wait on it
	byID := map[string]*Card{}
	for _, c := range open {
		byID[c.ID] = c
	}
	for _, c := range open {
		for _, w := range WaitsFor(s, c, nil) {
			if _, ok := byID[w]; ok {
				dependents[w] = append(dependents[w], c.ID)
			}
		}
	}
	// behind is the cards not landed that wait on the subjects, the subjects not counted.
	behind := func(subjects []string) int {
		seen := map[string]bool{}
		for _, id := range subjects {
			seen[id] = true
		}
		stack := slices.Clone(subjects)
		n := 0
		for len(stack) > 0 {
			x := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, d := range dependents[x] {
				if seen[d] {
					continue
				}
				seen[d] = true
				stack = append(stack, d)
				if !IsSentinel(byID[d]) {
					n++
				}
			}
		}
		return n
	}
	age := func(t time.Time) time.Duration {
		if t.IsZero() {
			return 0
		}
		return max(s.Now.Sub(t), 0)
	}
	streamCards := func(stream string) []string {
		var out []string
		for _, c := range open {
			if c.Row == stream {
				out = append(out, c.ID)
			}
		}
		return out
	}

	var out []Need
	named := map[string]bool{} // a card or stream an open judgment names has its need there
	at := map[string]int{}
	for _, o := range s.Open {
		n := o.Note
		i, ok := at[n.ID]
		if !ok {
			i = len(out)
			at[n.ID] = i
			out = append(out, Need{Kind: NeedJudgment, ID: n.ID, Alias: n.Alias, Type: n.Type, Cards: []string{}, Age: age(n.At), What: n.What, Paths: EvidencePaths(n.What)})
		}
		sub := o.Subject()
		named[sub] = true
		switch {
		case strings.HasPrefix(sub, "stream:"):
			out[i].Cards = append(out[i].Cards, streamCards(strings.TrimPrefix(sub, "stream:"))...)
		case sub != SprintSubject:
			out[i].Cards = append(out[i].Cards, sub)
		}
	}
	for i := range out {
		slices.Sort(out[i].Cards)
		out[i].Cards = slices.Compact(out[i].Cards)
		out[i].Behind = behind(out[i].Cards)
	}

	for _, c := range s.Work.Column(Waiting) {
		held := IsHeld(c)
		if named[c.ID] || !held && !(IsSentinel(c) && c.F("reached") != "") {
			continue
		}
		since := stampAt(c, "reached")
		if held {
			since = stampAt(c, FieldHeld)
		}
		kind := NeedHeld
		if IsSentinel(c) {
			kind = NeedSentinel
		}
		out = append(out, Need{Kind: kind, ID: c.ID, Type: c.Row, Cards: []string{c.ID}, Behind: behind([]string{c.ID}), Age: age(since)})
	}
	if s.Merge != nil {
		for _, stream := range s.Merge.Rows() {
			ctl := s.StreamCtl(stream)
			if ctl == nil || ctl.F("state") != StreamStopped || named[StreamSubject(stream)] {
				continue
			}
			cards := streamCards(stream)
			out = append(out, Need{Kind: NeedStream, ID: stream, Type: ctl.F("cause"), Cards: cards, Behind: behind(cards), Age: age(stampAt(ctl, "since"))})
		}
	}

	slices.SortStableFunc(out, func(x, y Need) int {
		return cmp.Or(cmp.Compare(y.Behind, x.Behind), cmp.Compare(y.Age, x.Age), cmp.Compare(needRank[x.Kind], needRank[y.Kind]), cmp.Compare(x.ID, y.ID))
	})
	return out
}

// EvidencePaths is the paths a judgment's words name: a word with a slash in it, or ending
// in a file extension a report or a finding has (.md .json .txt .log), in the order named.
func EvidencePaths(what string) []string {
	var out []string
	for _, w := range strings.Fields(what) {
		w = strings.Trim(w, "`'\".,;:()[]<>")
		if w == "" || strings.Contains(w, "://") {
			continue
		}
		ext := false
		for _, e := range []string{".md", ".json", ".txt", ".log"} {
			ext = ext || strings.HasSuffix(w, e)
		}
		if (ext || strings.Contains(w, "/")) && !slices.Contains(out, w) {
			out = append(out, w)
		}
	}
	return out
}
