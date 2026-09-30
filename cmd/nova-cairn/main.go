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
// separate explicit choices and are refused here as unknown verbs, so no
// friend's practice is renamed by adopting this tool.
//
// Every path, every identity and every policy comes from a flag. There is no
// default store, no environment variable and no discovery: a missing flag is
// a refusal, never a guess. The dispatch, the banner, the help, the version
// verb, the refusals and the output envelope are internal/tool's.
package main

import (
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cairn"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

var version string

func main() { os.Exit(cairnTool().Main()) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return cairnTool().Run(args, stdin, stdout, stderr)
}

const publishes = "never, manual, deferred or immediate"

func cairnTool() *tool.Tool {
	return &tool.Tool{
		Name:  "nova-cairn",
		What:  "optional checkpoints, no memory lifecycle (see docs/SPEC-CAIRN.md)",
		Stamp: version,
		How: `The store is plain files named by --store; there is no environment variable and
no discovery. A note is fsync-durable before success is reported, independently
of Redis and of any remote. Two shapes are read: this tool's own
(sessions/<id>.md, entries/, log.jsonl) and one markdown file per session
directly under the store (<id>.md), appended by hand, where append lands a dated
"## <stamp> - <entry>" section at the end of the file. Retrying an append with
the same entry id and the same words succeeds as a duplicate; the same id with
different words is a conflict. --now stamps a replay (RFC 3339 UTC); the real
clock in UTC is the default. There is deliberately no seal, consume, delete,
grade, consolidate or wake verb (SPEC-CAIRN.md's boundary). The four examples
are one sitting, in order: the open makes ./cairns and the rest read it back.`,
		ExitTable: "0 ran and passed, 1 ran and failed (conflict), 2 could not run (bad invocation).",
		Verbs: []tool.Verb{
			{
				Name:    "open",
				Usage:   "open --store <dir> --session <id> [--source <ptr>] --publish <never|manual|deferred|immediate> [--now <rfc3339-utc>]",
				Example: "open --store ./cairns --session s1 --publish manual",
				Flags: func(f *tool.Flags) {
					record(f)
					f.String("source", "", "where the record points back to; appends with no --source carry it")
					f.String("publish", "", "publication policy: "+publishes+" (required)")
					f.String("now", "", "RFC 3339 UTC stamp for tests and replays; default the real clock")
				},
				Run: open,
			},
			{
				Name:    "append",
				Usage:   "append --store <dir> --session <id> --entry <id> (--text <words> | --file <path|->) [--source <ptr>] --publish <never|manual|deferred|immediate> [--now <rfc3339-utc>]",
				Example: `append --store ./cairns --session s1 --entry e1 --text "the words to keep" --publish manual`,
				Flags: func(f *tool.Flags) {
					record(f)
					f.String("entry", "", "stable entry identifier (required)")
					f.String("text", "", "the friend's exact words, stored byte for byte; exactly one of --text or --file")
					f.String("file", "", "file holding the exact words; - reads stdin")
					f.String("source", "", "where the words came from; recorded, never opened")
					f.String("publish", "", "publication policy: "+publishes+" (required)")
					f.String("now", "", "RFC 3339 UTC stamp for tests and replays; default the real clock")
				},
				Run: appendEntry,
			},
			{
				Name:    "index",
				Usage:   "index --store <dir> [--session <id>] [--max <n>]",
				Example: "index --store ./cairns",
				Flags: func(f *tool.Flags) {
					f.String("store", "", "checkpoint store directory (required)")
					f.String("session", "", "one session to index; default every record")
					f.Max()
				},
				Run: index,
			},
			{
				Name:    "receipt",
				Usage:   "receipt --store <dir> --session <id> --entry <id>",
				Example: "receipt --store ./cairns --session s1 --entry e1",
				Flags: func(f *tool.Flags) {
					record(f)
					f.String("entry", "", "stable entry identifier (required)")
				},
				Run: receipt,
			},
		},
	}
}

// record declares the two flags every verb that names a record takes.
func record(f *tool.Flags) {
	f.String("store", "", "checkpoint store directory (required)")
	f.String("session", "", "stable session identifier (required)")
}

// clock resolves the stamp: the real clock in UTC unless --now names one.
// --now exists for tests and replays; a masked, local-format or otherwise
// unparsable time is a refusal, because a stamp that did not come from a clock
// (or an explicit replay of one) is how estimates get filed as facts.
func clock(c *tool.Call) time.Time {
	if !c.Given("now") {
		return time.Now().UTC()
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(c.Str("now")))
	if err != nil {
		c.Problem("--now must parse as RFC 3339 UTC (got " + c.Str("now") + "); refusing to guess")
	}
	return t.UTC()
}

func stampOf(t time.Time) string { return t.Format(time.RFC3339Nano) }

// sourceOf is a source pointer as a field: "" reads as absent, "-".
func sourceOf(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func open(c *tool.Call) *tool.Out {
	store := c.Want("store", "the checkpoint store directory")
	session := c.Want("session", "the stable session identifier")
	publish := c.Want("publish", "the publication policy: "+publishes)
	stamp := clock(c)
	if o := c.Refused(); o != nil {
		return o
	}
	if err := cairn.Open(store, session, c.Str("source"), stamp, publish); err != nil {
		return tool.Refuse(err.Error())
	}
	stored, err := cairn.SessionSource(store, session)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	return tool.Done().Fact("session", session).Fact("store", store).Fact("source", sourceOf(stored)).
		Fact("publish", publish).Fact("stamp", stampOf(stamp))
}

func appendEntry(c *tool.Call) *tool.Out {
	store := c.Want("store", "the checkpoint store directory")
	session := c.Want("session", "the stable session identifier")
	entry := c.Want("entry", "the stable entry identifier")
	publish := c.Want("publish", "the publication policy: "+publishes)
	// The words arrive by exactly one road: two roads is two candidates for
	// "the friend's chosen words", and the tool must not pick between them.
	words := ""
	switch {
	case c.Given("text") && c.Given("file"):
		c.Problem("--text and --file both name the words; give exactly one")
	case c.Given("text"):
		words = c.Str("text")
	case c.Given("file"):
		raw, err := readWords(c.Str("file"), c.Stdin)
		if err != nil {
			c.Problem(err.Error())
		}
		words = string(raw)
	default:
		c.Problem("the words come from --text or --file; refusing to guess")
	}
	stamp := clock(c)
	if o := c.Refused(); o != nil {
		return o
	}
	res, err := cairn.Append(store, session, entry, words, c.Str("source"), stamp, publish)
	var conflict *cairn.ConflictError
	switch {
	case errors.As(err, &conflict):
		return tool.Fail(err.Error()).Fact("session", session).Fact("entry", entry)
	case err != nil:
		return tool.Refuse(err.Error())
	}
	return tool.Done().Fact("session", session).Fact("entry", entry).Fact("source", sourceOf(res.Source)).
		Fact("persisted", true).Fact("published", false).Fact("publish", res.Policy).
		Fact("duplicate", res.Duplicate).Fact("stamp", stampOf(res.Stamp))
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

// index lists every entry; the coverage counts on its first line are never
// capped, so the total is carried whether or not --max elides entries.
func index(c *tool.Call) *tool.Out {
	store := c.Want("store", "the checkpoint store directory")
	if o := c.Refused(); o != nil {
		return o
	}
	all, total, err := cairn.Index(store, c.Str("session"), 0)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	o := tool.Done().Fact("sessions", cairn.Coverage(store).Sessions).Fact("entries", total)
	for _, r := range all {
		o.Item("entry", "session", r.Session, "entry", r.ID, "stamp", stampOf(r.Stamp), "bytes", r.Bytes, "source", sourceOf(r.Source))
	}
	return o
}

func receipt(c *tool.Call) *tool.Out {
	store := c.Want("store", "the checkpoint store directory")
	session := c.Want("session", "the stable session identifier")
	entry := c.Want("entry", "the stable entry identifier")
	if o := c.Refused(); o != nil {
		return o
	}
	rc, err := cairn.Receipt(store, session, entry)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	return tool.Done().Fact("session", rc.Session).Fact("entry", rc.ID).Fact("stamp", stampOf(rc.Stamp)).
		Fact("bytes", rc.Bytes).Fact("source", sourceOf(rc.Source)).Fact("persisted", true).
		Fact("published", false).Fact("publish", rc.Policy)
}
