package pulse

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// CutTemplateInput is everything needed to render a card template with dependencies.
type CutTemplateInput struct {
	Template  string
	Row       PoolRow
	DependsOn []string
}

// ParseDependsOn splits a comma-separated depends-on flag string into a slice of card ids.
// If s is empty or "-", it returns a slice containing "-".
func ParseDependsOn(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return []string{"-"}
	}
	var items []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			items = append(items, part)
		}
	}
	if len(items) == 0 {
		return []string{"-"}
	}
	return items
}

// FormatDependsOn formats the DEPENDS-ON: line for a card header.
// If deps is empty or contains only "-", it renders "DEPENDS-ON: -".
// If dependencies are provided, it renders "DEPENDS-ON: a, b".
func FormatDependsOn(deps []string) string {
	val := FormatDependsOnValue(deps)
	return "DEPENDS-ON: " + val
}

// FormatDependsOnValue formats the value portion of DEPENDS-ON.
// If deps is empty or contains only "-", it returns "-".
// If dependencies are provided, it returns "a, b".
func FormatDependsOnValue(deps []string) string {
	var items []string
	for _, d := range deps {
		for _, part := range strings.Split(d, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				items = append(items, part)
			}
		}
	}
	if len(items) == 0 || (len(items) == 1 && items[0] == "-") {
		return "-"
	}
	return strings.Join(items, ", ")
}

// ApplyDependsOn applies the DEPENDS-ON header declaration to card text:
//   - If DEPENDS-ON: already exists, it is replaced in-place.
//   - If <depends-on> placeholder exists, it is replaced.
//   - Otherwise, DEPENDS-ON: is inserted immediately after PATHS: if present,
//     or immediately after TEST: if present.
//   - If neither PATHS: nor TEST: is present, the card is returned untouched.
func ApplyDependsOn(text string, deps []string) string {
	depLine := FormatDependsOn(deps)
	depVal := FormatDependsOnValue(deps)

	if strings.Contains(text, "<depends-on>") {
		text = strings.ReplaceAll(text, "DEPENDS-ON: <depends-on>", depLine)
		text = strings.ReplaceAll(text, "<depends-on>", depVal)
	}

	crlf := strings.Contains(text, "\r\n")
	newline := "\n"
	if crlf {
		newline = "\r\n"
	}

	rawLines := strings.Split(text, "\n")
	lines := make([]string, len(rawLines))
	for i, l := range rawLines {
		lines[i] = strings.TrimRight(l, "\r")
	}

	dependsOnIdx := -1
	pathsIdx := -1
	testIdx := -1

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "DEPENDS-ON:") {
			dependsOnIdx = i
			break
		}
		if pathsIdx == -1 && strings.HasPrefix(trimmed, "PATHS:") {
			pathsIdx = i
		}
		if testIdx == -1 && strings.HasPrefix(trimmed, "TEST:") {
			testIdx = i
		}
	}

	if dependsOnIdx != -1 {
		lines[dependsOnIdx] = depLine
		return strings.Join(lines, newline)
	}

	if pathsIdx != -1 {
		insertIdx := pathsIdx + 1
		newLines := make([]string, 0, len(lines)+1)
		newLines = append(newLines, lines[:insertIdx]...)
		newLines = append(newLines, depLine)
		newLines = append(newLines, lines[insertIdx:]...)
		return strings.Join(newLines, newline)
	}

	if testIdx != -1 {
		insertIdx := testIdx + 1
		newLines := make([]string, 0, len(lines)+1)
		newLines = append(newLines, lines[:insertIdx]...)
		newLines = append(newLines, depLine)
		newLines = append(newLines, lines[insertIdx:]...)
		return strings.Join(newLines, newline)
	}

	return text
}

// RenderTemplate renders one candidate's card from its template and returns the card text,
// or a reason to refuse it.
func RenderTemplate(in CutTemplateInput) (string, string) {
	rendered := strings.NewReplacer(
		"<label>", in.Row.ID,
		"<source>", in.Row.Source,
		"<id>", in.Row.ID,
		"<kind>", in.Row.Kind,
		"<title>", in.Row.Title,
		"<branch>", branchOf(in.Row),
	).Replace(in.Template)

	rendered = ApplyDependsOn(rendered, in.DependsOn)

	lines := strings.Split(rendered, "\n")
	line1 := strings.TrimSpace(lines[0])
	if (!strings.HasPrefix(line1, "RESULT ") && !strings.HasPrefix(line1, "RESULT:")) || !strings.Contains(line1, "sha=") {
		return "", "rule 5: line 1 is not the RESULT contract (line 1 must be `RESULT <label> sha=<sha12>`)"
	}
	if strings.Contains(rendered, "../scratch") {
		return "", "rule 5: the card mentions ../scratch (scratch notes live in the repo directory as notes.txt)"
	}
	step1, ok := stepOne(lines)
	if !ok {
		return "", "rule 5: no STEP 1 line (a card wants STEP 1 as the mkdir/clone/checkout)"
	}
	for _, want := range []string{"mkdir -p scratch", "checkout -b"} {
		if !strings.Contains(step1, want) {
			return "", fmt.Sprintf("rule 5: STEP 1 lacks %q (STEP 1 must carry mkdir -p scratch and checkout -b)", want)
		}
	}
	if strings.Contains(step1, "TMPDIR") {
		return "", "rule 5: STEP 1 sets TMPDIR (the runner exports TMPDIR outside every repo; a card sets none of its own)"
	}
	if strings.Contains(step1, "git@") || !strings.Contains(step1, "https://") {
		return "", "rule 5: STEP 1 does not clone over https (the clone URL is https, never git@)"
	}
	if steps := countSteps(lines); steps > turnBudget(in.Row.Kind) {
		return "", fmt.Sprintf("the card has %d steps, over the %d-turn budget (#855; the step count is the turn budget: name the exact file and line range, one check per step)", steps, turnBudget(in.Row.Kind))
	}
	if textKinds[in.Row.Kind] {
		if !strings.Contains(strings.ToLower(rendered), "do not run go build") {
			return "", "rule 6: the text template lacks the no-build line (read, text and tone cards must state `Do not run go build, go test or any toolchain`)"
		}
	} else if in.Row.Kind == "fix" || in.Row.Kind == "replay" || in.Row.Kind == "drift" {
		if !strings.Contains(strings.ToLower(rendered), "red line") || !strings.Contains(strings.ToLower(rendered), "green line") {
			return "", "rule 6: the writing template lacks the red-then-green row rule (fix, replay and drift cards must name the red line and the green line)"
		}
	} else if !strings.Contains(strings.ToLower(rendered), "red line") || !strings.Contains(strings.ToLower(rendered), "green line") {
		return "", "rule 6: the writing template lacks the red-then-green row rule (fix, replay and drift cards must name the red line and the green line)"
	}
	body := strings.Join(lines[1:], "\n")
	sum := sha256.Sum256([]byte(body))
	if strings.HasPrefix(line1, "RESULT:") {
		lines[0] = fmt.Sprintf("RESULT: %s sha=%s", in.Row.ID, hex.EncodeToString(sum[:])[:12])
	} else {
		lines[0] = fmt.Sprintf("RESULT %s sha=%s", in.Row.ID, hex.EncodeToString(sum[:])[:12])
	}
	return strings.Join(lines, "\n"), ""
}

// RenderCardTemplate is an alias for RenderTemplate.
var RenderCardTemplate = RenderTemplate
