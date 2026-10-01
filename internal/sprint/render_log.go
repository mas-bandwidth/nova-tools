package sprint

import (
	"fmt"
	"sort"
	"strings"
)

// Render is a log line in plain words, as `log` and a card's timeline print
// it: the one place a line becomes words. It does not print the time, the
// caller's clock format, or the words given with the change, which follow
// the line as paragraphs (RenderText).
func Render(l Line) string {
	if l.Note != nil {
		return renderNote(l)
	}
	if n := len(l.Cards); n > 1 {
		one := l
		one.Cards = nil
		words := Render(one)
		more := strings.Join(l.Cards[1:min(n, 4)], ", ")
		if n > 4 {
			more += fmt.Sprintf(" and %d more", n-4)
		}
		return fmt.Sprintf("%d cards: %s (with %s)", n, words, more)
	}
	by := byWhom(l.Actor)
	fromRow, fromCol, _ := strings.Cut(l.From, ":")
	toRow, toCol, _ := strings.Cut(l.To, ":")
	moved := l.From != l.To || l.Removed
	switch {
	case toCol == Ctl || fromCol == Ctl:
		return renderCtl(l, toRow)
	case l.Table == Work:
		return renderPrimary(l, fromCol, toCol, moved, by)
	case l.Table == Fleet:
		return renderWork(l, fromRow, fromCol, toRow, toCol, moved, by)
	case l.Table == Readers:
		return renderRead(l, toRow, toCol, moved, by)
	case l.Table == Merge:
		return renderMerge(l, toRow, toCol, moved, by)
	}
	return renderPlain(l, by)
}

// RenderText is the words given with a line (a report, a finding, a fix, a
// reason), each a paragraph, in a fixed order.
func RenderText(l Line) []string {
	var out []string
	for _, f := range TextFields {
		if v := strings.TrimSpace(l.Text[f]); v != "" {
			out = append(out, textLabel(f)+": "+v)
		}
	}
	return out
}

func textLabel(f string) string {
	switch f {
	case "return_reason":
		return "reason"
	case "ci_note":
		return "ci"
	}
	return f
}

func byWhom(actor string) string {
	switch actor {
	case "", MachineActor:
		return "by the machine"
	}
	return "by " + actor
}

func attemptOf(id string) string {
	// <primary>.w<n> and <primary>.r<n>.<reader>
	parts := strings.Split(id, ".")
	for _, p := range parts[1:] {
		if len(p) > 1 && (p[0] == 'w' || p[0] == 'r') {
			return p[1:]
		}
	}
	return ""
}

func renderPrimary(l Line, fromCol, toCol string, moved bool, by string) string {
	id := l.Card
	switch {
	case l.From == "" && !l.Removed:
		s := fmt.Sprintf("%s added to %s %s", id, l.Stream, by)
		if toCol == string(Waiting) {
			s += ", waiting for what it needs"
		}
		return s
	case l.Removed:
		return fmt.Sprintf("%s taken off the table %s", id, by)
	case !moved:
		return fmt.Sprintf("%s changed %s: %s", id, by, setWords(l.Set))
	}
	switch toCol {
	case string(Ready):
		if fromCol == string(Waiting) {
			return fmt.Sprintf("%s is ready: what it needs has landed", id)
		}
		return fmt.Sprintf("%s is ready again %s", id, by)
	case string(Working):
		if fromCol == string(Review) {
			return fmt.Sprintf("%s reworked %s: attempt %s", id, by, l.Set["attempt"])
		}
		return fmt.Sprintf("%s is being worked: attempt %s dealt", id, l.Set["attempt"])
	case string(Review):
		if fromCol == string(Merging) {
			return fmt.Sprintf("%s returned to review %s", id, by)
		}
		return fmt.Sprintf("%s is back for review", id)
	case string(Merging):
		return fmt.Sprintf("%s accepted %s; queued to merge in %s", id, by, l.Stream)
	case string(Landed):
		return fmt.Sprintf("%s landed", id)
	case string(Waiting):
		return fmt.Sprintf("%s waits: a sentinel was put in front of it %s", id, by)
	}
	return renderPlain(l, by)
}

