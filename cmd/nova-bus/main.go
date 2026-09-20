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
//	          so that a line's first send is not a header written from memory
//	send      assigns an id that cannot collide, pastes the date, and pushes with fetch,
//	          rebase and bounded retry INSIDE the tool, so no rejected push reaches a person
//	inbox     the notes addressed to me that nothing of mine answers, receipts separated
//	          from notes that carry a question, a finding or a request
//	wait      the same listing, blocking: it polls the bus INSIDE the tool call and returns
//	          the moment something arrives, so a session that cannot be woken cannot forget
//	receipt   marks a note heard without writing a reply, in one command
//	check     validates the bus: headers, ids, threads, receipts, lanes
//	names     echoes the roster, so a person can spell a To line the tool will accept
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
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const usage = `nova-bus: the bus, with the races taken out (see docs/SPEC.md)

usage:
  nova-bus draft --bus <dir> --as <name> --to <names> [--cc <names>] [--subject <text>] [--re <id-or-path-or-subject>] [--out <path> [--overwrite] | > <file>]
  nova-bus draft --bus <dir> --as <name> --reply-to <id-or-path-or-subject> --body-file <path> --draft-dir <dir> --remote <name> --branch <name>
        [--to <names>] [--cc <names>] [--subject <text>] [--max-body-bytes <n>]
  nova-bus prepare --bus <dir> --as <name> (--file <path>|--stdin) [--slug <s>]
  nova-bus send --bus <dir> (--file <path>|--stdin) [--as <name>] --remote <name> --branch <name> [--attempts <n>] [--slug <s>] [--no-push] [--dry-run] [--git-timeout <seconds>]
  nova-bus send --bus <dir> (--prepared <path>|--prepared-stdin) --as <name> --remote <name> --branch <name> [--attempts <n>] [--git-timeout <seconds>]
  nova-bus reply --bus <dir> --as <name> --re <id> --file <draft> --remote <name> --branch <name> [--advance] [--dry-run] [--attempts <n>] [--git-timeout <seconds>]
  nova-bus inbox --bus <dir> --as <name> --receipt-max-words <n> [--bodies [--max-notes <n>] [--max-bytes <n>] [--after <token>]] [--full] [--open [--open-max <n>]] [--open-warn <n>] [--max-commits <n>]
        [--legacy-before <date-or-instant>|--legacy-now|--carry-history]
        [--advance --remote <name> --branch <name> [--attempts <n>] [--no-push]]
        [--diagnostics]
        [--decide [--floor <f>] [--key-env <name>] [--base-url <url>] [--allow-private]]
  nova-bus wait --bus <dir> --as <name> --receipt-max-words <n> --timeout <duration> --remote <name> --branch <name>
        [--until <instant>] [--idle-exit <n>]
        [--bodies [--max-notes <n>] [--max-bytes <n>] [--after <token>]]
        [--interval <duration>] [--open [--open-max <n>]] [--open-warn <n>]
        [--legacy-before <date-or-instant>|--carry-history]
        [--advance [--attempts <n>] [--no-push]]
        [--quiet-beats]
        [--max-commits <n>]
        [--diagnostics]
  nova-bus receipt --bus <dir> --as <name> --note <id-or-path> [--note ...] --remote <name> --branch <name> [--attempts <n>] [--no-push]
  nova-bus close --bus <dir> --as <name> --before <RFC3339> [--dry-run] [--remote <name> --branch <name> [--attempts <n>] [--no-push]]
  nova-bus check --bus <dir> (--full | --as <name> | --since <commit>) [--legacy-before <date-or-instant>] [--rebuild-index]
  nova-bus names --bus <dir>

every verb that runs git also takes [--git-timeout <seconds>], default 60.

exit codes: 0 the verb ran and passed; 1 the verb ran and said NO -- a draft
refused, a bus that failed check, a push that could not be landed, a cursor
that is no longer on this history, another run holding this checkout; 2 could
not run: missing flag, unreadable bus, bad invocation.

Every path comes from a flag. There is no default bus, no default remote, no
default branch and no default receipt word count; a missing one is a refusal:
refusing to guess. --attempts DOES have one, 25, because it is not a fact about
your bus but a budget measured against it: five lines sending at once consumed
nine attempts at the peak, and a caller who has to name a number will name one
too small and lose a note. The roster is always <bus>/participants.json,
because two lines running this tool over one bus must read one roster. Flags
come before positional arguments.

One nova-bus runs on one checkout at a time: a second holds off for ten seconds
and then refuses, because two runs writing one OPEN list is not a race any care
here can win.

inbox and check read from your CURSOR -- the commit you last read to, kept in
your own lane -- so their cost is the size of the CHANGE and not the size of the
bus. Your OPEN list carries each open note's own line, so a run parses the NEW
notes and nothing else, however many you are carrying. --full walks everything,
which is what adoption and CI on main want. --advance moves your cursor and
pushes it, the same way a receipt is pushed.

EVERY inbox and wait return has the same three parts. What is NEW, in full, on
every run in every mode -- that is what a poll is for. Then exactly one
INBOX OPEN carrying=<n> heard=<m> line for the backlog. Then, only if you asked
with --open (or --full), the carried list itself, from the open list and without
opening a note, capped at --open-max (default 20) with one line saying how many
it did not print. Past --open-warn carried (default 40), every return adds one
line saying the list is large and the three ways out of it -- answer a note by
naming it, receipt it, or draw the switch-day line now and start over.

--bodies puts each NEW note's TEXT in that same return, so a reader answering a
note has it from the call that said it arrived. Each note's line is followed by
one INBOX BODY id=<id> bytes=<n> line, exactly n bytes of body -- no escaping, no
re-wrapping, no trailing-newline normalisation -- one separator newline IF AND
ONLY IF n is 0 or the body does not end in one, and one INBOX BODY END id=<id>.
The COUNT is the frame, never the closing line, so nothing a body holds can be
read as an event line. It is also the only flag that BOUNDS the NEW half, which
is otherwise unbounded: --max-notes (default 20, ceiling 1000) is how many NEW
items print AT ALL -- summary line and frame together -- and --max-bytes (default
65536, ceiling 1048576) is the body bytes. Both are checked before a frame is
opened, so a note is never printed half: an item past either limit is left for
the next call, whole, and the return says complete=false. Zero is not unlimited
and over-ceiling is not as much as you can: either one is INBOX REFUSED and exit
2. One INBOX BODIES printed= bytes= oversize= gaps= drained= complete= next= line
ends the return; next= is an opaque token you hand back as --after <token> to
continue the SAME snapshot, and a chain drains while next= is present -- never
loop on complete=false. A body no --max-bytes on this run carries is named on an
INBOX BODY OVERSIZE line and left whole where it is, and a chain that ends
holding one prints an INBOX BODIES GAP line saying which --max-bytes would carry
it, or that none under the ceiling does. Without --advance nothing moves; with
it the cursor stops at the last WHOLE commit printed before the first gap, and
never past it. Without --bodies, inbox and wait are exactly what they are today.

A note is closed by a Re: line naming it, and a Re: line is not a thing anybody
writes from memory: a line that answered every note by hand carried all 74 of
them for ever, because none of its answers named anything. So draft --re takes
an id, a path, OR the exact subject of a note on your open list and writes the id
for you; a Re: line in a draft may name that subject too, and send resolves it to
the newest match, writes the id, and says which note it closed; and a draft that
reads like a reply and names nothing gets one SEND NOTE saying so. None of the
three refuses anything.

--legacy-before draws the switch-day line on a bus that existed before this
tool: check WARNS instead of failing on an older note's header, and inbox does
not carry an older note on your open list, counting them on one INBOX LEGACY
line instead -- notes= for the notes, unreadable= for the files that will not
parse, which are not named one by one either once they are behind the line. A
file dated on or after it, or with no readable date at all, is still named on
every run, and --full lists everything. It takes a UTC date (YYYY-MM-DD,
midnight at its start) or an RFC 3339 UTC instant like 2026-09-09T18:07:00Z,
and compares by INSTANT; switching TODAY wants the instant you switched,
because a date still to come is midnight AFTER everything written today and
would hide every one of those notes. --legacy-now IS that instant, worked out
for you: it is --legacy-before <this run's UTC instant> and nothing else, so
the shape nobody can type is the shape that is one word. inbox records the line
in your cursor, so later runs honour it without the flag; moving it earlier is
refused unless the read is --full.

If your cursor's line is a DATE standing at today or later, every inbox run --
and check --as <you> -- prints one INBOX SWITCH line saying which day it hides
and the exact command that redraws it at an instant. It is a note and not a
refusal: the run does what it was asked, and nothing moves until you run the
command it names.

Your FIRST --advance on a bus holding notes older than today is refused unless
you have said what to do with them: --legacy-before <date-or-instant> or
--legacy-now takes the history as read, or --carry-history carries every old
note on your open list. The refusal names the count and the exact line to run,
and the line it hands you carries --legacy-now -- everything on the bus at the
moment you run it is history and everything after it is news. Every advance
after the first needs neither, and a bus with no old notes needs neither ever.

inbox REPORTS and exits 0 whether the inbox is empty or full; check is the gate.

wait is inbox on a clock, for a harness that does not wake you: it fetches every
--interval (default 10s) and RETURNS the moment your inbox would list something
new, printing exactly what inbox prints. Without --advance the cursor does not
move, so an unadvanced cursor makes wait return AT ONCE with the same listing
inbox would print, every call, for as long as it stays where it is: a caller
with a backlog runs inbox first to clear it, or passes --advance so the second
wait is a real wait for a note newer than the start. Nothing by --timeout is a
WAIT TIMEOUT line and exit 0 -- not an error, the answer "nothing yet" -- and you
issue the next one. --timeout is required, because every wait has a deadline, and
is at most 60m: a wait runs inside your harness's tool call, so ask your harness
what its limit is and sit under it. The loop is wait, answer, wait, with --advance
so the second wait is a real wait:

  nova-bus wait --bus ~/bus --as Ada --receipt-max-words 40 --timeout 25m \
    --advance --remote origin --branch main

--quiet-beats is accepted and changes nothing since 2026-09-17 (#328): a change
that is only beats and cursors -- a lane's BEAT or CURSOR moving, no note --
never wakes a wait; a beat is not news, exactly as before.

--until <instant> is an absolute deadline beside --timeout, an RFC 3339 UTC
instant, and the wait ends at whichever of the two comes first: a caller whose
own limit is a MOMENT rather than a duration does not have to work out how long
is left. --idle-exit <n> is the exit code a TIMEOUT returns instead of 0, so a
harness can branch on the code without parsing anything; 1 and 2 are refused,
because they are this tool's own -- a refusal, and an invocation that could not
run -- and a harness that got one back could not tell a quiet bus from a broken
one.

A HARNESS THAT CANNOT LOOP -- OpenCode's, and every harness like it -- runs this
exact sequence and nothing else. Once, to clear the backlog:

  nova-bus inbox --bus ~/bus --as Freddy --receipt-max-words 40 \
    --advance --remote origin --branch main

Then one wait per turn:

  nova-bus wait --bus ~/bus --as Freddy --receipt-max-words 40 --timeout 25m \
    --until 2026-09-18T18:00:00Z --idle-exit 3 \
    --advance --remote origin --branch main

Exit 0 is a note: the listing is on stdout, answer it, then issue the same wait
again. Exit 3 is the one line WAIT TIMEOUT after=<d> polls=<n> cursor=<sha|->
idle-exit=3 and nothing came: issue the same wait again, or stop if your own
deadline has passed. Exit 1 is a refusal and exit 2 is an invocation that could
not run, both with the reason on stderr, and neither is re-armed until somebody
has read it. The harness keeps no clock and runs no loop of its own: every call
ends by itself, at the note or at the deadline, and the WAIT DONE ...
next=<command> line is the command to issue again.

A FIRST SEND, end to end. draft prints a skeleton and NOTHING else, so its
standard output is a file:

  nova-bus draft --bus ~/bus --as Ada --to Bo --subject 'the gate' > draft.md
  $EDITOR draft.md
  nova-bus send --bus ~/bus --file draft.md --as Ada --remote origin --branch main

send tolerates the shapes a first draft arrives in rather than refusing them, and
prints a SEND NOTE line for each: a markdown heading above the header becomes the
Subject, a Date line is replaced by the tool's own, --as writes a missing From
line, blank lines above the header are skipped, and a **Key**: in markdown bold
loses its asterisks. It still refuses what it cannot read without guessing -- a
recipient the roster does not know, no To line at all, a key nobody knows, a Re
naming nothing -- and it reports EVERY problem in the draft in one run.

prepare computes a note's deterministic id and assigns its Date before any bus
mutation, outputting a self-contained JSON artifact to stdout. Do not prepare
again while pending; retry the saved artifact. Two preparations at different
instants can assign different Date values and IDs even when the original draft
is identical. send --prepared confirms or publishes that exact saved artifact
with bounded recovery.

Copy the example bus out first; every line below runs against it.

  cp -R cmd/nova-bus/testdata/example-bus ./bus

example:
  nova-bus names --bus ./bus
  nova-bus check --bus ./bus --full
  nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40 --full --open
  nova-bus draft --bus ./bus --as Ada --to Bo --subject gate

./bus there is a bus of your own: a repository whose ROOT is a directory of
lanes, not a subdirectory of a larger one. cmd/nova-bus/testdata/example-bus in
this repo is one the size of a first run -- three participants, four notes, a
thread, a receipt and a cursor -- and its README says how to copy it out and give
it a repository of its own. Every line above is run against it by the tests, and
docs/TESTS.md carries the whole first sitting: read, receipt, advance, send.
`

// refuse is what an unusable invocation costs: ONE line naming what was wrong, and the
// door to the usage rather than the usage itself. It was the whole 102-line banner, on
// every flag typo -- 6,473 bytes, about 1.6K tokens, to say that a dash was in the wrong
// place. THE THREE SITES BELOW ARE THE ONLY ONES THIS CHANGE TOUCHES in this binary: the
// bare invocation, the unknown verb, and the flag parse error. Everything else nova-bus
// prints is another line's work.
func refuse(stderr io.Writer, where, what string) int {
	fmt.Fprintf(stderr, "nova-bus%s: %s; run: nova-bus help\n", oneline.Escape(where), oneline.Escape(what))
	return 2
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, time.Now().UTC()))
}

// run is the whole tool, with its streams and clock injected so the tests can drive it.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, now time.Time) int {
	if len(args) == 0 {
		return refuse(stderr, "", "no verb given; `inbox --as <name>` is the one that only looks")
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
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
	return refuse(stderr, "", fmt.Sprintf("unknown subcommand %q", cmd))
}

// ------------------------------------------------------------------------------- flags

// stringList is a flag that may be repeated, for `receipt --note`.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// flags is one verb's flag set, with package flag's two mouths closed. Its error text
// quotes the argument it could not parse and its usage dump follows, so an argument
// beginning with a dash could otherwise author a whole line of stderr before any code
// here ran.
type flags struct {
	verb string
	fs   *flag.FlagSet
}

func newFlags(verb string) *flags {
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	return &flags{verb: verb, fs: fs}
}

// parse runs the flag set and enforces the no-guessing rule for every flag named in
// required. It prints its own refusal, escaped; exit 2 belongs to the caller.
func (f *flags) parse(args []string, stderr io.Writer, required map[string]*string) bool {
	if err := f.fs.Parse(args); err != nil {
		// -h and -help land here as flag.ErrHelp and are refused like any other unusable
		// invocation: exit 2, never 0.
		refuse(stderr, " "+f.verb, oneline.Cap(err.Error(), oneline.TailBytes))
		return false
	}
	if n := f.fs.NArg(); n > 0 {
		fmt.Fprintf(stderr, "nova-bus %s: takes no positional arguments, got %d (flags come before arguments)\n", f.verb, n)
		return false
	}
	var missing []string
	for name, value := range required {
		if strings.TrimSpace(*value) == "" {
			missing = append(missing, name)
		}
	}
	for i := 1; i < len(missing); i++ {
		for j := i; j > 0 && strings.Compare(missing[j], missing[j-1]) < 0; j-- {
			missing[j], missing[j-1] = missing[j-1], missing[j]
		}
	}
	for _, name := range missing {
		fmt.Fprintf(stderr, "nova-bus %s: --%s is required; refusing to guess; run: nova-bus help\n", f.verb, name)
	}
	return len(missing) == 0
}

// gitArgs checks the two flags that become git's own argv. A --remote or --branch
// beginning with a dash is an OPTION to git rather than a name, and this tool would run
// it; the charset narrows the rest. It prints its own refusal; exit 2 belongs to the
// caller, because a flag this tool will not pass on is a bad invocation and not a bus
// that failed.
func (f *flags) gitArgs(remote, branch string, stderr io.Writer) bool {
	for _, c := range []struct {
		what, value string
	}{{"remote", remote}, {"branch", branch}} {
		if err := bus.ValidGitArg(c.what, c.value); err != nil {
			fmt.Fprintf(stderr, "nova-bus %s: %s\n", f.verb, oneline.Err(err))
			return false
		}
	}
	return true
}

// atLeastZero reads an integer flag whose floor is zero rather than one, which is the
// floor a THRESHOLD has: --open-warn 0 says "tell me on every run", and that is a thing a
// reader may reasonably mean. A negative one is not, and is a bad invocation.
func (f *flags) atLeastZero(name string, value int, stderr io.Writer) bool {
	if value < 0 {
		fmt.Fprintf(stderr, "nova-bus %s: --%s counts entries, so it is 0 or more, got %d\n", f.verb, name, value)
		return false
	}
	return true
}

// count reads a required positive integer flag.
func (f *flags) count(name string, value int, stderr io.Writer) bool {
	if value < 1 {
		fmt.Fprintf(stderr, "nova-bus %s: --%s must be given and at least 1, got %d; refusing to guess; run: nova-bus help\n", f.verb, name, value)
		return false
	}
	return true
}

// set answers whether a flag was named on the command line at all, so a default source can
// tell "absent" from "given a value that is not usable", which are different mistakes.
func (f *flags) set(name string) bool {
	given := false
	f.fs.Visit(func(fl *flag.Flag) {
		if fl.Name == name {
			given = true
		}
	})
	return given
}

// receiptMaxWords resolves the one count a body is a receipt under. The flag wins; when it
// is absent, a `receipt-max-words=<n>` line in <bus>/.nova-bus/defaults is read, then the
// NOVA_BUS_RECEIPT_MAX_WORDS environment variable. It refuses only when none of the three
// yields a positive number, and the refusal names the two default sources as the remedy.
func (f *flags) receiptMaxWords(flagValue int, flagWasSet bool, busDir string, stderr io.Writer) (int, bool) {
	if !flagWasSet {
		if v, ok := receiptMaxWordsFromDefaults(busDir); ok {
			flagValue = v
		} else if v, ok := receiptMaxWordsFromEnv(); ok {
			flagValue = v
		}
	}
	if flagValue < 1 {
		fmt.Fprintf(stderr, "nova-bus %s: --receipt-max-words must be given and at least 1, got %d; refusing to guess; give it as a `receipt-max-words=<n>` line in <bus>/.nova-bus/defaults or the NOVA_BUS_RECEIPT_MAX_WORDS env var; run: nova-bus help\n", f.verb, flagValue)
		return 0, false
	}
	return flagValue, true
}

