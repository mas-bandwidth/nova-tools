package update

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/release"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// Environment supplies deterministic clock/network seams. Nil values use the
// machine clock and a credential-free, redirect-bounded HTTP client.
type Environment struct {
	Now         func() time.Time
	Client      *http.Client
	Context     context.Context
	WorkerStart func(id int)
	JobAttempt  func(index int)
	DrainTimer  func(time.Duration) (<-chan time.Time, func() bool)
}
type options struct {
	file, host, snapshot, as, to, bus, remote, branch, target, adopt, store string
	max                                                                     int
	timeout, budget                                                         time.Duration
	kinds                                                                   kindFlags
	draft, send, dryRun                                                     bool
}
type kindFlags []string

func (k *kindFlags) String() string { return strings.Join(*k, ",") }
func (k *kindFlags) Get() any       { return k }
func (k *kindFlags) Set(v string) error {
	if !kindValid(v) {
		return fmt.Errorf("unknown kind %s (use harness,engine,model,tool,pin)", v)
	}
	*k = append(*k, v)
	return nil
}
func field(s string) string {
	if s == "" {
		return "-"
	}
	return oneline.Field(s)
}

// refusal is the one refusal line (STANDARD §2): what was wrong and what the
// input wants, then the command a reader runs next.
func refusal(w io.Writer, token, run string, err error) int {
	fmt.Fprintf(w, "%s REFUSED: %s; run: %s\n", token, oneline.Err(err), run)
	return 2
}

// updateVerbNames are nova-update's verbs, as a refusal lists them.
const updateVerbNames = "example, check, status, apply, report, watch, adoption, release, version"

// flagProblem says what a flag parse error means in the words a reader acts on
// (STANDARD §3.2): an unknown flag is named with every flag the verb takes, a
// bad value with what the flag wants, never the flag package's own sentence.
func flagProblem(f *flag.FlagSet, err error) error {
	msg := err.Error()
	if name, ok := strings.CutPrefix(msg, "flag provided but not defined: -"); ok {
		var names []string
		f.VisitAll(func(fl *flag.Flag) { names = append(names, "--"+fl.Name) })
		return fmt.Errorf("unknown flag --%s; the flags are %s", strings.TrimLeft(name, "-"), strings.Join(names, ", "))
	}
	if name, ok := strings.CutPrefix(msg, "flag needs an argument: -"); ok {
		return fmt.Errorf("--%s needs a value", strings.TrimLeft(name, "-"))
	}
	if rest, ok := strings.CutPrefix(msg, "invalid value "); ok {
		if value, err := strconv.QuotedPrefix(rest); err == nil {
			name, why, _ := strings.Cut(strings.TrimPrefix(rest[len(value):], " for flag -"), ": ")
			if fl := f.Lookup(name); fl != nil {
				switch kind, _ := flag.UnquoteUsage(fl); kind {
				case "duration":
					return fmt.Errorf("--%s wants a duration (5s, 2m), got %s", name, value)
				case "int":
					return fmt.Errorf("--%s wants a whole number, got %s", name, value)
				}
			}
			return fmt.Errorf("--%s got %s: %s", name, value, why)
		}
	}
	return err
}

// updateVerbs is SPEC-UPDATE's verbs block, byte for byte,
// including its placeholder spellings: <k> not <kind>, <v> not <version>,
// <who,who> not <recipients>, <r> and <b> for the remote and the branch, and the
// report line's alternation showing that --send is the one that needs a bus. A
// change here belongs in the spec first, and TestHelpIsTheSpecsVerbsBlock reads
// the spec file and compares the two. nova-version's usage lines are its
// verbs' own (versiontool.go). The release verbs are one line here; their own
// lines are release.Verbs, printed by `nova-update help release`.
const updateVerbs = `nova-update example [--out <path>]
nova-update check --file <path> [--max <n>] [--timeout <d>] [--budget <d>] [--kind <k>]
nova-update status --file <path> [--max <n>] [--timeout <d>] [--budget <d>] [--kind <k>]
nova-update apply --file <path> <name> [--version <v>] [--dry-run] [--timeout <d>]
nova-update report --file <path> [--host <label>] [--snapshot <path>] [--draft --as <friend> --to <who,who> | --send --as <friend> --to <who,who> --bus <path> --remote <r> --branch <b>] [--max <n>] [--timeout <d>] [--budget <d>] [--kind <k>]
nova-update report --store <host:port> [--timeout <d>]
nova-update watch --adopt <checks.tsv> [--bus <path> --remote <r> --branch <b> --as <friend> --to <who,who>] [--host <label>] [--timeout <d>] [--budget <d>]
nova-update adoption --file <path> [--as <friend>] [--max <n>]
nova-update release <cut|build|install|adopt|pull> ...   nova-tools' own release pipeline: nova-update help release prints its usage lines
nova-update help`

// manifestShape is the one sentence that says what the file --file names holds:
// the rule-2 manifest, one tab-separated line per tool, written by hand in git.
// The usage line and the refusal on a missing file both carry it, so neither
// reads as if --file were an output. Only `example` writes one, the example to
// start from; the spec carries the same shape once (SPEC-UPDATE rule 2).
const manifestShape = "one line per tool, six tab-separated fields name kind installed latest apply owner, written by hand"

// updateOpening opens the banner with its three answers:
// what the tool does (line 1, the README's sentence), how it works, and the
// first run (ONBOARDING.md point 6).
const (
	updateOpening = `nova-update: compare installed tools with their latest releases, and update one when asked

how it works: the manifest is a tab-separated file you write, one tool per line:
how to read its installed version, where its latest release is published, and
the command that installs it. check and report compare the two; apply runs one
named entry's command and reads the version again, nothing else. The release
verbs build, publish and install nova-tools' own releases.
first run: the binary alone; the lines under example: write a one-tool manifest
(Go) to ./versions.tsv and read it; they install nothing.`
)

// helpText is what help prints, as a string: the text verbflag quotes a verb's
// lines from.
func helpText(name string) string {
	var b strings.Builder
	help(name, &b)
	return b.String()
}

