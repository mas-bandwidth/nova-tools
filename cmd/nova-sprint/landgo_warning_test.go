package main

// The tree gate's finding names the failing diagnostic before the go toolchain's own
// warning lines. The toolchain prints "warning: ..." (its GOPATH/GOROOT install warning
// among them) on the stream a build or vet diagnostic arrives on, so gateWhy keeps every
// line but moves the warnings after the rest, each group in order, and only warnings
// remain as the toolchain wrote them.

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	goPrefixWarning = "go: warning: both GOPATH and GOROOT are the same directory (/usr/local/go)"
	toolchainWarn   = "warning: both GOPATH and GOROOT are the same directory (/usr/local/go)"
	vetFinding      = `bad.go:5:26: fmt.Printf format %d has arg "s" of wrong type string`
)

// TestGateWhyToolchainWarningBeforeDiagnostic: a warning line ahead of a vet diagnostic
// gives the diagnostic first, the warning after, and each group keeps its order.
func TestGateWhyToolchainWarningBeforeDiagnostic(t *testing.T) {
	t.Parallel()
	assert.Equal(t,
		"go vet ./...: exit status 1: "+vetFinding+" | "+toolchainWarn,
		gateWhy([]string{"go", "vet", "./..."}, errors.New("exit status 1"),
			toolchainWarn+"\n"+vetFinding+"\n"))
	assert.Equal(t,
		"go vet ./...: exit status 1: one | two | "+toolchainWarn+" | "+goPrefixWarning,
		gateWhy([]string{"go", "vet", "./..."}, errors.New("exit status 1"),
			toolchainWarn+"\none\n"+goPrefixWarning+"\ntwo\n"))
}

// TestGateWhyToolchainWarningGoPrefix: a "go: warning: " line is moved after the
// diagnostic the same way.
func TestGateWhyToolchainWarningGoPrefix(t *testing.T) {
	t.Parallel()
	assert.Equal(t,
		"go build ./...: exit status 1: "+vetFinding+" | "+goPrefixWarning,
		gateWhy([]string{"go", "build", "./..."}, errors.New("exit status 1"),
			goPrefixWarning+"\n"+vetFinding+"\n"))
}

// TestGateWhyToolchainWarningOnly: output that holds only warnings is joined as today.
func TestGateWhyToolchainWarningOnly(t *testing.T) {
	t.Parallel()
	assert.Equal(t,
		"go vet ./...: exit status 1: "+toolchainWarn+" | "+goPrefixWarning,
		gateWhy([]string{"go", "vet", "./..."}, errors.New("exit status 1"),
			toolchainWarn+"\n"+goPrefixWarning+"\n"))
}

// TestGateWhyToolchainWarningNoWarning: output with no warning is byte-identical to today,
// the shape TestTreeGateWords pins at land_go_test.go:59.
func TestGateWhyToolchainWarningNoWarning(t *testing.T) {
	t.Parallel()
	assert.Equal(t,
		"go vet ./...: exit status 1: # example.com/m | ./bad.go:5:2: Printf format %d has arg \"s\" of wrong type string",
		gateWhy([]string{"go", "vet", "./..."}, errors.New("exit status 1"),
			"# example.com/m\n./bad.go:5:2: Printf format %d has arg \"s\" of wrong type string\n\n"))
}

// TestGateWhyToolchainWarningSurvivesCap: the diagnostic survives oneline.Cap's 1500-byte
// ceiling when a long warning would otherwise push it out.
func TestGateWhyToolchainWarningSurvivesCap(t *testing.T) {
	t.Parallel()
	got := gateWhy([]string{"go", "vet", "./..."}, errors.New("exit status 1"),
		"warning: "+strings.Repeat("x", 2000)+"\n"+vetFinding+"\n")
	assert.Contains(t, got, vetFinding)
}
