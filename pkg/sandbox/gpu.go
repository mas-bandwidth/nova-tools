// Local GPU capability for docs/SPEC-SANDBOX.md, nova-tools #230.
//
// The only explicit capability is `--gpu none|metal` (default none). Opting
// in prints gpu=metal on the SANDBOX OK line and appends MetalGrants to the
// darwin profile: two named IOKit user clients and three named XPC services,
// never a blanket iokit-open or mach-lookup.
package sandbox

import (
	"strings"
)

// MetalGrants is the darwin profile text --gpu metal appends: the measured minimum for a
// headless Metal renderer that compiles its shaders from source and records and decodes
// HEVC/H.264 video (measured 2026-10-19 on mini-m5-1, Apple M5 Pro, macOS 27, with VG
// render2 --record and a MTLCreateSystemDefaultDevice + makeLibrary(source:) + compute
// probe, each clause removed in turn):
//
//	AGXDeviceUserClient       MTLCreateSystemDefaultDevice returns nil without it, and
//	                          render2 dies "a Metal 4 capable GPU is required";
//	IOSurfaceRootUserClient   render2 dies silently at its first frame without it;
//	MTLCompilerService        makeLibrary(source:) fails "Unable to reach
//	                          MTLCompilerService ... Sandbox restriction" without it;
//	coremedia.videoencoder    AVAssetWriter startWriting fails without it;
//	coremedia.videodecoder    AVAssetReader startReading "Cannot Decode" without it.
//
// iokit-get-properties was measured unnecessary and is not granted. No global-name is
// added, so the clipboard, the pasteboard and every launchd service stay denied.
const MetalGrants = `
;; --gpu metal: the measured minimum for Metal compute/render, the shader compiler and
;; the hardware video encoder/decoder (pkg/sandbox/gpu.go MetalGrants). Named user
;; clients and named XPC services only; never a blanket iokit-open or mach-lookup.
(allow iokit-open (iokit-user-client-class "AGXDeviceUserClient" "IOSurfaceRootUserClient"))
(allow mach-lookup (xpc-service-name "com.apple.MTLCompilerService" "com.apple.coremedia.videoencoder" "com.apple.coremedia.videodecoder"))
`

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
