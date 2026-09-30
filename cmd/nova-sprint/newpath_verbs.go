package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	spverbs "github.com/mas-bandwidth/nova-tools/internal/sprint/verbs"
)

// The new path's dispatch table: every verb of the present command, and the
// ones section 3 adds, each with its flags as the present command and section
// 3 give them, the item whose function it calls, and that call. A verb whose
// item has not landed has the call stubbed(item): its words and flags are
// parsed and checked as they will be, and it is refused naming the item.

// verbCall is a verb on the new path: its parsed words and flags, run
// through internal/sprint/verbs (or IT17's tick loop) on the Env.
type verbCall func(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error)

// flagKind is how a flag's value is read.
type flagKind int

const (
	kString flagKind = iota
	kBool
	kInt
	kInt64
	kDuration
	kList // given again or comma separated (listFlag)
)

// flagDef is one flag of a verb: its name, kind, default (as text; for a
// string, the variable env names when it is set), and usage.
type flagDef struct {
	name  string
	kind  flagKind
	def   string
	env   string
	usage string
}

// wordsRule is how many words a verb takes after its name and flags.
type wordsRule int

const (
	wordsNone wordsRule = iota
	wordsOne
	wordsSome // one or more
	wordsAny
)

// newVerb is one entry of the table.
type newVerb struct {
	name, syntax string
	// class is who may run it (coordinator.go's classes); the present verbs
	// take theirs from verbClasses.
	class string
	// item is the design item whose function the call runs (IT18: the
	// machine's verbs; IT17: run and tick; IT19 to IT22: the rest), or what
	// the verb waits for when no item builds it yet.
	item  string
	flags []flagDef
	words wordsRule
	// check refuses the words and flags before anything is read (exit 2).
	check func(p *parsed) error
	// notYet are the present flags the verb's function does not carry yet,
	// each with what it waits for: parsed as the present grammar has them, and
	// refused by name when given, before anything is read.
	notYet map[string]string
	// writes says the verb writes: after it applied, the sprint's line is
	// printed (the present command's sprint line, from IT22's Where).
	writes bool
	call   verbCall
	// local is a verb of the command itself, given its raw words (play: the
	// driver, whose every verb is run through the entry point).
	local func(a *app, args []string, stdout, stderr io.Writer) int
}

// parsed is a verb's words and flags, read.
type parsed struct {
	app   *app
	verb  string
	c     common
	words []string
	vals  map[string]any
	given map[string]bool
	// view is what a read verb's call read, printed with --json; out is where
	// a verb that draws (where --watch) draws.
	view any
	out  io.Writer
}

func (p *parsed) str(n string) string { return *(p.vals[n].(*string)) }
func (p *parsed) on(n string) bool    { return *(p.vals[n].(*bool)) }
func (p *parsed) num(n string) int    { return *(p.vals[n].(*int)) }
func (p *parsed) num64(n string) int64 {
	return *(p.vals[n].(*int64))
}
func (p *parsed) dur(n string) time.Duration { return *(p.vals[n].(*time.Duration)) }
func (p *parsed) list(n string) []string     { return []string(*(p.vals[n].(*listFlag))) }
func (p *parsed) has(n string) bool          { return p.given[n] }

// classOf is the verb's class: verbClasses' for a present verb, else its own.
func classOf(v newVerb) string {
	if c, ok := verbClasses[v.name]; ok {
		return c
	}
	return v.class
}

