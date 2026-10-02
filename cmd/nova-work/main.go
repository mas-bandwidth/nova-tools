// Command nova-work holds every issue of every repository of an organization
// in one tree file, with each issue's full contents, and proves the tree
// holds exactly what the source holds (docs/SPEC-WORK-V1.md section 1).
//
// import reads the source, read-only, and writes the tree; verify reads the
// source again and compares it with the tree field for field. Neither verb
// writes to the source: the GitHub seam refuses any document that is not a
// query. The dispatch, the banner, the help, the version verb, the refusals
// and the output (typed lines or --json of one value) are internal/tool's.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
	"github.com/mas-bandwidth/nova-tools/internal/workgh"
)

var version string

func main() { os.Exit(workTool(realGitHub()).Main()) }

// github is how the verbs reach GitHub and the clock: where gh is found, the
// query that runs it, and the time. main's runs the real gh; a test's answers
// from a recorded conversation (workgh.Replay), starts no process and passes a
// fixed time in, so the logic is tested apart from the transport.
type github struct {
	lookPath func(file string) (string, error)
	query    func(program string) workgh.Query
	now      func() time.Time
}

func realGitHub() github {
	return github{lookPath: exec.LookPath, query: workgh.GhQuery, now: time.Now}
}

func workTool(gh github) *tool.Tool {
	return &tool.Tool{
		Name:  "nova-work",
		What:  "every issue of an organization's repositories in one tree file, verified field for field",
		Stamp: version,
		How: `import reads every issue through your gh login, read-only, into one tree file.
--dry-run reads GitHub exactly as the import does (every issue, the same calls) and writes nothing.
verify reads GitHub again: one MISSING, EXTRA or DRIFT line per difference; none is the proof.
first run: gh logged in (gh auth status); export ORG and REPO, a repository you can read.`,
		ExitTable: "0 done, or verify found no difference; 1 verify found differences, or an import's " +
			"encoded tree did not read back equal; 2 could not run (a flag, the budget, gh, GitHub, a file)",
		Verbs: []tool.Verb{
			{
				Name: "import",
				Usage: "import --org <org> (--out <tree.lisp> | --dry-run) [--repo <owner/name>]... [--max-calls <n>] " +
					"[--page-size <n>] [--gh <path>] [--timeout <d>]",
				Example: `import --org $ORG --repo $ORG/$REPO --page-size 15 --dry-run
import --org $ORG --repo $ORG/$REPO --page-size 15 --out ./tree.lisp`,
				Effect: "local write: writes the tree file --out names; reads GitHub through gh, read-only; " +
					"--dry-run reads GitHub exactly as the import does and writes nothing",
				Detail:    importDetail,
				ExitTable: "0 the tree is written (with --dry-run: read and checked, nothing written); 1 the encoded tree did not read back equal to what was fetched, nothing written; 2 could not run (a flag, the budget, gh, GitHub, the --out directory)",
				DryRun:    true,
				Flags: func(f *tool.Flags) {
					f.Required("org", "the organization whose repositories are read, as GitHub spells it")
					f.String("out", "", "the tree `file` to write, created or replaced; its directory must exist; required unless --dry-run")
					sourceFlags(f)
					f.Check(checkImport)
				},
				Run: gh.importTree,
			},
			{
				Name: "verify",
				Usage: "verify --tree <tree.lisp> [--repo <owner/name>]... [--max <n>] [--max-calls <n>] [--page-size <n>] " +
					"[--gh <path>] [--timeout <d>] [--max-bytes <n>]",
				Example:   "verify --tree ./tree.lisp --repo $ORG/$REPO --page-size 15",
				Effect:    "inspection: reads the tree, and GitHub through gh, read-only; writes nothing",
				Detail:    verifyDetail,
				ExitTable: "0 no difference; 1 one or more differences; 2 could not run (a flag, an unreadable or refused tree, the budget, gh, GitHub)",
				Flags: func(f *tool.Flags) {
					f.Required("tree", "the tree file to compare, as import wrote it")
					f.Max()
					f.Int("max-bytes", 1<<30, "the largest tree file read, in bytes (default 1073741824)")
					sourceFlags(f)
					f.Check(func(c *tool.Call) {
						if c.Int("max-bytes") <= 0 {
							c.Problem(fmt.Sprintf("--max-bytes must be positive (got %d); it wants the largest tree file read, in bytes", c.Int("max-bytes")))
						}
					})
				},
				Run: gh.verifyTree,
			},
		},
	}
}

