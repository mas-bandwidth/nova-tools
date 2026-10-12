package fleet

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
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestToolchainsPlayRendersEveryPinnedToolchain pins the hand step that used
// to leave Linux benches without schema-card tools. The role's installation
// rules live in docs/FLEET.md, "Language toolchains".
func TestToolchainsPlayRendersEveryPinnedToolchain(t *testing.T) {
	t.Parallel()
	plays := readTools(t)
	var play map[string]any
	for _, candidate := range plays {
		if asText(candidate["hosts"]) == "all" && hasTag(candidate["tags"], "toolchains") {
			play = candidate
			break
		}
	}
	require.NotNil(t, play, "tools.yml has no hosts: all play tagged [toolchains]; see docs/FLEET.md, Language toolchains")
	require.True(t, hasRole(play["roles"], "toolchains"), "the toolchains-tagged fleet play does not run the toolchains role")
	assert.True(t, hasTag(play["tags"], "never"), "the opt-in toolchains play must not run in an untagged fleet rollout")

	defaultsBytes, err := os.ReadFile("roles/toolchains/defaults/main.yml")
	require.NoError(t, err)
	var defaults map[string]any
	require.NoError(t, yaml.Unmarshal(defaultsBytes, &defaults))
	toolchains, ok := defaults["nova_toolchains"].(map[string]any)
	require.True(t, ok, "toolchains defaults must define nova_toolchains")
	for _, name := range []string{"node", "dotnet", "dart", "elixir", "otp", "rust", "jdk", "sbcl", "gh"} {
		item, found := toolchains[name].(map[string]any)
		require.True(t, found, "toolchains defaults omit %s", name)
		require.NotEmpty(t, item["version"], "%s has no pinned version", name)
		platforms, found := item["platforms"].(map[string]any)
		require.True(t, found, "%s has no platform archive map", name)
		for _, platform := range []string{"linux-x86_64", "darwin-arm64"} {
			archive, found := platforms[platform].(map[string]any)
			require.True(t, found, "%s has no %s archive", name, platform)
			require.True(t, strings.HasPrefix(asText(archive["url"]), "https://"), "%s %s URL is not pinned to HTTPS", name, platform)
			require.Regexp(t, regexp.MustCompile(`^[0-9a-f]{64}$`), asText(archive["sha256"]), "%s %s needs a pinned SHA-256", name, platform)
		}
	}

	// Run only against local fixture hosts, with check mode forced. All
	// Ansible scratch and HOME state stay under this job's tmp directory.
	ansible, err := exec.LookPath("ansible-playbook")
	if err != nil {
		t.Skip("ansible-playbook is not installed on this machine; the shape assertions above still ran")
	}
	tmpRoot := filepath.Clean(filepath.Join("..", "..", "tmp"))
	require.NoError(t, os.MkdirAll(tmpRoot, 0o700))
	tmp, err := os.MkdirTemp(tmpRoot, "toolchains-play-")
	require.NoError(t, err)
	tmp, err = filepath.Abs(tmp)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })
	for _, dir := range []string{"home", "ansible", "local", "remote"} {
		require.NoError(t, os.MkdirAll(filepath.Join(tmp, dir), 0o700))
	}
	listCtx, listCancel := context.WithTimeout(context.Background(), 30*time.Second)
	listCmd := exec.CommandContext(listCtx, ansible, "tools.yml", "--list-tasks", "-i", "testdata/toolchains-inventory.ini", "--limit", "fixture-linux", "-e", "nova_adopt_canary=none")
	listCmd.Dir = "."
	listCmd.Env = append(os.Environ(),
		"HOME="+filepath.Join(tmp, "home"),
		"ANSIBLE_HOME="+filepath.Join(tmp, "ansible"),
		"ANSIBLE_LOCAL_TEMP="+filepath.Join(tmp, "local"),
		"ANSIBLE_REMOTE_TEMP="+filepath.Join(tmp, "remote"),
		"ANSIBLE_NOCOLOR=1",
		"ANSIBLE_PIPELINING=1",
	)
	listOutput, listErr := listCmd.CombinedOutput()
	listCancel()
	require.NoError(t, listErr, "ansible-playbook tools.yml --list-tasks failed:\n%s", listOutput)
	assert.NotContains(t, string(listOutput), "the host has a pinned language-toolchain archive set", "normal tools runs must skip the opt-in role")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ansible,
		"tools.yml", "--tags", "toolchains", "--check", "--diff",
		"-i", "testdata/toolchains-inventory.ini", "--limit", "toolchain_fixtures",
		"-e", "nova_sdk_home="+filepath.Join(tmp, "sdk"),
		"-e", "nova_adopt_canary=none",
	)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(),
		"HOME="+filepath.Join(tmp, "home"),
		"ANSIBLE_HOME="+filepath.Join(tmp, "ansible"),
		"ANSIBLE_LOCAL_TEMP="+filepath.Join(tmp, "local"),
		"ANSIBLE_REMOTE_TEMP="+filepath.Join(tmp, "remote"),
		"ANSIBLE_NOCOLOR=1",
		"ANSIBLE_PIPELINING=1",
	)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "ansible-playbook --tags toolchains --check failed:\n%s", output)
	assert.Contains(t, string(output), "PLAY RECAP")
	assert.Contains(t, string(output), "TOOLCHAINS CHECK", "the dry run must name its host and toolchain facts")
	assert.Contains(t, string(output), "TOOLCHAINS WOULD-INSTALL", "the dry run must name each pinned archive")
	for _, name := range []string{"node", "dotnet", "dart", "elixir", "otp", "rust", "jdk", "sbcl", "gh"} {
		assert.Contains(t, string(output), "tool="+name, "the dry run must name %s", name)
	}
	assert.NotContains(t, strings.ToLower(string(output)), "run this yourself")
	assert.NotContains(t, strings.ToLower(string(output)), "scp ")
}

