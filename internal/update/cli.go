package update

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/release"
	"github.com/mas-bandwidth/nova-tools/internal/wake"
)

// Environment supplies deterministic clock/network seams. Nil values use the
// machine clock and a credential-free, redirect-bounded HTTP client.
type Environment struct {
	Now    func() time.Time
	Client *http.Client
}
type options struct {
	file, host, snapshot, as, to, bus, remote, branch, target, adopt string
	max                                                              int
	timeout, budget                                                  time.Duration
	kinds                                                            kindFlags
	draft, send                                                      bool
}
type kindFlags []string

func (k *kindFlags) String() string { return strings.Join(*k, ",") }
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
func refusal(w io.Writer, token string, err error) int {
	fmt.Fprintf(w, "%s REFUSED: %s\n", token, oneline.Err(err))
	return 2
}

// updateVerbs and versionVerbs are SPEC-UPDATE's verbs block, byte for byte,
// including its placeholder spellings: <k> not <kind>, <v> not <version>,
// <who,who> not <recipients>, <r> and <b> for the remote and the branch, and the
// report line's alternation showing that --send is the one that needs a bus. A
// change here belongs in the spec first, and TestHelpIsTheSpecsVerbsBlock reads
// the spec file and compares the two. nova-version's moved line is SPEC-
// VERSION's own block, byte for byte, which the spec carried before the verb
// existed (#2288).
const updateVerbs = `nova-update check --file <path> [--max <n>] [--timeout <d>] [--budget <d>] [--kind <k>]
nova-update apply --file <path> <name> [--version <v>] [--timeout <d>]
nova-update report --file <path> [--host <label>] [--snapshot <path>] [--draft --as <friend> --to <who,who> | --send --as <friend> --to <who,who> --bus <path> --remote <r> --branch <b>] [--max <n>] [--timeout <d>] [--budget <d>] [--kind <k>]
nova-update watch --adopt <checks.tsv> [--bus <path> --remote <r> --branch <b> --as <friend> --to <who,who>] [--host <label>] [--timeout <d>] [--budget <d>]
nova-update adoption --file <path> [--as <friend>] [--max <n>]
` + release.Verbs + `
nova-update help`

// manifestShape is the one sentence that says what the file --file names holds:
// the rule-2 manifest, one tab-separated line per tool, written by hand in git.
// The usage line and the refusal on a missing file both carry it, so neither
// reads as if --file were an output. No verb writes the file, so no verb is
// named; the spec carries the same shape once (SPEC-UPDATE rule 2).
const manifestShape = "one line per tool, six tab-separated fields name kind installed latest apply owner, written by hand"

const versionVerbs = `nova-version moved --from <sha> --to <sha> --repo <dir> --out <path>
nova-version snapshot --file <manifest: ` + manifestShape + `>
nova-version snapshot --bin <dir> --out <file.tsv> [--timeout <d>] [--budget <d>]
nova-version diff --from <a.tsv> --to <b.tsv>
nova-version report --file <manifest: ` + manifestShape + `> [--host <label>] [--snapshot <path>] [--draft --as <friend> --to <who,who>] [--max <n>] [--timeout <d>] [--budget <d>] [--kind <k>]
nova-version send --file <manifest: ` + manifestShape + `> --as <friend> --to <who,who> --bus <path> --remote <r> --branch <b> [--snapshot <path>] [--host <label>]
nova-version help`

