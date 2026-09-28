package redtriage

// attribution.go is the rule table that decides a single failing test's
// owning PR. It carries the rule, not the Jev call: a rule that needs the
// provider for every red is a rule that costs a call to answer every test
// it already knows the answer to. The table reads:
//
//  1. A friend-attributed cause held out of the call short-circuits the
//     row: the Friend set in TriageOptions is the SAME set the DONE-WHEN
//     measurement holds out, and the row's HeldOut=true is what the
//     measurement joins on.
//
//  2. The flake verdict reads first, because it is mechanical and the
//     verifier never re-runs the flake parse to know whether the test was
//     in it. flakeMatches reads each failing test against the table on
//     its own (one flake beside one real red is one `flaky` row and one
//     `real` row), and a failure with no At file:line still gets a flake
//     verdict against the test name alone.
//
//  3. The mechanical ownership picks candidates by file overlap: each
//     in-flight member PR is in or out, and the count decides.
//
//    candidate=0 -> `no-candidate`, escalate.
//    candidate=1 -> `Owner` stands. The Confidence is 1.00; the Decider
//                   is `rules`; the Why is `-`. No provider is asked.
//    candidate>=2 -> a Jev call is needed and the verdict is `asked`.
//
//  4. The `flaky` class is preserved across the table. A flaky test whose
//     mechanical owner is one PR is a `flaky` row with that PR as its
//     owner. The PR did not cause the failure; the test is non-deterministic.
//     That is the trap a `real` verdict would set, and the audit sample
//     walks into it without that distinction.

import (
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ci"
)

// ClassFor returns the class the rule table would give this failing test.
// It is called by both the mechanical rule path AND the Jev-asked path:
// a Jev answer that returns `flaky` is one the rule table already knew,
// so ClassFor is the source of truth.
//
// flake is the truth: flakeMatches(Test) over the table as of `now`. It
// is read by the caller, because the rule and the verifier share the
// table rather than holding two copies (the table is the file the
// `--flakes` flag points at; both readers parse it).
func ClassFor(testName string, flake bool) string {
	if flake {
		return ClassFlaky
	}
	return ClassRed
}

// flakeMatches is true iff flakes has a row for `name` whose expiry is on
// or after today (UTC), the per-test form of the predicate
// internal/ci/cired.go uses for ci-red reading 6. Triage calls it once per
// failing test; it never asks whether EVERY red is a flake. A row whose
// `Expiry` is zero (a row written before the column existed) matches
// nothing; a row whose expiry parses as a date in
// the past matches nothing; the row's expiry is a YYYY-MM-DD string the
// caller already parsed into time.Time.
func flakeMatches(name string, flakes []ci.FlakeRow, today string) bool {
	for _, f := range flakes {
		if f.Test != name {
			continue
		}
		if f.Expiry.IsZero() {
			continue
		}
		rowDay := f.Expiry.UTC().Format("2006-01-02")
		if today <= rowDay {
			return true
		}
	}
	return false
}

// todayUTC returns today's date as YYYY-MM-DD in UTC. nil reads today.
func todayUTC(t *time.Time) string {
	if t == nil {
		now := time.Now().UTC().Format("2006-01-02")
		return now
	}
	return t.UTC().Format("2006-01-02")
}

// candidates are the in-flight member PRs whose Touched list contains
// `file`. file is the file side of TestFailure.At
// (`file_test.go:73` -> `file_test.go`); "" returns no candidates, the
// row escalates as `no-candidate`.
//
// touchedBy is the index built by Roster.indexMembers(). The match is
// exact and case-sensitive: the file paths came out of `nova-merge
// member --diff`, which uses git's own paths, and git paths are
// case-sensitive on Linux, case-insensitive on the macOS default. Two
// machines with two different paths for the same file is a problem git
// own; redtriage matches what the caller passed in. Caller rules:
//
//   - Lowercase the path on the caller's side, OR
//   - Lowercase the roster's Touched side, OR
//   - Set candidates to no-match and let the row escalate as `no-candidate`.
//
// redtriage does not lowercase silently; a path it cannot match is a path
// that escalates, and a path it matches twice is two candidates Jev sees.
func candidates(file string, touchedBy map[int][]string, members []MemberPR) []MemberPR {
	if file == "" || len(touchedBy) == 0 {
		return nil
	}
	var out []MemberPR
	for _, m := range members {
		if m.Number <= 0 {
			continue
		}
		touched, ok := touchedBy[m.Number]
		if !ok {
			continue
		}
		for _, t := range touched {
			if t == file {
				out = append(out, m)
				break
			}
		}
	}
	return out
}

// friendOwner returns the held-out owner a friend attributed this red to,
// or "" when no row matched. The match is on Test and the file side of
// At; a friend that named the test only (no file) is NOT held out,
// because a held-out attribution needs both facts and one of them is At.
// The audit sample joins on HeldOut=true, not on the friend's typed
// reason, so this match is the only one the held-out set exercises.
func friendOwner(testName, file string, friends []FriendAttribution) (FriendAttribution, bool) {
	if testName == "" {
		return FriendAttribution{}, false
	}
	for _, f := range friends {
		if f.Test != testName {
			continue
		}
		if file != "" && fileOf(f.At) != file {
			// A friend's At narrows the held-out match to a single file;
			// a friend whose At is empty matches the test against any file.
			continue
		}
		if f.Owner == "" {
			continue
		}
		return f, true
	}
	return FriendAttribution{}, false
}

// jevFloor returns the floor for a call, mirroring decide.DefaultFloor
// when the caller passed a zero. A caller that wires this package to its
// own floor (the rough analogue of `nova-decide --floor 0.7`) sets
// opt.Floor and that floor is propagated unchanged.
func jevFloor(opt TriageOptions) float64 {
	if opt.Floor > 0 {
		return opt.Floor
	}
	return DefaultFloor
}
