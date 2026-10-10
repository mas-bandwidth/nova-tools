// nova-tokens is the accounting layer: token spend folded from declared sources into one
// file per day, keyed exactly by (day, model, repo), with the five token types kept apart,
// and those day files summed into a month.
//
// It exists because we have an obligation to report token spend, and the first thing that
// shape was built as — three scripts of Python under zsh — produced nine day files and
// every way it could fail at once: five paths defaulted inside the script, so a run on
// another bench folds the wrong bench; every path here is a flag, and the tool removes no month file on a real
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
	"os"
	"strconv"
	"strings"
	"time"

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
first run: create a tiny transcript and rules file with the lines under setup:
above example:, then run the lines under example: in order.

usage:
  nova-tokens fold    --out <dir> (--day <YYYY-MM-DD> | --all) --repos <file>
                      [--claude <label>=<dir>]... [--opencode <label>=<file>]... [--swarm <label>=<pool>]... [--bus <dir>]
                      [--provider <kind>:<label>=<file>]... [--scratch <dir>] [--timeout <seconds>] [--allow-shrink] [--max <n>] [--dry-run]
  nova-tokens report (local mode) --who <name> --day <YYYY-MM-DD> --repos <file>
                      mode: local note body, printed as the tokens note artifact
                      [--claude <label>=<dir>]... [--opencode <label>=<file>]... [--provider <kind>:<label>=<file>]...
                      [--supersedes <note-id>]... [--note <path>] [--scratch <dir>] [--timeout <seconds>] [--dry-run]
  nova-tokens report (store mode) --redis <host:port> --month <YYYY-MM> [--by model|repo|day|tuple] [--max <n>]
                      mode: Redis month summary
                      [--user <name>] [--password-env <NAME>]
  the local mode is selected by --who and --day; the store mode by --redis and --month; giving both --who and --redis selects the store mode (--redis wins); a mix of --who and --redis prints the store summary
  nova-tokens ledger  --out <dir> (--day <YYYY-MM-DD> | --month <YYYY-MM>) --redis <host:port>
                      [--user <name>] [--password-env <NAME>] [--dry-run]
  nova-tokens sum     --out <dir> --month <YYYY-MM> [--max <n>]
  nova-tokens check   --out <dir> [--strict | --no-spend <file>] [--through <YYYY-MM-DD>] [--allow-empty] [--max <n>]
  nova-tokens sources --repos <file> (--day <YYYY-MM-DD> | --all) [<source flags>] [--unattributed] [--max <n>]
  nova-tokens profiles --swarm-root <dir>
                      one PROFILES MODEL line per model (cards, median output, overshoot), then a PROFILES OK line with totals
  nova-tokens session --claude-session <jsonl> [--out <dir>] [--day <YYYY-MM-DD>] [--dry-run]
                      [--role <name>] [--weights <in,cw,cr,out>]
  nova-tokens version

Every verb but version takes --json: the same result as one JSON object on stdout, a
refusal included. A verb that writes takes --dry-run: it is the real run's own plan --
it reads what the real run reads and refuses what the real run refuses -- prints what it
would write with dry_run=true on its last line, and writes nothing (ledger --dry-run dials
no store). The one difference: a dry fold or session takes no fold.lock, so it neither waits for nor
refuses on a fold holding one. --opencode under --dry-run (and under sources) still reads a
copy of the database, made in a new directory of the run's own under --scratch
(.nova-tokens-dry-run-*) and removed before it exits: --scratch is left as it was. ` + "`<verb> -h`" + ` lists a verb's flags and states its effect.

exit codes: 0 the verb ran and passed; 1 the verb ran and said FAILED -- an unreadable
source, an unparsed bus line or note, a row of two day bases, a lane-day with competing
reports, a day that would shrink, a fold whose every message had no id and so folded nothing,
a check finding (an --out holding no day file is one), a report with nothing to show; 2 could
not run: a missing flag, a bad flag value, a duplicate label, two sources of one
provider sharing message ids, sqlite3 absent when
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

Two declared sources of one provider that feed the same message ids are refused
before any day file is written. The refusal names both labels and the duplicate
count, and the remedy is to drop one of the two flags. An id is comparable only
within one provider, and a shared id is not dropped from the other source, so
check and sum never see a doubled day.

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
file holds the folding process's id and stays in --out between runs (it is never data);
check counts it as neither a day file nor a stray.

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
either: an --out with no day file in it is CHECK FAILED, never a green over nothing.