// receiptMaxWordsFromDefaults reads the `receipt-max-words=<n>` line out of
// <bus>/.nova-bus/defaults, the file default source. The file is key=value lines; only this
// one key matters. A missing file, a missing key, or an unusable value is "absent".
func receiptMaxWordsFromDefaults(busDir string) (int, bool) {
	if busDir == "" {
		return 0, false
	}
	raw, err := os.ReadFile(filepath.Join(busDir, ".nova-bus", "defaults"))
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "receipt-max-words" {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(val))
		if err != nil || n < 1 {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

// receiptMaxWordsFromEnv reads NOVA_BUS_RECEIPT_MAX_WORDS, the environment default source.
// An empty or unusable value is "absent".
func receiptMaxWordsFromEnv() (int, bool) {
	s := strings.TrimSpace(os.Getenv("NOVA_BUS_RECEIPT_MAX_WORDS"))
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// defaultAttempts is the retry budget when the caller names none.
//
// THE ONE FLAG THAT GETS A DEFAULT, and it is a departure from the rule above, so here is
// the measurement that bought it. In the scenario run, five lines sent three notes each at
// once with `--attempts 3`: fifteen were sent and SIX LANDED. With `--attempts 25` all
// fifteen landed and the deepest any one of them went was nine attempts. A retry budget is
// not a fact about a bus that only its owner can supply -- it is the number of times this
// tool will keep trying against a remote that is moving under it, and a caller made to
// invent one invents a small one and loses notes. Twenty-five is nine with room, and the
// backoff caps the whole of it at a person's wait rather than a schedule.
const defaultAttempts = 25

// attempts checks the retry budget. Unlike count it has a default, so a zero here is a
// caller who asked for one and asked for none.
func (f *flags) attempts(value int, stderr io.Writer) bool {
	if value < 1 {
		fmt.Fprintf(stderr, "nova-bus %s: --attempts is a number of tries and is at least 1, got %d\n", f.verb, value)
		return false
	}
	return true
}

// gitTimeoutFlag sets the per-subprocess budget every git this run starts is held to. A
// hung fetch is a tool that has stopped saying anything, which is indistinguishable from a
// tool that is working.
func (f *flags) gitTimeoutFlag(seconds int, stderr io.Writer) bool {
	if err := gitTimeoutProblem(seconds); err != nil {
		fmt.Fprintf(stderr, "nova-bus %s: %s\n", f.verb, oneline.Err(err))
		return false
	}
	return true
}

// gitTimeoutProblem is the same check with its answer RETURNED rather than printed, for the
// one verb that collects every problem in an invocation before it prints any of them. One
// spelling, two callers: a check that printed for one caller and returned for the other
// would be two rules wearing one name.
func gitTimeoutProblem(seconds int) error {
	if seconds < 1 {
		return fmt.Errorf("--git-timeout is a whole number of seconds and at least 1, got %d", seconds)
	}
	return bus.SetGitTimeout(time.Duration(seconds) * time.Second)
}

// defaultGitTimeoutSeconds is DefaultGitTimeout as the flag spells it.
const defaultGitTimeoutSeconds = 60

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
		fmt.Fprintf(stderr, "%s REFUSED: %s\n", token, oneline.Err(err))
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
		fmt.Fprintf(stderr, "nova-bus %s: %s\n", verb, oneline.Err(err))
		return nil, false
	}
	t, err := bus.ReadBus(busDir, c)
	if err != nil {
		fmt.Fprintf(stderr, "nova-bus %s: %s\n", verb, oneline.Err(err))
		return nil, false
	}
	return t, true
}

// ------------------------------------------------------------------------------- verbs


func cmdPrepare(args []string, stdin io.Reader, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("prepare")
	busDir := f.fs.String("bus", "", "the bus's repository root (required)")
	as := f.fs.String("as", "", "which participant you are (required)")
	file := f.fs.String("file", "", "the draft to prepare")
	useStdin := f.fs.Bool("stdin", false, "read the draft from standard input instead of --file")
	slug := f.fs.String("slug", "", "the human half of the filename (default: from the subject)")
	if !f.parse(args, stderr, map[string]*string{"bus": busDir, "as": as}) {
		return 2
	}
	if (*file == "") == !*useStdin {
		fmt.Fprint(stderr, "nova-bus prepare: give exactly one of --file and --stdin; refusing to guess; run: nova-bus help\n")
		return 2
	}
	source := "(stdin)"
	var text string
	if *useStdin {
		raw, err := io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintf(stderr, "nova-bus prepare: %s\n", oneline.Err(err))
			return 2
		}
		text = string(raw)
	} else {
		source = *file
		raw, err := os.ReadFile(*file)
		if err != nil {
			fmt.Fprintf(stderr, "nova-bus prepare: %s\n", oneline.Err(err))
			return 2
		}
		text = string(raw)
	}
	if err := bus.IsRepoRoot(*busDir); err != nil {
		fmt.Fprintf(stderr, "nova-bus prepare: %s\n", oneline.Err(err))
		return 2
	}
	t, ok := openBus("prepare", *busDir, stderr)
	if !ok {
		return 2
	}
	prepared, err := bus.PrepareDraft(t, text, now, *slug, *as)
	if err != nil {
		for _, reason := range bus.Reasons(err) {
			fmt.Fprintf(stderr, "PREPARE FAIL %s: %s\n", oneline.Escape(source), oneline.Err(reason))
		}
		return 1
	}
	for _, notice := range prepared.Notices {
		fmt.Fprintf(stderr, "PREPARE NOTE %s\n", oneline.Escape(notice))
	}
	artifactJSON, err := bus.RenderPreparedArtifact(prepared)
	if err != nil {
		fmt.Fprintf(stderr, "PREPARE FAIL %s: %s\n", oneline.Escape(source), oneline.Err(err))
		return 1
	}
	fmt.Fprint(stdout, artifactJSON)
	return 0
}

func cmdSend(args []string, stdin io.Reader, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("send")
	busDir := f.fs.String("bus", "", "the bus's repository root (required)")
	file := f.fs.String("file", "", "the draft to send")
	useStdin := f.fs.Bool("stdin", false, "read the draft from standard input instead of --file")
	preparedFile := f.fs.String("prepared", "", "the prepared artifact to send or confirm")
	usePreparedStdin := f.fs.Bool("prepared-stdin", false, "read the prepared artifact from standard input instead of --prepared")
	remote := f.fs.String("remote", "", "the git remote to push to (required)")
	branch := f.fs.String("branch", "", "the branch the bus lives on (required)")
	as := f.fs.String("as", "", "which participant you are; supplies the From line when the draft has none, and is refused if the draft's From line names anybody else")
	slug := f.fs.String("slug", "", "the human half of the filename (default: from the subject)")
	attempts := f.fs.Int("attempts", defaultAttempts, "how many times to push before giving up")
	gitSeconds := f.fs.Int("git-timeout", defaultGitTimeoutSeconds, "how long one git subprocess may take before this run gives up on it")
	noPush := f.fs.Bool("no-push", false, "commit but do not push; the note is NOT on the bus until it is pushed")
	dryRun := f.fs.Bool("dry-run", false, "stop after the preflight and the shaping: commit nothing, push nothing, print the note that would be sent")
	if !f.parse(args, stderr, map[string]*string{"bus": busDir, "remote": remote, "branch": branch}) {
		return 2
	}
	if !f.attempts(*attempts, stderr) {
		return 2
	}
	if !f.gitTimeoutFlag(*gitSeconds, stderr) {
		return 2
	}
	if !f.gitArgs(*remote, *branch, stderr) {
		return 2
	}
	hasDraft := *file != "" || *useStdin
	hasPrepared := *preparedFile != "" || *usePreparedStdin
	if hasDraft && hasPrepared {
		fmt.Fprint(stderr, "nova-bus send: --prepared is mutually exclusive with --file and --stdin\n")
		return 2
	}
	if hasPrepared && *dryRun {
		fmt.Fprint(stderr, "nova-bus send: --dry-run shapes an ordinary draft; drop --prepared or drop --dry-run\n")
		return 2
	}
	if hasPrepared {
		if (*preparedFile == "") == !*usePreparedStdin {
			fmt.Fprint(stderr, "nova-bus send: give exactly one of --prepared and --prepared-stdin; refusing to guess; run: nova-bus help\n")
			return 2
		}
	} else {
		if (*file == "") == !*useStdin {
			fmt.Fprint(stderr, "nova-bus send: give exactly one of --file and --stdin; refusing to guess; run: nova-bus help\n")
			return 2
		}
	}
	if *preparedFile != "" || *usePreparedStdin {
		if *noPush {
			fmt.Fprint(stderr, "nova-bus send: --no-push cannot be used with --prepared\n")
			return 2
		}
		if *slug != "" {
			fmt.Fprint(stderr, "nova-bus send: --slug cannot be used with --prepared\n")
			return 2
		}
		if *as == "" {
			fmt.Fprint(stderr, "nova-bus send: --as is required with --prepared\n")
			return 2
		}
		source := "(prepared-stdin)"
		var raw []byte
		var err error
		if *usePreparedStdin {
			raw, err = io.ReadAll(stdin)
		} else {
			source = *preparedFile
			raw, err = os.ReadFile(*preparedFile)
		}
		if err != nil {
			fmt.Fprintf(stderr, "nova-bus send: %s\n", oneline.Err(err))
			return 2
		}
		if err := bus.IsRepoRoot(*busDir); err != nil {
			fmt.Fprintf(stderr, "nova-bus send: %s\n", oneline.Err(err))
			return 2
		}
		c, err := bus.LoadConfig(*busDir)
		if err != nil {
			fmt.Fprintf(stderr, "nova-bus send: %s\n", oneline.Err(err))
			return 2
		}
		art, p, err := bus.ValidatePreparedArtifact(raw, *busDir, c, *as)
		if err != nil {
			for _, reason := range bus.Reasons(err) {
				fmt.Fprintf(stderr, "SEND FAIL %s: %s\n", oneline.Escape(source), oneline.Err(reason))
			}
			return 1
		}
		release, err := bus.LockCheckout(*busDir, checkoutLockWait)
		if err != nil {
			fmt.Fprintf(stderr, "SEND REFUSED: %s\n", oneline.Err(err))
			return 1
		}
		defer release()

		res, err := bus.SendPreparedArtifact(*busDir, *remote, *branch, p, art, *attempts)
		if err != nil {
			fmt.Fprintf(stderr, "SEND FAIL %s: %s\n", oneline.Escape(art.Path), oneline.Err(err))
			printTranscript(stderr, err)
			return 1
		}
		to, _ := p.Note.Header.Recipients(c)
		fmt.Fprintf(stdout, "SEND OK id=%s path=%s commit=%s pushed=%t attempts=%d state=%s wakes=%d body_bytes=%d\n",
			oneline.Field(art.ID), oneline.Field(art.Path), oneline.Field(res.Commit), res.Pushed, res.Attempts, oneline.Field(res.State), len(to), len(p.Note.Body))
		return 0
	}

	source := "(stdin)"
	var text string
	if *useStdin {
		raw, err := io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintf(stderr, "nova-bus send: %s\n", oneline.Err(err))
			return 2
		}
		text = string(raw)
	} else {
		source = *file
		raw, err := os.ReadFile(*file)
		if err != nil {
			fmt.Fprintf(stderr, "nova-bus send: %s\n", oneline.Err(err))
			return 2
		}
		text = string(raw)
	}
	// THE PREFLIGHT, before any commit: two shapes a hand-written draft arrives in that
	// this tool mints rather than reads. A hand-written Id is the tool's to assign, and a
	// Re line names one thread. Both are exit 2 with one remedy line, and both leave the
	// bus, the index and the working tree exactly as they were.
	if kind, ok := preflightDraft(text); !ok {
		switch kind {
		case "id":
			fmt.Fprintf(stderr, "nova-bus send: the tool mints the Id; delete the Id: header from %s\n", oneline.Field(source))
		case "re":
			fmt.Fprintf(stderr, "nova-bus send: Re: names one thread; name one id in %s\n", oneline.Field(source))
		}
		return 2
	}
	if *dryRun {
		if err := bus.IsRepoRoot(*busDir); err != nil {
			fmt.Fprintf(stderr, "nova-bus send: %s\n", oneline.Err(err))
			return 2
		}
		t, ok := openBus("send", *busDir, stderr)
		if !ok {
			return 2
		}
		prepared, err := bus.PrepareDraft(t, text, now, *slug, *as)
		if err != nil {
			for _, reason := range bus.Reasons(err) {
				fmt.Fprintf(stderr, "SEND FAIL %s: %s\n", oneline.Escape(source), oneline.Err(reason))
			}
			return 1
		}
		printSendDraft(stdout, prepared, t.Config, now)
		return 0
	}
	if err := bus.IsRepoRoot(*busDir); err != nil {
		fmt.Fprintf(stderr, "nova-bus send: %s\n", oneline.Err(err))
		return 2
	}
	release, err := bus.LockCheckout(*busDir, checkoutLockWait)
	if err != nil {
		fmt.Fprintf(stderr, "SEND REFUSED: %s\n", oneline.Err(err))
		return 1
	}
	defer release()
	t, ok := openBus("send", *busDir, stderr)
	if !ok {
		return 2
	}
	prepared, err := bus.PrepareDraft(t, text, now, *slug, *as)
	if err != nil {
		// EVERY reason, one line each. A refusal that named the first of three mistakes in
		// a draft cost the writer three runs to find the other two, and the tool had read
		// all three before it printed anything.
		for _, reason := range bus.Reasons(err) {
			fmt.Fprintf(stderr, "SEND FAIL %s: %s\n", oneline.Escape(source), oneline.Err(reason))
		}
		return 1
	}
	// What the tolerances did, before anything is written, one line each. A tool that
	// quietly rewrites what a person wrote teaches nobody anything and cannot be checked;
	// these lines are the whole difference between a tolerance and a guess.
	for _, notice := range prepared.Notices {
		fmt.Fprintf(stdout, "SEND NOTE %s\n", oneline.Escape(notice))
	}
	// THE SENDER'S OWN BEAT IS NOT SOMEBODY ELSE'S WORK (#488). `wait` writes
	// from-<me>/BEAT, and a wait killed between its tick and its commit leaves it in the
	// tree. Refusing over it told the writer their checkout held "changes that are not
	// this note" and named a file they had never touched, and their answer did not go out.
	// It is the caller's own machinery: it is allowed past the clean guard here and folded
	// into this note's commit below, so the send carries it out rather than stopping on it.
	// Every other path in the tree is still the refusal it always was.
	beat := bus.BeatPath(prepared.Sender.Lane)
	if err := checkoutReady(*busDir, *branch, []string{beat}); err != nil {
		fmt.Fprintf(stderr, "SEND FAIL %s: %s\n", oneline.Escape(source), oneline.Err(err))
		return 1
	}
	if err := levelWithRemote(*busDir, *remote, *branch, *noPush); err != nil {
		fmt.Fprintf(stderr, "SEND REFUSED: %s\n", oneline.Err(err))
		return 1
	}
	if err := prepared.Save(*busDir); err != nil {
		fmt.Fprintf(stderr, "SEND FAIL %s: %s\n", oneline.Escape(source), oneline.Err(err))
		return 1
	}
	// The lane's catalogue is appended in the SAME commit as the note. A catalogue that
	// could lag the notes by a commit is one a reader between the two would resolve
	// wrongly, and a note that landed without its line would be invisible to every later
	// id lookup until somebody ran a rebuild.
	if err := prepared.AppendIndex(*busDir); err != nil {
		fmt.Fprintf(stderr, "SEND FAIL %s: %s\n", oneline.Escape(source), oneline.Err(err))
		return 1
	}
	// The union-merge rules, written once per bus and committed with the note that first
	// needed them. INDEX and RECEIPTS are append-only, and without the rule two benches of
	// one lane appending at the same end over one base conflict and the bench is wedged. It
	// goes on the FIRST send rather than being asked of the bus's owner because a bus
	// that has to be prepared by hand before it is safe is a bus somebody will not
	// prepare. See internal/bus/attributes.go.
	paths := prepared.Paths()
	wroteAttrs, err := bus.EnsureMergeAttributes(*busDir)
	if err != nil {
		fmt.Fprintf(stderr, "SEND FAIL %s: %s\n", oneline.Escape(bus.AttributesName), oneline.Err(err))
		return 1
	}
	if wroteAttrs {
		paths = append(paths, bus.AttributesName)
	}
	// The beat is folded in only when there is one: StagePaths drops a path that is
	// neither on disk nor in the index, because asking git to stage one that is neither is
	// exit 128, and a sender who has never run `wait` has no BEAT at all.
	paths, err = bus.StagePaths(*busDir, append(paths, beat))
	if err != nil {
		fmt.Fprintf(stderr, "SEND FAIL %s: %s\n", oneline.Escape(source), oneline.Err(err))
		return 1
	}
	res, err := commit(*busDir, prepared.Sender, paths,
		bus.WithTrailer(prepared.Message, bus.TrailerSend+" "+prepared.Note.Header.ID),
		*remote, *branch, *attempts, *noPush)
	if err != nil {
		fmt.Fprintf(stderr, "SEND FAIL %s: %s\n", oneline.Escape(prepared.Path), oneline.Err(err))
		printTranscript(stderr, err)
		return 1
	}
	to, _ := prepared.Note.Header.Recipients(t.Config)
	fmt.Fprintf(stdout, "SEND OK id=%s path=%s commit=%s pushed=%t attempts=%d wakes=%d body_bytes=%d\n",
		oneline.Field(prepared.Note.Header.ID), oneline.Field(prepared.Path), oneline.Field(res.Commit), res.Pushed, res.Attempts, len(to), len(prepared.Note.Body))
	return 0
}

// preflightDraft is the send --file preflight: the two mistakes a shaped note refuses
// before any commit. It returns which one it found and false, or "" and true.
//
// A hand-written Id is refused because the tool mints it, and a Re line naming more than
// one id is refused because a Re line names one thread. The Re check counts tokens that
// have the shape of a bus id, so a subject holding a comma is still the subject it is.
func preflightDraft(text string) (string, bool) {
	n, _ := bus.ParseNoteAll("", text)
	if strings.TrimSpace(n.Header.ID) != "" {
		return "id", false
	}
	if countBusIDs(n.Header.Re) > 1 {
		return "re", false
	}
	return "", true
}

// countBusIDs counts the id-shaped tokens across a note's Re lines: a Re line may separate
// ids with a comma, a semicolon or a space, and only a token that is a bus id at all is
// counted.
func countBusIDs(res []string) int {
	count := 0
	for _, r := range res {
		spaced := strings.ReplaceAll(strings.ReplaceAll(r, ",", " "), ";", " ")
		for _, tok := range strings.Fields(spaced) {
			if isBusID(tok) {
				count++
			}
		}
	}
	return count
}

