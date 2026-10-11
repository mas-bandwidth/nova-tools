// verify.go holds the verify verb: its flags, its run and the helpers only it uses.

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/memindex"
	"github.com/mas-bandwidth/nova-tools/pkg/bounded"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

func cmdVerify(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	rf := addRootFlags(fs)
	links := fs.String("links", "", "gate|info: whether unresolved wikilinks drive the exit code (required)")
	var coverage, front, exempt multiFlag
	fs.Var(&coverage, "coverage", "A:B glob pair, repeatable")
	fs.Var(&front, "frontmatter", "glob whose files must carry a frontmatter name:, repeatable")
	fs.Var(&exempt, "exempt", "basename prefix exempt from --frontmatter, repeatable (nothing is exempt by default)")
	failMax := fs.Int("fail-max", bounded.Default, "finding lines to print per kind before one MORE line stands for the rest; 0 prints all")
	asJSON := fs.Bool("json", false, "print the result as one JSON object instead of lines")
	given, pos, ok := parse(fs, args, stderr, "root", "links")
	if given == nil {
		return 2
	}
	bad := !ok
	if len(pos) > 0 {
		refuse(stderr, " verify", fmt.Sprintf("unexpected argument %q", pos[0]))
		bad = true
	}
	gateLinks := false
	if given["links"] {
		switch *links {
		case "gate":
			gateLinks = true
		case "info":
			gateLinks = false
		default:
			refuse(stderr, " verify", fmt.Sprintf("--links must be gate or info (got %q); refusing to guess", *links))
			bad = true
		}
	}
	if given["fail-max"] && *failMax < 0 {
		// Zero already means "all". A negative ceiling is neither a number of lines nor
		// a way of asking for every line, so it is a typo with two readings and gets
		// neither.
		refuse(stderr, " verify", fmt.Sprintf("--fail-max must be a line ceiling of zero or more (got %d); 0 means print them all", *failMax))
		bad = true
	}
	if bad {
		return 2
	}
	if len(rf.root) != 1 {
		// --coverage and --frontmatter globs and [[wikilink]] resolution all
		// walk one tree, and a relative .md link resolves against one root, so
		// verification names one root and no more.
		return refuse(stderr, " verify", fmt.Sprintf("--root names exactly one tree for verification, but %d were given", len(rf.root)))
	}
	if len(coverage) == 0 && len(front) == 0 && !gateLinks {
		// Every check is off and wikilinks are informational: this run can
		// only ever exit 0. A green that could not have been anything else is
		// not a check, so it is refused rather than printed.
		return refuse(stderr, " verify", "no gating check requested (no --coverage, no --frontmatter, --links=info) — a run that cannot fail is not a verification")
	}
	if len(exempt) > 0 && len(front) == 0 {
		return refuse(stderr, " verify", "--exempt only applies to --frontmatter, which was not given")
	}

	c, _, ok := rf.build("verify", stderr)
	if !ok {
		return 2
	}
	// docs/SPEC.md, "verify — the coverage ritual, mechanized": every relative
	// .md link inside the B files resolves. os.DirFS follows a directory symlink
	// that stays lexically inside the root and stats a file outside it, so that
	// link is not a finding. An os.Root refuses the escape, and Coverage already
	// reports a failed Stat as "which does not exist".
	opened, err := os.OpenRoot(rf.root[0])
	if err != nil {
		return refuse(stderr, " verify", fmt.Sprintf("--root %s is not a readable directory: %s", oneline.Escape(rf.root[0]), oneline.Err(err)))
	}
	defer opened.Close() // ignored: a read-only root holds nothing to flush
	fsys := memindex.Excluding(opened.FS(), rf.excluded)

	var gating, info []memindex.Finding
	coverageFindings, frontmatterFindings := 0, 0
	for _, pair := range coverage {
		a, b, found := strings.Cut(pair, ":")
		if !found || a == "" || b == "" {
			return refuse(stderr, " verify", fmt.Sprintf("--coverage wants A:B, got %q", pair))
		}
		fnds, err := memindex.Coverage(fsys, a, b)
		if err != nil {
			return refuse(stderr, " verify", oneline.Err(err))
		}
		coverageFindings += len(fnds)
		gating = append(gating, fnds...)
	}
	for _, g := range front {
		fnds, err := memindex.FrontmatterPresent(fsys, g, exempt)
		if err != nil {
			return refuse(stderr, " verify", oneline.Err(err))
		}
		frontmatterFindings += len(fnds)
		gating = append(gating, fnds...)
	}
	wl, err := memindex.Wikilinks(fsys, c)
	if err != nil {
		return refuse(stderr, " verify", oneline.Err(err))
	}
	if gateLinks {
		gating = append(gating, wl...)
	} else {
		info = wl
	}

	if *asJSON {
		o := result("verify")
		for _, f := range info {
			o.Item(f.Kind, "gating", false, "detail", tool.Text(f.Detail))
		}
		for _, f := range gating {
			o.Item(f.Kind, "gating", true, "detail", tool.Text(f.Detail))
		}
		o.Fact("gating", len(gating)).Fact("info", len(info)).Fact("coverage", coverageFindings).
			Fact("frontmatter", frontmatterFindings).Fact("links", *links)
		if len(gating) > 0 {
			o.Status, o.Exit = tool.Failed, 1
		}
		return o.Cap(*failMax).Render(stdout, true)
	}

	// EACH KIND IS CAPPED SEPARATELY. A flat cap over the concatenated findings would
	// mean that on a corpus with ten thousand unresolved wikilinks the twenty lines a
	// reader gets are twenty wikilinks, and the one frontmatter finding -- the finding
	// they did not already know about -- is the line the cap ate.
	infos := bounded.Grouped(stdout, *failMax, "VERIFY", failMaxRemedy)
	for _, f := range info {
		infos.Line(f.Kind, fmt.Sprintf("VERIFY INFO %s: %s", f.Kind, oneline.Escape(oneline.Cap(f.Detail, oneline.TailBytes))))
	}
	infos.More()

	fails := bounded.Grouped(stderr, *failMax, "VERIFY", failMaxRemedy)
	for _, f := range gating {
		fails.Line(f.Kind, fmt.Sprintf("VERIFY FAIL %s %s", f.Kind, oneline.Escape(oneline.Cap(f.Detail, oneline.TailBytes))))
	}
	fails.More()

	// THE COUNT LINE PRINTS ON FAILURE TOO: the listing above is capped, so counting
	// its lines would understate how bad a failing run is, and the total is here.
	if fails.Total() > 0 {
		fmt.Fprintf(stderr, "VERIFY FAIL gating=%d shown=%d info=%d coverage=%d frontmatter=%d links=%s\n",
			fails.Total(), fails.Shown(), infos.Total(), coverageFindings, frontmatterFindings, *links)
		return 1
	}
	fmt.Fprintf(stdout, "VERIFY OK gating=0 info=%d shown=%d coverage=%d frontmatter=%d links=%s\n",
		infos.Total(), infos.Shown(), coverageFindings, frontmatterFindings, *links)
	return 0
}