sources --unattributed prints the path stems that were SEEN and matched no rule, heaviest
first, capped by --max, with the mentions each stem got (one per message that touched a path
in it). That listing is what other=<pct>% on a day line is made of, and it is the evidence
for improving the --repos file.

setup:
  mkdir -p ./transcripts ./out
  printf '%s' '{"type":"assistant","timestamp":"2026-09-11T09:12:' > ./transcripts/window.jsonl
  printf '%s' '00Z","message":{"id":"example-1","model":"claude-' >> ./transcripts/window.jsonl
  printf '%s' 'fable-5-1","usage":{"input_tokens":812,' >> ./transcripts/window.jsonl
  printf '%s' '"output_tokens":40,"cache_creation_input_tokens":' >> ./transcripts/window.jsonl
  printf '%s' '1200,"cache_read_input_tokens":90000},' >> ./transcripts/window.jsonl
  printf '%s' '"content":[{"type":"tool_use","input":{' >> ./transcripts/window.jsonl
  printf '%s\n' '"file_path":"/work/schema/wire.md"}}]}}' >> ./transcripts/window.jsonl
  cp ./transcripts/window.jsonl ./session.jsonl
  printf 'schema\t(^|/)schema($|/)\n' > ./repos.tsv

example:
  nova-tokens fold --out ./out --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts
  nova-tokens check --out ./out
  nova-tokens sum --out ./out --month 2026-09
  nova-tokens sources --repos ./repos.tsv --all --claude bench=./transcripts
  nova-tokens sources --repos ./repos.tsv --all --claude bench=./transcripts --unattributed --max 20
  nova-tokens report --who ada --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts

