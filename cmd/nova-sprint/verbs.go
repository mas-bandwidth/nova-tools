package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/cardlimits"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

type flagSet = *flag.FlagSet

type verb struct {
	name, syntax, example string
	run                   func(*app, []string, io.Writer, io.Writer) int
}

var verbs []verb

func init() {
	verbs = []verb{
		{"init", "[--readers <a,b,...>] [--members <m1[:<width>],m2,...>] [--coordinator <name>] [--rules <file>]", "init --readers reader-a,reader-b,reader-c --members m1:64,m2:64", (*app).cmdInit},
		{"add", "--stream <s> (<id>... | --count <n> | --sentinel <id> | --brief-dir <dir> | --brief-file <f1> --brief-file <f2>...: a card per file, its id the file's name without .md) [--needs <a,b>] [--before <id> | --after <id> | --score <n>] [--brief <text> | --brief-file <path>: once, the brief of the cards named] [--rules <file>]", "add --stream s1 --count 100", (*app).cmdAdd},
		{"quack", "--streams <a,b,...> --count <n> --repo <clone url> [--tiers <t,...>] [--base <branch>]", "quack --streams a,b --count 2 --repo https://example.com/quack.git", (*app).cmdQuack},
		{"release", "<sentinel>... --reason <text> [--answers <note>]", "release s1-stop --reason 'the layer is green and read'", (*app).cmdRelease},
		{"resolve", "[<id>...] [--stream <s>] [--limit <n>]", "resolve", (*app).cmdResolve},
		{"start", "", "start", (*app).cmdMachineStart},
		{"stop", "", "stop", (*app).cmdMachineStop},
		{"run", "", "run", (*app).cmdRun},
		{"tick", "", "tick", (*app).cmdTick},
		{"goal set", "<name> [--file <path>] [--to file:<path>]", "goal set friend-a --file goal-a.txt --to file:/tmp/reminder-a.txt", (*app).cmdGoalSet},
		{"goal show", "[<name>]", "goal show friend-a", (*app).cmdGoalShow},
		{"goal drop", "<name>", "goal drop friend-a", (*app).cmdGoalDrop},
		{"take", "--as <member> [<card>@<gen>...] [--epoch <n>] [--limit <n>]", "take --as m1 s1-1.w1@1 --epoch 0", (*app).cmdTake},
		{"finish", "--as <member> <card>@<gen>... --epoch <n> (--head <commit> | --failed) [--report <text>] [--usage <text>]", "finish --as m1 s1-1.w1@1 --epoch 0 --head 9f3c2e1 --report 'tests green'", (*app).cmdFinish},
		{"ask", "[<id>... | --group <id> [--expect <n>]] [--stream <s>] [--limit <n>] [--another] [--answers <note>]", "ask", (*app).cmdAsk},
		{"queue", "--as <reader|member> | --stream <s>", "queue --as reader-a", (*app).cmdQueue},
		{"read", "--as <reader> (--begin | --ok | --broken) [<card>...] --epoch <n> [--limit <n>] [--finding <text>] [--usage <text>] | --as <reader> --return <card> --reason <text> --epoch <n> [--usage <text>]", "read --as reader-a --ok --limit 5 --epoch 0", (*app).cmdRead},
		{"accept", "(<id>... | --stream <s> | --read-ok | --group <id> [--expect <n>]) [--answers <note>]", "accept --read-ok", (*app).cmdAccept},
		{"rework", "(<id>... | --group <id> [--expect <n>]) [--fix <text>] [--answers <note>]", "rework s1-4 --fix 'handle the empty case'", (*app).cmdRework},
		{"return", "(<id>... | --group <id> [--expect <n>]) [--reason <text>] [--answers <note>]", "return s1-7 --reason 'suspect of the red batch'", (*app).cmdReturn},
		{"drop", "(<id>... | --stream <s> --col <state> | --group <id> [--expect <n>]) --reason <text> [--answers <note>]", "drop s1-9 --reason obsolete", (*app).cmdDrop},
		{"rank", "<id>... (--score <n> | --first) [--answers <note>]", "rank s2-3 --first", (*app).cmdRank},
		{"brief", "<id> (--brief <text> | --brief-file <path>) [--rules <file>]", "brief s1-4 --brief-file s1-4.md", (*app).cmdBrief},
		{"move", "<id>... --stream <s> [--before <id> | --after <id> | --score <n>]", "move s1-4 s1-5 --stream s2", (*app).cmdMove},
		{"merge", "--stream <s> [--batch <n>] [--conflict <id> | --cross <id>=<other> | --red [--suspect <id>...] | --rejected] [--note <text>]", "merge --stream s1 --batch 100", (*app).cmdMerge},
		{"land", "[--stream <s>...] [--repo-dir <clone>] [--base <branch>] [--check <command>] [--dry-run]", "land --stream s1 --dry-run", (*app).cmdLand},
		{"resume", "--stream <s> [--did <text>] [--answers <note>]", "resume --stream s1 --did 'rebased s1-4'", (*app).cmdResume},
		{"fleet beat", "<member> [--load <percent>]", "fleet beat m1", (*app).cmdFleetBeat},
		{"fleet up", "<member> [--width <n>]", "fleet up m1 --width 64", func(a *app, args []string, o, e io.Writer) int { return a.cmdFleet("up", args, o, e) }},
		{"fleet down", "<member>", "fleet down m1", func(a *app, args []string, o, e io.Writer) int { return a.cmdFleet("down", args, o, e) }},
		{"fleet sync", "[--check] [--pg <dsn>]", "fleet sync --check", (*app).cmdFleetSync},
		{"fleet level", "", "fleet level", func(a *app, args []string, o, e io.Writer) int { return a.cmdFleet("level", args, o, e) }},
		{"friend sync", "[--pg <dsn>]", "friend sync", (*app).cmdFriendSync},
		{"friend beat", "<friend>", "friend beat friend-a", (*app).cmdFriendBeat},
		{"friend down", "<friend>", "friend down friend-a", func(a *app, args []string, o, e io.Writer) int { return a.cmdFriendHold(true, args, o, e) }},
		{"friend up", "<friend>", "friend up friend-a", func(a *app, args []string, o, e io.Writer) int { return a.cmdFriendHold(false, args, o, e) }},
		{"reader add", "<reader>...", "reader add reader-d", (*app).cmdReaderAdd},
		{"reader away", "<reader>...", "reader away reader-d", func(a *app, args []string, o, e io.Writer) int { return a.cmdReaderHold(true, args, o, e) }},
		{"reader up", "<reader>...", "reader up reader-d", func(a *app, args []string, o, e io.Writer) int { return a.cmdReaderHold(false, args, o, e) }},
		{"reader remove", "<reader>...", "reader remove reader-d", (*app).cmdReaderRemove},
		{"stream remove", "<stream>...", "stream remove a b c", (*app).cmdStreamRemove},
		{"ci", "<id>... (--red | --green) --epoch <n> [--head <h>] [--run <id>] [--source <s>] [--note <text>]", "ci s1-3 --red --run 812 --source ci --epoch 0", (*app).cmdCI},
		{"wait", "<note> (--for <duration> | --until <RFC3339>)", "wait tick-ask-x-1.2 --for 30m", (*app).cmdWait},
		{"ack", "<note>... --reason <text>", "ack ci-x-1.1 --reason 'a flaky runner; the rerun is green'", (*app).cmdAck},
		{"inbox", "[--open <group>] [--read] [--wait [--timeout <duration>]] [--deadline <duration>] [--stale <duration>]", "inbox --wait", (*app).cmdInbox},
		{"card", "<id>", "card s1-4", (*app).cmdCard},
		{"log", "[--card <id>] [--stream <s>] [--member <m>] [--since <10m|RFC3339>] [--at-epoch <n>]", "log --card s1-4", (*app).cmdLog},
		{"check", "", "check", (*app).cmdCheck},
		{"repair", "", "repair", (*app).cmdRepair},
		{"where", "[--watch] [--every <duration>]", "where", (*app).cmdWhere},
		{"routes", "", "routes", (*app).cmdRoutes},
		{"stats", "", "stats", (*app).cmdStats},
		{"play", "[--simulation] [--seed <n>] [--every <duration>] [--broken <p>] [--fail <p>] [--stuck <p>] [--cross <p>] [--down <p>] [--up <p>] [--red <p>] [--flap <p>] [--batch <n>] [--hold] [--silent <member>@<from>+<for>]... [--ticks <n>]", "play --seed 7 --every 1s", (*app).cmdPlay},
		{"clear", "--confirm sprint", "clear --confirm sprint", (*app).cmdClear},
		{"teardown", "--confirm sprint", "teardown --confirm sprint", (*app).cmdTeardown},
	}
}

func verbNames() []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range verbs {
		n, _, _ := strings.Cut(v.name, " ")
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return append(out, "help", "version")
}

// groupVerbs is the verbs of the group word names (fleet, friend, reader, goal, stream):
// every verb whose name is that word and more; nil for a word that is no group.
func groupVerbs(word string) []string {
	var out []string
	for _, v := range verbs {
		if strings.HasPrefix(v.name, word+" ") {
			out = append(out, v.name)
		}
	}
	return out
}

// opening is the banner's first three answers: what the tool does (line 1,
// the README's sentence), how it works, and the first run (ONBOARDING.md
// point 6).
const opening = `nova-sprint: a sprint of work cards, dealt to a fleet of workers and read before they land

how it works: one store (a Redis, or a twin file) holds one sprint as four
tables (work, merge, readers, fleet) and the view sprint. A card is one unit of
work in a stream; each tick deals ready cards to members (machines with a
width), sends finished work to readers, queues what they pass to merge by
stream, and puts every judgment it cannot make in the coordinator's inbox.
first run, no Redis (the store is the file sprint.twin):
  export NOVA_SPRINT_REDIS=mem:sprint.twin NOVA_SPRINT_ACTOR=boss
then the card's flow under "trying it without a Redis", ticking by hand; the
example: block is the coordinator's day on a real store, and "A real fleet"
below connects the machines (run --listen, NOVA_SPRINT_SERVER, nova-swarm member).
the rest: this help is long; nova-sprint help <verb> (or <verb> -h) prints one
verb's usage, examples, flags and exit codes, and nova-sprint help <group>
(fleet, friend, reader, goal, stream) one group's.`

func banner() string {
	var b strings.Builder
	b.WriteString(opening + "\n\nusage:\n")
	for _, v := range verbs {
		b.WriteString("  nova-sprint " + strings.TrimSpace(v.name+" "+v.syntax) + "\n")
	}
	b.WriteString(`
Every store verb takes --redis <addr> (else NOVA_SPRINT_REDIS, then
NOVA_REDIS_ADDR), --actor <name> (else NOVA_SPRINT_ACTOR; no
default: a verb that writes wants one), --op <id> (the same id again returns
the recorded result), --json and --max <n> (listed items; 0 is all). The
coordinator's verbs are the coordinator's alone (the first init names it:
--coordinator, else the actor); take, finish, read, fleet beat and friend
beat are the workers', whose actor is the member, reader or friend named; merge and ci are
reports; tick and run are the machine's; the reads need no actor (inbox
--read, which moves the coordinator's cursor, is the coordinator's). A set is
ids, a stream, a column, --limit n, or an inbox group: --group <id>, the id
inbox prints, which does not move, with --expect <n>, the size it printed,
which refuses a group that has changed. Each verb prints what moved (MOVED),
what did not and why (REFUSED, on stderr), its summary line, and the sprint's
line: landed/all percent -> ETA <estimate> (the cards left, each at the average
time a card has taken to land, in minutes rounded up; where shows the largest
of the last 10 s; the word alone until one has landed; a stopped
machine has no ETA: STOPPED, then
landed/all and the percent when there are cards; every card landed, no ETA:
done in <time from the first start> while it runs, and STOPPED ... done once
the machine has stopped itself).

The tables are work, merge, readers and fleet, and the view is sprint; a store
holds one sprint (a second sprint is a second store). The work table's cost
column is, per stream, the sum of its landed cards' total cost in US dollars
(each consumer's actual cost, else its predicted one; - when none was priced),
with the sum over the streams at the bottom; card <id> shows the detail. clear and teardown want
--confirm sprint, the name of the view, and refuse anything else.

A work card is named with its generation, <card>@<gen>: the generation the
worker holds, from queue --as <member> (--json: "gen"). take by id and finish
name it for every card; a card named without one is refused, naming the live
generation, and a generation that is not the live one is refused as stale.
take with no card takes the member's oldest ready cards (--limit n, default 1)
and prints each one's generation.

` + inboxExample + `
` + machineWords() + `
` + serverWords() + `
` + fleetWords() + `
` + friendWords() + `
` + readerWords() + `
` + streamWords() + `
` + goalWords() + `
` + twinWords() + `
` + landWords() + `
` + wordsSection() + `
` + exitLine + `

the coordinator's day, in five lines (NOVA_SPRINT_REDIS and NOVA_SPRINT_ACTOR set; nova-sprint run ticking in a shell of its own; brief.txt is a card that passes the lint, from nova-swarm template --name card, its REPO: and BASE: filled in):

example:
`)
	for _, l := range dayLines {
		b.WriteString("  " + l + "\n")
	}
	return b.String()
}

