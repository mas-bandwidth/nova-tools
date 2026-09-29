package definition

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// MaxIdentityBytes bounds a repository identity.
const MaxIdentityBytes = 256

var (
	identityRE = regexp.MustCompile(`^[A-Za-z0-9._~-]+(?::[0-9]{1,5})?(?:/[A-Za-z0-9._~+-]+)+$`)
	scpRE      = regexp.MustCompile(`^(?:[A-Za-z0-9._~-]+@)?([A-Za-z0-9.-]{2,}):(.+)$`)
)

// identityWhy is why s is not a repository identity: `host[:port]/segment/...`,
// no scheme, no user, no dot segments, at most MaxIdentityBytes.
func identityWhy(s string) string {
	switch {
	case len(s) > MaxIdentityBytes:
		return fmt.Sprintf("the identity is %d bytes, at most %d", len(s), MaxIdentityBytes)
	case !identityRE.MatchString(s):
		return fmt.Sprintf("identity %q is not host[:port]/owner/name (letters, digits and . _ ~ - + only, no scheme, no user)", s)
	}
	for _, seg := range strings.Split(s, "/") {
		if seg == "." || seg == ".." {
			return fmt.Sprintf("identity %q has a dot segment", s)
		}
	}
	return ""
}

// normalizeOrigin reads a remote URL as a repository identity: the host (lower
// case, with a port when the URL names one) and the path, with the scheme, the
// user and credentials, a trailing slash and a trailing .git removed. Local paths,
// file: URLs, query strings and fragments have no identity.
func normalizeOrigin(raw string) (identity, why string) {
	raw = strings.TrimSpace(raw)
	var host, path string
	switch {
	case raw == "":
		return "", "the origin URL is empty"
	case strings.Contains(raw, "://"):
		u, err := url.Parse(raw)
		if err != nil {
			return "", "the origin URL does not parse"
		}
		switch u.Scheme {
		case "https", "http", "ssh", "git", "git+ssh", "ssh+git":
		default:
			return "", fmt.Sprintf("the origin scheme %q is not a remote", u.Scheme)
		}
		if u.Hostname() == "" || u.RawQuery != "" || u.Fragment != "" {
			return "", "the origin URL has no host, or carries a query or fragment"
		}
		host = strings.ToLower(u.Hostname())
		if p := u.Port(); p != "" {
			host += ":" + p
		}
		path = u.Path
	case strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, ".") || strings.HasPrefix(raw, "~"):
		return "", "the origin is a local path"
	default:
		m := scpRE.FindStringSubmatch(raw)
		if m == nil {
			return "", "the origin is neither a URL nor host:path"
		}
		host, path = strings.ToLower(m[1]), m[2]
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	id := host + "/" + path
	if w := identityWhy(id); w != "" {
		return "", w
	}
	return id, ""
}
