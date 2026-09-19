package secrets

import (
	"strings"
	"testing"
)

// Glenn ran `keygen` on the Air on 2026-09-18 and read its receipt as a failure
// (nova-tools#1393). The verb had succeeded, but the line a reader's eye lands on -- the
// last one -- said the placeholder "stands unfilled", and the OK line sat at the top in
// the same machine-readable shape as the rest. Nothing was wrong; the tool had left its
// verdict where a person does not look.
//
// So the shape is the contract now: the machine-readable lines keep their place for the
// callers that parse them, and the run closes with a plain line a human cannot misread --
// it worked, where the key is, and the next step -- and a NOTE that cannot be mistaken
// for a refusal.

// TestKeygenEndsWithThePlainClosingLine pins the last two lines. It takes no binary and
// no store: the assembly is a pure function precisely so this can never be skipped.
func TestKeygenEndsWithThePlainClosingLine(t *testing.T) {
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
			if len(lines) < 2 {
				t.Fatal("keygen printed fewer than two lines")
			}
			last := lines[len(lines)-1]
			if last != "Next: send this public key to whoever seals your seat: age1pub" {
				t.Errorf("the last line is not the plain closing line:\n%s", strings.Join(lines, "\n"))
			}
			if !strings.HasPrefix(lines[len(lines)-2], "Done. Your new key is at ") {
				t.Errorf("the second-to-last line does not say it worked and where the key is:\n%s", strings.Join(lines, "\n"))
			}
			// The machine-readable OK line stays for callers that parse it.
			found := false
			for _, l := range lines {
				if strings.HasPrefix(l, "SECRETS KEYGEN OK ") {
					found = true
				}
			}
			if !found {
				t.Errorf("the machine-readable OK line is gone:\n%s", strings.Join(lines, "\n"))
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
	}
}

// TestKeygenPlaceholderNoteCannotBeReadAsARefusal keeps the note that a --store-less run
// leaves the recovery key as a placeholder -- it is true and it matters -- while denying
// it the last line and any wording that reads as a failure.
func TestKeygenPlaceholderNoteCannotBeReadAsARefusal(t *testing.T) {
	t.Parallel()
	lines := keygenLines("rowan", "/k/rowan.key", "age1pub", "<recovery key>", true)
	found := -1
	for i, l := range lines {
		if strings.Contains(l, "placeholder:") {
			found = i
		}
	}
	if found < 0 {
		t.Fatalf("a run without --store no longer carries the placeholder note:\n%s", strings.Join(lines, "\n"))
	}
	if found >= len(lines)-1 {
		t.Errorf("the placeholder note is the last line again:\n%s", strings.Join(lines, "\n"))
	}
	if strings.Contains(lines[found], "unfilled") {
		t.Errorf("the placeholder note still reads as a failure: %s", lines[found])
	}
	// And a run WITH a store never prints it at all.
	for _, l := range keygenLines("rowan", "/k/rowan.key", "age1pub", "age1recovery", false) {
		if strings.Contains(l, "placeholder:") {
			t.Errorf("a run with --store still prints the placeholder note: %s", l)
		}
	}
}