// TestToolchainsPlayAppliesLocalArchivesAndReplacesDrift exercises the
// install path with tiny fixture archives, never a fleet host or live toolchain.
func TestToolchainsPlayAppliesLocalArchivesAndReplacesDrift(t *testing.T) {
	t.Parallel()
	// The four real plays share one budget below certification's 75-second
	// package bound; a fresh timeout per play does not bound their total.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ansible, err := exec.LookPath("ansible-playbook")
	if err != nil {
		t.Skip("ansible-playbook is not installed on this machine; the shape assertions above still ran")
	}
	tmpRoot := filepath.Clean(filepath.Join("..", "..", "tmp"))
	require.NoError(t, os.MkdirAll(tmpRoot, 0o700))
	tmp, err := os.MkdirTemp(tmpRoot, "toolchains-apply-")
	require.NoError(t, err)
	tmp, err = filepath.Abs(tmp)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })
	for _, dir := range []string{"home", "ansible", "local", "remote"} {
		require.NoError(t, os.MkdirAll(filepath.Join(tmp, dir), 0o700))
	}
	archivePath := filepath.Join(tmp, "fixtures.tar.gz")
	sha := writeToolchainFixtureArchive(t, archivePath)
	varsPath := filepath.Join(tmp, "toolchains.json")
	writeToolchainFixtureVars(t, varsPath, tmp, sha)

	first := runToolchainsFixturePlay(t, ctx, ansible, tmp, varsPath, false, false)
	assert.Contains(t, first, "PLAY RECAP")
	assert.NotContains(t, first, "failed=1", first)
	assertToolchainFixtureInstall(t, tmp)

	second := runToolchainsFixturePlay(t, ctx, ansible, tmp, varsPath, false, false)
	assert.Regexp(t, `fixture-linux\s+: ok=\d+\s+changed=0\s+unreachable=0\s+failed=0`, second)

	// A changed installed executable is detected even when its archive marker
	// still names the pinned SHA; replacement restores the verified fixture.
	node := filepath.Join(tmp, "sdk", "node-1.0.0", "fixture", "bin", "node")
	require.NoError(t, os.WriteFile(node, []byte("tampered"), 0o755))
	drift := runToolchainsFixturePlay(t, ctx, ansible, tmp, varsPath, false, false)
	assertToolchainFixtureInstall(t, tmp)
	assert.Contains(t, drift, "changed=")

	// A bad pinned digest refuses the archive before removing the current SDK.
	writeToolchainFixtureVars(t, varsPath, tmp, strings.Repeat("0", 64))
	_ = runToolchainsFixturePlay(t, ctx, ansible, tmp, varsPath, false, true)
	assert.FileExists(t, node, "checksum mismatch must preserve the existing install")
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

func writeToolchainFixtureArchive(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Create(path)
	require.NoError(t, err)
	sha := sha256.New()
	gz := gzip.NewWriter(io.MultiWriter(f, sha))
	tw := tar.NewWriter(gz)
	for _, spec := range toolchainFixtureSpecs {
		for _, name := range spec.paths {
			body := []byte(fmt.Sprintf("#!/bin/sh\nprintf 'fixture-%s\\n'\n", name))
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

func runToolchainsFixturePlay(t *testing.T, parent context.Context, ansible, tmp, varsPath string, check, wantError bool) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 40*time.Second)
	defer cancel()
	args := []string{"tools.yml", "--tags", "toolchains", "-i", "testdata/toolchains-inventory.ini", "--limit", "fixture-linux", "-e", "@" + varsPath}
	if check {
		args = append(args, "--check", "--diff")
	}
	cmd := exec.CommandContext(ctx, ansible, args...)
	cmd.WaitDelay = 5 * time.Second
	cmd.Dir = "."
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

func hasTag(raw any, want string) bool {
	switch value := raw.(type) {
	case string:
		return value == want
	case []any:
		for _, item := range value {
			if asText(item) == want {
				return true
			}
		}
	}
	return false
}

func hasRole(raw any, want string) bool {
	roles, ok := raw.([]any)
	if !ok {
		return false
	}
	for _, rawRole := range roles {
		role, ok := rawRole.(map[string]any)
		if ok && (asText(role["role"]) == want || asText(role["name"]) == want) {
			return true
		}
		if asText(rawRole) == want {
			return true
		}
	}
	return false
}
