package cardgen

import (
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
)

// Commit is one commit a re-land brief is cut from. Files are the paths it
// changed, repository-relative. Test is `pkg TestName` when the caller already
// found one in the commit's packages, and empty when it did not: the planner
// then names TestRelandHolds for the card to write.
type Commit struct {
	SHA     string
	Subject string
	Files   []string
	Test    string
}

// RelandTest is the test a commits card writes when none of its packages has one.
const RelandTest = "TestRelandHolds"

// PlanCommits is one re-land card per commit, in the order given (oldest first).
// PATHS are that commit's files, folded past MaxPaths the way every other card
// is. The gate packages are the directories of its Go files. A later card that
// touches a path an earlier card touches names every such earlier card on
// DEPENDS-ON, so the two are not dealt together. prefix "" is reland; tier ""
// is pro. max keeps the oldest max cards, 0 is all. Ids are zero-padded so a
// brief directory's file order is this order, and a stream that adds the
// directory lands the oldest first.
func PlanCommits(commits []Commit, prefix, tier string, max int) Plan {
	if tier == "" {
		tier = "pro"
	}
	if prefix == "" {
		prefix = "reland"
	}
	if max > 0 && len(commits) > max {
		commits = commits[:max]
	}
	width := 4
	if n := len(commits); n >= 10000 {
		width = len(strconv.Itoa(n))
	}
	cards := make([]Card, 0, len(commits))
	for _, src := range commits {
		files := cleanFiles(src.Files)
		if len(files) == 0 {
			continue
		}
		sha := src.SHA
		short := sha
		if len(short) > 12 {
			short = short[:12]
		}
		test := strings.TrimSpace(src.Test)
		wrote := false
		if test == "" {
			test = fallbackTest(files)
			wrote = true
		}
		c := Card{
			ID:        fmt.Sprintf("%s-%0*d-%s", prefix, width, len(cards)+1, short),
			File:      files[0],
			Paths:     MergePaths(files),
			Test:      test,
			Tier:      tier,
			Kind:      "fix-red",
			KeptPaths: true,
			GatePkgs:  goPackages(files),
			Stop:      "the commit is re-landed on the base, or the code already does what it did and the report says no change, and the STEP 4 gate passes",
		}
		c.Task = commitTask(sha, src.Subject, files, test, wrote)
		cards = append(cards, c)
	}
	for i := range cards {
		for j := 0; j < i; j++ {
			if pathsOverlap(cards[i].Paths, cards[j].Paths) {
				cards[i].Deps = append(cards[i].Deps, cards[j].ID)
			}
		}
		if len(cards[i].Deps) > 0 {
			cards[i].Wave = 2
		} else {
			cards[i].Wave = 1
		}
	}
	waves := 1
	for _, c := range cards {
		if c.Wave > waves {
			waves = c.Wave
		}
	}
	return Plan{Cards: cards, Tier: tier, Waves: waves}
}

func cleanFiles(files []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range files {
		f = path.Clean(strings.TrimPrefix(strings.TrimSpace(f), "./"))
		if f == "" || f == "." || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// goPackages is the directories of a commit's Go files, in file order, each once.
func goPackages(files []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range files {
		if !strings.HasSuffix(f, ".go") {
			continue
		}
		dir := path.Dir(f)
		if dir == "." || seen[dir] {
			continue
		}
		seen[dir] = true
		out = append(out, dir)
	}
	return out
}

func fallbackTest(files []string) string {
	dir := ""
	for _, f := range files {
		if strings.HasSuffix(f, ".go") {
			dir = path.Dir(f)
			break
		}
	}
	if dir == "" && len(files) > 0 {
		dir = path.Dir(files[0])
	}
	if dir == "" || dir == "." {
		dir = "."
	}
	return dir + " " + RelandTest
}

func commitTask(sha, subject string, files []string, test string, wrote bool) string {
	subject = plainSubject(subject)
	task := fmt.Sprintf("Re-land commit %s (%s) onto the current base: cherry-pick it, keep its intent, and finish with no change when the code already does it. The commit's files are %s.", sha, subject, strings.Join(files, ", "))
	if wrote {
		task += " No test of this commit is in its packages yet: write " + testName(test) + " red first in the package the TEST line names, then make it green."
	}
	return task + draftRule
}

func plainSubject(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.NewReplacer("<", "", ">", "", "`", "'").Replace(s)
	if s == "" {
		return "no subject"
	}
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}

func pathsOverlap(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y || pathCovers(x, y) || pathCovers(y, x) {
				return true
			}
		}
	}
	return false
}

func pathCovers(glob, file string) bool {
	if !strings.ContainsAny(glob, "*?") {
		return false
	}
	ok, err := path.Match(glob, file)
	return err == nil && ok
}
