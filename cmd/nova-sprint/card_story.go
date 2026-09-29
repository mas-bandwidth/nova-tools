package main

import (
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// storyLine is one event of a card's timeline as card prints it: its local
// time, its words, the attempt it belongs to, and the log's lines it tells
// (one event may tell several: a merge and its batch, two readers asked).
type storyLine struct {
	At      string        `json:"at"`
	Attempt int           `json:"attempt"`
	Words   string        `json:"words"`
	Lines   []sprint.Line `json:"lines"`
}

// storyText is a report, a finding or a fix, whole: who gave it, on which
// attempt, and its words.
type storyText struct {
	At      string `json:"at"`
	Attempt string `json:"attempt"`
	By      string `json:"by"`
	What    string `json:"what"` // report, finding, fix, reason
	Verdict string `json:"verdict,omitempty"`
	Text    string `json:"text"`
}

// story is a card's history from the log: one line per event a person would
// name, in order, each with its attempt, and the words given along the way.
// The log keeps every record apart; only the telling joins them.
func (a *app) story(v store.CardInfo, lines []sprint.Line) ([]storyLine, []storyText) {
	id := v.Primary.ID
	var about []sprint.Line
	for _, l := range lines {
		if l.About(id) {
			about = append(about, l)
		}
	}
	about = sprint.Timeline(about, id)
	var events []storyLine
	attempt := 0
	for i := 0; i < len(about); {
		j := i + 1
		for j < len(about) && about[j].Op != "" && about[j].Op == about[i].Op {
			j++
		}
		for _, e := range tell(about[i:j]) {
			if n := lineAttempt(e.Lines[0]); n > attempt {
				attempt = n
			}
			e.Attempt = attempt
			e.At = e.Lines[0].At.In(a.zone()).Format("15:04:05")
			events = append(events, e)
		}
		i = j
	}
	return events, texts(a, about)
}

// tell is one step's lines as the events a person would name: the readers
// asked together are one event, a merge and its batch one, a step's answer
// to a judgment part of the move it made, a "came back" note part of the
// finish it follows.
func tell(run []sprint.Line) []storyLine {
	var out []storyLine
	var asked []string
	var askedLines []sprint.Line
	var answers []string
	var batch *sprint.Line
	moved, finished := false, false
	for _, l := range run {
		if l.Note == nil {
			moved = true
			if l.Table == sprint.Fleet && (strings.HasSuffix(l.To, ":"+sprint.DoneOK) || strings.HasSuffix(l.To, ":"+sprint.DoneFailed)) {
				finished = true
			}
		}
		if l.Note != nil && l.Note.Type == sprint.NBatchLanded {
			b := l
			batch = &b
		}
	}
	for _, l := range run {
		switch {
		case l.Note == nil && l.Table == sprint.Readers && l.From == "" && !l.Removed:
			reader, _, _ := strings.Cut(l.To, ":")
			asked = append(asked, reader)
			askedLines = append(askedLines, l)
			continue
		case l.Note != nil && l.Note.Kind == sprint.Decided && moved:
			answers = append(answers, l.Note.Type)
			continue
		case l.Note != nil && l.Note.Type == sprint.NWorkOK && finished:
			continue
		case l.Note != nil && l.Note.Type == sprint.NBatchLanded:
			continue
		}
		words := sprint.Render(l)
		if l.Note == nil {
			words += quoted(l)
		}
		if l.Note != nil && l.Note.Kind == sprint.Judgment && givenIn(run, l.Note.What) {
			// the words are the finish's or the read's, quoted on its line
			n := *l.Note
			n.What = ""
			words = sprint.Render(sprint.Line{Kind: l.Kind, Note: &n})
		}
		if l.Note == nil && l.Table == sprint.Merge && strings.HasSuffix(l.To, ":"+sprint.Merged) && batch != nil {
			stream, _, _ := strings.Cut(l.To, ":")
			words = fmt.Sprintf("merged into %s and landed %s%s", stream, byOf(l), batchWords(*batch, l.Card))
		}
		out = append(out, storyLine{Words: words, Lines: []sprint.Line{l}})
	}
	if len(asked) > 0 {
		w := sprint.Render(askedLines[0])
		if len(asked) > 1 {
			w = fmt.Sprintf("%s asked to read attempt %s %s", strings.Join(asked, " and "), attemptOf(askedLines[0]), byOf(askedLines[0]))
		}
		out = append(out, storyLine{Words: w, Lines: askedLines})
	}
	if len(answers) > 0 && len(out) > 0 {
		out[0].Words += "; answers " + strings.Join(quoteAll(answers), ", ")
	}
	return out
}

// givenIn says a text is the words a move of the step gave.
func givenIn(run []sprint.Line, text string) bool {
	text = strings.TrimSpace(text)
	for _, l := range run {
		for _, t := range l.Text {
			if text != "" && strings.TrimSpace(t) == text {
				return true
			}
		}
	}
	return false
}

func quoteAll(xs []string) []string {
	var out []string
	for _, x := range xs {
		out = append(out, fmt.Sprintf("%q", x))
	}
	return out
}

func byOf(l sprint.Line) string {
	if l.Actor == "" || l.Actor == sprint.MachineActor {
		return "by the machine"
	}
	return "by " + l.Actor
}

// batchWords is the batch a card merged in: its size and the others in it,
// and what the batch note says (ci green).
func batchWords(b sprint.Line, card string) string {
	var others []string
	for _, p := range b.Note.Primaries {
		if p != card {
			others = append(others, p)
		}
	}
	s := ""
	if len(others) > 0 {
		s = fmt.Sprintf(" in a batch of %d (with %s)", len(others)+1, strings.Join(others, ", "))
	}
	if w := strings.TrimSpace(b.Note.What); w != "" {
		s += ", " + w
	}
	return s
}

// quoted is the first line of the words a finish or a read gave, quoted
// after its event: the whole text is below the timeline.
func quoted(l sprint.Line) string {
	for _, f := range []string{"finding", "report"} {
		if t := strings.TrimSpace(l.Text[f]); t != "" {
			first, _, more := strings.Cut(t, "\n")
			q := ` "` + strings.TrimSpace(first) + `"`
			if more {
				q += " (more below)"
			}
			return "." + q
		}
	}
	return ""
}

// lineAttempt is the attempt a line is of: its work or read card's, else the
// attempt a primary's line sets; 0 when it names none.
func lineAttempt(l sprint.Line) int {
	if l.Note != nil {
		return l.Note.Attempt
	}
	n := 0
	fmt.Sscan(attemptOf(l), &n)
	return n
}

func attemptOf(l sprint.Line) string {
	parts := strings.Split(l.Card, ".")
	for _, p := range parts[1:] {
		if len(p) > 1 && (p[0] == 'w' || p[0] == 'r') {
			return p[1:]
		}
	}
	return l.Set["attempt"]
}

// texts is the words given along the way, whole, each once per attempt.
func texts(a *app, about []sprint.Line) []storyText {
	var out []storyText
	given := map[string]bool{}
	for _, l := range about {
		for _, f := range []string{"report", "finding", "fix", "reason", "return_reason", "did"} {
			t := strings.TrimSpace(l.Text[f])
			if t == "" {
				continue
			}
			who := l.Actor
			if who == "" || who == sprint.MachineActor {
				who = "the machine"
			}
			_, col, _ := strings.Cut(l.To, ":")
			verdict := ""
			switch l.Table {
			case sprint.Fleet:
				who, _, _ = strings.Cut(l.From, ":")
				verdict = col
			case sprint.Readers:
				who, _, _ = strings.Cut(l.To, ":")
				verdict = col
			}
			what := f
			if f == "return_reason" {
				what = "reason"
			}
			k := attemptOf(l) + "|" + what + "|" + t
			if given[k] {
				continue
			}
			given[k] = true
			out = append(out, storyText{At: l.At.In(a.zone()).Format("15:04:05"), Attempt: attemptOf(l), By: who, What: what, Verdict: verdict, Text: t})
		}
	}
	return out
}

// printStory is card as a story: what the card is and where in its stream;
// for a card in flight, what holds it now, first; its brief, and for a card
// in flight the fix it was given; its timeline in local time, an attempt at
// a time; the reports, findings and fixes as paragraphs; and for a card that
// has ended, one line saying so.
func (a *app) printStory(w io.Writer, v store.CardInfo, events []storyLine, texts []storyText, held *sprint.Hold, place string) {
	p := v.Primary
	state := "-"
	if p.Placed() {
		state = p.Col
	}
	ended := !p.Placed() || p.Col == string(sprint.Landed)
	head := fmt.Sprintf("%s   stream %s   %s", p.ID, orDashStr(p.F("stream"), p.Row), state)
	if at := p.F("attempt"); at != "" && at != "0" {
		head += "   attempt " + at
	}
	if h := p.F("head"); h != "" && h != p.ID && !strings.HasPrefix(h, p.ID+".w") {
		head += "   head " + h
	}
	for i := len(v.Work) - 1; i >= 0; i-- {
		if b := v.Work[i].F("branch"); b != "" {
			head += "   branch " + b
			break
		}
	}
	if place != "" && !ended {
		head += "   " + place
	}
	fmt.Fprintln(w, oneline.Escape(head))
	if !ended {
		fmt.Fprintln(w, "\nnow:")
		for _, s := range a.nowLines(v, held) {
			fmt.Fprintln(w, "  "+oneline.Escape(s))
		}
	}
	if b := strings.TrimSpace(p.F("brief")); b != "" {
		fmt.Fprintln(w, "\nbrief:")
		paragraph(w, b)
	}
	if f := strings.TrimSpace(p.F("fix")); f != "" && !ended {
		fmt.Fprintln(w, "\nthe fix this attempt was given:")
		paragraph(w, f)
	}
	fmt.Fprintln(w, "\ntimeline:")
	last := -1
	why := map[int]string{}
	for _, e := range events {
		if e.Attempt != last && e.Attempt > 0 {
			heading := fmt.Sprintf("attempt %d", e.Attempt)
			if r := why[e.Attempt-1]; r != "" && e.Attempt > 1 {
				heading += ", because " + r
			}
			fmt.Fprintf(w, "\n  %s\n", heading)
			last = e.Attempt
		}
		for _, l := range e.Lines {
			if r := outcome(l); r != "" {
				why[lineAttempt(l)] = r
			}
		}
		fmt.Fprintf(w, "  %s  %s\n", e.At, oneline.Escape(e.Words))
	}
	if len(texts) > 0 {
		fmt.Fprintln(w, "\nreports and findings:")
		for _, t := range texts {
			label := fmt.Sprintf("  %s  attempt %s, %s by %s", t.At, orDashStr(t.Attempt, "-"), t.What, t.By)
			if t.Verdict != "" {
				label += " (" + t.Verdict + ")"
			}
			fmt.Fprintln(w, oneline.Escape(label)+":")
			paragraph(w, t.Text)
		}
	}
	if ended {
		fmt.Fprintln(w, "\nnow:")
		for _, s := range a.nowLines(v, held) {
			fmt.Fprintln(w, "  "+oneline.Escape(s))
		}
	}
	fmt.Fprintln(w)
}

// outcome is why an attempt ended, from a line of it: its work failed, a
// reader found it broken, it reached its bound.
func outcome(l sprint.Line) string {
	a := attemptOf(l)
	switch {
	case l.Note == nil && l.Table == sprint.Fleet && strings.HasSuffix(l.To, ":"+sprint.DoneFailed):
		return "attempt " + a + " failed"
	case l.Note == nil && l.Table == sprint.Readers && strings.HasSuffix(l.To, ":"+sprint.Broken):
		reader, _, _ := strings.Cut(l.To, ":")
		return reader + " found attempt " + a + " broken"
	case l.Note != nil && l.Note.Type == sprint.NBound:
		return fmt.Sprintf("attempt %d reached its bound", l.Note.Attempt)
	}
	return ""
}

// nowLines is what holds the card at this moment: each open judgment with the
// commands that answer it, or the actor that holds it and its deadline, and
// what it needs; for a card that has ended, that it has.
func (a *app) nowLines(v store.CardInfo, held *sprint.Hold) []string {
	p := v.Primary
	if !p.Placed() {
		return []string{"off the table: " + orDashStr(p.F("outcome"), "dropped") + ", " + orDashStr(p.F("reason"), "-")}
	}
	if p.Col == string(sprint.Landed) && len(v.Open) == 0 {
		return []string{"landed; nothing waits"}
	}
	var out []string
	for _, o := range v.Open {
		out = append(out, fmt.Sprintf("waits on your judgment: %s (%s)", o.Note.Type, o.Note.ID))
		for _, c := range sprint.NoteCommands(o.Note, []string{p.ID}) {
			for _, line := range c.Lines {
				out = append(out, "  "+c.Decision+": "+line)
			}
		}
	}
	if held != nil && held.By != sprint.HeldByJudgment {
		out = append(out, held.Why)
	}
	for _, n := range v.Needs {
		s := "needs " + n.ID + " (" + n.State + ")"
		if n.Waived {
			s += ", waived"
		}
		out = append(out, s)
	}
	if len(v.NeededBy) > 0 {
		out = append(out, "needed by "+strings.Join(v.NeededBy, ", "))
	}
	if len(out) == 0 && held != nil {
		out = append(out, held.String())
	}
	return out
}

// paragraph prints words as they were given, indented, a line at a time;
// control characters other than a line break or a tab are shown escaped, so
// no text given can move the terminal.
func paragraph(w io.Writer, text string) {
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		var b strings.Builder
		for _, r := range line {
			switch {
			case r == '\t':
				b.WriteString("    ")
			case unicode.IsControl(r) || r == ' ' || r == ' ' || unicode.Is(unicode.Bidi_Control, r):
				fmt.Fprintf(&b, `\u%04x`, r)
			default:
				b.WriteRune(r)
			}
		}
		fmt.Fprintln(w, "    "+b.String())
	}
}

func orDashStr(s, alt string) string {
	if s != "" {
		return s
	}
	if alt != "" {
		return alt
	}
	return "-"
}

// linePlace is where a primary stands in its stream's line: first, or its
// place among the stream's cards not landed and the card before it.
func linePlace(p *sprint.Card, all []*sprint.Card) string {
	var open []*sprint.Card
	for _, c := range all {
		if c.Placed() && c.Row == p.Row && c.Col != string(sprint.Landed) && !sprint.IsSentinel(c) {
			open = append(open, c)
		}
	}
	sprint.SortCards(open)
	for i, c := range open {
		if c.ID != p.ID {
			continue
		}
		if i == 0 {
			return "first in line in " + p.Row
		}
		return fmt.Sprintf("%d of %d in line in %s, after %s", i+1, len(open), p.Row, open[i-1].ID)
	}
	return ""
}
