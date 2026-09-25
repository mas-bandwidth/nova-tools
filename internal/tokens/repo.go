package tokens

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// Repo attribution: ONE rule, ONE function, ONE statement.
//
// The prototype had two copies of this rule in two scripts with two different regexp
// tables, and they disagreed about `serialize`, `rowan` and `freddy`. So the table is the
// caller's file — there is no built-in list, and `--repos` is required — and the ladder
// below is written once and called from every source reader.
//
// The two named buckets are the other half. `unknown` means no path in this transcript
// has ever named a repo; `other` means paths were seen and no rule matched them. They are
// different facts about a rules file, and every TOKENS DAY line prints each one's share,
// which is how a person sees whether their file is good enough.

// The bucket names, which are repo names like any other and are never invented per source.
const (
	Unknown      = "unknown"
	Other        = "other"
	Unattributed = "unattributed"
)

type rule struct {
	name string
	re   *regexp.Regexp
}

// Rules is a rules file: lines of <name><TAB><regexp>, in priority order.
//
// It optionally keeps the tally behind `sources --unattributed`: the path stems that were
// SEEN and matched no rule, which is the evidence a person needs to improve the file.
// `other=81%` on a day line says the rules are not good enough; it does not say which line
// to add, and until this tally existed nobody could find out without grepping transcripts
// by hand.
type Rules struct {
	rules []rule
	// watch is off unless a caller asked for the tally, so an ordinary fold pays nothing
	// for it: neither the map nor the second pass over the tokens.
	watch bool
	stems map[string]int
	total int
}

// stemLimit is the ceiling on distinct stems held while watching. A transcript tree can
// name hundreds of thousands of paths, and a tally that grew with the tree would be the
// same unbounded growth this repo's listings exist to end. Past the ceiling the counts
// already held keep rising and no NEW stem is admitted, so the top of the list -- which is
// what the flag is for -- is unaffected, and TotalUnattributed still counts every token.
//
// Measured 2026-09-18 on this bench's own transcripts: 2,256 files, 122,056 unattributed
// tokens, and over 4,096 distinct stems -- so the first ceiling tried was one a real bench
// reached on its first run. 50,000 stems is about 3 MB of strings and is a guard against a
// pathological tree rather than a limit on an ordinary one.
const stemLimit = 50000

// stemDepth is how many leading path elements a stem keeps. Three is where a tree is named
// on a bench -- /Users/glenn/deepseek-working-3, /home/nova/actions-runner -- and a tally
// keyed by the whole file path would be a list of FILES rather than a list of candidate
// rules: one unnamed tree would arrive as a hundred stems, one per directory in it, and the
// top of the list would say nothing.
//
// A path deeper than that folds onto its tree, which is the right grain for the first
// question ("what is `other` made of?") and not for the second ("which repo inside that
// tree?"). The second is one grep of that stem, and the flag's answer is what makes the
// grep possible at all.
const stemDepth = 3

// UnattributedStem is one path stem no rule named, with how many path tokens fell onto it.
type UnattributedStem struct {
	Stem  string
	Count int
}

// WatchUnattributed turns the tally on. It is called before any source is read, and never
// by fold: `sources --unattributed` is a verb that only looks.
func (r *Rules) WatchUnattributed() {
	r.watch = true
	r.stems = map[string]int{}
	r.total = 0
}