// dayLines is the coordinator's day in five lines, the banner's example: block.
var dayLines = []string{
	"nova-sprint init --readers reader-a,reader-b --members m1:8",
	"nova-sprint add --stream s1 --count 3 --brief-file brief.txt",
	"nova-sprint start",
	"nova-sprint inbox --wait",
	"nova-sprint land --stream s1 --check 'make test'",
}

// inboxExample is the worked example of reading the inbox and answering it,
// in nova-sprint help and nova-sprint help inbox.
const inboxExample = `reading the inbox and answering a judgment:

  $ nova-sprint inbox
  JUDGMENT finish-0314a1b2-1.1   work came back failed  stream=s1  size=2  waited=4m0s  due=10:14:00  (s1-3,s1-7)  the tests went red
    rework with a fix:
      nova-sprint rework --group finish-0314a1b2-1.1 --expect 2 --answers finish-0314a1b2-1.1
    drop:
      nova-sprint drop --group finish-0314a1b2-1.1 --expect 2 --reason '<why>' --answers finish-0314a1b2-1.1
  JUDGMENT merge-0315c3d4-1.1   stream stopped: stream branch red  stream=s2  size=10  waited=1m0s  due=10:25:00  (s2-1,s2-2,s2-3,s2-4,s2-5,s2-6,s2-7,s2-8,... all: nova-sprint inbox --open merge-0315c3d4-1.1)  suspects: s2-4 (of the batch of 10)
    take the suspect off and resume:
      nova-sprint return s2-4 --reason 'suspect of the red batch' --answers merge-0315c3d4-1.1
      nova-sprint resume --stream s2 --did 'returned s2-4' --answers merge-0315c3d4-1.1
    rework the suspect:
      nova-sprint return s2-4 --reason 'suspect of the red batch' --answers merge-0315c3d4-1.1
      nova-sprint rework s2-4 --fix '<fix>'
      nova-sprint resume --stream s2 --did 'returned s2-4 for rework' --answers merge-0315c3d4-1.1
    resume with what you did:
      nova-sprint resume --stream s2 --did '<what you did>' --answers merge-0315c3d4-1.1
  HAPPENED finish-0316e5f6-1.1   work came back ok  stream=s1  size=5  (s1-1,s1-2,s1-4,s1-5,s1-6)
  INBOX OK judgments=2 happened=1 cursor=-

A group is named by its id (its oldest notification's), which does not move
as groups come and go; a group number is refused. size is what --expect
takes: when the group has another size now the verb is refused, names what
was added or is gone, and changes nothing. Each decision is its commands, one
per line, in order: copy them, filling in a '<...>' first. inbox --open <id>
lists every member of a group, and every need a blocked group names; card <id> is everything about one primary.

The sprint done is no judgment: the tick that finds nothing open says it, one
HAPPENED line addressed to the coordinator and shown first, and stops the
machine (DONE):
  HAPPENED tick-done-0317a1b2-1.1   the sprint is done  x1  for=coordinator  9 landed, 0 dropped, took 1h2m0s from the first start
    to continue: add work, then nova-sprint start

one answer to each judgment (every one prints its own, filled in):
  ready to accept             accept --group <id> --expect <n> --answers <notes>
  work came back failed       rework --group <id> --expect <n> --answers <notes>  (each fix is the work's report; --fix for all)
  a reader found it broken    rework --group <id> --expect <n> --answers <notes>  (each fix is the reader's finding)
  conflict on a card          resume --stream <s> --did 'rebased <card>' --answers <note>
  stream branch red           return <suspect> --answers <note>, then resume --stream <s> --did 'returned <suspect>' --answers <note>
  needs another stream first  rank <other> --first, then resume --stream <s> once <other> has landed
  merge queue rejected        resume --stream <s> --did '<what you did>' --answers <note>
  ci red                      rework --group <id> --expect <n> --fix '<fix>' --answers <notes>
  blocked on a dropped card   drop --group <id> --expect <n> --reason '<why>' --answers <notes>
  blocked on a missing card   drop <ids> --reason '<why>' or ack <notes> --reason '<why the named missing needs can be waived>'
  reads exhausted             ask --group <id> --expect <n> --another --answers <notes>
  repair skipped changes      card <primary>, then rework, return or drop --group <id> --expect <n> --answers <notes>
  an operation was stuck      check, then ack <note> --reason '<what you found>'
  a repeat: stop and look     card <primary>
  overdue: act                a decision above, or wait <note> --for 30m
  a stream not moving: look   where, then queue --stream <s>
  sentinel reached            release <sentinel> --reason '<what you found>' --answers <note>
  returned to review          rework, accept (its reads standing) or drop --group <id> --expect <n> --answers <notes>
  stranded in review          rework or drop (or ask, if never asked) --group <id> --expect <n> --answers <notes>
  stalled                     card <primary> (HELD says what holds it), then the decision it prints, or ack <note> --reason '<why>'
`

// verbExamples holds one more worked example per form a verb's -h shows,
// beyond its table's example, each without the tool's name.
var verbExamples = map[string][]string{
	"add": {
		"add --stream s1 --brief-dir briefs",
		"add --stream s1 --brief-file a.md --brief-file b.md",
	},
}

// verbExample is the lines a verb's -h shows above its flags: its examples,
// one runnable line per form from the verb table, for verbflag.RecoverWith.
func verbExample(name string) string {
	for _, v := range verbs {
		if v.name != name {
			continue
		}
		lines := append([]string{v.example}, verbExamples[name]...)
		var b strings.Builder
		b.WriteString("example:\n")
		for _, line := range lines {
			if line != "" {
				fmt.Fprintf(&b, "  %s %s\n", prog, line)
			}
		}
		return b.String()
	}
	return ""
}

func versionLine() string { return buildinfo.Line(prog, version) }

func helpCommand(path []string, stdout, stderr io.Writer) int {
	if len(path) == 0 {
		fmt.Fprint(stdout, banner())
		return 0
	}
	name := strings.Join(path, " ")
	if len(groupVerbs(name)) > 0 {
		fmt.Fprintln(stdout, "usage:")
		for _, v := range verbs {
			if strings.HasPrefix(v.name, name+" ") {
				fmt.Fprintln(stdout, "  nova-sprint "+strings.TrimSpace(v.name+" "+v.syntax))
			}
		}
		if name == "goal" {
			fmt.Fprint(stdout, "\n"+goalWords())
		}
		if name == "fleet" {
			fmt.Fprint(stdout, "\n"+fleetWords())
		}
		if name == "friend" {
			fmt.Fprint(stdout, "\n"+friendWords())
		}
		if name == "reader" {
			fmt.Fprint(stdout, "\n"+readerWords())
		}
		if name == "stream" {
			fmt.Fprint(stdout, "\n"+streamWords())
		}
		fmt.Fprintf(stdout, "\nnova-sprint help %s <verb> (or nova-sprint %s <verb> -h) prints a verb's flags, examples and exit codes.\n", name, name)
		return 0
	}
	for _, v := range verbs {
		if v.name == name {
			code := func() (code int) {
				defer recoverHelp(stdout, &code)
				return v.run(newApp(func(string) string { return "" }), []string{"--help"}, stdout, stderr)
			}()
			if name == "inbox" && code == 0 {
				fmt.Fprint(stdout, "\n"+inboxExample)
			}
			return code
		}
	}
	return refuse(stderr, "help", "unknown verb "+oneline.Escape(name)+"; run: nova-sprint help")
}

// parse is the verb's flags anywhere among its words; words after -- are
// taken as they are.
func parse(fs *flag.FlagSet, args []string) ([]string, error) { return parseEach(fs, args, nil) }

// parseEach is parse, telling each (when set) of each flag the words give: its
// name, where it begins and how many words it is (1, or 2 with its value). It hands the flag
// package one flag at a time, its value with it, so the flag package never sees
// the -- that ends the flags (it would end its parse there, and the words after
// it would be read as flags again): every word after a -- is taken as it is, and
// a -- that is a flag's value is that value.
func parseEach(fs *flag.FlagSet, args []string, each func(name string, at, n int)) ([]string, error) {
	var pos []string
	for i := 0; i < len(args); {
		w := args[i]
		switch {
		case w == "--":
			return append(pos, args[i+1:]...), nil
		case len(w) < 2 || w[0] != '-':
			pos = append(pos, w)
			i++
			continue
		}
		n := 1
		name, _, inline := strings.Cut(strings.TrimPrefix(w[1:], "-"), "=")
		if f := fs.Lookup(name); f != nil && !inline && i+1 < len(args) {
			if b, ok := f.Value.(interface{ IsBoolFlag() bool }); !ok || !b.IsBoolFlag() {
				n = 2
			}
		}
		if err := verbflag.Parse(fs, args[i:i+n]); err != nil {
			if strings.Contains(err.Error(), "flag provided but not defined: -prefix") {
				return nil, errNoPrefix
			}
			return nil, flagRefusal(fs, err)
		}
		if each != nil {
			each(name, i, n)
		}
		i += n
	}
	return pos, nil
}

// flagError is a flag-parse refusal already worded as the verb's whole line, `unknown flag
// --x; run: nova-sprint help <verb>`: a caller that wraps an error in its own words
// (argErr) and refuse, which appends the verb's -h pointer, leave it as it is.
type flagError struct{ msg string }

func (e *flagError) Error() string { return e.msg }

// flagRefusal words a flag package's parse error once: a flag the verb does not define
// is `unknown flag --x`, a flag missing its value is `--x wants a value`, each with the
// verb's help to run; a value that does not parse names the flag and what it wants
// (verbflag.Explain).
func flagRefusal(fs *flag.FlagSet, err error) error {
	name := fs.Name()
	if w := strings.Fields(name); len(w) > 0 && !slices.ContainsFunc(verbs, func(v verb) bool { return v.name == name }) {
		name = w[0]
	}
	help := "; run: " + prog + " help " + name
	const undefined, needs = "flag provided but not defined: ", "flag needs an argument: "
	switch msg := err.Error(); {
	case strings.HasPrefix(msg, undefined):
		// the nearest flag and the flags the verb takes, never the flag package's line
		// (tool ledger X2, the tool-answers rule)
		return &flagError{verbflag.Explain(fs, err) + help}
	case strings.HasPrefix(msg, needs):
		return &flagError{"-" + strings.TrimPrefix(msg, needs) + " wants a value" + help}
	}
	// a value that does not parse names the flag and what it wants, as its own whole
	// line: a verb's words are never glued in front of it ("takes no words invalid value")
	return &flagError{verbflag.Explain(fs, err) + help}
}

// sel is the set flags of a verb.
type sel struct {
	stream, col string
	limit       int
	group       string // an inbox group's id
	expect      int    // the group's size when it was printed; 0 is not given
}

func (s *sel) register(fs flagSet, withCol bool) {
	fs.StringVar(&s.stream, "stream", "", "the cards of one stream")
	if withCol {
		fs.StringVar(&s.col, "col", "", "the cards in one column (a state)")
	}
	fs.IntVar(&s.limit, "limit", 0, "at most n cards, in work order; accept and ask take them in stream turns from the work table's stream index")
	fs.StringVar(&s.group, "group", "", "the members of the inbox group of this id (the id inbox prints; a group number is refused)")
	fs.IntVar(&s.expect, "expect", 0, "with --group: the group's size as inbox printed it; a group of another size now is refused and nothing changes")
}

func (s *sel) sel(ids []string) sprint.Sel {
	return sprint.Sel{IDs: ids, Stream: s.stream, Col: s.col, Limit: s.limit}
}

func answers(s string) []string { return sprint.Split(s) }

// listFlag is a flag given again or comma separated: every value, in order.
type listFlag []string

func (l *listFlag) String() string { return strings.Join(*l, ",") }

func (l *listFlag) Set(v string) error {
	*l = append(*l, sprint.Split(v)...)
	return nil
}

// stringList is a flag given again: every value, in order, as given (no comma
// split: a file name may hold one).
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// verbSetup is the flag set of a store verb with the common flags.
func (a *app) verbSetup(name string) (flagSet, *common) {
	fs := verbflag.New(name)
	c := &common{verb: name}
	c.register(fs, a.getenv)
	return fs, c
}