func help(name string, w io.Writer) {
	// SPEC-UPDATE's "The verbs" block says these lines are what help prints,
	// BYTE FOR BYTE, and names one string in the binary as the reason the spec
	// and the help cannot drift apart. This is that string.
	fmt.Fprintf(w, "%s\n\n", updateOpening)
	fmt.Fprintln(w, updateVerbs)
	fmt.Fprintf(w, "%s version (or --version)\nDefaults: --max 20 (0 = all), --timeout 5s, --budget 60s. Repeat --kind to select kinds. Every verb but watch and release takes --json: the same result as one JSON object on stdout. A result's first line is the verb, OK, FAIL or REFUSED, and the run's counts; `<verb> -h` lists a verb's flags and effect.\n", name)
	note := "Report needs no bus or network. Updates require an explicit apply name. status is check with every entry shown, current ones too. apply --dry-run prints the plan and writes nothing. "
	note += "Cross-process delivery recovery needs --snapshot; without it, each send is a new intention. Do not prepare again while pending; retry the saved artifact. A snapshot uses a sibling .lock file for a kernel lock; its presence never means a process is running."
	fmt.Fprintln(w, note)
	fmt.Fprintf(w, "\nLocals: latest=local:<path> runs that binary (or argv) on this host to read the version; e.g., local:/usr/local/bin/nova-update or local:go version. The installed column can be a version string (v1.2.3), a single command name found on PATH, or a full argv.\n")
	fmt.Fprint(w, twoBinaries())
	fmt.Fprint(w, manifestHelp(name))
	fmt.Fprintf(w, "\n%s\n\nexample:\n", exitCodes(name))
	for _, line := range []string{"example --out versions.tsv", "report --file versions.tsv", "status --file versions.tsv", "apply --file versions.tsv go --dry-run", "version"} {
		fmt.Fprintf(w, "  %s %s\n", name, line)
	}
}

// twoBinaries says, in nova-update's banner, how nova-update and nova-version
// divide the work, so a reader who finds both on a PATH knows which to reach for;
// nova-version's banner says the same in its how text (versiontool.go). They are
// two builds sharing this package: neither one's verbs are a subset of the other's.
func twoBinaries() string {
	// The line opens with "Both", not the tool's name: a banner line that opens
	// with the name is read as a usage line naming a verb ("and").
	return "\nBoth nova-update and nova-version read this manifest: they are two binaries that share the manifest reader and report (report prints the same lines under either). " +
		"Use nova-update to ASK whether what you depend on is current and to CHANGE it: check and status (installed against latest, one line per finding; status shows the current ones too), apply (install the one entry you name, or print the plan with --dry-run), watch (run a file of adoption checks and post the receipt), adoption (list who adopted which tool) and release (cut, build, install, adopt and pull a nova-tools release). " +
		"Use nova-version to RECORD what is installed: snapshot, diff, moved and send are nova-version's.\n"
}

// manifestHelp is the manifest format in six lines, under the `report` example line so
// `report -h` quotes it (verbflag.Excerpt reads a verb's lines with the lines indented
// beneath them): the rule-2 file --file names, the same for both tools.
func manifestHelp(name string) string {
	return "\nTHE MANIFEST is the file --file names, written by hand, the same for both tools:\n" +
		"  " + name + " report --file versions.tsv     the six lines that say what versions.tsv holds:\n" +
		"      1. line 1 is the header, byte for byte: " + tabbed(Header) + "; every other line is six fields, one tab between, none empty; a line starting # is a comment\n" +
		"      2. kind is harness, engine, model, tool or pin; name is unique in the file; owner is who answers for it\n" +
		"      3. installed is a version (v1.2.3), a command name on PATH, or an argv whose first line of output carries the version (single spaces, no quotes)\n" +
		"      4. latest is github:<owner>/<repo>, npm:<package>, brew:<formula>, ollama:<model>:<tag> (kind model), local:<argv> (a pin takes this only), or - for not known yet\n" +
		"      5. apply is the argv that updates it, or none; a run prints EVERY problem of the file at once, each with its line, never the first alone\n" +
		"      6. example: go<TAB>tool<TAB>go version<TAB>local:go version<TAB>none<TAB>me\n"
}

// verbDetail is what `<verb> -h` adds to the verb's usage lines and flags: the
// manifest's rules for the verbs that read one (report's are quoted from the
// banner already) and the verb's effect, one of inspection, local write or
// delivery (STANDARD §2, "its effects are explicit").
func verbDetail(name, verb string) string {
	effects := map[string]string{
		"check":    "inspection: reads each tool's installed version and asks its latest source (github:, npm:, brew: and ollama: are network reads); writes nothing",
		"status":   "inspection: the reads of check; writes nothing",
		"apply":    "local write: runs the named entry's apply command, which installs; --dry-run starts no process and writes nothing",
		"report":   "inspection: reads each installed version, no latest, no network; --snapshot writes its state file (local write); --send delivers the note through nova-bus (delivery); --store reads the fleet's Redis",
		"watch":    "inspection: runs each check's command; with --bus and its four companions, delivery: the receipt goes out through nova-bus",
		"adoption": "inspection: reads the ledger, writes nothing",
		"example":  "inspection: prints the example manifest; with --out, local write: writes it, never over another file",
		"version":  "inspection: prints this binary's version line",
	}
	detail := ""
	if verb == "check" || verb == "status" || verb == "apply" {
		detail = strings.TrimPrefix(manifestHelp(name), "\n")
	}
	if verb == "watch" {
		detail = "lines: ADOPT OK or ADOPT REFUSED per check; ADOPT ESCALATE names a refused check's owner, for whoever answers refusals (this tool files nothing); " +
			"ADOPT DONE ends the pass, its sha= the first twelve hex of the sha256 of the pass's sorted results, so two passes with one outcome share it. It takes no --json.\n"
	}
	if e, ok := effects[verb]; ok {
		detail += "effect: " + e + "\n"
	}
	return detail
}

