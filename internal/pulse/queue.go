package pulse

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// DependencyChecker checks whether a required dependency has been merged or landed.
type DependencyChecker interface {
	// IsDependencyMerged reports whether dep is merged/landed.
	// If false, reason explains why (e.g. "not merged into dev").
	IsDependencyMerged(dep string) (bool, string)
}

// MapDependencyChecker is an in-memory DependencyChecker for testing.
type MapDependencyChecker map[string]bool

func (m MapDependencyChecker) IsDependencyMerged(dep string) (bool, string) {
	if m != nil && m[dep] {
		return true, "mock merged"
	}
	return false, fmt.Sprintf("dependency %s not merged into dev", dep)
}

// GitAndResultsChecker checks dependencies against the results store and git repository.
type GitAndResultsChecker struct {
	Repo       string
	BaseBranch string
	ResultsDir string
	RunGit     func(dir string, args ...string) (string, error)
}

// NewGitAndResultsChecker constructs a GitAndResultsChecker with defaults.
func NewGitAndResultsChecker(repo, baseBranch, resultsDir string) *GitAndResultsChecker {
	if repo == "" {
		repo = "."
	}
	if baseBranch == "" {
		baseBranch = "dev"
	}
	if resultsDir == "" {
		if env := os.Getenv("NOVA_BENCH_RESULTS"); env != "" {
			resultsDir = env
		} else if home, err := os.UserHomeDir(); err == nil && home != "" {
			resultsDir = filepath.Join(home, "nova-bench", "results")
		} else {
			resultsDir = filepath.Join(os.TempDir(), "nova-bench", "results")
		}
	}

	return &GitAndResultsChecker{
		Repo:       repo,
		BaseBranch: baseBranch,
		ResultsDir: resultsDir,
		RunGit: func(dir string, args ...string) (string, error) {
			cmd := exec.Command("git", args...)
			if dir != "" {
				cmd.Dir = dir
			}
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			if err != nil {
				return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
			}
			return stdout.String(), nil
		},
	}
}