// groupIDs is the members of the inbox group of the id, with the inbox read.
func groupIDs(ctx context.Context, st *store.Store, id string) (store.InboxView, sprint.Group, error) {
	v, err := st.Inbox(ctx, defaultDeadline, defaultStale, 10000)
	if err != nil {
		return v, sprint.Group{}, err
	}
	if isNumber(id) {
		return v, sprint.Group{}, fmt.Errorf("group numbers are not accepted: a group is named by its id, which does not move; %s", groupList(v.Groups))
	}
	g, ok := sprint.FindGroup(v.Groups, id)
	if !ok && sprint.IDEpoch(id) != st.PinnedEpoch() {
		return v, g, errors.New(sprint.OtherEpoch(id, sprint.IDEpoch(id), st.PinnedEpoch()))
	}
	if !ok {
		return v, g, fmt.Errorf("no inbox group %s now (answered, or its oldest notification closed); %s", id, groupList(v.Groups))
	}
	return v, g, nil
}

func isNumber(s string) bool {
	_, err := strconv.Atoi(strings.TrimSpace(s))
	return err == nil
}

// groupList is the inbox's groups by id, for a refusal.
func groupList(groups []sprint.Group) string {
	if len(groups) == 0 {
		return "the inbox is empty; run: nova-sprint inbox"
	}
	var ids []string
	for _, g := range groups {
		ids = append(ids, fmt.Sprintf("%s (%s, %s, size %d)", g.ID, g.Type, dashed(g.Stream), g.Size))
	}
	return "the groups now: " + strings.Join(ids, "; ") + "; run: nova-sprint inbox"
}

// groupChange is how a group differs from what the coordinator saw, told by
// the notifications the verb answers: added is members of notifications it
// does not name, gone is subjects of the ones it names that are no longer open.
func groupChange(v store.InboxView, g sprint.Group, answers []string) (added, gone []string) {
	named := map[string]bool{}
	for _, a := range answers {
		named[a] = true
	}
	in := map[string]bool{}
	for _, id := range g.Notes {
		in[id] = true
	}
	open := map[string]bool{}
	byNote := map[string]sprint.Note{}
	for _, o := range v.Open {
		if in[o.Note.ID] || named[o.Note.ID] {
			open[o.Note.ID+"|"+o.Subject()] = true
			byNote[o.Note.ID] = o.Note
		}
	}
	for _, m := range g.Members {
		old := false
		for _, o := range v.Open {
			if named[o.Note.ID] && in[o.Note.ID] && (o.Subject() == m || o.Note.StreamLevel && slices.Contains(o.Note.Primaries, m)) {
				old = true
			}
		}
		if !old {
			added = append(added, m)
		}
	}
	for _, a := range answers {
		n, ok := byNote[a]
		if !ok {
			gone = append(gone, "notification "+a+" (closed)")
			continue
		}
		for _, sub := range n.Subjects() {
			if !open[a+"|"+sub] {
				gone = append(gone, sub)
			}
		}
	}
	return added, gone
}

const (
	defaultDeadline = 10 * time.Minute
	defaultStale    = 30 * time.Minute
)

// epochVerbs are the verbs that act on cards handed to an actor outside the
// sprint: a worker's take by id and finish, a reader's read, a merger's merge
// and a CI observation. Each names the epoch it was handed its cards at
// (--epoch, from queue), so a worker, reader or merger from before a clear
// never reports on the new epoch's card of the same name: a clear moves the
// epoch, and a card of the same name in the new epoch is another card.
// Every other verb is the coordinator's, which acts on the cards it reads in
// the step's own fenced read of the epoch: with no --epoch the step runs at
// the epoch it finds (a clear between the read and the write is read again),
// so the coordinator needs no epoch to name.
var epochVerbs = map[string]bool{"finish": true, "read": true, "merge": true, "ci": true, "take by id": true}

// needsEpoch is whether the verb must be given --epoch: the verbs of
// epochVerbs, except a merge run by the sprint's coordinator, which merges
// the cards of its own read of the merge queue and names no handed card.
func needsEpoch(verbName string, coordinator bool) bool {
	return epochVerbs[verbName] && !(verbName == "merge" && coordinator)
}

// runStep runs a step and reports it: exit 0 when everything named moved, 1
// when a card was refused or the step was cut, 2 when the store did not
// confirm.
func (a *app) runStep(verbName string, c common, st *store.Store, step store.Step, stdout, stderr io.Writer) int {
	ctx := context.Background()
	if a.serving && c.epoch < 0 {
		// a worker's write through the server runs at the epoch its worker holds, as the
		// verb parsed it: with none (or one a later word undid) the step could run in a
		// sprint the worker has not read, a clear later (serve.go)
		return refuse(stderr, strings.TrimSuffix(verbName, " by id"), "a worker's verb sent to the server names the epoch its worker holds, --epoch <n> (queue prints it), and this one runs at none; nothing was changed")
	}
	if epochVerbs[verbName] && c.epoch < 0 {
		coordinator := false
		if verbName == "merge" {
			if name, err := st.B.Coordinator(ctx); err == nil {
				coordinator = name != "" && name == c.actor
			}
		}
		if needsEpoch(verbName, coordinator) {
			now := "the sprint's epoch"
			if es, err := st.EpochNow(ctx); err == nil {
				now = fmt.Sprintf("the sprint's epoch is %d", es.N)
			}
			name := strings.TrimSuffix(verbName, " by id")
			return refuse(stderr, name, fmt.Sprintf("a report names the epoch its cards were handed at: --epoch <n> (queue and card print it); %s; nothing was changed", now))
		}
	}
	step.CallerOp = c.op
	if c.epoch >= 0 {
		e := uint64(c.epoch)
		step.Epoch = &e
	}
	res, err := st.Run(ctx, step)
	if c.packets != nil && err == nil {
		c.handed = c.packets(ctx, st, res)
	}
	if c.after != nil && err == nil {
		c.says = append(c.says, c.after(ctx, st, res)...)
	}
	if err != nil || (len(res.Refused) > 0 && len(res.Moved) == 0) {
		c.says = nil // what it would have said is about moves that did not happen
	}
	return a.report(ctx, verbName, c, st, res, err, stdout, stderr)
}

func token(verbName string) string {
	return strings.ToUpper(strings.ReplaceAll(verbName, " ", "-"))
}

// output is a step's report for a program.
type output struct {
	store.Result
	Error   string `json:"error,omitempty"`
	Unknown bool   `json:"unknown,omitempty"`
	Sprint  string `json:"sprint,omitempty"`
	// Group, with --group: the group's id, how many it acted on, and the
	// size --expect said it had when printed.
	Group    string `json:"group,omitempty"`
	ActedOn  int    `json:"acted_on,omitempty"`
	Expected int    `json:"expected,omitempty"`
	// Packets is what the step hands its actor: take's cards' packets.
	Packets []sprint.Packet `json:"packets,omitempty"`
	// Says is the verb's NOTE lines: what it did that the moves do not say.
	Says []string `json:"says,omitempty"`
}

// groupReport is what a verb given --group says about the group.
type groupReport struct {
	ID       string
	ActedOn  int
	Expected int
}

// line is the group's line: the count acted on, and the size when printed
// when --expect said it.
func (g groupReport) line() string {
	l := fmt.Sprintf("GROUP %s acted on %d", oneline.Escape(g.ID), g.ActedOn)
	if g.Expected > 0 {
		l += fmt.Sprintf(", the group had %d when printed", g.Expected)
	}
	return l
}

// stepExit is a step's exit code: 0 when everything named moved, 1 when a card
// was refused or the step was cut, 2 when the store did not confirm.
func stepExit(res store.Result, err error) int {
	code := 0
	if len(res.Refused) > 0 {
		code = 1
	}
	var pe *store.PendingError
	var cut *store.CutError
	var cleared *store.ClearedError
	var synced *store.SyncError
	switch {
	case err == nil:
	case errors.As(err, &synced):
		// the write committed: its cards are in the table, so the step is not a failure
	case errors.Is(err, store.ErrUnknown):
		code = 2
	case errors.As(err, &pe), errors.As(err, &cut), errors.As(err, &cleared):
		code = 1
	default:
		code = 2
	}
	return code
}

func (a *app) report(ctx context.Context, verbName string, c common, st *store.Store, res store.Result, err error, stdout, stderr io.Writer) int {
	code := stepExit(res, err)
	var synced *store.SyncError
	line := sprintLine(ctx, st)
	if c.json {
		o := output{Result: res, Sprint: line, Unknown: errors.Is(err, store.ErrUnknown), Group: c.group.ID, ActedOn: c.group.ActedOn, Expected: c.group.Expected, Packets: c.handed, Says: c.says}
		if o.Moved == nil {
			o.Moved = []string{}
		}
		if o.Refused == nil {
			o.Refused = []sprint.Refusal{}
		}
		if err != nil {
			o.Error = err.Error()
		}
		b, _ := json.Marshal(o)
		fmt.Fprintln(stdout, string(b))
		return code
	}
	for _, r := range res.Repaired {
		fmt.Fprintf(stdout, "REPAIRED %s\n", oneline.Escape(r))
	}
	listed(stdout, "MOVED", res.Moved, c.max, verbName)
	for _, p := range c.handed {
		printPacket(stdout, p)
	}
	if c.group.ID != "" {
		fmt.Fprintln(stdout, c.group.line())
	}
	var why []string
	for _, r := range res.Refused {
		why = append(why, r.Key+": "+r.Why)
	}
	listed(stderr, "REFUSED", why, c.max, verbName)
	status := "OK"
	if code != 0 {
		status = "FAIL"
	}
	fields := fmt.Sprintf("moved=%d refused=%d notes=%d", len(res.Moved), len(res.Refused), res.Notes)
	if verbName == "add" {
		fields = fmt.Sprintf("stream=%s cards=%d before=%s %s", oneline.Field(c.addStream), len(res.Moved), oneline.Field(dashed(c.addBefore)), fields)
	}
	if res.Op != "" {
		fields += " op=" + oneline.Escape(res.Op)
	}
	if res.Replay {
		fields += " replay=yes"
	}
	if res.Pending != "" {
		fields += " pending=" + oneline.Escape(res.Pending)
	}
	if err != nil && !errors.As(err, &synced) {
		changed := "no"
		if errors.Is(err, store.ErrUnknown) {
			changed = "unknown"
		}
		fields += " changed=" + changed
	}
	out := stdout
	if code != 0 {
		out = stderr
	}
	fmt.Fprintf(out, "%s %s %s\n", token(verbName), status, fields)
	for _, s := range c.says {
		fmt.Fprintf(out, "NOTE %s\n", oneline.Escape(s))
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: %s\n", prog, verbName, oneline.Escape(err.Error()))
	}
	if line != "" {
		fmt.Fprintln(stdout, line)
	}
	return code
}

// listed prints at most max lines of a kind, then a MORE line.
func listed(w io.Writer, kind string, lines []string, max int, verbName string) {
	for i, l := range lines {
		if max > 0 && i == max {
			fmt.Fprintf(w, "MORE kind=%s shown=%d total=%d run: nova-sprint %s ... --max 0\n", strings.ToLower(kind), max, len(lines), verbName)
			return
		}
		fmt.Fprintf(w, "%s %s\n", kind, oneline.Escape(l))
	}
}

// sprintLine is the summary line: landed / all primaries, percent, ETA. A
// STOPPED machine has no ETA, so its line is the STOPPED text the header of
// where shows, then, with cards on the table, landed / all and the percent.
// Every primary landed, the line has no ETA (errata 3 amendment 6): while the
// machine runs, "N/N 100.0% done in <duration>" from its first start; once it
// has stopped because the sprint is done, "STOPPED  N/N 100.0% done".
func sprintLine(ctx context.Context, st *store.Store) string {
	if st == nil {
		return ""
	}
	shapes, err := st.B.Shapes(ctx, []string{st.Names.Table(sprint.Work)})
	if err != nil || len(shapes) == 0 {
		return ""
	}
	machine := st.MachineLine(ctx)
	landed, all := counts(shapes[0])
	full := all > 0 && landed == all
	state := strings.TrimPrefix(machine, "machine: ")
	switch {
	case state == store.DoneState:
		if full {
			return store.Stopped + "  " + progress(shapes[0]) + " done"
		}
		return store.Stopped + "  " + progress(shapes[0])
	case strings.HasPrefix(state, "STOPPED"):
		if all == 0 && landed == 0 {
			return state
		}
		return state + "  " + progress(shapes[0])
	case full:
		return strings.TrimSpace(progress(shapes[0]) + " done" + tookSince(ctx, st) + "  " + machine)
	}
	since, started := st.SinceFirstStart(ctx)
	return strings.TrimSpace(summary(shapes[0], etaMinutes(shapes[0], since, started)) + "  " + machine)
}

// tookSince is " in <duration>": the wall time from the machine's first start
// of the sprint's epoch to now; empty when it is not known.
func tookSince(ctx context.Context, st *store.Store) string {
	if d, ok := st.SinceFirstStart(ctx); ok {
		return " in " + sprint.TookText(d)
	}
	return ""
}

