package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// resume takes --stream again or comma separated, with one --did for all:
// each stream is resumed or refused on its own line, and the exit is 1 when
// any is refused (docs/SPEC-SPRINT.md, "resume-many-streams-b").
func TestResumeTakesSeveralStreams(t *testing.T) {
	t.Parallel()
	stopped := func(ta *testApp, s string) {
		t.Helper()
		ta.ok("init --readers reader-a --members m1")
		ta.ok("add --stream " + s + " " + s + "-1 --one")
		ta.ok("start")
		ta.ok("tick")
		ta.ok("take --as m1 --limit 1")
		ta.ok("finish --as m1 " + s + "-1.w1@1")
		ta.ok("ask")
		ta.ok("read --as reader-a --ok --limit 5")
		ta.ok("accept --stream " + s)
		ta.ok("merge --stream " + s + " --conflict " + s + "-1")
	}

	// comma separated: s1 resumes on its line, nope is refused on its own, exit 1
	ta := newTestApp(t)
	stopped(ta, "s1")
	code, out, errs := ta.do("resume --stream s1,nope --did 'rebased s1-1'")
	require.Equal(t, 1, code, "any refused stream is exit 1: %s%s", out, errs)
	require.Contains(t, out, "RESUME", "s1 is resumed, on its own line: %s", out)
	require.Contains(t, out+errs, "nope", "the refused stream is named on its line: %s%s", out, errs)

	// the flag given again: s1 resumes, nope2 is refused, exit 1
	ta2 := newTestApp(t)
	stopped(ta2, "s1")
	code, out, errs = ta2.do("resume --stream s1 --stream nope2 --did 'rebased s1-1'")
	require.Equal(t, 1, code, "any refused stream is exit 1: %s%s", out, errs)
	require.Contains(t, out, "RESUME", "s1 is resumed once: %s", out)
	require.Contains(t, out+errs, "nope2", "the refused stream is named: %s%s", out, errs)

	// the brief's case: a is running and b is stopped; b resumes, a is
	// refused on its own line, exit 1
	ta3 := newTestApp(t)
	ta3.ok("init --readers reader-a --members m1")
	ta3.ok("add --stream a a-1 --one")
	ta3.ok("add --stream b b-1 --one")
	ta3.ok("start")
	ta3.ok("tick")
	ta3.ok("take --as m1 b-1.w1@1")
	ta3.ok("finish --as m1 b-1.w1@1")
	ta3.ok("ask")
	ta3.ok("read --as reader-a --ok --limit 5")
	ta3.ok("accept --stream b")
	ta3.ok("merge --stream b --conflict b-1")
	code, out, errs = ta3.do("resume --stream a,b --did 'rebased b-1'")
	require.Equal(t, 1, code, "a running stream is refused, exit 1: %s%s", out, errs)
	require.Contains(t, out, "RESUME", "b is resumed on its own line: %s", out)
	require.Contains(t, out, "MOVED stream b stopped -> merging", "b moves on its own line: %s", out)
	require.Contains(t, errs, "REFUSED a: not stopped", "a is refused on its own line: %s", errs)
	ta3.ok("merge --stream b") // b runs again

	ta.clean()
	ta2.clean()
	ta3.clean()
}
