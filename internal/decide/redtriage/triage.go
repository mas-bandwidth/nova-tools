package redtriage

// triage.go is the one entry the package exposes. Triage walks each
// failing test in the report, runs the rule table in attribution.go, and
// asks Jev only where the table has too many candidates to settle.

import (
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ci"
)

// JevCaller is the seam between this package and the decide.Client.
// `decide.Client.Decide` returns a map of question -> Answer, but this
// package only ever asks ONE question per failing test, so the caller
// hands us a typed func that frames the evidence, calls the wire, and
// returns the chosen PR number with a confidence. A nil JevCaller turns
// the package into the rule-only path: every multi-candidate row
// escalates as `below-floor` (the table literally has no answer), and
// the DONE-WHEN measurement joins those rows as "no call made" rather
// than holding them to the provider it never asked.
type JevCaller func(state string) (pr string, conf float64, err error)

// Triage is the one entry the package exposes. It walks r.Failures in the
// order the report named them, runs the rule table once per test, and
// returns the rows the verb prints.
//
// roster is the in-flight member PRs; opt carries the flake table, the
// held-out friend attributions, the date the flake table is read against
// and the floor. jev nil makes this the rule-only path: every
// multi-candidate row returns ClassEscalated with Why=below-floor, and
// the DONE-WHEN measurement joins those rows as "no call made" rather
// than holding them to the provider it never asked.
//
// Triage is PURE: it never reads the clock (opt.Now is the date), never a
// file (roster and opt come from the caller) and never a network (jev is
// the seam). Two calls with the same inputs return the same rows.
func Triage(r ci.FailedReport, roster Roster, opt TriageOptions, jev JevCaller) TriageResult {
	floor := jevFloor(opt)
	res := TriageResult{Floor: floor}

	roster.sortMembers()
	touchedBy := roster.indexMembers()

	now := parseNow(opt.Now)
	names := testNames(r)
	flakeTests := map[string]bool{}
	if everyFlaking(names, opt.Flakes, &now) {
		for _, n := range names {
			flakeTests[n] = true
		}
	}

	for _, f := range r.Failures {
		row := classifyOne(f, opt, touchedBy, roster.Members, floor, jev, &now, flakeTests[f.Test])
		res.Rows = append(res.Rows, row)
		res.Tests++
		switch row.Class {
		case ClassRed:
			res.Real++
			if row.OwnerString() != "-" {
				res.Named++
			}
		case ClassFlaky:
			res.Flaky++
			if row.OwnerString() != "-" {
				res.Named++
			}
		case ClassEscalated:
			res.Escalated++
		}
		if row.HeldOut {
			res.HeldOut++
		}
	}
	return res
}

// classifyOne answers one failing test. It is the rule table for one
// row, and it is the one place a Jev call is made.
func classifyOne(
	f ci.TestFailure,
	opt TriageOptions,
	touchedBy map[int][]string,
	members []MemberPR,
	floor float64,
	jev JevCaller,
	now *time.Time,
	flake bool,
) Attribution {
	a := Attribution{
		Test:    f.Test,
		Package: f.Package,
		At:      strings.TrimSpace(f.At),
		Floor:   floor,
		Decider: DeciderNone,
		Why:     "-",
	}

	// (1) Friend-attributed cause held out. The held-out set is the
	// ground truth against which the DONE-WHEN measurement joins; a row
	// that landed here is `held_out=yes` and never returned up as
	// attribution.
	if fr, ok := friendOwner(f.Test, fileOf(f.At), opt.Friends); ok {
		n, own := splitOwner(fr.Owner)
		if n > 0 {
			a.Owner = own
			a.PRNumber = n
			a.HeldOut = true
			a.Decider = DeciderFriend
			if flake {
				a.Class = ClassFlaky
			} else {
				a.Class = ClassRed
			}
			a.Confidence = 1.00
			a.Floor = floor
			return a
		}
	}

	// (2) Flake verdict reads first.
	if flake {
		a.Class = ClassFlaky
	} else {
		a.Class = ClassRed
	}

	// (3) Mechanical candidates.
	file := fileOf(f.At)
	cands := candidates(file, touchedBy, members)
	a.Candidates = len(cands)

	switch len(cands) {
	case 0:
		// No PR touched this file -- nobody owned the red; escalate.
		a.Decider = DeciderRules
		a.Confidence = 1.00
		a.Class = ClassEscalated
		a.Escalate = defaultEscalate
		a.Why = WhyNoCandidate
		return a
	case 1:
		// Exactly one PR touched this file -- it stands; no provider call.
		a.Owner = cands[0].Repo
		if a.Owner == "" {
			// Member had no Repo; treat the author as the owner with the
			// prefix "author/" so the printed line still parses.
			a.Owner = cands[0].Author
		}
		a.PRNumber = cands[0].Number
		a.Decider = DeciderRules
		a.Confidence = 1.00
		return a
	default:
		// Two or more touched the file -- ask Jev.
		if jev == nil {
			a.Decider = DeciderRules
			a.Class = ClassEscalated
			a.Escalate = defaultEscalate
			a.Why = WhyBelowFloor
			a.Confidence = 0
			return a
		}
		answer, conf, err := jev(frameState(f, cands))
		if err != nil || answer == "" {
			a.Decider = DeciderNone
			a.Class = ClassEscalated
			a.Escalate = defaultEscalate
			a.Confidence = 0
			a.Why = WhyProviderError
			return a
		}
		// The provider's answer is the pr-number string; pick the matching
		// candidate. An answer outside the candidate set is `Why=below-floor`
		// because the typed question's closed set refused it.
		n, ok := prNumber(answer)
		if !ok {
			a.Decider = DeciderJev
			a.Class = ClassEscalated
			a.Escalate = defaultEscalate
			a.Confidence = conf
			a.Why = WhyProviderError
			return a
		}
		var picked *MemberPR
		for i := range cands {
			if cands[i].Number == n {
				picked = &cands[i]
				break
			}
		}
		if picked == nil {
			a.Decider = DeciderJev
			a.Class = ClassEscalated
			a.Escalate = defaultEscalate
			a.Confidence = conf
			a.Why = WhyProviderError
			return a
		}
		if conf < floor {
			// Below the floor the answer stands NOT; escalate and never
			// assert. The rule 4 escalation happens here.
			a.Decider = DeciderJev
			a.Class = ClassEscalated
			a.Escalate = defaultEscalate
			a.Confidence = conf
			a.Floor = floor
			a.Owner = picked.Repo
			if a.Owner == "" {
				a.Owner = picked.Author
			}
			a.PRNumber = picked.Number
			a.Why = WhyBelowFloor
			return a
		}
		// Above the floor: stand the answer.
		a.Decider = DeciderJev
		a.Confidence = conf
		a.Floor = floor
		a.Owner = picked.Repo
		if a.Owner == "" {
			a.Owner = picked.Author
		}
		a.PRNumber = picked.Number
		return a
	}
}

