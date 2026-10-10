package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestToolMeetsTheStandard(t *testing.T) {
	t.Parallel()
	assert.Empty(t, newTool().Problems())
}
