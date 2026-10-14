package gitrun

import "strings"

// DefaultGitIdentity is the identity the sprint machine commits as when the sprint row
// names none: a generic machine identity, which the coordinator sets to the fleet's own.
const DefaultGitIdentity = "nova-sprint <nova-sprint@example.invalid>"

// GitIdentity is one parsed identity: the name and email of "Name <email>".
type GitIdentity struct {
	Name, Email string
}

// ParseGitIdentity parses a git identity as "Name <email>": the name is everything before
// the last " <", the email the text between the last "<" and ">". ok is false when the
// value is empty or does not spell a name and an email.
func ParseGitIdentity(v string) (GitIdentity, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return GitIdentity{}, false
	}
	lt := strings.LastIndex(v, "<")
	gt := strings.LastIndex(v, ">")
	if lt < 0 || gt < lt {
		return GitIdentity{}, false
	}
	name := strings.TrimSpace(v[:lt])
	email := strings.TrimSpace(v[lt+1 : gt])
	if name == "" || email == "" {
		return GitIdentity{}, false
	}
	return GitIdentity{Name: name, Email: email}, true
}

// Env is the GIT_AUTHOR_*/GIT_COMMITTER_* environment this identity sets, so a commit
// takes the configured name and email whatever the host's global git config or the
// caller's environment held.
func (id GitIdentity) Env() []string {
	return []string{
		"GIT_AUTHOR_NAME=" + id.Name,
		"GIT_AUTHOR_EMAIL=" + id.Email,
		"GIT_COMMITTER_NAME=" + id.Name,
		"GIT_COMMITTER_EMAIL=" + id.Email,
	}
}
