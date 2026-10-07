package cardhdr

import (
	"regexp"
	"strings"
)

// Value is the value of the first line of text that KeyValue reads with the key key (as
// written, case and all); ok is false when no line carries it. A card's REPO and BASE are read
// here, so every reader of them reads a line the same way.
func Value(text, key string) (value string, ok bool) {
	for _, l := range strings.Split(text, "\n") {
		if k, v, isKV := KeyValue(l); isKV && k == key {
			return v, true
		}
	}
	return "", false
}

// repoValueRE is the owner/name a REPO: line carries: one owner and one name, each starting
// with a letter, a digit or an underscore and holding letters, digits, dots, underscores and
// hyphens. It is the shape internal/friend/stage.go's repoRE holds staging to, so a value the
// admission lint and recut/rework accept is one the friend's staging can take.
var repoValueRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*/[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

// IsRepoValue reports whether a REPO: line's value names one repository, exactly
// `<owner>/<name>`: no other word (the tier a writer appended to the line), no clone URL, no
// local path, no `-` and no `none`. A value that is none of those is no repository: the
// friend's staging reads the whole value, refuses "its REPO %q is no owner/name" and leaves
// the card unstageable (internal/friend/stage.go, brief_repo.go).
func IsRepoValue(value string) bool {
	v := strings.TrimSpace(value)
	return repoValueRE.MatchString(v) && !strings.Contains(v, "..")
}

// sha40RE is a full commit sha as a card tree writes it.
var sha40RE = regexp.MustCompile(`^[0-9a-f]{40}$`)

// ParseBase reads a BASE value: `<ref>` (a branch, a tag or a sha) or `<ref>@<sha40>`, the form
// a card tree pins a base in (cardtree: `BASE: <ref>@<land>`). sha is the pin, "" when the
// value names none; ok is false for an empty ref or anything after the @ that is no full sha,
// so a pin is never read as its ref.
func ParseBase(v string) (ref, sha string, ok bool) {
	ref, sha, pinned := strings.Cut(strings.TrimSpace(v), "@")
	switch {
	case ref == "":
		return "", "", false
	case pinned && !sha40RE.MatchString(sha):
		return "", "", false
	}
	return ref, sha, true
}
