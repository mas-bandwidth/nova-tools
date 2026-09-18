package pulse

// THE CARD'S FRONT MATTER GRAMMAR, in one place.
//
// A card declares four things a machine reads back out of the worker's `RESULT.md`: the
// VERDICT, the BRANCH the work was committed on, the REPO that branch belongs to, and the
// BASE it is measured and opened against. Until 2026-09-18 the writer and the reader were
// two different ideas in two different places -- the shipped `rows.md` template told a
// worker to "write RESULT.md with line 1 equal to this card's line 1" and nothing else,
// while `harvest --bench` refused that very RESULT.md four times over, once per field it
// did not find. A template that cannot produce what the harvester reads is a loop that
// cannot close, and the dogfood loop on hulk closed it by hand three times.
//
// So there is one grammar here, and both ends use it:
//
//   - `cut` renders CardFront.FrontMatter() into the card, so the card CARRIES the branch,
//     the repo and the base the harvester will look for.
//   - the template's last step tells the worker to copy those lines into RESULT.md, in the
//     words ResultInstruction() writes.
//   - `harvest --bench` reads them back with ResultField, ResultVerdict and MissingFields.
//
// A test cuts a card from the shipped template, writes the RESULT.md that card asks for,
// and harvests it: the round trip is the contract, and it is red the moment either end
// drifts.

import (
	"fmt"
	"strings"
)

// The RESULT.md field names. They are read as `NAME <value>` and as `NAME: <value>`: the
// bench scripts write the colon form and the templates the bare one, and a grammar that
// refused half its own writers would be a grammar nobody uses.
const (
	FieldBranch  = "BRANCH"
	FieldRepo    = "REPO"
	FieldBase    = "BASE"
	FieldSession = "SESSION"
)

// The verdicts a RESULT.md may carry. DONE is the only green one: SPEC-PULSE rule 6 puts
// the verdict on line 2 and names exactly these three, and anything else -- a harness that
// died, a card that never got there -- is not a card that finished.
const (
	VerdictDone    = "DONE"
	VerdictAbstain = "ABSTAIN"
	VerdictBlocked = "BLOCKED"
	// VerdictNone is what a RESULT.md with no verdict token at all reads as. It is red:
	// the worker wrote a file and never said how it went, which is the shape a harness
	// killed at its deadline leaves behind.
	VerdictNone = "none"
)

// redVerdicts are the words a RESULT.md may say instead of DONE. `RED` and `FAILED` are
// not in the spec's three but are what workers write when a test is red, and reading them
// as anything other than red is how a red card reached `done/`.
var redVerdicts = map[string]bool{
	VerdictAbstain: true,
	VerdictBlocked: true,
	"RED":          true,
	"FAILED":       true,
}

// JobRepoDir is the clone's directory INSIDE a job directory: `<job>/repo`. It is the one
// place in the fleet a card's commits live -- `nova-swarm`'s wall reader, the manager's
// push and `harvest --bench`'s fetch over `ssh://<bench><job>/repo` all read it there --
// and the shipped `rows.md` template cloned into `<job>` itself, so the harvester fetched
// a path that was never a repository. The constant is here so the template and the three
// readers cannot disagree again.
const JobRepoDir = "repo"

// CardFront is a card's front matter: what `cut` declares to the worker and what the
// worker copies back into RESULT.md. Every field is one token on one line.
type CardFront struct {
	Label   string
	Branch  string
	Repo    string // <owner>/<name>, never a URL
	Base    string
	Session string
}

// FrontMatter is the block a card carries and a RESULT.md carries back, in a fixed order.
// An empty field is left out rather than written blank: a `REPO` line with nothing after
// it reads as a repo named "" everywhere that parses it.
func (f CardFront) FrontMatter() string {
	var b strings.Builder
	for _, p := range []struct{ name, value string }{
		{FieldBranch, f.Branch},
		{FieldRepo, f.Repo},
		{FieldBase, f.Base},
		{FieldSession, f.Session},
	} {
		if strings.TrimSpace(p.value) == "" {
			continue
		}
		fmt.Fprintf(&b, "%s %s\n", p.name, p.value)
	}
	return b.String()
}

