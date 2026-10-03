package main

import (
	"context"
	"fmt"
	"os"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
	"github.com/mas-bandwidth/nova-tools/internal/workfile"
	"github.com/mas-bandwidth/nova-tools/internal/workgh"
)

// verifyFlags declares verify's flags: the shared ones, --tree, --max and
// --max-bytes.
func verifyFlags(f *tool.Flags) {
	commonFlags(f)
	f.Required("tree", "the tree file")
	f.Max()
	f.Int("max-bytes", 1<<30, "the largest tree file read (default 1073741824).")
	f.Check(func(c *tool.Call) {
		if c.Int("max-bytes") <= 0 {
			c.Problem("--max-bytes must be positive")
		}
	})
	commonChecks(f)
}

// runVerify is layer 1's check (SPEC-WORK-V1 section 1.6): a fresh read of
// the source compared with the tree field for field; zero differences is the
// receipt tla/WorkImport.tla's Verify action records.
func runVerify(c *tool.Call, q workgh.Query) *tool.Out {
	treePath := c.Str("tree")
	maxLines := c.Int("max")
	maxBytes := c.Int("max-bytes")
	named := repos(c)

	start := now()
	fail := func(format string, a ...any) *tool.Out {
		fmt.Fprintf(c.Stderr, "VERIFY FAILED tree=%s reason=%s remedy=\"nova-work verify -h\"\n", oneline.Field(treePath), oneline.Field(fmt.Sprintf(format, a...)))
		return tool.Exit(2)
	}
	fi, err := os.Stat(treePath)
	if err != nil {
		return fail("%v", err)
	}
	if fi.Size() > int64(maxBytes) {
		return fail("the tree is %d bytes, past --max-bytes=%d", fi.Size(), maxBytes)
	}
	data, err := os.ReadFile(treePath)
	if err != nil {
		return fail("%v", err)
	}
	tree, err := workfile.Decode(treePath, data, workfile.Limits(maxBytes))
	if err != nil {
		return fail("%v", err)
	}
	if tree.Source != "github" {
		return fail("the tree's source is %q; this verb reads github trees", tree.Source)
	}
	for _, r := range named {
		if o, _, _ := cut(r); o != tree.Org {
			return tool.Refuse(fmt.Sprintf("--repo %s is not in the tree's organization %s", r, tree.Org))
		}
	}
	q, err = resolveGH(c, q)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.Dur("timeout"))
	defer cancel()
	f := &workgh.Fetcher{Q: q, PageSize: c.Int("page-size"), MaxCalls: c.Int("max-calls"), Log: c.Stderr, Remaining: -1}

	var metas []workgh.RepoMeta
	if len(named) > 0 {
		metas, err = scope(ctx, f, tree.Org, named)
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
	diffs := workfile.Diff(tree, fresh, named)
	for i, d := range diffs {
		if maxLines > 0 && i >= maxLines {
			fmt.Fprintln(c.Stdout, bounded.MoreLine("VERIFY", "difference", maxLines, len(diffs), tool.MaxRemedy))
			break
		}
		switch d.Kind {
		case "MISSING":
			fmt.Fprintf(c.Stdout, "MISSING path=%s field=%s want=%s\n", oneline.Field(d.Path), d.Field, oneline.Field(d.Want))
		case "EXTRA":
			fmt.Fprintf(c.Stdout, "EXTRA path=%s field=%s got=%s\n", oneline.Field(d.Path), d.Field, oneline.Field(d.Got))
		default:
			fmt.Fprintf(c.Stdout, "DRIFT path=%s field=%s want=%s got=%s\n", oneline.Field(d.Path), d.Field, oneline.Field(d.Want), oneline.Field(d.Got))
		}
	}
	cnt := fresh.Count()
	fields := fmt.Sprintf("tree=%s sha256=%s repos=%d issues=%d comments=%d calls=%d points=%d rest=0 seconds=%.1f",
		oneline.Field(treePath), sum(data), cnt.Repos, cnt.Issues, cnt.Comments, f.Calls, f.Points, now().Sub(start).Seconds())
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
		fmt.Fprintf(c.Stderr, "VERIFY FAILED %s differences=%d missing=%d extra=%d drift=%d\n", fields, len(diffs), missing, extra, drift)
		return tool.Exit(1)
	}
	fmt.Fprintf(c.Stdout, "VERIFY OK %s differences=0\n", fields)
	return tool.Exit(0)
}
