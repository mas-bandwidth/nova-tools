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

// Line is the row as the ledger prints it. A tool with no verbs prints
// `verb=-`: its bare invocation is a unit like any other, and a unit with no
// row can never be dogfooded.
func (r Row) Line() string {
	return fmt.Sprintf("DOGFOOD tool=%s verb=%s by=%s at=%s ok=%s issue=%s",
		r.Tool, r.Verb, r.By, r.At, r.OK, r.Issue)
}

// Summary is the count line under the rows.
type Summary struct {
	Verbs       int // verbs docs/CLI.md declares
	Dogfooded   int // verbs with at least one receipt
	ByNonAuthor int // verbs a non-author has run and said ok
	OpenEdges   int // edges nobody has since cleared
	Unfiled     int // of those, the ones with no issue anybody can act on

	// Unmatched is the receipts naming a verb the list does not declare. It is
	// a COUNT ON THE LINE, not a note beside it: on 2026-09-18 the gate
	// reported open-edges=0 at exit 0 with not-ok receipts sitting in the
	// directory it had just read, and findings=1 while three more sat
	// unmatched, because a receipt that matched nothing simply left the
	// arithmetic. A count that silently leaves evidence out is worse than no
	// count, so every read prints this one whether it is zero or not.
	Unmatched int
}

