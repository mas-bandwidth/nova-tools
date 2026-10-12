package toolchainfixture

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Check runs the real toolchains role against tiny archives for all nine pinned
// tools, then verifies one lifecycle behavior. Each caller is its own package so
// certification isolates its Ansible process and gives it the same package bound.
func Check(t *testing.T, fleetRoot, scenario string) {
	t.Helper()
	require.Contains(t, []string{"idempotent", "drift", "bad-digest"}, scenario)
	// Each isolated verification package performs a real install of all nine tools.
	// Its verification play runs on that install under the existing 60-second bound.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ansible, err := exec.LookPath("ansible-playbook")
	if err != nil {
		t.Skip("ansible-playbook is not installed; real-play coverage runs in certification")
	}
	tmp := t.TempDir()
	for _, dir := range []string{"home", "ansible", "local", "remote"} {
		require.NoError(t, os.MkdirAll(filepath.Join(tmp, dir), 0o700))
	}
	archivePath := filepath.Join(tmp, "fixtures.tar.gz")
	sha := writeToolchainFixtureArchive(t, archivePath, "fixture")
	varsPath := filepath.Join(tmp, "toolchains.json")
	writeToolchainFixtureVars(t, varsPath, tmp, sha)

	first := runToolchainsFixturePlay(t, ctx, ansible, fleetRoot, tmp, varsPath, false, false)
	assert.Contains(t, first, "PLAY RECAP")
	assert.NotContains(t, first, "failed=1", first)
	assertToolchainFixtureInstall(t, tmp)

	node := filepath.Join(tmp, "sdk", "node-1.0.0", "fixture", "bin", "node")
	original, err := os.ReadFile(node)
	require.NoError(t, err)
	if scenario == "idempotent" {
		second := runToolchainsFixturePlay(t, ctx, ansible, fleetRoot, tmp, varsPath, false, false)
		assert.Regexp(t, `fixture-linux\s+: ok=\d+\s+changed=0\s+unreachable=0\s+failed=0`, second)
		return
	}
	if scenario == "drift" {
		// Replacement restores a changed executable even when its archive marker
		// still names the pinned SHA.
		require.NoError(t, os.WriteFile(node, []byte("tampered"), 0o755))
		drift := runToolchainsFixturePlay(t, ctx, ansible, fleetRoot, tmp, varsPath, false, false)
		assertToolchainFixtureInstall(t, tmp)
		restored, err := os.ReadFile(node)
		require.NoError(t, err)
		assert.Equal(t, original, restored, "drift must restore the pinned executable bytes")
		assert.Contains(t, drift, "changed=")
		return
	}
	// A different replacement archive with a bad pinned digest must be refused
	// before removing the current SDK; byte equality detects an accidental replacement.
	_ = writeToolchainFixtureArchive(t, archivePath, "replacement")
	writeToolchainFixtureVars(t, varsPath, tmp, strings.Repeat("0", 64))
	_ = runToolchainsFixturePlay(t, ctx, ansible, fleetRoot, tmp, varsPath, false, true)
	assert.FileExists(t, node, "checksum mismatch must preserve the existing install")
	preserved, err := os.ReadFile(node)
	require.NoError(t, err)
	assert.Equal(t, original, preserved, "checksum refusal must preserve the existing executable bytes")
}

type toolchainFixtureSpec struct {
	version string
	fact    string
	paths   []string
}

var toolchainFixtureSpecs = map[string]toolchainFixtureSpec{
	"node":   {"1.0.0", "bin/node", []string{"bin/node", "bin/npm", "bin/npx"}},
	"dotnet": {"1.0.0", "dotnet", []string{"dotnet"}},
	"dart":   {"1.0.0", "bin/dart", []string{"bin/dart"}},
	"elixir": {"1.0.0", "bin/elixir", []string{"bin/elixir", "bin/elixirc", "bin/iex", "bin/mix"}},
	"otp":    {"1.0.0", "bin/erl", []string{"bin/erl", "bin/erlc", "bin/escript", "bin/epmd"}},
	"rust":   {"1.0.0", "bin/rustc", []string{"bin/cargo", "bin/rustc"}},
	"jdk":    {"1.0.0", "bin/java", []string{"bin/java", "bin/javac", "bin/jar", "bin/jshell"}},
	"sbcl":   {"1.0.0", "bin/sbcl", []string{"bin/sbcl"}},
	"gh":     {"1.0.0", "bin/gh", []string{"bin/gh"}},
}