func (a *app) cmdInit(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("init")
	readers := fs.String("readers", "", "the readers' rows, comma separated")
	members := fs.String("members", "", fmt.Sprintf("fleet members to bring up, comma separated, each <name> or <name>:<width>, its width the most work cards it runs at once; it holds %d times that, ready and working (default %d)", sprint.DealAhead, sprint.DefaultWidth))
	coordinator := fs.String("coordinator", "", "the sprint's coordinator, the one actor who releases sentinels (default: the actor)")
	rules := fs.String("rules", "", "the child rules file every brief is held to: one required sentence per line, its path recorded for the sprint (default: the built-in general rules; add --rules <file> overrides it for one add)")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "init", err.Error())
	}
	if len(pos) > 0 {
		return refuse(stderr, "init", "takes no words, found "+pos[0])
	}
	rulesPath := ""
	if *rules != "" {
		// the file is read now, so a file that cannot be a rule set is refused before the sprint exists
		abs, err := filepath.Abs(*rules)
		if err != nil {
			return refuse(stderr, "init", "--rules: "+err.Error())
		}
		if _, err := swarm.ReadChildRules(abs); err != nil {
			return refuse(stderr, "init", "--rules: "+err.Error()+"; one required sentence per line, see `nova-swarm lint --rules`")
		}
		rulesPath = abs
	}
	c.coordinator = *coordinator
	specs, err := sprint.ParseMembers(*members)
	if err != nil {
		return refuse(stderr, "init", "--members: "+err.Error())
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "init", err.Error())
	}
	ctx := context.Background()
	if err := st.Init(ctx); err != nil {
		fmt.Fprintf(stderr, "%s init: %s\n", prog, oneline.Escape(err.Error()))
		return 1
	}
	if *coordinator == "" {
		*coordinator = c.actor
	}
	if err := st.B.SetCoordinator(ctx, *coordinator); err != nil {
		fmt.Fprintf(stderr, "%s init: %s\n", prog, oneline.Escape(err.Error()))
		return 1
	}
	if rs := sprint.Split(*readers); len(rs) > 0 {
		for _, r := range rs {
			if !sprint.ValidID(r) {
				return refuse(stderr, "init", "a reader name wants letters, digits, _ and -: "+r)
			}
		}
		if err := st.B.RowsAdd(ctx, st.Names.Table(sprint.Readers), rs); err != nil {
			fmt.Fprintf(stderr, "%s init: %s\n", prog, oneline.Escape(err.Error()))
			return 1
		}
	}
	if rulesPath != "" {
		if err := st.SetRulesPath(ctx, rulesPath); err != nil {
			fmt.Fprintf(stderr, "%s init: %s\n", prog, oneline.Escape(err.Error()))
			return 1
		}
	}
	readerRows, err := st.ReaderRows(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "%s init: %s\n", prog, oneline.Escape(err.Error()))
		return 1
	}
	tables := []string{st.Names.Table(sprint.Work), st.Names.Table(sprint.Readers), st.Names.Table(sprint.Merge), st.Names.Table(sprint.Fleet)}
	// with --json the members' steps print nothing of their own: the one object
	// names them (tool ledger P8)
	steps := stdout
	if c.json {
		steps = io.Discard
	} else {
		fmt.Fprintf(stdout, "INIT OK tables=%s view=%s readers=%s\n", strings.Join(tables, ","), st.Names.View(), dashed(strings.Join(readerRows, ",")))
	}
	var memberNames []string
	for _, m := range specs {
		if code := a.runStep("fleet up", *c, st, a.fleetStep(st, "up", m.Name, c.actor, m.Width), steps, stderr); code != 0 {
			return code
		}
		memberNames = append(memberNames, m.Name)
	}
	notes := []string{}
	if len(specs) > 0 && a.twinOpen(c.redis) {
		// a twin beats every member at every verb (beatTwin): a member added
		// down beats at the next verb and is up from the next tick's presence
		notes = append(notes, "a twin beats every member at every verb: each member added is up after the next nova-sprint tick")
	}
	if c.json {
		sayOK(stdout, true, "init", "", map[string]any{"tables": tables, "view": st.Names.View(), "readers": nonNil(readerRows),
			"members": nonNil(memberNames), "coordinator": *coordinator, "notes": notes})
		return 0
	}
	for _, n := range notes {
		fmt.Fprintln(stdout, "NOTE "+n)
	}
	return 0
}

func (a *app) cmdAdd(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("add")
	stream := fs.String("stream", "", "the stream the primaries belong to, for life; with --count, several streams comma separated, one step")
	count := fs.Int("count", 0, "admit n primaries with generated ids <stream>-<n>")
	needs := fs.String("needs", "", "primaries that must land first, comma separated; each is a primary on the table")
	brief := fs.String("brief", "", fmt.Sprintf("the brief: a child's whole brief, at most %d KiB (the card lint advises %d bytes), held to the card lint (the sentences of the rules file: --rules, else the one init --rules recorded, else the built-in general rules; nova-swarm template --name card prints a card that passes the general ones, nova-swarm lint --rules lists them) and refused, exit 2, nothing written, when it fails; a card with no brief is not linted", cardlimits.MaxBriefBytes>>10, cardlimits.BriefAdvisoryBytes))
	var briefFiles stringList
	fs.Var(&briefFiles, "brief-file", "the brief, read from this file: its bytes as they are, its one trailing newline cut (a brief of many paragraphs), then held to the card lint like --brief; given once, the brief of the cards the ids, --count or --sentinel name; given again, one card per file in the order given, each card's id its file's name without .md (a1.md is a1); not with --brief or --brief-dir")
	briefDir := fs.String("brief-dir", "", "one card per *.md file in this directory, in byte order of file name, each card's id its file's name without .md (a1.md is a1); not with --brief-file")
	rules := fs.String("rules", "", "the child rules `file`, read at add time (not recorded, unlike init --rules): one required sentence per line, [name] sentence names its token (default: the file init --rules recorded, else the built-in general rules); e.g. --rules rules/card.txt")
	score := fs.String("score", "", "the first primary's score; the rest follow it (default: after every primary)")
	sentinel := fs.String("sentinel", "", "admit a sentinel with this id: a stop the coordinator releases; what sorts after it waits for it")
	before := fs.String("before", "", "place the cards in line in front of this primary of the stream")
	after := fs.String("after", "", "place the cards in line after this primary of the stream")
	every := fs.Int("sentinel-every", 0, "with --count: a sentinel <stream>-gate-<n> after every k cards (a stop by its place in line)")
	last := fs.Bool("sentinel-last", false, "with --sentinel-every: a sentinel after the last card too")
	ids, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "add", err.Error())
	}
	if *count < 0 {
		// a negative count admitted no card and opened the stream with an OK
		return refuse(stderr, "add", fmt.Sprintf("--count wants the number of cards to admit, at least 1, got %d", *count))
	}
	// The many-brief form: --brief-dir <dir>, or --brief-file given again,
	// names one brief file per card. One --brief-file alone is the one brief
	// for every card the ids, --count or --sentinel name.
	if *briefDir != "" || len(briefFiles) > 1 {
		if *briefDir != "" && len(briefFiles) > 0 {
			return refuse(stderr, "add", "--brief-dir and --brief-file are two ways to name the brief files: give one")
		}
		if *brief != "" {
			return refuse(stderr, "add", "--brief and --brief-dir (or a repeated --brief-file) are two ways to give the brief: give one")
		}
		if len(ids) > 0 {
			return refuse(stderr, "add", "takes no ids with --brief-dir or a repeated --brief-file: the cards are the files")
		}
		if *count != 0 {
			return refuse(stderr, "add", "--count names the cards by number, and --brief-dir or a repeated --brief-file names them by file: give one")
		}
		if *every != 0 || *last {
			return refuse(stderr, "add", "--sentinel-every goes with --count, not a card per brief file")
		}
		return a.cmdAddMany(*stream, *needs, *briefDir, briefFiles, *sentinel, *rules, *score, *before, *after, c, stdout, stderr)
	}
	if len(briefFiles) == 1 {
		if *brief != "" {
			return refuse(stderr, "add", "--brief and --brief-file are two ways to give the brief: give one")
		}
		text, err := readBriefFile(briefFiles[0])
		if err != nil {
			return refuse(stderr, "add", "--brief-file: "+err.Error())
		}
		*brief = text
	}
	if *sentinel != "" {
		if len(ids) > 0 || *count != 0 {
			return refuse(stderr, "add", "--sentinel <id> admits one sentinel, with no other ids or --count")
		}
		ids = []string{*sentinel}
	}
	if len(briefFiles) == 1 && len(ids) == 0 && *count == 0 {
		// one --brief-file is the brief of the cards named, and names none itself
		f := briefFiles[0]
		id := strings.TrimSuffix(filepath.Base(f), ".md")
		return refuse(stderr, "add", fmt.Sprintf("one --brief-file is the brief of the cards the ids, --count or --sentinel name, and this add names none; for a card with its id from the file, name the id (nova-sprint add --stream %s %s --brief-file %s), or give --brief-file twice or more, or --brief-dir <dir>: a card per file", orDashStr(*stream, "<s>"), id, f))
	}
	if *stream == "" || (len(ids) == 0) == (*count == 0) {
		return refuse(stderr, "add", "wants --stream and either ids, --count <n> or --sentinel <id> (or --brief-dir <dir>, or --brief-file twice or more: a card per file)")
	}
	if *last && *every == 0 {
		return refuse(stderr, "add", "--sentinel-last goes with --sentinel-every <k>")
	}
	streams := sprint.Split(*stream)
	if len(streams) > 1 && *count == 0 {
		return refuse(stderr, "add", "several streams take --count <n>: each gets n cards")
	}
	// A BRIEF IS A CHILD'S WHOLE BRIEF, AND THE CARD LINT HOLDS IT TO THE RULES OF ONE: the
	// rules are the coordinator's (internal/swarm/lintchild.go), from --rules, else the file
	// init recorded, else the general defaults, and they are checked here in process, before
	// anything is written. A card with no brief (a --count card, a sentinel) carries none to
	// check.
	var st *store.Store
	if *rules != "" && *brief == "" {
		return refuse(stderr, "add", "--rules is the rule set a brief is held to, and this add gives no brief; give --brief or --brief-file")
	}
	if *sentinel == "" && *brief != "" {
		if code := a.holdBrief("add", *brief, *rules, c, &st, stderr); code != 0 {
			return code
		}
		c.says = append(c.says, unfilledSays("the brief", *brief)...)
	}
	var rs []sprint.AddReq
	for _, sn := range streams {
		r := sprint.AddReq{Stream: sn, IDs: ids, Count: *count, Needs: sprint.Split(*needs), Brief: *brief, Who: c.actor,
			Sentinel: *sentinel != "", Before: *before, After: *after, Every: *every, Last: *last}
		if *score != "" {
			f, err := strconv.ParseFloat(*score, 64)
			if err != nil {
				return refuse(stderr, "add", "--score wants a number")
			}
			r.Score = &f
		}
		rs = append(rs, r)
	}
	if st == nil {
		var err error
		if st, err = a.store(*c); err != nil {
			return refuse(stderr, "add", err.Error())
		}
	}
	if *brief == "" && *sentinel == "" {
		c.says = append(c.says, "the cards have no brief, so a worker is handed no task with them; give each one before it is dealt, on a STOPPED machine: nova-sprint brief <id> --brief-file <path>")
	}
	c.addStream = *stream
	c.addBefore = *before
	if len(rs) == 1 {
		return a.runStep("add", *c, st, store.AddStep(rs[0]), stdout, stderr)
	}
	return a.runStep("add", *c, st, store.AddEachStep(rs), stdout, stderr)
}

