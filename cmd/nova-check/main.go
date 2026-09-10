// nova-check runs the record-layer checks described in SPEC.md: boot
// attestation, link integrity, the kernel size budget, the self/machinery
// separation, the SEED-CORE ↔ SEED.md floor-set parity, and the protected
// corpus a line has chosen never to lose silently. Exit 0 pass,
// 1 check failed, 2 could not run.
//
// Every path and every budget comes from a flag. There are no defaults:
// a missing flag is a refusal, never a guess.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/check"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const usage = `nova-check: record-layer checks for a nova self repo (see SPEC.md)

usage:
  nova-check quickstart --dir <dir> [--fail-max <n>] the two checks a first run can make
                                                     with nothing but a directory: links,
                                                     then nocode. Both run even if the
                                                     first says NO.
  nova-check attest --home <dir> --manifest <file>   did the full self load
  nova-check links  --dir <dir>                      every relative md link resolves
  nova-check kernel --file <file> --max-bytes <n>    kernel size budget, in bytes
  nova-check kernel --file <file> --max-tokens <n> --bytes-per-token <r>
                                                     kernel size budget, in tokens
  nova-check nocode --dir <dir>                      no code files in a self repo
        [--allow <prefix>]     where machinery may live (repeatable, empty by default)
        [--deny-ext <l|@f>]    replace the floor EXTENSION list wholesale
        [--deny-ext-add <l|@f>] extend the floor EXTENSION list
        [--print-deny-list]    print both floors in force, exit 0
    two floors: an EXTENSION list, and a NAME list for build machinery named
    or located rather than extensioned (Makefile, .github/workflows/). The
    --deny-ext flags govern the EXTENSION list only; --allow is the escape
    for the name floor, and names where machinery may live.
  nova-check floors --core <SEED-CORE.md> --source <SEED.md>
                                                     the door's floor set matches the seed's
  nova-check corpus --ledger <file> --root <dir> --min-anchors <n>
                                                     protected material is still where the
                                                     ledger says it is

  --fail-max <n>   on quickstart, attest, links, nocode and corpus: how many
                   FAIL lines to print before one MORE line stands for the
                   rest. Default 20, and 0 means all. The count line prints
                   whether the check passed or failed, so a run that found 800
                   broken links says 800 without printing 800.

exit codes: 0 pass, 1 check failed, 2 could not run (bad invocation).

example:
  nova-check quickstart --dir ./self
  nova-check attest --home ./self --manifest ./self/MANIFEST
  nova-check kernel --file ./self/SEED-CORE.md --max-bytes 4000
  nova-check corpus --ledger ./self/corpus/anchors.md --root ./self --min-anchors 2

./self there is a directory of your own; cmd/nova-check/testdata/example-self
in this repo is one the size of a first run, and every line above is run
against it by the tests.
`

// The hints below turn this binary's most-hit refusals into a next step. The
// no-guessing law is unchanged — a missing flag is still exit 2 and still says
// "refusing to guess" — but a refusal that names only what was wrong leaves a
// first-time caller to guess what the flag wanted, which is the same guessing
// the tool refuses to do, moved onto the reader. Each hint says what the flag
// IS and what a first run should put there.
const (
	dirHint      = `--dir <dir> is the tree to walk, your self repo's root or a directory inside it; it is never guessed from the working directory, so write it out every run`
	homeHint     = `--home <dir> is your memory-home directory: the tree the manifest's paths are relative to, and the only place attest reads`
	manifestHint = `--manifest <file> is a text file listing the paths a full boot must read, one per line, relative to --home (blank lines and # comments ignored); this tool ships none, because what a full boot reads is yours`
	fileHint     = `--file <file> is the one kernel file to measure — the file whose size you are holding to a budget, not the directory it lives in`
	coreHint     = `--core <file> is the door: the derived copy, usually SEED-CORE.md, whose floor set is checked against the source's`
	sourceHint   = `--source <file> is the source the door was derived from, usually SEED.md; the check is that the copy still agrees with it`
	ledgerHint   = `--ledger <file> is your ledger of protected material: a markdown file whose table rows are | fragment | home file | given | by |, written in advance and by you — this tool ships no corpus`
	rootHint     = `--root <dir> is the repo the ledger's home paths are relative to; it is never guessed from the working directory or from where the ledger happens to sit`
	anchorsHint  = `--min-anchors <n> is the fewest rows the ledger may hold, a positive number you state: the ledger lives inside the tree it protects, so its own shrinking has to be red`
	budgetHint   = `state the unit: --max-bytes <n> for a byte budget, or --max-tokens <n> --bytes-per-token <r> for the unit a context window actually spends (the divisor is one you measured on your own writing; there is no default)`
)