// isBusID reports the shape the tool mints: a sender slug, a hyphen, and twelve lower-case
// hex digits.
func isBusID(tok string) bool {
	i := strings.LastIndexByte(tok, '-')
	if i <= 0 || len(tok)-i-1 != 12 {
		return false
	}
	for _, r := range tok[i+1:] {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// printSendDraft is `send --dry-run`: the shaped note as one line naming every field, then
// the note verbatim framed by an id, so a caller can pipe it to a file.
func printSendDraft(stdout io.Writer, p bus.Prepared, c *bus.Config, now time.Time) {
	id := p.Note.Header.ID
	to, cc := p.Note.Header.Recipients(c)
	re := "none"
	if len(p.Note.Header.Re) > 0 {
		re = p.Note.Header.Re[0]
	}
	note := p.Note.Render()
	fmt.Fprintf(stdout, "SEND DRAFT id=%s path=%s to=%d cc=%d re=%s subject=%s date=%s bytes=%d\n",
		oneline.Field(id), oneline.Field(p.Path), len(to), len(cc), oneline.Field(re),
		oneline.Field(p.Note.Header.Subject), oneline.Field(now.UTC().Format(time.RFC3339)), len(note))
	fmt.Fprintf(stdout, "SEND DRAFT id=%s\n", oneline.Field(id))
	fmt.Fprint(stdout, note)
	fmt.Fprintf(stdout, "SEND DRAFT END id=%s\n", oneline.Field(id))
}

func cmdReceipt(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("receipt")
	busDir := f.fs.String("bus", "", "the bus's repository root (required)")
	as := f.fs.String("as", "", "which participant you are (required)")
	remote := f.fs.String("remote", "", "the git remote to push to (required)")
	branch := f.fs.String("branch", "", "the branch the bus lives on (required)")
	attempts := f.fs.Int("attempts", defaultAttempts, "how many times to push before giving up")
	gitSeconds := f.fs.Int("git-timeout", defaultGitTimeoutSeconds, "how long one git subprocess may take before this run gives up on it")
	noPush := f.fs.Bool("no-push", false, "commit but do not push; the receipt is NOT on the bus until it is pushed")
	var notes stringList
	f.fs.Var(&notes, "note", "a note to mark heard, by id or by path (required; repeatable)")
	if !f.parse(args, stderr, map[string]*string{"bus": busDir, "as": as, "remote": remote, "branch": branch}) {
		return 2
	}
	if !f.attempts(*attempts, stderr) {
		return 2
	}
	if !f.gitTimeoutFlag(*gitSeconds, stderr) {
		return 2
	}
	if !f.gitArgs(*remote, *branch, stderr) {
		return 2
	}
	if len(notes) == 0 {
		fmt.Fprint(stderr, "nova-bus receipt: --note is required; refusing to guess; run: nova-bus help\n")
		return 2
	}
	if err := bus.IsRepoRoot(*busDir); err != nil {
		fmt.Fprintf(stderr, "nova-bus receipt: %s\n", oneline.Err(err))
		return 2
	}
	release, lockErr := bus.LockCheckout(*busDir, checkoutLockWait)
	if lockErr != nil {
		fmt.Fprintf(stderr, "RECEIPT REFUSED: %s\n", oneline.Err(lockErr))
		return 1
	}
	defer release()
	t, ok := openBus("receipt", *busDir, stderr)
	if !ok {
		return 2
	}
	me, found := t.Config.Lookup(*as)
	if !found {
		fmt.Fprintf(stderr, "nova-bus receipt: --as %q names no one on this bus (known: %s)\n", *as, oneline.Escape(strings.Join(t.Config.KnownNames(), "; ")))
		return 2
	}
	plan, err := bus.PlanReceipts(t, me, notes, now)
	if err != nil {
		fmt.Fprintf(stderr, "RECEIPT FAIL %s: %s\n", oneline.Escape(me.Name), oneline.Err(err))
		return 1
	}
	for _, already := range plan.Already {
		fmt.Fprintf(stdout, "RECEIPT ALREADY note=%s lane=%s\n", oneline.Field(already), oneline.Field(plan.Lane))
	}
	if len(plan.Record) == 0 {
		fmt.Fprintf(stdout, "RECEIPT OK recorded=0 already=%d commit=- pushed=false attempts=0\n", len(plan.Already))
		return 0
	}
	// The reader's own BEAT, as in send: `wait` wrote it, so it is this run's own
	// machinery and not a change that is "not this receipt" (#488).
	beat := bus.BeatPath(me.Lane)
	if err := checkoutReady(*busDir, *branch, []string{plan.Path, beat}); err != nil {
		fmt.Fprintf(stderr, "RECEIPT FAIL %s: %s\n", oneline.Escape(plan.Path), oneline.Err(err))
		return 1
	}
	if err := levelWithRemote(*busDir, *remote, *branch, *noPush); err != nil {
		fmt.Fprintf(stderr, "RECEIPT REFUSED: %s\n", oneline.Err(err))
		return 1
	}
	if err := plan.Append(*busDir); err != nil {
		fmt.Fprintf(stderr, "RECEIPT FAIL %s: %s\n", oneline.Escape(plan.Path), oneline.Err(err))
		return 1
	}
	paths, err := bus.StagePaths(*busDir, []string{plan.Path, beat})
	if err != nil {
		fmt.Fprintf(stderr, "RECEIPT FAIL %s: %s\n", oneline.Escape(plan.Path), oneline.Err(err))
		return 1
	}
	res, err := commit(*busDir, me, paths,
		bus.WithTrailer(plan.Message(me), bus.TrailerReceipt),
		*remote, *branch, *attempts, *noPush)
	if err != nil {
		fmt.Fprintf(stderr, "RECEIPT FAIL %s: %s\n", oneline.Escape(plan.Path), oneline.Err(err))
		printTranscript(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "RECEIPT OK recorded=%d already=%d commit=%s pushed=%t attempts=%d\n",
		len(plan.Record), len(plan.Already), oneline.Field(res.Commit), res.Pushed, res.Attempts)
	return 0
}

// cmdClose is `close --before`, the whole backlog at once: every open note dated before the
// stamp is closed by one receipt note each, batched in one commit. It is the other end of
// the INBOX OPEN large-list remedy line -- the normal answer is reply or receipt per note,
// and `close --before` is the explicit opt-in bulk cutoff: `--advance` draws a line past the
// history and leaves the notes behind it; `close` answers them, each with a Re line that
// removes it from the reader's open list for good.
func cmdClose(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("close")
	busDir := f.fs.String("bus", "", "the bus's repository root (required)")
	as := f.fs.String("as", "", "which participant you are (required)")
	beforeFlag := f.fs.String("before", "", "every open note addressed to you and dated before this RFC 3339 instant is closed by a receipt (required)")
	dryRun := f.fs.Bool("dry-run", false, "report what would be closed and write nothing")
	remote := f.fs.String("remote", "", "the git remote to push to (required to write)")
	branch := f.fs.String("branch", "", "the branch the bus lives on (required to write)")
	attempts := f.fs.Int("attempts", defaultAttempts, "how many times to push before giving up")
	gitSeconds := f.fs.Int("git-timeout", defaultGitTimeoutSeconds, "how long one git subprocess may take before this run gives up on it")
	noPush := f.fs.Bool("no-push", false, "commit but do not push")
	if !f.parse(args, stderr, map[string]*string{"bus": busDir, "as": as, "before": beforeFlag}) {
		return 2
	}
	if !f.attempts(*attempts, stderr) {
		return 2
	}
	if !f.gitTimeoutFlag(*gitSeconds, stderr) {
		return 2
	}
	before, err := time.Parse(time.RFC3339, *beforeFlag)
	if err != nil {
		fmt.Fprintf(stderr, "nova-bus close: --before %q is not an RFC 3339 instant; refusing to guess; run: nova-bus help\n", *beforeFlag)
		return 2
	}
	if !*dryRun {
		if !f.gitArgs(*remote, *branch, stderr) {
			return 2
		}
		if strings.TrimSpace(*remote) == "" || strings.TrimSpace(*branch) == "" {
			fmt.Fprint(stderr, "nova-bus close: writing onto the bus needs --remote and --branch; refusing to guess (or pass --dry-run); run: nova-bus help\n")
			return 2
		}
	}
	if err := bus.IsRepoRoot(*busDir); err != nil {
		fmt.Fprintf(stderr, "nova-bus close: %s\n", oneline.Err(err))
		return 2
	}
	t, ok := openBus("close", *busDir, stderr)
	if !ok {
		return 2
	}
	me, found := t.Config.Lookup(*as)
	if !found {
		fmt.Fprintf(stderr, "nova-bus close: --as %q names no one on this bus (known: %s)\n", *as, oneline.Escape(strings.Join(t.Config.KnownNames(), "; ")))
		return 2
	}
	plan, err := bus.PlanClose(t, me, before, now)
	if err != nil {
		fmt.Fprintf(stderr, "CLOSE FAIL %s: %s\n", oneline.Escape(me.Name), oneline.Err(err))
		return 1
	}
	// closed= COUNTS NOTES, not receipts. Since #1540 one receipt closes every note one
	// sender left before the stamp, so len(plan.Prepared) is the number of lanes answered
	// and would be a different, smaller and quite surprising number here.
	if *dryRun {
		fmt.Fprintf(stdout, "CLOSE OK closed=%d kept=%d commit=-\n", plan.Closed, plan.Kept)
		return 0
	}
	if len(plan.Prepared) == 0 {
		fmt.Fprintf(stdout, "CLOSE OK closed=0 kept=%d commit=-\n", plan.Kept)
		return 0
	}
	if err := checkoutReady(*busDir, *branch, nil); err != nil {
		fmt.Fprintf(stderr, "CLOSE FAIL: %s\n", oneline.Err(err))
		return 1
	}
	if err := levelWithRemote(*busDir, *remote, *branch, *noPush); err != nil {
		fmt.Fprintf(stderr, "CLOSE REFUSED: %s\n", oneline.Err(err))
		return 1
	}
	// A PARTIAL CLOSE COMPLETES OR LEAVES NOTHING BEHIND (#1540). The collision this fix
	// removes used to stop the loop at its second Save, with the first receipt written into
	// the working tree, no commit, and the cursor where it started -- so the next run met a
	// file it had not committed and the lane had to be cleaned by hand. Whatever stops the
	// loop now, the files this run wrote go back out of the tree before it returns.
	written := make([]string, 0, len(plan.Prepared))
	undo := func() {
		for i := len(written) - 1; i >= 0; i-- {
			if err := os.Remove(filepath.Join(*busDir, filepath.FromSlash(written[i]))); err != nil && !os.IsNotExist(err) {
				fmt.Fprintf(stderr, "CLOSE NOTE %s was written and could not be taken back: %s\n", oneline.Escape(written[i]), oneline.Err(err))
			}
		}
	}
	paths := make([]string, 0, len(plan.Prepared)+1)
	for i := range plan.Prepared {
		p := &plan.Prepared[i]
		if err := p.Save(*busDir); err != nil {
			undo()
			fmt.Fprintf(stderr, "CLOSE FAIL %s: %s\n", oneline.Escape(p.Path), oneline.Err(err))
			return 1
		}
		written = append(written, p.Path)
		if err := p.AppendIndex(*busDir); err != nil {
			undo()
			fmt.Fprintf(stderr, "CLOSE FAIL %s: %s\n", oneline.Escape(p.Path), oneline.Err(err))
			return 1
		}
		paths = append(paths, p.Path)
	}
	paths = append(paths, bus.IndexPath(me.Lane))
	wroteAttrs, err := bus.EnsureMergeAttributes(*busDir)
	if err != nil {
		fmt.Fprintf(stderr, "CLOSE FAIL %s: %s\n", oneline.Escape(bus.AttributesName), oneline.Err(err))
		return 1
	}
	if wroteAttrs {
		paths = append(paths, bus.AttributesName)
	}
	res, err := commit(*busDir, me, paths,
		bus.WithTrailer(plan.Message(me), bus.TrailerClose),
		*remote, *branch, *attempts, *noPush)
	if err != nil {
		fmt.Fprintf(stderr, "CLOSE FAIL: %s\n", oneline.Err(err))
		printTranscript(stderr, err)
		return 1
	}
	sha8 := res.Commit
	if len(sha8) > 8 {
		sha8 = sha8[:8]
	}
	// receipts= is new with #1540 and is the one number that changed shape: closed= counts
	// notes, as it always did and as the dry run above already did, and receipts= says how
	// many notes it took to close them -- one per sender lane. A reader who wants to know
	// whether a close collapsed 2964 files into a handful reads it here.
	fmt.Fprintf(stdout, "CLOSE OK closed=%d kept=%d receipts=%d commit=%s\n", plan.Closed, plan.Kept, len(plan.Prepared), oneline.Field(sha8))
	return 0
}

func cmdInbox(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("inbox")
	busDir := f.fs.String("bus", "", "the bus's repository root (required)")
	as := f.fs.String("as", "", "which participant you are (required)")
	maxWords := f.fs.Int("receipt-max-words", 0, "a body under this many words may be a receipt (required, at least 1)")
	full := f.fs.Bool("full", false, "walk the whole bus instead of what changed since your cursor")
	openList := f.fs.Bool("open", false, "list every open note, not only what is new; the default prints one INBOX OPEN line for them")
	openMax := f.fs.Int("open-max", defaultOpenMax, "with --open, how many carried entries to print before saying how many more there are")
	openWarn := f.fs.Int("open-warn", defaultOpenWarn, "how many carried entries before every return adds one line saying the list is large and how to empty it")
	bodies := f.fs.Bool("bodies", false, "print bodies for NEW notes, bounded by --max-notes and --max-bytes")
	maxNotes := f.fs.Int("max-notes", defaultBodiesNotes, "with --bodies, maximum NEW items to print")
	maxBytes := f.fs.Int64("max-bytes", defaultBodiesBytes, "with --bodies, maximum body bytes to print")
	maxCommits := f.fs.Int("max-commits", defaultMaxCommits, "how many commits a since-walk may cross before it stops and names the remedy; raise it to read a staler cursor")
	after := f.fs.String("after", "", "continue a bounded --bodies snapshot")
	advance := f.fs.Bool("advance", false, "move your cursor to HEAD and push it, the way a receipt is pushed")
	remote := f.fs.String("remote", "", "the git remote to push the cursor to (required with --advance)")
	branch := f.fs.String("branch", "", "the branch the bus lives on (required with --advance)")
	attempts := f.fs.Int("attempts", defaultAttempts, "how many times to push the cursor before giving up")
	gitSeconds := f.fs.Int("git-timeout", defaultGitTimeoutSeconds, "how long one git subprocess may take before this run gives up on it")
	noPush := f.fs.Bool("no-push", false, "with --advance, commit the cursor but do not push it")
	legacyBefore := f.fs.String("legacy-before", "", "notes dated before this UTC date (YYYY-MM-DD, midnight at its start) or UTC instant (RFC 3339, e.g. 2026-09-09T18:07:00Z) are not carried on your open list, and are counted rather than listed")
	legacyNow := f.fs.Bool("legacy-now", false, "draw the switch-day line at THIS run's UTC instant: exactly --legacy-before <now>, so everything already on the bus is history and everything after this moment is news")
	carryHistory := f.fs.Bool("carry-history", false, "on your FIRST --advance, carry every old note on your open list instead of drawing a switch-day line; does nothing otherwise")
	diagnostics := f.fs.Bool("diagnostics", false, "name every unreadable file with its reason, even ones already shown; the default collapses unchanged ones to one count line")
	askDecide := f.fs.Bool("decide", false, "ask the provider for a typed kind, needs_reply and blocked on every INBOX NOTE line")
	decideFloor := f.fs.Float64("floor", 0.9, "with --decide, the confidence floor below which a decision is only a suggestion")
	decideKeyEnv := f.fs.String("key-env", decide.DefaultKeyEnv, "with --decide, the environment variable holding the provider key")
	decideBaseURL := f.fs.String("base-url", decide.DefaultBaseURL, "with --decide, the provider endpoint")
	allowPrivate := f.fs.Bool("allow-private", false, "with --decide, send note text from a bus whose clone has no .public marker")
	if !f.parse(args, stderr, map[string]*string{"bus": busDir, "as": as}) {
		return 2
	}
	// --legacy-now IS --legacy-before, with the one value nobody can type worked out here:
	// this run's own UTC instant, in the same RFC 3339 shape the flag takes and the cursor
	// records. It is sugar and nothing else -- it goes through the same parse, lands in the
	// same LegacyLine, and is written into the cursor as the instant it named, so a reader
	// who runs it and a reader who pasted `date -u +%Y-%m-%dT%H:%M:%SZ` end up with the
	// same line. It exists because the correct shape was a twenty-character timestamp a
	// person had to produce before the run that needed it, and the friend this was written
	// for did not produce it: he drew a date instead, and his inbox went quiet.
	legacyValue := *legacyBefore
	if *legacyNow {
		// Two flags naming the same line say nothing about where it stands, whichever way
		// round they disagree. This is exit 2, a bad invocation, like every other pair.
		if strings.TrimSpace(*legacyBefore) != "" {
			fmt.Fprint(stderr, "nova-bus inbox: --legacy-now draws the switch-day line at this run's instant and --legacy-before draws it where you say; give one or the other\n")
			return 2
		}
		legacyValue = now.UTC().Format(bus.LegacyInstantLayout)
	}
	flagLegacy, ok := legacyLine("inbox", legacyValue, stderr)
	if !ok {
		return 2
	}
	// The two answers to the first-advance guard are answers to the SAME question -- what
	// this reader does about the history that was on the bus before them -- and giving both
	// says nothing about which. A line is drawn or it is not.
	if *carryHistory && !flagLegacy.Before.IsZero() {
		if *legacyNow {
			fmt.Fprint(stderr, "nova-bus inbox: --legacy-now draws a switch-day line and --carry-history says there is none to draw; give one or the other\n")
			return 2
		}
		fmt.Fprint(stderr, "nova-bus inbox: --legacy-before draws a switch-day line and --carry-history says there is none to draw; give one or the other\n")
		return 2
	}
	maxWordsValue, ok := f.receiptMaxWords(*maxWords, f.set("receipt-max-words"), *busDir, stderr)
	if !ok {
		return 2
	}
	if !f.count("open-max", *openMax, stderr) {
		return 2
	}
	if !f.atLeastZero("open-warn", *openWarn, stderr) {
		return 2
	}
	if *after != "" && !*bodies {
		fmt.Fprintln(stderr, "nova-bus inbox: --after requires --bodies")
		return 2
	}
	if *bodies && !bodyLimit(stderr, "--max-notes", int64(*maxNotes), maxBodiesNotesCeiling) {
		return 2
	}
	if *bodies && !bodyLimit(stderr, "--max-bytes", *maxBytes, maxBodiesBytesCeiling) {
		return 2
	}
	if !f.gitTimeoutFlag(*gitSeconds, stderr) {
		return 2
	}
	if !f.count("max-commits", *maxCommits, stderr) {
		return 2
	}
	// --advance WRITES to the bus, so it takes the same three flags a receipt takes and
	// refuses to guess any of them. Without it, inbox writes nothing at all, which is what
	// a report should do unless it was asked otherwise.
	if *advance {
		if strings.TrimSpace(*remote) == "" || strings.TrimSpace(*branch) == "" {
			fmt.Fprint(stderr, "nova-bus inbox: --advance moves a cursor onto the bus, so it needs --remote and --branch; refusing to guess; run: nova-bus help\n")
			return 2
		}
		if !f.attempts(*attempts, stderr) {
			return 2
		}
		if !f.gitArgs(*remote, *branch, stderr) {
			return 2
		}
	}
	if *askDecide && (*decideFloor < 0 || *decideFloor > 1) {
		fmt.Fprintf(stderr, "nova-bus inbox: --floor is a confidence and stands between 0 and 1 (got %g); refusing to guess; run: nova-bus help\n", *decideFloor)
		return 2
	}
	// A bus with no .public marker is a private one, and --decide would hand its note
	// text to a provider that may train on what it receives. The marker is the clone's
	// own statement that the bus is public; without it the run refuses by name, and
	// --allow-private is the one explicit way to mean it anyway.
	if *askDecide && !*allowPrivate {
		marker := filepath.Join(*busDir, publicMarker)
		if _, err := os.Stat(marker); err != nil {
			fmt.Fprintf(stderr, "INBOX REFUSED: --decide sends note text to a provider that may train on it, and %s has no %s marker; pass --allow-private to mean it anyway, or leave that clone private\n",
				oneline.Quote(*busDir), publicMarker)
			return 2
		}
	}
	o := inboxOpts{
		busDir: *busDir, as: *as, maxWords: maxWordsValue,
		full: *full, openList: *openList, openMax: *openMax, openWarn: *openWarn, advance: *advance,
		remote: *remote, branch: *branch, attempts: *attempts, noPush: *noPush,
		legacy: flagLegacy, carryHistory: *carryHistory,
		bodies: *bodies, maxNotes: *maxNotes, maxBytes: *maxBytes, after: *after,
		maxCommits: *maxCommits, walkProgress: true,
		diagnostics: *diagnostics,
		decide:      *askDecide, floor: *decideFloor, keyEnv: *decideKeyEnv, baseURL: *decideBaseURL,
	}
	// THE ROOT CHECK COMES BEFORE THE ROSTER, and it did not. Point --bus at a
	// subdirectory of a bigger repository and the run refused with "participants.json: no
	// such file" -- true, and the wrong sentence: the caller's mistake is the directory,
	// not the roster, and the refusal that names the repository root is the one that fixes
	// the invocation. The cheaper check is not the more useful one, so the more useful one
	// runs first.
	if !o.full || o.advance {
		if err := bus.IsRepoRoot(o.busDir); err != nil {
			fmt.Fprintf(stderr, "nova-bus inbox: reading only what changed, and moving a cursor, need git; %s\n", oneline.Err(err))
			return 2
		}
		release, code := lockCheckout("INBOX", o.busDir, stderr)
		if code != 0 {
			return code
		}
		defer release()
	}
	code, r := inboxListing(o, stdout, stderr, now)
	// r.Bounded is the listing that did not run: the since-walk stopped at --max-commits,
	// the remedy is already on stderr, and there is nothing read for a cursor to stand on.
	if code != 0 || r.Bounded || !o.advance || (o.bodies && r.AdvanceTo == "") {
		return code
	}
	if o.bodies {
		return advanceCursorTo(o.busDir, r.Me, r.Open, legacyToken(r.Legacy), r.AdvanceTo, o.remote, o.branch, o.attempts, o.noPush, now, stdout, stderr)
	}
	return advanceCursor(o.busDir, r.Me, r.Open, legacyToken(r.Legacy), o.remote, o.branch, o.attempts, o.noPush, now, stdout, stderr)
}

// inboxOpts is one listing's whole invocation, read from the flags and checked.
//
// It is a struct because there are now TWO verbs that produce an inbox listing -- `inbox`,
// and `wait`, which is `inbox` on a clock -- and a listing assembled twice is two inboxes
// that will disagree about something on the day one of them is changed. Everything below
// this line is written once and both verbs run it.
type inboxOpts struct {
	busDir, as     string
	maxWords       int
	full, openList bool
	// openMax is how many carried entries a listing run prints before it says how many it
	// did not, and openWarn is how large the open list gets before every return says so.
	// Both are on the struct rather than read at the print site because `wait` is `inbox`
	// on a clock and a flag one verb honoured and the other did not is two inboxes.
	openMax        int
	openWarn       int
	advance        bool
	remote, branch string
	attempts       int
	noPush         bool
	// legacy is the --legacy-before line as the flag gave it, or the zero line for no flag.
	// What a run actually reads under is effectiveLegacy of this and the cursor's own.
	legacy       bus.LegacyLine
	carryHistory bool
	bodies       bool
	maxNotes     int
	maxBytes     int64
	after        string
	// maxCommits bounds the since-walk: a cursor more than this many commits behind HEAD
	// stops the run with one INBOX WALK bounded line and a remedy rather than walking a
	// history nobody asked to read. Zero means the default; `wait` leaves it zero.
	maxCommits int
	// walkProgress is set by `inbox` (and not by `wait`, whose polls are short and plural)
	// so the since-walk reports INBOX WALK progress on stderr. The bound applies either
	// way; only the narration is a verb's choice.
	walkProgress bool
	diagnostics  bool
	// decide turns the semantic pass on: every INBOX NOTE line is sent to the
	// provider as one typed decision, and the line carries the answer. floor is the
	// confidence floor below which the answer is only a suggestion, keyEnv names the
	// environment variable holding the key, and baseURL is the provider endpoint.
	// All four are `inbox`'s only; `wait` leaves decide false.
	decide  bool
	floor   float64
	keyEnv  string
	baseURL string
	// quietBeats records that the caller passed `wait --quiet-beats`. Since #328 a change
	// that is only beats and cursors never wakes a wait, so the flag is accepted and
	// changes nothing; it is kept so callers that pass it keep working. It is `wait`'s
	// only; `inbox` leaves it false.
	quietBeats bool
	// me is the reader resolved against the roster, carried so `wait` can write their beat
	// without resolving the roster twice; beat is how often a wait pushes its BEAT file as
	// its own commit; and lease is how far into the future each BEAT's until= promises the
	// line is alive, so a manager cycle between two waits still reads awake. These are `wait`'s
	// only; `inbox` leaves them zero.
	me    bus.Participant
	beat  time.Duration
	lease time.Duration
}

// inboxReading is what one listing found, for the caller that has to act on it: `inbox`
// advances a cursor over it, and `wait` decides from it whether to stop waiting.
type inboxReading struct {
	// Me is the reader, resolved against the roster.
	Me bus.Participant
	// Open is the open list this run derived, which is what an --advance writes back.
	Open []bus.OpenEntry
	// Legacy is the switch-day line this run READ UNDER: the flag, or the cursor's.
	Legacy bus.LegacyLine
	// Cursor is the commit this run read from, and "" for a full read.
	Cursor string
	// Bounded says the since-walk STOPPED at --max-commits and this listing read nothing:
	// the run printed one INBOX WALK bounded line with the remedy and exited 0. It is on
	// the reading rather than in the exit code because "nothing to report" and "I did not
	// look" are the same 0 to a shell and must not be the same thing to a caller holding
	// --advance: a cursor moved over a walk nobody made takes every note behind the bound
	// as read.
	Bounded bool
	// Full says the run walked the whole bus.
	Full bool
	// SwitchDay says this listing PRINTED the INBOX SWITCH line: the reader's cursor
	// carries a forward-drawn date line, and the listing has already handed them the
	// command that redraws it. `wait` reads it so that the one sentence about a line
	// drawn forward is said once, by the listing, and not again by the clock around it.
	SwitchDay bool
	// New is how many notes this run would show a reader as NEWS: the notes it put on the
	// open list that were not on it before, and on a full read -- a reader with no cursor,
	// who has been shown nothing yet -- the whole open list. It is what `wait` returns on,
	// and it is deliberately not the size of the open list on an incremental run: a reader
	// carrying five hundred settled notes is not a reader with news.
	New int
	// HeardNew is how many of the notes this run would show as NEWS are already heard by
	// this reader -- receipted with `receipt --note` since the cursor, not answered. They
	// are still new to the open list (heard is not answered), but they are news the reader
	// has already taken; `wait --advance` skips them rather than returning on them.
	HeardNew int
	// Changed is how many lane paths the incremental diff named. It is scope.Changed, held
	// here for a caller that wants to tell a beat/cursor-only change from no change at all.
	Changed int
	// NoteChanges is how many of those changed paths were notes; see bus.InboxResult.
	NoteChanges int
	Next        string
	BodyBytes   int64
	BodyPrinted int
	BodyGaps    int
	AdvanceTo   string
}

// inboxListing is the whole of an inbox report: what to read, what it found, and every
// line of it printed. It writes nothing to the bus -- moving the cursor is advanceCursor,
// which its caller runs after it -- and it takes no lock: the caller holds the checkout,
// because `wait` holds it across a poll's fetch as well as its listing.
func inboxListing(o inboxOpts, stdout, stderr io.Writer, now time.Time) (int, inboxReading) {
	var r inboxReading
	// When --decide is on, this judges the notes it prints. It is lazy, so a listing
	// with no notes builds no client and calls no provider.
	var d *noteDecider
	if o.decide {
		d = newNoteDecider(o)
	}
	c, err := bus.LoadConfig(o.busDir)
	if err != nil {
		fmt.Fprintf(stderr, "nova-bus inbox: %s\n", oneline.Err(err))
		return 2, r
	}
	me, found := c.Lookup(o.as)
	if !found {
		fmt.Fprintf(stderr, "nova-bus inbox: --as %q names no one on this bus (known: %s)\n", o.as, oneline.Escape(strings.Join(c.KnownNames(), "; ")))
		return 2, r
	}
	if me.Lane == "" {
		fmt.Fprintf(stderr, "nova-bus inbox: %q has no lane on this bus, so nothing can answer for them\n", me.Name)
		return 2, r
	}

	scope := bus.Scope{Full: o.full}
	var res bus.InboxResult
	var cursor bus.Cursor
	var priorOpen []bus.OpenEntry
	// held is the cursor as it stands on the bus, read for the SWITCH-DAY LINE it carries
	// as well as for the commit. A full run reads it too, and ignores a cursor it cannot
	// read: `--full --advance` is the documented repair for a broken cursor, and a repair
	// that refuses to run is not one. What a full run must not do is silently forget a line
	// a reader drew months ago, which is what reading it here prevents.
	held := bus.Cursor{}
	if o.full {
		held, _ = bus.ReadCursor(o.busDir, me.Lane)
		// A full read rebuilds the current listing, but an existing valid OPEN file is
		// still the bookkeeping that distinguishes carried work from NEW bodies.  Keep
		// its survivors when a bounded full page advances; only newly eligible bodies
		// wait for their own emitted prefix.
		priorOpen, _ = bus.ReadOpen(o.busDir, me.Lane)
	}
	// The line this run reads under, whichever mode it is in.
	var legacy bus.LegacyLine
	if !o.full {
		cursor, err = bus.ReadCursor(o.busDir, me.Lane)
		if err != nil {
			fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.Err(err))
			return 1, r
		}
		held = cursor
		if cursor.Commit == "" {
			// A reader with no cursor has no place to stand, so the first run is a full
			// one and --advance gives them a cursor from then on. That is the adoption
			// path, and it costs exactly one full read, ever.
			scope.Full = true
		} else {
			// A line that MOVES EARLIER re-opens every note between the two dates, which
			// on the bus this was written for is hundreds -- and it would arrive as a
			// listing the reader has already settled, with nothing saying why. Moving it
			// later forgives more and is fine; moving it earlier is a refusal that names
			// the read which can honestly do it, because a --full run derives the whole
			// open list again rather than taking the cursor's word for it.
			if !o.legacy.Before.IsZero() && held.Legacy != "" && o.legacy.Before.Before(held.LegacyBefore()) {
				fmt.Fprintf(stderr, "INBOX REFUSED: your cursor was written with legacy=%s and --legacy-before %s moves the line earlier, which would put the notes between the two back on your open list; read once with --full --legacy-before %s --advance, which builds the open list again from the whole bus, or leave the flag off and the cursor's line stands\n",
					oneline.Field(held.Legacy), oneline.Field(o.legacy.Text), oneline.Field(o.legacy.Text))
				return 1, r
			}
			ok, err := bus.IsAncestor(o.busDir, cursor.Commit)
			if err != nil {
				fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.Err(err))
				return 1, r
			}
			if !ok {
				fmt.Fprintf(stderr, "INBOX REFUSED: the cursor %s is not an ancestor of HEAD, so a diff from it would report changes that are not changes and miss notes that are (a rewritten history, or a cursor from another branch); read once with --full, and --advance will replace it\n", oneline.Field(cursor.Commit))
				return 1, r
			}
			// The other way a cursor stops being trustworthy: the OPEN list it was
			// written beside is gone. An empty OPEN list is REMOVED rather than left
			// zero-length, so absent and nothing-open are one state on disk -- which is
			// why the cursor carries the count it was written with, and why a cursor
			// that says it was carrying notes with no OPEN beside it is refused here
			// instead of quietly reporting open=0 over the notes it dropped.
			if cursor.Counted && cursor.Open > 0 && !bus.OpenPresent(o.busDir, me.Lane) {
				fmt.Fprintf(stderr, "INBOX REFUSED: your cursor %s says it was carrying %d notes and %s is not on the bus, so a read from it would drop them and print open=0; read once with --full --advance, which rebuilds the open list from the whole bus\n",
					oneline.Field(cursor.Commit), cursor.Open, oneline.Field(bus.OpenPath(me.Lane)))
				return 1, r
			}
			// THE SINCE-WALK IS BOUNDED AND IT SAYS SO. A cursor left hundreds of commits
			// behind turns one diff into a long silence: on the bus this was written for a
			// 285-commit stale cursor over 2150 open notes ran four minutes and printed
			// nothing new. The count is cheap and comes first, so an over-bound cursor costs
			// one `rev-list --count` and one line rather than the walk. The line carries the
			// remedy and the run is exit 0: refusing to read is not this verb's business,
			// saying what the read would cost is.
			//
			// THE COUNT IS BOUNDED TOO, which is the half that was missing. `rev-list --count`
			// walks the whole distance before it can answer, so the run that reads NOTHING --
			// the over-bound one -- was paying for every commit between the cursor and the head
			// in order to be told it should not read them. Asked with the bound
			// (CommitsSinceBounded), git stops one commit past it: a cursor five hundred
			// commits behind and one fifty thousand commits behind now cost the same 501, and a
			// cursor inside the bound still gets its exact total for the progress line.
			limit := o.maxCommits
			if limit <= 0 {
				limit = defaultMaxCommits
			}
			total, over, err := bus.CommitsSinceBounded(o.busDir, cursor.Commit, limit)
			if err != nil {
				fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.Err(err))
				return 1, r
			}
			if over {
				fmt.Fprintf(stderr, "INBOX WALK bounded commits=%d %s\n", limit, boundedWalkRemedy)
				// THE BOUND ENDS THE RUN, AND THAT INCLUDES THE ADVANCE. This is the one
				// exit-0 way out of a listing that did not run, and the caller reads exit 0
				// plus --advance as "the listing is done, move the cursor". It used to hand
				// back the ZERO reading -- Me is resolved onto it at the END of a listing
				// that finished -- so the advance ran with an EMPTY LANE, wrote CURSOR at
				// the checkout ROOT, and left it there for every later run on that bus to
				// refuse over until a human deleted it.
				//
				// Both halves are named here: who the reader is, so nothing downstream is
				// guessing, and Bounded, which is the caller's instruction not to advance. A
				// walk that read NOTHING has no claim to make -- advancing over it would take
				// every unread note behind the bound as read, which is the one outcome the
				// bound exists to prevent.
				r.Me, r.Bounded = me, true
				return 0, r
			}
			var walk *walkProgress
			// A continuation (`--after`) is an explicit resume of a bounded snapshot, and its
			// refusals are the ONE line refuseContinuation prints, validated AFTER this walk:
			// narrating the walk first would put a second line above a refusal the contract
			// says is one. So the narration is for the ordinary inbox read and the resume
			// stays quiet; the bound above still applies to both.
			if o.walkProgress && o.after == "" {
				walk = newWalkProgress(stderr, total)
			}
			changed, err := bus.ChangedSince(o.busDir, cursor.Commit)
			if err != nil {
				if walk != nil {
					walk.abort()
				}
				fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.Err(err))
				return 1, r
			}
			if walk != nil {
				// The commits are behind us; what is left is parsing the notes they named.
				walk.advance(total, 0)
			}
			open, err := bus.ReadOpen(o.busDir, me.Lane)
			if err != nil {
				if walk != nil {
					walk.abort()
				}
				fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.Err(err))
				return 1, r
			}
			priorOpen = open
			scope.From, scope.Changed = cursor.Commit, len(changed)
			legacy = effectiveLegacy(o.legacy, held)
			res, err = bus.InboxSince(o.busDir, c, me, changed, open, o.maxWords, legacy)
			if err != nil {
				if walk != nil {
					walk.abort()
				}
				fmt.Fprintf(stderr, "nova-bus inbox: %s\n", oneline.Err(err))
				return 2, r
			}
			if walk != nil {
				walk.finish(total, res.NoteChanges)
			}
		}
	}
	if scope.Full {
		t, err := bus.ReadBus(o.busDir, c)
		if err != nil {
			fmt.Fprintf(stderr, "nova-bus inbox: %s\n", oneline.Err(err))
			return 2, r
		}
		legacy = effectiveLegacy(o.legacy, held)
		// A full read is the one that DERIVES the open list rather than inheriting it: the
		// display line of every open note, the heard flag of every receipted one rebuilt from
		// RECEIPTS, and the unreadable files carried rather than named once and dropped. Every
		// later incremental run prints from what this run writes.
		res.Unreadable = t.Unreadable(me.Lane)
		// EVERY note on the bus that reaches no reader, named on a full read to every
		// reader. A note whose To line resolves to nobody parses, so it is not unreadable,
		// and it is in no inbox, so no listing has ever mentioned it: 22 of them on the
		// family's real bus. See internal/bus/unaddressed.go.
		res.Unaddressed = t.UnaddressedOnBus()
		// The line still shapes the open list a full run WRITES -- an old note is not
		// carried, and neither is an old file nobody can read -- but a full run LISTS
		// everything it found, unreadable files included, whatever their date. That is what
		// a full read is for: the whole picture, asked for on purpose, on the day you adopt
		// the tool or the day something looks wrong. The quiet is the incremental run's.
		res.Open, res.Legacy, res.LegacyUnreadable = bus.SplitLegacy(bus.OpenFromFull(t.Inbox(me, o.maxWords), res.Unreadable), legacy)
	}

	// THE FIRST ADVANCE ON A LANE, on a bus that is older than this reader's cursor.
	//
	// THE FAILURE, from a live line. Its first run was
	// `inbox --full --advance` with no switch-day line, on a bus holding about 1,900 notes.
	// It worked exactly as written: 602 old notes went onto the open list, the cursor was
	// written beside them, and every poll from then on printed the same 602 carried notes,
	// forever, because an open note only comes off the list when something answers it.
	// Glenn, reading the polls: "lots of spam there. do we need so much spam? it costs $$$".
	// Every one of those lines was paid for, on every run, by a reader who had never said
	// they meant to carry the history.
	//
	// The switch-day line already existed and would have prevented all of it. What did not
	// exist was anything making the reader MEET it: the flag had to be known about before
	// the run that needed it, and the run that needed it is by definition the first one.
	// So the first advance on a lane is the one place this tool asks. It fires only when
	// all four are true -- an --advance, no CURSOR file on this lane, no line in force, and
	// notes on the open list dated before today -- and it names the number it would have
	// carried and the exact line to run. `--carry-history` is the other answer, for the
	// reader who means it, and it is a flag rather than a default because the default that
	// carried 602 notes is the one being fixed.
	//
	// It refuses BEFORE the listing is printed. A first full read of an old bus prints a
	// line per open note, which on that bus is the six hundred lines this guard exists to
	// stop; printing them and then refusing would charge the reader for them anyway.
	if o.advance && !o.carryHistory && legacy.Before.IsZero() && !bus.CursorPresent(o.busDir, me.Lane) {
		_, oldNotes, oldUnreadable := bus.SplitLegacy(res.Open, bus.LegacyLine{Before: utcDay(now)})
		old := oldNotes + oldUnreadable
		if old > 0 {
			// THE SUGGESTED LINE IS AN INSTANT, and it used to be tomorrow's DATE. A date
			// is midnight at its START, so tomorrow's date is a moment AFTER everything
			// written today: the line this guard handed a stuck reader made the whole switch
			// day legacy, and the notes they were being refused for carrying were joined by
			// every note anybody sent while they were reading the refusal. Drawn at an
			// instant, everything on the bus at that moment is history and everything after
			// it is news, which is the sentence this message actually makes.
			//
			// It hands over --legacy-now rather than the timestamp it would print, and that
			// is a second fix on top of the first. A pasted timestamp is a line drawn at the
			// moment of the REFUSAL, which may be an hour before the reader gets round to
			// running it; --legacy-now is drawn at the moment of the READ. And a command
			// nobody has to retype correctly is a command nobody mistypes into a date, which
			// is exactly what the reader this was written for did.
			fmt.Fprintf(stderr, "INBOX REFUSED: this is the first advance on %s and %d of the %d notes it would carry are dated before now, so every run after it would print all %d again; draw the switch-day line at this instant with `nova-bus inbox --bus %s --as %s --receipt-max-words %d --full --legacy-now --advance --remote %s --branch %s`, which takes everything already on the bus as read and leaves you what arrives after that moment, or pass --carry-history to carry all %d\n",
				oneline.Field(bus.CursorPath(me.Lane)), old, len(res.Open), len(res.Open),
				oneline.Quote(o.busDir), oneline.Quote(me.Name), o.maxWords,
				oneline.Quote(o.remote), oneline.Quote(o.branch), len(res.Open))
			return 1, r
		}
	}

	// What was walked, said FIRST, because a listing that does not say what it looked at
	// is a listing a reader will mistake for everything.
	fmt.Fprintf(stdout, "INBOX SCOPE mode=%s cursor=%s changed=%d carrying=%d\n",
		oneline.Field(scope.Mode()), oneline.Field(dash(cursor.Commit)), scope.Changed, len(res.Open))
	// AND THEN, IF THIS READER'S LINE IS DRAWN FORWARD, THE SENTENCE THAT SAYS SO. It is
	// printed from the cursor as it stands on the bus -- not from the flag this run was
	// given -- because it is a fact about the state a reader is stuck in, and the run that
	// is finally fixing it should say once what it is fixing. See the site below and
	// bus.LegacyDateAtOrAfterToday.
	r.SwitchDay = printSwitchDayNote(stdout, held.Legacy, o.busDir, me.Name, fmt.Sprintf("%d", o.maxWords), o.remote, o.branch, now)
	// The switch-day line, said as ONE line and next to the scope, because it is part of
	// what this run looked at: `notes=` is how many notes it left off the open list for
	// being older than the line, and `unreadable=` how many FILES it left off for the same
	// reason. They are never listed one by one -- the whole point of the line is that six
	// hundred of them do not become a listing -- and this is the only place a reader is
	// told either number, so it is printed whenever a line is in force, counts of zero
	// included. Two numbers and not one, because they are two different facts: a note taken
	// as read, and a file nobody could read in the first place.
	if !legacy.Before.IsZero() {
		fmt.Fprintf(stdout, "INBOX LEGACY before=%s notes=%d unreadable=%d\n",
			oneline.Field(legacy.Text), res.Legacy, res.LegacyUnreadable)
	}
	// The files that would not parse are named next and are never silent. A note on the
	// bus that this tool cannot read is not a note that does not exist, and dropping it
	// from the listing was the same failure as a lost push with a quieter cause. The one
	// thing that is not named per file is a file dated BEHIND the switch-day line on an
	// incremental run -- history, counted on the LEGACY line above; see bus.LegacyLine.
	//
	// An unreadable file that is CARRIED rather than new is unchanged since the cursor:
	// the reader has already been shown it, and a long list of them printed on every poll
	// buries the inbox above them. So the default collapses an unchanged set to one count
	// line. The per-file lines stay for anything new -- which is news, and needs its reason
	// before a reader can fix it -- for a full read, which lists everything on purpose,
	// and for --diagnostics, which asks for the whole picture whatever the default is.
	if len(res.Unreadable) > 0 && (o.diagnostics || res.UnreadableChanged || scope.Full) {
		for _, n := range res.Unreadable {
			fmt.Fprintf(stdout, "INBOX UNREADABLE path=%s: %s\n", oneline.Field(n.Path), oneline.Err(n.Parse.Err))
		}
	} else if len(res.Unreadable) > 0 {
		fmt.Fprintf(stdout, "INBOX UNREADABLE count=%d unchanged=true first=%s\n",
			len(res.Unreadable), oneline.Field(res.Unreadable[0].Path))
	}
	// And the notes that PARSE and reach nobody, which is the quieter half of the same
	// failure: a file this tool cannot read is at least reported, and a note addressed to a
	// name the roster does not hold was in no listing at all. Whichever way the run was
	// asked, for the same reason UNREADABLE is: a note nobody is shown is not a listing
	// choice.
	for _, u := range res.Unaddressed {
		fmt.Fprintf(stdout, "INBOX UNADDRESSED path=%s: %s\n", oneline.Field(u.Path), oneline.Escape(u.Reason))
	}
	notes, receipts, heard := res.Counts()
	// WHAT IS NEW, IN FULL, ON EVERY RUN. This is the listing a poll is FOR, and until now
	// there was no way to get it on its own: the choice was one summary line, or the whole
	// carried list. So a reader who wanted to see the note that had just arrived asked for
	// `--open` and got every note they had ever failed to answer, on every return, above the
	// one they were looking for. A line on a 260K-token model did exactly that with 74
	// carried notes and blew its context. News and backlog are different questions, and the
	// news is the one every run answers.
	//
	// It is skipped on a run that is about to list the WHOLE open list -- `--open`, or a
	// full read -- because the new entries are in that list and printing them twice is the
	// same noise from the other end.
	listCarried := o.openList || scope.Full
	if o.bodies || !listCarried {
		if o.bodies {
			head, headErr := bus.HeadCommit(o.busDir)
			if headErr != nil {
				fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.Err(headErr))
				return 1, r
			}
			base, expected := cursor.Commit, cursor.Commit
			if scope.Full {
				base, expected = held.Commit, held.Commit
			}
			snapshot := bus.BodySnapshot{Base: base, Head: head, Reader: me.Name, Selector: "inbox-new"}
			if o.after != "" {
				snapshot, _, headErr = bus.BodyContinuation(o.after)
				if headErr != nil {
					return refuseContinuation(stderr, headErr), r
				}
				if snapshot.Reader != me.Name || snapshot.Selector != "inbox-new" {
					return refuseContinuation(stderr, errors.New("it belongs to another reader or selector")), r
				}
			}
			// Rebuild C0..H directly. Current OPEN is deliberately not an input: a note
			// after H may have answered an older item, while that older token still owes
			// the body from its immutable snapshot.
			items, pageErr := bus.BodyNewItemsAtSnapshot(o.busDir, snapshot, c, me, o.maxWords, legacy)
			if pageErr != nil {
				if o.after != "" {
					return refuseContinuation(stderr, pageErr), r
				}
				fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.Err(pageErr))
				return 2, r
			}
			page, pageErr := bus.BodyPageFor(items, bus.BodyPageRequest{Snapshot: snapshot, ExpectedCursor: expected, Advance: o.advance, Token: o.after, MaxNotes: o.maxNotes, MaxBytes: o.maxBytes})
			if pageErr != nil {
				if o.after != "" {
					return refuseContinuation(stderr, pageErr), r
				}
				fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.Err(pageErr))
				return 2, r
			}
			if err := printBodyPage(stdout, page, o.maxBytes, d); err != nil {
				if o.decide {
					fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.Err(err))
					return 2, r
				}
				fmt.Fprintf(stderr, "INBOX FAIL output: %s\n", oneline.Err(err))
				return 1, r
			}
			r.BodyPrinted, r.BodyBytes, r.BodyGaps, r.Next = page.Frames, page.PrintedBytes, page.GapCount, page.Next
			// Only the NEW entries fully emitted on this page become carried.  Keeping
			// every entry from the current listing here would move CURSOR past B while
			// silently placing B in OPEN, so its body would never get its own NEW turn.
			r.Open = openAfterBodies(res.Open, res.Fresh, priorOpen, items, page, scope.Full)
			if page.SafeFrontier != expected {
				r.AdvanceTo = page.SafeFrontier
			}
			// THE REMEDY STANDS IMMEDIATELY BEFORE THE RECEIPT AND NOWHERE ELSE. A chain
			// holding many gaps names the earliest one and prints no list, so this is one
			// line at every state -- and it is on stdout, beside the receipt it explains,
			// because a caller reconciling `complete=false` reads one stream.
			if page.Drained && !page.Complete && page.EarliestGap != nil {
				gap := page.EarliestGap
				kind, retry := "over-budget", strconv.FormatInt(gap.Bytes, 10)
				if gap.Bytes > maxBodiesBytesCeiling {
					// No value under the hard ceiling carries it, so there is no number to
					// name: the door is the file, and path= is what opens it.
					kind, retry = "over-ceiling", "-"
				}
				if _, err := fmt.Fprintf(stdout, "INBOX BODIES GAP id=%s kind=%s retry-max-bytes=%s path=%s\n", oneline.Field(dash(gap.ID)), oneline.Field(kind), oneline.Field(retry), oneline.Field(gap.Path)); err != nil {
					fmt.Fprintf(stderr, "INBOX FAIL output: %s\n", oneline.Err(err))
					return 1, inboxReading{}
				}
			}
			if _, err := fmt.Fprintf(stdout, "INBOX BODIES printed=%d bytes=%d oversize=%d gaps=%d drained=%t complete=%t next=%s\n", r.BodyPrinted, r.BodyBytes, len(page.Gaps), r.BodyGaps, page.Drained, page.Complete, oneline.Field(dash(r.Next))); err != nil {
				fmt.Fprintf(stderr, "INBOX FAIL output: %s\n", oneline.Err(err))
				return 1, inboxReading{}
			}
		} else {
			if _, err := printOpenEntries(stdout, res.Fresh, len(res.Fresh), d); err != nil {
				if o.decide {
					fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.Err(err))
					return 2, r
				}
				fmt.Fprintf(stderr, "INBOX FAIL output: %s\n", oneline.Err(err))
				return 1, r
			}
		}
	}
	// THEN ONE LINE FOR THE BACKLOG, whichever way the run was asked. It was printed only on
	// the runs that did NOT list, which meant the two shapes of return had no line in common
	// and a reader parsing `--open` output could not find the count at all. One line, always,
	// under one name. The large-list sentence used to be a SECOND line, past a size, with a
	// whole command pasted into it; a reader parsing OPEN could not tell small from large
	// without knowing the threshold, and the command duplicated every run's own flags. One
	// line carries both now: `large=` is the sentence, and `remedy=` names the one whole-
	// backlog way out without spelling a command the caller already built. A large set's
	// remedy is not `inbox --advance`: that moves the read cursor while the OPEN entries
	// stay carried, so it loops without resolving anything. Reply or receipt each note is
	// the normal answer, and `close --before` is the explicit opt-in bulk cutoff.
	if len(res.Open) > o.openWarn {
		fmt.Fprintf(stdout, "INBOX OPEN carrying=%d heard=%d large=true remedy=%s\n",
			len(res.Open), heard, remedyLarge)
	} else {
		fmt.Fprintf(stdout, "INBOX OPEN carrying=%d heard=%d large=false remedy=%s\n",
			len(res.Open), heard, remedyAdvance)
	}
	// LISTING THE CARRIED NOTES IS A CHOICE, and the default is not to. A reader carrying five
	// hundred notes gets five hundred lines on every run, and the one new note is in the
	// middle of them -- which is the listing-nobody-reads failure the switch-day line exists
	// to stop, arriving from the other end. So `--open` prints the entries, and `--full` lists
	// them too because a full read is what a person asks for when they want the whole picture.
	// Nothing is hidden either way: the counts are on the OK line, and the entries are in
	// OPEN, which is a file.
	//
	// AND IT IS CAPPED. `--open` on a long list was the footgun itself: a flag whose cost
	// grows with the backlog, asked for by the reader with the biggest backlog, printed into
	// a context window that has no way to refuse it. --open-max is the cap, it has a default
	// so that nobody has to know about it before the run that needed it, and the line under
	// the listing says how many were left and which flag widens it.
	if listCarried {
		rows := bus.SortForListing(res.Open)
		shown, err := printOpenEntries(stdout, rows, o.openMax, d)
		if err != nil {
			if o.decide {
				fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.Err(err))
				return 2, r
			}
			fmt.Fprintf(stderr, "INBOX FAIL output: %s\n", oneline.Err(err))
			return 1, r
		}
		if more := countListable(rows) - shown; more > 0 {
			fmt.Fprintf(stdout, "INBOX OPEN listed=%d and %d more (--open-max to widen)\n", shown, more)
		}
	}
	// AND, PAST A SIZE, THE LINE THAT SAYS THE LIST IS LARGE AND HOW TO EMPTY IT. A backlog
	// grows one unanswered note at a time and nothing about any single run says it is
	// growing: `carrying=74` is a number, and a number is not a sentence. The line that
	// carried 74 was answering every one of those notes by hand -- and none of the answers
	// carried a Re line, so none of them closed anything, and nothing anywhere said so.
	// The word for it is now `large=` on the one OPEN line above, and the remedy names the
	// normal answer (reply or receipt each carried note) with `close --before` as the
	// explicit opt-in bulk cutoff, never `--advance` alone.
	// THE TWO NUMBERS, and why they are both here under the names they are printed under
	// elsewhere. `carrying=` on the SCOPE and OPEN lines is the size of the OPEN LIST -- every
	// entry on it, the heard and the unreadable included -- and `open=` is what is still
	// WAITING ON ME, which is the notes and the bare receipts and nothing else. A note I
	// have receipted is on the list and out of that count: I have answered the sender's
	// question about whether it arrived, and not their note. The two therefore differ, by
	// exactly heard + unreadable, and on one real run they read `carrying=658` and
	// `open=657` on two lines with no third line saying why. So the OK line now carries
	// BOTH, under the same names, beside the decomposition that makes them add up.
	fmt.Fprintf(stdout, "INBOX OK as=%s carrying=%d open=%d notes=%d receipts=%d heard=%d unaddressed=%d unreadable=%d\n",
		oneline.Field(me.Name), len(res.Open), notes+receipts, notes, receipts, heard, len(res.Unaddressed), len(res.Unreadable))
	// AND, LAST, WHAT --decide FOUND. n is the notes it judged, needs_reply how many of
	// them at or above 0.5, and below_floor how many kinds the provider was less sure of
	// than the floor -- those are suggestions, and the caller keeps today's behaviour.
	if d != nil {
		fmt.Fprintf(stdout, "INBOX DECIDED n=%d needs_reply=%d below_floor=%d wake=%d\n",
			d.counts.n, d.counts.needsReply, d.counts.belowFloor, d.counts.wake)
	}
	r.Me, r.Legacy, r.Cursor, r.Full = me, legacy, cursor.Commit, scope.Full
	r.Changed, r.NoteChanges = scope.Changed, res.NoteChanges
	if !o.bodies {
		r.Open = res.Open
	}
	// What this run would show a reader as news; see inboxReading.New.
	r.New = res.New
	if scope.Full {
		r.New = len(res.Open)
	}
	for _, e := range res.Fresh {
		if e.Heard {
			r.HeardNew++
		}
	}
	return 0, r
}

