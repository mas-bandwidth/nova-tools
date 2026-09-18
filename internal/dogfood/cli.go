// Package dogfood answers one question about a tool that its own tests
// cannot: has somebody who did not write it actually run it?
//
// Glenn, 2026-09-18: a tool is not finished until it is tested, dogfooded by a
// non-author on real work with the edges filed, the feedback applied,
// documented and released. Nothing tracked that. This package holds the three
// mechanical halves of the tracking: the list of verbs the command reference
// declares (cli.go), the receipts a dogfooder writes (receipt.go), and the
// ledger and the gate that read one against the other (ledger.go). Authorship,
// which decides whether a receipt counts, is authors.go.
//
// Nothing here reaches the network, and every path is given by the caller.
package dogfood

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Verb is one verb the command reference declares: the tool that owns it, the
// verb itself, and the line of the reference that declared it first.
type Verb struct {
	Tool string
	Verb string
	Line int
}

// Key is the ledger's identity for a verb: what a receipt has to name to be
// about this verb. Tool and verb, one space between them.
func (v Verb) Key() string { return v.Tool + " " + v.Verb }

// ParseCLI reads a command reference (docs/CLI.md) and returns every verb it
// declares, in the order the document declares them, deduplicated on tool+verb.
//
// The rule is mechanical, because a rule a reader cannot apply by hand is a
// rule nobody can check the tool against:
//
//   - only lines inside a fenced code block count (prose that names a verb is
//     talking about it, not declaring it);
//   - the line must START with a tool name, `nova-<something>`; a transcript
//     line ("$ nova-check links ...") is an example of a verb declared
//     elsewhere, and counting it would let one worked example invent a verb;
//   - the verb is the leading lowercase bare words after the tool name, at most
//     two, so `nova-fuse lift quarantine` and `nova-fuse lift lockdown` are the
//     two different verbs they are, while `nova-check links --dir <dir>` stops
//     at the first flag;
//   - a line whose next token is a flag, a placeholder or a bracket declares no
//     verb (`nova-decide --questions <json file>`) and is skipped.
//
// The tool comes from the line and not from the section heading: SPEC-shaped
// documents show one tool's verb inside another tool's section, and the verb
// belongs to the tool that runs it.
func ParseCLI(path string) ([]Verb, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cli: %w", err)
	}
	defer f.Close()

	var (
		verbs  []Verb
		seen   = map[string]bool{}
		fenced bool
		lineNo int
	)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if !fenced {
			continue
		}
		tool, verb, ok := declaredVerb(line)
		if !ok {
			continue
		}
		key := tool + " " + verb
		if seen[key] {
			continue
		}
		seen[key] = true
		verbs = append(verbs, Verb{Tool: tool, Verb: verb, Line: lineNo})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("cli: %w", err)
	}
	if len(verbs) == 0 {
		return nil, fmt.Errorf("cli: %s declares no verbs; a ledger over no verbs would say OK about nothing", path)
	}
	return verbs, nil
}

// declaredVerb applies the rule ParseCLI documents to one line.
func declaredVerb(line string) (tool, verb string, ok bool) {
	if line == "" || line[0] == ' ' || line[0] == '\t' {
		return "", "", false
	}
	fields := strings.Fields(line)
	if len(fields) < 2 || !isToolName(fields[0]) {
		return "", "", false
	}
	var words []string
	for _, f := range fields[1:] {
		if len(words) == 2 || !isBareWord(f) {
			break
		}
		words = append(words, f)
	}
	if len(words) == 0 {
		return "", "", false
	}
	return fields[0], strings.Join(words, " "), true
}

// isToolName reports whether a token is a tool this family ships: `nova-` and
// then lowercase words. The prefix is what keeps an indented shell line in a
// transcript from declaring a verb of `cp`.
func isToolName(s string) bool {
	if !strings.HasPrefix(s, "nova-") || len(s) <= len("nova-") {
		return false
	}
	return isBareWord(s)
}

// isBareWord reports whether a token is a lowercase word: a verb, never a flag
// (`--dir`), a placeholder (`<dir>`), an option group (`(--issue ...`), an
// optional (`[--max <n>]`) or a word of the description that follows a
// synopsis (`REFUSED forever, by design` — uppercase, and so not a verb).
func isBareWord(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9', r == '-':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// Keys returns the verb keys, sorted: useful to a caller that wants the set
// rather than the document's order.
func Keys(verbs []Verb) []string {
	keys := make([]string, 0, len(verbs))
	for _, v := range verbs {
		keys = append(keys, v.Key())
	}
	sort.Strings(keys)
	return keys
}
