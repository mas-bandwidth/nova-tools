package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// nova-check's definition meets the standard its banner and help cannot hold
// by construction: every verb's effect, and a how text of five short lines.
func TestCheckToolMeetsTheStandard(t *testing.T) {
	t.Parallel()
	assert.Empty(t, novaCheck().Problems())
}
