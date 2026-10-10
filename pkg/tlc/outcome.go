package tlc

import (
	"regexp"
	"strings"
)

// TLC's exit statuses this package acts on. Any other status is a failure of
// the run, never a result.
const (
	ExitPass      = 0  // no error found
	ExitInvariant = 12 // an invariant (or a deadlock or assertion) is violated
	ExitProperty  = 13 // an action or temporal property is violated
	ExitTimeout   = 124
	ExitNoStart   = 127 // java could not be started
)

// Violation is one property TLC said is violated.
type Violation struct {
	Kind string // "Invariant", "Action property" or "Temporal property"
	Name string // empty when TLC does not name a temporal property
}

// Outcome is what one TLC output says, apart from its exit status.
type Outcome struct {
	Completed  bool   // "Model checking completed. No error has been found."
	Generated  string // states generated, digits only; "-" when TLC printed none
	Distinct   string // distinct states found, digits only; "-" when TLC printed none
	Violations []Violation
	// InitialState is set when an invariant is violated by the initial state.
	// TLC stops there and prints no state count; the state it prints is the
	// one it examined, so Generated and Distinct are 1 and 1.
	InitialState bool
}

const completedLine = "Model checking completed. No error has been found."

var (
	statsRE = regexp.MustCompile(`([\d,]+) states generated, ([\d,]+) distinct states found`)
	// A violation line is "Error: Invariant X is violated." and its sibling, or
	// "Error: Invariant X is violated by the initial state:". The name is one
	// token, as a TLA+ identifier is.
	violatedRE = regexp.MustCompile(`(Invariant|Action property) (\S+) is violated(\.| by the initial state)`)
	// Older TLC says "Temporal properties were violated." and names nothing;
	// newer TLC says "Temporal property X was violated.".
	temporalNamedRE = regexp.MustCompile(`Temporal property (\S+) was violated\.`)
	propertyLineRE  = regexp.MustCompile(`(?m)^PROPERT(?:Y|IES)\s+([^\n]+)`)
)

const temporalGeneric = "Temporal properties were violated."

// Parse reads a TLC output. The statistics are the LAST pair printed: TLC
// prints a progress line for every minute of a long run and the totals at the
// end, and the totals are the number a record keeps.
func Parse(output string) Outcome {
	o := Outcome{
		Completed: strings.Contains(output, completedLine),
		Generated: "-",
		Distinct:  "-",
	}
	if all := statsRE.FindAllStringSubmatch(output, -1); len(all) > 0 {
		last := all[len(all)-1]
		o.Generated = strings.ReplaceAll(last[1], ",", "")
		o.Distinct = strings.ReplaceAll(last[2], ",", "")
	}
	for _, m := range violatedRE.FindAllStringSubmatch(output, -1) {
		o.Violations = append(o.Violations, Violation{Kind: m[1], Name: m[2]})
		if m[3] != "." {
			o.InitialState = true
		}
	}
	if o.InitialState && !o.HasStats() {
		o.Generated, o.Distinct = "1", "1"
	}
	for _, m := range temporalNamedRE.FindAllStringSubmatch(output, -1) {
		o.Violations = append(o.Violations, Violation{Kind: "Temporal property", Name: m[1]})
	}
	if strings.Contains(output, temporalGeneric) {
		o.Violations = append(o.Violations, Violation{Kind: "Temporal property"})
	}
	return o
}

// HasStats reports whether TLC printed its state counts.
func (o Outcome) HasStats() bool { return o.Generated != "-" && o.Distinct != "-" }

// Violated reports whether TLC named this invariant or action property.
func (o Outcome) Violated(kind, name string) bool {
	for _, v := range o.Violations {
		if v.Kind == kind && v.Name == name {
			return true
		}
	}
	return false
}

// TemporalViolations returns the temporal violations TLC reported.
func (o Outcome) TemporalViolations() []Violation {
	var out []Violation
	for _, v := range o.Violations {
		if v.Kind == "Temporal property" {
			out = append(out, v)
		}
	}
	return out
}

// Accepts reports whether the exit status and output are the result a case
// declares. It never accepts a timeout, a parse failure or a violation of some
// other property.
//
//   - pass: exit 0 and the completion line.
//   - invariant: exit 12 and one of the "|"-separated names is violated.
//   - action: exit 13 and one of the names is violated.
//   - temporal: exit 13 and a temporal violation, where the case's config
//     selects exactly the one property the case names. TLC that names the
//     violated property must name that one; TLC that does not is trusted on
//     the config alone. config is the text of the case's .cfg file.
func Accepts(c Case, code int, output, config string) bool {
	o := Parse(output)
	switch c.Expected {
	case "pass":
		return code == ExitPass && o.Completed
	case "temporal":
		if code != ExitProperty {
			return false
		}
		found := o.TemporalViolations()
		if len(found) == 0 {
			return false
		}
		line := propertyLineRE.FindStringSubmatch(config)
		if line == nil || strings.TrimSpace(line[1]) != c.Property {
			return false
		}
		for _, v := range found {
			if v.Name != "" && v.Name != c.Property {
				return false
			}
		}
		return true
	case "invariant":
		return code == ExitInvariant && anyViolated(o, "Invariant", c.Property)
	case "action":
		return code == ExitProperty && anyViolated(o, "Action property", c.Property)
	}
	return false
}

func anyViolated(o Outcome, kind, names string) bool {
	for _, name := range strings.Split(names, "|") {
		if o.Violated(kind, name) {
			return true
		}
	}
	return false
}
