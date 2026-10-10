package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/check"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// addMax puts the same ceiling on every verb that lists findings, so a reader
// learns one flag and not five: the skeleton's --max, which refuses a negative
// number (zero already means all, and a negative ceiling is a typo with two
// readings), and the flag's old spelling --fail-max, registered on the same
// value and accepted for one release. withAlias says when a run spelled it.
func addMax(f *tool.Flags) {
	f.Max()
	f.Var(f.Lookup("max").Value, "fail-max", "the old spelling of --max, accepted for one release; it sets the same value")
}

// withAlias adds the note a run owes when it spelled the ceiling --fail-max.
func withAlias(run func(c *tool.Call) *tool.Out) func(c *tool.Call) *tool.Out {
	return func(c *tool.Call) *tool.Out {
		o := run(c)
		if c.Given("fail-max") {
			o.Note("--fail-max is --max")
		}
		return o
	}
}

// aliasNote is withAlias for a verb that prints its own lines (Prints), whose
// Out carries no notes: the note goes to stderr as the line it always was.
func aliasNote(c *tool.Call) {
	if c.Given("fail-max") {
		fmt.Fprintln(c.Stderr, "NOTE --fail-max is --max")
	}
}

// verdict is the answer of a check that returns failures: OK when there are
// none, FAILED with one `finding` item per failure, its subject and its reason.
// The caller adds the facts of its verb, then the count the verdict shows.
func verdict(failures []check.Failure) *tool.Out {
	if len(failures) == 0 {
		return tool.Done()
	}
	o := tool.Fail()
	for _, f := range failures {
		o.Item("finding", "subject", f.Subject, "reason", tool.Text(f.Reason))
	}
	return o
}

// quickstartVerb is the first run: the two checks that need nothing but a
// directory, in one command, so that a stranger's first invocation is a line
// they can type from the usage banner rather than a choice between six verbs
// and the flags each of them wants. It adds no check of its own: it runs
// links and then nocode, and both run even when the first says NO, because a
// first run should learn everything this pair can tell it in one go. The caps
// are inherited: a first run on a repo nobody has checked should cost about
// forty lines, not a thousand. It prints its own lines (Prints): the RUN line
// that opens it, the OK or FAILED line that closes it, and the lines of the two
// checks it delegates to, each rendered by Out.Render.
func quickstartVerb() tool.Verb {
	return tool.Verb{
		Name:      "quickstart",
		Usage:     "quickstart --dir <dir> [--exclude <prefix>] [--max <n>]",
		Example:   "quickstart --dir ./self",
		Effect:    tool.Effect("inspection: reads markdown under --dir, runs links then nocode, writes nothing"),
		Detail:    "Both checks run even if the first says NO.",
		ExitTable: exitCodes,
		Flags: func(f *tool.Flags) {
			f.Prints()
			f.Required("dir", dirHint)
			addMax(f)
			f.Var(&repeatable{}, "exclude", "path prefix not scanned by links (repeatable; empty by default)")
		},
		Run: quickstart,
	}
}

func quickstart(c *tool.Call) *tool.Out {
	aliasNote(c)
	dir := c.Str("dir")
	deny, source, err := effectiveDenyList("", "")
	if err != nil {
		return tool.Refuse(oneline.Err(err))
	}
	fmt.Fprintf(c.Stdout, "QUICKSTART RUN dir=%s checks=2: links, then nocode\n", oneline.Field(dir))
	lo := linksOut(dir, nil, c.Get("exclude").([]string))
	no := nocodeOut(dir, nil, deny, source)
	writeOut(c, lo)
	writeOut(c, no)
	worst := max(lo.Exit, no.Exit)
	var failed []string
	for _, p := range []struct {
		name string
		code int
	}{{"links", lo.Exit}, {"nocode", no.Exit}} {
		if p.code != 0 {
			failed = append(failed, p.name)
		}
	}
	// The closing line is printed on every outcome. The OK word is a claim that
	// both checks passed, so it is printed only then. What comes next depends on
	// the outcome: after a pass, the checks that want an input of yours; after a
	// failure or a refusal, the failed check run alone, which is the one to fix
	// before anything else.
	if len(failed) == 0 {
		fmt.Fprintf(c.Stdout, "QUICKSTART OK done=2 worst-exit=%d next=kernel,attest,floors,corpus (kernel wants a size budget, attest a manifest of what a full boot reads, floors a derived copy and its source, corpus a ledger of protected lines: nova-check help)\n", worst)
	} else {
		fmt.Fprintf(c.Stdout, "QUICKSTART FAILED checks=2 failed=%s worst-exit=%d next=%s (fix what it names, then run quickstart again)\n",
			oneline.Field(strings.Join(failed, ",")), worst, oneline.Escape("nova-check "+failed[0]+" --dir "+oneline.ShellWord(dir)))
	}
	return tool.Exit(worst)
}

