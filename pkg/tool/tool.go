// Package tool is the one shape of a nova command. A Tool is its verbs; a Verb
// declares its flags and returns one value, Out, which is rendered either as
// typed lines or as the JSON of the same value (out.go). Everything a command
// writes for itself lives here once: the verb dispatch, the banner (what the
// tool is, how it works, its usage lines, its exit codes, a runnable example
// block), `help` and `<verb> -h`, the `version` verb, the standard flags
// (--json on every verb; --max and --dry-run where a verb opts in), refusing to
// guess (every problem of one invocation named at once), and the refusal line
// with its remedy: an unknown verb or flag is answered with the nearest name
// and the ones there are, and a verb group's -h lists its verbs. A row carries
// a prose tail (Out.ItemText): a reason or a command renders plain after the
// row's typed fields and as the `text` field of the row's JSON, so the line and
// the object stay one value. A tool whose exit 0 already means CLEAR sets
// HelpRefused, and `<verb> -h` is then a refusal at exit 2 naming `help`, never
// an answer at exit 0. A tool may name a default verb (`<tool> <file>`) and its
// own status words (STALE beside FAIL). A verb may be hidden (Verb.Hidden): it
// runs and answers `-h`, and the banner, the usage block and the unknown-verb
// list do not show it, a probe step verb a user never types. A long-running
// verb prints each item as it goes; the Out it returns is the closing
// line. Call.Ctx is cancelled when the run's context ends and on interrupt
// (RunContext). A command holds only what its verbs do.
package tool

import (
	"bytes"
	"cmp"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/pkg/bounded"
	"github.com/mas-bandwidth/nova-tools/pkg/buildinfo"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// Tool is one command.
type Tool struct {
	Name string // the binary: nova-<name>
	What string // line 1 of the banner: what the tool is for
	// Stage, when set, is one sentence on how ready the tool is ("nova-x is
	// pre-alpha: not ready for production use."): the banner's line 2, the
	// second line of every verb's -h, and an indented NOTE line under a bare
	// command's refusal, so no reader meets the tool without it.
	Stage     string
	How       string // how it works: the paragraph under line 1
	Verbs     []Verb // in banner order; version and help are added here
	ExitTable string // "0 ..., 1 ..., 2 ...": the banner's exit-codes line
	Stamp     string // the build stamp (-ldflags -X main.version), for version
	// Default is the verb run when the first word is no verb: a flag, a path
	// (a word with a separator), or a word naming a file that is there
	// (`<tool> <file>...`); any other word is refused as no verb and no file.
	// The default verb accepts positional arguments, also when named explicitly.
	// "" makes every first word a verb and leaves every verb flags-only.
	Default string
	// Exists reports whether path is a file or directory that is there: the
	// one seam the skeleton reads the filesystem through (skeleton contract
	// 2.1). Nil defaults to checking with os.Stat. Tests pass a map.
	Exists func(path string) bool
	// Words are the tool's own status words (STALE, MISSING, UNCHANGED), the
	// only ones Out.As may put in place of OK or FAILED: at most MaxWords,
	// upper case, none of OK, FAILED, REFUSED, MORE or NOTE (Problems).
	Words []string
	// HelpRefused refuses `<verb> -h` (and --help) at exit 2 instead of
	// answering it at exit 0: a tool sets it when its exit 0 already means
	// CLEAR, so a `-h` answer could read as CLEAR (STANDARD §3 names the one
	// exception). The refusal names `help` as the door. No tool sets it yet.
	HelpRefused bool
	// NoJSON, when set, replaces the banner's standard --json sentence's
	// clause after the colon: a tool whose verbs print their own prose (the
	// note body a bus carries) says there why they take no --json and pastes
	// a line they print, so the help and the output cannot drift (ONBOARDING
	// point 6). Empty keeps the standard sentence.
	NoJSON string
	// UsageNote, when set, is printed under the usage block, before the
	// standard flags sentence: a tool states once, where a reader has just
	// read the usage lines, the shape of a value several of them name (the
	// manifest of --file), so no usage line carries it. Empty prints none.
	UsageNote string
	// Topics are the tool's help topics: `help <topic>` prints the topic's
	// text at exit 0, and the banner lists the topic names on one line
	// (skeleton contract 2.7). A tool's reference text lives here, never in
	// the banner, which a reader takes in at a glance (STANDARD §3 point 6).
	// A topic's name is none of the tool's verbs, since `help <name>` is one
	// door (Problems).
	Topics []Topic
}

// Topic is one help topic: `<tool> help <name>` prints Text on stdout at exit
// 0. It is where a tool's reference text lives, so the banner stays short
// (skeleton contract 2.7, STANDARD §3 point 6).
type Topic struct {
	Name string
	Text string
}

// MaxWords bounds a tool's own status words: a reader learns them all at once.
const MaxWords = 6

// reserved are the words every tool's lines already give a meaning.
var reserved = []string{"OK", "FAILED", "REFUSED", "MORE", "NOTE"}

// wordRe is one status word: upper case, digits and dashes after the first letter.
var wordRe = regexp.MustCompile(`^[A-Z][A-Z0-9-]*$`)

// Verb is one verb of a tool. A name of two words ("fn load") puts the verb in
// a group ("fn"): `<tool> fn -h` lists the group's verbs at exit 0.
type Verb struct {
	Name      string
	Usage     string         // the usage line(s) after the tool's name, one form per line
	Example   string         // runnable line(s) after the tool's name, for the banner's example block
	Effect    Effect         // what running it does to the world, stated in `help <verb>`
	Detail    string         // lines `help <verb>` prints above its flags: a format, a worked example
	ExitTable string         // this verb's exit codes, quoted by its -h; "" quotes the tool's
	DryRun    bool           // the verb takes --dry-run and honours it (Call.DryRun): it plans and writes nothing
	Hidden    bool           // the verb runs and answers -h and `help <it>`, but the banner, the usage block and the verb lists a refusal names do not show it: a probe step verb a user never types (STANDARD §3, help is never a refusal; §2, a list names the verbs there are for the reader)
	Flags     func(f *Flags) // declares the verb's flags; nil declares none
	Run       func(c *Call) *Out
}

// Effect is what running a verb does beyond printing: one of the three below,
// optionally with a clause after it; a verb whose flags change it states the
// strongest and says which flag (Problems holds every verb to one of the three).
type Effect string

const (
	Inspection Effect = "inspection: reads, writes nothing"
	LocalWrite Effect = "local write: writes files on this machine"
	Delivery   Effect = "delivery: sends beyond this machine"
)

// Main runs the tool over the process's arguments and streams and returns the exit code.
func (t *Tool) Main() int { return t.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr) }

