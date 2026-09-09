// nova-bus is the table: a git repository where several lines write notes to each
// other, one lane directory per sender, one Markdown file per note.
//
// It exists because the table it was written for lost notes. A shared branch keyed by the
// clock races: two senders pushing in the same second meant one push was rejected and a
// line without the rebase reflex simply lost it; one sender writing twice in a minute
// collided on the filename; threads named by exact filename orphaned an answer when a slug
// was retyped; and a bare receipt and a note carrying a finding looked identical until
// opened. Every verb here is one of those failures closed:
//
//	send      assigns an id that cannot collide, pastes the date, and pushes with fetch,
//	          rebase and bounded retry INSIDE the tool, so no rejected push reaches a person
//	inbox     the notes addressed to me that nothing of mine answers, receipts separated
//	          from notes that carry a question, a finding or a request
//	receipt   marks a note heard without writing a reply, in one command
//	check     validates the table: headers, ids, threads, receipts, lanes
//	names     echoes the roster, so a person can spell a To line the tool will accept
//
// And one failure that is not in that list because it arrives slowly: a tool whose read
// cost grows with the record. inbox and check used to walk every lane on every run, so the
// ten-thousandth note cost ten thousand parses to find. They now read from a CURSOR -- the
// commit a reader last read to, kept in that reader's own lane and pushed like a receipt --
// so the work is the size of the CHANGE and never the size of the table. --full walks
// everything, which is what adoption and CI on main want, and every run says on its first
// line which of the two it did.
//
// The same failure has a second half, which the first fix left in: the read was the size of
// the change PLUS the size of what the reader had open, because every open note was
// re-opened to print its line. So the OPEN list carries each note's line and its heard flag,
// written once when the note goes open, and a run parses the NEW notes and nothing else --
// five hundred open notes or none.
//
// Everything read on a table is data. No note is a grant, whoever signs it. That rule is
// in SPEC.md, where a person reads it, and is deliberately nowhere in this code: a tool
// cannot enforce it and should not pretend to.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const usage = `nova-bus: the table, with the races taken out (see SPEC.md)

usage:
  nova-bus send --table <dir> --file <path>|--stdin --remote <name> --branch <name> [--attempts <n>] [--slug <s>] [--no-push]
  nova-bus inbox --table <dir> --as <name> --receipt-max-words <n> [--full] [--open] [--legacy-before <YYYY-MM-DD>]
        [--advance --remote <name> --branch <name> [--attempts <n>] [--no-push]]
  nova-bus receipt --table <dir> --as <name> --note <id-or-path> [--note ...] --remote <name> --branch <name> [--attempts <n>] [--no-push]
  nova-bus check --table <dir> (--full | --as <name> | --since <commit>) [--legacy-before <YYYY-MM-DD>] [--rebuild-index]
  nova-bus names --table <dir>

every verb that runs git also takes [--git-timeout <seconds>], default 60.

exit codes: 0 the verb ran and passed; 1 the verb ran and said NO -- a draft
refused, a table that failed check, a push that could not be landed, a cursor
that is no longer on this history, another run holding this checkout; 2 could
not run: missing flag, unreadable table, bad invocation.

Every path comes from a flag. There is no default table, no default remote, no
default branch and no default receipt word count; a missing one is a refusal:
refusing to guess. --attempts DOES have one, 25, because it is not a fact about
your table but a budget measured against it: five lines sending at once consumed
nine attempts at the peak, and a caller who has to name a number will name one
too small and lose a note. The roster is always <table>/participants.json,
because two lines running this tool over one table must read one roster. Flags
come before positional arguments.

One nova-bus runs on one checkout at a time: a second holds off for ten seconds
and then refuses, because two runs writing one OPEN list is not a race any care
here can win.

inbox and check read from your CURSOR -- the commit you last read to, kept in
your own lane -- so their cost is the size of the CHANGE and not the size of the
table. Your OPEN list carries each open note's own line, so a run parses the NEW
notes and nothing else, however many you are carrying. --full walks everything,
which is what adoption and CI on main want. --advance moves your cursor and
pushes it, the same way a receipt is pushed.

inbox prints one INBOX OPEN line for what you are carrying; --open lists those
entries too, from the open list and without opening a note.