// hintFor returns the already-indented hint line for a required flag, newline
// included, or "" for a flag whose own usage entry is the whole story. It
// returns package constants only, which is why printing its result is safe.
func hintFor(name string) string {
	switch name {
	case "dir":
		return "  " + dirHint + "\n"
	case "home":
		return "  " + homeHint + "\n"
	case "manifest":
		return "  " + manifestHint + "\n"
	case "file":
		return "  " + fileHint + "\n"
	case "core":
		return "  " + coreHint + "\n"
	case "source":
		return "  " + sourceHint + "\n"
	case "ledger":
		return "  " + ledgerHint + "\n"
	case "root":
		return "  " + rootHint + "\n"
	}
	return ""
}

// failMaxRemedy is the second half of every MORE line this binary prints. A cap with no
// remedy is censorship; a cap with one is an index, so the line that says what was not
// shown says in the same breath how to see it.
const failMaxRemedy = "--fail-max <n> raises the ceiling, --fail-max 0 prints every finding"

// refuse is what an unusable invocation costs: ONE line naming what was wrong, and the
// door to the usage rather than the usage itself. It was the whole 38-line banner, on
// every flag typo -- 2,411 bytes to say a dash was in the wrong place.
func refuse(stderr io.Writer, where, what string) int {
	fmt.Fprintf(stderr, "nova-check%s: %s; run: nova-check help\n", oneline.Escape(where), oneline.Escape(what))
	return 2
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return refuse(stderr, "", "no verb given; quickstart is the first run")
	}
	switch args[0] {
	case "quickstart":
		return cmdQuickstart(args[1:], stdout, stderr)
	case "attest":
		return cmdAttest(args[1:], stdout, stderr)
	case "links":
		return cmdLinks(args[1:], stdout, stderr)
	case "kernel":
		return cmdKernel(args[1:], stdout, stderr)
	case "nocode":
		return cmdNoCode(args[1:], stdout, stderr)
	case "floors":
		return cmdFloors(args[1:], stdout, stderr)
	case "corpus":
		return cmdCorpus(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		return refuse(stderr, "", fmt.Sprintf("unknown subcommand %q", args[0]))
	}
}

// parse runs a subcommand flag set and enforces the no-guessing rule:
// every listed flag must have been given a non-empty value.
//
// Package flag is given no stream: its error text quotes the argument it
// could not parse, raw, and its usage dump follows -- so an argument holding
// a newline authored a whole line of stderr before any code in this file ran.
// The refusal is printed here instead, escaped, and -h after a verb is refused
// at exit 2 like any other unusable invocation.
func parse(fs *flag.FlagSet, args []string, stderr io.Writer, required map[string]*string) bool {
	if !parseFlags(fs, args, stderr) {
		return false
	}
	return requireFlags(fs, stderr, required)
}

// parseFlags is the half of parse that decides whether anything after it can be
// trusted: once the flag set has failed to parse, the values and the positional
// arguments are both meaningless, so no verb adds a second complaint on top.
func parseFlags(fs *flag.FlagSet, args []string, stderr io.Writer) bool {
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		refuse(stderr, " "+fs.Name(), oneline.Cap(err.Error(), oneline.TailBytes))
		return false
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "nova-check %s: unexpected argument %q\n", fs.Name(), fs.Arg(0))
		return false
	}
	return true
}

// requireFlags reports EVERY missing required flag, not the first: the flags are
// independent of each other, so a caller who omitted two should learn about two
// in one run rather than being sent back for a second refusal. Each one carries
// the hint that says what the flag wants.
func requireFlags(fs *flag.FlagSet, stderr io.Writer, required map[string]*string) bool {
	names := make([]string, 0, len(required))
	for name := range required {
		names = append(names, name)
	}
	sort.Strings(names) // deterministic order, not map order
	ok := true
	for _, name := range names {
		if *required[name] == "" {
			fmt.Fprintf(stderr, "nova-check %s: --%s is required; refusing to guess\n%s", fs.Name(), name, hintFor(name))
			ok = false
		}
	}
	return ok
}