// cmdAddMany is add --brief-dir <dir>, or add with --brief-file given again:
// one card per brief file, in byte order of the directory's *.md files or in
// the order the files were named. Every brief is read and linted first (one
// failing brief refuses the whole call, exit 2, nothing written), and one
// store write adds every card.
func (a *app) cmdAddMany(stream, needs, briefDir string, briefFiles []string, sentinel, rules, score, before, after string, c *common, stdout, stderr io.Writer) int {
	if stream == "" {
		return refuse(stderr, "add", "wants --stream and --brief-dir <dir> or a repeated --brief-file")
	}
	if strings.Contains(stream, ",") {
		return refuse(stderr, "add", "--brief-dir and a repeated --brief-file name the cards by file in one stream: give one stream")
	}
	files, code := a.briefFiles(briefDir, briefFiles, stderr)
	if code != 0 {
		return code
	}
	var st *store.Store
	rs, code := a.briefRules("add", rules, c, &st, stderr)
	if code != 0 {
		return code
	}
	extra := sprint.Split(needs)
	cards := make([]sprint.CardAdd, 0, len(files))
	for _, path := range files {
		id := strings.TrimSuffix(filepath.Base(path), ".md")
		if !sprint.ValidID(id) {
			return refuse(stderr, "add", fmt.Sprintf("%s: the card id is the file's base name without .md, and %q is not one (letters, digits, _ and -)", path, id))
		}
		text, err := readTextFile(path, briefReadCap)
		if err != nil {
			return refuse(stderr, "add", fmt.Sprintf("%s: %v", path, err))
		}
		// an empty file here is linted, and the lint says it once, naming the file
		briefText := strings.TrimSuffix(text, "\n")
		if len(briefText) > store.MaxBriefBytes {
			return refuse(stderr, "add", fmt.Sprintf("%s: the brief is %d bytes, over the %d bytes a brief may be; a brief is a child's whole brief; shorten it", path, len(briefText), store.MaxBriefBytes))
		}
		cards = append(cards, sprint.CardAdd{ID: id, Brief: briefText, Needs: uniquify(append(briefNeeds(briefText), extra...)), File: path})
	}
	// Every brief is linted first: one failing brief refuses the whole call,
	// nothing written, every failing file named with its findings.
	if code := lintBriefFiles(cards, rs, c.max, stderr); code != 0 {
		return code
	}
	// --sentinel <id> admits a stop after every card of the call: the sentinel
	// sorts after the cards, and what sorts after it waits for it.
	if sentinel != "" {
		cards = append(cards, sprint.CardAdd{ID: sentinel, Sentinel: true})
	}
	if st == nil {
		s, err := a.store(*c)
		if err != nil {
			return refuse(stderr, "add", err.Error())
		}
		st = s
	}
	r := sprint.AddReq{Stream: stream, Cards: cards, Who: c.actor, Before: before, After: after}
	if score != "" {
		f, err := strconv.ParseFloat(score, 64)
		if err != nil {
			return refuse(stderr, "add", "--score wants a number")
		}
		r.Score = &f
	}
	c.says = append(c.says, fmt.Sprintf("each card's id is its brief file's name without .md (%s is %s)", files[0], cards[0].ID))
	for _, cd := range cards {
		c.says = append(c.says, unfilledSays("the brief of "+cd.ID, cd.Brief)...)
	}
	c.addStream = stream
	c.addBefore = before
	return a.runStep("add", *c, st, store.AddStep(r), stdout, stderr)
}

// briefFiles is the brief files of a many-brief add, in order: the *.md files
// of dir in byte order of file name, or the named files in the order given. A
// directory with no *.md file is refused naming the directory.
func (a *app) briefFiles(dir string, files []string, stderr io.Writer) ([]string, int) {
	if dir == "" {
		return append([]string(nil), files...), 0
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, refuse(stderr, "add", "--brief-dir: "+err.Error())
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	if len(out) == 0 {
		return nil, refuse(stderr, "add", fmt.Sprintf("--brief-dir %s holds no *.md file", dir))
	}
	return out, 0
}

// briefNeeds is the needs a brief names: the first `Needs:` header line (read
// by cardhdr.KeyValue), its ids comma separated, each cut at an opening
// parenthesis. "none" or "-" (also after the cut, so "none (first card)") or
// no such line is no needs.
func briefNeeds(brief string) []string {
	for _, line := range strings.Split(brief, "\n") {
		key, value, ok := cardhdr.KeyValue(line)
		if !ok || key != "Needs" {
			continue
		}
		var out []string
		for _, id := range strings.Split(value, ",") {
			if cut, _, ok := strings.Cut(id, "("); ok {
				id = cut
			}
			switch id = strings.TrimSpace(id); id {
			case "", "-", "none":
				continue
			}
			out = append(out, id)
		}
		return out
	}
	return nil
}

// uniquify keeps the first of each id, in order: a need named by a brief and
// again by --needs is stored once.
func uniquify(ids []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// modelLinesWhy is the refusal a brief's model lines get, the same whether the
// brief came in as one or among many: the model lines the deal reads (line 1's
// tier, a model: pin) are read by the one parser the deal and the frame use, so
// a brief the deal would refuse is refused here, before it is admitted.
func modelLinesWhy(why string) string {
	return "the brief's model lines: " + why + "; line 1 names `tier: flash|pro|frontier`, and a pinned card carries `model: <provider>/<model>` (with `tokens: <n>|unmetered` and `deadline: <seconds>`) under it"
}

// lintBriefReads holds one brief to the card lint's child rules and to its
// model lines: it returns the model-line why ("" when the lines read) and the
// lint findings. The single-brief and many-brief paths both call it, so one
// brief is held the same however it is given.
func lintBriefReads(brief string, rules []swarm.ChildRule) (modelWhy string, findings []swarm.CardHeaderFinding) {
	if _, why := cardhdr.ReadModel(brief); why != "" {
		return why, nil
	}
	return "", swarm.LintCardChildWith([]byte(brief), rules)
}

// lintBriefFiles holds every brief of a many-brief add to the card lint's
// child rules and its model lines: one failing brief refuses the whole call,
// exit 2, nothing written, every failing file named with its findings, at most
// max of them (0 is all) before the one MORE line.
func lintBriefFiles(cards []sprint.CardAdd, rules []swarm.ChildRule, max int, stderr io.Writer) int {
	type finding struct {
		file string
		f    swarm.CardHeaderFinding
	}
	var all []finding
	var failed []string
	for _, c := range cards {
		modelWhy, findings := lintBriefReads(c.Brief, rules)
		if modelWhy != "" {
			return refuse(stderr, "add", c.File+": "+modelLinesWhy(modelWhy))
		}
		if len(findings) == 0 {
			continue
		}
		failed = append(failed, c.File)
		for _, f := range findings {
			all = append(all, finding{c.File, f})
		}
	}
	if len(all) == 0 {
		return 0
	}
	printed, more := all, false
	if max > 0 && len(all) > max {
		printed, more = all[:max], true
	}
	for _, x := range printed {
		fmt.Fprintf(stderr, "LINT DRIFT brief %s: %s: %d: %s remedy=%s\n", oneline.Field(x.file), oneline.Field(x.f.Check), x.f.Line,
			oneline.Escape(oneline.Cap(x.f.Excerpt, oneline.TailBytes)), oneline.Escape(swarm.ChildRemedy(rules, x.f.Check)))
	}
	if more {
		fmt.Fprintf(stderr, "LINT MORE brief findings=%d remedy=add --max 0\n", len(all))
	}
	return refuse(stderr, "add", fmt.Sprintf("the brief of %s fails the card lint (%s); a brief is a child's whole brief and carries every rule of its rule set (--rules, else the file init --rules recorded, else the general rules); run: nova-swarm template --name card", strings.Join(failed, ", "), findingsCount(len(all))))
}

// briefRules is the rule set an add holds its brief to: the file --rules names, else the
// file init --rules recorded for the sprint (read through the store, opened once into *st
// for the add to use), else swarm.DefaultChildRules. A file that cannot be read or is no
// rule set is a usage refusal naming it.
func (a *app) briefRules(verbName, file string, c *common, st **store.Store, stderr io.Writer) ([]swarm.ChildRule, int) {
	if file != "" {
		abs, err := filepath.Abs(file)
		if err != nil {
			return nil, refuse(stderr, verbName, "--rules: "+err.Error())
		}
		rs, err := swarm.ReadChildRules(abs)
		if err != nil {
			return nil, refuse(stderr, verbName, "--rules: "+err.Error())
		}
		return rs, 0
	}
	s, err := a.store(*c)
	if err != nil {
		return nil, refuse(stderr, verbName, err.Error())
	}
	*st = s
	path, err := s.RulesPath(context.Background())
	if err != nil {
		return nil, a.readFailed(verbName, err, stderr)
	}
	if path == "" {
		return swarm.DefaultChildRules, 0
	}
	rs, err := swarm.ReadChildRules(path)
	if err != nil {
		return nil, refuse(stderr, verbName, "the sprint's rules file (recorded by init --rules) cannot serve: "+err.Error()+"; give --rules <file> for this "+verbName+", or run: nova-sprint init --rules <file>")
	}
	return rs, 0
}

// holdBrief holds one brief to the card lint under the rule set briefRules
// finds (rules, else the sprint's, else the general ones): add's one brief and
// brief's replacement, so a brief is held the same however it comes.
func (a *app) holdBrief(verbName, brief, rules string, c *common, st **store.Store, stderr io.Writer) int {
	rs, code := a.briefRules(verbName, rules, c, st, stderr)
	if code != 0 {
		return code
	}
	return lintBrief(verbName, brief, rs, c.max, stderr)
}

// readBriefFile is a --brief-file's brief: the file's bytes as they are, read
// whole up to briefReadCap, its one trailing newline cut.
func readBriefFile(path string) (string, error) {
	text, err := readTextFile(path, briefReadCap)
	if err != nil {
		return "", err
	}
	return briefFromFile(path, text)
}

// briefFromFile is a brief file's text, its one trailing newline cut; a file
// that holds nothing but blanks and newlines is refused naming it, since a card
// admitted with no brief is handed no task and is never linted.
func briefFromFile(path, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("%s holds no brief (it is empty); write the card's whole brief there (nova-swarm template --name card prints one to start from)", path)
	}
	return strings.TrimSuffix(text, "\n"), nil
}

// lintBrief holds one brief to the card lint's child rules and its model lines
// (lintBriefReads): the findings print on stderr in the lint's own grammar, at
// most max of them (0 is all) before a MORE line, and a brief with any is
// refused, exit 2.
func lintBrief(verbName, brief string, rules []swarm.ChildRule, max int, stderr io.Writer) int {
	modelWhy, findings := lintBriefReads(brief, rules)
	if modelWhy != "" {
		return refuse(stderr, verbName, modelLinesWhy(modelWhy))
	}
	if len(findings) == 0 {
		return 0
	}
	printed, more := findings, false
	if max > 0 && len(findings) > max {
		printed, more = findings[:max], true
	}
	for _, f := range printed {
		fmt.Fprintf(stderr, "LINT DRIFT brief %s: %d: %s remedy=%s\n", oneline.Field(f.Check), f.Line,
			oneline.Escape(oneline.Cap(f.Excerpt, oneline.TailBytes)), oneline.Escape(swarm.ChildRemedy(rules, f.Check)))
	}
	if more {
		fmt.Fprintf(stderr, "LINT MORE brief findings=%d remedy=%s --max 0\n", len(findings), verbName)
	}
	return refuse(stderr, verbName, fmt.Sprintf("the brief fails the card lint (%s); a brief is a child's whole brief and carries every rule of its rule set (--rules, else the file init --rules recorded, else the general rules); run: nova-swarm template --name card", findingsCount(len(findings))))
}

func (a *app) cmdRelease(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("release")
	reason := fs.String("reason", "", "what you looked at and found: recorded on the sentinel and in its notification")
	ans := fs.String("answers", "", "the judgment notifications this answers, comma separated; coordinator-only; one invalid answer refuses the whole step, writing nothing")
	ids, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "release", err.Error())
	}
	if len(ids) == 0 {
		return refuse(stderr, "release", "wants the sentinels it releases and --reason <text>")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "release", err.Error())
	}
	coordinator, err := st.B.Coordinator(context.Background())
	if err != nil {
		return a.readFailed("release", err, stderr)
	}
	return a.runStep("release", *c, st, store.ReleaseStep(sprint.ReleaseReq{IDs: ids, Reason: *reason, Coordinator: coordinator,
		Answers: answers(*ans), Who: c.actor}), stdout, stderr)
}

// withGroup resolves --group into ids: the group of the id, checked against
// --expect. A group of another size than expected is refused, naming what
// changed, and nothing moves.
func (a *app) withGroup(verbName string, fs flagSet, c *common, st *store.Store, s *sel, ids []string, stdout, stderr io.Writer) ([]string, int) {
	if s.group == "" {
		if s.expect != 0 {
			return nil, refuse(stderr, verbName, "--expect goes with --group <id>")
		}
		return ids, 0
	}
	if len(ids) > 0 {
		return nil, refuse(stderr, verbName, "takes ids or --group, not both")
	}
	if s.expect < 0 {
		return nil, refuse(stderr, verbName, "--expect wants the group's size, a whole number from 1")
	}
	v, g, err := groupIDs(context.Background(), st, s.group)
	if err != nil {
		if isNumber(s.group) {
			return nil, refuse(stderr, verbName, err.Error())
		}
		fmt.Fprintf(stderr, "%s %s: %s\n", prog, verbName, oneline.Escape(err.Error()))
		return nil, 1
	}
	var ans []string
	if f := fs.Lookup("answers"); f != nil {
		ans = answers(f.Value.String())
	}
	c.group = groupReport{ID: g.ID, ActedOn: len(g.Members), Expected: s.expect}
	if s.expect > 0 && len(g.Members) != s.expect {
		added, gone := groupChange(v, g, ans)
		if c.json {
			b, _ := json.Marshal(map[string]any{"error": "the group changed", "group": g.ID, "size": len(g.Members), "expected": s.expect,
				"added": nonNil(added), "gone": nonNil(gone), "members": nonNil(g.Members), "moved": []string{}})
			fmt.Fprintln(stdout, string(b))
			return nil, 1
		}
		fmt.Fprintf(stderr, "REFUSED group %s: it has %d now, not %d as printed; nothing changed; run: %s inbox --open %s and answer the group as it is now\n", oneline.Escape(g.ID), len(g.Members), s.expect, prog, oneline.Escape(g.ID))
		if ans == nil {
			listed(stderr, "NOW", g.Members, c.max, "inbox --open "+g.ID)
		} else {
			listed(stderr, "ADDED", added, c.max, "inbox --open "+g.ID)
			listed(stderr, "GONE", gone, c.max, "inbox --open "+g.ID)
		}
		fmt.Fprintf(stderr, "%s FAIL moved=0 group=%s size=%d expected=%d; run: nova-sprint inbox --open %s\n", token(verbName), oneline.Escape(g.ID), len(g.Members), s.expect, oneline.Escape(g.ID))
		return nil, 1
	}
	if len(g.Members) == 0 {
		fmt.Fprintf(stderr, "%s %s: group %s has no primaries to act on; run: nova-sprint inbox --open %s\n", prog, verbName, oneline.Escape(g.ID), oneline.Escape(g.ID))
		return nil, 1
	}
	return g.Members, 0
}

