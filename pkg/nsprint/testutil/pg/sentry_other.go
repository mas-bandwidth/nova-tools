//go:build !unix

package pg

import "os/exec"

// Without process groups there is no sentry: a postmaster is its test's, and
// the test's cleanup is what stops it.

func join(*exec.Cmd, int) {}

func enlist() (*post, error) {
	return &post{gone: make(chan struct{})}, nil
}