// Unattributed is the tally, heaviest first and then by stem, so two runs over one tree
// print the same list in the same order.
func (r *Rules) Unattributed() []UnattributedStem {
	out := make([]UnattributedStem, 0, len(r.stems))
	for stem, n := range r.stems {
		out = append(out, UnattributedStem{Stem: stem, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Stem < out[j].Stem
	})
	return out
}

// TotalUnattributed is every path token that fell to `other`, counted whether or not its
// stem got a place in the tally.
func (r *Rules) TotalUnattributed() int { return r.total }

// record tallies the tokens of one message that named no repo this file knows.
func (r *Rules) record(tokens []string) {
	for _, tok := range tokens {
		if tok == "" {
			continue
		}
		r.total++
		stem := PathStem(tok)
		if _, held := r.stems[stem]; !held && len(r.stems) >= stemLimit {
			continue
		}
		r.stems[stem]++
	}
}

// PathStem is the leading directory a token names, to stemDepth elements. It is what the
// unattributed tally counts, and it is written here rather than in the command so that the
// listing and the rule it suggests are cut from the same string.
func PathStem(tok string) string {
	if remoteLike(tok) {
		return tok
	}
	// A leading `/` is kept and is not an element; a leading `~` IS an element, because
	// `~/rowan-working/nova-tools` and `/Users/glenn/rowan-working` are the same depth of
	// answer -- the home directory has been named either way.
	lead, rest := "", tok
	if strings.HasPrefix(tok, "/") {
		lead, rest = "/", tok[1:]
	}
	parts := strings.Split(rest, "/")
	kept := make([]string, 0, stemDepth)
	for _, p := range parts {
		if p == "" {
			continue
		}
		kept = append(kept, p)
		if len(kept) == stemDepth {
			break
		}
	}
	return lead + strings.Join(kept, "/")
}

// LoadRules reads the caller's rules file. A malformed line is an error naming the line,
// because a rules file half-read is a repo column nobody can defend.
func LoadRules(path string) (*Rules, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var rs Rules
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		name, expr, ok := strings.Cut(line, "\t")
		if !ok || strings.TrimSpace(name) == "" || strings.TrimSpace(expr) == "" {
			return nil, fmt.Errorf("line %d: a rule is <name><TAB><regexp>, in priority order: %s", n, line)
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("line %d: %v", n, err)
		}
		rs.rules = append(rs.rules, rule{name: strings.TrimSpace(name), re: re})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return &rs, nil
}

// Attribute is THE statement of the rule, and every reader reaches a repo name through it:
//
//	take every path-like token in the message's tool-call inputs
//	for each token, in order: the first rule whose regexp matches names the repo; stop
//	a token that matches no rule counts as "seen"
//	no token matched a rule, at least one token seen  -> other
//	no token at all                                   -> the stream's previous repo
//	no previous repo                                  -> unknown
//
// prev is the stream's previous repo — per transcript file, per OpenCode session, and
// never across sources.
//
// The `other` arm is also where the unattributed tally is taken, when a caller asked for
// it: the tokens that reached it are exactly the ones a better rules file would name.
func (r *Rules) Attribute(tokens []string, prev string) string {
	seen := false
	for _, tok := range tokens {
		if tok == "" {
			continue
		}
		// THE THREE BUCKET NAMES PASS THROUGH. They are not repos and no rules file
		// names them, so a recorded name of `unattributed` arriving from a bus line
		// would otherwise become `other` -- and a provider total that crossed the bus
		// would land under a different repo from the same total folded straight from
		// the export. The bucket is a fact this tool wrote; it is not re-attributed.
		switch tok {
		case Unattributed, Unknown, Other:
			return tok
		}
		seen = true
		for _, rule := range r.rules {
			if rule.re.MatchString(tok) {
				return rule.name
			}
		}
	}
	if seen {
		if r.watch {
			r.record(tokens)
		}
		return Other
	}
	if prev != "" {
		return prev
	}
	return Unknown
}

// AttributeInputs is Attribute over the raw strings of a message's tool-call inputs, with
// the path-like tokens pulled out of them first. A reader that carried its own regexp
// would be the second copy of the rule, so the extraction is here too.
func (r *Rules) AttributeInputs(inputs []string, prev string) string {
	return r.Attribute(PathTokens(inputs), prev)
}

// PathTokens is every path-like token in the strings given: one starting with `/`, `~`, or
// a `<host>:<org>/<repo>` remote, and continuing over path characters.
func PathTokens(inputs []string) []string {
	var out []string
	for _, in := range inputs {
		for _, field := range strings.FieldsFunc(in, func(r rune) bool {
			return r == ' ' || r == '\t' || r == '\n' || r == '"' || r == '\'' || r == ',' || r == ';'
		}) {
			field = strings.Trim(field, "()[]{}<>")
			switch {
			case strings.HasPrefix(field, "/"), strings.HasPrefix(field, "~"):
				out = append(out, field)
			case remoteLike(field):
				out = append(out, field)
			}
		}
	}
	return out
}

// remoteLike matches <host>:<org>/<repo>, the other shape a repo is named in: a colon with
// something either side and a slash after it.
func remoteLike(s string) bool {
	host, rest, ok := strings.Cut(s, ":")
	if !ok || host == "" || !strings.Contains(rest, "/") {
		return false
	}
	return !strings.ContainsAny(host, "/\\")
}