func nonNil(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

// setVerb is the shape of the verbs over a set of primaries.
func (a *app) setVerb(verbName string, args []string, stdout, stderr io.Writer, withCol bool, extra func(fs flagSet),
	need func(ids []string, s *sel) string, step func(ids []string, s *sel, c *common) store.Step) int {
	fs, c := a.verbSetup(verbName)
	var s sel
	s.register(fs, withCol)
	if extra != nil {
		extra(fs)
	}
	ids, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, verbName, err.Error())
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, verbName, err.Error())
	}
	ids, code := a.withGroup(verbName, fs, c, st, &s, ids, stdout, stderr)
	if code != 0 {
		return code
	}
	if need != nil {
		if why := need(ids, &s); why != "" {
			return refuse(stderr, verbName, why)
		}
	}
	return a.runStep(verbName, *c, st, step(ids, &s, c), stdout, stderr)
}

func (a *app) cmdResolve(args []string, stdout, stderr io.Writer) int {
	return a.setVerb("resolve", args, stdout, stderr, false, nil, nil, func(ids []string, s *sel, c *common) store.Step {
		return store.ResolveStep(sprint.ResolveReq{Sel: s.sel(ids), Who: c.actor})
	})
}

// cardGens splits <card>@<gen> words into ids and generations.
func cardGens(words []string) ([]string, map[string]int, error) {
	gens := map[string]int{}
	var ids []string
	for _, w := range words {
		id, g, ok := strings.Cut(w, "@")
		if ok {
			n, err := strconv.Atoi(g)
			if err != nil || n < 1 {
				return nil, nil, fmt.Errorf("%s: a generation is a whole number from 1", w)
			}
			gens[id] = n
		}
		ids = append(ids, id)
	}
	return ids, gens, nil
}

func (a *app) cmdTake(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("take")
	as := fs.String("as", "", "the fleet member taking its cards; several, comma separated, each take from their own ready queue in one step")
	limit := fs.Int("limit", 0, "take the first n of its ready queue (default 1); with several members, n of each")
	words, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "take", err.Error())
	}
	ids, gens, err := cardGens(words)
	if err != nil {
		return refuse(stderr, "take", err.Error())
	}
	if *as == "" || len(gens) != len(ids) {
		return refuse(stderr, "take", "wants --as <member>, and every card named as <card>@<gen>, the generation from queue --as <member>")
	}
	c.orActor(*as)
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "take", err.Error())
	}
	name := "take"
	if len(ids) > 0 {
		name = "take by id" // a report on named cards
	}
	members := sprint.Split(*as)
	c.packets = func(ctx context.Context, st *store.Store, res store.Result) []sprint.Packet {
		taken := map[string]bool{}
		for _, m := range res.Moved {
			if f := strings.Fields(m); len(f) > 0 {
				taken[f[0]] = true
			}
		}
		var mine []*sprint.Card
		for _, member := range members {
			cs, err := st.ReadCells(ctx, sprint.Fleet, member, sprint.Working)
			if err != nil {
				return nil
			}
			for _, x := range cs {
				if taken[x.ID] {
					mine = append(mine, x)
				}
			}
		}
		ps, _ := st.Packets(ctx, mine)
		return ps
	}
	if len(ids) == 0 && *limit >= 0 {
		c.after = func(ctx context.Context, st *store.Store, res store.Result) []string {
			return takeShort(ctx, st, res, members, max(*limit, 1))
		}
	}
	return a.runStep(name, *c, st, store.TakeStep(sprint.TakeReq{Sel: sprint.Sel{IDs: ids, Limit: *limit}, As: *as, Gens: gens, Who: *as}), stdout, stderr)
}

// takeShort is why a take by count took fewer than asked, one line per member
// it names: the member is not up, it is at its width (the width is hard: it
// takes another as one is finished, sprint.Take), or its ready queue is
// empty. A member that took what it asked says nothing.
func takeShort(ctx context.Context, st *store.Store, res store.Result, members []string, asked int) []string {
	taken := map[string]bool{}
	for _, m := range res.Moved {
		if f := strings.Fields(m); len(f) > 0 {
			taken[f[0]] = true
		}
	}
	s, err := st.Load(ctx, []string{sprint.Fleet}, nil)
	if err != nil {
		return []string{"why the take took what it did is not known: the fleet table did not read: " + err.Error()}
	}
	var out []string
	for _, m := range members {
		working := s.Fleet.Cell(m, sprint.Working)
		n := 0
		for _, x := range working {
			if taken[x.ID] {
				n++
			}
		}
		if n >= asked {
			continue
		}
		head := fmt.Sprintf("%s took %d of the %d asked: ", m, n, asked)
		if !s.Fleet.HasRow(m) {
			// a name the fleet table lacks takes nothing, and says so rather than an OK alone
			out = append(out, head+"it is no member of the fleet table (members: "+orDashStr(strings.Join(s.Fleet.Rows(), ","), "none")+"); run: nova-sprint fleet up "+m+" --width <n>")
			continue
		}
		switch status := s.MemberCtl(m).F("status"); {
		case status != sprint.Up:
			out = append(out, head+"it is "+orDashStr(status, "-")+", and only a member up takes")
		case len(working) >= s.Width(m):
			out = append(out, fmt.Sprintf("%sit is at its width, %d working of %d; it takes another as it finishes one", head, len(working), s.Width(m)))
		case len(s.Fleet.Cell(m, sprint.Ready)) == 0:
			out = append(out, head+"its ready queue is empty")
		}
	}
	return out
}

func (a *app) cmdFinish(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("finish")
	as := fs.String("as", "", "the fleet member finishing its cards; several, comma separated, each finishing its own named cards in one step")
	failed := fs.Bool("failed", false, "the work failed (default: ok)")
	head := fs.String("head", "", "the commit the work finished at, the head land merges (default: the card's id, for a run with no git: land refuses a head that is not a commit id)")
	report := fs.String("report", "", "the worker's report")
	branch := fs.String("branch", "", "the branch the work is on (its packet names the one to use)")
	baseBranch := fs.String("base", "", "the branch the work started from")
	usage := fs.String("usage", "", "what the run spent, one line (the member passes its child's budget, wall, tokens by class and cost): kept on the attempt's record, timed and priced")
	words, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "finish", err.Error())
	}
	ids, gens, err := cardGens(words)
	if err != nil {
		return refuse(stderr, "finish", err.Error())
	}
	if *as == "" || len(ids) == 0 || len(gens) != len(ids) {
		return refuse(stderr, "finish", "wants --as <member> and every card as <card>@<gen>, the generation the worker holds")
	}
	c.orActor(*as)
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "finish", err.Error())
	}
	return a.runStep("finish", *c, st, store.FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: ids}, As: *as, Gens: gens, Failed: *failed,
		Head: *head, Report: *report, Branch: *branch, Base: *baseBranch, Usage: *usage, Who: *as}), stdout, stderr)
}

func (a *app) cmdAsk(args []string, stdout, stderr io.Writer) int {
	var another *bool
	var ans, instead *string
	return a.setVerb("ask", args, stdout, stderr, false, func(fs flagSet) {
		another = fs.Bool("another", false, "one more reader for a primary already asked")
		ans = fs.String("answers", "", "the judgment notifications this answers, comma separated; coordinator-only; one invalid answer refuses the whole step, writing nothing")
		instead = fs.String("instead", "", "take back this reader's read (asked or reading) of the one primary named and ask one other reader, as --another chooses")
	}, func(ids []string, s *sel) string {
		if *instead != "" && s.group != "" {
			return "--instead takes back one read of one primary and asks one other reader: ask <primary> --instead <reader>, with no --another, --group, --stream or --limit; nothing was changed"
		}
		return ""
	}, func(ids []string, s *sel, c *common) store.Step {
		return store.AskStep(sprint.AskReq{Sel: s.sel(ids), Another: *another, Answers: answers(*ans), Who: c.actor, Instead: *instead})
	})
}

func (a *app) cmdRead(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("read")
	as := fs.String("as", "", "the reader; use read-card IDs from queue --as <reader>; several readers, comma separated, each reporting its own named read cards in one step")
	begin := fs.Bool("begin", false, "asked -> reading")
	ok := fs.Bool("ok", false, "the read found it good")
	broken := fs.Bool("broken", false, "the read found it broken")
	finding := fs.String("finding", "", "what the read found")
	limit := fs.Int("limit", 0, "the first n of the reader's queue (default 1)")
	ret := fs.String("return", "", "hand back a read the reader holds and has no verdict on: not a read; the next tick asks it of another reader free at the attempt, or of this reader again; no finding against the work")
	reason := fs.String("reason", "", "with --return: why the read has no verdict (it reaches the inbox)")
	usage := fs.String("usage", "", "with --ok, --broken or --return: what the read spent, one line (the reader passes its child's tokens, wall and cost): kept on the read card, timed and priced")
	ids, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "read", err.Error())
	}
	n := 0
	for _, b := range []bool{*begin, *ok, *broken, *ret != ""} {
		if b {
			n++
		}
	}
	if *as == "" || n != 1 {
		return refuse(stderr, "read", "wants --as <reader> and one of --begin, --ok, --broken, --return <card> --reason <text>")
	}
	if *ret != "" {
		if len(ids) > 0 || *reason == "" {
			return refuse(stderr, "read", "--return names its one card and wants --reason <text>")
		}
		ids = []string{*ret}
		c.actor = *as // the returner is the reader, whoever runs the verb
	}
	verdict := "ok"
	if *broken {
		verdict = "broken"
	}
	c.orActor(*as)
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "read", err.Error())
	}
	if len(ids) == 0 {
		col := sprint.Reading
		if *begin {
			col = sprint.Asked
		}
		c.after = func(ctx context.Context, st *store.Store, res store.Result) []string {
			return readShort(ctx, st, res, sprint.Split(*as), col)
		}
	}
	return a.runStep("read", *c, st, store.ReadStep(sprint.ReadReq{Sel: sprint.Sel{IDs: ids, Limit: *limit}, As: *as, Begin: *begin,
		Verdict: verdict, Finding: *finding, Return: *ret != "", Reason: *reason, Usage: *usage, Who: *as}), stdout, stderr)
}

// readShort is why a read by queue moved nothing, one line per reader named: the
// name is no row of the readers table, or the reader holds no read card in the
// column the verb moves from (asked for --begin, reading for a verdict). A read
// that moved a card says nothing.
func readShort(ctx context.Context, st *store.Store, res store.Result, readers []string, col sprint.State) []string {
	if len(res.Moved) > 0 {
		return nil
	}
	rows, err := st.ReaderRows(ctx)
	if err != nil {
		return []string{"why the read moved nothing is not known: the readers table did not read: " + err.Error()}
	}
	var out []string
	for _, r := range readers {
		if !slices.Contains(rows, r) {
			out = append(out, r+" read nothing: it is no reader of the readers table (readers: "+orDashStr(strings.Join(rows, ","), "none")+"); run: nova-sprint reader add "+r)
			continue
		}
		if cs, err := st.ReadCells(ctx, sprint.Readers, r, col); err == nil && len(cs) == 0 {
			out = append(out, r+" read nothing: it holds no read card "+col+"; run: nova-sprint queue --as "+r)
		}
	}
	return out
}

