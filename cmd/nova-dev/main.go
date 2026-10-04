// nova-dev carries this repository's own development process: the dogfood
// ledger (who has run each verb on real work, and the edges they filed), the
// convergence reading (whether each stream is contracting), and the hygiene
// check (the four mechanical checks the accept gate runs, on a branch). The
// general record checks and the seed's charter checks stay in nova-check: a
// tool is one thing, and these three serve a repository's process, not its
// records. Exit 0 pass, 1 the verb ran and said no, 2 could not run.
//
// The dispatch, the banner, the help, the version verb, the refusals and the
// output envelope are internal/tool's (STANDARD section 2, one shape across
// the set). Every path and every budget comes from a flag: a missing flag is
// a refusal, never a guess.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

var version string

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// run is the entry point the tests drive. The three verbs print their own
// lines (Flags.Prints) and parse their own flags, so each is handed the raw
// arguments after its verb word; the skeleton's flag set declares the same
// flags so a typo is refused and -h answered before a verb runs.
func run(args []string, stdout, stderr io.Writer) int {
	return devTool(args).Run(args, os.Stdin, stdout, stderr)
}

// devTool is the command: its three verbs and the shared skeleton
// (internal/tool). rest is the invocation after its verb word; every verb
// here is one word long, and each parses rest itself.
func devTool(args []string) *tool.Tool {
	rest := []string{}
	if len(args) > 0 && args[0] != "help" && args[0] != "-h" && args[0] != "--help" && args[0] != "--version" {
		rest = args[1:]
	}
	return &tool.Tool{
		Name:  "nova-dev",
		What:  "this repository's own development process: the dogfood ledger, the convergence reading and the branch hygiene check",
		Stamp: version,
		How: `dogfood reads the verbs from the command reference or the built binaries and
the receipts from a directory you name, and appends one receipt per real run.
convergence reads the forge, a checkout, the receipts and the files you name.
hygiene reads a git range you name and writes nothing.
first run: the three examples are one sitting over the fixture under testdata/.`,
		ExitTable: "0 pass, 1 the verb ran and said no (a finding, a widening streak, an unproven verb), 2 could not run (bad invocation).",
		Default:   "dogfood",
		Verbs: []tool.Verb{
			{
				Name: "dogfood",
				Usage: "dogfood ledger --cli <file> --receipts <dir> [--authors <file>] [--repo <dir>]\n" +
					"dogfood record (--cli <docs/CLI.md> | --tools <dir>) --tool <t> --verb <v> --by <name> (--ok|--not-ok)\n" +
					"dogfood record --notes <text> [--issue <n>] [--closes <id>] --receipts <dir> [--tools-timeout <s>] [--fail-max <n>]\n" +
					"dogfood gate --cli <file> --receipts <dir> [--shipped <cmd dir>] [--require-all] [--allow-empty]",
				Example: "dogfood ledger --cli ./docs/CLI.md --receipts ./dogfood-receipts\n" +
					"dogfood record --cli ./docs/CLI.md --tool nova-dev --verb hygiene --by Ada --ok --notes dogfooded-the-ledger-over-this-reference --receipts ./dogfood-receipts\n" +
					"dogfood ledger --cli ./docs/CLI.md --receipts ./dogfood-receipts --repo .",
				Effect: tool.Inspection,
				Flags:  func(f *tool.Flags) { f.Prints() },
				Run: func(c *tool.Call) *tool.Out {
					return tool.Exit(cmdDogfood(rest, c.Stdout, c.Stderr))
				},
			},
			{
				Name: "convergence",
				Usage: "convergence --repo <owner/name> --ledger <md> --receipts <dir> --retired <file> --since <RFC3339|24h>\n" +
					"convergence [--bin <dir>] [--repo-dir <dir>] [--batch-logs <dir>] [--versions <tsv>]\n" +
					"convergence [--certs <tsv>] [--state <file>] [--by <name>] [--json]",
				Effect: tool.Inspection,
				Flags: func(f *tool.Flags) {
					f.Prints()
					convergenceFlagValues(f.FlagSet)
				},
				Run: func(c *tool.Call) *tool.Out {
					return tool.Exit(cmdConvergence(rest, c.Stdout, c.Stderr))
				},
			},
			{
				Name: "hygiene",
				Usage: "hygiene --repo <dir> --base <ref> --head <ref> --identity \"<Name> <email>\"\n" +
					"hygiene [--paths <glob>[,<glob>...]] [--kind <card kind>] [--max <n>] [--timeout <seconds>]",
				Effect: tool.Inspection,
				Flags: func(f *tool.Flags) {
					f.Prints()
					hygieneFlagValues(f.FlagSet)
				},
				Run: func(c *tool.Call) *tool.Out {
					return tool.Exit(cmdHygiene(rest, c.Stdout, c.Stderr))
				},
			},
		},
	}
}

