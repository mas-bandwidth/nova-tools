package bus

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// THE CHECK REPORT, CAPPED. A full check over a bus adopted onto an old history
// is the wall of red #2574 measured: 1,059 FAIL lines, most of them one class
// of finding repeated. The report a check prints is capped at --max findings
// (nova-bus does the printing; this file holds what the cap needs): the class
// each finding belongs to, and the counts that survive the cap -- because the
// listing may be capped and the counting never is. The count line is the truth
// about the bus whether or not every finding was printed.

// ClassCount is one finding class's share of a check's findings, for the BUS
// CHECK count line.
type ClassCount struct {
	Class string
	Count int
}

// CheckCounts is a check run's findings counted, in total and by class. The
// counts are taken from the WHOLE finding list a run walked, not from what a
// cap let it print: findings= is the truth about the bus, and a reader who was
// shown 20 of 1,059 still reads fail=28 warn=25 and which class each came from.
type CheckCounts struct {
	Findings int
	Fail     int
	Warn     int
	Class    []ClassCount // one per class present, sorted by class name
}

// ProblemClass names the class a check finding belongs to, for the BUS CHECK
// count line. It is a DISPLAY rule and nothing else: the exit code, the
// findings themselves and the cap answer nothing to it.
//
// The check's emitters -- CheckWith, CheckSince, CheckIndex, checkReceipts,
// checkLaneStateFile -- name each finding's class in its Where or in its
// Reason, the same text a person reads, and this reads that text rather than
// threading a new field through every emitter, which would be an edit to four
// files for one count line. What each class reads on:
//
//	receipt    Where is a RECEIPTS line (or the file itself)
//	catalogue  Where is an INDEX line, or the reason names a note no INDEX
//	           line holds
//	state      Where is a lane's CURSOR, OPEN, BEAT or bare INDEX file
//	id         the reason is an Id finding, "Id: ..."
//	re         the reason is a Re finding, "Re: ..."
//	stray      the reason names a file a lane may not hold
//	lane       the reason names a lane no roster entry owns
//	parse      the reason is a parse error, which ParseNoteAll writes "line <n>: ..."
//	header     everything else, which on a note is a header finding -- the loud
//	           kind: no Subject, a To naming nobody, a From naming nobody
//
// A note whose file cannot be READ at all (a permission error under --since)
// has no class of its own and is counted under header; it is a finding about
// one note, it is rare, and inventing a class per error text is a second
// taxonomy nobody reads.
func ProblemClass(p Problem) string {
	// The Where a file names, cut to its last path segment and its :line half.
	// A note is never named RECEIPTS or INDEX exactly -- readLane reads those
	// as the files they are -- so the segment match cannot swallow a note.
	tail := p.Where
	if i := strings.LastIndexByte(tail, '/'); i >= 0 {
		tail = tail[i+1:]
	}
	file, _, numbered := strings.Cut(tail, ":")
	switch {
	case file == ReceiptsName:
		return "receipt"
	case file == IndexName && numbered:
		return "catalogue"
	case file == IndexName || file == CursorName || file == OpenName || file == BeatName:
		return "state"
	}
	switch {
	case strings.HasPrefix(p.Reason, KeyID+":"):
		return "id"
	case strings.HasPrefix(p.Reason, KeyRe+":"):
		return "re"
	case strings.HasPrefix(p.Reason, "no line in "):
		return "catalogue"
	case strings.HasPrefix(p.Reason, "a lane holds notes ("):
		return "stray"
	case strings.HasSuffix(p.Reason, "owns this lane"):
		return "lane"
	case parseFindingRe.MatchString(p.Reason):
		return "parse"
	}
	return "header"
}

// parseFindingRe matches the one shape ParseNoteAll writes every parse error
// in, `line <n>: ...`. A finding about a note's SHAPE carries the line; a
// finding about its header does not.
var parseFindingRe = regexp.MustCompile(`^line \d+: `)

// CountCheckFindings counts a check's findings, in total and by class. Sorted
// by class name, so the count line is the same line for the same findings,
// whatever order the walk met them in.
func CountCheckFindings(ps []Problem) CheckCounts {
	var cs CheckCounts
	byClass := map[string]int{}
	for _, p := range ps {
		cs.Findings++
		if p.Warn {
			cs.Warn++
		} else {
			cs.Fail++
		}
		byClass[ProblemClass(p)]++
	}
	cs.Class = make([]ClassCount, 0, len(byClass))
	for class, n := range byClass {
		cs.Class = append(cs.Class, ClassCount{Class: class, Count: n})
	}
	sort.Slice(cs.Class, func(i, j int) bool { return cs.Class[i].Class < cs.Class[j].Class })
	return cs
}

// ResolveSinceCommit turns a --since value into the commit a check diffs from.
// A revision, in the spelling ResolveCommit takes, wins: a branch or a tag
// named 2026-09-01 is still itself. A value that is not a revision but IS a
// UTC date or an RFC 3339 instant -- the two spellings --legacy-before takes,
// read by the same ParseLegacyBefore -- names the last commit written before
// it, so `check --since 2026-09-01` is "what the bus gained since that day"
// in words the tool already speaks.
func ResolveSinceCommit(dir, value string) (string, error) {
	sha, revErr := ResolveCommit(dir, value)
	if revErr == nil {
		return sha, nil
	}
	when, dateErr := ParseLegacyBefore(value)
	if dateErr != nil {
		return "", revErr
	}
	return LastCommitBefore(dir, when)
}

// LastCommitBefore is the newest commit in this checkout written before the
// moment: the base a --since <date> diffs from. A date before every commit in
// the checkout is a refusal, not a guess at an empty change set -- a check
// that answered "nothing has changed" from a date the bus did not exist at
// would be the quiet lie the whole tool exists to stop.
func LastCommitBefore(dir string, when time.Time) (string, error) {
	out, err := git(dir, "rev-list", "-n", "1", "--before="+when.UTC().Format(time.RFC3339), "HEAD")
	if err != nil {
		return "", fmt.Errorf("no commit in this checkout is dated before %s, so there is nothing to read since it (%s)",
			when.UTC().Format(time.RFC3339), truncate(strings.TrimSpace(out), 120))
	}
	sha := strings.TrimSpace(out)
	if sha == "" {
		return "", fmt.Errorf("no commit in this checkout is dated before %s, so there is nothing to read since it", when.UTC().Format(time.RFC3339))
	}
	if err := ValidCommitHex(sha); err != nil {
		return "", err
	}
	return sha, nil
}
