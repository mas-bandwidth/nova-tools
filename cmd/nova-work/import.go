package main

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
	"github.com/mas-bandwidth/nova-tools/internal/workfile"
	"github.com/mas-bandwidth/nova-tools/internal/workgh"
)

var lookPath = exec.LookPath

// now is the clock; a test fixes it.
var now = time.Now

// importFlags declares import's flags: the shared ones, --org, --out, and the
// rules that tie --out to --dry-run and --repo to --org. It declares Prints
// because runImport writes its own lines (GH, PLAN, REPO, IMPORT) and answers
// tool.Exit, so the skeleton renders nothing for it and offers no --json
// (internal/tool, Flags.Prints).
func importFlags(f *tool.Flags) {
	f.Prints()
	commonFlags(f)
	f.Required("org", "the organization")
	f.String("out", "", "the tree file to write (created or replaced; its directory must exist). Required unless --dry-run.")
	f.Check(func(c *tool.Call) {
		if c.Str("out") == "" && !c.Bool("dry-run") {
			c.Problem("--out is required unless --dry-run")
		}
		if c.Str("out") != "" && c.Bool("dry-run") {
			c.Problem("--out and --dry-run exclude each other")
		}
		if c.Str("out") != "" && !dirExists(c.Str("out")) {
			c.Problem(fmt.Sprintf("the directory of --out %q does not exist", c.Str("out")))
		}
		for _, r := range repos(c) {
			if o, _, _ := cut(r); o != c.Str("org") && c.Str("org") != "" {
				c.Problem(fmt.Sprintf("--repo %s is not in --org %s", r, c.Str("org")))
			}
		}
	})
	commonChecks(f)
}

// resolveGH resolves the GitHub seam: the injected q when there is one, else
// the gh named by --gh (or on PATH), echoing the program found.
func resolveGH(c *tool.Call, q workgh.Query) (workgh.Query, error) {
	if q != nil {
		return q, nil
	}
	prog := c.Str("gh")
	if prog == "" {
		prog = workgh.DefaultProgram()
	}
	found, err := lookPath(prog)
	if err != nil {
		return nil, fmt.Errorf("the GitHub CLI %q is not found (%v); install it or name it with --gh", prog, err)
	}
	fmt.Fprintf(c.Stdout, "GH OK path=%s\n", oneline.Field(found))
	return workgh.GhQuery(found), nil
}

// runImport is layer 1's import (SPEC-WORK-V1 section 1.5): read-only,
// non-destructive, every issue of every repository in scope, checked
// through the file before it is written. tla/WorkImport.tla's Fetch and
// WriteTree actions.
func runImport(c *tool.Call, q workgh.Query) *tool.Out {
	org := c.Str("org")
	out := c.Str("out")
	dry := c.DryRun()
	named := repos(c)

	q, err := resolveGH(c, q)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.Dur("timeout"))
	defer cancel()
	start := now()
	f := &workgh.Fetcher{Q: q, PageSize: c.Int("page-size"), MaxCalls: c.Int("max-calls"), Log: c.Stderr, Remaining: -1}
	fail := func(code int, format string, a ...any) *tool.Out {
		fmt.Fprintf(c.Stderr, "IMPORT FAILED org=%s calls=%d points=%d reason=%s\n", oneline.Field(org), f.Calls, f.Points,
			oneline.Field(fmt.Sprintf(format, a...)))
		return tool.Exit(code)
	}

	metas, err := scope(ctx, f, org, named)
	if err != nil {
		return fail(2, "%v", err)
	}
	issues, est := 0, f.Calls
	for _, m := range metas {
		issues += m.Issues
		est += pages(m.Issues, c.Int("page-size"))
	}
	fmt.Fprintf(c.Stdout, "PLAN OK org=%s repos=%d issues=%d est_calls=%d max_calls=%d page_size=%d\n",
		oneline.Field(org), len(metas), issues, est, c.Int("max-calls"), c.Int("page-size"))
	if est > c.Int("max-calls") {
		return fail(2, "the plan needs about %d calls and --max-calls is %d; narrow it with --repo or raise --max-calls", est, c.Int("max-calls"))
	}

	tree := &workfile.Tree{Source: "github", Org: org, Fetched: start.UTC().Format(time.RFC3339)}
	for _, m := range metas {
		before := f.Calls
		r, err := f.Issues(ctx, m)
		if err != nil {
			return fail(2, "%v", err)
		}
		tree.Repos = append(tree.Repos, r)
		n := workfile.Tree{Repos: []workfile.Repo{r}}
		cnt := n.Count()
		fmt.Fprintf(c.Stdout, "REPO OK repo=%s issues=%d comments=%d references=%d linked_prs=%d calls=%d\n",
			oneline.Field(m.Name), cnt.Issues, cnt.Comments, cnt.References, cnt.LinkedPRs, f.Calls-before)
	}

	data, err := workfile.Encode(tree)
	if err != nil {
		return fail(2, "%v", err)
	}
	// The round trip through the file, before anything is written: the tree
	// read back must equal what was fetched, field for field.
	back, err := workfile.Decode("(encoded)", data, workfile.Limits(len(data)+1))
	if err != nil {
		return fail(1, "the encoded tree does not read back: %v", err)
	}
	if diffs := workfile.Diff(back, tree, nil); len(diffs) > 0 {
		d := diffs[0]
		return fail(1, "the encoded tree reads back with %d differences, first %s %s %s", len(diffs), d.Kind, d.Path, d.Field)
	}
	if !dry {
		if err := writeFile(out, data); err != nil {
			return fail(2, "write %s: %v", out, err)
		}
	}
	cnt := tree.Count()
	outField := "-"
	if !dry {
		outField = out
	}
	fmt.Fprintf(c.Stdout, "IMPORT OK org=%s out=%s repos=%d issues=%d comments=%d references=%d linked_prs=%d bytes=%d sha256=%s calls=%d points=%d rest=0 seconds=%.1f dry_run=%t\n",
		oneline.Field(org), oneline.Field(outField), cnt.Repos, cnt.Issues, cnt.Comments, cnt.References, cnt.LinkedPRs,
		len(data), sum(data), f.Calls, f.Points, now().Sub(start).Seconds(), dry)
	return tool.Exit(0)
}

// scope is the repositories a run reads: every repository of org, or the
// ones named, each read from GitHub so its issue count plans the run.
func scope(ctx context.Context, f *workgh.Fetcher, org string, named []string) ([]workgh.RepoMeta, error) {
	if len(named) == 0 {
		return f.Repos(ctx, org)
	}
	var out []workgh.RepoMeta
	seen := map[string]bool{}
	for _, n := range named {
		if seen[n] {
			continue
		}
		seen[n] = true
		m, err := f.Repo(ctx, n)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func pages(issues, size int) int {
	if issues == 0 {
		return 1
	}
	return (issues + size - 1) / size
}

func cut(repo string) (string, string, bool) {
	for i := 0; i < len(repo); i++ {
		if repo[i] == '/' {
			return repo[:i], repo[i+1:], true
		}
	}
	return repo, "", false
}
