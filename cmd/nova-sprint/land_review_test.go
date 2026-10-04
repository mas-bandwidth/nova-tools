package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLandReviewDifferentRepositoriesHaveDifferentCloneNames(t *testing.T) {
	t.Parallel()
	assert.NotEqual(t, repoDirName("https://example.invalid/a-b/c.git"), repoDirName("https://example.invalid/a/b-c.git"))
}
