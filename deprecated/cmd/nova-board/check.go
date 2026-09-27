package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// check is the rule, mechanized. CHECK BEFORE YOU FILE.
//
// IT EXITS 1 WHEN IT MATCHES — ALWAYS, AND WITH NO EXCEPTION — and that is this tool's
// most important sentence. A check in
// this repo is a thing that can say NO (SPEC.md: a check never seen failing is not a
// check), and the NO a board owes a filer is "this is already on the board, do not file
// it". The inverted reading — 0 for "found it" — would make the natural && chain file
// EXACTLY the duplicates, which is why the mnemonic is written into the banner, into the
// README's first run and into a test named for it.
//
// MATCHING IS DELIBERATELY DUMB, because a clever matcher is one a filer cannot predict:
// the query is split on whitespace, each word is lower-cased, and a card matches when
// EVERY word appears as a substring of its lower-cased text. No stemming, no synonyms, no
// ranking, no regular expressions. A filer who gets a surprising answer can see why by
// reading the card.
//
// It is an ADVISORY INSTRUMENT WITH A VERDICT, and the difference matters: the verdict is
// "a card on this board contains all your words", which is a fact; it is not "your finding
// is a duplicate", which is a judgment the filer makes. The 25-duplicate batch did not
// happen because anybody overrode a check — it happened because there was nothing to
// override.
func cmdCheck(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("check")
	var words string
	var all bool
	var max int
	f.fs.StringVar(&words, "words", "", "")
	f.fs.BoolVar(&all, "all", false, "")
	f.fs.IntVar(&max, "max", bounded.Default, "")
	if !f.parse(args, stderr) {
		return 2
	}
	backend, _, source := f.backend()
	f.need(words, wordsHint)
	max = f.cap(max)
	query := strings.Fields(strings.ToLower(words))
	if strings.TrimSpace(words) != "" && len(query) == 0 {
		f.want(wordsHint)
	}
	if len(f.problems) > 0 {
		return f.refused(stderr)
	}
	// The stale window plays no part in a check, so it is not asked for: rule 8 is that
	// every duration a verb DECIDES BY comes from a flag, not that a verb takes flags it
	// does not use.
	b, ok := read(backend, source, now, time.Duration(0), stderr)
	if !ok {
		return 2
	}

	scanned := "OPEN"
	if all {
		scanned = "ALL"
	}
	var hits []string
	matched, cards := 0, 0
	perWord := make([]int, len(query))
	for _, card := range b.Cards {
		if !all && !card.Open() {
			continue
		}
		cards++
		text := strings.ToLower(card.Text)
		every := true
		for i, w := range query {
			if strings.Contains(text, w) {
				perWord[i]++
			} else {
				every = false
			}
		}
		if !every {
			continue
		}
		matched++
		hits = append(hits, fmt.Sprintf("CHECK HIT id=%s state=%s owner=%s: %s",
			oneline.Field(card.ID), oneline.Field(card.State), oneline.Field(dash(card.Owner)),
			oneline.Escape(card.Text)))
	}
	capped(stdout, max, "card", hits, "--max 0 shows all")

	// matched= is never capped: the cap is a promise about the OUTPUT and the count is the
	// truth about the BOARD.
	fmt.Fprintf(stdout, "CHECK OK matched=%d cards=%d scanned=%s words=%d\n",
		matched, cards, oneline.Field(scanned), len(query))
	for i, w := range query {
		if cards > 0 && perWord[i]*2 > cards {
			fmt.Fprintf(stdout, "BOARD NOTE the word %s is in %d of the %d cards scanned: a word that common is a filer's mistake rather than a match, and narrowing --words is what sharpens it\n",
				oneline.Field(w), perWord[i], cards)
		}
	}
	// F1: matched=0 over a long phrase is almost always the AND, not an empty board. The
	// remedy line is for the query that CANNOT match rather than for every miss: three
	// words is where a phrase stops being something a filer holds in their head.
	if matched == 0 && len(query) >= 3 {
		fmt.Fprintf(stdout, "BOARD NOTE matched=0 over %d words: a card matches only when EVERY word appears in its text, so a long phrase is the NARROWEST check there is; try two or three rare words, as in %s\n",
			len(query), oneline.Field(rarest(query, perWord)))
	}
	// F2: A MATCH CARRIED ONLY BY COMMON WORDS IS SAID IN WORDS, NOT IN AN EXIT CODE. Every
	// word of this query is in more than half the cards scanned, so the hits say something
	// about the board's prose and nothing about this filer's finding — worth a line, and
	// not worth a second exit code. This once returned 2, which contradicted the tool's
	// most important sentence: a check that MATCHED exits 1, always. Exit 2 is
	// could-not-run — a missing or malformed flag, no backend or two, an unreadable board —
	// and a run that printed hits and counts plainly ran. The verdict is a FACT (a card on
	// this board contains all your words); what it is worth is the filer's judgment, and
	// this note is what that judgment needs.
	if cards > 1 && matched > 0 && allCommon(query, perWord, cards) {
		fmt.Fprintf(stdout, "BOARD NOTE every word of --words is in more than half the %d cards scanned, so these %d hits are about the board's prose rather than about your finding; narrow --words to two or three RARE words and run it again before you believe this NO\n",
			cards, matched)
	}
	if matched > 0 {
		return 1
	}
	return 0
}

// allCommon reports whether every word of the query is in more than half the cards
// scanned. A match needs all of them, so when all of them are that common the match
// survives only through words nobody chose for their meaning. A board of ONE card is
// exempt: one card cannot tell a common word from a rare one, and a rule that fired there
// would turn the tool's most important sentence — check exits 1 when it matches — into a
// could-not-run on the smallest board there is.
func allCommon(query []string, perWord []int, cards int) bool {
	if cards == 0 || len(query) == 0 {
		return false
	}
	for i := range query {
		if perWord[i]*2 <= cards {
			return false
		}
	}
	return true
}

// rarest names the word of this query that appeared in the fewest cards, which is the one
// worth keeping when the phrase is cut down.
func rarest(query []string, perWord []int) string {
	best := 0
	for i := range query {
		if perWord[i] < perWord[best] {
			best = i
		}
	}
	return query[best]
}
