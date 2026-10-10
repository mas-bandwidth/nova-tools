//go:build windows

package functionalrun

import "os/exec"

// setOwnProcessGroup does nothing on Windows: the functional tier runs in a
// Linux container through a unix client, and the package only has to build.
func setOwnProcessGroup(*exec.Cmd) {}
