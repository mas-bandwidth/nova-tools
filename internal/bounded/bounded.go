/*
Package bounded is the one shape every listing in this repo prints, and the reason it
exists is an incident: a line running on a 260K-context model died reading a single
674-line return from a verb that lists its state. Nothing was wrong with the state and
nothing was wrong with the reader. The tool printed all of it, every time, because no
verb here had ever been given a ceiling.

SPEC.md promises one machine-scannable line per event. It does not promise that the
number of events is small, and at a large state it is not: 10,000 unresolved wikilinks,
800 broken links, 1,200 self-talk claims, 300 quarantines. A listing whose length is the
state's length is not a report, it is the state, copied -- and a reader who has to hold
all of it to learn "there are 10,000" learned one number for 197,000 tokens.

So every listing here is a cap and a count:

	at most Max item lines, in the order the tool produced them
	then ONE line naming what was not shown and how to see it:

	    <TOKEN> MORE kind=<kind> shown=<n> total=<t> <remedy>

The remedy is never advice. It is the flag that widens the cap or the file that holds the
whole list, written so it can be typed. A cap with no remedy is censorship; a cap with one
is an index.

Two rules the caps do not cover, and the callers keep:

	THE COUNT LINE PRINTS ON FAILURE TOO. Every verb in this repo used to print its
	summary -- files=, links=, gating= -- only when it passed, so a failing run gave N
	lines and never N. Shown and Total are exported for exactly that line.

	MAX 0 MEANS ALL. A cap a caller cannot turn off is a tool deciding what its user may
	see. Every --*-max flag here takes 0, and 0 prints everything.

Grouped is the same shape per kind, for a verb that runs several checks into one stream:
20 findings of one kind must not bury the single finding of another, which is what a
flat cap over a concatenated list does.

Nothing here is a substitute for the escape. Every item line is rendered through
oneline.Escape on its way out -- which is a no-op over text the caller already escaped,
because Escape does not escape a backslash -- so a listing cannot break its own line
count, whatever the state holds.
*/
package bounded

