package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/mas-bandwidth/nova-tools/internal/worklang"
)

// s8VerbFlags registers the query verb's flags. The query verb is the S8
// slice: "nova-work query friends and escalations over the socket". The
// spec (docs/SPEC-WORK.md, "The verbs") pins the flags and constraints;
// this client sends them as the caller spelled them, because the session
// validates and a thin client never second-guesses the engine's bounds.
//
// `--xy` is registered separately in queryVerb because it does not travel
// over the socket (nova-tools#2595: the offline path reads
// docs/roadmaps/nova-work.sexp through the worklang reader and never reaches
// the session).
func init() {
	verbFlags["query"] = []flagSpec{
		{name: "session"},
		{name: "snapshot"},
		{name: "max-bytes"},
		{name: "max-depth"},
		{name: "max-nodes"},
		{name: "cache"},
		{name: "ask"},
		{name: "branch"},
		{name: "node"},
		{name: "repo"},
		{name: "owner"},
		{name: "category"},
		{name: "axis"},
		{name: "for"},
		{name: "class"},
		{name: "since"},
		{name: "at"},
		{name: "from"},
		{name: "to"},
		{name: "after"},
		{name: "page-budget"},
		{name: "max"},
		{name: "order"},
		{name: "window"},
	}
}

// validAskKinds is the spec's own closed list of --ask values
// (docs/SPEC-WORK.md, the query verb line).
var validAskKinds = map[string]bool{
	"done":      true,
	"remaining": true,
	"who":       true,
	"percent":   true,
	"size":      true,
	"stream":    true,
	"under":     true,
	"stale":     true,
	"handoffs":  true,
	"roadmap":   true,
	"friends":   true,
	"models":    true,
	"ready":     true,
	"fleet":     true,
	"routes":    true,
	// reports stands on the spec's own --ask line and is marked SPEC-AHEAD
	// (nova-tools#854) there. The client carries it because help prints that
	// line verbatim, and a help naming an ask the client itself refuses is
	// worse than a session refusing one it does not answer yet.
	"reports": true,
}

// openOnlyAsks are the asks the spec admits under --branch open and nowhere
// else: "who, stale, handoffs and reports: --branch open only, the other two
// exit 2".
var openOnlyAsks = map[string]bool{
	"who":      true,
	"stale":    true,
	"handoffs": true,
	"reports":  true,
}

