package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/tool"
	"github.com/mas-bandwidth/nova-tools/internal/workfile"
	"github.com/mas-bandwidth/nova-tools/internal/workgh"
)

// checkImport is import's rules over its flags, run with every other rule so
// one invocation names every problem.
func checkImport(c *tool.Call) {
	org, out, dry := c.Str("org"), c.Str("out"), c.Bool("dry-run")
	switch {
	case out == "" && !dry:
		c.Problem("--out is required unless --dry-run; it wants the tree file to write")
	case out != "" && dry:
		c.Problem("--out and --dry-run exclude each other; a dry run writes nothing")
	case !dirExists(out):
		c.Problem(fmt.Sprintf("the directory of --out %q does not exist; make it first, or name a file in one that does", out))
	}
	for _, r := range repos(c) {
		if o, _, _ := strings.Cut(r, "/"); org != "" && o != org {
			c.Problem(fmt.Sprintf("--repo %s is not in --org %s", r, org))
		}
	}
}

// importTree is layer 1's import (SPEC-WORK-V1 section 1.5): read-only,
// non-destructive, every issue of every repository in scope, checked
// through the file before it is written. tla/WorkImport.tla's Fetch and
// WriteTree actions. A dry run reads all the same and writes nothing.
func (g github) importTree(c *tool.Call) *tool.Out {
	org, out, dry := c.Str("org"), c.Str("out"), c.DryRun()
	q, ghPath, refused := g.open(c, "import")
	if refused != nil {
		return refused
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.Dur("timeout"))
	defer cancel()
	start := g.now()
	var retries bytes.Buffer
	f := &workgh.Fetcher{Q: q, PageSize: c.Int("page-size"), MaxCalls: c.Int("max-calls"), Log: &retries, Remaining: -1}
	// ended gives every result, OK or not, the calls spent, the gh run and
	// the pages retried; more are facts of its own after the calls.
	ended := func(o *tool.Out, more ...tool.Field) *tool.Out {
		o.Facts = append(tool.Fields{{K: "org", V: org}}, o.Facts...)
		o.Fact("calls", f.Calls).Fact("points", f.Points)
		o.Facts = append(o.Facts, more...)
		o.Fact("gh", ghPath)
		for _, l := range strings.Split(strings.TrimSpace(retries.String()), "\n") {
			if l != "" {
				o.Note(l)
			}
		}
		return o
	}
	refuse := func(err error) *tool.Out {
		o := tool.Refuse(err.Error())
		o.Remedy = ghRemedy(ghPath)
		if errors.Is(err, workgh.ErrBudget) {
			o.Remedy = importAgain(c, 2*c.Int("max-calls"))
		}
		return ended(o)
	}

	metas, err := scope(ctx, f, org, repos(c))
	if err != nil {
		return refuse(err)
	}
	issues, est := 0, f.Calls
	for _, m := range metas {
		issues += m.Issues
		est += pages(m.Issues, c.Int("page-size"))
	}
	plan := []any{"repos", len(metas), "issues", issues, "est_calls", est, "max_calls", c.Int("max-calls"), "page_size", c.Int("page-size")}
	if est > c.Int("max-calls") {
		o := tool.Refuse(fmt.Sprintf("the plan needs about %d calls and --max-calls is %d; narrow it with --repo or raise --max-calls", est, c.Int("max-calls")))
		o.Remedy = importAgain(c, est)
		return ended(o).Item("plan", plan...)
	}

	tree := &workfile.Tree{Source: "github", Org: org, Fetched: start.UTC().Format(time.RFC3339)}
	var perRepo [][]any
	for _, m := range metas {
		before := f.Calls
		r, err := f.Issues(ctx, m)
		if err != nil {
			return refuse(err)
		}
		tree.Repos = append(tree.Repos, r)
		cnt := (&workfile.Tree{Repos: []workfile.Repo{r}}).Count()
		perRepo = append(perRepo, []any{"repo", m.Name, "issues", cnt.Issues, "comments", cnt.Comments,
			"references", cnt.References, "linked_prs", cnt.LinkedPRs, "calls", f.Calls - before})
	}

	data, err := workfile.Encode(tree)
	if err != nil {
		o := tool.Refuse("the tree cannot be written without loss: " + err.Error())
		o.Remedy = "nova-work import -h"
		return ended(o)
	}
	// The round trip through the file, before anything is written: the tree
	// read back must equal what was fetched, field for field.
	back, err := workfile.Decode("(encoded)", data, workfile.Limits(len(data)+1))
	if err != nil {
		return ended(tool.Fail("the encoded tree does not read back: " + err.Error()))
	}
	if diffs := workfile.Diff(back, tree, nil); len(diffs) > 0 {
		d := diffs[0]
		return ended(tool.Fail(fmt.Sprintf("the encoded tree reads back with %d differences, first %s %s %s", len(diffs), d.Kind, d.Path, d.Field)))
	}
	outField := "-"
	if !dry {
		if err := writeFile(out, data); err != nil {
			o := tool.Refuse(fmt.Sprintf("write %s: %v", out, err))
			o.Remedy = "nova-work import -h"
			return ended(o)
		}
		outField = out
	}
	cnt := tree.Count()
	o := tool.Done().Fact("out", outField).Fact("repos", cnt.Repos).Fact("issues", cnt.Issues).
		Fact("comments", cnt.Comments).Fact("references", cnt.References).Fact("linked_prs", cnt.LinkedPRs).
		Fact("bytes", len(data)).Fact("sha256", sum(data))
	o = ended(o, tool.Field{K: "rest", V: 0}, tool.Field{K: "seconds", V: fmt.Sprintf("%.1f", g.now().Sub(start).Seconds())})
	o.Item("plan", plan...)
	for _, r := range perRepo {
		o.Item("repo", r...)
	}
	if dry {
		// A dry run is not offline: it reads what the import reads. The run
		// says so, not only the help.
		o.Note(fmt.Sprintf("the dry run read GitHub as the import does (calls=%d, read-only) and wrote nothing", f.Calls))
	}
	return o
}

// importAgain is the import as it was asked, with --max-calls set to n: the
// remedy of a run the budget stopped.
func importAgain(c *tool.Call, n int) string {
	cmd := []string{"nova-work", "import", "--org", c.Str("org")}
	for _, r := range repos(c) {
		cmd = append(cmd, "--repo", r)
	}
	if c.Str("out") != "" {
		cmd = append(cmd, "--out", c.Str("out"))
	} else {
		cmd = append(cmd, "--dry-run")
	}
	for _, name := range []string{"page-size", "gh", "timeout"} {
		if c.Given(name) {
			cmd = append(cmd, "--"+name, fmt.Sprint(c.Get(name)))
		}
	}
	return strings.Join(append(cmd, "--max-calls", fmt.Sprint(n)), " ")
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
