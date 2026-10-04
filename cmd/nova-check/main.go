// nova-check runs checks over markdown records and repositories (SPEC-CHECK.md):
// quickstart (links, then nocode), boot attestation, link integrity, the
// kernel size budget, the self/machinery separation (nocode, and nocode
// --staged over the git index), the floor-set parity of a derived door and its
// source, the protected corpus, and spelling. The repository's own process
// checks (dogfood, convergence, hygiene) are nova-dev's. Exit 0 pass, 1 check
// failed, 2 could not run.
//
// Every path and every budget comes from a flag. There are no defaults: a
// missing flag is a refusal, never a guess. One verb writes, only when asked
// and with --dry-run: spelling --write edits files in place.
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

type env struct {
	// wd is the directory a relative path resolves against. Empty is the
	// process's own working directory, which is what a run from a shell reads:
	// os.ReadFile and filepath.Abs resolve a relative path against it already.
	wd string
	// lookPath finds a program by name; nil leaves the program to gitrun's own
	// default, its name on the process's PATH.
	lookPath func(string) (string, error)
}

func newEnv() env { return env{lookPath: exec.LookPath} }

func (e env) path(p string) string {
	if e.wd == "" || p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(e.wd, p)
}

func (e env) workdir() (string, error) {
	if e.wd != "" {
		return e.wd, nil
	}
	return os.Getwd()
}

func (e env) git() string {
	if e.lookPath == nil {
		return ""
	}
	if p, err := e.lookPath("git"); err == nil {
		return p
	}
	return ""
}

func (e env) gitOptions(root string) gitrun.Options {
	return gitrun.Options{Bin: e.git(), C: root}
}

func main() { os.Exit(novaCheck().Main()) }

func run(args []string, stdout, stderr io.Writer) int {
	return runWith(newEnv(), args, stdout, stderr)
}

func runWith(e env, args []string, stdout, stderr io.Writer) int {
	return novaCheckWith(e).Run(args, os.Stdin, stdout, stderr)
}

// novaCheck is the tool: its verbs and their flags, run by internal/tool.
func novaCheck() *tool.Tool {
	return novaCheckWith(newEnv())
}