func (a *app) cmdAccept(args []string, stdout, stderr io.Writer) int {
	var readOK *bool
	var ans *string
	return a.setVerb("accept", args, stdout, stderr, false, func(fs flagSet) {
		readOK = fs.Bool("read-ok", false, "every primary in review with ok reads from two different readers; moves eligible primaries into the merge queue")
		ans = fs.String("answers", "", "the judgment notifications this answers, comma separated; coordinator-only; one invalid answer refuses the whole step, writing nothing")
	}, func(ids []string, s *sel) string {
		if len(ids) == 0 && s.stream == "" && !*readOK && s.limit == 0 {
			return "wants ids, --stream <s>, --read-ok or --group <id>"
		}
		return ""
	}, func(ids []string, s *sel, c *common) store.Step {
		r := sprint.AcceptReq{Sel: s.sel(ids), Answers: answers(*ans), Who: c.actor}
		if s.group != "" {
			r.Sel = sprint.Sel{Only: ids} // a selection: the eligible move
		}
		return store.AcceptStep(r)
	})
}

func (a *app) cmdRework(args []string, stdout, stderr io.Writer) int {
	var fix, ans *string
	return a.setVerb("rework", args, stdout, stderr, false, func(fs flagSet) {
		fix = fs.String("fix", "", "the fix for every primary; without it each takes its own: the finding of its broken read, or the report of its failed work")
		ans = fs.String("answers", "", "the judgment notifications this answers, comma separated; coordinator-only; one invalid answer refuses the whole step, writing nothing")
	}, func(ids []string, s *sel) string {
		if len(ids) == 0 && s.stream == "" {
			return "wants ids (or --group, --stream); --fix <text> for all, else each primary's own finding or report"
		}
		return ""
	}, func(ids []string, s *sel, c *common) store.Step {
		return store.ReworkStep(sprint.ReworkReq{Sel: s.sel(ids), Fix: *fix, Answers: answers(*ans), Who: c.actor})
	})
}

func (a *app) cmdReturn(args []string, stdout, stderr io.Writer) int {
	var reason, ans *string
	return a.setVerb("return", args, stdout, stderr, false, func(fs flagSet) {
		reason = fs.String("reason", "", "why it goes back to review")
		ans = fs.String("answers", "", "the judgment notifications this answers, comma separated; coordinator-only; one invalid answer refuses the whole step, writing nothing")
	}, func(ids []string, s *sel) string {
		if len(ids) == 0 && s.stream == "" {
			return "wants ids, --stream <s> or --group <id>"
		}
		return ""
	}, func(ids []string, s *sel, c *common) store.Step {
		return store.ReturnStep(sprint.ReturnReq{Sel: s.sel(ids), Reason: *reason, Answers: answers(*ans), Who: c.actor})
	})
}

func (a *app) cmdDrop(args []string, stdout, stderr io.Writer) int {
	var reason, ans *string
	return a.setVerb("drop", args, stdout, stderr, true, func(fs flagSet) {
		reason = fs.String("reason", "", "why it leaves the table; kept with its record")
		ans = fs.String("answers", "", "the judgment notifications this answers, comma separated; coordinator-only; one invalid answer refuses the whole step, writing nothing")
	}, func(ids []string, s *sel) string {
		if *reason == "" || len(ids) == 0 && s.stream == "" && s.col == "" {
			return "wants ids (or --stream/--col, --group) and --reason <text>"
		}
		return ""
	}, func(ids []string, s *sel, c *common) store.Step {
		return store.DropStep(sprint.DropReq{Sel: s.sel(ids), Reason: *reason, Answers: answers(*ans), Who: c.actor})
	})
}

// cmdBrief replaces the brief of a primary that has not started (the owner,
// 2026-10-01: "What other things should you be able to do to mutate a stopped
// sprint" / "I don't want you manually hopping in and working around it and
// doing manual stuff."): the brief held to the card lint as add's is
// (holdBrief), then one step (sprint.Brief), refused on a RUNNING machine and
// for a card dealt; the card keeps its id, stream, score and needs.
func (a *app) cmdBrief(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("brief")
	brief := fs.String("brief", "", fmt.Sprintf("the new brief: a child's whole brief, at most %d KiB, held to the card lint as add holds one (--rules, else the file init --rules recorded, else the built-in general rules) and refused, exit 2, nothing written, when it fails", cardlimits.MaxBriefBytes>>10))
	briefFile := fs.String("brief-file", "", "the new brief, read from this file: its bytes as they are, its one trailing newline cut; not with --brief")
	rules := fs.String("rules", "", "the child rules file the brief is held to (default: the file init --rules recorded, else the built-in general rules)")
	ids, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "brief", err.Error())
	}
	if len(ids) != 1 || (*brief == "") == (*briefFile == "") {
		return refuse(stderr, "brief", "wants one primary and one of --brief <text>, --brief-file <path>")
	}
	if *briefFile != "" {
		text, err := readBriefFile(*briefFile)
		if err != nil {
			return refuse(stderr, "brief", "--brief-file: "+err.Error())
		}
		*brief = text
	}
	var st *store.Store
	if code := a.holdBrief("brief", *brief, *rules, c, &st, stderr); code != 0 {
		return code
	}
	c.says = append(c.says, unfilledSays("the brief of "+ids[0], *brief)...)
	return a.runStep("brief", *c, st, store.BriefStep(sprint.BriefReq{ID: ids[0], Brief: *brief, Who: c.actor}), stdout, stderr)
}

// cmdMove moves unstarted primaries to another stream (the owner,
// 2026-10-01: "What other things should you be able to do to mutate a stopped
// sprint" / "I don't want you manually hopping in and working around it and
// doing manual stuff."): one step (sprint.MoveCards), on a STOPPED machine,
// each card waiting or ready with nothing dealt, placed as add places cards,
// all or none.
func (a *app) cmdMove(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("move")
	stream := fs.String("stream", "", "the stream the cards move to: one of the sprint's, or a new one, made as add makes it")
	score := fs.String("score", "", "the first card's score in its new line; the rest follow it (default: after every primary)")
	before := fs.String("before", "", "place the cards in line in front of this primary of the stream")
	after := fs.String("after", "", "place the cards in line after this primary of the stream")
	ids, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "move", err.Error())
	}
	if len(ids) == 0 || *stream == "" {
		return refuse(stderr, "move", "wants ids and --stream <s>")
	}
	r := sprint.MoveReq{IDs: ids, Stream: *stream, Before: *before, After: *after, Who: c.actor}
	if *score != "" {
		f, err := strconv.ParseFloat(*score, 64)
		if err != nil {
			return refuse(stderr, "move", "--score wants a number")
		}
		r.Score = &f
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "move", err.Error())
	}
	return a.runStep("move", *c, st, store.MoveStep(r), stdout, stderr)
}

func (a *app) cmdRank(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("rank")
	score := fs.String("score", "", "the new score of the first id; the rest follow it")
	first := fs.Bool("first", false, "ahead of every primary")
	ans := fs.String("answers", "", "the judgment notifications this answers, comma separated; coordinator-only; one invalid answer refuses the whole step, writing nothing")
	ids, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "rank", err.Error())
	}
	if len(ids) == 0 || (*score == "") == !*first {
		return refuse(stderr, "rank", "wants ids and one of --score <n>, --first")
	}
	r := sprint.RankReq{IDs: ids, First: *first, Answers: answers(*ans), Who: c.actor}
	if *score != "" {
		f, err := strconv.ParseFloat(*score, 64)
		if err != nil {
			return refuse(stderr, "rank", "--score wants a number")
		}
		r.Score = &f
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "rank", err.Error())
	}
	return a.runStep("rank", *c, st, store.RankStep(r), stdout, stderr)
}

func (a *app) cmdMerge(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("merge")
	stream := fs.String("stream", "", "the stream whose queued batches are selected to merge and land")
	batch := fs.Int("batch", 10, "the batch: the head n of the stream's queue")
	conflict := fs.String("conflict", "", "fact: this card of the batch did not merge")
	cross := fs.String("cross", "", "fact: <card>=<other>: the card needs <other> first; <other> is on the table, in another stream, not landed")
	red := fs.Bool("red", false, "fact: the stream branch went red on the batch")
	rejected := fs.Bool("rejected", false, "fact: the merge queue rejected the batch")
	note := fs.String("note", "", "what the facts' source said")
	var suspects listFlag
	fs.Var(&suspects, "suspect", "with --red: a card of the batch suspected of turning it red; again, comma separated, or ids after it for more")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "merge", err.Error())
	}
	if len(suspects) > 0 {
		suspects, pos = append(suspects, pos...), nil
		if !*red {
			return refuse(stderr, "merge", "--suspect goes with --red")
		}
	}
	facts := 0
	for _, f := range []bool{*conflict != "", *cross != "", *red, *rejected} {
		if f {
			facts++
		}
	}
	if *stream == "" || len(pos) > 0 || facts > 1 {
		return refuse(stderr, "merge", "wants --stream <s> and at most one fact of --conflict, --cross, --red, --rejected")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "merge", err.Error())
	}
	return a.runStep("merge", *c, st, store.MergeStep(sprint.MergeReq{Stream: *stream, Batch: *batch, Conflict: *conflict, Cross: *cross,
		Red: *red, Suspects: suspects, Rejected: *rejected, Note: *note, Who: c.actor}), stdout, stderr)
}

func (a *app) cmdResume(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("resume")
	stream := fs.String("stream", "", "the stopped stream")
	did := fs.String("did", "", "what the coordinator did about the cause; required after a red branch")
	ans := fs.String("answers", "", "the judgment notifications this answers, comma separated; coordinator-only; one invalid answer refuses the whole step, writing nothing")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "resume", err.Error())
	}
	if *stream == "" || len(pos) > 0 {
		return refuse(stderr, "resume", "wants --stream <s>")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "resume", err.Error())
	}
	return a.runStep("resume", *c, st, store.ResumeStep(sprint.ResumeReq{Stream: *stream, Did: *did, Answers: answers(*ans), Who: c.actor}), stdout, stderr)
}

func (a *app) cmdFleet(op string, args []string, stdout, stderr io.Writer) int {
	name := "fleet " + op
	fs, c := a.verbSetup(name)
	var width *string
	if op == "up" {
		width = fs.String("width", "", fmt.Sprintf("the member's width: the most work cards it runs at once; the deal holds it at %d times that, ready and working; 1 to %d (default: as it is, %d for a new member)", sprint.DealAhead, sprint.MaxWidth, sprint.DefaultWidth))
	}
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if (op == "level") != (len(pos) == 0) || len(pos) > 1 {
		return refuse(stderr, name, "wants one member (level takes none)")
	}
	w := 0
	if width != nil && *width != "" {
		if w, err = sprint.ParseWidth(*width); err != nil {
			return refuse(stderr, name, "--width: "+err.Error())
		}
	}
	member := ""
	if len(pos) == 1 {
		member = pos[0]
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	return a.runStep(name, *c, st, a.fleetStep(st, op, member, c.actor, w), stdout, stderr)
}

func (a *app) cmdReaderAdd(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("reader add")
	names, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "reader add", err.Error())
	}
	if len(names) == 0 {
		return refuse(stderr, "reader add", "wants at least one reader")
	}
	for _, n := range names {
		if !sprint.ValidID(n) {
			return refuse(stderr, "reader add", "a reader name wants letters, digits, _ and -: "+n)
		}
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "reader add", err.Error())
	}
	if err := st.B.RowsAdd(context.Background(), st.Names.Table(sprint.Readers), names); err != nil {
		fmt.Fprintf(stderr, "%s reader add: %s\n", prog, oneline.Escape(err.Error()))
		return 1
	}
	sayOK(stdout, c.json, "reader add", "READER-ADD OK readers="+strings.Join(names, ","), map[string]any{"readers": names})
	return 0
}

// readerNames are the readers a reader verb names: at least one, each a name
// sprint.ValidID accepts.
func readerNames(verbName string, args []string, stderr io.Writer, fs flagSet) ([]string, int) {
	names, err := parse(fs, args)
	if err != nil {
		return nil, refuse(stderr, verbName, err.Error())
	}
	if len(names) == 0 {
		return nil, refuse(stderr, verbName, "wants at least one reader")
	}
	for _, n := range names {
		if !sprint.ValidID(n) {
			return nil, refuse(stderr, verbName, "a reader name wants letters, digits, _ and -: "+n)
		}
	}
	return names, 0
}

// unknownReaders is the named readers the readers table has no row for.
func unknownReaders(rows, names []string) []string {
	var out []string
	for _, n := range names {
		if !slices.Contains(rows, n) {
			out = append(out, n)
		}
	}
	return out
}