// writeOut renders one delegated check's Out to the stream its status belongs
// on: a run that said no writes to stderr, a clean one to stdout. A refusal
// carries the door the skeleton gives every refusal, nova-check help, because
// this Out is rendered here and not by Tool.Run.
func writeOut(c *tool.Call, o *tool.Out) {
	o.Cap(c.Int("max"))
	if o.Status == tool.Refused && o.Remedy == "" {
		o.Remedy = "nova-check help"
	}
	w := c.Stdout
	if o.Exit != 0 {
		w = c.Stderr
	}
	o.Render(w, false)
}

func attestVerb() tool.Verb {
	return tool.Verb{
		Name:      "attest",
		Usage:     "attest --home <dir> --manifest <file> [--max <n>]",
		Effect:    tool.Effect("inspection: reads the manifest and the files it names, writes nothing"),
		Detail:    "A manifest lists one path per line relative to --home (blank lines and # comments ignored).",
		ExitTable: exitCodes,
		Flags: func(f *tool.Flags) {
			f.Required("home", homeHint)
			f.Required("manifest", manifestHint)
			addMax(f)
		},
		Run: withAlias(attest),
	}
}

func attest(c *tool.Call) *tool.Out {
	home, manifest := c.Str("home"), c.Str("manifest")
	att, failures, err := check.Attest(home, manifest)
	if err != nil {
		return tool.Refuse(oneline.Err(err))
	}
	return verdict(failures).Fact("home", home).Fact("manifest", manifest).Fact("files", att.Files).
		Fact("bytes", att.Bytes).Fact("sha256", att.SHA256).Fact("findings", len(failures))
}

func linksVerb() tool.Verb {
	return tool.Verb{
		Name:      "links",
		Usage:     "links --dir <dir> [--file <path>] [--exclude <prefix>] [--max <n>]",
		Example:   "links --dir ./self",
		Effect:    tool.Effect("inspection: reads the markdown under --dir, writes nothing"),
		Detail:    "Every relative md link resolves; findings are relative to --dir.",
		ExitTable: exitCodes,
		Flags:     linksFlags,
		Run:       withAlias(links),
	}
}

func links(c *tool.Call) *tool.Out {
	return linksOut(c.Str("dir"), c.Get("file").([]string), c.Get("exclude").([]string))
}

// linksOut runs the links check and returns its Out, shared with quickstart so
// a first run inherits the same lines. A whole-file finding (the .md itself
// could not be read) has no line and no target: a named failure like any other,
// per SPEC, not a refusal.
func linksOut(dir string, files, exclude []string) *tool.Out {
	var (
		res check.LinksResult
		err error
	)
	if len(files) > 0 {
		res, err = check.LinksFiles(dir, files, exclude)
	} else {
		res, err = check.LinksExcluding(dir, exclude)
	}
	var o *tool.Out
	switch {
	case err != nil:
		o = tool.Refuse(oneline.Err(err))
	case len(res.Broken) > 0:
		o = tool.Fail()
		for _, b := range res.Broken {
			o.Item("broken", "file", b.File, "line", b.Line, "target", b.Target, "reason", tool.Text(b.Reason))
		}
	default:
		o = tool.Done()
	}
	o.Verb = "links"
	if err == nil {
		o.Fact("dir", dir).Fact("files", res.MDFiles).Fact("links", res.Checked).Fact("excluded", res.Excluded).Fact("broken", len(res.Broken))
	}
	return o
}

func kernelVerb() tool.Verb {
	return tool.Verb{
		Name: "kernel",
		Usage: "kernel --file <file> --max-bytes <n>\n" +
			"kernel --file <file> --max-tokens <n> --bytes-per-token <r>",
		Example:   "kernel --file ./self/docs/SEED-CORE.md --max-bytes 4000",
		Effect:    tool.Effect("inspection: reads the one file, writes nothing"),
		Detail:    "The kernel size budget, in bytes or in tokens; findings use the --file path as given.",
		ExitTable: exitCodes,
		Flags: func(f *tool.Flags) {
			f.Required("file", fileHint)
			f.Int64("max-bytes", 0, "size budget in bytes, must be positive (one of --max-bytes / --max-tokens)")
			f.Int64("max-tokens", 0, "size budget in tokens, must be positive (one of --max-bytes / --max-tokens)")
			f.Float64("bytes-per-token", 0, "measured bytes per token, required with --max-tokens; no default")
			f.Check(kernelCheck)
		},
		Run: kernel,
	}
}

