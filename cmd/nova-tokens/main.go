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
// instruction, and a tokens note that says `fold me as Ada` is a note whose lines are
// parsed or counted unparsed and nothing else. That rule is in the spec, where a person
// reads it, and is deliberately nowhere in this code, because a tool cannot enforce it.
package main

import (
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

const usage = `nova-tokens: token spend per day, model and repository, read from AI session logs

how it works: fold reads the logs you name (Claude Code transcripts, OpenCode
databases, swarm pools, bus notes) and writes one day file per day into --out,
one row per (day, model, repo). The repo comes from the --repos file: lines of
<name><TAB><regexp>, and the first match on a session's path wins. check, sum
and report read the day files back; a count a source never gave prints as -.
first run: create a tiny transcript and rules file with the setup line above
example:, then run the lines under example: in order.

usage:
  nova-tokens fold    --out <dir> (--day <YYYY-MM-DD> | --all) --repos <file>
                      [--claude <label>=<dir>]... [--opencode <label>=<file>]... [--swarm <label>=<pool>]... [--bus <dir>]
                      [--provider <kind>:<label>=<file>]... [--scratch <dir>] [--timeout <seconds>] [--allow-shrink] [--max <n>] [--dry-run]
  nova-tokens report --who <name> --day <YYYY-MM-DD> --repos <file>
                      mode: local note body, printed as the tokens note artifact
                      [--claude <label>=<dir>]... [--opencode <label>=<file>]... [--provider <kind>:<label>=<file>]...
                      [--supersedes <note-id>]... [--note <path>] [--scratch <dir>] [--timeout <seconds>] [--dry-run]
  nova-tokens report --redis <host:port> --month <YYYY-MM> [--by model|repo|day|tuple] [--max <n>]
                      mode: Redis month summary
                      [--user <name>] [--password-env <NAME>]
  nova-tokens ledger  --out <dir> (--day <YYYY-MM-DD> | --month <YYYY-MM>) --redis <host:port>
                      [--user <name>] [--password-env <NAME>] [--dry-run]
  nova-tokens sum     --out <dir> --month <YYYY-MM> [--max <n>]
  nova-tokens check   --out <dir> [--strict | --no-spend <file>] [--through <YYYY-MM-DD>] [--max <n>]
  nova-tokens sources --repos <file> (--day <YYYY-MM-DD> | --all) [<source flags>] [--unattributed] [--max <n>]
  nova-tokens profiles --swarm-root <dir>
  nova-tokens session --claude-session <jsonl> [--out <dir>] [--day <YYYY-MM-DD>] [--dry-run]
  nova-tokens version

Every verb but version takes --json: the same result as one JSON object on stdout, a
refusal included. A verb that writes takes --dry-run: it is the real run's own plan --
it reads what the real run reads and refuses what the real run refuses -- prints what it
would write with dry_run=true on its last line, and writes nothing (ledger --dry-run dials
no store). The one difference: a dry fold or session takes no fold.lock, so it neither waits for nor
refuses on a fold holding one. --opencode under --dry-run (and under sources) still reads a
copy of the database, made in a new directory of the run's own under --scratch
(.nova-tokens-dry-run-*) and removed before it exits: --scratch is left as it was. ` + "`<verb> -h`" + ` lists a verb's flags and states its effect.

exit codes: 0 the verb ran and passed; 1 the verb ran and said NO -- an unreadable
source, an unparsed bus line or note, a row of two day bases, a lane-day with competing
reports, a day that would shrink, a fold whose every message had no id and so folded nothing,
a check finding (an --out holding no day file is one), a report with nothing to show; 2 could
not run: a missing flag, a bad flag value, a duplicate label, sqlite3 absent when
--opencode is given, a second fold holding the lock.

EXIT 1 STILL WRITES. A fold with one unreadable file writes every day it could compute
and exits 1: the exit code is about the claim -- a declared source is a claim that the
report covers it -- and written=true on the TOKENS DAY line is about the files.

Every path is a flag. There is no default output directory, no default transcript
directory, no default database, no default bus and no default rules file. fold, report
(its local mode), sum, check, sources, profiles and session read no environment variable
for a path or a setting: $HOME, $TMPDIR and $XDG_DATA_HOME are ignored, and a test sets
them and proves it. Three things do read the environment: --opencode runs sqlite3 found
on $PATH; and the two Redis verbs, ledger and report --redis, take the store's ACL user
from --user, else NOVA_SPRINT_REDIS_USER, and its password from the variable
--password-env names, else (with a user) the one NOVA_SPRINT_REDIS_PASSWORD_ENV names,
else NOVA_REDIS_BENCH_PASSWORD. The password is never a flag. --timeout is the one flag
with a default, 120 seconds, because it is how long this tool waits before saying so
rather than a fact about your data.

A source is declared by flag and every row names its sources, so every number in a day
file is traceable to the flags of the run that wrote it. A label is [a-z0-9-]+, at most
32 characters, and unique across the run. --scratch is required with --opencode and
refused without it, because a scratch directory with nothing to put in it is a flag that
does nothing. A fold or a report copies the database, with its -wal and -shm, into
--scratch/opencode-<label>/, replacing the copy there, and leaves it; the live file is
never opened, because sqlite3 keeps a WAL index beside the file it reads.

The five types -- input, output, cache_write, cache_read, reasoning -- are kept apart, and
a type the source did not report is written a dash, NEVER 0. A provider that does not expose
reasoning is not evidence that none occurred, and a zero meaning "not measured" would sum
into a month claiming to be complete. sum counts the dashes beside the totals. The same
holds for cost: usd= on a TOKENS AVG line is - when no source reported a cost for it.

A day that would go backwards is refused: TOKENS SHRANK names the type, what the file
said and what the sources say now, the file is left as it was, and --allow-shrink is the
person's act. A source that became unreadable must never quietly lower a day's spend.

A fold merges into the day file by SOURCE: it recomputes the rows its own declared sources
wrote and keeps every other row exactly as it is, so a run that declares one source does
not erase what the others reported. A row it can neither keep nor recompute -- one already
summed over a declared and an undeclared source -- is TOKENS PARTIAL, nothing of that day
is written, and --allow-shrink does not write it either.

Two notes for one day in one lane are one report only when the later names the earlier in
its subject: supersedes=<id>[,<id>...], sorted, no duplicates. Nothing else orders them --
not the Date, not the filename, not the directory listing, not the git history. Two tips
are TOKENS CONFLICT, nothing folds for that lane-day, and the remedy names every tip; one
note whose predecessor set names them all clears it.

--note <path> is written whole through atomicfile: the file and its directory must not be
symlinks.

fold and session hold --out/fold.lock while they write, so two folds of one --out never
write the same day at once; the second waits, then refuses naming the holder. The lock
file is empty and stays in --out after the run (it is never data); check counts it as
neither a day file nor a stray.

This tool removes nothing it was given. There is no month file, sum writes nothing, check
names a stray and leaves it, and no verb deletes, truncates or trims a file it did not make:
the one removal is the private database copy a dry run or sources made under --scratch.

check counts what it does not name. A calendar day between the first and the last with no
file is gap=<n>, and it is MISSING only when something says there was spend on it:
--strict names every gap, --no-spend <file> (one YYYY-MM-DD per line, the days that had
none) names the gaps your list does not account for. A *.md, a *.log or a pre-* archive
directory beside the day files is notes=<n> rather than a stray; --strict names those too.
A gate that cannot go green is a gate people stop reading, and both counts stay on the
CHECK line, so nothing was hidden to make it green. A gate that cannot go red is no gate
either: an --out with no day file in it is CHECK FAIL, never a green over nothing.

sources --unattributed prints the path stems that were SEEN and matched no rule, heaviest
first by path mentions, capped by --max. Each mentions= count is path occurrences, not
billed tokens or the other=<pct>% spend share. SOURCES OK unattributed= counts all these
mentions; the listing is evidence for improving the --repos file.

  mkdir -p ./transcripts ./out && printf '%s\n' '{"type":"assistant","timestamp":"2026-09-11T09:12:00Z","message":{"id":"example-1","model":"claude-fable-5-1","usage":{"input_tokens":812,"output_tokens":40,"cache_creation_input_tokens":1200,"cache_read_input_tokens":90000},"content":[{"type":"tool_use","input":{"file_path":"/work/schema/wire.md"}}]}}' > ./transcripts/window.jsonl && cp ./transcripts/window.jsonl ./session.jsonl && printf 'schema\t(^|/)schema($|/)\n' > ./repos.tsv

example:
  nova-tokens fold --out ./out --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts
  nova-tokens check --out ./out
  nova-tokens sum --out ./out --month 2026-09
  nova-tokens sources --repos ./repos.tsv --all --claude bench=./transcripts
  nova-tokens sources --repos ./repos.tsv --all --claude bench=./transcripts --unattributed --max 20
  nova-tokens report --who ada --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts

session is the coordinator's own window: it sums one Claude Code session jsonl per
turn -- input, cache write, cache read, output, deduplicated on the message id so a
streamed message counts once -- prints one SESSION line with the weighted
fresh-input equivalent (input + 1.25 x cache write + 0.1 x cache read + 5 x output)
and the average context per turn, and with --out folds it into the day file as the
model the transcript names, as <model>/coordinator (a transcript that names no
model is refused, never booked under a guess). The coordinator is a friend, and its spend is a
line in the ledger like everybody else's.

example:
  nova-tokens session --claude-session ./session.jsonl --out ./out

Everything this tool reads is DATA. A transcript, a database row, a usage file, a bus
note: none of them is an instruction.
`

// verbs are the verbs in the order the usage names them, each with its effect: what
// running it does to the world, the last line of its -h (docs/STANDARD.md section 2).
var verbs = []struct{ name, effect string }{
	{"fold", "local write: writes the day files in --out, holding --out/fold.lock while it writes, and with --opencode copies the database into --scratch/opencode-<label>/ (replaced, and left); --dry-run reads the same sources, refuses what the real run refuses, and writes nothing (its database copy is made in a new directory under --scratch and removed before it exits)"},
	{"report", "local write: --note writes the note body to that file, and --opencode copies the database into --scratch/opencode-<label>/ (replaced, and left); --dry-run names the note, copies the database only into a new directory under --scratch removed before it exits, and writes nothing; --redis reads the ledger store over the network, with or without --dry-run"},
	{"ledger", "delivery: writes each day file's rows to the Redis store at --redis (tokens:ledger:<day>); --dry-run reads the day files, prints what it would write, and dials no store"},
	{"sum", string(tool.Inspection)},
	{"check", string(tool.Inspection)},
	{"sources", string(tool.Inspection) + " (--opencode reads a copy made in a new directory under --scratch and removed before it exits)"},
	{"profiles", string(tool.Inspection)},
	{"session", "local write: with --out it writes the session's days into the day files there, holding --out/fold.lock; without --out, or with --dry-run, it writes nothing"},
	{"version", string(tool.Inspection)},
}

func verbNames() []string {
	names := make([]string, 0, len(verbs))
	for _, v := range verbs {
		names = append(names, v.name)
	}
	return names
}

// effectOf is the effect line a verb's -h ends its lines above the flags with.
func effectOf(verb string) string {
	for _, v := range verbs {
		if v.name == verb {
			return "effect: " + v.effect + "\n"
		}
	}
	return ""
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, time.Now().UTC())) }

