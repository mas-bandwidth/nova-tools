package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The needs verb is a read (docs/SPEC-SPRINT.md section 11): it changes
// nothing and needs no actor, and it prints the dependency graph of the
// waiting cards. The class is kept here, beside the verb, since the verb
// table registers the verb and needs.go adds its class.
func init() { verbClasses["needs"] = classRead }

// needLine is one unmet need as needs prints it: the need's id and its state
// (a column name, dropped for a kept record with outcome dropped, or absent
// for no record at all).
type needLine struct {
	ID    string `json:"id"`
	State string `json:"state"`
}

// needsMore follows a list --max cut short: how many items were left out, and
// the command that prints them. Boundary, on a cut of cards or streams, is
// the first card the cut hid and how many unmet needs it has, so the cut
// cannot be read as that card needing nothing.
type needsMore struct {
	Omitted  int    `json:"omitted"`
	Command  string `json:"command"`
	Boundary string `json:"boundary,omitempty"`
	Needs    *int   `json:"needs,omitempty"`
}

// needsCard is one waiting card in the graph: its stream, its depth, whether
// it is a root, and its unmet needs. More follows Needs when --max hid some.
type needsCard struct {
	ID     string     `json:"id"`
	Stream string     `json:"stream"`
	Depth  int        `json:"depth"`
	Root   bool       `json:"root"`
	Needs  []needLine `json:"needs"`
	More   *needsMore `json:"more,omitempty"`
}

// needsWidth is the number of a stream's waiting cards at one depth.
type needsWidth struct {
	Depth int `json:"depth"`
	Width int `json:"width"`
}

// needsStream is one stream's graph: its waiting cards in chain order, the
// width at each depth, and the number of its cards that name a dropped or
// absent id. Total is every waiting card of the stream, whether or not --max
// listed it. CardsMore, WidthsMore and CycleMore follow the list each cut.
type needsStream struct {
	Stream     string       `json:"stream"`
	Cards      []needsCard  `json:"cards"`
	CardsMore  *needsMore   `json:"cards_more,omitempty"`
	Widths     []needsWidth `json:"width"`
	WidthsMore *needsMore   `json:"width_more,omitempty"`
	Total      int          `json:"total"`
	Orphans    int          `json:"dropped_or_absent"`
	// Cycle names the cards whose needs make a cycle in this stream, so the
	// graph prints the cycle instead of being followed around it.
	Cycle     []string   `json:"cycle,omitempty"`
	CycleMore *needsMore `json:"cycle_more,omitempty"`
}

// needsView is the whole graph: a stream a section, and the sprint's totals.
// More follows Streams when --max hid some. Cards counts every waiting card.
type needsView struct {
	Streams []needsStream `json:"streams"`
	More    *needsMore    `json:"more,omitempty"`
	Cards   int           `json:"cards"`
	Orphans int           `json:"dropped_or_absent"`
}

// cmdNeeds prints the dependency graph of the waiting cards: for each stream
// (or the one --stream names) its waiting cards in chain order, a card after
// every card it needs, each with its unmet needs and their states, the roots
// marked, each card's depth, the width at each depth, and a line counting the
// cards whose needs name a dropped or absent id. It reads the work table once
// with the off-table needs of the waiting cards read too (sprint.ResolveExtras
// as Load's extras, the same read the tick's extras make), builds every need's
// state from the same sprint.NeedsOf the card view uses, writes nothing, and
// prints one object with --json (docs/SPEC-SPRINT.md section 11).
func (a *app) cmdNeeds(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("needs")
	stream := fs.String("stream", "", "the waiting cards of one stream (default: every stream)")
	roots := fs.Bool("roots", false, "print only the roots and the width lines")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "needs", argErr("takes no words ", err, pos...))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "needs", err.Error())
	}
	// One read of the work table: the placed waiting cards and, as extras, the
	// needs of the waiting cards that are off the table, so an absent need and a
	// dropped record are told apart (sprint.ResolveExtras; the tick reads them
	// the same way, store/tick.go tickExtras).
	s, err := st.Load(context.Background(), []string{sprint.Work}, func(s *sprint.Snapshot) map[string][]string {
		return map[string][]string{sprint.Work: sprint.ResolveExtras(s)}
	})
	if err != nil {
		return a.readFailed("needs", err, stderr)
	}
	v := sprintNeeds(s, *stream, *roots).bound(c.max, *roots)
	if c.json {
		b, _ := json.Marshal(v)
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprint(stdout, needsText(v, *roots))
	return 0
}