// exitCodes is nova-update's exit-code paragraph, for the verbs it has (check, apply,
// report); nova-version's is its Tool's ExitTable (versiontool.go), and neither names a
// verb of the other.
func exitCodes(name string) string {
	return "exit codes: 0 every entry current, an apply that left the box on the target (or an apply --dry-run that printed its plan), a report whose every entry answered; 1 the tool said NO (anything STALE, NEWER, DIFFERENT or UNKNOWN, an apply whose after is not the target, a report with an UNKNOWN or a send that was refused or unconfirmed); 2 could not run (a refusal naming the remedy)."
}
func interspersed(f *flag.FlagSet, args []string) []string {
	var flags, positionals []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			positionals = append(positionals, a)
			continue
		}
		flags = append(flags, a)
		key, _, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
		known := f.Lookup(key)
		if !hasValue && known != nil {
			isBool := false
			if b, ok := known.Value.(interface{ IsBoolFlag() bool }); ok {
				isBool = b.IsBoolFlag()
			}
			if !isBool && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		}
	}
	if len(positionals) > 0 {
		flags = append(flags, "--")
		flags = append(flags, positionals...)
	}
	return flags
}

// versionVerb prints the version line, or with --json internal/tool's Out with the
// line as its payload: the one shape every skeleton tool's version verb answers
// (internal/tool's verbs), refusals worded as the skeleton words them.
func versionVerb(name, stamp string, args []string, out, errs io.Writer) int {
	f := verbflag.New("version")
	asJSON := f.Bool("json", false, "print the result as one JSON object instead of lines")
	help := name + " version -h"
	if err := verbflag.Parse(f, args); err != nil {
		return emit(refused("version", help, oneline.Cap(verbflag.Explain(f, err), oneline.TailBytes)), verbflag.BoolGiven(f, args, "json"), 0, out, errs)
	}
	if f.NArg() > 0 {
		// The skeleton's remedy for a problem the parse did not find is the banner.
		return emit(refused("version", name+" help", fmt.Sprintf("takes no positional arguments, got %q (flags come before arguments)", f.Arg(0))), *asJSON, 0, out, errs)
	}
	o := tool.Payload(buildinfo.Line(name, stamp))
	o.Verb = "version"
	return emit(o, *asJSON, 0, out, errs)
}

func Main(name string, args []string, stamp string, out, errs io.Writer) int {
	return Run(name, args, stamp, out, errs, Environment{})
}
func Run(name string, args []string, stamp string, out, errs io.Writer, env Environment) (rc int) {
	if env.Now == nil {
		env.Now = time.Now
	}
	if name == "nova-version" {
		return VersionTool(stamp, env).Run(args, nil, out, errs)
	}
	// `<verb> -h` and `help <verb>` print that verb's help on stdout at exit 0,
	// before any manifest, bus or store is read (the CLI style's rule (b), #4505).
	defer verbflag.RecoverWith(out, name, helpText(name), &rc, func(verb string) string { return verbDetail(name, verb) })
	door, asked := name+" help", verbflag.BoolAsked(args, "json")
	if len(args) == 0 {
		return emit(refused("update", door, "no verb given; the verbs are "+updateVerbNames), asked, 0, out, errs)
	}
	verb := args[0]
	args = args[1:]
	if verb == "help" || verb == "--help" || verb == "-h" {
		if verb == "help" && len(args) > 0 && args[0] != "help" && !verbflag.IsHelp(args[0]) {
			return Run(name, append(args, "--help"), stamp, out, errs, env)
		}
		help(name, out)
		return 0
	}
	if verb == "version" || verb == "--version" {
		return versionVerb(name, stamp, args, out, errs)
	}
	// `release` is the last mile -- cut, build, install, adopt -- and it is a
	// verb of nova-update rather than a tool of its own because it is the same
	// question this binary already answers (what is installed here, and is it
	// what it should be) asked from the other end. internal/release holds it.
	if verb == "release" {
		// The stamp goes down with it: `release adopt` compares what THIS
		// binary is against the release it is fanning out, because the
		// install every machine runs is the one this host is holding.
		return release.Main(name, args, stamp, out, errs)
	}
	if verb == "adoption" {
		return adoptionVerb(name, args, out, errs)
	}
	if verb == "example" {
		f := flag.NewFlagSet("example", flag.ContinueOnError)
		f.SetOutput(io.Discard)
		path := f.String("out", "", "write the example manifest to this path (an existing file is never overwritten); without it, print the manifest")
		asJSON := f.Bool("json", false, "print the result as one JSON object instead of lines")
		if err := verbflag.Parse(f, args); err != nil {
			return emit(refused("example", name+" example -h", flagProblem(f, err).Error()), asked, 0, out, errs)
		}
		if f.NArg() != 0 {
			return emit(refused("example", name+" example -h", fmt.Sprintf("example takes no positional arguments, got %q", f.Arg(0))), *asJSON, 0, out, errs)
		}
		return emit(exampleVerb(name, *path), *asJSON, 0, out, errs)
	}
	if verb != "report" && verb != "check" && verb != "status" && verb != "apply" && verb != "watch" {
		return emit(refused("update", door, fmt.Sprintf("unknown verb %q; the verbs are %s", verb, updateVerbNames)), asked, 0, out, errs)
	}
	if verb == "watch" {
		return watchMain(name, args, out, errs, env)
	}
	o := options{max: 20, timeout: 5 * time.Second, budget: 60 * time.Second}
	asJSON := false
	f := flag.NewFlagSet(verb, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&o.file, "file", "", "the manifest (required): "+manifestShape)
	timeoutWants := "one read's deadline, such as 5s"
	if verb == "apply" {
		timeoutWants = "the deadline of each version read and of the install command itself, such as 5m for a slow installer"
	}
	f.DurationVar(&o.timeout, "timeout", o.timeout, timeoutWants)
	f.BoolVar(&asJSON, "json", false, "print the result as one JSON object instead of lines")
	if verb == "apply" {
		f.StringVar(&o.target, "version", "", "the version to install, when the entry's apply argv holds {version}; default: the latest its source reports")
		f.BoolVar(&o.dryRun, "dry-run", false, "print the plan and install nothing: no process starts")
	} else {
		f.DurationVar(&o.budget, "budget", o.budget, "the whole run's deadline, such as 60s")
		f.IntVar(&o.max, "max", 20, "lines listed per kind before one MORE line stands for the rest; 0 lists all")
		f.Var(&o.kinds, "kind", "read only entries of this kind (harness, engine, model, tool or pin); repeat for several")
	}
	if verb == "report" {
		reportDeliveryFlags(f, &o)
		f.BoolVar(&o.send, "send", false, "deliver the note through nova-bus (needs --as, --to, --bus, --remote, --branch)")
		f.StringVar(&o.store, "store", "", "a fleet Redis host:port: report every bench's nova-sprint build from its beat, instead of --file")
	}
	if err := verbflag.Parse(f, interspersed(f, args)); err != nil {
		// `<tool> <verb> --help` never lands here: verbflag.Parse raises that
		// verb's help, which Run prints on stdout at exit 0 (asking is not an
		// error; darwin dogfood, 2026-09-18).
		return emit(refused(verb, name+" "+verb+" -h", flagProblem(f, err).Error()), verbflag.BoolAsked(args, "json"), 0, out, errs)
	}
	if o.store != "" {
		return emit(storeReport(name, o, f.Args(), env), asJSON, 0, out, errs)
	}
	return emit(checked(name, verb, o, f.Args(), env), asJSON, o.max, out, errs)
}