// Run dispatches one invocation: help, version, or a verb. `<verb> -h` and
// `help <verb>` print that verb's help on stdout at exit 0 before anything is
// read or written (the CLI style's rule (b)); a tool that refuses help
// (HelpRefused) still answers `help <verb>` by name, while `<verb> -h` is a
// refusal at exit 2, since its exit 0 would read as CLEAR.
func (t *Tool) Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return t.RunContext(context.Background(), args, stdin, stdout, stderr)
}

// RunContext is Run on ctx. Call.Ctx is ctx, cancelled also on interrupt, so a
// long-running verb can stop (skeleton contract 2.4, STANDARD §2). A context
// that has already ended does not run the verb: the closing line names the
// context's error. One cancelled while the verb runs replaces that closing line
// the same way.
func (t *Tool) RunContext(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	return t.dispatch(ctx, args, stdin, stdout, stderr)
}

// dispatch is RunContext without the interrupt wrap: help's rewrite keeps ctx.
func (t *Tool) dispatch(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) (code int) {
	defer t.help(args, stdout, stderr, &code)
	if len(args) == 0 {
		given := "no verb given"
		if t.Default != "" {
			given = "no verb and no file given"
		}
		code := t.emit(nil, Refuse(given+"; the verbs are "+verbflag.List(t.names())), false, stdout, stderr)
		if t.Stage != "" {
			fmt.Fprintf(stderr, "  NOTE %s\n", t.Stage)
		}
		return code
	}
	switch args[0] {
	case "help", "-h", "--help":
		if args[0] == "help" && len(args) > 1 && args[1] != "help" && !verbflag.IsHelp(args[1]) {
			if text, ok := t.topic(args[1]); ok {
				fmt.Fprint(stdout, strings.TrimSuffix(text, "\n")+"\n")
				return 0
			}
			if t.HelpRefused {
				// Naming help is still help: only the -h flag is refused.
				var match *Verb
				for _, v := range t.verbs() { // the longest name the words begin with: "fn load" over "fn"
					if words := strings.Fields(v.Name); len(args)-1 >= len(words) && strings.Join(args[1:1+len(words)], " ") == v.Name &&
						(match == nil || len(v.Name) > len(match.Name)) {
						match = &v
					}
				}
				if match != nil {
					t.writeHelp(match.Name, match.flags().FlagSet, stdout)
					return 0
				}
			}
			return t.dispatch(ctx, append(args[1:], "--help"), stdin, stdout, stderr)
		}
		fmt.Fprint(stdout, t.Banner())
		return 0
	case "--version":
		args = append([]string{"version"}, args[1:]...)
	}
	var match *Verb
	for _, v := range t.verbs() { // the longest name the words begin with: "fn load" over "fn"
		if words := strings.Fields(v.Name); len(args) >= len(words) && strings.Join(args[:len(words)], " ") == v.Name &&
			(match == nil || len(v.Name) > len(match.Name)) {
			match = &v
		}
	}
	if match != nil {
		return t.call(ctx, *match, args[len(strings.Fields(match.Name)):], stdin, stdout, stderr)
	}
	asJSON := verbflag.BoolAsked(args, "json")
	if members := t.group(args[0]); len(members) > 0 {
		return t.inGroup(args, members, asJSON, stdout, stderr)
	}
	if t.Default != "" && (strings.HasPrefix(args[0], "-") || strings.ContainsRune(args[0], os.PathSeparator) || t.exists(args[0])) {
		for _, v := range t.verbs() { // a flag, a path, or a file: the default verb's
			if v.Name == t.Default {
				return t.call(ctx, v, args, stdin, stdout, stderr)
			}
		}
	}
	why := fmt.Sprintf("unknown verb %q;%s the verbs are %s", args[0], didYouMean(args[0], t.names()), verbflag.List(t.names()))
	if t.Default != "" {
		why = fmt.Sprintf("%q is no verb and no file;%s the verbs are %s, and a file is given by its path (./%s)",
			args[0], didYouMean(args[0], t.names()), verbflag.List(t.names()), args[0])
	}
	if len(t.Topics) > 0 {
		// A name that is no verb may be a topic: the refusal names both sets,
		// so one turn answers it (STANDARD §2, the names there are).
		why += ", and the help topics are " + verbflag.List(t.topicNames())
	}
	return t.emit(nil, Refuse(why), asJSON, stdout, stderr)
}

