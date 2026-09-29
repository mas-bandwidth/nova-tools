// Package privacy screens outgoing text against the author's private material.
//
// A payload is compared with every entry marked private in the configured
// sources. A term counts as evidence only when it is rare twice over: rare
// among the entries of the sources, and rare across a background corpus of
// the author's ordinary writing, so that ordinary English does not raise an
// alarm. Three or more shared rare terms raise a flag, which is a reading
// assignment for a mind and never a verdict.
//
// The screen cannot see derivation that shares no vocabulary, so the best
// answer it gives is UNPROVEN-CLEAN, never clean. "I could not check" is
// always a different outcome from "I checked and found nothing".
//
// This file and judge.go and structure.go are pure: no filesystem, no clock,
// no environment. config.go parses the configuration text; corpus.go is the
// one file that reads the disk.
package privacy

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// DefaultMarker is how an entry declares itself private when the
// configuration names no other marker.
const DefaultMarker = "(private)"

// DefaultEntryTokens open an entry when a line starts with one of them
// followed by a single blank: "## " and "- ". A "### " sub-heading and a
// "---" rule open nothing.
var DefaultEntryTokens = []string{"##", "-"}

// MarkerScanRunes is how far into an entry's body the marker is looked for.
// A marker in the opening declares the entry private; one further down is
// prose about privacy. Counted in runes so non-ASCII text cannot move the
// window.
const MarkerScanRunes = 200

// FlagThreshold is how many shared distinctive terms raise a flag. Two is
// where unrelated prose brushes a private entry by accident.
const FlagThreshold = 3

// MinTermLen is the shortest distinctive term, in bytes. Terms are ASCII by
// construction, so bytes and runes agree.
const MinTermLen = 5

// The rarity bounds, in integer arithmetic so every threshold can be
// reproduced by hand. A term is rare in the private corpus when at most
// RarePercent of its entries hold it (never fewer than RareFloor), and rare in
// the background when at most BackgroundPercent of its documents hold it
// (never fewer than BackgroundFloor).
const (
	RarePercent       = 4
	RareFloor         = 2
	BackgroundPercent = 3
	BackgroundFloor   = 3
)

// baseStopWords are words every entry shares by construction or that carry no
// meaning on their own. The marker's own words are added by NewRules, and a
// configuration adds its own with `stop`.
var baseStopWords = strings.Fields(`the a an and or but if of to in on for with from by as is are was were be been being
this that these those it its not no nor than then so such at into over under about your you
my our their his her they them we us me one two three thing things make made makes new own
more most very just also only same other another each every all any some can could would
should will shall may might must do does did done get got go goes going day today work
works working write writes writing read reads reading time first last next here there what
when where which who whom how why because while after before again still even much many
private status idea ideas note file line entry`)

// termRE is a lowercase ASCII letter and at least three more word characters.
var termRE = regexp.MustCompile(`[a-z][a-z0-9'-]{3,}`)

// Pattern is one structure shape: a class name and the expression that
// matches it.
type Pattern struct {
	Class string
	RE    *regexp.Regexp
}

// Rules are the judgment's settings: the marker, the entry tokens, the stop
// words, and the structure patterns. Build them with NewRules or
// DefaultRules; the zero value is not usable.
type Rules struct {
	Marker      string
	EntryTokens []string
	Refuse      []Pattern
	Warn        []Pattern
	Allow       []string

	stop     map[string]bool
	trimSet  string
	allowSet map[string]bool
}

// DefaultRules are the marker, entry tokens and stop words with no structure
// patterns.
func DefaultRules() Rules {
	r, err := NewRules(DefaultMarker, DefaultEntryTokens, nil, nil, nil, nil)
	if err != nil {
		panic(err)
	}
	return r
}

// NewRules validates and assembles the rules. An empty marker or entry list
// takes the default; extra stop words are lowercased and added to the base
// list.
func NewRules(marker string, entryTokens, stop []string, refuse, warn []Pattern, allow []string) (Rules, error) {
	marker = strings.TrimSpace(marker)
	if marker == "" {
		marker = DefaultMarker
	}
	if len(entryTokens) == 0 {
		entryTokens = DefaultEntryTokens
	}
	trim := " "
	for _, tok := range entryTokens {
		if tok == "" || strings.ContainsAny(tok, " \t") {
			return Rules{}, fmt.Errorf("entry token %q must be one non-blank word, such as ## or -", tok)
		}
		for _, c := range tok {
			if !strings.ContainsRune(trim, c) {
				trim += string(c)
			}
		}
	}
	r := Rules{
		Marker:      marker,
		EntryTokens: append([]string(nil), entryTokens...),
		Refuse:      append([]Pattern(nil), refuse...),
		Warn:        append([]Pattern(nil), warn...),
		Allow:       append([]string(nil), allow...),
		stop:        map[string]bool{},
		trimSet:     trim,
		allowSet:    map[string]bool{},
	}
	for _, w := range baseStopWords {
		r.stop[w] = true
	}
	for _, w := range termRE.FindAllString(strings.ToLower(marker), -1) {
		r.stop[w] = true
	}
	for _, w := range stop {
		r.stop[strings.ToLower(w)] = true
	}
	for _, a := range allow {
		r.allowSet[strings.ToLower(a)] = true
	}
	return r, nil
}