// reportDeliveryFlags are report's flags for a note, the same under both tools:
// a draft to read, or (with --send, or nova-version's send) a delivery.
func reportDeliveryFlags(f *flag.FlagSet, o *options) {
	f.StringVar(&o.host, "host", "", "a label for the machine the report ran on, carried in the note's subject")
	f.StringVar(&o.snapshot, "snapshot", "", "a state file that carries a prepared note across processes: retry the saved note, never prepare again while one is pending")
	f.BoolVar(&o.draft, "draft", false, "print the note that --send would deliver, and deliver nothing (needs --as, --to)")
	f.StringVar(&o.as, "as", "", "the sender the note is from")
	f.StringVar(&o.to, "to", "", "the recipients, comma-separated")
	f.StringVar(&o.bus, "bus", "", "the bus checkout that delivers the note")
	f.StringVar(&o.remote, "remote", "", "the bus remote")
	f.StringVar(&o.branch, "branch", "", "the bus branch")
}

// storeReport is `report --store` (#3880): every bench's nova-sprint build from
// its beat, so it takes no manifest, snapshot or note and never runs ssh.
func storeReport(name string, o options, positional []string, env Environment) *tool.Out {
	help := name + " report -h"
	if o.file != "" || o.snapshot != "" || o.draft || o.send || o.host != "" || len(positional) != 0 {
		return refused("report", help, "--store reads the bench beats and takes no --file, --snapshot, --host, --draft or --send (drop --store and give --file to report this machine)")
	}
	if o.timeout <= 0 {
		return refused("report", help, "--timeout wants a positive duration")
	}
	return fleetReport(o.store, help, o.timeout, env.Now)
}

