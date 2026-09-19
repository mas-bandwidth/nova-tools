package ci

// cired.go is the rule table and the licence behind `nova-ci failed --decide`: the CI-red
// reading of docs/SPEC-DECIDE.md, "6. The CI-red reading: one licensed rerun, or a
// finding". It is PURE: it takes the report the verb already read and the caller's data --
// a flake table, an infra-steps table and a clock -- and returns the class plus whether a
// single rerun is licensed. The reading never reruns anything (rule 7): it prints the
// licence, and the caller's own rerun command is the act.
//
// Every row is a fact the forge reports about the run, or a name matched against a table;
// none is a reading of the log's prose, because the change under test prints the log.

import (
	"fmt"
	"strings"
	"time"
)

// The closed class set of the CI-red reading. own-build-break is a decider's label: the
// rule table can never license it, because a build that does not compile is the change's.
const (
	RedNamedTest     = "named-test"
	RedOwnBuildBreak = "own-build-break"
	RedCancelledLeg  = "cancelled-leg"
	RedKnownFlake    = "known-flake"
	RedInfra         = "infra"
)

// RedJob is one failed job as the reading sees it: the forge's own conclusion and the name
// of the step that failed. Both are mechanical facts, never a reading of the log.
type RedJob struct {
	Name       string
	Conclusion string
	Step       string
}

// FlakeRow is one row of the flake table: a test name, the issue that tracks it and the
// date the row expires. A row past its expiry matches nothing.
type FlakeRow struct {
	Test   string
	Issue  string
	Expiry time.Time
}

// RedOptions is the caller's data and the clock for one reading. Reruns is the forge's own
// attempt count for this job at this sha less one; Withdrawn is set when a decider took the
// licence back. With no provider both are the mechanical values.
type RedOptions struct {
	Flakes     []FlakeRow
	InfraSteps []string
	Now        time.Time
	Reruns     int
	Withdrawn  bool
}

// RedReading is the reading's whole answer: the class the rule table found ("" when it
// found no row at all), the licence, and why it was withheld.
type RedReading struct {
	Class   string
	Rerun   string // "licensed" or "no"
	Finding string // "yes" or "no"
	Reruns  int
	Why     string // "withdrawn" when a decider took the licence back; else ""
}

// ClassifyRed runs the rule table over one report and returns the class and the licence.
// The table carries the whole licence: the rerunnable classes are cancelled-leg,
// known-flake and infra, and rerun=licensed needs one of them, no withdrawal, and zero
// reruns already made for this job at this sha. Everything else -- a named failing test, a
// class the table cannot place, a second red at the same sha, a withdrawn licence -- is a
// finding at once, because a rerun that turns a real red green is manufactured green.
func ClassifyRed(r FailedReport, opt RedOptions) RedReading {
	now := opt.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	reading := RedReading{Class: redClass(r, opt, now), Reruns: opt.Reruns}
	switch reading.Class {
	case RedCancelledLeg, RedKnownFlake, RedInfra:
		switch {
		case opt.Withdrawn:
			reading.Rerun, reading.Finding, reading.Why = "no", "yes", "withdrawn"
		case opt.Reruns == 0:
			reading.Rerun, reading.Finding = "licensed", "no"
		default:
			reading.Rerun, reading.Finding = "no", "yes"
		}
	default:
		// A red nobody can class mechanically, and a named failing test, are findings.
		// There is no rerun to withdraw and no class to print; the licence was never there.
		reading.Rerun, reading.Finding = "no", "yes"
	}
	if r.Jobs == 0 && len(r.Failures) == 0 {
		// Nothing red: there is no finding to open and no licence to grant.
		reading.Finding = "no"
	}
	return reading
}

// redClass is the rule table itself: a fact the forge reports about the run, or a name
// matched against a table. Any `--- FAIL` name present makes the red a known-flake when
// every failing name is in the unexpired flake table and a named-test otherwise, even
// beside a cancelled leg. With no FAIL name the failed jobs' own conclusions decide.
func redClass(r FailedReport, opt RedOptions, now time.Time) string {
	if names := failingTestNames(r); len(names) > 0 {
		if everyFlaking(names, opt.Flakes, now) {
			return RedKnownFlake
		}
		return RedNamedTest
	}
	if len(r.RedJobs) == 0 {
		return ""
	}
	if everyRedJob(r.RedJobs, func(j RedJob) bool { return isCancelledConclusion(j.Conclusion) }) {
		return RedCancelledLeg
	}
	if everyRedJob(r.RedJobs, func(j RedJob) bool { return infraJob(j, opt.InfraSteps) }) {
		return RedInfra
	}
	return ""
}