// HasMarker reports whether s carries the marker, case-insensitively.
func (r Rules) HasMarker(s string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(r.Marker))
}

// Block is one entry of a private-material source. Source and Index are its
// identity; the title is only what a person recognises, and two entries may
// share one.
type Block struct {
	Source string
	Index  int
	Title  string
	Body   string
}

// Text is what gets measured: the title and the body, with a blank between
// so the two never fuse into one word.
func (b Block) Text() string { return b.Title + " " + b.Body }

// IsPrivate reports whether an entry declares itself private: the marker
// anywhere in its title, or within the first MarkerScanRunes of its body.
func (r Rules) IsPrivate(b Block) bool {
	return r.HasMarker(b.Title) || r.HasMarker(headRunes(b.Body, MarkerScanRunes))
}

func headRunes(s string, n int) string {
	i := 0
	for idx := range s {
		if i == n {
			return s[:idx]
		}
		i++
	}
	return s
}

// ParseBlocks splits a source into its entries, in file order. Lines before
// the first entry are a preamble and belong to no entry, since a preamble
// commonly explains the marker and would otherwise declare itself private.
func (r Rules) ParseBlocks(source, text string) []Block {
	var out []Block
	var body []string
	title, open := "", false
	flush := func() {
		if open {
			out = append(out, Block{Source: source, Index: len(out), Title: title, Body: strings.Join(body, "\n")})
		}
	}
	for _, ln := range strings.Split(text, "\n") {
		if r.opensEntry(ln) {
			flush()
			title = strings.TrimSpace(strings.TrimLeft(ln, r.trimSet))
			body, open = nil, true
			continue
		}
		if open {
			body = append(body, strings.TrimRight(ln, "\r"))
		}
	}
	flush()
	return out
}

func (r Rules) opensEntry(ln string) bool {
	for _, tok := range r.EntryTokens {
		if strings.HasPrefix(ln, tok+" ") {
			return true
		}
	}
	return false
}

// Terms is the set of words in text that could matter: lowercased, four
// characters or more, not stop words. A set, so repetition adds nothing.
func (r Rules) Terms(text string) map[string]bool {
	out := map[string]bool{}
	for _, w := range termRE.FindAllString(strings.ToLower(text), -1) {
		if !r.stop[w] {
			out[w] = true
		}
	}
	return out
}

// RareBound is the document-frequency ceiling within the private corpus.
func RareBound(blocks int) int {
	if blocks < 1 {
		blocks = 1
	}
	if b := blocks * RarePercent / 100; b > RareFloor {
		return b
	}
	return RareFloor
}

// BackgroundBound is the document-frequency ceiling across the background.
func BackgroundBound(docs int) int {
	if docs < 0 {
		docs = 0
	}
	if b := docs * BackgroundPercent / 100; b > BackgroundFloor {
		return b
	}
	return BackgroundFloor
}

// Bounds carries both ceilings and the number of background documents read.
// Docs of zero means there is no background model at all.
type Bounds struct {
	Rare       int
	Background int
	Docs       int
}

// DocFrequency counts, for every term, how many entries hold it. It counts
// all entries, private or not: rarity is a property of the whole corpus.
func (r Rules) DocFrequency(blocks []Block) map[string]int {
	df := map[string]int{}
	for _, b := range blocks {
		for w := range r.Terms(b.Text()) {
			df[w]++
		}
	}
	return df
}

// Distinctive is the set of terms in text that could raise a flag: rare in
// the private corpus, rare in the background, and at least MinTermLen long.
func (r Rules) Distinctive(text string, df, bg map[string]int, b Bounds) map[string]bool {
	out := map[string]bool{}
	for w := range r.Terms(text) {
		if df[w] <= b.Rare && bg[w] <= b.Background && len(w) >= MinTermLen {
			out[w] = true
		}
	}
	return out
}

// SharedRareTerms is the payload's overlap with a private entry's
// distinctive terms, sorted. Only the private side is filtered, so the
// overlap is distinctive by construction.
func SharedRareTerms(payloadTerms, privateDistinctive map[string]bool) []string {
	var out []string
	for w := range payloadTerms {
		if privateDistinctive[w] {
			out = append(out, w)
		}
	}
	sort.Strings(out)
	return out
}

// MayStaySilent reports whether a payload may pass one private entry with no
// flag: fewer than FlagThreshold shared terms.
func MayStaySilent(shared []string) bool { return len(shared) < FlagThreshold }

// CanEverFire reports whether a private entry has enough distinctive terms
// to raise a flag against any payload at all.
func CanEverFire(distinctive map[string]bool) bool {
	return len(distinctive) >= FlagThreshold
}