func openAfterBodies(current, fresh, prior []bus.OpenEntry, items []bus.BodyItem, page bus.BodyPage, full bool) []bus.OpenEntry {
	freshPath := make(map[string]bool, len(fresh))
	for _, entry := range fresh {
		freshPath[entry.Path] = true
	}
	// A full read derives OPEN anew.  Its listing may contain every outstanding note
	// while a bodies page has emitted only one, so retaining current here would move a
	// cursor past unprinted bodies.  Incremental reads retain only their prior carry;
	// current-fresh rows are rebuilt below from the immutable emitted prefix.
	out := make([]bus.OpenEntry, 0, len(current)+len(items))
	if full {
		currentByPath := make(map[string]bus.OpenEntry, len(current))
		for _, entry := range current {
			currentByPath[entry.Path] = entry
		}
		for _, entry := range prior {
			if survivor, ok := currentByPath[entry.Path]; ok {
				out = append(out, survivor)
			}
		}
	} else {
		for _, entry := range current {
			if !freshPath[entry.Path] {
				out = append(out, entry)
			}
		}
	}
	inOut := make(map[string]bool, len(out))
	for _, entry := range out {
		inOut[entry.Path] = true
	}
	// SafeFrontier is precisely the greatest complete emitted prefix.  Re-add every
	// eligible item through it, rather than only this page's Items: the final page of a
	// two-note commit must persist both A and B, and a later page must not replace A.
	frontier := -1
	for i, item := range items {
		if item.Commit == page.SafeFrontier {
			frontier = i
		}
	}
	for i := 0; i <= frontier; i++ {
		item := items[i]
		if !inOut[item.Entry.Path] {
			out = append(out, item.Entry)
			inOut[item.Entry.Path] = true
		}
	}
	return out
}

