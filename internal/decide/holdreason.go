// holdreason is a decision that classifies HOLD reports.
// The state is a HOLD report as text. The questions ask:
//
// 1. class (choice): what kind of hold this is
//    - paths-too-narrow: the report says the paths are too narrow
//    - missing-dependency: the report says a dependency is missing
//    - already-done: the report says the work is already done
//    - work-defect: the report says there is a defect in the work
//    - harness-failure: the report says the harness failed
//
// 2. p (noul): the probability that the report is correctly classified
//
// 3. proposed_paths (noul): the report contains PATHS-PROPOSED line
//
// The answer for class gives a probability per option.
// The answer for proposed_paths gives p=yes if PATHS-PROPOSED line is found, else p=no.
// ExtractProposedPaths extracts the PATHS-PROPOSED line if present, else returns paths the report names as needed.

package decide

import (
	"slices"
	"strings"
)

const HoldName = "holdreason"

const (
	PathsTooNarrow    = "paths-too-narrow"
	MissingDependency = "missing-dependency"
	AlreadyDone       = "already-done"
	WorkDefect        = "work-defect"
	HarnessFailure    = "harness-failure"
)

// HoldSchema returns the schema for the hold decision.
func HoldSchema() Schema {
	return Schema{
		Name: HoldName,
		Questions: map[string]Question{
			"class": {
				Type:         Choice,
				Instructions: "what kind of hold this is",
				Criteria: map[string]string{
					PathsTooNarrow:    "the report says the paths are too narrow",
					MissingDependency: "the report says a dependency is missing",
					AlreadyDone:       "the report says the work is already done",
					WorkDefect:        "the report says there is a defect in the work",
					HarnessFailure:    "the report says the harness failed",
				},
			},
			"proposed_paths": {
				Type:         Noul,
				Instructions: "the report contains PATHS-PROPOSED line",
			},
		},
	}
}

// HoldState returns the state text for the hold decision.
func HoldState(report string) string {
	return "HOLD (the report as provided):\n" + report
}

// ExtractProposedPaths extracts the PATHS-PROPOSED line if present,
// else returns the paths the report names as needed.
func ExtractProposedPaths(report string) string {
	lines := strings.Split(report, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "PATHS-PROPOSED:") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "PATHS-PROPOSED:"))
		}
	}
	// If no PATHS-PROPOSED line, return paths that look like they are named as needed.
	var paths []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "path") && (strings.Contains(trimmed, "need") || strings.Contains(trimmed, "change")) {
			paths = append(paths, trimmed)
		}
	}
	return strings.Join(paths, "\n")
}

// HoldClassifications returns the list of possible hold classifications in order.
func HoldClassifications() []string {
	return []string{PathsTooNarrow, MissingDependency, AlreadyDone, WorkDefect, HarnessFailure}
}

// HoldClassificationsSorted returns the sorted list of possible hold classifications.
func HoldClassificationsSorted() []string {
	classes := HoldClassifications()
	slices.Sort(classes)
	return classes
}
