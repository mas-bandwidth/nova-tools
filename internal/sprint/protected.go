package sprint

import (
	"slices"
	"strings"
)

// The protected branches (docs/SPEC-SPRINT.md section 7): the lander never lands on a
// protected branch of a repository unless the card's stream is marked for that
// repository: a card cut on dev lands straight onto it and ejects the merge queue's
// promotion run, so a stream lands on its sprint branch and promotion is the marked stream.
const (
	// FieldLandProtected is a stream's control card's field: the repositories, comma
	// separated, whose protected branches the stream is marked to land on, or
	// LandProtectedAny for every repository; written by `stream set --land-protected`.
	FieldLandProtected = "land_protected"
	// LandProtectedAny marks a stream for the protected branches of every repository,
	// a card naming no repository among them.
	LandProtectedAny = "any"
)

// ProtectedBranches is the branches of a repository the lander never lands on in an
// unmarked stream: dev, which promotion alone reaches, and main, the release branch
// (docs/SPEC-SPRINT.md section 7).
var ProtectedBranches = []string{"dev", "main"}

// repoKey is a repository as a mark compares it: owner/name in lower case, whatever
// the spelling (a URL, scp-like or bare, with or without .git).
func repoKey(repo string) string {
	r := strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(repo)), "/"), ".git")
	r = strings.ReplaceAll(r, ":", "/")
	parts := strings.Split(r, "/")
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	return strings.Join(parts, "/")
}

// ProtectedLandWhy is why the lander refuses to land card on base, the card's stream
// and repository given, "" when it may (docs/SPEC-SPRINT.md section 7, the protected
// branches): base is a protected branch and the stream's mark names neither the
// repository nor LandProtectedAny. The remedy marks the stream; a card meant for the
// sprint branch is re-cut on it instead.
func ProtectedLandWhy(s *Snapshot, stream, repo, base, card string) string {
	if !slices.Contains(ProtectedBranches, base) {
		return ""
	}
	for _, m := range strings.Split(s.StreamCtl(stream).F(FieldLandProtected), ",") {
		if m == LandProtectedAny || repo != "" && repoKey(m) == repoKey(repo) {
			return ""
		}
	}
	of, mark := "of "+repo, repo
	if repo == "" {
		of, mark = "of its repository (it names no REPO: line)", LandProtectedAny
	}
	return "card " + card + " lands on " + base + ", a protected branch " + of + ", and stream " + stream +
		" is not marked to land on it (a card for the sprint branch is re-cut with that BASE: line; the promotion stream is marked)" +
		"; run: nova-sprint stream set " + stream + " --land-protected " + mark
}

// landProtectedWhy is why a --land-protected value is refused, "" when it holds:
// repositories (owner/name or a clone URL), comma separated, LandProtectedAny, or
// ReadTierDefault to take the mark off.
func landProtectedWhy(v string) string {
	if v == ReadTierDefault {
		return ""
	}
	for _, m := range strings.Split(v, ",") {
		if m == "" || strings.HasPrefix(m, "-") || strings.ContainsAny(m, " \t") || m != LandProtectedAny && !strings.Contains(m, "/") {
			return "--land-protected wants repositories (owner/name, comma separated), " + LandProtectedAny + " for every repository, or " +
				ReadTierDefault + " to take the mark off; found " + v
		}
	}
	return ""
}
