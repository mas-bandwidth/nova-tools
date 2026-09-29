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
)

type flagSet = *flag.FlagSet

type verb struct {
	name, syntax, example string
	run                   func(*app, []string, io.Writer, io.Writer) int
}

var verbs []verb

func init() {
	verbs = []verb{
		{"init", "[--readers <a,b,...>] [--members <m1,m2,...>]", "init --readers reader-a,reader-b,reader-c --members m1,m2", (*app).cmdInit},
		{"add", "--stream <s> (<id>... | --count <n>) [--needs <a,b>] [--brief <text>] [--score <n>]", "add --stream s1 --count 100", (*app).cmdAdd},
		{"resolve", "[<id>...] [--stream <s>] [--limit <n>]", "resolve", (*app).cmdResolve},
		{"start", "(<id>... | --stream <s> | --limit <n> | --group <n>)", "start --limit 10", (*app).cmdStart},
		{"take", "--as <member> [<card>[@<gen>]...] [--limit <n>]", "take --as m1 --limit 5", (*app).cmdTake},
		{"finish", "--as <member> <card>@<gen>... [--failed] [--head <h>] [--report <text>]", "finish --as m1 s1-1.w1@1", (*app).cmdFinish},
		{"ask", "[<id>...] [--stream <s>] [--limit <n>] [--another]", "ask", (*app).cmdAsk},
		{"queue", "--as <reader|member> | --stream <s>", "queue --as reader-a", (*app).cmdQueue},
		{"read", "--as <reader> (--begin | --ok | --broken) [<card>...] [--limit <n>] [--finding <text>]", "read --as reader-a --ok --limit 5", (*app).cmdRead},
		{"accept", "(<id>... | --stream <s> | --read-ok | --group <n>) [--answers <note>]", "accept --read-ok", (*app).cmdAccept},
		{"rework", "(<id>... | --group <n>) --fix <text> [--answers <note>]", "rework s1-4 --fix 'handle the empty case'", (*app).cmdRework},
		{"return", "<id>... [--reason <text>] [--answers <note>]", "return s1-7 --reason 'suspect of the red batch'", (*app).cmdReturn},
		{"drop", "(<id>... | --stream <s> --col <state> | --group <n>) --reason <text> [--answers <note>]", "drop s1-9 --reason obsolete", (*app).cmdDrop},
		{"rank", "<id>... (--score <n> | --first) [--answers <note>]", "rank s2-3 --first", (*app).cmdRank},
		{"merge", "--stream <s> [--batch <n>] [--conflict <id> | --cross <id>=<other> | --red | --rejected] [--note <text>]", "merge --stream s1 --batch 100", (*app).cmdMerge},
		{"resume", "--stream <s> [--did <text>] [--answers <note>]", "resume --stream s1 --did 'rebased s1-4'", (*app).cmdResume},
		{"fleet up", "<member>", "fleet up m1", func(a *app, args []string, o, e io.Writer) int { return a.cmdFleet("up", args, o, e) }},
		{"fleet down", "<member>", "fleet down m1", func(a *app, args []string, o, e io.Writer) int { return a.cmdFleet("down", args, o, e) }},
		{"fleet level", "", "fleet level", func(a *app, args []string, o, e io.Writer) int { return a.cmdFleet("level", args, o, e) }},
		{"reader add", "<reader>...", "reader add reader-d", (*app).cmdReaderAdd},
		{"ci", "<id>... (--red | --green) [--head <h>] [--run <id>] [--source <s>] [--note <text>]", "ci s1-3 --red --run 812 --source ci", (*app).cmdCI},
		{"wait", "<note> (--for <duration> | --until <RFC3339>)", "wait start-x-1.2 --for 30m", (*app).cmdWait},
		{"inbox", "[--open <n>] [--read] [--deadline <duration>] [--stale <duration>]", "inbox", (*app).cmdInbox},
		{"card", "<id>", "card s1-4", (*app).cmdCard},
		{"check", "", "check", (*app).cmdCheck},
		{"repair", "", "repair", (*app).cmdRepair},
		{"where", "[--watch] [--every <duration>]", "where", (*app).cmdWhere},
		{"play", "[--seed <n>] [--every <duration>] [--start] [--fail <p>] [--broken <p>] [--batch <n>] [--stuck <p>] [--cross <p>] [--red <p>] [--flap <p>] [--ticks <n>]", "play --seed 7 --every 1s --start", (*app).cmdPlay},
		{"teardown", "--confirm <prefix>", "teardown --confirm dev-", (*app).cmdTeardown},
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
NOVA_REDIS_ADDR), --prefix <p> (else NOVA_SPRINT_PREFIX: every table, view and
key of this sprint carries it), --actor, --op <id> (the same id again returns
the recorded result), --json and --max <n> (listed items; 0 is all). A set is
ids, a stream, a column, --limit n, or an inbox group (--group n). Each verb
prints what moved (MOVED), what did not and why (REFUSED, on stderr), its
summary line, and the sprint's line: landed/all percent -> ETA.

A card id with @<gen> names the generation of a work card the worker holds;
a finish of a generation that is not the live one is refused as stale.

exit codes: 0 done, 1 refused, 2 usage or a store that did not answer

`)
	return b.String()
}

func versionLine() string { return buildinfo.Line(prog, version) }

func helpCommand(path []string, stdout, stderr io.Writer) int {
	if len(path) == 0 {
		fmt.Fprint(stdout, banner())
		return 0
	}
	name := strings.Join(path, " ")
	if name == "fleet" || name == "reader" {
		fmt.Fprintln(stdout, "usage:")
		for _, v := range verbs {
			if strings.HasPrefix(v.name, name+" ") {
				fmt.Fprintln(stdout, "  nova-sprint "+strings.TrimSpace(v.name+" "+v.syntax))
			}
		}
		return 0
	}
	for _, v := range verbs {
		if v.name == name {
			return v.run(newApp(func(string) string { return "" }), []string{"--help"}, stdout, stderr)
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
	group       int
	answers     string
}

func (s *sel) register(fs flagSet, withCol bool) {
	fs.StringVar(&s.stream, "stream", "", "the cards of one stream")
	if withCol {
		fs.StringVar(&s.col, "col", "", "the cards in one column (a state)")
	}
	fs.IntVar(&s.limit, "limit", 0, "at most n cards, in work order")
	fs.IntVar(&s.group, "group", 0, "the primaries of inbox group n")
}

func (s *sel) sel(ids []string) sprint.Sel {
	return sprint.Sel{IDs: ids, Stream: s.stream, Col: s.col, Limit: s.limit}
}

func answers(s string) []string { return sprint.Split(s) }

// verbSetup is the flag set of a store verb with the common flags.
func (a *app) verbSetup(name string) (flagSet, *common) {
	fs := verbflag.New(name)
	c := &common{}
	c.register(fs, a.getenv)
	return fs, c
}

// groupIDs is the primaries of inbox group n, all of them.
func groupIDs(ctx context.Context, st *store.Store, n int) ([]string, error) {
	v, err := st.Inbox(ctx, defaultDeadline, defaultStale, 10000)
	if err != nil {
		return nil, err
	}
	if n < 1 || n > len(v.Groups) {
		return nil, fmt.Errorf("no inbox group %d (there are %d); run: nova-sprint inbox", n, len(v.Groups))
	}
	return groupMembers(v, v.Groups[n-1]), nil
}

// groupMembers is every subject of a group: the open subjects of its
// judgments, or the primaries of its notifications.
func groupMembers(v store.InboxView, g sprint.Group) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		if !seen[s] && !strings.HasPrefix(s, "stream:") {
			seen[s] = true
			out = append(out, s)
		}
	}
	notes := map[string]bool{}
	for _, id := range g.Notes {
		notes[id] = true
	}
	if g.Kind == sprint.Judgment {
		for _, o := range v.Open {
			if notes[o.Note.ID] {
				add(o.Subject())
				if o.Note.StreamLevel {
					for _, p := range o.Note.Primaries {
						add(p)
					}
				}
			}
		}
		return out
	}
	for _, n := range v.Recent {
		if notes[n.ID] {
			for _, p := range n.Primaries {
				add(p)
			}
		}
	}
	return out
}

const (
	defaultDeadline = 10 * time.Minute
	defaultStale    = 30 * time.Minute
)

// runStep runs a step and reports it: exit 0 when everything named moved, 1
// when a card was refused or the step was cut, 2 when the store did not
// confirm.
func (a *app) runStep(verbName string, c common, st *store.Store, step store.Step, stdout, stderr io.Writer) int {
	ctx := context.Background()
	step.CallerOp = c.op
	res, err := st.Run(ctx, step)
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
}

func (a *app) report(ctx context.Context, verbName string, c common, st *store.Store, res store.Result, err error, stdout, stderr io.Writer) int {
	code := 0
	if len(res.Refused) > 0 {
		code = 1
	}
	var pe *store.PendingError
	var cut *store.CutError
	switch {
	case err == nil:
	case errors.Is(err, store.ErrUnknown):
		code = 2
	case errors.As(err, &pe), errors.As(err, &cut):
		code = 1
	default:
		code = 2
	}
	line := sprintLine(ctx, st)
	if c.json {
		o := output{Result: res, Sprint: line, Unknown: errors.Is(err, store.ErrUnknown)}
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

// sprintLine is the summary line: landed / all primaries, percent, ETA.
func sprintLine(ctx context.Context, st *store.Store) string {
	if st == nil {
		return ""
	}
	shapes, err := st.B.Shapes(ctx, []string{st.Names.Table(sprint.Work)})
	if err != nil || len(shapes) == 0 {
		return ""
	}
	return summary(shapes[0])
}

func (a *app) cmdInit(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("init")
	readers := fs.String("readers", "", "the readers' rows, comma separated")
	members := fs.String("members", "", "fleet members to bring up, comma separated")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "init", err.Error())
	}
	if len(pos) > 0 {
		return refuse(stderr, "init", "takes no words, found "+pos[0])
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
	for _, m := range sprint.Split(*members) {
		if code := a.runStep("fleet up", *c, st, store.FleetStep(sprint.FleetReq{Op: "up", Member: m, Who: c.actor}), stdout, stderr); code != 0 {
			return code
		}
	}
	return 0
}

func (a *app) cmdAdd(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("add")
	stream := fs.String("stream", "", "the stream the primaries belong to, for life")
	count := fs.Int("count", 0, "admit n primaries with generated ids <stream>-<n>")
	needs := fs.String("needs", "", "primaries that must land first, comma separated")
	brief := fs.String("brief", "", "the brief")
	score := fs.String("score", "", "the first primary's score; the rest follow it (default: after every primary)")
	ids, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "add", err.Error())
	}
	if *stream == "" || (len(ids) == 0) == (*count == 0) {
		return refuse(stderr, "add", "wants --stream and either ids or --count <n>")
	}
	r := sprint.AddReq{Stream: *stream, IDs: ids, Count: *count, Needs: sprint.Split(*needs), Brief: *brief, Who: c.actor}
	if *score != "" {
		f, err := strconv.ParseFloat(*score, 64)
		if err != nil {
			return refuse(stderr, "add", "--score wants a number")
		}
		r.Score = &f
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "add", err.Error())
	}
	return a.runStep("add", *c, st, store.AddStep(r), stdout, stderr)
}

// withGroup resolves --group into ids.
func (a *app) withGroup(verbName string, st *store.Store, s *sel, ids []string, stderr io.Writer) ([]string, int) {
	if s.group == 0 {
		return ids, 0
	}
	if len(ids) > 0 {
		return nil, refuse(stderr, verbName, "takes ids or --group, not both")
	}
	g, err := groupIDs(context.Background(), st, s.group)
	if err != nil {
		return nil, refuse(stderr, verbName, err.Error())
	}
	return g, 0
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
	ids, code := a.withGroup(verbName, st, &s, ids, stderr)
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

func (a *app) cmdStart(args []string, stdout, stderr io.Writer) int {
	return a.setVerb("start", args, stdout, stderr, false, nil, func(ids []string, s *sel) string {
		if len(ids) == 0 && s.stream == "" && s.limit == 0 {
			return "wants ids, --stream <s>, --limit <n> or --group <n>"
		}
		return ""
	}, func(ids []string, s *sel, c *common) store.Step {
		return store.StartStep(sprint.StartReq{Sel: s.sel(ids), Who: c.actor})
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
	as := fs.String("as", "", "the fleet member taking its cards")
	limit := fs.Int("limit", 0, "take the first n of its ready queue (default 1)")
	words, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "take", err.Error())
	}
	ids, gens, err := cardGens(words)
	if err != nil || *as == "" {
		return refuse(stderr, "take", fmt.Sprint("wants --as <member>; ", err))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "take", err.Error())
	}
	return a.runStep("take", *c, st, store.TakeStep(sprint.TakeReq{Sel: sprint.Sel{IDs: ids, Limit: *limit}, As: *as, Gens: gens, Who: *as}), stdout, stderr)
}

func (a *app) cmdFinish(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("finish")
	as := fs.String("as", "", "the fleet member finishing its cards")
	failed := fs.Bool("failed", false, "the work failed (default: ok)")
	head := fs.String("head", "", "the head the work finished at (default: the card's id)")
	report := fs.String("report", "", "the worker's report")
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
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "finish", err.Error())
	}
	return a.runStep("finish", *c, st, store.FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: ids}, As: *as, Gens: gens, Failed: *failed,
		Head: *head, Report: *report, Who: *as}), stdout, stderr)
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
	as := fs.String("as", "", "the reader")
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
			return "wants ids, --stream <s>, --read-ok or --group <n>"
		}
		return ""
	}, func(ids []string, s *sel, c *common) store.Step {
		r := sprint.AcceptReq{Sel: s.sel(ids), Answers: answers(*ans), Who: c.actor}
		if s.group > 0 {
			r.Sel = sprint.Sel{Only: ids} // a selection: the eligible move
		}
		return store.AcceptStep(r)
	})
}

func (a *app) cmdRework(args []string, stdout, stderr io.Writer) int {
	var fix, ans *string
	return a.setVerb("rework", args, stdout, stderr, false, func(fs flagSet) {
		fix = fs.String("fix", "", "the fix: what the next attempt must change")
		ans = fs.String("answers", "", "the judgment notifications this answers, comma separated")
	}, func(ids []string, s *sel) string {
		if *fix == "" || len(ids) == 0 && s.stream == "" {
			return "wants ids (or --group, --stream) and --fix <text>"
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
			return "wants ids, --stream <s> or --group <n>"
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
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "merge", err.Error())
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
		Red: *red, Rejected: *rejected, Note: *note, Who: c.actor}), stdout, stderr)
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
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if (op == "level") != (len(pos) == 0) || len(pos) > 1 {
		return refuse(stderr, name, "wants one member (level takes none)")
	}
	member := ""
	if len(pos) == 1 {
		member = pos[0]
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	return a.runStep(name, *c, st, store.FleetStep(sprint.FleetReq{Op: op, Member: member, Who: c.actor}), stdout, stderr)
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
	if err := st.B.SetReview(context.Background(), pos[0], at); err != nil {
		fmt.Fprintf(stderr, "%s wait: %s\n", prog, oneline.Escape(err.Error()))
		return 1
	}
	fmt.Fprintf(stdout, "WAIT OK note=%s review=%s\n", oneline.Escape(pos[0]), at.UTC().Format(time.RFC3339))
	return 0
}

func (a *app) cmdRepair(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("repair")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "repair", fmt.Sprint("takes no words ", err))
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

func (a *app) cmdTeardown(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("teardown")
	confirm := fs.String("confirm", "", "the prefix again, to confirm; 'none' for an empty prefix")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "teardown", fmt.Sprint("takes no words ", err))
	}
	want := c.prefix
	if want == "" {
		want = "none"
	}
	if *confirm != want {
		return refuse(stderr, "teardown", "drops the four tables, the view and every key of the sprint under prefix "+strconv.Quote(c.prefix)+"; wants --confirm "+want)
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
	fmt.Fprintf(stdout, "TEARDOWN OK prefix=%s keys=%d\n", oneline.Escape(c.prefix), n)
	return 0
}
