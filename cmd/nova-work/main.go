// Command nova-work holds every issue of every repository of an organization
// in one tree file, with each issue's full contents, and proves the tree
// holds exactly what the source holds (docs/SPEC-WORK-V1.md section 1).
//
// import reads the source, read-only, and writes the tree; verify reads the
// source again and compares it with the tree field for field. Neither verb
// writes to the source: the GitHub seam refuses any document that is not a
// query. Exit 0 done or equal, 1 verify found differences (or an import's own
// round trip did), 2 could not run.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/workgh"
)

var version string

const bannerText = `nova-work: imports GitHub issues for an organization into a single Lisp tree file and verifies them field for field against the API (docs/SPEC-WORK-V1.md)

State lives on the filesystem in a single Lisp tree file (--out or --tree) representing an organization's complete issue history. Import reads all open and closed issues, comments, cross-references, and closing pull requests read-only from GitHub using the gh CLI. Verify reads the tree file and queries GitHub again to compare every issue field for field. Discrepancies are reported as MISSING, EXTRA, or DRIFT lines, where zero output lines proves exact correspondence.

usage:
  nova-work import --org <org> (--out <tree.lisp> | --dry-run) [--repo <owner/name>]... [--max-calls <n>] [--page-size <n>] [--gh <path>] [--timeout <d>]
  nova-work verify --tree <tree.lisp> [--repo <owner/name>]... [--max <n>] [--max-calls <n>] [--page-size <n>] [--gh <path>] [--timeout <d>] [--max-bytes <n>]
  nova-work help [<verb>]
  nova-work version

import reads every issue of every repository of --org (or of each --repo)
from GitHub, read-only, with its full contents, and writes the tree to --out.
verify reads the same repositories again and prints one line for every
difference from the tree: MISSING (on GitHub, not in the tree), EXTRA (in the
tree, not on GitHub), DRIFT (a field that differs). Zero lines is the proof.

exit: 0 done, or verify found no difference; 1 verify found differences, or
import's own round trip through the file failed; 2 could not run.
`

var workExamples = []string{
	"nova-work version",
	"nova-work help import",
	"nova-work help verify",
	"nova-work import --org example-org --repo example-org/tools --dry-run",
	"nova-work import --org example-org --repo example-org/tools --out ./tree.lisp",
	"nova-work verify --tree ./tree.lisp --repo example-org/tools",
}

var banner = func() string {
	var b strings.Builder
	b.WriteString(bannerText)
	b.WriteString("\nexample:\n")
	for _, ex := range workExamples {
		b.WriteString("  " + ex + "\n")
	}
	return b.String()
}()

const importHelp = `nova-work import --org <org> (--out <tree.lisp> | --dry-run) [flags]

Reads every issue (open and closed) of every repository of --org from GitHub,
read-only, with its full contents: number, url, node id, title, body, state and
state reason, author and association, origin (internal or external), created,
updated and closed times, lock, labels, assignees, milestone, every comment
(id, url, author, association, times, body), every cross-reference to it, and
the pull requests that close it. Nothing is cut at a bound: a connection longer
than one page is read to its end. The tree is written to --out only after it
has been encoded, read back and compared with what was fetched, with zero
differences.

flags:
  --org <org>          the organization. Required.
  --repo <owner/name>  read only this repository; repeat for more. Default:
                       every repository of --org.
  --out <file>         the tree file to write (created or replaced; its
                       directory must exist). Required unless --dry-run.
  --dry-run            fetch and check everything, write nothing.
  --max-calls <n>      the GitHub call budget of the run (default 1500; 0 is
                       refused). The plan's estimate is checked against it
                       before the first issue is read.
  --page-size <n>      issues per page, 1 to 100 (default 50). A page GitHub
                       fails to answer is asked again at half the size.
  --gh <path>          the GitHub CLI (default gh on PATH); the path found is
                       echoed.
  --timeout <d>        the whole run's deadline (default 30m).

output (stdout): PLAN OK, then REPO OK per repository, then
  IMPORT OK org= out= repos= issues= comments= references= linked_prs= bytes=
  sha256= calls= points= rest=0 seconds= dry_run=
calls are GraphQL calls; points are what GitHub charged for them; rest is REST
calls, always 0. Failures go to stderr as IMPORT FAIL <reason>.

exit: 0 the tree is written (or, with --dry-run, fetched and checked); 1 the
encoded tree did not read back equal to what was fetched, nothing written;
2 could not run (a flag, the budget, GitHub, the --out directory).
`

