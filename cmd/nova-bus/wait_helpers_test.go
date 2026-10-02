//go:build functional || slow || perf

// Helpers the functional, slow and perf tiers share. Every test that calls them starts
// git over a real bus checkout, so the unit tier builds none of them; the constraint is
// wider than functional because slow_test.go and timing_test.go call them too.

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// waitFlags is the invocation the tests share; extra flags follow it.
//
// It carries --open, which is the flag a caller of this verb wants and which `wait`
// honours exactly as `inbox` does: without it the run prints ONE line for what is being
// carried, because a reader carrying five hundred settled notes does not want five hundred
// lines on every poll. A wait returns because there is news, and the caller's next action
// is the note, so the note is listed.
func waitFlags(checkout, who, timeout string, extra ...string) []string {
	return append([]string{
		"wait", "--bus", checkout, "--as", who, "--receipt-max-words", "40",
		"--timeout", timeout, "--interval", "100ms", "--open",
		"--remote", "origin", "--branch", "main", "--attempts", "3",
	}, extra...)
}

// settled gives Ada a cursor with the fixture's two old notes behind a switch-day line, so
// that the waits below are incremental reads with an empty open list -- a reader who is up
// to date, which is the reader a wait is for.
func settled(t *testing.T, checkout string) {
	t.Helper()
	invoke(t, "", advance(checkout, "Ada", "--legacy-before", "2026-09-09")...).
		mustCode(t, 0).mustContain(t, "stdout", "INBOX CURSOR commit=")
}

// afterOf is how long the TOOL says a wait took, read off its own closing line.
//
// The claim "this wait did not come back before its deadline" used to be made with
// time.Since around the call, and that is the harness measuring the harness: a runner that
// parks the test goroutine after the call returns inflates it, and one that parks it before
// the call starts is the flake in the other direction. `after=` is the verb's own number,
// taken between its own start and its own return, so the assertion is about the verb.
func afterOf(t *testing.T, stdout string) time.Duration {
	t.Helper()
	i := strings.Index(stdout, "WAIT TIMEOUT")
	if i < 0 {
		i = strings.Index(stdout, "WAIT OK")
	}
	require.Falsef(t, i < 0, "no WAIT TIMEOUT or WAIT OK line to read after= from:\n%s", stdout)
	d, err := time.ParseDuration(field(t, stdout[i:], "after="))
	require.NoErrorf(t, err, "after= is not a duration: %v\n%s", err, stdout)
	return d
}
