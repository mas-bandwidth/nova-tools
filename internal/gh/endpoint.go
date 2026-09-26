package gh

import "strings"

// Endpoint is the shape a call is counted under: METHOD and the path with
// the owner/name pair after repos as {repo}, every number as {n}, every
// 40-hex sha as {sha}, and no query. GET /repos/o/r/pulls/12 and
// GET /repos/o/r/pulls/13 are the same endpoint.
func Endpoint(method, path string) string {
	path, _, _ = strings.Cut(path, "?")
	segs := strings.Split(strings.Trim(path, "/"), "/")
	out := make([]string, 0, len(segs))
	for i := 0; i < len(segs); i++ {
		s := segs[i]
		switch {
		case s == "":
			continue
		case s == "repos" && i+2 < len(segs):
			out = append(out, "repos", "{repo}")
			i += 2
		case isDigits(s):
			out = append(out, "{n}")
		case isSHA(s):
			out = append(out, "{sha}")
		default:
			out = append(out, s)
		}
	}
	return strings.ToUpper(strings.TrimSpace(method)) + " /" + strings.Join(out, "/")
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
