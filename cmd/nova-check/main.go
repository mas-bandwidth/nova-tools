// nova-check runs the record-layer checks described in SPEC.md: boot
// attestation, link integrity, the kernel size budget, and the
// self/machinery separation. Exit 0 pass, 1 check failed, 2 could not run.
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

	"github.com/mas-bandwidth/nova-tools/internal/check"
)

const usage = `nova-check: record-layer checks for a nova self repo (see SPEC.md)

usage:
  nova-check attest --home <dir> --manifest <file>   did the full self load
  nova-check links  --dir <dir>                      every relative md link resolves
  nova-check kernel --file <file> --max-bytes <n>    kernel size budget
  nova-check nocode --dir <dir>                      no code files in a self repo

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
	maxBytes := fs.Int64("max-bytes", 0, "size budget in bytes, must be positive (required)")
	if !parse(fs, args, stderr, map[string]*string{"file": file}) {
		return 2
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

func cmdNoCode(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("nocode", flag.ContinueOnError)
	dir := fs.String("dir", "", "self-repo directory to scan (required)")
	if !parse(fs, args, stderr, map[string]*string{"dir": dir}) {
		return 2
	}
	scanned, findings, err := check.NoCode(*dir)
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
	fmt.Fprintf(stdout, "NOCODE OK files=%d clean\n", scanned)
	return 0
}
