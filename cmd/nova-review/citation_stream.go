package main

import (
	"path"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// streamingCitations recognizes Rule 2 citations without retaining a source
// line. With one scoped spec it accepts both bare and basename-prefixed
// forms; with multiple specs, only the explicit basename form is accepted.
type streamingCitations struct {
	specs   []streamCitationSpec
	bare    *citationSequence
	pending []byte // at most an incomplete UTF-8 rune
}

type streamCitationSpec struct {
	spec     scopedSpec
	needle   []rune
	failure  []int
	matched  int
	prefixed bool
	active   citationExpression
	seen     map[int]bool
	results  []specRule
}

func newStreamingCitations(specs []scopedSpec) *streamingCitations {
	s := &streamingCitations{specs: make([]streamCitationSpec, len(specs))}
	for i, spec := range specs {
		needle := []rune(strings.ToLower(path.Base(spec.Path) + " "))
		s.specs[i] = streamCitationSpec{spec: spec, needle: needle, failure: kmpFailure(needle)}
	}
	if len(specs) == 1 {
		e := newCitationSequence(specs[0].Rules)
		s.bare = &e
	}
	return s
}

func kmpFailure(needle []rune) []int {
	failure := make([]int, len(needle))
	for i, j := 1, 0; i < len(needle); i++ {
		for j > 0 && needle[i] != needle[j] {
			j = failure[j-1]
		}
		if needle[i] == needle[j] {
			j++
		}
		failure[i] = j
	}
	return failure
}

func (s *streamingCitations) Feed(data []byte) {
	for len(data) != 0 {
		if len(s.pending) != 0 {
			// Complete only the at-most-four-byte UTF-8 prefix left by the
			// preceding Write. Never prepend that prefix with append: a giant
			// incoming chunk must remain caller-owned, not become a line copy.
			s.pending = append(s.pending, data[0])
			data = data[1:]
			if !utf8.FullRune(s.pending) {
				continue
			}
			r, n := utf8.DecodeRune(s.pending)
			if n == len(s.pending) {
				s.pending = s.pending[:0]
			} else {
				copy(s.pending, s.pending[n:])
				s.pending = s.pending[:len(s.pending)-n]
			}
			s.feedRune(r)
			continue
		}
		if !utf8.FullRune(data) {
			// A UTF-8 rune is at most four bytes, so this preserves the bound
			// even when the source line is arbitrarily long.
			s.pending = append(s.pending, data...)
			return
		}
		r, n := utf8.DecodeRune(data)
		data = data[n:]
		s.feedRune(r)
	}
}

func (s *streamingCitations) feedRune(r rune) {
	r = unicode.ToLower(r)
	if s.bare != nil {
		s.bare.Feed(r)
	}
	for i := range s.specs {
		state := &s.specs[i]
		if state.prefixed {
			state.active.Feed(r)
		}
		for state.matched > 0 && r != state.needle[state.matched] {
			state.matched = state.failure[state.matched-1]
		}
		if len(state.needle) != 0 && r == state.needle[state.matched] {
			state.matched++
		}
		if state.matched == len(state.needle) {
			state.prefixed = true
			state.active = newCitationExpression(state.spec.Rules)
			state.matched = state.failure[state.matched-1]
		}
		if state.prefixed {
			if rules, done := state.active.Result(); done {
				state.appendRules(rules)
				state.active = citationExpression{done: true}
			}
		}
	}
}

func (s *streamCitationSpec) appendRules(numbers []int) {
	for _, n := range numbers {
		if s.seen == nil {
			s.seen = map[int]bool{}
		}
		if s.seen[n] {
			continue
		}
		s.seen[n] = true
		s.results = append(s.results, s.spec.Rules[n])
	}
}

func (s *streamingCitations) Finish() []specRule {
	if len(s.pending) != 0 {
		// Go's range/strings behavior treats an incomplete final sequence as a
		// RuneError. Feed the same replacement rune rather than dropping it.
		s.feedRune(utf8.RuneError)
		s.pending = nil
	}
	if s.bare != nil {
		s.bare.Finish()
	}
	for i := range s.specs {
		state := &s.specs[i]
		if state.prefixed {
			state.active.Finish()
			if rules, done := state.active.Result(); done {
				state.appendRules(rules)
			}
		}
	}
	var out []specRule
	seen := map[string]bool{}
	for i := range s.specs {
		state := &s.specs[i]
		results := state.results
		if len(s.specs) == 1 && s.bare != nil {
			// A sole spec permits both bare and basename-prefixed expressions.
			// The bare sequence scans every expression in source order; using it
			// here also prevents a prefix later on the line from hiding an
			// earlier bare citation.
			if rules, done := s.bare.Result(); done {
				results = make([]specRule, 0, len(rules))
				for _, n := range rules {
					results = append(results, state.spec.Rules[n])
				}
			} else {
				results = nil
			}
		}
		for _, rule := range results {
			key := patchRuleKey(rule)
			if !seen[key] {
				seen[key] = true
				out = append(out, rule)
			}
		}
	}
	return out
}

// citationSequence scans every independent Rule 2 expression on a bare
// line. Its result set is bounded by the caller-declared rules, not by the
// number of repetitions in source text.
type citationSequence struct {
	declared map[int]specRule
	all      bool
	active   citationExpression
	seen     map[int]bool
	result   []int
}

func newCitationSequence(declared map[int]specRule) citationSequence {
	return citationSequence{declared: declared, active: newCitationExpression(declared)}
}

func newCitationNumberSequence() citationSequence {
	return citationSequence{all: true, active: newCitationNumberExpression()}
}

func (s *citationSequence) Feed(r rune) {
	s.active.Feed(r)
	if rules, done := s.active.Result(); done {
		s.appendRules(rules)
		if s.all {
			s.active = newCitationNumberExpression()
		} else {
			s.active = newCitationExpression(s.declared)
		}
		// The terminating punctuation can also begin the next word. Replaying
		// it makes "rule 1; rule 2" two independent citations.
		s.active.Feed(r)
	}
}

func (s *citationSequence) Finish() {
	s.active.Finish()
	if rules, done := s.active.Result(); done {
		s.appendRules(rules)
	}
}

func (s *citationSequence) Result() ([]int, bool) { return s.result, true }

func (s *citationSequence) appendRules(numbers []int) {
	for _, n := range numbers {
		if s.seen == nil {
			s.seen = map[int]bool{}
		}
		if !s.seen[n] {
			s.seen[n] = true
			s.result = append(s.result, n)
		}
	}
}

const (
	citationSeekingWord = iota
	citationParsing
)

// citationExpression is one citedRuleNumbers invocation. It keeps finite word
// and token state plus distinct declared rule numbers, never the line itself.
type citationExpression struct {
	declared map[int]specRule
	all      bool
	mode     int
	done     bool
	word     boundedRuleWord
	kind     string
	rest     citationRest
	result   []int
}

func newCitationExpression(declared map[int]specRule) citationExpression {
	return citationExpression{declared: declared}
}

func newCitationNumberExpression() citationExpression { return citationExpression{all: true} }

func (e *citationExpression) Feed(r rune) {
	if e.done {
		return
	}
	if e.mode == citationSeekingWord {
		if unicode.IsSpace(r) {
			e.finishWord()
			return
		}
		e.word.Feed(r)
		return
	}
	e.rest.Feed(r)
	if done, result := e.rest.Result(e.kind); done {
		e.done, e.result = true, result
	}
}

func (e *citationExpression) finishWord() {
	if kind := e.word.Kind(); kind != "" {
		e.kind = kind
		e.mode = citationParsing
		e.rest = citationRest{declared: e.declared, collectAll: e.all, kind: kind}
	}
	e.word.Reset()
}

func (e *citationExpression) Finish() {
	if e.done {
		return
	}
	if e.mode == citationSeekingWord {
		e.finishWord()
	}
	if e.mode == citationParsing {
		e.rest.Finish()
		if done, result := e.rest.Result(e.kind); done {
			e.done, e.result = true, result
		}
	}
	if !e.done {
		e.done = true
	}
}

func (e *citationExpression) Result() ([]int, bool) { return e.result, e.done }

type boundedRuleWord struct {
	trimmed bool
	invalid bool
	text    []rune // at most len("rules")
}

func (w *boundedRuleWord) Feed(r rune) {
	if !w.trimmed && strings.ContainsRune("([{\"'", r) {
		return
	}
	w.trimmed = true
	if len(w.text) >= len("rules") {
		w.invalid = true
		return
	}
	w.text = append(w.text, r)
}
func (w *boundedRuleWord) Kind() string {
	if w.invalid {
		return ""
	}
	s := string(w.text)
	if s == "rule" || s == "rules" {
		return s
	}
	return ""
}
func (w *boundedRuleWord) Reset() { w.trimmed, w.invalid, w.text = false, false, w.text[:0] }

type citationRest struct {
	declared   map[int]specRule
	collectAll bool
	started    bool
	ended      bool
	finalized  bool
	following  []rune
	invalid    bool

	kind      string
	comma     bool
	andCount  int
	andBefore int
	numbers   int   // four means "four or more"; grammar never admits it.
	lastToken uint8 // 0 none, 1 number, 2 comma, 3 conjunction

	seenNumbers        map[int]bool
	matched            []int
	current            []rune
	currentSpaceBefore bool
	nextSpaceBefore    bool
	possessive         bool
}

const (
	citationNoToken uint8 = iota
	citationNumberToken
	citationCommaToken
	citationAndToken
)

func allowedCitationRune(r rune) bool {
	return (r >= '0' && r <= '9') || r == ',' || r == '\'' || r == 's' || r == 'a' || r == 'n' || r == 'd' || r == ' '
}

func (r *citationRest) Feed(ch rune) {
	if !r.started {
		if unicode.IsSpace(ch) {
			return
		}
		r.started = true
	}
	if r.ended {
		r.feedFollowing(ch)
		return
	}
	if unicode.IsSpace(ch) {
		r.finishCurrent(true, false)
		r.nextSpaceBefore = true
		return
	}
	if r.possessive {
		// A possessive may end before prose punctuation, but it cannot be
		// followed by another candidate token (for example, "1's 2").
		if allowedCitationRune(ch) {
			r.invalid = true
		}
		r.possessive = false
	}
	if !allowedCitationRune(ch) {
		r.finishCurrent(false, false)
		r.ended = true
		r.feedFollowing(ch)
		return
	}
	if ch == ',' {
		r.finishCurrent(false, false)
		if r.lastToken != citationNumberToken {
			r.invalid = true
		}
		r.comma = true
		r.lastToken = citationCommaToken
		r.nextSpaceBefore = false
		return
	}
	if len(r.current) == 0 {
		r.currentSpaceBefore = r.nextSpaceBefore
		r.nextSpaceBefore = false
	}
	if len(r.current) >= 32 {
		r.invalid = true
		return
	}
	r.current = append(r.current, ch)
}

func (r *citationRest) finishCurrent(spaceAfter, terminal bool) {
	if len(r.current) == 0 {
		return
	}
	token := r.current
	r.current = r.current[:0]
	if len(token) >= 2 && token[len(token)-2] == '\'' && token[len(token)-1] == 's' {
		if r.kind != "rule" || (!terminal && !spaceAfter) {
			r.invalid = true
		} else {
			r.possessive = true
		}
		token = token[:len(token)-2]
	} else if strings.ContainsRune(string(token), '\'') {
		r.invalid = true
	}
	if len(token) == 0 {
		r.invalid = true
		return
	}
	if string(token) == "and" {
		if !r.currentSpaceBefore || !spaceAfter || r.lastToken != citationNumberToken {
			r.invalid = true
		}
		r.andCount++
		r.andBefore = r.numbers
		r.lastToken = citationAndToken
		r.currentSpaceBefore = false
		return
	}
	n, err := strconv.Atoi(string(token))
	if err != nil || n <= 0 || (r.lastToken != citationNoToken && r.lastToken != citationCommaToken && r.lastToken != citationAndToken) {
		r.invalid = true
		r.currentSpaceBefore = false
		return
	}
	if r.numbers < 4 {
		r.numbers++
	}
	r.lastToken = citationNumberToken
	r.currentSpaceBefore = false
	_, declared := r.declared[n]
	if r.collectAll || declared {
		if r.seenNumbers == nil {
			r.seenNumbers = map[int]bool{}
		}
		if !r.seenNumbers[n] {
			r.seenNumbers[n] = true
			r.matched = append(r.matched, n)
		}
	}
}

func (r *citationRest) feedFollowing(ch rune) {
	r.following = append(r.following, ch)
	s := string(r.following)
	if strings.HasPrefix("through", s) || strings.HasPrefix("to ", s) {
		if s == "through" || s == "to " {
			r.invalid = true
			r.finalized = true
		}
		return
	}
	// The first non-matching rune proves neither reserved continuation exists.
	r.finalize()
}

func (r *citationRest) Finish() { r.finalize() }

func (r *citationRest) finalize() {
	if !r.ended {
		r.finishCurrent(false, true)
	}
	r.finalized = true
}

func (r *citationRest) Result(kind string) (bool, []int) {
	if !r.finalized {
		return false, nil
	}
	valid := !r.invalid && r.lastToken == citationNumberToken
	if kind == "rule" {
		valid = valid && r.numbers == 1 && !r.comma && r.andCount == 0
	} else if r.comma {
		// The listed comma forms contain at least two numbers. An "and" form
		// has exactly three, after the second number.
		valid = valid && r.numbers >= 2 && (r.andCount == 0 || (r.andCount == 1 && r.andBefore == 2 && r.numbers == 3))
	} else {
		valid = valid && r.andCount == 1 && r.andBefore == 1 && r.numbers == 2
	}
	if !valid {
		return true, nil
	}
	return true, append([]int(nil), r.matched...)
}