func queryVerb(args []string, stdout, stderr io.Writer) int {
	f := flag.NewFlagSet("query", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	strs := map[string]*string{}
	for _, s := range verbFlags["query"] {
		strs[s.name] = f.String(s.name, "", "")
	}
	// --xy is the offline-roadmap path: it reads docs/roadmaps/nova-work.sexp
	// through the worklang reader (nova-tools#2595) and never reaches the
	// session. It is registered on the flag set here rather than in
	// verbFlags["query"] because the spec's verbs block does not name it --
	// the offline reader lives only in the client and is named in the help
	// once the spec catches up.
	xy := f.String("xy", "", "the :by-feature node id to read offline (refuses to guess)")
	if err := f.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return printVerbHelp(stderr, "query")
		}
		return refused(stderr, "query: "+err.Error())
	}
	if f.NArg() != 0 {
		return refused(stderr, fmt.Sprintf("query takes no positional arguments (got %q)", f.Arg(0)))
	}

	// --xy reads docs/roadmaps/nova-work.sexp through the offline reader, not
	// the socket. It is the verb's only path that does not require --session
	// or --snapshot (nova-tools#2595: every nova-work verb that takes a
	// roadmap reads this file through one reader, and the percent the spec
	// asks for is derivable from the file alone).
	if *xy != "" {
		features, err := readRoadmapByFeature(defaultRoadmapSexp)
		if err != nil {
			fmt.Fprintf(stderr, "QUERY FAIL xy=%s: %s\n",
				oneline.Field(*xy), oneline.Escape(err.Error()))
			return 2
		}
		return printRoadmapXY(stdout, features, *xy)
	}

	session := *strs["session"]
	snapshot := *strs["snapshot"]
	if session == "" && snapshot == "" {
		return refused(stderr, "--session or --snapshot is required; refusing to guess")
	}
	if session != "" && snapshot != "" {
		return refused(stderr, "--session and --snapshot are mutually exclusive")
	}

	// Snapshot requires the three bounds and --cache.
	if snapshot != "" {
		if *strs["max-bytes"] == "" {
			return refused(stderr, "--snapshot requires --max-bytes")
		}
		if *strs["max-depth"] == "" {
			return refused(stderr, "--snapshot requires --max-depth")
		}
		if *strs["max-nodes"] == "" {
			return refused(stderr, "--snapshot requires --max-nodes")
		}
		if *strs["cache"] == "" {
			return refused(stderr, "--snapshot requires --cache")
		}
	}

	askKind := *strs["ask"]
	if askKind == "" {
		return refused(stderr, "--ask is required")
	}
	if !validAskKinds[askKind] {
		return refused(stderr, fmt.Sprintf("--ask %q is not one of the known kinds (done, remaining, who, percent, size, stream, under, stale, handoffs, roadmap, friends, models, ready, fleet, routes, reports)", askKind))
	}

	branch := *strs["branch"]
	if branch == "" {
		return refused(stderr, "--branch is required")
	}
	if branch != "open" && branch != "closed" && branch != "root" {
		return refused(stderr, fmt.Sprintf("--branch %q is not one of open, closed, root", branch))
	}

	// Constraint: who, stale, handoffs and reports are --branch open only
	// ("who, stale, handoffs and reports: --branch open only, the other two
	// exit 2").
	if openOnlyAsks[askKind] && branch != "open" {
		return refused(stderr, "--ask "+askKind+" is --branch open only; --branch "+branch+" is refused")
	}

	// Constraint: reports requires --since ("reports: --since <revision>,
	// required (SPEC-AHEAD: #854)").
	if askKind == "reports" && *strs["since"] == "" {
		return refused(stderr, "--ask reports requires --since <revision>")
	}

	// Constraint: --class is the routes ask's own ("routes: --class optional,
	// the card class whose ordered route list the projection emits"), and
	// names nothing on any other ask.
	if *strs["class"] != "" && askKind != "routes" {
		return refused(stderr, "--class is refused on --ask "+askKind+" (only --ask routes admits it)")
	}

	// Constraint: --branch closed and --branch root require --from and --to.
	if (branch == "closed" || branch == "root") && !openOnlyAsks[askKind] {
		if *strs["from"] == "" || *strs["to"] == "" {
			return refused(stderr, "--branch "+branch+" requires --from and --to")
		}
	}

	// Constraint: --from and --to are refused under --branch open.
	if branch == "open" && (*strs["from"] != "" || *strs["to"] != "") {
		return refused(stderr, "--from and --to are refused under --branch open")
	}

	// Constraint: who and stale require --window.
	if (askKind == "who" || askKind == "stale") && *strs["window"] == "" {
		return refused(stderr, "--ask "+askKind+" requires --window")
	}

	// Constraint: --order priority on any ask other than ready is exit 2.
	if *strs["order"] == "priority" && askKind != "ready" {
		return refused(stderr, "--order priority is refused on --ask "+askKind+" (only --ask ready admits it)")
	}

	// Constraint: percent with --axis is required on a matrix and refused on a zero- or one-axis roadmap.
	// The client cannot know the roadmap shape, so it only enforces that --axis is not given for non-percent asks.
	if askKind != "percent" && *strs["axis"] != "" {
		return refused(stderr, "--axis is refused on --ask "+askKind)
	}

	// Constraint: fleet --for with --node on a member that excludes the kind is refused.
	// The client cannot know member exclusions, so it passes both to the session.

	// --snapshot names a published snapshot FILE the offline reader opens under
	// its own three bounds and its own --cache; it is not a session and there
	// is no socket to dial (docs/SPEC-WORK.md:291). The reader answers the one
	// ask it can answer honestly from the file alone -- done under --branch
	// open, empty by construction -- and refuses every other ask towards the
	// resident session, which is the fix for a path that was handed to the
	// socket dialler and sent after a session that was never there
	// (nova-tools#1787).
	if snapshot != "" {
		return querySnapshot(snapshot, strs, askKind, branch, stdout, stderr)
	}

	socket := session

	var b strings.Builder
	fmt.Fprintf(&b, "%s", "query")
	for _, s := range verbFlags["query"] {
		if v := *strs[s.name]; v != "" {
			fmt.Fprintf(&b, " --%s %s", oneline.Field(s.name), oneline.Field(v))
		}
	}
	return ask(socket, b.String(), frameFor("query", verbFlags["query"], strs, nil, nil), askTimeout, stdout, stderr)
}

