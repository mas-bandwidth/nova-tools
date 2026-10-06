package sprint

import (
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// What a card's brief names, which is the stream's record of repository and
// base (docs/SPEC-SPRINT.md section 11, streams). add stores the brief on the
// card. The listing reads those lines. It does not copy them onto the control
// card: a later brief would leave a copy behind, and the brief is the record
// the card already keeps.

// CardStreamRepo is the repository the brief's REPO: line names, as
// owner/name. A line that is already owner/name is that. A URL is the last
// two path segments, without a trailing .git. Empty when the brief names none.
func CardStreamRepo(brief string) string {
	b := swarm.ReadCardBase([]byte(brief))
	named := strings.TrimSpace(b.Named)
	if named == "" {
		return ""
	}
	if ownerRepo(named) {
		return named
	}
	path := b.Repo
	if path == "" {
		path = named
	}
	return ownerFromPath(path)
}

// CardStreamBase is the branch the brief's BASE: line names, the ref before
// any @. Empty when the brief names none.
func CardStreamBase(brief string) string {
	return strings.TrimSpace(swarm.ReadCardBase([]byte(brief)).Ref)
}

// ownerRepo says s is owner/name: one slash, no scheme and no blank.
func ownerRepo(s string) bool {
	i := strings.IndexByte(s, '/')
	if i <= 0 || strings.LastIndexByte(s, '/') != i || i == len(s)-1 {
		return false
	}
	return !strings.ContainsAny(s, ": @")
}

// ownerFromPath is the owner/name at the end of a clone URL or path.
func ownerFromPath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimSuffix(p, ".git")
	p = strings.TrimRight(p, "/")
	if i := strings.Index(p, "://"); i >= 0 {
		p = p[i+3:]
	}
	var segs []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			segs = append(segs, s)
		}
	}
	if len(segs) < 2 {
		return ""
	}
	return segs[len(segs)-2] + "/" + segs[len(segs)-1]
}
