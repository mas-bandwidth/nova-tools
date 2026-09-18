package main

import (
	"bytes"
	"strings"
	"testing"
)

// nova-self-talk has no sub-verbs: its one invocation takes the flags itself.
// The class rule is the same shape all the same -- a request for help is
// answered on stdout at exit 0, never refused with package flag's own
// `flag: help requested` (internal/cliflags).
func TestTheVerblessInvocationAnswersHelp(t *testing.T) {
	for _, spelling := range []string{"--help", "-h"} {
		var out, errs bytes.Buffer
		if code := run([]string{spelling}, &out, &errs); code != 0 {
			t.Errorf("nova-self-talk %s: exit %d, want 0; stderr: %s", spelling, code, errs.String())
		}
		if !strings.Contains(out.String(), "nova-self-talk") {
			t.Errorf("nova-self-talk %s: stdout carries no usage:\n%s", spelling, out.String())
		}
		if strings.TrimSpace(errs.String()) != "" {
			t.Errorf("nova-self-talk %s: a question wrote to stderr: %q", spelling, errs.String())
		}
	}
}
