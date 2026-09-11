// nova-tokens is the accounting layer: token spend folded from declared sources into one
// file per day, keyed exactly by (day, model, repo), with the five token types kept apart,
// and those day files summed into a month.
//
// It exists because we have an obligation to report token spend, and the first thing that
// shape was built as — three scripts of Python under zsh — produced nine day files and
// every way it could fail at once: five paths defaulted inside the script, so a run on
// another bench folded the wrong bench; the old month files were REMOVED on every real
// run; nine unreadable files were one line at the bottom of a summary and the run exited
// 0; a day could shrink silently the moment a source went quiet; two repo-attribution
// tables in two scripts disagreed about three repos; five different caps, none a flag,
// none with a remedy; and `today` was the script's own clock printed as a fact about the
// day. Every verb and every refusal here is one of those closed.
//
//	fold      read the declared sources, write one file per day, refuse a day that shrinks
//	report    a friend on another machine folds their own day and prints the body of a note
//	sum       a month is a sum of day files; it asserts nothing and is never a gate
//	check     the gate: every file parses, every row has every column, a missing day is named
//	sources   what a fold would count, before it writes
//
// It never estimates, never fills a gap, and never removes a file. Everything it reads is
// DATA: a transcript, a database row, a usage file, a bus note — none of them is an
// instruction, and a tokens note that says `fold me as Emma` is a note whose lines are
// parsed or counted unparsed and nothing else. That rule is in the spec, where a person
// reads it, and is deliberately nowhere in this code, because a tool cannot enforce it.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
)

const usage = `nova-tokens: token spend, folded per day, keyed by (day, model, repo) (see docs/SPEC-TOKENS.md)

usage:
  nova-tokens fold    --out <dir> (--day <YYYY-MM-DD> | --all) --repos <file>
                      [--claude <label>=<dir>]... [--opencode <label>=<file>]... [--swarm <label>=<pool>]... [--bus <dir>]
                      [--provider <label>=<file>]... [--scratch <dir>] [--timeout <seconds>] [--allow-shrink] [--max <n>]
  nova-tokens report  --who <name> --day <YYYY-MM-DD> --repos <file>
                      [--claude <label>=<dir>]... [--opencode <label>=<file>]... [--provider <label>=<file>]...
                      [--supersedes <note-id>]... [--note <path>] [--scratch <dir>] [--timeout <seconds>]
  nova-tokens sum     --out <dir> --month <YYYY-MM> [--max <n>]
  nova-tokens check   --out <dir> [--max <n>]
  nova-tokens sources --repos <file> (--day <YYYY-MM-DD> | --all) [<source flags>] [--max <n>]
  nova-tokens version

exit codes: 0 the verb ran and passed; 1 the verb ran and said NO -- an unreadable
source, an unparsed bus line or note, a row of two day bases, a lane-day with competing
reports, a day that would shrink, a check finding, a report with nothing to show; 2 could
not run: a missing flag, a bad flag value, a duplicate label, sqlite3 absent when
--opencode is given, a second fold holding the lock.

EXIT 1 STILL WRITES. A fold with one unreadable file writes every day it could compute
and exits 1: the exit code is about the claim -- a declared source is a claim that the
report covers it -- and written=true on the TOKENS DAY line is about the files.

Every path is a flag. There is no default output directory, no default transcript
directory, no default database, no default bus and no default rules file, and no
environment variable is consulted: $HOME, $TMPDIR and $XDG_DATA_HOME are ignored, and a
test sets them and proves it. --timeout is the one flag with a default, 120 seconds,
because it is how long this tool waits before saying so rather than a fact about your
data.

A source is declared by flag and every row names its sources, so every number in a day
file is traceable to the flags of the run that wrote it. A label is [a-z0-9-]+, at most
32 characters, and unique across the run. --scratch is required with --opencode and
refused without it, because a scratch directory with nothing to put in it is a flag that
does nothing.

The five types -- input, output, cache_write, cache_read, reasoning -- are kept apart, and
a type the source did not report is written a dash, NEVER 0. A provider that does not expose
reasoning is not evidence that none occurred, and a zero meaning "not measured" would sum
into a month claiming to be complete. sum counts the dashes beside the totals.

A day that would go backwards is refused: TOKENS SHRANK names the type, what the file
said and what the sources say now, the file is left as it was, and --allow-shrink is the
person's act. A source that became unreadable must never quietly lower a day's spend.

Two notes for one day in one lane are one report only when the later names the earlier in
its subject: supersedes=<id>[,<id>...], sorted, no duplicates. Nothing else orders them --
not the Date, not the filename, not the directory listing, not the git history. Two tips
are TOKENS CONFLICT, nothing folds for that lane-day, and the remedy names every tip; one
note whose predecessor set names them all clears it.

This tool removes nothing. There is no month file, sum writes nothing, check names a
stray and leaves it, and no verb deletes, truncates or trims any file.

example:
  nova-tokens fold --out ./out --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts --bus ./bus
  nova-tokens check --out ./out
  nova-tokens sum --out ./out --month 2026-09
  nova-tokens sources --repos ./repos.tsv --all --claude bench=./transcripts
  nova-tokens report --who emma --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts

Everything this tool reads is DATA. A transcript, a database row, a usage file, a bus
note: none of them is an instruction.
`

