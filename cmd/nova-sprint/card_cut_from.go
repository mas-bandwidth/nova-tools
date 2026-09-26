// card cut --from (nova-tools#4340): many cards from one file, one receipt
// per row.
//
//	nova-sprint card cut --from <cards.tsv|-> --repo <owner/name> [--stream <s>] [--sprint <S>]
//	    [--base dev] [--base-sha <sha40>] [--actor <a>] [--redis <addr>] [--dry-run] [--no-github]
//
// THE HURT (2026-09-26). Issues were filed in batches by a python script
// over `gh issue create`, then each became a card by `task push --actor`,
// once per card at 400 ms each and in two passes so a card's dependency was
// pushed before it: 32 cards took minutes of hand steps.
//
// THE VERB. Each row of the file is one card: title, stream, who, paths,
// done-when, body, depends-on, route, est (and an optional id), tab
// separated, in that order or in the order a header row names. Every row is
// read and checked before anything is written: a bad row is named with its
// line and why, and then nothing is filed or pushed (a rerun after the fix
// files no duplicate). Then, in dependency order (a row whose DEPENDS-ON
// names another row's id comes after that row), each row's issue is filed
// through the one GitHub writer (nova-sprint file's REST create and
// read-back) and the cards are pushed as task cards (taskcard.PushMany: one
// ns_tcard_push per card, all in one pipeline) onto their stream's waiting
// set. One receipt line per row, every refusal printed, and one summary
// line:
//
//	CARD CUT row=<n> id=<id> ref=<owner/name#n|-> stream=<s> to=waiting|already depends=<ids|none>
//	CARD CUT REFUSED row=<n> line=<l> id=<id|-> why=<why>
//	CARD CUT DRY row=<n> id=<id|-> stream=<s> who=<w> route=<r> est=<e> depends=<d> title=<t>
//	CARD CUT FROM file=<f> rows=<n> cut=<k> already=<a> refused=<r> filed=<f> reused=<u> github=on|off ms=<ms>
//
// THE LEDGER (the cold read of #4358). Each issue filed is written to
// cut:<sha256 of the file> (row -> issue, taskcard.WriteCutLedger) before
// the next is filed, and the ledger is read before the first filing, so a
// rerun of the same file after a partial or a full filing files nothing
// twice: a row the ledger holds takes its issue from there (reused=), and
// its card, when an earlier run pushed it, is to=already (#4352 N: running
// a verb twice is not a refusal).
//
// A cell writes a newline as \n, a tab as \t and a backslash as \\. A row's
// id is <repo name>-<issue n> (the card cut label), or with --no-github a
// slug of its title; an id cell names it. DEPENDS-ON entries are #<n> (an
// issue of --repo), owner/name#n, or a task id (task:<id> or <id>); none or
// - is none. A task id that is another row's id cell is that row: the issue
// carries that row's issue ref, the card's blocked_on its task id (which the
// waiting-resolve duty reads). A row that is depended on must have an id
// cell (#3409: one DEPENDS-ON form, so row:<n> is refused, naming the id
// column).
//
// Exit 0 every row cut or already, 1 a row refused (named), 2 usage.
//
// THE HIERARCHY (nova-tools#4317). `card cut --parent <id> --from
// <children.tsv>` cuts the rows as the CHILDREN of an existing card and one
// STITCH card behind them, in the one call: every child rides the parent's
// stream (a row naming another stream is refused) and carries parent=<id>
// phase=child; the stitch, <id>-stitch, DEPENDS-ON every child (the one
// edge form, a task id per entry), carries the parent's DONE-WHEN as its
// own, the union of the children's PATHS, ROUTE --stitch-route (frontier:
// advertised, never a name) and a body whose generated section
// (taskcard.StitchBrief) is every child's PR, RESULT.md summary and read
// score, rewritten when the resolver releases the stitch. Then the parent
// is bound (taskcard.BindPlan): kind plan, children, stitch, and DEPENDS-ON
// the stitch, so the parent's state is derived and it lands when the stitch
// lands. A parent that already has a stitch still in waiting takes more
// children (the stitch's edges grow); one whose stitch moved on is refused.
//
//	CARD CUT PLAN parent=<id> children=<n> stitch=<id> parent_to=waiting depends=<stitch>
package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/file"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
)

// cutColumns are a row's cells in the default order; id is optional.
var cutColumns = []string{"title", "stream", "who", "paths", "done-when", "body", "depends-on", "route", "est"}

// cutFromOpts are card cut --from's inputs; Text is the file's bytes.
type cutFromOpts struct {
	From                                       string
	Text                                       []byte
	Repo, Stream, Sprint, Base, BaseSHA, Actor string
	DryRun, NoGitHub                           bool
	// Join names an open stream a row's PATHS may overlap: the row is cut
	// onto it instead of its own stream (#4322).
	Join string
	// Parent makes the rows a plan's children (#4317); StitchRoute and
	// StitchEst are the stitch card's ROUTE (frontier) and EST (60).
	Parent, StitchRoute, StitchEst string
}

// planFacts is what card cut --parent reads of the parent before any write:
// its record and, when it already has a stitch, where that stitch is.
type planFacts struct {
	Rec                    map[string]string
	StitchWhere, StitchRef string // the stitch's where and issue ref, when the parent has one
	// Children is the parent's children field as a set: a row whose id is
	// one of them is a rerun's, to=already, never a refusal.
	Children map[string]bool
}

