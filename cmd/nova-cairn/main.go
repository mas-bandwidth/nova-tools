// nova-cairn keeps a session's words as plain files: it opens a session
// record, appends the caller's exact words with a clock stamp, a stable id
// and a source pointer, and reads them back as an index and receipts. It
// stores checkpoints and leaves every lifecycle decision (sealing, deleting,
// grading, consolidating) to the caller; those verbs are refused as unknown.
//
// Every path and identity comes from a flag. There is no default store, no
// environment variable and no discovery: a missing flag is a refusal, never a
// guess. The publication policy is named once, at open, and an append carries
// it. The dispatch, the banner, the help, the version verb, the refusals and
// the output envelope are pkg/tool's.
package main

import (
	"cmp"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cairn"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

var version string

func main() { os.Exit(runCairn(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

// runCairn runs one invocation. `help` with more than one word refuses as help
// before any verb runs its flag checks, so `nova-cairn help open append` is
// one HELP REFUSED naming the single verb name it wants, not a verb dispatch
// carrying a stray positional. main and the in-process tests both go through
// it, so the refusal is exercised by the tests rather than owned by main alone.
func runCairn(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	t := cairnTool()
	if len(args) > 1 && args[0] == "help" && args[1] != "help" &&
		!verbflag.IsHelp(args[1]) && helpNameWords(args[1:]) > 1 {
		o := tool.Refuse("help takes one verb name; the verbs are " + verbflag.List(verbNames(t)))
		o.Verb = "help"
		o.Remedy = t.Name + " help"
		asJSON := verbflag.BoolAsked(args, "json")
		w := stderr
		if asJSON {
			w = stdout
		}
		return o.Render(w, asJSON)
	}
	return t.Run(args, stdin, stdout, stderr)
}

// helpNameWords counts the verb-name words after help: every argument that is
// a word, not a flag (one beginning with -). `help open --json append` and
// `help open -- append` each count two and refuse as help, while `help open
// --json` counts one and forwards to open's own help, so a flag sitting between
// help, the verb and a stray word cannot smuggle the call into the verb.
func helpNameWords(args []string) int {
	words := 0
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			words++
		}
	}
	return words
}

// verbNames returns the tool's verb names, including the implicit version verb
// the framework adds.
func verbNames(t *tool.Tool) []string {
	names := make([]string, 0, len(t.Verbs)+1)
	for _, v := range t.Verbs {
		names = append(names, v.Name)
	}
	names = append(names, "version")
	return names
}

var publishes = strings.Join(cairn.Policies, ", ")

func cairnTool() *tool.Tool {
	return &tool.Tool{
		Name:  "nova-cairn",
		What:  "a session's words, kept durably as plain files you can come back to",
		Stamp: version,
		How: `a store is a directory you name (--store), plain files only, synced to disk before OK.
open starts a session and records its --publish policy; append keeps an entry's exact words.
Same id, same words: duplicate (duplicate=true, exit 0); same id, other words: conflict (exit 1).
--publish records your policy only: nothing is sent, and every line says published=false.
first run: the four examples are one sitting: the open makes ./cairns, the rest read it back.`,
		ExitTable: "0 done, 2 usage or could not run, for every verb; by verb:\n" +
			"  open: 0 the record stands (opened, or already matching); 1 a re-open naming\n" +
			"    another policy or source; 2 usage, or a store that did not answer\n" +
			"  append: 0 the words are written, or the entry already holds them\n" +
			"    (duplicate=true); 1 the entry id holds other words, or --publish names\n" +
			"    another policy than the session holds; 2 usage, or a store that did not answer\n" +
			"  index: 0 listed; 2 usage, or a store that did not answer\n" +
			"  receipt: 0 read; 2 usage, or no such session or entry",
		Verbs: []tool.Verb{
			{
				Name: "open",
				Usage: "open --store <dir> --session <id> [--source <ptr>] --publish <never|manual|deferred|immediate> [--now <rfc3339-utc>] [--dry-run]\n" +
					"NOTE: --publish is a recorded word, nothing more: never, manual, deferred and immediate are the four this tool accepts and it acts on none of them; append with no --publish carries the session's, and one that differs is a conflict naming both (exit 1).",
				Example: "open --store ./cairns --session s1 --publish manual",
				Effect:  tool.LocalWrite,
				Detail: "A re-open naming the recorded policy (and source, when given) changes nothing; one naming\n" +
					"another is a conflict, exit 1, and names the open that matches.",
				DryRun: true,
				Flags: func(f *tool.Flags) {
					record(f)
					f.String("source", "", "where the record points back to; appends with no --source carry it")
					f.Required("publish", "the publication policy: "+publishes)
					now(f)
					checkPublish(f)
				},
				Run: open,
			},
			{
				Name:    "append",
				Usage:   "append --store <dir> --session <id> --entry <id> (--text <words> | --file <path|->) [--source <ptr>] [--publish <policy>] [--now <rfc3339-utc>] [--dry-run]",
				Example: `append --store ./cairns --session s1 --entry e1 --text "the words to keep"`,
				Effect:  tool.LocalWrite,
				Detail: "With no --source or --publish the entry carries the session's, as open recorded them; --publish\n" +
					"that names another policy is a conflict naming both. A flat record (<store>/<session>.md)\n" +
					"records no policy, and says publish=unknown.",
				DryRun: true,
				Flags: func(f *tool.Flags) {
					record(f)
					f.Required("entry", "the stable entry identifier")
					f.String("text", "", "the exact words, stored byte for byte; exactly one of --text or --file")
					f.String("file", "", "file holding the exact words; - reads stdin")
					f.String("source", "", "where the words came from; recorded, never opened (default: the session's)")
					f.String("publish", "", "the publication policy: "+publishes+" (default: the one the session was opened with)")
					now(f)
					checkPublish(f)
					checkID(f, "entry")
					// The words arrive by exactly one road: two roads are two
					// candidates for the words to keep, and the tool must not pick.
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
					checkID(f, "session")
					f.Max()
				},
				Run: index,
			},
			{
				Name:    "receipt",
				Usage:   "receipt --store <dir> --session <id> --entry <id> [--text]",
				Example: "receipt --store ./cairns --session s1 --entry e1 --text",
				Effect:  tool.Inspection,
				Flags: func(f *tool.Flags) {
					record(f)
					f.Required("entry", "the stable entry identifier")
					checkID(f, "entry")
					f.Bool("text", false, "include the entry's stored words as a quoted text fact")
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
	checkID(f, "session")
}

// checkID holds a given id flag to cairn's id rule, so a bad id is named with
// every other problem of the run rather than after them.
func checkID(f *tool.Flags, name string) {
	f.Check(func(c *tool.Call) {
		if v := c.Str(name); v != "" && !cairn.ValidID(v) {
			c.Problem("--" + name + " " + quote(v) + " is not an id: " + cairn.IDRule)
		}
	})
}

// checkPublish holds a given --publish to the policies.
func checkPublish(f *tool.Flags) {
	f.Check(func(c *tool.Call) {
		if v := c.Str("publish"); v != "" && !cairn.ValidPublish(v) {
			c.Problem("--publish " + quote(v) + " is not a policy; it is one of " + publishes)
		}
	})
}

func quote(s string) string { return `"` + s + `"` }

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

// refusal is the result for an error from the store: a conflict ran and said
// no (exit 1); anything else could not run (exit 2). Either names the command
// to run next when the store knows it.
func refusal(err error) *tool.Out {
	var conflict *cairn.ConflictError
	var missing *cairn.NotFoundError
	switch {
	case errors.As(err, &conflict):
		o := tool.Fail(conflict.Msg)
		o.Remedy = conflict.Remedy
		return o
	case errors.As(err, &missing):
		o := tool.Refuse(missing.Msg)
		o.Remedy = missing.Remedy
		return o
	}
	return tool.Refuse(err.Error())
}

func open(c *tool.Call) *tool.Out {
	store, session, publish, stamp := c.Str("store"), c.Str("session"), c.Str("publish"), clock(c)
	// Read DryRun before any return: a verb that skips it is failed as a tool
	// bug (Call.DryRun). A re-open prints the opened time the record already
	// stores, not this call's clock (docs/SPEC-CAIRN.md, the open verb).
	dry := c.DryRun()
	before, err := cairn.ReadOpen(store, session)
	if err != nil {
		return refusal(err)
	}
	var rec cairn.OpenRecord
	if dry {
		rec, err = cairn.PlanOpen(store, session, c.Str("source"), stamp, publish)
	} else if err = cairn.Open(store, session, c.Str("source"), stamp, publish); err == nil {
		rec, err = cairn.ReadOpen(store, session)
	}
	if err != nil {
		return refusal(err)
	}
	shown := stamp
	o := tool.Done().Fact("session", session).Fact("store", store).Fact("source", cmp.Or(rec.Source, "-")).
		Fact("publish", publish)
	if before.Found {
		if !before.Opened.IsZero() {
			shown = before.Opened
		}
		o = o.Fact("reopened", true)
	}
	return o.Fact("stamp", stampOf(shown))
}

func appendEntry(c *tool.Call) *tool.Out {
	store, session, entry := c.Str("store"), c.Str("session"), c.Str("entry")
	words := c.Str("text")
	if c.Given("file") {
		raw, err := readWords(c.Str("file"), c.Stdin)
		if err != nil {
			return tool.Refuse(err.Error())
		}
		words = string(raw)
	}
	write := cairn.Append
	if c.DryRun() {
		write = cairn.PlanAppend
	}
	res, err := write(store, session, entry, words, c.Str("source"), clock(c), c.Str("publish"))
	if err != nil {
		o := refusal(err)
		if o.Status == tool.Failed { // a conflict names what it is about
			o.Fact("session", session).Fact("entry", entry)
		}
		return o
	}
	return tool.Done().Fact("session", session).Fact("entry", entry).Fact("source", cmp.Or(res.Source, "-")).
		Fact("persisted", res.Persisted).Fact("published", false).Fact("publish", res.Policy).
		Fact("duplicate", res.Duplicate).Fact("stamp", stampOf(res.Stamp))
}

// readWords reads the exact words from a file, or from stdin when the path
// is -. The bytes are never trimmed: trimming would file other words than
// the caller chose.
func readWords(name string, stdin io.Reader) ([]byte, error) {
	if name == "-" {
		return io.ReadAll(stdin)
	}
	return os.ReadFile(name)
}

// index lists every entry; the coverage counts on its first line are the
// selection's, so --session counts one session and the full index counts all.
// An INDEX SESSION line is printed for every session in the selection, before
// the entries, entries or none, so an empty session is found. The line names
// the id, the publish policy and the opened stamp the open record stores
// (unknown and - when it stores none, a flat file), and --max bounds the
// lines with MORE (docs/SPEC-CAIRN.md, the index verb).
func index(c *tool.Call) *tool.Out {
	store := c.Str("store")
	session := c.Str("session")
	all, names, total, err := cairn.Index(store, session)
	if err != nil {
		return refusal(err)
	}
	perSession := map[string]int{}
	for _, r := range all {
		perSession[r.Session]++
	}
	o := tool.Done()
	for _, s := range names {
		publish, opened, err := sessionFacts(store, s)
		if err != nil {
			return refusal(err)
		}
		o.Item("session", "session", s, "publish", publish, "opened", opened, "entries", perSession[s])
	}
	o.Fact("sessions", len(names)).Fact("entries", total)
	for _, r := range all {
		o.Item("entry", "session", r.Session, "entry", r.ID, "stamp", stampOf(r.Stamp), "bytes", r.Bytes, "source", cmp.Or(r.Source, "-"))
	}
	return o
}

// sessionFacts is the policy and opened stamp index prints for one session.
// A flat record, or an open that never reached the log, stores neither:
// publish=unknown and opened=- (docs/SPEC-CAIRN.md, the index verb).
func sessionFacts(store, session string) (publish, opened string, err error) {
	rec, err := cairn.ReadOpen(store, session)
	if err != nil {
		return "", "", err
	}
	publish, opened = cairn.PublishUnknown, "-"
	if !rec.Found || !cairn.ValidPublish(rec.Publish) {
		return publish, opened, nil
	}
	if !rec.Opened.IsZero() {
		opened = stampOf(rec.Opened)
	}
	return rec.Publish, opened, nil
}

func receipt(c *tool.Call) *tool.Out {
	rc, err := cairn.Receipt(c.Str("store"), c.Str("session"), c.Str("entry"))
	if err != nil {
		return refusal(err)
	}
	o := tool.Done().Fact("session", rc.Session).Fact("entry", rc.ID).Fact("stamp", stampOf(rc.Stamp)).
		Fact("bytes", rc.Bytes).Fact("source", cmp.Or(rc.Source, "-")).Fact("persisted", true).
		Fact("published", false).Fact("publish", rc.Policy)
	if c.Bool("text") {
		text, err := cairn.EntryText(c.Str("store"), c.Str("session"), c.Str("entry"))
		if err != nil {
			return refusal(err)
		}
		// Text: quoted with its spaces kept, never hex-escaped; JSON carries it as a string.
		o.Fact("text", tool.Text(text))
	}
	return o
}
