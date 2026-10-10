package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// printPacket prints what a worker or a reader is handed with a card: where
// to work (a work card) or what to read (a read card), the brief, the fix
// of this attempt, the worker's report (a read), and the notes, each whole,
// and the command that reports it.
func printPacket(w io.Writer, p sprint.Packet) {
	kv := func(k, v string) {
		if v != "" {
			fmt.Fprintf(w, "  %s: %s\n", k, oneline.Escape(v))
		}
	}
	fmt.Fprintf(w, "PACKET %s attempt=%d gen=%d epoch=%d\n", oneline.Escape(p.Card), p.Attempt, p.Gen, p.Epoch)
	kv("tier", p.Tier) // every packet names its tier (sprint.DealtTier), in text as in --json
	if p.Kind == "work" {
		kv("branch", p.Branch)
		kv("base", orDashStr(p.Base, "the stream's base"))
		if p.Route != "" {
			kv("route", fmt.Sprintf("%s model=%s tokens=%s deadline=%ds", p.Route, p.Model, orDashStr(p.Tokens, "-"), p.Deadline))
		}
	} else {
		kv("work", fmt.Sprintf("attempt %d by %s", p.Attempt, orDashStr(p.Worker, "-")))
		kv("head", p.Head)
		kv("branch", p.WorkBranch)
		kv("base", p.WorkBase)
	}
	para := func(k, v string) {
		if strings.TrimSpace(v) == "" {
			return
		}
		fmt.Fprintf(w, "  %s:\n", k)
		paragraph(w, v)
	}
	para("brief", p.Brief)
	para("why this attempt exists", p.Why)
	para("a reader found", p.Finding)
	para("fix (this attempt)", p.Fix)
	para("report", p.Report)
	if len(p.Notes) == 0 {
		fmt.Fprintln(w, "  notes: none")
	}
	for _, n := range p.Notes {
		para("note", n)
	}
	if p.Kind != "work" {
		fmt.Fprintln(w, "  attribution: the By: line, the Co-Authored-By trailer and the model or harness named are never a finding and never decide a verdict")
	}
	if p.Kind == "work" {
		fmt.Fprintf(w, "  report it: nova-sprint finish --as %s %s@%d --epoch %d --branch %s --head <commit> --report '<what you did>' [--failed]\n", p.As, p.Card, p.Gen, p.Epoch, p.Branch)
	} else if sprint.IsFriendRow(p.As) {
		// a read asked of a friend is on her fleet row: her outbox returns it, or the read verb
		fmt.Fprintf(w, "  report it: %s\n", sprint.FriendReadOutboxLine(p.As, p.Card, p.Epoch))
	} else {
		fmt.Fprintf(w, "  report it: nova-sprint read --as %s (--ok | --broken) %s --epoch %d --finding '<file:line, and what to change>'\n", p.As, p.Card, p.Epoch)
	}
}