// effectiveLegacy is the line a run reads under: the flag when it is given, and otherwise
// the line this reader's cursor was already written with. A line that had to be retyped on
// every run is a line that would be forgotten on one, and the run that forgot it would open
// every note behind it at once.
func effectiveLegacy(flag bus.LegacyLine, held bus.Cursor) bus.LegacyLine {
	if !flag.Before.IsZero() {
		return flag
	}
	return held.LegacyLine()
}

// legacyToken is the line as a cursor records it -- the text it was given, so that a line
// drawn to the second is stored to the second -- or "" for no line at all.
func legacyToken(l bus.LegacyLine) string {
	if l.Before.IsZero() {
		return ""
	}
	return l.Text
}

// advanceCursor writes this reader's CURSOR and OPEN, commits them under their own
// identity, and pushes them with the same protocol a receipt uses.
//
// The same protocol and not a lighter one, on purpose. A cursor is a claim that everything
// up to a commit has been read, and a claim only one bench can see is a claim nobody at the
// bus can check -- the same reason a receipt is a file and not a mood. So it refuses a
// dirty checkout, refuses a branch ahead of the remote, commits naming its paths under the
// roster's identity, and recovers a rejected push by fetching and rebasing.
//
// It runs AFTER the listing is printed. A reader who was shown their inbox and whose push
// then failed has still been shown their inbox; the cursor simply has not moved, and the
// next run shows them the same notes again -- which is the safe direction to fail in.
//
// The cursor also records the SWITCH-DAY LINE this run read under, so the next run honours
// it without the flag and everybody on the bus can see which notes this reader has taken
// as read.
func advanceCursorTo(busDir string, me bus.Participant, open []bus.OpenEntry, legacy, head, remote, branch string, attempts int, noPush bool, now time.Time, stdout, stderr io.Writer) int {
	// NO LANE, NO WRITE, AND NOTHING TOUCHED. Every state path this function builds is
	// lane + "/" + name, so a lane-less reader names "/CURSOR" and "/OPEN" -- absolute
	// paths that land at the checkout ROOT and that git refuses to stage as outside the
	// repository. That is how the 2026-09-19 break ended: the cursor was written at the
	// root, the staging failed, and the stray file refused every later run on the bus.
	//
	// A lane-less reader is a shape the roster can hold -- a participant with no lane of
	// their own is listed so they can be addressed -- so this is a refusal and not a
	// panic, and it comes FIRST, before the checkout is read or a byte is written. The
	// cost of the old order was never the exit code; it was the file left behind.
	if me.Lane == "" {
		// A reader who reached here with no name either is not on the roster at all or was
		// never resolved against it, and "" has no lane on this bus is a line nobody can act
		// on. The refusal says which reader it is when it knows and says so plainly when it
		// does not.
		who := me.Name
		if who == "" {
			who = "this reader"
		}
		fmt.Fprintf(stderr, "INBOX REFUSED: %s has no lane on this bus, so there is nowhere to write a cursor\n", oneline.Field(who))
		return 1
	}
	paths := []string{bus.CursorPath(me.Lane), bus.OpenPath(me.Lane), bus.BeatPath(me.Lane)}
	if err := checkoutReady(busDir, branch, paths); err != nil {
		fmt.Fprintf(stderr, "INBOX FAIL %s: %s\n", oneline.Escape(bus.CursorPath(me.Lane)), oneline.Err(err))
		return 1
	}
	if err := levelWithRemote(busDir, remote, branch, noPush); err != nil {
		fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.Err(err))
		return 1
	}
	if err := bus.WriteOpen(busDir, me.Lane, open); err != nil {
		fmt.Fprintf(stderr, "INBOX FAIL %s: %s\n", oneline.Escape(bus.OpenPath(me.Lane)), oneline.Err(err))
		return 1
	}
	if err := bus.WriteCursor(busDir, me.Lane, head, len(open), legacy, now); err != nil {
		fmt.Fprintf(stderr, "INBOX FAIL %s: %s\n", oneline.Escape(bus.CursorPath(me.Lane)), oneline.Err(err))
		return 1
	}
	// The commit names both paths, so an OPEN file this run REMOVED -- a reader with
	// nothing left open -- is staged as the deletion it is rather than left behind. A
	// reader who never had one is a different case: there is nothing to record, and asking
	// git to stage a path that is neither on disk nor in the index is exit 128.
	staged, err := bus.StagePaths(busDir, paths)
	if err != nil {
		fmt.Fprintf(stderr, "INBOX FAIL %s: %s\n", oneline.Escape(bus.CursorPath(me.Lane)), oneline.Err(err))
		return 1
	}
	res, err := commit(busDir, me, staged,
		bus.WithTrailer(me.Slug()+": read to "+head[:shortSHA], bus.TrailerCursor),
		remote, branch, attempts, noPush)
	if err != nil {
		fmt.Fprintf(stderr, "INBOX FAIL %s: %s\n", oneline.Escape(bus.CursorPath(me.Lane)), oneline.Err(err))
		printTranscript(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "INBOX CURSOR commit=%s carrying=%d pushed=%t attempts=%d\n",
		oneline.Field(head), len(open), res.Pushed, res.Attempts)
	return 0
}

// advanceCursor preserves the released full-head behaviour for listings without bodies.
// Bodies mode calls advanceCursorTo with the paginator's whole-commit safe frontier.
func advanceCursor(busDir string, me bus.Participant, open []bus.OpenEntry, legacy, remote, branch string, attempts int, noPush bool, now time.Time, stdout, stderr io.Writer) int {
	head, err := bus.HeadCommit(busDir)
	if err != nil {
		fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.Err(err))
		return 1
	}
	return advanceCursorTo(busDir, me, open, legacy, head, remote, branch, attempts, noPush, now, stdout, stderr)
}

// defaultOpenMax is how many carried entries `--open` prints before it says how many it
// did not. Twenty is a screen: enough that a reader with an ordinary backlog sees all of
// it, and few enough that a reader with a big one is not paying for the whole of it on
// every return. It is a DEFAULT rather than a required flag, on the same test the tool's
// other defaults pass -- it is not a fact about a bus that only its owner can supply --
// and the line under the listing names the flag that widens it.
const defaultOpenMax = 20

// defaultOpenWarn is how large a backlog gets before every return says so. Forty is above
// what a working line carries between reads and below the seventy-four that broke one, so
// the line fires while a backlog is still answerable and not after it is hopeless.
const defaultOpenWarn = 40

// remedyAdvance is the INBOX OPEN `remedy=` for a small list: moving the read cursor past
// what is already heard is the normal small-list action. It never claims to close carried
// entries.
const remedyAdvance = "inbox --advance"

// remedyLarge is the INBOX OPEN `remedy=` for a large carrying set. Reply or receipt is
// the normal path that actually resolves the carried notes, and `close --before` is named
// only as the explicit opt-in bulk cutoff -- never `--advance` alone, which loops without
// resolving anything.
const remedyLarge = "reply or receipt each note, or close --before <instant> as an explicit bulk cutoff"

// defaultMaxCommits is how many commits a since-walk may cross before it stops and hands
// back the remedy instead of walking. A cursor hundreds of commits stale was left, not a
// bus that is unreadable, and the run says so in one line rather than spending minutes
// parsing what the reader never asked for. It is a DEFAULT for the same reason `--attempts`
// is: it is not a fact about a bus only its owner can supply, and a caller who has to name
// a number names one too small and is then refused the read they wanted.
const defaultMaxCommits = 500

// boundedWalkRemedy is the one-line remedy an INBOX WALK bounded line carries. It names
// both doors: raise the bound to read the stale cursor, or draw a switch-day line with
// close --before to take the history as read and start the cursor over.
const boundedWalkRemedy = `remedy="raise --max-commits or close --before <instant>"`

// walkProgress narrates a since-walk on stderr:
//
//	INBOX WALK commits=<n>/<total> notes=<n> elapsed=<s>
//
// at most once a second, and once at the end. The throttle is what makes it a progress
// line rather than a transcript: a fast walk prints exactly one, at the end, and a slow one
// prints one a second until it is done. It exists because a program that takes longer than
// 0.1 s says what it is doing, and the failure this closes ran four minutes in silence.
type walkProgress struct {
	mu      sync.Mutex
	stderr  io.Writer
	start   time.Time
	last    time.Time
	total   int
	done    int
	notes   int
	stop    chan struct{}
	stopped sync.WaitGroup
}

// newWalkProgress starts the ticker. The caller must call finish, which stops it.
func newWalkProgress(stderr io.Writer, total int) *walkProgress {
	p := &walkProgress{stderr: stderr, start: time.Now(), total: total, stop: make(chan struct{})}
	p.last = p.start
	p.stopped.Add(1)
	go func() {
		defer p.stopped.Done()
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-p.stop:
				return
			case <-t.C:
				p.emit(false)
			}
		}
	}()
	return p
}

// emit writes one progress line unless a line was written less than a second ago and force
// is false. The lock spans the write so the ticker and the closing line cannot interleave,
// which is what keeps a plain bytes.Buffer stderr safe under a test.
func (p *walkProgress) emit(force bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	if !force && now.Sub(p.last) < time.Second {
		return
	}
	p.last = now
	fmt.Fprintf(p.stderr, "INBOX WALK commits=%d/%d notes=%d elapsed=%s\n",
		p.done, p.total, p.notes, oneline.Field(now.Sub(p.start).Round(time.Millisecond).String()))
}

// advance records how far the walk has got and writes a throttled line, so the note parse
// after the commit walk still shows the commits behind it as done rather than at zero.
func (p *walkProgress) advance(done, notes int) {
	p.mu.Lock()
	p.done, p.notes = done, notes
	p.mu.Unlock()
	p.emit(false)
}

// finish stops the ticker, records what the walk found and writes the closing line. It is
// the one write that always happens, so a walk that never reached a second still says it
// ran.
func (p *walkProgress) finish(done, notes int) {
	close(p.stop)
	p.stopped.Wait()
	p.mu.Lock()
	p.done, p.notes = done, notes
	p.mu.Unlock()
	p.emit(true)
}

// abort stops the ticker without writing the closing line. A since-walk that ends in a
// refusal is ONE line, the refusal, and the narration this run would have printed above it
// is not the answer: the dev contract that every refusal is a single INBOX REFUSED line
// predates the walk, and a run that cannot read the bus has nothing to report about how far
// it got. The ticker is stopped the same way finish stops it so no goroutine is left behind.
func (p *walkProgress) abort() {
	close(p.stop)
	p.stopped.Wait()
}

// publicMarker is the file a bus clone carries to say it is public. --decide refuses a
// clone without it, because a provider may train on what it is sent, and the marker is the
// clone's own statement that the text may leave.
const publicMarker = ".public"

// noteJudgment is one note's typed decision, rendered as the suffix on its INBOX NOTE
// line. kind is the choice, needsReply and blocked are the provider's noul probabilities,
// wake says whether this note needs its reader (the note reading, #1617), and conf is how
// sure it was of the kind. owner and refs are mechanical: the note's To: header and the
// issue references its subject and body carry, read by the tool and asked of nobody.
type noteJudgment struct {
	kind       string
	needsReply float64
	blocked    float64
	wake       string
	owner      string
	refs       string
	conf       float64
}

// inboxDecider is the one method --decide needs from the provider client, so a test can
// stand in a fake without a socket or a key.
type inboxDecider interface {
	Decide(ctx context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error)
}

// noteDecider makes one typed decision per printed INBOX NOTE line. It is LAZY: the
// client is built and the provider is called on the first note that actually prints, so a
// listing with no notes makes zero provider calls whatever the flags say. STOP:/HOLD:
// subjects are structured signals and bypass the provider entirely.
type noteDecider struct {
	o      inboxOpts
	client inboxDecider
	qs     map[string]decide.Question
	cache  map[string]noteJudgment
	counts decideCounts
}

// decideCounts is the running tally behind the final INBOX DECIDED line.
type decideCounts struct {
	n          int
	needsReply int
	belowFloor int
	// wake is how many notes need their reader: `needs-action` by rule or by answer,
	// and `unknown` (a note the decider did not label), because an unlabelled note
	// fails toward a wake and never toward silence.
	wake int
}

func newNoteDecider(o inboxOpts) *noteDecider {
	return &noteDecider{o: o, qs: inboxQuestions(), cache: map[string]noteJudgment{}}
}

// inboxQuestions is the one question set --decide asks about every note.
func inboxQuestions() map[string]decide.Question {
	return map[string]decide.Question{
		"kind": {
			Instructions: "Classify this note. start asks to begin work; done reports finished work; question asks and needs an answer; edge carries a structured signal that bypasses semantic filtering; refusal declines or forbids; receipt only acknowledges.",
			Choice: map[string]string{
				"start":    "asks to begin work",
				"done":     "reports finished work",
				"question": "asks a question that needs an answer",
				"edge":     "a structured signal that bypasses semantic filtering",
				"refusal":  "declines or forbids",
				"receipt":  "only acknowledges",
			},
		},
		"needs_reply": {Instructions: "The probability, from 0 to 1, that this note needs a reply from its reader.", Noul: true},
		"blocked":     {Instructions: "The probability, from 0 to 1, that this note blocks the reader's work until it is answered.", Noul: true},
		"wake": {
			Instructions: "Does this note wake its reader? ack: the note only confirms receipt or completion of something the reader already knows, and asks nothing; info: the note reports a fact and asks nothing of this reader; needs-action: the note asks this reader to do, decide, review, answer or stop something, or reports something broken that this reader owns.",
			Choice: map[string]string{
				"ack":          "only confirms receipt or completion of something the reader already knows, and asks nothing",
				"info":         "reports a fact and asks nothing of this reader",
				"needs-action": "asks this reader to do, decide, review, answer or stop something, or reports something broken that this reader owns",
			},
		},
	}
}

// decideSuffix is the typed decision appended to an INBOX NOTE line. The four fields
// that were here before the note reading -- kind, needs_reply, blocked and conf -- keep
// their names, order and spelling; wake, owner and ref are appended after them, so no
// existing reader's field positions move.
func decideSuffix(j noteJudgment) string {
	return fmt.Sprintf("kind=%s needs_reply=%.2f blocked=%.2f conf=%.2f wake=%s owner=%s ref=%s",
		oneline.Field(j.kind), j.needsReply, j.blocked, j.conf,
		oneline.Field(j.wake), oneline.Field(j.owner), oneline.Field(j.refs))
}

