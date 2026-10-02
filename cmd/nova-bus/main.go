// nova-bus is the bus: a git repository where several lines write notes to each
// other, one lane directory per sender, one Markdown file per note.
//
// It exists because the bus it was written for lost notes. A shared branch keyed by the
// clock races: two senders pushing in the same second meant one push was rejected and a
// line without the rebase reflex simply lost it; one sender writing twice in a minute
// collided on the filename; threads named by exact filename orphaned an answer when a slug
// was retyped; and a bare receipt and a note carrying a finding looked identical until
// opened. Every verb here is one of those failures closed:
//
//	draft     prints the header a note needs, with the names checked against the roster,
//	          so that a line's first send is not a header written from memory; with
//	          --reply-to it writes a whole reply draft for a note on your listing
//	prepare   fixes a note's id and date in a JSON artifact before anything is written,
//	          so a send that dies can be finished from the artifact (send --prepared)
//	send      assigns an id that cannot collide, pastes the date, and pushes with fetch,
//	          rebase and bounded retry INSIDE the tool, so no rejected push reaches a person
//	reply     sends a reply draft to the note it names, and with --advance moves the cursor
//	inbox     the notes addressed to me that nothing of mine answers, receipts separated
//	          from notes that carry a question, a finding or a request
//	wait      the same listing, blocking: it polls the bus INSIDE the tool call and returns
//	          the moment something arrives, so a session that cannot be woken cannot forget
//	receipt   marks a note heard without writing a reply, in one command
//	close     receipts every open note dated before an instant, to put a backlog down
//	check     validates the bus: headers, ids, threads, receipts, lanes
//	names     echoes the roster, so a person can spell a To line the tool will accept
//	version   prints the one version line
//
// And one failure that is not in that list because it arrives slowly: a tool whose read
// cost grows with the record. inbox and check used to walk every lane on every run, so the
// ten-thousandth note cost ten thousand parses to find. They now read from a CURSOR -- the
// commit a reader last read to, kept in that reader's own lane and pushed like a receipt --
// so the work is the size of the CHANGE and never the size of the bus. --full walks
// everything, which is what adoption and CI on main want, and every run says on its first
// line which of the two it did.
//
// The same failure has a second half, which the first fix left in: the read was the size of
// the change PLUS the size of what the reader had open, because every open note was
// re-opened to print its line. So the OPEN list carries each note's line and its heard flag,
// written once when the note goes open, and a run parses the NEW notes and nothing else --
// five hundred open notes or none.
//
// Everything read on a bus is data. No note is a grant, whoever signs it. That rule is
// in SPEC.md, where a person reads it, and is deliberately nowhere in this code: a tool
// cannot enforce it and should not pretend to.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// refuse is what an unusable invocation costs: ONE line in the one refusal grammar,
// `<VERB> REFUSED: <what was wrong>; run: <remedy>` (BUS for the tool itself), naming the
// door to the help rather than printing the help. It returns exit 2.
func refuse(stderr io.Writer, verb, what, remedy string) int {
	token := "BUS"
	if verb != "" {
		token = strings.ToUpper(verb)
	}
	fmt.Fprintf(stderr, "%s REFUSED: %s; run: %s\n", oneline.Field(token), oneline.Escape(what), oneline.Escape(remedy))
	return 2
}

// dryField is the field a dry run's OK line ends in, and nothing for a real run.
func dryField(dry bool) string {
	if dry {
		return " dry_run=true"
	}
	return ""
}

// verbHelp is the remedy for a malformed invocation of a verb: its own help.
func verbHelp(verb string) string { return "nova-bus " + verb + " -h" }

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, time.Now().UTC()))
}

