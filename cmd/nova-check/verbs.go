package main

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/check"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// kernelCheck holds the kernel verb's budget rules over the parsed flags, so
// one run names every problem at once. The file and the budget are independent,
// so both are judged before either sends the caller away.
func kernelCheck(c *tool.Call) {
	switch {
	case c.Given("max-bytes") && c.Given("max-tokens"):
		c.Problem("give exactly one of --max-bytes or --max-tokens, not both; " + budgetHint)
	case !c.Given("max-bytes") && !c.Given("max-tokens"):
		c.Problem("--max-bytes or --max-tokens is required; it wants " + budgetHint + "; refusing to guess")
	}
	if c.Given("bytes-per-token") && c.Given("max-bytes") {
		c.Problem("--bytes-per-token applies only to --max-tokens; a divisor with a byte budget means one of the two is not what you meant; " + budgetHint)
	}
	if c.Given("max-tokens") {
		if !c.Given("bytes-per-token") {
			c.Problem("--max-tokens requires --bytes-per-token; the divisor is a measurement you make on your own writing, and there is no default; refusing to guess; " + budgetHint)
		} else if c.Get("bytes-per-token").(float64) <= 0 {
			c.Problem(fmt.Sprintf("--bytes-per-token must be a positive ratio (got %g); refusing to guess; %s", c.Get("bytes-per-token").(float64), budgetHint))
		}
		if c.Get("max-tokens").(int64) <= 0 {
			c.Problem(fmt.Sprintf("--max-tokens must be a positive token budget (got %d); refusing to guess", c.Get("max-tokens").(int64)))
		}
	}
}

// quickstart is the first run: the two checks that need nothing but a
// directory, in one command. It adds no check of its own — it runs links and
// then nocode, and both run even when the first says NO.
func quickstart(c *tool.Call) *tool.Out {
	dir := c.Str("dir")
	exclude := c.Get("exclude").([]string)
	fmt.Fprintf(c.Stdout, "QUICKSTART RUN dir=%s checks=2: links, then nocode\n", oneline.Field(dir))
	lo := linksOut(dir, nil, []string(exclude), c.Int("max"))
	no := nocodeOut(dir, nil, nil, check.DenyFloor, c.Int("max"))
	lo.Cap(c.Int("max"))
	no.Cap(c.Int("max"))
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
	if len(failed) == 0 {
		fmt.Fprintf(c.Stdout, "QUICKSTART OK done=2 worst-exit=%d next=kernel,attest,floors,corpus (kernel wants a size budget, attest a manifest of what a full boot reads, floors a derived copy and its source, corpus a ledger of protected lines: nova-check help)\n", worst)
		return tool.Exit(worst)
	}
	fmt.Fprintf(c.Stdout, "QUICKSTART FAILED checks=2 failed=%s worst-exit=%d next=%s (fix what it names, then run quickstart again)\n",
		oneline.Field(strings.Join(failed, ",")), worst, oneline.Escape("nova-check "+failed[0]+" --dir "+oneline.ShellWord(dir)))
	return tool.Exit(worst)
}

// writeOut renders one delegated check's Out to the stream its status belongs
// on: a run that said no writes to stderr, a clean one to stdout.
func writeOut(c *tool.Call, o *tool.Out) {
	w := c.Stdout
	if o.Exit != 0 {
		w = c.Stderr
	}
	o.Render(w, false)
}

func attest(c *tool.Call) *tool.Out {
	home, manifest := c.Str("home"), c.Str("manifest")
	att, failures, err := check.Attest(home, manifest)
	if err != nil {
		return tool.Refuse(oneline.Err(err))
	}
	if len(failures) == 0 {
		return tool.Done().Fact("files", att.Files).Fact("bytes", att.Bytes).Fact("sha256", att.SHA256)
	}
	o := tool.Fail()
	for _, f := range failures {
		o.Item("finding", "subject", f.Subject, "reason", f.Reason)
	}
	o.Fact("failed", len(failures)).Fact("shown", capN(len(failures), c.Int("max"))).Fact("manifest", manifest)
	return o
}