// Line is the summary as the ledger prints it.
func (s Summary) Line() string {
	return fmt.Sprintf("DOGFOOD OK verbs=%d dogfooded=%d by-nonauthor=%d open-edges=%d unfiled=%d unmatched=%d",
		s.Verbs, s.Dogfooded, s.ByNonAuthor, s.OpenEdges, s.Unfiled, s.Unmatched)
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
			summary.Unmatched++
			continue
		}
		byVerb[key] = append(byVerb[key], r)
	}

	rows := make([]Row, 0, len(verbs))
	for _, v := range verbs {
		key := normalizeKey(v.Key())
		got := byVerb[key]
		row := Row{Tool: v.Tool, Verb: v.Spelling(), By: "nobody", At: "-", OK: "-", Issue: "-"}
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
		for _, edge := range openEdges(got) {
			summary.OpenEdges++
			if !edge.Filed() {
				summary.Unfiled++
			}
		}
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

// openEdges returns the receipts for one verb that found something and that no
// later receipt has cleared. A receipt finds something when the verb did not do
// what the run needed, or when its notes name an edge; it is cleared when
// somebody runs the verb again, later, and records neither. That is what
// "feedback applied" means from outside the fix — and an edge with no issue
// number is one nobody else can act on at all, which the summary counts
// separately.
func openEdges(got []Receipt) []Receipt {
	var latestClean time.Time
	for _, r := range got {
		if !r.RecordsAnEdge() && r.Time().After(latestClean) {
			latestClean = r.Time()
		}
	}
	var open []Receipt
	for _, r := range got {
		if !r.RecordsAnEdge() {
			continue
		}
		if !latestClean.IsZero() && !r.Time().After(latestClean) {
			continue
		}
		open = append(open, r)
	}
	return open
}

// Strand is one receipt that named a verb the reference does not declare: the
// file it is in, what it said, and the declared verb it was probably meant to
// be. The 2026-09-18 dogfood pass was told "receipts=9 name a verb docs/CLI.md
// does not declare" and nothing else, so nine real runs were invisible and
// nobody could tell which nine or how to spell them.
type Strand struct {
	Receipt Receipt
	File    string // where the stranded receipt lives
	Nearest string // the closest declared verb, or "" when nothing is close
}

// Line is the strand as the ledger and the gate print it.
func (s Strand) Line() string {
	where := s.File
	if where == "" {
		where = "(receipt)"
	}
	nearest := "nothing close enough to suggest"
	if s.Nearest != "" {
		nearest = "nearest declared: " + s.Nearest
	}
	return fmt.Sprintf("DOGFOOD NOTE stranded %s: tool=%s verb=%s by=%s (%s)",
		where, s.Receipt.Tool, s.Receipt.Spelling(), s.Receipt.By, nearest)
}

// Stranded returns every receipt naming a verb the reference does not declare,
// in the order they were read, each with the nearest declared verb.
func Stranded(verbs []Verb, receipts []Receipt) []Strand {
	declared := map[string]bool{}
	for _, v := range verbs {
		declared[v.Key()] = true
	}
	var strands []Strand
	for _, r := range receipts {
		if declared[r.Key()] {
			continue
		}
		strands = append(strands, Strand{Receipt: r, File: r.File, Nearest: Nearest(verbs, r.Tool, r.Verb)})
	}
	return strands
}

// Nearest returns the declared verb closest to what a receipt wrote, or "" when
// nothing is close enough to be worth suggesting. A verb of the same tool wins
// over a nearer one of another tool: a remedy that sent a reader to a different
// binary would be further from the truth than saying nothing.
func Nearest(verbs []Verb, tool, verb string) string {
	want := NormalizeKey(tool, verb)
	// A verb that is written down INSIDE a declared one is not a near miss, it
	// is the sub-verb somebody dropped a word from: `--verb ledger` for
	// `dogfood ledger`, which is exactly what stranded the receipts on
	// 2026-09-18. The shortest declared verb of the same tool that carries
	// these words wins, ahead of any edit distance.
	if words := strings.Fields(verb); len(words) > 0 {
		best := ""
		for _, v := range verbs {
			if v.Tool != tool || !containsWords(strings.Fields(v.Verb), words) {
				continue
			}
			if best == "" || len(v.Key()) < len(best) {
				best = v.Key()
			}
		}
		if best != "" {
			return best
		}
	}
	best, bestDist := "", -1
	for _, v := range verbs {
		d := editDistance(want, v.Key())
		if v.Tool != tool {
			// Another tool's verb has to be much closer to be worth naming.
			d += 4
		}
		if bestDist < 0 || d < bestDist {
			best, bestDist = v.Key(), d
		}
	}
	// A suggestion further away than half the spelling is a guess, and a guess
	// in a remedy line is worse than an honest silence.
	if bestDist < 0 || bestDist > len(want)/2 {
		return ""
	}
	return best
}

// containsWords reports whether want appears in have as a run of whole words.
func containsWords(have, want []string) bool {
	if len(want) == 0 || len(want) > len(have) {
		return false
	}
	for i := 0; i+len(want) <= len(have); i++ {
		match := true
		for j := range want {
			if have[i+j] != want[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// editDistance is Levenshtein, over the short strings a verb key is.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, min(cur[j-1]+1, prev[j-1]+cost))
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
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

// Gate is what the release lane calls. It says no on three grounds:
//
//   - an UNMATCHED not-ok receipt: somebody ran something, it did not do what
//     they needed, and the verb they named is not one the list declares. The
//     gate used to drop it entirely — `open-edges=0` at exit 0 with not-ok
//     receipts sitting in the very directory it had just read, and `findings=1`
//     while three more sat unmatched beside it. A lane must not be able to pass
//     on a bench where the only thing anybody found is unreadable. An unmatched
//     receipt that says OK is counted and named and is NOT a failure: a wrong
//     spelling or a stale document is not a reason to stop a release nobody
//     found anything wrong with.
//   - an open edge: somebody ran the verb, it did not do what they needed, and
//     nobody has run it since and said it did. Feedback filed is not feedback
//     applied.
//   - with requireAll, a verb no non-author has run and passed. This is the
//     definition of done as Glenn wrote it on 2026-09-18, made mechanical:
//     the author's own pass is not evidence the tool works for anybody else.
//
// The unmatched findings come FIRST — a lane reads what was thrown away before
// it reads anything derived from what was kept — and the rest come back in the
// reference's order, verb by verb, so the list a release reads is the list the
// ledger printed.
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
	// What was thrown away is said first, and only a NOT-OK one is a failure.
	for _, s := range Stranded(verbs, receipts) {
		if s.Receipt.OK {
			continue
		}
		where := s.File
		if where == "" {
			where = "(receipt)"
		}
		nearest := "nothing close enough to suggest"
		if s.Nearest != "" {
			nearest = "nearest declared: " + s.Nearest
		}
		findings = append(findings, GateFinding{
			Kind: "unmatched",
			Tool: s.Receipt.Tool,
			Verb: s.Receipt.Spelling(),
			Reason: fmt.Sprintf("not-ok receipt %s from %s at %s names no verb the list declares (%s), so it counts for nothing: %s",
				where, s.Receipt.By, s.Receipt.At, nearest, s.Receipt.Notes),
		})
	}
	for _, v := range verbs {
		key := normalizeKey(v.Key())
		got := byVerb[key]
		for _, edge := range openEdges(got) {
			reason := fmt.Sprintf("open edge from %s at %s", edge.By, edge.At)
			if edge.Filed() {
				reason += fmt.Sprintf(" (issue #%d)", edge.Issue)
			} else {
				reason += " (no issue filed)"
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
	return fmt.Sprintf("DOGFOOD GATE OK verbs=%d by-nonauthor=%d open-edges=%d unfiled=%d unmatched=%d require-all=%s",
		s.Verbs, s.ByNonAuthor, s.OpenEdges, s.Unfiled, s.Unmatched, require)
}

// GateCountLine is the gate's red count line: the total, and how much of it was
// printed under the ceiling.
func (s Summary) GateCountLine(findings, shown int) string {
	return fmt.Sprintf("DOGFOOD GATE FAIL verbs=%d findings=%d shown=%d unmatched=%d", s.Verbs, findings, shown, s.Unmatched)
}