// addFailMax puts the same ceiling on every verb that lists findings, so a reader learns
// one flag and not five. Zero prints everything; a negative number is refused, because
// zero already means "all" and a negative ceiling is a typo with two readings.
func addFailMax(fs *flag.FlagSet) *int {
	return fs.Int("fail-max", bounded.Default, "FAIL lines to print before one MORE line stands for the rest; 0 prints all")
}

// checkFailMax refuses a negative ceiling, naming the verb.
func checkFailMax(fs *flag.FlagSet, max int, stderr io.Writer) bool {
	if max < 0 {
		fmt.Fprintf(stderr, "nova-check %s: --fail-max must be a line ceiling of zero or more (got %d); 0 means print them all\n", fs.Name(), max)
		return false
	}
	return true
}

// cmdQuickstart is the first run: the two checks that need nothing but a
// directory, in one command, so that a stranger's first invocation is a line
// they can type from the usage banner rather than a choice between six verbs
// and the flags each of them wants. It adds no check of its own — it runs
// links and then nocode, and both run even when the first says NO, because a
// first run should learn everything this pair can tell it in one go.
func cmdQuickstart(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("quickstart", flag.ContinueOnError)
	dir := fs.String("dir", "", "directory tree to check (required)")
	failMax := addFailMax(fs)
	if !parse(fs, args, stderr, map[string]*string{"dir": dir}) {
		return 2
	}
	if !checkFailMax(fs, *failMax, stderr) {
		return 2
	}
	// THE CAPS ARE INHERITED, and this is the verb that most needed them: quickstart is
	// the FIRST RUN, the one a stranger makes on a repo nobody has checked before, and
	// uncapped it answered with 1,400 lines for two lines of verdict. A first run should
	// cost about forty.
	max := fmt.Sprintf("%d", *failMax)
	fmt.Fprintf(stdout, "QUICKSTART OK dir=%s checks=2: links, then nocode\n", oneline.Field(*dir))
	linksCode := cmdLinks([]string{"--dir", *dir, "--fail-max", max}, stdout, stderr)
	nocodeCode := cmdNoCode([]string{"--dir", *dir, "--fail-max", max}, stdout, stderr)
	worst := 0
	for _, code := range []int{linksCode, nocodeCode} {
		if code > worst {
			worst = code
		}
	}
	// The closing line is printed on every outcome, because the verb a first
	// run needs NEXT does not depend on whether this one was green.
	fmt.Fprintf(stdout, "QUICKSTART OK done=2 worst-exit=%d next=kernel,attest,floors,corpus (each wants a budget, a manifest or a ledger of yours: nova-check help)\n", worst)
	return worst
}

func cmdAttest(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("attest", flag.ContinueOnError)
	home := fs.String("home", "", "memory-home directory (required)")
	manifest := fs.String("manifest", "", "file listing the paths a full boot must read, relative to --home (required)")
	failMax := addFailMax(fs)
	if !parse(fs, args, stderr, map[string]*string{"home": home, "manifest": manifest}) {
		return 2
	}
	if !checkFailMax(fs, *failMax, stderr) {
		return 2
	}
	att, failures, err := check.Attest(*home, *manifest)
	if err != nil {
		fmt.Fprintf(stderr, "nova-check attest: %s\n", oneline.Err(err))
		return 2
	}
	if len(failures) > 0 {
		list := bounded.Capped(stderr, *failMax, "ATTEST", "entry", failMaxRemedy)
		for _, f := range failures {
			list.Line(fmt.Sprintf("ATTEST FAIL %s: %s", oneline.Escape(f.Subject), oneline.Escape(oneline.Cap(f.Reason, oneline.TailBytes))))
		}
		list.More()
		fmt.Fprintf(stderr, "ATTEST FAIL failed=%d shown=%d manifest=%s\n", list.Total(), list.Shown(), oneline.Field(*manifest))
		return 1
	}
	fmt.Fprintf(stdout, "ATTEST OK files=%d bytes=%d sha256=%s\n", att.Files, att.Bytes, att.SHA256)
	return 0
}