// topic is the text of the topic named, and whether the tool has one:
// `help <topic>` prints it at exit 0 (skeleton contract 2.7).
func (t *Tool) topic(name string) (string, bool) {
	for _, tp := range t.Topics {
		if tp.Name == name {
			return tp.Text, true
		}
	}
	return "", false
}

// topicNames are the tool's topic names, in the order the tool declares them.
func (t *Tool) topicNames() []string {
	var names []string
	for _, tp := range t.Topics {
		names = append(names, tp.Name)
	}
	return names
}

// exists reports whether a word names a file or directory that is there.
func (t *Tool) exists(path string) bool {
	if t.Exists != nil {
		return t.Exists(path)
	}
	_, err := os.Stat(path)
	return err == nil
}

// group is the verbs whose name's first word is word and has more after it.
func (t *Tool) group(word string) []string {
	var members []string
	for _, n := range t.names() {
		if rest, ok := strings.CutPrefix(n, word+" "); ok && rest != "" {
			members = append(members, n)
		}
	}
	return members
}

// inGroup answers a group with no verb of it matched: help (`<group> -h`,
// `help <group>`) lists the group's usage at exit 0, since help is never a
// refusal; a bare group or an unknown verb of it is refused with its verbs.
func (t *Tool) inGroup(args, members []string, asJSON bool, stdout, stderr io.Writer) int {
	g := args[0]
	if len(args) > 1 && verbflag.IsHelp(args[1]) {
		var subs []string
		for _, m := range members {
			subs = append(subs, strings.TrimPrefix(m, g+" "))
		}
		fmt.Fprintln(stdout, verbflag.UsageLine(t.Name, g, subs))
		for _, v := range t.verbs() {
			if slices.Contains(members, v.Name) {
				for _, l := range lines(v.Usage) {
					fmt.Fprintf(stdout, "  %s %s\n", t.Name, l)
				}
			}
		}
		fmt.Fprintf(stdout, "`%s %s <verb> -h` lists a verb's flags.\nexit codes: %s\n", t.Name, g, t.ExitTable)
		return 0
	}
	why := g + " wants one of its verbs;"
	if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
		why = fmt.Sprintf("unknown verb %q in %s;%s", g+" "+args[1], g, didYouMean(g+" "+args[1], members))
	}
	o := Refuse(why + " the verbs are " + verbflag.List(members))
	o.Remedy = t.Name + " " + g + " -h"
	return t.emit(nil, o, asJSON, stdout, stderr)
}