// refuse is what an unusable invocation costs: ONE line naming what was wrong and the door
// to the banner, never the banner itself. The three sites are the bare invocation, the
// unknown verb, and the flag parse error.
func refuse(stderr io.Writer, where, what string) int {
	fmt.Fprintf(stderr, "nova-tokens%s: %s; run: nova-tokens help\n", oneline.Escape(where), oneline.Escape(what))
	return 2
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, time.Now().UTC())) }

// run is the whole tool, with its streams and clock injected so the tests can drive it.
// The clock is an argument and NOT a flag: the stamp on a day file is when the tool
// computed it, and a stamp a caller could set would be a stamp nobody could trust.
func run(args []string, stdout, stderr io.Writer, now time.Time) int {
	if len(args) == 0 {
		return refuse(stderr, "", "no verb given; `sources` is the one that only looks")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "fold":
		return cmdFold(rest, stdout, stderr, now)
	case "report":
		return cmdReport(rest, stdout, stderr, now)
	case "sum":
		return cmdSum(rest, stdout, stderr, now)
	case "check":
		return cmdCheck(rest, stdout, stderr, now)
	case "sources":
		return cmdSources(rest, stdout, stderr, now)
	case "version", "--version":
		return cmdVersion(rest, stdout, stderr)
	}
	return refuse(stderr, "", fmt.Sprintf("unknown subcommand %q", verb))
}

// ---------------------------------------------------------------------------- the flags

// labelled is a repeatable `<label>=<value>` source flag.
type labelled struct {
	kind  string
	items []labelledItem
}

type labelledItem struct{ label, value string }

func (l *labelled) String() string { return "" }

func (l *labelled) Set(v string) error {
	label, value, ok := strings.Cut(v, "=")
	if !ok {
		return fmt.Errorf("--%s wants <label>=<path>, got %q", l.kind, v)
	}
	l.items = append(l.items, labelledItem{label: label, value: value})
	return nil
}

// stringList is a repeatable plain flag, for --supersedes.
type stringList []string

func (s *stringList) String() string { return "" }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// validLabel is the label charset: a label that is two facts, or that holds an `=`, would
// make the sources column of a row a lie.
func validLabel(label string) bool {
	if label == "" || len(label) > 32 {
		return false
	}
	for _, r := range label {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return false
	}
	return true
}

// refusals collects every independent problem so one run reports them all: sending a
// first run back three times for three flags is three refusals the first one already
// knew about.
type refusals struct {
	token string
	list  []string
}

func (r *refusals) add(problem string) { r.list = append(r.list, problem) }

func (r *refusals) required(name, value, wants string) {
	if strings.TrimSpace(value) == "" {
		r.add("--" + name + " is required; it wants " + wants + "; refusing to guess")
	}
}

// print writes one line per problem, in the order they were found, and returns exit 2.
func (r *refusals) print(stderr io.Writer) int {
	for _, problem := range r.list {
		fmt.Fprintf(stderr, "%s REFUSED: %s\n", oneline.Field(r.token), oneline.Escape(problem))
	}
	return 2
}

// wants is what each flag is FOR, in the words a refusal uses.
const (
	wantsOut     = "the directory the day files are written to"
	wantsRepos   = "a file of <name><TAB><regexp> lines, in priority order, naming your repos"
	wantsDay     = "one UTC day as YYYY-MM-DD, or --all for every day the sources name"
	wantsWho     = "the name this report is from, as the bus knows it"
	wantsMonth   = "one month as YYYY-MM"
	wantsSources = "--claude <label>=<dir>, --opencode <label>=<file>, --swarm <label>=<pool>, --bus <dir> or --provider <label>=<file>"
	wantsScratch = "a directory this run may copy the OpenCode database into"
)

// sourceFlags are the five source flags, declared once so that fold, sources and report
// cannot drift apart about what a source is.
type sourceFlags struct {
	claude   labelled
	opencode labelled
	swarm    labelled
	provider labelled
	bus      string
	scratch  string
	timeout  int
	repos    string
}

func (s *sourceFlags) declare(fs *flag.FlagSet, withSwarmAndBus bool) {
	s.claude.kind, s.opencode.kind, s.swarm.kind, s.provider.kind = "claude", "opencode", "swarm", "provider"
	fs.Var(&s.claude, "claude", "")
	fs.Var(&s.opencode, "opencode", "")
	fs.Var(&s.provider, "provider", "")
	if withSwarmAndBus {
		fs.Var(&s.swarm, "swarm", "")
		fs.StringVar(&s.bus, "bus", "", "")
	}
	fs.StringVar(&s.scratch, "scratch", "", "")
	fs.StringVar(&s.repos, "repos", "", "")
	fs.IntVar(&s.timeout, "timeout", int(tokens.DefaultTimeout/time.Second), "")
}