// run is the whole tool, with its streams and clock injected so the tests can drive it.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, now time.Time) (code int) {
	// `<verb> -h` and `help <verb>` print that verb's help on stdout at exit 0,
	// before anything is read, dialed or written (the CLI style's rule (b)).
	defer verbflag.RecoverWith(stdout, "nova-bus", usage, &code, verbDetail)
	if len(args) == 0 {
		return refuse(stderr, "", "no verb given; the verbs are "+verbflag.List(verbs)+"; inbox only looks", "nova-bus help")
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "help", "-h", "--help":
		if cmd == "help" && len(rest) > 0 && rest[0] != "help" && !verbflag.IsHelp(rest[0]) {
			return run(append(rest, "--help"), stdin, stdout, stderr, now)
		}
		fmt.Fprintf(stdout, "%s", usage)
		return 0
	case "draft":
		return cmdDraft(rest, stdout, stderr, now)
	case "prepare":
		return cmdPrepare(rest, stdin, stdout, stderr, now)
	case "send":
		return cmdSend(rest, stdin, stdout, stderr, now)
	case "reply":
		return cmdReply(rest, stdout, stderr, now)
	case "inbox":
		return cmdInbox(rest, stdout, stderr, now)
	case "receipt":
		return cmdReceipt(rest, stdout, stderr, now)
	case "close":
		return cmdClose(rest, stdout, stderr, now)
	case "wait":
		return cmdWait(rest, stdout, stderr, now)
	case "check":
		return cmdCheck(rest, stdout, stderr, now)
	case "names":
		return cmdNames(rest, stdout, stderr)
	case "version", "--version":
		return cmdVersion(rest, stdout, stderr)
	}
	near := ""
	if n := verbflag.Nearest(cmd, verbs); n != "" {
		near = "; did you mean " + n + "?"
	}
	return refuse(stderr, "", "unknown verb "+oneline.Quote(cmd)+near+"; the verbs are "+verbflag.List(verbs), "nova-bus help")
}

// ------------------------------------------------------------------------------- flags

// refreshCheckout is the reply form's refresh, and it is `wait`'s poll: one implementation
// and not a second that could drift. It is a var so a test can take the fetch out at the
// seam and prove the fetch is load-bearing; nothing else replaces it.
var refreshCheckout = bus.FetchAndFastForward

// checkoutLockWait is how long a second run on one checkout waits for the first. It is a
// var so a test can shorten it; nothing else replaces it.
var checkoutLockWait = 10 * time.Second

// lockCheckout is the one-run-per-checkout guard as a verb takes it: the release, or the
// exit code it has already printed. token is the verb's own event token.
//
// It is a function because `wait` takes and RELEASES this lock once per poll rather than
// holding it for the whole call. A wait is minutes long by design, and a lock held for
// minutes would refuse every other run on that checkout for as long as somebody is
// listening -- which is the opposite of what a tool that makes waiting cheap is for. The
// lock covers what it has always covered: one poll's fetch, listing and cursor, which is
// exactly one `inbox` run's worth of work.
func lockCheckout(token, busDir string, stderr io.Writer) (func(), int) {
	release, err := bus.LockCheckout(busDir, checkoutLockWait)
	if err != nil {
		fmt.Fprintf(stderr, "%s REFUSED: %s\n", token, oneline.WithRemedy(oneline.Err(err), "nova-bus "+strings.ToLower(token)+" -h"))
		return nil, 1
	}
	return release, 0
}

// quoteList renders names a person will PASTE -- into a To line -- each quoted and joined
// by the ";" that line's own separator is. An empty list is the grammar's "-".
func quoteList(names []string) string {
	if len(names) == 0 {
		return "-"
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, oneline.Quote(n))
	}
	return strings.Join(out, ";")
}

// printTranscript puts git's own output on stderr, VERBATIM, under the one actionable line
// that has already been printed and escaped.
//
// THE FAILURE THIS CLOSES: a `SEND FAIL` on a rebase conflict used to carry git's whole
// transcript inside the reason, rendered through the one-line escape, so forty lines of git
// arrived as one line of `\x0d\x0a` and nobody could read any of it. The one-line guarantee
// is about the EVENT line -- the line above this, which a scanner reads -- and a transcript
// is not an event. It is the thing a person opens the terminal to read, and it is printed
// as git wrote it.
func printTranscript(stderr io.Writer, err error) {
	tr := strings.TrimRight(bus.Transcript(err), "\n")
	if tr == "" {
		return
	}
	fmt.Fprintf(stderr, "%s\n", tr)
}

