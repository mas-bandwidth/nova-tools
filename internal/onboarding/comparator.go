package onboarding

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// CompareTranscript is the ONE comparison a firstrun_test.go may make
// (docs/SPEC-TOOLWORK.md documents rule 2).
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
	if refusals := recordedRefusals(doc, volatile); len(refusals) > 0 {
		return refusals
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

// shellVariable is the one spelling a recorded name's Doc may take: the shell
// variable a reader sets.
var shellVariable = regexp.MustCompile(`^\$[A-Z][A-Z0-9_]*$`)

// recordedRefusals holds each `recorded` declaration to the document it
// normalises: the variable is one a documented command types, and the recorded
// name appears nowhere in the document as written. A name the document also
// prints literally would be rewritten on the tool's side and not on the
// document's, and a variable no command types stands for nothing the reader
// set.
func recordedRefusals(doc []Step, volatile []Field) []Problem {
	var refusals []Problem
	for _, f := range volatile {
		entry, ok := volatileEntry(f.Name)
		if !ok || !entry.Repeatable {
			continue
		}
		typed := false
		word := regexp.MustCompile(`\b` + regexp.QuoteMeta(f.Run) + `\b`)
		literal := ""
		for _, s := range doc {
			typed = typed || strings.Contains(s.Line, f.Doc)
			for _, line := range append([]string{s.Line}, s.Want...) {
				if literal == "" && word.MatchString(line) {
					literal = line
				}
			}
		}
		if !typed {
			refusals = append(refusals, Problem{Message: fmt.Sprintf(
				"the recorded name %q is written %s, and no documented command types %s; a recorded name stands for a variable the reader sets on the command line", f.Run, f.Doc, f.Doc)})
		}
		if literal != "" {
			refusals = append(refusals, Problem{Message: fmt.Sprintf(
				"the recorded name %q appears in the document as written:\n  %s\nso rewriting it as %s on the tool's side would compare two different things; a recorded name is one the document never prints", f.Run, literal, f.Doc)})
		}
	}
	return refusals
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
	// NeedsPath is true for the kinds of value a pattern must not guess at: a
	// path, or a recorded name, whose two spellings the test supplies. A pattern
	// broad enough to match any path would swallow the documented paths a reader
	// types.
	NeedsPath bool
	// Repeatable is true for an entry a transcript may name more than once, once
	// per documented spelling: a recorded fixture carries more than one name.
	Repeatable bool

	// norm builds the normalisation applied to both sides of the comparison.
	norm func(f Field) Norm
}

// Volatile is the ONE table of run-owned values. It is the whole set of things a
// transcript may leave uncompared, and it is shared so that "this transcript is
// green" means the same in every binary. Growing it is a reading, not a call
// site's decision -- which is what the refusal below is for.
//
// The seven entries are the ones docs/SPEC-TOOLWORK.md documents rule 2 names:
// `at=`, `took=`, `created=`, a temporary directory, a fresh sha, a name a
// recorded fixture carries, and the stamp on a `branch=` nova-secrets seals on.
//
// FIVE OF THE SIX ARE TOKEN-ANCHORED, and the sixth says why it is not. A norm
// that names a field replaces only a whitespace-delimited token spelled
// `<field>=<value>` in full (Norm.apply, transcript.go): a pattern that ran over
// the whole line would let an entry declared for one field swallow a
// neighbour's value, which is exactly #1629's ROW 1 defect, repaired at
// 4f2d552b and reintroduced here in the first cut of this table --
// `Field{Name:"sha"}` also normalised `base_sha=`, `took` also normalised
// `last_took=`, and no test said so. TestAVolatileEntryNeverSwallows-
// ANeighbouringFieldsValue now holds every entry to it, by the shape of the
// mistake rather than by the entry, so a sixth entry that forgets is one row of
// a table away from being caught.
//
// `tmpdir` is the exception and is sound without a field: Path replaces its
// literal absolute directory only at the end of a token, before `/`, or before
// the comma used around a path in prose, so it covers descendants without
// swallowing a longer path that shares the prefix.
var Volatile = []VolatileField{
	{
		Name: "at",
		What: "at= (the instant of this run)",
		norm: func(Field) Norm { return Instant("at") },
	},
	{
		Name: "took",
		What: "took= (how long this run took)",
		// field and valid are this package's own, and are set here rather than
		// through a constructor because transcript.go has none for a duration.
		// valid PARSES the value, the property 4f2d552b gave Instant: a
		// normalisation that erases an impossible value erases the finding with
		// it, so a `took=` that is not a duration stays on the line.
		norm: func(Field) Norm {
			return Norm{
				Name:  "took= (how long this run took)",
				Re:    regexp.MustCompile(`^took=.*$`),
				As:    "took=<how long this run took>",
				field: "took",
				valid: isDuration,
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
		// Anchored at both ends AND named, so `base_sha=` is another token and
		// is compared as written. A sha of some other length, or with a
		// non-hex digit in it, is the tool disagreeing with the document and
		// stays on the line -- the same rule HexID keeps.
		norm: func(Field) Norm {
			return Norm{
				Name:  "sha= (a sha this run made)",
				Re:    regexp.MustCompile(`^sha=[0-9a-f]{7,40}$`),
				As:    "sha=<a sha this run made>",
				field: "sha",
			}
		},
	},
	{
		Name:       "recorded",
		What:       "a name a recorded fixture carries, written in the document as the variable the reader sets",
		NeedsPath:  true,
		Repeatable: true,
		// A transcript run against a RECORDING (nova-work's GitHub fixture, a
		// public repository of the project's own organization) prints the names
		// the recording carries, and the reader's run prints theirs: the document
		// writes the shell variable the reader sets ($ORG), the test names the
		// recorded spelling. Only that whole name is replaced -- at a word
		// boundary before it, and before `/`, a blank, `,` or the end after it --
		// so a longer name that merely contains it is compared as written.
		norm: func(f Field) Norm { return Recorded(f.Doc, f.Run) },
	},
	{
		Name: "branch",
		What: "branch= (the seal branch this run stamped with its instant)",
		// nova-secrets carries a change on `seal/<seat>-<NAMES>-<stamp>` and names
		// the branch on its OK line; the stamp is the run's UTC instant. The whole
		// token goes, because a norm replaces a token and never part of one -- the
		// seat and the names are also `seat=` and `names=` on the same line, where
		// they are compared as written. The stamp is PARSED, as Instant's is: a
		// branch whose stamp is not a real instant stays on the line.
		norm: func(Field) Norm {
			return Norm{
				Name:  "branch= (the seal branch this run stamped with its instant)",
				Re:    regexp.MustCompile(`^branch=seal/[A-Za-z0-9_+-]+-[0-9]{8}-[0-9]{6}$`),
				As:    "branch=<the seal branch this run stamped>",
				field: "branch",
				valid: isStampedBranch,
			}
		},
	},
}

// isStampedBranch answers whether v ends in a real `-YYYYMMDD-HHMMSS` instant.
func isStampedBranch(v string) bool {
	if len(v) < len("20060102-150405") {
		return false
	}
	_, err := time.Parse("20060102-150405", v[len(v)-len("20060102-150405"):])
	return err == nil
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
		key := f.Name
		if entry.Repeatable {
			// A recorded name is named once, by one variable: a second entry for
			// the same variable, or the same recorded name under a second
			// variable, is refused as a declaration made twice.
			if !shellVariable.MatchString(f.Doc) {
				refusals = append(refusals, Problem{Message: fmt.Sprintf(
					"the onboarding.Volatile entry %q is %s: its Doc is the shell variable the reader sets, `$NAME` in capitals, and this declaration gives Doc=%q.\nAny other spelling would let a declaration turn one value of the document into another.",
					f.Name, entry.What, f.Doc)})
				continue
			}
			if seen[f.Name+"\x01"+f.Run] && f.Run != "" {
				refusals = append(refusals, Problem{Message: fmt.Sprintf(
					"the transcript names the recorded name %q from the onboarding.Volatile table twice; one recorded name is written as one variable", f.Run)})
				continue
			}
			seen[f.Name+"\x01"+f.Run] = true
			key += "\x00" + f.Doc
		}
		if seen[key] {
			refusals = append(refusals, Problem{Message: fmt.Sprintf(
				"the transcript names %q from the onboarding.Volatile table twice; one declaration is the whole of what is not compared for that value",
				f.Name)})
			continue
		}
		seen[key] = true
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

// isDuration answers whether v is a duration Go can parse and not only the
// shape of one, the property Instant's isInstant keeps for an instant.
func isDuration(v string) bool {
	_, err := time.ParseDuration(v)
	return err == nil
}
