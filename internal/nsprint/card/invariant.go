package card

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/cardhdr"
)

// filesCache is FilesAt's reads, one per repo and sha: a batch of cards on
// one base reads the tree once.
var filesCache sync.Map

// FilesAt is every file in owner/name at sha, read from this host's mirror
// (git ls-tree, no forge call): the tree the one-invariant lint counts a
// card's PATHS packages against (#4396). nil when there is no mirror, no sha,
// or the mirror does not hold it: the lint then reads each path by its shape.
func FilesAt(repo, sha string) []string {
	if repo == "" || !shaRE.MatchString(sha) {
		return nil
	}
	dir := mirrorPath(repo)
	if dir == "" || !hasMirror(repo) {
		return nil
	}
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err != nil {
		return nil
	}
	key := dir + "@" + sha
	if v, ok := filesCache.Load(key); ok {
		return v.([]string)
	}
	out, err := exec.Command("git", "--git-dir", dir, "ls-tree", "-r", "--name-only", sha).Output()
	if err != nil {
		return nil
	}
	files := []string{}
	for _, f := range strings.Split(string(out), "\n") {
		if f != "" {
			files = append(files, f)
		}
	}
	filesCache.Store(key, files)
	return files
}

// LintOneInvariant is cardhdr.LintOneInvariant over a card body, its PATHS
// counted against the repo at the card's base-sha (FilesAt).
func LintOneInvariant(repo, sha string, text []byte) cardhdr.Refusals {
	return cardhdr.LintOneInvariant(cardhdr.Card{Text: string(text), Files: FilesAt(repo, sha)})
}

// LintLines is a card's refusals, one line each, each ending card=<name> when
// the card is named (a file of a batch, or a cut's label).
func LintLines(name string, rs cardhdr.Refusals) string {
	var b strings.Builder
	for _, r := range rs {
		b.WriteString(r.String())
		if name != "" {
			b.WriteString(" card=" + strconv.Quote(name))
		}
		b.WriteString("\n")
	}
	return b.String()
}
