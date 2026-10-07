package selftalk

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Names returns the shape names from a set of findings.
func Names(text string) []string {
	var out []string
	for _, c := range Scan(text) {
		if c.Verdict == Standing {
			out = append(out, string(Standing))
		}
	}
	for _, i := range ScanInstallation(text) {
		out = append(out, string(i.Shape))
	}
	return out
}

// AssertEmpty asserts that the scanner found nothing.
func AssertEmpty(t *testing.T, got []Claim) {
	t.Helper()
	assert.Empty(t, got)
}

// AssertEmptyInstallation asserts that the installation scanner found nothing.
func AssertEmptyInstallation(t *testing.T, got []Installation) {
	t.Helper()
	assert.Empty(t, got)
}

// AssertNotEmpty asserts that the scanner found something.
func AssertNotEmpty(t *testing.T, got []Claim) {
	t.Helper()
	assert.NotEmpty(t, got)
}

// AssertNotEmptyInstallation asserts that the installation scanner found something.
func AssertNotEmptyInstallation(t *testing.T, got []Installation) {
	t.Helper()
	assert.NotEmpty(t, got)
}
