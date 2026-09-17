// Local GPU capability probe for docs/SPEC-SANDBOX.md, nova-tools #230.
//
// A bounded local model trial on Apple Silicon runs MLX GPU arithmetic
// normally, but the same operation inside nova-sandbox fails at import with
// `[metal::load_device] No Metal device available`. The probe below runs the
// tiny operation under the proposed child policy and classifies the outcome,
// so a reader learns before downloading a large model whether the failure is a
// missing runtime, a package-discovery failure (the wrapper resolves the
// virtualenv Python symlink to its base executable), a missing Metal device,
// or a policy refusal. GPU availability is never inferred from an unconfined
// parent probe.
//
// The only explicit capability is `--gpu none|metal` (default none). Opting
// in records intent on the SANDBOX OK line; it never widens mach-lookup and
// never grants blanket device access. The minimum Metal mechanisms are still
// unmeasured, so a metal probe that reaches the device reports
// device_unavailable rather than silently passing.
package sandbox

import (
	"os"
	"path/filepath"
	"strings"
)

// GPUMode is the explicit local GPU capability. The zero value is no GPU.
type GPUMode string

const (
	GPUNone  GPUMode = "none"
	GPUMetal GPUMode = "metal"
)

// GPUStatus is the bounded taxonomy a GPU probe reports. Each value names an
// actionable diagnostic, never a raw traceback.
type GPUStatus string

const (
	GPUOK                GPUStatus = "gpu_ok"
	GPUMissingRuntime    GPUStatus = "missing_runtime"
	GPUPackageDiscovery  GPUStatus = "package_discovery"
	GPUDeviceUnavailable GPUStatus = "device_unavailable"
	GPUPolicyRefusal     GPUStatus = "policy_refusal"
)

// GPUResult is one classified GPU probe outcome. Detail is one bounded line;
// it never carries prompts, credentials, or model output.
type GPUResult struct {
	Status GPUStatus
	Detail string
}

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

// ClassifyGPUProbe sorts one child run's combined output into the taxonomy
// above. It reads only the bounded diagnostic text the child printed; tests
// use fakes and never call a provider, and availability is never inferred
// from an unconfined parent probe.
func ClassifyGPUProbe(output string, code int) GPUResult {
	lower := strings.ToLower(output)
	switch {
	case strings.Contains(output, "metal::load_device"),
		strings.Contains(lower, "no metal device available"):
		return GPUResult{GPUDeviceUnavailable, "metal device unavailable under the child policy: no Metal device answered inside the wall"}
	case strings.Contains(output, "ModuleNotFoundError"),
		strings.Contains(output, "No module named"),
		strings.Contains(lower, "no module named"):
		return GPUResult{GPUPackageDiscovery, "package discovery failed: the child python cannot import the inference package; name the virtualenv site-packages with --read"}
	case strings.Contains(lower, "no such file or directory"),
		strings.Contains(lower, "command not found"),
		strings.Contains(lower, "not found in"),
		strings.Contains(lower, "no python"):
		return GPUResult{GPUMissingRuntime, "missing runtime: the child has no python or inference runtime on its wall-visible PATH"}
	case strings.Contains(output, "Operation not permitted"),
		strings.Contains(output, "sandbox-exec"),
		strings.Contains(lower, "denied"),
		code != 0:
		return GPUResult{GPUPolicyRefusal, "policy refusal: the wall denied what the probe asked; compare with the same command outside the wall"}
	}
	if code == 0 {
		return GPUResult{GPUOK, "gpu arithmetic answered inside the wall"}
	}
	return GPUResult{GPUPolicyRefusal, "policy refusal: the wall denied what the probe asked; compare with the same command outside the wall"}
}

// VenvSitePackages returns the site-packages directory belonging to the
// virtualenv whose python is at pythonExe, WITHOUT resolving the symlink to
// its base executable. A venv python is a symlink to a base interpreter, so
// EvalSymlinks on it names the base and loses the venv's packages; this reads
// the link's own directory (<venv>/bin/python3 -> <venv>/lib/python*/site-packages)
// and reports the first existing candidate. The caller names the result with
// --read; nothing is guessed outside the spelled venv.
func VenvSitePackages(pythonExe string) (string, error) {
	if strings.TrimSpace(pythonExe) == "" {
		return "", errf("python path is empty: name the virtualenv python")
	}
	bin := filepath.Dir(pythonExe)
	venv := filepath.Dir(bin)
	entries, err := os.ReadDir(filepath.Join(venv, "lib"))
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "python") {
			continue
		}
		candidate := filepath.Join(venv, "lib", e.Name(), "site-packages")
		if fi, err := os.Stat(candidate); err == nil && fi.IsDir() {
			return candidate, nil
		}
	}
	return "", errf("no site-packages under %s: name the virtualenv site-packages with --read", filepath.Join(venv, "lib"))
}

func errf(format string, a ...any) error {
	return refuse("bad_read", format, a...)
}
