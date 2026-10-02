package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// cmdLog prints the epoch's log: every change of every card and every
// notification, in the order written, filtered by card, stream, member and
// time. The inbox's cursor never hides a line. A brief given on a line is
// said by its size and the card that shows it, not printed (--json has it).
func (a *app) cmdLog(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("log")
	card := fs.String("card", "", "the lines about this card (a primary: its work, read and merge cards too)")
	stream := fs.String("stream", "", "the lines of this stream")
	member := fs.String("member", "", "the lines of this fleet member or reader: what was dealt to, taken from or read by it")
	since := fs.String("since", "", "the lines at or after this time: a duration back from now (10m) or a time (RFC 3339)")
	atEpoch := fs.Int64("at-epoch", -1, "the log of an earlier epoch (before a clear), as it was")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "log", argErr("takes no words ", err))
	}
	var from time.Time
	if *since != "" {
		if d, err := time.ParseDuration(*since); err == nil {
			from = a.now().Add(-d)
		} else if t, err := time.Parse(time.RFC3339, *since); err == nil {
			from = t
		} else {
			return refuse(stderr, "log", "--since wants a duration back from now (10m) or an RFC 3339 time, found "+*since)
		}
	}
	st, err := a.storeAt(*c, *atEpoch)
	if err != nil {
		return refuse(stderr, "log", err.Error())
	}
	lines, err := st.Log(context.Background())
	if err != nil {
		return a.readFailed("log", err, stderr)
	}
	var out []sprint.Line
	for _, l := range lines {
		if keepLine(l, *card, *stream, *member, from) {
			out = append(out, l)
		}
	}
	if c.json {
		if out == nil {
			out = []sprint.Line{}
		}
		b, _ := json.Marshal(struct {
			Lines []sprint.Line `json:"lines"`
		}{out})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	for _, l := range out {
		fmt.Fprintln(stdout, a.logLine(l, *card == ""))
		// a brief is a child's whole brief: the line says it was given and where
		// it is shown, never the brief itself (--json carries it whole)
		brief := l.Text["brief"]
		if brief != "" {
			l.Text = maps.Clone(l.Text)
			delete(l.Text, "brief")
		}
		for _, p := range sprint.RenderText(l) {
			fmt.Fprintln(stdout, "    "+oneline.Escape(p))
		}
		if strings.TrimSpace(brief) != "" {
			fmt.Fprintf(stdout, "    brief: %d bytes, shown by nova-sprint card %s\n", len(brief), oneline.Field(l.Card))
		}
	}
	fmt.Fprintf(stdout, "LOG OK lines=%d of=%d\n", len(out), len(lines))
	return 0
}

// logLine is one line as the log and a card's timeline print it: the local
// time, the card when the view is not one card's, and its words.
func (a *app) logLine(l sprint.Line, withCard bool) string {
	at := l.At.In(a.zone()).Format("15:04:05")
	words := sprint.Render(l)
	if withCard && l.Note == nil && !strings.Contains(words, l.Card) {
		words = l.Card + ": " + words
	}
	return at + "  " + oneline.Escape(words)
}

// zone is the local time zone every time is printed in.
func (a *app) zone() *time.Location {
	if a.loc != nil {
		return a.loc
	}
	return time.Local
}

func keepLine(l sprint.Line, card, stream, member string, from time.Time) bool {
	if !from.IsZero() && l.At.Before(from) {
		return false
	}
	if card != "" && !l.About(card) {
		return false
	}
	if stream != "" && l.Stream != stream && (l.Note == nil || l.Note.Stream != stream) {
		return false
	}
	if member != "" && !byMember(l, member) {
		return false
	}
	return true
}

func byMember(l sprint.Line, m string) bool {
	if l.Note != nil {
		return l.Note.Who == m || strings.Contains(l.Note.What, m)
	}
	if l.Table != sprint.Fleet && l.Table != sprint.Readers {
		return false
	}
	fromRow, _, _ := strings.Cut(l.From, ":")
	toRow, _, _ := strings.Cut(l.To, ":")
	return fromRow == m || toRow == m
}