// kernelCheck holds the kernel verb's budget rules over the parsed flags, so one
// run names every problem at once: the file and the budget are independent, so
// `nova-check kernel` with nothing at all names --file and the budget together.
// Which budget was GIVEN counts, not which value survived: --max-bytes 0 is a
// stated (and refused) budget, not an absent one.
func kernelCheck(c *tool.Call) {
	switch {
	case c.Given("max-bytes") && c.Given("max-tokens"):
		c.Problem("give exactly one of --max-bytes or --max-tokens, not both; the line names the unit, the tool does not pick; " + budgetHint)
	case !c.Given("max-bytes") && !c.Given("max-tokens"):
		c.Problem("--max-bytes or --max-tokens is required; it wants " + budgetHint + "; refusing to guess")
	}
	if c.Given("bytes-per-token") && c.Given("max-bytes") {
		c.Problem("--bytes-per-token applies only to --max-tokens; a divisor with a byte budget means one of the two is not what you meant; " + budgetHint)
	}
	if c.Given("max-tokens") {
		perToken := c.Get("bytes-per-token").(float64)
		if !c.Given("bytes-per-token") {
			c.Problem("--max-tokens requires --bytes-per-token; the divisor is a measurement you make on your own writing, and there is no default; refusing to guess; " + budgetHint)
		} else if perToken <= 0 {
			c.Problem(fmt.Sprintf("--bytes-per-token must be a positive ratio (got %g); refusing to guess; %s", perToken, budgetHint))
		}
		if n := c.Get("max-tokens").(int64); n <= 0 {
			c.Problem(fmt.Sprintf("--max-tokens must be a positive token budget (got %d); refusing to guess; %s", n, budgetHint))
		}
	}
	if c.Given("max-bytes") && !c.Given("max-tokens") {
		if n := c.Get("max-bytes").(int64); n <= 0 {
			c.Problem(fmt.Sprintf("--max-bytes must be a positive byte budget (got %d); refusing to guess", n))
		}
	}
}

func kernel(c *tool.Call) *tool.Out {
	file := c.Str("file")
	if c.Given("max-tokens") {
		budget, perToken := c.Get("max-tokens").(int64), c.Get("bytes-per-token").(float64)
		measured, tokens, failures, err := check.KernelTokens(file, budget, perToken)
		if err != nil {
			return tool.Refuse(oneline.Err(err))
		}
		// The OK line teaches the unit it enforced: tokens first, then the
		// bytes and the divisor they were derived from, so the number can be
		// re-derived by anyone reading the line.
		return verdict(failures).Fact("file", file).Fact("tokens", tokens).Fact("budget", budget).
			Fact("bytes", measured).Fact("divisor", perToken).Fact("findings", len(failures))
	}
	budget := c.Get("max-bytes").(int64)
	measured, failures, err := check.Kernel(file, budget)
	if err != nil {
		return tool.Refuse(oneline.Err(err))
	}
	return verdict(failures).Fact("file", file).Fact("bytes", measured).Fact("budget", budget).Fact("findings", len(failures))
}

