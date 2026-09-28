//go:build !unix

package stream

import "os/exec"

func ownGroup(*exec.Cmd) {}
