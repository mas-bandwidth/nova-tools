package onboarding

import (
	"fmt"
	"regexp"
	"strings"
)

// The opening of a banner answers the three questions a stranger brings to a
// tool before the usage lines do (docs/ONBOARDING.md point 6): what does it do,
// how does it work, and how do I use it. Line 1 is the first answer, one
// sentence; a paragraph labelled HowItWorksLabel near the top is the second;
// the `example:` block, three or more command lines run in order, is the third.
// The functions here read those three parts; the caller's test decides.

// HowItWorksLabel opens the paragraph that names a tool's nouns and where its
// state lives.
const HowItWorksLabel = "how it works:"

// HowItWorksWithin is how many lines from the top the paragraph must start in:
// it is read before the usage lines, never found under them.
const HowItWorksWithin = 15

// MinExampleCommands is the fewest command lines an `example:` block holds: a
// first run is a sitting, and one line is a single call rather than a sitting.
const MinExampleCommands = 3

// sentenceBreak is a sentence ending inside the line: a full stop, a semicolon,
// a question or an exclamation mark with more words after it.
var sentenceBreak = regexp.MustCompile(`[.;?!] `)

// OpeningSentence returns the sentence of a banner's first line, the part after
// `<tool>: `, or an error naming what the line lacks. The line is one sentence
// saying what the tool does: it names the tool, then says it in at least three
// words, with no second sentence, no usage line and no pointer to another
// document in place of the answer.
func OpeningSentence(banner, tool string) (string, error) {
	first, _, _ := strings.Cut(banner, "\n")
	first = strings.TrimRight(first, " \r")
	sentence, ok := strings.CutPrefix(first, tool+": ")
	if !ok {
		return "", fmt.Errorf("line 1 of `%s help` is %q; it must open with %q and say in one sentence what the tool does", tool, first, tool+": ")
	}
	sentence = strings.TrimSpace(sentence)
	var bad []string
	if len(strings.Fields(sentence)) < 3 {
		bad = append(bad, "it says it in fewer than three words")
	}
	if sentenceBreak.MatchString(sentence) || strings.HasSuffix(sentence, ".") {
		bad = append(bad, "it holds a sentence break (one sentence, no closing full stop)")
	}
	if strings.Contains(sentence, "docs/") || strings.Contains(sentence, "(see ") {
		bad = append(bad, "it points at a document; the line itself is the answer")
	}
	if strings.HasPrefix(sentence, "usage") || strings.Contains(sentence, " --") {
		bad = append(bad, "it is a usage line, not a sentence")
	}
	if len(bad) > 0 {
		return sentence, fmt.Errorf("line 1 of `%s help`, %q: %s", tool, first, strings.Join(bad, "; "))
	}
	return sentence, nil
}

// HowItWorksLine returns the 1-based line of the banner that opens the
// how-it-works paragraph, when one opens within the first HowItWorksWithin
// lines; 0 when none does.
func HowItWorksLine(banner string) int {
	for i, line := range strings.SplitN(banner, "\n", HowItWorksWithin+1) {
		if i == HowItWorksWithin {
			break
		}
		if strings.HasPrefix(line, HowItWorksLabel+" ") {
			return i + 1
		}
	}
	return 0
}

// ExampleCommands returns the command lines of a banner's first `example:`
// block that run the tool: the lines up to the first blank line under the
// heading, each with any leading NAME=value environment assignments removed,
// that then begin with the tool's name. A line continued with ` \` is one
// command, counted by its first line. A setup line (mkdir, cp) is part of the
// sitting and is not counted: it is not a use of the tool.
func ExampleCommands(banner, tool string) []string {
	_, tail, found := strings.Cut(banner, ExampleHeading)
	if !found {
		return nil
	}
	var out []string
	continued := false
	for _, line := range strings.Split(tail, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		was := continued
		continued = strings.HasSuffix(line, `\`)
		if was {
			continue
		}
		fields := strings.Fields(line)
		for len(fields) > 0 && isAssignment(fields[0]) {
			fields = fields[1:]
		}
		if len(fields) > 0 && fields[0] == tool {
			out = append(out, line)
		}
	}
	return out
}

// isAssignment reports whether a shell word is a NAME=value environment
// assignment: a name of letters, digits and underscores that does not start
// with a digit, then =.
func isAssignment(word string) bool {
	name, _, ok := strings.Cut(word, "=")
	if !ok || name == "" || (name[0] >= '0' && name[0] <= '9') {
		return false
	}
	for _, r := range name {
		if !(r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}
