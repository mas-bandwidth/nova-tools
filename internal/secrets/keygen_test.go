package secrets

import (
	"strings"
	"testing"
)

// Glenn ran `keygen` on the Air on 2026-09-18 and read its receipt as a failure
// (nova-tools#1393). The verb had succeeded: it printed the OK line FIRST and then
// three `SECRETS RULE` lines whose last one said the placeholder "stands unfilled".
// A reader reads the LAST line of a command's output, and the last line said
// something was unfilled. Nothing was wrong; the tool had simply left its verdict
// at the top and its homework at the bottom.
//
// So the order is the contract now: what is left to do comes first, the verdict
// comes LAST, and the line that says what is left to do calls itself a next step
// rather than a state of the world.

// TestKeygenPrintsTheOKLineLast pins the order. It takes no binary and no store:
// the assembly is a pure function precisely so this can never be skipped.
func TestKeygenPrintsTheOKLineLast(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		recoveryKey string
		placeholder bool
	}{
		{"with --store", "age1recovery", false},
		{"without --store", "<recovery key>", true},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lines := keygenLines("rowan", "/k/rowan.key", "age1pub", tc.recoveryKey, tc.placeholder)
			if len(lines) == 0 {
				t.Fatal("keygen printed nothing")
			}
			last := lines[len(lines)-1]
			if !strings.HasPrefix(last, "SECRETS KEYGEN OK ") {
				t.Errorf("the last line is not the verdict; a reader reads the last line:\n%s", strings.Join(lines, "\n"))
			}
			for i, l := range lines[:len(lines)-1] {
				if strings.HasPrefix(l, "SECRETS KEYGEN OK") {
					t.Errorf("line %d is a second OK line: %s", i, l)
				}
			}
			if !strings.HasPrefix(lines[0], "SECRETS RULE   creation_rules:") {
				t.Errorf("the rule block does not come first: %q", lines[0])
			}
		})
	}
}

// TestKeygenNextStepSaysItIsANextStep pins the wording. "placeholder ... stands
// unfilled" is a state; "NEXT: add these two lines" is an instruction, and only
// one of the two reads as an error at the end of a green run.
func TestKeygenNextStepSaysItIsANextStep(t *testing.T) {
	t.Parallel()
	const want = "SECRETS RULE NEXT: add these two lines to .sops.yaml (or run `nova-secrets seat add`)"
	for _, placeholder := range []bool{false, true} {
		lines := keygenLines("rowan", "/k/rowan.key", "age1pub", "age1recovery", placeholder)
		joined := strings.Join(lines, "\n")
		if !strings.Contains(joined, want) {
			t.Errorf("placeholder=%v: the receipt carries no next-step line\nwant: %s\ngot:\n%s", placeholder, want, joined)
		}
		// The next step is the last thing before the verdict, so the two lines a
		// reader's eye lands on are "here is what to do" and "this worked".
		if n := len(lines); n < 2 || lines[n-2] != want {
			t.Errorf("placeholder=%v: the next-step line is not immediately above the OK line:\n%s", placeholder, joined)
		}
	}
}

// TestKeygenPlaceholderNoteIsNotTheLastWord keeps the note that a `--store`-less run
// leaves the recovery key unfilled -- it is true and it matters -- while denying it
// the last line, which is the one Glenn read as the verdict.
func TestKeygenPlaceholderNoteIsNotTheLastWord(t *testing.T) {
	t.Parallel()
	lines := keygenLines("rowan", "/k/rowan.key", "age1pub", "<recovery key>", true)
	found := -1
	for i, l := range lines {
		if strings.Contains(l, "stands unfilled") {
			found = i
		}
	}
	if found < 0 {
		t.Fatalf("a run without --store no longer says the recovery key is unfilled:\n%s", strings.Join(lines, "\n"))
	}
	if found >= len(lines)-1 {
		t.Errorf("the placeholder note is the last line again:\n%s", strings.Join(lines, "\n"))
	}
	// And a run WITH a store never prints it at all.
	for _, l := range keygenLines("rowan", "/k/rowan.key", "age1pub", "age1recovery", false) {
		if strings.Contains(l, "stands unfilled") {
			t.Errorf("a run with --store still prints the placeholder note: %s", l)
		}
	}
}