// failingTestNames lists the `--- FAIL: <Test>` names once each, in the order first seen.
func failingTestNames(r FailedReport) []string {
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

// everyFlaking is true when every failing name has an unexpired row in the flake table. An
// expired row matches nothing, so one expired row never makes a name flaky.
func everyFlaking(names []string, flakes []FlakeRow, now time.Time) bool {
	for _, name := range names {
		if !flakeMatches(name, flakes, now) {
			return false
		}
	}
	return true
}

// flakeMatches is true when the table has a row for the name whose expiry is not before
// today: the row is expired when now, read as a UTC date, is after its date.
func flakeMatches(name string, flakes []FlakeRow, now time.Time) bool {
	today := now.UTC().Format("2006-01-02")
	for _, f := range flakes {
		if f.Test != name || f.Expiry.IsZero() {
			continue
		}
		if today <= f.Expiry.UTC().Format("2006-01-02") {
			return true
		}
	}
	return false
}

// infraJob is true for a job that failed without an assertion of the repository's: the
// forge's own timed_out or startup_failure conclusion, or a failed step whose NAME is in
// the caller's infra-steps table. The step name is a fact from the forge, not a line of
// the log.
func infraJob(j RedJob, steps []string) bool {
	switch strings.ToLower(strings.TrimSpace(j.Conclusion)) {
	case "timed_out", "startup_failure":
		return true
	}
	for _, s := range steps {
		if s != "" && strings.EqualFold(strings.TrimSpace(s), strings.TrimSpace(j.Step)) {
			return true
		}
	}
	return false
}

// isCancelledConclusion is true for the forge's cancelled, spelled either way.
func isCancelledConclusion(conclusion string) bool {
	switch strings.ToLower(strings.TrimSpace(conclusion)) {
	case "cancelled", "canceled":
		return true
	}
	return false
}

// everyRedJob is true when the predicate holds for every job, and false for no jobs.
func everyRedJob(jobs []RedJob, ok func(RedJob) bool) bool {
	if len(jobs) == 0 {
		return false
	}
	for _, j := range jobs {
		if !ok(j) {
			return false
		}
	}
	return true
}

// ParseFlakeTable reads the flake table: one row a line, TAB-separated test, issue and
// expiry (YYYY-MM-DD). Blank lines and lines beginning '#' are skipped. A row that is not
// the shape it wants is refused by line rather than silently dropped, so an expired row can
// never hide behind a parse this code guessed at.
func ParseFlakeTable(text string) ([]FlakeRow, error) {
	var out []FlakeRow
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			return nil, fmt.Errorf("line %d is not test<TAB>issue<TAB>expiry: %q", i+1, line)
		}
		expiry, err := time.Parse("2006-01-02", strings.TrimSpace(parts[2]))
		if err != nil {
			return nil, fmt.Errorf("line %d names expiry %q, which is not YYYY-MM-DD", i+1, strings.TrimSpace(parts[2]))
		}
		out = append(out, FlakeRow{
			Test:   strings.TrimSpace(parts[0]),
			Issue:  strings.TrimSpace(parts[1]),
			Expiry: expiry,
		})
	}
	return out, nil
}

// ParseInfraSteps reads the infra-steps table: one step NAME a line, blank lines and lines
// beginning '#' skipped. The names are the caller's data and there is no default.
func ParseInfraSteps(text string) []string {
	var out []string
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// Fields is the tail the closing line gains under --decide, in one fixed shape:
//
//	red=<class> rerun=<licensed|no> finding=<yes|no> reruns=<n>
//
// A red with no rule row prints red=unknown, the absence of a class rather than a guessed
// one. The field names, their order and their spelling are the output grammar other lines
// parse and do not change.
func (rr RedReading) Fields() string {
	class := rr.Class
	if class == "" {
		class = "unknown"
	}
	return fmt.Sprintf(" red=%s rerun=%s finding=%s reruns=%d", class, rr.Rerun, rr.Finding, rr.Reruns)
}
