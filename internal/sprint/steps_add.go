package sprint

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// BriefPlace reads the repository and base a card brief names
// (docs/SPEC-SPRINT.md section 11, streams). The repository is the REPO:
// line (owner/name). The base is BASE: before any @. A brief that names
// neither returns empty strings.
func BriefPlace(brief string) (repo, base string) {
	b := swarm.ReadCardBase([]byte(brief))
	return b.Named, b.Ref
}

// RecordPlaces is the repositories and bases a stream's card briefs name,
// each once, in order (docs/SPEC-SPRINT.md section 11, streams). Add stores
// each brief on the card; the listing reads those lines back. Empty names
// are left out. The result is never nil.
func RecordPlaces(briefs []string) (repos, bases []string) {
	var rs, bs []string
	for _, brief := range briefs {
		repo, base := BriefPlace(brief)
		rs = append(rs, repo)
		bs = append(bs, base)
	}
	return uniqueSorted(rs), uniqueSorted(bs)
}

// TaskTitle is the first sentence of the body line that starts THE TASK
// (docs/SPEC-SPRINT.md section 11, streams). A brief with no such line
// has an empty title.
func TaskTitle(brief string) string {
	for _, line := range strings.Split(brief, "\n") {
		rest, ok := taskRest(strings.TrimSpace(line))
		if !ok || rest == "" {
			continue
		}
		if i := strings.IndexAny(rest, ".!?"); i >= 0 {
			return rest[:i+1]
		}
		return rest
	}
	return ""
}

// MixFindings is the finding a stream prints when its cards name more than
// one repository or more than one base (docs/SPEC-SPRINT.md section 11,
// streams). The stream keeps every name either way.
func MixFindings(stream string, repos, bases []string) []string {
	var out []string
	if len(repos) > 1 {
		out = append(out, fmt.Sprintf("stream %s names more than one repository: %s", stream, strings.Join(repos, ", ")))
	}
	if len(bases) > 1 {
		out = append(out, fmt.Sprintf("stream %s names more than one base: %s", stream, strings.Join(bases, ", ")))
	}
	return out
}

func taskRest(line string) (string, bool) {
	rest, ok := strings.CutPrefix(line, "THE TASK")
	if !ok {
		return "", false
	}
	if rest == "" {
		return "", true
	}
	switch rest[0] {
	case ' ', '\t', '.', ':':
		return strings.TrimSpace(strings.TrimLeft(rest, " \t.:")), true
	default:
		return "", false
	}
}

func uniqueSorted(in []string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
