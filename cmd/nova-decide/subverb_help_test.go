package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestEverySubVerbAnswersHelp is this package's half of the class rule
// internal/ci's TestEverySubVerbFlagSetAnswersHelp states in source: `nova-decide
// <verb> --help` prints THAT verb's usage on stdout and exits 0. It used to
// exit 2 with `flag: help requested`, package flag's own sentinel text, because
// every verb here parses with a flag.ContinueOnError set whose output is
// io.Discard and nobody answered flag.ErrHelp. A dogfooder measured it across
// the family on 2026-09-18; internal/cliflags is the one answer.
func TestEverySubVerbAnswersHelp(t *testing.T) {
	// The ladder verbs (route, help, log) landed on dev while this was being
	// written and arrived with the same hurt; they are named here with tune.
	for _, verb := range []string{"tune", "route", "help", "log"} {
		for _, spelling := range []string{"--help", "-h"} {
			var out, errs bytes.Buffer
			code := run([]string{verb, spelling}, &out, &errs)
			assertSubVerbHelp(t, "nova-decide", verb, spelling, code, out.String(), errs.String())
		}
	}
}

// The verbless invocation is a verb too: `nova-decide --help` is how somebody
// asks what the tool itself takes, and the whole banner is the right answer to
// that one.
func TestTheVerblessInvocationAnswersHelp(t *testing.T) {
	for _, spelling := range []string{"--help", "-h"} {
		var out, errs bytes.Buffer
		if code := run([]string{spelling}, &out, &errs); code != 0 {
			t.Errorf("nova-decide %s: exit %d, want 0; stderr: %s", spelling, code, errs.String())
		}
		if !strings.Contains(out.String(), "nova-decide --questions") {
			t.Errorf("nova-decide %s: stdout carries no usage:\n%s", spelling, out.String())
		}
	}
}

// assertSubVerbHelp is the whole contract: exit 0, that verb's usage on stdout,
// no other verb's usage with it, and nothing on stderr -- an answer, not a
// complaint.
func assertSubVerbHelp(t *testing.T, tool, verb, spelling string, code int, stdout, stderr string) {
	t.Helper()
	if code != 0 {
		t.Errorf("%s %s %s: exit %d, want 0; stderr: %s", tool, verb, spelling, code, stderr)
		return
	}
	// The usage blocks pad verbs into columns, so every comparison is made on
	// the words, not on the spacing between them.
	words := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	if !strings.Contains(words(stdout), tool+" "+verb) {
		t.Errorf("%s %s %s: stdout carries no usage for the verb:\n%s", tool, verb, spelling, stdout)
	}
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		flat := words(line)
		if !strings.Contains(flat, tool+" ") || strings.Contains(flat, tool+" "+verb) {
			continue
		}
		t.Errorf("%s %s %s: answered with another verb's line: %q", tool, verb, spelling, line)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Errorf("%s %s %s: a question wrote to stderr: %q", tool, verb, spelling, stderr)
	}
}
