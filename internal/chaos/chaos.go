// Package chaos provides a reusable chaos engineering harness.
// A Fault injects a failure, polls for recovery on an injected clock, heals,
// and reports. An optional Invariant hook is checked after each fault.
package chaos

import (
	"time"
)

// Clock abstracts time for testability.
type Clock interface {
	Now() time.Time
	Sleep(time.Duration)
}

// RealClock uses the real time.
type RealClock struct{}

func (RealClock) Now() time.Time          { return time.Now() }
func (RealClock) Sleep(d time.Duration)   { time.Sleep(d) }

// Bound is a duration deadline.
type Bound time.Duration

// Fault describes a failure scenario.
type Fault struct {
	// Name of the fault.
	Name string
	// Inject causes the failure.
	Inject func() error
	// Heal restores normal operation (optional).
	Heal func()
	// Recovered returns true when the system has recovered.
	Recovered func() bool
	// Bound is the maximum time allowed for recovery.
	Bound Bound
}

// Report is a single fault result line.
type Report struct {
	Suite     string
	Fault     string
	Recovered bool
	Took      time.Duration
	Bound     time.Duration
}

// String formats the report as a log line.
func (r Report) String() string {
	return "CHAOS " + r.Suite + " " + r.Fault + " recovered=" + boolToStr(r.Recovered) + " took=" + r.Took.String() + " bound=" + r.Bound.String()
}

func boolToStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// Runner executes faults with an injected clock.
type Runner struct {
	Clock Clock
	// Invariant is checked after each fault. If it returns an error,
	// the runner stops and reports the fault as failed.
	Invariant func() error
}

// Run executes each fault in order and returns reports.
func (r *Runner) Run(suite string, faults ...Fault) []Report {
	var reports []Report
	for _, f := range faults {
		reports = append(reports, r.runFault(suite, f))
	}
	return reports
}

func (r *Runner) runFault(suite string, f Fault) Report {
	start := r.Clock.Now()
	if err := f.Inject(); err != nil {
		return Report{Suite: suite, Fault: f.Name, Recovered: false, Took: 0, Bound: time.Duration(f.Bound)}
	}

	poll := 100 * time.Millisecond
	var took time.Duration
	recovered := false
	for {
		if f.Recovered() {
			recovered = true
			break
		}
		if took >= time.Duration(f.Bound) {
			break
		}
		r.Clock.Sleep(poll)
		took = r.Clock.Now().Sub(start)
	}

	if f.Heal != nil {
		f.Heal()
	}

	if r.Invariant != nil {
		if err := r.Invariant(); err != nil {
			recovered = false
		}
	}

	return Report{
		Suite:     suite,
		Fault:     f.Name,
		Recovered: recovered,
		Took:      took,
		Bound:     time.Duration(f.Bound),
	}
}

// PrintReport prints the report line to stdout.
func PrintReport(r Report) {
	println(r.String())
}