// cmdReaderHold is reader away (away) and reader up: the coordinator holds the
// named readers away, whatever they beat, or releases the hold (the state is
// then the beat's). A named reader with no row refuses the whole call, and
// nothing is written (docs/SPEC-SPRINT.md section 6).
func (a *app) cmdReaderHold(away bool, args []string, stdout, stderr io.Writer) int {
	verbName := map[bool]string{true: "reader away", false: "reader up"}[away]
	fs, c := a.verbSetup(verbName)
	names, code := readerNames(verbName, args, stderr, fs)
	if code != 0 {
		return code
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, verbName, err.Error())
	}
	ctx := context.Background()
	rows, err := st.ReaderRows(ctx)
	if err != nil {
		return a.readFailed(verbName, err, stderr)
	}
	if bad := unknownReaders(rows, names); len(bad) > 0 {
		fmt.Fprintf(stderr, "%s %s: no reader %s on the readers table (readers: %s); nothing was changed; run: nova-sprint reader add <name>\n", prog, verbName, strings.Join(bad, ","), strings.Join(rows, ","))
		return 1
	}
	for _, n := range names {
		if err := st.SetReaderAway(ctx, n, away, c.actor); err != nil {
			fmt.Fprintf(stderr, "%s %s: %s\n", prog, verbName, oneline.Escape(err.Error()))
			return 1
		}
	}
	sayOK(stdout, c.json, verbName, token(verbName)+" OK readers="+strings.Join(names, ","), map[string]any{"readers": names})
	return 0
}

// cmdReaderRemove takes the named readers off the readers table (the mirror of
// reader add), in one write: refused, exit 1 and nothing written, when a named
// reader is no row of the table or holds a read card (asked, reading, ok or
// broken: the row delete would take the card's place with it), naming the
// reader and the read cards it holds. The model tla/SprintEvents.tla holds
// the readers as a constant set with no add or remove action; the presence of
// a reader is tla/DirtyTick.tla's.
func (a *app) cmdReaderRemove(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("reader remove")
	names, code := readerNames("reader remove", args, stderr, fs)
	if code != 0 {
		return code
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "reader remove", err.Error())
	}
	ctx := context.Background()
	rows, err := st.ReaderRows(ctx)
	if err != nil {
		return a.readFailed("reader remove", err, stderr)
	}
	if bad := unknownReaders(rows, names); len(bad) > 0 {
		fmt.Fprintf(stderr, "%s reader remove: no reader %s on the readers table (readers: %s); nothing was changed\n", prog, strings.Join(bad, ","), strings.Join(rows, ","))
		return 1
	}
	var holds []string
	for _, n := range names {
		cs, err := st.ReadCells(ctx, sprint.Readers, n, sprint.Asked, sprint.Reading, sprint.OK, sprint.Broken)
		if err != nil {
			return a.readFailed("reader remove", err, stderr)
		}
		var ids []string
		for _, x := range cs {
			ids = append(ids, x.ID+" ("+x.Col+")")
		}
		if len(ids) > 0 {
			holds = append(holds, n+" holds "+sprint.Preview(ids, ", "))
		}
	}
	if len(holds) > 0 {
		fmt.Fprintf(stderr, "%s reader remove: %s; nothing was changed; a read moves on (read --as <reader>), is sent to another reader (ask --another), or leaves with its primary (rework, drop)\n", prog, oneline.Escape(strings.Join(holds, "; ")))
		return 1
	}
	if err := st.B.RowsDel(ctx, st.Names.Table(sprint.Readers), names); err != nil {
		fmt.Fprintf(stderr, "%s reader remove: %s\n", prog, oneline.Escape(err.Error()))
		return 1
	}
	// the rows are gone: their beats and holds go with them
	if err := st.ForgetReaders(ctx, names); err != nil {
		fmt.Fprintf(stderr, "%s reader remove: the rows were removed, their records were not: %s\n", prog, oneline.Escape(err.Error()))
		return 1
	}
	sayOK(stdout, c.json, "reader remove", "READER-REMOVE OK readers="+strings.Join(names, ","), map[string]any{"readers": names})
	return 0
}

// streamWords is what a stream is to the coordinator's verbs, in nova-sprint
// help and nova-sprint help stream.
func streamWords() string {
	return strings.TrimSpace(`
The streams: add --stream <s> opens a stream, its row of the work and merge
tables, and a clear keeps it. stream remove takes streams off both tables, on
a STOPPED machine only (nova-sprint stop first), refused while a stream holds a
card (a primary or a sentinel in any column of its work row, a merge card in
its merge row: nova-sprint clear --confirm sprint, or drop) and all or none
for the streams named. A clear does not bring a removed stream back, and its
name is added again only after the next clear.`) + "\n"
}

// cmdStreamRemove takes the named streams off the work and merge tables (the
// owner, 2026-10-01: "you should have a verb to remove work streams" / "they
// should only succeed on a STOPPED sprint machine"): each stream's row of both
// tables, with the stream's control card the merge row holds, the one card add
// made for it. Refused, exit 1 and nothing written, on a RUNNING machine, for
// a stream that is no row, or for one that holds a card (sprint.StreamRemove),
// all or none for the streams named.
func (a *app) cmdStreamRemove(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("stream remove")
	names, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "stream remove", err.Error())
	}
	if len(names) == 0 {
		return refuse(stderr, "stream remove", "wants at least one stream")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "stream remove", err.Error())
	}
	ctx := context.Background()
	m, _, err := st.Machine(ctx)
	if err != nil {
		return a.readFailed("stream remove", err, stderr)
	}
	s, err := st.Load(ctx, []string{sprint.Work, sprint.Merge}, nil)
	if err != nil {
		return a.readFailed("stream remove", err, stderr)
	}
	if refused := sprint.StreamRemove(s, m.Running(), names); len(refused) > 0 {
		var whys []string
		for _, r := range refused {
			whys = append(whys, r.Key+": "+r.Why)
		}
		fmt.Fprintf(stderr, "%s stream remove: %s; run: nova-sprint help stream\n", prog, oneline.Escape(strings.Join(whys, "; ")))
		return 1
	}
	for _, t := range []string{sprint.Work, sprint.Merge} {
		if err := st.B.RowsDel(ctx, st.Names.Table(t), names); err != nil {
			fmt.Fprintf(stderr, "%s stream remove: %s; run it again to finish\n", prog, oneline.Escape(err.Error()))
			return 1
		}
	}
	fmt.Fprintf(stdout, "STREAM-REMOVE OK streams=%s\n", strings.Join(names, ","))
	return 0
}

func (a *app) cmdCI(args []string, stdout, stderr io.Writer) int {
	var red, green *bool
	var head, run, source, note *string
	return a.setVerb("ci", args, stdout, stderr, false, func(fs flagSet) {
		red = fs.Bool("red", false, "the run failed")
		green = fs.Bool("green", false, "the run passed")
		head = fs.String("head", "", "the head the run tested (default: the primary's)")
		run = fs.String("run", "", "the run's id: a retried report of it is recorded once")
		source = fs.String("source", "", "where the result comes from")
		note = fs.String("note", "", "what the run said")
	}, func(ids []string, s *sel) string {
		if *red == *green || len(ids) == 0 && s.stream == "" {
			return "wants ids (or --stream, --group) and one of --red, --green"
		}
		return ""
	}, func(ids []string, s *sel, c *common) store.Step {
		return store.CIStep(sprint.CIReq{Sel: s.sel(ids), Red: *red, Head: *head, Run: *run, Source: *source, Note: *note, Who: c.actor})
	})
}

func (a *app) cmdWait(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("wait")
	dur := fs.Duration("for", 0, "review it again after this long")
	until := fs.String("until", "", "review it again at this time (RFC3339)")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "wait", err.Error())
	}
	if len(pos) != 1 || (*dur == 0) == (*until == "") {
		return refuse(stderr, "wait", "wants one notification id and one of --for <duration>, --until <time>")
	}
	at := a.now().Add(*dur)
	if *until != "" {
		if at, err = time.Parse(time.RFC3339, *until); err != nil {
			return refuse(stderr, "wait", "--until wants an RFC3339 time")
		}
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "wait", err.Error())
	}
	res, held, err := st.Wait(context.Background(), pos[0], at)
	if err == nil && len(res.Refused) > 0 {
		err = fmt.Errorf("%s", res.Refused[0].Why)
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s wait: %s\n", prog, oneline.Escape(err.Error()))
		return 1
	}
	if held {
		fmt.Fprintf(stdout, "WAIT OK note=%s held until=%s of running time: the tick raises it again then if it still holds\n", oneline.Escape(pos[0]), at.UTC().Format(time.RFC3339))
		return 0
	}
	fmt.Fprintf(stdout, "WAIT OK note=%s review=%s\n", oneline.Escape(pos[0]), at.UTC().Format(time.RFC3339))
	return 0
}

func (a *app) cmdAck(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("ack")
	reason := fs.String("reason", "", "why nothing is to be done")
	notes, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "ack", err.Error())
	}
	if len(notes) == 0 || *reason == "" {
		return refuse(stderr, "ack", "wants notification ids and --reason <text>")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "ack", err.Error())
	}
	return a.runStep("ack", *c, st, store.AckStep(sprint.AckReq{Notes: notes, Reason: *reason, Who: c.actor}), stdout, stderr)
}

func (a *app) cmdRepair(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("repair")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "repair", argErr("takes no words ", err, pos...))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "repair", err.Error())
	}
	rr, err := st.Repair(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "%s repair: %s\n", prog, oneline.WithRemedy(err.Error(), prog+" repair -h"))
		return 2
	}
	if c.json {
		if rr == nil {
			rr = []store.RepairResult{}
		}
		b, _ := json.Marshal(map[string]any{"repaired": rr})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	code := 0
	for _, r := range rr {
		fmt.Fprintf(stdout, "OPERATION %s verb=%s done=%s detail=%s\n", oneline.Escape(r.Op), oneline.Escape(r.Verb), r.Done, oneline.Escape(r.Detail))
		if r.Done == "open" {
			code = 1
		}
	}
	status := "OK"
	if code != 0 {
		status = "FAIL"
	}
	fmt.Fprintf(stdout, "REPAIR %s operations=%d\n", status, len(rr))
	return code
}

// confirmName is what clear and teardown want after --confirm: the name of the
// sprint's view, sprint.
func confirmName() string { return sprint.Names{}.View() }

func (a *app) cmdTeardown(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("teardown")
	confirm := fs.String("confirm", "", "the sprint's name, to confirm: the name of its view, sprint")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "teardown", argErr("takes no words ", err, pos...))
	}
	want := confirmName()
	if *confirm != want {
		return refuse(stderr, "teardown", "drops the four tables, the view and every key of the sprint; wants --confirm "+want+" (the name of the sprint's view)")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "teardown", err.Error())
	}
	n, err := st.Teardown(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "%s teardown: %s\n", prog, oneline.Escape(err.Error()))
		return 1
	}
	sayOK(stdout, c.json, "teardown", fmt.Sprintf("TEARDOWN OK sprint=%s keys=%d", oneline.Escape(want), n), map[string]any{"sprint": want, "keys": n})
	return 0
}

func (a *app) cmdClear(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("clear")
	confirm := fs.String("confirm", "", "the sprint's name, to confirm: the name of its view, sprint")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "clear", argErr("takes no words ", err, pos...))
	}
	want := confirmName()
	if *confirm != want {
		return refuse(stderr, "clear", "stops the sprint and clears all work in it (a new epoch; the old one stays readable with --at-epoch); wants --confirm "+want+" (the name of the sprint's view)")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "clear", err.Error())
	}
	ctx := context.Background()
	res, err := st.Clear(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "%s clear: %s\n", prog, oneline.WithRemedy(err.Error(), prog+" clear -h"))
		return 2
	}
	var held []string
	for _, k := range []string{"primaries", "work cards", "read cards", "merge cards", "open judgments"} {
		held = append(held, strings.ReplaceAll(k, " ", "_")+"="+strconv.Itoa(res.Held[k]))
	}
	extra := ""
	if res.Finished != "" {
		extra += " finished=" + oneline.Escape(res.Finished)
	}
	if res.Abandoned != "" {
		extra += " abandoned=" + oneline.Escape(res.Abandoned)
	}
	if res.Restored {
		extra += " restored=yes"
	}
	if res.Machine != "" {
		extra += " machine=" + res.Machine + "->" + store.Stopped
	}
	line := sprintLine(ctx, st)
	if c.json {
		heldN := map[string]int{}
		for k, v := range res.Held {
			heldN[strings.ReplaceAll(k, " ", "_")] = v
		}
		sayOK(stdout, true, "clear", "", map[string]any{"from": res.From, "to": res.To, "at": res.At.UTC().Format(time.RFC3339), "held": heldN,
			"finished": res.Finished, "abandoned": res.Abandoned, "restored": res.Restored, "machine_was": res.Machine, "sprint": line})
		return 0
	}
	fmt.Fprintf(stdout, "CLEAR OK epoch=%d->%d at=%s held: %s%s\n", res.From, res.To, res.At.UTC().Format(time.RFC3339), strings.Join(held, " "), extra)
	if line != "" {
		fmt.Fprintln(stdout, line)
	}
	if res.Machine == store.Running {
		fmt.Fprintf(stdout, "the machine is STOPPED; when the new sprint is ready: nova-sprint start\n")
	}
	return 0
}