// querySnapshot is the offline reader of docs/SPEC-WORK.md:291-293: a reader
// who is not the coordinator reads a published snapshot with --snapshot in
// place of --session, read-only, and the answer carries the snapshot's
// revision. There is no socket and no dial here -- the file is the whole
// session -- so the three bounds the caller named govern the snapshot and the
// cache alike (:843-845): a file past one is refused whole, naming the bound
// and the file, never truncated; and the cache's :state-sha256 must be the
// sha of the snapshot's own bytes, the same identity check the engine's reader
// makes (lisp/nova-work/src/state-export.lisp, read-loaded-snapshot).
func querySnapshot(snapshot string, strs map[string]*string, askKind, branch string, stdout, stderr io.Writer) int {
	// done --branch open is the one ask this reader answers, because the spec
	// pins it as empty by construction (SPEC-WORK.md:1791-1793): every :to
	// :done settles in the same envelope, so no done item is left in O, and
	// the honest answer is shown=0 with no rows. Every other ask folds the
	// closed index or the lease log, which only the engine holds, so it goes
	// to the resident session rather than being guessed at from a file.
	if askKind != "done" || branch != "open" {
		return refused(stderr, "query --snapshot answers --ask done --branch open, the one ask that is empty by construction; --ask "+askKind+" --branch "+branch+" needs the resident session: use --session <path>")
	}

	maxBytes, err := snapshotBound(strs, "max-bytes")
	if err != nil {
		return refused(stderr, "query --snapshot: "+err.Error())
	}
	maxDepth, err := snapshotBound(strs, "max-depth")
	if err != nil {
		return refused(stderr, "query --snapshot: "+err.Error())
	}
	maxNodes, err := snapshotBound(strs, "max-nodes")
	if err != nil {
		return refused(stderr, "query --snapshot: "+err.Error())
	}
	limits := worklang.Limits{MaxBytes: maxBytes, MaxDepth: maxDepth, MaxNodes: maxNodes}

	// BYTES first, on both files, before a byte of either is parsed: a file
	// past the bound is refused whole, never truncated to fit.
	data, over, err := readBounded(snapshot, limits.MaxBytes)
	if err != nil {
		return refused(stderr, "query --snapshot: could not read the snapshot at "+snapshot+": "+err.Error())
	}
	if over {
		return refused(stderr, "query --snapshot: the snapshot at "+snapshot+fmt.Sprintf(" is past --max-bytes=%d (more than %d bytes read); refused whole, never truncated", limits.MaxBytes, limits.MaxBytes))
	}
	cachePath := *strs["cache"]
	cacheData, over, err := readBounded(cachePath, limits.MaxBytes)
	if err != nil {
		return refused(stderr, "query --snapshot: could not read the cache at "+cachePath+": "+err.Error())
	}
	if over {
		return refused(stderr, "query --snapshot: the cache at "+cachePath+fmt.Sprintf(" is past --max-bytes=%d (more than %d bytes read); refused whole, never truncated", limits.MaxBytes, limits.MaxBytes))
	}

	// The cache identifies the snapshot: its :state-sha256 names the bytes
	// this run read, so a cache another snapshot wrote cannot vouch for this
	// one, and the reader never answers for bytes it did not check.
	cacheForm, err := worklang.Read(cachePath, cacheData, limits)
	if err != nil {
		return refused(stderr, "query --snapshot: "+err.Error())
	}
	sum := swarm.HashBytes(data)
	if f, ok := formGetf(cacheForm, "state-sha256"); !ok || f.Kind != worklang.String || f.Value != sum {
		return refused(stderr, "query --snapshot: the offline reader refused the snapshot at "+snapshot+": the cache at "+cachePath+" does not match it (its state-sha256 is not the snapshot's "+sum+")")
	}

	snapForm, err := worklang.Read(snapshot, data, limits)
	if err != nil {
		return refused(stderr, "query --snapshot: "+err.Error())
	}
	rev, err := snapshotRevision(snapshot, snapForm)
	if err != nil {
		return refused(stderr, "query --snapshot: "+err.Error())
	}

	// The counters the grammar's every QUERY OK carries, as this reader spent
	// them: two restricted reads (the snapshot, the cache) and no replay -- the
	// revision is read from the history the clip pinned, never recounted.
	fmt.Fprintf(stdout, "QUERY OK ask=done scope=%d branch=open rows=0 shown=0 parses=2 replays=0\n", rev)
	return 0
}

