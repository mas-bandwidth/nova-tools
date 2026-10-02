package update

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/release"
	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// benchPlatformRow defines one row of the fleet's installation matrix (#4321).
type benchPlatformRow struct {
	benchKind   string // "darwin-arm64", "linux-amd64", "wsl2"
	platform    string // flag value passed to --platform
	targetGOOS  string // expected target GOOS
	targetArch  string // expected target GOARCH
	description string // fleet role description
}

// fleetMatrix defines every bench kind the fleet has.
var fleetMatrix = []benchPlatformRow{
	{
		benchKind:   "darwin-arm64",
		platform:    "darwin-arm64",
		targetGOOS:  "darwin",
		targetArch:  "arm64",
		description: "darwin-arm64 host",
	},
	{
		benchKind:   "linux-amd64",
		platform:    "linux-amd64",
		targetGOOS:  "linux",
		targetArch:  "amd64",
		description: "linux-amd64 host",
	},
	{
		benchKind:   "wsl2",
		platform:    "linux-amd64",
		targetGOOS:  "linux",
		targetArch:  "amd64",
		description: "wsl2 host",
	},
}

// stageReleaseArtifacts lays down a mock release directory tree for a given version
// and platform with real runnable mock binaries and a verified SHA256SUMS file.
func stageReleaseArtifacts(t *testing.T, root, version, platform string, tools []string) string {
	t.Helper()
	goos, goarch, err := release.Platform(platform)
	if err != nil {
		t.Fatal(err)
	}
	platDir := release.ArtifactDir(root, version, goos, goarch)
	if err := os.MkdirAll(platDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var sumLines []string
	for _, tool := range tools {
		toolFile := release.ToolFile(tool, goos)
		toolPath := filepath.Join(platDir, toolFile)
		script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = \"version\" ]; then\n  echo \"%s %s %s/%s\"\n  exit 0\nfi\necho \"%s %s %s/%s\"\n",
			tool, version, goos, goarch, tool, version, goos, goarch)
		if err := testbin.WriteExecutable(toolPath, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(toolPath)
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(data)
		sumLines = append(sumLines, fmt.Sprintf("%x  %s", h, toolFile))
	}
	sumsPath := filepath.Join(platDir, release.SumsFile)
	if err := os.WriteFile(sumsPath, []byte(strings.Join(sumLines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return platDir
}

// TestInstallFunctionalMatrix verifies nova-update install across every bench kind
// in the fleet (darwin-arm64, linux-amd64, and WSL2), ensuring one-step install
// functions correctly, idempotently skips current tools, and installs executable binaries.
func TestInstallFunctionalMatrix(t *testing.T) {
	t.Parallel()

	standardTools := []string{"nova-bus", "nova-swarm", "nova-wake", "nova-update"}

	for _, row := range fleetMatrix {
		row := row
		t.Run(row.benchKind, func(t *testing.T) {
			t.Parallel()

			from := t.TempDir()
			bin := t.TempDir()
			retireDir := t.TempDir()
			version := "v0.16.0"

			stageReleaseArtifacts(t, from, version, row.platform, standardTools)

			// Pre-populate retire directory with an obsolete tool and an unrelated file
			obsoleteFile := filepath.Join(retireDir, "nova-bus")
			if err := testbin.WriteExecutable(obsoleteFile, []byte("#!/bin/sh\necho old\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			unrelatedFile := filepath.Join(retireDir, "unrelated.txt")
			if err := os.WriteFile(unrelatedFile, []byte("preserve me"), 0o644); err != nil {
				t.Fatal(err)
			}

			// 1. Direct one-step installation without `release` prefix
			args := []string{
				"install",
				"--from", from,
				"--version", version,
				"--bin", bin,
				"--retire", retireDir,
				"--platform", row.platform,
			}

			var out, errs bytes.Buffer
			code := Main("nova-update", args, version, &out, &errs)
			if code != 0 {
				t.Fatalf("[%s] nova-update install failed: code=%d errs=%s", row.benchKind, code, errs.String())
			}

			// Verify receipt output line
			expectedOutput := fmt.Sprintf("RELEASE INSTALLED version=%s tools=4 skipped=0 retired=1 bin=%s platform=%s",
				version, bin, row.targetGOOS+"-"+row.targetArch)
			if !strings.Contains(out.String(), expectedOutput) {
				t.Fatalf("[%s] expected output line %q, got:\n%s", row.benchKind, expectedOutput, out.String())
			}

			// Verify each installed binary is executable and returns the expected version
			for _, tool := range standardTools {
				toolPath := filepath.Join(bin, release.ToolFile(tool, row.targetGOOS))
				info, err := os.Stat(toolPath)
				if err != nil {
					t.Fatalf("[%s] %s not installed in %s: %v", row.benchKind, tool, bin, err)
				}
				if !info.Mode().IsRegular() {
					t.Fatalf("[%s] %s is not a regular file", row.benchKind, toolPath)
				}
				if info.Mode().Perm()&0o111 == 0 {
					t.Fatalf("[%s] %s does not have executable permissions: %v", row.benchKind, toolPath, info.Mode())
				}

				cmd := exec.Command(toolPath, "version")
				output, err := cmd.Output()
				if err != nil {
					t.Fatalf("[%s] executing %s version failed: %v", row.benchKind, toolPath, err)
				}
				expectedSubstr := tool + " " + version
				if !strings.Contains(string(output), expectedSubstr) {
					t.Fatalf("[%s] %s version output %q does not contain %q", row.benchKind, toolPath, string(output), expectedSubstr)
				}
			}

			// Verify retired tool was deleted and unrelated file was untouched
			if _, err := os.Stat(obsoleteFile); !os.IsNotExist(err) {
				t.Fatalf("[%s] obsolete file %s was not retired", row.benchKind, obsoleteFile)
			}
			if _, err := os.Stat(unrelatedFile); err != nil {
				t.Fatalf("[%s] unrelated file %s was unexpectedly removed: %v", row.benchKind, unrelatedFile, err)
			}

			// 2. Second pass: idempotency and skip logic
			out.Reset()
			errs.Reset()
			code2 := Main("nova-update", args, version, &out, &errs)
			if code2 != 0 {
				t.Fatalf("[%s] second install pass failed: code=%d errs=%s", row.benchKind, code2, errs.String())
			}

			expectedSkip := fmt.Sprintf("RELEASE INSTALLED version=%s tools=0 skipped=4 retired=0 bin=%s platform=%s",
				version, bin, row.targetGOOS+"-"+row.targetArch)
			if !strings.Contains(out.String(), expectedSkip) {
				t.Fatalf("[%s] expected skip line %q, got:\n%s", row.benchKind, expectedSkip, out.String())
			}

			// 3. Upgrade pass: v0.17.0 with partial current binary
			v2 := "v0.17.0"
			stageReleaseArtifacts(t, from, v2, row.platform, standardTools)

			// Pre-upgrade one tool (nova-wake) to v0.17.0 to verify partial skip
			wakePath := filepath.Join(bin, release.ToolFile("nova-wake", row.targetGOOS))
			v2Script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = \"version\" ]; then\n  echo \"nova-wake %s %s/%s\"\n  exit 0\nfi\necho \"nova-wake %s %s/%s\"\n",
				v2, row.targetGOOS, row.targetArch, v2, row.targetGOOS, row.targetArch)
			if err := testbin.WriteExecutable(wakePath, []byte(v2Script), 0o755); err != nil {
				t.Fatal(err)
			}

			argsV2 := []string{
				"install",
				"--from", from,
				"--version", v2,
				"--bin", bin,
				"--platform", row.platform,
			}
			out.Reset()
			errs.Reset()
			code3 := Main("nova-update", argsV2, v2, &out, &errs)
			if code3 != 0 {
				t.Fatalf("[%s] v2 install failed: code=%d errs=%s", row.benchKind, code3, errs.String())
			}

			expectedPartial := fmt.Sprintf("RELEASE INSTALLED version=%s tools=3 skipped=1 retired=0 bin=%s platform=%s",
				v2, bin, row.targetGOOS+"-"+row.targetArch)
			if !strings.Contains(out.String(), expectedPartial) {
				t.Fatalf("[%s] expected partial upgrade line %q, got:\n%s", row.benchKind, expectedPartial, out.String())
			}
		})
	}
}

// TestInstallRefusalMatrix verifies that checksum mismatches, missing flags,
// or non-existent directories refuse cleanly before touching the target bin directory.
func TestInstallRefusalMatrix(t *testing.T) {
	t.Parallel()

	for _, row := range fleetMatrix {
		row := row
		t.Run(row.benchKind, func(t *testing.T) {
			t.Parallel()

			from := t.TempDir()
			bin := t.TempDir()
			version := "v0.16.0"

			platDir := stageReleaseArtifacts(t, from, version, row.platform, []string{"nova-bus", "nova-swarm"})

			// Tamper with nova-bus binary after checksum generation
			busPath := filepath.Join(platDir, release.ToolFile("nova-bus", row.targetGOOS))
			if err := testbin.WriteExecutable(busPath, []byte("tampered-content"), 0o755); err != nil {
				t.Fatal(err)
			}

			args := []string{
				"install",
				"--from", from,
				"--version", version,
				"--bin", bin,
				"--platform", row.platform,
			}

			var out, errs bytes.Buffer
			code := Main("nova-update", args, version, &out, &errs)
			if code == 0 {
				t.Fatalf("[%s] expected failure on tampered checksum, but exited 0", row.benchKind)
			}
			if !strings.Contains(errs.String(), "does not match SHA256SUMS") {
				t.Fatalf("[%s] expected checksum mismatch refusal, got:\n%s", row.benchKind, errs.String())
			}

			// Atomic refusal guarantee: bin directory must remain completely empty
			entries, err := os.ReadDir(bin)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("[%s] bin directory contains %d files after refused install; want 0", row.benchKind, len(entries))
			}
		})
	}
}

// TestInstallMissingFlagsRefusal verifies required flags for nova-update install.
func TestInstallMissingFlagsRefusal(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"no_flags", []string{"install"}},
		{"missing_version", []string{"install", "--from", "dir", "--bin", "dir"}},
		{"missing_from", []string{"install", "--version", "v1.0.0", "--bin", "dir"}},
		{"missing_bin", []string{"install", "--from", "dir", "--version", "v1.0.0"}},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var out, errs bytes.Buffer
			code := Main("nova-update", tc.args, "", &out, &errs)
			if code != 2 {
				t.Fatalf("%s exited %d, want 2; errs: %s", tc.name, code, errs.String())
			}
		})
	}
}

