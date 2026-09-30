package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
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
		{"init", "[--readers <a,b,...>] [--members <m1[:<width>],m2,...>] [--coordinator <name>]", "init --readers reader-a,reader-b,reader-c --members m1:64,m2:64", (*app).cmdInit},
		{"add", "--stream <s> (<id>... | --count <n> | --sentinel <id>) [--needs <a,b>] [--before <id> | --after <id> | --score <n>] [--brief <text> | --brief-file <path>]", "add --stream s1 --count 100", (*app).cmdAdd},
		{"release", "<sentinel>... --reason <text> [--answers <note>]", "release s1-stop --reason 'the layer is green and read'", (*app).cmdRelease},
		{"resolve", "[<id>...] [--stream <s>] [--limit <n>]", "resolve", (*app).cmdResolve},
		{"start", "", "start", (*app).cmdMachineStart},
		{"stop", "", "stop", (*app).cmdMachineStop},
		{"run", "", "run", (*app).cmdRun},
		{"tick", "", "tick", (*app).cmdTick},
		{"goal set", "<name> [--file <path>] [--to file:<path>]", "goal set friend-a --file goal-a.txt --to file:/tmp/reminder-a.txt", (*app).cmdGoalSet},
		{"goal show", "[<name>]", "goal show friend-a", (*app).cmdGoalShow},
		{"goal drop", "<name>", "goal drop friend-a", (*app).cmdGoalDrop},
		{"take", "--as <member> [<card>@<gen>...] [--limit <n>]", "take --as m1 s1-1.w1@1", (*app).cmdTake},
		{"finish", "--as <member> <card>@<gen>... [--failed] [--head <h>] [--report <text>]", "finish --as m1 s1-1.w1@1", (*app).cmdFinish},
		{"ask", "[<id>... | --group <id> [--expect <n>]] [--stream <s>] [--limit <n>] [--another] [--answers <note>]", "ask", (*app).cmdAsk},
		{"queue", "--as <reader|member> | --stream <s>", "queue --as reader-a", (*app).cmdQueue},
		{"read", "--as <reader> (--begin | --ok | --broken) [<card>...] [--limit <n>] [--finding <text>]", "read --as reader-a --ok --limit 5", (*app).cmdRead},
		{"accept", "(<id>... | --stream <s> | --read-ok | --group <id> [--expect <n>]) [--answers <note>]", "accept --read-ok", (*app).cmdAccept},
		{"rework", "(<id>... | --group <id> [--expect <n>]) [--fix <text>] [--answers <note>]", "rework s1-4 --fix 'handle the empty case'", (*app).cmdRework},
		{"return", "(<id>... | --group <id> [--expect <n>]) [--reason <text>] [--answers <note>]", "return s1-7 --reason 'suspect of the red batch'", (*app).cmdReturn},
		{"drop", "(<id>... | --stream <s> --col <state> | --group <id> [--expect <n>]) --reason <text> [--answers <note>]", "drop s1-9 --reason obsolete", (*app).cmdDrop},
		{"rank", "<id>... (--score <n> | --first) [--answers <note>]", "rank s2-3 --first", (*app).cmdRank},
		{"merge", "--stream <s> [--batch <n>] [--conflict <id> | --cross <id>=<other> | --red [--suspect <id>...] | --rejected] [--note <text>]", "merge --stream s1 --batch 100", (*app).cmdMerge},
		{"resume", "--stream <s> [--did <text>] [--answers <note>]", "resume --stream s1 --did 'rebased s1-4'", (*app).cmdResume},
		{"fleet beat", "<member> [--load <percent>]", "fleet beat m1", (*app).cmdFleetBeat},
		{"fleet up", "<member> [--width <n>]", "fleet up m1 --width 64", func(a *app, args []string, o, e io.Writer) int { return a.cmdFleet("up", args, o, e) }},
		{"fleet down", "<member>", "fleet down m1", func(a *app, args []string, o, e io.Writer) int { return a.cmdFleet("down", args, o, e) }},
		{"fleet sync", "[--check] [--pg <dsn>]", "fleet sync --check", (*app).cmdFleetSync},
		{"fleet level", "", "fleet level", func(a *app, args []string, o, e io.Writer) int { return a.cmdFleet("level", args, o, e) }},
		{"reader add", "<reader>...", "reader add reader-d", (*app).cmdReaderAdd},
		{"ci", "<id>... (--red | --green) [--head <h>] [--run <id>] [--source <s>] [--note <text>]", "ci s1-3 --red --run 812 --source ci", (*app).cmdCI},
		{"wait", "<note> (--for <duration> | --until <RFC3339>)", "wait tick-ask-x-1.2 --for 30m", (*app).cmdWait},
		{"ack", "<note>... --reason <text>", "ack ci-x-1.1 --reason 'a flaky runner; the rerun is green'", (*app).cmdAck},
		{"inbox", "[--open <group>] [--read] [--wait [--timeout <duration>]] [--deadline <duration>] [--stale <duration>]", "inbox --wait", (*app).cmdInbox},
		{"card", "<id>", "card s1-4", (*app).cmdCard},
		{"log", "[--card <id>] [--stream <s>] [--member <m>] [--since <10m|RFC3339>] [--at-epoch <n>]", "log --card s1-4", (*app).cmdLog},
		{"check", "", "check", (*app).cmdCheck},
		{"repair", "", "repair", (*app).cmdRepair},
		{"where", "[--watch] [--every <duration>]", "where", (*app).cmdWhere},
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