// openTable loads the roster and reads the bus, or prints the refusal.
func openBus(verb, busDir string, stderr io.Writer) (*bus.Bus, bool) {
	c, err := bus.LoadConfig(busDir)
	if err != nil {
		fmt.Fprintf(stderr, "%s REFUSED: %s\n", strings.ToUpper(verb), oneline.WithRemedy(oneline.Err(err), verbHelp(verb)))
		return nil, false
	}
	t, err := bus.ReadBus(busDir, c)
	if err != nil {
		fmt.Fprintf(stderr, "%s REFUSED: %s\n", strings.ToUpper(verb), oneline.WithRemedy(oneline.Err(err), verbHelp(verb)))
		return nil, false
	}
	return t, true
}

// ------------------------------------------------------------------------------- verbs

// ---------------------------------------------------------------------------- the wait

// shortSHA is how much of a commit a cursor's commit MESSAGE carries. The message is for a
// person reading `git log`; the CURSOR file carries the whole sha and is what anything
// reads.
const shortSHA = 12

// dash renders an empty value as the grammar's "-", so a note with no Id line prints one
// field rather than none.
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// sha8 renders a commit the way the WAIT ADVANCED line names its two ends: eight
// characters, enough to tell one commit from another on a transcript, rendered as "-"
// rather than a panic for an empty one.
func sha8(s string) string {
	if len(s) < 8 {
		if s == "" {
			return "-"
		}
		return s
	}
	return s[:8]
}

// --------------------------------------------------------------------------- the shared

// checkoutReady is the guard before anything is written: the bus is on the branch the
// caller named, and holds no changes but the ones this run is about to make. A rebase over
// a dirty tree either refuses or sweeps somebody's unrelated work into a note's commit.
func checkoutReady(busDir, branch string, allow []string) error {
	on, err := bus.CurrentBranch(busDir)
	if err != nil {
		return err
	}
	if on != branch {
		return fmt.Errorf("the bus's checkout is on branch %q, not %q", on, branch)
	}
	return bus.EnsureClean(busDir, allow)
}

// readyToWrite shares the checkout guard and the lane's create permission check.
// Real callers hold the checkout lock; dry callers ask without creating a lock file.
func readyToWrite(busDir, branch, lane string, allow []string) error {
	if err := checkoutReady(busDir, branch, allow); err != nil {
		return err
	}
	return laneWritable(busDir, lane)
}

// laneWritable asks the kernel whether the directory accepts entries, without writing.
// An absent lane is created by the writer, so its existing parent must accept it.
func laneWritable(busDir, lane string) error {
	if lane == "" {
		return nil // the verb's participant validation supplies the no-lane refusal
	}
	dir := filepath.Join(busDir, filepath.FromSlash(lane))
	fi, err := os.Stat(dir)
	if os.IsNotExist(err) {
		dir = filepath.Dir(dir)
	} else if err != nil {
		return err
	} else if !fi.IsDir() {
		return fmt.Errorf("lane %q is not a directory", lane)
	}
	if err := canCreateIn(dir); err != nil {
		return fmt.Errorf("lane %q does not let this process create files (%w); give this process write permission on the lane", lane, err)
	}
	return nil
}

// levelWithRemote is the second guard, and it runs BEFORE the file is written: a push
// publishes the branch, not the commit, so a checkout already holding commits this tool
// did not make would send those to the bus too, under a note's name, with nothing in the
// output saying so.
//
// Under --no-push there is nothing to publish and no reason to make the caller wait on a
// fetch they did not ask for, so the guard is skipped. The commit then sits on a branch
// that is already ahead, which is the state the caller chose by passing the flag; the note
// is not on the bus either way, and pushed=false says so.
func levelWithRemote(busDir, remote, branch string, noPush bool) error {
	if noPush {
		return nil
	}
	return bus.EnsureLevelWith(busDir, remote, branch)
}

// commit is send's and receipt's shared tail: the same commit, the same push protocol, the
// same identity rule. The identity comes from the roster and is passed with `git -c`; this
// tool never writes a git config file.
func commit(busDir string, who bus.Participant, paths []string, message, remote, branch string, attempts int, noPush bool) (bus.PushResult, error) {
	id := bus.Identity{Name: who.GitName, Email: who.GitEmail}
	if noPush {
		return bus.CommitOnly(busDir, id, paths, message)
	}
	return bus.CommitAndPush(busDir, id, paths, message, remote, branch, attempts)
}