// check validates the declared sources without reading any of them.
func (s *sourceFlags) check(r *refusals) {
	r.required("repos", s.repos, wantsRepos)
	seen := map[string]bool{}
	any := false
	for _, l := range []*labelled{&s.claude, &s.opencode, &s.swarm, &s.provider} {
		for _, it := range l.items {
			any = true
			if !validLabel(it.label) {
				r.add("--" + l.kind + " " + it.label + "=: a label is [a-z0-9-]+, at most 32 characters")
				continue
			}
			if seen[it.label] {
				r.add("the label " + it.label + " is used twice; two sources with one label would make the sources column a lie")
			}
			seen[it.label] = true
			if strings.TrimSpace(it.value) == "" {
				r.add("--" + l.kind + " " + it.label + "= has no path; it wants <label>=<path>")
			}
			if l.kind == "provider" && !tokens.KnownParser(it.label) {
				r.add("--provider " + it.label + ": the label names the parser, and it wants one of " + strings.Join(tokens.Parsers, ", "))
			}
		}
	}
	if s.bus != "" {
		any = true
	}
	if !any {
		r.add("at least one source flag is required; it wants " + wantsSources + "; refusing to guess")
	}
	if len(s.opencode.items) > 0 {
		if strings.TrimSpace(s.scratch) == "" {
			r.required("scratch", "", wantsScratch)
		}
		if err := tokens.HaveSQLite(); err != nil {
			r.add(err.Error())
		}
	} else if strings.TrimSpace(s.scratch) != "" {
		r.add("--scratch is refused without --opencode; a scratch directory with nothing to put in it is a flag that does nothing")
	}
	if s.timeout < 1 {
		r.add("--timeout is a whole number of seconds and at least 1, got " + strconv.Itoa(s.timeout) + "; it is how long this tool waits for sqlite3 before saying so")
	}
}

// read reads every declared source, in declaration order, through the one reader per kind.
func (s *sourceFlags) read(rules *tokens.Rules, now time.Time) []*tokens.Source {
	var out []*tokens.Source
	for _, it := range s.claude.items {
		out = append(out, tokens.ReadClaude(it.label, it.value, rules))
	}
	for _, it := range s.opencode.items {
		out = append(out, tokens.ReadOpenCode(it.label, it.value, s.scratch, time.Duration(s.timeout)*time.Second, rules))
	}
	for _, it := range s.swarm.items {
		out = append(out, tokens.ReadSwarm(it.label, it.value, rules))
	}
	for _, it := range s.provider.items {
		out = append(out, tokens.ReadProvider(it.label, it.value, rules))
	}
	if s.bus != "" {
		out = append(out, tokens.ReadBus(s.bus, rules, now)...)
	}
	for _, src := range out {
		keys := map[tokens.Key]bool{}
		for _, m := range src.Stream {
			keys[tokens.Key{Day: m.Day, Model: m.Model, Repo: m.Repo}] = true
		}
		src.Stat.Rows = len(keys)
	}
	return out
}

// newFlagSet builds a flag set with package flag's two mouths closed: its error text
// quotes the argument it could not parse and its usage dump follows, so an argument
// beginning with a dash could otherwise author a whole line of stderr.
func newFlagSet(verb string) *flag.FlagSet {
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	return fs
}

// maxFlag reads the listing ceiling. 0 is all; a negative one is a typo with two readings
// and is refused, because 0 already means all.
func checkMax(r *refusals, max int) {
	if max < 0 {
		r.add("--max is a ceiling on each listing, 0 for all, and is never negative, got " + strconv.Itoa(max))
	}
}

// stamp is the tool's own UTC stamp, the one on every day file and every summary line. No
// flag sets it.
func stamp(now time.Time) string { return now.UTC().Format(time.RFC3339) }

// ------------------------------------------------------------------------------- fold

// maxRemedy is the flag that lifts a listing's ceiling, written the way it would be typed.
// A cap with no remedy is censorship; a cap with one is an index.
func maxRemedy(verb string) string { return "nova-tokens " + verb + " ... --max 0" }

// sourceLine is the line where a number becomes traceable: what each declared source
// opened, refused, counted and fed. A field that is not a measurement for the kind prints
// a dash, because a dash is an absence where a zero is a measurement.
func sourceLine(token string, s *tokens.Source) string {
	return fmt.Sprintf("%s SOURCE label=%s kind=%s path=%s reports=%s day_basis=%s files=%s unreadable=%s messages=%s dup=%s noid=%s nousage=%s unparsed=%s comments=%s redated=%s superseded=%s rows=%s",
		oneline.Field(token), oneline.Field(s.Label), oneline.Field(s.Kind), oneline.Field(s.Path),
		oneline.Field(s.ReportsList()), oneline.Field(s.Basis),
		oneline.Field(s.StatField("files")), oneline.Field(s.StatField("unreadable")),
		oneline.Field(s.StatField("messages")), oneline.Field(s.StatField("dup")),
		oneline.Field(s.StatField("noid")), oneline.Field(s.StatField("nousage")),
		oneline.Field(s.StatField("unparsed")), oneline.Field(s.StatField("comments")),
		oneline.Field(s.StatField("redated")), oneline.Field(s.StatField("superseded")),
		oneline.Field(s.StatField("rows")))
}

func unreadableLine(token string, u tokens.Unreadable) string {
	return fmt.Sprintf("%s UNREADABLE label=%s path=%s: %s",
		oneline.Field(token), oneline.Field(u.Label), oneline.Field(u.Path),
		oneline.Escape(oneline.Cap(u.Why, oneline.TailBytes)))
}

func unparsedLine(token string, u tokens.Unparsed) string {
	return fmt.Sprintf("%s UNPARSED label=%s note=%s line=%d: %s",
		oneline.Field(token), oneline.Field(u.Label), oneline.Field(u.Note), u.Line,
		oneline.Escape(oneline.Cap(u.Text, oneline.TailBytes)))
}

