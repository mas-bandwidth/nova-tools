package main

import (
	"fmt"
	"strings"
	"testing"
)

// `<bus>/.nova-bus/defaults` used to be a file with ONE key in it. `receipt-max-words` was
// read from it and nothing else was, so a bus whose reader is habitually five thousand
// commits behind had to remember `--max-commits` by hand on every call, and forgetting it
// bought an `INBOX WALK bounded` line and no notes.
//
// That is the wrong shape for the same reason a one-key config file is always the wrong
// shape: the key that needs a default next is never the key somebody special-cased. So the
// file is now GENERAL -- any flag this verb takes may have its default there, under its own
// name -- and the precedence is the one every tool has: the flag wins, then the file, then
// (for the one flag that has one) the environment.

// busOfCommits hands back a checkout with n extra commits on top of the fixture and the
// commit it stood at before them, which is what a stale cursor points to.
func busOfCommits(t *testing.T, n int) (checkout, base string) {
	t.Helper()
	checkout, _ = busDir(t)
	base = strings.TrimSpace(gitIn(t, checkout, "rev-parse", "HEAD"))
	for i := 0; i < n; i++ {
		writeFile(t, checkout, fmt.Sprintf("from-bo/filler-%d.txt", i), fmt.Sprintf("%d\n", i))
		gitIn(t, checkout, "add", "-A")
		gitIn(t, checkout, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", fmt.Sprintf("filler %d", i))
	}
	return checkout, base
}

// TestAnyDocumentedFlagMayHaveItsDefaultInTheDefaultsFile is the generalisation, shown on
// the flag that needed it.
func TestAnyDocumentedFlagMayHaveItsDefaultInTheDefaultsFile(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, base := busOfCommits(t, 4)
	writeFile(t, checkout, "from-ada/CURSOR", base+" 2026-09-18T12:00:00Z open=0\n")

	// THE FILE SUPPLIES IT. A bound of one, from the file alone, stops a cursor four
	// commits back.
	writeFile(t, checkout, ".nova-bus/defaults", "# this bus's reader is habitually behind\nmax-commits=1\n")
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40").
		mustCode(t, 0).
		mustContain(t, "stderr", "INBOX WALK bounded commits=1 ")

	// THE FLAG WINS. The same file, with the bound named on the line: the walk runs.
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--max-commits", "500").
		mustCode(t, 0).
		mustContain(t, "stderr", "INBOX WALK commits=4/4 ")

	// THE ONE KEY THAT WAS ALWAYS THERE still works, through the general path.
	writeFile(t, checkout, ".nova-bus/defaults", "receipt-max-words=40\nmax-commits=500\n")
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada").
		mustCode(t, 0).
		mustContain(t, "stderr", "INBOX WALK commits=4/4 ")

	// A VALUE THE FLAG WILL NOT TAKE IS A REFUSAL, at exit 2, naming the file, the line
	// and the key. A default nobody can use is a bad invocation written down, and a tool
	// that passed over it would read the bus with a number the owner did not choose.
	writeFile(t, checkout, ".nova-bus/defaults", "receipt-max-words=40\nmax-commits=lots\n")
	r := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada").mustCode(t, 2)
	for _, want := range []string{".nova-bus/defaults", "line 2", "max-commits"} {
		if !strings.Contains(r.stderr, want) {
			t.Fatalf("the refusal does not name %q:\n%s", want, r.stderr)
		}
	}

	// A LINE THAT IS NOT `<flag>=<value>` is the same refusal: a defaults file is read, so
	// a line in it that says nothing is a mistake somebody should see.
	writeFile(t, checkout, ".nova-bus/defaults", "max-commits 500\n")
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40").
		mustCode(t, 2).
		mustContain(t, "stderr", ".nova-bus/defaults")

	// A KEY THIS VERB DOES NOT TAKE IS PASSED OVER, and this is deliberate: ONE file
	// serves every verb on the bus, so `inbox`'s keys sit beside `send`'s and neither may
	// refuse the other's.
	writeFile(t, checkout, ".nova-bus/defaults", "receipt-max-words=40\nmax-commits=500\nattempts=7\n")
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada").
		mustCode(t, 0).
		mustContain(t, "stderr", "INBOX WALK commits=4/4 ")
}

// TestABoundedWalkSaysHowFarBehindTheCursorIs is the other half of the same report: the
// run printed `INBOX WALK bounded commits=500` and no notes, and a reader who did not
// already know their cursor was five thousand commits back had no way to tell that line
// from a quiet bus. The line now says whose cursor, that it is behind by MORE than the
// bound, that nothing was read, and what to do -- all on the one line, because a remedy
// on a second line is a remedy somebody's grep drops.
func TestABoundedWalkSaysHowFarBehindTheCursorIs(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, base := busOfCommits(t, 4)
	writeFile(t, checkout, "from-ada/CURSOR", base+" 2026-09-18T12:00:00Z open=0\n")

	r := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--max-commits", "2").
		mustCode(t, 0)
	for _, want := range []string{
		"INBOX WALK bounded commits=2",
		"cursor=" + base,
		"behind=more-than-2",
		"notes=0",
		`remedy="raise --max-commits or close --before <instant>"`,
	} {
		if !strings.Contains(r.stderr, want) {
			t.Fatalf("the bounded line does not carry %q:\n%s", want, r.stderr)
		}
	}
	// One line, not two: everything above is on the same one.
	var bounded string
	for _, l := range strings.Split(r.stderr, "\n") {
		if strings.HasPrefix(l, "INBOX WALK bounded") {
			if bounded != "" {
				t.Fatalf("two bounded lines:\n%s", r.stderr)
			}
			bounded = l
		}
	}
	for _, want := range []string{"cursor=" + base, "behind=more-than-2", "notes=0", "remedy="} {
		if !strings.Contains(bounded, want) {
			t.Fatalf("%q is not on the bounded line itself: %s", want, bounded)
		}
	}
}