--legacy-before draws the switch-day line on a table that existed before this
tool: check WARNS instead of failing on an older note's header, and inbox does
not carry an older note on your open list, counting them on one INBOX LEGACY
line instead. inbox records the date in your cursor, so later runs honour it
without the flag; moving the line earlier is refused unless the read is --full.

inbox REPORTS and exits 0 whether the inbox is empty or full; check is the gate.
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
	case "send":
		return cmdSend(rest, stdin, stdout, stderr, now)
	case "inbox":
		return cmdInbox(rest, stdout, stderr, now)
	case "receipt":
		return cmdReceipt(rest, stdout, stderr, now)
	case "check":
		return cmdCheck(rest, stdout, stderr)
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
// caller, because a flag this tool will not pass on is a bad invocation and not a table
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
// not a fact about a table that only its owner can supply -- it is the number of times this
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

// openTable loads the roster and reads the table, or prints the refusal.
func openTable(verb, table string, stderr io.Writer) (*bus.Table, bool) {
	c, err := bus.LoadConfig(table)
	if err != nil {
		fmt.Fprintf(stderr, "nova-bus %s: %s\n", verb, oneline.Err(err))
		return nil, false
	}
	t, err := bus.ReadTable(table, c)
	if err != nil {
		fmt.Fprintf(stderr, "nova-bus %s: %s\n", verb, oneline.Err(err))
		return nil, false
	}
	return t, true
}

// ------------------------------------------------------------------------------- verbs

