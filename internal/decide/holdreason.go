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
// 2. proposed_paths (noul): the report contains PATHS-PROPOSED line
//
// The answer for class gives a probability per option.
// The answer for proposed_paths gives p=yes if PATHS-PROPOSED line is found, else p=no.
// ExtractProposedPaths extracts the PATHS-PROPOSED line if present, else returns paths the report names as needed.

package decide

import "strings"

const HoldName = "holdreason"

const (
	PathsTooNarrow    = "paths-too-narrow"
	MissingDependency = "missing-dependency"
	AlreadyDone       = "already-done"
	WorkDefect        = "work-defect"
	HarnessFailure    = "harness-failure"
)

// HoldClassifications returns the list of possible hold classifications in order.
func HoldClassifications() []string {
	return []string{PathsTooNarrow, MissingDependency, AlreadyDone, WorkDefect, HarnessFailure}
}

// holdCriteria is what each class means: the criterion the class question states
// for it (SPEC-NOVA-DECIDE section 15).
var holdCriteria = map[string]string{
	PathsTooNarrow:    "the report says the paths are too narrow",
	MissingDependency: "the report says a dependency is missing",
	AlreadyDone:       "the report says the work is already done",
	WorkDefect:        "the report says there is a defect in the work",
	HarnessFailure:    "the report says the harness failed",
}

// HoldSchema returns the schema for the hold decision.
func HoldSchema() Schema {
	criteria := make(map[string]string, len(holdCriteria))
	for _, class := range HoldClassifications() {
		criteria[class] = holdCriteria[class]
	}
	return Schema{
		Name: HoldName,
		Questions: map[string]Question{
			"class": {
				Type:         Choice,
				Instructions: "what kind of hold this is",
				Criteria:     criteria,
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

// holdPathCues are the words on a line that say what the work needs or changes:
// the paths the report names as needed stand on such a line.
var holdPathCues = []string{"need", "change", "update"}

// ExtractProposedPaths extracts the PATHS-PROPOSED line if present, else
// returns the paths the report names as needed: the path-like tokens on a line
// that says what the work needs or changes. The result is one space-separated
// list, and "" when the report proposes none.
func ExtractProposedPaths(report string) string {
	lines := strings.Split(report, "\n")
	for _, line := range lines {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "PATHS-PROPOSED:"); ok {
			return strings.Join(strings.Fields(strings.ReplaceAll(rest, ",", " ")), " ")
		}
	}
	var paths []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !namesNeededWork(trimmed) {
			continue
		}
		for _, token := range strings.Fields(trimmed) {
			if token = strings.Trim(token, ",.;:\"'()[]"); looksLikePath(token) {
				paths = append(paths, token)
			}
		}
	}
	return strings.Join(paths, " ")
}

// namesNeededWork says a line says what the work needs or changes.
func namesNeededWork(line string) bool {
	lower := strings.ToLower(line)
	for _, cue := range holdPathCues {
		if strings.Contains(lower, cue) {
			return true
		}
	}
	return false
}

// looksLikePath says a token names a file: it holds a separator or an extension.
func looksLikePath(token string) bool {
	return strings.Contains(token, "/") || strings.Contains(token, ".")
}