// ResultMD is the RESULT.md a worker that followed the card writes: line 1 the card's own
// contract line, line 2 the verdict, then the front matter. It is what the last step
// instructs in Go, so a test can be the worker without a model in it.
func (f CardFront) ResultMD(contract, verdict string) string {
	if strings.TrimSpace(verdict) == "" {
		verdict = VerdictDone
	}
	return strings.TrimRight(contract, "\n") + "\n" + verdict + "\n" + f.FrontMatter()
}

// ResultInstruction is the card's last step, in the words the template carries. It names
// every field by name, so a worker reading the card knows what the harvester will look
// for, and `cut` and `harvest` are reading the same sentence.
func ResultInstruction() string {
	return fmt.Sprintf(
		"Write RESULT.md beside ./%s, not inside it: line 1 exactly the line 1 of this card; line 2 one of %s, %s <why> or %s <why>; then copy this card's %s, %s and %s lines verbatim.",
		JobRepoDir, VerdictDone, VerdictAbstain, VerdictBlocked, FieldBranch, FieldRepo, FieldBase)
}

// ReadCardFront reads a rendered card's declared front matter. A card is the source of
// what the worker is asked to write, so the test that plays the worker reads it from here
// and never from a second copy of the grammar.
func ReadCardFront(text string) CardFront {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	return CardFront{
		Branch:  ResultField(lines, FieldBranch),
		Repo:    ResultField(lines, FieldRepo),
		Base:    ResultField(lines, FieldBase),
		Session: ResultField(lines, FieldSession),
	}
}

// ResultField reads one field line -- `NAME <value>` or `NAME: <value>` -- and answers the
// first one that names it, with a `github.com/` prefix taken off a repo. A line that only
// STARTS with the name (`BRANCHES are cheap`) is prose, not a field: the name must be
// followed by a colon or by space.
func ResultField(lines []string, name string) string {
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, name) {
			continue
		}
		rest := strings.TrimPrefix(t, name)
		rest = strings.TrimPrefix(rest, ":")
		if rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
			continue
		}
		v := strings.TrimSpace(rest)
		if v == "" {
			continue
		}
		if name == FieldRepo {
			v = strings.TrimPrefix(v, "github.com/")
		}
		return v
	}
	return ""
}

// MissingFields names every field of `want` the lines do not carry, in the order asked.
// It is what folds four refusals into one: a RESULT.md missing both its BRANCH and its
// REPO said so twice, on two runs, because each guard returned before the next one looked.
func MissingFields(lines []string, want ...string) []string {
	var out []string
	for _, name := range want {
		if ResultField(lines, name) == "" {
			out = append(out, name)
		}
	}
	return out
}

// ResultVerdict reads the verdict out of a RESULT.md and says whether it is red. Line 1 is
// the contract line and is never the verdict; every line after it is looked at, because a
// worker that wrote its BRANCH line before its verdict still said how it went. A DONE
// anywhere below line 1 is green; otherwise the first red word found is the verdict, and a
// RESULT.md with no verdict word at all is VerdictNone, which is red.
//
// This is the whole of rule (1) of 2026-09-18: `harvest --bench` took "has a RESULT.md"
// for "done", so an ABSTAIN drained its card to `done/` and released the lane as a
// success. A red result is never done.
func ResultVerdict(lines []string) (verdict string, red bool) {
	found := ""
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" || i == 0 {
			continue
		}
		word := t
		if i := strings.IndexAny(word, " \t:"); i > 0 {
			word = word[:i]
		}
		word = strings.ToUpper(word)
		if word == VerdictDone {
			return VerdictDone, false
		}
		if found == "" && redVerdicts[word] {
			found = word
		}
	}
	if found != "" {
		return found, true
	}
	return VerdictNone, true
}