// judge returns the decision for one note, reading its file for the body and cacheing by
// path. A STOP: or HOLD: subject is never sent: it is marked edge with needs_reply=1.00 by
// rule, because a structured signal is not something a model filters.
func (d *noteDecider) judge(e bus.OpenEntry) (noteJudgment, error) {
	if j, ok := d.cache[e.Path]; ok {
		return j, nil
	}
	subject, body, owner := e.Subject, "", ""
	if raw, err := os.ReadFile(filepath.Join(d.o.busDir, filepath.FromSlash(e.Path))); err == nil {
		if n, perr := bus.ParseNote(e.Path, string(raw)); perr == nil {
			if subject == "" {
				subject = n.Header.Subject
			}
			body = n.Body
			owner = n.Header.To
		}
	}
	var j noteJudgment
	if structuredSubject(subject) {
		j = noteJudgment{kind: "edge", needsReply: 1, wake: wakeNeedsAction, conf: 1}
	} else {
		if d.client == nil {
			c, err := decide.New(d.o.baseURL, d.o.keyEnv)
			if err != nil {
				return noteJudgment{}, err
			}
			d.client = c
		}
		answers, _, err := d.client.Decide(context.Background(), decideState(subject, body), d.qs)
		if err != nil {
			return noteJudgment{}, err
		}
		kind := answers["kind"]
		j = noteJudgment{
			kind:       kind.Choice,
			needsReply: answers["needs_reply"].Noul,
			blocked:    answers["blocked"].Noul,
			wake:       answers["wake"].Choice,
			conf:       kind.Confidence,
		}
	}
	if j.wake == "" {
		// A note no decider labelled is `unknown`: the absence of an answer wakes,
		// because silence about a note must never read as permission to sleep.
		j.wake = wakeUnknown
	}
	j.owner = owner
	j.refs = noteRefs(subject, body)
	d.cache[e.Path] = j
	d.counts.n++
	if j.needsReply >= 0.5 {
		d.counts.needsReply++
	}
	if j.conf < d.o.floor {
		d.counts.belowFloor++
	}
	if wakesReader(j.wake) {
		d.counts.wake++
	}
	return j, nil
}

// The note reading's three answers, and the absence of one (#1617, SPEC-DECIDE
// "1. The note reading"). ack and info report a note that asks nothing; needs-action
// asks the reader to act; unknown is what a note no decider labelled reads.
const (
	wakeAck         = "ack"
	wakeInfo        = "info"
	wakeNeedsAction = "needs-action"
	wakeUnknown     = "unknown"
)

// wakesReader reports whether an answer is one the reader must be woken for. ack and
// info defer; everything else -- needs-action, unknown, or any value this code does not
// know -- wakes, so an answer can never put a window to sleep past a note that needed it.
func wakesReader(wake string) bool {
	switch wake {
	case wakeAck, wakeInfo:
		return false
	default:
		return true
	}
}

// noteRefPattern is every issue reference a note names: a bare `#<digits>` and the
// `<owner>/<repo>#<digits>` that carries a repository. Read mechanically from the text,
// never asked of a provider.
var noteRefPattern = regexp.MustCompile(`[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+#[0-9]+|#[0-9]+`)

// noteRefs lists the issue references in the parts given, in order, at most four of
// them, then `+<n>`; `-` when there are none.
func noteRefs(parts ...string) string {
	var refs []string
	for _, p := range parts {
		refs = append(refs, noteRefPattern.FindAllString(p, -1)...)
	}
	if len(refs) == 0 {
		return "-"
	}
	if len(refs) > 4 {
		return strings.Join(refs[:4], ",") + fmt.Sprintf("+%d", len(refs)-4)
	}
	return strings.Join(refs, ",")
}

// structuredSubject reports the subjects a model must never be asked to filter: the
// STOP: and HOLD: prefixes are structured signals, and Stella's rule is that they bypass
// semantic judgement.
func structuredSubject(subject string) bool {
	return strings.HasPrefix(subject, "STOP:") || strings.HasPrefix(subject, "HOLD:")
}

// decideState is what a note sends to the provider: its subject and the first 600
// characters of its body, with any sk- key redacted.
func decideState(subject, body string) string {
	return subject + "\n\n" + redactSK(firstChars(body, decideStateChars))
}

// decideStateChars is how much of a note's body --decide sends.
const decideStateChars = 600