// cutFromDeps are the verb's seams: the one GitHub writer, the one-pipeline
// push, the cut ledger, the mirror's tip and the clock. Tests pass fakes;
// nothing here reaches GitHub or Redis on its own.
type cutFromDeps struct {
	File        func(ctx context.Context, repo, title, body string) (int, string, error)
	Push        func(ctx context.Context, reqs []taskcard.PushRequest) ([]taskcard.PushOutcome, error)
	LedgerRead  func(ctx context.Context, key string) (taskcard.CutLedger, error)
	LedgerWrite func(ctx context.Context, key, repo string, row, issue int) error
	BaseSHA     func(repo, base string) (string, error)
	Now         func() time.Time
	// StreamPaths reads what the paths gate reads (ws.ReadGateView); nil (a
	// dry run with no --redis) gates nothing. The push's own FCALL gates
	// each row again, atomically (SP.gate, #4322).
	StreamPaths func(ctx context.Context) (ws.GateView, error)
	// Plan reads the parent (--parent); Bind makes it a plan after the push.
	Plan func(ctx context.Context, id string) (planFacts, error)
	Bind func(ctx context.Context, parent string, children []string, stitch, by string) (taskcard.Result, error)
}

// cutRow is one card row.
type cutRow struct {
	n, line                                   int
	title, stream, who, paths, doneWhen, body string
	route, est, id                            string
	deps                                      []string // entries as written, none dropped
	depRow                                    []int    // per entry: the row its id names, 0 outside the file
	rowDeps                                   []int    // the rows this row depends on
	why                                       string   // a refusal
	ref, origin                               string   // the filed issue
	fromLedger, already                       bool     // the issue came from the ledger; the card was pushed before
	stitch                                    bool     // the plan's stitch row (#4317): its edges are set by planRows
	fields                                    []string // more record fields: parent, phase
}

var (
	cutIDRE    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	cutRowRE   = regexp.MustCompile(`^row:([0-9]+)$`)
	cutIssueRE = regexp.MustCompile(`^#([1-9][0-9]*)$`)
	cutRefRE   = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*/)?[A-Za-z0-9][A-Za-z0-9._-]*#[1-9][0-9]*$`)
	cutEstRE   = regexp.MustCompile(`^[1-9][0-9]*(\s*(m|min|mins|minutes|h|hr|hrs|hours))?$`)
	cutSlugRE  = regexp.MustCompile(`[^a-z0-9]+`)
	cutSHARE   = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// cutHeader reads a header row: every cell a column name (done_when and
// DONE-WHEN spell done-when), title among them. ok is false for a card row.
func cutHeader(cells []string) (cols []string, ok bool, err error) {
	known := map[string]bool{"id": true}
	for _, c := range cutColumns {
		known[c] = true
	}
	seen := map[string]bool{}
	for _, c := range cells {
		name := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(c)), "_", "-")
		if !known[name] {
			return nil, false, nil
		}
		if seen[name] {
			return nil, false, fmt.Errorf("the header names %s twice", name)
		}
		seen[name] = true
		cols = append(cols, name)
	}
	if !seen["title"] {
		return nil, false, fmt.Errorf("the header names no title column")
	}
	return cols, true, nil
}

