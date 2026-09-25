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
	"fmt"
	"os"
	"sort"
	"strings"
)

// Verb is one unit of dogfooding: the tool that owns it, the verb itself, and
// the line of the reference that declared it first. A tool with no verbs — one
// run as `nova-decide --questions <file>` — carries the empty verb, which is
// its bare invocation and prints as `verb=-`.
type Verb struct {
	Tool string
	Verb string
	Line int
}

// BareVerb is how the empty verb — a tool's bare invocation — is written on a
// line and on the command line, since a field is never empty and a shell
// argument never should be.
const BareVerb = "-"

// Key is the ledger's identity for a verb: what a receipt has to name to be
// about this verb.
func (v Verb) Key() string { return NormalizeKey(v.Tool, v.Verb) }

// Spelling is the verb as a row prints it and as `--verb` takes it.
func (v Verb) Spelling() string {
	if v.Verb == "" {
		return BareVerb
	}
	return v.Verb
}

// NormalizeKey builds the ledger key from a tool and a verb as somebody wrote
// them: runs of whitespace collapse, and the bare marker means the bare
// invocation, so `--verb -` and a reference line with no verb are the same
// unit.
func NormalizeKey(tool, verb string) string {
	verb = strings.Join(strings.Fields(verb), " ")
	if verb == BareVerb {
		verb = ""
	}
	tool = strings.TrimSpace(tool)
	if verb == "" {
		return tool
	}
	return tool + " " + verb
}

// ParseCLI reads a command reference (docs/CLI.md) and returns every unit it
// declares, in the order the document declares them, deduplicated on tool+verb.
//
// The rule is mechanical, because a rule a reader cannot apply by hand is a
// rule nobody can check the tool against. It reads the three shapes this
// reference actually uses, which is the whole lesson of the 2026-09-18 dogfood
// pass: `nova-sandbox` and `nova-work` contributed zero rows of 77, not because
// nobody had run them but because one is documented as prose with a worked
// transcript and the other by pasting its own indented help block. A tool can
// go un-dogfooded forever by being documented in a shape the extractor does not
// read, so the extractor reads every shape:
//
//   - **a command line inside a fenced block**, at any indentation, with or
//     without a `$ ` prompt and with or without leading `VAR=value` environment
//     prefixes. The first token is the tool, `nova-<something>`; the verb is
//     the leading lowercase bare words after it, at most two, so
//     `nova-fuse lift quarantine` and `nova-fuse lift lockdown` are the two
//     verbs they are while `nova-check links --dir <dir>` stops at the first
//     flag. A line whose next token is a flag, a placeholder or a bracket —
//     `nova-decide --questions <json file>` — declares the tool's BARE
//     invocation, which is a unit like any other: it is how that tool is run.
//   - **a `### <verb>` heading** under a `## nova-<tool>` section, which is how
//     a verb documented in prose declares itself. One word only, and the
//     heading must be that word alone or that word before a separator, so
//     `### cut` and `### serve: the process outside a session` are verbs and
//     `### native and batch` and `### The seven verbs` are prose.
//
// The tool comes from the line and not from the section heading: a reference
// shows one tool's verb inside another tool's section, and a verb belongs to
// the tool that runs it. Headings are the exception — a heading has no tool in
// it, so it takes the section's.
func ParseCLI(path string) ([]Verb, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cli: %w", err)
	}
	verbs := ParseReference(string(raw))
	if len(verbs) == 0 {
		return nil, fmt.Errorf("cli: %s declares no verbs; a ledger over no verbs would say OK about nothing", path)
	}
	return verbs, nil
}

// ParseReference is ParseCLI over text already in hand: a markdown reference,
// where only fenced blocks and `###` headings declare.
func ParseReference(text string) []Verb { return parseLines(text, false) }

// ParseHelp is the same rules over a binary's own `help` output, which is a
// usage block and nothing else: there are no fences to be inside of, so every
// line is read as a command line.
func ParseHelp(text string) []Verb { return parseLines(text, true) }