// didYouMean is " did you mean <name>?" for the name nearest to got, else "".
func didYouMean(got string, names []string) string {
	if best := verbflag.Nearest(got, names); best != "" {
		return " did you mean " + best + "?"
	}
	return ""
}

// help is deferred by dispatch: a verb's -h (verbflag's Help) prints that verb's
// help, quoted from the banner with its flags and the exit codes (the verb's
// own, Verb.ExitTable, where it states them), then the verb's effect, on
// stdout at exit 0. A tool that refuses help (HelpRefused) answers -h with a
// refusal instead: `-h` is not an answer the tool gives, and its exit 0 means
// CLEAR, so answering it could read as CLEAR. The refusal is a refusal like any
// other, so it renders as the one JSON object on stdout when args hold --json
// (skeleton contract 1.4 and 1.6: --json is always stdout), and as the plain
// line on stderr when they do not.
func (t *Tool) help(args []string, stdout, stderr io.Writer, code *int) {
	r := recover()
	if r == nil {
		return
	}
	h, ok := r.(verbflag.Help)
	if !ok {
		panic(r)
	}
	if t.HelpRefused {
		name := h.FS.Name()
		var v *Verb
		for _, cand := range t.verbs() {
			if cand.Name == name {
				v = &cand
				break
			}
		}
		o := Refuse("-h is not an answer this tool gives, its exit 0 means CLEAR")
		o.Remedy = t.Name + " help"
		*code = t.emit(v, o, verbflag.BoolAsked(args, "json"), stdout, stderr)
		return
	}
	t.writeHelp(h.FS.Name(), h.FS, stdout)
	*code = 0
}

// writeHelp prints one verb's help: its lines quoted from the banner with its
// flags and the exit codes (the verb's own, Verb.ExitTable, where it states
// them), then the verb's effect, on stdout.
func (t *Tool) writeHelp(name string, fs *flag.FlagSet, stdout io.Writer) {
	effect, detail, exit := Effect("unstated"), "", []string{"exit codes: " + t.ExitTable}
	for _, v := range t.verbs() {
		if v.Name == name {
			detail = strings.Trim(v.Detail, "\n")
			if v.Effect != "" {
				effect = v.Effect
			}
			if v.ExitTable != "" {
				exit = []string{"exit codes: " + v.ExitTable}
			}
		}
	}
	var b strings.Builder
	verbflag.Print(&b, t.Name, t.Banner(), fs, exit...)
	if detail != "" {
		detail += "\n"
	}
	text := b.String()
	if t.Stage != "" { // line 2, under the usage line
		usage, rest, _ := strings.Cut(text, "\n")
		text = usage + "\n" + t.Stage + "\n" + rest
	}
	fmt.Fprintf(stdout, "%seffect: %s\n", verbflag.Insert(text, detail), effect)
}

// HowLines and HowWidth bound the banner's how-it-works text: a reader takes
// in five lines at a glance, and a line past 100 characters wraps.
const (
	HowLines = 5
	HowWidth = 100
)

// HowLabel opens the how-it-works text in the banner (docs/ONBOARDING.md point
// 6), so a tool's How is the paragraph without it; the width bound counts it on
// the first line, where it is printed.
const HowLabel = "how it works: "

