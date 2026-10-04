package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
	"github.com/mas-bandwidth/nova-tools/internal/workfile"
	"github.com/mas-bandwidth/nova-tools/internal/workgh"
)

// verifyTree is layer 1's check (SPEC-WORK-V1 section 1.6): a fresh read of
// the source compared with the tree field for field; zero differences is the
// receipt tla/WorkImport.tla's Verify action records.
func (g github) verifyTree(c *tool.Call) *tool.Out {
	path := c.Str("tree")
	start := g.now()
	tree, data, refused := readTree(c, "tree")
	if refused != nil {
		return refused
	}
	if tree.Source != "github" {
		return withTree(tool.Refuse(fmt.Sprintf("the tree's source is %q; this verb reads github trees", tree.Source)), path, "nova-work verify -h")
	}
	for _, r := range repos(c) {
		if o, _, _ := strings.Cut(r, "/"); o != tree.Org {
			c.Problem(fmt.Sprintf("--repo %s is not in the tree's organization %s", r, tree.Org))
		}
	}
	if o := c.Refused(); o != nil {
		o.Remedy = "nova-work verify -h"
		return o
	}
	if against := c.Str("against"); against != "" {
		return verifyAgainst(c, path, tree, data, against, start, g)
	}
	q, ghPath, refused := g.open(c, "verify")
	if refused != nil {
		return refused
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.Dur("timeout"))
	defer cancel()
	f := &workgh.Fetcher{Q: q, PageSize: c.Int("page-size"), MaxCalls: c.Int("max-calls"), Remaining: -1}
	gone := func(err error) *tool.Out {
		return withTree(tool.Refuse(err.Error()), path, ghRemedy(ghPath)).Fact("calls", f.Calls).Fact("gh", ghPath)
	}

	var metas []workgh.RepoMeta
	var err error
	if len(repos(c)) > 0 {
		metas, err = scope(ctx, f, tree.Org, repos(c))
	} else {
		metas, err = f.Repos(ctx, tree.Org)
	}
	if err != nil {
		return gone(err)
	}
	fresh := &workfile.Tree{Source: "github", Org: tree.Org}
	for _, m := range metas {
		r, err := f.Issues(ctx, m)
		if err != nil {
			return gone(err)
		}
		fresh.Repos = append(fresh.Repos, r)
	}
	diffs := workfile.Diff(tree, fresh, repos(c))
	cnt := fresh.Count()
	o := tool.Done()
	if len(diffs) > 0 {
		o = tool.Fail()
	}
	o.Fact("tree", path).Fact("sha256", sum(data)).Fact("repos", cnt.Repos).Fact("issues", cnt.Issues).
		Fact("comments", cnt.Comments).Fact("calls", f.Calls).Fact("points", f.Points).Fact("rest", 0).
		Fact("seconds", fmt.Sprintf("%.1f", g.now().Sub(start).Seconds()))
	return differences(o, diffs).Fact("gh", ghPath)
}

// verifyAgainst is verify with a second tree where GitHub stands: the same
// comparison, no gh, no network.
func verifyAgainst(c *tool.Call, path string, tree *workfile.Tree, data []byte, against string, start time.Time, g github) *tool.Out {
	other, otherData, refused := readTree(c, "against")
	if refused != nil {
		return refused
	}
	scope := repos(c)
	fresh := &workfile.Tree{Source: other.Source, Org: other.Org}
	for _, r := range other.Repos {
		if len(scope) == 0 || slices.Contains(scope, r.Name) {
			fresh.Repos = append(fresh.Repos, r)
		}
	}
	diffs := workfile.Diff(tree, fresh, scope)
	cnt := fresh.Count()
	o := tool.Done()
	if len(diffs) > 0 {
		o = tool.Fail()
	}
	o.Fact("tree", path).Fact("sha256", sum(data)).Fact("against", against).Fact("against_sha256", sum(otherData)).
		Fact("repos", cnt.Repos).Fact("issues", cnt.Issues).Fact("comments", cnt.Comments).
		Fact("seconds", fmt.Sprintf("%.1f", g.now().Sub(start).Seconds()))
	return differences(o, diffs)
}

// differences adds the count of each kind and one item per difference: the
// value of a field is a Text, quoted with its spaces kept.
func differences(o *tool.Out, diffs []workfile.Difference) *tool.Out {
	kinds := map[string]int{}
	for _, d := range diffs {
		kinds[d.Kind]++
		switch d.Kind {
		case "MISSING":
			o.Item("missing", "path", d.Path, "field", d.Field, "want", tool.Text(d.Want))
		case "EXTRA":
			o.Item("extra", "path", d.Path, "field", d.Field, "got", tool.Text(d.Got))
		default:
			o.Item("drift", "path", d.Path, "field", d.Field, "want", tool.Text(d.Want), "got", tool.Text(d.Got))
		}
	}
	return o.Fact("differences", len(diffs)).Fact("missing", kinds["MISSING"]).Fact("extra", kinds["EXTRA"]).Fact("drift", kinds["DRIFT"])
}

// readTree reads and decodes the tree file the flag names (--tree or
// --against), of at most --max-bytes, or returns the refusal naming why it
// cannot be read and what to run next; a path in a remedy is one shell word.
func readTree(c *tool.Call, flag string) (*workfile.Tree, []byte, *tool.Out) {
	path, maxBytes := c.Str(flag), c.Int("max-bytes")
	refuse := func(why, remedy string) *tool.Out {
		o := tool.Refuse(why)
		o.Remedy = remedy
		return o.Fact(flag, path)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil, nil, refuse(err.Error()+"; import writes a tree file with --out", "nova-work import -h")
	}
	if fi.Size() > int64(maxBytes) {
		return nil, nil, refuse(fmt.Sprintf("the file is %d bytes, past --max-bytes=%d", fi.Size(), maxBytes),
			again(c, "verify", []string{"tree", "against", "repo", "max", "max-calls", "page-size", "gh", "timeout", "max-bytes"},
				map[string]string{"max-bytes": fmt.Sprint(fi.Size())}))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, refuse(err.Error(), "ls -l -- "+oneline.ShellWord(path))
	}
	tree, err := workfile.Decode(path, data, workfile.Limits(maxBytes))
	if err != nil {
		return nil, nil, refuse(err.Error(), "nova-work verify -h")
	}
	return tree, data, nil
}

// withTree names the tree a refusal is about and what to run next.
func withTree(o *tool.Out, path, remedy string) *tool.Out {
	o.Remedy = remedy
	return o.Fact("tree", path)
}