func cmdLinks(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("links", flag.ContinueOnError)
	dir := fs.String("dir", "", "directory tree to scan for markdown links (required)")
	failMax := addFailMax(fs)
	if !parse(fs, args, stderr, map[string]*string{"dir": dir}) {
		return 2
	}
	if !checkFailMax(fs, *failMax, stderr) {
		return 2
	}
	mdFiles, checked, broken, err := check.Links(*dir)
	if err != nil {
		fmt.Fprintf(stderr, "nova-check links: %s\n", oneline.Err(err))
		return 2
	}
	if len(broken) > 0 {
		list := bounded.Capped(stderr, *failMax, "LINKS", "broken", failMaxRemedy)
		for _, b := range broken {
			if b.Line == 0 && b.Target == "" {
				// A whole-file finding: the .md itself could not be read, so there
				// is no line and no target — `LINKS FAIL <file>: unreadable (<why>)`.
				// A named failure like any other, per SPEC; not a refusal.
				list.Line(fmt.Sprintf("LINKS FAIL %s: %s", oneline.Escape(b.File), oneline.Escape(oneline.Cap(b.Reason, oneline.TailBytes))))
				continue
			}
			list.Line(fmt.Sprintf("LINKS FAIL %s:%d: %s (%s)", oneline.Escape(b.File), b.Line,
				oneline.Escape(oneline.Cap(b.Target, oneline.TailBytes)), oneline.Escape(oneline.Cap(b.Reason, oneline.TailBytes))))
		}
		list.More()
		// The count line prints on FAILURE too. It did not, so a failing run gave N lines
		// and never N: the one number a reader wanted was the one thing they had to
		// derive by counting the output.
		fmt.Fprintf(stderr, "LINKS FAIL files=%d links=%d broken=%d shown=%d\n", mdFiles, checked, list.Total(), list.Shown())
		return 1
	}
	fmt.Fprintf(stdout, "LINKS OK files=%d links=%d\n", mdFiles, checked)
	return 0
}

func cmdKernel(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("kernel", flag.ContinueOnError)
	file := fs.String("file", "", "kernel file to measure (required)")
	maxBytes := fs.Int64("max-bytes", 0, "size budget in bytes, must be positive (one of --max-bytes / --max-tokens)")
	maxTokens := fs.Int64("max-tokens", 0, "size budget in tokens, must be positive (one of --max-bytes / --max-tokens)")
	bytesPerToken := fs.Float64("bytes-per-token", 0, "measured bytes per token, required with --max-tokens; no default")
	if !parseFlags(fs, args, stderr) {
		return 2
	}
	// The file and the budget are independent, so both are judged before
	// either sends the caller away: `nova-check kernel` with nothing at all
	// used to name --file and stop, and the second run then learned about the
	// budget. One run, every problem it can find.
	ok := requireFlags(fs, stderr, map[string]*string{"file": file})
	// Which budget was GIVEN, not which value survived: --max-bytes 0 is a
	// stated (and refused) budget, not an absent one.
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	switch {
	case given["max-bytes"] && given["max-tokens"]:
		fmt.Fprintf(stderr, "nova-check kernel: give exactly one of --max-bytes or --max-tokens, not both; the line names the unit, the tool does not pick\n  %s\n", budgetHint)
		ok = false
	case !given["max-bytes"] && !given["max-tokens"]:
		fmt.Fprintf(stderr, "nova-check kernel: --max-bytes or --max-tokens is required; refusing to guess\n  %s\n", budgetHint)
		ok = false
	}
	if given["bytes-per-token"] && given["max-bytes"] {
		fmt.Fprintf(stderr, "nova-check kernel: --bytes-per-token applies only to --max-tokens; a divisor with a byte budget means one of the two is not what you meant\n  %s\n", budgetHint)
		ok = false
	}
	if !ok {
		return 2
	}

	if given["max-tokens"] {
		// Independent again, and reported together: a run that named a zero
		// budget and forgot the divisor has two things wrong with it.
		unit := true
		if !given["bytes-per-token"] {
			fmt.Fprintf(stderr, "nova-check kernel: --max-tokens requires --bytes-per-token; the divisor is a measurement you make on your own writing, and there is no default; refusing to guess\n  %s\n", budgetHint)
			unit = false
		} else if *bytesPerToken <= 0 {
			fmt.Fprintf(stderr, "nova-check kernel: --bytes-per-token must be a positive ratio (got %g); refusing to guess\n  %s\n", *bytesPerToken, budgetHint)
			unit = false
		}
		if *maxTokens <= 0 {
			fmt.Fprintf(stderr, "nova-check kernel: --max-tokens must be a positive token budget (got %d); refusing to guess\n  %s\n", *maxTokens, budgetHint)
			unit = false
		}
		if !unit {
			return 2
		}
		measured, tokens, failures, err := check.KernelTokens(*file, *maxTokens, *bytesPerToken)
		if err != nil {
			fmt.Fprintf(stderr, "nova-check kernel: %s\n", oneline.Err(err))
			return 2
		}
		if len(failures) > 0 {
			for _, f := range failures {
				fmt.Fprintf(stderr, "KERNEL FAIL %s: %s\n", oneline.Escape(f.Subject), oneline.Escape(f.Reason))
			}
			return 1
		}
		// The OK line teaches the unit it enforced: tokens first, then the
		// bytes and the divisor they were derived from, so the number can be
		// re-derived by anyone reading the line.
		fmt.Fprintf(stdout, "KERNEL OK tokens=%d budget=%d bytes=%d divisor=%g\n", tokens, *maxTokens, measured, *bytesPerToken)
		return 0
	}

	if *maxBytes <= 0 {
		fmt.Fprintf(stderr, "nova-check kernel: --max-bytes must be a positive byte budget (got %d); refusing to guess\n", *maxBytes)
		return 2
	}
	measured, failures, err := check.Kernel(*file, *maxBytes)
	if err != nil {
		fmt.Fprintf(stderr, "nova-check kernel: %s\n", oneline.Err(err))
		return 2
	}
	if len(failures) > 0 {
		for _, f := range failures {
			fmt.Fprintf(stderr, "KERNEL FAIL %s: %s\n", oneline.Escape(f.Subject), oneline.Escape(f.Reason))
		}
		return 1
	}
	fmt.Fprintf(stdout, "KERNEL OK bytes=%d budget=%d\n", measured, *maxBytes)
	return 0
}