// sprintNeeds builds the graph from one snapshot (pure over the read): only is
// a stream to keep ("" is every stream), rootsOnly keeps only the roots in
// each stream's cards. A card's depth is one more than its deepest waiting
// need; a card with no waiting need is a root at depth 0. A cycle through
// needs is never walked forever: its cards are marked and so are not roots
// (docs/SPEC-SPRINT.md section 11).
func sprintNeeds(s *sprint.Snapshot, only string, rootsOnly bool) needsView {
	waiting := s.Work.Column(sprint.Waiting)
	inWaiting := make(map[string]bool, len(waiting))
	for _, c := range waiting {
		inWaiting[c.ID] = true
	}
	// each waiting card's needs, resolved once, as the card view resolves them
	needs := make(map[string][]sprint.NeedState, len(waiting))
	for _, c := range waiting {
		ns, _ := sprint.NeedsOf(s, c.ID)
		needs[c.ID] = ns
	}
	depth := make(map[string]int, len(waiting))
	cycle := map[string]bool{}
	on := map[string]bool{}
	var walk func(id string) int
	walk = func(id string) int {
		if d, ok := depth[id]; ok {
			return d
		}
		if on[id] {
			cycle[id] = true
			return 0
		}
		on[id] = true
		d := 0
		for _, n := range needs[id] {
			if n.Waived || n.State != sprint.Waiting || !inWaiting[n.ID] {
				continue
			}
			if nd := walk(n.ID) + 1; nd > d {
				d = nd
			}
		}
		delete(on, id)
		depth[id] = d
		return d
	}
	for _, c := range waiting {
		walk(c.ID)
	}
	var v needsView
	byStream := map[string]int{}
	for _, c := range waiting {
		if only != "" && c.Row != only {
			continue
		}
		i, ok := byStream[c.Row]
		if !ok {
			v.Streams = append(v.Streams, needsStream{Stream: c.Row})
			i = len(v.Streams) - 1
			byStream[c.Row] = i
		}
		st := &v.Streams[i]
		st.Total++
		card := needsCard{ID: c.ID, Stream: c.Row, Depth: depth[c.ID]}
		// orphan is whether this card's needs name a dropped or absent id:
		// the closing count counts cards, one card once however many of its
		// needs name one (docs/SPEC-SPRINT.md section 11).
		orphan := false
		for _, n := range needs[c.ID] {
			word := needWord(n.State)
			if n.Waived || word == sprint.Landed {
				continue
			}
			card.Needs = append(card.Needs, needLine{ID: n.ID, State: word})
			if word == droppedWord || word == absentWord {
				orphan = true
			}
		}
		if orphan {
			st.Orphans++
		}
		card.Root = !cycle[c.ID] && card.Depth == 0
		if !rootsOnly || card.Root {
			st.Cards = append(st.Cards, card)
		}
	}
	for i := range v.Streams {
		st := &v.Streams[i]
		// Chain order: a card after every card it needs. A card's depth is one
		// more than its deepest waiting need's, so every card sorts after the
		// cards it waits on; the work order is kept within a depth, where no
		// card waits on another (docs/SPEC-SPRINT.md section 11).
		sort.SliceStable(st.Cards, func(i, j int) bool {
			return st.Cards[i].Depth < st.Cards[j].Depth
		})
		st.Widths = needsWidths(waiting, depth, st.Stream)
		for _, c := range waiting {
			if c.Row == st.Stream && cycle[c.ID] {
				st.Cycle = append(st.Cycle, c.ID)
			}
		}
		if st.Cards == nil {
			st.Cards = []needsCard{}
		}
		v.Cards += st.Total
		v.Orphans += st.Orphans
	}
	return v
}