// checked is a parsed check, apply or report: the flags' own rules, the
// manifest read, and the verb run. nova-version's report and send reach it
// through their verbs in versiontool.go with the flags already parsed.
func checked(name, verb string, o options, positional []string, env Environment) *tool.Out {
	help := name + " " + verb + " -h"
	// Every problem of the invocation is named in one refusal (STANDARD §2), so a
	// reader fixes the call once.
	var problems []string
	missing := []string{}
	if o.file == "" {
		missing = append(missing, "--file")
	}
	if o.draft || o.send {
		for _, x := range []struct{ n, v string }{{"as", o.as}, {"to", o.to}} {
			if x.v == "" {
				missing = append(missing, "--"+x.n)
			}
		}
	}
	if o.send {
		for _, x := range []struct{ n, v string }{{"bus", o.bus}, {"remote", o.remote}, {"branch", o.branch}} {
			if x.v == "" {
				missing = append(missing, "--"+x.n)
			}
		}
	}
	if len(missing) > 0 {
		problems = append(problems, "missing "+strings.Join(missing, ", ")+"; refusing to guess")
	}
	if o.max < 0 {
		problems = append(problems, fmt.Sprintf("--max wants 0 or more (0 shows all), got %d", o.max))
	}
	if o.timeout <= 0 || o.budget <= 0 {
		problems = append(problems, "--timeout and --budget want positive durations")
	}
	if verb == "apply" && len(positional) != 1 {
		problems = append(problems, fmt.Sprintf("apply wants exactly one entry name, got %d", len(positional)))
	} else if verb != "apply" && len(positional) != 0 {
		problems = append(problems, fmt.Sprintf("%s takes no positional arguments, got %q", verb, positional[0]))
	}
	if o.draft && o.send {
		problems = append(problems, "--draft and --send are exclusive (choose one)")
	}
	if (o.draft || o.send) && strings.ContainsAny(o.as+o.to+o.host, "\r\n") {
		problems = append(problems, "note header contains a newline (use a single-line --as, --to and --host)")
	}
	if len(problems) > 0 {
		return refused(verb, help, strings.Join(problems, "; "))
	}
	file, err := os.Open(o.file)
	if err != nil {
		return refused(verb, help, fmt.Sprintf("cannot open %s (supply a readable --file: %s; %s example --out %s writes one to start from)", o.file, manifestShape, name, o.file))
	}
	entries, err := Load(file)
	file.Close()
	if err != nil {
		return refused(verb, help, fmt.Sprintf("%s: %s", o.file, err))
	}
	if verb == "apply" {
		return apply(entries, positional[0], help, o, env)
	}
	// kinds= names the kinds the run read: those of the selected entries, never
	// a kind the file does not hold (ledger U13).
	selected := []Entry{}
	present := map[string]bool{}
	for _, e := range entries {
		if len(o.kinds) == 0 || contains(o.kinds, e.Kind) {
			selected = append(selected, e)
			present[e.Kind] = true
		}
	}
	var kinds []string
	for k := range present {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	started := env.Now()
	baseCtx := env.Context
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	if env.DrainTimer != nil {
		baseCtx = WithDrainTimer(baseCtx, env.DrainTimer)
	}
	ctx, cancel := context.WithTimeout(baseCtx, o.budget)
	defer cancel()
	if verb == "report" || verb == "send" {
		return report(ctx, verb, entries, selected, o, strings.Join(kinds, ","), help, started, env)
	}
	// status is check's read with every entry's item shown, current ones included
	// (SPEC-UPDATE rule 9): the same reads, the same exit, its own first token.
	results := readEntries(ctx, selected, o, env, false)
	counts := map[string]int{}
	pins := 0
	res := &tool.Out{Verb: verb, Status: tool.OK}
	for _, r := range results {
		status, ahead := verdict(r)
		counts[status]++
		if r.Entry.Kind == "pin" && status == "DIFFERENT" {
			pins++
		}
		if status == "EQUAL" && verb != "status" {
			continue
		}
		e := r.Entry
		switch status {
		case "UNKNOWN":
			x := r.Installed
			if x.Known() {
				x = r.Latest
			}
			res.Item("unknown", "name", e.Name, "kind", e.Kind, "installed", r.Installed.Version, "path", r.Installed.Path, "source", r.Latest.Source, "reason", x.Reason, "remedy", x.Remedy)
		case "AHEAD":
			res.Item("ahead", "name", e.Name, "kind", e.Kind, "installed", r.Installed.Version, "latest", r.Latest.Version, "ahead", ahead, "path", r.Installed.Path, "source", r.Latest.Source, "owner", e.Owner)
		default:
			res.Item(strings.ToLower(status), "name", e.Name, "kind", e.Kind, "installed", r.Installed.Version, "latest", r.Latest.Version, "path", r.Installed.Path, "source", r.Latest.Source, "owner", e.Owner)
		}
	}
	if counts["EQUAL"] != len(selected) {
		res.Status, res.Exit = tool.Failed, 1
	}
	return res.Fact("checked", len(selected)).Fact("current", counts["EQUAL"]).Fact("stale", counts["STALE"]).Fact("newer", counts["NEWER"]).
		Fact("ahead", counts["AHEAD"]).Fact("differ", counts["DIFFERENT"]).Fact("unknown", counts["UNKNOWN"]).Fact("pins", pins).
		Fact("took", env.Now().Sub(started).Round(time.Millisecond).String()).Fact("file", o.file).Fact("entries", len(entries)).
		Fact("kinds", strings.Join(kinds, ",")).Fact("at", started.UTC().Format(time.RFC3339)).
		Fact("timeout", o.timeout.String()).Fact("budget", o.budget.String()).Fact("max", o.max)
}
func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

type entryRead struct {
	Entry             Entry
	Installed, Latest Read
}

func readEntries(ctx context.Context, entries []Entry, o options, env Environment, report bool) []entryRead {
	rs := make([]entryRead, len(entries))
	jobs := make(chan int)
	var wg sync.WaitGroup
	var started sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		started.Add(1)
		go func(workerID int) {
			defer wg.Done()
			if env.WorkerStart != nil {
				env.WorkerStart(workerID)
			}
			started.Done()
			for i := range jobs {
				e := entries[i]
				r := entryRead{Entry: e, Installed: Installed(ctx, e, o.timeout, report), Latest: Read{Source: e.Latest}}
				if !report {
					r.Latest = Latest(ctx, e, o.timeout, env.Client)
				} else if strings.HasPrefix(e.Latest, "local:") {
					r.Latest = Latest(ctx, e, o.timeout, env.Client)
				}
				rs[i] = r
			}
		}(w)
	}
	started.Wait()
	for i := range entries {
		if env.JobAttempt != nil {
			env.JobAttempt(i)
		}
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].Entry.Kind == "pin" && rs[j].Entry.Kind != "pin" })
	return rs
}
func verdict(r entryRead) (string, string) {
	if !r.Installed.Known() || !r.Latest.Known() {
		return "UNKNOWN", ""
	}
	if r.Entry.Kind == "pin" {
		if r.Installed.Version != "" && r.Latest.Version == r.Installed.Version {
			return "EQUAL", ""
		}
		return "DIFFERENT", ""
	}
	if r.Entry.Kind == "model" {
		if r.Installed.Version == r.Latest.Version {
			return "EQUAL", ""
		}
		return "DIFFERENT", ""
	}
	if c, ok := ahead(r.Installed.Version, r.Latest.Version); ok {
		return "AHEAD", c
	}
	v := Compare(r.Installed.Version, r.Latest.Version)
	if v == "OLDER" {
		return "STALE", ""
	}
	return v, ""
}
func apply(entries []Entry, name, help string, o options, env Environment) *tool.Out {
	var e *Entry
	var names []string
	for i := range entries {
		names = append(names, entries[i].Name)
		if entries[i].Name == name {
			e = &entries[i]
		}
	}
	if e == nil {
		// The names the file holds are the choices, so the next call is a paste.
		if len(names) > manifestProblemCap {
			names = append(names[:manifestProblemCap], fmt.Sprintf("and %d more", len(names)-manifestProblemCap))
		}
		return refused("apply", help, fmt.Sprintf("name %s absent from %s; its %d entries are %s", name, o.file, len(entries), strings.Join(names, ", ")))
	}
	if e.Kind == "model" {
		return refused("apply", help, fmt.Sprintf("model %s is not installed by this tool (its owner %s pulls it: ollama pull %s)", name, e.Owner, name))
	}
	if len(e.Apply) == 0 {
		return refused("apply", help, fmt.Sprintf("%s is installed by hand: its apply column is none (its owner %s installs it, or write the install argv in that column)", name, e.Owner))
	}
	if o.target != "" && !strings.Contains(strings.Join(e.Apply, " "), "{version}") {
		return refused("apply", help, "this entry's apply does not take a version (remove --version or declare {version} in the manifest)")
	}
	started := env.Now()
	target := o.target
	if target == "" {
		r := Latest(context.Background(), *e, o.timeout, env.Client)
		if !r.Known() {
			return refused("apply", help, fmt.Sprintf("latest unknown for %s (pass --version <v>, or ask again when the source answers)", name))
		}
		target = r.Version
	} else {
		var err error
		target, err = versionKey(target)
		if err != nil {
			return refused("apply", help, fmt.Sprintf("invalid target %q (pass a complete version with --version, such as 1.2.3)", o.target))
		}
	}
	before := Installed(context.Background(), *e, o.timeout, false)
	args := append([]string(nil), e.Apply...)
	for i := range args {
		args[i] = strings.ReplaceAll(args[i], "{version}", target)
	}
	// --dry-run is the plan this function is about to take, printed and not taken
	// (SPEC-UPDATE rule 13): every refusal above has passed, the target and the argv
	// are the ones below, and no process starts.
	if o.dryRun {
		return applyDryRun(*e, before, target, args)
	}
	res := &tool.Out{Verb: "apply", Status: tool.OK}
	res.Item("before", "name", name, "kind", e.Kind, "installed", before.Version, "path", before.Path, "latest", target, "source", e.Latest)
	res.Item("run", "name", name, "argv", len(args), "version", target, "command", strings.Join(args, " "))
	ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
	p := process(ctx, args, nil, ChildCap)
	cancel()
	after := Installed(context.Background(), *e, o.timeout, false)
	res.Item("after", "name", name, "installed", after.Version, "was", before.Version)
	reason := p.Reason
	if reason == "" && !after.Known() {
		reason = after.Reason
	}
	if reason == "" && after.Version != target {
		reason = "installed " + after.Version + ", asked " + target
	}
	if reason != "" {
		res.Status, res.Exit, res.Why = tool.Failed, 1, []string{reason}
	}
	return res.Fact("name", name).Fact("from", before.Version).Fact("to", after.Version).Fact("took", env.Now().Sub(started).Round(time.Millisecond).String())
}

