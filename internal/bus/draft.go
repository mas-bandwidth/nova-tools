package bus

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// This file is the FIRST SEND: the skeleton a line who has never used this tool can ask
// for, and the tolerances that let the house style of a line who has never read SPEC.md
// through the door.
//
// It exists because of one real first send. A new line wrote a note the way it writes
// every note -- a markdown heading for the subject, then a Date line it had pasted by hand
// for years, and no From line, because on its own bus who was writing was obvious -- and
// this tool refused it on the Date line and said nothing about the other two. Three runs
// to find three refusals, and every one of them was a shape the tool could have read.
//
// THE RULE THESE TOLERANCES ARE HELD TO, and it is the same rule the address list is held
// to: a tolerance may only do what a person reading the draft would do WITHOUT GUESSING.
// A heading above a header is the subject; a Date line is a date the tool is going to
// write anyway; a bold key is a key with two asterisks on it; a blank line above a header
// is nothing at all. None of those is an inference about what the writer meant. A
// recipient the roster does not know, a header with no To line, a key nobody knows and a
// Re naming nothing are all guesses, and they stay refusals.
//
// AND EVERY TOLERANCE SAYS SO, on its own SEND NOTE line. A tool that silently rewrites
// what a person wrote is a tool that teaches nobody anything and that nobody can check;
// the notice is what makes the tolerance a tolerance rather than a guess.
//
// The tolerances are the SEND side only. Every reader on the bus -- inbox, check -- still
// refuses these shapes, because a file already on the bus is not a draft anybody is
// still editing, and a reader that quietly repaired one would be reporting a bus that
// does not exist.

// Problems is every reason one draft was refused.
//
// A refusal used to be the FIRST reason: a draft with a Date line, no From line and a
// misspelled recipient was three runs of the tool, and the person at the terminal did the
// tool's counting. So send collects, and prints one line per reason.
type Problems struct{ Reasons []error }

func (p *Problems) Error() string {
	parts := make([]string, 0, len(p.Reasons))
	for _, r := range p.Reasons {
		parts = append(parts, r.Error())
	}
	return strings.Join(parts, "; ")
}

// Unwrap gives errors.Is and errors.As every reason rather than the first.
func (p *Problems) Unwrap() []error { return p.Reasons }

// problemsOf is the whole list, or nil when nothing was wrong.
func problemsOf(reasons []error) error {
	if len(reasons) == 0 {
		return nil
	}
	return &Problems{Reasons: reasons}
}

// Reasons is every reason inside an error, whether it carries one or many, so a caller
// printing a refusal prints all of it without knowing which kind it holds.
func Reasons(err error) []error {
	var many *Problems
	if errors.As(err, &many) && len(many.Reasons) > 0 {
		return many.Reasons
	}
	return []error{err}
}

// ---------------------------------------------------------------- the skeleton

// PlaceholderSubject and PlaceholderBody are what `draft` writes where it has nothing to
// write. They are ANGLE-BRACKETED on purpose: a skeleton sent without being edited says
// so in its subject line and in its body, where a plausible-looking default would have
// been sent and read as a real note.
const (
	PlaceholderSubject = "<one line saying what this note is about>"
	PlaceholderBody    = "<the note goes here>"
)

// Skeleton is a draft's header before anybody has written the note: the verb `draft`
// resolves the names against the roster and this renders them.
//
// It writes no Date and no Id, because those are the tool's and are written at send. What
// it does write is the ORDER send writes, so that the file a person edits and the file
// that lands on the bus are the same shape.
type Skeleton struct {
	From string
	// To and Cc are the caller's OWN lines, resolved against the roster and then written
	// as they were written. A group is a name on this bus and stays the group's name, and
	// an instance qualifier stays on the name it qualifies: the header's rule everywhere
	// else is that the author's text is preserved, and a skeleton is the author's text.
	To      string
	Cc      string
	Re      []string
	Subject string
}

