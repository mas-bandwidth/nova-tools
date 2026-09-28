//go:build !unix

package testredis

import "os/exec"

// Without process groups there is no sentry: a server is its test's, and the
// test's cleanup is what stops it.

func join(*exec.Cmd, int) {}

func enlist(sentrySpec) (*post, error) {
	return &post{gone: make(chan struct{})}, nil
}
