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
