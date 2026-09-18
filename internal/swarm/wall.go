package swarm

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// THE WALL DEATH (issue #918). A harness that DENIES a tool call returns a tool error the
// model routes around and keeps working -- that is the configured fence (fence.go). But a
// harness build that still auto-rejects stops the model there, and the run ends with no
// RESULT.md while the card's commits sit in ./repo: eight of thirty cards died this way on
// 2026-09-16 and the batch scored them `no-result`, a model that chose to publish nothing,
// with the work stranded and unpushed.
//
// WallDeath is what such a run is: the harness's own fence named a path it would not touch,
// the job published nothing, and ./repo is left with commits. The report NAMES the path and
// the commits so the harvester pushes the work -- `WALL task=<id> path=<p>` and, when there
// are commits past the repo's base, `commits=<n> branch=<name>`.
//
// A FENCE REJECTION BESIDE A RESULT IS NOT A DEATH. A run that emitted the line and then
// published is a model that took the tool error and finished, and it is done. The result is
// asked first, wherever the gather looks for one (FindCardResult), so the two shapes never
// trade places.
func WallDeath(jobDir, task string) (string, bool) {
	if _, found := FindCardResult(jobDir); found {
		return "", false
	}
	raw, err := fenceCapture(jobDir)
	if err != nil {
		return "", false
	}
	path, rejected := FenceRejection(raw)
	if !rejected {
		return "", false
	}
	report := "WALL task=" + oneline.Field(task) + " path=" + oneline.Field(path)
	if branch, n, ok := repoCommits(jobDir); ok {
		report += fmt.Sprintf(" commits=%d branch=%s", n, oneline.Field(branch))
	}
	return report, true
}

// fenceCapture is the file the harness's own words land in: the native path's own capture
// (<job>/harness-output.log), or the supervisor's and a runner's <job>/harness.log. Both
// names are asked because the two execution paths write different ones, and the fence line
// is in whichever path ran.
func fenceCapture(jobDir string) ([]byte, error) {
	for _, name := range []string{"harness-output.log", "harness.log"} {
		if raw, err := readRegular(filepath.Join(jobDir, name)); err == nil {
			return raw, nil
		}
	}
	return nil, fmt.Errorf("no harness capture under %s", jobDir)
}

// repoCommits is the work the card already did: the branch it is on and how many commits are
// past the repo's base, or ok=false when ./repo is not a repository, is on no branch, or has
// no base to count against. The base is the branch's upstream when it has one, else the
// remote's own default branch -- never a guess at a commit.
func repoCommits(jobDir string) (string, int, bool) {
	dir := filepath.Join(jobDir, "repo")
	if fi, err := os.Stat(filepath.Join(dir, ".git")); err != nil || !fi.IsDir() {
		return "", 0, false
	}
	branch := gitOut(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if branch == "" || branch == "HEAD" {
		return "", 0, false
	}
	base := repoBase(dir)
	if base == "" {
		return "", 0, false
	}
	n, err := strconv.Atoi(gitOut(dir, "rev-list", "--count", base+"..HEAD"))
	if err != nil || n <= 0 {
		return "", 0, false
	}
	return branch, n, true
}

// repoBase is the ref ./repo was cloned from: the branch's own upstream, and failing that
// the remote's default branch under the names a clone writes.
func repoBase(dir string) string {
	for _, ref := range []string{"@{upstream}", "origin/HEAD", "origin/main", "origin/master", "origin/dev"} {
		if gitOut(dir, "rev-parse", "--verify", "--quiet", ref) != "" {
			return ref
		}
	}
	return ""
}

// gitOut runs one local git command in dir and returns its trimmed stdout; an error is the
// empty string, because every caller here treats a missing answer as "no answer" and never
// as zero.
func gitOut(dir string, args ...string) string {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
