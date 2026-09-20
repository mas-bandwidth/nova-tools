package hygiene

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

// driveLetterRE is a Windows drive-letter prefix, `C:` or `c:`, with or without the
// slash after it. IT IS LEXICAL AND NOT `filepath.VolumeName`, ON PURPOSE (#1853, Emma's
// item-4 dogfood): a card is linted on the bench that cuts it and run on another, so a
// rule that answers differently on darwin and on windows is a rule a card can walk
// through by being written on the right machine. `C:/Windows/system32` is an absolute
// path on every bench that reads this card, whatever the bench's own separator is.
var driveLetterRE = regexp.MustCompile(`^[A-Za-z]:`)

// maxPaths is the cap on a card's PATHS: line. Eight globs is enough to name a fix's
// source file, its test, a fixture directory and a few siblings; a card that needs
// nine is a card whose bound has stopped bounding anything, and the right answer to it
// is two cards.
const maxPaths = 8

// ValidatePaths is the PATHS: line's own rule. It is checked at `cut`, before a card is
// admitted, and again here before the line is used to judge a diff: a bound that is
// validated only at the point it is written is a bound that any later edit removes.
//
// No `..`, because a glob that climbs out of the repository bounds nothing. No absolute
// path, for the same reason and because a card's paths are repo-relative by definition.
// No glob that matches every file there is, because a card whose declared paths are
// "everything" has declared nothing -- the check would run and always pass, which is
// worse than not running, because the row would say it ran.
//
// That last rule is not a list of spellings. `**` and `**/` were refused by name, and
// `**/*`, `*/**` and a bare `*` walked straight past them and matched everything just
// the same. What bounds a glob is a LITERAL character somewhere in it that a path has
// to carry, so that is what is asked for: one segment holding something that is not a
// wildcard. `sign/**`, `*.go` and `**/*.go` all clear it; `**/*` and `*/**` do not.
func ValidatePaths(paths []string) error {
	if len(paths) > maxPaths {
		return fmt.Errorf("PATHS: has %d entries, at most %d", len(paths), maxPaths)
	}
	for _, p := range paths {
		if strings.TrimSpace(p) != p || p == "" {
			return fmt.Errorf("PATHS: %q is empty or padded", p)
		}
		if strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) || driveLetterRE.MatchString(p) {
			return fmt.Errorf("PATHS: %q is absolute; the globs are repo-relative", p)
		}
		if boundsNothing(p) {
			return fmt.Errorf(`PATHS: %q matches every file there is; a card whose paths are "everything" has declared nothing`, p)
		}
		for _, seg := range strings.Split(p, "/") {
			if seg == ".." {
				return fmt.Errorf("PATHS: %q climbs out of the repository", p)
			}
		}
	}
	return nil
}

// boundsNothing answers whether a glob holds any literal character at all. A segment
// made only of `*` and `?` constrains nothing about that segment, and a glob whose
// every segment is like that constrains nothing about anything.
func boundsNothing(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if strings.Trim(seg, "*?") != "" {
			return false
		}
	}
	return true
}

// matchGlob answers whether a repo-relative path matches one glob. `*` stays inside one
// segment, as path.Match has it; `**` spans any number of segments, which path.Match
// does not do at all and which is why this is written out rather than delegated.
func matchGlob(glob, p string) bool {
	return matchSegments(strings.Split(glob, "/"), strings.Split(p, "/"))
}

func matchSegments(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			// `a/**` matches `a/b` and `a/b/c`; it also matches `a` itself, which
			// is what a card naming a directory means by it.
			if len(pat) == 1 {
				return true
			}
			for i := 0; i <= len(segs); i++ {
				if matchSegments(pat[1:], segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		ok, err := path.Match(pat[0], segs[0])
		if err != nil || !ok {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}

// matchesStray answers whether a path is on the stray list for this card kind. A
// pattern with no `/` is a BASE NAME rule and matches anywhere in the tree; a pattern
// with a `/` matches the whole repo-relative path.
func matchesStray(rules []strayRule, kind, p string) (string, bool) {
	base := path.Base(p)
	for _, r := range rules {
		if r.except[kind] && kind != "" {
			continue
		}
		subject := base
		if strings.Contains(r.pattern, "/") {
			subject = p
		}
		if matchGlob(r.pattern, subject) {
			return r.pattern, true
		}
	}
	return "", false
}