// firstChars returns at most n characters of s, cut on a rune boundary so a multi-byte
// character is never split.
func firstChars(s string, n int) string {
	if n <= 0 {
		return ""
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}

// skPattern is an sk- key: the prefix and the token that follows it.
var skPattern = regexp.MustCompile(`sk-[A-Za-z0-9_-]+`)

// redactSK replaces every sk- key in s with a placeholder, so a key pasted into a note
// cannot leave on the wire.
func redactSK(s string) string {
	return skPattern.ReplaceAllString(s, "sk-[redacted]")
}

// printOpenEntries prints an open list in the order it is listed in -- the notes that carry
// something, then what has been heard and still owes an answer, then the bare
// acknowledgements -- and stops after max of them. It returns how many it printed.
//
// Three groups, in that order, because the listing that hid four real notes among the
// receipts is the reason they are separated rather than interleaved by clock. HEARD is
// between them because a note I have already said "heard" to is still owed an answer, and
// the receipt that says so must not make it disappear. Every field comes from the open
// list, so nothing here opens a note unless --decide asked for a judgement.
//
// The cap counts PRINTED entries and not entries considered, so a capped listing is the
// first max of the same order a full one would have printed: the notes first, and the bare
// acknowledgements last, which is the right end to lose.
func printOpenEntries(stdout io.Writer, entries []bus.OpenEntry, max int, d *noteDecider) (int, error) {
	shown := 0
	for _, group := range []string{"NOTE", "HEARD", "RECEIPT"} {
		for _, e := range entries {
			if e.Kind == bus.OpenUnreadable {
				continue
			}
			token := "NOTE"
			switch {
			case e.Heard:
				token = "HEARD"
			case e.Kind == bus.OpenReceipt:
				token = "RECEIPT"
			}
			if token != group {
				continue
			}
			if shown >= max {
				return shown, nil
			}
			if token == "NOTE" && d != nil {
				j, err := d.judge(e)
				if err != nil {
					return shown, err
				}
				fmt.Fprintf(stdout, "INBOX %s id=%s from=%s addr=%s at=%s path=%s: %s %s\n",
					token, oneline.Field(dash(e.ID)), oneline.Field(dash(e.From)), oneline.Field(dash(e.Addr)),
					oneline.Field(dash(e.Date)), oneline.Field(e.Path), oneline.Escape(e.Subject),
					decideSuffix(j))
				shown++
				continue
			}
			fmt.Fprintf(stdout, "INBOX %s id=%s from=%s addr=%s at=%s path=%s: %s\n",
				token, oneline.Field(dash(e.ID)), oneline.Field(dash(e.From)), oneline.Field(dash(e.Addr)),
				oneline.Field(dash(e.Date)), oneline.Field(e.Path), oneline.Escape(e.Subject))
			shown++
		}
	}
	return shown, nil
}

// bodyLimit checks one of the two --bodies budgets. Zero is not "unlimited" and
// over-ceiling is not "as much as you can": either one names the flag, the value given and
// the ceiling, in the INBOX REFUSED shape the rest of this listing's refusals use, and is
// exit 2 -- a bad invocation, like every other pair of numbers this tool will not guess.
func bodyLimit(stderr io.Writer, name string, value, ceiling int64) bool {
	if value < 1 {
		fmt.Fprintf(stderr, "INBOX REFUSED: %s %d is not unlimited; give 1 to %d\n", oneline.Field(name), value, ceiling)
		return false
	}
	if value > ceiling {
		fmt.Fprintf(stderr, "INBOX REFUSED: %s %d is over the ceiling %d\n", oneline.Field(name), value, ceiling)
		return false
	}
	return true
}

// refuseContinuation is the ONE shape a --after refusal takes, and it always carries the
// same remedy: the same command without --after. Two of the reasons have their own
// sentence because their remedy needs one -- a token that names no item in this range, and
// a cursor another read moved under an open chain -- and everything else says what did not
// match. None of them moves a cursor, writes a file, or leaves a half-listing behind.
func refuseContinuation(stderr io.Writer, err error) int {
	var moved *bus.BodyCursorMismatchError
	switch {
	case errors.As(err, &moved):
		fmt.Fprintf(stderr, "INBOX REFUSED: --after names cursor %s and this reader's cursor is %s; rerun without --after\n",
			oneline.Field(dash(moved.Token)), oneline.Field(dash(moved.Persisted)))
	case errors.Is(err, bus.ErrBodyTokenNoItem):
		fmt.Fprintln(stderr, "INBOX REFUSED: --after <token> names no item in this range; rerun without --after")
	default:
		fmt.Fprintf(stderr, "INBOX REFUSED: --after <token> is not a continuation for this read: %s; rerun without --after\n", oneline.Err(err))
	}
	return 2
}

const (
	defaultBodiesNotes          = 20
	defaultBodiesBytes    int64 = 65536
	maxBodiesNotesCeiling       = 1000
	maxBodiesBytesCeiling int64 = 1048576
)

func printBodyPage(stdout io.Writer, page bus.BodyPage, maxBytes int64, d *noteDecider) error {
	// Selection, continuation identities, and safe-frontier accounting stay in canonical
	// snapshot order. Display follows the inbox's established NOTE, HEARD, RECEIPT groups;
	// an earlier receipt must not push a later note below the summary groups on this page.
	for _, group := range []string{"NOTE", "HEARD", "RECEIPT"} {
		for _, emission := range page.Emissions {
			if emission.Gap != nil {
				continue
			}
			if emission.Item == nil {
				return fmt.Errorf("body page has an empty emission")
			}
			if bodyDisplayGroup(*emission.Item) != group {
				continue
			}
			if err := printBodyItem(stdout, *emission.Item, d); err != nil {
				return err
			}
		}
	}
	// Gaps are accounting events rather than listing groups. Preserve every selected gap
	// in canonical order after the grouped listing rows, and retain the same write-error
	// path that prevents a cursor advance when stdout breaks.
	for _, emission := range page.Emissions {
		if emission.Gap == nil {
			continue
		}
		gap := emission.Gap
		if _, err := fmt.Fprintf(stdout, "INBOX BODY OVERSIZE id=%s bytes=%d max-bytes=%d path=%s\n", oneline.Field(dash(gap.ID)), gap.Bytes, maxBytes, oneline.Field(gap.Path)); err != nil {
			return err
		}
	}
	return nil
}

func bodyDisplayGroup(item bus.BodyItem) string {
	e := item.Entry
	if e.Heard {
		return "HEARD"
	}
	if e.Kind == bus.OpenReceipt {
		return "RECEIPT"
	}
	return "NOTE"
}

func printBodyItem(stdout io.Writer, item bus.BodyItem, d *noteDecider) error {
	e := item.Entry
	kind := bodyDisplayGroup(item)
	if kind != "NOTE" {
		if _, err := fmt.Fprintf(stdout, "INBOX %s id=%s from=%s addr=%s at=%s path=%s: %s\n", kind, oneline.Field(dash(e.ID)), oneline.Field(dash(e.From)), oneline.Field(dash(e.Addr)), oneline.Field(dash(e.Date)), oneline.Field(e.Path), oneline.Escape(e.Subject)); err != nil {
			return err
		}
		return nil
	}
	bodyBytes := item.Body
	if d != nil {
		j, err := d.judge(e)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(stdout, "INBOX NOTE id=%s from=%s addr=%s at=%s path=%s: %s %s\n", oneline.Field(dash(e.ID)), oneline.Field(dash(e.From)), oneline.Field(dash(e.Addr)), oneline.Field(dash(e.Date)), oneline.Field(e.Path), oneline.Escape(e.Subject), decideSuffix(j)); err != nil {
			return err
		}
	} else if _, err := fmt.Fprintf(stdout, "INBOX NOTE id=%s from=%s addr=%s at=%s path=%s: %s\n", oneline.Field(dash(e.ID)), oneline.Field(dash(e.From)), oneline.Field(dash(e.Addr)), oneline.Field(dash(e.Date)), oneline.Field(e.Path), oneline.Escape(e.Subject)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "INBOX BODY id=%s bytes=%d\n", oneline.Field(dash(e.ID)), len(item.Body)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "%s", bodyBytes); err != nil {
		return err
	}
	if len(item.Body) == 0 || item.Body[len(item.Body)-1] != '\n' {
		if _, err := fmt.Fprint(stdout, "\n"); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(stdout, "INBOX BODY END id=%s\n", oneline.Field(dash(e.ID))); err != nil {
		return err
	}
	return nil
}

// countListable is how many entries printOpenEntries would print with no cap: the whole
// list bar the unreadable entries, which have no header to print a line from and are named
// on their own INBOX UNREADABLE lines instead.
func countListable(entries []bus.OpenEntry) int {
	n := 0
	for _, e := range entries {
		if e.Kind != bus.OpenUnreadable {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------------------- the wait

// cmdWait is `inbox`, on a clock, INSIDE one tool call.
//
// THE FAILURE THIS CLOSES, and it is not a failure of the bus. A line reading this bus
// through a harness that does not wake it has a poller running beside its session,
// mechanically, on time. What the poller cannot do is get the session's attention: the
// notes land in the checkout and the session, being non-deterministic about housekeeping,
// does not always come back and look. So a note can sit answered by nobody for an hour
// beside a poller that has been doing its job the whole time.
//
// A SESSION INSIDE A TOOL CALL CANNOT FORGET. That is the whole idea here: the harness
// itself wakes the session when the call returns, on every harness there is, because that
// is what a tool call is. So the polling moves INSIDE the tool -- one blocking verb, which
// returns the moment there is something to read and says so when there is not.
//
// It is `inbox` and not a second reader: the same rules about what is addressed to you,
// the same open list, the same switch-day line, the same lines on stdout, so the caller's
// next action is the one an inbox listing always implies. The only things it adds are a
// clock and a fetch. inboxListing is the shared body; there is no second inbox to keep in
// step with this one.
//
// EVERY WAIT HAS A DEADLINE, which is why --timeout is required and has no default: an
// asynchronous wait with no deadline is a line that is stuck rather than waiting, and
// nobody outside can tell the two apart. A timeout is not an error -- it is the answer
// "nothing yet", exit 0, and the caller issues the next one.
//
// rearmCommand rebuilds `wait`'s re-arm line from the caller's own argv, shell-quoting each
// argument so that what is printed is what the caller pasted. The failure it closes: the
// command used to join the raw argv with spaces, so a --bus path holding a space came back
// split in two -- --bus received the prefix through the space, and the remainder landed as a
// separate argument -- and pasting it named a bus that does not exist instead of the one it
// was waiting on.
func rearmCommand(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	return "nova-bus wait " + strings.Join(quoted, " ")
}

// shellQuote quotes one argument for a POSIX shell: unchanged when it holds nothing the
// shell would act on, otherwise single quotes with the one character that cannot live
// inside them written the shell's own way. A value carrying a space, a dollar sign or a
// semicolon unquoted would split the pasted command or run something the caller did not
// write.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '/' || c == '.' || c == '_' || c == '-' || c == ':') {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func cmdWait(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("wait")
	busDir := f.fs.String("bus", "", "the bus's repository root (required)")
	as := f.fs.String("as", "", "which participant you are (required)")
	maxWords := f.fs.Int("receipt-max-words", 0, "a body under this many words may be a receipt (required, at least 1)")
	timeout := f.fs.Duration("timeout", 0, "how long to wait before returning WAIT TIMEOUT (required; a duration like 25m, at most "+maxWaitTimeout.String()+")")
	until := f.fs.String("until", "", "an absolute deadline as an RFC 3339 UTC instant (e.g. 2026-09-18T18:00:00Z); the wait ends at that moment or at --timeout, whichever comes first")
	idleExit := f.fs.Int("idle-exit", 0, "exit with this code instead of 0 when the wait times out, so a harness that cannot loop can branch on the code without parsing anything; 1 and 2 are refused, they are this tool's own")
	interval := f.fs.Duration("interval", defaultWaitInterval, "how long between polls")
	beat := f.fs.Duration("beat", defaultBeatInterval, "how often to push your BEAT liveness file as its own commit")
	beatLease := f.fs.Duration("beat-lease", defaultBeatLease, "how far into the future each BEAT's until= promises the line is alive, so a manager cycle between waits still reads awake")
	openList := f.fs.Bool("open", false, "list every open note when this wait returns, not only what is new")
	openMax := f.fs.Int("open-max", defaultOpenMax, "with --open, how many carried entries to print before saying how many more there are")
	openWarn := f.fs.Int("open-warn", defaultOpenWarn, "how many carried entries before every return adds one line saying the list is large and how to empty it")
	bodies := f.fs.Bool("bodies", false, "print bodies for NEW notes, bounded by --max-notes and --max-bytes")
	maxNotes := f.fs.Int("max-notes", defaultBodiesNotes, "with --bodies, maximum NEW items to print")
	maxBytes := f.fs.Int64("max-bytes", defaultBodiesBytes, "with --bodies, maximum body bytes to print")
	after := f.fs.String("after", "", "continue a bounded --bodies snapshot")
	advance := f.fs.Bool("advance", false, "move your cursor to HEAD and push it when this wait returns, the way inbox --advance does")
	remote := f.fs.String("remote", "", "the git remote to fetch the bus from (required: a wait that cannot fetch cannot notice anything)")
	branch := f.fs.String("branch", "", "the branch the bus lives on (required)")
	attempts := f.fs.Int("attempts", defaultAttempts, "how many times to push the cursor before giving up")
	gitSeconds := f.fs.Int("git-timeout", defaultGitTimeoutSeconds, "how long one git subprocess may take before this run gives up on it")
	noPush := f.fs.Bool("no-push", false, "with --advance, commit the cursor but do not push it")
	legacyBefore := f.fs.String("legacy-before", "", "notes dated before this UTC date (YYYY-MM-DD, midnight at its start) or UTC instant (RFC 3339, e.g. 2026-09-09T18:07:00Z) are not carried on your open list, and are counted rather than listed")
	carryHistory := f.fs.Bool("carry-history", false, "on your FIRST --advance, carry every old note on your open list instead of drawing a switch-day line; does nothing otherwise")
	diagnostics := f.fs.Bool("diagnostics", false, "name every unreadable file with its reason, even ones already shown; the default collapses unchanged ones to one count line")
	quietBeats := f.fs.Bool("quiet-beats", false, "accepted for callers that pass it; since #328 (2026-09-17) a change that is only beats and cursors never wakes a wait, with or without this flag; it is not news")
	// --max-commits IS HERE BECAUSE THE REMEDY HAS TO BE TYPEABLE AT THE VERB THAT NEEDS IT
	// (#1518). The since-walk is bounded in inboxListing, which `wait` polls through, so a
	// wait has always been bounded -- it simply had no way to say a bigger number. Johnny's
	// loop ran for days behind a cursor the bus had left far behind, printing the bounded
	// line's `remedy="raise --max-commits"` at a verb that refused the flag.
	maxCommits := f.fs.Int("max-commits", defaultMaxCommits, "how many commits a since-walk may cross before it stops and names the remedy; raise it to read a staler cursor")
	// --remote and --branch are required here and conditional on inbox, because a wait
	// FETCHES: that is the difference between waiting and sleeping. A wait that read only
	// what its checkout already held would wait out its whole timeout beside a bus full of
	// notes, and this tool does not guess a remote.
	if !f.parse(args, stderr, map[string]*string{"bus": busDir, "as": as, "remote": remote, "branch": branch}) {
		return 2
	}
	flagLegacy, ok := legacyLine("wait", *legacyBefore, stderr)
	if !ok {
		return 2
	}
	if *carryHistory && !flagLegacy.Before.IsZero() {
		fmt.Fprint(stderr, "nova-bus wait: --legacy-before draws a switch-day line and --carry-history says there is none to draw; give one or the other\n")
		return 2
	}
	maxWordsValue, ok := f.receiptMaxWords(*maxWords, f.set("receipt-max-words"), *busDir, stderr)
	if !ok {
		return 2
	}
	if !f.count("open-max", *openMax, stderr) {
		return 2
	}
	if !f.count("max-commits", *maxCommits, stderr) {
		return 2
	}
	if !f.atLeastZero("open-warn", *openWarn, stderr) {
		return 2
	}
	if *after != "" && !*bodies {
		fmt.Fprintln(stderr, "nova-bus wait: --after requires --bodies")
		return 2
	}
	if *bodies && !bodyLimit(stderr, "--max-notes", int64(*maxNotes), maxBodiesNotesCeiling) {
		return 2
	}
	if *bodies && !bodyLimit(stderr, "--max-bytes", *maxBytes, maxBodiesBytesCeiling) {
		return 2
	}
	if !f.gitTimeoutFlag(*gitSeconds, stderr) {
		return 2
	}
	if !f.attempts(*attempts, stderr) {
		return 2
	}
	if !f.gitArgs(*remote, *branch, stderr) {
		return 2
	}
	if *timeout <= 0 {
		fmt.Fprint(stderr, "nova-bus wait: --timeout is required and is a duration like 25m; every wait has a deadline, and one with no deadline is a line that is stuck rather than waiting; refusing to guess; run: nova-bus help\n")
		return 2
	}
	// THE CEILING IS ABOUT THE HARNESS AND NOT ABOUT THE BUS. This verb is meant to be
	// called from inside a tool call, and every harness kills a tool call that runs too
	// long -- so a wait longer than the harness's limit does not wait longer, it is killed,
	// and the caller is told nothing at all. An hour is above every limit we know of and
	// below anything anybody would call a hang.
	if *timeout > maxWaitTimeout {
		fmt.Fprintf(stderr, "nova-bus wait: --timeout %s is longer than %s, which is as long as this verb will block; a wait runs inside your harness's tool call and every harness kills one that runs too long, so a longer timeout is not a longer wait, it is a call that is killed with nothing said; ask your harness for its limit, sit under it, and issue the next wait when this one returns\n",
			oneline.Field(timeout.String()), oneline.Field(maxWaitTimeout.String()))
		return 2
	}
	// --until IS THE DEADLINE A HARNESS ALREADY HAS. A duration is the wrong shape for a
	// caller whose own limit is a MOMENT -- the end of a session, the hour a shift hands over
	// -- because turning one into the other means knowing how long the call took to start,
	// and a caller that guesses that is a caller whose last wait runs past the thing it was
	// waiting for. So the two live side by side and the EARLIER ONE WINS: --timeout is how
	// long this call may block, --until is the moment past which blocking is pointless, and a
	// wait ends at whichever comes first. The ceiling needs no second check: --timeout is
	// required, it is capped at maxWaitTimeout, and the effective window is never longer.
	waitFor := *timeout
	if *until != "" {
		when, err := time.Parse(time.RFC3339, *until)
		if err != nil {
			fmt.Fprintf(stderr, "nova-bus wait: --until %s is not an RFC 3339 instant like 2026-09-18T18:00:00Z; %s\n", oneline.Field(*until), oneline.Err(err))
			return 2
		}
		left := when.Sub(now)
		if left <= 0 {
			fmt.Fprintf(stderr, "nova-bus wait: --until %s is now or in the past (it is %s), so this wait would end before it began; give an instant in the future, or leave --until off and let --timeout bound the call\n",
				oneline.Field(*until), oneline.Field(now.UTC().Format(time.RFC3339)))
			return 2
		}
		if left < waitFor {
			waitFor = left
		}
	}
	// --idle-exit IS FOR A HARNESS THAT CANNOT LOOP (Freddy's, and every harness like it): it
	// runs one tool call per turn and branches on the exit code, and it has no way to tell
	// "nothing arrived" from "a note arrived" when both are exit 0. So a timeout may carry a
	// code of the caller's choosing. 1 and 2 are refused rather than allowed: they are this
	// tool's own -- a refusal and an invocation that could not run -- and a harness that saw
	// either would have to parse the output to know which it was, which is the thing this
	// flag exists to make unnecessary. 126 and up are the shell's own.
	if *idleExit < 0 || *idleExit > maxIdleExit {
		fmt.Fprintf(stderr, "nova-bus wait: --idle-exit %d is not an exit code this verb will use; give one between 0 and %d, and note that %d and above belong to the shell\n", *idleExit, maxIdleExit, maxIdleExit+1)
		return 2
	}
	if *idleExit == 1 || *idleExit == 2 {
		fmt.Fprintf(stderr, "nova-bus wait: --idle-exit %d is this tool's own code -- 1 is a refusal and 2 is an invocation that could not run -- so a harness that got it back could not tell a quiet bus from a broken one; pick another, 3 is free\n", *idleExit)
		return 2
	}
	if *interval < minWaitInterval {
		fmt.Fprintf(stderr, "nova-bus wait: --interval %s is shorter than %s, and every poll is a git fetch against somebody's server; refusing to fetch faster than that\n",
			oneline.Field(interval.String()), oneline.Field(minWaitInterval.String()))
		return 2
	}
	if *beat <= 0 {
		fmt.Fprint(stderr, "nova-bus wait: --beat must be a positive duration like 60s\n")
		return 2
	}
	if *beatLease <= 0 {
		fmt.Fprint(stderr, "nova-bus wait: --beat-lease must be a positive duration like 10m\n")
		return 2
	}
	// A wait always runs git, so the root check is unconditional -- see the same check, and
	// the same reason for the order it is in, in cmdInbox.
	if err := bus.IsRepoRoot(*busDir); err != nil {
		fmt.Fprintf(stderr, "nova-bus wait: a wait fetches the bus and reads what changed since your cursor, which need git; %s\n", oneline.Err(err))
		return 2
	}
	c, err := bus.LoadConfig(*busDir)
	if err != nil {
		fmt.Fprintf(stderr, "nova-bus wait: %s\n", oneline.Err(err))
		return 2
	}
	me, found := c.Lookup(*as)
	if !found {
		fmt.Fprintf(stderr, "nova-bus wait: --as %q names no one on this bus (known: %s)\n", *as, oneline.Escape(strings.Join(c.KnownNames(), "; ")))
		return 2
	}
	if me.Lane == "" {
		fmt.Fprintf(stderr, "nova-bus wait: %q has no lane on this bus, so nothing can answer for them\n", me.Name)
		return 2
	}
	o := inboxOpts{
		busDir: *busDir, as: *as, maxWords: maxWordsValue,
		openList: *openList, openMax: *openMax, openWarn: *openWarn, advance: *advance,
		remote: *remote, branch: *branch, attempts: *attempts, noPush: *noPush,
		legacy: flagLegacy, carryHistory: *carryHistory,
		bodies: *bodies, maxNotes: *maxNotes, maxBytes: *maxBytes, after: *after,
		me: me, beat: *beat, lease: *beatLease,
		diagnostics: *diagnostics,
		quietBeats:  *quietBeats,
		maxCommits:  *maxCommits,
	}
	// The cursor as it stands, for the line that says this call BEGAN. A cursor that will
	// not read is not refused here: the first poll's listing refuses it, in the sentence
	// inbox already refuses it in.
	held, _ := bus.ReadCursor(*busDir, me.Lane)
	// A WAIT THAT CANNOT SEE THE BUS IS NOT A WAIT, AND IT SAYS SO BEFORE IT BLOCKS (#1518).
	//
	// The distance from a cursor to HEAD only GROWS while a wait runs -- the cursor moves
	// on --advance, at the end, and never during -- so a walk that is over the bound now is
	// over it on every poll this call will make. Every one of those polls reads nothing,
	// and the call then prints the same WAIT TIMEOUT as a wait over a quiet bus. Johnny's
	// loop did exactly that, once a minute, for hours, at exit 0:
	//
	//	INBOX WALK bounded commits=500 remedy="raise --max-commits or close --before <instant>"
	//	WAIT TIMEOUT after=1m2.926s polls=2 cursor=8cd06f5a...
	//
	// So it is asked ONCE, here, before anything blocks, and it is a REFUSAL rather than a
	// note: a loop that is green and deaf is worse than one that stops, because nobody goes
	// to look at the one that is green. The bounded walk's own remedy is carried word for
	// word, and --max-commits above is now one of the two things it names.
	if over, err := waitWalkOverBound(*busDir, held.Commit, *maxCommits); err == nil && over {
		fmt.Fprintf(stderr, "WAIT BLIND commits=%d %s\n", *maxCommits, boundedWalkRemedy)
		fmt.Fprintf(stderr, "WAIT REFUSED: as=%s cursor=%s is further behind than this walk may cross, so every poll of this wait would read nothing and it would end saying `nothing yet`; raise the bound or close the backlog, then wait again\n",
			oneline.Field(me.Name), oneline.Field(dash(held.Commit)))
		return 2
	}
	// One line at the start, before anything is waited on, so that a transcript shows the
	// call began and what it was told to do. A tool call that prints nothing for twenty
	// minutes and then prints everything is, while it runs, indistinguishable from one
	// that has hung.
	//
	// THE TWO SPELLINGS ARE ONE DECISION AND NOT A DUPLICATE. A caller that passes neither
	// --until nor --idle-exit sees exactly the line it saw before they existed, byte for byte
	// (testdata/today/wait.txt holds those bytes and readhalf_test.go compares them): a flag
	// nobody used must not change what everybody reads. A caller that passes EITHER gets both
	// fields, filled in, because a deadline and an exit code are what that caller is asking
	// about and half a pair says less than none.
	if *until == "" && *idleExit == 0 {
		fmt.Fprintf(stdout, "WAIT as=%s timeout=%s interval=%s cursor=%s\n",
			oneline.Field(me.Name), oneline.Field(timeout.String()), oneline.Field(interval.String()), oneline.Field(dash(held.Commit)))
	} else {
		fmt.Fprintf(stdout, "WAIT as=%s timeout=%s interval=%s cursor=%s until=%s idle-exit=%d\n",
			oneline.Field(me.Name), oneline.Field(timeout.String()), oneline.Field(interval.String()), oneline.Field(dash(held.Commit)),
			oneline.Field(dash(*until)), *idleExit)
	}
	// THE ENTRY BEAT, written before the first poll, so a line that is about to wait
	// already reads awake the moment its call begins, lease and all.
	if err := bus.WriteBeat(*busDir, me.Lane, held.Commit, now, now.Add(*beatLease)); err != nil {
		fmt.Fprintf(stderr, "WAIT NOTE beat write failed: %s\n", oneline.Err(err))
	}
	// The command the caller issues again to re-arm this wait: the next one, with the same
	// flags, echoed back so a harness that does not wake on its own can paste it. A wait is
	// ONE read, with one terminal line saying it ended and must be re-armed. Each argument
	// is shell-quoted, not joined raw: a --bus path carrying a space must come back as the
	// same one argument after a paste, not split in two.
	next := rearmCommand(args)
	return waitLoop(o, waitFor, *interval, *idleExit, next, stdout, stderr, now)
}

// waitWalkOverBound answers whether this lane's cursor is further behind HEAD than the
// walk's bound, which is the one condition under which a wait can see nothing whatever
// happens (#1518).
//
// IT FAILS OPEN, in both directions that matter. A lane with no cursor at all is not over
// any bound -- it reads from the beginning of the switch-day line, which is what a first
// wait does -- and an error asking git is NOT a refusal: a wait that cannot measure the
// distance is a wait whose first poll's listing will refuse in inbox's own sentence, and
// this check exists to name a silence, never to invent a new way to fail.
func waitWalkOverBound(busDir, cursor string, limit int) (bool, error) {
	if cursor == "" || limit <= 0 {
		return false, nil
	}
	_, over, err := bus.CommitsSinceBounded(busDir, cursor, limit)
	if err != nil {
		return false, err
	}
	return over, nil
}

// maxIdleExit is the highest code --idle-exit will take. 126 and 127 are the shell's own --
// "found and not executable", "not found" -- and 128 up is a signal, so a wait that returned
// one of those would be read as something the shell did to it rather than something it said.
const maxIdleExit = 125

// defaultWaitInterval is how long a wait leaves between polls when the caller names no
// interval. It is a default, unlike --timeout, on the same test the tool's other two
// defaults pass: it is not a fact about a bus that only its owner can supply. Ten seconds
// is under the time it takes to read a note and well over the cost of a fetch, and it is
// the number Glenn asked for after watching the family's lines wait on each other: "the
// polling should be 10 sec". A shorter interval is a shorter round trip between two lines
// that are answering each other, and a git fetch of a bus this size is cheap enough that
// the round trip is what the number should be chosen for.
const defaultWaitInterval = 10 * time.Second

// defaultBeatInterval is how often a wait pushes its BEAT file as its own commit when the
// reader names no --beat. It is Glenn's "a beat a minute" from docs/SPEC-WORK.md:
// presence is a beat a minute, and no beat for five minutes is asleep. Sixty seconds is
// the beat the whole Presence design is built on.
const defaultBeatInterval = 60 * time.Second

// defaultBeatLease is how far into the future a BEAT's until= promises the line is alive
// when `wait` writes it on entry, every tick and on exit. It is longer than --window
// (five minutes in SPEC-WORK's Presence) so that a manager cycle between two waits -- the
// wait returns with a note and the harness works it before issuing the next -- reads awake
// throughout, rather than ageing past --window into "asleep" while the process is alive.
const defaultBeatLease = 10 * time.Minute

// maxWaitTimeout is as long as `wait` will block, and it is a fact about HARNESSES rather
// than about buses; see the refusal above.
const maxWaitTimeout = 60 * time.Minute

// minWaitInterval is as fast as a wait will poll, because a poll is a git fetch.
const minWaitInterval = 100 * time.Millisecond

// waitBlockedHook is a test-only sync point, set behind this unexported name. It
// is called with the bus directory at the exact boundary a wait becomes blocked:
// it has polled, found nothing new, and is about to sleep until its next poll.
// Tests point it at a channel close so a note can be pushed at that boundary
// instead of after a sleep that races the poll (#370). It is an atomic because it
// is read from whichever goroutine runs waitLoop and written once by a test, and
// parallel tests may run waitLoop while a sibling's hook is installed.
type waitBlockedHook func(busDir string)

var testWaitBlockedHook atomic.Pointer[waitBlockedHook]

// writeBeatLease writes the lane's BEAT carrying until=now+lease, so a line whose manager
// process is alive but between waits still reads awake to `nova-wake awake`. It is called
// on entry, every tick and on exit; the stamp and until come from the same Now so the
// lease's length is exact.
func writeBeatLease(o inboxOpts, cursor string) error {
	now := time.Now()
	return bus.WriteBeat(o.busDir, o.me.Lane, cursor, now, now.Add(o.lease))
}

// waitLoop is the clock: poll, and either return what arrived or sleep and poll again
// until the deadline. It is apart from the flags so that what it does is readable without
// them.
//
// THE FIRST POLL HAPPENS IMMEDIATELY, before any sleep, because the commonest case is a
// note that is already there -- the caller answered the last one and came straight back --
// and making them wait an interval for news the bus already had would be a tool inventing
// latency.
func waitLoop(o inboxOpts, timeout, interval time.Duration, idleExit int, next string, stdout, stderr io.Writer, now time.Time) int {
	start := time.Now()
	deadline := start.Add(timeout)
	// The moment this call cannot see past: a switch-day line drawn after it hides
	// everything that could possibly arrive during this wait.
	horizon := now.Add(timeout)
	polls := 0
	cursor := ""
	lastBeat := start
	// The cursor this call starts from, so the beat the first tick writes carries the
	// commit the line is standing at rather than a dash: the entry beat in cmdWait already
	// wrote that cursor, and a tick that overwrote it with "-" would take presence
	// backwards for the sake of a file it rewrites a moment later.
	if held, err := bus.ReadCursor(o.busDir, o.me.Lane); err == nil {
		cursor = held.Commit
	}
	// The beat write: one line into the working tree, cheap, every tick.
	writeBeat := func() {
		if err := writeBeatLease(o, cursor); err != nil {
			fmt.Fprintf(stderr, "WAIT NOTE beat write failed: %s\n", oneline.Err(err))
		}
	}
	// THE BEAT A WAIT LEAVES BEHIND (#488). landBeat commits the lane's BEAT and pushes it
	// on its own as "beat <name>" -- WHEN THERE IS ONE TO LAND.
	//
	// The bug it closes: the beat was written to the working tree on every tick and
	// committed only once per --beat, so a wait that returned inside one --beat -- which is
	// every wait that returns because a note arrived -- handed the next verb a checkout
	// holding a modified BEAT, and the friend's `send` refused with "the bus's checkout
	// holds changes that are not this note", naming a file they had never touched. Every
	// friend on the bus hit it in one day. So every exit lands the beat, and a wait leaves
	// the checkout clean.
	//
	// THE DIRTY CHECK IS THE WHOLE OF WHY THIS IS NOT ONE MORE COMMIT PER WAIT. The beat is
	// written BEFORE the poll, so a poll that advances the cursor folds BEAT into its own
	// cursor commit -- advanceCursorTo has always named the beat among its paths -- and
	// there is then nothing left over to commit on the way out. A beat already on the bus
	// is not committed twice, an advance's cursor commit stays the HEAD it was, and the
	// push stays bounded by --beat rather than becoming one per tick.
	//
	// IT IS PUSHED AND NOT MERELY COMMITTED. A beat commit left unpushed leaves this
	// checkout AHEAD of the bus, and FetchAndFastForward -- which is the poll, and which
	// will not rebase a bench's own commits during a read -- has nothing to fast-forward
	// onto while a checkout is ahead. The NEXT wait would then poll a bus it cannot see,
	// for its whole timeout, silently. A wait leaves the checkout clean AND level.
	//
	// A push that cannot land is still possible, and that is the other half of #488: send,
	// receipt and inbox --advance take an uncommitted BEAT of the caller's own line, and an
	// unpushed commit of the caller's own beat, as their own machinery and carry it out
	// with the note rather than refusing over it.
	landBeat := func() {
		dirty, err := bus.PathDirty(o.busDir, bus.BeatPath(o.me.Lane))
		if err != nil {
			fmt.Fprintf(stderr, "WAIT NOTE beat push failed: %s\n", oneline.Err(err))
			return
		}
		if !dirty {
			return
		}
		if _, err := commit(o.busDir, o.me, []string{bus.BeatPath(o.me.Lane)},
			bus.WithTrailer("beat "+o.me.Slug(), bus.TrailerBeat), o.remote, o.branch, o.attempts, false); err != nil {
			fmt.Fprintf(stderr, "WAIT NOTE beat push failed: %s\n", oneline.Err(err))
		}
	}
	for {
		polls++
		elapsed := time.Since(start).Round(time.Millisecond)
		pollNow := now.Add(elapsed)
		// Issue #328, re-landed: a wait returns the moment it sees news, an unadvanced
		// cursor's backlog included, printing exactly what inbox prints for that state;
		// it blocks only while there is nothing new at all, until a note arrives or the
		// deadline. #352 blocked over the backlog instead and broke byte-identity with
		// inbox, which is why it was reverted.
		//
		// #328 (2026-09-17): a beat or a cursor is never news. A wait that woke on every
		// line's presence beat (one a minute, six lines) was a poll with extra steps and cost the
		// window a turn per beat; --quiet-beats is accepted and changes nothing (Johnny's read).
		keep := func(r inboxReading) bool { return r.New > 0 || hiddenWholeWait(r.Legacy, horizon) }
		// THE BEAT, written before the poll. A waiting line's cursor does not move --
		// there was nothing to read, so nothing was recorded -- and a line whose cursor
		// does not move reads asleep to `nova-wake awake`. The BEAT is the file that moves
		// anyway. Writing it HERE rather than after the poll is what lets a poll that
		// advances the cursor carry the beat out inside its own cursor commit; see
		// landBeat.
		writeBeat()
		code, r, lines, skipped := waitPoll(o, polls == 1, pollNow, keep, stderr)
		if r.Cursor != "" {
			cursor = r.Cursor
		}
		if code != 0 {
			// The listing this poll had already printed, if it printed one, under the
			// refusal that is on stderr: a reader who was shown their inbox has been shown
			// it, whatever happened after.
			landBeat()
			fmt.Fprint(stdout, lines)
			fmt.Fprintf(stdout, "WAIT DONE reason=signal rearm=required next=%s\n", next)
			return code
		}
		// At most once per --beat the BEAT is committed and pushed on its own as "beat
		// <name>", so the line's liveness lands on the bus even while it is simply waiting.
		// The write is to the working tree on every tick; the push is the only part
		// bounded, because it is the only part that costs somebody's server.
		if time.Since(lastBeat) >= o.beat {
			lastBeat = time.Now()
			landBeat()
		}
		if skipped {
			// The cursor moved over heard notes without returning, so the timeout line at
			// the end of the wait names where the reader now stands, and the clock goes on.
			fmt.Fprint(stdout, lines)
			if head, err := bus.HeadCommit(o.busDir); err == nil {
				cursor = head
			}
			continue
		}
		if keep(r) {
			// Why this wait is not waiting, when the answer is not "a note arrived": the
			// reader's own switch-day line is drawn after everything this call could see,
			// so no note written during it would be listed. Telling them costs one line;
			// not telling them costs an hour of waiting for something that cannot happen.
			if hiddenWholeWait(r.Legacy, horizon) && !r.SwitchDay {
				fmt.Fprintf(stdout, "WAIT NOTE %s\n", oneline.Escape(hiddenReason(r.Legacy, pollNow)))
			}
			fmt.Fprintf(stdout, "WAIT OK new=%d after=%s polls=%d\n", r.New, oneline.Field(elapsed.String()), polls)
			fmt.Fprint(stdout, lines)
			fmt.Fprintf(stdout, "WAIT DONE reason=new rearm=required next=%s\n", next)
			landBeat()
			return 0
		}
		// A line drawn in the future that does NOT cover the whole wait is no reason to
		// stop, and is still worth a sentence: the notes written before it will not be
		// listed, and a reader who did not mean to draw it there would otherwise find that
		// out by being told about none of them.
		if polls == 1 && !r.Legacy.Before.IsZero() && r.Legacy.Before.After(pollNow) && !r.SwitchDay {
			fmt.Fprintf(stdout, "WAIT NOTE %s\n", oneline.Escape(hiddenReason(r.Legacy, pollNow)))
		}
		if polls == 1 {
			if h := testWaitBlockedHook.Load(); h != nil {
				(*h)(o.busDir)
			}
		}
		left := time.Until(deadline)
		if left <= 0 {
			break
		}
		// The last sleep is the short one, so the LAST poll lands ON the deadline rather
		// than before it: a note that arrives in the final interval is a note this call
		// saw, and stopping early would hand it to the next call for no reason.
		if left < interval {
			time.Sleep(left)
			continue
		}
		time.Sleep(interval)
	}
	// A TIMEOUT IS NOT AN ERROR. Nothing arrived, and nothing was written -- no cursor
	// moves on a wait that found nothing, because there is nothing to record having read --
	// and the caller's move is to issue the next wait. Exit 0, with the counts that say the
	// tool was awake the whole time. The exit beat extends the lease over the gap to the
	// next wait exactly as the entry and tick beats do -- the last tick wrote it, moments
	// ago and with the same lease -- and it is LANDED here for the reason every exit lands
	// one: the next verb in the friend's sequence gets a clean, level checkout. It is not
	// rewritten first: a fresh line written on top of a beat this poll has just pushed
	// would be one more commit for a stamp a fraction of a second newer, and the push is
	// bounded by --beat.
	landBeat()
	// ONE LINE A HARNESS CAN GREP, and -- with --idle-exit -- one code it does not have to
	// grep for at all. The code is named ON the line as well, because an exit code that
	// appears nowhere in the transcript is a number somebody reads a bug into: a harness
	// branching on 3 and a person reading the log see the same fact.
	if idleExit == 0 {
		fmt.Fprintf(stdout, "WAIT TIMEOUT after=%s polls=%d cursor=%s\n",
			oneline.Field(time.Since(start).Round(time.Millisecond).String()), polls, oneline.Field(dash(cursor)))
	} else {
		fmt.Fprintf(stdout, "WAIT TIMEOUT after=%s polls=%d cursor=%s idle-exit=%d\n",
			oneline.Field(time.Since(start).Round(time.Millisecond).String()), polls, oneline.Field(dash(cursor)), idleExit)
	}
	fmt.Fprintf(stdout, "WAIT DONE reason=timeout rearm=required next=%s\n", next)
	return idleExit
}

// waitPoll is ONE poll, under the checkout lock: the fetch, the listing, and -- only on the
// poll that returns -- the cursor. It hands its lines back rather than printing them,
// because a poll that found nothing prints nothing: twenty polls of a quiet bus are not
// twenty listings.
//
// The lock is taken and released here rather than around the loop; see lockCheckout.
func waitPoll(o inboxOpts, first bool, now time.Time, keep func(inboxReading) bool, stderr io.Writer) (int, inboxReading, string, bool) {
	release, code := lockCheckout("WAIT", o.busDir, stderr)
	if code != 0 {
		return code, inboxReading{}, "", false
	}
	defer release()
	// THE FETCH IS THE POLL. Every read in this tool reads the working tree, so a poll that
	// fetched and left the checkout where it was would never see anything; see
	// bus.FetchAndFastForward, which moves it only when moving it is a fast-forward.
	if _, err := bus.FetchAndFastForward(o.busDir, o.remote, o.branch); err != nil {
		if first {
			// The first poll's fetch failing is the invocation being wrong -- a remote that
			// is not there, a branch nobody has, a checkout that has diverged -- and the
			// caller should hear that now rather than in an hour.
			fmt.Fprintf(stderr, "WAIT REFUSED: %s\n", oneline.Err(err))
			return 1, inboxReading{}, "", false
		}
		// A later one is the network, or somebody's server, and it is not this reader's to
		// fix. It is said out loud and the wait goes on: the deadline still bounds the
		// whole thing, and a wait that gave up on one failed fetch is a wait nobody can
		// rely on.
		fmt.Fprintf(stderr, "WAIT POLL fetch: %s\n", oneline.Err(err))
	}
	var buf bytes.Buffer
	code, r := inboxListing(o, &buf, stderr, now)
	if code != 0 {
		return code, r, buf.String(), false
	}
	// A poll whose since-walk hit the bound read nothing, so it has neither news to return
	// on nor a read to advance over. `wait` refuses a cursor already past the bound before
	// it blocks (WAIT BLIND, #1518); this is the same state arriving mid-wait, when the bus
	// moves past the bound while the wait is standing there.
	if r.Bounded {
		return 0, r, "", false
	}
	if !keep(r) {
		return 0, r, "", false
	}
	// Issue #328: `wait --advance` skips notes already heard before it blocks. A reader who
	// receipted a note and then waits has already taken that note -- heard is not answered,
	// so the note is still news to the open list -- and a wait that returns on it pays a
	// turn for nothing. When every new note is already heard, the cursor is moved to the
	// head instead, one WAIT ADVANCED line names the move, and the wait keeps blocking for a
	// genuinely new note. Addressed-elsewhere notes never reach this listing at all, so
	// "every new note is heard" is exactly "no note between the cursor and the head owes
	// this reader an answer".
	if o.advance && !o.bodies && r.New > 0 && r.HeardNew == r.New {
		head, err := bus.HeadCommit(o.busDir)
		if err != nil {
			fmt.Fprintf(stderr, "WAIT REFUSED: %s\n", oneline.Err(err))
			return 1, r, "", false
		}
		var quiet bytes.Buffer
		if code := advanceCursor(o.busDir, r.Me, r.Open, legacyToken(r.Legacy), o.remote, o.branch, o.attempts, o.noPush, now, &quiet, stderr); code != 0 {
			return code, r, "", false
		}
		skip := fmt.Sprintf("WAIT ADVANCED from=%s to=%s heard=%d\n", oneline.Field(sha8(r.Cursor)), oneline.Field(sha8(head)), r.HeardNew)
		return 0, r, skip, true
	}
	if o.advance && o.bodies && r.AdvanceTo != "" {
		code = advanceCursorTo(o.busDir, r.Me, r.Open, legacyToken(r.Legacy), r.AdvanceTo, o.remote, o.branch, o.attempts, o.noPush, now, &buf, stderr)
	} else if o.advance && !o.bodies {
		code = advanceCursor(o.busDir, r.Me, r.Open, legacyToken(r.Legacy), o.remote, o.branch, o.attempts, o.noPush, now, &buf, stderr)
	}
	return code, r, buf.String(), false
}

// hiddenWholeWait reports whether a switch-day line is drawn after every moment this call
// could see, so that nothing arriving during it would be listed.
//
// THE TRAP IT NAMES. A line given as a DATE is midnight at that date's start, so a line of
// tomorrow's date -- which is what a reader who wanted "from today" naturally types -- is a
// moment after everything anybody writes today. Every note that arrives during the wait is
// then history: not carried, not listed, counted on INBOX LEGACY and nowhere else. The wait
// would run its whole timeout beside a bus that was answering it, which is the exact shape
// of failure this verb exists to end. So it is reported, and the wait returns instead of
// waiting on a line that hides everything.
func hiddenWholeWait(legacy bus.LegacyLine, horizon time.Time) bool {
	return !legacy.Before.IsZero() && legacy.Before.After(horizon)
}

// hiddenReason is that sentence, with the line the reader is standing behind and the line
// they probably meant: an INSTANT, which is what a switch-day line drawn today has to be.
//
// ONE SENTENCE PER LINE DRAWN FORWARD, and where two verbs could both say it, the shared
// listing says it. A forward-drawn DATE is the shape with a remedy, and inboxListing prints
// that remedy -- INBOX SWITCH, with the whole command in it -- on every listing `wait`
// returns; both WAIT NOTE sites above stand down when it did (inboxReading.SwitchDay). What
// is left for WAIT NOTE is the case the listing has nothing to say about: a line drawn
// forward as an INSTANT, which somebody set to the second on purpose and which no canned
// command can fix for them.
func hiddenReason(legacy bus.LegacyLine, now time.Time) string {
	at := now.UTC().Format(bus.LegacyInstantLayout)
	return fmt.Sprintf("your switch-day line is %s, which is after a note written now (%s), so a note arriving during this wait would be taken as history rather than listed; a line drawn today has to be an INSTANT -- read once with `--full --legacy-before %s --advance` to draw it at this moment, or leave it where it is and wait for what comes after it",
		legacy.Text, at, at)
}

// utcDay is the UTC calendar day a moment falls in, at its start. It is what the first-advance
// guard asks its question against: a bus with notes dated before TODAY has a history somebody
// has to decide about, and a bus whose notes are all from today does not. What the guard then
// SUGGESTS is the instant of the refusal rather than a day, because a day boundary in the
// future is a moment after everything written today; see the site above.
func utcDay(now time.Time) time.Time {
	u := now.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

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

// printSwitchDayNote prints the ONE line this whole change exists to print, and prints
// nothing at all when there is nothing to say.
//
// THE SILENCE IT ENDS. A friend's cursor read `... open=0 legacy=2026-09-10` and his inbox
// listed nothing, day after day, on a bus that was busy. Every note was there; his own
// switch-day line stood in front of all of them, because a date is midnight at its START
// and that date was tomorrow's. v0.10.1 made the line an instant and wrote the recovery
// down, and a recovery written down is a recovery for whoever goes looking. He had no
// reason to go looking: from where he sat the tool was working and nobody was writing to
// him. THAT is the bug -- not the date, which he was entitled to draw, but a tool that knew
// exactly what was wrong and exactly what to run, and said neither.
//
// So the line is printed on EVERY run whose cursor carries a forward-drawn date line,
// incremental or full, busy or empty, and it carries the whole command with this run's own
// values in it: nothing to work out, nothing to look up, one line to paste. It is a NOTE
// and not a refusal -- exit codes are untouched and nothing moves without the flag. The
// reader runs the command; the tool only names it.
//
// IT HAS A TOKEN OF ITS OWN, and it wanted one. This sentence first went out under
// `INBOX NOTE`, which is already the token for a listed note -- `INBOX NOTE id=<id> ...` --
// and every line parser on this bus reads field 3 of an `INBOX NOTE` as `id=`. A second
// shape under one token is a grammar that cannot be parsed without reading the whole line,
// so the remedy is `INBOX SWITCH` and `INBOX NOTE` keeps its one meaning. It is also the
// better name: it says what the line is about.
//
// The values this run was not given are printed as the placeholders they are, because
// `check` has no --receipt-max-words, --remote or --branch and a run without --advance has
// no remote either. A quoted `"<remote>"` says "your remote goes here" in a slot that is
// otherwise pasteable; inventing `origin` would be a guess, and this tool does not guess.
//
// The names and paths in it are QUOTED rather than field-escaped, exactly as the
// first-advance guard quotes them, because this half of the sentence is meant to be pasted:
// a bus directory holding a space is `--bus "/a bus/here"` and not `--bus /a\x20bus/here`.
func printSwitchDayNote(stdout io.Writer, legacy, busDir, name, words, remote, branch string, now time.Time) bool {
	drawn, hides, yes := bus.LegacyDateAtOrAfterToday(legacy, now)
	if !yes {
		return false
	}
	fmt.Fprintf(stdout, "INBOX SWITCH your switch-day line is the date %s, which hides every note dated %s or earlier; draw it at an instant, once: nova-bus inbox --bus %s --as %s --receipt-max-words %s --full --legacy-now --advance --remote %s --branch %s\n",
		oneline.Field(drawn.Format(bus.LegacyDateLayout)), oneline.Field(hides.Format(bus.LegacyDateLayout)),
		oneline.Quote(busDir), oneline.Quote(orPlaceholder(name, "<you>")), oneline.Field(words),
		oneline.Quote(orPlaceholder(remote, "<remote>")), oneline.Quote(orPlaceholder(branch, "<branch>")))
	return true
}

// orPlaceholder is a value this run has, or the angle-bracket name of the value it does not.
func orPlaceholder(value, placeholder string) string {
	if value == "" {
		return placeholder
	}
	return value
}

// legacyLine reads a --legacy-before flag: the empty string is no line, and anything that is
// neither a UTC calendar date nor an RFC 3339 UTC instant is exit 2, a bad invocation rather
// than a guess. Both verbs that take the flag read it here, and it reads through
// bus.NewLegacyLine, so the two flags and a cursor cannot accept different spellings of one
// line. The TEXT is kept beside the moment: what a reader typed is what the cursor records
// and what INBOX LEGACY echoes back.
func legacyLine(verb, value string, stderr io.Writer) (bus.LegacyLine, bool) {
	if strings.TrimSpace(value) == "" {
		return bus.LegacyLine{}, true
	}
	line, err := bus.NewLegacyLine(value)
	if err != nil {
		fmt.Fprintf(stderr, "nova-bus %s: --legacy-before %s\n", verb, oneline.Err(err))
		return bus.LegacyLine{}, false
	}
	return line, true
}

func cmdCheck(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("check")
	busDir := f.fs.String("bus", "", "the bus's repository root (required)")
	full := f.fs.Bool("full", false, "walk the whole bus: what CI on main and a first adoption run want")
	as := f.fs.String("as", "", "check what changed since this participant's cursor")
	since := f.fs.String("since", "", "check what changed since this commit")
	legacyBefore := f.fs.String("legacy-before", "", "a finding about the header of a note dated before this UTC date (YYYY-MM-DD, midnight at its start) or UTC instant (RFC 3339, e.g. 2026-09-09T18:07:00Z) warns instead of failing")
	rebuildIndex := f.fs.Bool("rebuild-index", false, "with --full, rewrite each lane's INDEX from the notes on disk")
	gitSeconds := f.fs.Int("git-timeout", defaultGitTimeoutSeconds, "how long one git subprocess may take before this run gives up on it")
	if !f.parse(args, stderr, map[string]*string{"bus": busDir}) {
		return 2
	}
	if !f.gitTimeoutFlag(*gitSeconds, stderr) {
		return 2
	}
	// A check with no baseline is not a check of nothing, it is a caller who has not said
	// what they want checked. There is no default here for the same reason there is no
	// default bus.
	if !*full && strings.TrimSpace(*as) == "" && strings.TrimSpace(*since) == "" {
		fmt.Fprint(stderr, "nova-bus check: give one of --full, --as <name> or --since <commit>; refusing to guess; run: nova-bus help\n")
		return 2
	}
	if *rebuildIndex && !*full {
		fmt.Fprint(stderr, "nova-bus check: --rebuild-index writes each lane's INDEX from every note in it, so it needs --full\n")
		return 2
	}
	line, ok := legacyLine("check", *legacyBefore, stderr)
	if !ok {
		return 2
	}
	opts := bus.CheckOptions{LegacyBefore: line.Before}
	// The root check comes BEFORE the roster: a --bus pointing at a subdirectory of a
	// bigger repository refused with "participants.json: no such file", which is true and
	// is not the caller's mistake. See the same reordering in cmdInbox.
	if !*full {
		if err := bus.IsRepoRoot(*busDir); err != nil {
			fmt.Fprintf(stderr, "nova-bus check: checking only what changed needs git; %s\n", oneline.Err(err))
			return 2
		}
	}
	if *rebuildIndex {
		release, lockErr := bus.LockCheckout(*busDir, checkoutLockWait)
		if lockErr != nil {
			fmt.Fprintf(stderr, "BUS REFUSED: %s\n", oneline.Err(lockErr))
			return 1
		}
		defer release()
	}
	c, err := bus.LoadConfig(*busDir)
	if err != nil {
		fmt.Fprintf(stderr, "nova-bus check: %s\n", oneline.Err(err))
		return 2
	}
	scope := bus.Scope{Full: *full}
	from := ""
	// The lane whose CURSOR this run read, and the switch-day line it found there, kept for
	// the note printed below the scope line. They are "" for every other way of asking.
	noteName, noteLegacy := "", ""
	if !*full {
		if strings.TrimSpace(*since) != "" {
			from, err = bus.ResolveCommit(*busDir, *since)
			if err != nil {
				fmt.Fprintf(stderr, "nova-bus check: %s\n", oneline.Err(err))
				return 2
			}
		} else {
			me, ok := c.Lookup(*as)
			if !ok {
				fmt.Fprintf(stderr, "nova-bus check: --as %q names no one on this bus (known: %s)\n", *as, oneline.Escape(strings.Join(c.KnownNames(), "; ")))
				return 2
			}
			if me.Lane == "" {
				fmt.Fprintf(stderr, "nova-bus check: %q has no lane on this bus, so has no cursor\n", me.Name)
				return 2
			}
			cursor, cerr := bus.ReadCursor(*busDir, me.Lane)
			if cerr != nil {
				fmt.Fprintf(stderr, "BUS REFUSED: %s\n", oneline.Err(cerr))
				return 1
			}
			// A reader with no cursor yet has no baseline, so this run is a full one. It
			// is the same adoption path inbox takes, and it costs one full check once.
			scope.Full, from = cursor.Commit == "", cursor.Commit
			noteName, noteLegacy = me.Name, cursor.Legacy
		}
	}
	if from != "" {
		ok, aerr := bus.IsAncestor(*busDir, from)
		if aerr != nil {
			fmt.Fprintf(stderr, "BUS REFUSED: %s\n", oneline.Err(aerr))
			return 1
		}
		if !ok {
			fmt.Fprintf(stderr, "BUS REFUSED: %s is not an ancestor of HEAD, so a diff from it would name changes that are not changes; run check --full\n", oneline.Field(from))
			return 1
		}
	}

	var problems []bus.Problem
	var stats bus.CheckStats
	if scope.Full {
		t, terr := bus.ReadBus(*busDir, c)
		if terr != nil {
			fmt.Fprintf(stderr, "nova-bus check: %s\n", oneline.Err(terr))
			return 2
		}
		if *rebuildIndex {
			for _, lane := range c.Lanes() {
				n, rerr := bus.RebuildLaneIndex(*busDir, c, t, lane)
				if rerr != nil {
					fmt.Fprintf(stderr, "BUS FAIL %s: %s\n", oneline.Escape(bus.IndexPath(lane)), oneline.Err(rerr))
					return 1
				}
				fmt.Fprintf(stdout, "BUS INDEX lane=%s notes=%d\n", oneline.Field(lane), n)
			}
		}
		idx, ierr := bus.ReadIndex(*busDir, c)
		if ierr != nil {
			fmt.Fprintf(stderr, "nova-bus check: %s\n", oneline.Err(ierr))
			return 2
		}
		problems = append(t.CheckWith(opts), bus.CheckIndex(c, t, idx)...)
		stats = bus.CheckStats{Notes: len(t.Notes), Lanes: len(c.Lanes()), Receipts: len(t.Receipts)}
	} else {
		changed, derr := bus.ChangedSince(*busDir, from)
		if derr != nil {
			fmt.Fprintf(stderr, "BUS REFUSED: %s\n", oneline.Err(derr))
			return 1
		}
		idx, ierr := bus.ReadIndex(*busDir, c)
		if ierr != nil {
			fmt.Fprintf(stderr, "nova-bus check: %s\n", oneline.Err(ierr))
			return 2
		}
		scope.From, scope.Changed = from, len(changed)
		problems, stats = bus.CheckSince(*busDir, c, idx, changed, opts)
	}
	fmt.Fprintf(stdout, "BUS SCOPE mode=%s cursor=%s changed=%d\n",
		oneline.Field(scope.Mode()), oneline.Field(dash(from)), scope.Changed)
	// THE SAME SENTENCE, WHEREVER THE CURSOR IS READ. `check --as <you>` reads a lane's
	// CURSOR for its commit, so it sees the drawn-forward line as plainly as `inbox` does,
	// and a reader polling `check` and reading nothing is in exactly the trouble this note
	// exists for. It is printed under `INBOX SWITCH` here rather than under a `BUS` token of
	// its own -- the one place this verb speaks in another verb's grammar, and on purpose:
	// it is a fact about an INBOX cursor, it names an `inbox` command, and one grep finds
	// it wherever it was met. The values `check` was never given are the placeholders they
	// are; see printSwitchDayNote.
	printSwitchDayNote(stdout, noteLegacy, *busDir, noteName, "<n>", "", "", now)
	failed, warned := 0, 0
	for _, p := range problems {
		// A WARN GOES TO STDOUT, and it went to stderr. The grammar says which stream a
		// line is on and the rule is one sentence: FAIL lines and refusals to stderr,
		// everything else to stdout. A WARN is neither -- it is a finding that was
		// TOLERATED, reported by a run that passed -- so putting it on stderr made every
		// clean-but-forgiving run look like a failing one to anything reading the streams
		// apart, which is what CI does. It is an informational line and it is now where the
		// informational lines are.
		if p.Warn {
			warned++
			fmt.Fprintf(stdout, "BUS WARN %s: %s\n", oneline.Escape(p.Where), oneline.Escape(p.Reason))
			continue
		}
		failed++
		fmt.Fprintf(stderr, "BUS FAIL %s: %s\n", oneline.Escape(p.Where), oneline.Escape(p.Reason))
	}
	if failed > 0 {
		return 1
	}
	fmt.Fprintf(stdout, "BUS OK notes=%d lanes=%d receipts=%d participants=%d warn=%d\n",
		stats.Notes, stats.Lanes, stats.Receipts, len(c.Participants), warned)
	return 0
}

func cmdNames(args []string, stdout, stderr io.Writer) int {
	f := newFlags("names")
	busDir := f.fs.String("bus", "", "the bus's repository root (required)")
	if !f.parse(args, stderr, map[string]*string{"bus": busDir}) {
		return 2
	}
	c, err := bus.LoadConfig(*busDir)
	if err != nil {
		fmt.Fprintf(stderr, "nova-bus names: %s\n", oneline.Err(err))
		return 2
	}
	// THE NAMES ARE QUOTED, NOT FIELD-ESCAPED, and this verb exists for exactly the reason
	// that matters. `names` is what a person runs to find out how to spell a To line this
	// tool will accept -- and under oneline.Field, which escapes every space so that a
	// key=value field is one token, "Ada Vale" printed as `Ada\x20Claude`. Paste that
	// into a To line and `send` refuses it. The verb whose whole job is to tell you the
	// spelling was telling you one the tool does not take.
	//
	// oneline.Quote keeps the one-line guarantee by another route (see its comment): the
	// quotes delimit the value, so a space inside one is not the end of a field, and every
	// character that could break or reorder a line is still escaped. A list is each name
	// quoted and joined by the ";" a To line separates on, so `aliases="Ada Vale";"the
	// keeper"` is two names a person can lift straight out. The lane is a slug and stays a
	// field: it holds no space by construction and is not something anybody pastes.
	for _, p := range c.Participants {
		lane := p.Lane
		if lane == "" {
			lane = "-"
		}
		fmt.Fprintf(stdout, "NAMES NAME name=%s lane=%s aliases=%s\n",
			oneline.Quote(p.Name), oneline.Field(lane), quoteList(p.Aliases))
	}
	for _, g := range c.Groups {
		fmt.Fprintf(stdout, "NAMES GROUP name=%s members=%s\n", oneline.Quote(g.Name), quoteList(g.Members))
	}
	fmt.Fprintf(stdout, "NAMES OK participants=%d groups=%d senders=%d\n", len(c.Participants), len(c.Groups), len(c.Senders()))
	return 0
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