const verifyHelp = `nova-work verify --tree <tree.lisp> [flags]

Reads the tree, reads the same repositories from GitHub again (read-only), and
compares them field for field. Every difference is one line on stdout:

  MISSING path=<path> field=<field> want=<value>   on GitHub, not in the tree
  EXTRA path=<path> field=<field> got=<value>      in the tree, not on GitHub
  DRIFT path=<path> field=<field> want=<value> got=<value>

A path is repos/<owner>/<repo>/issues/<n>, with /comments/<id>,
/references or /linked-prs below it. A value longer than 80 bytes, or of more
than one line, is shown as its length and the head of its SHA-256.

With no --repo, the scope is the tree's organization: every repository GitHub
lists for it and every repository the tree holds. An issue edited on GitHub
after the import is DRIFT on its updated time and the fields that changed:
that is the check working.

flags:
  --tree <file>        the tree file. Required.
  --repo <owner/name>  compare only this repository; repeat for more.
  --max <n>            difference lines shown (default 20; 0 shows all). The
                       total is always counted.
  --max-calls <n>      the GitHub call budget of the run (default 1500).
  --page-size <n>      issues per page, 1 to 100 (default 50).
  --gh <path>          the GitHub CLI (default gh on PATH).
  --timeout <d>        the whole run's deadline (default 30m).
  --max-bytes <n>      the largest tree file read (default 1073741824).

output: VERIFY OK tree= sha256= repos= issues= comments= calls= points= rest=0
seconds= on stdout when there is no difference; VERIFY FAIL ... differences=
on stderr when there is.

exit: 0 no difference; 1 one or more differences; 2 could not run (a flag,
an unreadable or refused tree, the budget, GitHub).
`

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, nil)) }

// run is the command; q, when not nil, replaces the GitHub seam (tests).
func run(args []string, stdout, stderr io.Writer, q workgh.Query) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "nova-work: no command given; run: nova-work help")
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		if len(args) > 1 {
			switch args[1] {
			case "import":
				fmt.Fprint(stdout, importHelp)
				return 0
			case "verify":
				fmt.Fprint(stdout, verifyHelp)
				return 0
			}
			fmt.Fprintf(stderr, "nova-work help: unknown verb %q; verbs: import verify help version; run: nova-work help\n", args[1])
			return 2
		}
		fmt.Fprint(stdout, banner)
		return 0
	case "version", "--version":
		fmt.Fprintln(stdout, buildinfo.Line("nova-work", version))
		return 0
	case "import":
		return runImport(args[1:], stdout, stderr, q)
	case "verify":
		return runVerify(args[1:], stdout, stderr, q)
	}
	fmt.Fprintf(stderr, "nova-work: unknown verb %q; verbs: import verify help version; run: nova-work help\n", args[0])
	return 2
}

type repoList []string

func (r *repoList) String() string { return strings.Join(*r, ",") }
func (r *repoList) Set(v string) error {
	owner, name, ok := strings.Cut(v, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return fmt.Errorf("%q is not owner/name", v)
	}
	*r = append(*r, v)
	return nil
}

type common struct {
	repos    repoList
	maxCalls int
	pageSize int
	gh       string
	timeout  time.Duration
}

func (c *common) bind(fs *flag.FlagSet) {
	fs.Var(&c.repos, "repo", "")
	fs.IntVar(&c.maxCalls, "max-calls", 1500, "")
	fs.IntVar(&c.pageSize, "page-size", 50, "")
	fs.StringVar(&c.gh, "gh", "", "")
	fs.DurationVar(&c.timeout, "timeout", 30*time.Minute, "")
}

// check validates the shared flags and resolves the GitHub seam, echoing
// the program found.
func (c *common) check(verb string, q workgh.Query, stdout io.Writer) (workgh.Query, []string) {
	var bad []string
	if c.maxCalls <= 0 {
		bad = append(bad, "--max-calls must be positive")
	}
	if c.pageSize < 1 || c.pageSize > 100 {
		bad = append(bad, "--page-size must be 1 to 100")
	}
	if c.timeout <= 0 {
		bad = append(bad, "--timeout must be positive")
	}
	if q != nil || len(bad) > 0 {
		return q, bad
	}
	prog := c.gh
	if prog == "" {
		prog = workgh.DefaultProgram()
	}
	found, err := lookPath(prog)
	if err != nil {
		return nil, []string{fmt.Sprintf("the GitHub CLI %q is not found (%v); install it or name it with --gh", prog, err)}
	}
	fmt.Fprintf(stdout, "GH OK path=%s\n", oneline.Field(found))
	return workgh.GhQuery(found), nil
}

func parse(verb string, fs *flag.FlagSet, args []string, stderr io.Writer) (int, bool) {
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return -1, false
		}
		fmt.Fprintf(stderr, "nova-work %s: %s; run: nova-work %s -h\n", verb, oneline.Escape(err.Error()), verb)
		return 2, false
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "nova-work %s: unexpected argument %q; run: nova-work %s -h\n", verb, fs.Arg(0), verb)
		return 2, false
	}
	return 0, true
}

func refuse(stderr io.Writer, verb string, problems []string) int {
	fmt.Fprintf(stderr, "nova-work %s: %s; run: nova-work %s -h\n", verb, strings.Join(problems, "; "), verb)
	return 2
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func dirExists(path string) bool {
	fi, err := os.Stat(filepath.Dir(path))
	return err == nil && fi.IsDir()
}

// writeFile writes data to path through a temporary file in the same
// directory and a rename, so a reader never sees half a tree.
func writeFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".nova-work-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	if werr == nil {
		werr = tmp.Sync()
	}
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), path)
	}
	if werr != nil {
		os.Remove(tmp.Name())
	}
	return werr
}