func renderWork(l Line, fromRow, fromCol, toRow, toCol string, moved bool, by string) string {
	a := attemptOf(l.Card)
	switch {
	case l.From == "" && !l.Removed:
		return fmt.Sprintf("attempt %s dealt to %s", a, toRow)
	case l.Removed:
		return fmt.Sprintf("attempt %s taken off %s's queue %s", a, fromRow, by)
	case !moved:
		return fmt.Sprintf("attempt %s changed %s: %s", a, by, setWords(l.Set))
	case toCol == Withdrawn:
		return fmt.Sprintf("attempt %s taken back from %s %s", a, fromRow, whyOf(l))
	case toCol == string(Working):
		return fmt.Sprintf("%s took attempt %s", toRow, a)
	case toCol == DoneOK:
		s := fmt.Sprintf("%s finished attempt %s: ok", fromRow, a)
		if h := l.Set["head"]; h != "" {
			s += ", head " + h
		}
		if b := l.Set["branch"]; b != "" {
			s += " on " + b
		}
		return s
	case toCol == DoneFailed:
		return fmt.Sprintf("%s finished attempt %s: FAILED", fromRow, a)
	case toCol == string(Ready) && fromCol == Withdrawn:
		return fmt.Sprintf("attempt %s redealt to %s (generation %d%s)", a, toRow, l.Gen, redealOf(l))
	case toCol == string(Ready) && fromRow != toRow && strings.Contains(l.Verb, "level"):
		return fmt.Sprintf("attempt %s moved from %s's queue to %s's to even the queues %s (generation %d)", a, fromRow, toRow, by, l.Gen)
	case toCol == string(Ready) && fromRow != toRow:
		return fmt.Sprintf("attempt %s redealt from %s to %s %s (generation %d%s)", a, fromRow, toRow, whyOf(l), l.Gen, redealOf(l))
	}
	return renderPlain(l, by)
}

func renderRead(l Line, toRow, toCol string, moved bool, by string) string {
	a := attemptOf(l.Card)
	reader := toRow
	switch {
	case l.From == "" && !l.Removed:
		return fmt.Sprintf("%s asked to read attempt %s %s", reader, a, by)
	case l.Set[FieldReturned] != "":
		// read --return: not a read, the card back in asked on its row; the
		// reason is the inbox note's
		return fmt.Sprintf("%s returned its read of attempt %s with no verdict", reader, a)
	case l.Removed && l.Set["retired_by"] == "returned":
		// a returned read another reader took: the retirement's words, as a
		// work card taken back says them
		return fmt.Sprintf("the read of attempt %s taken back from %s %s", a, l.Card[strings.LastIndex(l.Card, ".")+1:], by)
	case !moved && l.Set["asked"] != "":
		// a returned read asked of its reader again, in place
		return fmt.Sprintf("%s asked again to read attempt %s %s", reader, a, by)
	case l.Removed:
		return fmt.Sprintf("the read of attempt %s by %s taken off %s", a, l.Card[strings.LastIndex(l.Card, ".")+1:], by)
	case !moved:
		return fmt.Sprintf("the read of attempt %s by %s changed: %s", a, reader, setWords(l.Set))
	case toCol == Reading:
		return fmt.Sprintf("%s began reading attempt %s", reader, a)
	case toCol == OK:
		return fmt.Sprintf("%s read attempt %s: ok", reader, a)
	case toCol == Broken:
		return fmt.Sprintf("%s read attempt %s: broken", reader, a)
	case toCol == Asked:
		return fmt.Sprintf("%s asked again to read attempt %s %s", reader, a, by)
	}
	return renderPlain(l, by)
}

func renderMerge(l Line, toRow, toCol string, moved bool, by string) string {
	switch {
	case l.From == "" && !l.Removed:
		return fmt.Sprintf("queued to merge in %s", toRow)
	case l.Removed:
		return fmt.Sprintf("off the merge queue of %s %s", toRow, by)
	case !moved:
		return fmt.Sprintf("its merge card changed %s: %s", by, setWords(l.Set))
	case toCol == Merged:
		return fmt.Sprintf("merged into %s and landed %s", toRow, by)
	case toCol == Stuck:
		return fmt.Sprintf("stuck in the merge of %s %s", toRow, whyOf(l))
	case toCol == Returned:
		return fmt.Sprintf("off the merge queue of %s: returned %s", toRow, by)
	case toCol == Queued:
		return fmt.Sprintf("queued to merge in %s again %s", toRow, by)
	}
	return renderPlain(l, by)
}

func renderCtl(l Line, row string) string {
	if st := l.Set["state"]; st != "" {
		s := fmt.Sprintf("%s %s %s", row, st, byWhom(l.Actor))
		if c := l.Set["cause"]; c != "" {
			s += " (" + c + ")"
		}
		return s
	}
	return fmt.Sprintf("%s changed %s: %s", row, byWhom(l.Actor), setWords(l.Set))
}

func renderPlain(l Line, by string) string {
	from, to := orDash(l.From), orDash(l.To)
	if l.Removed {
		to = "off the table"
	}
	return fmt.Sprintf("%s %s -> %s %s (%s)", l.Card, from, to, by, l.Verb)
}