func help(name string, w io.Writer) {
	// SPEC-UPDATE's "The verbs" block says these lines are what help prints,
	// BYTE FOR BYTE, and names one string in the binary as the reason the spec
	// and the help cannot drift apart. This is that string.
	if name == "nova-version" {
		fmt.Fprintln(w, versionVerbs)
	} else {
		fmt.Fprintln(w, updateVerbs)
	}
	fmt.Fprintf(w, "%s version (or --version)\nDefaults: --max 20 (0 = all), --timeout 5s, --budget 60s; snapshot's --timeout is 30s, because the first run of a newly installed binary is assessed by the platform and that cost is charged to the deadline. Repeat --kind to select kinds.\n", name)
	note := "Report needs no bus or network. "
	if name != "nova-version" {
		note += "Updates require an explicit apply name. "
	}
	note += "Cross-process delivery recovery needs --snapshot; without it, each send is a new intention. Do not prepare again while pending; retry the saved artifact. A snapshot uses a sibling .lock file for a kernel lock; its presence never means a process is running."
	fmt.Fprintln(w, note)
	fmt.Fprintf(w, "\nLocals: latest=local:<path> runs that binary (or argv) on this host to read the version; e.g., local:/usr/local/bin/nova-update or local:go version. The installed column can be a version string (v1.2.3), a single command name found on PATH, or a full argv.\n")
	fmt.Fprintf(w, "\nFrom a nova-tools checkout:\nexample:\n  %s report --file cmd/%s/testdata/example.tsv\n  %s version\n", name, name, name)
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
func Main(name string, args []string, stamp string, out, errs io.Writer) int {
	return Run(name, args, stamp, out, errs, Environment{})
}
func Run(name string, args []string, stamp string, out, errs io.Writer, env Environment) int {
	if env.Now == nil {
		env.Now = time.Now
	}
	tool := "UPDATE"
	if name == "nova-version" {
		tool = "VERSION"
	}
	if len(args) == 0 {
		return refusal(errs, tool, fmt.Errorf("a verb is required (run: %s help)", name))
	}
	verb := args[0]
	args = args[1:]
	if verb == "help" || verb == "--help" || verb == "-h" {
		if len(args) != 0 {
			return refusal(errs, tool, fmt.Errorf("help takes no arguments (run %s help)", name))
		}
		help(name, out)
		return 0
	}
	if verb == "version" || verb == "--version" {
		if len(args) != 0 {
			return refusal(errs, tool, fmt.Errorf("version takes no arguments (run %s version)", name))
		}
		fmt.Fprintln(out, buildinfo.Line(name, stamp))
		return 0
	}
	if verb == "snapshot" {
		if name != "nova-version" {
			return refusal(errs, tool, fmt.Errorf("unknown verb (run %s help)", name))
		}
		return snapshotVerb(name, args, out, errs, env)
	}
	if verb == "diff" {
		if name != "nova-version" {
			return refusal(errs, tool, fmt.Errorf("unknown verb (run %s help)", name))
		}
		return diffVerb(name, args, out, errs)
	}
	// `moved` writes the TOOLS MOVED note from two revisions' own builds
	// (SPEC-VERSION; #2288). It is nova-version's, like snapshot and diff:
	// the note compares revisions of the whole cmd/* set, which is the
	// question this binary exists to answer.
	if verb == "moved" {
		if name != "nova-version" {
			return refusal(errs, tool, fmt.Errorf("unknown verb (run %s help)", name))
		}
		return movedVerb(name, args, out, errs, env)
	}
	impliedSend := name == "nova-version" && verb == "send"
	if impliedSend {
		verb = "report"
	}
	// `release` is the last mile -- cut, build, install, adopt -- and it is a
	// verb of nova-update rather than a tool of its own because it is the same
	// question this binary already answers (what is installed here, and is it
	// what it should be) asked from the other end. internal/release holds it.
	if verb == "release" {
		if name != "nova-update" {
			return refusal(errs, tool, fmt.Errorf("unknown verb (run %s help)", name))
		}
		// The stamp goes down with it: `release adopt` compares what THIS
		// binary is against the release it is fanning out, because the
		// install every machine runs is the one this host is holding.
		return release.Main(name, args, stamp, out, errs)
	}
	if verb == "adoption" {
		if name != "nova-update" {
			return refusal(errs, tool, fmt.Errorf("unknown verb (run %s help)", name))
		}
		return adoptionVerb(name, args, stamp, out, errs)
	}
	if (name == "nova-version" && verb != "report") || (verb != "report" && verb != "check" && verb != "apply" && verb != "watch") {
		return refusal(errs, tool, fmt.Errorf("unknown verb (run %s help)", name))
	}
	if verb == "watch" {
		return watchMain(name, args, out, errs, env)
	}
	token := strings.ToUpper(verb)
	if verb == "check" {
		token = "UPDATE"
	}
	o := options{max: 20, timeout: 5 * time.Second, budget: 60 * time.Second}
	f := flag.NewFlagSet(verb, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&o.file, "file", "", "manifest")
	f.DurationVar(&o.timeout, "timeout", o.timeout, "one read deadline")
	if verb == "apply" {
		f.StringVar(&o.target, "version", "", "target")
	} else {
		f.DurationVar(&o.budget, "budget", o.budget, "whole run deadline")
		f.IntVar(&o.max, "max", 20, "per-kind output cap")
		f.Var(&o.kinds, "kind", "kind filter")
	}
	if verb == "report" {
		f.StringVar(&o.host, "host", "", "execution bench label")
		f.StringVar(&o.snapshot, "snapshot", "", "explicit state file")
		f.BoolVar(&o.draft, "draft", false, "print note only")
		f.BoolVar(&o.send, "send", impliedSend, "explicit delivery")
		for flagName, p := range map[string]*string{"as": &o.as, "to": &o.to, "bus": &o.bus, "remote": &o.remote, "branch": &o.branch} {
			f.StringVar(p, flagName, "", flagName)
		}
	}
	if err := f.Parse(interspersed(f, args)); err != nil {
		// `<tool> <verb> --help` is a reasonable question, and the flag
		// package answers it with the sentinel flag.ErrHelp. Printing that
		// gave the person `flag: help requested` -- the package's internals,
		// leaked to somebody who asked for help (darwin dogfood, 2026-09-18).
		// They get the usage, and exit 0, because asking is not an error.
		if errors.Is(err, flag.ErrHelp) {
			help(name, out)
			return 0
		}
		return refusal(errs, token, fmt.Errorf("%s (run %s help)", err, name))
	}
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
		return refusal(errs, token, fmt.Errorf("missing %s; refusing to guess (supply each named flag; run: %s help)", strings.Join(missing, ", "), name))
	}
	if o.max < 0 || o.timeout <= 0 || o.budget <= 0 {
		return refusal(errs, token, fmt.Errorf("invalid bound (use --max >= 0 and positive --timeout/--budget)"))
	}
	if (verb == "apply" && len(f.Args()) != 1) || (verb != "apply" && len(f.Args()) != 0) {
		return refusal(errs, token, fmt.Errorf("%s requires %s (run %s help)", verb, map[bool]string{true: "exactly one entry name", false: "no positional arguments"}[verb == "apply"], name))
	}
	if o.draft && o.send {
		return refusal(errs, token, fmt.Errorf("draft and send are exclusive (choose --draft or --send)"))
	}
	if o.draft || o.send {
		required := []struct{ n, v string }{{"as", o.as}, {"to", o.to}}
		if o.send {
			required = append(required, struct{ n, v string }{"bus", o.bus}, struct{ n, v string }{"remote", o.remote}, struct{ n, v string }{"branch", o.branch})
		}
		for _, x := range required {
			if x.v == "" {
				return refusal(errs, token, fmt.Errorf("--%s is required (supply --%s)", x.n, x.n))
			}
		}
		for _, s := range []string{o.as, o.to, o.host} {
			if strings.ContainsAny(s, "\r\n") {
				return refusal(errs, token, fmt.Errorf("note header contains a newline (use a single-line --as, --to and --host)"))
			}
		}
	}
	file, err := os.Open(o.file)
	if err != nil {
		return refusal(errs, token, fmt.Errorf("cannot open %s (supply a readable --file: %s)", o.file, manifestShape))
	}
	entries, err := Load(file)
	file.Close()
	if err != nil {
		return refusal(errs, token, fmt.Errorf("%s: %w", o.file, err))
	}
	if verb == "apply" {
		return apply(entries, f.Args()[0], o, out, errs, env)
	}
	selected := []Entry{}
	for _, e := range entries {
		if len(o.kinds) == 0 || contains(o.kinds, e.Kind) {
			selected = append(selected, e)
		}
	}
	kinds := []string(o.kinds)
	if len(kinds) == 0 {
		kinds = append([]string(nil), Kinds...)
	}
	sort.Strings(kinds)
	started := env.Now()
	ctx, cancel := context.WithTimeout(context.Background(), o.budget)
	defer cancel()
	if verb == "report" {
		return report(ctx, entries, selected, o, strings.Join(kinds, ","), started, out, errs, env)
	}
	fmt.Fprintf(out, "UPDATE at=%s file=%s entries=%d kinds=%s timeout=%s budget=%s max=%d\n", field(started.UTC().Format(time.RFC3339)), field(o.file), len(entries), field(strings.Join(kinds, ",")), o.timeout, o.budget, o.max)
	results := readEntries(ctx, selected, o, env, false)
	counts := map[string]int{}
	pins := 0
	group := bounded.Grouped(out, o.max, "UPDATE", "use --max 0 to show all")
	for _, r := range results {
		status, ahead := verdict(r)
		counts[status]++
		if r.Entry.Kind == "pin" && status == "DIFFERENT" {
			pins++
		}
		if status == "EQUAL" {
			continue
		}
		if status == "UNKNOWN" {
			x := r.Installed
			if x.Known() {
				x = r.Latest
			}
			group.Line("unknown", fmt.Sprintf("UPDATE UNKNOWN name=%s kind=%s installed=%s path=%s source=%s: %s (%s)", field(r.Entry.Name), field(r.Entry.Kind), field(r.Installed.Version), field(r.Installed.Path), field(r.Latest.Source), oneline.Escape(x.Reason), oneline.Escape(x.Remedy)))
		} else if status == "AHEAD" {
			group.Line("ahead", fmt.Sprintf("UPDATE AHEAD name=%s kind=%s installed=%s latest=%s ahead=%s path=%s source=%s owner=%s", field(r.Entry.Name), field(r.Entry.Kind), field(r.Installed.Version), field(r.Latest.Version), field(ahead), field(r.Installed.Path), field(r.Latest.Source), field(r.Entry.Owner)))
		} else {
			group.Line(strings.ToLower(status), fmt.Sprintf("UPDATE %s name=%s kind=%s installed=%s latest=%s path=%s source=%s owner=%s", status, field(r.Entry.Name), field(r.Entry.Kind), field(r.Installed.Version), field(r.Latest.Version), field(r.Installed.Path), field(r.Latest.Source), field(r.Entry.Owner)))
		}
	}
	group.More()
	code := 0
	result := "OK"
	w := out
	if counts["EQUAL"] != len(selected) {
		code = 1
		result = "FAIL"
		w = errs
	}
	fmt.Fprintf(w, "UPDATE %s checked=%d current=%d stale=%d newer=%d ahead=%d differ=%d unknown=%d pins=%d took=%s file=%s\n", result, len(selected), counts["EQUAL"], counts["STALE"], counts["NEWER"], counts["AHEAD"], counts["DIFFERENT"], counts["UNKNOWN"], pins, time.Since(started).Round(time.Millisecond), field(o.file))
	return code
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
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
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
		}()
	}
	for i := range entries {
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
		if wake.AcceptBus(r.Installed.Version, r.Latest.Version) {
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
func apply(entries []Entry, name string, o options, out, errs io.Writer, env Environment) int {
	var e *Entry
	for i := range entries {
		if entries[i].Name == name {
			e = &entries[i]
			break
		}
	}
	if e == nil {
		return refusal(errs, "APPLY", fmt.Errorf("name %s absent from %s (%d entries; name one exact entry)", name, o.file, len(entries)))
	}
	if e.Kind == "model" {
		return refusal(errs, "APPLY", fmt.Errorf("model %s is not installed by this tool (owner: ollama pull %s; nova-local status --list)", name, name))
	}
	if len(e.Apply) == 0 {
		return refusal(errs, "APPLY", fmt.Errorf("%s is installed by hand (follow the owner's installation procedure)", name))
	}
	if o.target != "" && !strings.Contains(strings.Join(e.Apply, " "), "{version}") {
		return refusal(errs, "APPLY", fmt.Errorf("this entry's apply does not take a version (remove --version or declare {version} in the manifest)"))
	}
	started := env.Now()
	target := o.target
	if target == "" {
		r := Latest(context.Background(), *e, o.timeout, env.Client)
		if !r.Known() {
			return refusal(errs, "APPLY", fmt.Errorf("latest unknown for %s (pass --version <v>, or ask again when the source answers)", name))
		}
		target = r.Version
	} else {
		var err error
		target, err = versionKey(target)
		if err != nil {
			return refusal(errs, "APPLY", fmt.Errorf("invalid target (pass a complete version with --version)"))
		}
	}
	before := Installed(context.Background(), *e, o.timeout, false)
	fmt.Fprintf(out, "APPLY BEFORE name=%s kind=%s installed=%s path=%s latest=%s source=%s\n", field(name), field(e.Kind), field(before.Version), field(before.Path), field(target), field(e.Latest))
	args := append([]string(nil), e.Apply...)
	for i := range args {
		args[i] = strings.ReplaceAll(args[i], "{version}", target)
	}
	fmt.Fprintf(out, "APPLY RUN name=%s argv=%d version=%s: %s\n", field(name), len(args), field(target), oneline.Escape(strings.Join(args, " ")))
	ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
	p := process(ctx, args, nil, ChildCap)
	cancel()
	after := Installed(context.Background(), *e, o.timeout, false)
	fmt.Fprintf(out, "APPLY AFTER name=%s installed=%s was=%s\n", field(name), field(after.Version), field(before.Version))
	reason := p.Reason
	if reason == "" && !after.Known() {
		reason = after.Reason
	}
	if reason == "" && after.Version != target {
		reason = "installed " + after.Version + ", asked " + target
	}
	if reason != "" {
		fmt.Fprintf(errs, "APPLY FAIL name=%s from=%s to=%s took=%s: %s\n", field(name), field(before.Version), field(after.Version), time.Since(started).Round(time.Millisecond), oneline.Escape(reason))
		return 1
	}
	fmt.Fprintf(out, "APPLY OK name=%s from=%s to=%s took=%s\n", field(name), field(before.Version), field(after.Version), time.Since(started).Round(time.Millisecond))
	return 0
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
func movedVerb(name string, args []string, out, errs io.Writer, env Environment) int {
	started := env.Now()
	fs := flag.NewFlagSet("moved", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var from, to, repo, outPath string
	timeout, budget := movedChildTimeout, movedBudget
	fs.StringVar(&from, "from", "", "revision to compare from")
	fs.StringVar(&to, "to", "", "revision to compare to")
	fs.StringVar(&repo, "repo", "", "checkout holding both revisions")
	fs.StringVar(&outPath, "out", "", "note to write")
	fs.DurationVar(&timeout, "timeout", timeout, "one child's deadline")
	fs.DurationVar(&budget, "budget", budget, "whole run deadline")
	if err := fs.Parse(interspersed(fs, args)); err != nil {
		return refusal(errs, "MOVED", fmt.Errorf("%s (run %s help)", err, name))
	}
	var missing []string
	for _, x := range []struct{ n, v string }{{"--from", from}, {"--to", to}, {"--repo", repo}, {"--out", outPath}} {
		if x.v == "" {
			missing = append(missing, x.n)
		}
	}
	if len(missing) > 0 {
		return refusal(errs, "MOVED", fmt.Errorf("missing %s; refusing to guess (supply each named flag; run: %s help)", strings.Join(missing, ", "), name))
	}
	if timeout <= 0 || budget <= 0 {
		return refusal(errs, "MOVED", fmt.Errorf("invalid bound (use positive --timeout/--budget)"))
	}
	if len(fs.Args()) != 0 {
		return refusal(errs, "MOVED", fmt.Errorf("moved takes no positional arguments (run %s help)", name))
	}
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
		return refusal(errs, "MOVED", err)
	}
	toSha, err := resolve(to)
	if err != nil {
		return refusal(errs, "MOVED", err)
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
		return refusal(errs, "MOVED", fmt.Errorf("cannot read the commits between %s and %s in %s (%s)", fromSha, toSha, repo, oneline.Escape(p.Reason)))
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
		return refusal(errs, "MOVED", fmt.Errorf("cannot create a staging directory (%s)", oneline.Escape(err.Error())))
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
		for _, tool := range tools {
			bin := filepath.Join(built, tool)
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
					what = "the run's " + budget.String() + " budget was spent before " + tool + " was read"
				} else if p.Reason == "timeout" {
					what = "timeout after " + timeout.String()
				}
			}
			if p.Reason != "" || strings.TrimSpace(p.Stdout) == "" {
				// SPEC-VERSION rule 4: a cmd/* that builds but answers no
				// help names the tool, the revision and the build to repair
				// there.
				return nil, fmt.Errorf("cannot read %s help at %s (%s) (repair the build there: go build ./cmd/%s, or raise --timeout)", tool, rev, what, tool)
			}
			inv[tool] = parseMovedHelp(tool, p.Stdout)
		}
		return inv, nil
	}
	before, err := inventory(fromSha)
	if err != nil {
		return refusal(errs, "MOVED", err)
	}
	after := before
	if toSha != fromSha {
		if after, err = inventory(toSha); err != nil {
			return refusal(errs, "MOVED", err)
		}
	}
	entries, counts := diffMoved(before, after, stated)
	var note strings.Builder
	fmt.Fprintf(&note, "MOVED from=%s to=%s at=%s\n", field(fromSha), field(toSha), field(started.UTC().Format(time.RFC3339)))
	for _, e := range entries {
		fmt.Fprintln(&note, e)
	}
	if err := os.WriteFile(outPath, []byte(note.String()), 0o644); err != nil {
		return refusal(errs, "MOVED", fmt.Errorf("cannot write --out %s (supply a writable --out path)", outPath))
	}
	// The one line, every field named (SPEC-VERSION rule 3): added, deleted
	// and renamed count tools, and verbs counts the (tool, verb) pairs the
	// --to build answers -- the size of the surface the note describes.
	fmt.Fprintf(out, "MOVED OK from=%s to=%s added=%d deleted=%d renamed=%d verbs=%d file=%s\n",
		field(fromSha), field(toSha), counts.added, counts.deleted, counts.renamed, counts.verbs, field(outPath))
	return 0
}

// movedInv is one revision's inventory as its own builds reported it: every
// cmd/* tool the revision builds, and for each, the verbs and flags its `help`
// printed.
type movedInv map[string]map[string]map[string]bool

// parseMovedHelp reads one built tool's `help` into verbs and flags. Only lines
// that begin with the tool's own name count: a help that prints another tool's
// usage line (SPEC-VERSION's block names nova-update's in nova-version's help)
// cannot add that tool to THIS revision's inventory, and a line that is not a
// usage line -- the defaults, the notes, the examples -- contributes nothing.
// This function is the whole of "never a hand-written list" (#2288): whatever
// these lines do not print, the note cannot announce.
func parseMovedHelp(tool, help string) map[string]map[string]bool {
	verbs := map[string]map[string]bool{}
	for _, line := range strings.Split(help, "\n") {
		if !strings.HasPrefix(line, tool+" ") {
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