// cmdFold is the wall: it reads every declared source whole and writes the days it could
// compute. It says NO when a source could not be read, a bus line or note did not parse,
// a row mixed two day bases, a lane-day had competing reports, or a day would have shrunk
// -- and it still writes the rest, because the exit code is about the claim.
func cmdFold(args []string, stdout, stderr io.Writer, now time.Time) int {
	fs := newFlagSet("fold")
	out := fs.String("out", "", "")
	day := fs.String("day", "", "")
	all := fs.Bool("all", false, "")
	allowShrink := fs.Bool("allow-shrink", false, "")
	max := fs.Int("max", bounded.Default, "")
	var sf sourceFlags
	sf.declare(fs, true)
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, " fold", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if n := fs.NArg(); n > 0 {
		return refuse(stderr, " fold", fmt.Sprintf("takes no positional arguments, got %d (flags come before arguments)", n))
	}
	r := &refusals{token: "TOKENS"}
	r.required("out", *out, wantsOut)
	checkDay(r, *day, *all)
	sf.check(r)
	checkMax(r, *max)
	if len(r.list) > 0 {
		return r.print(stderr)
	}
	if fi, err := os.Stat(*out); err != nil || !fi.IsDir() {
		r.add("--out is not a directory: " + *out + "; it wants " + wantsOut)
		return r.print(stderr)
	}
	rules, err := tokens.LoadRules(sf.repos)
	if err != nil {
		r.add("--repos " + sf.repos + ": " + err.Error() + "; it wants " + wantsRepos)
		return r.print(stderr)
	}
	release, err := tokens.TakeFoldLock(*out, tokens.LockWait)
	if err != nil {
		r.add(err.Error())
		return r.print(stderr)
	}
	defer release()

	sources := sf.read(rules, now)
	folder := tokens.NewFolder()
	for _, s := range sources {
		for _, m := range s.Stream {
			folder.Add(s.Label, m)
		}
	}

	fmt.Fprintf(stdout, "TOKENS FOLD at=%s build=%s out=%s sources=%d days=%s repos=%s\n",
		oneline.Field(stamp(now)), oneline.Field(buildVersion()), oneline.Field(*out),
		len(sources), oneline.Field(daysAsked(*day, *all)), oneline.Field(sf.repos))

	srcList := bounded.Capped(stdout, *max, "TOKENS", "source", maxRemedy("fold"))
	unreadable := bounded.Capped(stderr, *max, "TOKENS", "unreadable", maxRemedy("fold"))
	unparsed := bounded.Capped(stderr, *max, "TOKENS", "unparsed", maxRemedy("fold"))
	superseded := bounded.Capped(stdout, *max, "TOKENS", "superseded", maxRemedy("fold"))
	conflicts := bounded.Capped(stderr, *max, "TOKENS", "conflict", maxRemedy("fold"))
	touched := bounded.Capped(stdout, *max, "TOKENS", "touched", maxRemedy("fold"))
	mixedList := bounded.Capped(stderr, *max, "TOKENS", "mixed", maxRemedy("fold"))
	dayList := bounded.Capped(stdout, *max, "TOKENS", "day", maxRemedy("fold"))
	shrankList := bounded.Capped(stderr, *max, "TOKENS", "shrank", maxRemedy("fold"))

	conflictDays := map[string]bool{}
	for _, s := range sources {
		srcList.Line(sourceLine("TOKENS", s))
	}
	srcList.More()
	for _, s := range sources {
		for _, u := range s.Unreadables {
			unreadable.Line(unreadableLine("TOKENS", u))
		}
	}
	unreadable.More()
	for _, s := range sources {
		for _, u := range s.Unparseds {
			unparsed.Line(unparsedLine("TOKENS", u))
		}
	}
	unparsed.More()
	for _, s := range sources {
		for _, sp := range s.Supersededs {
			superseded.Line(fmt.Sprintf("TOKENS SUPERSEDED label=%s note=%s by=%s day=%s",
				oneline.Field(sp.Label), oneline.Field(sp.Note), oneline.Field(sp.By), oneline.Field(sp.Day)))
		}
	}
	superseded.More()
	for _, s := range sources {
		for _, c := range s.Conflicts {
			conflictDays[c.Day] = true
			conflicts.Line(fmt.Sprintf("TOKENS CONFLICT label=%s day=%s notes=%s: competing reports; send a correction whose subject carries supersedes=%s",
				oneline.Field(c.Label), oneline.Field(c.Day), oneline.Field(strings.Join(c.Notes, ",")),
				oneline.Field(strings.Join(c.Notes, ","))))
		}
	}
	conflicts.More()
	for _, s := range sources {
		for _, t := range s.Toucheds {
			touched.Line(fmt.Sprintf("TOKENS TOUCHED label=%s day=%s repos=%s",
				oneline.Field(t.Label), oneline.Field(t.Day), oneline.Field(strings.Join(t.Repos, ","))))
		}
	}
	touched.More()

	days := folder.Days()
	if !*all {
		days = []string{*day}
	}
	daysWritten, rowsWritten := 0, 0
	for _, d := range days {
		rows, mixed := folder.DayRows(d)
		for _, m := range mixed {
			mixedList.Line(fmt.Sprintf("TOKENS MIXED date=%s model=%s repo=%s bases=%s: two day bases on one row; declare one export for that day",
				oneline.Field(m.Day), oneline.Field(m.Model), oneline.Field(m.Repo), oneline.Field(strings.Join(m.Bases, ","))))
		}
		if len(rows) == 0 && !conflictDays[d] {
			continue
		}
		file := buildDayFile(d, rows, folder, now)
		written := false
		shrank := false
		if !conflictDays[d] {
			if old, findings, err := tokens.ReadDayFile(tokens.Path(*out, d)); err == nil && !hasVersionFinding(findings) {
				for _, sh := range tokens.Shrinks(old.Totals(), file.Totals(), d) {
					shrank = true
					shrankList.Line(fmt.Sprintf("TOKENS SHRANK date=%s type=%s file=%s now=%s written=%t: a source went quiet; --allow-shrink writes it anyway",
						oneline.Field(sh.Day), oneline.Field(tokens.TypeNames[sh.Type]),
						oneline.Field(sh.File), oneline.Field(sh.Now), *allowShrink))
				}
			}
			if !shrank || *allowShrink {
				if err := file.Write(*out); err != nil {
					unreadable.Line(unreadableLine("TOKENS", tokens.Unreadable{Label: "out", Path: tokens.Path(*out, d), Why: err.Error()}))
				} else {
					written = true
					daysWritten++
					rowsWritten += len(rows)
				}
			}
		}
		dayList.Line(dayLine(d, file, rows, folder, written))
	}
	mixedList.More()
	dayList.More()
	shrankList.More()

	counts := fmt.Sprintf("days=%d rows=%d sources=%d unreadable=%d unparsed=%d mixed=%d conflict=%d shrank=%d",
		daysWritten, rowsWritten, len(sources), unreadable.Total(), unparsed.Total(),
		mixedList.Total(), conflicts.Total(), shrankList.Total())
	bad := unreadable.Total() > 0 || unparsed.Total() > 0 || mixedList.Total() > 0 ||
		conflicts.Total() > 0 || (shrankList.Total() > 0 && !*allowShrink)
	if bad {
		fmt.Fprintf(stderr, "TOKENS FAIL %s\n", counts)
	} else {
		fmt.Fprintf(stdout, "TOKENS OK %s\n", counts)
	}
	fmt.Fprintf(stdout, "TOKENS NOTE %s\n", oneline.Escape(remedy(sources, unreadable.Total(), unparsed.Total(),
		mixedList.Total(), conflicts.Total(), shrankList.Total(), *allowShrink, *out)))
	if bad {
		return 1
	}
	return 0
}

