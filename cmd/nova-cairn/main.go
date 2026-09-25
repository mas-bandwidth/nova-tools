// nova-cairn checkpoints a session without imposing a memory lifecycle.
//
// WHAT IT IS FOR. The repeated cost of carrying work across a session's end:
// manual timestamps, repeated append/commit commands, recovering session
// pointers, reconstructing what was preserved. This tool opens a session
// record, appends the friend's exact words with a real clock stamp, stable
// identifiers and source pointers, and builds a bounded index and coverage
// ledger over them — mechanically, with the caller choosing the publication
// policy on every mutating verb.
//
// WHAT IT REFUSES TO BE. Not a lifecycle: no seal, no consume, no delete,
// no grading, no consolidation, no liveness inference, no mandatory
// cardinality, no keeper/bud model, no prescribed headings. Those are
// separate explicit choices and are refused here as unknown subcommands, so
// no friend's practice is renamed by adopting this tool.
//
// Every path, every identity and every policy comes from a flag. There is no
// default store, no environment variable and no discovery: a missing flag is
// a refusal, never a guess. Exit 0 ran and passed, 1 ran and failed (a
// conflict the caller must resolve), 2 could not run.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/cairn"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

var version string

const usage = `nova-cairn: optional checkpoints, no memory lifecycle (see docs/SPEC-CAIRN.md)

usage:
  nova-cairn version
  nova-cairn open    --store <dir> --session <id> [--source <ptr>] --publish <never|manual|deferred|immediate> [--now <rfc3339-utc>]
  nova-cairn append  --store <dir> --session <id> --entry <id> (--text <words> | --file <path|->) [--source <ptr>] --publish <never|manual|deferred|immediate> [--now <rfc3339-utc>]
  nova-cairn index   --store <dir> [--session <id>] [--max <n>]
  nova-cairn receipt --store <dir> --session <id> --entry <id>

flags:
  --store <dir>     the checkpoint store. Required, always: there is no
                    environment variable and no discovery from the working
                    directory. The store is plain files; a note is fsync-durable
                    before success is reported, independently of Redis and of
                    any remote. Two shapes are read: this tool's own
                    (sessions/<id>.md, entries/, log.jsonl) and a bench store of
                    one markdown file per session directly under the store
                    (<id>.md), appended by hand. On the second, open is a no-op
                    and append lands a dated "## <stamp> - <entry>" section at
                    the end of the file; no index and no directory appear
                    beside it.
  --session <id>    the stable session identifier. Required: retries and
                    recoveries address the same record by this name.
  --entry <id>      the stable entry identifier. Required on append and receipt:
                    retrying with the same id and the same words succeeds as a
                    duplicate; the same id with different words is refused.
  --text <words>    the friend's exact words, stored byte-for-byte. Exactly one
  --file <path|->   of --text or --file: --file - reads stdin.
  --source <ptr>    where the words came from (transcript path, line range,
                    bench/session pointer). Recorded, never opened.
  --publish <pol>   caller-chosen publication policy, one of never, manual,
                    deferred, immediate. Required on open and append. This slice
                    implements no transport: every success reports
                    persisted=true with published=false, and local durability
                    never implies replicated durability.
  --now <rfc3339>   the stamp, for tests and replays. Default is the real clock
                    in UTC; a value that does not parse as RFC 3339 UTC is exit 2.
  --max <n>         index only: entry lines to print before one MORE line stands
                    for the rest. Default 20, and 0 prints all. The count is
                    never capped -- the coverage line carries the total.

There is deliberately no seal, consume, delete, grade, consolidate or wake
verb: the boundary in SPEC-CAIRN.md lists them, and naming one here is exit 2.

exit codes: 0 ran and passed, 1 ran and failed (conflict), 2 could not run (bad invocation).

example:
  nova-cairn open --store ./cairns --session s1 --publish manual
  nova-cairn append --store ./cairns --session s1 --entry e1 --text "the words to keep" --publish manual
  nova-cairn index --store ./cairns
  nova-cairn receipt --store ./cairns --session s1 --entry e1

Those four are one sitting, in order: the open makes ./cairns, and the append,
index and receipt read it back. A line run alone names a record it did not make.
`