// Problems is where t falls short of the standard its banner and help carry
// by construction only when the definition is complete: a what line, an exit
// table, a how text of at most HowLines lines of at most HowWidth characters,
// and every verb's effect one of inspection, local write or delivery. A tool
// on this package runs it in its own tests (internal/ci holds every such
// package to that).
func (t *Tool) Problems() []string {
	var p []string
	if strings.TrimSpace(t.What) == "" || strings.TrimSpace(t.ExitTable) == "" {
		p = append(p, t.Name+": What and ExitTable are required")
	}
	how := strings.Split(strings.TrimSpace(t.How), "\n")
	if len(how) > HowLines {
		p = append(p, fmt.Sprintf("%s: the how text is %d lines, at most %d", t.Name, len(how), HowLines))
	}
	for i, l := range how {
		if i == 0 {
			l = HowLabel + l
		}
		if n := utf8.RuneCountInString(l); n > HowWidth {
			p = append(p, fmt.Sprintf("%s: how line %d is %d characters, at most %d", t.Name, i+1, n, HowWidth))
		}
	}
	if t.Default != "" && !slices.Contains(t.names(), t.Default) {
		p = append(p, fmt.Sprintf("%s: the default verb %q is none of its verbs", t.Name, t.Default))
	}
	if len(t.Words) > MaxWords {
		p = append(p, fmt.Sprintf("%s: %d status words of its own, at most %d", t.Name, len(t.Words), MaxWords))
	}
	for _, w := range t.Words {
		if !wordRe.MatchString(w) || slices.Contains(reserved, w) {
			p = append(p, fmt.Sprintf("%s: the status word %q is not an upper-case word of its own (OK, FAILED, REFUSED, MORE and NOTE are every tool's)", t.Name, w))
		}
	}
	for _, tp := range t.Topics {
		if slices.Contains(t.names(), tp.Name) {
			p = append(p, fmt.Sprintf("%s: the help topic %q is one of its verbs; a topic is a name of its own", t.Name, tp.Name))
		}
	}
	for _, v := range t.verbs() {
		e := string(v.Effect)
		if !strings.HasPrefix(e, "inspection") && !strings.HasPrefix(e, "local write") && !strings.HasPrefix(e, "delivery") {
			p = append(p, fmt.Sprintf("%s %s: the effect %q is not inspection, local write or delivery", t.Name, v.Name, e))
		}
		v.flags().VisitAll(func(f *flag.Flag) {
			if strings.TrimSpace(f.Usage) == "" {
				p = append(p, fmt.Sprintf("%s %s: --%s has no description; say what it wants", t.Name, v.Name, f.Name))
			}
		})
	}
	return p
}

// verbs is the tool's verbs and the version verb every tool has.
func (t *Tool) verbs() []Verb {
	return append(append([]Verb(nil), t.Verbs...), Verb{
		Name:   "version",
		Usage:  "version",
		Effect: Inspection,
		Run:    func(*Call) *Out { return Payload(buildinfo.Line(t.Name, t.Stamp)) },
	})
}

// shown is the verbs every list the tool prints names: the banner's usage and
// example blocks, the --json sentence and a refusal's verb list. A hidden verb
// (Verb.Hidden) is off every one of them, while it runs and answers help like
// any verb (STANDARD §2: an unknown name is answered with the names there are
// for the reader, and a probe step verb is not one of them).
func (t *Tool) shown() []Verb {
	var vs []Verb
	for _, v := range t.verbs() {
		if !v.Hidden {
			vs = append(vs, v)
		}
	}
	return vs
}

func (t *Tool) names() []string {
	var names []string
	for _, v := range t.shown() {
		names = append(names, v.Name)
	}
	return names
}