// applyDryRun is what `apply` would do to one entry, done to none of it: the entry's
// item as `status` shows it (installed against the target the real run would
// install), and the plan, the argv the real run's RUN item carries. No process starts
// and nothing is written; it exits 0, the plan having been made (SPEC-UPDATE rule 13,
// `--dry-run`).
func applyDryRun(e Entry, before Read, target string, argv []string) *tool.Out {
	r := entryRead{Entry: e, Installed: before, Latest: Read{Version: target, Source: e.Latest}}
	state, _ := verdict(r)
	res := &tool.Out{Verb: "apply", Status: tool.OK}
	res.Fact("name", e.Name).Fact("dry_run", true).Fact("from", before.Version).Fact("to", target).Fact("source", e.Latest)
	if before.Known() {
		res.Item(strings.ToLower(state), "name", e.Name, "kind", e.Kind, "installed", before.Version, "latest", target, "path", before.Path, "source", e.Latest, "owner", e.Owner)
	} else {
		res.Item("unknown", "name", e.Name, "kind", e.Kind, "installed", "", "path", before.Path, "source", e.Latest, "reason", before.Reason, "remedy", before.Remedy)
	}
	res.Item("plan", "name", e.Name, "argv", len(argv), "version", target, "command", strings.Join(argv, " "))
	return res.Note("dry run: nothing installed, nothing written")
}

// movedChildTimeout is the default deadline one child of `moved` gets, and
// `--timeout` is how a caller changes it. It is snapshot's thirty seconds, not
// report's five, for the same measured reason (#890): every binary this verb
// reads is one it built a moment ago, so the platform's one-time assessment of
// a never-seen executable is charged to the first exec of every tool at every
// revision. A five-second bound here refused healthy builds and sent the reader
// to repair a revision that was fine.
var movedChildTimeout = 30 * time.Second

// movedBudget bounds the whole run -- two checkouts, two builds and every
// help -- as the same per-child/per-run pair `check`, `report`, `watch` and
// `snapshot` already take.
var movedBudget = 60 * time.Second

