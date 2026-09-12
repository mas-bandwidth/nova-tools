//go:build linux

package main

import (
	"os"
	"strconv"
)

// parentExecutable is /proc/<pid>/exe, which is the kernel's own answer and needs no
// helper process. The linux (Landlock) body of docs/SPEC-SANDBOX.md is not built yet, so
// no probe reaches this on linux today; it is here so that the guard is not a darwin
// special case waiting to be discovered when that body lands.
func parentExecutable(pid int) (string, error) {
	return os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
}