// checkDay enforces the one-of rule on --day and --all.
func checkDay(r *refusals, day string, all bool) {
	switch {
	case day == "" && !all:
		r.add("one of --day <YYYY-MM-DD> or --all is required; it wants " + wantsDay + "; refusing to guess")
	case day != "" && all:
		r.add("--day and --all are two answers to one question; give one")
	case day != "" && !tokens.ValidDay(day):
		r.add("--day is not a day: " + day + "; it wants " + wantsDay)
	}
}

func daysAsked(day string, all bool) string {
	if all {
		return "all"
	}
	return day
}

// hasVersionFinding reports whether a file on disk is too malformed to compare against:
// a shrink comparison with a file whose version line is missing would be a comparison
// with a guess, and `check` is what names that file.
func hasVersionFinding(findings []tokens.Finding) bool {
	for _, f := range findings {
		if f.Line <= 2 {
			return true
		}
	}
	return false
}

// buildDayFile turns a day's folded rows into the file that will be written.
func buildDayFile(day string, rows []*tokens.Row, folder *tokens.Folder, now time.Time) *tokens.DayFile {
	f := &tokens.DayFile{Day: day, At: stamp(now), Build: buildVersion(), Turns: tokens.Dash}
	if n, ok := folder.Turns(day); ok {
		f.Turns = strconv.Itoa(n)
	}
	labels := map[string]bool{}
	for _, r := range rows {
		for _, l := range r.Sources() {
			labels[l] = true
		}
		f.Rows = append(f.Rows, tokens.DayRow{
			Date: day, Model: r.Model, Repo: r.Repo, Counts: r.Counts,
			Rough: r.Rough, Basis: r.Basis(), Sources: r.Sources(),
		})
	}
	for l := range labels {
		f.Sources = append(f.Sources, l)
	}
	sort.Strings(f.Sources)
	return f
}

// dayLine is one line per day written or refused, and the two shares on it are how a
// person sees whether the rules file is good enough.
func dayLine(day string, file *tokens.DayFile, rows []*tokens.Row, folder *tokens.Folder, written bool) string {
	models, repos := map[string]bool{}, map[string]bool{}
	var whole, unknown, other int64
	dashes, nonutc := 0, 0
	rough := 0
	for _, r := range rows {
		models[r.Model] = true
		repos[r.Repo] = true
		t := r.Counts.Total()
		whole += t
		switch r.Repo {
		case "unknown":
			unknown += t
		case "other":
			other += t
		}
		dashes += r.Counts.Dashes()
		rough += r.Rough
		if r.Basis() != tokens.UTC {
			nonutc++
		}
	}
	return fmt.Sprintf("TOKENS DAY date=%s rows=%d models=%d repos=%d turns=%s unknown=%s%% other=%s%% rough=%d dashes=%d nonutc=%d sources=%s written=%t",
		oneline.Field(day), len(rows), len(models), len(repos), oneline.Field(file.Turns),
		oneline.Field(tokens.Percent(unknown, whole)), oneline.Field(tokens.Percent(other, whole)),
		rough, dashes, nonutc, oneline.Field(strings.Join(file.Sources, ",")), written)
}