const importDetail = `Reads every issue (open and closed) of every repository of --org (or of each
--repo) with its full contents: number, url, node id, title, body, state and
state reason, author and association, origin (internal or external), created,
updated and closed times, lock, labels, assignees, milestone, every comment,
every cross-reference to it, and the pull requests that close it. Nothing is
cut at a bound: a connection longer than one page is read to its end. The tree
is written to --out only after it has been encoded, read back and compared
with what was fetched, with zero differences. --dry-run reads GitHub exactly
as the import does (every issue, read-only, the calls IMPORT PLAN counts),
checks the round trip, and writes nothing: it needs gh and the network.

output: IMPORT OK with the counts, bytes=, the tree's sha256=, calls= (GraphQL
calls), points= (what GitHub charged), rest=0 and gh= (the gh run); then
IMPORT PLAN (est_calls, checked against --max-calls before any issue is read)
and one IMPORT REPO per repository. A dry run adds dry_run=true, out=-, and an
IMPORT NOTE naming the calls it read and that it wrote nothing.
`

const verifyDetail = `Reads the tree, reads the same repositories from GitHub again (read-only), and
compares them field for field. Every difference is one line:

  VERIFY MISSING path=<path> field=<field> want="<value>"   on GitHub, not in the tree
  VERIFY EXTRA path=<path> field=<field> got="<value>"      in the tree, not on GitHub
  VERIFY DRIFT path=<path> field=<field> want="<value>" got="<value>"

A path is repos/<owner>/<repo>/issues/<n>, with /comments/<id>, /references or
/linked-prs below it. A value longer than 80 bytes, or of more than one line,
is shown as its length and the head of its SHA-256. With no --repo, the scope
is the tree's organization: every repository GitHub lists for it and every
repository the tree holds. An issue edited on GitHub after the import is DRIFT
on its updated time and the fields that changed: that is the check working.
`

// sourceFlags are the flags of a read of GitHub, the same on both verbs.
func sourceFlags(f *tool.Flags) {
	f.Var(&repoList{}, "repo", "read only this repository, as `owner/name`; repeat for more (default: every repository of the organization)")
	f.Int("max-calls", 1500, "the GitHub call budget of the run (default 1500); the estimate is checked against it before any issue is read; 0 is refused")
	f.Int("page-size", 50, "issues per GraphQL page, 1 to 100 (default 50); a page GitHub fails to answer is asked again at half the size")
	f.String("gh", "", "the GitHub CLI to run, a `path` (default: gh on PATH); the path found is echoed as gh=")
	f.Duration("timeout", 30*time.Minute, "the whole run's deadline (default 30m)")
	f.Check(func(c *tool.Call) {
		if c.Int("max-calls") <= 0 {
			c.Problem(fmt.Sprintf("--max-calls must be positive (got %d); it wants the GitHub call budget of the run", c.Int("max-calls")))
		}
		if p := c.Int("page-size"); p < 1 || p > 100 {
			c.Problem(fmt.Sprintf("--page-size must be 1 to 100 (got %d); it wants the issues per GraphQL page", p))
		}
		if c.Dur("timeout") <= 0 {
			c.Problem("--timeout must be positive; it wants the whole run's deadline, such as 30m")
		}
	})
}

// repoList is --repo, repeated: owner/name each.
type repoList []string

func (r *repoList) String() string { return strings.Join(*r, ",") }
func (r *repoList) Get() any       { return []string(*r) }
func (r *repoList) Set(v string) error {
	owner, name, ok := strings.Cut(v, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return fmt.Errorf("%q is not owner/name", v)
	}
	*r = append(*r, v)
	return nil
}

func repos(c *tool.Call) []string { return c.Get("repo").([]string) }

// open finds gh (the --gh path, else gh on PATH) and returns the query that
// runs it with the path found, or the refusal naming what is missing.
func (g github) open(c *tool.Call, verb string) (workgh.Query, string, *tool.Out) {
	prog := c.Str("gh")
	if prog == "" {
		prog = workgh.DefaultProgram()
	}
	found, err := g.lookPath(prog)
	if err != nil {
		o := tool.Refuse(fmt.Sprintf("the GitHub CLI %q is not found (%v); install it, or name it with --gh <path>", prog, err))
		o.Remedy = "nova-work " + verb + " -h"
		return nil, "", o
	}
	return g.query(found), found, nil
}

// ghRemedy is the next command when GitHub did not answer: the login check of
// the gh this run used.
func ghRemedy(path string) string { return path + " auth status" }

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
	return atomicfile.Write(filepath.Clean(path), data, 0o600, atomicfile.ExactMode())
}
