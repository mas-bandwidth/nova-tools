package sandbox

// Red test for nova-tools #230: local inference under nova-sandbox fails at
// import with `[metal::load_device] No Metal device available`, and the wrapper
// resolves the virtualenv Python symlink to its base executable, losing the
// venv's packages before Metal itself is tested. The probe must distinguish
// missing runtime, package-discovery failure, device unavailable, and policy
// refusal under the proposed child policy, never from an unconfined parent.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGPUProbeDistinguishesMetalDeviceUnavailableFromPolicyRefusal(t *testing.T) {
	r := ClassifyGPUProbe("[metal::load_device] No Metal device available\n", 1)
	if r.Status != GPUDeviceUnavailable {
		t.Fatalf("metal load failure classified as %q, want %q (%s)", r.Status, GPUDeviceUnavailable, r.Detail)
	}
}

func TestGPUProbeDistinguishesPackageDiscoveryFailure(t *testing.T) {
	r := ClassifyGPUProbe("ModuleNotFoundError: No module named 'mlx'\n", 1)
	if r.Status != GPUPackageDiscovery {
		t.Fatalf("missing mlx import classified as %q, want %q (%s)", r.Status, GPUPackageDiscovery, r.Detail)
	}
}

func TestGPUProbeDistinguishesMissingRuntime(t *testing.T) {
	r := ClassifyGPUProbe("python3: No such file or directory\n", 127)
	if r.Status != GPUMissingRuntime {
		t.Fatalf("missing python classified as %q, want %q (%s)", r.Status, GPUMissingRuntime, r.Detail)
	}
}

func TestVenvPythonKeepsItsPackagesInsteadOfResolvingToBase(t *testing.T) {
	base := t.TempDir()
	venvBin := filepath.Join(base, "venv", "bin")
	if err := os.MkdirAll(venvBin, 0o755); err != nil {
		t.Fatal(err)
	}
	site := filepath.Join(base, "venv", "lib", "python3.13", "site-packages")
	if err := os.MkdirAll(site, 0o755); err != nil {
		t.Fatal(err)
	}
	// The venv python is a symlink to a base executable elsewhere; resolving it
	// must not lose the venv's own site-packages.
	target := filepath.Join(base, "base-python3")
	if err := os.WriteFile(target, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(venvBin, "python3")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	got, err := VenvSitePackages(link)
	if err != nil {
		t.Fatalf("VenvSitePackages(%q): %s", link, err)
	}
	if got != site {
		t.Fatalf("VenvSitePackages(%q) = %q, want %q: the wrapper resolved the symlink to its base and lost the venv", link, got, site)
	}
}

func TestParseGPUModeRejectsBlanketAccess(t *testing.T) {
	if _, err := ParseGPUMode("all"); err == nil {
		t.Fatal("ParseGPUMode(\"all\") accepted a blanket GPU grant; only none|metal are explicit capabilities")
	}
	m, err := ParseGPUMode("metal")
	if err != nil || m != GPUMetal {
		t.Fatalf("ParseGPUMode(metal) = %q, %v; want metal", m, err)
	}
}