session is the caller's own window: it sums one Claude Code session jsonl per
turn -- input, cache write, cache read, output, deduplicated on the message id so a
streamed message counts once -- prints one SESSION line with the weighted
fresh-input equivalent (in x input + cw x cache write + cr x cache read + out x output,
the four --weights sets) and the average context per turn, and with --out folds
it into the day file as the model the transcript names, as <model> or, when --role
names one, <model>/<role> (a transcript that names no model is refused, never
booked under a guess). Its spend is a line in the ledger like every other row's.

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
func run(args []string, stdout, stderr io.Writer, now time.Time) (code int) {
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
			return run(append([]string{rest[0], "--help"}, rest[1:]...), stdout, stderr, now)
		}
		fmt.Fprintf(stdout, "%s", usage)
		return 0
	case "fold":
		return cmdFold(rest, stdout, stderr, now)
	case "report":
		return cmdReport(rest, stdout, stderr, now)
	case "ledger":
		return cmdLedger(rest, stdout, stderr)
	case "sum":
		return cmdSum(rest, stdout, stderr, now)
	case "check":
		return cmdCheck(rest, stdout, stderr, now)
	case "sources":
		return cmdSources(rest, stdout, stderr, now)
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
	maxFiles int
	exclude  stringList
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
	fs.IntVar(&s.maxFiles, "max-files", tokens.DefaultMaxClaudeFiles, "ceiling on the transcript files one --claude tree holds (default 20000); the whole tree is walked and counted before any file is opened, and a tree over the ceiling is refused naming the files and bytes it found; 0 is no ceiling")
	fs.Var(&s.exclude, "exclude", "path or glob kept out of a recursive source tree (--claude), repeatable (nothing is excluded by default)")
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
				if why := notADir("claude "+it.label+"="+it.value, it.value, "the directory the transcripts live under"); why != "" {
					r.add(why)
				}
			case "swarm":
				if why := notADir("swarm "+it.label+"="+it.value, it.value, "the swarm pool directory"); why != "" {
					r.add(why)
				}
			}
		}
	}
	if s.bus != "" {
		any = true
		if why := notADir("bus", s.bus, "the bus directory"); why != "" {
			r.add(why)
		}
	}
	if !any {
		r.add("at least one source flag is required; it wants " + wantsSources + "; refusing to guess")
	}
	if len(s.opencode.items) > 0 {
		if strings.TrimSpace(s.scratch) == "" {
			r.required("scratch", "", wantsScratch)
		} else if why := notADir("scratch", s.scratch, wantsScratch); why != "" {
			r.add(why)
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
		out = append(out, tokens.ReadClaude(it.label, it.value, os.DirFS(it.value), rules,
			tokens.ClaudeBound{MaxFiles: s.maxFiles, Exclude: []string(s.exclude)}))
	}
	scratch := s.scratch
	if private && len(s.opencode.items) > 0 {
		dir, err := os.MkdirTemp(s.scratch, ".nova-tokens-dry-run-")
		if err != nil {
			for _, it := range s.opencode.items {
				src := &tokens.Source{Label: tokens.Label(tokens.KindOpenCode, it.label), Kind: tokens.KindOpenCode, Path: it.value, Basis: tokens.UTC}
				src.Unreadables = append(src.Unreadables, tokens.Unreadable{Label: src.Label, Path: it.value, Why: "a private copy directory under --scratch: " + err.Error()})
				out = append(out, src)
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
		out = append(out, tokens.ReadSwarm(it.label, it.value, os.DirFS(it.value), rules))
	}
	for _, it := range s.provider.items {
		kind, name, _ := strings.Cut(it.label, ":")
		out = append(out, tokens.ReadProvider(kind, name, it.value, rules))
	}
	if s.bus != "" {
		out = append(out, tokens.ReadBus(s.bus, os.DirFS(s.bus), rules, now)...)
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

// sourceLine is the line where a number becomes traceable: what each declared source
// opened, refused, counted and fed. A field that is not a measurement for the kind prints
// a dash, because a dash is an absence where a zero is a measurement.
func sourceLine(s *sink, token string, src *tokens.Source) string {
	kv := []any{"label", src.Label, "kind", src.Kind, "path", src.Path, "reports", src.ReportsList(), "day_basis", src.Basis}
	for _, f := range []string{"files", "unreadable", "messages", "dup", "noid", "nousage", "unparsed", "comments", "redated", "superseded", "rows"} {
		kv = append(kv, f, count(src.StatField(f)))
	}
	return s.line(token, "SOURCE", "", kv...)
}

func unreadableLine(s *sink, token string, u tokens.Unreadable) string {
	why := u.Why
	if u.Line > 0 {
		why = "line " + strconv.Itoa(u.Line) + ": " + why
	}
	return s.line(token, "UNREADABLE", oneline.Cap(why, oneline.TailBytes), "label", u.Label, "path", u.Path)
}

func unparsedLine(s *sink, token string, u tokens.Unparsed) string {
	return s.line(token, "UNPARSED", oneline.Cap(u.Text, oneline.TailBytes), "label", u.Label, "note", u.Note, "line", u.Line)
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

// notADir is the refusal for a flag whose value must be a directory that is there, or "".
// A label source types its path into the flag itself (--claude <label>=<path>, and so
// --swarm), so that path is already in flag and is not named twice; every other flag names
// the directory after the flag name (--bus <path>). The three sentences are "does not
// exist", the stat error, and "is not a directory".
func notADir(flag, path, wants string) string {
	fi, err := os.Stat(path)
	// A label source is the one flag shaped <label>=<path>: its path is already typed.
	named := strings.Contains(flag, "=")
	pathAt, pathMid := ": "+path, " "+path
	if named {
		pathAt, pathMid = "", ""
	}
	switch {
	case err != nil && os.IsNotExist(err):
		return "--" + flag + " does not exist" + pathAt + "; it wants " + wants
	case err != nil:
		return "--" + flag + pathMid + ": " + err.Error() + "; it wants " + wants
	case !fi.IsDir():
		return "--" + flag + " is not a directory" + pathAt + "; it wants " + wants
	}
	return ""
}

// foldFindings is everything the one remedy line reads: the counts, the flags, the output
// directory and the first label each branch names, so the call names its fields instead of
// passing fourteen positional arguments.
type foldFindings struct {
	sources      []*tokens.Source
	unreadable   int
	unparsed     int
	mixed        int
	conflict     int
	shrank       int
	partial      int
	quiet        int
	allowShrink  bool
	dryRun       bool
	out          string
	mixedLabels  string
	firstPartial string
	firstQuiet   string
}

// remedy is the ONE line TOKENS NOTE carries. It names the label and the act, in the order
// a reader would act on them, and when nothing was wrong it names the gate. SPEC-TOKENS:
// "TOKENS NOTE is exactly one remedy line." dryRun is the plan the run would take, so a
// day --allow-shrink would write is reported as it would be, never as it was.
func remedy(f foldFindings) string {
	switch {
	case f.unreadable > 0:
		// A line that is not JSON is not a permission problem: its remedy is the one act
		// that clears it, naming the file and the first bad line. Every other unreadable
		// (a file that would not open, a day file this run could not write) keeps the
		// permission remedy SPEC-TOKENS gives.
		if u, ok := firstBadline(f.sources); ok {
			return "a declared source has a line that is not JSON (" + u.Label + ", " + u.Path + " line " + strconv.Itoa(u.Line) + "): inspect or remove that line, or drop the flag"
		}
		return "a declared source could not be read whole (" + firstUnreadableLabel(f.sources) + "): open those files to this group, or drop the flag -- a declared source is a claim that the report covers it"
	case f.unparsed > 0:
		// The advice is for the KIND that failed. Every unparsed was a bus line once, and
		// a swarm usage file refused by its header was told the shape of a bus body line.
		kind, note, own := firstUnparsed(f.sources)
		if own != "" {
			return own
		}
		switch kind {
		case tokens.KindSwarm:
			return "a swarm usage file did not parse (" + note + "): its header is the sixteen columns SPEC-WORKER rule 12 names, in order -- " + strings.Join(tokens.SwarmColumns, ", ")
		case tokens.KindProvider:
			return "a line of a billing export did not parse (" + note + "): the parser is the kind in --provider <kind>:<label>=<path>, and a row carries the columns that kind declares"
		case tokens.KindClaude, tokens.KindOpenCode:
			return "a message's stamp did not parse (" + note + "): a day comes from the message's own RFC 3339 stamp, and this tool dates nothing by a guess"
		}
		return "a bus line or note did not parse (" + note + "): a body line is date<TAB>who<TAB>model<TAB>repo<TAB>type<TAB>count, with an optional day_basis=<zone>"
	case f.conflict > 0:
		return "a lane-day has competing reports (" + firstConflictLabel(f.sources) + "): one note whose subject carries supersedes=<every tip, sorted> is the replacement snapshot that clears it"
	case f.mixed > 0:
		// The two labels, because "declare one export for that day" is not an act until
		// the caller knows which two are competing. Every other branch of this switch
		// names a label, a note or a lane; this one named nothing.
		return "a row was fed by two day bases (" + f.mixedLabels + "): declare one of those two for that day, not both"
	case f.partial > 0:
		// Above the shrank branches: a row this fold cannot compute is not a day going
		// backwards, and --allow-shrink is not the act that clears it.
		return "a row of the day file was written by sources this fold did not declare (" + f.firstPartial + "): declare every source in that file's sources= line, or fold this day into its own --out -- --allow-shrink does not write it"
	case f.shrank > 0 && !f.allowShrink:
		return "a day would have gone backwards and was left as it was: --allow-shrink writes it anyway, and it is a person's act"
	case f.shrank > 0 && f.dryRun:
		return "a day would be written smaller at your word (--allow-shrink); nova-tokens check --out " + f.out + " is the gate"
	case f.shrank > 0:
		return "a day was written smaller at your word (--allow-shrink); nova-tokens check --out " + f.out + " is the gate"
	case f.quiet > 0:
		// A quiet source is not a failure, and it is not "nothing was wrong" either: the
		// day file names a source this run declared and read nothing from for that day.
		return "a declared source fed no message for a day its file names (" + f.firstQuiet + "): its rows there were recomputed from nothing; if it did spend that day, its files are not under the path you declared"
	case noidAndDup(f.sources) != "":
		return noidAndDup(f.sources) + "; those messages are NOT in any row"
	}
	return "nothing was wrong; nova-tokens check --out " + f.out + " is the gate"
}

func firstUnreadableLabel(sources []*tokens.Source) string {
	for _, s := range sources {
		if len(s.Unreadables) > 0 {
			return s.Unreadables[0].Label
		}
	}
	return "-"
}

// firstBadline is the first unreadable whose failure is a line that is not JSON, and the
// one the NOTE's remedy names: a bad line is inspected or removed, never opened to a group.
func firstBadline(sources []*tokens.Source) (tokens.Unreadable, bool) {
	for _, s := range sources {
		for _, u := range s.Unreadables {
			if u.Line > 0 {
				return u, true
			}
		}
	}
	return tokens.Unreadable{}, false
}

// firstUnparsed names the kind of the first source with an unparsed line and what it was
// reading, so that the one remedy line is the remedy for the thing that failed.
// noidAndDup is the sentence for spend that was read and then dropped: a message with no
// id is not folded (rule 4) and a repeated id is counted once. Both are numbers on a green
// TOKENS SOURCE line and nowhere else, and 100% of a file's usage can be a message with no
// id (a number is not a sentence).
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

// ---------------------------------------------------------------------------- report

// ------------------------------------------------------------------------------- sum

func validMonth(m string) bool {
	return tokens.ValidMonth(m)
}

// ----------------------------------------------------------------------------- check
