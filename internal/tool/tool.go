// Package tool is the one shape of a nova command. A Tool is its verbs; a Verb
// declares its flags and returns one value, Out, which is rendered either as
// typed lines or as the JSON of the same value (out.go). Everything a command
// used to write for itself lives here once: the verb dispatch, the banner (what
// the tool is, how it works, its usage lines, its exit codes, a runnable
// example block), `help` and `<verb> -h`, the `version` verb, the standard
// flags (--json on every verb; --max, --actor, --op and --redis where a verb
// opts in), refusing to guess (every problem of one invocation named at once),
// and the refusal line with its remedy. A command holds only what its verbs do.
package tool

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
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
}

// Verb is one verb of a tool.
type Verb struct {
	Name    string
	Usage   string         // the usage line(s) after the tool's name, one form per line
	Example string         // runnable line(s) after the tool's name, for the banner's example block
	Effect  Effect         // what running it does to the world, stated in `help <verb>`
	Detail  string         // lines `help <verb>` prints above its flags: a format, a worked example
	Flags   func(f *Flags) // declares the verb's flags; nil declares none
	Run     func(c *Call) *Out
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
		return t.emit(nil, Refuse("no verb given; the verbs are "+strings.Join(t.names(), ", ")), false, stdout, stderr)
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
	for _, v := range t.verbs() {
		if v.Name == args[0] {
			return t.call(v, args[1:], stdin, stdout, stderr)
		}
	}
	return t.emit(nil, Refuse(fmt.Sprintf("unknown verb %q; the verbs are %s", args[0], strings.Join(t.names(), ", "))),
		verbflag.BoolAsked(args, "json"), stdout, stderr)
}

// help is deferred by Run: a verb's -h (verbflag's Help) prints that verb's
// help, quoted from the banner with its flags and the exit codes, then the
// verb's effect, on stdout at exit 0.
func (t *Tool) help(stdout io.Writer, code *int) {
	r := recover()
	if r == nil {
		return
	}
	h, ok := r.(verbflag.Help)
	if !ok {
		panic(r)
	}
	var b strings.Builder
	verbflag.Print(&b, t.Name, t.Banner(), h.FS)
	effect, detail := Effect("unstated"), ""
	for _, v := range t.verbs() {
		if v.Name == h.FS.Name() {
			detail = strings.Trim(v.Detail, "\n")
			if v.Effect != "" {
				effect = v.Effect
			}
		}
	}
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
	for _, v := range t.verbs() {
		e := string(v.Effect)
		if !strings.HasPrefix(e, "inspection") && !strings.HasPrefix(e, "local write") && !strings.HasPrefix(e, "delivery") {
			p = append(p, fmt.Sprintf("%s %s: the effect %q is not inspection, local write or delivery", t.Name, v.Name, e))
		}
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
		return t.emit(&v, Refuse(oneline.Cap(err.Error(), oneline.TailBytes)), !f.prints && verbflag.BoolAsked(args, "json"), stdout, stderr)
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
	if !f.args && f.NArg() > 0 {
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
	if f.max {
		o.capItems(c.Int("max"))
	}
	return t.emit(&v, o, asJSON, stdout, stderr)
}

// flags is the verb's flag set: its own flags and the standard ones.
func (v Verb) flags() *Flags {
	f := &Flags{FlagSet: verbflag.New(v.Name)}
	if v.Flags != nil {
		v.Flags(f)
	}
	if !f.prints {
		f.Bool("json", false, "print the result as one JSON object instead of lines")
	}
	return f
}

// emit fills what the tool knows (the verb, the door) and renders o: lines on
// stdout when it is OK and on stderr when it is not; JSON always on stdout.
func (t *Tool) emit(v *Verb, o *Out, asJSON bool, stdout, stderr io.Writer) int {
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
	if !asJSON && o.Status != OK {
		w = stderr
	}
	var b bytes.Buffer
	o.Render(&b, asJSON)
	_, _ = w.Write(b.Bytes())
	return o.Exit
}

// Flags is one verb's flag set: package flag's own, with its two mouths
// closed (verbflag.New), and the standard flags a verb opts into.
type Flags struct {
	*flag.FlagSet
	max, args, prints bool
	required          [][2]string
	checks            []func(c *Call)
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

// Positional lets the verb take positional arguments; without it one is refused.
func (f *Flags) Positional() { f.args = true }

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

// Args is the positional arguments (a verb that declared Positional).
func (c *Call) Args() []string { return c.flags.FlagSet.Args() }

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