// TestDirectInstallDispatchParity verifies that `nova-update install` produces
// byte-identical dispatch results to `nova-update release install`.
func TestDirectInstallDispatchParity(t *testing.T) {
	t.Parallel()

	tools := []string{"nova-bus", "nova-update"}
	version := "v0.16.0"

	from := t.TempDir()
	stageReleaseArtifacts(t, from, version, "darwin-arm64", tools)

	bin1 := t.TempDir()
	bin2 := t.TempDir()

	var out1, errs1 bytes.Buffer
	code1 := Main("nova-update", []string{"install", "--from", from, "--version", version, "--bin", bin1, "--platform", "darwin-arm64"}, version, &out1, &errs1)
	if code1 != 0 {
		t.Fatalf("nova-update install failed: %s", errs1.String())
	}

	var out2, errs2 bytes.Buffer
	code2 := Main("nova-update", []string{"release", "install", "--from", from, "--version", version, "--bin", bin2, "--platform", "darwin-arm64"}, version, &out2, &errs2)
	if code2 != 0 {
		t.Fatalf("nova-update release install failed: %s", errs2.String())
	}

	// Compare outputs (normalizing temporary bin paths)
	norm1 := strings.ReplaceAll(out1.String(), bin1, "{bin}")
	norm2 := strings.ReplaceAll(out2.String(), bin2, "{bin}")
	if norm1 != norm2 {
		t.Fatalf("dispatch parity mismatch:\ninstall:         %s\nrelease install: %s", norm1, norm2)
	}
}