// frameState is the bounded state the Jev question is asked over. It is
// the failing test's name, package and a SINGLE file:line, the candidate
// PRs and their files, never the log's prose. The total bytes are bounded
// by the caller's framing rule (the wire questions package's Truncate);
// this function does not truncate, because the caller may want to verify
// the state without truncation.
//
// The caller is expected to wrap this state in a S2-shaped frame using
// `internal/decide/questions.Frame` before calling decide.Client.Decide;
// redtriage keeps framing out of its hands because the FRAME preamble and
// the nonce are caller's data, not the evidence's.
func frameState(f ci.TestFailure, cands []MemberPR) string {
	var b strings.Builder
	b.WriteString("test: ")
	b.WriteString(strings.TrimSpace(f.Test))
	b.WriteString("\npkg: ")
	b.WriteString(strings.TrimSpace(f.Package))
	b.WriteString("\nat: ")
	b.WriteString(strings.TrimSpace(f.At))
	b.WriteString("\nfile: ")
	b.WriteString(fileOf(f.At))
	b.WriteString("\ncandidates:")
	for _, c := range cands {
		b.WriteString(" ")
		b.WriteString(itoa(c.Number))
	}
	b.WriteString("\n")
	return b.String()
}

// BuildMemberQuestion builds the typed question a JevCaller wraps for
// one attribution. The closed set is the candidate set the rule table
// narrowed to (PR numbers as strings -- decide.Question's closed set is
// string-typed); an answer outside Members is a provider error and never
// stands (SPEC-DECIDE S3, :620-630). The caller builds this in the
// `JevCaller` closure, NOT in Triage, because the question text is the
// caller's data, not the evidence's.
func BuildMemberQuestion(cands []MemberPR) (string, map[string]string) {
	instr := "Given the failing test and the candidate PRs that touched its file, return the PR number that most likely owns the red."
	members := make(map[string]string, len(cands))
	for _, c := range cands {
		first := ""
		if len(c.Touched) > 0 {
			first = c.Touched[0]
		}
		members[itoa(c.Number)] = "PR #" + itoa(c.Number) + " touched " + first
	}
	return instr, members
}

// prNumber parses the provider's "chosen" answer ("<n>") into an int and
// reports whether it is one. An empty answer or a non-numeric one is
// false; the caller routes it to Why=below-floor (or Why=provider-error).
func prNumber(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	if n <= 0 {
		return 0, false
	}
	return n, true
}

// splitOwner splits "<owner>/<repo>#<n>" -> n, "<owner>/<repo>" with a
// fallback to the friend-attribution's first slash.
func splitOwner(owner string) (int, string) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return 0, ""
	}
	hash := strings.LastIndex(owner, "#")
	if hash < 0 {
		return 0, owner
	}
	num := strings.TrimSpace(owner[hash+1:])
	n, ok := prNumber(num)
	if !ok {
		return 0, ""
	}
	return n, strings.TrimSpace(owner[:hash])
}

// testNames returns the unique test names from r.Failures, in the order
// first seen.
func testNames(r ci.FailedReport) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range r.Failures {
		if f.Test == "" || seen[f.Test] {
			continue
		}
		seen[f.Test] = true
		out = append(out, f.Test)
	}
	return out
}

// parseNow parses the opt.Now string (YYYY-MM-DD), returning the parsed
// time. An empty string returns today's UTC date.
func parseNow(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Now().UTC()
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Now().UTC()
	}
	return t
}
