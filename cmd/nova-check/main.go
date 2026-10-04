// nova-check runs checks over markdown records and repositories (SPEC-CHECK.md):
// quickstart (links, then nocode), boot attestation, link integrity, the
// kernel size budget, the self/machinery separation (nocode, and nocode
// --staged over the git index), the floor-set parity of a derived door and its
// source, the protected corpus, branch hygiene, the dogfood ledger (record,
// ledger, gate), convergence and spelling. Exit 0 pass, 1 check failed, 2 could
// not run.
//
// Every path and every budget comes from a flag. There are no defaults: a
// missing flag is a refusal, never a guess. Three verbs write, each only when
// asked and each with --dry-run: dogfood record appends a receipt, spelling
// --write edits files, convergence --state stores its streak.
//
// The dispatch, the banner, the help, the version verb, the refusals and the
// output envelope are internal/tool's. A verb's Run returns one *tool.Out,
// rendered as typed lines or as the JSON of the same value.
package main

import (
	"io"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

func main() { os.Exit(novaCheck().Main()) }

// run is the entry point the tests call directly; it is the shared skeleton's
// Run with an empty stdin, not a second dispatcher.
func run(args []string, stdout, stderr io.Writer) int {
	return novaCheck().Run(args, os.Stdin, stdout, stderr)
}

// novaCheck is the tool: its verbs and their flags, run by internal/tool.
func novaCheck() *tool.Tool {
	return &tool.Tool{
		Name:      "nova-check",
		What:      "checks over markdown records and repositories, each finding named by file and line",
		Stamp:     version,
		ExitTable: "0 pass, 1 check failed, 2 could not run (bad invocation)",
		How: `most verbs inspect named paths and keep no state between runs.
dogfood record appends a receipt; spelling --write edits files in place (--dry-run: neither writes).
convergence reads forge data through gh, an optional checkout through git, and
the files you name; --state stores its two-tick streak. Other repository checks
read the manifests, ledgers and receipts you name.
first run: create the small markdown tree below, then run the example commands.`,
		Verbs: []tool.Verb{
			{
				Name:    "quickstart",
				Usage:   "quickstart --dir <dir> [--max <n>]",
				Example: "quickstart --dir ./self",
				Effect:  tool.Effect("inspection: reads the directory, writes nothing"),
				Flags: func(f *tool.Flags) {
					f.Required("dir", dirHint)
					f.Max()
					f.Var(&repeatable{}, "exclude", "path prefix not scanned by links (repeatable; empty by default)")
				},
				Run: quickstart,
			},
			{
				Name:    "attest",
				Usage:   "attest --home <dir> --manifest <file> [--max <n>]",
				Example: "attest --home ./self --manifest ./self/MANIFEST",
				Effect:  tool.Effect("inspection: reads the manifest and the files it names, writes nothing"),
				Flags: func(f *tool.Flags) {
					f.Required("home", homeHint)
					f.Required("manifest", manifestHint)
					f.Max()
				},
				Run: attest,
			},
			{
				Name:    "links",
				Usage:   "links --dir <dir> [--file <path>] [--exclude <prefix>] [--max <n>]",
				Example: "links --dir ./self",
				Effect:  tool.Effect("inspection: reads the markdown under --dir, writes nothing"),
				Flags: func(f *tool.Flags) {
					f.Required("dir", dirHint)
					f.Var(&repeatable{}, "file", "one markdown file to scan, narrowing the walk to just these (repeatable; --dir is still the resolution root)")
					f.Var(&repeatable{}, "exclude", "path prefix not scanned, and links into it not checked (repeatable; empty by default)")
					f.Max()
				},
				Run: links,
			},
			{
				Name: "kernel",
				Usage: "kernel --file <file> --max-bytes <n>\n" +
					"kernel --file <file> --max-tokens <n> --bytes-per-token <r>",
				Example: "kernel --file ./self/docs/SEED-CORE.md --max-bytes 4000",
				Effect:  tool.Effect("inspection: reads the one file, writes nothing"),
				Flags: func(f *tool.Flags) {
					f.Required("file", fileHint)
					f.Int64("max-bytes", 0, "size budget in bytes, must be positive (one of --max-bytes / --max-tokens)")
					f.Int64("max-tokens", 0, "size budget in tokens, must be positive (one of --max-bytes / --max-tokens)")
					f.Float64("bytes-per-token", 0, "measured bytes per token, required with --max-tokens; no default")
					f.Check(kernelCheck)
				},
				Run: kernel,
			},
			{
				Name:    "nocode",
				Usage:   "nocode --dir <dir> [--staged] [--allow <prefix>] [--deny-ext <l|@f>] [--deny-ext-add <l|@f>] [--print-deny-list] [--max <n>]",
				Example: "nocode --dir ./self",
				Effect:  tool.Effect("inspection: reads the tree, or with --staged the git index, writes nothing"),
				Flags: func(f *tool.Flags) {
					f.Required("dir", dirHint)
					f.Bool("staged", false, "advisory over the index: classify what is about to be committed, not the working tree (--dir is the repository root)")
					f.Var(&repeatable{}, "allow", "path prefix where machinery may live (repeatable; empty by default)")
					f.String("deny-ext", "", "replace the floor EXTENSION list (not the name floor): comma list, or @file")
					f.String("deny-ext-add", "", "extend the floor EXTENSION list (not the name floor): comma list, or @file")
					f.Bool("print-deny-list", false, "print both floors in force (extensions and names) and exit 0")
					f.Max()
				},
				Run: nocode,
			},
			{
				Name:    "floors",
				Usage:   "floors --core <docs/SEED-CORE.md> --source <docs/SEED.md>",
				Example: "floors --core ./self/docs/SEED-CORE.md --source ./self/docs/SEED.md",
				Effect:  tool.Effect("inspection: reads the two files, writes nothing"),
				Flags: func(f *tool.Flags) {
					f.Required("core", coreHint)
					f.Required("source", sourceHint)
				},
				Run: floors,
			},
			{
				Name:    "corpus",
				Usage:   "corpus --ledger <file> --root <dir> --min-anchors <n> [--max <n>]",
				Example: "corpus --ledger ./self/corpus/anchors.md --root ./self --min-anchors 1",
				Effect:  tool.Effect("inspection: reads the ledger and the files it names, writes nothing"),
				Flags: func(f *tool.Flags) {
					f.Required("ledger", ledgerHint)
					f.Required("root", rootHint)
					f.Int("min-anchors", 0, "the fewest rows the ledger may hold, must be positive (required); the ledger is inside what it protects, so its own shrinking must be red")
					f.Max()
					f.Check(func(c *tool.Call) {
						if !c.Given("min-anchors") {
							c.Problem("--min-anchors is required; the ledger lives inside the tree it protects and can be shrunk by the same events its rows exist to catch, so the floor is a number you state; refusing to guess")
						} else if c.Int("min-anchors") <= 0 {
							c.Problem("--min-anchors must be a positive row floor; a floor of zero guards nothing, which is what an empty ledger already is; refusing to guess")
						}
					})
				},
				Run: corpus,
			},
			{
				Name:    "hygiene",
				Usage:   "hygiene --repo <dir> --base <ref> --head <ref> --identity \"<Name> <email>\" [--paths <glob>[,<glob>...]] [--kind <card kind>] [--max <n>] [--timeout <seconds>]",
				Example: "hygiene --repo . --base main --head card --identity \"you <you@example.com>\" --kind sweep",
				Effect:  tool.Effect("inspection: reads the repository through git, writes nothing"),
				Flags: func(f *tool.Flags) {
					f.Required("repo", "the git checkout to inspect")
					f.Required("base", "the base git ref of the comparison")
					f.Required("head", "the head git ref of the comparison")
					f.String("paths", "", "comma-separated allowed path globs; empty skips out-of-path checking")
					f.String("identity", "", "comma-separated allowed authors in Name <email> form")
					f.String("kind", "", "card kind to validate; empty skips kind-specific checks")
					f.Int("max", bounded.Default, "finding lines to print; 0 prints all")
					f.Int("timeout", 120, "git inspection deadline in positive seconds")
				},
				Run: hygieneRun,
			},
			{
				Name:    "dogfood ledger",
				Usage:   "dogfood ledger (--cli <docs/CLI.md> | --tools <dir>) --receipts <dir> [--authors <file>] [--repo <dir>]",
				Example: "dogfood ledger --cli ./docs/CLI.md --receipts ./dogfood-receipts",
				Effect:  tool.Effect("inspection: reads the verb list and the receipts (--repo reads git, --tools runs each binary's help), writes nothing"),
				Flags:   dogfoodReadFlags,
				Run:     dogfoodLedger,
			},
			{
				Name:    "dogfood record",
				Usage:   "dogfood record (--cli <docs/CLI.md> | --tools <dir>) --tool <t> --verb <v> --by <name> (--ok|--not-ok) --notes <text> [--issue <n>] [--closes <id>] --receipts <dir> [--tools-timeout <s>] [--max <n>] [--dry-run]",
				Example: "dogfood record --cli ./docs/CLI.md --tool nova-check --verb links --by you --ok --notes \"ran it on real work\" --receipts ./dogfood-receipts",
				Effect:  tool.Effect("local write: appends one receipt file to --receipts (--dry-run writes none)"),
				Flags:   dogfoodRecordFlags,
				Run:     dogfoodRecord,
			},
			{
				Name:    "dogfood gate",
				Usage:   "dogfood gate (--cli <docs/CLI.md> | --tools <dir>) --receipts <dir> [--shipped <cmd dir>] [--require-all] [--allow-empty]",
				Example: "dogfood gate --cli ./docs/CLI.md --receipts ./dogfood-receipts",
				Effect:  tool.Effect("inspection: reads the verb list and the receipts (--repo reads git, --tools runs each binary's help), writes nothing"),
				Flags:   dogfoodGateFlags,
				Run:     dogfoodGate,
			},
			{
				Name:    "convergence",
				Usage:   "convergence --repo <owner/name> --ledger <md> --receipts <dir> --retired <file> --since <RFC3339|24h> [--bin <dir>] [--repo-dir <dir>] [--batch-logs <dir>] [--versions <tsv>] [--certs <tsv>] [--state <file>] [--by <name>] [--json] [--dry-run]",
				Example: "convergence --repo owner/name --ledger ./pitstop.md --receipts ./dogfood-receipts --retired ./retired.md --since 24h",
				Effect:  tool.Effect("local write: --state stores the two-tick streak (--dry-run writes none); LANDING and PRS read the forge through gh, over the network, and CLASSES reads --repo-dir through git"),
				Flags:   convergenceFlags,
				Run:     convergence,
			},
			{
				Name:    "spelling",
				Usage:   "spelling (--dir <dir> | --file <path> | --path <pattern>) [--ignore <word|@file>] [--write] [--exclude <prefix>] [--max <n>] [--dry-run]",
				Example: "spelling --dir ./self",
				Effect:  tool.Effect("local write: --write edits the files in place (--dry-run, or no --write, writes nothing)"),
				Flags:   spellingFlags,
				Run:     spelling,
			},
		},
	}
}

// repeatable collects a flag given more than once. Every scope narrowing is
// the caller's, stated per run, and starts empty.
type repeatable []string

func (r *repeatable) String() string { return strings.Join(*r, ",") }
func (r *repeatable) Set(v string) error {
	*r = append(*r, v)
	return nil
}