// Banner is what `help` prints: what, how, usage, the standard flags, the exit
// codes, and the example block last (docs/ONBOARDING.md point 1).
func (t *Tool) Banner() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s\n", t.Name, t.What)
	if t.Stage != "" {
		b.WriteString(t.Stage + "\n")
	}
	b.WriteString("\n")
	if how := strings.TrimSpace(t.How); how != "" {
		b.WriteString(HowLabel + how + "\n\n")
	}
	b.WriteString("usage:\n")
	for _, v := range t.shown() {
		for _, l := range lines(v.Usage) {
			fmt.Fprintf(&b, "  %s %s\n", t.Name, l)
		}
	}
	fmt.Fprintf(&b, "  %s help [<verb>]\n", t.Name)
	if len(t.Topics) > 0 {
		// One line names the topics and the way to read one: the reference
		// text itself stays out of the banner (skeleton contract 2.7).
		fmt.Fprintf(&b, "topics: %s; run: %s help <topic>\n", strings.Join(t.topicNames(), ", "), t.Name)
	}
	b.WriteString("\n")
	if t.UsageNote != "" {
		b.WriteString(t.UsageNote + "\n\n")
	}
	var own []string
	for _, v := range t.shown() {
		if v.flags().prints {
			own = append(own, v.Name)
		}
	}
	json := "Every verb takes --json"
	if len(own) > 0 {
		json = "Every verb but " + strings.Join(own, ", ") + " takes --json"
	}
	why := "the same result as one JSON object on stdout"
	if t.NoJSON != "" {
		why = t.NoJSON
	}
	b.WriteString(json + ": " + why + ". A verb that lists takes --max <n> (default 20, 0 lists all) and says MORE for the rest. `<verb> -h` lists a verb's flags.\n\n")
	fmt.Fprintf(&b, "exit codes: %s\n\n", t.ExitTable)
	b.WriteString("example:\n")
	for _, v := range t.shown() {
		for _, l := range lines(v.Example) {
			fmt.Fprintf(&b, "  %s %s\n", t.Name, l)
		}
	}
	return b.String()
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// call parses one verb's flags, runs it, caps its listing, and renders its Out.
func (t *Tool) call(ctx context.Context, v Verb, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	f := v.flags()
	c := &Call{Ctx: ctx, Stdin: stdin, Stdout: stdout, Stderr: stderr, flags: f, given: map[string]bool{},
		token: strings.ToUpper(strings.Join(strings.Fields(v.Name), "-"))}
	if err := verbflag.Parse(f.FlagSet, args); err != nil {
		o := Refuse(flagProblems(&v, f, args, err)...)
		o.Remedy = t.Name + " " + v.Name + " -h"
		return t.emit(&v, o, !f.prints && verbflag.BoolGiven(f.FlagSet, args, "json"), stdout, stderr)
	}
	f.Visit(func(fl *flag.Flag) { c.given[fl.Name] = true })
	asJSON := !f.prints && c.Bool("json")
	c.asJSON = asJSON
	// One pass over every rule the verb declared, so one run names every
	// problem: the required flags, the verb's own checks, --max, arguments.
	for _, r := range f.required {
		c.Want(r[0], r[1])
	}
	for _, check := range f.checks {
		check(c)
	}
	if f.max && c.Int("max") < 0 {
		c.Problem(fmt.Sprintf("--max must be zero or more (got %d); 0 lists all", c.Int("max")))
	}
	if v.Name != t.Default && f.NArg() > 0 {
		c.Problem(fmt.Sprintf("takes no positional arguments, got %q (flags come before arguments)", f.Arg(0)))
	}
	if o := c.Refused(); o != nil {
		return t.emit(&v, o, asJSON, stdout, stderr)
	}
	if err := c.Ctx.Err(); err != nil {
		// A cancelled context ends the verb (skeleton contract 2.4): the closing
		// line names the context's error, and a verb that has not started does not run.
		return t.emit(&v, Fail(err.Error()), asJSON, stdout, stderr)
	}
	o := v.Run(c)
	if err := c.Ctx.Err(); err != nil {
		o = Fail(err.Error())
	} else if r := c.Refused(); r != nil && (o == nil || o.Status != Refused) {
		o = r // a problem the verb recorded is never dropped
	}
	if o == nil {
		o = Fail("the verb returned no result") // never silent
	}
	if c.Given("dry-run") && c.Bool("dry-run") {
		switch {
		case !c.dryRead: // a tool bug its own tests meet: the verb ran as if for real
			o = Fail("--dry-run was given and the verb never read it (Call.DryRun); it may have written")
		case o.Status == OK && !hasFact(o, "dry_run"):
			o.Fact("dry_run", true)
		}
	}
	if f.max {
		o.Cap(c.Int("max"))
	}
	return t.emit(&v, o, asJSON, stdout, stderr)
}