func writeToolchainFixtureArchive(t *testing.T, path, label string) string {
	t.Helper()
	f, err := os.Create(path)
	require.NoError(t, err)
	sha := sha256.New()
	gz := gzip.NewWriter(io.MultiWriter(f, sha))
	tw := tar.NewWriter(gz)
	for _, spec := range toolchainFixtureSpecs {
		for _, name := range spec.paths {
			body := []byte(fmt.Sprintf("#!/bin/sh\nprintf '%s-%s\\n'\n", label, name))
			err := tw.WriteHeader(&tar.Header{Name: filepath.ToSlash(filepath.Join("fixture", name)), Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
			require.NoError(t, err)
			_, err = tw.Write(body)
			require.NoError(t, err)
		}
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	require.NoError(t, f.Close())
	return hex.EncodeToString(sha.Sum(nil))
}

func writeToolchainFixtureVars(t *testing.T, path, tmp, sha string) {
	t.Helper()
	url := "file://" + filepath.Join(tmp, "fixtures.tar.gz")
	toolchains := map[string]any{}
	for name, spec := range toolchainFixtureSpecs {
		executables := make([]map[string]string, 0, len(spec.paths))
		for _, p := range spec.paths {
			executables = append(executables, map[string]string{"name": filepath.Base(p), "path": p})
		}
		toolchains[name] = map[string]any{
			"version": spec.version, "fact_path": spec.fact, "executables": executables,
			"platforms": map[string]any{"linux-x86_64": map[string]any{
				"url": url, "sha256": sha, "root": "fixture", "probe": spec.paths[0],
			}},
		}
	}
	vars := map[string]any{
		"nova_home":                            filepath.Join(tmp, "home"),
		"nova_sdk_home":                        filepath.Join(tmp, "sdk"),
		"nova_toolchains_fact_file":            filepath.Join(tmp, "home", ".config", "nova", "toolchains.json"),
		"nova_toolchains_allow_local_archives": true,
		"nova_toolchains":                      toolchains,
		"nova_adopt_canary":                    "none",
	}
	b, err := json.Marshal(vars)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, b, 0o600))
}

func runToolchainsFixturePlay(t *testing.T, parent context.Context, ansible, fleetRoot, tmp, varsPath string, check, wantError bool) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 40*time.Second)
	defer cancel()
	args := []string{"tools.yml", "--tags", "toolchains", "-i", "testdata/toolchains-inventory.ini", "--limit", "fixture-linux", "-e", "@" + varsPath}
	if check {
		args = append(args, "--check", "--diff")
	}
	cmd := exec.CommandContext(ctx, ansible, args...)
	cmd.WaitDelay = 5 * time.Second
	cmd.Dir = fleetRoot
	cmd.Env = append(os.Environ(),
		"HOME="+filepath.Join(tmp, "home"),
		"ANSIBLE_HOME="+filepath.Join(tmp, "ansible"),
		"ANSIBLE_LOCAL_TEMP="+filepath.Join(tmp, "local"),
		"ANSIBLE_REMOTE_TEMP="+filepath.Join(tmp, "remote"),
		"ANSIBLE_NOCOLOR=1",
		// Local fixtures need no privilege escalation or module-file transfers.
		// Pipelining keeps the real role and every install/drift assertion.
		"ANSIBLE_PIPELINING=1",
	)
	output, err := cmd.CombinedOutput()
	require.NoError(t, ctx.Err(), "fixture play exceeded its budget:\n%s", output)
	if wantError {
		require.Error(t, err, "expected Ansible to reject a mismatched fixture digest:\n%s", output)
		assert.Contains(t, strings.ToLower(string(output)), "checksum")
	} else {
		require.NoError(t, err, "ansible-playbook --tags toolchains fixture apply failed:\n%s", output)
	}
	return string(output)
}

func assertToolchainFixtureInstall(t *testing.T, tmp string) {
	t.Helper()
	for name, spec := range toolchainFixtureSpecs {
		prefix := filepath.Join(tmp, "sdk", name+"-"+spec.version, "fixture")
		for _, rel := range spec.paths {
			info, err := os.Stat(filepath.Join(prefix, rel))
			require.NoError(t, err, "%s executable missing", rel)
			assert.NotZero(t, info.Mode()&0o111, "%s is not executable", rel)
		}
		link, err := os.Readlink(filepath.Join(tmp, "sdk", "bin", filepath.Base(spec.paths[0])))
		require.NoError(t, err, "link missing for %s", name)
		assert.Equal(t, filepath.Join(prefix, spec.paths[0]), link)
	}
	var facts struct {
		Toolchains map[string]struct {
			Path    string `json:"path"`
			Version string `json:"version"`
		} `json:"toolchains"`
	}
	b, err := os.ReadFile(filepath.Join(tmp, "home", ".config", "nova", "toolchains.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &facts))
	for name, spec := range toolchainFixtureSpecs {
		fact := facts.Toolchains[name]
		assert.Equal(t, spec.version, fact.Version, "%s fact version", name)
		assert.Equal(t, filepath.Join(tmp, "sdk", name+"-"+spec.version, "fixture", spec.fact), fact.Path, "%s fact path", name)
		assert.FileExists(t, fact.Path, "%s fact path does not exist", name)
	}
}