// redealOf is a redeal's count against its bound, as a line says it.
func redealOf(l Line) string {
	if n := l.Set["redeals"]; n != "" {
		return fmt.Sprintf("; redeal %s of %d", n, MaxRedeals)
	}
	return ""
}

func whyOf(l Line) string {
	if l.Cause == "" {
		return byWhom(l.Actor)
	}
	return byWhom(l.Actor) + ": " + l.Cause
}

// setWords is the fields a change set, as name=value, sorted, stamps aside.
func setWords(set map[string]string) string {
	var out []string
	for k, v := range set {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

func renderNote(l Line) string {
	n := l.Note
	who := byWhom(n.Who)
	if l.Verb == "updated" {
		return "judgment updated: " + n.Type + ": " + n.What
	}
	switch n.Kind {
	case Judgment:
		s := "judgment: " + n.Type
		if n.What != "" {
			s += ": " + firstLine(n.What)
		}
		if len(n.Decisions) > 0 {
			s += " (decisions: " + strings.Join(n.Decisions, ", ") + ")"
		}
		return s
	case Decided:
		return fmt.Sprintf("%s answered %q: %s", strings.TrimPrefix(who, "by "), n.Type, firstLine(strings.TrimPrefix(n.What, "answered by ")))
	case Acknowledged:
		if !n.Review.IsZero() {
			return fmt.Sprintf("%q held %s until %s", n.Type, who, n.Review.Format("15:04"))
		}
		return fmt.Sprintf("%q acknowledged %s", n.Type, who)
	}
	s := n.Type
	if n.What != "" {
		s += ": " + firstLine(n.What)
	}
	return s
}

// firstLine is a text's first line, marked when more follows: the whole
// text is printed as a paragraph below the timeline.
func firstLine(s string) string {
	if first, _, more := strings.Cut(strings.TrimSpace(s), "\n"); more {
		return strings.TrimSpace(first) + " (more below)"
	}
	return strings.TrimSpace(s)
}

// Timeline is a primary's lines as its story tells them: in the order
// written, a step's lines with the primary's own first, then its work, read
// and merge cards', then the notifications; a primary's line that only
// mirrors its work card's in the same step (dealt, finished, taken back)
// left to the work card's.
func Timeline(lines []Line, id string) []Line {
	rank := func(l Line) int {
		switch {
		case l.Note != nil:
			return 4
		case l.Table == Work:
			return 0
		case l.Table == Fleet:
			return 1
		case l.Table == Readers:
			return 2
		}
		return 3
	}
	fleetIn := map[string]bool{} // ops with a line of the primary's work card
	for _, l := range lines {
		if l.Note == nil && l.Table == Fleet && l.Primary == id {
			fleetIn[l.Op] = true
		}
	}
	mergeIn := map[string]bool{} // ops with a line of the primary's merge card
	for _, l := range lines {
		if l.Note == nil && l.Table == Merge && l.Card == id {
			mergeIn[l.Op] = true
		}
	}
	var out []Line
	for _, l := range lines {
		switch {
		case l.Note == nil && l.Table == Work && l.Card == id && fleetIn[l.Op] && mirrorsWork(l):
			continue
		case l.Note == nil && l.Table == Work && l.Card == id && mergeIn[l.Op] && strings.HasSuffix(l.To, ":"+string(Landed)):
			continue // the merge card's line says merged and landed
		case l.Note == nil && l.Table == Merge && l.From == "" && !l.Removed:
			continue // queued at the accept, which says so
		case l.Note == nil && l.From == l.To && !l.Removed && len(l.Text) == 0 && l.Set["score"] == "":
			continue // a field set in passing (a stamp, the readers asked): the lines around it say what happened
		}
		out = append(out, l)
	}
	// within a step (a run of lines of one op), the primary's first
	for i := 0; i < len(out); {
		j := i + 1
		for j < len(out) && out[j].Op != "" && out[j].Op == out[i].Op {
			j++
		}
		run := out[i:j]
		sort.SliceStable(run, func(x, y int) bool { return rank(run[x]) < rank(run[y]) })
		i = j
	}
	return out
}

// mirrorsWork says a primary's line says only what its work card's line of
// the same step says: dealt (ready -> working), finished (working ->
// review), taken back (working -> ready).
func mirrorsWork(l Line) bool {
	_, from, _ := strings.Cut(l.From, ":")
	_, to, _ := strings.Cut(l.To, ":")
	switch {
	case from == string(Ready) && to == string(Working):
		return true
	case from == string(Working) && to == string(Review):
		return true
	case from == string(Working) && to == string(Ready):
		return true
	}
	return false
}