// needsRestCommand is the needs invocation that prints the list a cap cut,
// the same stream and --roots the reader asked for, with --max 0.
func needsRestCommand(stream string, rootsOnly bool) string {
	var b strings.Builder
	b.WriteString("nova-sprint needs")
	if stream != "" {
		b.WriteString(" --stream ")
		b.WriteString(stream)
	}
	if rootsOnly {
		b.WriteString(" --roots")
	}
	b.WriteString(" --max 0")
	return b.String()
}

func intPtr(n int) *int { return &n }

// bound keeps at most max items of each listed kind (streams, a stream's
// cards, a card's needs, widths, and a cycle). max <= 0 leaves the graph
// whole. Totals are already the whole counts and are not reduced. A cut list
// gets a more field. A cut of cards or streams names the first card it hid
// and that card's unmet-need count, taken before the kept cards' own needs
// are cut, so a blocked card is not shown as needing nothing.
func (v needsView) bound(max int, rootsOnly bool) needsView {
	if max <= 0 {
		return v
	}
	if len(v.Streams) > max {
		v.More = listMore(len(v.Streams)-max, needsRestCommand("", rootsOnly), v.Streams[max].Cards)
		v.Streams = append([]needsStream(nil), v.Streams[:max]...)
	}
	for i := range v.Streams {
		v.Streams[i] = v.Streams[i].bound(max, rootsOnly)
	}
	return v
}

func (st needsStream) bound(max int, rootsOnly bool) needsStream {
	if len(st.Cards) > max {
		st.CardsMore = listMore(len(st.Cards)-max, needsRestCommand(st.Stream, rootsOnly), st.Cards[max:])
		st.Cards = append([]needsCard(nil), st.Cards[:max]...)
	}
	for i := range st.Cards {
		ns := st.Cards[i].Needs
		if len(ns) > max {
			st.Cards[i].More = &needsMore{
				Omitted: len(ns) - max,
				Command: "nova-sprint card --fields " + st.Cards[i].ID,
			}
			st.Cards[i].Needs = append([]needLine(nil), ns[:max]...)
		}
	}
	if len(st.Widths) > max {
		st.WidthsMore = &needsMore{Omitted: len(st.Widths) - max, Command: needsRestCommand(st.Stream, rootsOnly)}
		st.Widths = append([]needsWidth(nil), st.Widths[:max]...)
	}
	if len(st.Cycle) > max {
		st.CycleMore = &needsMore{Omitted: len(st.Cycle) - max, Command: needsRestCommand(st.Stream, rootsOnly)}
		st.Cycle = append([]string(nil), st.Cycle[:max]...)
	}
	return st
}

// listMore is the more field for a cut whose first hidden item is a card,
// when the cut hid cards. hidden is the cards the cut did not list, in the
// order the graph would have printed them.
func listMore(omitted int, command string, hidden []needsCard) *needsMore {
	m := &needsMore{Omitted: omitted, Command: command}
	if len(hidden) > 0 {
		m.Boundary = hidden[0].ID
		m.Needs = intPtr(len(hidden[0].Needs))
	}
	return m
}

// needsWidths is the width at each depth of one stream's waiting cards, depth
// 0 first and a line for every depth between the shallowest and the deepest.
func needsWidths(waiting []*sprint.Card, depth map[string]int, stream string) []needsWidth {
	count := map[int]int{}
	maxDepth := -1
	for _, c := range waiting {
		if c.Row != stream {
			continue
		}
		d := depth[c.ID]
		count[d]++
		if d > maxDepth {
			maxDepth = d
		}
	}
	var out []needsWidth
	for d := 0; d <= maxDepth; d++ {
		if n := count[d]; n > 0 {
			out = append(out, needsWidth{Depth: d, Width: n})
		}
	}
	return out
}

// The words a need's state is printed with when it no longer sits in a work
// column: a kept record's outcome dropped, and no record at all.
const (
	droppedWord = "dropped"
	absentWord  = "absent"
)