func cmdSend(args []string, stdin io.Reader, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("send")
	table := f.fs.String("table", "", "the table's repository root (required)")
	file := f.fs.String("file", "", "the draft to send")
	useStdin := f.fs.Bool("stdin", false, "read the draft from standard input instead of --file")
	remote := f.fs.String("remote", "", "the git remote to push to (required)")
	branch := f.fs.String("branch", "", "the branch the table lives on (required)")
	slug := f.fs.String("slug", "", "the human half of the filename (default: from the subject)")
	attempts := f.fs.Int("attempts", defaultAttempts, "how many times to push before giving up")
	gitSeconds := f.fs.Int("git-timeout", defaultGitTimeoutSeconds, "how long one git subprocess may take before this run gives up on it")
	noPush := f.fs.Bool("no-push", false, "commit but do not push; the note is NOT on the table until it is pushed")
	if !f.parse(args, stderr, map[string]*string{"table": table, "remote": remote, "branch": branch}) {
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
	if err := bus.IsRepoRoot(*table); err != nil {
		fmt.Fprintf(stderr, "nova-bus send: %s\n", oneline.Err(err))
		return 2
	}
	release, err := bus.LockCheckout(*table, checkoutLockWait)
	if err != nil {
		fmt.Fprintf(stderr, "SEND REFUSED: %s\n", oneline.Err(err))
		return 1
	}
	defer release()
	t, ok := openTable("send", *table, stderr)
	if !ok {
		return 2
	}
	prepared, err := bus.Prepare(t, text, now, *slug)
	if err != nil {
		fmt.Fprintf(stderr, "SEND FAIL %s: %s\n", oneline.Escape(source), oneline.Err(err))
		return 1
	}
	if err := checkoutReady(*table, *branch, nil); err != nil {
		fmt.Fprintf(stderr, "SEND FAIL %s: %s\n", oneline.Escape(source), oneline.Err(err))
		return 1
	}
	if err := levelWithRemote(*table, *remote, *branch, *noPush); err != nil {
		fmt.Fprintf(stderr, "SEND REFUSED: %s\n", oneline.Err(err))
		return 1
	}
	if err := prepared.Save(*table); err != nil {
		fmt.Fprintf(stderr, "SEND FAIL %s: %s\n", oneline.Escape(source), oneline.Err(err))
		return 1
	}
	// The lane's catalogue is appended in the SAME commit as the note. A catalogue that
	// could lag the notes by a commit is one a reader between the two would resolve
	// wrongly, and a note that landed without its line would be invisible to every later
	// id lookup until somebody ran a rebuild.
	if err := prepared.AppendIndex(*table); err != nil {
		fmt.Fprintf(stderr, "SEND FAIL %s: %s\n", oneline.Escape(source), oneline.Err(err))
		return 1
	}
	// The union-merge rules, written once per table and committed with the note that first
	// needed them. INDEX and RECEIPTS are append-only, and without the rule two benches of
	// one lane appending at the same end over one base conflict and the bench is wedged. It
	// goes on the FIRST send rather than being asked of the table's owner because a table
	// that has to be prepared by hand before it is safe is a table somebody will not
	// prepare. See internal/bus/attributes.go.
	paths := prepared.Paths()
	wroteAttrs, err := bus.EnsureMergeAttributes(*table)
	if err != nil {
		fmt.Fprintf(stderr, "SEND FAIL %s: %s\n", oneline.Escape(bus.AttributesName), oneline.Err(err))
		return 1
	}
	if wroteAttrs {
		paths = append(paths, bus.AttributesName)
	}
	res, err := commit(*table, prepared.Sender, paths,
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
	table := f.fs.String("table", "", "the table's repository root (required)")
	as := f.fs.String("as", "", "which participant you are (required)")
	remote := f.fs.String("remote", "", "the git remote to push to (required)")
	branch := f.fs.String("branch", "", "the branch the table lives on (required)")
	attempts := f.fs.Int("attempts", defaultAttempts, "how many times to push before giving up")
	gitSeconds := f.fs.Int("git-timeout", defaultGitTimeoutSeconds, "how long one git subprocess may take before this run gives up on it")
	noPush := f.fs.Bool("no-push", false, "commit but do not push; the receipt is NOT on the table until it is pushed")
	var notes stringList
	f.fs.Var(&notes, "note", "a note to mark heard, by id or by path (required; repeatable)")
	if !f.parse(args, stderr, map[string]*string{"table": table, "as": as, "remote": remote, "branch": branch}) {
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
	if err := bus.IsRepoRoot(*table); err != nil {
		fmt.Fprintf(stderr, "nova-bus receipt: %s\n", oneline.Err(err))
		return 2
	}
	release, lockErr := bus.LockCheckout(*table, checkoutLockWait)
	if lockErr != nil {
		fmt.Fprintf(stderr, "RECEIPT REFUSED: %s\n", oneline.Err(lockErr))
		return 1
	}
	defer release()
	t, ok := openTable("receipt", *table, stderr)
	if !ok {
		return 2
	}
	me, found := t.Config.Lookup(*as)
	if !found {
		fmt.Fprintf(stderr, "nova-bus receipt: --as %q names no one at this table (known: %s)\n", *as, oneline.Escape(strings.Join(t.Config.KnownNames(), "; ")))
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
	if err := checkoutReady(*table, *branch, []string{plan.Path}); err != nil {
		fmt.Fprintf(stderr, "RECEIPT FAIL %s: %s\n", oneline.Escape(plan.Path), oneline.Err(err))
		return 1
	}
	if err := levelWithRemote(*table, *remote, *branch, *noPush); err != nil {
		fmt.Fprintf(stderr, "RECEIPT REFUSED: %s\n", oneline.Err(err))
		return 1
	}
	if err := plan.Append(*table); err != nil {
		fmt.Fprintf(stderr, "RECEIPT FAIL %s: %s\n", oneline.Escape(plan.Path), oneline.Err(err))
		return 1
	}
	res, err := commit(*table, me, []string{plan.Path},
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
	table := f.fs.String("table", "", "the table's repository root (required)")
	as := f.fs.String("as", "", "which participant you are (required)")
	maxWords := f.fs.Int("receipt-max-words", 0, "a body under this many words may be a receipt (required, at least 1)")
	full := f.fs.Bool("full", false, "walk the whole table instead of what changed since your cursor")
	openList := f.fs.Bool("open", false, "list every open note, not only what is new; the default prints one INBOX OPEN line for them")
	advance := f.fs.Bool("advance", false, "move your cursor to HEAD and push it, the way a receipt is pushed")
	remote := f.fs.String("remote", "", "the git remote to push the cursor to (required with --advance)")
	branch := f.fs.String("branch", "", "the branch the table lives on (required with --advance)")
	attempts := f.fs.Int("attempts", defaultAttempts, "how many times to push the cursor before giving up")
	gitSeconds := f.fs.Int("git-timeout", defaultGitTimeoutSeconds, "how long one git subprocess may take before this run gives up on it")
	noPush := f.fs.Bool("no-push", false, "with --advance, commit the cursor but do not push it")
	legacyBefore := f.fs.String("legacy-before", "", "notes dated before this UTC date (YYYY-MM-DD) are not carried on your open list, and are counted rather than listed")
	if !f.parse(args, stderr, map[string]*string{"table": table, "as": as}) {
		return 2
	}
	flagLegacy, ok := legacyDate("inbox", *legacyBefore, stderr)
	if !ok {
		return 2
	}
	if !f.count("receipt-max-words", *maxWords, stderr) {
		return 2
	}
	if !f.gitTimeoutFlag(*gitSeconds, stderr) {
		return 2
	}
	// --advance WRITES to the table, so it takes the same three flags a receipt takes and
	// refuses to guess any of them. Without it, inbox writes nothing at all, which is what
	// a report should do unless it was asked otherwise.
	if *advance {
		if strings.TrimSpace(*remote) == "" || strings.TrimSpace(*branch) == "" {
			fmt.Fprint(stderr, "nova-bus inbox: --advance moves a cursor onto the table, so it needs --remote and --branch; refusing to guess\n")
			return 2
		}
		if !f.attempts(*attempts, stderr) {
			return 2
		}
		if !f.gitArgs(*remote, *branch, stderr) {
			return 2
		}
	}
	// THE ROOT CHECK COMES BEFORE THE ROSTER, and it did not. Point --table at a
	// subdirectory of a bigger repository and the run refused with "participants.json: no
	// such file" -- true, and the wrong sentence: the caller's mistake is the directory,
	// not the roster, and the refusal that names the repository root is the one that fixes
	// the invocation. The cheaper check is not the more useful one, so the more useful one
	// runs first.
	if !*full || *advance {
		if err := bus.IsRepoRoot(*table); err != nil {
			fmt.Fprintf(stderr, "nova-bus inbox: reading only what changed, and moving a cursor, need git; %s\n", oneline.Err(err))
			return 2
		}
		release, lockErr := bus.LockCheckout(*table, checkoutLockWait)
		if lockErr != nil {
			fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.Err(lockErr))
			return 1
		}
		defer release()
	}
	c, err := bus.LoadConfig(*table)
	if err != nil {
		fmt.Fprintf(stderr, "nova-bus inbox: %s\n", oneline.Err(err))
		return 2
	}
	me, found := c.Lookup(*as)
	if !found {
		fmt.Fprintf(stderr, "nova-bus inbox: --as %q names no one at this table (known: %s)\n", *as, oneline.Escape(strings.Join(c.KnownNames(), "; ")))
		return 2
	}
	if me.Lane == "" {
		fmt.Fprintf(stderr, "nova-bus inbox: %q has no lane on this table, so nothing can answer for them\n", me.Name)
		return 2
	}

	scope := bus.Scope{Full: *full}
	var res bus.InboxResult
	var cursor bus.Cursor
	// held is the cursor as it stands on the table, read for the SWITCH-DAY LINE it carries
	// as well as for the commit. A full run reads it too, and ignores a cursor it cannot
	// read: `--full --advance` is the documented repair for a broken cursor, and a repair
	// that refuses to run is not one. What a full run must not do is silently forget a line
	// a reader drew months ago, which is what reading it here prevents.
	held := bus.Cursor{}
	if *full {
		held, _ = bus.ReadCursor(*table, me.Lane)
	}
	// The line this run reads under, whichever mode it is in.
	var legacy bus.LegacyLine
	if !*full {
		cursor, err = bus.ReadCursor(*table, me.Lane)
		if err != nil {
			fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.Err(err))
			return 1
		}
		held = cursor
		if cursor.Commit == "" {
			// A reader with no cursor has no place to stand, so the first run is a full
			// one and --advance gives them a cursor from then on. That is the adoption
			// path, and it costs exactly one full read, ever.
			scope.Full = true
		} else {
			// A line that MOVES EARLIER re-opens every note between the two dates, which
			// on the table this was written for is hundreds -- and it would arrive as a
			// listing the reader has already settled, with nothing saying why. Moving it
			// later forgives more and is fine; moving it earlier is a refusal that names
			// the read which can honestly do it, because a --full run derives the whole
			// open list again rather than taking the cursor's word for it.
			if !flagLegacy.IsZero() && held.Legacy != "" && flagLegacy.Before(held.LegacyBefore()) {
				fmt.Fprintf(stderr, "INBOX REFUSED: your cursor was written with legacy=%s and --legacy-before %s moves the line earlier, which would put the notes between the two dates back on your open list; read once with --full --legacy-before %s --advance, which builds the open list again from the whole table, or leave the flag off and the cursor's line stands\n",
					oneline.Field(held.Legacy), oneline.Field(flagLegacy.Format(bus.LegacyDateLayout)), oneline.Field(flagLegacy.Format(bus.LegacyDateLayout)))
				return 1
			}
			ok, err := bus.IsAncestor(*table, cursor.Commit)
			if err != nil {
				fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.Err(err))
				return 1
			}
			if !ok {
				fmt.Fprintf(stderr, "INBOX REFUSED: the cursor %s is not an ancestor of HEAD, so a diff from it would report changes that are not changes and miss notes that are (a rewritten history, or a cursor from another branch); read once with --full, and --advance will replace it\n", oneline.Field(cursor.Commit))
				return 1
			}
			// The other way a cursor stops being trustworthy: the OPEN list it was
			// written beside is gone. An empty OPEN list is REMOVED rather than left
			// zero-length, so absent and nothing-open are one state on disk -- which is
			// why the cursor carries the count it was written with, and why a cursor
			// that says it was carrying notes with no OPEN beside it is refused here
			// instead of quietly reporting open=0 over the notes it dropped.
			if cursor.Counted && cursor.Open > 0 && !bus.OpenPresent(*table, me.Lane) {
				fmt.Fprintf(stderr, "INBOX REFUSED: your cursor %s says it was carrying %d notes and %s is not on the table, so a read from it would drop them and print open=0; read once with --full --advance, which rebuilds the open list from the whole table\n",
					oneline.Field(cursor.Commit), cursor.Open, oneline.Field(bus.OpenPath(me.Lane)))
				return 1
			}
			changed, err := bus.ChangedSince(*table, cursor.Commit)
			if err != nil {
				fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.Err(err))
				return 1
			}
			open, err := bus.ReadOpen(*table, me.Lane)
			if err != nil {
				fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.Err(err))
				return 1
			}
			scope.From, scope.Changed = cursor.Commit, len(changed)
			legacy = effectiveLegacy(flagLegacy, held)
			res, err = bus.InboxSince(*table, c, me, changed, open, *maxWords, legacy)
			if err != nil {
				fmt.Fprintf(stderr, "nova-bus inbox: %s\n", oneline.Err(err))
				return 2
			}
		}
	}
	if scope.Full {
		t, err := bus.ReadTable(*table, c)
		if err != nil {
			fmt.Fprintf(stderr, "nova-bus inbox: %s\n", oneline.Err(err))
			return 2
		}
		legacy = effectiveLegacy(flagLegacy, held)
		// A full read is the one that DERIVES the open list rather than inheriting it: the
		// display line of every open note, the heard flag of every receipted one rebuilt from
		// RECEIPTS, and the unreadable files carried rather than named once and dropped. Every
		// later incremental run prints from what this run writes.
		res.Unreadable = t.Unreadable(me.Lane)
		// EVERY note on the table that reaches no reader, named on a full read to every
		// reader. A note whose To line resolves to nobody parses, so it is not unreadable,
		// and it is in no inbox, so no listing has ever mentioned it: 22 of them on the
		// family's real table. See internal/bus/unaddressed.go.
		res.Unaddressed = t.UnaddressedOnTable()
		res.Open, res.Legacy = bus.SplitLegacy(bus.OpenFromFull(t.Inbox(me, *maxWords), res.Unreadable), legacy)
	}

	// What was walked, said FIRST, because a listing that does not say what it looked at
	// is a listing a reader will mistake for everything.
	fmt.Fprintf(stdout, "INBOX SCOPE mode=%s cursor=%s changed=%d carrying=%d\n",
		oneline.Field(scope.Mode()), oneline.Field(dash(cursor.Commit)), scope.Changed, len(res.Open))
	// The switch-day line, said as ONE line and next to the scope, because it is part of
	// what this run looked at: `notes=` is how many notes it left off the open list for
	// being older than the line. They are never listed one by one -- the whole point of the
	// line is that six hundred of them do not become a listing -- and this is the only
	// place a reader is told the number, so it is printed whenever a line is in force, a
	// count of zero included.
	if !legacy.Before.IsZero() {
		fmt.Fprintf(stdout, "INBOX LEGACY before=%s notes=%d\n",
			oneline.Field(legacy.Before.Format(bus.LegacyDateLayout)), res.Legacy)
	}
	// The files that would not parse are named next and are never silent. A note on the
	// table that this tool cannot read is not a note that does not exist, and dropping it
	// from the listing was the same failure as a lost push with a quieter cause.
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
	if *openList || scope.Full {
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
	if !*advance {
		return 0
	}
	return advanceCursor(*table, me, res.Open, legacyToken(legacy), *remote, *branch, *attempts, *noPush, now, stdout, stderr)
}

// effectiveLegacy is the line a run reads under: the flag when it is given, and otherwise
// the line this reader's cursor was already written with. A line that had to be retyped on
// every run is a line that would be forgotten on one, and the run that forgot it would open
// every note behind it at once.
func effectiveLegacy(flag time.Time, held bus.Cursor) bus.LegacyLine {
	if !flag.IsZero() {
		return bus.LegacyLine{Before: flag}
	}
	return bus.LegacyLine{Before: held.LegacyBefore()}
}

// legacyToken is the line as a cursor records it, or "" for no line at all.
func legacyToken(l bus.LegacyLine) string {
	if l.Before.IsZero() {
		return ""
	}
	return l.Before.Format(bus.LegacyDateLayout)
}

// advanceCursor writes this reader's CURSOR and OPEN, commits them under their own
// identity, and pushes them with the same protocol a receipt uses.
//
// The same protocol and not a lighter one, on purpose. A cursor is a claim that everything
// up to a commit has been read, and a claim only one bench can see is a claim nobody at the
// table can check -- the same reason a receipt is a file and not a mood. So it refuses a
// dirty checkout, refuses a branch ahead of the remote, commits naming its paths under the
// roster's identity, and recovers a rejected push by fetching and rebasing.
//
// It runs AFTER the listing is printed. A reader who was shown their inbox and whose push
// then failed has still been shown their inbox; the cursor simply has not moved, and the
// next run shows them the same notes again -- which is the safe direction to fail in.
//
// The cursor also records the SWITCH-DAY LINE this run read under, so the next run honours
// it without the flag and everybody at the table can see which notes this reader has taken
// as read.
func advanceCursor(table string, me bus.Participant, open []bus.OpenEntry, legacy string, remote, branch string, attempts int, noPush bool, now time.Time, stdout, stderr io.Writer) int {
	head, err := bus.HeadCommit(table)
	if err != nil {
		fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.Err(err))
		return 1
	}
	paths := []string{bus.CursorPath(me.Lane), bus.OpenPath(me.Lane)}
	if err := checkoutReady(table, branch, paths); err != nil {
		fmt.Fprintf(stderr, "INBOX FAIL %s: %s\n", oneline.Escape(bus.CursorPath(me.Lane)), oneline.Err(err))
		return 1
	}
	if err := levelWithRemote(table, remote, branch, noPush); err != nil {
		fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.Err(err))
		return 1
	}
	if err := bus.WriteOpen(table, me.Lane, open); err != nil {
		fmt.Fprintf(stderr, "INBOX FAIL %s: %s\n", oneline.Escape(bus.OpenPath(me.Lane)), oneline.Err(err))
		return 1
	}
	if err := bus.WriteCursor(table, me.Lane, head, len(open), legacy, now); err != nil {
		fmt.Fprintf(stderr, "INBOX FAIL %s: %s\n", oneline.Escape(bus.CursorPath(me.Lane)), oneline.Err(err))
		return 1
	}
	// The commit names both paths, so an OPEN file this run REMOVED -- a reader with
	// nothing left open -- is staged as the deletion it is rather than left behind. A
	// reader who never had one is a different case: there is nothing to record, and asking
	// git to stage a path that is neither on disk nor in the index is exit 128.
	staged, err := bus.StagePaths(table, paths)
	if err != nil {
		fmt.Fprintf(stderr, "INBOX FAIL %s: %s\n", oneline.Escape(bus.CursorPath(me.Lane)), oneline.Err(err))
		return 1
	}
	res, err := commit(table, me, staged,
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

// legacyDate reads a --legacy-before flag: the empty string is no line, and anything that
// is not a UTC calendar date is exit 2, a bad invocation rather than a guess. Both verbs
// that take the flag read it here so the two cannot accept different spellings of a day.
func legacyDate(verb, value string, stderr io.Writer) (time.Time, bool) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, true
	}
	when, err := time.Parse(bus.LegacyDateLayout, value)
	if err != nil {
		fmt.Fprintf(stderr, "nova-bus %s: --legacy-before %s is not a UTC date of the form %s\n",
			verb, oneline.Field(value), bus.LegacyDateLayout)
		return time.Time{}, false
	}
	return when.UTC(), true
}