// parseNew reads a verb's flags anywhere among its words (parse, as the
// present verbs), then its words by its rule, then its check.
func (a *app) parseNew(v newVerb, args []string) (*parsed, error) {
	fs, c := a.verbSetup(v.name)
	p := &parsed{app: a, verb: v.name, vals: map[string]any{}, given: map[string]bool{}}
	for _, f := range v.flags {
		def := f.def
		if f.env != "" && a.getenv(f.env) != "" {
			def = a.getenv(f.env)
		}
		switch f.kind {
		case kString:
			p.vals[f.name] = fs.String(f.name, def, f.usage)
		case kBool:
			p.vals[f.name] = fs.Bool(f.name, def == "true", f.usage)
		case kInt:
			n, _ := strconv.Atoi(def)
			p.vals[f.name] = fs.Int(f.name, n, f.usage)
		case kInt64:
			n, _ := strconv.ParseInt(def, 10, 64)
			p.vals[f.name] = fs.Int64(f.name, n, f.usage)
		case kDuration:
			d, _ := time.ParseDuration(def)
			p.vals[f.name] = fs.Duration(f.name, d, f.usage)
		case kList:
			l := &listFlag{}
			fs.Var(l, f.name, f.usage)
			p.vals[f.name] = l
		}
	}
	words, err := parse(fs, args)
	if err != nil {
		return nil, err
	}
	fs.Visit(func(f *flag.Flag) { p.given[f.Name] = true })
	for _, f := range v.flags {
		if why, ok := v.notYet[f.name]; ok && p.given[f.name] {
			return nil, fmt.Errorf("--%s is not on the new path's %s yet: %s", f.name, v.name, why)
		}
	}
	p.words = words
	switch {
	case v.words == wordsNone && len(words) > 0:
		return nil, fmt.Errorf("takes no words, found %s", words[0])
	case v.words == wordsOne && len(words) != 1:
		return nil, fmt.Errorf("takes one word, found %d", len(words))
	case v.words == wordsSome && len(words) == 0:
		return nil, fmt.Errorf("wants %s", v.syntax)
	}
	switch classOf(v) {
	case classMachine:
		if c.actor == "" {
			c.actor = sprint.MachineActor
		}
	case classWorker:
		if as, ok := p.vals["as"].(*string); ok && *as != "" {
			c.orActor(*as)
		} else if v.name == "fleet beat" && len(words) > 0 {
			c.orActor(words[0])
		}
	}
	p.c = *c
	if v.check != nil {
		if err := v.check(p); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// actorRule is why a verb of the class may not run with no actor, "" when it
// may: the present rule (needsActor), by class.
func actorRule(class, actor string) string {
	switch class {
	case "", classRead, classMachine:
		return ""
	}
	if actor != "" {
		return ""
	}
	return "--actor <name> is required (or NOVA_SPRINT_ACTOR): who acts is recorded with every change, and there is no default; nothing was changed"
}

// The flags several verbs share.
var (
	fAnswers = flagDef{name: "answers", usage: "the judgment notifications this answers, comma separated"}
	fStream  = flagDef{name: "stream", usage: "the cards of one stream"}
	fLimit   = flagDef{name: "limit", kind: kInt, usage: "at most n cards, in work order"}
	fGroup   = flagDef{name: "group", usage: "the members of the inbox group of this id (the id inbox prints; a group number is refused)"}
	fExpect  = flagDef{name: "expect", kind: kInt, usage: "with --group: the group's size as inbox printed it; a group of another size now is refused and nothing changes"}
	fAtEpoch = flagDef{name: "at-epoch", kind: kInt64, def: "-1", usage: "an earlier epoch (before a clear), as it was"}
	fStale   = flagDef{name: "stale", kind: kDuration, def: defaultStale.String(), usage: "a stream with no progress for longer is shown stalled"}
)

// setFlags are the flags of a verb over a set of primaries (sel).
func setFlags(withCol bool, extra ...flagDef) []flagDef {
	out := []flagDef{fStream, fLimit, fGroup, fExpect}
	if withCol {
		out = append(out, flagDef{name: "col", usage: "the cards in one column (a state)"})
	}
	return append(out, extra...)
}

// newGroups are the words that begin two-word verbs.
var newGroups = map[string]bool{"fleet": true, "reader": true, "goal": true}

// newVerbs is the table. Every present verb is here, with its present
// grammar; the verbs section 3 adds (remove) are here too.
var newVerbs []newVerb

func init() {
	newVerbs = []newVerb{
		// IT18: the machine's verbs.
		{name: "init", syntax: "[--coordinator <name>] [--pg <dsn>]", item: "IT18", words: wordsAny, writes: true,
			flags: []flagDef{
				{name: "coordinator", usage: "the sprint's coordinator, the one actor who releases sentinels (default: the actor); on a sprint that has one, set it anew (the actor must be it)"},
				{name: "pg", env: envPG, usage: "nova-config's store, where the coordinator is read (else " + envPG + ")"},
				{name: "readers", usage: "not on the new path: init makes the clock and the coordinator; run reader add after it"},
				{name: "members", usage: "not on the new path: init makes the clock and the coordinator; run fleet up after it"},
			},
			check: checkInit, call: callInit},
		{name: "start", item: "IT18", writes: true, call: func(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
			return spverbs.Start(ctx, e, spverbs.ClockReq{Op: p.c.op})
		}},
		{name: "stop", item: "IT18", writes: true, call: func(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
			return spverbs.Stop(ctx, e, spverbs.ClockReq{Op: p.c.op})
		}},
		{name: "clear", syntax: "--confirm sprint", item: "IT18", writes: true,
			flags: []flagDef{{name: "confirm", usage: "the sprint's name, sprint, to confirm"}},
			check: func(p *parsed) error {
				if p.str("confirm") != confirmName() {
					return fmt.Errorf("stops the sprint and clears all work in it (a new epoch; the old one stays readable); wants --confirm %s", confirmName())
				}
				return nil
			},
			call: func(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
				return spverbs.Clear(ctx, e, spverbs.ClearReq{Op: p.c.op, Confirm: pathNames.Prefix})
			}},
		{name: "goal set", syntax: "<name> --file <path>", item: "IT18", words: wordsOne,
			flags: []flagDef{
				{name: "file", usage: "the file that holds the goal text: what this person is to keep doing"},
				{name: "to", usage: "not on the new path: section 3's goal is the goal record and remind:<person>, with no route"},
			},
			check: func(p *parsed) error {
				if p.has("to") {
					return fmt.Errorf("--to is not on the new path: the goal is its record and remind:%s (section 3), with no route", p.words[0])
				}
				if p.str("file") == "" {
					return fmt.Errorf("wants --file <path>, the goal's text")
				}
				return nil
			},
			call: func(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
				text, err := readGoalText(p.str("file"))
				if err != nil {
					return spverbs.Result{Verb: p.verb}, usage("%v", err)
				}
				return spverbs.GoalSet(ctx, e, spverbs.GoalReq{Op: p.c.op, Person: p.words[0], Goal: text})
			}},
		{name: "goal show", syntax: "<name>", item: "IT18", words: wordsAny,
			check: func(p *parsed) error {
				if len(p.words) != 1 {
					return fmt.Errorf("takes one name: the new path reads one person's goal")
				}
				return nil
			},
			call: func(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
				return spverbs.GoalShow(ctx, e, spverbs.GoalReq{Person: p.words[0]})
			}},
		{name: "goal drop", syntax: "<name>", item: "IT18", words: wordsOne,
			call: func(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
				return spverbs.GoalDrop(ctx, e, spverbs.GoalReq{Op: p.c.op, Person: p.words[0]})
			}},

		// IT17: the machine's loop.
		{name: "run", item: "IT17", call: callRun, writes: true},
		{name: "tick", item: "IT17", call: callTick, writes: true},

		// IT19: add, release, rank.
		{name: "add", syntax: "--stream <s> (<id>... | --count <n> | --sentinel <id>) [--needs <a,b>] [--before <id> | --after <id> | --score <n>] [--brief <text>] [--sentinel-every <k> [--sentinel-last]]",
			item: "IT19", words: wordsAny,
			flags: []flagDef{
				{name: "stream", usage: "the stream the primaries belong to, for life; with --count, several streams comma separated"},
				{name: "count", kind: kInt, usage: "admit n primaries with generated ids <stream>-<n>"},
				{name: "needs", usage: "primaries that must land first, comma separated"},
				{name: "brief", usage: "the brief"},
				{name: "score", usage: "the first primary's score; the rest follow it"},
				{name: "sentinel", usage: "admit a sentinel with this id"},
				{name: "before", usage: "place the cards in line in front of this primary of the stream"},
				{name: "after", usage: "place the cards in line after this primary of the stream"},
				{name: "sentinel-every", kind: kInt, usage: "with --count: a sentinel <stream>-gate-<n> after every k cards"},
				{name: "sentinel-last", kind: kBool, usage: "with --sentinel-every: a sentinel after the last card too"},
			},
			check: checkAdd, call: callAdd, writes: true},
		{name: "release", syntax: "<sentinel>... --reason <text> [--answers <note>]", item: "IT19", words: wordsSome,
			flags: []flagDef{{name: "reason", usage: "what you looked at and found"}, fAnswers},
			check: func(p *parsed) error {
				if p.str("reason") == "" {
					return fmt.Errorf("wants the sentinels it releases and --reason <text>")
				}
				return nil
			},
			call: callRelease, writes: true},
		{name: "rank", syntax: "<id>... (--score <n> | --first | --before <id> | --after <id>) [--answers <note>]", item: "IT19", words: wordsSome,
			flags: []flagDef{{name: "score", usage: "the new score of the first id; the rest follow it"},
				{name: "first", kind: kBool, usage: "ahead of every primary"},
				{name: "before", usage: "in front of this primary"}, {name: "after", usage: "after this primary"}, fAnswers},
			notYet: map[string]string{"first": "section 3's rank takes --score, --before or --after (rank <id> --before <the stream's first>)"},
			check: func(p *parsed) error {
				if count(p.str("score") != "", p.on("first"), p.str("before") != "", p.str("after") != "") != 1 {
					return fmt.Errorf("wants ids and one of --score <n>, --first, --before <id>, --after <id>")
				}
				if s := p.str("score"); s != "" {
					if _, err := strconv.ParseFloat(s, 64); err != nil {
						return fmt.Errorf("--score wants a number")
					}
				}
				return nil
			},
			call: callRank, writes: true},

		// IT20: the workers' verbs and the fleet's.
		{name: "take", syntax: "--as <member> [<card>@<gen>...] [--limit <n>]", item: "IT20", words: wordsAny,
			flags: []flagDef{{name: "as", usage: "the fleet member taking its cards"}, {name: "limit", kind: kInt, usage: "take the first n of its ready queue (default 1)"}},
			check: checkCardGens(false), call: callTake, writes: true},
		{name: "finish", syntax: "--as <member> <card>@<gen>... [--failed] [--head <h>] [--report <text>] [--branch <b>] [--base <b>]", item: "IT20", words: wordsSome,
			flags: []flagDef{{name: "as", usage: "the fleet member finishing its cards"}, {name: "failed", kind: kBool, usage: "the work failed (default: ok)"},
				{name: "head", usage: "the head the work finished at"}, {name: "report", usage: "the worker's report"},
				{name: "branch", usage: "the branch the work is on"}, {name: "base", usage: "the branch the work started from"}},
			check: checkCardGens(true), call: callFinish, writes: true},
		{name: "read", syntax: "--as <reader> (--begin | --ok | --broken) [<card>...] [--limit <n>] [--finding <text>]", item: "IT20", words: wordsAny,
			flags: []flagDef{{name: "as", usage: "the reader"}, {name: "begin", kind: kBool, usage: "asked -> reading"},
				{name: "ok", kind: kBool, usage: "the read found it good"}, {name: "broken", kind: kBool, usage: "the read found it broken"},
				{name: "finding", usage: "what the read found"}, {name: "limit", kind: kInt, usage: "the first n of the reader's queue (default 1)"}},
			check: func(p *parsed) error {
				if p.str("as") == "" || count(p.on("begin"), p.on("ok"), p.on("broken")) != 1 {
					return fmt.Errorf("wants --as <reader> and one of --begin, --ok, --broken")
				}
				return nil
			},
			notYet: map[string]string{"limit": "the readers' queue is not a read of IT20's queue yet; name the read cards"},
			call:   callRead, writes: true},
		{name: "queue", syntax: "--as <reader|member> | --stream <s> [--col waiting]", item: "IT20",
			flags: []flagDef{{name: "as", usage: "a reader or a fleet member"}, {name: "stream", usage: "a stream: its merge queue, then its stuck cards"},
				{name: "col", usage: "with --stream: waiting lists the stream's waiting primaries"}},
			check: func(p *parsed) error {
				if (p.str("as") == "") == (p.str("stream") == "") {
					return fmt.Errorf("wants one of --as <reader|member>, --stream <s>")
				}
				if c := p.str("col"); c != "" && (c != sprint.Waiting || p.str("stream") == "") {
					return fmt.Errorf("--col takes waiting, with --stream <s>")
				}
				return nil
			},
			call: callQueue},
		{name: "fleet beat", syntax: "<member>... [--load <percent>]", item: "IT20", words: wordsSome,
			flags: []flagDef{{name: "load", usage: "the load as a percent of all the machine's cores, instead of measuring it"}},
			call:  callFleetBeat, writes: true},
		{name: "fleet up", syntax: "<member>...", item: "IT20", words: wordsSome, call: callFleetUp, writes: true},
		{name: "fleet down", syntax: "<member>...", item: "IT20", words: wordsSome, call: callFleetDown, writes: true},
		{name: "reader add", syntax: "<reader>...", item: "IT20", words: wordsSome,
			check: func(p *parsed) error {
				for _, n := range p.words {
					if !sprint.ValidID(n) {
						return fmt.Errorf("a reader name wants letters, digits, _ and -: %s", n)
					}
				}
				return nil
			},
			call: callReaderAdd, writes: true},

		// IT21: review, merge, drop.
		{name: "ask", syntax: "[<id>... | --group <id> [--expect <n>]] [--stream <s>] [--limit <n>] [--another] [--answers <note>]", item: "IT21", words: wordsAny,
			flags:  setFlags(false, flagDef{name: "another", kind: kBool, usage: "one more reader for a primary already asked"}, fAnswers),
			notYet: map[string]string{"stream": "ask names its primaries", "limit": "ask names its primaries", "group": "the new inbox has no --group selection yet", "expect": "the new inbox has no --group selection yet", "answers": "ask's request carries no answers"},
			check:  checkGroup, call: callAsk, writes: true},
		{name: "accept", syntax: "(<id>... | --stream <s> | --read-ok | --group <id> [--expect <n>]) [--answers <note>]", item: "IT21", words: wordsAny,
			flags: setFlags(false, flagDef{name: "read-ok", kind: kBool, usage: "every primary in review with ok reads from two different readers"}, fAnswers),
			check: func(p *parsed) error {
				if err := checkGroup(p); err != nil {
					return err
				}
				if len(p.words) == 0 && p.str("stream") == "" && !p.on("read-ok") && p.num("limit") == 0 && p.str("group") == "" {
					return fmt.Errorf("wants ids, --stream <s>, --read-ok or --group <id>")
				}
				return nil
			},
			notYet: map[string]string{"limit": "accept takes ids, --stream or --read-ok", "group": "the new inbox has no --group selection yet", "expect": "the new inbox has no --group selection yet", "answers": "accept's request carries no answers"},
			call:   callAccept, writes: true},
		{name: "rework", syntax: "(<id>... | --group <id> [--expect <n>]) [--fix <text>] [--answers <note>]", item: "IT21", words: wordsAny,
			flags:  setFlags(false, flagDef{name: "fix", usage: "the fix for every primary"}, fAnswers),
			check:  needsSet("wants ids (or --group, --stream); --fix <text> for all, else each primary's own finding or report"),
			notYet: map[string]string{"limit": "rework takes ids or --stream", "group": "the new inbox has no --group selection yet", "expect": "the new inbox has no --group selection yet", "answers": "rework's request carries no answers"},
			call:   callRework, writes: true},
		{name: "return", syntax: "(<id>... | --group <id> [--expect <n>]) [--reason <text>] [--answers <note>]", item: "IT21", words: wordsAny,
			flags: setFlags(false, flagDef{name: "reason", usage: "why it goes back to review"}, fAnswers),
			check: needsSet("wants ids, --stream <s> or --group <id>"),
			notYet: map[string]string{"stream": "return names its primaries", "limit": "return names its primaries", "group": "the new inbox has no --group selection yet", "expect": "the new inbox has no --group selection yet",
				"reason": "return's request carries no reason", "answers": "return's request carries no answers"},
			call: callReturn, writes: true},
		{name: "drop", syntax: "(<id>... | --stream <s>[,<s>...] [--col <state>] | --group <id> [--expect <n>]) --reason <text> [--answers <note>] | --abort --op <op>", item: "IT21", words: wordsAny,
			flags: setFlags(true, flagDef{name: "reason", usage: "why it leaves the table; kept with its record"}, fAnswers,
				flagDef{name: "abort", kind: kBool, usage: "with --op: end a drop in parts, clearing its marks and naming what was left"}),
			check: func(p *parsed) error {
				if p.on("abort") {
					if p.c.op == "" || len(p.words) > 0 || p.str("stream") != "" || p.str("group") != "" {
						return fmt.Errorf("drop --abort takes --op <op> alone")
					}
					return nil
				}
				if err := checkGroup(p); err != nil {
					return err
				}
				if p.str("reason") == "" || len(p.words) == 0 && p.str("stream") == "" && p.str("col") == "" && p.str("group") == "" {
					return fmt.Errorf("wants ids (or --stream/--col, --group) and --reason <text>")
				}
				return nil
			},
			notYet: map[string]string{"limit": "drop takes ids or --stream", "group": "the new inbox has no --group selection yet", "expect": "the new inbox has no --group selection yet", "answers": "drop's request carries no answers"},
			call:   callDrop, writes: true},
		{name: "ci", syntax: "<id>... (--red | --green) [--head <h>] [--run <id>] [--source <s>] [--note <text>]", item: "IT21", words: wordsAny,
			flags: setFlags(false, flagDef{name: "red", kind: kBool, usage: "the run failed"}, flagDef{name: "green", kind: kBool, usage: "the run passed"},
				flagDef{name: "head", usage: "the head the run tested"}, flagDef{name: "run", usage: "the run's id"},
				flagDef{name: "source", usage: "where the result comes from"}, flagDef{name: "note", usage: "what the run said"}),
			check: func(p *parsed) error {
				if err := checkGroup(p); err != nil {
					return err
				}
				if p.on("red") == p.on("green") || len(p.words) == 0 && p.str("stream") == "" && p.str("group") == "" {
					return fmt.Errorf("wants ids (or --stream, --group) and one of --red, --green")
				}
				return nil
			},
			notYet: map[string]string{"stream": "ci names its primaries", "limit": "ci names its primaries", "group": "the new inbox has no --group selection yet", "expect": "the new inbox has no --group selection yet",
				"run": "ci's request carries the head alone", "source": "ci's request carries the head alone", "note": "ci's request carries the head alone"},
			call: callCI, writes: true},
		{name: "merge", syntax: "--stream <s> [--batch <n>] [--conflict <id> | --cross <id>=<other> | --red [--suspect <id>...] | --rejected] [--note <text>]", item: "IT21", words: wordsAny,
			flags: []flagDef{{name: "stream", usage: "the stream"}, {name: "batch", kind: kInt, def: "10", usage: "the batch: the head n of the stream's queue"},
				{name: "conflict", usage: "fact: this card of the batch did not merge"}, {name: "cross", usage: "fact: <card>=<other>"},
				{name: "red", kind: kBool, usage: "fact: the stream branch went red on the batch"}, {name: "rejected", kind: kBool, usage: "fact: the merge queue rejected the batch"},
				{name: "note", usage: "what the facts' source said"}, {name: "suspect", kind: kList, usage: "with --red: a card of the batch suspected of turning it red"}},
			check: func(p *parsed) error {
				words := p.words
				if len(p.list("suspect")) > 0 {
					words = nil // ids after --suspect are more suspects
					if !p.on("red") {
						return fmt.Errorf("--suspect goes with --red")
					}
				}
				if p.str("stream") == "" || len(words) > 0 || count(p.str("conflict") != "", p.str("cross") != "", p.on("red"), p.on("rejected")) > 1 {
					return fmt.Errorf("wants --stream <s> and at most one fact of --conflict, --cross, --red, --rejected")
				}
				return nil
			},
			call: callMerge, writes: true},
		{name: "resume", syntax: "--stream <s>[,<s>...] [--did <text>] [--answers <note>]", item: "IT21",
			flags: []flagDef{{name: "stream", usage: "the stopped streams, comma separated"}, {name: "did", usage: "what the coordinator did about the cause"}, fAnswers},
			check: func(p *parsed) error {
				if p.str("stream") == "" {
					return fmt.Errorf("wants --stream <s>")
				}
				return nil
			},
			notYet: map[string]string{"answers": "resume's request carries no answers"},
			call:   callResume, writes: true},

		// IT22: judgments and reads.
		{name: "ack", syntax: "<note>... --reason <text>", item: "IT22", words: wordsSome,
			flags: []flagDef{{name: "reason", usage: "why nothing is to be done"}},
			check: func(p *parsed) error {
				if p.str("reason") == "" {
					return fmt.Errorf("wants notification ids and --reason <text>")
				}
				return nil
			},
			call: callAck, writes: true},
		{name: "wait", syntax: "<note>... (--for <duration> | --until <RFC3339>) [--reason <text>]", item: "IT22", words: wordsSome,
			flags: []flagDef{{name: "for", kind: kDuration, usage: "review it again after this long"}, {name: "until", usage: "review it again at this time (RFC3339)"},
				{name: "reason", usage: "why it waits"}},
			check: func(p *parsed) error {
				if (p.dur("for") == 0) == (p.str("until") == "") {
					return fmt.Errorf("wants notification ids and one of --for <duration>, --until <time>")
				}
				if u := p.str("until"); u != "" {
					if _, err := time.Parse(time.RFC3339, u); err != nil {
						return fmt.Errorf("--until wants an RFC3339 time")
					}
				}
				return nil
			},
			call: callWait, writes: true},
		{name: "inbox", syntax: "[--open <group>] [--read] [--wait [--timeout <duration>]] [--after <cursor>] [--deadline <duration>] [--stale <duration>] [--at-epoch <n>]", item: "IT22",
			flags: []flagDef{{name: "open", usage: "list every member and notification of the group of this id"},
				{name: "read", kind: kBool, usage: "move the coordinator's cursor, stored on the sprint, to the last line shown (the coordinator's alone)"},
				{name: "after", kind: kInt64, def: "-1", usage: "the lines after this cursor, for a caller that keeps its own (from --wait's last); by default the coordinator's stored cursor, from which --wait also starts"},
				{name: "wait", kind: kBool, usage: "block until the end of the next tick that addressed the coordinator (one wake a tick), then say where its batch is"},
				{name: "timeout", kind: kDuration, usage: "with --wait: at most this long (default a judgment's deadline)"},
				{name: "deadline", kind: kDuration, def: defaultDeadline.String(), usage: "a judgment open longer is overdue"}, fStale, fAtEpoch},
			notYet: map[string]string{"open": "the new inbox lists groups, not their members", "deadline": "the new inbox's deadlines are the design's",
				"stale": "the new inbox's spans are the design's", "at-epoch": "the reads read the active epoch"},
			check: func(p *parsed) error {
				if p.on("read") && p.num64("at-epoch") >= 0 {
					return fmt.Errorf("--read moves the cursor of the sprint's epoch, and --at-epoch reads an earlier one as it was: give one of them")
				}
				if isNumber(p.str("open")) {
					return fmt.Errorf("group numbers are not accepted: --open wants a group's id, as inbox prints it")
				}
				if p.has("after") && p.num64("after") < 0 {
					return fmt.Errorf("--after wants a cursor: the seq of a line, 0 or more")
				}
				if p.has("after") && p.on("read") {
					return fmt.Errorf("--read moves the coordinator's stored cursor and --after reads from one of the caller's own: give one of them")
				}
				if p.on("read") && p.on("wait") {
					return fmt.Errorf("--read and --wait are two calls: wait, then read")
				}
				return nil
			},
			call: callInbox},
		{name: "card", syntax: "<id> [--fields] [--at-epoch <n>]", item: "IT22", words: wordsOne,
			flags:  []flagDef{fAtEpoch, {name: "fields", kind: kBool, usage: "every field of the primary and its cards, instead of its story"}},
			notYet: map[string]string{"at-epoch": "the reads read the active epoch", "fields": "card shows the record and its follows"},
			call:   callCard},
		{name: "log", syntax: "[--card <id>] [--stream <s>] [--member <m>] [--since <10m|RFC3339>] [--at-epoch <n>]", item: "IT22",
			flags: []flagDef{{name: "card", usage: "the lines about this card"}, {name: "stream", usage: "the lines of this stream"},
				{name: "member", usage: "the lines of this fleet member or reader"}, {name: "since", usage: "the lines at or after this time"}, fAtEpoch},
			notYet: map[string]string{"member": "log reads a card, a stream or the lines after a seq", "at-epoch": "the reads read the active epoch"},
			call:   callLog},
		{name: "where", syntax: "[--watch] [--every <duration>] [--stale <duration>] [--at-epoch <n>]", item: "IT22",
			flags: []flagDef{{name: "watch", kind: kBool, usage: "redraw in place every --every until interrupted"},
				{name: "every", kind: kDuration, def: time.Second.String(), usage: "the redraw interval with --watch, above 0"}, fStale, fAtEpoch},
			check: func(p *parsed) error {
				if p.on("watch") && p.dur("every") <= 0 {
					return fmt.Errorf("--every wants a duration above 0, got %s", p.dur("every"))
				}
				return nil
			},
			notYet: map[string]string{"stale": "the new view's spans are the design's", "at-epoch": "the reads read the active epoch"},
			call:   callWhere},

		// The command's own: the driver, every verb of which runs through the
		// entry point, on the path the app is on.
		{name: "play", syntax: "[--simulation] [--seed <n>] [--every <duration>] [--broken <p>] [--fail <p>] [--stuck <p>] [--cross <p>] [--down <p>] [--up <p>] [--red <p>] [--flap <p>] [--batch <n>] [--hold] [--silent <member>@<from>+<for>]... [--ticks <n>]",
			item: "the command (R8's driver)", local: (*app).cmdPlay},

		// No item on the stack builds these yet.
		{name: "check", item: "IT26's rule 11 (errata 3 amendment 7)", call: func(ctx context.Context, e *spverbs.Env, _ *parsed) (spverbs.Result, error) {
			return spverbs.Check(ctx, e)
		}},
		{name: "remove", syntax: "--stream <s>[,<s>...] --confirm sprint | --abort --op <op>", class: classCoordinator, item: "IT27, verbs.Remove (after; AL6)",
			flags: []flagDef{{name: "stream", usage: "the streams to remove, comma separated"}, {name: "confirm", usage: "the sprint's name, sprint, to confirm"},
				{name: "abort", kind: kBool, usage: "with --op: end a remove in parts"}},
			check: func(p *parsed) error {
				if p.on("abort") {
					if p.c.op == "" || p.str("stream") != "" {
						return fmt.Errorf("remove --abort takes --op <op> alone")
					}
					return nil
				}
				if p.str("stream") == "" || p.str("confirm") != confirmName() {
					return fmt.Errorf("removes every card of the streams and their rows; wants --stream <s> and --confirm %s", confirmName())
				}
				return nil
			},
			call: stubbed("IT27, verbs.Remove (after; AL6 in the contract)")},
		{name: "teardown", syntax: "--confirm sprint", item: "IT23 (Layer 1's lifecycle teardown)",
			flags: []flagDef{{name: "confirm", usage: "the sprint's name, sprint, to confirm"}},
			check: func(p *parsed) error {
				if p.str("confirm") != confirmName() {
					return fmt.Errorf("drops the four tables, the view and every key of the sprint but its lifecycle receipts; wants --confirm %s", confirmName())
				}
				return nil
			},
			call: callTeardown},
		{name: "repair", item: "AL7 (Layer 1's repair entry) and IT26", call: stubbed("AL7 and IT26: repair is not a verb until they land (section 3)")},
		{name: "resolve", syntax: "[<id>...] [--stream <s>] [--limit <n>]", item: "none: section 3 has no resolve (the tick's rules do it)", words: wordsAny,
			flags: []flagDef{fStream, fLimit}, call: stubbed("no item: section 3 has no resolve verb; the tick's rules resolve waiting primaries")},
		{name: "fleet level", item: "none: section 3 has no fleet level", call: stubbed("no item: section 3 has no fleet level verb")},
	}
}

// newVerbNames is every verb's first word, in the table's order, then help
// and version.
func newVerbNames() []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range newVerbs {
		n, _, _ := strings.Cut(v.name, " ")
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return append(out, "help", "version")
}

// newBanner is the new path's help: every verb with its grammar and the item
// it runs, then the exit codes.
func newBanner() string {
	var b strings.Builder
	b.WriteString("nova-sprint: the sprint table, on the event-driven path (internal/sprint/verbs)\n\nusage:\n")
	for _, v := range newVerbs {
		b.WriteString("  nova-sprint " + strings.TrimSpace(v.name+" "+v.syntax) + "\n")
	}
	b.WriteString(`
Every store verb takes --redis <addr> (else NOVA_SPRINT_REDIS, then
NOVA_REDIS_ADDR), --prefix <p> (else NOVA_SPRINT_PREFIX), --actor <name> (else
NOVA_SPRINT_ACTOR), --op <id> (the same op and arguments again return the
recorded result; other arguments are refused), --epoch <n> (the epoch the
caller holds), --json and --max <n>.

exit codes: 0 done, 2 refused, 3 a bug refusal (the store refused the step
with a code that is a bug, 1.3.5 of the design)

`)
	return b.String()
}

// newHelp is help on the new path: the banner, or one verb's flags.
func newHelp(path []string, stdout, stderr io.Writer) int {
	if len(path) == 0 {
		fmt.Fprint(stdout, newBanner())
		return 0
	}
	name := strings.Join(path, " ")
	if newGroups[name] {
		fmt.Fprintln(stdout, "usage:")
		for _, v := range newVerbs {
			if strings.HasPrefix(v.name, name+" ") {
				fmt.Fprintln(stdout, "  nova-sprint "+strings.TrimSpace(v.name+" "+v.syntax))
			}
		}
		return 0
	}
	a := newApp(func(string) string { return "" })
	a.newPath = true
	for _, v := range newVerbs {
		if v.name == name {
			return a.runNew(append(strings.Fields(v.name), "--help"), stdout, stderr)
		}
	}
	return refuse(stderr, "help", "unknown verb "+oneline.Escape(name)+"; run: nova-sprint help")
}

// count is how many of the conditions hold.
func count(conds ...bool) int {
	n := 0
	for _, c := range conds {
		if c {
			n++
		}
	}
	return n
}

// checkInit is init's grammar on the new path: --readers and --members are
// refused naming the verbs that do them now; init takes no words.
func checkInit(p *parsed) error {
	for _, f := range []struct{ flag, verb string }{{"readers", "reader add"}, {"members", "fleet up"}} {
		if p.has(f.flag) {
			return fmt.Errorf("--%s is not on the new path's init (section 3: init makes the clock and the coordinator); run nova-sprint %s after init", f.flag, f.verb)
		}
	}
	if len(p.words) > 0 {
		return fmt.Errorf("takes no words, found %s", p.words[0])
	}
	return nil
}

// callInit is init: the coordinator is --coordinator's name, else the actor;
// nova-config is read only when --pg (or NOVA_PG_DSN) names its store, and
// then the name given must be the one it names (the grammar decisions, 9 to
// 12). On a sprint that has a clock, init --coordinator <name> sets the
// coordinator anew (IT18's InitCoordinator: the actor must be it).
func callInit(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
	req := spverbs.InitReq{Op: p.c.op, Coordinator: p.str("coordinator")}
	if req.Coordinator == "" {
		req.Coordinator = e.Actor
	}
	if dsn := p.str("pg"); dsn != "" {
		rows, closer, err := p.app.configRows(ctx, dsn)
		if err != nil {
			return spverbs.Result{Verb: p.verb}, err
		}
		if closer != nil {
			defer func() { _ = closer() }()
		}
		req.Config = rows
		if !p.has("coordinator") {
			req.Coordinator = ""
		}
	}
	// Layer 1's lifecycle defines the namespace first: the four tables, the
	// view sprint and this build (the lifecycle amendment, section 2); a
	// namespace already defined goes on to the clock step, which says whether
	// the sprint is made.
	build, err := sprintBuild()
	if err != nil {
		return spverbs.Result{Verb: p.verb}, err
	}
	// The store's library is checked before the define reaches it (the
	// grammar decisions, 30): a store of another build is refused with
	// nothing sent, as the client's first call would refuse it.
	if lc, ok := e.C.(interface{ CheckLibrary(context.Context) error }); ok {
		if err := lc.CheckLibrary(ctx); err != nil {
			return spverbs.Result{Verb: p.verb}, err
		}
	}
	lc, err := p.app.lifecycleAt(ctx, p.c.redis)
	if err != nil {
		return spverbs.Result{Verb: p.verb}, err
	}
	if _, err := spverbs.Define(ctx, lc, e.Names, build); err != nil {
		return spverbs.Result{Verb: p.verb}, err
	}
	res, err := spverbs.Init(ctx, e, req)
	var rf *spverbs.Refused
	if p.has("coordinator") && errors.As(err, &rf) && rf.Local && rf.Code() == sprintfn.CodeMachineState {
		again, err := spverbs.InitCoordinator(ctx, e, req)
		again.Trips += res.Trips
		return again, err
	}
	return res, err
}

// callTeardown is teardown: Layer 1's lifecycle teardown of the sprint's
// namespace, confirmed by the view's name; refused RUNNING while the machine
// runs (stop it first) and NOSPACE when there is no sprint.
func callTeardown(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
	lc, err := p.app.lifecycleAt(ctx, p.c.redis)
	if err != nil {
		return spverbs.Result{Verb: p.verb}, err
	}
	return spverbs.Teardown(ctx, lc, e.Names, spverbs.TeardownReq{Confirm: p.str("confirm")})
}

// checkAdd is add's grammar, as the present add checks it.
func checkAdd(p *parsed) error {
	ids := p.words
	if s := p.str("sentinel"); s != "" {
		if len(ids) > 0 || p.num("count") != 0 {
			return fmt.Errorf("--sentinel <id> admits one sentinel, with no other ids or --count")
		}
		ids = []string{s}
	}
	if p.str("stream") == "" || (len(ids) == 0) == (p.num("count") == 0) {
		return fmt.Errorf("wants --stream and either ids, --count <n> or --sentinel <id>")
	}
	if p.on("sentinel-last") && p.num("sentinel-every") == 0 {
		return fmt.Errorf("--sentinel-last goes with --sentinel-every <k>")
	}
	if len(sprint.Split(p.str("stream"))) > 1 && p.num("count") == 0 {
		return fmt.Errorf("several streams take --count <n>: each gets n cards")
	}
	if s := p.str("score"); s != "" {
		if _, err := strconv.ParseFloat(s, 64); err != nil {
			return fmt.Errorf("--score wants a number")
		}
	}
	return nil
}

// checkCardGens is take's and finish's grammar: --as, and every card named
// <card>@<gen>; finish names at least one.
func checkCardGens(atLeastOne bool) func(p *parsed) error {
	return func(p *parsed) error {
		ids, gens, err := cardGens(p.words)
		if err != nil {
			return err
		}
		if p.str("as") == "" || len(gens) != len(ids) || atLeastOne && len(ids) == 0 {
			return fmt.Errorf("wants --as <member>, and every card named as <card>@<gen>, the generation from queue --as <member>")
		}
		return nil
	}
}

// checkGroup is --group's and --expect's grammar (withGroup's checks that need
// no read).
func checkGroup(p *parsed) error {
	if p.str("group") == "" {
		if p.num("expect") != 0 {
			return fmt.Errorf("--expect goes with --group <id>")
		}
		return nil
	}
	if len(p.words) > 0 {
		return fmt.Errorf("takes ids or --group, not both")
	}
	if p.num("expect") < 0 {
		return fmt.Errorf("--expect wants the group's size, a whole number from 1")
	}
	if isNumber(p.str("group")) {
		return fmt.Errorf("group numbers are not accepted: a group is named by its id, which does not move")
	}
	return nil
}

// needsSet is a set verb's check that it names a set.
func needsSet(why string) func(p *parsed) error {
	return func(p *parsed) error {
		if err := checkGroup(p); err != nil {
			return err
		}
		if len(p.words) == 0 && p.str("stream") == "" && p.str("group") == "" {
			return fmt.Errorf("%s", why)
		}
		return nil
	}
}

// newVerbNamesSorted is every full verb name of the table, sorted (the tests
// enumerate it).
func newVerbNamesSorted() []string {
	var out []string
	for _, v := range newVerbs {
		out = append(out, v.name)
	}
	sort.Strings(out)
	return out
}