// repeatable collects a flag given more than once. Every scope narrowing is
// the caller's, stated per run, and starts empty.
type repeatable []string

func (r *repeatable) String() string     { return strings.Join(*r, ",") }
func (r *repeatable) Set(v string) error { *r = append(*r, v); return nil }

func cmdNoCode(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("nocode", flag.ContinueOnError)
	dir := fs.String("dir", "", "self-repo directory to scan (required)")
	denyExt := fs.String("deny-ext", "", "replace the floor EXTENSION list (not the name floor): comma list, or @file")
	denyExtAdd := fs.String("deny-ext-add", "", "extend the floor EXTENSION list (not the name floor): comma list, or @file")
	printList := fs.Bool("print-deny-list", false, "print both floors in force (extensions and names) and exit 0")
	failMax := addFailMax(fs)
	var allow repeatable
	fs.Var(&allow, "allow", "path prefix where machinery may live (repeatable; empty by default)")

	fs.SetOutput(io.Discard) // see parse: the flag package is not allowed to print
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, " nocode", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "nova-check nocode: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	if *denyExt != "" && *denyExtAdd != "" {
		fmt.Fprintln(stderr, "nova-check nocode: --deny-ext and --deny-ext-add are mutually exclusive")
		return 2
	}
	if !checkFailMax(fs, *failMax, stderr) {
		return 2
	}

	// Resolve the effective deny-list and its provenance before anything else:
	// a guard that cannot say what it forbids must refuse, not pass.
	deny, source, err := effectiveDenyList(*denyExt, *denyExtAdd)
	if err != nil {
		fmt.Fprintf(stderr, "nova-check nocode: %s\n", oneline.Err(err))
		return 2
	}

	if *printList {
		// The NAME floor is printed alongside the extension list because this
		// flag's whole job is to print what is actually in force. A floor that
		// fires but does not appear here would be exactly the hidden default
		// the deny-list is defended against being.
		names, prefixes, nerr := check.FloorDenyNames()
		if nerr != nil {
			fmt.Fprintf(stderr, "nova-check nocode: %s\n", oneline.Err(nerr))
			return 2
		}
		fmt.Fprintf(stdout, "NOCODE DENY-LIST source=%s count=%d\n", source, len(deny))
		for _, e := range deny {
			fmt.Fprintf(stdout, "%s\n", oneline.Escape(e))
		}
		fmt.Fprintf(stdout, "NOCODE NAME-LIST source=%s names=%d paths=%d\n", check.DenyFloor, len(names), len(prefixes))
		for _, n := range sortedNames(names) {
			fmt.Fprintf(stdout, "name:%s\n", oneline.Escape(n))
		}
		for _, pre := range prefixes {
			fmt.Fprintf(stdout, "path:%s/\n", oneline.Escape(pre))
		}
		return 0
	}

	if *dir == "" {
		fmt.Fprintf(stderr, "nova-check nocode: --dir is required; refusing to guess\n%s", hintFor("dir"))
		return 2
	}

	opts := check.NoCodeOptions{Dir: *dir, Allow: allow, DenyExt: deny, DenySource: source}

	scanned, findings, err := check.NoCode(opts)
	if err != nil {
		fmt.Fprintf(stderr, "nova-check nocode: %s\n", oneline.Err(err))
		return 2
	}
	if len(findings) > 0 {
		list := bounded.Capped(stderr, *failMax, "NOCODE", "file", failMaxRemedy)
		for _, f := range findings {
			list.Line(fmt.Sprintf("NOCODE FAIL %s: %s", oneline.Escape(f.Subject), oneline.Escape(oneline.Cap(f.Reason, oneline.TailBytes))))
		}
		list.More()
		fmt.Fprintf(stderr, "NOCODE FAIL files=%d findings=%d shown=%d deny-list=%s\n", scanned, list.Total(), list.Shown(), source)
		return 1
	}
	// A run that classified nothing should not read as a run that found
	// nothing: an empty tree and a wrong --dir are indistinguishable here.
	if scanned == 0 {
		// The audit had no such warning, so a --dir that resolved to an empty
		// or unreadable tree read as a clean repo with nothing to say.
		fmt.Fprintf(stderr, "nova-check nocode: classified NOTHING under %s — an empty tree, everything allowed, or the wrong directory\n", oneline.Escape(*dir))
	}
	fmt.Fprintf(stdout, "NOCODE OK files=%d clean deny-list=%s\n", scanned, source)
	return 0
}

