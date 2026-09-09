// nova-message-bus is the table: a git repository where several lines write notes to each
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
//	check     validates the whole table: headers, ids, threads, receipts, lanes
//	names     echoes the roster, so a person can spell a To line the tool will accept
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

	"github.com/mas-bandwidth/nova-tools/internal/messagebus"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const usage = `nova-message-bus: the family's table, with the races taken out (see SPEC.md)

usage:
  nova-message-bus send --table <dir> --file <path>|--stdin --remote <name> --branch <name> --attempts <n> [--slug <s>] [--no-push]
  nova-message-bus inbox --table <dir> --as <name> --receipt-max-words <n>
  nova-message-bus receipt --table <dir> --as <name> --note <id-or-path> [--note ...] --remote <name> --branch <name> --attempts <n> [--no-push]
  nova-message-bus check --table <dir> [--legacy-before <YYYY-MM-DD>]
  nova-message-bus names --table <dir>

exit codes: 0 the verb ran and passed; 1 the verb ran and said NO -- a draft
refused, a table that failed check, a push that could not be landed; 2 could
not run: missing flag, unreadable table, bad invocation.

Every path comes from a flag. There is no default table, no default remote, no
default branch, no default retry budget and no default receipt word count; a
missing one is a refusal: refusing to guess. The roster is always
<table>/participants.json, because two lines running this tool over one table
must read one roster. Flags come before positional arguments.

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
		return cmdInbox(rest, stdout, stderr)
	case "receipt":
		return cmdReceipt(rest, stdout, stderr, now)
	case "check":
		return cmdCheck(rest, stdout, stderr)
	case "names":
		return cmdNames(rest, stdout, stderr)
	}
	fmt.Fprintf(stderr, "nova-message-bus: unknown subcommand %q\n\n%s", cmd, usage)
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
		fmt.Fprintf(stderr, "nova-message-bus %s: %s\n\n%s", f.verb, oneline.Err(err), usage)
		return false
	}
	if n := f.fs.NArg(); n > 0 {
		fmt.Fprintf(stderr, "nova-message-bus %s: takes no positional arguments, got %d (flags come before arguments)\n", f.verb, n)
		return false
	}
	for name, value := range required {
		if strings.TrimSpace(*value) == "" {
			fmt.Fprintf(stderr, "nova-message-bus %s: --%s is required; refusing to guess\n", f.verb, name)
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
		if err := messagebus.ValidGitArg(c.what, c.value); err != nil {
			fmt.Fprintf(stderr, "nova-message-bus %s: %s\n", f.verb, oneline.Err(err))
			return false
		}
	}
	return true
}

// count reads a required positive integer flag.
func (f *flags) count(name string, value int, stderr io.Writer) bool {
	if value < 1 {
		fmt.Fprintf(stderr, "nova-message-bus %s: --%s must be given and at least 1, got %d; refusing to guess\n", f.verb, name, value)
		return false
	}
	return true
}

// openTable loads the roster and reads the table, or prints the refusal.
func openTable(verb, table string, stderr io.Writer) (*messagebus.Table, bool) {
	c, err := messagebus.LoadConfig(table)
	if err != nil {
		fmt.Fprintf(stderr, "nova-message-bus %s: %s\n", verb, oneline.Err(err))
		return nil, false
	}
	t, err := messagebus.ReadTable(table, c)
	if err != nil {
		fmt.Fprintf(stderr, "nova-message-bus %s: %s\n", verb, oneline.Err(err))
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
	attempts := f.fs.Int("attempts", 0, "how many times to push before giving up (required, at least 1)")
	noPush := f.fs.Bool("no-push", false, "commit but do not push; the note is NOT on the table until it is pushed")
	if !f.parse(args, stderr, map[string]*string{"table": table, "remote": remote, "branch": branch}) {
		return 2
	}
	if !f.count("attempts", *attempts, stderr) {
		return 2
	}
	if !f.gitArgs(*remote, *branch, stderr) {
		return 2
	}
	if (*file == "") == !*useStdin {
		fmt.Fprint(stderr, "nova-message-bus send: give exactly one of --file and --stdin; refusing to guess\n")
		return 2
	}
	source := "(stdin)"
	var text string
	if *useStdin {
		raw, err := io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintf(stderr, "nova-message-bus send: %s\n", oneline.Err(err))
			return 2
		}
		text = string(raw)
	} else {
		source = *file
		raw, err := os.ReadFile(*file)
		if err != nil {
			fmt.Fprintf(stderr, "nova-message-bus send: %s\n", oneline.Err(err))
			return 2
		}
		text = string(raw)
	}
	if err := messagebus.IsRepo(*table); err != nil {
		fmt.Fprintf(stderr, "nova-message-bus send: %s\n", oneline.Err(err))
		return 2
	}
	t, ok := openTable("send", *table, stderr)
	if !ok {
		return 2
	}
	prepared, err := messagebus.Prepare(t, text, now, *slug)
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
	res, err := commit(*table, prepared.Sender, []string{prepared.Path}, prepared.Message, *remote, *branch, *attempts, *noPush)
	if err != nil {
		fmt.Fprintf(stderr, "SEND FAIL %s: %s\n", oneline.Escape(prepared.Path), oneline.Err(err))
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
	attempts := f.fs.Int("attempts", 0, "how many times to push before giving up (required, at least 1)")
	noPush := f.fs.Bool("no-push", false, "commit but do not push; the receipt is NOT on the table until it is pushed")
	var notes stringList
	f.fs.Var(&notes, "note", "a note to mark heard, by id or by path (required; repeatable)")
	if !f.parse(args, stderr, map[string]*string{"table": table, "as": as, "remote": remote, "branch": branch}) {
		return 2
	}
	if !f.count("attempts", *attempts, stderr) {
		return 2
	}
	if !f.gitArgs(*remote, *branch, stderr) {
		return 2
	}
	if len(notes) == 0 {
		fmt.Fprint(stderr, "nova-message-bus receipt: --note is required; refusing to guess\n")
		return 2
	}
	if err := messagebus.IsRepo(*table); err != nil {
		fmt.Fprintf(stderr, "nova-message-bus receipt: %s\n", oneline.Err(err))
		return 2
	}
	t, ok := openTable("receipt", *table, stderr)
	if !ok {
		return 2
	}
	me, found := t.Config.Lookup(*as)
	if !found {
		fmt.Fprintf(stderr, "nova-message-bus receipt: --as %q names no one at this table (known: %s)\n", *as, oneline.Escape(strings.Join(t.Config.KnownNames(), "; ")))
		return 2
	}
	plan, err := messagebus.PlanReceipts(t, me, notes, now)
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
	res, err := commit(*table, me, []string{plan.Path}, plan.Message(me), *remote, *branch, *attempts, *noPush)
	if err != nil {
		fmt.Fprintf(stderr, "RECEIPT FAIL %s: %s\n", oneline.Escape(plan.Path), oneline.Err(err))
		return 1
	}
	fmt.Fprintf(stdout, "RECEIPT OK recorded=%d already=%d commit=%s pushed=%t attempts=%d\n",
		len(plan.Record), len(plan.Already), oneline.Field(res.Commit), res.Pushed, res.Attempts)
	return 0
}

func cmdInbox(args []string, stdout, stderr io.Writer) int {
	f := newFlags("inbox")
	table := f.fs.String("table", "", "the table's repository root (required)")
	as := f.fs.String("as", "", "which participant you are (required)")
	maxWords := f.fs.Int("receipt-max-words", 0, "a body under this many words may be a receipt (required, at least 1)")
	if !f.parse(args, stderr, map[string]*string{"table": table, "as": as}) {
		return 2
	}
	if !f.count("receipt-max-words", *maxWords, stderr) {
		return 2
	}
	t, ok := openTable("inbox", *table, stderr)
	if !ok {
		return 2
	}
	me, found := t.Config.Lookup(*as)
	if !found {
		fmt.Fprintf(stderr, "nova-message-bus inbox: --as %q names no one at this table (known: %s)\n", *as, oneline.Escape(strings.Join(t.Config.KnownNames(), "; ")))
		return 2
	}
	if me.Lane == "" {
		fmt.Fprintf(stderr, "nova-message-bus inbox: %q has no lane on this table, so nothing can answer for them\n", me.Name)
		return 2
	}
	// The files that would not parse are named FIRST and are never silent. A note on the
	// table that this tool cannot read is not a note that does not exist, and dropping it
	// from the listing was the same failure as a lost push with a quieter cause.
	unreadable := t.Unreadable(me.Lane)
	for _, n := range unreadable {
		fmt.Fprintf(stdout, "INBOX UNREADABLE path=%s: %s\n", oneline.Field(n.Path), oneline.Err(n.Parse.Err))
	}
	items := t.Inbox(me, *maxWords)
	notes, heard, receipts := 0, 0, 0
	// Three groups, in this order: the notes that carry something, the ones already heard
	// but not answered, and the bare acknowledgements. The listing that hid four real
	// notes among the receipts is the reason they are separated rather than interleaved by
	// clock; HEARD is between them because a note I have already said "heard" to is still
	// owed an answer, and the receipt that says so must not make it disappear.
	for _, group := range []string{"NOTE", "HEARD", "RECEIPT"} {
		for _, item := range items {
			token := "NOTE"
			switch {
			case item.Heard:
				token = "HEARD"
			case item.Receipt:
				token = "RECEIPT"
			}
			if token != group {
				continue
			}
			switch token {
			case "HEARD":
				heard++
			case "RECEIPT":
				receipts++
			default:
				notes++
			}
			id := item.Note.Header.ID
			if id == "" {
				id = "-"
			}
			at := "-"
			if when := item.Note.When(); !when.IsZero() {
				at = when.Format(messagebus.ReceiptStampLayout)
			}
			fmt.Fprintf(stdout, "INBOX %s id=%s from=%s addr=%s at=%s path=%s: %s\n",
				token, oneline.Field(id), oneline.Field(item.From), oneline.Field(item.Address),
				oneline.Field(at), oneline.Field(item.Note.Path), oneline.Escape(item.Note.Header.Subject))
		}
	}
	// open counts what is still waiting on me. A note I have receipted is listed and is
	// NOT in that count: I have answered the sender's question about whether it arrived.
	fmt.Fprintf(stdout, "INBOX OK as=%s open=%d notes=%d receipts=%d heard=%d unreadable=%d\n",
		oneline.Field(me.Name), notes+receipts, notes, receipts, heard, len(unreadable))
	return 0
}

// legacyDateLayout is the one shape --legacy-before takes: a UTC calendar date, meaning
// midnight at its start. A date rather than a timestamp because the thing being drawn is
// the day a table adopted this tool, and nobody knows that to the second.
const legacyDateLayout = "2006-01-02"

func cmdCheck(args []string, stdout, stderr io.Writer) int {
	f := newFlags("check")
	table := f.fs.String("table", "", "the table's repository root (required)")
	legacyBefore := f.fs.String("legacy-before", "", "notes dated before this UTC date (YYYY-MM-DD) warn instead of failing, for a parse failure or a dangling Re")
	if !f.parse(args, stderr, map[string]*string{"table": table}) {
		return 2
	}
	var opts messagebus.CheckOptions
	if *legacyBefore != "" {
		when, err := time.Parse(legacyDateLayout, *legacyBefore)
		if err != nil {
			fmt.Fprintf(stderr, "nova-message-bus check: --legacy-before %s is not a UTC date of the form %s\n",
				oneline.Field(*legacyBefore), legacyDateLayout)
			return 2
		}
		opts.LegacyBefore = when.UTC()
	}
	t, ok := openTable("check", *table, stderr)
	if !ok {
		return 2
	}
	problems := t.CheckWith(opts)
	failed, warned := 0, 0
	for _, p := range problems {
		// A WARN is a finding that was tolerated, so it goes where the findings go. Its
		// COUNT is on the OK line, on stdout, because a run that passed with fifty
		// forgiven notes and one that passed clean are not the same run.
		token := "BUS FAIL"
		if p.Warn {
			token, warned = "BUS WARN", warned+1
		} else {
			failed++
		}
		fmt.Fprintf(stderr, "%s %s: %s\n", token, oneline.Escape(p.Where), oneline.Escape(p.Reason))
	}
	if failed > 0 {
		return 1
	}
	fmt.Fprintf(stdout, "BUS OK notes=%d lanes=%d receipts=%d participants=%d warn=%d\n",
		len(t.Notes), len(t.Config.Lanes()), len(t.Receipts), len(t.Config.Participants), warned)
	return 0
}

func cmdNames(args []string, stdout, stderr io.Writer) int {
	f := newFlags("names")
	table := f.fs.String("table", "", "the table's repository root (required)")
	if !f.parse(args, stderr, map[string]*string{"table": table}) {
		return 2
	}
	c, err := messagebus.LoadConfig(*table)
	if err != nil {
		fmt.Fprintf(stderr, "nova-message-bus names: %s\n", oneline.Err(err))
		return 2
	}
	for _, p := range c.Participants {
		lane := p.Lane
		if lane == "" {
			lane = "-"
		}
		fmt.Fprintf(stdout, "NAMES NAME name=%s lane=%s aliases=%s\n",
			oneline.Field(p.Name), oneline.Field(lane), oneline.Field(strings.Join(p.Aliases, ";")))
	}
	for _, g := range c.Groups {
		fmt.Fprintf(stdout, "NAMES GROUP name=%s members=%s\n", oneline.Field(g.Name), oneline.Field(strings.Join(g.Members, ";")))
	}
	fmt.Fprintf(stdout, "NAMES OK participants=%d groups=%d senders=%d\n", len(c.Participants), len(c.Groups), len(c.Senders()))
	return 0
}

// --------------------------------------------------------------------------- the shared

// checkoutReady is the guard before anything is written: the table is on the branch the
// caller named, and holds no changes but the ones this run is about to make. A rebase over
// a dirty tree either refuses or sweeps somebody's unrelated work into a note's commit.
func checkoutReady(table, branch string, allow []string) error {
	on, err := messagebus.CurrentBranch(table)
	if err != nil {
		return err
	}
	if on != branch {
		return fmt.Errorf("the table's checkout is on branch %q, not %q", on, branch)
	}
	return messagebus.EnsureClean(table, allow)
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
	return messagebus.EnsureLevelWith(table, remote, branch)
}

// commit is send's and receipt's shared tail: the same commit, the same push protocol, the
// same identity rule. The identity comes from the roster and is passed with `git -c`; this
// tool never writes a git config file.
func commit(table string, who messagebus.Participant, paths []string, message, remote, branch string, attempts int, noPush bool) (messagebus.PushResult, error) {
	id := messagebus.Identity{Name: who.GitName, Email: who.GitEmail}
	if noPush {
		return messagebus.CommitOnly(table, id, paths, message)
	}
	return messagebus.CommitAndPush(table, id, paths, message, remote, branch, attempts)
}