// Render is the skeleton as a file: the header, a blank line, one line of body.
func (s Skeleton) Render() string {
	var b strings.Builder
	b.WriteString(KeyFrom + ": " + s.From + "\n")
	b.WriteString(KeyTo + ": " + s.To + "\n")
	if strings.TrimSpace(s.Cc) != "" {
		b.WriteString(KeyCc + ": " + s.Cc + "\n")
	}
	for _, re := range s.Re {
		b.WriteString(KeyRe + ": " + re + "\n")
	}
	subject := s.Subject
	if strings.TrimSpace(subject) == "" {
		subject = PlaceholderSubject
	}
	b.WriteString(KeySubject + ": " + subject + "\n")
	b.WriteString("\n")
	b.WriteString(PlaceholderBody + "\n")
	return b.String()
}

// OneLine refuses a value that would not survive being written on a header line: a
// newline in a --subject would forge a header line under it, and a line separator would
// break the file for every reader that follows Unicode rather than counting newlines.
// The check is here rather than at the flag because it is a property of the header.
func OneLine(what, value string) error {
	for _, r := range value {
		if r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029 || (unicode.IsControl(r) && r != '\t') {
			return fmt.Errorf("%s: a header line is one line, and %q holds a line break or a control character", what, truncate(value, maxQuotedKey))
		}
	}
	return nil
}

// ---------------------------------------------------------------- the tolerances

// tolerated is a draft after the send-side tolerances have run: the lines to parse, where
// each of them came from in the file the person wrote, and what was done to it.
type tolerated struct {
	lines    []string
	at       []int
	notices  []string
	problems []error
}