// effectiveDenyList resolves the floor list, a replacement, or an extension,
// and reports which of the three produced it.
func effectiveDenyList(replace, add string) ([]string, string, error) {
	if replace != "" {
		exts, err := check.ParseDenyList(replace)
		if err != nil {
			return nil, "", err
		}
		return exts, check.DenyReplaced, nil
	}
	floor, err := check.FloorDenyExts()
	if err != nil {
		return nil, "", err
	}
	if add == "" {
		return floor, check.DenyFloor, nil
	}
	extra, err := check.ParseDenyList(add)
	if err != nil {
		return nil, "", err
	}
	seen := make(map[string]bool, len(floor)+len(extra))
	for _, e := range floor {
		seen[e] = true
	}
	for _, e := range extra {
		seen[e] = true
	}
	merged := make([]string, 0, len(seen))
	for e := range seen {
		merged = append(merged, e)
	}
	sort.Strings(merged)
	return merged, check.DenyExtended, nil
}

func cmdFloors(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("floors", flag.ContinueOnError)
	core := fs.String("core", "", "the door: path to SEED-CORE.md (required)")
	source := fs.String("source", "", "the source: path to SEED.md (required)")
	if !parse(fs, args, stderr, map[string]*string{"core": core, "source": source}) {
		return 2
	}
	floors, failures, err := check.Floors(*core, *source)
	if err != nil {
		fmt.Fprintf(stderr, "nova-check floors: %s\n", oneline.Err(err))
		return 2
	}
	if len(failures) > 0 {
		for _, f := range failures {
			fmt.Fprintf(stderr, "FLOORS FAIL %s: %s\n", oneline.Escape(f.Subject), oneline.Escape(f.Reason))
		}
		return 1
	}
	fmt.Fprintf(stdout, "FLOORS OK floors=%d\n", floors)
	return 0
}

