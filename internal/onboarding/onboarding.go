// Package onboarding reads the two things docs/ONBOARDING.md makes every
// command in this repo carry — the `example:` block at the foot of its usage
// banner, and its `### First run` section in docs/TESTS.md — and reduces an
// output line to the part that document promises. It is the shared half of the
// tests that pin the standard, so that "the examples run" and "the transcript
// is what the tool prints" mean the same thing in every binary rather than five
// similar things.
//
// It holds no assertions of its own: it parses, and the caller's test decides.
// Nothing here reads a file, so the caller says where the bytes came from.
package onboarding

import (
	"fmt"
	"strings"
)

// ExampleHeading is the line that opens the runnable block at the foot of a
// usage banner. Everything under it, up to the first line that is not a
// command for this tool, is a command a first run can type.
const ExampleHeading = "\nexample:\n"

// FirstRunHeading is the subsection a stranger reads before anything else
// about a tool. It names a section in whichever document the caller supplies:
// docs/TESTS.md for the transcripts these tests execute.
const FirstRunHeading = "### First run"

// ExampleLines returns the command lines under a usage banner's `example:`
// heading — those beginning with the tool's own name, whitespace collapsed so
// a banner may align its flags. It returns an error rather than nothing when
// the block is missing, because a banner without one is the failure.
func ExampleLines(usage, tool string) ([]string, error) {
	_, tail, found := strings.Cut(usage, ExampleHeading)
	if !found {
		return nil, fmt.Errorf("the usage banner has no `example:` block; a bare %s must end in lines a first run can type", tool)
	}
	// The block ENDS at the first line that is not one of this tool's commands
	// -- the blank line under it, or the prose that follows. Scanning to the end
	// of the banner instead would sweep up any later sentence that happens to
	// begin with the tool's own name, and try to run it.
	var out []string
	for _, line := range strings.Split(tail, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, tool+" ") {
			break
		}
		out = append(out, strings.Join(strings.Fields(line), " "))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the `example:` block holds no %s command", tool)
	}
	return out, nil
}

// Section returns the body of a top-level `## <name>` section of a markdown
// document, up to the next top-level heading.
func Section(md, name string) (string, bool) {
	_, tail, found := strings.Cut("\n"+md, "\n## "+name+"\n")
	if !found {
		return "", false
	}
	body, _, cut := strings.Cut(tail, "\n## ")
	if !cut {
		body = tail
	}
	return body, true
}

// FirstRun returns the lines of the fenced transcript under a tool's
// `### First run`, which must live inside that tool's own `## <tool>` section.
func FirstRun(md, tool string) ([]string, error) {
	section, ok := Section(md, tool)
	if !ok {
		return nil, fmt.Errorf("the document has no `## %s` section", tool)
	}
	_, tail, found := strings.Cut(section, FirstRunHeading+"\n")
	if !found {
		return nil, fmt.Errorf("the document's `## %s` has no `%s` subsection; it is what a stranger reads before anything else here", tool, FirstRunHeading)
	}
	var lines []string
	fenced := false
	for _, line := range strings.Split(tail, "\n") {
		if strings.HasPrefix(line, "### ") && !fenced {
			break
		}
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("`%s` under `## %s` holds no fenced transcript", FirstRunHeading, tool)
	}
	return lines, nil
}

// Shape reduces an output line to the part a transcript promises: the
// two-token event prefix, then the field names in order. Everything after the
// ": " that closes the fields is a run's own business — scores, counts, paths,
// snippets — and is deliberately not compared, so that a transcript stays a
// document rather than becoming a fixture. A line that is not an event line
// (no upper-case first token) reduces to "".
func Shape(line string) string {
	head := line
	if i := strings.Index(line, ": "); i >= 0 {
		head = line[:i]
	}
	toks := strings.Fields(head)
	if len(toks) < 2 || strings.ToUpper(toks[0]) != toks[0] || strings.Trim(toks[0], "ABCDEFGHIJKLMNOPQRSTUVWXYZ-") != "" {
		return ""
	}
	out := []string{toks[0], toks[1]}
	for _, tok := range toks[2:] {
		if k, _, ok := strings.Cut(tok, "="); ok {
			out = append(out, k+"=")
		}
	}
	return strings.Join(out, " ")
}
