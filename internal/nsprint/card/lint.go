package card

import (
	"regexp"
	"strings"
)

type Finding struct {
	Check string
	Line  int
	Text  string
}

// LintCard lints card text.
func LintCard(raw string) []Finding {
	var findings []Finding
	lines := strings.Split(raw, "\n")

	// Find kind
	kind := "model"
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(strings.ToUpper(trimmed), "KIND:") {
			kind = strings.TrimSpace(trimmed[5:])
			break
		}
	}

	checkBound := CheckBound(kind)

	hasCheck := false
	hasExpect := false
	checkLine := -1

	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(strings.ToUpper(trimmed), "CHECK:") {
			hasCheck = true
			checkLine = i + 1
		}
		if strings.HasPrefix(strings.ToUpper(trimmed), "EXPECT:") {
			hasExpect = true
			expectVal := strings.TrimSpace(trimmed[7:])
			if _, err := regexp.Compile(expectVal); err != nil {
				findings = append(findings, Finding{Check: "EXPECT is not a regexp", Line: i + 1, Text: l})
			}
		}
	}

	if checkBound {
		if !hasCheck {
			findings = append(findings, Finding{Check: "missing CHECK", Line: 1, Text: ""})
		}
		if hasCheck && !hasExpect {
			findings = append(findings, Finding{Check: "missing EXPECT", Line: checkLine, Text: ""})
		}
	}

	return findings
}
