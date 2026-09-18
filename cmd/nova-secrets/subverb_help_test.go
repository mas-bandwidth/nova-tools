package main

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// TestEverySubVerbAnswersHelp is this binary's half of the class rule
// internal/ci's TestEverySubVerbFlagSetAnswersHelp states in source:
// `nova-secrets <verb> --help` prints THAT verb's usage on stdout and exits 0.
// It used to exit 2 (125 for exec) with `flag: help requested`, package flag's
// own sentinel text, because every verb here parses with a flag.ContinueOnError
// set whose output is io.Discard and nobody answered flag.ErrHelp. A dogfooder
// measured it across the family on 2026-09-18; internal/cliflags is the one
// answer.
//
// This binary is driven through its built executable rather than in process,
// because its verbs end in os.Exit and there is no seam short of the process.
func TestEverySubVerbAnswersHelp(t *testing.T) {
	bin := buildNovaSecrets(t)
	for _, verb := range []string{
		"exec", "names", "check", "gate", "keygen", "place", "placed", "seal",
		// A two-word verb: `seat` dispatches, `seat add` parses. Fields splits it
		// so the binary is handed the two words it expects.
		"seat add",
	} {
		for _, spelling := range []string{"--help", "-h"} {
			args := append(strings.Fields(verb), spelling)
			// exec reads its own flags before the `--` that starts the command,
			// so a help request for it is spelled the way a person would.
			if verb == "exec" {
				args = []string{verb, spelling, "--", "true"}
			}
			cmd := exec.Command(bin, args...)
			var out, errs strings.Builder
			cmd.Stdout, cmd.Stderr = &out, &errs
			code := 0
			if err := cmd.Run(); err != nil {
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					t.Fatalf("nova-secrets %v: %v", args, err)
				}
				code = exit.ExitCode()
			}
			assertSubVerbHelp(t, "nova-secrets", verb, spelling, code, out.String(), errs.String())
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
