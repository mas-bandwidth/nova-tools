//go:build functional

package main

import "testing"

// TestLandReturnsAnEmptyCommitLandingToReview runs the same real landing path
// under the functional harness (docs/SPEC-SPRINT section 7).
func TestLandReturnsAnEmptyCommitLandingToReview(t *testing.T) {
	t.Parallel()
	exerciseEmptyCommitLanding(t)
}