// cutUnescape reads a cell: \n a newline, \t a tab, \\ a backslash.
func cutUnescape(s string) string {
	if !strings.Contains(s, `\`) {
		return strings.TrimSpace(s)
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				b.WriteByte('\n')
				i++
				continue
			case 't':
				b.WriteByte('\t')
				i++
				continue
			case '\\':
				b.WriteByte('\\')
				i++
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return strings.TrimSpace(b.String())
}

// parseCutRows reads the file into rows; the error is the whole file's.
func parseCutRows(text []byte) ([]*cutRow, error) {
	cols := cutColumns
	var rows []*cutRow
	sc := bufio.NewScanner(bytes.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	line, first := 0, true
	for sc.Scan() {
		line++
		raw := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(raw) == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		cells := strings.Split(raw, "\t")
		if first {
			first = false
			h, ok, err := cutHeader(cells)
			if err != nil {
				return nil, fmt.Errorf("line %d: %v", line, err)
			}
			if ok {
				cols = h
				continue
			}
		}
		r := &cutRow{n: len(rows) + 1, line: line}
		if len(cells) > len(cols) {
			r.why = fmt.Sprintf("%d cells where the columns are %d (%s); a tab inside a cell is written \\t", len(cells), len(cols), strings.Join(cols, ","))
		}
		get := map[string]string{}
		for i, c := range cells {
			if i < len(cols) {
				get[cols[i]] = cutUnescape(c)
			}
		}
		r.title, r.stream, r.who, r.paths = get["title"], get["stream"], get["who"], get["paths"]
		r.doneWhen, r.body, r.route, r.est, r.id = get["done-when"], get["body"], strings.ToLower(get["route"]), get["est"], get["id"]
		r.deps = strings.FieldsFunc(get["depends-on"], func(c rune) bool {
			return c == ',' || c == ';' || c == ' ' || c == '\t' || c == '\n'
		})
		if len(r.deps) == 1 && (r.deps[0] == "none" || r.deps[0] == "-") {
			r.deps = nil
		}
		rows = append(rows, r)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("line %d: %v", line+1, err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no card rows")
	}
	return rows, nil
}

// cutWhoOK is WHO's grammar as the deal reads it (TM.admits): any, only
// <names> or except <names>.
func cutWhoOK(who string) bool {
	f := strings.Fields(who)
	if len(f) == 0 {
		return false
	}
	switch f[0] {
	case "any":
		return len(f) == 1
	case "only", "except":
		return len(f) > 1
	}
	return false
}

// cutSlug is a --no-github row's id from its title.
func cutSlug(title string) string {
	s := strings.Trim(cutSlugRE.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if len(s) > 48 {
		s = strings.TrimRight(s[:48], "-")
	}
	return s
}

// checkCutRow fills a row's defaults and names what is wrong with it. byID
// is the file's id cells (id -> row); slugs, with --no-github, the title
// slugs of the rows without one.
func checkCutRow(r *cutRow, byID, slugs map[string]int, o cutFromOpts) {
	fail := func(why string) {
		if r.why == "" {
			r.why = why
		}
	}
	if r.stream == "" {
		r.stream = strings.TrimSpace(o.Stream)
	}
	if r.who == "" {
		r.who = "any"
	}
	if r.route == "" {
		r.route = taskcard.RouteFriend
	}
	if r.est == "" {
		r.est = "30"
	}
	switch {
	case r.title == "":
		fail("no title")
	case strings.ContainsAny(r.title, "\n\t"):
		fail("the title is one line")
	case strings.HasPrefix(r.title, "@") || strings.HasPrefix(r.body, "@"):
		fail("a cell starts with @ (a literal @path, not its text)")
	case r.stream == "":
		fail("no stream (a stream cell, or --stream)")
	case len(r.stream) > 64 || strings.ContainsAny(r.stream, "|\n\t"):
		fail(fmt.Sprintf("stream %q is not a stream name (64 bytes at most, no |)", r.stream))
	case !cutWhoOK(r.who):
		fail(fmt.Sprintf("who %q is not any, only <names> or except <names>", r.who))
	case r.paths == "":
		fail("no paths")
	case r.doneWhen == "":
		fail("no done-when")
	case r.route != taskcard.RouteFriend && !cardhdr.IsRoute(r.route):
		fail(fmt.Sprintf("route %q is not %s, or friend", r.route, cardhdr.RouteList))
	case !cutEstRE.MatchString(r.est):
		fail(fmt.Sprintf("est %q is not minutes (30, 45 min, 2 h)", r.est))
	case r.id != "" && (!cutIDRE.MatchString(r.id) || taskcard.IsCopy(r.id)):
		fail(fmt.Sprintf("id %q is not a task id ([A-Za-z0-9._-], no ~<n>)", r.id))
	}
	if r.stitch {
		if r.why == "" && r.route != taskcard.RouteFriend {
			s := cutSpec(r, o, "")
			if missing := s.Complete("", ""); len(missing) > 0 {
				fail("the stitch (route " + r.route + ") lacks " + strings.Join(missing, ", ") + "; pass --base-sha, or --stitch-route friend")
			}
		}
		return
	}
	r.depRow = make([]int, len(r.deps))
	for i, d := range r.deps {
		if m := cutRowRE.FindStringSubmatch(d); m != nil {
			fail(fmt.Sprintf("depends-on %s is refused (#3409: one DEPENDS-ON form); add an id column (a header row naming id), give row %s an id and name that id", d, m[1]))
			continue
		}
		if cutIssueRE.MatchString(d) || cutRefRE.MatchString(d) {
			continue
		}
		id := strings.TrimPrefix(d, "task:")
		if !cutIDRE.MatchString(id) {
			fail(fmt.Sprintf("depends-on %q is not #<n>, owner/name#<n> or a task id (another row's id cell names that row)", d))
			continue
		}
		if k, ok := byID[id]; ok {
			if k == r.n {
				fail(fmt.Sprintf("depends-on %s names the row itself", d))
			} else {
				r.depRow[i] = k
				r.rowDeps = append(r.rowDeps, k)
			}
			continue
		}
		if k, ok := slugs[id]; ok && k != r.n {
			fail(fmt.Sprintf("depends-on %s is row %d's title, and a row that is depended on needs an id; add an id column (a header row naming id) and give row %d an id", d, k, k))
		}
	}
	if r.id == "" && o.NoGitHub {
		if r.id = cutSlug(r.title); r.id == "" {
			fail("no id: the title has no letter or digit for one; add an id cell")
		}
	}
	if r.why == "" && r.route != taskcard.RouteFriend {
		s := cutSpec(r, o, "")
		if missing := s.Complete("", ""); len(missing) > 0 {
			fail("a " + r.route + " card lacks " + strings.Join(missing, ", "))
		}
	}
}

// orderCutRows is the push order: file order, except that a row comes
// after every row its DEPENDS-ON names. Rows on a cycle are refused.
func orderCutRows(rows []*cutRow) []*cutRow {
	placed := make([]bool, len(rows)+1)
	var order []*cutRow
	for len(order) < len(rows) {
		moved := false
		for _, r := range rows {
			if placed[r.n] {
				continue
			}
			ready := true
			for _, k := range r.rowDeps {
				if !placed[k] {
					ready = false
					break
				}
			}
			if ready {
				placed[r.n] = true
				order = append(order, r)
				moved = true
				break // the earliest ready row first, then look again from the top
			}
		}
		if !moved {
			var stuck []string
			for _, r := range rows {
				if !placed[r.n] {
					stuck = append(stuck, strconv.Itoa(r.n))
				}
			}
			for _, r := range rows {
				if !placed[r.n] {
					placed[r.n] = true
					if r.why == "" {
						r.why = "depends-on is a cycle among rows " + strings.Join(stuck, ",")
					}
					order = append(order, r)
				}
			}
		}
	}
	return order
}

// cutDepends renders a row's DEPENDS-ON: issue is true for the issue text
// (another row's id as that row's ref), false for the card's blocked_on
// (that row's task id). "none" when it has none.
func cutDepends(r *cutRow, rows []*cutRow, repo string, issue bool) string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for i, d := range r.deps {
		if k := r.depRow[i]; k > 0 {
			dep := rows[k-1]
			if issue && dep.ref != "" {
				add(dep.ref)
			} else {
				add(dep.id)
			}
			continue
		}
		switch {
		case cutIssueRE.MatchString(d):
			add(repo + d)
		default:
			add(d)
		}
	}
	if len(out) == 0 {
		return "none"
	}
	return strings.Join(out, ",")
}

// cutIssueText is the issue a row files and the body its card carries: the
// card lines first (so a cell wins over a like-named line in the body), then
// the body.
func cutIssueText(r *cutRow, rows []*cutRow, o cutFromOpts) string {
	var b strings.Builder
	fmt.Fprintf(&b, "STREAM: %s\nWHO: %s\nROUTE: %s\nPATHS: %s\nDEPENDS-ON: %s\nEST: %s\nBASE: %s\n",
		r.stream, r.who, r.route, strings.ReplaceAll(r.paths, "\n", " "), cutDepends(r, rows, o.Repo, true), r.est, o.Base)
	if r.route != taskcard.RouteFriend {
		fmt.Fprintf(&b, "base-sha: %s\n", o.BaseSHA)
	}
	if o.Parent != "" {
		phase := taskcard.PhaseChild
		if r.stitch {
			phase = taskcard.PhaseStitch
		}
		fmt.Fprintf(&b, "PARENT: %s (%s of the plan)\n", o.Parent, phase)
	}
	fmt.Fprintf(&b, "DONE-WHEN: %s\n", strings.ReplaceAll(r.doneWhen, "\n", " "))
	if r.body != "" {
		b.WriteString("\n" + r.body + "\n")
	}
	return b.String()
}

// cutSpec is a row's card content: the issue text through the one parser,
// with the repo and base the cut names.
func cutSpec(r *cutRow, o cutFromOpts, text string) taskcard.Spec {
	var s taskcard.Spec
	if text != "" {
		s = taskcard.ParseIssue(text)
	} else {
		s = taskcard.Spec{Route: r.route, Who: r.who, Paths: r.paths, DoneWhen: r.doneWhen, Est: r.est, Task: r.title}
	}
	if r.stitch {
		s.Kind = taskcard.KindStitch
	}
	s.Repo, s.Base = o.Repo, o.Base
	if r.route != taskcard.RouteFriend {
		s.BaseSHA = o.BaseSHA
	}
	return s
}

// cutField is a receipt field: - when empty, quoted when it has a space.
func cutField(s string) string {
	if s == "" {
		return "-"
	}
	if strings.ContainsAny(s, " \t\"\n\r=") {
		return strconv.Quote(s)
	}
	return s
}

func cutRefused(out io.Writer, r *cutRow) {
	if strings.HasPrefix(r.why, "REFUSED PATHS ") {
		// the push's own paths gate (#4322): its receipt, as the check prints it
		fmt.Fprintf(out, "%s row=%s line=%d id=%s\n", r.why, cutRowN(r), r.line, cutField(r.id))
		return
	}
	fmt.Fprintf(out, "CARD CUT REFUSED row=%s line=%d id=%s why=%s\n", cutRowN(r), r.line, cutField(r.id), cutField(r.why))
}

// cutRowN is a receipt's row: its number, or stitch for the plan's stitch.
func cutRowN(r *cutRow) string {
	if r.stitch {
		return "stitch"
	}
	return strconv.Itoa(r.n)
}

// planRows reads the parent and shapes the rows as its children plus the
// stitch row (#4317): the parent's stream, repo and base win, every row
// carries parent and phase, and the stitch row comes last, its edges every
// child row (set here, not through an id cell) and every child the parent
// already has. A refusal is the whole cut's (nothing is written) and names
// the remedy.
func planRows(ctx context.Context, o *cutFromOpts, d cutFromDeps, rows []*cutRow) ([]*cutRow, planFacts, error) {
	if d.Plan == nil {
		return nil, planFacts{}, fmt.Errorf("--parent needs the store: pass --redis <addr> (a dry run reads the parent too)")
	}
	facts, err := d.Plan(ctx, o.Parent)
	if err != nil {
		return nil, facts, err
	}
	rec := facts.Rec
	if len(rec) == 0 {
		return nil, facts, fmt.Errorf("no task:%s: push the parent first (card cut --issue <n>, or task push --id %s)", o.Parent, o.Parent)
	}
	switch rec["where"] {
	case "waiting", "ready":
	default:
		return nil, facts, fmt.Errorf("task:%s is %s; a plan is cut while its parent waits (waiting or ready)", o.Parent, cutField(rec["where"]))
	}
	stitch := taskcard.StitchID(o.Parent)
	if old := rec[taskcard.FieldStitch]; old != "" {
		if old != stitch {
			return nil, facts, fmt.Errorf("task:%s is a plan whose stitch is %s, not %s", o.Parent, old, stitch)
		}
		if facts.StitchWhere != "waiting" {
			return nil, facts, fmt.Errorf("task:%s is a plan whose stitch %s is %s: more children are cut while the stitch waits; cut a new plan otherwise", o.Parent, old, cutField(facts.StitchWhere))
		}
	}
	if rec["stream"] == "" {
		return nil, facts, fmt.Errorf("task:%s has no stream: nova-sprint task move --id %s --to-stream <s> first", o.Parent, o.Parent)
	}
	facts.Children = map[string]bool{}
	for _, id := range strings.Fields(rec[taskcard.FieldChildren]) {
		facts.Children[id] = true
	}
	o.Stream = rec["stream"]
	if o.Repo == "" {
		o.Repo = rec["repo"]
	}
	if o.Repo == "" && !o.NoGitHub {
		return nil, facts, fmt.Errorf("task:%s names no repo: pass --repo <owner/name> (or --no-github)", o.Parent)
	}
	if b := rec["base"]; b != "" && (o.Base == "" || o.Base == "dev") {
		o.Base = b
	}
	if o.BaseSHA == "" && cutSHARE.MatchString(rec["base_sha"]) {
		o.BaseSHA = rec["base_sha"]
	}
	doneWhen := strings.TrimSpace(rec["done_when"])
	if doneWhen == "" {
		return nil, facts, fmt.Errorf("task:%s has no DONE-WHEN; the stitch's DONE-WHEN is the parent's: nova-sprint task move --id %s --set done_when", o.Parent, o.Parent)
	}
	seen := map[string]bool{}
	var paths []string
	for _, r := range rows {
		if r.stream != "" && r.stream != o.Stream {
			r.why = fmt.Sprintf("stream %q is not the parent's stream %q (children ride the parent's stream)", r.stream, o.Stream)
		}
		r.stream = o.Stream
		r.fields = append(r.fields, taskcard.FieldParent, o.Parent, taskcard.FieldPhase, taskcard.PhaseChild)
		for _, p := range strings.Fields(r.paths) {
			if !seen[p] {
				seen[p] = true
				paths = append(paths, p)
			}
		}
	}
	if o.StitchRoute == "" {
		o.StitchRoute = cardhdr.RouteFrontier
	}
	if o.StitchEst == "" {
		o.StitchEst = "60"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Stitch of plan %s%s: the coordinator's second phase. Read every child's PR below as one change (duplicates, drifted names, tests that fail together), stitch what the plan's DONE-WHEN still needs on top of the landed children, and land it; a child that fell short is a new child (card cut --parent %s), not a fix here.\n\n%s\n",
		o.Parent, planRef(rec["ref"]), o.Parent, taskcard.BriefMarker)
	st := &cutRow{n: len(rows) + 1, stitch: true, id: stitch, title: "stitch: " + cutOneLine(rec["title"]),
		stream: o.Stream, who: "any", route: strings.ToLower(o.StitchRoute), est: o.StitchEst,
		paths: strings.Join(paths, " "), doneWhen: doneWhen, body: b.String(),
		fields: []string{taskcard.FieldParent, o.Parent, taskcard.FieldPhase, taskcard.PhaseStitch}}
	if st.paths == "" {
		st.paths = strings.TrimSpace(rec["paths"])
	}
	// A stitch already waiting is never pushed again: its edges grow at the
	// bind (taskcard.BindPlan), and its row is to=already.
	if rec[taskcard.FieldStitch] != "" {
		st.already = true
		st.ref = facts.StitchRef
	}
	// The stitch's edges: every child row (by row, resolved to its id at the
	// push and its ref in the issue, as an id cell would be) and the ids the
	// parent already has.
	for _, r := range rows {
		st.deps = append(st.deps, "child:"+strconv.Itoa(r.n))
		st.depRow = append(st.depRow, r.n)
		st.rowDeps = append(st.rowDeps, r.n)
	}
	for _, id := range strings.Fields(rec[taskcard.FieldChildren]) {
		st.deps = append(st.deps, id)
		st.depRow = append(st.depRow, 0)
	}
	return append(rows, st), facts, nil
}

func planRef(ref string) string {
	if ref == "" {
		return ""
	}
	return " (" + ref + ")"
}

func cutOneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return "-"
	}
	return s
}

// cardCutFrom is the verb below its flags.
func cardCutFrom(ctx context.Context, o cutFromOpts, d cutFromDeps, out io.Writer) int {
	start := d.Now()
	github := "on"
	if o.NoGitHub {
		github = "off"
	}
	var cut, already, filed, reused int
	summary := func(rows, refused int) {
		fmt.Fprintf(out, "CARD CUT FROM file=%s rows=%d cut=%d already=%d refused=%d filed=%d reused=%d github=%s ms=%d\n",
			cutField(o.From), rows, cut, already, refused, filed, reused, github, d.Now().Sub(start).Milliseconds())
	}
	rows, err := parseCutRows(o.Text)
	if err != nil {
		fmt.Fprintf(out, "CARD CUT REFUSED file=%s why=%s\n", cutField(o.From), cutField(err.Error()))
		summary(0, 1)
		return 1
	}
	children := len(rows)
	var facts planFacts
	if o.Parent != "" {
		if rows, facts, err = planRows(ctx, &o, d, rows); err != nil {
			fmt.Fprintf(out, "CARD CUT REFUSED parent=%s why=%s\n", o.Parent, cutField(err.Error()))
			summary(children, children)
			return 1
		}
	}
	// The base sha is read once, and only when a row runs on the swarm.
	for _, r := range rows {
		if cardhdr.IsRoute(r.route) && o.BaseSHA == "" {
			sha, err := d.BaseSHA(o.Repo, o.Base)
			if err != nil {
				fmt.Fprintf(out, "CARD CUT REFUSED file=%s why=%s remedy=%s\n", cutField(o.From), cutField(err.Error()),
					cutField("pass --base-sha <sha40>, or nova-sprint mirror refresh so the mirror holds "+o.Base))
				summary(len(rows), len(rows))
				return 1
			}
			o.BaseSHA = sha
			break
		}
	}
	// The id cells name rows for DEPENDS-ON (the first of a repeated id;
	// the repeat is refused below). With --no-github a row without one is
	// its title's slug, which a dependent may not name: it needs an id.
	byID, slugs := map[string]int{}, map[string]int{}
	for _, r := range rows {
		if r.id == "" {
			if o.NoGitHub {
				if s := cutSlug(r.title); s != "" {
					if _, ok := slugs[s]; !ok {
						slugs[s] = r.n
					}
				}
			}
		} else if _, ok := byID[r.id]; !ok {
			byID[r.id] = r.n
		}
	}
	ids := map[string]int{}
	for _, r := range rows {
		checkCutRow(r, byID, slugs, o)
		if r.id != "" && r.why == "" {
			if k, ok := ids[r.id]; ok {
				r.why = fmt.Sprintf("id %s is row %d's too", r.id, k)
			}
			ids[r.id] = r.n
		}
	}
	order := orderCutRows(rows)
	refused := 0
	for _, r := range rows {
		if r.why != "" {
			refused++
			cutRefused(out, r)
		}
	}
	if refused > 0 {
		// Nothing is filed or pushed while a row is bad: the fixed file
		// reruns whole, with no duplicate issue.
		summary(len(rows), refused)
		return 1
	}
	// No path belongs to two open streams (nova-tools #4322): every row is
	// gated, in file order, against every OTHER open stream's paths and the
	// rows before it, before any issue is filed (and in a dry run with
	// --redis, reported); --join names the one open stream a row may join
	// instead of its own. The push's FCALL gates each row again, atomically.
	if d.StreamPaths != nil {
		open, err := d.StreamPaths(ctx)
		if err != nil {
			fmt.Fprintf(out, "CARD CUT REFUSED file=%s why=%s remedy=%s\n", cutField(o.From), cutField(err.Error()),
				cutField("check --redis or NOVA_SPRINT_REDIS and rerun; nothing was filed"))
			summary(len(rows), len(rows))
			return 1
		}
		bad := 0
		for _, r := range rows {
			paths := ws.SplitPaths(r.paths)
			to, no := open.Gate(r.stream, paths, o.Join)
			if no != nil {
				fmt.Fprintf(out, "%s row=%d line=%d id=%s\n", no.Receipt(), r.n, r.line, cutField(r.id))
				bad++
				continue
			}
			r.stream = to
			open.Paths.Add(to, paths)
		}
		if bad > 0 {
			summary(len(rows), bad)
			return 1
		}
	}
	if o.DryRun {
		for _, r := range order {
			fmt.Fprintf(out, "CARD CUT DRY row=%s id=%s stream=%s who=%s route=%s est=%s depends=%s title=%s\n", cutRowN(r), cutField(r.id),
				cutField(r.stream), cutField(r.who), r.route, cutField(r.est), cutField(cutDepends(r, rows, o.Repo, false)), cutField(r.title))
		}
		if o.Parent != "" {
			fmt.Fprintf(out, "CARD CUT DRY PLAN parent=%s children=%d stitch=%s parent_to=waiting depends=%s\n", o.Parent, children, taskcard.StitchID(o.Parent), taskcard.StitchID(o.Parent))
		}
		summary(len(rows), 0)
		return 0
	}

	// The ledger: the rows an earlier run of this file filed take their
	// issue from it and are never filed again.
	name := o.Repo[strings.IndexByte(o.Repo, '/')+1:]
	ledger := taskcard.CutLedgerKey(o.Text)
	if !o.NoGitHub {
		l, err := d.LedgerRead(ctx, ledger)
		if err != nil {
			fmt.Fprintf(out, "CARD CUT REFUSED file=%s why=%s remedy=%s\n", cutField(o.From), cutField("ledger: "+err.Error()),
				cutField("check --redis or NOVA_SPRINT_REDIS and rerun; nothing was filed"))
			summary(len(rows), len(rows))
			return 1
		}
		if len(l.Issues) > 0 && l.Repo != o.Repo {
			fmt.Fprintf(out, "CARD CUT REFUSED file=%s why=%s remedy=%s\n", cutField(o.From),
				cutField(fmt.Sprintf("%s filed this file's rows on %s, not %s", ledger, l.Repo, o.Repo)), cutField("rerun with --repo "+l.Repo))
			summary(len(rows), len(rows))
			return 1
		}
		for _, r := range rows {
			if n, ok := l.Issues[r.n]; ok {
				r.fromLedger = true
				r.ref, r.origin = fmt.Sprintf("%s#%d", o.Repo, n), fmt.Sprintf("https://github.com/%s/issues/%d", o.Repo, n)
				if r.id == "" {
					r.id = fmt.Sprintf("%s-%d", name, n)
				}
				reused++
			}
		}
	}

	// File each issue in dependency order through the one writer, and write
	// it to the ledger before the next; the first failure stops the filing
	// (a forge that refused one refuses the next), and the rows behind it
	// are named, never filed.
	stopped := ""
	for _, r := range order {
		if o.NoGitHub {
			break
		}
		if r.fromLedger || r.already {
			continue // the ledger's issue, or a waiting stitch: never filed again
		}
		if stopped != "" {
			r.why = "not filed: the filing stopped at row " + stopped
			continue
		}
		n, url, err := d.File(ctx, o.Repo, r.title, cutIssueText(r, rows, o))
		if n > 0 {
			filed++
			if lerr := d.LedgerWrite(ctx, ledger, o.Repo, r.n, n); lerr != nil {
				r.why = fmt.Sprintf("ledger: %v; %s#%d is filed but not in the ledger, so a rerun files it again: record it first with redis-cli HSET %s repo %s %d %d",
					lerr, o.Repo, n, ledger, o.Repo, r.n, n)
				stopped = strconv.Itoa(r.n)
				continue
			}
		}
		if err != nil {
			r.why = "file: " + err.Error()
			if n > 0 {
				r.why += fmt.Sprintf("; %s#%d is in the ledger, so a rerun pushes its card without filing it again", o.Repo, n)
			}
			stopped = strconv.Itoa(r.n)
			continue
		}
		r.ref, r.origin = fmt.Sprintf("%s#%d", o.Repo, n), url
		if r.id == "" {
			r.id = fmt.Sprintf("%s-%d", name, n)
		}
	}

	// Push every filed row as a task card, in one pipeline, in order.
	var push []*cutRow
	var reqs []taskcard.PushRequest
	for _, r := range order {
		if r.why != "" || r.already {
			continue
		}
		text := cutIssueText(r, rows, o)
		spec := cutSpec(r, o, text)
		blocked := cutDepends(r, rows, o.Repo, false)
		if blocked == "none" {
			blocked = ""
		}
		push = append(push, r)
		why := "card cut --from"
		if o.Parent != "" {
			why = "card cut --parent " + o.Parent
		}
		reqs = append(reqs, taskcard.PushRequest{ID: r.id, Where: "waiting", Stream: r.stream, Sprint: o.Sprint,
			Ref: r.ref, Origin: r.origin, Title: r.title, Repo: o.Repo, DependsOn: blocked,
			By: o.Actor, Why: why, Fields: r.fields, Spec: &spec, Join: o.Join})
	}
	var outcomes []taskcard.PushOutcome
	if len(reqs) > 0 {
		outcomes, err = d.Push(ctx, reqs)
		if err != nil {
			for _, r := range push {
				r.why = "push: " + err.Error()
			}
			outcomes = nil
		}
	}
	for i, oc := range outcomes {
		r := push[i]
		if oc.Err != nil {
			if why, ok := taskcard.IsRefused(oc.Err); ok {
				if (r.fromLedger || facts.Children[r.id]) && strings.HasPrefix(why, "EXISTS ") {
					r.already = true // an earlier run of this file cut it (or the plan lists it)
					continue
				}
				r.why = "push refused: " + why
				if no, ok := ws.ParseRefusal(why); ok {
					r.why = no.Receipt() // the push's own gate (SP.gate, #4322): a race the check above lost
				}
			} else {
				r.why = "push: " + oc.Err.Error()
			}
		} else if oc.Result.Where != "waiting" {
			r.why = "push placed it in " + oc.Result.Where + ", not waiting"
		}
	}
	var childIDs []string
	stitchOK := false
	for _, r := range order {
		if r.why != "" {
			cutRefused(out, r)
			continue
		}
		to := "waiting"
		if r.already {
			to = "already"
			already++
		} else {
			cut++
		}
		if r.stitch {
			stitchOK = true
		} else if o.Parent != "" {
			childIDs = append(childIDs, r.id)
		}
		fmt.Fprintf(out, "CARD CUT row=%s id=%s ref=%s stream=%s to=%s depends=%s\n", cutRowN(r), r.id, cutField(r.ref),
			cutField(r.stream), to, cutField(cutDepends(r, rows, o.Repo, false)))
	}
	// The parent is bound last, once its children and stitch exist: a cut
	// whose stitch was refused leaves the parent as it was (the children
	// stand as cards of the stream; a rerun with the fix cuts the stitch).
	if o.Parent != "" && stitchOK {
		stitch := taskcard.StitchID(o.Parent)
		res, err := d.Bind(ctx, o.Parent, childIDs, stitch, o.Actor)
		if err != nil {
			why := err.Error()
			if w, ok := taskcard.IsRefused(err); ok {
				why = w
			}
			fmt.Fprintf(out, "CARD CUT REFUSED parent=%s why=%s\n", o.Parent, cutField("bind: "+why))
			summary(len(rows), len(rows)-cut-already)
			return 1
		}
		fmt.Fprintf(out, "CARD CUT PLAN parent=%s children=%d stitch=%s parent_to=%s depends=%s\n", o.Parent, len(childIDs), stitch, res.To, stitch)
	}
	summary(len(rows), len(rows)-cut-already)
	if cut+already < len(rows) {
		return 1
	}
	return 0
}

// cmdCardCutFrom wires card cut --from: the file, the task store (opened
// before any issue is filed, so a store that is down files nothing) and the
// one GitHub writer (nova-sprint file's Issuer).
func cmdCardCutFrom(ctx context.Context, o cutFromOpts, addr string, stdout, stderr io.Writer) int {
	const verb = "card cut"
	if o.Parent == "" && !landRepoOK(o.Repo) {
		return refuse(stderr, verb, "--from wants --repo <owner/name>: the repo the issues are filed on and the cards name")
	}
	if o.Parent != "" {
		if o.Repo != "" && !landRepoOK(o.Repo) {
			return refuse(stderr, verb, "--repo wants <owner/name>")
		}
		if !cutIDRE.MatchString(o.Parent) || taskcard.IsCopy(o.Parent) {
			return refuse(stderr, verb, "--parent wants a task id ([A-Za-z0-9._-], no ~<n>), not "+strconv.Quote(o.Parent))
		}
		if o.StitchRoute != "" && !cardhdr.IsRoute(strings.ToLower(o.StitchRoute)) && strings.ToLower(o.StitchRoute) != taskcard.RouteFriend {
			return refuse(stderr, verb, "--stitch-route wants "+cardhdr.RouteList+", or friend")
		}
		if o.StitchEst != "" && !cutEstRE.MatchString(o.StitchEst) {
			return refuse(stderr, verb, "--stitch-est wants minutes (60, 2 h)")
		}
	}
	if o.Base == "" {
		o.Base = "dev"
	}
	if o.BaseSHA != "" && !cutSHARE.MatchString(o.BaseSHA) {
		return refuse(stderr, verb, "--base-sha wants 40 hex digits, not "+strconv.Quote(o.BaseSHA))
	}
	if o.Sprint != "" && !sprint.ValidName(o.Sprint) {
		return refuse(stderr, verb, "--sprint must match [a-z0-9-]{1,40}")
	}
	var err error
	if o.From == "-" {
		o.Text, err = io.ReadAll(os.Stdin)
	} else {
		o.Text, err = os.ReadFile(o.From)
	}
	if err != nil {
		return refuse(stderr, verb, "cannot read --from: "+err.Error())
	}
	d := cutFromDeps{Now: time.Now, BaseSHA: card.MirrorBranchSHA}
	if raddr := taskAddr(addr); o.DryRun && o.Parent == "" && raddr != "" {
		// a dry run with a store reports every row the paths gate would
		// refuse (#4322); it reads, and writes nothing
		st, err := store.Open(ctx, raddr)
		if err != nil {
			return refuse(stderr, verb, "redis: "+err.Error()+"; nothing filed")
		}
		defer func() { _ = st.Close() }()
		d.StreamPaths = func(ctx context.Context) (ws.GateView, error) {
			return ws.ReadGateView(ctx, st.Client())
		}
	}
	if !o.DryRun || o.Parent != "" {
		if !o.DryRun {
			if o.Actor = quackActor(o.Actor); o.Actor == "" {
				return refuse(stderr, verb, "--actor is required when "+seatEnv+" is empty")
			}
		}
		raddr := taskAddr(addr)
		if raddr == "" {
			return refuse(stderr, verb, "needs --redis <addr> or NOVA_SPRINT_REDIS")
		}
		st, err := store.Open(ctx, raddr)
		if err != nil {
			return refuse(stderr, verb, "redis: "+err.Error()+"; nothing filed")
		}
		defer func() { _ = st.Close() }()
		d.Push = func(ctx context.Context, reqs []taskcard.PushRequest) ([]taskcard.PushOutcome, error) {
			return taskcard.PushMany(ctx, st.Client(), reqs)
		}
		d.StreamPaths = func(ctx context.Context) (ws.GateView, error) {
			return ws.ReadGateView(ctx, st.Client())
		}
		d.LedgerRead = func(ctx context.Context, key string) (taskcard.CutLedger, error) {
			return taskcard.ReadCutLedger(ctx, st.Client(), key)
		}
		d.LedgerWrite = func(ctx context.Context, key, repo string, row, issue int) error {
			return taskcard.WriteCutLedger(ctx, st.Client(), key, repo, row, issue)
		}
		d.Plan = func(ctx context.Context, id string) (planFacts, error) {
			return readPlanFacts(ctx, st.Client(), id)
		}
		d.Bind = func(ctx context.Context, parent string, children []string, stitch, by string) (taskcard.Result, error) {
			return taskcard.BindPlan(ctx, st.Client(), parent, children, stitch, by)
		}
		if o.DryRun {
			return cardCutFrom(ctx, o, d, stdout)
		}
		if !o.NoGitHub {
			is, err := file.NewIssuer(file.Deps{Token: githubToken, Redis: st.Client()})
			if err != nil {
				return refuse(stderr, verb, err.Error()+"; nothing filed (--no-github pushes the cards alone)")
			}
			d.File = is.Post
		}
	}
	return cardCutFrom(ctx, o, d, stdout)
}

// readPlanFacts reads --parent's record and, when it has a stitch, where the
// stitch is: two reads, nothing written.
func readPlanFacts(ctx context.Context, c redis.Cmdable, id string) (planFacts, error) {
	rec, err := c.HGetAll(ctx, taskcard.Key(id)).Result()
	if err != nil {
		return planFacts{}, fmt.Errorf("task:%s: %w", id, err)
	}
	f := planFacts{Rec: rec}
	if s := rec[taskcard.FieldStitch]; s != "" {
		v, err := c.HMGet(ctx, taskcard.Key(s), "where", "ref").Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return f, fmt.Errorf("task:%s: %w", s, err)
		}
		f.StitchWhere, _ = v[0].(string)
		f.StitchRef, _ = v[1].(string)
	}
	return f, nil
}