import (
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Default is the ceiling every --*-max flag in this repo starts at. Twenty is a screen:
// enough to see the shape of what failed and to recognise a pattern, few enough that a
// reader who wanted the number rather than the list has not paid for the list. It is a
// default and not a law -- every flag that carries it can be widened, and 0 lifts it.
const Default = 20

// List prints at most max item lines and then one line standing for the rest.
//
// The zero value is not usable; build one with Capped. A nil *List is, deliberately, not
// usable either: a listing that silently printed nothing would be the same silence this
// package exists to end.
type List struct {
	w      io.Writer
	max    int // 0 means no ceiling
	token  string
	kind   string
	remedy string
	shown  int
	total  int
	err    error
}

// Capped returns a List that writes to w, prints at most max lines, and stands the rest
// behind one MORE line.
//
// token is the event token the verb already prints (VERIFY, LINKS, SCAN), so the MORE
// line sorts and greps with the lines it caps. kind names what is being counted, and is
// what makes a per-kind cap readable. remedy is the flag or the file that shows the rest,
// written the way it would be typed. max <= 0 prints everything: a caller who asked for
// all of it gets all of it, and gets no MORE line, because there is no more.
func Capped(w io.Writer, max int, token, kind, remedy string) *List {
	return &List{w: w, max: max, token: token, kind: kind, remedy: remedy}
}

// Line offers one already-rendered item line to the listing. It is counted always and
// printed only while the ceiling allows, so Total is the truth about the state even when
// the output is not.
//
// The line arrives WITHOUT its newline; one trailing newline is tolerated and removed
// rather than escaped into a visible \x0a, because a caller converting an existing
// fmt.Fprintf site is copying a format string that ends in one, and a tool that punished
// that with mangled output would be teaching its authors to be careful instead of being
// robust.
func (l *List) Line(line string) {
	l.total++
	if l.max > 0 && l.shown >= l.max {
		return
	}
	// SHOWN MEANS IT REACHED THE STREAM. A writer that failed -- a closed pipe,
	// a reader that has gone, a full disk -- has not shown anything, and a
	// caller whose delivery record follows Shown() would mark a line delivered
	// that nobody ever read. So the error is not discarded: the line is counted
	// in Total, which is the truth about the state, and not in Shown, which is
	// the truth about the output.
	if _, err := fmt.Fprintf(l.w, "%s\n", oneline.Escape(strings.TrimSuffix(line, "\n"))); err != nil {
		l.err = err
		return
	}
	l.shown++
}

// Err is the first write error this listing hit, or nil. A caller that records
// what it has delivered asks this before it writes that record down.
func (l *List) Err() error { return l.err }

// More prints the one line that stands for everything Line counted and did not print,
// and prints nothing at all when nothing was elided -- a MORE line saying total equals
// shown is a line that carries no information, and this package is about not printing
// those.
func (l *List) More() {
	if l.total <= l.shown {
		return
	}
	fmt.Fprintf(l.w, "%s MORE kind=%s shown=%d total=%d %s\n",
		oneline.Field(l.token), oneline.Field(l.kind), l.shown, l.total, oneline.Escape(l.remedy))
}

// Shown is how many item lines reached the stream.
func (l *List) Shown() int { return l.shown }

// Total is how many there were. This is the number a summary line must carry, and the
// reason Line counts past the ceiling instead of stopping at it.
func (l *List) Total() int { return l.total }

// Elided is Total minus Shown: what the reader would have to widen the cap to see.
func (l *List) Elided() int { return l.total - l.shown }

// Group is one cap per kind over a single stream, for a verb that runs several checks and
// prints their findings together.
//
// A flat cap over a concatenated list is the wrong shape there: 10,000 wikilink findings
// ahead of one frontmatter finding means the frontmatter finding is never printed, and
// the one line that would have told the reader something they did not already know is the
// line the cap ate. Each kind gets its own ceiling and its own MORE line, and the kinds
// print in the order they were first seen, so the output is deterministic without being
// alphabetised into a shape the verb did not choose.
type Group struct {
	w      io.Writer
	max    int
	token  string
	remedy string
	order  []string
	lists  map[string]*List
}

// Grouped returns a Group whose every kind is capped at max. See Capped for the arguments.
func Grouped(w io.Writer, max int, token, remedy string) *Group {
	return &Group{w: w, max: max, token: token, remedy: remedy, lists: map[string]*List{}}
}

// Line offers one item line under its kind, opening that kind's list the first time it
// is seen.
func (g *Group) Line(kind, line string) {
	l, ok := g.lists[kind]
	if !ok {
		l = Capped(g.w, g.max, g.token, kind, g.remedy)
		g.lists[kind] = l
		g.order = append(g.order, kind)
	}
	l.Line(line)
}

// More prints one MORE line per kind that elided anything, in the order the kinds were
// first seen.
func (g *Group) More() {
	for _, kind := range g.order {
		g.lists[kind].More()
	}
}

// Shown is how many item lines reached the stream across every kind.
func (g *Group) Shown() int {
	n := 0
	for _, l := range g.lists {
		n += l.shown
	}
	return n
}

// Total is how many there were across every kind.
func (g *Group) Total() int {
	n := 0
	for _, l := range g.lists {
		n += l.total
	}
	return n
}

// Elided is Total minus Shown across every kind.
func (g *Group) Elided() int { return g.Total() - g.Shown() }

// List is one kind's list, or nil when this group has not seen that kind. It is
// here for a verb whose MORE line carries a field this package does not print
// -- nova-wake's carries n=<elided>, which its spec requires -- so that such a
// verb can reuse the per-kind capping and still write its own summary, rather
// than hand-rolling a second map of lists beside this one.
func (g *Group) List(kind string) *List { return g.lists[kind] }

// Kinds returns the kinds seen, in first-seen order.
func (g *Group) Kinds() []string { return append([]string(nil), g.order...) }