// movedVerb is SPEC-VERSION's TOOLS MOVED note (#2288): it compares two
// revisions by BUILDING both and reading what each build's own `help` prints,
// never a hand-written list. The hurt it removes is the ADOPT EVERYTHING note,
// which named four `--decide` flags that were still on open PRs (#1141): a list
// a person wrote can announce a flag no binary ever answered, and a reader
// cannot tell that from a reading. Here every announced verb and flag was
// parsed off a `<tool> help` this run executed, so a flag on no binary's help
// cannot be announced (SPEC-VERSION rules 2 and 10). A tool that vanishes is
// deleted and one that appears is added; a rename is counted only when a commit
// message or a MOVED file states it and the builds confirm it (rule 2); an
// empty diff is three zeros, exit 0, never a refusal (rule 3).
func movedVerb(c *tool.Call, env Environment) *tool.Out {
	// Read once at entry, so a refusal on the way keeps its own reason: the
	// skeleton fails a --dry-run call whose verb never read it.
	dryRun := c.DryRun()
	started := env.Now()
	from, to, repo, outPath := c.Str("from"), c.Str("to"), c.Str("repo"), c.Str("out")
	timeout, budget := c.Dur("timeout"), c.Dur("budget")
	// One deadline per child and one for the run: every git, every go build
	// and every help hangs off both, through the same bounded process
	// machinery -- internal/bounded's capture -- the rest of this package
	// already runs its children through.
	run, cancelRun := context.WithTimeout(context.Background(), budget)
	defer cancelRun()
	runChild := func(argv []string) ProcessResult {
		ctx, cancel := context.WithTimeout(run, timeout)
		p := process(ctx, argv, nil, ChildCap)
		cancel()
		return p
	}
	// Both revisions resolve in --repo alone, before anything is built or
	// written (SPEC-VERSION rule 1: never the cwd, never origin/HEAD). A
	// revision that is not a commit names the git fetch that would bring it;
	// the fetch itself is the caller's, because this verb reaches for no
	// network of its own (rule 12).
	// A --repo that is no checkout is named as such first: a revision cannot be a
	// commit there, and a fetch would not help.
	if p := runChild([]string{"git", "-C", repo, "rev-parse", "--git-dir"}); strings.HasPrefix(p.Reason, "exit ") {
		return tool.Refuse(fmt.Sprintf("--repo %s is not a git checkout (name the checkout holding both revisions)", repo))
	}
	resolve := func(rev string) (string, error) {
		p := runChild([]string{"git", "-C", repo, "rev-parse", "--verify", rev + "^{commit}"})
		if p.Reason != "" {
			// git answered and said no: the revision is not a commit here.
			// Anything else -- git missing, not executable, past the deadline
			// -- is not a fact about the revision, and naming the fetch would
			// send the reader to repair a checkout that is fine.
			if strings.HasPrefix(p.Reason, "exit ") {
				return "", fmt.Errorf("revision %s is not a commit in %s (run git fetch to bring it, or name a revision this checkout holds)", rev, repo)
			}
			return "", fmt.Errorf("cannot run git against %s (%s) (supply a --repo and an environment where git answers)", repo, oneline.Escape(p.Reason))
		}
		return strings.TrimSpace(strings.SplitN(p.Stdout, "\n", 2)[0]), nil
	}
	fromSha, err := resolve(from)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	toSha, err := resolve(to)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	// A rename is stated, never inferred: the commits between the two
	// revisions and a MOVED file at --to are the only sources, and a
	// statement counts only when the builds confirm it.
	stated := map[string]string{}
	readStated := func(text string) {
		for _, line := range strings.Split(text, "\n") {
			f := strings.Fields(strings.ToLower(line))
			if len(f) == 4 && f[0] == "renamed" && f[2] == "to" {
				stated[f[1]] = f[3]
			}
		}
	}
	if p := runChild([]string{"git", "-C", repo, "log", "--format=%B", fromSha + ".." + toSha}); p.Reason != "" {
		return tool.Refuse(fmt.Sprintf("cannot read the commits between %s and %s in %s (%s)", fromSha, toSha, repo, oneline.Escape(p.Reason)))
	} else {
		readStated(p.Stdout)
	}
	if p := runChild([]string{"git", "-C", repo, "show", toSha + ":MOVED"}); p.Reason == "" {
		readStated(p.Stdout)
	}
	// Each revision's inventory comes from its own build: cmd/* at the
	// revision, checked out into a worktree of that revision, built there,
	// and each built binary asked its help. Nothing here reads a list any
	// person wrote.
	stage, err := os.MkdirTemp("", "nova-version-moved-")
	if err != nil {
		return tool.Refuse(fmt.Sprintf("cannot create a staging directory (%s)", oneline.Escape(err.Error())))
	}
	// The staging tree comes down by the names it went up with, through
	// os.Remove alone: the removeall class rule allows no os.RemoveAll of a
	// computed path, and every path below stage is one this function created
	// and knows -- the built binaries, the per-revision build directories,
	// and stage itself, last.
	var staged []string
	defer func() {
		for i := len(staged) - 1; i >= 0; i-- {
			os.Remove(staged[i])
		}
		os.Remove(stage)
	}()
	inventory := func(rev string) (movedInv, error) {
		p := runChild([]string{"git", "-C", repo, "ls-tree", "-d", "--name-only", rev + ":cmd"})
		if p.Reason != "" {
			return nil, fmt.Errorf("cannot list cmd/* at %s in %s (%s) (supply a --repo whose %s revision holds a cmd directory)", rev, repo, oneline.Escape(p.Reason), rev)
		}
		var tools []string
		for _, line := range strings.Split(p.Stdout, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				tools = append(tools, line)
			}
		}
		work := filepath.Join(stage, "wt-"+safeRevision(rev))
		if p = runChild([]string{"git", "-C", repo, "worktree", "add", "--detach", work, rev}); p.Reason != "" {
			return nil, fmt.Errorf("cannot check out %s into a worktree (%s) (supply a --repo this revision resolves in)", rev, oneline.Escape(p.Reason))
		}
		// The worktree is git's own tree, so git takes it down; anything it
		// leaves behind stays behind rather than being removed by hand.
		// ignored: git takes its own worktree down (see the comment above); anything left stays in the scratch root
		defer func() { _ = runChild([]string{"git", "-C", repo, "worktree", "remove", "--force", work}) }()
		// The build directory is named for the revision, so the two builds
		// cannot collide and the set each revision produced sits in one
		// place to be asked its help.
		built := filepath.Join(stage, safeRevision(rev))
		if err := os.MkdirAll(built, 0o755); err != nil {
			return nil, fmt.Errorf("cannot create a build directory (%s)", oneline.Escape(err.Error()))
		}
		staged = append(staged, built)
		if p = runChild([]string{"go", "build", "-C", work, "-o", built, "./cmd/..."}); p.Reason != "" {
			return nil, fmt.Errorf("cannot build ./cmd/... at %s (%s) (repair the package at that revision)", rev, oneline.Escape(p.Reason))
		}
		inv := movedInv{}
		for _, cmd := range tools {
			bin := filepath.Join(built, cmd)
			staged = append(staged, bin)
			p := runChild([]string{bin, "help"})
			what := "it printed no help"
			if p.Reason != "" {
				what = p.Reason
				// A child killed because the RUN ran out reports `timeout`
				// like any other, since all it can see is its own cancelled
				// context; the run's context is the one that knows which
				// bound was spent, so a spent budget is never reported as a
				// slow binary.
				if run.Err() != nil {
					what = "the run's " + budget.String() + " budget was spent before " + cmd + " was read"
				} else if p.Reason == "timeout" {
					what = "timeout after " + timeout.String()
				}
			}
			if p.Reason != "" || strings.TrimSpace(p.Stdout) == "" {
				// SPEC-VERSION rule 4: a cmd/* that builds but answers no
				// help names the cmd, the revision and the build to repair
				// there.
				return nil, fmt.Errorf("cannot read %s help at %s (%s) (repair the build there: go build ./cmd/%s, or raise --timeout)", cmd, rev, what, cmd)
			}
			inv[cmd] = parseMovedHelp(cmd, p.Stdout)
		}
		return inv, nil
	}
	before, err := inventory(fromSha)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	after := before
	if toSha != fromSha {
		if after, err = inventory(toSha); err != nil {
			return tool.Refuse(err.Error())
		}
	}
	entries, counts := diffMoved(before, after, stated)
	var note strings.Builder
	fmt.Fprintf(&note, "MOVED from=%s to=%s at=%s\n", field(fromSha), field(toSha), field(started.UTC().Format(time.RFC3339)))
	for _, e := range entries {
		fmt.Fprintln(&note, e)
	}
	// The one line, every field named (SPEC-VERSION rule 3): added, deleted
	// and renamed count tools, and verbs counts the (tool, verb) pairs the
	// --to build answers -- the size of the surface the note describes.
	o := tool.Done().Fact("from", fromSha).Fact("to", toSha).Fact("added", counts.added).Fact("deleted", counts.deleted).
		Fact("renamed", counts.renamed).Fact("verbs", counts.verbs).Fact("file", outPath)
	// --dry-run is the same builds and reads with the note printed, not written.
	if dryRun { // the skeleton adds dry_run=true
		o.Payload = note.String()
		return o
	}
	if err := os.WriteFile(outPath, []byte(note.String()), 0o644); err != nil {
		return tool.Refuse(fmt.Sprintf("cannot write --out %s (supply a writable --out path)", outPath))
	}
	return o
}

