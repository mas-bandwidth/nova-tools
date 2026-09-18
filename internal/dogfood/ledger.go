package dogfood

import (
	"fmt"
	"strings"
	"time"
)

// Authors maps a verb key ("nova-check links") to the person who wrote it.
// A verb with no entry has no known author, so every receipt for it counts as
// a non-author's: the gate is about evidence somebody else ran the verb, and
// an unknown author is not a reason to hold a verb back, it is a reason to
// write the mapping down.
type Authors map[string]string

// Author returns the recorded author of a verb, or "" when there is none.
func (a Authors) Author(key string) string { return a[normalizeKey(key)] }

// Set records an author for a verb key.
func (a Authors) Set(key, author string) { a[normalizeKey(key)] = strings.TrimSpace(author) }

// wrote reports whether `by` is the author of this verb. Names are compared
// case-insensitively and trimmed, and nothing else: a receipt that spells a
// name differently is a receipt with a different name, and the ledger says who
// it has rather than guessing who it meant.
func (a Authors) wrote(key, by string) bool {
	author := a.Author(key)
	if author == "" || strings.TrimSpace(by) == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(author), strings.TrimSpace(by))
}

func normalizeKey(key string) string { return strings.Join(strings.Fields(key), " ") }

// Row is one verb's line in the ledger: the verb, and the receipt that speaks
// best for it. Every field is already the token the CLI prints, so the line
// grammar lives in one place and the tests can read it.
type Row struct {
	Tool      string
	Verb      string
	By        string // the dogfooder's name, or "nobody"
	At        string // RFC3339, or "-"
	OK        string // "yes", "no", or "-" when nobody has run it
	Issue     string // the edge filed, or "-"
	NonAuthor bool   // the receipt shown is somebody other than the author's
}

// Line is the row as the ledger prints it.
func (r Row) Line() string {
	return fmt.Sprintf("DOGFOOD tool=%s verb=%s by=%s at=%s ok=%s issue=%s",
		r.Tool, r.Verb, r.By, r.At, r.OK, r.Issue)
}

// Summary is the count line under the rows.
type Summary struct {
	Verbs       int // verbs docs/CLI.md declares
	Dogfooded   int // verbs with at least one receipt
	ByNonAuthor int // verbs a non-author has run and said ok
	OpenEdges   int // ok=false receipts nobody has since cleared
	Unknown     int // receipts naming a verb the reference does not declare
}

// Line is the summary as the ledger prints it.
func (s Summary) Line() string {
	return fmt.Sprintf("DOGFOOD OK verbs=%d dogfooded=%d by-nonauthor=%d open-edges=%d",
		s.Verbs, s.Dogfooded, s.ByNonAuthor, s.OpenEdges)
}

// Ledger reads the receipts against the verbs and returns one row per verb, in
// the reference's order, and the summary.
//
// One verb can carry several receipts, and the row shows the one that speaks
// best for the verb: a non-author's pass first, then a non-author's run,
// then the author's own pass, then whatever there is, latest first inside each
// rank. The counts do not come from the shown row — `dogfooded` counts verbs
// with any receipt at all, `by-nonauthor` counts verbs a non-author ran and
// said ok — so a verb whose newest receipt is an author's still counts as run
// by the person who ran it.
func Ledger(verbs []Verb, receipts []Receipt, authors Authors) ([]Row, Summary) {
	if authors == nil {
		authors = Authors{}
	}
	byVerb := map[string][]Receipt{}
	declared := map[string]bool{}
	for _, v := range verbs {
		declared[normalizeKey(v.Key())] = true
	}
	summary := Summary{Verbs: len(verbs)}
	for _, r := range receipts {
		key := normalizeKey(r.Key())
		if !declared[key] {
			summary.Unknown++
			continue
		}
		byVerb[key] = append(byVerb[key], r)
	}

	rows := make([]Row, 0, len(verbs))
	for _, v := range verbs {
		key := normalizeKey(v.Key())
		got := byVerb[key]
		row := Row{Tool: v.Tool, Verb: v.Verb, By: "nobody", At: "-", OK: "-", Issue: "-"}
		if len(got) > 0 {
			summary.Dogfooded++
			best := got[0]
			for _, r := range got[1:] {
				if rank(authors, key, r) > rank(authors, key, best) ||
					(rank(authors, key, r) == rank(authors, key, best) && r.Time().After(best.Time())) {
					best = r
				}
			}
			row.By = best.By
			row.At = best.At
			row.OK = yesNo(best.OK)
			row.NonAuthor = !authors.wrote(key, best.By)
			if best.Issue > 0 {
				row.Issue = fmt.Sprintf("%d", best.Issue)
			}
			for _, r := range got {
				if r.OK && !authors.wrote(key, r.By) {
					summary.ByNonAuthor++
					break
				}
			}
		}
		rows = append(rows, row)
		summary.OpenEdges += len(openEdges(got))
	}
	return rows, summary
}

