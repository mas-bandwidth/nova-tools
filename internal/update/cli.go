package update

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/wake"
)

// Environment supplies deterministic clock/network seams. Nil values use the
// machine clock and a credential-free, redirect-bounded HTTP client.
type Environment struct {
	Now    func() time.Time
	Client *http.Client
}
type options struct {
	file, host, snapshot, as, to, bus, remote, branch, target string
	max                                                       int
	timeout, budget                                           time.Duration
	kinds                                                     kindFlags
	draft, send                                               bool
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
// the spec file and compares the two.
const updateVerbs = `nova-update check --file <path> [--max <n>] [--timeout <d>] [--budget <d>] [--kind <k>]
nova-update apply --file <path> <name> [--version <v>] [--timeout <d>]
nova-update report --file <path> [--host <label>] [--snapshot <path>] [--draft --as <friend> --to <who,who> | --send --as <friend> --to <who,who> --bus <path> --remote <r> --branch <b>] [--max <n>] [--timeout <d>] [--budget <d>] [--kind <k>]
nova-update help`

const versionVerbs = `nova-version report --file <path> [--host <label>] [--snapshot <path>] [--draft --as <friend> --to <who,who>] [--max <n>] [--timeout <d>] [--budget <d>] [--kind <k>]
nova-version send --file <path> --as <friend> --to <who,who> --bus <path> --remote <r> --branch <b> [--snapshot <path>] [--host <label>]
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
	fmt.Fprintf(w, "%s version (or --version)\nDefaults: --max 20 (0 = all), --timeout 5s, --budget 60s. Repeat --kind to select kinds.\n", name)
	fmt.Fprintln(w, "Report needs no bus or network. Updates require an explicit apply name. Cross-process delivery recovery needs --snapshot; without it, each send is a new intention. Do not prepare again while pending; retry the saved artifact. A snapshot uses a sibling .lock file for a kernel lock; its presence never means a process is running.")
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
	if len(args) == 0 {
		return refusal(errs, "UPDATE", fmt.Errorf("a verb is required (run: %s help)", name))
	}
	verb := args[0]
	args = args[1:]
	if verb == "help" || verb == "--help" || verb == "-h" {
		if len(args) != 0 {
			return refusal(errs, "UPDATE", fmt.Errorf("help takes no arguments (run %s help)", name))
		}
		help(name, out)
		return 0
	}
	if verb == "version" || verb == "--version" {
		if len(args) != 0 {
			return refusal(errs, "UPDATE", fmt.Errorf("version takes no arguments (run %s version)", name))
		}
		fmt.Fprintln(out, buildinfo.Line(name, stamp))
		return 0
	}
	impliedSend := name == "nova-version" && verb == "send"
	if impliedSend {
		verb = "report"
	}
	if (name == "nova-version" && verb != "report") || (verb != "report" && verb != "check" && verb != "apply") {
		return refusal(errs, "UPDATE", fmt.Errorf("unknown verb (run %s help)", name))
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
		return refusal(errs, token, fmt.Errorf("cannot open %s (supply a readable --file)", o.file))
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
		status := verdict(r)
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
	fmt.Fprintf(w, "UPDATE %s checked=%d current=%d stale=%d newer=%d differ=%d unknown=%d pins=%d took=%s file=%s\n", result, len(selected), counts["EQUAL"], counts["STALE"], counts["NEWER"], counts["DIFFERENT"], counts["UNKNOWN"], pins, time.Since(started).Round(time.Millisecond), field(o.file))
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
func verdict(r entryRead) string {
	if !r.Installed.Known() || !r.Latest.Known() {
		return "UNKNOWN"
	}
	if r.Entry.Kind == "pin" {
		if wake.AcceptBus(r.Installed.Version, r.Latest.Version) {
			return "EQUAL"
		}
		return "DIFFERENT"
	}
	if r.Entry.Kind == "model" {
		if r.Installed.Version == r.Latest.Version {
			return "EQUAL"
		}
		return "DIFFERENT"
	}
	v := Compare(r.Installed.Version, r.Latest.Version)
	if v == "OLDER" {
		return "STALE"
	}
	return v
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

// Kept as a narrow seam for command tests and bus delivery; no shell is involved.
func captureRun(ctx context.Context, args []string, input []byte, cap int) ProcessResult {
	return process(ctx, args, bytes.NewReader(input), cap)
}
