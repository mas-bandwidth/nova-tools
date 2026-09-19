package onboarding

import (
	"fmt"
	"regexp"
	"strings"
)

// CompareTranscript is the ONE comparison a firstrun_test.go may make
// (docs/SPEC-TOOLWORK.md §7 rule 2, draft 5).
//
// Before it there were three comparisons in this repository and they were worth
// three different things. A `printed map[string]bool` asked whether each
// documented line was somewhere in what the tool printed, so an abridged or a
// reordered block passed. Shape compared a line's event prefix and its field
// NAMES and dropped every value, so a changed count passed. Execute compared
// line for line, but with a norm list assembled at each call site, so what was
// not compared differed per tool and nobody could read the set. A reader of a
// green build could not tell which of the three a tool's transcript had earned.
//
// One comparator means one answer: same number of lines, same lines, same order,
// every value compared AS WRITTEN -- except the run-owned values named from the
// one shared table below, which a test may name and may not invent.
//
// Nothing here asserts or runs a process: it compares a parsed document with the
// results of a sitting the caller ran, and returns every disagreement. The
// caller's test decides what is fatal, as the rest of this package does.
func CompareTranscript(doc []Step, got []Result, volatile []Field) []Problem {
	norms, refusals := volatileNorms(volatile)
	if len(refusals) > 0 {
		// A refused declaration is not a comparison that failed: it is a
		// comparison that was never sound, so no line is compared under it.
		return refusals
	}
	if len(doc) == 0 {
		return []Problem{{Message: "the transcript holds no command, so this comparison would pass by comparing nothing"}}
	}
	if len(got) != len(doc) {
		return []Problem{{Message: fmt.Sprintf(
			"the document writes %d command(s) and the run produced %d result(s); every documented command is run, in order, in one sitting",
			len(doc), len(got))}}
	}
	var problems []Problem
	for i, s := range doc {
		problems = append(problems, Compare(s, got[i], norms)...)
	}
	return problems
}

// Field names one entry of the Volatile table for one transcript.
//
// Doc and Run are the two spellings of a value no pattern can match -- a
// directory this run made -- and are given for a table entry that asks for them
// and for no other. Everything else on the line is compared as written.
type Field struct {
	// Name is an entry's name in the Volatile table. A name the table does not
	// hold is refused.
	Name string
	// Doc is what the document writes; Run is what this run made.
	Doc, Run string
}

// VolatileField is one entry of the shared table: a value that belongs to the
// RUN rather than to the document, named so that what is not compared is a short
// list a reader can check rather than whatever a loose pattern swallowed.
type VolatileField struct {
	// Name is how a transcript's test names this value.
	Name string
	// What a reader of a failing test is told is not compared.
	What string
	// NeedsPath is true for the one kind of value a pattern must not guess at: a
	// path, whose two spellings the test supplies. A pattern broad enough to
	// match any path would swallow the documented paths a reader types.
	NeedsPath bool

	// norm builds the normalisation applied to both sides of the comparison.
	norm func(f Field) Norm
}

// Volatile is the ONE table of run-owned values. It is the whole set of things a
// transcript may leave uncompared, and it is shared so that "this transcript is
// green" means the same in every binary. Growing it is a reading, not a call
// site's decision -- which is what the refusal below is for.
//
// The five entries are the ones docs/SPEC-TOOLWORK.md §7 rule 2 names: `at=`,
// `took=`, `created=`, a temporary directory and a fresh sha. Each pattern
// matches the value WITH its field name, so an entry declared for one field
// cannot quietly swallow another's value.
var Volatile = []VolatileField{
	{
		Name: "at",
		What: "at= (the instant of this run)",
		norm: func(Field) Norm { return Instant("at") },
	},
	{
		Name: "took",
		What: "took= (how long this run took)",
		norm: func(Field) Norm {
			return Norm{
				Name: "took= (how long this run took)",
				Re:   regexp.MustCompile(`took=[0-9]+(\.[0-9]+)?(ns|µs|us|ms|s|m|h)`),
				As:   "took=<how long this run took>",
			}
		},
	},
	{
		Name: "created",
		What: "created= (the instant this run created the record)",
		norm: func(Field) Norm { return Instant("created") },
	},
	{
		Name:      "tmpdir",
		What:      "the directory this run made",
		NeedsPath: true,
		// Both sides are reduced to what the DOCUMENT writes, so a failure
		// message shows the path a reader typed rather than one of this run's.
		norm: func(f Field) Norm { return Path(f.Doc, f.Run) },
	},
	{
		Name: "sha",
		What: "sha= (a sha this run made)",
		norm: func(Field) Norm {
			return Norm{
				Name: "sha= (a sha this run made)",
				Re:   regexp.MustCompile(`sha=[0-9a-f]{7,40}`),
				As:   "sha=<a sha this run made>",
			}
		},
	},
}

// VolatileNames returns the table's names in the table's order, which is the
// sentence a refusal shows a reader who has to choose one.
func VolatileNames() []string {
	names := make([]string, 0, len(Volatile))
	for _, f := range Volatile {
		names = append(names, f.Name)
	}
	return names
}

// volatileEntry looks a declared name up in the table.
func volatileEntry(name string) (VolatileField, bool) {
	for _, f := range Volatile {
		if f.Name == name {
			return f, true
		}
	}
	return VolatileField{}, false
}

// volatileNorms turns a transcript's declarations into the norms Compare
// applies, in the order the transcript declares them, and REFUSES a declaration
// the table does not hold. A refusal is returned rather than ignored: a test
// that could invent a normalisation could turn any red green by widening one
// pattern, and a test whose declaration was silently dropped would be comparing
// something other than what it says it compares.
func volatileNorms(fields []Field) ([]Norm, []Problem) {
	var norms []Norm
	var refusals []Problem
	seen := map[string]bool{}
	for _, f := range fields {
		entry, ok := volatileEntry(f.Name)
		if !ok {
			refusals = append(refusals, Problem{Message: fmt.Sprintf(
				"%q is not in the onboarding.Volatile table, and a transcript may name a value from that table and may not invent one.\nThe table holds: %s.\nEverything else on a documented line is compared as written; a run-owned value the table does not hold is an addition to the table, and is read as one.",
				f.Name, strings.Join(VolatileNames(), ", "))})
			continue
		}
		if seen[f.Name] {
			refusals = append(refusals, Problem{Message: fmt.Sprintf(
				"the transcript names %q from the onboarding.Volatile table twice; one declaration is the whole of what is not compared for that value",
				f.Name)})
			continue
		}
		seen[f.Name] = true
		switch {
		case entry.NeedsPath && (f.Doc == "" || f.Run == ""):
			refusals = append(refusals, Problem{Message: fmt.Sprintf(
				"the onboarding.Volatile entry %q is %s, so it is named with BOTH spellings -- Doc, what the document writes, and Run, what this run made -- and this declaration gives Doc=%q Run=%q.\nA pattern broad enough to match any path would swallow the documented paths a reader types.",
				f.Name, entry.What, f.Doc, f.Run)})
		case !entry.NeedsPath && (f.Doc != "" || f.Run != ""):
			refusals = append(refusals, Problem{Message: fmt.Sprintf(
				"the onboarding.Volatile entry %q is %s, which is matched by shape and carries no path, and this declaration gives Doc=%q Run=%q",
				f.Name, entry.What, f.Doc, f.Run)})
		default:
			norms = append(norms, entry.norm(f))
		}
	}
	return norms, refusals
}