func parseLines(text string, fenced bool) []Verb {
	var (
		verbs   []Verb
		seen    = map[string]bool{}
		section string
	)
	for i, line := range strings.Split(text, "\n") {
		lineNo := i + 1
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			fenced = !fenced
			continue
		}
		if !fenced {
			if tool, ok := toolSection(line); ok {
				section = tool
				continue
			}
			if verb, ok := headingVerb(line); ok && section != "" {
				add(&verbs, seen, Verb{Tool: section, Verb: verb, Line: lineNo})
			}
			continue
		}
		tool, verb, ok := declaredVerb(line)
		if !ok {
			continue
		}
		add(&verbs, seen, Verb{Tool: tool, Verb: verb, Line: lineNo})
	}
	return verbs
}

func add(verbs *[]Verb, seen map[string]bool, v Verb) {
	key := v.Key()
	if seen[key] {
		return
	}
	seen[key] = true
	*verbs = append(*verbs, v)
}

// toolSection reads a `## nova-<tool>` heading, which is what a `### verb`
// heading under it belongs to.
func toolSection(line string) (string, bool) {
	rest, ok := strings.CutPrefix(line, "## ")
	if !ok {
		return "", false
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 || !isToolName(fields[0]) {
		return "", false
	}
	return fields[0], true
}

// headingVerb reads a `### <verb>` heading. One word, and the heading is that
// word alone or that word before a separator: a heading that continues in
// words is a sentence about the tool, not a verb of it.
func headingVerb(line string) (string, bool) {
	rest, ok := strings.CutPrefix(line, "### ")
	if !ok {
		return "", false
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return "", false
	}
	word := fields[0]
	// A separator may be glued to the word (`serve:`), which is still the
	// heading naming one verb.
	word = strings.TrimRight(word, ":—,")
	if !isBareWord(word) {
		return "", false
	}
	if len(fields) == 1 {
		return word, true
	}
	if word != fields[0] { // the separator was glued on
		return word, true
	}
	switch fields[1] {
	case "—", "--", "-", "|", "(", ":":
		return word, true
	}
	return "", false
}

// declaredVerb applies the command-line rule ParseCLI documents to one line.
// The second return is the verb, empty for a tool's bare invocation; ok is
// false when the line declares nothing at all.
func declaredVerb(line string) (tool, verb string, ok bool) {
	// A pasted help block puts the description in a second column, and the gap
	// is what separates them: `nova-work ask            delivers ONE unit to
	// the FRIEND who owns it` is the verb `ask`, while `nova-work plan check
	// --file <path>` is the verb `plan check`. Two spaces or more end the
	// command; one space never does.
	fields := strings.Fields(command(strings.TrimSpace(line)))
	// A prompt and any environment prefixes come off: a transcript line is a
	// declaration in every reference that documents a tool by worked example,
	// and refusing to read one is how nova-sandbox reached zero rows.
	if len(fields) > 0 && fields[0] == "$" {
		fields = fields[1:]
	}
	for len(fields) > 0 && isEnvPrefix(fields[0]) {
		fields = fields[1:]
	}
	if len(fields) == 0 || !isToolName(fields[0]) {
		return "", "", false
	}
	var words []string
	for _, f := range fields[1:] {
		if len(words) == 2 || !isBareWord(f) {
			break
		}
		words = append(words, f)
	}
	// A second bare word is part of the verb only when the line stops after it
	// or continues with something that is not a word: `session start --session
	// <path>` is one verb in two words, while `version       print this build
	// identity` is one verb and the description a pasted help block puts beside
	// it. Three bare words in a row are prose, and the first of them is the verb.
	if len(words) == 2 && len(fields) > 3 && isBareWord(fields[3]) {
		words = words[:1]
	}
	return fields[0], strings.Join(words, " "), true
}

// command returns the part of a line before the first run of two or more
// spaces: the command a reader would type, without the column of description
// beside it.
func command(line string) string {
	for i := 0; i+1 < len(line); i++ {
		if line[i] == ' ' && line[i+1] == ' ' {
			return line[:i]
		}
		if line[i] == '\t' {
			return line[:i]
		}
	}
	return line
}

// isEnvPrefix reports whether a token is a `VAR=value` prefix a transcript
// puts before the command, as in `HOME=/…/home nova-sandbox probe …`.
func isEnvPrefix(s string) bool {
	name, _, found := strings.Cut(s, "=")
	if !found || name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
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
