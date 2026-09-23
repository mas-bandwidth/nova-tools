// Package redtriage answers "which test, whose red, flaky or real" for one
// red CI gate (nova-tools #1619, #1666).
//
// The ci-red reading (internal/ci/cired.go, SPEC-DECIDE reading 6) closes the
// run with `red=<class> rerun=<...> finding=<yes|no>` -- it names the class
// of a red and says whether one rerun is licensed. It does not say WHICH
// PR to look at, because that is a separate question: the failing test is
// read from the forge, but the owning PR is membership, not classification.
//
// This package adds the second layer the reading does not: given a
// FailedReport and a roster of in-flight member PRs with their touched
// files, name the failing test and the member PR that owns it -- and label
// the red "real" or "flaky" against the flake table, the way `nova-ci
// failed --decide` already does it. Where the mechanical answer is one PR,
// it is printed; where it is zero or several, the call is escalated to Jev
// (SPEC-DECIDE rule 4, §4 rules 3-5 of SPEC-TOOLWORK) and the verdict
// stands only if Jev returns above the floor -- below the floor is escalated
// and never asserted.
//
// Three rules govern this package, and they are why it is a package and
// not a method.
//
//  1. The evidence is bounded and mechanical. The frame that goes to Jev
//     carries the failing test's name, package and a SINGLE file:line, the
//     PR candidates with their files, the flake verdict, the floor and the
//     bench's head. It never carries the log's prose: a failing test is
//     the most writable evidence in this section, because the change under
//     test prints the log (SPEC-DECIDE reading 6, "The evidence").
//
//  2. A friend-attributed cause is held out of the answer, not folded in.
//     Where a friend already attributed the red to a PR (the rule 4 `HOLD`
//     or a typed DISPOSITION comment naming the PR), that attribution
//     wins, and this package's answer prints it without re-asking Jev
//     (SPEC-DECIDE §4 rules 1-2: a decision already made is not a
//     question). Where no friend did, Jev is asked and the answer holds.
//
//  3. Below the floor the answer is the absence of an answer. An
//     attribution the model is not sure enough of is escalated to the
//     reader, NOT asserted as a verdict. A red "names" a PR only when the
//     confidence stands, and the bite of an asserted wrong attribution is
//     why `why=below-floor` ends the row as a `ESCALATE` line and not a
//     `RED-TRIAGE` line.
//
// The package is PURE: nothing in it reads a file, the clock, or the
// network. The forge seam, the flake table, and the bench's roster of
// member PRs all come from the caller -- today, that caller is the
// imagined `nova-ci failed --triage` verb (SPEC-DECIDE reading 6 will gain
// `--triage <roster>`, after this card lands; the call-site annotation in
// the matching RESULT.md names the line a follow-up card will edit).
package redtriage

import (
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/ci"
)

// DefaultFloor is the confidence floor for an attribution. It mirrors
// decide.DefaultFloor (0.65) but is redeclared so this package has no
// import-time dependency on decide.New setting one on a Client it did not
// build -- a caller wiring the chain to its own floor sets Floor on each
// Attribution instead.
const DefaultFloor = 0.65

// ClassRed is the verdict member for a red gate: the failing test is owned
// by a member PR the package names with a Confidence at or above Floor.
// Confidences below the floor flow to Escalated instead and never stand.
const ClassRed = "real"

// ClassFlaky is the verdict member for a known-flake red: the failing test
// is in the flake table (everyFlaking), so the gate is real enough that
// Jev still names an owner PR but the call carries `flaky=yes` so the
// caller does not re-queue the runner. The decision is `flaky`, not
// `real`, because the red is not a defect of any single PR.
const ClassFlaky = "flaky"

// ClassEscalated is the verdict member for the absence of an answer: an
// attribution below the floor, an attribution over too many candidates for
// the mechanical rule to settle, or a friend-attributed cause held out so
// this call returned nothing. This is the row the next reader opens.
const ClassEscalated = "escalated"

// ClassUnknown is a member of NO answer set. Tampered or refused
// attributions fall through to it; a caller never acts on it.
const ClassUnknown = "unknown"