// needWord is a need's state as this verb prints it: a column name, dropped
// for a kept record whose outcome is dropped, that outcome for a record kept
// with any other outcome, or absent for no record at all (sprint.NeedsOf
// reads the record; sprint.StateOf and Card.Placed tell the three apart;
// docs/SPEC-SPRINT.md section 11).
func needWord(state string) string {
	switch state {
	case "not on the table":
		return absentWord
	}
	if out, ok := offOutcome(state); ok {
		if out == "dropped" {
			return droppedWord
		}
		return out
	}
	return state
}

// offOutcome is the outcome a kept record's state names: NeedsOf writes the
// state "off the table (<outcome>)" (internal/sprint/steps_work.go), false
// for a placed card's state, which is a column name.
func offOutcome(state string) (string, bool) {
	rest, ok := strings.CutPrefix(state, "off the table (")
	if !ok {
		return "", false
	}
	out, ok := strings.CutSuffix(rest, ")")
	if !ok {
		return "", false
	}
	return out, true
}

// needsText is the graph in lines: one line a card (name, depth, ROOT, its
// unmet needs and their states), a width line a stream, then the closing
// counts. With rootsOnly it prints only the roots and the width lines.
func needsText(v needsView, rootsOnly bool) string {
	var b strings.Builder
	for _, st := range v.Streams {
		for _, c := range st.Cards {
			root := ""
			if c.Root {
				root = " ROOT"
			}
			fmt.Fprintf(&b, "NEEDS stream=%s depth=%d%s %s", st.Stream, c.Depth, root, c.ID)
			if len(c.Needs) == 0 {
				b.WriteString(" needs none")
			} else {
				b.WriteString(" needs ")
				for i, n := range c.Needs {
					if i > 0 {
						b.WriteString(", ")
					}
					fmt.Fprintf(&b, "%s %s", n.ID, n.State)
				}
			}
			b.WriteString("\n")
			writeNeedsMore(&b, "needs", "card="+c.ID, c.More, len(c.Needs))
		}
		writeNeedsMore(&b, "cards", "stream="+st.Stream, st.CardsMore, len(st.Cards))
		b.WriteString("WIDTH stream=" + st.Stream)
		for i, w := range st.Widths {
			if i == 0 {
				b.WriteString(" ")
			} else {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "depth %d: %d", w.Depth, w.Width)
		}
		b.WriteString("\n")
		writeNeedsMore(&b, "width", "stream="+st.Stream, st.WidthsMore, len(st.Widths))
		if len(st.Cycle) > 0 || st.CycleMore != nil {
			fmt.Fprintf(&b, "CYCLE stream=%s its needs make a cycle through %s\n", st.Stream, strings.Join(st.Cycle, ", "))
			writeNeedsMore(&b, "cycle", "stream="+st.Stream, st.CycleMore, len(st.Cycle))
		}
		if rootsOnly {
			continue
		}
		fmt.Fprintf(&b, "NEEDS stream=%s cards=%d dropped-or-absent=%d\n", st.Stream, st.Total, st.Orphans)
	}
	writeNeedsMore(&b, "streams", "", v.More, len(v.Streams))
	if !rootsOnly {
		fmt.Fprintf(&b, "NEEDS OK cards=%d dropped-or-absent=%d\n", v.Cards, v.Orphans)
	}
	return b.String()
}

// writeNeedsMore prints the one line that stands for a list --max cut, and
// prints nothing when the list was whole.
func writeNeedsMore(b *strings.Builder, kind, attr string, m *needsMore, shown int) {
	if m == nil {
		return
	}
	if attr != "" {
		fmt.Fprintf(b, "MORE kind=%s %s shown=%d total=%d omitted=%d", kind, attr, shown, shown+m.Omitted, m.Omitted)
	} else {
		fmt.Fprintf(b, "MORE kind=%s shown=%d total=%d omitted=%d", kind, shown, shown+m.Omitted, m.Omitted)
	}
	if m.Boundary != "" {
		fmt.Fprintf(b, " boundary=%s", m.Boundary)
		if m.Needs != nil {
			fmt.Fprintf(b, " needs=%d", *m.Needs)
		}
	}
	fmt.Fprintf(b, " run: %s\n", m.Command)
}
