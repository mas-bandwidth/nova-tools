// issue #1451: nova-post's refusals that come from an ENGINE error -- the ones
// fail builds from a *post.Refusal, and the flag-parse errors -- carried no door
// at all, because only the hand-written remedies named it. ONBOARDING point 1
// fixes the sentence: an invocation the tool cannot run prints one line ending
// in `; run: nova-post help` and exits 2. A strings.Contains check (what
// firstrun_test.go:55 does) cannot see that: a line that says "refusing to
// guess" and dumps the whole usage still contains the token, and a doubled door passes.
package main

import (
	"strings"
	"testing"
)

func TestIssue1451EveryRefusalNamesTheDoor(t *testing.T) {
	// door is the ONBOARDING point 1 shape: the line ENDS with it. doorWord is
	// the same phrase without the separator, counted so a second copy -- the
	// `, run:` hand-written ones left in place beside the appended `; run:` --
	// is a failure rather than an invisible extra.
	const door = "; run: nova-post help"
	const doorWord = "run: nova-post help"

	// The verbs come from the tool's own help banner, not from memory.
	code, banner, helpErr := cli("help")
	if code != 0 {
		t.Fatalf("`nova-post help` exit=%d, want 0; stderr: %q", code, helpErr)
	}
	var verbs []string
	seen := map[string]bool{}
	for _, line := range strings.Split(banner, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "nova-post" && !seen[f[1]] {
			seen[f[1]] = true
			verbs = append(verbs, f[1])
		}
	}
	if len(verbs) == 0 {
		t.Fatal("the help banner names no verbs")
	}

	type invocation struct {
		what string
		args []string
	}
	cases := []invocation{{"bare invocation", nil}}
	for _, v := range verbs {
		cases = append(cases, invocation{"bare " + v, []string{v}})
	}
	cases = append(cases,
		invocation{"unknown verb", []string{"no-such-verb"}},
		// The door-less half: refusals fail builds from an engine error, and
		// the flag parser's own errors. These have no hand-written door.
		invocation{"engine bad-channel", []string{"draft", "--channel", "bogus", "--target", "x", "--drafts", "d", "--allowlist", "a"}},
		invocation{"engine bad-flag", []string{"draft", "--no-such-flag"}},
	)

	for _, tc := range cases {
		t.Run(tc.what, func(t *testing.T) {
			got, stdout, stderr := cli(tc.args...)
			if got == 0 {
				// A bare verb that is a valid invocation (version, help) is
				// not a refusal; it must not pretend to be one.
				if strings.Contains(stderr, "POST REFUSED") {
					t.Fatalf("%s exits 0 but refused: %q", tc.what, stderr)
				}
				return
			}
			if got != 2 {
				t.Fatalf("%s exits %d, want 2; stdout=%q stderr=%q", tc.what, got, stdout, stderr)
			}
			if strings.TrimSpace(stderr) == "" {
				t.Fatalf("%s exited 2 with no refusal on stderr", tc.what)
			}
			for _, line := range strings.Split(strings.TrimSuffix(stderr, "\n"), "\n") {
				if strings.TrimSpace(line) == "" {
					continue
				}
				if !strings.Contains(line, "POST REFUSED") {
					t.Errorf("%s: stderr line is not a refusal: %q", tc.what, line)
					continue
				}
				if !strings.HasSuffix(line, door) {
					t.Errorf("%s: refusal does not END in %q: %q", tc.what, door, line)
				}
				if n := strings.Count(line, doorWord); n != 1 {
					t.Errorf("%s: %q appears %d times, want exactly 1: %q", tc.what, doorWord, n, line)
				}
			}
		})
	}
}
