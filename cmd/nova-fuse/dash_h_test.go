package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDashHPrintsHelpAtExitZero(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"check", "init"} {
		code, out, errs := runFuse(t, verb, "-h")
		assert.Equal(t, 0, code, "%s -h exit code", verb)
		assert.Empty(t, errs, "%s -h stderr", verb)
		assert.True(t, strings.Contains(out, "usage: nova-fuse "+verb), "%s -h prints the verb's usage line", verb)
	}
}
