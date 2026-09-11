package merge

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// Work list 3: the conflict, and rule 7 -- THE LANE NEVER EDITS AN ENTRY'S CONTENT.
//
// On 2026-09-11 the lane met four conflicts. All four were real, all four were on one
// Java file the tip had changed, and none was on either of the two files the prototype's
// mechanical resolver named. A resolver that had matched would have written code nobody
// read into an entry. So there is no resolver here at all: a conflict is the host's word
// or git's, the entry becomes BLOCKED, detail names every conflicting file, and a hand
// resolves it in a clone of its own.
//
// Nothing in this package opens a file in the clone's work tree for writing, and a source
// test asserts it.

// ConflictFiles lists every path git left unmerged, sorted, so a reader acts on a
// sentence rather than opening anything.
func ConflictFiles(g *Git) ([]string, error) {
	out, err := g.Out("diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			files = append(files, s)
		}
	}
	sort.Strings(files)
	return files, nil
}

// AbortMerge undoes a merge that conflicted AND CHECKS THAT IT DID: an abort that failed
// silently leaves a clone with a half-merge in it, which the next build would then
// mistake for a conflict of its own.
func AbortMerge(g *Git) error {
	if _, err := g.Run("merge", "--abort"); err != nil {
		return fmt.Errorf("the conflicted merge could not be aborted, so this clone is mid-merge and no build can use it: %w", err)
	}
	if out, err := g.Out("rev-parse", "--verify", "--quiet", "MERGE_HEAD"); err == nil && out != "" {
		return fmt.Errorf("the merge was aborted and MERGE_HEAD is still %s; this clone is mid-merge", oneLineOf(out))
	}
	return nil
}

// BlockedDetail is the one-line reason a BLOCKED entry carries: every conflicting file,
// named, so that "conflicts with <base> in test/java-tables/Versioning.java" is a
// sentence a reader can act on without opening anything.
func BlockedDetail(base string, files []string) string {
	if len(files) == 0 {
		return fmt.Sprintf("conflicts with %s", base)
	}
	return fmt.Sprintf("conflicts with %s in %s", base, strings.Join(files, ", "))
}

// HandCommand is rule 17: the exact commands a hand runs, in the order a hand runs them
// -- the clone, the fetch, the merge, the conflicting files and the push. A BLOCKED entry
// that names a file and no next step cost a child an afternoon, four times, on
// 2026-09-11.
func HandCommand(repo, base, headRef string, files []string) string {
	dir := path.Base(headRef)
	if dir == "" || dir == "." || dir == "/" {
		dir = "entry"
	}
	return fmt.Sprintf("git clone --branch %s https://github.com/%s.git %s && cd %s && git fetch origin %s && git merge origin/%s  # resolve %s && git push origin HEAD:%s",
		headRef, repo, dir, dir, base, base, strings.Join(files, " "), headRef)
}
