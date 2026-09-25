package swarm

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// THE PUBLIC-CLASS GATE (CARD-8390). A worker description may carry
// `class: public | paid` (default paid). When the worker's class is public,
// the machinery scans the card for clone URLs -- `git clone ... <url>` and
// `github.com/<owner>/<repo>` -- and refuses the card unless every URL's
// owner/repo is listed in <root>/public-repos.txt (one owner/repo per line;
// a missing file means refuse every card for a public-class worker). The
// refusal line is `CARD REFUSED reason=private-source repo=<owner/repo>
// class=public worker=<name>` and the card is not admitted.

// Public class words a worker description may carry. Empty means paid: no
// description written before this gate changes meaning by being re-read.
const (
	WorkerClassPublic = "public"
	WorkerClassPaid   = "paid"
)

// IsPublic reports whether this worker may see public source only.
func (w Worker) IsPublic() bool { return w.Class == WorkerClassPublic }

// PublicRefusalWhy is the text after "CARD REFUSED ": the fixed reason, the
// offending repo, the class and the worker name, each through oneline.Field.
func PublicRefusalWhy(repo, workerName string) string {
	return "reason=private-source repo=" + oneline.Field(repo) + " class=public worker=" + oneline.Field(workerName)
}

// PublicRefusalLine is the one refusal line the gate prints.
func PublicRefusalLine(repo, workerName string) string {
	return "CARD REFUSED " + PublicRefusalWhy(repo, workerName)
}

// IsPublicRefusal reports whether an admission reason came from this gate.
func IsPublicRefusal(why string) bool {
	return strings.HasPrefix(why, "reason=private-source")
}

// githubPathRE matches github.com/<owner>/<repo> in any spelling the card may
// carry: https://github.com/o/r(.git), git@github.com:o/r(.git) and bare
// github.com/o/r. Owner and name are github's own characters.
var githubPathRE = regexp.MustCompile(`github\.com[:/]([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)`)

// gitCloneRE matches `git clone` with any flags and captures the URL operand:
// the first non-flag token after it, optionally quoted.
var gitCloneRE = regexp.MustCompile(`git\s+clone\s+(?:--[^\s]+\s+)*(?:['"]?)([^\s'"]+)`)

// CardCloneRepos returns the distinct owner/repo pairs a card's text names as
// clone URLs, in order of appearance: every `git clone <url>` operand that
// names github, plus every bare `github.com/<owner>/<repo>` mention.
func CardCloneRepos(text string) []string {
	var repos []string
	seen := map[string]bool{}
	add := func(r string) {
		r = strings.TrimSuffix(r, ".git")
		if r == "" || seen[r] {
			return
		}
		seen[r] = true
		repos = append(repos, r)
	}
	for _, m := range gitCloneRE.FindAllStringSubmatch(text, -1) {
		if len(m) < 2 {
			continue
		}
		url := strings.Trim(m[1], `'"`)
		if g := githubPathRE.FindStringSubmatch(url); len(g) == 3 {
			add(g[1] + "/" + g[2])
		}
	}
	for _, m := range githubPathRE.FindAllStringSubmatch(text, -1) {
		add(m[1] + "/" + m[2])
	}
	return repos
}

// loadPublicAllowlist reads <root>/public-repos.txt into a set of owner/repo
// lines. ok is false when the file is missing: a missing file refuses every
// card for a public-class worker, so absence is never an empty pass.
func loadPublicAllowlist(root string) (map[string]bool, bool) {
	raw, err := os.ReadFile(filepath.Join(root, "public-repos.txt"))
	if err != nil {
		return nil, false
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSuffix(line, ".git")
		out[line] = true
	}
	return out, true
}

// CheckPublicCard applies the gate: a non-public worker admits everything.
// A public worker admits a card only when every clone URL's owner/repo is
// listed in <root>/public-repos.txt. It returns the offending repo and true
// when the card is refused; a missing allowlist refuses every card (the first
// repo where the card names one, "-" where it names none).
func CheckPublicCard(w Worker, cardText, root string) (string, bool) {
	if !w.IsPublic() {
		return "", false
	}
	repos := CardCloneRepos(cardText)
	allow, ok := loadPublicAllowlist(root)
	if !ok {
		if len(repos) > 0 {
			return repos[0], true
		}
		return "-", true
	}
	for _, r := range repos {
		if !allow[r] {
			return r, true
		}
	}
	return "", false
}
