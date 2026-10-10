package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNovaDeleteToolMeetsTheStandard(t *testing.T) {
	t.Parallel()
	require.Empty(t, newTool().Problems())
}