// run is the whole tool, with its streams and clock injected so the tests can drive it.
// The clock is an argument and NOT a flag: the stamp on a day file is when the tool
// computed it, and a stamp a caller could set would be a stamp nobody could trust.
func run(args []string, stdout, stderr io.Writer, now time.Time) int {
	return runWith(args, stdout, stderr, now, newPrivateDir, tokens.HaveSQLite)
}

// runWith injects private-copy allocation and SQLite availability so either refusal
// can be exercised without depending on the host installation.
func runWith(args []string, stdout, stderr io.Writer, now time.Time, newPrivate privateDir, haveSQLite func() error) (code int) {
	// `<verb> -h` and `help <verb>` print that verb's help, its effect included, on stdout
	// at exit 0, before anything is read or written (the CLI style's rule (b)).
	defer verbflag.RecoverWith(stdout, "nova-tokens", usage, &code, effectOf)
	asJSON := verbflag.BoolAsked(args, "json")
	if len(args) == 0 {
		return refuse(stdout, stderr, asJSON, "no verb given; the verbs are "+verbflag.List(verbNames())+", and sources is the one that only looks")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "help", "-h", "--help":
		if verb == "help" && len(rest) > 0 && rest[0] != "help" && !verbflag.IsHelp(rest[0]) {
			// --help goes right after the verb: after a word or a -- it would be one.
			return runWith(append([]string{rest[0], "--help"}, rest[1:]...), stdout, stderr, now, newPrivate, haveSQLite)
		}
		fmt.Fprintf(stdout, "%s", usage)
		return 0
	case "fold":
		return cmdFold(rest, stdout, stderr, now, newPrivate, haveSQLite)
	case "report":
		return cmdReport(rest, stdout, stderr, now, newPrivate, haveSQLite)
	case "ledger":
		return cmdLedger(rest, stdout, stderr)
	case "sum":
		return cmdSum(rest, stdout, stderr, now)
	case "check":
		return cmdCheck(rest, stdout, stderr, now)
	case "sources":
		return cmdSources(rest, stdout, stderr, now, newPrivate, haveSQLite)
	case "profiles":
		return cmdProfiles(rest, stdout, stderr, now)
	case "session":
		return cmdSession(rest, stdout, stderr, now)
	case "version", "--version":
		return cmdVersion(rest, stdout, stderr)
	}
	near := ""
	if n := verbflag.Nearest(verb, verbNames()); n != "" {
		near = " did you mean " + n + "?"
	}
	return refuse(stdout, stderr, asJSON, "unknown verb "+strconv.Quote(verb)+";"+near+" the verbs are "+verbflag.List(verbNames()))
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

// wants is what each flag is FOR, in the words a refusal uses.
const (
	wantsOut     = "the directory the day files are written to"
	wantsRepos   = "a file of <name><TAB><regexp> lines, in priority order, naming your repos"
	wantsDay     = "one UTC day as YYYY-MM-DD, or --all for every day the sources name"
	wantsWho     = "the name this report is from, as the bus knows it"
	wantsMonth   = "one month as YYYY-MM"
	wantsSources = "--claude <label>=<dir>, --opencode <label>=<file>, --swarm <label>=<pool>, --bus <dir> or --provider <kind>:<label>=<file> (kind one of google, openai, xai)"
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
	// newPrivate makes the private copy directory under --scratch (newPrivateDir).
	newPrivate privateDir
	haveSQLite func() error
}

func (s *sourceFlags) declare(fs *flag.FlagSet, withSwarmAndBus bool) {
	s.claude.kind, s.opencode.kind, s.swarm.kind, s.provider.kind = "claude", "opencode", "swarm", "provider"
	fs.Var(&s.claude, "claude", "labeled Claude Code transcript directory; repeatable")
	fs.Var(&s.opencode, "opencode", "labeled OpenCode database file; repeatable")
	fs.Var(&s.provider, "provider", "kind:labeled provider export file; repeatable")
	if withSwarmAndBus {
		fs.Var(&s.swarm, "swarm", "labeled swarm pool directory; repeatable")
		fs.StringVar(&s.bus, "bus", "", "nova-bus directory with token notes")
	}
	fs.StringVar(&s.scratch, "scratch", "", "directory the OpenCode database is copied into: opencode-<label>/ in it, replaced and left by a run that writes; a new directory removed before exit by a dry run or sources")
	fs.StringVar(&s.repos, "repos", "", "tab-separated repo names and path regular expressions")
	fs.IntVar(&s.timeout, "timeout", int(tokens.DefaultTimeout/time.Second), "seconds to wait for the OpenCode sqlite3 reader")
}

// check validates the declared sources without reading any of them.
func (s *sourceFlags) check(r *refusals) {
	r.required("repos", s.repos, wantsRepos)
	seen := map[string]bool{}
	any := false
	for _, l := range []*labelled{&s.claude, &s.opencode, &s.swarm, &s.provider} {
		for _, it := range l.items {
			any = true
			// A provider's label is `<kind>:<label>`; every other flag's is the label
			// itself. Uniqueness is on the label -- the friend or the bench -- across
			// every kind, because the sources column names one of them per source.
			name := it.label
			if l.kind == "provider" {
				if _, after, ok := strings.Cut(it.label, ":"); ok {
					name = after
				}
			}
			if l.kind != "provider" && !validLabel(it.label) {
				r.add("--" + l.kind + " " + it.label + "=: a label is [a-z0-9-]+, at most 32 characters")
				continue
			}
			if seen[name] {
				r.add("the label " + name + " is used twice; two sources with one label would make the sources column a lie")
			}
			seen[name] = true
			if strings.TrimSpace(it.value) == "" {
				r.add("--" + l.kind + " " + it.label + "= has no path; it wants <label>=<path>")
			}
			if l.kind == "provider" {
				kind, name, ok := strings.Cut(it.label, ":")
				switch {
				case !ok:
					r.add("--provider " + it.label + "=: it wants <kind>:<label>=<path>, the kind one of " + strings.Join(tokens.Parsers, ", ") + " and the label the friend whose export it is, so that two friends' exports from one provider are two sources")
				case !tokens.KnownParser(kind):
					r.add("--provider " + it.label + ": the kind names the parser, and it wants one of " + strings.Join(tokens.Parsers, ", "))
				case !validLabel(name):
					r.add("--provider " + it.label + "=: a label is [a-z0-9-]+, at most 32 characters")
				}
			}
			switch l.kind {
			case "claude":
				if fi, err := os.Stat(it.value); err != nil || !fi.IsDir() {
					if err != nil && os.IsNotExist(err) {
						r.add("--claude " + it.label + "=" + it.value + " does not exist; it wants the directory the transcripts live under")
					} else if err != nil {
						r.add("--claude " + it.label + "=" + it.value + ": " + err.Error() + "; it wants the directory the transcripts live under")
					} else {
						r.add("--claude " + it.label + "=" + it.value + " is not a directory; it wants the directory the transcripts live under")
					}
				}
			case "swarm":
				if fi, err := os.Stat(it.value); err != nil || !fi.IsDir() {
					if err != nil && os.IsNotExist(err) {
						r.add("--swarm " + it.label + "=" + it.value + " does not exist; it wants the swarm pool directory")
					} else if err != nil {
						r.add("--swarm " + it.label + "=" + it.value + ": " + err.Error() + "; it wants the swarm pool directory")
					} else {
						r.add("--swarm " + it.label + "=" + it.value + " is not a directory; it wants the swarm pool directory")
					}
				}
			}
		}
	}
	if s.bus != "" {
		any = true
		if fi, err := os.Stat(s.bus); err != nil || !fi.IsDir() {
			if err != nil && os.IsNotExist(err) {
				r.add("--bus does not exist: " + s.bus + "; it wants the bus directory")
			} else if err != nil {
				r.add("--bus " + s.bus + ": " + err.Error() + "; it wants the bus directory")
			} else {
				r.add("--bus is not a directory: " + s.bus + "; it wants the bus directory")
			}
		}
	}
	if !any {
		r.add("at least one source flag is required; it wants " + wantsSources + "; refusing to guess")
	}
	if len(s.opencode.items) > 0 {
		if strings.TrimSpace(s.scratch) == "" {
			r.required("scratch", "", wantsScratch)
		} else if fi, err := os.Stat(s.scratch); err != nil || !fi.IsDir() {
			if err != nil && os.IsNotExist(err) {
				r.add("--scratch does not exist: " + s.scratch + "; it wants " + wantsScratch)
			} else if err != nil {
				r.add("--scratch " + s.scratch + ": " + err.Error() + "; it wants " + wantsScratch)
			} else {
				r.add("--scratch is not a directory: " + s.scratch + "; it wants " + wantsScratch)
			}
		}
		haveSQLite := s.haveSQLite
		if haveSQLite == nil {
			haveSQLite = tokens.HaveSQLite
		}
		if err := haveSQLite(); err != nil {
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
//
// An OpenCode database is read from a COPY, never in place: the live file is the one
// OpenCode is writing, and sqlite3 even read-only keeps a WAL index (-shm) beside the file
// it opens, while reading it as immutable would skip rows still in the WAL and count a
// different day. A run that writes copies into --scratch/opencode-<label>/, replacing what
// is there, and leaves the copy (rule 16). A run that writes nothing (private: sources, and
// every --dry-run) copies into a directory of its own made under --scratch
// (.nova-tokens-dry-run-*, new and private to the run, so no file there is ever truncated)
// and removes it before returning, so --scratch is as it was. A copy it could not remove
// is named in the returned notes.
func (s *sourceFlags) read(rules *tokens.Rules, now time.Time, private bool) (out []*tokens.Source, notes []string) {
	for _, it := range s.claude.items {
		out = append(out, tokens.ReadClaude(it.label, it.value, rules))
	}
	scratch := s.scratch
	if private && len(s.opencode.items) > 0 {
		dir, err := s.newPrivate(s.scratch)
		if err != nil {
			for _, it := range s.opencode.items {
				out = append(out, tokens.UnreadableOpenCode(it.label, it.value, "a private copy directory under --scratch: "+err.Error()))
			}
			scratch = ""
		} else {
			scratch = dir
			defer func() {
				if err := removePrivateCopy(dir); err != nil {
					notes = append(notes, "the private copy "+dir+" could not be removed: "+err.Error()+"; remove it by hand")
				}
			}()
		}
	}
	for _, it := range s.opencode.items {
		if scratch == "" {
			break
		}
		out = append(out, tokens.ReadOpenCode(it.label, it.value, scratch, time.Duration(s.timeout)*time.Second, rules))
	}
	for _, it := range s.swarm.items {
		out = append(out, tokens.ReadSwarm(it.label, it.value, rules))
	}
	for _, it := range s.provider.items {
		kind, name, _ := strings.Cut(it.label, ":")
		out = append(out, tokens.ReadProvider(kind, name, it.value, rules))
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
	return out, notes
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

// avgRate is a model-day's dollars per million tokens (usdMicro / tokens), for sorting the
// AVG listing highest first. A zero-token or unpriced model has no average and sorts below
// every real rate, which is never negative.
func avgRate(usdMicro, tokens int64, priced bool) float64 {
	if tokens == 0 || !priced {
		return -1
	}
	return float64(usdMicro) / float64(tokens)
}

// sourceLine is the line where a number becomes traceable: what each declared source
// opened, refused, counted and fed. A field that is not a measurement for the kind prints
// a dash, because a dash is an absence where a zero is a measurement.
func sourceLine(s *sink, token string, src *tokens.Source) string {
	kv := []any{"label", src.Label, "kind", src.Kind, "path", src.Path, "reports", src.ReportsList(), "day_basis", src.Basis}
	for _, f := range []string{"files", "unreadable", "messages", "dup", "noid", "nousage", "unparsed", "comments", "redated", "superseded", "rows"} {
		kv = append(kv, f, src.StatField(f))
	}
	return s.line(token, "SOURCE", "", kv...)
}

func unreadableLine(s *sink, token string, u tokens.Unreadable) string {
	return s.line(token, "UNREADABLE", oneline.Cap(u.Why, oneline.TailBytes), "label", u.Label, "path", u.Path)
}

func unparsedLine(s *sink, token string, u tokens.Unparsed) string {
	return s.line(token, "UNPARSED", oneline.Cap(u.Text, oneline.TailBytes), "label", u.Label, "note", u.Note, "line", u.Line)
}

// foldLists are fold's listings in the order their lines print, each capped by --max with
// its own MORE line; true is a list whose lines are findings, on stderr.
var foldLists = []struct {
	kind  string
	toErr bool
}{
	{"source", false}, {"unreadable", true}, {"unparsed", true}, {"superseded", false},
	{"conflict", true}, {"touched", false}, {"mixed", true}, {"day", false},
	{"shrank", true}, {"partial", true}, {"quiet", true},
}

// cmdFold is the wall: it reads every declared source whole and writes the days it could
// compute. It says NO when a source could not be read, a bus line or note did not parse,
// a row mixed two day bases, a lane-day had competing reports, or a day would have shrunk
// -- and it still writes the rest, because the exit code is about the claim. Under
// --dry-run it reads and decides exactly the same and writes nothing, the lock included.
func cmdFold(args []string, stdout, stderr io.Writer, now time.Time, newPrivate privateDir, haveSQLite func() error) int {
	fs := newFlagSet("fold")
	out := fs.String("out", "", "directory for daily token files")
	day := fs.String("day", "", "one UTC day to fold as YYYY-MM-DD")
	all := fs.Bool("all", false, "fold every day named by the sources")
	allowShrink := fs.Bool("allow-shrink", false, "write a day even when its totals shrink")
	max := fs.Int("max", bounded.Default, "maximum findings or rows to print; 0 prints all")
	dryRun := fs.Bool("dry-run", false, "read the sources and print what would be written, and write nothing (no day file, no lock)")
	var sf sourceFlags
	sf.declare(fs, true)
	s, code, ok := start(fs, args, "TOKENS", stdout, stderr)
	if !ok {
		return code
	}
	r := &refusals{token: "TOKENS", s: s}
	r.required("out", *out, wantsOut)
	checkDay(r, *day, *all)
	sf.haveSQLite = haveSQLite
	sf.check(r)
	checkMax(r, *max)
	if len(r.list) > 0 {
		return r.print(stderr)
	}
	fi, statErr := os.Stat(*out)
	if statErr != nil || !fi.IsDir() {
		if statErr != nil && os.IsNotExist(statErr) {
			r.add("--out does not exist: " + *out + "; it wants " + wantsOut)
		} else if statErr != nil {
			r.add("--out " + *out + ": " + statErr.Error() + "; it wants " + wantsOut)
		} else {
			r.add("--out is not a directory: " + *out + "; it wants " + wantsOut)
		}
		return r.print(stderr)
	}
	rules, err := tokens.LoadRules(sf.repos)
	if err != nil {
		r.add("--repos " + sf.repos + ": " + err.Error() + "; it wants " + wantsRepos)
		return r.print(stderr)
	}
	if !*dryRun {
		release, err := tokens.TakeFoldLock(*out, tokens.LockWait)
		if err != nil {
			r.add(err.Error())
			return r.print(stderr)
		}
		defer release()
	}

	sf.newPrivate = newPrivate
	sources, copyNotes := sf.read(rules, now, *dryRun)
	folder := tokens.NewFolder()
	for _, src := range sources {
		for _, m := range src.Stream {
			folder.Add(src.Label, m)
		}
	}

	fmt.Fprintln(s.out(), s.line("TOKENS", "FOLD", "", "at", stamp(now), "build", buildVersion(), "out", *out,
		"sources", len(sources), "days", daysAsked(*day, *all), "repos", sf.repos))

	lists := map[string]*bounded.List{}
	for _, l := range foldLists {
		lists[l.kind] = s.list(l.toErr, *max, "TOKENS", l.kind, maxRemedy("fold"))
	}
	conflictDays := map[string]bool{}
	// The labels this run declared: exactly what lands in a row's sources column, and so
	// exactly the rows this fold is entitled to recompute (rule 10).
	declared := make([]string, 0, len(sources))
	for _, src := range sources {
		declared = append(declared, src.Label)
		lists["source"].Line(sourceLine(s, "TOKENS", src))
	}
	lists["source"].More()
	for _, src := range sources {
		for _, u := range src.Unreadables {
			lists["unreadable"].Line(unreadableLine(s, "TOKENS", u))
		}
	}
	// The unreadable listing is NOT closed here: a day file this run could not WRITE is an
	// unreadable too (below), and its MORE line and its count are closed after the last
	// line either can get.
	for _, src := range sources {
		for _, u := range src.Unparseds {
			lists["unparsed"].Line(unparsedLine(s, "TOKENS", u))
		}
	}
	lists["unparsed"].More()
	for _, src := range sources {
		for _, sp := range src.Supersededs {
			lists["superseded"].Line(s.line("TOKENS", "SUPERSEDED", "", "label", sp.Label, "note", sp.Note, "by", sp.By, "day", sp.Day))
		}
	}
	lists["superseded"].More()
	for _, src := range sources {
		for _, c := range src.Conflicts {
			conflictDays[c.Day] = true
			notes := strings.Join(c.Notes, ",")
			lists["conflict"].Line(s.line("TOKENS", "CONFLICT", "competing reports; send a correction whose subject carries supersedes="+oneline.Field(notes),
				"label", c.Label, "day", c.Day, "notes", notes))
		}
	}
	lists["conflict"].More()
	for _, src := range sources {
		for _, t := range src.Toucheds {
			lists["touched"].Line(s.line("TOKENS", "TOUCHED", "", "label", t.Label, "day", t.Day, "repos", strings.Join(t.Repos, ",")))
		}
	}
	lists["touched"].More()

	days := folder.Days()
	if !*all {
		days = []string{*day}
	}
	daysWritten, rowsWritten, quiet := 0, 0, 0
	mixedLabels := "-"
	firstPartial, firstQuiet := "", ""
	for _, d := range days {
		rows, mixed := folder.DayRows(d)
		for _, m := range mixed {
			if mixedLabels == "-" && len(m.Labels) > 0 {
				mixedLabels = strings.Join(m.Labels, " and ")
			}
			lists["mixed"].Line(s.line("TOKENS", "MIXED", "two day bases on one row; declare one export for that day",
				"date", m.Day, "model", m.Model, "repo", m.Repo, "bases", strings.Join(m.Bases, ",")))
		}
		outPath := tokens.Path(*out, d)
		old, findings, readErr := tokens.ReadDayFile(outPath)
		if len(rows) == 0 && !conflictDays[d] && readErr != nil && os.IsNotExist(readErr) {
			continue
		}
		file := buildDayFile(d, rows, folder, now)
		written, wouldWrite := false, false
		shrank := false
		partial := false
		var dayShrinks []tokens.Shrink
		if !conflictDays[d] {
			switch {
			case readErr != nil && !os.IsNotExist(readErr):
				lists["unreadable"].Line(unreadableLine(s, "TOKENS", tokens.Unreadable{Label: "out", Path: outPath, Why: readErr.Error()}))
			case readErr == nil && len(findings) > 0:
				for _, f := range findings {
					why := oneline.Escape(f.Reason)
					if f.Line > 0 {
						why = fmt.Sprintf("line %d: %s", f.Line, oneline.Escape(f.Reason))
					}
					lists["unreadable"].Line(unreadableLine(s, "TOKENS", tokens.Unreadable{Label: "out", Path: outPath, Why: why}))
				}
			case readErr == nil && len(findings) == 0:
				// Merge by source BEFORE anything else touches the file: a row no declared
				// source wrote is carried over, a row they all wrote is replaced, and a row
				// this fold can neither keep nor recompute refuses the day. Rule 10 then
				// compares the file with the MERGED file, which is like with like.
				merged, retained, partials := tokens.MergeDay(old.Rows, file.Rows, declared)
				for _, pt := range partials {
					partial = true
					lists["partial"].Line(s.line("TOKENS", "PARTIAL", "this fold declared only some of the sources that wrote the row; declare every source in the file's sources= line, or fold this day into its own --out",
						"date", pt.Day, "model", pt.Model, "repo", pt.Repo, "sources", strings.Join(pt.Sources, ","),
						"folded", strings.Join(pt.Folded, ","), "written", false))
					if firstPartial == "" {
						firstPartial = pt.Model + " on " + pt.Repo + " for " + pt.Day
					}
				}
				if !partial {
					file.Rows = merged
					file.Sources = tokens.SourcesOf(merged)
					if retained > 0 {
						// turns= counts the messages THIS run read and cannot be split per
						// source, so a file holding a row this run did not read is a file
						// whose turns nobody can state.
						file.Turns = tokens.Dash
					}
					dayShrinks = tokens.Shrinks(old.Totals(), file.Totals(), d)
					shrank = len(dayShrinks) > 0
				}
				// A declared source with zero samples for an explicitly selected existing
				// day is quiet: the day file still names it, and the fold prints a bounded
				// line naming it rather than letting the refusal speak only in day totals.
				for _, src := range sources {
					fedDay := slices.ContainsFunc(src.Stream, func(m tokens.Message) bool { return m.Day == d })
					if fedDay || !slices.Contains(old.Sources, src.Label) {
						continue
					}
					quiet++
					if firstQuiet == "" {
						firstQuiet = src.Label + " on " + d
					}
					lists["quiet"].Line(s.line("TOKENS", "QUIET", "a declared source has zero samples for an explicitly selected existing day",
						"label", src.Label, "day", d))
				}
			}
			// --allow-shrink is a person's word about a day going backwards. It is NOT a
			// word about a row this fold cannot compute, so it does not override a partial,
			// and not a word about replacing a malformed file. Absent and empty are one
			// state: a fold never writes a day file with no rows.
			wouldWrite = !partial && (!shrank || *allowShrink) && len(file.Rows) > 0 &&
				(readErr == nil && len(findings) == 0 || readErr != nil && os.IsNotExist(readErr))
			switch {
			case wouldWrite && *dryRun:
				daysWritten++
				rowsWritten += len(rows)
			case wouldWrite:
				if err := file.Save(*out); err != nil {
					lists["unreadable"].Line(unreadableLine(s, "TOKENS", tokens.Unreadable{Label: "out", Path: outPath, Why: err.Error()}))
				} else {
					written = true
					daysWritten++
					rowsWritten += len(rows)
				}
			}
			for _, sh := range dayShrinks {
				lists["shrank"].Line(s.line("TOKENS", "SHRANK", "a source went quiet; --allow-shrink writes it anyway",
					"date", sh.Day, "type", tokens.TypeNames[sh.Type], "file", sh.File, "now", sh.Now, "written", written))
			}
		}
		lists["day"].Line(dayLine(s, d, file, written, *dryRun, wouldWrite))
	}
	for _, kind := range []string{"unreadable", "mixed", "day", "shrank", "partial", "quiet"} {
		lists[kind].More()
	}

	n := func(kind string) int { return lists[kind].Total() }
	counts := []any{"days", daysWritten, "rows", rowsWritten, "sources", len(sources), "unreadable", n("unreadable"),
		"unparsed", n("unparsed"), "mixed", n("mixed"), "conflict", n("conflict"), "shrank", n("shrank"),
		"partial", n("partial"), "quiet", quiet}
	if *dryRun {
		counts = append(counts, "dry_run", true)
	}
	// A FOLD THAT DROPPED EVERY MESSAGE FOLDED NOTHING, AND A GATE READING THE EXIT CODE MUST
	// SEE IT. Some messages dropped is a TOKENS NOTE (the day is short and the note says
	// so); every message dropped, with none folded, is a fold that did not do its job:
	// exit 1 with the counts.
	dropped, of, allDropped := allMessagesDropped(sources)
	bad := n("unreadable") > 0 || n("unparsed") > 0 || n("mixed") > 0 || n("conflict") > 0 ||
		(n("shrank") > 0 && !*allowShrink) || n("partial") > 0 || allDropped
	if allDropped {
		fmt.Fprintf(s.err(), "FOLD FAIL dropped=%d of %d: %s\n", dropped, of, allDroppedWhy)
		s.o.Why = append(s.o.Why, fmt.Sprintf("dropped=%d of %d: %s", dropped, of, allDroppedWhy))
	}
	if bad {
		fmt.Fprintf(s.err(), "TOKENS FAIL%s\n", s.factFields(counts...))
	} else {
		fmt.Fprintf(s.out(), "TOKENS OK%s\n", s.factFields(counts...))
	}
	note := remedy(sources, folder.Overlaps(), n("unreadable"), n("unparsed"), n("mixed"), n("conflict"), n("shrank"), n("partial"), quiet,
		*allowShrink, *out, mixedLabels, firstPartial, firstQuiet)
	fmt.Fprintf(s.out(), "TOKENS NOTE %s\n", oneline.Escape(note))
	s.note(note)
	copyNote(s, "TOKENS", copyNotes)
	if bad {
		return s.done(1, *max)
	}
	return s.done(0, *max)
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
	f.Sources = slices.Sorted(maps.Keys(labels))
	return f
}

// dayLine is one line per day written or refused, and the two shares on it are how a
// person sees whether the rules file is good enough. Under --dry-run nothing is written,
// and would_write says whether the real run would have written the day.
func dayLine(s *sink, day string, file *tokens.DayFile, written, dryRun, wouldWrite bool) string {
	models, repos := map[string]bool{}, map[string]bool{}
	var whole, unknown, other int64
	dashes, nonutc := 0, 0
	rough := 0
	for _, r := range file.Rows {
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
		if r.Basis != tokens.UTC {
			nonutc++
		}
	}
	kv := []any{"date", day, "rows", len(file.Rows), "models", len(models), "repos", len(repos), "turns", file.Turns,
		"unknown", tokens.Percent(unknown, whole) + "%", "other", tokens.Percent(other, whole) + "%",
		"rough", rough, "dashes", dashes, "nonutc", nonutc, "sources", strings.Join(file.Sources, ","), "written", written}
	if dryRun {
		kv = append(kv, "would_write", wouldWrite)
	}
	return s.line("TOKENS", "DAY", "", kv...)
}

// remedy is the ONE line TOKENS NOTE carries. It names the label and the act, in the order
// a reader would act on them, and when nothing was wrong it names the gate.
func remedy(sources []*tokens.Source, overlaps []tokens.Overlap, unreadable, unparsed, mixed, conflict, shrank, partial, quiet int, allowShrink bool, out, mixedLabels, firstPartial, firstQuiet string) string {
	switch {
	case unreadable > 0:
		return "a declared source could not be read whole (" + firstUnreadableLabel(sources) + "): open those files to this group, or drop the flag -- a declared source is a claim that the report covers it"
	case unparsed > 0:
		// The advice is for the KIND that failed. Every unparsed was a bus line once, and
		// a swarm usage file refused by its header was told the shape of a bus body line.
		kind, note, own := firstUnparsed(sources)
		if own != "" {
			return own
		}
		switch kind {
		case tokens.KindSwarm:
			return "a swarm usage file did not parse (" + note + "): its header is the sixteen columns SPEC-SWARM rule 12 names, in order -- " + strings.Join(tokens.SwarmColumns, ", ")
		case tokens.KindProvider:
			return "a line of a billing export did not parse (" + note + "): the parser is the kind in --provider <kind>:<label>=<path>, and a row carries the columns that kind declares"
		case tokens.KindClaude, tokens.KindOpenCode:
			return "a message's stamp did not parse (" + note + "): a day comes from the message's own RFC 3339 stamp, and this tool dates nothing by a guess"
		}
		return "a bus line or note did not parse (" + note + "): a body line is date<TAB>who<TAB>model<TAB>repo<TAB>type<TAB>count, with an optional day_basis=<zone>"
	case conflict > 0:
		return "a lane-day has competing reports (" + firstConflictLabel(sources) + "): one note whose subject carries supersedes=<every tip, sorted> is the replacement snapshot that clears it"
	case mixed > 0:
		// The two labels, because "declare one export for that day" is not an act until
		// the caller knows which two are competing. Every other branch of this switch
		// names a label, a note or a lane; this one named nothing.
		return "a row was fed by two day bases (" + mixedLabels + "): declare one of those two for that day, not both"
	case partial > 0:
		// Above the shrank branches: a row this fold cannot compute is not a day going
		// backwards, and --allow-shrink is not the act that clears it.
		return "a row of the day file was written by sources this fold did not declare (" + firstPartial + "): declare every source in that file's sources= line, or fold this day into its own --out -- --allow-shrink does not write it"
	case shrank > 0 && !allowShrink:
		return "a day would have gone backwards and was left as it was: --allow-shrink writes it anyway, and it is a person's act"
	case shrank > 0:
		return "a day was written smaller at your word (--allow-shrink); nova-tokens check --out " + out + " is the gate"
	case quiet > 0:
		// A quiet source is not a failure, and it is not "nothing was wrong" either: the
		// day file names a source this run declared and read nothing from for that day.
		return "a declared source fed no message for a day its file names (" + firstQuiet + "): its rows there were recomputed from nothing; if it did spend that day, its files are not under the path you declared"
	case noidAndDup(sources) != "":
		return noidAndDup(sources) + "; those messages are NOT in any row"
	case len(overlaps) > 0:
		o := overlaps[0]
		return "two declared sources fed the same " + strconv.Itoa(o.IDs) + " message ids (" + o.A + " and " + o.B + "): those messages are counted TWICE, because this fold does not de-duplicate across sources; one harness is one source flag, and a scratch tree under a declared directory holds the same transcripts again"
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

// firstUnparsed names the kind of the first source with an unparsed line and what it was
// reading, so that the one remedy line is the remedy for the thing that failed.
// noidAndDup is the sentence for spend that was read and then dropped: a message with no
// id is not folded (rule 4) and a repeated id is counted once. Both are numbers on a green
// TOKENS SOURCE line and nowhere else, and 100% of a file's usage can be a message with no
// id (lesson 95: a number is not a sentence).
func noidAndDup(sources []*tokens.Source) string {
	noid, dup, label := 0, 0, "-"
	for _, s := range sources {
		if s.Stat.NoID > 0 || s.Stat.Dup > 0 {
			if label == "-" {
				label = s.Label
			}
			noid += s.Stat.NoID
			dup += s.Stat.Dup
		}
	}
	if noid == 0 {
		return ""
	}
	return "a source fed " + strconv.Itoa(noid) + " messages with no id (" + label + "): a message is counted by its id (rule 4), and one with none is noid= and is not folded"
}

// allDroppedWhy is the tail of the TOKENS FAIL line for a fold that dropped every message.
const allDroppedWhy = "no message had an id, so none was folded (a message is counted by its id: a transcript's message.id, an opencode message id, a swarm row's job); run: nova-tokens sources <the same source flags> --day <d> to see noid= per source"

// allMessagesDropped says whether the sources read at least one message and dropped every
// one of them for having no id (rule 4): the count dropped, the count read (dropped, plus
// the messages folded), and whether that is the whole of it. Some dropped and some folded
// is false: the TOKENS NOTE names that one.
func allMessagesDropped(sources []*tokens.Source) (dropped, of int, all bool) {
	folded := 0
	for _, s := range sources {
		dropped += s.Stat.NoID
		folded += len(s.Stream)
	}
	return dropped, dropped + folded, dropped > 0 && folded == 0
}

func firstUnparsed(sources []*tokens.Source) (kind, note, own string) {
	for _, s := range sources {
		if len(s.Unparseds) > 0 {
			return s.Kind, s.Unparseds[0].Note, s.Unparseds[0].Remedy
		}
	}
	return "", "-", ""
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
func cmdSources(args []string, stdout, stderr io.Writer, now time.Time, newPrivate privateDir, haveSQLite func() error) int {
	fs := newFlagSet("sources")
	day := fs.String("day", "", "one UTC day to inspect as YYYY-MM-DD")
	all := fs.Bool("all", false, "inspect every day named by the sources")
	max := fs.Int("max", bounded.Default, "maximum rows to print; 0 prints all")
	unattributed := fs.Bool("unattributed", false, "list seen paths that matched no repo rule")
	var sf sourceFlags
	sf.declare(fs, true)
	s, code, ok := start(fs, args, "SOURCES", stdout, stderr)
	if !ok {
		return code
	}
	r := &refusals{token: "SOURCES", s: s}
	checkDay(r, *day, *all)
	sf.haveSQLite = haveSQLite
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
	// The tally is switched on BEFORE any source is read, because it is taken inside the
	// attribution ladder as the paths go past; there is no second walk of the transcripts.
	if *unattributed {
		rules.WatchUnattributed()
		if !*all {
			rules.FilterDay(*day)
		}
	}
	sf.newPrivate = newPrivate
	sources, copyNotes := sf.read(rules, now, true)
	if !*all {
		// When --day is specified (without --all), message counts, row keys, and unattributed
		// path tallies are scoped to the selected day. Source-wide inventory and error metadata
		// (files, unreadable sources, and unparsed lines) remain source-wide because unreadable
		// or unparsed files may lack valid dates and describe properties of the declared source.
		for _, src := range sources {
			var dayStream []tokens.Message
			keys := map[tokens.Key]bool{}
			for _, m := range src.Stream {
				if m.Day == *day {
					dayStream = append(dayStream, m)
					keys[tokens.Key{Day: m.Day, Model: m.Model, Repo: m.Repo}] = true
				}
			}
			src.Stream = dayStream
			if tokens.Applies(src.Kind, "messages") {
				src.Stat.Messages = len(dayStream)
			}
			src.Stat.Rows = len(keys)
		}
	}

	srcList := s.list(false, *max, "SOURCES", "source", maxRemedy("sources"))
	unreadable := s.list(true, *max, "SOURCES", "unreadable", maxRemedy("sources"))
	unparsed := s.list(true, *max, "SOURCES", "unparsed", maxRemedy("sources"))
	stems := s.list(false, *max, "SOURCES", "unattributed", maxRemedy("sources"))
	files, messages, rows := 0, 0, 0
	for _, src := range sources {
		srcList.Line(sourceLine(s, "SOURCES", src))
		files += src.Stat.Files
		messages += src.Stat.Messages
		rows += src.Stat.Rows
	}
	srcList.More()
	for _, src := range sources {
		for _, u := range src.Unreadables {
			unreadable.Line(unreadableLine(s, "SOURCES", u))
		}
	}
	unreadable.More()
	for _, src := range sources {
		for _, u := range src.Unparseds {
			unparsed.Line(unparsedLine(s, "SOURCES", u))
		}
	}
	unparsed.More()
	// The listing that says WHICH paths `other` is made of. Without it a person reads
	// `other=81%` on a day line and has nowhere to go but grep; with it the top stems ARE
	// the rules the file is missing, written in the shape a rule matches.
	unattributedField := tokens.Dash
	if *unattributed {
		for _, u := range rules.Unattributed() {
			stems.Line(s.line("SOURCES", "UNATTRIBUTED", "", "stem", u.Stem, "mentions", u.Count))
		}
		stems.More()
		unattributedField = strconv.Itoa(rules.TotalUnattributed())
	}
	counts := []any{"sources", len(sources), "files", files, "messages", messages, "unreadable", unreadable.Total(),
		"unparsed", unparsed.Total(), "rows", rows, "unattributed", unattributedField}
	fmt.Fprintf(s.out(), "SOURCES OK%s\n", s.factFields(counts...))
	copyNote(s, "SOURCES", copyNotes)
	return s.done(0, *max)
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
func cmdReport(args []string, stdout, stderr io.Writer, now time.Time, newPrivate privateDir, haveSQLite func() error) int {
	fs := newFlagSet("report")
	who := fs.String("who", "", "name to write in each note body row")
	day := fs.String("day", "", "one UTC day to report as YYYY-MM-DD")
	notePath := fs.String("note", "", "atomically write the note body to this path")
	var supersedes stringList
	fs.Var(&supersedes, "supersedes", "note id this report replaces; repeatable")
	max := fs.Int("max", bounded.Default, "maximum summary rows to print; 0 prints all")
	monthFlag := fs.String("month", "", "month to summarize as YYYY-MM")
	byFlag := fs.String("by", "model", "Redis summary grouping: model, repo, day or tuple")
	redisAddr := fs.String("redis", "", "Redis address for the store summary mode")
	redisUser := fs.String("user", "", "Redis username for the store summary mode")
	passwordEnv := fs.String("password-env", "", "environment variable holding the Redis password")
	dryRun := fs.Bool("dry-run", false, "print the body and name the --note file, and write no file")
	var sf sourceFlags
	sf.declare(fs, true)
	s, code, ok := start(fs, args, "REPORT", stdout, stderr)
	if !ok {
		return code
	}
	if *redisAddr != "" {
		return cmdReportStore(s, *redisAddr, *redisUser, *passwordEnv, *monthFlag, *byFlag, *max, stderr)
	}
	if *monthFlag != "" {
		return (&refusals{token: "REPORT", s: s, list: []string{"--month is the store's month report; it wants --redis <host:port>"}}).print(stderr)
	}
	r := &refusals{token: "REPORT", s: s}
	r.required("who", *who, wantsWho)
	switch {
	case *day == "":
		r.add("--day is required; it wants " + wantsDay + "; refusing to guess")
	case !tokens.ValidDay(*day):
		r.add("--day is not a day: " + *day + "; it wants " + wantsDay)
	}
	sf.haveSQLite = haveSQLite
	sf.check(r)
	checkMax(r, *max)
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
	sorted := slices.Sorted(slices.Values(supersedes))

	sf.newPrivate = newPrivate
	sources, copyNotes := sf.read(rules, now, *dryRun)
	folder := tokens.NewFolder()
	for _, src := range sources {
		for _, m := range src.Stream {
			folder.Add(src.Label, m)
		}
	}
	// Rule 20: report "folds that machine's own sources for one day, the same sources and
	// the same attribution as fold", and that includes what the fold SAYS about them: an
	// unreadable file, a line that did not parse and a message with no id each leave a
	// line here (rule 3: counted and printed, never skipped silently).
	unreadable, unparsed := 0, 0
	for _, src := range sources {
		for _, u := range src.Unreadables {
			fmt.Fprintln(s.err(), unreadableLine(s, "TOKENS", u))
			unreadable++
		}
	}
	for _, src := range sources {
		for _, u := range src.Unparseds {
			fmt.Fprintln(s.err(), unparsedLine(s, "TOKENS", u))
			unparsed++
		}
	}
	// A message the fold could not count by id is not an unparsed line and is not a
	// refusal; it is spend that was read and then dropped, and fold names it on its one
	// remedy line. So does this verb.
	if dropped := noidAndDup(sources); dropped != "" {
		fmt.Fprintf(s.err(), "TOKENS NOTE %s\n", oneline.Escape(dropped))
		s.note(dropped)
	}
	copyNote(s, "TOKENS", copyNotes)
	rows, mixed := folder.DayRows(*day)
	for _, m := range mixed {
		fmt.Fprintln(s.err(), s.line("TOKENS", "MIXED", "two day bases on one row; declare one export for that day",
			"date", m.Day, "model", m.Model, "repo", m.Repo, "bases", strings.Join(m.Bases, ",")))
	}
	// A mixed key is not in `rows` at all (Folder.DayRows keeps them apart), so the body
	// below is exactly "no line for that key" and every other key's lines.
	var rendered []string
	for _, row := range rows {
		for t := tokens.Type(0); t < tokens.NTypes; t++ {
			v, ok := row.Counts.Get(t)
			if !ok {
				continue
			}
			rendered = append(rendered, tokens.BodyLine(*day, *who, row.Model, row.Repo, t, v, row.Basis()))
		}
	}
	lines := len(rendered)
	body := ""
	if lines > 0 {
		body = strings.Join(rendered, "\n") + "\n"
	}
	s.o.Payload = body
	s.fact("who", *who)
	s.fact("day", *day)
	s.fact("rows", lines)
	if lines == 0 || len(mixed) > 0 || unreadable > 0 || unparsed > 0 {
		// A friend with nothing to show says so, and never sends zeros. A day that is
		// short -- a declared source not read whole (rule 3), a mixed key -- is a FAIL
		// too: the closing word, the exit code and the counts on it agree with the lines
		// above it. A REPORT FAIL writes nothing: an existing --note file is left
		// byte-unchanged. The body still prints, so the friend sees what the other keys
		// came to: the spec's sentence for a mixed key is "no line for that key".
		if lines > 0 {
			fmt.Fprint(s.out(), body)
		}
		fmt.Fprintf(s.err(), "REPORT FAIL who=%s day=%s rows=%d unreadable=%d unparsed=%d\n",
			oneline.Field(*who), oneline.Field(*day), lines, unreadable, unparsed)
		s.fact("unreadable", unreadable)
		s.fact("unparsed", unparsed)
		return s.done(1, *max)
	}
	fmt.Fprint(s.out(), body)
	if *notePath != "" {
		// A dry run checks the note's path exactly as the write would (atomicfile.Check)
		// and refuses what it refuses; only the write itself is skipped.
		write := func() error { return atomicfile.Write(filepath.Clean(*notePath), []byte(body), 0o644) }
		if *dryRun {
			write = func() error { return atomicfile.Check(filepath.Clean(*notePath), 0o644) }
		}
		if err := write(); err != nil {
			r.add("--note " + *notePath + ": " + err.Error())
			return r.print(stderr)
		}
	}
	// One TOKENS AVG line per model, after the body lines: the daily blended cost per
	// token, summed over every repo the model wrote that day. Four types count toward
	// tokens (input, output, cache write, cache read); reasoning is its own column and is
	// not in the denominator. usd= is the cost the sources reported, usd_per_mtok= divides
	// it by the tokens that cost covers and no others, and unpriced= counts the tokens no
	// source priced. A model no source priced prints usd=- and usd_per_mtok=-: a cost
	// nobody reported is no measurement, and never a zero.
	type modelAvg struct {
		name         string // provider/model, or model where no source named a provider
		tokens       int64  // every billed token the model's rows hold
		pricedTokens int64  // the tokens the reported cost covers
		usd          int64
		priced       bool
	}
	avgs := map[string]*modelAvg{}
	for _, row := range rows {
		name := row.Model
		if row.Provider != "" {
			name = row.Provider + "/" + row.Model
		}
		a, ok := avgs[name]
		if !ok {
			a = &modelAvg{name: name}
			avgs[name] = a
		}
		a.tokens += row.Counts.Billed()
		a.pricedTokens += row.PricedTokens
		a.usd += row.Usd
		a.priced = a.priced || row.Priced
	}
	sortedAvg := slices.Collect(maps.Values(avgs))
	sort.Slice(sortedAvg, func(i, j int) bool {
		pi := avgRate(sortedAvg[i].usd, sortedAvg[i].pricedTokens, sortedAvg[i].priced)
		pj := avgRate(sortedAvg[j].usd, sortedAvg[j].pricedTokens, sortedAvg[j].priced)
		if pi != pj {
			return pi > pj
		}
		return sortedAvg[i].name < sortedAvg[j].name
	})
	avgList := s.list(true, *max, "TOKENS", "avg", maxRemedy("report"))
	var allTokens, allPricedTokens, allUsd int64
	allPriced := false
	for _, a := range sortedAvg {
		allTokens += a.tokens
		allPricedTokens += a.pricedTokens
		allUsd += a.usd
		allPriced = allPriced || a.priced
		avgList.Line(s.line("TOKENS", "AVG", "", "day", *day, "model", a.name, "tokens", a.tokens,
			"usd", usdCell(a.usd, a.priced), "usd_per_mtok", usdPerMtokCell(a.usd, a.pricedTokens, a.priced),
			"unpriced", a.tokens-a.pricedTokens))
	}
	avgList.More()
	fmt.Fprintln(s.err(), s.line("TOKENS", "AVG-ALL", "", "day", *day, "tokens", allTokens,
		"usd", usdCell(allUsd, allPriced), "usd_per_mtok", usdPerMtokCell(allUsd, allPricedTokens, allPriced),
		"unpriced", allTokens-allPricedTokens))
	// The OK line is the grammar's, field for field: every source was read whole. Under
	// --dry-run --note was not written, and the line says so.
	subject := tokens.Subject(*day, stamp(now), buildVersion(), sorted)
	fmt.Fprintf(s.err(), "REPORT OK who=%s day=%s rows=%d at=%s build=%s%s subject=%s\n",
		oneline.Field(*who), oneline.Field(*day), lines, oneline.Field(stamp(now)),
		oneline.Field(buildVersion()), s.dryRunFields(*dryRun, "note", *notePath), oneline.Escape(subject))
	s.fact("at", stamp(now))
	s.fact("build", buildVersion())
	s.fact("subject", tool.Text(subject))
	return s.done(0, *max)
}

// usdCell is a cost as a field: the dollars, or - when no source reported one.
func usdCell(micro int64, priced bool) string {
	if !priced {
		return tokens.Dash
	}
	return tokens.Usd(micro)
}

// usdPerMtokCell is the blended rate as a field: - when no source reported a cost, or the
// model had no tokens to divide by.
func usdPerMtokCell(micro, n int64, priced bool) string {
	if !priced {
		return tokens.Dash
	}
	return tokens.UsdPerMtok(micro, n)
}

// ------------------------------------------------------------------------------- sum

// cmdSum ASSERTS NOTHING and is never a gate. It exits 0 whenever it ran, including over a
// month with gaps, because answering is its job and missing=<n> is the answer.
func cmdSum(args []string, stdout, stderr io.Writer, now time.Time) int {
	fs := newFlagSet("sum")
	out := fs.String("out", "", "directory holding daily token files")
	month := fs.String("month", "", "month to sum as YYYY-MM")
	max := fs.Int("max", bounded.Default, "maximum rows to print; 0 prints all")
	s, code, ok := start(fs, args, "SUM", stdout, stderr)
	if !ok {
		return code
	}
	r := &refusals{token: "SUM", s: s}
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
	sm, err := tokens.SumMonth(*out, *month)
	if err != nil {
		r.add(err.Error())
		return r.print(stderr)
	}
	turns := tokens.Dash
	if sm.HaveTurn {
		turns = strconv.Itoa(sm.Turns)
	}
	first, last := tokens.Dash, tokens.Dash
	if len(sm.Days) > 0 {
		first, last = sm.Days[0], sm.Days[len(sm.Days)-1]
	}
	fmt.Fprintln(s.out(), s.line("SUM", "MONTH", "", "month", *month, "at", stamp(now), "build", buildVersion(),
		"days", len(sm.Days), "first", first, "last", last, "missing", len(sm.Missing), "rows", sm.Rows, "turns", turns))

	widen := "nova-tokens sum --out " + *out + " --month " + *month + " --max 0"
	pairs := s.list(false, *max, "SUM", "pair", widen)
	for _, p := range sm.Pairs {
		pairs.Line(s.line("SUM", "PAIR", "", append(append([]any{"model", p.Model, "repo", p.Repo}, aggFields(p.Agg)...), "days", p.Agg.Days())...))
	}
	pairs.More()
	models := s.list(false, *max, "SUM", "model", widen)
	for _, m := range sm.Models {
		models.Line(s.line("SUM", "MODEL", "", append(append([]any{"model", m.Model}, aggFields(m.Agg)...), "repos", m.Agg.Keys())...))
	}
	models.More()
	fmt.Fprintln(s.out(), s.line("SUM", "TOTAL", "", append(aggFields(sm.Total), "turns", turns, "pairs", len(sm.Pairs), "models", len(sm.Models))...))
	counts := []any{"month", *month, "days", len(sm.Days), "missing", len(sm.Missing), "pairs", len(sm.Pairs), "models", len(sm.Models), "nonutc", sm.Total.NonUTC}
	fmt.Fprintf(s.out(), "SUM OK%s\n", s.factFields(counts...))
	return s.done(0, *max)
}

// aggFields is the five totals, the rough count, the per-column dash counts and the
// non-UTC count: everything a reader needs to know what a total does NOT cover.
func aggFields(a *tokens.Agg) []any {
	return []any{"input", a.Cell(tokens.Input), "output", a.Cell(tokens.Output), "cache_write", a.Cell(tokens.CacheWrite),
		"cache_read", a.Cell(tokens.CacheRead), "reasoning", a.Cell(tokens.Reasoning), "rough", a.Rough,
		"dashes", fmt.Sprintf("%d,%d,%d,%d,%d", a.Dashes[tokens.Input], a.Dashes[tokens.Output], a.Dashes[tokens.CacheWrite],
			a.Dashes[tokens.CacheRead], a.Dashes[tokens.Reasoning]), "nonutc", a.NonUTC}
}

func validMonth(m string) bool {
	return tokens.ValidMonth(m)
}

// ----------------------------------------------------------------------------- check

// cmdCheck is the GATE. It says NO on any malformed file, any malformed row, any missing
// day and any stray, and it prints the count line either way. A missing day is NAMED and
// never filled: nobody folded it, and this tool does not invent what nobody measured.
//
// What `missing` and `stray` MEAN is internal/tokens/check.go's paragraph, and the short
// of it is that a calendar gap and a person's README are counted here (gap=, notes=) and
// named only under --strict or a --no-spend list. A gate that cannot go green is a gate
// people learn to skip, and this one could not: 40 findings on reports/tokens, none of
// them work anybody would do.
func cmdCheck(args []string, stdout, stderr io.Writer, now time.Time) int {
	fs := newFlagSet("check")
	out := fs.String("out", "", "directory containing daily token files")
	max := fs.Int("max", bounded.Default, "maximum findings to print; 0 prints all")
	strict := fs.Bool("strict", false, "treat every gap and note as a finding")
	noSpend := fs.String("no-spend", "", "file listing UTC dates with no spend, one per line")
	through := fs.String("through", "", "require coverage through this UTC day, YYYY-MM-DD")
	s, code, ok := start(fs, args, "CHECK", stdout, stderr)
	if !ok {
		return code
	}
	r := &refusals{token: "CHECK", s: s}
	r.required("out", *out, wantsOut)
	checkMax(r, *max)
	if *strict && strings.TrimSpace(*noSpend) != "" {
		r.add("--strict and --no-spend are two answers to one question: --strict names every calendar gap, --no-spend names the gaps your list does not account for; give one")
	}
	if strings.TrimSpace(*through) != "" {
		if !tokens.ValidDay(*through) {
			r.add("--through is not a day: " + *through + "; it wants YYYY-MM-DD (e.g. 2026-09-18)")
		}
	}
	opt := tokens.CheckOptions{Strict: *strict, Through: strings.TrimSpace(*through)}
	if strings.TrimSpace(*noSpend) != "" {
		days, err := tokens.ReadNoSpendFile(*noSpend)
		if err != nil {
			r.add("--no-spend " + *noSpend + ": " + err.Error())
		} else {
			opt.NoSpend = days
		}
	}
	if len(r.list) > 0 {
		return r.print(stderr)
	}
	res, err := tokens.Check(*out, opt)
	if err != nil {
		r.add(err.Error())
		return r.print(stderr)
	}
	remedyLine := "nova-tokens check --out " + *out + " --max 0"
	files := s.list(true, *max, "CHECK", "file", remedyLine)
	rowsList := s.list(true, *max, "CHECK", "row", remedyLine)
	missing := s.list(true, *max, "CHECK", "missing", remedyLine)
	strays := s.list(true, *max, "CHECK", "stray", remedyLine)
	for _, f := range res.Findings {
		reason := oneline.Cap(f.Reason, oneline.TailBytes)
		line := fmt.Sprintf("CHECK FAIL %s: %s", oneline.Escape(f.Path), oneline.Escape(reason))
		if f.Line > 0 {
			line = fmt.Sprintf("CHECK FAIL %s:%d: %s", oneline.Escape(f.Path), f.Line, oneline.Escape(reason))
		}
		kind, list := "file", files
		if f.Line > 2 {
			kind, list = "row", rowsList
		}
		list.Line(line)
		s.item(kind, "path", f.Path, "line", f.Line, "why", tool.Text(reason))
	}
	files.More()
	rowsList.More()
	for _, d := range res.Missing {
		missing.Line("CHECK MISSING date=" + oneline.Field(d))
		s.item("missing", "date", d)
	}
	missing.More()
	for _, p := range res.Strays {
		strays.Line("CHECK STRAY " + oneline.Escape(p))
		s.item("stray", "path", p)
	}
	strays.More()

	first, last := tokens.Dash, tokens.Dash
	if res.First != "" {
		first, last = res.First, res.Last
	}
	if res.Stale {
		fmt.Fprintf(s.err(), "CHECK FAIL stale last=%s through=%s\n", oneline.Field(last), oneline.Field(*through))
		s.item("stale", "last", last, "through", *through)
	}
	// A GATE THAT CANNOT GO RED IS NO GATE. An --out holding no day file has nothing in it
	// to pass, and a green over nothing reads exactly like a green over a month: it is a
	// finding, with the fold that makes the first file as its remedy.
	empty := res.Files == 0
	if empty {
		why := "--out " + *out + " holds no day file, so there is nothing to check; fold one first: nova-tokens fold --out " + *out + " --day <YYYY-MM-DD> --repos <file> <source flags>"
		fmt.Fprintf(s.err(), "CHECK FAIL %s\n", oneline.Escape(why))
		s.o.Why = append(s.o.Why, why)
	}
	bad := files.Total() + rowsList.Total()
	counts := []any{"files", res.Files, "rows", res.Rows, "first", first, "last", last}
	if bad > 0 || len(res.Missing) > 0 || len(res.Strays) > 0 || res.Stale || empty {
		counts = append(counts, "bad", bad, "missing", len(res.Missing), "stray", len(res.Strays), "gap", len(res.Gaps), "notes", len(res.Notes))
		fmt.Fprintf(s.err(), "CHECK FAIL%s\n", s.factFields(counts...))
		return s.done(1, *max)
	}
	// gap= and notes= are on the OK line too, and that is the whole point: what the gate
	// stopped naming it still counts, so nothing was hidden to make the line green.
	counts = append([]any{"at", stamp(now), "build", buildVersion()}, append(counts, "missing", 0, "stray", 0, "gap", len(res.Gaps), "notes", len(res.Notes))...)
	fmt.Fprintf(s.out(), "CHECK OK%s\n", s.factFields(counts...))
	return s.done(0, *max)
}
