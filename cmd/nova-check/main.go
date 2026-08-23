// nova-check runs the record-layer checks described in SPEC.md: boot
// attestation, link integrity, the kernel size budget, the self/machinery
// separation, and the SEED-CORE ↔ SEED.md floor-set parity. Exit 0 pass,
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

	"github.com/mas-bandwidth/nova-tools/internal/check"
)

const usage = `nova-check: record-layer checks for a nova self repo (see SPEC.md)

usage:
  nova-check attest --home <dir> --manifest <file>   did the full self load
  nova-check links  --dir <dir>                      every relative md link resolves
  nova-check kernel --file <file> --max-bytes <n>    kernel size budget, in bytes
  nova-check kernel --file <file> --max-tokens <n> --bytes-per-token <r>
                                                     kernel size budget, in tokens
  nova-check nocode --dir <dir>                      no code files in a self repo
        [--allow <prefix>]     where machinery may live (repeatable, empty by default)
        [--deny-ext <l|@f>]    replace the floor deny-list wholesale
        [--deny-ext-add <l|@f>] extend the floor deny-list
        [--staged]             classify repo-relative paths from stdin (the commit gate)
        [--print-deny-list]    print the deny-list in force, exit 0
  nova-check floors --core <SEED-CORE.md> --source <SEED.md>
                                                     the door's floor set matches the seed's

exit codes: 0 pass, 1 check failed, 2 could not run (bad invocation).
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
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
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "nova-check: unknown subcommand %q\n\n%s", args[0], usage)
		return 2
	}
}

// parse runs a subcommand flag set and enforces the no-guessing rule:
// every listed flag must have been given a non-empty value.
func parse(fs *flag.FlagSet, args []string, stderr io.Writer, required map[string]*string) bool {
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return false
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "nova-check %s: unexpected argument %q\n", fs.Name(), fs.Arg(0))
		return false
	}
	names := make([]string, 0, len(required))
	for name := range required {
		names = append(names, name)
	}
	sort.Strings(names) // deterministic order, not map order
	ok := true
	for _, name := range names {
		if *required[name] == "" {
			fmt.Fprintf(stderr, "nova-check %s: --%s is required; refusing to guess\n", fs.Name(), name)
			ok = false
		}
	}
	return ok
}

func cmdAttest(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("attest", flag.ContinueOnError)
	home := fs.String("home", "", "memory-home directory (required)")
	manifest := fs.String("manifest", "", "file listing the paths a full boot must read, relative to --home (required)")
	if !parse(fs, args, stderr, map[string]*string{"home": home, "manifest": manifest}) {
		return 2
	}
	att, failures, err := check.Attest(*home, *manifest)
	if err != nil {
		fmt.Fprintf(stderr, "nova-check attest: %v\n", err)
		return 2
	}
	if len(failures) > 0 {
		for _, f := range failures {
			fmt.Fprintf(stderr, "ATTEST FAIL %s: %s\n", f.Subject, f.Reason)
		}
		return 1
	}
	fmt.Fprintf(stdout, "ATTEST OK files=%d bytes=%d sha256=%s\n", att.Files, att.Bytes, att.SHA256)
	return 0
}

func cmdLinks(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("links", flag.ContinueOnError)
	dir := fs.String("dir", "", "directory tree to scan for markdown links (required)")
	if !parse(fs, args, stderr, map[string]*string{"dir": dir}) {
		return 2
	}
	mdFiles, checked, broken, err := check.Links(*dir)
	if err != nil {
		fmt.Fprintf(stderr, "nova-check links: %v\n", err)
		return 2
	}
	if len(broken) > 0 {
		for _, b := range broken {
			if b.Line == 0 && b.Target == "" {
				// A whole-file finding: the .md itself could not be read, so there
				// is no line and no target — `LINKS FAIL <file>: unreadable (<why>)`.
				// A named failure like any other, per SPEC; not a refusal.
				fmt.Fprintf(stderr, "LINKS FAIL %s: %s\n", b.File, b.Reason)
				continue
			}
			fmt.Fprintf(stderr, "LINKS FAIL %s:%d: %s (%s)\n", b.File, b.Line, b.Target, b.Reason)
		}
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
	if !parse(fs, args, stderr, map[string]*string{"file": file}) {
		return 2
	}
	// Which budget was GIVEN, not which value survived: --max-bytes 0 is a
	// stated (and refused) budget, not an absent one.
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	switch {
	case given["max-bytes"] && given["max-tokens"]:
		fmt.Fprintln(stderr, "nova-check kernel: give exactly one of --max-bytes or --max-tokens, not both; the line names the unit, the tool does not pick")
		return 2
	case !given["max-bytes"] && !given["max-tokens"]:
		fmt.Fprintln(stderr, "nova-check kernel: --max-bytes or --max-tokens is required; refusing to guess")
		return 2
	}
	if given["bytes-per-token"] && given["max-bytes"] {
		fmt.Fprintln(stderr, "nova-check kernel: --bytes-per-token applies only to --max-tokens; a divisor with a byte budget means one of the two is not what you meant")
		return 2
	}

	if given["max-tokens"] {
		if !given["bytes-per-token"] {
			fmt.Fprintln(stderr, "nova-check kernel: --max-tokens requires --bytes-per-token; the divisor is a measurement you make on your own writing, and there is no default; refusing to guess")
			return 2
		}
		if *maxTokens <= 0 {
			fmt.Fprintf(stderr, "nova-check kernel: --max-tokens must be a positive token budget (got %d); refusing to guess\n", *maxTokens)
			return 2
		}
		if *bytesPerToken <= 0 {
			fmt.Fprintf(stderr, "nova-check kernel: --bytes-per-token must be a positive ratio (got %g); refusing to guess\n", *bytesPerToken)
			return 2
		}
		measured, tokens, failures, err := check.KernelTokens(*file, *maxTokens, *bytesPerToken)
		if err != nil {
			fmt.Fprintf(stderr, "nova-check kernel: %v\n", err)
			return 2
		}
		if len(failures) > 0 {
			for _, f := range failures {
				fmt.Fprintf(stderr, "KERNEL FAIL %s: %s\n", f.Subject, f.Reason)
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
		fmt.Fprintf(stderr, "nova-check kernel: %v\n", err)
		return 2
	}
	if len(failures) > 0 {
		for _, f := range failures {
			fmt.Fprintf(stderr, "KERNEL FAIL %s: %s\n", f.Subject, f.Reason)
		}
		return 1
	}
	fmt.Fprintf(stdout, "KERNEL OK bytes=%d budget=%d\n", measured, *maxBytes)
	return 0
}

// stdinReader is where --staged reads its path list. A variable so a test can
// drive the commit-gate path without a process boundary.
var stdinReader io.Reader = os.Stdin

// repeatable collects a flag given more than once. Every scope narrowing is
// the caller's, stated per run, and starts empty.
type repeatable []string

func (r *repeatable) String() string     { return strings.Join(*r, ",") }
func (r *repeatable) Set(v string) error { *r = append(*r, v); return nil }

func cmdNoCode(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("nocode", flag.ContinueOnError)
	dir := fs.String("dir", "", "self-repo directory to scan (required)")
	denyExt := fs.String("deny-ext", "", "replace the floor deny-list: comma list, or @file")
	denyExtAdd := fs.String("deny-ext-add", "", "extend the floor deny-list: comma list, or @file")
	printList := fs.Bool("print-deny-list", false, "print the deny-list in force and exit 0")
	staged := fs.Bool("staged", false, "classify repo-relative paths read from stdin (the commit gate)")
	var allow repeatable
	fs.Var(&allow, "allow", "path prefix where machinery may live (repeatable; empty by default)")

	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "nova-check nocode: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	if *denyExt != "" && *denyExtAdd != "" {
		fmt.Fprintln(stderr, "nova-check nocode: --deny-ext and --deny-ext-add are mutually exclusive")
		return 2
	}
	// A hook given both would print a list and exit 0 having gated nothing.
	if *printList && *staged {
		fmt.Fprintln(stderr, "nova-check nocode: --print-deny-list and --staged are mutually exclusive; a gate that only prints is not a gate")
		return 2
	}

	// Resolve the effective deny-list and its provenance before anything else:
	// a guard that cannot say what it forbids must refuse, not pass.
	deny, source, err := effectiveDenyList(*denyExt, *denyExtAdd)
	if err != nil {
		fmt.Fprintf(stderr, "nova-check nocode: %v\n", err)
		return 2
	}

	if *printList {
		fmt.Fprintf(stdout, "NOCODE DENY-LIST source=%s count=%d\n", source, len(deny))
		for _, e := range deny {
			fmt.Fprintln(stdout, e)
		}
		return 0
	}

	if *dir == "" {
		fmt.Fprintln(stderr, "nova-check nocode: --dir is required; refusing to guess")
		return 2
	}

	opts := check.NoCodeOptions{Dir: *dir, Allow: allow, DenyExt: deny, DenySource: source}
	if *staged {
		paths, nulSeparated, err := readPaths(stdinReader)
		if err != nil {
			fmt.Fprintf(stderr, "nova-check nocode: cannot read staged paths: %v\n", err)
			return 2
		}
		opts.Staged, opts.StagedSet = paths, true
		// Only the newline form can carry git's quoting; under -z a name that
		// begins and ends with a quote is that name, not an encoding.
		opts.StagedMayBeQuoted = !nulSeparated
	}

	scanned, findings, err := check.NoCode(opts)
	if err != nil {
		fmt.Fprintf(stderr, "nova-check nocode: %v\n", err)
		return 2
	}
	if len(findings) > 0 {
		for _, f := range findings {
			fmt.Fprintf(stderr, "NOCODE FAIL %s: %s\n", f.Subject, f.Reason)
		}
		return 1
	}
	// A staged run that classified nothing is either a pure-deletion commit or
	// a hook pointed at the wrong directory, and those are indistinguishable
	// from here. Failing would break the legitimate case, so it is said out
	// loud instead: a gate that looked at nothing should not read as a gate
	// that found nothing.
	if opts.StagedSet && scanned == 0 {
		// Fires on an empty path list too: a hook that hands the gate nothing
		// is at least as suspicious as one whose paths all vanished, and a
		// --diff-filter that silently drops renames produces exactly that.
		fmt.Fprintf(stderr, "nova-check nocode: classified NOTHING out of %d staged path(s) — all deleted, all allowed, a --diff-filter that dropped them, or --dir is not the repository root\n", len(opts.Staged))
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

// readPaths reads the staged path list, accepting either form git emits:
// NUL-separated (`-z`, which never quotes and is the safe one) or one per
// line. The separator is detected rather than demanded, so a hook written
// either way behaves the same.
func readPaths(r io.Reader) (paths []string, nulSeparated bool, err error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, false, err
	}
	s := string(b)
	sep := "\n"
	if strings.ContainsRune(s, 0) {
		sep, nulSeparated = "\x00", true
	}
	var out []string
	for _, p := range strings.Split(s, sep) {
		if !nulSeparated {
			p = strings.TrimSuffix(p, "\r")
		}
		if p != "" {
			out = append(out, p)
		}
	}
	return out, nulSeparated, nil
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
		fmt.Fprintf(stderr, "nova-check floors: %v\n", err)
		return 2
	}
	if len(failures) > 0 {
		for _, f := range failures {
			fmt.Fprintf(stderr, "FLOORS FAIL %s: %s\n", f.Subject, f.Reason)
		}
		return 1
	}
	fmt.Fprintf(stdout, "FLOORS OK floors=%d\n", floors)
	return 0
}