// Attribution is one triage row: what test failed, which member PR owns
// it, how sure the attribution is, why it was held back when it was, and
// whether the failure was a known flake. It is what the verb prints as one
// line per failing test, and it is what the audit sample joins a held-out
// ground-truth label to.
//
// The fields are ALL mechanical: the failing test, the owner PR, the
// confidence the provider returned, the floor it was gated on, the
// decider that named it (rules|jev|none), and a why of `-` when an
// attribution stands and `below-floor` (or `held-out` or `no-candidate`)
// when one does not. Flaky is the one bit that comes from the flake table,
// never from an answer (SPEC-DECIDE reading 6, "The rule table, which
// carries the whole licence").
type Attribution struct {
	Test       string  // the test's `--- FAIL: <Test>` name, the field Jev names
	Package    string  // the package it failed in, for a join back to the forge
	At         string  // the file:line Jev saw; "" when the forge named none
	Owner      string  // "rowan/nova-tools#<n>" when one PR stands; "" otherwise
	PRNumber   int     // the integer n in "<owner>/<repo>#<n>", 0 when Owner is ""
	Class      string  // ClassReal, ClassFlaky, ClassEscalated or ClassUnknown
	Confidence float64 // the provider's confidence for an answer; 1.00 for a rule answer; 0 for absent
	Floor      float64 // the floor the call was gated on; DefaultFloor when the caller set none
	Decider    string  // DeciderRules, DeciderJev or DeciderNone
	Why        string  // "-" when an attribution stands; below-floor|held-out|no-candidate otherwise
	Escalate   string  // the stronger reader a `Class=escalated` row goes to (SPEC-DECIDE D5)

	// Candidates is the count of in-flight member PRs whose touched files
	// overlap this failing test's At. It is one fact: candidate=0 is the
	// mechanical `no-candidate` line, candidate=1 is the mechanical `Owner=`
	// line, candidate>=2 is the Jev-asked path.
	Candidates int

	// HeldOut is true when a friend-attributed cause for this red was
	// already on file and the package held it out of the Jev ask. It is
	// printed so a reader of the line can tell attribution-by-friend from
	// attribution-by-mechanical-rule, and so the DONE-WHEN measurement (the
	// 100 held-out reds) joins HeldOut=true rows against the ground truth
	// rather than conflating them with Jev-asked rows.
	HeldOut bool
}

// OwnerString is "<owner>/<repo>#<n>" when an owner stands; "-" otherwise.
// It is the printed shape callers paste back into `nova-merge read --pr <n>`.
func (a Attribution) OwnerString() string {
	if a.PRNumber <= 0 || a.Owner == "" {
		return "-"
	}
	return a.Owner + "#" + itoa(a.PRNumber)
}

// Line is the one scannable line this row prints: test, package,
// file:line, owner PR, class, confidence, floor, decider, why, the
// escalation target, the candidate count and the held-out bit. The order
// is fixed and stable for the audit sample (D6's observed-versus-truth
// split, SPEC-DECIDE :1032-1054).
func (a Attribution) Line() string {
	return "RED-TRIAGE test=" + onelineField(a.Test) +
		" pkg=" + onelineField(a.Package) +
		" at=" + onelineField(a.At) +
		" owner=" + onelineField(a.OwnerString()) +
		" class=" + a.Class +
		" conf=" + ftoa(a.Confidence) +
		" floor=" + ftoa(a.Floor) +
		" decider=" + a.Decider +
		" why=" + whyOrDash(a.Why) +
		" escalate=" + onelineField(a.Escalate) +
		" candidates=" + itoa(a.Candidates) +
		" held_out=" + boolYesNo(a.HeldOut)
}

// Roster is the in-flight member PRs a triage call is asked over. Each is
// the forge's record: who opened it (the friendly name, not the login),
// the integer PR number, and the files touched in the diff that put the
// test's owning code on disk. Touched is the closed list the mechanical
// rule matches against, in the same shape a `nova-merge member --diff`
// already returns (each path a single token, no `/./`, never a build
// artefact). Roster is sorted by PR number on the way in so the candidate
// ranking is stable across two reads of the same data.
type Roster struct {
	Members []MemberPR
}

