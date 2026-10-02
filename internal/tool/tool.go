// Package tool is the one shape of a nova command. A Tool is its verbs; a Verb
// declares its flags and returns one value, Out, which is rendered either as
// typed lines or as the JSON of the same value (out.go). Everything a command
// writes for itself lives here once: the verb dispatch, the banner (what
// the tool is, how it works, its usage lines, its exit codes, a runnable
// example block), `help` and `<verb> -h`, the `version` verb, the standard
// flags (--json on every verb; --max, --actor, --op, --redis and --dry-run
// where a verb opts in), refusing to guess (every problem of one invocation
// named at once), and the refusal line with its remedy: an unknown verb or
// flag is answered with the nearest name and the ones there are, and a verb
// group's -h lists its verbs. A tool may name a default verb (`<tool> <file>`)
// and its own status words (STALE beside FAIL). A command holds only what its
// verbs do.
package tool

import (
	"bytes"
	"cmp"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Tool is one command.
type Tool struct {
	Name      string // the binary: nova-<name>
	What      string // line 1 of the banner: what the tool is for
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
	// Words are the tool's own status words (STALE, MISSING, UNCHANGED), the
	// only ones Out.As may put in place of OK or FAIL: at most MaxWords,
	// upper case, none of OK, FAIL, REFUSED, MORE or NOTE (Problems).
	Words []string
}

// MaxWords bounds a tool's own status words: a reader learns them all at once.
const MaxWords = 6

// reserved are the words every tool's lines already give a meaning.
var reserved = []string{"OK", "FAIL", "FAILED", "REFUSED", "MORE", "NOTE"}

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
// read or written (the CLI style's rule (b)).
func (t *Tool) Run(args []string, stdin io.Reader, stdout, stderr io.Writer) (code int) {
	defer t.help(stdout, &code)
	if len(args) == 0 {
		given := "no verb given"
		if t.Default != "" {
			given = "no verb and no file given"
		}
		return t.emit(nil, Refuse(given+"; the verbs are "+verbflag.List(t.names())), false, stdout, stderr)
	}
	switch args[0] {
	case "help", "-h", "--help":
		if args[0] == "help" && len(args) > 1 && args[1] != "help" && !verbflag.IsHelp(args[1]) {
			return t.Run(append(args[1:], "--help"), stdin, stdout, stderr)
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
		return t.call(*match, args[len(strings.Fields(match.Name)):], stdin, stdout, stderr)
	}
	asJSON := verbflag.BoolAsked(args, "json")
	if members := t.group(args[0]); len(members) > 0 {
		return t.inGroup(args, members, asJSON, stdout, stderr)
	}
	if t.Default != "" && (strings.HasPrefix(args[0], "-") || strings.ContainsRune(args[0], os.PathSeparator) || exists(args[0])) {
		for _, v := range t.verbs() { // a flag, a path, or a file: the default verb's
			if v.Name == t.Default {
				return t.call(v, args, stdin, stdout, stderr)
			}
		}
	}
	why := fmt.Sprintf("unknown verb %q;%s the verbs are %s", args[0], didYouMean(args[0], t.names()), verbflag.List(t.names()))
	if t.Default != "" {
		why = fmt.Sprintf("%q is no verb and no file;%s the verbs are %s, and a file is given by its path (./%s)",
			args[0], didYouMean(args[0], t.names()), verbflag.List(t.names()), args[0])
	}
	return t.emit(nil, Refuse(why), asJSON, stdout, stderr)
}

// exists reports whether a word names a file or directory that is there.
func exists(path string) bool {
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

// help is deferred by Run: a verb's -h (verbflag's Help) prints that verb's
// help, quoted from the banner with its flags and the exit codes (the verb's
// own, Verb.ExitTable, where it states them), then the verb's effect, on
// stdout at exit 0.
func (t *Tool) help(stdout io.Writer, code *int) {
	r := recover()
	if r == nil {
		return
	}
	h, ok := r.(verbflag.Help)
	if !ok {
		panic(r)
	}
	effect, detail, exit := Effect("unstated"), "", []string{"exit codes: " + t.ExitTable}
	for _, v := range t.verbs() {
		if v.Name == h.FS.Name() {
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
	verbflag.Print(&b, t.Name, t.Banner(), h.FS, exit...)
	if detail != "" {
		detail += "\n"
	}
	fmt.Fprintf(stdout, "%seffect: %s\n", verbflag.Insert(b.String(), detail), effect)
	*code = 0
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
			p = append(p, fmt.Sprintf("%s: the status word %q is not an upper-case word of its own (OK, FAIL, REFUSED, MORE and NOTE are every tool's)", t.Name, w))
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

func (t *Tool) names() []string {
	var names []string
	for _, v := range t.verbs() {
		names = append(names, v.Name)
	}
	return names
}

// Banner is what `help` prints: what, how, usage, the standard flags, the exit
// codes, and the example block last (docs/ONBOARDING.md point 1).
func (t *Tool) Banner() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s\n\n", t.Name, t.What)
	if how := strings.TrimSpace(t.How); how != "" {
		b.WriteString(HowLabel + how + "\n\n")
	}
	b.WriteString("usage:\n")
	for _, v := range t.verbs() {
		for _, l := range lines(v.Usage) {
			fmt.Fprintf(&b, "  %s %s\n", t.Name, l)
		}
	}
	fmt.Fprintf(&b, "  %s help [<verb>]\n\n", t.Name)
	var own []string
	for _, v := range t.verbs() {
		if v.flags().prints {
			own = append(own, v.Name)
		}
	}
	json := "Every verb takes --json"
	if len(own) > 0 {
		json = "Every verb but " + strings.Join(own, ", ") + " takes --json"
	}
	b.WriteString(json + ": the same result as one JSON object on stdout. A verb that lists takes --max <n> (default 20, 0 lists all) and says MORE for the rest. `<verb> -h` lists a verb's flags.\n\n")
	fmt.Fprintf(&b, "exit codes: %s\n\n", t.ExitTable)
	b.WriteString("example:\n")
	for _, v := range t.verbs() {
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
func (t *Tool) call(v Verb, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	f := v.flags()
	c := &Call{Stdin: stdin, Stdout: stdout, Stderr: stderr, flags: f, given: map[string]bool{}}
	if err := verbflag.Parse(f.FlagSet, args); err != nil {
		o := Refuse(oneline.Cap(verbflag.Explain(f.FlagSet, err), oneline.TailBytes))
		o.Remedy = t.Name + " " + v.Name + " -h"
		return t.emit(&v, o, !f.prints && verbflag.BoolGiven(f.FlagSet, args, "json"), stdout, stderr)
	}
	f.Visit(func(fl *flag.Flag) { c.given[fl.Name] = true })
	asJSON := !f.prints && c.Bool("json")
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
	o := v.Run(c)
	if r := c.Refused(); r != nil && (o == nil || o.Status != Refused) {
		o = r // a problem the verb recorded is never dropped
	}
	if o == nil {
		o = Fail("the verb returned no result") // never silent
	}
	if c.Given("dry-run") && c.Bool("dry-run") {
		switch {
		case !c.dryRead: // a tool bug its own tests meet: the verb ran as if for real
			o = Fail("--dry-run was given and the verb never read it (Call.DryRun); it may have written")
		case o.Status == OK:
			o.Fact("dry_run", true)
		}
	}
	if f.max {
		o.Cap(c.Int("max"))
	}
	return t.emit(&v, o, asJSON, stdout, stderr)
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

// Check adds a rule over the parsed flags (c.Problem, c.Want, c.WantCount),
// run with the required flags, --max and the arguments before the verb runs.
func (f *Flags) Check(rule func(c *Call)) { f.checks = append(f.checks, rule) }

// Max adds --max: the items listed before one MORE line stands for the rest.
func (f *Flags) Max() {
	f.max = true
	f.Int("max", bounded.Default, "items listed before one MORE line stands for the rest; 0 lists all")
}

// Actor adds --actor: who is acting, recorded with the change.
func (f *Flags) Actor() { f.String("actor", "", "who is acting, recorded with the change") }

// Op adds --op: the caller's operation id, for a write retried safely.
func (f *Flags) Op() {
	f.String("op", "", "the caller's operation id: the same id again returns the recorded result and changes nothing")
}

// Redis adds --redis with the tool's seat-first default (the address of the
// seat the process runs as, else the environment's), so a verb reads
// c.Want("redis", ...) and an empty address is a refusal, never a guess.
func (f *Flags) Redis(seatFirst string) {
	f.String("redis", seatFirst, "the Redis address, host:port (default: the seat's)")
}

// Prints marks a verb that writes its own output (a payload a program reads,
// a child's stream, or a body shared with a tool not yet on this package): it
// gets no --json, and returns Exit(code) after writing to c.Stdout and c.Stderr.
func (f *Flags) Prints() { f.prints = true }

// Call is one invocation of a verb: its streams and its parsed flags.
type Call struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer // written only by a verb that Prints
	flags          *Flags
	given          map[string]bool
	problems       []string
	dryRead        bool
}

// DryRun reports whether --dry-run was given (only a Verb with DryRun takes
// it). A verb reads it before it writes and, when it is set, returns the plan
// the real run would carry out, from the same code path, and writes nothing;
// the skeleton adds dry_run=true to the OK line.
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

// WantCount is Want for a count whose floor is one.
func (c *Call) WantCount(name, wants string) int {
	v := c.Int(name)
	if v < 1 {
		c.Problem(fmt.Sprintf("--%s is required and is at least 1, got %d; it wants %s; refusing to guess", name, v, wants))
	}
	return v
}

// Problem records one reason the invocation cannot run.
func (c *Call) Problem(what string) { c.problems = append(c.problems, what) }

// Refused is the refusal naming every problem recorded, or nil when there is none.
func (c *Call) Refused() *Out {
	if len(c.problems) == 0 {
		return nil
	}
	return Refuse(c.problems...)
}
