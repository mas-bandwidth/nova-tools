// Command nova-card generates a directory of pre-linted briefs from a structured
// source, ready for one `nova-sprint add --brief-dir`. The planning is
// internal/cardgen (pure functions over text, docs/SPEC-CARD-CONTRACT.md,
// "generated cards"); this file is the transport: it reads the source from a
// checkout, resolves the base, runs the lint over every brief and writes the
// directory, or refuses to with the red line printed.
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardgen"
	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

const preAlpha = "nova-card is pre-alpha: not ready for production use."

const usage = `nova-card: writes a directory of pre-linted briefs from a ledger, a findings file or a tool's help
` + preAlpha + `

how it works: a source is read from a checkout of the target repository (a ratchet ledger of
internal/ci, a findings TSV, a tool's rendered help); the planner cuts one card per file with
its PATHS, TEST and tier computed from the row, puts cards that edit one ledger in alternating
waves, and holds every brief to the lint nova-sprint add runs before the directory is written.
State: none; the directory, its manifest.tsv and the one CARDS OK line are the whole result.

the flow, three lines:
  nova-card generate --from ledger --ledger serial-tests --repo-dir ./repo --out ./cards
  nova-sprint add --stream debt --brief-dir ./cards --allow-shared-paths
  nova-sprint where

usage:
  nova-card generate --from ledger --ledger <name> --repo-dir <dir> --out <dir> [--tier flash|pro] [--prefix <p>] [--minutes <n>] [--max <n>] [--base <branch>] [--repo <owner/name>] [--dry-run]
  nova-card generate --from findings --file <tsv> --out <dir> (--repo-dir <dir> | --repo <owner/name> --base <branch> --sha <40hex>) [--tier flash|pro] [--prefix <p>] [--minutes <n>]
  nova-card generate --from help --tool <name> [--tool <name>...] --out <dir> [--bin-dir <dir>] (--repo-dir <dir> | --repo --base --sha) [--tier flash|pro]
  nova-card lint --card <file> [--card <file>...]
  nova-card lint --card ./cards/finding-cmd-nova-bus-main.md
  nova-card version
  nova-card help [<verb>]

generate reads the repository, the branch and the base sha from --repo-dir (its origin URL,
its branch, its HEAD); --repo, --base and --sha each override one, and all three together
need no checkout. With a checkout every PATHS entry is checked to exist at it, so a card never
names a path the add would reject. The ledgers: ` + "`nova-card generate -h`" + ` lists them.
A ledger card is flash and a findings or help card is pro unless --tier says otherwise.
Cards of one ordinary ledger alternate waves (odd rows wave 1, even rows wave 2 depending on
their neighbours) because adjacent deletions conflict at land; a generated ledger
(docs/SPEC-SPRINT.md section 7) gets one wave and no dependency. Wave 1 cards of one ledger
share its path, so the add wants --allow-shared-paths; the CARDS line says so.
lint holds a brief to what the add holds it to (the model lines, the child rules, the typed
header, a tree card's steps) and to the template's unfilled <...> lines, one LINT DRIFT line
each. template prints nova-swarm's card template, the shape every generated brief has.

what it prints:
  CARDS OK dir=<dir> cards=<n> waves=<k> tier=<t> [shared-paths=yes]   then manifest.tsv in <dir> (--dry-run: the manifest on stdout, dry-run=yes)
  CARDS NOTE <what was skipped: a row the ledger did not read, a tool with no help>
  LINT DRIFT card=<id> check=<check> line=<n>: <excerpt>              and nothing is written
  LINT OK file=<file>

exit codes: 0 done; 1 a brief is red, named on its LINT DRIFT line, and nothing was written;
2 could not run: a missing flag, a source that cannot be read, a checkout with no HEAD

example:
  nova-card generate --from findings --file ./cmd/nova-card/testdata/findings.tsv --repo example/repo --base dev --sha 0123456789abcdef0123456789abcdef01234567 --out ./cards
  nova-card lint --card ./cards/finding-internal-bus-send.md
  nova-card lint --card ./cards/finding-cmd-nova-bus-main.md
`

var verbs = []string{"generate", "lint", "template", "version", "help"}

