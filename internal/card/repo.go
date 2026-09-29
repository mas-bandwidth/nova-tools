package card

import (
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// MaxRepositoryBytes bounds a repository identity.
const MaxRepositoryBytes = 128

// MaxPathBytes bounds a repository-relative path.
const MaxPathBytes = 512

// Repository is a repository identity: `host[:port]/segment/...`, with no
// scheme, no user and no credentials, no `.git` suffix, and the host in lower
// case. Two remotes of one repository that differ only in scheme, user, default
// port, a trailing slash or a `.git` suffix have one identity. The path is kept
// exactly as written, so its case is significant: `Owner/Repo` and `owner/repo`
// are two identities, because the hosts that fold case differ from those that
// do not and this layer does not know which host it is talking to.
type Repository string

var identityRE = regexp.MustCompile(`^[A-Za-z0-9._~-]+(?::[0-9]{1,5})?(?:/[A-Za-z0-9._~+-]+)+$`)

// The rules a repository identity or an origin URL can fail. A refusal about an
// origin names one of these and never quotes the origin, which can carry a
// credential.
const (
	RuleEmpty         = "empty"
	RuleTooLong       = "too long"
	RuleUnparseable   = "does not parse as a URL"
	RuleNotRemote     = "scheme is not a remote scheme"
	RuleNoHost        = "has no host"
	RuleQueryFragment = "carries a query or a fragment"
	RuleLocalPath     = "is a local path"
	RuleNotHostPath   = "is neither a URL nor host:path"
	RuleGrammar       = "is not host[:port]/segment/... of letters, digits and . _ ~ - + only"
	RuleDotSegment    = "has a dot segment"
	RuleNotCanonical  = "is not in canonical form"
)

// RepositoryWhy reports which rule s breaks as a repository identity, or "":
// at most MaxRepositoryBytes, the identity grammar, no dot segment, host in
// lower case, no default port (22 or 443), no `.git` suffix. It quotes nothing of s.
func RepositoryWhy(s string) string {
	switch {
	case s == "":
		return RuleEmpty
	case len(s) > MaxRepositoryBytes:
		return RuleTooLong
	case !identityRE.MatchString(s):
		return RuleGrammar
	}
	for _, seg := range strings.Split(s, "/") {
		if seg == "." || seg == ".." {
			return RuleDotSegment
		}
	}
	host, _, _ := strings.Cut(s, "/")
	if host != strings.ToLower(host) || strings.HasSuffix(host, ":22") || strings.HasSuffix(host, ":443") || strings.HasSuffix(s, ".git") {
		return RuleNotCanonical
	}
	return ""
}

// Valid reports whether the identity is well formed and canonical.
func (r Repository) Valid() bool { return RepositoryWhy(string(r)) == "" }

var scpRE = regexp.MustCompile(`^(?:[A-Za-z0-9._~-]+@)?([A-Za-z0-9.-]{2,}):(.+)$`)

// NormalizeOrigin reads a remote URL as a repository identity: the host in lower
// case, with its port when the URL names one that is not a default (22 or 443),
// and the path with the scheme, the user and credentials, a trailing slash and a
// trailing `.git` removed. Local paths, file: URLs, query strings and fragments
// have no identity. The second result is the rule the origin broke, "" on
// success; it never contains any of the origin.
func NormalizeOrigin(raw string) (Repository, string) {
	raw = strings.TrimSpace(raw)
	var host, path string
	switch {
	case raw == "":
		return "", RuleEmpty
	case len(raw) > 4096 || !utf8.ValidString(raw):
		return "", RuleUnparseable
	case strings.Contains(raw, "://"):
		u, err := url.Parse(raw)
		if err != nil {
			return "", RuleUnparseable
		}
		switch u.Scheme {
		case "https", "http", "ssh", "git", "git+ssh", "ssh+git":
		default:
			return "", RuleNotRemote
		}
		if u.Hostname() == "" {
			return "", RuleNoHost
		}
		if u.RawQuery != "" || u.Fragment != "" {
			return "", RuleQueryFragment
		}
		host = strings.ToLower(u.Hostname())
		if p := u.Port(); p != "" && p != "22" && p != "443" {
			host += ":" + p
		}
		path = u.Path
	case strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, ".") || strings.HasPrefix(raw, "~"):
		return "", RuleLocalPath
	default:
		m := scpRE.FindStringSubmatch(raw)
		if m == nil {
			return "", RuleNotHostPath
		}
		host, path = strings.ToLower(m[1]), m[2]
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	id := host + "/" + path
	if w := RepositoryWhy(id); w != "" {
		return "", w
	}
	return Repository(id), ""
}

var driveRE = regexp.MustCompile(`^[A-Za-z]:`)

// PathFault reports why p is not a repository-relative path, as a cause and a
// reason that quotes nothing of p, or "": at most MaxPathBytes, valid UTF-8,
// nonempty, relative, slash separated, no empty segment, no `.` or `..` segment,
// no `.git` segment (in any case), no backslash, no drive letter, no leading
// dash, no NUL, newline or other control character. The causes are required,
// too-long, invalid-utf8, control-character, path-escapes and invalid-path.
func PathFault(p string) (Cause, string) {
	switch {
	case p == "":
		return CauseRequired, "the path is empty"
	case len(p) > MaxPathBytes:
		return CauseTooLong, "a path is at most 512 bytes"
	case !utf8.ValidString(p):
		return CauseInvalidUTF8, "the path is not valid UTF-8"
	case TextFault(p, MaxPathBytes) != "":
		return CauseControlChar, "the path holds a control, NUL or newline character"
	case strings.HasPrefix(p, "/") || driveRE.MatchString(p):
		return CausePathEscapes, "the path is absolute; paths are relative to the repository root"
	case strings.Contains(p, `\`):
		return CauseInvalidPath, "the path holds a backslash"
	case p[0] == '-':
		return CauseInvalidPath, "the path starts with a dash, which a command would read as an option"
	}
	for _, seg := range strings.Split(p, "/") {
		switch {
		case seg == "..":
			return CausePathEscapes, "the path climbs out of the repository"
		case seg == "", seg == ".":
			return CauseInvalidPath, "the path has an empty or `.` segment or a trailing slash"
		case strings.EqualFold(seg, ".git"):
			return CauseInvalidPath, "the path has a `.git` segment"
		}
	}
	return "", ""
}

// ValidPath reports whether p is a repository-relative path.
func ValidPath(p string) bool { c, _ := PathFault(p); return c == "" }
