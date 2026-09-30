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
		How: `The store is plain files under --store, fsync-durable before success; no Redis, remote or discovery.
It reads its own layout (sessions/, entries/, log.jsonl) or a hand-kept markdown file per session.
The same entry id with the same words is a duplicate, and with different words a conflict (exit 1).
--now stamps a replay (RFC 3339 UTC). There is no seal, consume, delete or grade verb (SPEC-CAIRN).
The four examples are one sitting: the open makes ./cairns and the rest read it back.`,
		ExitTable: "0 ran and passed, 1 ran and failed (conflict), 2 could not run (bad invocation).",
		Verbs: []tool.Verb{
			{
				Name:    "open",
				Usage:   "open --store <dir> --session <id> [--source <ptr>] --publish <never|manual|deferred|immediate> [--now <rfc3339-utc>]",
				Example: "open --store ./cairns --session s1 --publish manual",
				Effect:  tool.LocalWrite,
				Flags: func(f *tool.Flags) {
					record(f)
					f.String("source", "", "where the record points back to; appends with no --source carry it")
					f.Required("publish", "the publication policy: "+publishes)
					now(f)
				},
				Run: open,
			},
			{
				Name:    "append",
				Usage:   "append --store <dir> --session <id> --entry <id> (--text <words> | --file <path|->) [--source <ptr>] --publish <never|manual|deferred|immediate> [--now <rfc3339-utc>]",
				Example: `append --store ./cairns --session s1 --entry e1 --text "the words to keep" --publish manual`,
				Effect:  tool.LocalWrite,
				Flags: func(f *tool.Flags) {
					record(f)
					f.Required("entry", "the stable entry identifier")
					f.String("text", "", "the friend's exact words, stored byte for byte; exactly one of --text or --file")
					f.String("file", "", "file holding the exact words; - reads stdin")
					f.String("source", "", "where the words came from; recorded, never opened")
					f.Required("publish", "the publication policy: "+publishes)
					now(f)
					// The words arrive by exactly one road: two roads is two candidates
					// for "the friend's chosen words", and the tool must not pick.
					f.Check(func(c *tool.Call) {
						switch {
						case c.Given("text") && c.Given("file"):
							c.Problem("--text and --file both name the words; give exactly one")
						case !c.Given("text") && !c.Given("file"):
							c.Problem("the words come from --text or --file; refusing to guess")
						}
					})
				},
				Run: appendEntry,
			},
			{
				Name:    "index",
				Usage:   "index --store <dir> [--session <id>] [--max <n>]",
				Example: "index --store ./cairns",
				Effect:  tool.Inspection,
				Flags: func(f *tool.Flags) {
					f.Required("store", "the checkpoint store directory")
					f.String("session", "", "one session to index; default every record")
					f.Max()
				},
				Run: index,
			},
			{
				Name:    "receipt",
				Usage:   "receipt --store <dir> --session <id> --entry <id>",
				Example: "receipt --store ./cairns --session s1 --entry e1",
				Effect:  tool.Inspection,
				Flags: func(f *tool.Flags) {
					record(f)
					f.Required("entry", "the stable entry identifier")
				},
				Run: receipt,
			},
		},
	}
}

// record declares the two flags every verb that names a record takes.
func record(f *tool.Flags) {
	f.Required("store", "the checkpoint store directory")
	f.Required("session", "the stable session identifier")
}

// now declares --now and its rule: a masked, local-format or otherwise
// unparsable time is a refusal, because a stamp that did not come from a clock
// (or an explicit replay of one) is how estimates get filed as facts.
func now(f *tool.Flags) {
	f.String("now", "", "RFC 3339 UTC stamp for tests and replays; default the real clock")
	f.Check(func(c *tool.Call) {
		if _, err := time.Parse(time.RFC3339, strings.TrimSpace(c.Str("now"))); c.Given("now") && err != nil {
			c.Problem("--now must parse as RFC 3339 UTC (got " + c.Str("now") + "); refusing to guess")
		}
	})
}

// clock is the stamp: the real clock in UTC unless --now (checked) names one.
func clock(c *tool.Call) time.Time {
	if !c.Given("now") {
		return time.Now().UTC()
	}
	t, _ := time.Parse(time.RFC3339, strings.TrimSpace(c.Str("now")))
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
	store, session, publish, stamp := c.Str("store"), c.Str("session"), c.Str("publish"), clock(c)
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
	store, session, entry, publish := c.Str("store"), c.Str("session"), c.Str("entry"), c.Str("publish")
	words := c.Str("text")
	if c.Given("file") {
		raw, err := readWords(c.Str("file"), c.Stdin)
		if err != nil {
			return tool.Refuse(err.Error())
		}
		words = string(raw)
	}
	stamp := clock(c)
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
	store := c.Str("store")
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
	rc, err := cairn.Receipt(c.Str("store"), c.Str("session"), c.Str("entry"))
	if err != nil {
		return tool.Refuse(err.Error())
	}
	return tool.Done().Fact("session", rc.Session).Fact("entry", rc.ID).Fact("stamp", stampOf(rc.Stamp)).
		Fact("bytes", rc.Bytes).Fact("source", sourceOf(rc.Source)).Fact("persisted", true).
		Fact("published", false).Fact("publish", rc.Policy)
}
