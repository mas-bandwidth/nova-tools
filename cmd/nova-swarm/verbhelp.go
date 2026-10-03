package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
)

// exitParagraph is the banner's exit codes, every verb's at once: `nova-swarm help` prints
// it whole, and a verb's -h prints that verb's own line in its place (verbExits; docs/
// STANDARD.md section 2, "Every verb's -h quotes the table, or the verb's own"; tool
// ledger X9). It is the usage const's own text, and a test holds the two to one string.
const exitParagraph = `exit codes: 0 the verb ran and passed; 1 the verb ran and said NO -- a verification that failed, a lint that found a defect; 2 could not run:
a missing flag, an unreadable worker description, a key file that is
absent or empty, a bad invocation; 3 member: its binary was replaced on disk
(MEMBER STOP: its supervisor starts the new one; with children running it first
takes no new card and stops when the last is reported).`

// verbExit is each verb's own exit codes.
var verbExit = map[string]string{
	"step":          "exit codes: 0 every step run is ok, each on its STEP OK line (--remainder: the card printed); 1 a step failed, on its STEP FAILED line, and the steps after it were not run; 2 could not run: a missing flag, a card that cannot be read, whose tree has a finding or that is not script steps only, no wall and no --no-wall",
	"lint":          "exit codes: 0 the card is clean (a NOTE line is advice and changes nothing); 1 a drift, each on its LINT DRIFT line; 2 could not run: a missing flag, a file that cannot be read, a bad invocation",
	"verify":        "exit codes: 0 the result holds its contract; 1 it does not (the line says why); 2 could not run: a missing flag, a file that cannot be read, a receipt that cannot be written",
	"worker":        "exit codes: 0 WORKER OK; 1 the description was read and drifts, each on its WORKER DRIFT line; 2 it cannot be read, or a bad invocation",
	"worker check":  "exit codes: 0 WORKER OK; 1 the description was read and drifts, each on its WORKER DRIFT line; 2 it cannot be read, or a bad invocation",
	"member":        "exit codes: 0 it stopped as asked (--once, --ticks); 2 could not run: a missing flag, a directory that cannot be made; 3 its binary was replaced on disk (MEMBER STOP: its supervisor starts the new one; with children running it first takes no new card and stops when the last is reported)",
	"native":        "exit codes: 0 the child exited 0 (the NATIVE line's OK, or INCOMPLETE and its why=, is the verdict); 1 the child was killed (its deadline, a TERM) or exited 255; any other code is the child's own; 2 could not run: a missing flag, a wall, a card or a worker description that is not there",
	"slots take":    "exit codes: 0 the leases are granted; 2 refused: the owner's share or the bench is full (SLOTS REFUSED names the holders), a missing flag, or a store that cannot be read",
	"doctor":        "exit codes: 0 the binaries agree, or there is one to read; 2 they drift, one shadows the other, or one cannot be read (the DOCTOR line says which)",
	"disk-guard":    "exit codes: 0 DISK-GUARD OK, everything it looked at done (a KEPT line is a refusal it means); 1 DISK-GUARD INCOMPLETE, something could not be read or removed (each on its NOTE line); 2 could not run: a bad flag",
	"slots release": "exit codes: 0 the leases named are freed; 2 a lease's holder still runs (SLOTS KEPT; --force frees it), a missing flag or a store that cannot be read",
}

// commonExit is the codes of every other verb.
const commonExit = "exit codes: 0 done; 2 could not run: a missing flag, a file that cannot be read, a bad invocation"

// verbExits is the exit-code paragraph a verb's -h prints.
func verbExits(name string) string {
	if e, ok := verbExit[name]; ok {
		return e
	}
	return commonExit
}

// recoverHelp is verbflag.RecoverWith with the verb's own exit codes in place of the
// banner's paragraph: a verb's -h (or help <verb>) prints its usage quoted from the
// banner, its example, its flags and its exit codes on stdout, exit 0. It is deferred
// directly, as RecoverWith is.
func recoverHelp(out io.Writer, code *int) {
	r := recover()
	if r == nil {
		return
	}
	h, ok := r.(verbflag.Help)
	if !ok {
		panic(r)
	}
	name := verbflag.Verb("nova-swarm", h.FS)
	var b strings.Builder
	verbflag.Print(&b, "nova-swarm", strings.Replace(usage, exitParagraph, verbExits(name), 1), h.FS)
	*code = 0
	help := verbflag.Insert(b.String(), verbHelpLines(name))
	if _, err := fmt.Fprint(out, help); err != nil {
		// the help did not reach its reader (a closed stdout): the exit code says so
		*code = 1
	}
}
