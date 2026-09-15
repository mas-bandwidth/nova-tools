//go:build !linux

package sandbox

import (
	"fmt"
	"io"
	"runtime"
)

// LinuxChildVerb is the hidden verb's name; the linux body defines its own copy
// under its build tag, and this one exists so cmd/nova-sandbox compiles here.
const LinuxChildVerb = "linux-child"

// LinuxChild is the hidden verb's body on every platform whose linux body is not
// built. It is unreachable by any path but a caller typing it — Run REFUSES on
// every such platform (wrap_darwin.go and wrap_other.go) — and this refusal is that
// caller's answer.
func LinuxChild(args []string, stdout, stderr io.Writer, env []string) int {
	_ = args
	fmt.Fprintf(stderr, "SANDBOX REFUSED reason=probe_step_not_a_child: linux-child runs only as the child of a linux wrap; this %s build has no linux body\n", runtime.GOOS)
	return ExitCannotRun
}