// rank orders the receipts for one verb by how much they settle the question
// the ledger asks. A non-author's pass settles it; the author's own pass does
// not settle it at all, but it is still better than nothing to show.
func rank(authors Authors, key string, r Receipt) int {
	nonAuthor := !authors.wrote(key, r.By)
	switch {
	case nonAuthor && r.OK:
		return 3
	case nonAuthor:
		return 2
	case r.OK:
		return 1
	default:
		return 0
	}
}

func yesNo(ok bool) string {
	if ok {
		return "yes"
	}
	return "no"
}

// openEdges returns the receipts for one verb that said NO and that no later
// receipt for the same verb has cleared with a yes. An edge is open until
// somebody runs the verb again and it does what they needed: that is what
// "feedback applied" means from outside the fix.
func openEdges(got []Receipt) []Receipt {
	var latestOK time.Time
	for _, r := range got {
		if r.OK && r.Time().After(latestOK) {
			latestOK = r.Time()
		}
	}
	var open []Receipt
	for _, r := range got {
		if r.OK {
			continue
		}
		// An edge recorded after the last pass is still open; one recorded
		// before it has been answered by a later run that worked.
		if !latestOK.IsZero() && !r.Time().After(latestOK) {
			continue
		}
		open = append(open, r)
	}
	return open
}

// GateFinding is one reason the gate says no.
type GateFinding struct {
	Kind   string // "open-edge" or "not-dogfooded"
	Tool   string
	Verb   string
	Reason string
}

// Line is the finding as the gate prints it.
func (f GateFinding) Line() string {
	return fmt.Sprintf("DOGFOOD GATE FAIL tool=%s verb=%s: %s", f.Tool, f.Verb, f.Reason)
}

// Gate is what the release lane calls. It says no on two grounds:
//
//   - an open edge: somebody ran the verb, it did not do what they needed, and
//     nobody has run it since and said it did. Feedback filed is not feedback
//     applied.
//   - with requireAll, a verb no non-author has run and passed. This is the
//     definition of done as Glenn wrote it on 2026-09-18, made mechanical:
//     the author's own pass is not evidence the tool works for anybody else.
//
// Findings come back in the reference's order, verb by verb, so the list a
// release reads is the list the ledger printed.
func Gate(verbs []Verb, receipts []Receipt, authors Authors, requireAll bool) ([]GateFinding, Summary) {
	if authors == nil {
		authors = Authors{}
	}
	_, summary := Ledger(verbs, receipts, authors)
	byVerb := map[string][]Receipt{}
	declared := map[string]bool{}
	for _, v := range verbs {
		declared[normalizeKey(v.Key())] = true
	}
	for _, r := range receipts {
		key := normalizeKey(r.Key())
		if declared[key] {
			byVerb[key] = append(byVerb[key], r)
		}
	}

	var findings []GateFinding
	for _, v := range verbs {
		key := normalizeKey(v.Key())
		got := byVerb[key]
		for _, edge := range openEdges(got) {
			reason := fmt.Sprintf("open edge from %s at %s", edge.By, edge.At)
			if edge.Issue > 0 {
				reason += fmt.Sprintf(" (issue #%d)", edge.Issue)
			}
			reason += ": " + edge.Notes
			findings = append(findings, GateFinding{Kind: "open-edge", Tool: v.Tool, Verb: v.Verb, Reason: reason})
		}
		if !requireAll {
			continue
		}
		passed := false
		for _, r := range got {
			if r.OK && !authors.wrote(key, r.By) {
				passed = true
				break
			}
		}
		if !passed {
			reason := "not dogfooded by a non-author; a tool is done when somebody who did not write it has run it on real work"
			if len(got) > 0 {
				reason = fmt.Sprintf("only the author has run it (%d receipt(s)); %s", len(got), reason)
			}
			findings = append(findings, GateFinding{Kind: "not-dogfooded", Tool: v.Tool, Verb: v.Verb, Reason: reason})
		}
	}
	return findings, summary
}

// GateLine is the gate's green line: what it checked and what it found.
func (s Summary) GateLine(requireAll bool) string {
	require := "no"
	if requireAll {
		require = "yes"
	}
	return fmt.Sprintf("DOGFOOD GATE OK verbs=%d by-nonauthor=%d open-edges=%d require-all=%s",
		s.Verbs, s.ByNonAuthor, s.OpenEdges, require)
}

// GateCountLine is the gate's red count line: the total, and how much of it was
// printed under the ceiling.
func (s Summary) GateCountLine(findings, shown int) string {
	return fmt.Sprintf("DOGFOOD GATE FAIL verbs=%d findings=%d shown=%d", s.Verbs, findings, shown)
}