// readBounded opens path and reads at most maxBytes+1 bytes of it, so the
// caller's --max-bytes bounds the allocation itself, not just the check made
// after it: a file (or a stream such as /dev/zero) past the bound is reported
// over=true having consumed only maxBytes+1 bytes, and is refused whole.
func readBounded(path string, maxBytes int) (data []byte, over bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	return readBoundedFrom(f, maxBytes)
}

// readBoundedFrom is readBounded's reader half: it never reads more than
// maxBytes+1 bytes from r.
func readBoundedFrom(r io.Reader, maxBytes int) (data []byte, over bool, err error) {
	data, err = io.ReadAll(io.LimitReader(r, int64(maxBytes)+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) > maxBytes {
		return nil, true, nil
	}
	return data, false, nil
}

// snapshotBound reads one of --snapshot's three bounds. queryVerb required the
// flag above; here it must also be a positive count, because this reader --
// not a session -- is the one that enforces it, and a bound it cannot read is
// a refusal rather than a guess.
func snapshotBound(strs map[string]*string, name string) (int, error) {
	v := *strs[name]
	n := 0
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("--%s %q is not a positive count", name, v)
		}
		n = n*10 + int(c-'0')
		if n > 1000000000 {
			return 0, fmt.Errorf("--%s %q is not a positive count", name, v)
		}
	}
	if n <= 0 {
		return 0, fmt.Errorf("--%s %q is not a positive count", name, v)
	}
	return n, nil
}

// formGetf reads one property from a plist form the way the engine's own getf
// does: the value immediately after the keyword, the first spelling winning.
// A form that is not a list holds no properties at all.
func formGetf(f worklang.Form, key string) (worklang.Form, bool) {
	if f.Kind != worklang.List {
		return worklang.Form{}, false
	}
	for i := 0; i+1 < len(f.List); i += 2 {
		if f.List[i].IsKeyword(key) {
			return f.List[i+1], true
		}
	}
	return worklang.Form{}, false
}

// snapshotRevision reads the revision the snapshot pins, the way the engine's
// replay computes it (lisp/nova-work/src/state.lisp, apply-event): the seed
// starts at revision 0 and every event's :rev raises the revision to its own
// maximum, so the newest :rev in the history IS the revision the answer
// carries. The records are read, never replayed: this holds the engine's own
// pinned value, not a recount of it.
func snapshotRevision(snapshot string, form worklang.Form) (int64, error) {
	if form.Kind != worklang.List {
		return 0, fmt.Errorf("the snapshot at %s is not a (:seed ... :history ...) form: its one form is not a list", snapshot)
	}
	history, ok := formGetf(form, "history")
	if !ok {
		return 0, nil
	}
	if history.Kind != worklang.List {
		return 0, fmt.Errorf("the snapshot at %s holds a :history that is not a list", snapshot)
	}
	var rev int64
	for _, record := range history.List {
		if record.Kind != worklang.List {
			return 0, fmt.Errorf("the snapshot at %s holds a history record that is not a list", snapshot)
		}
		events, ok := formGetf(record, "events")
		if !ok {
			continue
		}
		if events.Kind != worklang.List {
			return 0, fmt.Errorf("the snapshot at %s holds a history record whose :events is not a list", snapshot)
		}
		for _, event := range events.List {
			if event.Kind != worklang.List {
				return 0, fmt.Errorf("the snapshot at %s holds an event that is not a list", snapshot)
			}
			r, ok := formGetf(event, "rev")
			if !ok || r.Kind != worklang.Integer {
				return 0, fmt.Errorf("the snapshot at %s holds an event without an integer :rev", snapshot)
			}
			if r.Int > rev {
				rev = r.Int
			}
		}
	}
	return rev, nil
}
