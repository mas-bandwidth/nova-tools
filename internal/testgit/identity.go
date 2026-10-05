// Package testgit sets a fixed author and committer on git commands a test runs.
// A hosted runner has no global git identity, so a commit in a scratch repository
// has to name both on the command itself.
package testgit

import (
	"os"
	"os/exec"
	"strings"
)

// Name and Email are the fixture identity every helper-built commit carries.
const (
	Name  = "Test User"
	Email = "test@example.com"
)

const (
	authorName     = "GIT_AUTHOR_NAME"
	authorEmail    = "GIT_AUTHOR_EMAIL"
	committerName  = "GIT_COMMITTER_NAME"
	committerEmail = "GIT_COMMITTER_EMAIL"
)

// Env is the four identity variables, author and committer both set.
func Env() []string {
	return []string{
		authorName + "=" + Name,
		authorEmail + "=" + Email,
		committerName + "=" + Name,
		committerEmail + "=" + Email,
	}
}

// Apply sets the fixture identity on cmd. An identity already in cmd.Env is
// replaced, so the fixture is the one git sees. Every other variable is kept.
// A nil Env starts from the process environment.
func Apply(cmd *exec.Cmd) {
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	kept := cmd.Env[:0:0]
	for _, e := range cmd.Env {
		if identityKey(e) {
			continue
		}
		kept = append(kept, e)
	}
	cmd.Env = append(kept, Env()...)
}

func identityKey(e string) bool {
	k, _, ok := strings.Cut(e, "=")
	if !ok {
		return false
	}
	switch k {
	case authorName, authorEmail, committerName, committerEmail:
		return true
	default:
		return false
	}
}