// remedy is the ONE line TOKENS NOTE carries. It names the label and the act, in the order
// a reader would act on them, and when nothing was wrong it names the gate.
func remedy(sources []*tokens.Source, unreadable, unparsed, mixed, conflict, shrank int, allowShrink bool, out string) string {
	switch {
	case unreadable > 0:
		return "a declared source could not be read whole (" + firstUnreadableLabel(sources) + "): open those files to this group, or drop the flag -- a declared source is a claim that the report covers it"
	case unparsed > 0:
		return "a bus line or note did not parse (" + firstUnparsedNote(sources) + "): a body line is date<TAB>who<TAB>model<TAB>repo<TAB>type<TAB>count, with an optional day_basis=<zone>"
	case conflict > 0:
		return "a lane-day has competing reports (" + firstConflictLabel(sources) + "): one note whose subject carries supersedes=<every tip, sorted> is the replacement snapshot that clears it"
	case mixed > 0:
		return "a row was fed by two day bases: declare one export for that day, not both"
	case shrank > 0 && !allowShrink:
		return "a day would have gone backwards and was left as it was: --allow-shrink writes it anyway, and it is a person's act"
	case shrank > 0:
		return "a day was written smaller at your word (--allow-shrink); nova-tokens check --out " + out + " is the gate"
	}
	return "nothing was wrong; nova-tokens check --out " + out + " is the gate"
}

func firstUnreadableLabel(sources []*tokens.Source) string {
	for _, s := range sources {
		if len(s.Unreadables) > 0 {
			return s.Unreadables[0].Label
		}
	}
	return "-"
}

func firstUnparsedNote(sources []*tokens.Source) string {
	for _, s := range sources {
		if len(s.Unparseds) > 0 {
			return s.Unparseds[0].Note
		}
	}
	return "-"
}

func firstConflictLabel(sources []*tokens.Source) string {
	for _, s := range sources {
		if len(s.Conflicts) > 0 {
			return s.Conflicts[0].Label
		}
	}
	return "-"
}

// ---------------------------------------------------------------------------- sources

// cmdSources reads the declared sources exactly as fold does, through the same code -- a
// second reader would drift -- prints what each yielded, and writes nothing. It exists so
// a person can see what a fold would count before it writes, and it exits 0 whenever it
// ran, because asserting is not its job.
func cmdSources(args []string, stdout, stderr io.Writer, now time.Time) int {
	fs := newFlagSet("sources")
	day := fs.String("day", "", "")
	all := fs.Bool("all", false, "")
	max := fs.Int("max", bounded.Default, "")
	var sf sourceFlags
	sf.declare(fs, true)
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, " sources", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	r := &refusals{token: "SOURCES"}
	checkDay(r, *day, *all)
	sf.check(r)
	checkMax(r, *max)
	if len(r.list) > 0 {
		return r.print(stderr)
	}
	rules, err := tokens.LoadRules(sf.repos)
	if err != nil {
		r.add("--repos " + sf.repos + ": " + err.Error() + "; it wants " + wantsRepos)
		return r.print(stderr)
	}
	sources := sf.read(rules, now)

	srcList := bounded.Capped(stdout, *max, "SOURCES", "source", maxRemedy("sources"))
	unreadable := bounded.Capped(stderr, *max, "SOURCES", "unreadable", maxRemedy("sources"))
	unparsed := bounded.Capped(stderr, *max, "SOURCES", "unparsed", maxRemedy("sources"))
	files, messages, rows := 0, 0, 0
	for _, s := range sources {
		srcList.Line(sourceLine("SOURCES", s))
		files += s.Stat.Files
		messages += s.Stat.Messages
		rows += s.Stat.Rows
	}
	srcList.More()
	for _, s := range sources {
		for _, u := range s.Unreadables {
			unreadable.Line(unreadableLine("SOURCES", u))
		}
	}
	unreadable.More()
	for _, s := range sources {
		for _, u := range s.Unparseds {
			unparsed.Line(unparsedLine("SOURCES", u))
		}
	}
	unparsed.More()
	fmt.Fprintf(stdout, "SOURCES OK sources=%d files=%d messages=%d unreadable=%d unparsed=%d rows=%d\n",
		len(sources), files, messages, unreadable.Total(), unparsed.Total(), rows)
	return 0
}

// ---------------------------------------------------------------------------- report