// hintFor returns the already-indented hint line for a required flag, newline
// included, or "" for a flag whose own usage entry is the whole story. It
// returns package constants only, which is why printing its result is safe.
func hintFor(name string) string {
	switch name {
	case "cli":
		return "  " + cliHint + "\n"
	case "tools":
		return "  " + toolsHint + "\n"
	case "receipts":
		return "  " + receiptsHint + "\n"
	case "tool":
		return "  " + toolHint + "\n"
	case "verb":
		return "  " + verbHint + "\n"
	case "by":
		return "  " + byHint + "\n"
	case "notes":
		return "  " + notesHint + "\n"
	}
	return ""
}

// failMaxRemedy is the second half of every MORE line this binary prints. A cap with no
// remedy is censorship; a cap with one is an index, so the line that says what was not
// shown says in the same breath how to see it.
const failMaxRemedy = "--fail-max <n> raises the ceiling, --fail-max 0 prints every finding"

// refuse is what an unusable invocation costs: ONE line naming what was wrong, and the
// door to the usage rather than the usage itself.
func refuse(stderr io.Writer, where, what string) int {
	if out, ok := stderr.(*jsonOutput); ok && *out.enabled {
		return out.refuse(where, what)
	}
	fmt.Fprintf(stderr, "nova-dev%s: %s; run: nova-dev help\n", oneline.Escape(where), oneline.Escape(what))
	return 2
}

// refuseRan is what a check that RAN and answered NO costs: ONE line naming the
// verdict and its remedy, and no door. The door is for an unusable invocation
// (refuse); pointing a reader at `nova-dev help` after a gate has run is noise.
func refuseRan(stderr io.Writer, where, what string) int {
	fmt.Fprintf(stderr, "nova-dev%s: %s\n", oneline.Escape(where), oneline.Escape(what))
	return 1
}

// parse runs a subcommand flag set and enforces the no-guessing rule:
// every listed flag must have been given a non-empty value.
//
// Package flag is given no stream: its error text quotes the argument it
// could not parse, raw, and its usage dump follows -- so an argument holding
// a newline authored a whole line of stderr before any code in this file ran.
// The refusal is printed here instead, escaped. -h after a verb is not refused:
// verbflag.Parse raises that verb's help, which the skeleton prints on stdout at exit 0.
func parse(fs *flag.FlagSet, args []string, stderr io.Writer, required map[string]*string) bool {
	if !parseFlags(fs, args, stderr) {
		return false
	}
	return requireFlags(fs, stderr, required)
}

// parseFlags is the half of parse that decides whether anything after it can be
// trusted: once the flag set has failed to parse, the values and the
// positional arguments are both meaningless, so no verb adds a second complaint on top.
func parseFlags(fs *flag.FlagSet, args []string, stderr io.Writer) bool {
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if err := verbflag.Parse(fs, args); err != nil {
		refuse(stderr, " "+fs.Name(), oneline.Cap(err.Error(), oneline.TailBytes))
		return false
	}
	if fs.NArg() > 0 {
		// Through refuse like every other unusable invocation: a stray word after a
		// verb says what was wrong and where the usage lives.
		refuse(stderr, " "+fs.Name(), fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
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
			refuse(stderr, " "+fs.Name(), fmt.Sprintf("--%s is required; refusing to guess", name))
			fmt.Fprint(stderr, hintFor(name))
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
		refuse(stderr, " "+fs.Name(), fmt.Sprintf("--fail-max must be a line ceiling of zero or more (got %d); 0 means print them all", max))
		return false
	}
	return true
}

// repeatable collects a flag given more than once. Every scope narrowing is
// the caller's, stated per run, and starts empty.
type repeatable []string

func (r *repeatable) String() string     { return strings.Join(*r, ",") }
func (r *repeatable) Set(v string) error { *r = append(*r, v); return nil }