// IsDependencyMerged checks results store and git to see if dep has landed on BaseBranch.
func (g *GitAndResultsChecker) IsDependencyMerged(dep string) (bool, string) {
	dep = strings.TrimSpace(dep)
	if dep == "" {
		return true, ""
	}

	// 1. Results store check: if ~/nova-bench/results/<dep>/RESULT.md or .harvested exists
	if g.ResultsDir != "" {
		candidates := []string{
			dep,
			strings.TrimPrefix(dep, "card-"),
			"card-" + dep,
		}
		for _, cand := range candidates {
			storeDir := filepath.Join(g.ResultsDir, cand)
			resFile := filepath.Join(storeDir, "RESULT.md")
			if fi, err := os.Stat(resFile); err == nil && fi.Size() > 0 {
				data, err := os.ReadFile(resFile)
				if err == nil && isResultDone(data) {
					return true, fmt.Sprintf("results store %s/RESULT.md DONE", cand)
				}
			}
			harvestedFile := filepath.Join(storeDir, ".harvested")
			if _, err := os.Stat(harvestedFile); err == nil {
				return true, fmt.Sprintf("results store %s/.harvested present", cand)
			}
		}
	}

	// 2. File path check (e.g. <repo>:<path> or path/to/file)
	// Essential 3 (Issue #2437): A dependency path must exist on the base branch at the current tip.
	if strings.Contains(dep, ":") || strings.Contains(dep, "/") {
		path := dep
		if strings.Contains(dep, ":") {
			_, path, _ = strings.Cut(dep, ":")
		}
		path = filepath.Clean(strings.TrimSpace(path))
		if g.RunGit != nil {
			_, err := g.RunGit(g.Repo, "cat-file", "-e", g.BaseBranch+":"+path)
			if err == nil {
				return true, fmt.Sprintf("%s exists on %s@tip", dep, g.BaseBranch)
			}
			// If it is explicitly a file path, its absence at tip is an immediate refusal
			if strings.Contains(dep, ":") {
				return false, fmt.Sprintf("%s not on %s@tip", dep, g.BaseBranch)
			}
		}
	}

	// 3. Git merge-base ancestor check
	if g.RunGit != nil {
		// Check direct ref / branch / commit
		if _, err := g.RunGit(g.Repo, "merge-base", "--is-ancestor", dep, g.BaseBranch); err == nil {
			return true, fmt.Sprintf("%s is merged into %s", dep, g.BaseBranch)
		}
		// Check with origin/ prefix
		if _, err := g.RunGit(g.Repo, "merge-base", "--is-ancestor", "origin/"+dep, g.BaseBranch); err == nil {
			return true, fmt.Sprintf("origin/%s is merged into %s", dep, g.BaseBranch)
		}
		// Check if branch --merged includes it
		if out, err := g.RunGit(g.Repo, "branch", "-r", "--merged", g.BaseBranch); err == nil {
			for _, line := range strings.Split(out, "\n") {
				line = strings.TrimSpace(line)
				if line == dep || line == "origin/"+dep || strings.HasSuffix(line, "/"+dep) {
					return true, fmt.Sprintf("branch %s merged into %s", dep, g.BaseBranch)
				}
			}
		}
		// Check git log for actual merge commit or card landing on base branch.
		// A mere mention (e.g. "fixes #2484", "mention #2484") must NOT satisfy the check.
		grepTarget := dep
		if prNum := extractPRNumber(dep); prNum != "" {
			grepTarget = "#" + prNum
		}
		if out, err := g.RunGit(g.Repo, "log", g.BaseBranch, "-n", "50", "--format=%s", "--grep="+grepTarget); err == nil {
			for _, line := range strings.Split(out, "\n") {
				subject := strings.TrimSpace(line)
				if subject == "" {
					continue
				}
				if IsCommitMergeOf(subject, dep) {
					return true, fmt.Sprintf("commit merging %s found on %s: %s", dep, g.BaseBranch, subject)
				}
			}
		}
	}

	return false, fmt.Sprintf("dependency %s not merged into %s", dep, g.BaseBranch)
}

var (
	parenRegex   = regexp.MustCompile(`\(([^)]+)\)`)
	mentionRegex = regexp.MustCompile(`(?i)\b(fix|fixes|fixed|see|ref|refs|close|closes|closed|mention|mentions|mentioned|against|issue|issues)\b`)
)

// extractPRNumber extracts the numeric PR number from a dependency reference such as
// "#2484", "2484", "pr:2484", "pr/2484", "pull/2484", or "org/repo#2484".
// Returns empty string if dep is not a PR reference.
func extractPRNumber(dep string) string {
	dep = strings.TrimSpace(dep)
	if strings.Contains(dep, "#") {
		parts := strings.Split(dep, "#")
		candidate := parts[len(parts)-1]
		if isAllDigits(candidate) && len(candidate) > 0 {
			return candidate
		}
	}
	for _, prefix := range []string{"pr:", "pr/", "pull/"} {
		if strings.HasPrefix(dep, prefix) {
			candidate := strings.TrimPrefix(dep, prefix)
			if isAllDigits(candidate) && len(candidate) > 0 {
				return candidate
			}
		}
	}
	if isAllDigits(dep) && len(dep) > 0 {
		return dep
	}
	return ""
}

// resultDoneLineRe matches a "RESULT <id> DONE" line with an exact DONE status
// token as the final field — nothing after it, nothing between the id and it.
var resultDoneLineRe = regexp.MustCompile(`^RESULT\s+\S+\s+DONE$`)

// doneWithTokenRe matches a bare "DONE <token>" line (e.g. "DONE sha=1234567"),
// requiring DONE as its own leading word, not a prefix of a longer word.
var doneWithTokenRe = regexp.MustCompile(`^DONE\s+\S+$`)

// keyDoneRe matches "STATUS: DONE" / "VERDICT: DONE" with an exact DONE value,
// case-insensitively on the key.
var keyDoneRe = regexp.MustCompile(`(?i)^(STATUS|VERDICT):\s*DONE$`)