// cmdReport is the verb for a friend on another machine, and the first user of this tool
// is not this bench. It folds that machine's own sources for one day, the same sources and
// the same attribution as fold, and prints EXACTLY the body lines of a tokens note and
// nothing else: no heading, no stamp, no comment. The stamp and the build id go on the
// subject, which it prints on its one OK line.
//
// THIS IS THE ONE PLACE IN THE FAMILY WHERE THE OK LINE LEAVES STDOUT, because here stdout
// is the artifact. The spec says so in as many words, which is the exception SPEC.md's
// Conventions allow when a spec states one.
func cmdReport(args []string, stdout, stderr io.Writer, now time.Time) int {
	fs := newFlagSet("report")
	who := fs.String("who", "", "")
	day := fs.String("day", "", "")
	notePath := fs.String("note", "", "")
	var supersedes stringList
	fs.Var(&supersedes, "supersedes", "")
	var sf sourceFlags
	sf.declare(fs, false)
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, " report", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	r := &refusals{token: "REPORT"}
	r.required("who", *who, wantsWho)
	switch {
	case *day == "":
		r.add("--day is required; it wants " + wantsDay + "; refusing to guess")
	case !tokens.ValidDay(*day):
		r.add("--day is not a day: " + *day + "; it wants " + wantsDay)
	}
	sf.check(r)
	seen := map[string]bool{}
	for _, id := range supersedes {
		switch {
		case !tokens.ValidNoteID(id):
			r.add("--supersedes " + id + ": it wants a note id of the shape <sender>-<12 hex>")
		case seen[id]:
			r.add("--supersedes names " + id + " twice; the predecessor set is a set, with no duplicate")
		}
		seen[id] = true
	}
	if len(r.list) > 0 {
		return r.print(stderr)
	}
	rules, err := tokens.LoadRules(sf.repos)
	if err != nil {
		r.add("--repos " + sf.repos + ": " + err.Error() + "; it wants " + wantsRepos)
		return r.print(stderr)
	}
	sorted := append([]string(nil), supersedes...)
	sort.Strings(sorted)

	sources := sf.read(rules, now)
	folder := tokens.NewFolder()
	for _, s := range sources {
		for _, m := range s.Stream {
			folder.Add(s.Label, m)
		}
	}
	unreadable := 0
	for _, s := range sources {
		for _, u := range s.Unreadables {
			fmt.Fprintln(stderr, unreadableLine("TOKENS", u))
			unreadable++
		}
	}
	rows, mixed := folder.DayRows(*day)
	for _, m := range mixed {
		fmt.Fprintf(stderr, "TOKENS MIXED date=%s model=%s repo=%s bases=%s: two day bases on one row; declare one export for that day\n",
			oneline.Field(m.Day), oneline.Field(m.Model), oneline.Field(m.Repo), oneline.Field(strings.Join(m.Bases, ",")))
	}
	var body strings.Builder
	lines := 0
	for _, row := range rows {
		for t := tokens.Type(0); t < tokens.NTypes; t++ {
			v, ok := row.Counts.Get(t)
			if !ok {
				continue
			}
			body.WriteString(tokens.BodyLine(*day, *who, row.Model, row.Repo, t, v, row.Basis()))
			body.WriteString("\n")
			lines++
		}
	}
	if lines == 0 || len(mixed) > 0 {
		// A friend with nothing to show says so, and never sends zeros. A REPORT FAIL
		// writes nothing: an existing --note file is left byte-unchanged.
		fmt.Fprintf(stderr, "REPORT FAIL who=%s day=%s rows=0 unreadable=%d\n",
			oneline.Field(*who), oneline.Field(*day), unreadable)
		return 1
	}
	fmt.Fprint(stdout, body.String())
	if *notePath != "" {
		tmp := *notePath + ".tmp"
		if err := os.WriteFile(tmp, []byte(body.String()), 0o644); err != nil {
			r.add("--note " + *notePath + ": " + err.Error())
			return r.print(stderr)
		}
		if err := os.Rename(tmp, *notePath); err != nil {
			r.add("--note " + *notePath + ": " + err.Error())
			return r.print(stderr)
		}
	}
	fmt.Fprintf(stderr, "REPORT OK who=%s day=%s rows=%d at=%s build=%s subject=%s\n",
		oneline.Field(*who), oneline.Field(*day), lines, oneline.Field(stamp(now)),
		oneline.Field(buildVersion()),
		oneline.Escape(tokens.Subject(*day, stamp(now), buildVersion(), sorted)))
	return 0
}

// ------------------------------------------------------------------------------- sum

// cmdSum ASSERTS NOTHING and is never a gate. It exits 0 whenever it ran, including over a
// month with gaps, because answering is its job and missing=<n> is the answer.
func cmdSum(args []string, stdout, stderr io.Writer, now time.Time) int {
	fs := newFlagSet("sum")
	out := fs.String("out", "", "")
	month := fs.String("month", "", "")
	max := fs.Int("max", bounded.Default, "")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, " sum", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	r := &refusals{token: "SUM"}
	r.required("out", *out, wantsOut)
	switch {
	case *month == "":
		r.add("--month is required; it wants " + wantsMonth + "; refusing to guess")
	case !validMonth(*month):
		r.add("--month is not a month: " + *month + "; it wants " + wantsMonth)
	}
	checkMax(r, *max)
	if len(r.list) > 0 {
		return r.print(stderr)
	}
	s, err := tokens.SumMonth(*out, *month)
	if err != nil {
		r.add(err.Error())
		return r.print(stderr)
	}
	turns := tokens.Dash
	if s.HaveTurn {
		turns = strconv.Itoa(s.Turns)
	}
	first, last := tokens.Dash, tokens.Dash
	if len(s.Days) > 0 {
		first, last = s.Days[0], s.Days[len(s.Days)-1]
	}
	fmt.Fprintf(stdout, "SUM MONTH month=%s at=%s build=%s days=%d first=%s last=%s missing=%d rows=%d turns=%s\n",
		oneline.Field(*month), oneline.Field(stamp(now)), oneline.Field(buildVersion()),
		len(s.Days), oneline.Field(first), oneline.Field(last), len(s.Missing), s.Rows, oneline.Field(turns))

	pairs := bounded.Capped(stdout, *max, "SUM", "pair", "nova-tokens sum --out "+*out+" --month "+*month+" --max 0")
	for _, p := range s.Pairs {
		pairs.Line(fmt.Sprintf("SUM PAIR model=%s repo=%s %s days=%d",
			oneline.Field(p.Model), oneline.Field(p.Repo), aggFields(p.Agg), p.Agg.Days()))
	}
	pairs.More()
	models := bounded.Capped(stdout, *max, "SUM", "model", "nova-tokens sum --out "+*out+" --month "+*month+" --max 0")
	for _, m := range s.Models {
		models.Line(fmt.Sprintf("SUM MODEL model=%s %s repos=%d",
			oneline.Field(m.Model), aggFields(m.Agg), m.Agg.Keys()))
	}
	models.More()
	fmt.Fprintf(stdout, "SUM TOTAL %s turns=%s pairs=%d models=%d\n",
		aggFields(s.Total), oneline.Field(turns), len(s.Pairs), len(s.Models))
	fmt.Fprintf(stdout, "SUM OK month=%s days=%d missing=%d pairs=%d models=%d nonutc=%d\n",
		oneline.Field(*month), len(s.Days), len(s.Missing), len(s.Pairs), len(s.Models), s.Total.NonUTC)
	return 0
}

