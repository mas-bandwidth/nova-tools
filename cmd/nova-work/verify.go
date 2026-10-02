package main

import (
	"context"
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
		return refuse(stderr, "verify", bad)
	}
	start := now()
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "VERIFY FAILED tree=%s reason=%s remedy=\"nova-work verify -h\"\n", oneline.Field(*treePath), oneline.Field(fmt.Sprintf(format, a...)))
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
		return refuse(stderr, "verify", bad)
	}
	q, bad = c.check("verify", q, stdout)
	if len(bad) > 0 {
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
	for i, d := range diffs {
		if *maxLines > 0 && i >= *maxLines {
			fmt.Fprintf(stdout, "VERIFY MORE kind=difference shown=%d total=%d nova-work verify --tree %s --max 0\n", *maxLines, len(diffs), oneline.Field(*treePath))
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
	cnt := fresh.Count()
	fields := fmt.Sprintf("tree=%s sha256=%s repos=%d issues=%d comments=%d calls=%d points=%d rest=0 seconds=%.1f",
		oneline.Field(*treePath), sum(data), cnt.Repos, cnt.Issues, cnt.Comments, f.Calls, f.Points, now().Sub(start).Seconds())
	if len(diffs) > 0 {
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
		fmt.Fprintf(stderr, "VERIFY FAILED %s differences=%d missing=%d extra=%d drift=%d\n", fields, len(diffs), missing, extra, drift)
		return 1
	}
	fmt.Fprintf(stdout, "VERIFY OK %s differences=0\n", fields)
	return 0
}