// refuse is what an unusable invocation costs: one line naming what was
// wrong, and the door to the usage rather than the usage itself.
func refuse(stderr io.Writer, where, what string) int {
	fmt.Fprintf(stderr, "nova-cairn%s: %s; run: nova-cairn help\n", oneline.Escape(where), oneline.Escape(what))
	return 2
}

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return refuse(stderr, "", "no verb given; open starts a record")
	}
	switch args[0] {
	case "open":
		return cmdOpen(args[1:], stdout, stderr)
	case "append":
		return cmdAppend(args[1:], stdin, stdout, stderr)
	case "index":
		return cmdIndex(args[1:], stdout, stderr)
	case "receipt":
		return cmdReceipt(args[1:], stdout, stderr)
	case "version", "--version":
		return cmdVersion(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		return refuse(stderr, "", fmt.Sprintf("unknown subcommand %q", args[0]))
	}
}

// parse runs a subcommand flag set and enforces the no-guessing rule: every
// required flag must have been GIVEN. Every missing flag is reported, not
// the first, so one run teaches the whole invocation.
func parse(fs *flag.FlagSet, args []string, stderr io.Writer, required ...string) (given map[string]bool, ok bool) {
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		refuse(stderr, " "+fs.Name(), oneline.Cap(err.Error(), oneline.TailBytes))
		return nil, false
	}
	given = map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	sorted := append([]string(nil), required...)
	sort.Strings(sorted)
	ok = true
	for _, name := range sorted {
		if !given[name] {
			refuse(stderr, " "+fs.Name(), fmt.Sprintf("--%s is required; refusing to guess", name))
			ok = false
		}
	}
	return given, ok
}

// clock resolves the stamp: the real clock in UTC unless --now names one.
// --now exists for tests and replays; a masked, local-format or otherwise
// unparsable time is exit 2, because a stamp that did not come from a clock
// (or an explicit replay of one) is how estimates get filed as facts.
func clock(given map[string]bool, now string, verb string, stderr io.Writer) (time.Time, bool) {
	if !given["now"] {
		return time.Now().UTC(), true
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(now))
	if err != nil {
		refuse(stderr, " "+verb, fmt.Sprintf("--now must parse as RFC 3339 UTC (got %q); refusing to guess", now))
		return time.Time{}, false
	}
	return t.UTC(), true
}

func cmdVersion(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		return refuse(stderr, " version", fmt.Sprintf("takes no flags and no arguments, got %d", len(args)))
	}
	fmt.Fprintln(stdout, buildinfo.Line("nova-cairn", version))
	return 0
}