func novaCheckWith(e env) *tool.Tool {
	return &tool.Tool{
		Name:      "nova-check",
		What:      "checks over markdown records and repositories, each finding named by file and line",
		Stamp:     version,
		ExitTable: "0 pass, 1 check failed, 2 could not run (bad invocation)\n\nsetup:\n  mkdir -p ./self/docs\n  printf '# Kernel\\n' > ./self/docs/SEED-CORE.md",
		How: "verbs inspect named paths; spelling --write edits files in place.\n" +
			"(--dry-run writes nothing). attest, kernel, floors, and corpus read a seed's files.\n" +
			"links, quickstart, and nocode inspect any markdown tree; dogfood/convergence are in nova-dev.\n" +
			"first run: create the small markdown tree below, then run the example commands.",
		UsageNote: "  nova-check <verb> -h, nova-check help <verb>   the verb's flags, its effect and exit codes",
		Verbs: []tool.Verb{
			{
				Name:    "quickstart",
				Usage:   "quickstart --dir <dir> [--exclude <prefix>] [--max <n>]",
				Example: "quickstart --dir ./self",
				Effect:  tool.Effect("inspection: reads markdown under --dir, runs links then nocode, writes nothing"),
				Flags: func(f *tool.Flags) {
					f.Prints()
					f.Required("dir", dirHint)
					addMax(f)
					f.Var(&repeatable{}, "exclude", "path prefix not scanned by links (repeatable; empty by default)")
				},
				Run: func(c *tool.Call) *tool.Out { return quickstart(c, e) },
			},
			{
				Name:   "attest",
				Usage:  "attest --home <dir> --manifest <file> [--max <n>]",
				Effect: tool.Effect("inspection: reads the manifest and the files it names, writes nothing"),
				Flags: func(f *tool.Flags) {
					f.Required("home", homeHint)
					f.Required("manifest", manifestHint)
					addMax(f)
				},
				Run: func(c *tool.Call) *tool.Out { return attest(c, e) },
			},
			{
				Name:    "links",
				Usage:   "links --dir <dir> [--file <path>] [--exclude <prefix>] [--max <n>]",
				Example: "links --dir ./self",
				Effect:  tool.Effect("inspection: reads the markdown under --dir, writes nothing"),
				Flags:   linksFlags,
				Run:     func(c *tool.Call) *tool.Out { return cmdLinks(c, e) },
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
				Run: func(c *tool.Call) *tool.Out { return kernel(c, e) },
			},
			{
				Name:   "nocode",
				Usage:  "nocode --dir <dir> [--staged] [--allow <prefix>] [--deny-ext <l|@f>] [--deny-ext-add <l|@f>] [--print-deny-list] [--max <n>]",
				Effect: tool.Effect("inspection: reads the tree, or with --staged the git index, writes nothing"),
				Flags: func(f *tool.Flags) {
					f.String("dir", "", dirHint+" (required)")
					f.Bool("staged", false, "advisory over the index: classify what is about to be committed, not the working tree (--dir is the repository root)")
					f.Var(&repeatable{}, "allow", "path prefix where machinery may live (repeatable; empty by default)")
					f.String("deny-ext", "", "replace the floor EXTENSION list (not the name floor): comma list, or @file")
					f.String("deny-ext-add", "", "extend the floor EXTENSION list (not the name floor): comma list, or @file")
					f.Bool("print-deny-list", false, "print both floors in force (extensions and names) and exit 0")
					addMax(f)
					f.Check(func(c *tool.Call) {
						if !c.Bool("print-deny-list") {
							c.Want("dir", dirHint)
						}
					})
				},
				Run: func(c *tool.Call) *tool.Out { return nocode(c, e) },
			},
			{
				Name:   "floors",
				Usage:  "floors --core <docs/SEED-CORE.md> --source <docs/SEED.md>",
				Effect: tool.Effect("inspection: reads the two files, writes nothing"),
				Flags: func(f *tool.Flags) {
					f.Required("core", coreHint)
					f.Required("source", sourceHint)
				},
				Run: func(c *tool.Call) *tool.Out { return floors(c, e) },
			},
			{
				Name:   "corpus",
				Usage:  "corpus --ledger <file> --root <dir> --min-anchors <n> [--max <n>]",
				Effect: tool.Effect("inspection: reads the ledger and the files it names, writes nothing"),
				Flags: func(f *tool.Flags) {
					f.Required("ledger", ledgerHint)
					f.Required("root", rootHint)
					f.Int("min-anchors", 0, "the fewest rows the ledger may hold, must be positive (required); the ledger is inside what it protects, so its own shrinking must be red")
					addMax(f)
					f.Check(func(c *tool.Call) {
						if !c.Given("min-anchors") {
							c.Problem(fmt.Sprintf("--min-anchors is required; it wants %s; refusing to guess", anchorsHint))
						} else if c.Int("min-anchors") <= 0 {
							c.Problem(fmt.Sprintf("--min-anchors must be a positive row floor (got %d); a floor of zero guards nothing, which is what an empty ledger already is; refusing to guess", c.Int("min-anchors")))
						}
					})
				},
				Run: func(c *tool.Call) *tool.Out { return corpus(c, e) },
			},
			{
				Name:   "spelling",
				Usage:  "spelling (--dir <dir> | --file <path> | --path <pattern>) [--ignore <word|@file>] [--write] [--exclude <prefix>] [--max <n>] [--dry-run]",
				Effect: tool.Effect("local write: --write edits the files in place (--dry-run, or no --write, writes nothing)"),
				DryRun: true,
				Flags:  spellingFlags,
				Run:    func(c *tool.Call) *tool.Out { return cmdSpelling(c, e) },
			},
		},
	}
}

func linksFlags(f *tool.Flags) {
	f.Required("dir", dirHint)
	f.Var(&repeatable{}, "file", "one markdown file to scan, narrowing the walk to just these (repeatable; --dir is still the resolution root)")
	f.Var(&repeatable{}, "exclude", "path prefix not scanned, and links into it not checked (repeatable; empty by default)")
	addMax(f)
}

func cmdLinks(c *tool.Call, e env) *tool.Out {
	return linksOut(e, c.Str("dir"), c.Get("file").([]string), c.Get("exclude").([]string), c.Int("max"))
}

// repeatable collects a flag given more than once. Every scope narrowing is
// the caller's, stated per run, and starts empty.
type repeatable []string

func (r *repeatable) String() string { return strings.Join(*r, ",") }
func (r *repeatable) Set(v string) error {
	*r = append(*r, v)
	return nil
}

// Get returns the collected values, so the flag satisfies flag.Getter and the
// skeleton's Call.Get can read it.
func (r *repeatable) Get() any { return []string(*r) }
