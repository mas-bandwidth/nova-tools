package update

import (
	"context"
	"flag"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/release"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// Environment supplies deterministic clock/network seams. Nil values use the
// machine clock and a credential-free, redirect-bounded HTTP client.
type Environment struct {
	Process processFunc
	// Env is the environment a spawned child gets, and the PATH its name is
	// looked up on. Nil inherits this process's own.
	Env         []string
	Now         func() time.Time
	Client      *http.Client
	Context     context.Context
	WorkerStart func(id int)
	JobAttempt  func(index int)
	DrainTimer  func(time.Duration) (<-chan time.Time, func() bool)
	// Rename commits a snapshot; nil is os.Rename.
	Rename func(oldPath, newPath string) error
	// OpenStore dials the fleet store for report --store; nil is store.Open,
	// which authenticates from the process environment.
	OpenStore func(ctx context.Context, addr string) (*store.Store, error)
}

// rename is the snapshot commit operation this environment uses.
func (env Environment) rename() func(oldPath, newPath string) error {
	if env.Rename != nil {
		return env.Rename
	}
	return os.Rename
}

type options struct {
	file, host, snapshot, as, to, target, adopt, store string
	max                                                int
	timeout, budget                                    time.Duration
	kinds                                              kindFlags
	draft, send, dryRun                                bool
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
// input wants, then the command a reader runs next. A write error folds into
// the code the caller returns: 1 when the refusal carries no error of its own,
// the usage code 2 otherwise, as when the line was printed whole.
func refusal(w io.Writer, token, run string, err error) int {
	if _, e := fmt.Fprintf(w, "%s REFUSED: %s; run: %s\n", token, oneline.Err(err), run); e != nil && err == nil {
		return 1
	}
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
// the spec file and compares the two. The lines are indented two spaces and
// wrapped at 100 columns with a deeper continuation, as nova-ci's usage is, so
// the report line's synopsis is not one 270-character line. nova-version's usage
// lines are its verbs' own (versiontool.go). The release verbs are one line
// here; their own lines are release.Verbs, printed by `nova-update help release`.
const updateVerbs = `usage:
  nova-update example [--out <path>]
  nova-update check --file <path> [--max <n>] [--timeout <d>] [--budget <d>] [--kind <k>]
  nova-update status --file <path> [--max <n>] [--timeout <d>] [--budget <d>] [--kind <k>]
  nova-update apply --file <path> <name> [--version <v>] [--dry-run] [--timeout <d>]
  nova-update report --file <path> [--host <label>] [--snapshot <path>] [--draft --as <friend> --to
    <who,who> | --send --as <friend> --to <who,who>]
    [--max <n>] [--timeout <d>] [--budget <d>] [--kind <k>]
  nova-update report --store <host:port> [--timeout <d>]
  nova-update watch --adopt <checks.tsv> [--as <friend> --to <who,who>] [--host <label>]
    [--timeout <d>] [--budget <d>]
  nova-update adoption --file <path> [--as <friend>] [--max <n>]
  nova-update release <cut|build|install|adopt|pull> ...
    nova-tools' own release pipeline: nova-update help release prints its usage lines
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
	_ = help(name, &b) // ignored: a strings.Builder's writes never fail
	return b.String()
}

func help(name string, w io.Writer) int {
	// SPEC-UPDATE's "The verbs" block says these lines are what help prints,
	// BYTE FOR BYTE, and names one string in the binary as the reason the spec
	// and the help cannot drift apart. This is that string.
	if _, err := fmt.Fprintf(w, "%s\n\n", updateOpening); err != nil {
		return 1
	}
	if _, err := fmt.Fprintln(w, updateVerbs); err != nil {
		return 1
	}
	// The notes are one short paragraph per subject (a cold rating named the
	// wall of text): the defaults, the --json rendering, the verbs' shape,
	// then report's delivery and the snapshot's lock (SPEC-UPDATE rule 25).
	if _, err := fmt.Fprintf(w, "  %s version (or --version)\n", name); err != nil {
		return 1
	}
	if _, err := fmt.Fprintf(w, "\nAdoption states: evaluated, useful-now, tried, adopted, declined, deferred, unknown, equivalent.\n"); err != nil {
		return 1
	}
	if _, err := fmt.Fprintf(w, "\nDefaults: --max 20 (0 = all), --timeout 5s, --budget 60s. Repeat --kind to select kinds.\n"); err != nil {
		return 1
	}
	if _, err := fmt.Fprintf(w, "\nEvery verb but watch and release takes --json: the same result as one JSON object on stdout. A result's first line is the verb, OK, FAIL or REFUSED, and the run's counts; `<verb> -h` lists a verb's flags and effect.\n"); err != nil {
		return 1
	}
	if _, err := fmt.Fprintf(w, "\nReport needs no bus or network. Updates require an explicit apply name. status is check with every entry shown, current ones too. apply --dry-run prints the plan and writes nothing.\n"); err != nil {
		return 1
	}
	if _, err := fmt.Fprintf(w, "\nA delivery is one nova-bus send on the Redis bus (nova-bus reads its store from NOVA_BUS_REDIS); with --snapshot, a report unchanged since it was confirmed sent to the same recipients is not sent again.\n"); err != nil {
		return 1
	}
	if _, err := fmt.Fprintf(w, "\nA snapshot uses a sibling .lock file for a kernel lock; its presence never means a process is running.\n"); err != nil {
		return 1
	}
	if _, err := fmt.Fprintf(w, "\nLocals: latest=local:<path> runs that binary (or argv) on this host to read the version; e.g., local:/usr/local/bin/nova-update or local:go version. The installed column can be a version string (v1.2.3), a single command name found on PATH, or a full argv.\n"); err != nil {
		return 1
	}
	if _, err := fmt.Fprint(w, twoBinaries()); err != nil {
		return 1
	}
	if _, err := fmt.Fprint(w, manifestHelp(name)); err != nil {
		return 1
	}
	if _, err := fmt.Fprintf(w, "\n%s\n\nexample:\n", exitCodes(name)); err != nil {
		return 1
	}
	for _, line := range []string{"example --out versions.tsv", "report --file versions.tsv", "status --file versions.tsv", "apply --file versions.tsv go --dry-run", "version"} {
		if _, err := fmt.Fprintf(w, "  %s %s\n", name, line); err != nil {
			return 1
		}
	}
	return 0
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
// beneath them): the rule-2 file --file names, the same for both tools. Rule 5 is
// worded from rule 3 (a command is argv, never a shell): the apply argv is split on
// single spaces, no quotes, no shell, so a pipe, a glob or a $VAR is a literal argument.
func manifestHelp(name string) string {
	return "\nTHE MANIFEST is the file --file names, written by hand, the same for both tools:\n" +
		"  " + name + " report --file versions.tsv     the six lines that say what versions.tsv holds:\n" +
		"      1. line 1 is the header, byte for byte: " + tabbed(Header) + "; every other line is six fields, one tab between, none empty; a line starting # is a comment\n" +
		"      2. kind is harness, engine, model, tool or pin; name is unique in the file; owner is who answers for it\n" +
		"      3. installed is a version (v1.2.3), a command name on PATH, or an argv whose first line of output carries the version (single spaces, no quotes)\n" +
		"      4. latest is github:<owner>/<repo>, npm:<package>, brew:<formula>, ollama:<model>:<tag> (kind model), local:<argv> (a pin takes this only), or - for not known yet\n" +
		"      5. apply is the argv that updates it, or none, split on single spaces, no quotes, no shell: a pipe, a glob or a $VAR is a literal argument; a run prints EVERY problem of the file at once, each with its line, never the first alone\n" +
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
		"watch":    "inspection: runs each check's command; with --as and --to, delivery: the receipt goes out through nova-bus send",
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
	// before any manifest, bus or store is read (the CLI style's rule (b)).
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
		return help(name, out)
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
		f.BoolVar(&o.send, "send", false, "deliver the note through nova-bus (needs --as, --to)")
		f.StringVar(&o.store, "store", "", "a fleet Redis host:port: report every bench's nova-sprint build from its beat, instead of --file")
	}
	if err := verbflag.Parse(f, interspersed(f, args)); err != nil {
		// `<tool> <verb> --help` never lands here: verbflag.Parse raises that
		// verb's help, which Run prints on stdout at exit 0 (asking is not an
		// error).
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
	f.StringVar(&o.snapshot, "snapshot", "", "a state file that records what was observed and what each recipient was confirmed sent: an unchanged report is not sent again")
	f.BoolVar(&o.draft, "draft", false, "print the note that --send would deliver, and deliver nothing (needs --as, --to)")
	f.StringVar(&o.as, "as", "", "the sender the note is from")
	f.StringVar(&o.to, "to", "", "the recipients, comma-separated")
}

// storeReport is `report --store`: every bench's nova-sprint build from
// its beat, so it takes no manifest, snapshot or note and never runs ssh.
func storeReport(name string, o options, positional []string, env Environment) *tool.Out {
	help := name + " report -h"
	if o.file != "" || o.snapshot != "" || o.draft || o.send || o.host != "" || len(positional) != 0 {
		return refused("report", help, "--store reads the bench beats and takes no --file, --snapshot, --host, --draft or --send (drop --store and give --file to report this machine)")
	}
	if o.timeout <= 0 {
		return refused("report", help, "--timeout wants a positive duration")
	}
	return fleetReport(o.store, help, o.timeout, env)
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
	_ = file.Close() // ignored: file was opened only to be read
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
		if len(o.kinds) == 0 || slices.Contains(o.kinds, e.Kind) {
			selected = append(selected, e)
			present[e.Kind] = true
		}
	}
	kinds := slices.Sorted(maps.Keys(present))
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
				r := entryRead{Entry: e, Installed: installed(ctx, e, o.timeout, report, env.runProcess), Latest: Read{Source: e.Latest}}
				if !report {
					r.Latest = latestIn(ctx, env.Env, e, o.timeout, env.Client)
				} else if strings.HasPrefix(e.Latest, "local:") {
					r.Latest = latestIn(ctx, env.Env, e, o.timeout, env.Client)
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
		r := latestIn(context.Background(), env.Env, *e, o.timeout, env.Client)
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
	before := installed(context.Background(), *e, o.timeout, false, env.runProcess)
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
	p := process(ctx, env.Env, args, nil, ChildCap)
	cancel()
	after := installed(context.Background(), *e, o.timeout, false, env.runProcess)
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
		res.Item(strings.ToLower(state), "name", e.Name, "kind", e.Kind, "installed", before.Version, "target", target, "source", e.Latest, "path", before.Path, "owner", e.Owner)
	} else {
		res.Item("unknown", "name", e.Name, "kind", e.Kind, "installed", "", "target", target, "source", e.Latest, "path", before.Path, "reason", before.Reason, "remedy", before.Remedy)
	}
	res.Item("plan", "name", e.Name, "argv", len(argv), "version", target, "command", strings.Join(argv, " "))
	return res.Note("dry run: nothing installed, nothing written")
}