// movedInv is one revision's inventory as its own builds reported it: every
// cmd/* tool the revision builds, and for each, the verbs and flags its `help`
// printed.
type movedInv map[string]map[string]map[string]bool

// parseMovedHelp reads one built tool's `help` into verbs and flags. Only lines
// that begin with the tool's own name count, after any indent (a tool on
// internal/tool indents its usage lines): a help that prints another tool's
// usage line (SPEC-VERSION's block names nova-update's in nova-version's help)
// cannot add that tool to THIS revision's inventory, and a line that is not a
// usage line -- the defaults, the notes, the examples -- contributes nothing.
// This function is the whole of "never a hand-written list" (#2288): whatever
// these lines do not print, the note cannot announce.
func parseMovedHelp(tool, help string) map[string]map[string]bool {
	verbs := map[string]map[string]bool{}
	for _, line := range strings.Split(help, "\n") {
		if line = strings.TrimSpace(line); !strings.HasPrefix(line, tool+" ") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 || strings.HasPrefix(f[1], "-") {
			continue
		}
		if verbs[f[1]] == nil {
			verbs[f[1]] = map[string]bool{}
		}
		for _, tok := range f[2:] {
			tok = strings.TrimLeft(tok, "[(")
			if !strings.HasPrefix(tok, "--") {
				continue
			}
			tok = strings.TrimRight(tok, "),.;:]")
			if pre, _, ok := strings.Cut(tok, "="); ok {
				tok = pre
			}
			verbs[f[1]][tok] = true
		}
	}
	return verbs
}

// diffMoved compares the two inventories and returns the note's entry lines
// and the counts the MOVED OK line prints. A tool that vanishes is deleted and
// one that appears is added; a rename is counted only when it was stated (in a
// commit message or a MOVED file) AND the inventories confirm it -- the old
// name built only at --from, the new name only at --to -- so help text alone,
// however identical, never makes a rename (SPEC-VERSION rule 2). A confirmed
// rename consumes its pair: the statement, not a guess, is what moved the tool.
func diffMoved(before, after movedInv, stated map[string]string) (entries []string, counts struct{ added, deleted, renamed, verbs int }) {
	consumed := map[string]bool{}
	var renames [][2]string
	for old, name := range stated {
		_, oldBuiltBefore := before[old]
		_, oldBuiltAfter := after[old]
		_, newBuiltBefore := before[name]
		_, newBuiltAfter := after[name]
		if oldBuiltBefore && !oldBuiltAfter && newBuiltAfter && !newBuiltBefore {
			renames = append(renames, [2]string{old, name})
			consumed[old], consumed[name] = true, true
		}
	}
	sort.Slice(renames, func(i, j int) bool { return renames[i][0] < renames[j][0] })
	for _, r := range renames {
		entries = append(entries, "renamed="+r[0]+"->"+r[1])
		counts.renamed++
	}
	afterTools := make([]string, 0, len(after))
	for tool := range after {
		afterTools = append(afterTools, tool)
	}
	sort.Strings(afterTools)
	beforeTools := make([]string, 0, len(before))
	for tool := range before {
		beforeTools = append(beforeTools, tool)
	}
	sort.Strings(beforeTools)
	for _, tool := range afterTools {
		counts.verbs += len(after[tool])
		if _, ok := before[tool]; ok || consumed[tool] {
			continue
		}
		entries = append(entries, "added="+tool)
		counts.added++
	}
	for _, tool := range beforeTools {
		if _, ok := after[tool]; ok || consumed[tool] {
			continue
		}
		entries = append(entries, "deleted="+tool)
		counts.deleted++
	}
	// Verb and flag entries for the tools the two revisions share, and for
	// each confirmed rename, the pair the statement joined.
	pairs := map[string]string{}
	for _, tool := range afterTools {
		if _, ok := before[tool]; ok {
			pairs[tool] = tool
		}
	}
	for _, r := range renames {
		pairs[r[1]] = r[0]
	}
	for _, tool := range afterTools {
		old, ok := pairs[tool]
		if !ok {
			continue
		}
		beforeVerbs, afterVerbs := before[old], after[tool]
		names := map[string]bool{}
		for v := range beforeVerbs {
			names[v] = true
		}
		for v := range afterVerbs {
			names[v] = true
		}
		sorted := make([]string, 0, len(names))
		for v := range names {
			sorted = append(sorted, v)
		}
		sort.Strings(sorted)
		for _, v := range sorted {
			_, hadBefore := beforeVerbs[v]
			_, hasAfter := afterVerbs[v]
			switch {
			case hasAfter && !hadBefore:
				entries = append(entries, "added="+v+" tool="+tool)
			case hadBefore && !hasAfter:
				entries = append(entries, "deleted="+v+" tool="+tool)
			}
			if !hadBefore || !hasAfter {
				continue
			}
			flags := map[string]bool{}
			for f := range beforeVerbs[v] {
				flags[f] = true
			}
			for f := range afterVerbs[v] {
				flags[f] = true
			}
			flagNames := make([]string, 0, len(flags))
			for f := range flags {
				flagNames = append(flagNames, f)
			}
			sort.Strings(flagNames)
			for _, f := range flagNames {
				_, hadFlagBefore := beforeVerbs[v][f]
				_, hasFlagAfter := afterVerbs[v][f]
				switch {
				case hasFlagAfter && !hadFlagBefore:
					entries = append(entries, "added="+f+" tool="+tool+" verb="+v)
				case hadFlagBefore && !hasFlagAfter:
					entries = append(entries, "deleted="+f+" tool="+tool+" verb="+v)
				}
			}
		}
	}
	return entries, counts
}

// safeRevision makes a revision usable as one directory name under the staging
// root: the hex a real `git rev-parse` answers needs no change, and anything
// else a caller hands the flag is folded to the characters a single path
// segment can hold. "." and ".." are refused as names outright -- a staging
// subdir that climbed out of the staging root is how a temp dir becomes a
// deletion.
func safeRevision(rev string) string {
	var b strings.Builder
	for _, r := range rev {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if s := b.String(); s != "" && s != "." && s != ".." {
		return s
	}
	return "revision"
}

// Kept as a narrow seam for command tests and bus delivery; no shell is involved.
func captureRun(ctx context.Context, args []string, input []byte, cap int) ProcessResult {
	return process(ctx, args, bytes.NewReader(input), cap)
}
