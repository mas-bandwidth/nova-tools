//go:build functional

package ci

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

// TestFleetPlaysPassSyntaxAndCheckOnTheFixture runs the three plays the way
// docs/FLEET.md does, against the check fixture's inventory printed by the
// built nova-config (the machine running the test, connection local, no
// store): --syntax-check, then --check --diff, which renders every template
// and changes nothing outside the test's own home. It asserts what the
// renderings say: the build and install a run would make, the four ACL
// users, and each loop unit (a systemd unit, and a launchd plist with the
// facts saying darwin) with the record's command behind nova-secrets, its
// keys as --require, systemd's % doubled and its log under the home.
func TestFleetPlaysPassSyntaxAndCheckOnTheFixture(t *testing.T) {
	t.Parallel()
	playbook, err := exec.LookPath("ansible-playbook")
	if err != nil {
		t.Skip("ansible-playbook is not installed on this machine")
	}
	root := repoRoot(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	build := exec.Command("go", "build", "-o", bin+string(filepath.Separator), "./cmd/nova-config", "./cmd/nova-redis")
	build.Dir = root
	build.Env = goenv.Clean(os.Environ())
	out, err := build.CombinedOutput()
	require.NoError(t, err, string(out))
	inventory := filepath.Join(dir, "nova-inventory")
	require.NoError(t, os.WriteFile(inventory, []byte("#!/bin/sh\nexec nova-config inventory --fixture "+filepath.Join(root, "fleet", "testdata", "check-fixture.yml")+" \"$@\"\n"), 0o755))
	home := filepath.Join(dir, "home")

	play := func(name string, extra ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		args := append([]string{"-i", inventory, filepath.Join(root, "fleet", name)}, extra...)
		cmd := exec.CommandContext(ctx, playbook, args...)
		cmd.WaitDelay = 10 * time.Second
		cmd.Dir = root
		cmd.Stdin = nil
		cmd.Env = append(os.Environ(),
			"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
			"NOVA_MACHINE=localhost", "ANSIBLE_INVENTORY_UNPARSED_FAILED=true", "ANSIBLE_NOCOLOR=1",
			"ANSIBLE_HOME="+filepath.Join(dir, "ansible"), "ANSIBLE_LOCAL_TEMP="+filepath.Join(dir, "ansible", "tmp"))
		b, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s %v:\n%s", name, extra, b)
		return string(b)
	}
	check := []string{"--check", "--diff", "-e", "nova_home=" + home, "-e", "nova_version=v0.0.0-check", "-e", "nova_source=" + root, "-e", "nova_release_out=" + filepath.Join(dir, "release"), "-e", "nova_sops=/usr/bin/sops-of-the-fixture"}

	for _, p := range fleetPlays {
		assert.Contains(t, play(p, "--syntax-check"), "playbook: ")
	}
	tools := play("tools.yml", check...)
	assert.Contains(t, tools, "WOULD-BUILD version=v0.0.0-check platforms=")
	assert.Contains(t, tools, "TOOLS host=localhost platform=")
	assert.Contains(t, tools, "WOULD-INSTALL")
	assert.Contains(t, tools, "+v0.0.0-check", "the build fact's diff")

	redis := play("redis.yml", check...)
	assert.Contains(t, redis, "ACL RENDER OK users=4 ")
	for _, u := range []string{"coordinator", "bench", "ns-table", "ns-friend"} {
		assert.Contains(t, redis, "ACL SETUSER "+u+" on ")
	}

	loops := play("loops.yml", append(check, "-e", "ansible_system=Linux")...)
	for _, w := range []string{
		`ExecStart="` + home + `/.local/bin/nova-secrets" "exec" "--store" "` + home + `/nova-bench/secrets" "--as" "seat-local"`,
		`"--only" "API_KEY" "--require=API_KEY" "--" "` + home + `/.local/bin/nova-swarm" "member"`,
		`ExecStart="` + home + `/bin/tick" "--once" "50%%"`,
		"RestartSec=30",
		"StandardOutput=append:" + home + "/nova-bench/loops/member-local.log",
		"LOOPS host=localhost records=2 enabled=1 written=2 retired=0 (check: nothing changed)",
	} {
		assert.Contains(t, loops, w)
	}
	plist := play("loops.yml", append(check, "-e", "ansible_system=Darwin")...)
	for _, w := range []string{
		"<string>com.nova.loop.member-local</string>",
		"<string>" + home + "/.local/bin/nova-secrets</string>",
		"<string>--require=API_KEY</string>",
		"<key>StartInterval</key>",
		"<integer>30</integer>",
		"<key>KeepAlive</key>",
		"<string>50%</string>",
	} {
		assert.Contains(t, plist, w)
	}
	_, err = os.Stat(filepath.Join(home, ".config"))
	assert.True(t, os.IsNotExist(err), "--check wrote under the home")
	assert.False(t, strings.Contains(loops+plist, "FAILED!"), "a task failed")
}
