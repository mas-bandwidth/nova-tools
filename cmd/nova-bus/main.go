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
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const usage = `nova-bus: the bus, with the races taken out (see SPEC.md)

usage:
  nova-bus draft --bus <dir> --as <name> --to <names> [--cc <names>] [--subject <text>] [--re <id>]
  nova-bus send --bus <dir> --file <path>|--stdin [--as <name>] --remote <name> --branch <name> [--attempts <n>] [--slug <s>] [--no-push]
  nova-bus inbox --bus <dir> --as <name> --receipt-max-words <n> [--full] [--open] [--legacy-before <date-or-instant>|--legacy-now|--carry-history]
        [--advance --remote <name> --branch <name> [--attempts <n>] [--no-push]]
  nova-bus wait --bus <dir> --as <name> --receipt-max-words <n> --timeout <duration> --remote <name> --branch <name>
        [--interval <duration>] [--open] [--legacy-before <date-or-instant>|--carry-history]
        [--advance [--attempts <n>] [--no-push]]
  nova-bus receipt --bus <dir> --as <name> --note <id-or-path> [--note ...] --remote <name> --branch <name> [--attempts <n>] [--no-push]
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

inbox prints one INBOX OPEN line for what you are carrying; --open lists those
entries too, from the open list and without opening a note. Past 50 carried, the
plain run adds one INBOX HINT line saying so.

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
new, printing exactly what inbox prints. Nothing by --timeout is a WAIT TIMEOUT line
and exit 0 -- not an error, the answer "nothing yet" -- and you issue the next
one. --timeout is required, because every wait has a deadline, and is at most
60m: a wait runs inside your harness's tool call, so ask your harness what its
limit is and sit under it. The loop is wait, answer, wait:

  nova-bus wait --bus ~/bus --as Ada --receipt-max-words 40 --timeout 25m \
    --advance --remote origin --branch main

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
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, time.Now().UTC()))
}

// run is the whole tool, with its streams and clock injected so the tests can drive it.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, now time.Time) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "draft":
		return cmdDraft(rest, stdout, stderr)
	case "send":
		return cmdSend(rest, stdin, stdout, stderr, now)
	case "inbox":
		return cmdInbox(rest, stdout, stderr, now)
	case "receipt":
		return cmdReceipt(rest, stdout, stderr, now)
	case "wait":
		return cmdWait(rest, stdout, stderr, now)
	case "check":
		return cmdCheck(rest, stdout, stderr, now)
	case "names":
		return cmdNames(rest, stdout, stderr)
	case "version", "--version":
		return cmdVersion(rest, stdout, stderr)
	}
	fmt.Fprintf(stderr, "nova-bus: unknown subcommand %q\n\n%s", cmd, usage)
	return 2
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
		fmt.Fprintf(stderr, "nova-bus %s: %s\n\n%s", f.verb, oneline.Err(err), usage)
		return false
	}
	if n := f.fs.NArg(); n > 0 {
		fmt.Fprintf(stderr, "nova-bus %s: takes no positional arguments, got %d (flags come before arguments)\n", f.verb, n)
		return false
	}
	for name, value := range required {
		if strings.TrimSpace(*value) == "" {
			fmt.Fprintf(stderr, "nova-bus %s: --%s is required; refusing to guess\n", f.verb, name)
			return false
		}
	}
	return true
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

// count reads a required positive integer flag.
func (f *flags) count(name string, value int, stderr io.Writer) bool {
	if value < 1 {
		fmt.Fprintf(stderr, "nova-bus %s: --%s must be given and at least 1, got %d; refusing to guess\n", f.verb, name, value)
		return false
	}
	return true
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
	if seconds < 1 {
		fmt.Fprintf(stderr, "nova-bus %s: --git-timeout is a whole number of seconds and at least 1, got %d\n", f.verb, seconds)
		return false
	}
	if err := bus.SetGitTimeout(time.Duration(seconds) * time.Second); err != nil {
		fmt.Fprintf(stderr, "nova-bus %s: %s\n", f.verb, oneline.Err(err))
		return false
	}
	return true
}

// defaultGitTimeoutSeconds is DefaultGitTimeout as the flag spells it.
const defaultGitTimeoutSeconds = 60

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

// cmdDraft prints the header a note needs and nothing else.
//
// WHY A VERB AND NOT A PARAGRAPH IN THE README. A line's first send is a header they are
// writing from memory of some other bus, and the tool's answer to a header written from
// memory was a refusal per mistake. The skeleton is the same header send writes, with the
// names already checked against the roster, so the first draft cannot be wrong about the
// two things a first draft is always wrong about: what the keys are, and how a name is
// spelled here.
//
// Its standard output is a FILE: the skeleton, alone, with no OK line under it, so
// `nova-bus draft ... > draft.md` is a draft. Refusals go to stderr like every other
// verb's, and every one of them is printed rather than the first.
func cmdDraft(args []string, stdout, stderr io.Writer) int {
	f := newFlags("draft")
	busDir := f.fs.String("bus", "", "the bus's repository root (required: the roster lives in it)")
	as := f.fs.String("as", "", "which participant you are (required)")
	to := f.fs.String("to", "", "who the note is to, as a To line: names, aliases or a group, separated by ; (required)")
	cc := f.fs.String("cc", "", "who else is to see it, as a Cc line")
	subject := f.fs.String("subject", "", "the subject line (default: a placeholder you must replace)")
	var re stringList
	f.fs.Var(&re, "re", "an id this note answers, or `new` to start a thread (repeatable)")
	if !f.parse(args, stderr, map[string]*string{"bus": busDir, "as": as, "to": to}) {
		return 2
	}
	c, err := bus.LoadConfig(*busDir)
	if err != nil {
		fmt.Fprintf(stderr, "nova-bus draft: %s\n", oneline.Err(err))
		return 2
	}
	// Collected, like send's: a draft asked for with a misspelled name and a Re that is
	// not on the bus is two mistakes and one run.
	var problems []error
	if err := bus.OneLine("--subject", *subject); err != nil {
		problems = append(problems, err)
	}
	me, known := c.Lookup(*as)
	switch {
	case !known:
		problems = append(problems, fmt.Errorf("--as %q names no one on this bus (known: %s)", *as, strings.Join(c.KnownNames(), "; ")))
	case me.Lane == "":
		problems = append(problems, fmt.Errorf("--as %q has no lane on this bus, so has nowhere to send from", me.Name))
	}
	for _, line := range []struct{ flag, value string }{{"--to", *to}, {"--cc", *cc}} {
		if strings.TrimSpace(line.value) == "" {
			continue
		}
		names, unknown := c.ResolveList(line.value)
		if len(unknown) > 0 {
			problems = append(problems, fmt.Errorf("%s: %s names no one on this bus (known: %s)", line.flag, strings.Join(bus.UnknownNames(unknown), ", "), strings.Join(c.KnownNames(), "; ")))
			continue
		}
		if len(names) == 0 {
			problems = append(problems, fmt.Errorf("%s: no recipients", line.flag))
		}
	}
	// A Re is checked against the BUS and not the roster, so this is the one thing here
	// that opens the notes -- and only when a --re was given.
	if len(re) > 0 {
		t, terr := bus.ReadBus(*busDir, c)
		if terr != nil {
			fmt.Fprintf(stderr, "nova-bus draft: %s\n", oneline.Err(terr))
			return 2
		}
		for _, r := range re {
			if r == "new" {
				continue
			}
			if _, found := t.Resolve(r); !found {
				problems = append(problems, fmt.Errorf("--re %q is neither an id on this bus nor a note that exists; threads are named by id, and a slug is not a thread", r))
			}
		}
	}
	if len(problems) > 0 {
		for _, reason := range problems {
			fmt.Fprintf(stderr, "DRAFT REFUSED: %s\n", oneline.Err(reason))
		}
		return 2
	}
	skeleton := bus.Skeleton{From: me.Name, To: *to, Cc: *cc, Re: re, Subject: *subject}.Render()
	fmt.Fprint(stdout, skeleton)
	return 0
}

func cmdSend(args []string, stdin io.Reader, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("send")
	busDir := f.fs.String("bus", "", "the bus's repository root (required)")
	file := f.fs.String("file", "", "the draft to send")
	useStdin := f.fs.Bool("stdin", false, "read the draft from standard input instead of --file")
	remote := f.fs.String("remote", "", "the git remote to push to (required)")
	branch := f.fs.String("branch", "", "the branch the bus lives on (required)")
	as := f.fs.String("as", "", "which participant you are; supplies the From line when the draft has none, and is refused if the draft's From line names anybody else")
	slug := f.fs.String("slug", "", "the human half of the filename (default: from the subject)")
	attempts := f.fs.Int("attempts", defaultAttempts, "how many times to push before giving up")
	gitSeconds := f.fs.Int("git-timeout", defaultGitTimeoutSeconds, "how long one git subprocess may take before this run gives up on it")
	noPush := f.fs.Bool("no-push", false, "commit but do not push; the note is NOT on the bus until it is pushed")
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
	if (*file == "") == !*useStdin {
		fmt.Fprint(stderr, "nova-bus send: give exactly one of --file and --stdin; refusing to guess\n")
		return 2
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
	if err := checkoutReady(*busDir, *branch, nil); err != nil {
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
	res, err := commit(*busDir, prepared.Sender, paths,
		bus.WithTrailer(prepared.Message, bus.TrailerSend+" "+prepared.Note.Header.ID),
		*remote, *branch, *attempts, *noPush)
	if err != nil {
		fmt.Fprintf(stderr, "SEND FAIL %s: %s\n", oneline.Escape(prepared.Path), oneline.Err(err))
		printTranscript(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "SEND OK id=%s path=%s commit=%s pushed=%t attempts=%d\n",
		oneline.Field(prepared.Note.Header.ID), oneline.Field(prepared.Path), oneline.Field(res.Commit), res.Pushed, res.Attempts)
	return 0
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
		fmt.Fprint(stderr, "nova-bus receipt: --note is required; refusing to guess\n")
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
	if err := checkoutReady(*busDir, *branch, []string{plan.Path}); err != nil {
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
	res, err := commit(*busDir, me, []string{plan.Path},
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

func cmdInbox(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("inbox")
	busDir := f.fs.String("bus", "", "the bus's repository root (required)")
	as := f.fs.String("as", "", "which participant you are (required)")
	maxWords := f.fs.Int("receipt-max-words", 0, "a body under this many words may be a receipt (required, at least 1)")
	full := f.fs.Bool("full", false, "walk the whole bus instead of what changed since your cursor")
	openList := f.fs.Bool("open", false, "list every open note, not only what is new; the default prints one INBOX OPEN line for them")
	advance := f.fs.Bool("advance", false, "move your cursor to HEAD and push it, the way a receipt is pushed")
	remote := f.fs.String("remote", "", "the git remote to push the cursor to (required with --advance)")
	branch := f.fs.String("branch", "", "the branch the bus lives on (required with --advance)")
	attempts := f.fs.Int("attempts", defaultAttempts, "how many times to push the cursor before giving up")
	gitSeconds := f.fs.Int("git-timeout", defaultGitTimeoutSeconds, "how long one git subprocess may take before this run gives up on it")
	noPush := f.fs.Bool("no-push", false, "with --advance, commit the cursor but do not push it")
	legacyBefore := f.fs.String("legacy-before", "", "notes dated before this UTC date (YYYY-MM-DD, midnight at its start) or UTC instant (RFC 3339, e.g. 2026-09-09T18:07:00Z) are not carried on your open list, and are counted rather than listed")
	legacyNow := f.fs.Bool("legacy-now", false, "draw the switch-day line at THIS run's UTC instant: exactly --legacy-before <now>, so everything already on the bus is history and everything after this moment is news")
	carryHistory := f.fs.Bool("carry-history", false, "on your FIRST --advance, carry every old note on your open list instead of drawing a switch-day line; does nothing otherwise")
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
	if !f.count("receipt-max-words", *maxWords, stderr) {
		return 2
	}
	if !f.gitTimeoutFlag(*gitSeconds, stderr) {
		return 2
	}
	// --advance WRITES to the bus, so it takes the same three flags a receipt takes and
	// refuses to guess any of them. Without it, inbox writes nothing at all, which is what
	// a report should do unless it was asked otherwise.
	if *advance {
		if strings.TrimSpace(*remote) == "" || strings.TrimSpace(*branch) == "" {
			fmt.Fprint(stderr, "nova-bus inbox: --advance moves a cursor onto the bus, so it needs --remote and --branch; refusing to guess\n")
			return 2
		}
		if !f.attempts(*attempts, stderr) {
			return 2
		}
		if !f.gitArgs(*remote, *branch, stderr) {
			return 2
		}
	}
	o := inboxOpts{
		busDir: *busDir, as: *as, maxWords: *maxWords,
		full: *full, openList: *openList, advance: *advance,
		remote: *remote, branch: *branch, attempts: *attempts, noPush: *noPush,
		legacy: flagLegacy, carryHistory: *carryHistory,
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
	if code != 0 || !o.advance {
		return code
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
	advance        bool
	remote, branch string
	attempts       int
	noPush         bool
	// legacy is the --legacy-before line as the flag gave it, or the zero line for no flag.
	// What a run actually reads under is effectiveLegacy of this and the cursor's own.
	legacy       bus.LegacyLine
	carryHistory bool
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
}

// inboxListing is the whole of an inbox report: what to read, what it found, and every
// line of it printed. It writes nothing to the bus -- moving the cursor is advanceCursor,
// which its caller runs after it -- and it takes no lock: the caller holds the checkout,
// because `wait` holds it across a poll's fetch as well as its listing.
func inboxListing(o inboxOpts, stdout, stderr io.Writer, now time.Time) (int, inboxReading) {
	var r inboxReading
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
	// held is the cursor as it stands on the bus, read for the SWITCH-DAY LINE it carries
	// as well as for the commit. A full run reads it too, and ignores a cursor it cannot
	// read: `--full --advance` is the documented repair for a broken cursor, and a repair
	// that refuses to run is not one. What a full run must not do is silently forget a line
	// a reader drew months ago, which is what reading it here prevents.
	held := bus.Cursor{}
	if o.full {
		held, _ = bus.ReadCursor(o.busDir, me.Lane)
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
			changed, err := bus.ChangedSince(o.busDir, cursor.Commit)
			if err != nil {
				fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.Err(err))
				return 1, r
			}
			open, err := bus.ReadOpen(o.busDir, me.Lane)
			if err != nil {
				fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.Err(err))
				return 1, r
			}
			scope.From, scope.Changed = cursor.Commit, len(changed)
			legacy = effectiveLegacy(o.legacy, held)
			res, err = bus.InboxSince(o.busDir, c, me, changed, open, o.maxWords, legacy)
			if err != nil {
				fmt.Fprintf(stderr, "nova-bus inbox: %s\n", oneline.Err(err))
				return 2, r
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
	for _, n := range res.Unreadable {
		fmt.Fprintf(stdout, "INBOX UNREADABLE path=%s: %s\n", oneline.Field(n.Path), oneline.Err(n.Parse.Err))
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
	// LISTING THE OPEN NOTES IS A CHOICE, and the default is not to. A reader carrying five
	// hundred notes gets five hundred lines on every run, and the one new note is in the
	// middle of them -- which is the listing-nobody-reads failure the switch-day line exists
	// to stop, arriving from the other end. So the default prints ONE line for what is being
	// carried, `--open` prints the entries, and `--full` lists everything because a full read
	// is what a person asks for when they want the whole picture. Nothing is hidden either
	// way: the counts are on the OK line, and the entries are in OPEN, which is a file.
	if o.openList || scope.Full {
		// Three groups, in this order: the notes that carry something, the ones already
		// heard but not answered, and the bare acknowledgements. The listing that hid four
		// real notes among the receipts is the reason they are separated rather than
		// interleaved by clock; HEARD is between them because a note I have already said
		// "heard" to is still owed an answer, and the receipt that says so must not make it
		// disappear. Every field comes from the open list, so nothing here opens a note.
		rows := bus.SortForListing(res.Open)
		for _, group := range []string{"NOTE", "HEARD", "RECEIPT"} {
			for _, e := range rows {
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
				fmt.Fprintf(stdout, "INBOX %s id=%s from=%s addr=%s at=%s path=%s: %s\n",
					token, oneline.Field(dash(e.ID)), oneline.Field(dash(e.From)), oneline.Field(dash(e.Addr)),
					oneline.Field(dash(e.Date)), oneline.Field(e.Path), oneline.Escape(e.Subject))
			}
		}
	} else {
		fmt.Fprintf(stdout, "INBOX OPEN carrying=%d heard=%d\n", len(res.Open), heard)
		// And, past the point where a reader would have to count the line themselves, where
		// the entries are. The count alone is the right default at any size -- see the
		// comment above -- but a reader carrying hundreds is the one who wants to look, and
		// the flag that shows them is not guessable from a line that only holds a number.
		if len(res.Open) > openListHint {
			fmt.Fprintf(stdout, "INBOX HINT --open lists the %d carried entries; they are also in %s\n",
				len(res.Open), oneline.Field(bus.OpenPath(me.Lane)))
		}
	}
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
	r.Me, r.Open, r.Legacy, r.Cursor, r.Full = me, res.Open, legacy, cursor.Commit, scope.Full
	// What this run would show a reader as news; see inboxReading.New.
	r.New = res.New
	if scope.Full {
		r.New = len(res.Open)
	}
	return 0, r
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
func advanceCursor(busDir string, me bus.Participant, open []bus.OpenEntry, legacy string, remote, branch string, attempts int, noPush bool, now time.Time, stdout, stderr io.Writer) int {
	head, err := bus.HeadCommit(busDir)
	if err != nil {
		fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.Err(err))
		return 1
	}
	paths := []string{bus.CursorPath(me.Lane), bus.OpenPath(me.Lane)}
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

// openListHint is how many carried entries a plain run prints the INBOX HINT line above.
// It is a threshold on NOISE and not on cost: a reader carrying a handful can see them with
// one more flag and does not need telling, and a reader carrying hundreds is the one who
// asks where they went.
const openListHint = 50

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
func cmdWait(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("wait")
	busDir := f.fs.String("bus", "", "the bus's repository root (required)")
	as := f.fs.String("as", "", "which participant you are (required)")
	maxWords := f.fs.Int("receipt-max-words", 0, "a body under this many words may be a receipt (required, at least 1)")
	timeout := f.fs.Duration("timeout", 0, "how long to wait before returning WAIT TIMEOUT (required; a duration like 25m, at most "+maxWaitTimeout.String()+")")
	interval := f.fs.Duration("interval", defaultWaitInterval, "how long between polls")
	openList := f.fs.Bool("open", false, "list every open note when this wait returns, not only what is new")
	advance := f.fs.Bool("advance", false, "move your cursor to HEAD and push it when this wait returns, the way inbox --advance does")
	remote := f.fs.String("remote", "", "the git remote to fetch the bus from (required: a wait that cannot fetch cannot notice anything)")
	branch := f.fs.String("branch", "", "the branch the bus lives on (required)")
	attempts := f.fs.Int("attempts", defaultAttempts, "how many times to push the cursor before giving up")
	gitSeconds := f.fs.Int("git-timeout", defaultGitTimeoutSeconds, "how long one git subprocess may take before this run gives up on it")
	noPush := f.fs.Bool("no-push", false, "with --advance, commit the cursor but do not push it")
	legacyBefore := f.fs.String("legacy-before", "", "notes dated before this UTC date (YYYY-MM-DD, midnight at its start) or UTC instant (RFC 3339, e.g. 2026-09-09T18:07:00Z) are not carried on your open list, and are counted rather than listed")
	carryHistory := f.fs.Bool("carry-history", false, "on your FIRST --advance, carry every old note on your open list instead of drawing a switch-day line; does nothing otherwise")
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
	if !f.count("receipt-max-words", *maxWords, stderr) {
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
		fmt.Fprint(stderr, "nova-bus wait: --timeout is required and is a duration like 25m; every wait has a deadline, and one with no deadline is a line that is stuck rather than waiting; refusing to guess\n")
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
	if *interval < minWaitInterval {
		fmt.Fprintf(stderr, "nova-bus wait: --interval %s is shorter than %s, and every poll is a git fetch against somebody's server; refusing to fetch faster than that\n",
			oneline.Field(interval.String()), oneline.Field(minWaitInterval.String()))
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
		busDir: *busDir, as: *as, maxWords: *maxWords,
		openList: *openList, advance: *advance,
		remote: *remote, branch: *branch, attempts: *attempts, noPush: *noPush,
		legacy: flagLegacy, carryHistory: *carryHistory,
	}
	// The cursor as it stands, for the line that says this call BEGAN. A cursor that will
	// not read is not refused here: the first poll's listing refuses it, in the sentence
	// inbox already refuses it in.
	held, _ := bus.ReadCursor(*busDir, me.Lane)
	// One line at the start, before anything is waited on, so that a transcript shows the
	// call began and what it was told to do. A tool call that prints nothing for twenty
	// minutes and then prints everything is, while it runs, indistinguishable from one
	// that has hung.
	fmt.Fprintf(stdout, "WAIT as=%s timeout=%s interval=%s cursor=%s\n",
		oneline.Field(me.Name), oneline.Field(timeout.String()), oneline.Field(interval.String()), oneline.Field(dash(held.Commit)))
	return waitLoop(o, *timeout, *interval, stdout, stderr, now)
}

// defaultWaitInterval is how long a wait leaves between polls when the caller names no
// interval. It is a default, unlike --timeout, on the same test the tool's other two
// defaults pass: it is not a fact about a bus that only its owner can supply. Ten seconds
// is under the time it takes to read a note and well over the cost of a fetch, and it is
// the number Glenn asked for after watching the family's lines wait on each other: "the
// polling should be 10 sec". A shorter interval is a shorter round trip between two lines
// that are answering each other, and a git fetch of a bus this size is cheap enough that
// the round trip is what the number should be chosen for.
const defaultWaitInterval = 10 * time.Second

// maxWaitTimeout is as long as `wait` will block, and it is a fact about HARNESSES rather
// than about buses; see the refusal above.
const maxWaitTimeout = 60 * time.Minute

// minWaitInterval is as fast as a wait will poll, because a poll is a git fetch.
const minWaitInterval = 100 * time.Millisecond

// waitLoop is the clock: poll, and either return what arrived or sleep and poll again
// until the deadline. It is apart from the flags so that what it does is readable without
// them.
//
// THE FIRST POLL HAPPENS IMMEDIATELY, before any sleep, because the commonest case is a
// note that is already there -- the caller answered the last one and came straight back --
// and making them wait an interval for news the bus already had would be a tool inventing
// latency.
func waitLoop(o inboxOpts, timeout, interval time.Duration, stdout, stderr io.Writer, now time.Time) int {
	start := time.Now()
	deadline := start.Add(timeout)
	// The moment this call cannot see past: a switch-day line drawn after it hides
	// everything that could possibly arrive during this wait.
	horizon := now.Add(timeout)
	polls := 0
	cursor := ""
	for {
		polls++
		elapsed := time.Since(start).Round(time.Millisecond)
		pollNow := now.Add(elapsed)
		keep := func(r inboxReading) bool { return r.New > 0 || hiddenWholeWait(r.Legacy, horizon) }
		code, r, lines := waitPoll(o, polls == 1, pollNow, keep, stderr)
		if r.Cursor != "" {
			cursor = r.Cursor
		}
		if code != 0 {
			// The listing this poll had already printed, if it printed one, under the
			// refusal that is on stderr: a reader who was shown their inbox has been shown
			// it, whatever happened after.
			fmt.Fprint(stdout, lines)
			return code
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
			return 0
		}
		// A line drawn in the future that does NOT cover the whole wait is no reason to
		// stop, and is still worth a sentence: the notes written before it will not be
		// listed, and a reader who did not mean to draw it there would otherwise find that
		// out by being told about none of them.
		if polls == 1 && !r.Legacy.Before.IsZero() && r.Legacy.Before.After(pollNow) && !r.SwitchDay {
			fmt.Fprintf(stdout, "WAIT NOTE %s\n", oneline.Escape(hiddenReason(r.Legacy, pollNow)))
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
	// tool was awake the whole time.
	fmt.Fprintf(stdout, "WAIT TIMEOUT after=%s polls=%d cursor=%s\n",
		oneline.Field(time.Since(start).Round(time.Millisecond).String()), polls, oneline.Field(dash(cursor)))
	return 0
}

// waitPoll is ONE poll, under the checkout lock: the fetch, the listing, and -- only on the
// poll that returns -- the cursor. It hands its lines back rather than printing them,
// because a poll that found nothing prints nothing: twenty polls of a quiet bus are not
// twenty listings.
//
// The lock is taken and released here rather than around the loop; see lockCheckout.
func waitPoll(o inboxOpts, first bool, now time.Time, keep func(inboxReading) bool, stderr io.Writer) (int, inboxReading, string) {
	release, code := lockCheckout("WAIT", o.busDir, stderr)
	if code != 0 {
		return code, inboxReading{}, ""
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
			return 1, inboxReading{}, ""
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
		return code, r, buf.String()
	}
	if !keep(r) {
		return 0, r, ""
	}
	if o.advance {
		code = advanceCursor(o.busDir, r.Me, r.Open, legacyToken(r.Legacy), o.remote, o.branch, o.attempts, o.noPush, now, &buf, stderr)
	}
	return code, r, buf.String()
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
		fmt.Fprint(stderr, "nova-bus check: give one of --full, --as <name> or --since <commit>; refusing to guess\n")
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