func nocodeVerb(s seams) tool.Verb {
	return tool.Verb{
		Name: "nocode",
		Usage: "nocode --dir <dir> [--allow <prefix>] [--deny-ext <l|@f>] [--deny-ext-add <l|@f>] [--max <n>]\n" +
			"nocode --print-deny-list [--deny-ext <l|@f>] [--deny-ext-add <l|@f>]\n" +
			"nocode --staged --dir <repo> [--allow <prefix>] [--deny-ext <l|@f>] [--deny-ext-add <l|@f>] [--max <n>]",
		Effect: tool.Effect("inspection: reads the tree, or with --staged the git index, writes nothing"),
		Detail: "No code files in a self repo, by two floors: an EXTENSION list, and a NAME list for build machinery named\n" +
			"or located rather than extensioned (Makefile, .github/workflows/). The --deny-ext flags govern the\n" +
			"EXTENSION list only; --allow is the escape for the name floor, and names where machinery may live.\n" +
			"--staged is an advisory over the index: it classifies what is about to be committed, by the same rules\n" +
			"the audit walks the tree with; --dir is then the repository root, required.",
		ExitTable: exitCodes,
		Flags: func(f *tool.Flags) {
			f.String("dir", "", dirHint+" (required)")
			f.Bool("staged", false, "advisory over the index: classify what is about to be committed, not the working tree (--dir is the repository root)")
			f.Var(&repeatable{}, "allow", "path prefix where machinery may live (repeatable; empty by default)")
			f.String("deny-ext", "", "replace the floor EXTENSION list (not the name floor): comma list, or @file")
			f.String("deny-ext-add", "", "extend the floor EXTENSION list (not the name floor): comma list, or @file")
			f.Bool("print-deny-list", false, "print both floors in force (extensions and names) and exit 0")
			addMax(f)
			f.Check(func(c *tool.Call) {
				if c.Str("deny-ext") != "" && c.Str("deny-ext-add") != "" {
					c.Problem("--deny-ext and --deny-ext-add are mutually exclusive")
				}
				if !c.Bool("print-deny-list") {
					c.Want("dir", dirHint)
				}
			})
		},
		Run: withAlias(func(c *tool.Call) *tool.Out { return nocode(c, s.staged) }),
	}
}

func nocode(c *tool.Call, seams stagedSeams) *tool.Out {
	dir := c.Str("dir")
	allow := c.Get("allow").([]string)
	// Resolve the effective deny-list and its provenance before anything else:
	// a guard that cannot say what it forbids must refuse, not pass.
	deny, source, err := effectiveDenyList(c.Str("deny-ext"), c.Str("deny-ext-add"))
	if err != nil {
		return tool.Refuse(oneline.Err(err))
	}
	if c.Bool("print-deny-list") {
		// The NAME floor is printed alongside the extension list because this
		// flag's whole job is to print what is actually in force. A floor that
		// fires but does not appear here would be exactly the hidden default
		// the deny-list is defended against being.
		names, prefixes, nerr := check.FloorDenyNames()
		if nerr != nil {
			return tool.Refuse(oneline.Err(nerr))
		}
		if c.Bool("json") {
			return tool.Done().Fact("source", source).Fact("extensions", deny).Fact("names", sortedNames(names)).Fact("paths", prefixes)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "NOCODE DENY-LIST source=%s count=%d\n", oneline.Field(source), len(deny))
		for _, e := range deny {
			fmt.Fprintf(&b, "%s\n", oneline.Escape(e))
		}
		fmt.Fprintf(&b, "NOCODE NAME-LIST source=%s names=%d paths=%d\n", oneline.Field(check.DenyFloor), len(names), len(prefixes))
		for _, n := range sortedNames(names) {
			fmt.Fprintf(&b, "name:%s\n", oneline.Escape(n))
		}
		for _, pre := range prefixes {
			fmt.Fprintf(&b, "path:%s/\n", oneline.Escape(pre))
		}
		return tool.Payload(b.String())
	}
	// The staged advisory dispatches here, after the shared refusals above --
	// the deny-list floors, the cap, the required --dir -- so it keeps every
	// refusal the audit already makes, and it never sees a --dir it was
	// willing to guess. The verb's own wiring is staged.go.
	if c.Bool("staged") {
		return stagedRun(seams, dir, allow, deny, source)
	}
	return nocodeOut(dir, allow, deny, source)
}

// nocodeOut runs the nocode audit and returns its Out, shared with quickstart
// so a first run inherits the same lines.
func nocodeOut(dir string, allow, deny []string, source string) *tool.Out {
	scanned, findings, err := check.NoCode(check.NoCodeOptions{Dir: dir, Allow: allow, DenyExt: deny, DenySource: source})
	if err != nil {
		o := tool.Refuse(oneline.Err(err))
		o.Verb = "nocode"
		return o
	}
	o := verdict(findings).Fact("dir", dir).Fact("files", scanned).Fact("deny-list", source).Fact("findings", len(findings))
	o.Verb = "nocode"
	// A run that classified nothing should not read as a run that found
	// nothing: an empty tree and a wrong --dir are indistinguishable here.
	if scanned == 0 && len(findings) == 0 {
		o.Note(fmt.Sprintf("classified NOTHING under %s — an empty tree, everything allowed, or the wrong directory", oneline.Escape(dir)))
	}
	return o
}

