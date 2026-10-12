package fleet

import (
	"context"
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
