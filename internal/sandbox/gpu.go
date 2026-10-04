// Local GPU capability for docs/SPEC-SANDBOX.md, nova-tools #230.
//
// The only explicit capability is `--gpu none|metal` (default none). Opting
// in records intent on the SANDBOX OK line; it never widens mach-lookup and
// never grants blanket device access.
package sandbox

import (
	"strings"
)

// GPUMode is the explicit local GPU capability. The zero value is no GPU.
type GPUMode string

const (
	GPUNone  GPUMode = "none"
	GPUMetal GPUMode = "metal"
)

// ParseGPUMode accepts only the explicit capabilities. A blanket grant such
// as "all" is refused: filesystem, network, clipboard, and agent-socket
// constraints stay intact under every mode.
func ParseGPUMode(s string) (GPUMode, *Refusal) {
	switch GPUMode(strings.TrimSpace(s)) {
	case "", GPUNone:
		return GPUNone, nil
	case GPUMetal:
		return GPUMetal, nil
	}
	r := refuse("bad_gpu", "--gpu wants none or metal and got %s: --gpu <none|metal>", s)
	return GPUNone, &r
}