// MemberPR is one in-flight PR a triage call considers as the owner of a
// failing test. The PR was returned by the forge with its files list, so
// Touched is the diff the PR is carrying, not a guessed path.
type MemberPR struct {
	Author  string   // the friendly name the member is read by, e.g. "rowan"
	Number  int      // the PR's integer number on the forge
	Repo    string   // "<owner>/<name>", echoed back on the line
	Touched []string // the file paths the PR's diff touched, each a clean relative path
}

// FriendAttribution is one cause a friend already wrote for a red and we
// hold out of the Jev ask. Test is the test's `--- FAIL` name the friend
// named; Owner is the PR (`<owner>/<repo>#<n>`) the friend named as the
// owner; Reason is the typed word the friend used, so a reader of the
// audit can join `HOLD` and `DISPOSITION` rows on it; At is the file:line
// the friend named, when they named one (`""` matches any file the test
// could have failed in).
type FriendAttribution struct {
	Test   string // the failing test's name, the field `Attribution.Test` joins on
	Owner  string // the PR the friend named as owner, as "<owner>/<repo>#<n>"
	Reason string // the typed word: "HOLD", "DISPOSITION", etc.
	At     string // the file:line the friend named, when they named one; "" otherwise
}

// TriageOptions is the data and the floor one Triage call is asked over.
// Flakes is the same table `nova-ci failed --decide` already parses with
// internal/ci.ParseFlakeTable; an expired row matches nothing. Friends is
// the held-out set the DONE-WHEN measurement joins to; rows whose Test is
// in this set are HeldOut=true and never ask Jev. Now is read so a test
// can pin the flake-expiry check.
type TriageOptions struct {
	Flakes  []ci.FlakeRow
	Friends []FriendAttribution
	Now     string // YYYY-MM-DD; "" reads today, UTC
	Floor   float64
}

// TriageResult is the rows one call produced, the floors it was gated on
// and the totals a closing line needs. The closing shape mirrors a triage
// report: how many tests were triaged, how many went red, how many went
// flaky, how many escalated and how many were held out.
type TriageResult struct {
	Tests     int           // failing tests in the report
	Named     int           // rows with a non-empty Owner
	Real      int           // rows with Class=real
	Flaky     int           // rows with Class=flaky
	Escalated int           // rows with Class=escalated
	HeldOut   int           // rows that matched a friend-attributed cause
	Floor     float64       // the floor the call was gated on
	Rows      []Attribution // one per failing test, in the order the report named them
}

// Summary is the closing line of a triage, the shape a verb pastes under
// its existing FAILED summary. The fields are stable for `nova-decide log`
// joins and the DONE-WHEN measurement.
func (r TriageResult) Summary() string {
	return "RED-TRIAGE OK tests=" + itoa(r.Tests) +
		" named=" + itoa(r.Named) +
		" real=" + itoa(r.Real) +
		" flaky=" + itoa(r.Flaky) +
		" escalated=" + itoa(r.Escalated) +
		" held_out=" + itoa(r.HeldOut) +
		" floor=" + ftoa(r.Floor)
}

// sortMembers sorts the roster by PR number so the candidate list is
// deterministic: two reads of the same Roster over the same report return
// the same Attribution in the same order. It mutates the caller's slice
// in place rather than building a copy, because the slice lives a single
// call.
func (r *Roster) sortMembers() {
	sort.SliceStable(r.Members, func(i, j int) bool {
		return r.Members[i].Number < r.Members[j].Number
	})
}

// indexMembers builds an index from PR number to the slice of files it
// touched, so Triage walks the candidates in O(n) per failing test rather
// than scanning the roster each time.
func (r *Roster) indexMembers() map[int][]string {
	out := make(map[int][]string, len(r.Members))
	for _, m := range r.Members {
		if m.Number <= 0 {
			continue
		}
		cp := make([]string, len(m.Touched))
		copy(cp, m.Touched)
		out[m.Number] = cp
	}
	return out
}

// fileOf splits a TestFailure.At (`file_test.go:73`) on the colon and
// returns the file side; "" when At has no colon.
func fileOf(at string) string {
	at = strings.TrimSpace(at)
	if i := strings.LastIndex(at, ":"); i >= 0 {
		return at[:i]
	}
	return at
}
