//go:build functional

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestNativePrintsTheJobsSpend: a launch through the fake harness's real database prints
// the job's spend= word on its NATIVE line, the classes as the store holds them and the
// harness's cost, and a harness that reported nothing prints none.
func TestNativePrintsTheJobsSpend(t *testing.T) {
	t.Parallel()

	needsSQLite(t)
	rc, stdout, stderr := runBudgetCard(t, "50000", "FAKE-USAGE-DB 100 50 9000 90000 7 0.5\nFAKE-FINDINGS 1\n")
	assert.Zero(t, rc, stderr)
	assert.Contains(t, nativeOKLine(t, stdout), " spend=input:100,cache_read:90000,cache_write:9000,output:50,reasoning:7,requests:1,max_prompt:99100,cost:0.5,model:fake/fake-model")
	rc, stdout, stderr = runBudgetCard(t, "50000", "FAKE-FINDINGS 1\n")
	assert.Zero(t, rc, stderr)
	assert.NotContains(t, nativeOKLine(t, stdout), " spend=", "nothing reported, nothing spent on the line")
}