// flagProblems is every flag failure of one run, in the one wording (STANDARD §2:
// a refusal names every problem of one invocation at once; §3 point 2). The flag
// package stops at the first word a flag cannot take, so the skeleton reads the
// rest of the words itself and words each failure with verbflag.Explain, the
// skeleton's wording of the flag package's three fixable errors. The first
// failure is the parse error itself; the rest are tried against a fresh flag set
// of the verb's own declarations, so no value reaches the run's own set. A value
// is never repeated, since it may be a secret. The reading ends at a word that
// is no flag, as the flag package reads it: that word is the unknown flag's value
// or the first argument.
func flagProblems(v *Verb, f *Flags, args []string, err error) []string {
	out := []string{oneline.Cap(verbflag.Explain(f.FlagSet, err), oneline.TailBytes)}
	probe := v.flags().FlagSet
	first := true // the scan's first failure is the parse error, which out already holds
	add := func(m string) {
		if first {
			first = false
			return
		}
		if m = oneline.Cap(m, oneline.TailBytes); !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	isFlag := func(a string) bool { return len(a) > 1 && a[0] == '-' }
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" || !isFlag(a) {
			return out // the terminator, or the first argument, ends the flags
		}
		name, value, inline := strings.Cut(strings.TrimLeft(a, "-"), "=")
		fl := probe.Lookup(name)
		switch {
		case fl == nil:
			add(verbflag.Explain(f.FlagSet, fmt.Errorf("flag provided but not defined: -%s", name)))
			if !inline && i+1 < len(args) && !isFlag(args[i+1]) {
				return out
			}
		case inline || !boolFlag(fl):
			if !inline {
				if i+1 >= len(args) {
					add(verbflag.Explain(f.FlagSet, fmt.Errorf("flag needs an argument: -%s", name)))
					return out
				}
				i, value = i+1, args[i+1]
			}
			if perr := probe.Set(name, value); perr != nil {
				// Explain words a bad value without repeating it, so the value stands
				// empty here and the flag's name is the reading's only key.
				m := verbflag.Explain(f.FlagSet, fmt.Errorf("invalid value %q for flag -%s: %v", "", name, perr))
				if !strings.HasPrefix(m, "invalid value for --"+name+":") {
					m = verbflag.Explain(f.FlagSet, fmt.Errorf("flag provided but not defined: -%s", name))
				}
				add(m)
			}
		}
	}
	return out
}