// isResultDone checks whether RESULT.md content authoritatively indicates DONE.
// It requires an exact DONE status token — its own line, the final field of a
// "RESULT <id> DONE" line, "DONE <token>", or "STATUS:"/"VERDICT: DONE" — and
// rejects files that contain FAILED or merely mention "DONE" as a substring or
// alongside other words (negations like "NOT DONE", near-misses like "DONEISH",
// "ABANDONED", "CONDONED", or key/value noise like "DONE: false").
func isResultDone(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	if bytes.Contains(data, []byte("FAILED")) {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "DONE" || trimmed == "**DONE**" {
			return true
		}
		if doneWithTokenRe.MatchString(trimmed) {
			return true
		}
		if resultDoneLineRe.MatchString(trimmed) {
			return true
		}
		if keyDoneRe.MatchString(trimmed) {
			return true
		}
	}
	return false
}

func isAllDigits(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isCommitPRMerge(subject, prNum string) bool {
	subject = strings.TrimSpace(subject)
	prNum = strings.TrimSpace(prNum)
	if subject == "" || prNum == "" {
		return false
	}
	lower := strings.ToLower(subject)
	if strings.HasPrefix(lower, "revert ") || strings.HasPrefix(lower, "revert:") || strings.HasPrefix(lower, "revert \"") {
		return false
	}

	// 1. Merge pull request #<num> or Merge PR #<num>
	reMerge := regexp.MustCompile(`(?i)^merge (?:pull request|pr) #` + regexp.QuoteMeta(prNum) + `(?:[^\d]|$)`)
	if reMerge.MatchString(subject) {
		return true
	}

	rePR := regexp.MustCompile(`(?:^|[^\w#])#` + regexp.QuoteMeta(prNum) + `(?:[^\d]|$)`)

	// 2. Parenthesized squash merge or batch PR landing tag: (#<num>) or (#2502 #2484)
	matches := parenRegex.FindAllStringSubmatch(subject, -1)
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		content := m[1]
		// If it contains a mention verb (e.g. fixes, see, refs), it's an issue/PR cross-reference, not a squash merge tag
		if mentionRegex.MatchString(content) {
			continue
		}
		if rePR.MatchString(content) {
			return true
		}
	}

	// 3. Batch landing with "approved PRs"
	if regexp.MustCompile(`(?i)\bapproved prs\b`).MatchString(subject) {
		if rePR.MatchString(subject) {
			return true
		}
	}

	return false
}

func isCommitCardLanding(subject, dep string) bool {
	subject = strings.TrimSpace(subject)
	dep = strings.TrimSpace(dep)
	if subject == "" || dep == "" {
		return false
	}
	lower := strings.ToLower(subject)
	if strings.HasPrefix(lower, "revert ") || strings.HasPrefix(lower, "revert:") || strings.HasPrefix(lower, "revert \"") {
		return false
	}

	cands := []string{dep}
	if !strings.HasPrefix(dep, "card-") {
		cands = append(cands, "card-"+dep)
	}

	for _, cand := range cands {
		escaped := regexp.QuoteMeta(cand)
		// 1. RESULT <cand> followed by whitespace, colon, or end of string
		reResult := regexp.MustCompile(`(?i)^result\s+` + escaped + `(?:[\s:]|$)`)
		if reResult.MatchString(subject) {
			return true
		}
		// 2. [<cand>] or <cand>: followed by whitespace or end of string
		rePrefix := regexp.MustCompile(`(?i)^(?:\[` + escaped + `\]|` + escaped + `:)(?:[\s]|$)`)
		if rePrefix.MatchString(subject) {
			return true
		}
	}
	return false
}