func banner() string {
	var b strings.Builder
	b.WriteString("nova-sprint: the sprint table: four tables on nova-table, the moves between them, and the coordinator's inbox\n\nusage:\n")
	for _, v := range verbs {
		b.WriteString("  nova-sprint " + strings.TrimSpace(v.name+" "+v.syntax) + "\n")
	}
	b.WriteString(`
Every store verb takes --redis <addr> (else NOVA_SPRINT_REDIS, then
NOVA_REDIS_ADDR), --actor <name> (else NOVA_SPRINT_ACTOR; no
default: a verb that writes wants one), --op <id> (the same id again returns
the recorded result), --json and --max <n> (listed items; 0 is all). The
coordinator's verbs are the coordinator's alone (the first init names it:
--coordinator, else the actor); take, finish, read and fleet beat are the
workers', whose actor is the member or reader named; merge and ci are
reports; tick and run are the machine's; the reads need no actor (inbox
--read, which moves the coordinator's cursor, is the coordinator's). A set is
ids, a stream, a column, --limit n, or an inbox group: --group <id>, the id
inbox prints, which does not move, with --expect <n>, the size it printed,
which refuses a group that has changed. Each verb prints what moved (MOVED),
what did not and why (REFUSED, on stderr), its summary line, and the sprint's
line: landed/all percent -> ETA (a stopped machine has no ETA: STOPPED, then
landed/all and the percent when there are cards; every card landed, no ETA:
done in <time from the first start> while it runs, and STOPPED ... done once
the machine has stopped itself).

The tables are work, merge, readers and fleet, and the view is sprint; a store
holds one sprint (a second sprint is a second store). clear and teardown want
--confirm sprint, the name of the view, and refuse anything else.

A work card is named with its generation, <card>@<gen>: the generation the
worker holds, from queue --as <member> (--json: "gen"). take by id and finish
name it for every card; a card named without one is refused, naming the live
generation, and a generation that is not the live one is refused as stale.
take with no card takes the member's oldest ready cards (--limit n, default 1)
and prints each one's generation.

` + inboxExample + `
` + machineWords() + `
` + fleetWords() + `
` + goalWords() + `
exit codes: 0 done, 1 refused, 2 usage or a store that did not answer (fleet sync --check: there is drift), 3 fleet sync could not read the config

`)
	return b.String()
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

func versionLine() string { return buildinfo.Line(prog, version) }

func helpCommand(path []string, stdout, stderr io.Writer) int {
	if len(path) == 0 {
		fmt.Fprint(stdout, banner())
		return 0
	}
	name := strings.Join(path, " ")
	if name == "fleet" || name == "reader" || name == "goal" {
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
		return 0
	}
	for _, v := range verbs {
		if v.name == name {
			code := func() (code int) {
				defer verbflag.Recover(stdout, prog, banner(), &code)
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
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := verbflag.Parse(fs, args); err != nil {
			if strings.Contains(err.Error(), "flag provided but not defined: -prefix") {
				return nil, errNoPrefix
			}
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		if args[0] == "--" {
			return append(pos, args[1:]...), nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
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
			if named[o.Note.ID] && in[o.Note.ID] && (o.Subject() == m || o.Note.StreamLevel && contains(o.Note.Primaries, m)) {
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

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
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

func (a *app) report(ctx context.Context, verbName string, c common, st *store.Store, res store.Result, err error, stdout, stderr io.Writer) int {
	code := 0
	if len(res.Refused) > 0 {
		code = 1
	}
	var pe *store.PendingError
	var cut *store.CutError
	var cleared *store.ClearedError
	switch {
	case err == nil:
	case errors.Is(err, store.ErrUnknown):
		code = 2
	case errors.As(err, &pe), errors.As(err, &cut), errors.As(err, &cleared):
		code = 1
	default:
		code = 2
	}
	line := sprintLine(ctx, st)
	if c.json {
		o := output{Result: res, Sprint: line, Unknown: errors.Is(err, store.ErrUnknown), Group: c.group.ID, ActedOn: c.group.ActedOn, Expected: c.group.Expected, Packets: c.handed}
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
	if res.Op != "" {
		fields += " op=" + oneline.Escape(res.Op)
	}
	if res.Replay {
		fields += " replay=yes"
	}
	if res.Pending != "" {
		fields += " pending=" + oneline.Escape(res.Pending)
	}
	if err != nil {
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
	return strings.TrimSpace(summary(shapes[0]) + "  " + machine)
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
	members := fs.String("members", "", fmt.Sprintf("fleet members to bring up, comma separated, each <name> or <name>:<width>, its width the most work cards it holds at once, ready and working (default %d)", sprint.DefaultWidth))
	coordinator := fs.String("coordinator", "", "the sprint's coordinator, the one actor who releases sentinels (default: the actor)")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "init", err.Error())
	}
	if len(pos) > 0 {
		return refuse(stderr, "init", "takes no words, found "+pos[0])
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
	fmt.Fprintf(stdout, "INIT OK tables=%s view=%s\n", strings.Join([]string{st.Names.Table(sprint.Work), st.Names.Table(sprint.Readers), st.Names.Table(sprint.Merge), st.Names.Table(sprint.Fleet)}, ","), st.Names.View())
	for _, m := range specs {
		if code := a.runStep("fleet up", *c, st, a.fleetStep(st, "up", m.Name, c.actor, m.Width), stdout, stderr); code != 0 {
			return code
		}
	}
	return 0
}

func (a *app) cmdAdd(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("add")
	stream := fs.String("stream", "", "the stream the primaries belong to, for life; with --count, several streams comma separated, one step")
	count := fs.Int("count", 0, "admit n primaries with generated ids <stream>-<n>")
	needs := fs.String("needs", "", "primaries that must land first, comma separated; each is a primary on the table")
	brief := fs.String("brief", "", "the brief: a child's whole brief, held to the card lint (every rule the coordinator gives a child; nova-swarm template --name card prints a card that passes, nova-swarm lint --rules lists them) and refused, exit 2, nothing written, when it fails; a card with no brief is not linted")
	briefFile := fs.String("brief-file", "", "the brief, read from this file: its bytes as they are, its one trailing newline cut (a brief of many paragraphs), then held to the card lint like --brief; not with --brief")
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
	if *briefFile != "" {
		if *brief != "" {
			return refuse(stderr, "add", "--brief and --brief-file are two ways to give the brief: give one")
		}
		text, err := readGoalText(*briefFile)
		if err != nil {
			return refuse(stderr, "add", "--brief-file: "+err.Error())
		}
		*brief = strings.TrimSuffix(text, "\n")
	}
	if *sentinel != "" {
		if len(ids) > 0 || *count != 0 {
			return refuse(stderr, "add", "--sentinel <id> admits one sentinel, with no other ids or --count")
		}
		ids = []string{*sentinel}
	}
	if *stream == "" || (len(ids) == 0) == (*count == 0) {
		return refuse(stderr, "add", "wants --stream and either ids, --count <n> or --sentinel <id>")
	}
	if *last && *every == 0 {
		return refuse(stderr, "add", "--sentinel-last goes with --sentinel-every <k>")
	}
	streams := sprint.Split(*stream)
	if len(streams) > 1 && *count == 0 {
		return refuse(stderr, "add", "several streams take --count <n>: each gets n cards")
	}
	// A BRIEF IS A CHILD'S WHOLE BRIEF, AND THE CARD LINT HOLDS IT TO THE RULES OF ONE: every
	// rule the coordinator gives a child is a rule of internal/swarm/lintchild.go, checked
	// here in process, before anything is written. A card with no brief (a --count card, a
	// sentinel) carries none to check.
	if *sentinel == "" && *brief != "" {
		if code := lintBrief(*brief, c.max, stderr); code != 0 {
			return code
		}
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
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "add", err.Error())
	}
	if len(rs) == 1 {
		return a.runStep("add", *c, st, store.AddStep(rs[0]), stdout, stderr)
	}
	return a.runStep("add", *c, st, store.AddEachStep(rs), stdout, stderr)
}

// lintBrief holds one brief to the card lint's child rules (swarm.LintCardChild): the
// findings print on stderr in the lint's own grammar, at most max of them (0 is all)
// before a MORE line, and a brief with any is refused, exit 2.
func lintBrief(brief string, max int, stderr io.Writer) int {
	findings := swarm.LintCardChild([]byte(brief))
	if len(findings) == 0 {
		return 0
	}
	printed, more := findings, false
	if max > 0 && len(findings) > max {
		printed, more = findings[:max], true
	}
	for _, f := range printed {
		fmt.Fprintf(stderr, "LINT DRIFT brief %s: %d: %s remedy=%s\n", oneline.Field(f.Check), f.Line,
			oneline.Escape(oneline.Cap(f.Excerpt, oneline.TailBytes)), oneline.Escape(swarm.CardChildRemedies[f.Check]))
	}
	if more {
		fmt.Fprintf(stderr, "LINT MORE brief findings=%d remedy=add --max 0\n", len(findings))
	}
	return refuse(stderr, "add", fmt.Sprintf("the brief fails the card lint (%d findings); a brief is a child's whole brief and carries every rule the coordinator gives a child; run: nova-swarm template --name card", len(findings)))
}

func (a *app) cmdRelease(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("release")
	reason := fs.String("reason", "", "what you looked at and found: recorded on the sentinel and in its notification")
	ans := fs.String("answers", "", "the judgment notifications this answers, comma separated")
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
		fmt.Fprintf(stderr, "REFUSED group %s: it has %d now, not %d as printed; nothing changed\n", oneline.Escape(g.ID), len(g.Members), s.expect)
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
	return a.runStep(name, *c, st, store.TakeStep(sprint.TakeReq{Sel: sprint.Sel{IDs: ids, Limit: *limit}, As: *as, Gens: gens, Who: *as}), stdout, stderr)
}

func (a *app) cmdFinish(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("finish")
	as := fs.String("as", "", "the fleet member finishing its cards; several, comma separated, each finishing its own named cards in one step")
	failed := fs.Bool("failed", false, "the work failed (default: ok)")
	head := fs.String("head", "", "the head the work finished at (default: the card's id)")
	report := fs.String("report", "", "the worker's report")
	branch := fs.String("branch", "", "the branch the work is on (its packet names the one to use)")
	baseBranch := fs.String("base", "", "the branch the work started from")
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
		Head: *head, Report: *report, Branch: *branch, Base: *baseBranch, Who: *as}), stdout, stderr)
}

func (a *app) cmdAsk(args []string, stdout, stderr io.Writer) int {
	var another *bool
	var ans *string
	return a.setVerb("ask", args, stdout, stderr, false, func(fs flagSet) {
		another = fs.Bool("another", false, "one more reader for a primary already asked")
		ans = fs.String("answers", "", "the judgment notifications this answers, comma separated")
	}, nil, func(ids []string, s *sel, c *common) store.Step {
		return store.AskStep(sprint.AskReq{Sel: s.sel(ids), Another: *another, Answers: answers(*ans), Who: c.actor})
	})
}

func (a *app) cmdRead(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("read")
	as := fs.String("as", "", "the reader; several, comma separated, each reporting its own named cards in one step")
	begin := fs.Bool("begin", false, "asked -> reading")
	ok := fs.Bool("ok", false, "the read found it good")
	broken := fs.Bool("broken", false, "the read found it broken")
	finding := fs.String("finding", "", "what the read found")
	limit := fs.Int("limit", 0, "the first n of the reader's queue (default 1)")
	ids, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "read", err.Error())
	}
	n := 0
	for _, b := range []bool{*begin, *ok, *broken} {
		if b {
			n++
		}
	}
	if *as == "" || n != 1 {
		return refuse(stderr, "read", "wants --as <reader> and one of --begin, --ok, --broken")
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
	return a.runStep("read", *c, st, store.ReadStep(sprint.ReadReq{Sel: sprint.Sel{IDs: ids, Limit: *limit}, As: *as, Begin: *begin,
		Verdict: verdict, Finding: *finding, Who: *as}), stdout, stderr)
}

func (a *app) cmdAccept(args []string, stdout, stderr io.Writer) int {
	var readOK *bool
	var ans *string
	return a.setVerb("accept", args, stdout, stderr, false, func(fs flagSet) {
		readOK = fs.Bool("read-ok", false, "every primary in review with ok reads from two different readers")
		ans = fs.String("answers", "", "the judgment notifications this answers, comma separated")
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
		ans = fs.String("answers", "", "the judgment notifications this answers, comma separated")
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
		ans = fs.String("answers", "", "the judgment notifications this answers, comma separated")
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
		ans = fs.String("answers", "", "the judgment notifications this answers, comma separated")
	}, func(ids []string, s *sel) string {
		if *reason == "" || len(ids) == 0 && s.stream == "" && s.col == "" {
			return "wants ids (or --stream/--col, --group) and --reason <text>"
		}
		return ""
	}, func(ids []string, s *sel, c *common) store.Step {
		return store.DropStep(sprint.DropReq{Sel: s.sel(ids), Reason: *reason, Answers: answers(*ans), Who: c.actor})
	})
}

func (a *app) cmdRank(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("rank")
	score := fs.String("score", "", "the new score of the first id; the rest follow it")
	first := fs.Bool("first", false, "ahead of every primary")
	ans := fs.String("answers", "", "the judgment notifications this answers, comma separated")
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
	stream := fs.String("stream", "", "the stream")
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
	ans := fs.String("answers", "", "the judgment notifications this answers, comma separated")
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
		width = fs.String("width", "", fmt.Sprintf("the member's width: the most work cards it holds at once, ready and working, 1 to %d (default: as it is, %d for a new member)", sprint.MaxWidth, sprint.DefaultWidth))
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
	fmt.Fprintf(stdout, "READER-ADD OK readers=%s\n", strings.Join(names, ","))
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
		return refuse(stderr, "repair", argErr("takes no words ", err))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "repair", err.Error())
	}
	rr, err := st.Repair(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "%s repair: %s\n", prog, oneline.Escape(err.Error()))
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
		return refuse(stderr, "teardown", argErr("takes no words ", err))
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
	fmt.Fprintf(stdout, "TEARDOWN OK sprint=%s keys=%d\n", oneline.Escape(want), n)
	return 0
}

func (a *app) cmdClear(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("clear")
	confirm := fs.String("confirm", "", "the sprint's name, to confirm: the name of its view, sprint")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "clear", argErr("takes no words ", err))
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
		fmt.Fprintf(stderr, "%s clear: %s\n", prog, oneline.Escape(err.Error()))
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
	fmt.Fprintf(stdout, "CLEAR OK epoch=%d->%d at=%s held: %s%s\n", res.From, res.To, res.At.UTC().Format(time.RFC3339), strings.Join(held, " "), extra)
	if line := sprintLine(ctx, st); line != "" {
		fmt.Fprintln(stdout, line)
	}
	if res.Machine == store.Running {
		fmt.Fprintf(stdout, "the machine is STOPPED; when the new sprint is ready: nova-sprint start\n")
	}
	return 0
}
