package main

import (
	"strings"
	"testing"
)

// gitAs is a git with an identity, for the commits a test makes.
func gitAs(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return strings.TrimSpace(runGit(t, dir, append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...))
}