// aggFields is the five totals, the rough count, the per-column dash counts and the
// non-UTC count: everything a reader needs to know what a total does NOT cover.
func aggFields(a *tokens.Agg) string {
	return fmt.Sprintf("input=%d output=%d cache_write=%d cache_read=%d reasoning=%d rough=%d dashes=%d,%d,%d,%d,%d nonutc=%d",
		a.Totals[tokens.Input], a.Totals[tokens.Output], a.Totals[tokens.CacheWrite],
		a.Totals[tokens.CacheRead], a.Totals[tokens.Reasoning], a.Rough,
		a.Dashes[tokens.Input], a.Dashes[tokens.Output], a.Dashes[tokens.CacheWrite],
		a.Dashes[tokens.CacheRead], a.Dashes[tokens.Reasoning], a.NonUTC)
}

func validMonth(m string) bool {
	if len(m) != 7 || m[4] != '-' {
		return false
	}
	for i, r := range m {
		if i == 4 {
			continue
		}
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ----------------------------------------------------------------------------- check

// cmdCheck is the GATE. It says NO on any malformed file, any malformed row, any missing
// day and any stray, and it prints the count line either way. A missing day is NAMED and
// never filled: nobody folded it, and this tool does not invent what nobody measured.
func cmdCheck(args []string, stdout, stderr io.Writer, now time.Time) int {
	fs := newFlagSet("check")
	out := fs.String("out", "", "")
	max := fs.Int("max", bounded.Default, "")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, " check", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	r := &refusals{token: "CHECK"}
	r.required("out", *out, wantsOut)
	checkMax(r, *max)
	if len(r.list) > 0 {
		return r.print(stderr)
	}
	res, err := tokens.Check(*out)
	if err != nil {
		r.add(err.Error())
		return r.print(stderr)
	}
	remedyLine := "nova-tokens check --out " + *out + " --max 0"
	files := bounded.Capped(stderr, *max, "CHECK", "file", remedyLine)
	rowsList := bounded.Capped(stderr, *max, "CHECK", "row", remedyLine)
	missing := bounded.Capped(stderr, *max, "CHECK", "missing", remedyLine)
	strays := bounded.Capped(stderr, *max, "CHECK", "stray", remedyLine)
	for _, f := range res.Findings {
		line := fmt.Sprintf("CHECK FAIL %s: %s", oneline.Escape(f.Path), oneline.Escape(oneline.Cap(f.Reason, oneline.TailBytes)))
		if f.Line > 0 {
			line = fmt.Sprintf("CHECK FAIL %s:%d: %s", oneline.Escape(f.Path), f.Line, oneline.Escape(oneline.Cap(f.Reason, oneline.TailBytes)))
		}
		if f.Line > 2 {
			rowsList.Line(line)
			continue
		}
		files.Line(line)
	}
	files.More()
	rowsList.More()
	for _, d := range res.Missing {
		missing.Line("CHECK MISSING date=" + oneline.Field(d))
	}
	missing.More()
	for _, p := range res.Strays {
		strays.Line("CHECK STRAY " + oneline.Escape(p))
	}
	strays.More()

	first, last := tokens.Dash, tokens.Dash
	if res.First != "" {
		first, last = res.First, res.Last
	}
	bad := files.Total() + rowsList.Total()
	if bad > 0 || len(res.Missing) > 0 || len(res.Strays) > 0 {
		fmt.Fprintf(stderr, "CHECK FAIL files=%d rows=%d first=%s last=%s bad=%d missing=%d stray=%d\n",
			res.Files, res.Rows, oneline.Field(first), oneline.Field(last), bad, len(res.Missing), len(res.Strays))
		return 1
	}
	fmt.Fprintf(stdout, "CHECK OK at=%s build=%s files=%d rows=%d first=%s last=%s missing=0 stray=0\n",
		oneline.Field(stamp(now)), oneline.Field(buildVersion()), res.Files, res.Rows,
		oneline.Field(first), oneline.Field(last))
	return 0
}