// boolFlag reports whether the flag takes no value, the way the flag package
// reads its boolFlag interface.
func boolFlag(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

// flags is the verb's flag set: its own flags and the standard ones.
func (v Verb) flags() *Flags {
	f := &Flags{FlagSet: verbflag.New(v.Name)}
	if v.Flags != nil {
		v.Flags(f)
	}
	if v.DryRun {
		f.Bool("dry-run", false, "print what the verb would write and write nothing")
	}
	if !f.prints {
		f.Bool("json", false, "print the result as one JSON object instead of lines")
	}
	return f
}

// emit fills what the tool knows (the verb, the door) and renders o: lines on
// stdout when it is OK and on stderr when it is not, but for a verb that ran
// and names its findings (Out.Findings): those on stderr, the rest on stdout;
// JSON always on stdout. A status word the tool does not declare is a tool
// bug its own tests meet, never printed as if it were one.
func (t *Tool) emit(v *Verb, o *Out, asJSON bool, stdout, stderr io.Writer) int {
	if o.Word != "" && (o.Status == Refused || !slices.Contains(t.Words, o.Word)) {
		o = Fail(fmt.Sprintf("the verb answered %s %s, a status word %s does not declare (Tool.Words) or one on a refusal",
			o.Status, o.Word, t.Name))
	}
	o.token = strings.ToUpper(strings.TrimPrefix(t.Name, "nova-"))
	if v != nil {
		o.Verb = v.Name
		o.token = strings.ToUpper(strings.Join(strings.Fields(v.Name), "-"))
	}
	if o.Status == Refused && o.Remedy == "" {
		o.Remedy = t.Name + " help"
	}
	if o.printed {
		return o.Exit
	}
	w := stdout
	if !asJSON && o.Status != OK && (o.Status == Refused || len(o.findings) == 0) {
		w = stderr
	}
	var b, found bytes.Buffer
	exit := o.render(&b, &found, &found, asJSON)
	_, err := w.Write(b.Bytes())
	if found.Len() > 0 {
		_, err2 := stderr.Write(found.Bytes())
		err = cmp.Or(err, err2)
	}
	if err != nil && exit == 0 {
		// the result did not reach its reader (a closed pipe): the exit says so
		return 1
	}
	return exit
}

// Flags is one verb's flag set: package flag's own, with its two mouths
// closed (verbflag.New), and the standard flags a verb opts into.
type Flags struct {
	*flag.FlagSet
	max, prints bool
	required    [][2]string
	checks      []func(c *Call)
}

// Required declares a string flag the verb cannot run without: empty, it is a
// problem that says what it wants, named with every other problem at once.
func (f *Flags) Required(name, wants string) {
	f.String(name, "", wants+" (required)")
	f.required = append(f.required, [2]string{name, wants})
}

// Check adds a rule over the parsed flags (c.Problem, c.Want),
// run with the required flags, --max and the arguments before the verb runs.
func (f *Flags) Check(rule func(c *Call)) { f.checks = append(f.checks, rule) }

// Max adds --max: the items listed before one MORE line stands for the rest.
func (f *Flags) Max() {
	f.max = true
	f.Int("max", bounded.Default, "items listed before one MORE line stands for the rest; 0 lists all")
}

// Prints marks a verb that writes its own output (a payload a program reads,
// a child's stream, or a body shared with a tool not yet on this package): it
// gets no --json, and returns Exit(code) after writing to c.Stdout and c.Stderr.
func (f *Flags) Prints() { f.prints = true }

// Call is one invocation of a verb: its streams, its context and its parsed flags.
type Call struct {
	// Ctx is the run's context, cancelled when that context ends and on
	// interrupt (skeleton contract 2.4). A verb that runs for a while selects
	// on it; a context that has ended is the closing line, not the verb's Out.
	Ctx            context.Context
	Stdin          io.Reader
	Stdout, Stderr io.Writer // written by a verb that Prints
	flags          *Flags
	given          map[string]bool
	problems       []string
	reasons        []string // parallel to problems; "" when Problem, a code when ProblemAs
	dryRead        bool
	token          string
	asJSON         bool
}

// DryRun reports whether --dry-run was given (only a Verb with DryRun takes
// it). A verb reads it before it writes and, when it is set, returns the plan
// the real run would carry out, from the same code path, and writes nothing;
// the skeleton adds dry_run=true to the OK line unless the verb set it.
func (c *Call) DryRun() bool {
	c.dryRead = true
	return c.given["dry-run"] && c.Bool("dry-run")
}

// Get is a declared flag's value, for a flag.Value of the verb's own (a flag.Getter).
func (c *Call) Get(name string) any { return c.flags.Lookup(name).Value.(flag.Getter).Get() }

// Str, Int, Bool and Dur read a declared flag's value.
func (c *Call) Str(name string) string        { return c.Get(name).(string) }
func (c *Call) Int(name string) int           { return c.Get(name).(int) }
func (c *Call) Bool(name string) bool         { return c.Get(name).(bool) }
func (c *Call) Dur(name string) time.Duration { return c.Get(name).(time.Duration) }

// Given reports whether the flag was on the command line.
func (c *Call) Given(name string) bool { return c.given[name] }

// Want reads a required string flag, recording a problem that says what it
// wants when it is empty. Every Want is read before Refused, so one run names
// every missing flag (docs/ONBOARDING.md point 2).
func (c *Call) Want(name, wants string) string {
	v := c.Str(name)
	if strings.TrimSpace(v) == "" {
		c.Problem(fmt.Sprintf("--%s is required; it wants %s; refusing to guess", name, wants))
	}
	return v
}

// Problem records one reason the invocation cannot run.
func (c *Call) Problem(what string) {
	c.problems = append(c.problems, what)
	c.reasons = append(c.reasons, "")
}

// ProblemAs records one reason with a stable code (skeleton contract 2.10,
// STANDARD §2). The refusal line carries reason=<code> before the colon, and
// the JSON result adds "reasons" beside "why": a program reads the code, a
// person reads the sentence.
func (c *Call) ProblemAs(reason, what string) {
	c.problems = append(c.problems, what)
	c.reasons = append(c.reasons, reason)
}

// Refused is the refusal naming every problem recorded, or nil when there is none.
func (c *Call) Refused() *Out {
	if len(c.problems) == 0 {
		return nil
	}
	o := Refuse(c.problems...)
	for _, r := range c.reasons {
		if r != "" {
			o.Reasons = c.reasons
			break
		}
	}
	return o
}

// hasFact reports whether o carries a fact of that name.
func hasFact(o *Out, k string) bool {
	if o == nil {
		return false
	}
	for _, f := range o.Facts {
		if f.K == k {
			return true
		}
	}
	return false
}
