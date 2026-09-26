//go:build !linux && (!darwin || (!amd64 && !arm64))

package life

import "fmt"

// ProbeProcess reports an unsupported observation instead of inferring life
// from a PID. A harness adapter is required on this platform.
func ProbeProcess(pid int) ProcessSample {
	return ProcessSample{Err: fmt.Errorf("process creation identity unsupported on this platform")}
}
