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

// storyLine is one event of a card's timeline, for --json: the log's line,
// its words, and its local time.
type storyLine struct {
	At    string      `json:"at"`
	Words string      `json:"words"`
	Line  sprint.Line `json:"line"`
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

// story is a card's history from the log: the events worth a line, in
// order, and the words given along the way.
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
	var texts []storyText
	for _, l := range about {
		at := l.At.In(a.zone()).Format("15:04:05")
		events = append(events, storyLine{At: at, Words: sprint.Render(l), Line: l})
		for _, f := range []string{"report", "finding", "fix", "reason", "return_reason", "did"} {
			t := strings.TrimSpace(l.Text[f])
			if t == "" {
				continue
			}
			who := l.Actor
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
			texts = append(texts, storyText{At: at, Attempt: attemptWord(l), By: who, What: textWord(f), Verdict: verdict, Text: t})
		}
	}
	return events, texts
}

func attemptWord(l sprint.Line) string {
	parts := strings.Split(l.Card, ".")
	for _, p := range parts[1:] {
		if len(p) > 1 && (p[0] == 'w' || p[0] == 'r') {
			return p[1:]
		}
	}
	return l.Set["attempt"]
}

func textWord(f string) string {
	switch f {
	case "return_reason":
		return "reason"
	}
	return f
}

// printStory is card as a story: what the card is, its brief, its timeline
// in local time, the reports and findings as paragraphs, and what it waits
// for now.
func (a *app) printStory(w io.Writer, v store.CardInfo, events []storyLine, texts []storyText, held *sprint.Hold) {
	p := v.Primary
	state := "-"
	if p.Placed() {
		state = p.Col
	}
	head := fmt.Sprintf("%s   stream %s   %s", p.ID, orDashStr(p.F("stream"), p.Row), state)
	if at := p.F("attempt"); at != "" && at != "0" {
		head += "   attempt " + at
	}
	if wc := liveWork(v); wc != nil {
		head += "   " + wc.Row + " holds " + wc.ID + "@" + wc.F("gen") + " (" + wc.Col + ")"
	}
	fmt.Fprintln(w, oneline.Escape(head))
	if b := strings.TrimSpace(p.F("brief")); b != "" {
		fmt.Fprintln(w, "\nbrief:")
		paragraph(w, b)
	}
	if f := strings.TrimSpace(p.F("fix")); f != "" {
		fmt.Fprintln(w, "\nthe fix of this attempt:")
		paragraph(w, f)
	}
	fmt.Fprintln(w, "\ntimeline:")
	for _, e := range events {
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
	var waits []string
	for _, n := range v.Needs {
		s := "needs " + n.ID + " (" + n.State + ")"
		if n.Waived {
			s += ", waived"
			if n.WaivedBy != "" {
				s += " by " + n.WaivedBy
			}
		}
		waits = append(waits, s)
	}
	if len(v.NeededBy) > 0 {
		waits = append(waits, "needed by "+strings.Join(v.NeededBy, ", "))
	}
	for _, o := range v.Open {
		waits = append(waits, "open: "+o.Note.ID+" "+o.Note.Type+" -> "+strings.Join(o.Note.Decisions, " | "))
	}
	if held != nil {
		waits = append(waits, "held: "+held.String())
	}
	if len(waits) > 0 {
		fmt.Fprintln(w, "\nnow:")
		for _, s := range waits {
			fmt.Fprintln(w, "  "+oneline.Escape(s))
		}
	}
	fmt.Fprintln(w)
}

// liveWork is the primary's live work card, if it has one.
func liveWork(v store.CardInfo) *sprint.Card {
	for _, c := range v.Work {
		if c.Placed() && c.ID == v.Primary.F("work") && (c.Col == sprint.Ready || c.Col == sprint.Working) {
			return c
		}
	}
	return nil
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