// linksOut runs the links check and returns its Out, shared with quickstart so
// a first run inherits the same lines.
func linksOut(dir string, files, exclude []string, max int) *tool.Out {
	var (
		res check.LinksResult
		err error
	)
	if len(files) > 0 {
		res, err = check.LinksFiles(dir, files, exclude)
	} else {
		res, err = check.LinksExcluding(dir, exclude)
	}
	if err != nil {
		o := tool.Refuse(oneline.Err(err))
		o.Verb = "links"
		return o
	}
	o := tool.Done()
	o.Verb = "links"
	if len(res.Broken) == 0 {
		return o.Fact("files", res.MDFiles).Fact("links", res.Checked).Fact("excluded", res.Excluded)
	}
	f := tool.Fail()
	f.Verb = "links"
	f.Fact("files", res.MDFiles).Fact("links", res.Checked).Fact("broken", len(res.Broken)).Fact("shown", capN(len(res.Broken), max)).Fact("excluded", res.Excluded)
	for _, b := range res.Broken {
		f.Item("broken", "file", b.File, "line", b.Line, "target", b.Target, "reason", b.Reason)
	}
	return f
}

func kernel(c *tool.Call) *tool.Out {
	file := c.Str("file")
	if c.Given("max-tokens") {
		measured, tokens, failures, err := check.KernelTokens(file, c.Get("max-tokens").(int64), c.Get("bytes-per-token").(float64))
		if err != nil {
			return tool.Refuse(oneline.Err(err))
		}
		if len(failures) > 0 {
			o := tool.Fail()
			for _, f := range failures {
				o.Item("finding", "subject", f.Subject, "reason", f.Reason)
			}
			return o
		}
		return tool.Done().Fact("tokens", tokens).Fact("budget", c.Get("max-tokens").(int64)).Fact("bytes", measured).Fact("divisor", c.Get("bytes-per-token").(float64))
	}
	if c.Get("max-bytes").(int64) <= 0 {
		return tool.Refuse(fmt.Sprintf("--max-bytes must be a positive byte budget (got %d); refusing to guess", c.Get("max-bytes").(int64)))
	}
	measured, failures, err := check.Kernel(file, c.Get("max-bytes").(int64))
	if err != nil {
		return tool.Refuse(oneline.Err(err))
	}
	if len(failures) > 0 {
		o := tool.Fail()
		for _, f := range failures {
			o.Item("finding", "subject", f.Subject, "reason", f.Reason)
		}
		return o
	}
	return tool.Done().Fact("bytes", measured).Fact("budget", c.Get("max-bytes").(int64))
}

