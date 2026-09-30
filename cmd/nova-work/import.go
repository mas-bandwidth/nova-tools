package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/workfile"
	"github.com/mas-bandwidth/nova-tools/internal/workgh"
)

var lookPath = exec.LookPath

// now is the clock; a test fixes it.
var now = time.Now

// runImport is layer 1's import (SPEC-WORK-V1 section 1.5): read-only,
// non-destructive, every issue of every repository in scope, checked
// through the file before it is written. tla/WorkImport.tla's Fetch and
// WriteTree actions.
func runImport(args []string, stdout, stderr io.Writer, q workgh.Query) int {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	var c common
	c.bind(fs)
	org := fs.String("org", "", "")
	out := fs.String("out", "", "")
	dry := fs.Bool("dry-run", false, "")
	if code, ok := parse("import", fs, args, stderr); !ok {
		if code < 0 {
			fmt.Fprint(stdout, importHelp)
			return 0
		}
		return code
	}
	var bad []string
	if *org == "" {
		bad = append(bad, "--org is required")
	}
	if *out == "" && !*dry {
		bad = append(bad, "--out is required unless --dry-run")
	}
	if *out != "" && *dry {
		bad = append(bad, "--out and --dry-run exclude each other")
	}
	if *out != "" && !dirExists(*out) {
		bad = append(bad, fmt.Sprintf("the directory of --out %q does not exist", *out))
	}
	for _, r := range c.repos {
		if o, _, _ := cut(r); o != *org && *org != "" {
			bad = append(bad, fmt.Sprintf("--repo %s is not in --org %s", r, *org))
		}
	}
	if len(bad) > 0 {
		if c.json {
			env := importEnvelope{
				Result: jsonResult{
					Verb:   "import",
					Status: "refused",
					Exit:   2,
					Why:    bad,
					Remedy: "nova-work import -h",
				},
				Facts: map[string]any{},
			}
			b, _ := json.Marshal(env)
			fmt.Fprintln(stdout, string(b))
			return 2
		}
		return refuse(stderr, "import", bad)
	}
	q, bad = c.check("import", q, stdout)
	if len(bad) > 0 {
		if c.json {
			env := importEnvelope{
				Result: jsonResult{
					Verb:   "import",
					Status: "refused",
					Exit:   2,
					Why:    bad,
					Remedy: "nova-work import -h",
				},
				Facts: map[string]any{},
			}
			b, _ := json.Marshal(env)
			fmt.Fprintln(stdout, string(b))
			return 2
		}
		return refuse(stderr, "import", bad)
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	start := now()
	f := &workgh.Fetcher{Q: q, PageSize: c.pageSize, MaxCalls: c.maxCalls, Log: stderr, Remaining: -1}
	fail := func(code int, format string, a ...any) int {
		reason := fmt.Sprintf(format, a...)
		if c.json {
			st := "failed"
			if code == 2 {
				st = "refused"
			}
			fFacts := map[string]any{
				"org":    *org,
				"calls":  f.Calls,
				"points": f.Points,
			}
			if len(c.repos) > 0 {
				fFacts["repo"] = c.repos.String()
			}
			if *out != "" {
				fFacts["out"] = *out
			}
			env := importEnvelope{
				Result: jsonResult{
					Verb:   "import",
					Status: st,
					Exit:   code,
					Why:    []string{reason},
				},
				Facts: fFacts,
			}
			b, _ := json.Marshal(env)
			fmt.Fprintln(stdout, string(b))
			return code
		}
		fmt.Fprintf(stderr, "IMPORT FAIL org=%s calls=%d points=%d reason=%s\n", oneline.Field(*org), f.Calls, f.Points,
			oneline.Field(reason))
		return code
	}

	metas, err := scope(ctx, f, *org, c.repos)
	if err != nil {
		return fail(2, "%v", err)
	}
	issues, est := 0, f.Calls
	for _, m := range metas {
		issues += m.Issues
		est += pages(m.Issues, c.pageSize)
	}
	if !c.json {
		fmt.Fprintf(stdout, "PLAN OK org=%s repos=%d issues=%d est_calls=%d max_calls=%d page_size=%d\n",
			oneline.Field(*org), len(metas), issues, est, c.maxCalls, c.pageSize)
	}
	if est > c.maxCalls {
		return fail(2, "the plan needs about %d calls and --max-calls is %d; narrow it with --repo or raise --max-calls", est, c.maxCalls)
	}

	tree := &workfile.Tree{Source: "github", Org: *org, Fetched: start.UTC().Format(time.RFC3339)}
	for _, m := range metas {
		before := f.Calls
		r, err := f.Issues(ctx, m)
		if err != nil {
			return fail(2, "%v", err)
		}
		tree.Repos = append(tree.Repos, r)
		n := workfile.Tree{Repos: []workfile.Repo{r}}
		cnt := n.Count()
		if !c.json {
			fmt.Fprintf(stdout, "REPO OK repo=%s issues=%d comments=%d references=%d linked_prs=%d calls=%d\n",
				oneline.Field(m.Name), cnt.Issues, cnt.Comments, cnt.References, cnt.LinkedPRs, f.Calls-before)
		}
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
	if !*dry {
		if err := writeFile(*out, data); err != nil {
			return fail(2, "write %s: %v", *out, err)
		}
	}
	cnt := tree.Count()
	outField := "-"
	if !*dry {
		outField = *out
	}
	repoVal := "-"
	if len(c.repos) > 0 {
		repoVal = c.repos.String()
	} else if len(tree.Repos) == 1 {
		repoVal = tree.Repos[0].Name
	}
	if c.json {
		facts := map[string]any{
			"org":        *org,
			"repo":       repoVal,
			"issues":     cnt.Issues,
			"out":        outField,
			"repos":      cnt.Repos,
			"comments":   cnt.Comments,
			"references": cnt.References,
			"linked_prs": cnt.LinkedPRs,
			"bytes":      len(data),
			"sha256":     sum(data),
			"calls":      f.Calls,
			"points":     f.Points,
			"rest":       0,
			"seconds":    now().Sub(start).Seconds(),
			"dry_run":    *dry,
		}
		env := importEnvelope{
			Result: jsonResult{
				Verb:   "import",
				Status: "ok",
				Exit:   0,
			},
			Facts: facts,
		}
		b, _ := json.Marshal(env)
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprintf(stdout, "IMPORT OK org=%s out=%s repos=%d issues=%d comments=%d references=%d linked_prs=%d bytes=%d sha256=%s calls=%d points=%d rest=0 seconds=%.1f dry_run=%t\n",
		oneline.Field(*org), oneline.Field(outField), cnt.Repos, cnt.Issues, cnt.Comments, cnt.References, cnt.LinkedPRs,
		len(data), sum(data), f.Calls, f.Points, now().Sub(start).Seconds(), *dry)
	return 0
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