func cmdCorpus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("corpus", flag.ContinueOnError)
	ledger := fs.String("ledger", "", "the ledger of protected material, a markdown file (required)")
	root := fs.String("root", "", "the repo the ledger's home paths are relative to (required)")
	minAnchors := fs.Int("min-anchors", 0, "the fewest rows the ledger may hold, must be positive (required); the ledger is inside what it protects, so its own shrinking must be red")
	failMax := addFailMax(fs)
	if !parseFlags(fs, args, stderr) {
		return 2
	}
	// All three are independent, so `nova-check corpus` with nothing names all
	// three at once instead of sending a first run back twice.
	ok := requireFlags(fs, stderr, map[string]*string{"ledger": ledger, "root": root})
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	switch {
	case !given["min-anchors"]:
		fmt.Fprintf(stderr, "nova-check corpus: --min-anchors is required; the ledger lives inside the tree it protects and can be shrunk by the same events its rows exist to catch, so the floor is a number you state; refusing to guess\n  %s\n", anchorsHint)
		ok = false
	case *minAnchors <= 0:
		fmt.Fprintf(stderr, "nova-check corpus: --min-anchors must be a positive row floor (got %d); a floor of zero guards nothing, which is what an empty ledger already is; refusing to guess\n  %s\n", *minAnchors, anchorsHint)
		ok = false
	}
	if !checkFailMax(fs, *failMax, stderr) {
		ok = false
	}
	if !ok {
		return 2
	}
	// --root is validated BEFORE any finding is printed: a FAIL line from a
	// run that then exits 2 reports findings from a run that did not happen.
	if _, _, rootErr := check.ResolveRoot(*root); rootErr != nil {
		fmt.Fprintf(stderr, "nova-check corpus: %s\n", oneline.Err(rootErr))
		return 2
	}
	raw, err := os.ReadFile(*ledger)
	if err != nil {
		// Nothing was checked, so this is a refusal rather than a pass —
		// the one outcome a protection check must never confuse.
		fmt.Fprintf(stderr, "nova-check corpus: the ledger %s cannot be read (%s); NOTHING was checked, which is not a pass\n", oneline.Escape(*ledger), oneline.Err(err))
		return 2
	}
	anchors, malformed, parseErr := check.ParseLedger(raw)
	// Malformed rows print whether or not any good row survived: a ledger
	// whose rows are ALL malformed is visibly populated, and telling its
	// author it is empty while withholding the reason is the worst of both.
	// They are their own KIND under the cap, so a ledger with a thousand bad
	// rows cannot hide the anchors that also went missing.
	rows := bounded.Capped(stderr, *failMax, "CORPUS", "malformed-row", failMaxRemedy)
	for _, f := range malformed {
		rows.Line(fmt.Sprintf("CORPUS FAIL %s: %s", oneline.Escape(f.Subject), oneline.Escape(oneline.Cap(f.Reason, oneline.TailBytes))))
	}
	rows.More()
	if parseErr != nil {
		if len(malformed) > 0 {
			// Rows were found and judged bad. The check RAN, and the answer
			// is no — that is exit 1, not "could not run".
			fmt.Fprintf(stderr, "CORPUS FAIL malformed=%d shown=%d anchors=0 ledger=%s: no row survived parsing\n",
				rows.Total(), rows.Shown(), oneline.Field(*ledger))
			return 1
		}
		fmt.Fprintf(stderr, "nova-check corpus: %s: %s\n", oneline.Escape(*ledger), oneline.Err(parseErr))
		return 2
	}
	failures, err := check.Corpus(*root, *ledger, *minAnchors, anchors)
	if err != nil {
		fmt.Fprintf(stderr, "nova-check corpus: %s\n", oneline.Err(err))
		return 2
	}
	if len(failures) > 0 || len(malformed) > 0 {
		list := bounded.Capped(stderr, *failMax, "CORPUS", "anchor", failMaxRemedy)
		for _, f := range failures {
			list.Line(fmt.Sprintf("CORPUS FAIL %s: %s", oneline.Escape(f.Subject), oneline.Escape(oneline.Cap(f.Reason, oneline.TailBytes))))
		}
		list.More()
		fmt.Fprintf(stderr, "CORPUS FAIL anchors=%d floor=%d failed=%d shown=%d malformed=%d ledger=%s\n",
			len(anchors), *minAnchors, list.Total(), list.Shown(), rows.Total(), oneline.Field(*ledger))
		return 1
	}
	fmt.Fprintf(stdout, "CORPUS OK anchors=%d floor=%d ledger=%s\n", len(anchors), *minAnchors, oneline.Field(*ledger))
	return 0
}

// sortedNames returns the name-floor keys in a stable order, so that
// --print-deny-list output can be diffed between runs and between versions.
func sortedNames(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
