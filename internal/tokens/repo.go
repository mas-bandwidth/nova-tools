package tokens

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
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
type Rules struct{ rules []rule }

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