// tolerate is the pre-pass send runs over a draft, and NOTHING ELSE runs it. Each
// tolerance is one shape a first send actually arrives in; each leaves a notice; and each
// is documented in SPEC.md under the same names.
//
//  1. BLANK LINES above the header, which is a file that begins with the blank line the
//     writer left under a title they deleted. Skipped.
//  2. A MARKDOWN HEADING as the first line. It becomes the Subject when the draft has no
//     Subject line, and either way it is not in the body. (The reader's own tolerance
//     already steps over it; what it could not do is keep it, and a heading that IS the
//     subject was being thrown away.)
//  3. A DATE line. The tool writes the date from the clock at send; the author's line is
//     dropped and the notice says so. This used to be a refusal on the grounds of not
//     quietly replacing the author's line -- and the notice is what makes it not quiet.
//  4. NO FROM LINE, with --as to say who is sending. The tool writes the From line, in
//     the spelling the roster holds. A From line naming somebody ELSE is a refusal: one
//     line does not send another's note, and that is not a shape to guess at.
//  5. A KEY IN MARKDOWN BOLD -- `**Subject**:`. The asterisks come off. A key that is
//     still unknown once they are off is still a refusal, with the list of the eight.
func tolerate(c *Config, text, as string) tolerated {
	var out tolerated
	lines := SplitDraft(text)

	i := 0
	blanks := 0
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
		blanks++
	}
	// A file of nothing but blank lines has no header and no body, and saying "2 blank
	// lines were skipped" about it would be the only thing this tool said.
	if blanks == 1 && i < len(lines) {
		out.notices = append(out.notices, "a blank line stood above the header; it is skipped, and the header is read from the first Key: value line")
	} else if blanks > 1 && i < len(lines) {
		out.notices = append(out.notices, fmt.Sprintf("%d blank lines stood above the header; they are skipped, and the header is read from the first Key: value line", blanks))
	}

	heading, hadHeading, headingAt := "", false, 0
	if i < len(lines) && strings.HasPrefix(lines[i], headingPrefix) {
		heading, hadHeading, headingAt = strings.TrimSpace(strings.TrimPrefix(lines[i], headingPrefix)), true, i+1
		i++
		for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
			i++
		}
	}

	from, hasFrom, hasSubject := "", false, false
	header, at := []string{}, []int{}
	keep := func(line string, n int) {
		header = append(header, line)
		at = append(at, n)
	}
	j := i
	for ; j < len(lines); j++ {
		line := lines[j]
		if strings.TrimSpace(line) == "" {
			break
		}
		bullet, rest := "", line
		if cut, ok := strings.CutPrefix(line, bulletPrefix); ok {
			bullet, rest = bulletPrefix, cut
		}
		key, value, ok := strings.Cut(rest, ":")
		if !ok || key == "" || strings.TrimSpace(key) != key || isProseKey(key) {
			keep(line, j+1) // the parser refuses it, and says which mistake it is
			continue
		}
		// Tolerance 5.
		if plain, bold := unbold(key); bold && !isProseKey(plain) {
			out.notices = append(out.notices, fmt.Sprintf("line %d: the key %q was in markdown bold; headers are plain `Key: value`, so it is read as %q", j+1, truncate(key, maxQuotedKey), plain+":"))
			key, rest = plain, plain+":"+value
		}
		switch key {
		case KeyDate:
			// Tolerance 3.
			out.notices = append(out.notices, fmt.Sprintf("this draft carried a %s line (%q); send writes the date from the clock, so yours is replaced, and says so", KeyDate, truncate(strings.TrimSpace(value), maxQuotedKey)))
			continue
		case KeyFrom:
			from, hasFrom = strings.TrimSpace(value), true
		case KeySubject:
			hasSubject = strings.TrimSpace(value) != ""
		}
		keep(bullet+rest, j+1)
	}

	// Tolerance 4. The From line goes FIRST, where send writes it.
	if as != "" {
		me, known := c.Lookup(as)
		switch {
		case !known:
			out.problems = append(out.problems, fmt.Errorf("--as %q names no one on this bus (known: %s)", as, strings.Join(c.KnownNames(), "; ")))
		case !hasFrom:
			out.notices = append(out.notices, fmt.Sprintf("this draft had no %s line; --as says you are %q, so send wrote %q", KeyFrom, me.Name, KeyFrom+": "+me.Name))
			header = append([]string{KeyFrom + ": " + me.Name}, header...)
			at = append([]int{1}, at...)
		default:
			// A From line that names somebody else is not a shape to guess at in either
			// direction: neither whose note it is, nor which of the two the writer meant.
			if other, ok := c.ResolveOne(from); ok && other.Name != me.Name {
				out.problems = append(out.problems, fmt.Errorf("--as %q, but this draft's %s line says %q; send does not send one line's note as another", me.Name, KeyFrom, truncate(from, maxQuotedKey)))
			}
		}
	}

	// Tolerance 2, after the header is known, because what it does depends on whether
	// there is a Subject line in it.
	if hadHeading {
		switch {
		case heading == "":
			out.notices = append(out.notices, "the first line was an empty markdown heading; it is not in the note")
		case hasSubject:
			out.notices = append(out.notices, fmt.Sprintf("the first line was the markdown heading %q and this draft has its own %s line; the heading is not in the note", truncate(heading, maxQuotedKey), KeySubject))
		default:
			out.notices = append(out.notices, fmt.Sprintf("the first line was a markdown heading, so it is this note's %s (%q), and it is not in the body", KeySubject, truncate(heading, maxQuotedKey)))
			keep(KeySubject+": "+heading, headingAt)
		}
	}

	// The blank line that ends the header, numbered at the line it stood on in the file,
	// so that a draft with no header at all is refused at a line a person can open to.
	sep := len(lines)
	if j < len(lines) {
		sep = j + 1
	}
	out.lines = append(header, "")
	out.at = append(at, sep)
	if j < len(lines) {
		out.lines = append(out.lines, lines[j+1:]...)
		for n := j + 2; n <= len(lines); n++ {
			out.at = append(out.at, n)
		}
	}
	return out
}