// effects is each verb's effect line for its -h (docs/CLI-STYLE.md rule (b)).
var effects = map[string]string{
	"generate": "local write: creates --out and writes one .md per card and manifest.tsv into it; nothing when a brief is red; --dry-run plans, lints and prints the manifest, and writes nothing",
	"lint":     "inspection: reads, writes nothing",
	"template": "inspection: prints the card template, writes nothing",
	"version":  "inspection: prints the build identity",
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// refuse is one line on stderr, `nova-card[ <verb>] REFUSED: <what>; run: nova-card help[ <verb>]`,
// exit 2 (docs/STANDARD.md section 3).
func refuse(stderr io.Writer, verb, what string) int {
	where, door := "nova-card", "nova-card help"
	if verb != "" {
		where += " " + verb
		door += " " + verb
	}
	fmt.Fprintf(stderr, "%s REFUSED: %s; run: %s\n", where, oneline.Escape(what), door)
	return 2
}

func run(args []string, stdout, stderr io.Writer) (code int) {
	defer verbflag.RecoverWith(stdout, "nova-card", usage, &code, func(verb string) string {
		return "effect: " + effects[verb] + "\n"
	})
	if len(args) == 0 {
		return refuse(stderr, "", "no verb; one of "+strings.Join(verbs, ", "))
	}
	switch args[0] {
	case "help", "-h", "-help", "--help":
		if args[0] == "help" && len(args) > 1 && slices.Contains(verbs, args[1]) && args[1] != "help" {
			return run([]string{args[1], "-h"}, stdout, stderr)
		}
		fmt.Fprint(stdout, usage)
		return 0
	case "version", "--version":
		verbflag.HelpIfAsked(args[1:], "version")
		return cmdVersion(args[1:], stdout, stderr)
	case "template":
		verbflag.HelpIfAsked(args[1:], "template")
		if len(args) > 1 {
			return refuse(stderr, "template", "takes no flags and no arguments")
		}
		text, err := swarm.Template("card")
		if err != nil {
			return refuse(stderr, "template", err.Error())
		}
		fmt.Fprint(stdout, text)
		return 0
	case "lint":
		return cmdLint(args[1:], stdout, stderr)
	case "generate":
		return cmdGenerate(args[1:], stdout, stderr)
	}
	return refuse(stderr, "", fmt.Sprintf("unknown verb %q; one of %s", args[0], strings.Join(verbs, ", ")))
}

// multi is a repeatable string flag.
type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

func cmdLint(args []string, stdout, stderr io.Writer) int {
	fs := verbflag.New("lint")
	var cards multi
	fs.Var(&cards, "card", "a brief `file` to hold to the add's lint; repeat for more")
	if err := verbflag.Parse(fs, args); err != nil {
		return refuse(stderr, "lint", verbflag.Explain(fs, err))
	}
	if len(cards) == 0 || fs.NArg() > 0 {
		return refuse(stderr, "lint", "wants --card <file>, one per brief, and nothing else")
	}
	red := 0
	for _, file := range cards {
		raw, err := os.ReadFile(file)
		if err != nil {
			return refuse(stderr, "lint", "cannot read "+file+": "+err.Error())
		}
		id := strings.TrimSuffix(filepath.Base(file), ".md")
		findings := cardgen.Lint(id, string(raw))
		for _, f := range findings {
			fmt.Fprintln(stdout, oneline.Escape(f.String()))
		}
		if len(findings) > 0 {
			red++
			continue
		}
		fmt.Fprintf(stdout, "LINT OK file=%s\n", oneline.Field(file))
	}
	if red > 0 {
		return 1
	}
	return 0
}

func cmdGenerate(args []string, stdout, stderr io.Writer) int {
	fs := verbflag.New("generate")
	from := fs.String("from", "", "the source `kind`: ledger, findings or help")
	ledger := fs.String("ledger", "", "with --from ledger: the ledger's `name`, one of "+strings.Join(cardgen.LedgerNames(), ", "))
	file := fs.String("file", "", "with --from findings: the TSV `file` of file:line, finding, remedy, test (a header row is skipped)")
	var tools multi
	fs.Var(&tools, "tool", "with --from help: a tool `name` whose help the card is about; repeat for more")
	binDir := fs.String("bin-dir", "", "with --from help: the `dir` holding the tools' binaries (default: PATH)")
	repoDir := fs.String("repo-dir", "", "a checkout `dir` of the target repository at the base: the source is read from it, the repository, branch and sha are read off it, and every PATHS entry is checked to exist in it")
	repo := fs.String("repo", "", "the `owner/name` the REPO: line carries (default: --repo-dir's origin)")
	base := fs.String("base", "", "the `branch` the BASE: line carries (default: --repo-dir's branch)")
	sha := fs.String("sha", "", "the base `sha`, 40 hex (default: --repo-dir's HEAD)")
	out := fs.String("out", "", "the `dir` the briefs and manifest.tsv are written into; created, and refused when it already holds a brief")
	tier := fs.String("tier", "", "flash or pro; default by source: a ledger's own (mechanical ledgers flash, decisions pro), findings and help pro")
	prefix := fs.String("prefix", "", "the `word` every card id opens with (default: the ledger's name, finding, or help)")
	minutes := fs.Int("minutes", 0, "the Deadline line's `minutes` (default: 45 flash, 60 pro)")
	maxCards := fs.Int("max", 0, "write at most this many cards, in source order; 0 is all")
	dryRun := fs.Bool("dry-run", false, "plan and lint, print the manifest and the CARDS line, and write nothing")
	if err := verbflag.Parse(fs, args); err != nil {
		return refuse(stderr, "generate", verbflag.Explain(fs, err))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, "generate", fmt.Sprintf("unexpected argument %q; every input is a flag", fs.Arg(0)))
	}
	if *out == "" {
		return refuse(stderr, "generate", "wants --out <dir>")
	}
	if *tier != "" && *tier != "flash" && *tier != "pro" {
		return refuse(stderr, "generate", fmt.Sprintf("--tier %q; want flash or pro", *tier))
	}
	// the header: from the checkout, each line overridable
	h := cardgen.Header{Repo: *repo, Base: *base, Sha: *sha, Minutes: *minutes}
	if *repoDir != "" {
		if err := readCheckout(*repoDir, &h); err != nil {
			return refuse(stderr, "generate", err.Error())
		}
	}
	switch {
	case h.Repo == "":
		return refuse(stderr, "generate", "no repository: --repo <owner/name>, or --repo-dir a checkout whose origin names one")
	case h.Base == "":
		return refuse(stderr, "generate", "no base branch: --base <branch>, or --repo-dir a checkout on a branch (a detached HEAD names none)")
	case !shaRE.MatchString(h.Sha):
		return refuse(stderr, "generate", fmt.Sprintf("base sha %q is not 40 hex: --sha <40hex>, or --repo-dir a checkout with a HEAD", h.Sha))
	}
	var plan cardgen.Plan
	var notes []string
	switch *from {
	case "ledger":
		l, ok := cardgen.Ledgers[*ledger]
		if !ok {
			return refuse(stderr, "generate", fmt.Sprintf("--ledger %q is not a ledger this build knows; one of %s", *ledger, strings.Join(cardgen.LedgerNames(), ", ")))
		}
		if *repoDir == "" {
			return refuse(stderr, "generate", "--from ledger reads the ledger from --repo-dir <dir>, a checkout of the repository at the base")
		}
		raw, err := os.ReadFile(filepath.Join(*repoDir, filepath.FromSlash(l.File)))
		if err != nil {
			return refuse(stderr, "generate", "cannot read the ledger "+l.File+" in "+*repoDir+": "+err.Error())
		}
		rows, skipped := cardgen.ParseLedger(l, string(raw))
		notes = append(notes, skipped...)
		plan = cardgen.PlanLedger(l, rows, *prefix, *tier, *maxCards)
	case "findings":
		if *file == "" {
			return refuse(stderr, "generate", "--from findings wants --file <tsv>")
		}
		raw, err := os.ReadFile(*file)
		if err != nil {
			return refuse(stderr, "generate", "cannot read "+*file+": "+err.Error())
		}
		findings, skipped := cardgen.ParseFindings(string(raw))
		notes = append(notes, skipped...)
		plan = cardgen.PlanFindings(findings, *prefix, *tier, *maxCards)
	case "help":
		if len(tools) == 0 {
			return refuse(stderr, "generate", "--from help wants --tool <name>, one per tool")
		}
		plan = cardgen.Plan{Tier: *tier, Waves: 1}
		if plan.Tier == "" {
			plan.Tier = "pro"
		}
		if *maxCards > 0 && len(tools) > *maxCards {
			tools = tools[:*maxCards]
		}
		for _, tool := range tools {
			help, err := renderedHelp(*binDir, tool)
			if err != nil {
				notes = append(notes, tool+": "+err.Error())
				continue
			}
			plan.Cards = append(plan.Cards, cardgen.PlanHelp(tool, help, *prefix, *tier))
		}
	case "":
		return refuse(stderr, "generate", "wants --from ledger|findings|help")
	default:
		return refuse(stderr, "generate", fmt.Sprintf("--from %q; want ledger, findings or help", *from))
	}
	if len(plan.Cards) == 0 {
		return refuse(stderr, "generate", "the source yields no card; nothing to write")
	}
	// render, lint, and check the paths against the checkout: nothing is written while
	// one brief is red
	briefs := make([]string, len(plan.Cards))
	red := 0
	for i, c := range plan.Cards {
		briefs[i] = cardgen.Render(h, c)
		for _, f := range cardgen.Lint(c.ID, briefs[i]) {
			fmt.Fprintln(stdout, oneline.Escape(f.String()))
			red++
		}
		if *repoDir != "" {
			for _, p := range c.Paths {
				if !existsAt(*repoDir, p) {
					fmt.Fprintln(stdout, oneline.Escape(cardgen.LintFinding{ID: c.ID, Check: "paths-at-base", Line: 6, Excerpt: "PATHS entry " + p + " names nothing in " + *repoDir}.String()))
					red++
				}
			}
		}
	}
	if red > 0 {
		fmt.Fprintf(stderr, "nova-card generate FAILED: %d red line(s) above; nothing written to %s\n", red, oneline.Field(*out))
		return 1
	}
	if *dryRun {
		for _, n := range notes {
			fmt.Fprintf(stdout, "CARDS NOTE skipped %s\n", oneline.Escape(n))
		}
		fmt.Fprint(stdout, cardgen.Manifest(plan))
		fmt.Fprintln(stdout, cardgen.OKLine(*out, plan)+" dry-run=yes (nothing written)")
		return 0
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return refuse(stderr, "generate", "cannot create --out "+*out+": "+err.Error())
	}
	if held, _ := filepath.Glob(filepath.Join(*out, "*.md")); len(held) > 0 {
		return refuse(stderr, "generate", fmt.Sprintf("--out %s already holds %d brief(s); name an empty directory", *out, len(held)))
	}
	for i, c := range plan.Cards {
		if err := os.WriteFile(filepath.Join(*out, c.ID+".md"), []byte(briefs[i]), 0o644); err != nil {
			return refuse(stderr, "generate", "cannot write "+c.ID+".md: "+err.Error())
		}
	}
	if err := os.WriteFile(filepath.Join(*out, "manifest.tsv"), []byte(cardgen.Manifest(plan)), 0o644); err != nil {
		return refuse(stderr, "generate", "cannot write manifest.tsv: "+err.Error())
	}
	for _, n := range notes {
		fmt.Fprintf(stdout, "CARDS NOTE skipped %s\n", oneline.Escape(n))
	}
	line := cardgen.OKLine(*out, plan)
	if plan.Shared {
		line += " shared-paths=yes (add with --allow-shared-paths)"
	}
	fmt.Fprintln(stdout, line)
	return 0
}

var shaRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

// readCheckout fills the header's empty fields from a checkout: the repository from
// origin's URL, the branch from HEAD's name, the sha from HEAD.
func readCheckout(dir string, h *cardgen.Header) error {
	git := func(args ...string) (string, error) {
		res, err := gitrun.Run(context.Background(), gitrun.Options{C: dir}, args...)
		if err != nil {
			return "", fmt.Errorf("git %s in %s: %s", strings.Join(args, " "), dir, strings.TrimSpace(cmp(string(res.Stderr), err.Error())))
		}
		return strings.TrimSpace(string(res.Stdout)), nil
	}
	if h.Sha == "" {
		sha, err := git("rev-parse", "HEAD")
		if err != nil {
			return err
		}
		h.Sha = sha
	}
	if h.Base == "" {
		// a detached HEAD names no branch: symbolic-ref exits 1 and Base stays "",
		// refused by the caller (internal/bus/git.go CurrentBranch reads it the same way)
		if branch, err := git("symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
			h.Base = branch
		}
	}
	if h.Repo == "" {
		url, err := git("remote", "get-url", "origin")
		if err == nil {
			h.Repo = repoOfURL(url)
		}
	}
	return nil
}

func cmp(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

var repoURLRE = regexp.MustCompile(`[:/]([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+?)(?:\.git)?/?$`)

// repoOfURL is the owner/name of a forge URL, ssh or https; "" when it has none.
func repoOfURL(url string) string {
	m := repoURLRE.FindStringSubmatch(strings.TrimSpace(url))
	if m == nil {
		return ""
	}
	return m[1]
}

// existsAt says whether a PATHS entry (a file or a glob, repository-relative) names
// at least one file in the checkout.
func existsAt(dir, p string) bool {
	matches, err := filepath.Glob(filepath.Join(dir, filepath.FromSlash(p)))
	return err == nil && len(matches) > 0
}

// renderedHelp runs `<tool> help` and returns what it printed.
func renderedHelp(binDir, tool string) (string, error) {
	bin := tool
	if binDir != "" {
		bin = filepath.Join(binDir, tool)
	}
	cmd, cancel := subproc.Command(context.Background(), subproc.Tool, bin, "help")
	defer cancel()
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil && out.Len() == 0 {
		return "", fmt.Errorf("`%s help` printed nothing: %v", bin, err)
	}
	return out.String(), nil
}