// IsCommitMergeOf reports whether the commit subject represents an actual merge
// or landing of dep (e.g. Merge pull request #<num>, squash merge (#<num>),
// or anchored card landing RESULT <card> / <card>:), and not a mere mention
// (e.g. "fixes #<num>", "mention #<num>").
func IsCommitMergeOf(subject, dep string) bool {
	prNum := extractPRNumber(dep)
	if prNum != "" {
		if isCommitPRMerge(subject, prNum) {
			return true
		}
	}
	return isCommitCardLanding(subject, dep)
}

// CheckCardDependencies checks all dependencies of the card at path.
// If all dependencies are merged, returns ("", "", true).
// If any dependency is unmerged, returns (unmetDep, reason, false).
func CheckCardDependencies(path string, checker DependencyChecker) (string, string, bool) {
	if checker == nil {
		return "", "", true
	}
	deps := CardDependencies(path)
	for _, dep := range deps {
		merged, reason := checker.IsDependencyMerged(dep)
		if !merged {
			return dep, reason, false
		}
	}
	return "", "", true
}

// cardNode represents a card in the topological sort graph.
type cardNode struct {
	path         string
	label        string
	base         string
	priority     int
	dependencies []string
	inDegree     int
	dependents   []*cardNode
}

// SortQueueCards sorts card file paths in topological and priority order.
// Dependencies come before dependents. Ties are broken by priority descending,
// then lexical filename order ascending.
func SortQueueCards(cards []string) []string {
	if len(cards) <= 1 {
		return cards
	}

	nodes := make([]*cardNode, len(cards))
	byLabel := map[string]*cardNode{}
	byBase := map[string]*cardNode{}

	for i, path := range cards {
		info, err := ParseCard(path)
		label := ""
		priority := 0
		var deps []string
		if err == nil && info != nil {
			label = info.Label
			priority = info.Priority
			deps = info.Dependencies
		}
		base := filepath.Base(path)
		node := &cardNode{
			path:         path,
			label:        label,
			base:         base,
			priority:     priority,
			dependencies: deps,
		}
		nodes[i] = node
		if label != "" {
			byLabel[label] = node
		}
		byBase[base] = node
		// Also allow matching by stripping .md
		byBase[strings.TrimSuffix(base, ".md")] = node
	}

	// Build edges for dependencies that refer to cards within this queue set
	for _, node := range nodes {
		for _, dep := range node.dependencies {
			var prereq *cardNode
			if target, ok := byLabel[dep]; ok && target != node {
				prereq = target
			} else if target, ok := byBase[dep]; ok && target != node {
				prereq = target
			}
			if prereq != nil {
				prereq.dependents = append(prereq.dependents, node)
				node.inDegree++
			}
		}
	}

	// Collect nodes with inDegree == 0
	var ready []*cardNode
	for _, node := range nodes {
		if node.inDegree == 0 {
			ready = append(ready, node)
		}
	}

	var sorted []string
	visited := map[*cardNode]bool{}

	for len(ready) > 0 {
		// Sort ready nodes: highest priority first, then lexical order of filename
		sort.Slice(ready, func(i, j int) bool {
			if ready[i].priority != ready[j].priority {
				return ready[i].priority > ready[j].priority
			}
			return ready[i].base < ready[j].base
		})

		// Pop top candidate
		curr := ready[0]
		ready = ready[1:]
		if visited[curr] {
			continue
		}
		visited[curr] = true
		sorted = append(sorted, curr.path)

		for _, dep := range curr.dependents {
			dep.inDegree--
			if dep.inDegree == 0 && !visited[dep] {
				ready = append(ready, dep)
			}
		}
	}

	// If there were circular dependencies among cards in the queue,
	// append any unvisited cards sorted by priority and filename
	var remaining []*cardNode
	for _, node := range nodes {
		if !visited[node] {
			remaining = append(remaining, node)
		}
	}
	if len(remaining) > 0 {
		sort.Slice(remaining, func(i, j int) bool {
			if remaining[i].priority != remaining[j].priority {
				return remaining[i].priority > remaining[j].priority
			}
			return remaining[i].base < remaining[j].base
		})
		for _, node := range remaining {
			sorted = append(sorted, node.path)
		}
	}

	return sorted
}