func cmdCheck(args []string, stdout, stderr io.Writer) int {
	f := newFlags("check")
	table := f.fs.String("table", "", "the table's repository root (required)")
	full := f.fs.Bool("full", false, "walk the whole table: what CI on main and a first adoption run want")
	as := f.fs.String("as", "", "check what changed since this participant's cursor")
	since := f.fs.String("since", "", "check what changed since this commit")
	legacyBefore := f.fs.String("legacy-before", "", "a finding about the header of a note dated before this UTC date (YYYY-MM-DD) warns instead of failing")
	rebuildIndex := f.fs.Bool("rebuild-index", false, "with --full, rewrite each lane's INDEX from the notes on disk")
	gitSeconds := f.fs.Int("git-timeout", defaultGitTimeoutSeconds, "how long one git subprocess may take before this run gives up on it")
	if !f.parse(args, stderr, map[string]*string{"table": table}) {
		return 2
	}
	if !f.gitTimeoutFlag(*gitSeconds, stderr) {
		return 2
	}
	// A check with no baseline is not a check of nothing, it is a caller who has not said
	// what they want checked. There is no default here for the same reason there is no
	// default table.
	if !*full && strings.TrimSpace(*as) == "" && strings.TrimSpace(*since) == "" {
		fmt.Fprint(stderr, "nova-bus check: give one of --full, --as <name> or --since <commit>; refusing to guess\n")
		return 2
	}
	if *rebuildIndex && !*full {
		fmt.Fprint(stderr, "nova-bus check: --rebuild-index writes each lane's INDEX from every note in it, so it needs --full\n")
		return 2
	}
	when, ok := legacyDate("check", *legacyBefore, stderr)
	if !ok {
		return 2
	}
	opts := bus.CheckOptions{LegacyBefore: when}
	// The root check comes BEFORE the roster: a --table pointing at a subdirectory of a
	// bigger repository refused with "participants.json: no such file", which is true and
	// is not the caller's mistake. See the same reordering in cmdInbox.
	if !*full {
		if err := bus.IsRepoRoot(*table); err != nil {
			fmt.Fprintf(stderr, "nova-bus check: checking only what changed needs git; %s\n", oneline.Err(err))
			return 2
		}
	}
	if *rebuildIndex {
		release, lockErr := bus.LockCheckout(*table, checkoutLockWait)
		if lockErr != nil {
			fmt.Fprintf(stderr, "BUS REFUSED: %s\n", oneline.Err(lockErr))
			return 1
		}
		defer release()
	}
	c, err := bus.LoadConfig(*table)
	if err != nil {
		fmt.Fprintf(stderr, "nova-bus check: %s\n", oneline.Err(err))
		return 2
	}
	scope := bus.Scope{Full: *full}
	from := ""
	if !*full {
		if strings.TrimSpace(*since) != "" {
			from, err = bus.ResolveCommit(*table, *since)
			if err != nil {
				fmt.Fprintf(stderr, "nova-bus check: %s\n", oneline.Err(err))
				return 2
			}
		} else {
			me, ok := c.Lookup(*as)
			if !ok {
				fmt.Fprintf(stderr, "nova-bus check: --as %q names no one at this table (known: %s)\n", *as, oneline.Escape(strings.Join(c.KnownNames(), "; ")))
				return 2
			}
			if me.Lane == "" {
				fmt.Fprintf(stderr, "nova-bus check: %q has no lane on this table, so has no cursor\n", me.Name)
				return 2
			}
			cursor, cerr := bus.ReadCursor(*table, me.Lane)
			if cerr != nil {
				fmt.Fprintf(stderr, "BUS REFUSED: %s\n", oneline.Err(cerr))
				return 1
			}
			// A reader with no cursor yet has no baseline, so this run is a full one. It
			// is the same adoption path inbox takes, and it costs one full check once.
			scope.Full, from = cursor.Commit == "", cursor.Commit
		}
	}
	if from != "" {
		ok, aerr := bus.IsAncestor(*table, from)
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
		t, terr := bus.ReadTable(*table, c)
		if terr != nil {
			fmt.Fprintf(stderr, "nova-bus check: %s\n", oneline.Err(terr))
			return 2
		}
		if *rebuildIndex {
			for _, lane := range c.Lanes() {
				n, rerr := bus.RebuildLaneIndex(*table, c, t, lane)
				if rerr != nil {
					fmt.Fprintf(stderr, "BUS FAIL %s: %s\n", oneline.Escape(bus.IndexPath(lane)), oneline.Err(rerr))
					return 1
				}
				fmt.Fprintf(stdout, "BUS INDEX lane=%s notes=%d\n", oneline.Field(lane), n)
			}
		}
		idx, ierr := bus.ReadIndex(*table, c)
		if ierr != nil {
			fmt.Fprintf(stderr, "nova-bus check: %s\n", oneline.Err(ierr))
			return 2
		}
		problems = append(t.CheckWith(opts), bus.CheckIndex(c, t, idx)...)
		stats = bus.CheckStats{Notes: len(t.Notes), Lanes: len(c.Lanes()), Receipts: len(t.Receipts)}
	} else {
		changed, derr := bus.ChangedSince(*table, from)
		if derr != nil {
			fmt.Fprintf(stderr, "BUS REFUSED: %s\n", oneline.Err(derr))
			return 1
		}
		idx, ierr := bus.ReadIndex(*table, c)
		if ierr != nil {
			fmt.Fprintf(stderr, "nova-bus check: %s\n", oneline.Err(ierr))
			return 2
		}
		scope.From, scope.Changed = from, len(changed)
		problems, stats = bus.CheckSince(*table, c, idx, changed, opts)
	}
	fmt.Fprintf(stdout, "BUS SCOPE mode=%s cursor=%s changed=%d\n",
		oneline.Field(scope.Mode()), oneline.Field(dash(from)), scope.Changed)
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
	table := f.fs.String("table", "", "the table's repository root (required)")
	if !f.parse(args, stderr, map[string]*string{"table": table}) {
		return 2
	}
	c, err := bus.LoadConfig(*table)
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

// checkoutReady is the guard before anything is written: the table is on the branch the
// caller named, and holds no changes but the ones this run is about to make. A rebase over
// a dirty tree either refuses or sweeps somebody's unrelated work into a note's commit.
func checkoutReady(table, branch string, allow []string) error {
	on, err := bus.CurrentBranch(table)
	if err != nil {
		return err
	}
	if on != branch {
		return fmt.Errorf("the table's checkout is on branch %q, not %q", on, branch)
	}
	return bus.EnsureClean(table, allow)
}

// levelWithRemote is the second guard, and it runs BEFORE the file is written: a push
// publishes the branch, not the commit, so a checkout already holding commits this tool
// did not make would send those to the table too, under a note's name, with nothing in the
// output saying so.
//
// Under --no-push there is nothing to publish and no reason to make the caller wait on a
// fetch they did not ask for, so the guard is skipped. The commit then sits on a branch
// that is already ahead, which is the state the caller chose by passing the flag; the note
// is not on the table either way, and pushed=false says so.
func levelWithRemote(table, remote, branch string, noPush bool) error {
	if noPush {
		return nil
	}
	return bus.EnsureLevelWith(table, remote, branch)
}

// commit is send's and receipt's shared tail: the same commit, the same push protocol, the
// same identity rule. The identity comes from the roster and is passed with `git -c`; this
// tool never writes a git config file.
func commit(table string, who bus.Participant, paths []string, message, remote, branch string, attempts int, noPush bool) (bus.PushResult, error) {
	id := bus.Identity{Name: who.GitName, Email: who.GitEmail}
	if noPush {
		return bus.CommitOnly(table, id, paths, message)
	}
	return bus.CommitAndPush(table, id, paths, message, remote, branch, attempts)
}
