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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
	"github.com/mas-bandwidth/nova-tools/internal/workgh"
)

var version string

func main() { os.Exit(workTool(nil).Main()) }

// workTool is the command: its two verbs, import and verify, and the shared
// skeleton (internal/tool) that dispatches, refuses and renders. q is the
// GitHub seam; nil means the real gh on PATH, and a test passes a replay.
func workTool(q workgh.Query) *tool.Tool {
	return &tool.Tool{
		Name:  "nova-work",
		What:  "every issue of an organization's repositories in one tree file, verified field for field",
		Stamp: version,
		How: `import reads issues through your gh login and writes one tree file: a (work-tree ...)
record holding each repository and every field of each issue. verify reads GitHub again and prints
one MISSING, EXTRA or DRIFT line per difference; no lines is the proof.
The calls are counted and checked against --max-calls before any issue is read.
first run: needs gh logged in (gh auth status) and ORG and REPO set to one repository you can read.`,
		ExitTable: "0 done, or verify found no difference; 1 verify found differences, or import's own round trip through the file failed; 2 could not run.",
		Verbs: []tool.Verb{
			{
				Name:    "import",
				Usage:   "import --org <org> (--out <tree.lisp> | --dry-run) [--repo <owner/name>]... [--max-calls <n>] [--page-size <n>] [--gh <path>] [--timeout <d>]",
				Example: "import --org $ORG --repo $ORG/$REPO --page-size 15 --dry-run\nimport --org $ORG --repo $ORG/$REPO --page-size 15 --out ./tree.lisp",
				Effect:  tool.LocalWrite,
				Detail: `Reads every issue (open and closed) of every repository of --org from GitHub,
read-only, with its full contents, and writes the tree to --out only after it
has been encoded, read back and compared with what was fetched, with zero
differences.`,
				DryRun: true,
				Flags:  importFlags,
				Run:    func(c *tool.Call) *tool.Out { return runImport(c, q) },
			},
			{
				Name:    "verify",
				Usage:   "verify --tree <tree.lisp> [--repo <owner/name>]... [--max <n>] [--max-calls <n>] [--page-size <n>] [--gh <path>] [--timeout <d>] [--max-bytes <n>]",
				Example: "verify --tree ./tree.lisp --repo $ORG/$REPO --page-size 15",
				Effect:  tool.Inspection,
				Detail: `Reads the tree, reads the same repositories from GitHub again (read-only), and
compares them field for field. Every difference is one line: MISSING, EXTRA or
DRIFT.`,
				Flags: verifyFlags,
				Run:   func(c *tool.Call) *tool.Out { return runVerify(c, q) },
			},
		},
	}
}

// repoList is a repeatable --repo flag: every occurrence appends one
// owner/name, refused when it is not one.
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
func (r *repoList) Get() any { return []string(*r) }

// commonFlags declares the flags import and verify share.
func commonFlags(f *tool.Flags) {
	f.Var(&repoList{}, "repo", "read only this repository; repeat for more. Default: every repository of --org.")
	f.Int("max-calls", 1500, "the GitHub call budget of the run (default 1500; 0 is refused). The plan's estimate is checked against it before the first issue is read.")
	f.Int("page-size", 50, "issues per page, 1 to 100 (default 50). A page GitHub fails to answer is asked again at half the size.")
	f.String("gh", "", "the GitHub CLI (default gh on PATH); the path found is echoed.")
	f.Duration("timeout", 30*time.Minute, "the whole run's deadline (default 30m).")
}

// commonChecks declares the rules over the shared flags.
func commonChecks(f *tool.Flags) {
	f.Check(func(c *tool.Call) {
		if c.Int("max-calls") <= 0 {
			c.Problem("--max-calls must be positive")
		}
		if c.Int("page-size") < 1 || c.Int("page-size") > 100 {
			c.Problem("--page-size must be 1 to 100")
		}
		if c.Dur("timeout") <= 0 {
			c.Problem("--timeout must be positive")
		}
	})
}

// repos reads the --repo flags as a slice.
func repos(c *tool.Call) []string { return c.Get("repo").([]string) }

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
