package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// inboxDecideFloor is the confidence floor for the inbox triage decision. It is
// per call site and stated here beside the call: below it the note stays
// unclassified and every note prints as it always did.
const inboxDecideFloor = 0.9

// noteDecider is the one typed decision per new note. decide.Client is the
// production provider; tests point newNoteDecider at a fake, so no test ever
// dials the network or needs a key.
type noteDecider interface {
	Decide(ctx context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error)
}

// newNoteDecider is the provider seam. The real client reads its key only from
// the environment variable nova-secrets exec provides, never a file or argv.
var newNoteDecider = func() (noteDecider, error) {
	return decide.New("", "")
}

// noteClass is one note's triage result, ready to print on its INBOX NOTE line.
type noteClass struct {
	Class string
	Conf  float64
	// Below is set when the provider's confidence sat under the floor: the
	// answer is a suggestion, the note keeps today's behaviour, and the line
	// says below=class.
	Below bool
}

func (c noteClass) suffix() string {
	s := fmt.Sprintf(" class=%s conf=%.2f", oneline.Field(c.Class), c.Conf)
	if c.Below {
		s += " below=class"
	}
	return s
}

// inboxDecideOptions are the three typed choices the model picks among.
var inboxDecideOptions = map[string]string{
	"act-now":      "the reader must act on this note now",
	"read-later":   "the reader can read and work on this note later",
	"receipt-only": "the note needs no action; acknowledge receipt only",
}

// inboxDecidePrefixes are the leading markers a subject may carry. Only STOP and
// HOLD are decided by the machinery; the rest are features handed to the model.
var inboxDecidePrefixes = []string{"ADOPTED", "HOLD", "APPROVE", "STOP", "GREEN"}

// prNumberRe matches a subject that starts with a PR number: "#896" or "PR896".
var prNumberRe = regexp.MustCompile(`^(#[0-9]+|PR ?[0-9]+)`)

// subjectPrefix names the leading markers a subject carries, or "-" for none.
func subjectPrefix(subject string) string {
	up := strings.ToUpper(strings.TrimSpace(subject))
	var found []string
	for _, p := range inboxDecidePrefixes {
		if strings.HasPrefix(up, p) {
			found = append(found, p)
		}
	}
	if prNumberRe.MatchString(up) {
		found = append(found, "PR")
	}
	if len(found) == 0 {
		return "-"
	}
	return strings.Join(found, ",")
}

// deterministicActNow reports the subjects the machinery answers itself, before
// the model is ever asked. STOP and HOLD are always act-now.
func deterministicActNow(subject string) bool {
	up := strings.ToUpper(strings.TrimSpace(subject))
	return strings.HasPrefix(up, "STOP") || strings.HasPrefix(up, "HOLD")
}

// noteQuestion builds the public evidence and the one typed choice for a note.
// The evidence is the subject line, the From name, the addr and the leading
// marker and nothing else: the body is private and is never sent.
func noteQuestion(e bus.OpenEntry) (string, map[string]decide.Question) {
	state := "subject: " + strings.TrimSpace(e.Subject) +
		"\nfrom: " + strings.TrimSpace(e.From) +
		"\naddr: " + e.Addr +
		"\nprefix: " + subjectPrefix(e.Subject)
	qs := map[string]decide.Question{
		"class": {
			Instructions: "Classify this bus note by the reader's next action. Judge only from the subject line, the sender, the address line and the leading marker; the body is not shown and must not be assumed. Choose act-now if the reader must act now, read-later if it can wait, receipt-only if it needs no action.",
			Choice:       inboxDecideOptions,
		},
	}
	return state, qs
}

// triageNotes asks one typed choice per new note and returns each note's class.
// STOP and HOLD are act-now without asking. An answer under the floor, a missing
// answer, or a provider error is class=unknown and the note prints as today.
func triageNotes(dec noteDecider, entries []bus.OpenEntry) map[string]noteClass {
	out := make(map[string]noteClass)
	for _, e := range entries {
		if e.Kind != bus.OpenNote || e.Heard {
			continue
		}
		if deterministicActNow(e.Subject) {
			out[e.Path] = noteClass{Class: "act-now", Conf: 1}
			continue
		}
		state, qs := noteQuestion(e)
		answers, _, err := dec.Decide(context.Background(), state, qs)
		a, ok := answers["class"]
		if err != nil || !ok {
			out[e.Path] = noteClass{Class: "unknown", Conf: 0, Below: true}
			continue
		}
		if a.Confidence < inboxDecideFloor {
			out[e.Path] = noteClass{Class: "unknown", Conf: a.Confidence, Below: true}
			continue
		}
		switch a.Choice {
		case "act-now", "read-later", "receipt-only":
			out[e.Path] = noteClass{Class: a.Choice, Conf: a.Confidence}
		default:
			out[e.Path] = noteClass{Class: "unknown", Conf: a.Confidence, Below: true}
		}
	}
	return out
}

// onlyActNow drops every note the decision placed above the floor in a class
// other than act-now. A below-floor note has no class to gate on, so it stays:
// the floor's fallback is today's listing, never a hidden note.
func onlyActNow(entries []bus.OpenEntry, classes map[string]noteClass) []bus.OpenEntry {
	out := make([]bus.OpenEntry, 0, len(entries))
	for _, e := range entries {
		if c, ok := classes[e.Path]; ok && !c.Below && c.Class != "act-now" {
			continue
		}
		out = append(out, e)
	}
	return out
}