func cmdOpen(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("open", flag.ContinueOnError)
	store := fs.String("store", "", "checkpoint store directory (required)")
	session := fs.String("session", "", "stable session identifier (required)")
	source := fs.String("source", "", "where the record points back to (optional)")
	publish := fs.String("publish", "", "publication policy: never|manual|deferred|immediate (required)")
	now := fs.String("now", "", "RFC 3339 UTC stamp override for tests and replays")
	given, ok := parse(fs, args, stderr, "store", "session", "publish")
	if given == nil {
		return 2
	}
	bad := !ok
	if fs.NArg() > 0 {
		refuse(stderr, " open", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
		bad = true
	}
	stamp, ok := clock(given, *now, "open", stderr)
	if !ok {
		bad = true
	}
	if bad {
		return 2
	}
	if err := cairn.Open(*store, *session, *source, stamp, *publish); err != nil {
		return refuse(stderr, " open", oneline.Err(err))
	}
	fmt.Fprintf(stdout, "OPEN OK session=%s store=%s publish=%s stamp=%s\n",
		oneline.Field(*session), oneline.Escape(*store), oneline.Field(*publish), stamp.Format(time.RFC3339Nano))
	return 0
}

func cmdAppend(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("append", flag.ContinueOnError)
	store := fs.String("store", "", "checkpoint store directory (required)")
	session := fs.String("session", "", "stable session identifier (required)")
	entry := fs.String("entry", "", "stable entry identifier (required)")
	text := fs.String("text", "", "the friend's exact words")
	file := fs.String("file", "", "file holding the exact words (- reads stdin)")
	source := fs.String("source", "", "where the words came from (optional)")
	publish := fs.String("publish", "", "publication policy: never|manual|deferred|immediate (required)")
	now := fs.String("now", "", "RFC 3339 UTC stamp override for tests and replays")
	given, ok := parse(fs, args, stderr, "store", "session", "entry", "publish")
	if given == nil {
		return 2
	}
	bad := !ok
	if fs.NArg() > 0 {
		refuse(stderr, " append", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
		bad = true
	}
	// The words arrive by exactly one road: two roads is two candidates for
	// "the friend's chosen words", and the tool must not pick between them.
	words := ""
	switch {
	case given["text"] && given["file"]:
		refuse(stderr, " append", "--text and --file both name the words; give exactly one")
		bad = true
	case given["text"]:
		words = *text
	case given["file"]:
		raw, err := readWords(*file, stdin)
		if err != nil {
			refuse(stderr, " append", oneline.Err(err))
			bad = true
		} else {
			words = string(raw)
		}
	default:
		refuse(stderr, " append", "the words come from --text or --file; refusing to guess")
		bad = true
	}
	stamp, ok := clock(given, *now, "append", stderr)
	if !ok {
		bad = true
	}
	if bad {
		return 2
	}
	res, err := cairn.Append(*store, *session, *entry, words, *source, stamp, *publish)
	if err != nil {
		var conflict *cairn.ConflictError
		if errors.As(err, &conflict) {
			fmt.Fprintf(stderr, "APPEND FAIL session=%s entry=%s %s\n",
				oneline.Field(*session), oneline.Field(*entry), oneline.Escape(err.Error()))
			return 1
		}
		return refuse(stderr, " append", oneline.Err(err))
	}
	dup := "false"
	if res.Duplicate {
		dup = "true"
	}
	fmt.Fprintf(stdout, "APPEND OK session=%s entry=%s persisted=true published=false publish=%s duplicate=%s stamp=%s\n",
		oneline.Field(*session), oneline.Field(*entry), oneline.Field(res.Policy), dup, stamp.Format(time.RFC3339Nano))
	return 0
}

// readWords reads the exact words from a file, or from stdin when the path
// is -. The bytes are never trimmed: trimming would file different words
// than the friend chose.
func readWords(name string, stdin io.Reader) ([]byte, error) {
	if name == "-" {
		return io.ReadAll(stdin)
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// indexRemedy is the second half of the index MORE line: the flag that shows
// the rest, written the way it would be typed.
const indexRemedy = "--max <n> raises the ceiling, --max 0 prints every entry"

func cmdIndex(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("index", flag.ContinueOnError)
	store := fs.String("store", "", "checkpoint store directory (required)")
	session := fs.String("session", "", "one session to index (default: every record)")
	max := fs.Int("max", bounded.Default, "entry lines to print before one MORE line stands for the rest; 0 prints all")
	given, ok := parse(fs, args, stderr, "store")
	if given == nil {
		return 2
	}
	bad := !ok
	if fs.NArg() > 0 {
		refuse(stderr, " index", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
		bad = true
	}
	if given["max"] && *max < 0 {
		refuse(stderr, " index", fmt.Sprintf("--max must be zero or more (got %d); 0 means print them all", *max))
		bad = true
	}
	if bad {
		return 2
	}
	all, total, err := cairn.Index(*store, *session, *max)
	if err != nil {
		return refuse(stderr, " index", oneline.Err(err))
	}
	led := cairn.Coverage(*store)
	list := bounded.Capped(stdout, *max, "INDEX", "entry", indexRemedy)
	for _, r := range all {
		list.Line(fmt.Sprintf("INDEX ENTRY session=%s entry=%s stamp=%s bytes=%d source=%s",
			oneline.Field(r.Session), oneline.Field(r.ID),
			r.Stamp.Format(time.RFC3339Nano), r.Bytes, oneline.Escape(r.Source)))
	}
	list.More()
	fmt.Fprintf(stdout, "INDEX COVERAGE sessions=%d entries=%d shown=%d\n", led.Sessions, total, list.Shown())
	return 0
}

func cmdReceipt(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("receipt", flag.ContinueOnError)
	store := fs.String("store", "", "checkpoint store directory (required)")
	session := fs.String("session", "", "stable session identifier (required)")
	entry := fs.String("entry", "", "stable entry identifier (required)")
	given, ok := parse(fs, args, stderr, "store", "session", "entry")
	if given == nil {
		return 2
	}
	bad := !ok
	if fs.NArg() > 0 {
		refuse(stderr, " receipt", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
		bad = true
	}
	if bad {
		return 2
	}
	rc, err := cairn.Receipt(*store, *session, *entry)
	if err != nil {
		return refuse(stderr, " receipt", oneline.Err(err))
	}
	fmt.Fprintf(stdout, "RECEIPT OK session=%s entry=%s stamp=%s bytes=%d source=%s persisted=true published=false publish=%s\n",
		oneline.Field(rc.Session), oneline.Field(rc.ID),
		rc.Stamp.Format(time.RFC3339Nano), rc.Bytes, oneline.Escape(rc.Source), oneline.Field(rc.Policy))
	return 0
}
