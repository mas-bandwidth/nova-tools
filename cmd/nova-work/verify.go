package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/workfile"
	"github.com/mas-bandwidth/nova-tools/internal/workgh"
)

// runVerify is layer 1's check (SPEC-WORK-V1 section 1.6): a fresh read of
// the source compared with the tree field for field; zero differences is the
// receipt tla/WorkImport.tla's Verify action records.
func runVerify(args []string, stdout, stderr io.Writer, q workgh.Query) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	var c common
	c.bind(fs)
	treePath := fs.String("tree", "", "")
	maxLines := fs.Int("max", 20, "")
	maxBytes := fs.Int("max-bytes", 1<<30, "")
	if code, ok := parse("verify", fs, args, stderr); !ok {
		if code < 0 {
			fmt.Fprint(stdout, verifyHelp)
			return 0
		}
		return code
	}
	var bad []string
	if *treePath == "" {
		bad = append(bad, "--tree is required")
	}
	if *maxLines < 0 {
		bad = append(bad, "--max must be 0 (all) or more")
	}
	if *maxBytes <= 0 {
		bad = append(bad, "--max-bytes must be positive")
	}
	if len(bad) > 0 {
		if c.json {
			env := verifyEnvelope{
				Result: jsonResult{
					Verb:   "verify",
					Status: "refused",
					Exit:   2,
					Why:    bad,
					Remedy: "nova-work verify -h",
				},
				Facts: map[string]any{},
				Items: []verifyItem{},
			}
			b, _ := json.Marshal(env)
			fmt.Fprintln(stdout, string(b))
			return 2
		}
		return refuse(stderr, "verify", bad)
	}
	start := now()
	fail := func(format string, a ...any) int {
		reason := fmt.Sprintf(format, a...)
		if c.json {
			env := verifyEnvelope{
				Result: jsonResult{
					Verb:   "verify",
					Status: "refused",
					Exit:   2,
					Why:    []string{reason},
				},
				Facts: map[string]any{
					"tree": *treePath,
				},
				Items: []verifyItem{},
			}
			b, _ := json.Marshal(env)
			fmt.Fprintln(stdout, string(b))
			return 2
		}
		fmt.Fprintf(stderr, "VERIFY FAIL tree=%s reason=%s\n", oneline.Field(*treePath), oneline.Field(reason))
		return 2
	}
	fi, err := os.Stat(*treePath)
	if err != nil {
		return fail("%v", err)
	}
	if fi.Size() > int64(*maxBytes) {
		return fail("the tree is %d bytes, past --max-bytes=%d", fi.Size(), *maxBytes)
	}
	data, err := os.ReadFile(*treePath)
	if err != nil {
		return fail("%v", err)
	}
	tree, err := workfile.Decode(*treePath, data, workfile.Limits(*maxBytes))
	if err != nil {
		return fail("%v", err)
	}
	if tree.Source != "github" {
		return fail("the tree's source is %q; this verb reads github trees", tree.Source)
	}
	for _, r := range c.repos {
		if o, _, _ := cut(r); o != tree.Org {
			bad = append(bad, fmt.Sprintf("--repo %s is not in the tree's organization %s", r, tree.Org))
		}
	}
	if len(bad) > 0 {
		if c.json {
			env := verifyEnvelope{
				Result: jsonResult{
					Verb:   "verify",
					Status: "refused",
					Exit:   2,
					Why:    bad,
					Remedy: "nova-work verify -h",
				},
				Facts: map[string]any{
					"tree": *treePath,
				},
				Items: []verifyItem{},
			}
			b, _ := json.Marshal(env)
			fmt.Fprintln(stdout, string(b))
			return 2
		}
		return refuse(stderr, "verify", bad)
	}
	q, bad = c.check("verify", q, stdout)
	if len(bad) > 0 {
		if c.json {
			env := verifyEnvelope{
				Result: jsonResult{
					Verb:   "verify",
					Status: "refused",
					Exit:   2,
					Why:    bad,
					Remedy: "nova-work verify -h",
				},
				Facts: map[string]any{
					"tree": *treePath,
				},
				Items: []verifyItem{},
			}
			b, _ := json.Marshal(env)
			fmt.Fprintln(stdout, string(b))
			return 2
		}
		return refuse(stderr, "verify", bad)
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	f := &workgh.Fetcher{Q: q, PageSize: c.pageSize, MaxCalls: c.maxCalls, Log: stderr, Remaining: -1}

	var metas []workgh.RepoMeta
	if len(c.repos) > 0 {
		metas, err = scope(ctx, f, tree.Org, c.repos)
	} else {
		metas, err = f.Repos(ctx, tree.Org)
	}
	if err != nil {
		return fail("%v (calls=%d)", err, f.Calls)
	}
	fresh := &workfile.Tree{Source: "github", Org: tree.Org}
	for _, m := range metas {
		r, err := f.Issues(ctx, m)
		if err != nil {
			return fail("%v (calls=%d)", err, f.Calls)
		}
		fresh.Repos = append(fresh.Repos, r)
	}
	diffs := workfile.Diff(tree, fresh, c.repos)
	if !c.json {
		for i, d := range diffs {
			if *maxLines > 0 && i >= *maxLines {
				fmt.Fprintf(stdout, "VERIFY MORE kind=difference shown=%d total=%d run: nova-work verify --tree %s --max 0\n", *maxLines, len(diffs), oneline.Field(*treePath))
				break
			}
			switch d.Kind {
			case "MISSING":
				fmt.Fprintf(stdout, "MISSING path=%s field=%s want=%s\n", oneline.Field(d.Path), d.Field, oneline.Field(d.Want))
			case "EXTRA":
				fmt.Fprintf(stdout, "EXTRA path=%s field=%s got=%s\n", oneline.Field(d.Path), d.Field, oneline.Field(d.Got))
			default:
				fmt.Fprintf(stdout, "DRIFT path=%s field=%s want=%s got=%s\n", oneline.Field(d.Path), d.Field, oneline.Field(d.Want), oneline.Field(d.Got))
			}
		}
	}
	cnt := fresh.Count()
	missing, extra, drift := 0, 0, 0
	for _, d := range diffs {
		switch d.Kind {
		case "MISSING":
			missing++
		case "EXTRA":
			extra++
		default:
			drift++
		}
	}
	if c.json {
		st := "ok"
		exitCode := 0
		if len(diffs) > 0 {
			st = "failed"
			exitCode = 1
		}
		items := make([]verifyItem, 0, len(diffs))
		for i, d := range diffs {
			if *maxLines > 0 && i >= *maxLines {
				break
			}
			items = append(items, verifyItem{
				Kind:  d.Kind,
				Path:  d.Path,
				Field: d.Field,
				Want:  d.Want,
				Got:   d.Got,
			})
		}
		facts := map[string]any{
			"differences": len(diffs),
			"tree":        *treePath,
			"sha256":      sum(data),
			"repos":       cnt.Repos,
			"issues":      cnt.Issues,
			"comments":    cnt.Comments,
			"calls":       f.Calls,
			"points":      f.Points,
			"rest":        0,
			"seconds":     now().Sub(start).Seconds(),
		}
		if len(c.repos) > 0 {
			facts["repo"] = c.repos.String()
		}
		if len(diffs) > 0 {
			facts["missing"] = missing
			facts["extra"] = extra
			facts["drift"] = drift
		}
		env := verifyEnvelope{
			Result: jsonResult{
				Verb:   "verify",
				Status: st,
				Exit:   exitCode,
			},
			Facts: facts,
			Items: items,
		}
		if *maxLines > 0 && len(diffs) > *maxLines {
			env.More = []jsonMore{{
				Kind:   "difference",
				Shown:  *maxLines,
				Total:  len(diffs),
				Remedy: fmt.Sprintf("run: nova-work verify --tree %s --max 0", *treePath),
			}}
		}
		b, _ := json.Marshal(env)
		fmt.Fprintln(stdout, string(b))
		return exitCode
	}
	fields := fmt.Sprintf("tree=%s sha256=%s repos=%d issues=%d comments=%d calls=%d points=%d rest=0 seconds=%.1f",
		oneline.Field(*treePath), sum(data), cnt.Repos, cnt.Issues, cnt.Comments, f.Calls, f.Points, now().Sub(start).Seconds())
	if len(diffs) > 0 {
		fmt.Fprintf(stderr, "VERIFY FAIL %s differences=%d missing=%d extra=%d drift=%d\n", fields, len(diffs), missing, extra, drift)
		return 1
	}
	fmt.Fprintf(stdout, "VERIFY OK %s differences=0\n", fields)
	return 0
}