func floorsVerb() tool.Verb {
	return tool.Verb{
		Name:   "floors",
		Usage:  "floors --core <docs/SEED-CORE.md> --source <docs/SEED.md>",
		Effect: tool.Effect("inspection: reads the two files, writes nothing"),
		Detail: "The door's floor set matches the seed's: the fixed eight-floor charter, where --core has numbered bold\n" +
			"titles and --source has the section 6 charter enumeration and section 0 rank declarations.",
		ExitTable: exitCodes,
		Flags: func(f *tool.Flags) {
			f.Required("core", coreHint)
			f.Required("source", sourceHint)
		},
		Run: floors,
	}
}

func floors(c *tool.Call) *tool.Out {
	core, source := c.Str("core"), c.Str("source")
	n, failures, err := check.Floors(core, source)
	if err != nil {
		return tool.Refuse(oneline.Err(err))
	}
	return verdict(failures).Fact("core", core).Fact("source", source).Fact("floors", n).Fact("findings", len(failures))
}

func corpusVerb() tool.Verb {
	return tool.Verb{
		Name:      "corpus",
		Usage:     "corpus --ledger <file> --root <dir> --min-anchors <n> [--max <n>]",
		Effect:    tool.Effect("inspection: reads the ledger and the files it names, writes nothing"),
		Detail:    "Protected material is still where the ledger says it is. A ledger uses markdown rows: | fragment | home file | given | by |.",
		ExitTable: exitCodes,
		Flags: func(f *tool.Flags) {
			f.Required("ledger", ledgerHint)
			f.Required("root", rootHint)
			f.Int("min-anchors", 0, "the fewest rows the ledger may hold, must be positive (required); the ledger is inside what it protects, so its own shrinking must be red")
			addMax(f)
			f.Check(func(c *tool.Call) {
				if !c.Given("min-anchors") {
					c.Problem("--min-anchors is required; the ledger lives inside the tree it protects and can be shrunk by the same events its rows exist to catch, so the floor is a number you state; it wants " + anchorsHint + "; refusing to guess")
				} else if n := c.Int("min-anchors"); n <= 0 {
					c.Problem(fmt.Sprintf("--min-anchors must be a positive row floor (got %d); a floor of zero guards nothing, which is what an empty ledger already is; refusing to guess; %s", n, anchorsHint))
				}
			})
		},
		Run: withAlias(corpus),
	}
}

func corpus(c *tool.Call) *tool.Out {
	ledger, root, minAnchors := c.Str("ledger"), c.Str("root"), c.Int("min-anchors")
	// --root is validated BEFORE any finding is built: a FAILED line from a
	// run that then exits 2 reports findings from a run that did not happen.
	if _, _, rootErr := check.ResolveRoot(root); rootErr != nil {
		return tool.Refuse(oneline.Err(rootErr))
	}
	raw, err := os.ReadFile(ledger)
	if err != nil {
		// Nothing was checked, so this is a refusal rather than a pass --
		// the one outcome a protection check must never confuse.
		return tool.Refuse(fmt.Sprintf("the ledger %s cannot be read (%s); NOTHING was checked, which is not a pass", oneline.Escape(ledger), oneline.Err(err)))
	}
	anchors, malformed, parseErr := check.ParseLedger(raw)
	var failures []check.Failure
	if parseErr == nil {
		if failures, err = check.Corpus(root, ledger, minAnchors, anchors); err != nil {
			return tool.Refuse(oneline.Err(err))
		}
	} else if len(malformed) == 0 {
		return tool.Refuse(fmt.Sprintf("%s: %s", oneline.Escape(ledger), oneline.Err(parseErr)))
	}
	// Malformed rows print whether or not any good row survived: a ledger whose
	// rows are ALL malformed is visibly populated, and telling its author it is
	// empty while withholding the reason is the worst of both. They are their own
	// KIND under the cap, so a ledger with a thousand bad rows cannot hide the
	// anchors that also went missing. Rows were found and judged bad: the check
	// RAN, and the answer is no, which is exit 1 and not "could not run".
	o := tool.Done()
	if len(failures)+len(malformed) > 0 {
		o = tool.Fail()
	}
	for _, f := range malformed {
		o.Item("malformed-row", "subject", f.Subject, "reason", tool.Text(f.Reason))
	}
	for _, f := range failures {
		o.Item("anchor", "subject", f.Subject, "reason", tool.Text(f.Reason))
	}
	o.Fact("ledger", ledger).Fact("anchors", len(anchors)).Fact("floor", minAnchors).Fact("failed", len(failures)).Fact("malformed", len(malformed))
	if parseErr != nil {
		o.Why = append(o.Why, "no row survived parsing")
	}
	return o
}