func nocode(c *tool.Call) *tool.Out {
	dir := c.Str("dir")
	staged := c.Bool("staged")
	denyExt := c.Str("deny-ext")
	denyExtAdd := c.Str("deny-ext-add")
	printList := c.Bool("print-deny-list")
	allow := c.Get("allow").([]string)
	max := c.Int("max")
	if denyExt != "" && denyExtAdd != "" {
		return tool.Refuse("--deny-ext and --deny-ext-add are mutually exclusive")
	}
	if max < 0 {
		return tool.Refuse(fmt.Sprintf("--max must be a line ceiling of zero or more (got %d); 0 means print them all", max))
	}
	deny, source, err := effectiveDenyList(denyExt, denyExtAdd)
	if err != nil {
		return tool.Refuse(oneline.Err(err))
	}
	if printList {
		names, prefixes, nerr := check.FloorDenyNames()
		if nerr != nil {
			return tool.Refuse(oneline.Err(nerr))
		}
		if c.Bool("json") {
			o := tool.Done()
			o.Fact("source", source).Fact("extensions", deny).Fact("names", sortedNames(names)).Fact("paths", prefixes)
			return o
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
	if staged {
		return stagedRun(c, dir, allow, deny, source, max)
	}
	return nocodeOut(dir, allow, deny, source, max)
}

// nocodeOut runs the nocode audit and returns its Out, shared with quickstart
// so a first run inherits the same lines.
func nocodeOut(dir string, allow []string, deny []string, source string, max int) *tool.Out {
	opts := check.NoCodeOptions{Dir: dir, Allow: allow, DenyExt: deny, DenySource: source}
	scanned, findings, err := check.NoCode(opts)
	if err != nil {
		o := tool.Refuse(oneline.Err(err))
		o.Verb = "nocode"
		return o
	}
	o := tool.Done()
	o.Verb = "nocode"
	if len(findings) > 0 {
		o = tool.Fail()
		o.Verb = "nocode"
		for _, f := range findings {
			o.Item("file", "subject", f.Subject, "reason", f.Reason)
		}
		o.Fact("files", scanned).Fact("findings", len(findings)).Fact("shown", capN(len(findings), max)).Fact("deny-list", source)
		return o
	}
	if scanned == 0 {
		o.Note(fmt.Sprintf("classified NOTHING under %s — an empty tree, everything allowed, or the wrong directory", oneline.Escape(dir)))
	}
	o.Fact("files", scanned).Fact("clean", true).Fact("deny-list", source)
	return o
}

func floors(c *tool.Call) *tool.Out {
	floors, failures, err := check.Floors(c.Str("core"), c.Str("source"))
	if err != nil {
		return tool.Refuse(oneline.Err(err))
	}
	if len(failures) == 0 {
		return tool.Done().Fact("floors", floors)
	}
	o := tool.Fail()
	for _, f := range failures {
		o.Item("finding", "subject", f.Subject, "reason", f.Reason)
	}
	return o
}

func corpus(c *tool.Call) *tool.Out {
	ledger, root := c.Str("ledger"), c.Str("root")
	minAnchors := c.Int("min-anchors")
	max := c.Int("max")
	if _, _, rootErr := check.ResolveRoot(root); rootErr != nil {
		return tool.Refuse(oneline.Err(rootErr))
	}
	raw, err := os.ReadFile(ledger)
	if err != nil {
		return tool.Refuse(fmt.Sprintf("the ledger %s cannot be read (%s); NOTHING was checked, which is not a pass", oneline.Escape(ledger), oneline.Err(err)))
	}
	anchors, malformed, parseErr := check.ParseLedger(raw)
	if parseErr != nil && len(malformed) == 0 {
		return tool.Refuse(fmt.Sprintf("%s: %s", oneline.Escape(ledger), oneline.Err(parseErr)))
	}
	if parseErr != nil && len(malformed) > 0 {
		o := tool.Fail()
		for _, f := range malformed {
			o.Item("malformed-row", "subject", f.Subject, "reason", f.Reason)
		}
		o.Fact("malformed", len(malformed)).Fact("shown", capN(len(malformed), max)).Fact("anchors", 0).Fact("ledger", ledger)
		return o
	}
	failures, err := check.Corpus(root, ledger, minAnchors, anchors)
	if err != nil {
		return tool.Refuse(oneline.Err(err))
	}
	if len(failures) == 0 && len(malformed) == 0 {
		return tool.Done().Fact("anchors", len(anchors)).Fact("floor", minAnchors).Fact("ledger", ledger)
	}
	o := tool.Fail()
	for _, f := range malformed {
		o.Item("malformed-row", "subject", f.Subject, "reason", f.Reason)
	}
	for _, f := range failures {
		o.Item("anchor", "subject", f.Subject, "reason", f.Reason)
	}
	o.Fact("anchors", len(anchors)).Fact("floor", minAnchors).Fact("failed", len(failures)).Fact("shown", capN(len(failures), max)).Fact("malformed", len(malformed)).Fact("ledger", ledger)
	return o
}

// capN is the number of a listing that a cap shows: all of them, or the cap.
func capN(total, max int) int {
	if max > 0 && total > max {
		return max
	}
	return total
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
	return slices.Sorted(maps.Keys(seen)), check.DenyExtended, nil
}

// sortedNames returns the name-floor keys in a stable order, so that
// --print-deny-list output can be diffed between runs and between versions. It
// is never nil: the JSON rendering prints an empty floor as [], not null.
func sortedNames(m map[string]bool) []string {
	return slices.Sorted(maps.Keys(m))
}
