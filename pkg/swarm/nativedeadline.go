package swarm

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// A CARD ENDED AT ITS DEADLINE REPORTS THE WORK IT LEFT (v1.2.6, fault 9).
//
// Children on the direct routes (pro-deepseek-v4-direct, flash-deepseek41-direct on superman)
// worked until the route's wall deadline and were killed by it with no RESULT.md. In one case
// a real commit had been pushed, and the pool scored the card `no-result`: the model's
// choice to publish nothing, when the work existed and nobody was told. The deadline is held
// by the machinery (deadline.go), so the machinery is who can still say what is on disk: it
// does not ask the dead child for a report, it reads ./repo.
//
// WriteDeadlineResult writes that report when, and only when, all of these hold:
//   - the run was ended by its deadline (the caller asks; this file does not know),
//   - no result is found where the gather looks (FindCardResult): a card that published owns
//     its report, even a thin one, and this REFUSES to overwrite it,
//   - ./repo holds at least one commit past the remote's default branch. A card that
//     committed nothing has no work to lose and keeps the plain `no-result`.
//
// The base is the remote's DEFAULT branch and never the branch's own upstream: a child that
// pushed its commit has an upstream equal to HEAD, and counting against that reads a pushed
// card as having done nothing, the very case this report is for.
//
// Like the asked report, line 1 carries a verdict word the ledger counts (`RESULT: DEADLINE`)
// and a `written-by:` line says the machinery wrote it. It carries NO findings head, so it
// scores `plan-only` and never `ok` or `clean`: a report the machinery wrote can never be
// counted as work a worker did. It exists so the harvester can find and push the branch.
const (
	DeadlineResultPrefix = "RESULT: DEADLINE "
	DeadlineWrittenBy    = "written-by: nova-swarm native (the card was ended at its deadline and published no report of its own)"

	// DeadlineReportSubjects bounds how many commit subjects the report lists. The count and
	// the head are exact; the subjects are a convenience for the reader.
	DeadlineReportSubjects = 20
)

// DeadlineWork is what a card left in ./repo: the branch, the head, the commits past the
// remote's default branch, whether the remote-tracking ref for the branch already equals the
// head (the commit was pushed), and the newest subjects. ok=false when there is no work.
type DeadlineWork struct {
	Branch   string
	Head     string
	Commits  int
	Pushed   bool
	Subjects []string
}

// defaultBases are the refs a clone names the remote's default branch by, in the order asked.
// The branch's own upstream is deliberately absent: see above.
var defaultBases = []string{"origin/HEAD", "origin/main", "origin/master", "origin/dev"}

// FindDeadlineWork reads ./repo under jobDir. It never touches the network.
func FindDeadlineWork(jobDir string) (DeadlineWork, bool) {
	dir := filepath.Join(jobDir, "repo")
	if fi, err := os.Stat(filepath.Join(dir, ".git")); err != nil || !fi.IsDir() {
		return DeadlineWork{}, false
	}
	branch := gitOut(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if branch == "" || branch == "HEAD" {
		return DeadlineWork{}, false
	}
	head := gitOut(dir, "rev-parse", "--verify", "--quiet", "--end-of-options", "HEAD")
	if head == "" {
		return DeadlineWork{}, false
	}
	base := ""
	for _, ref := range defaultBases {
		if gitOut(dir, "rev-parse", "--verify", "--quiet", "--end-of-options", ref) != "" {
			base = ref
			break
		}
	}
	if base == "" {
		return DeadlineWork{}, false
	}
	n, err := strconv.Atoi(gitOut(dir, "rev-list", "--count", "--end-of-options", base+"..HEAD"))
	if err != nil || n <= 0 {
		return DeadlineWork{}, false
	}
	w := DeadlineWork{Branch: branch, Head: head, Commits: n}
	w.Pushed = gitOut(dir, "rev-parse", "--verify", "--quiet", "--end-of-options", "refs/remotes/origin/"+branch) == head
	if out := gitOut(dir, "log", "--format=%s", "-n", strconv.Itoa(DeadlineReportSubjects), "--end-of-options", base+"..HEAD"); out != "" {
		w.Subjects = strings.Split(out, "\n")
	}
	return w, true
}

// WriteDeadlineResult writes the report described above. It returns the path and whether it
// wrote one; a found result, or no work, is ("", false, nil).
func WriteDeadlineResult(jobDir, task string) (string, bool, error) {
	if _, found := FindCardResult(jobDir); found {
		return "", false, nil
	}
	w, ok := FindDeadlineWork(jobDir)
	if !ok {
		return "", false, nil
	}
	pushed := "no"
	if w.Pushed {
		pushed = "yes"
	}
	lines := []string{
		fmt.Sprintf("%sbranch=%s head=%s commits=%d pushed=%s", DeadlineResultPrefix,
			oneline.Field(w.Branch), oneline.Field(w.Head), w.Commits, pushed),
		"deadline: " + oneline.Field(task) + " was ended by its deadline before it wrote a report; its work is on the branch above in ./repo (pushed=no means the harvester must push it), so the run records where it is rather than leaving an empty job behind",
		DeadlineWrittenBy,
		"",
	}
	for _, s := range w.Subjects {
		lines = append(lines, "- "+oneline.Escape(oneline.Cap(s, 200)))
	}
	if w.Commits > len(w.Subjects) {
		lines = append(lines, fmt.Sprintf("- ...and %d older commit(s)", w.Commits-len(w.Subjects)))
	}
	dest := ResultPath(jobDir)
	if err := os.WriteFile(dest, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return "", false, err
	}
	return dest, true, nil
}

// DeadlineReport reports whether the RESULT.md at path is the one WriteDeadlineResult wrote,
// by BOTH marks, so that a card cannot promote its own verdict by typing the opening. It
// exists beside AskedReport for the same reason: the verdict line the run prints must not
// turn into `NATIVE OK` because the machinery wrote a file.
func DeadlineReport(path string) bool {
	raw, err := readRegularBounded(path, AskedTailBytes)
	if err != nil {
		return false
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], DeadlineResultPrefix) {
		return false
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == DeadlineWrittenBy {
			return true
		}
	}
	return false
}
