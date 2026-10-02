//go:build functional

package ci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/release"
)

// fleetPlayRig runs the plays the way docs/FLEET.md does, against the check
// fixture's inventory printed by the built nova-config: the machine running
// the test, connection local, no store, everything written under its own
// temp dir.
type fleetPlayRig struct {
	root, dir, bin, inventory, playbook, home string
}

func newFleetPlayRig(t *testing.T, fixture string) *fleetPlayRig {
	t.Helper()
	playbook, err := exec.LookPath("ansible-playbook")
	if err != nil {
		t.Skip("ansible-playbook is not installed on this machine")
	}
	r := &fleetPlayRig{root: repoRoot(t), dir: t.TempDir(), playbook: playbook}
	r.bin = filepath.Join(r.dir, "bin")
	r.home = filepath.Join(r.dir, "home")
	r.build(t, r.bin+string(filepath.Separator), "", "./cmd/nova-config", "./cmd/nova-redis")
	r.inventory = filepath.Join(r.dir, "nova-inventory")
	require.NoError(t, os.WriteFile(r.inventory, []byte("#!/bin/sh\nexec nova-config inventory --fixture "+filepath.Join(r.root, "fleet", "testdata", fixture)+" \"$@\"\n"), 0o755))
	return r
}

// build compiles packages of the checkout to out, stamped with version when
// it is not empty.
func (r *fleetPlayRig) build(t *testing.T, out, version string, pkgs ...string) {
	t.Helper()
	args := []string{"build", "-buildvcs=false", "-o", out}
	if version != "" {
		args = append(args, "-ldflags", release.Ldflags(version))
	}
	build := exec.Command("go", append(args, pkgs...)...)
	build.Dir = r.root
	build.Env = goenv.Clean(os.Environ())
	b, err := build.CombinedOutput()
	require.NoError(t, err, string(b))
}

func (r *fleetPlayRig) play(t *testing.T, name string, extra ...string) string {
	t.Helper()
	out, err := r.playResult(t, name, extra...)
	require.NoError(t, err, "%s %v:\n%s", name, extra, out)
	return out
}

func (r *fleetPlayRig) playResult(t *testing.T, name string, extra ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	args := append([]string{"-i", r.inventory, filepath.Join(r.root, "fleet", name)}, extra...)
	cmd := exec.CommandContext(ctx, r.playbook, args...)
	cmd.WaitDelay = 10 * time.Second
	cmd.Dir = r.root
	cmd.Stdin = nil
	cmd.Env = append(os.Environ(),
		"PATH="+r.bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"NOVA_MACHINE=localhost", "ANSIBLE_INVENTORY_UNPARSED_FAILED=true", "ANSIBLE_NOCOLOR=1",
		"ANSIBLE_HOME="+filepath.Join(r.dir, "ansible"), "ANSIBLE_LOCAL_TEMP="+filepath.Join(r.dir, "ansible", "tmp"))
	b, err := cmd.CombinedOutput()
	return string(b), err
}

// TestFleetPlaysPassSyntaxAndCheckOnTheFixture runs the three plays with
// --syntax-check, then --check --diff, which renders every template and
// changes nothing outside the test's own home. It asserts what the
// renderings say: the build and install a run would make, the directory of
// the build fact (the fixture's home has none), the four ACL users, and each
// loop unit (a systemd unit, and a launchd plist with the facts saying
// darwin) with the record's command behind nova-secrets, its keys as
// --require, systemd's % doubled and its log under the home.
func TestFleetPlaysPassSyntaxAndCheckOnTheFixture(t *testing.T) {
	t.Parallel()
	r := newFleetPlayRig(t, "check-fixture.yml")
	root, dir, home := r.root, r.dir, r.home
	play := func(name string, extra ...string) string { t.Helper(); return r.play(t, name, extra...) }
	check := []string{"--check", "--diff", "-e", "nova_home=" + home, "-e", "nova_version=v0.0.0-check", "-e", "nova_source=" + root, "-e", "nova_dogfood_receipts=" + filepath.Join(dir, "dogfood"), "-e", "nova_release_out=" + filepath.Join(dir, "release"), "-e", "nova_sops=/usr/bin/sops-of-the-fixture"}

	for _, p := range fleetPlays {
		assert.Contains(t, play(p, "--syntax-check"), "playbook: ")
	}
	tools := play("tools.yml", check...)
	assert.Contains(t, tools, "WOULD-BUILD version=v0.0.0-check out="+filepath.Join(dir, "release")+" built=none missing=")
	assert.Contains(t, tools, "TOOLS host=localhost platform=")
	assert.Contains(t, tools, "WOULD-INSTALL")
	assert.Contains(t, tools, "+v0.0.0-check", "the build fact's diff")
	assert.Contains(t, tools, `"path": `+strconv.Quote(filepath.Join(home, ".config", "nova"))+`,`, "the build fact's directory is created before the fact is written")

	redis := play("redis.yml", check...)
	assert.Contains(t, redis, "ACL RENDER OK users=4 ")
	for _, u := range []string{"coordinator", "bench", "ns-table", "ns-friend"} {
		assert.Contains(t, redis, "ACL SETUSER "+u+" on ")
	}

	// Three units already in the place: one this play wrote whose record is
	// gone (retired), one of another tool's (left, with a NOTE), and one of
	// another tool's that a record now names (taken over, not retired).
	units := filepath.Join(home, ".config", "systemd", "user")
	require.NoError(t, os.MkdirAll(units, 0o755))
	for name, text := range map[string]string{
		"nova-loop-old.service":          "# written by fleet/loops.yml from the loop record old\n[Service]\n",
		"nova-loop-mirror.service":       "[Service]\nExecStart=/bin/true\n",
		"nova-loop-member-local.service": "[Service]\nExecStart=/bin/true\n",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(units, name), []byte(text), 0o644))
	}
	loops := play("loops.yml", append(check, "-e", "ansible_system=Linux")...)
	for _, w := range []string{
		`ExecStart="` + home + `/.local/bin/nova-secrets" "exec" "--store" "` + home + `/nova-bench/secrets" "--as" "seat-local"`,
		`Environment="NOVA_SPRINT_REDIS=localhost:6380"`,
		`"--only" "API_KEY,NOVA_REDIS_BENCH_PASSWORD" "--require=API_KEY" "--require=NOVA_REDIS_BENCH_PASSWORD" "--" "/usr/bin/env" "NOVA_SPRINT_REDIS_USER=bench" "NOVA_SPRINT_REDIS_PASSWORD_ENV=NOVA_REDIS_BENCH_PASSWORD" "nova-swarm" "member"`,
		`ExecStart="` + home + `/bin/tick" "--once" "50%%"`,
		"Type=oneshot",
		"OnUnitActiveSec=30",
		"StandardOutput=append:" + home + "/nova-bench/loops/member-local.log",
		"WOULD-RETIRE old on localhost (" + filepath.Join(units, "nova-loop-old.service") + ")",
		"NOTE localhost " + filepath.Join(units, "nova-loop-mirror.service") + ": not written by this play",
		"retired=1 (check: nothing changed)",
	} {
		assert.Contains(t, loops, w)
	}
	assert.NotContains(t, loops, "WOULD-RETIRE member-local")
	// --check says which units a run restarts and why: a member's restart drains it
	// (nova-tools#5096 item 25); a disabled loop is stopped, not restarted
	assert.Contains(t, loops, "WOULD-RESTART member-local on localhost: its unit file changed; a member: the restart drains it (SIGTERM: it takes no new card, lets its running cards finish and reports them), waiting up to 7260 s, then the new unit starts")
	assert.NotContains(t, loops, "WOULD-RESTART tick-local")
	// the member's unit stops it by draining it; the periodic loop's is as it was
	assert.Equal(t, 1, strings.Count(loops, "+KillMode=mixed"), "the member's unit alone")
	assert.Equal(t, 1, strings.Count(loops, "+TimeoutStopSec=7260"))
	assert.NotContains(t, loops, `\u0001`)
	plist := play("loops.yml", append(check, "-e", "ansible_system=Darwin", "-e", "nova_launchd_domain=gui")...)
	for _, w := range []string{
		"<string>com.nova.loop.member-local</string>",
		"<string>" + home + "/.local/bin/nova-secrets</string>",
		"<string>--require=API_KEY</string>",
		"<key>StartInterval</key>",
		"<integer>30</integer>",
		"<key>KeepAlive</key>",
		"<key>NOVA_SPRINT_REDIS</key>",
		"<string>localhost:6380</string>",
		"<string>50%</string>",
		"<key>Disabled</key>",
	} {
		assert.Contains(t, plist, w)
	}
	assert.Equal(t, 1, strings.Count(plist, "+<key>ExitTimeOut</key>"), "the member's plist alone")
	assert.Contains(t, plist, "+<integer>7260</integer>")
	assert.NotContains(t, loops+plist, "NOVA_SPRINT_REDIS=old-store:6379")
	_, err := os.Stat(filepath.Join(home, ".config", "nova"))
	assert.True(t, os.IsNotExist(err), "--check wrote the build fact")
	_, err = os.Stat(filepath.Join(units, "nova-loop-old.service"))
	assert.NoError(t, err, "--check removed a unit")
	assert.False(t, strings.Contains(loops+plist, "FAILED!"), "a task failed")
}

// TestToolsPlayAppliesAndReappliesOnTheFixture runs tools.yml for real on the
// machine running the test, twice, against a release staged as a build
// would leave it (one stamped tool and its SHA256SUMS, so the build step
// finds it built) with a retired tool in the bin directory: the first run
// installs, removes the retired tool and writes the build fact into a home
// that has no .config; the second changes nothing and says UP-TO-DATE.
func TestToolsPlayAppliesAndReappliesOnTheFixture(t *testing.T) {
	t.Parallel()
	r := newFleetPlayRig(t, "check-fixture.yml")
	const version = "v0.0.0-apply"
	platform := runtime.GOOS + "-" + runtime.GOARCH
	out := filepath.Join(r.dir, "release")
	stage := filepath.Join(out, version, platform)
	r.build(t, filepath.Join(stage, "nova-update"), version, "./cmd/nova-update")
	tool, err := os.ReadFile(filepath.Join(stage, "nova-update"))
	require.NoError(t, err)
	sum := sha256.Sum256(tool)
	require.NoError(t, os.WriteFile(filepath.Join(stage, "SHA256SUMS"), []byte(hex.EncodeToString(sum[:])+"  nova-update\n"), 0o644))
	binDir := filepath.Join(r.home, ".local", "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "nova-pulse"), []byte("retired\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "nova-loop"), []byte("not ours to remove\n"), 0o755))
	_, err = os.Stat(filepath.Join(r.home, ".config"))
	require.True(t, os.IsNotExist(err), "the fixture's home starts with no .config")

	vars := []string{"-e", "nova_home=" + r.home, "-e", "nova_version=" + version, "-e", "nova_source=" + r.root, "-e", "nova_dogfood_receipts=" + filepath.Join(r.dir, "dogfood"), "-e", "nova_release_out=" + out}
	first := r.play(t, "tools.yml", vars...)
	assert.Contains(t, first, "built="+platform+" missing=none")
	assert.Contains(t, first, "was=none removed=1 INSTALLED RELEASE INSTALLED version="+version+" tools=1 ")
	fact, err := os.ReadFile(filepath.Join(r.home, ".config", "nova", "build"))
	require.NoError(t, err)
	assert.Equal(t, version+"\n", string(fact))
	info, err := os.Stat(filepath.Join(r.home, ".config", "nova"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	_, err = os.Stat(filepath.Join(binDir, "nova-pulse"))
	assert.True(t, os.IsNotExist(err), "the retired tool stayed")
	_, err = os.Stat(filepath.Join(binDir, "nova-loop"))
	assert.NoError(t, err, "a binary the list does not name was removed")

	second := r.play(t, "tools.yml", vars...)
	assert.Contains(t, second, "was="+version+" removed=0 UP-TO-DATE RELEASE INSTALLED version="+version+" tools=0 skipped=1")
	assert.Regexp(t, `localhost\s+: ok=\d+\s+changed=0 `, second, "a re-run changes nothing")
}

// TestDeployerPlaysCheckOnTheFixture runs the store_deployer plays of
// tools.yml and redis.yml with --check on a machine that is the store and the
// coordinator, with a nova-secrets that prints its arguments: the plays'
// variables resolve on that host (its home, read-only here, from the facts
// the plays gather: no nova_home is handed in), and the commands they would
// run name the seat, the secrets by name and the store.
func TestDeployerPlaysCheckOnTheFixture(t *testing.T) {
	t.Parallel()
	r := newFleetPlayRig(t, "deployer-fixture.yml")
	fake := filepath.Join(r.dir, "fake-bin")
	require.NoError(t, os.MkdirAll(fake, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(fake, "nova-secrets"), []byte("#!/bin/sh\necho \"FAKE-SECRETS $*\"\n"), 0o755))
	home := os.Getenv("HOME")
	vars := []string{"--check", "-e", "nova_bin_dir=" + fake, "-e", "nova_sops=/usr/bin/sops-of-the-fixture",
		"-e", "nova_version=v0.0.0-check", "-e", "nova_source=" + r.root, "-e", "nova_dogfood_receipts=" + filepath.Join(r.dir, "dogfood"), "-e", "nova_release_out=" + filepath.Join(r.dir, "release")}
	seat := "FAKE-SECRETS exec --store " + filepath.Join(home, "nova-bench", "secrets") + " --as seat-local --key " + filepath.Join(home, ".config", "nova-secrets", "seat-local.key") + " --sops /usr/bin/sops-of-the-fixture"

	redis := r.play(t, "redis.yml", vars...)
	assert.Contains(t, redis, seat+" --only NOVA_REDIS_ADMIN_PASSWORD,NOVA_REDIS_COORDINATOR_PASSWORD,NOVA_REDIS_BENCH_PASSWORD --require=NOVA_REDIS_ADMIN_PASSWORD -- "+fake+"/nova-redis acl check --addr localhost:6380 --user admin --password-env NOVA_REDIS_ADMIN_PASSWORD")
	assert.NotContains(t, redis, "/nova-redis acl apply", "--check applied")

	tools := r.play(t, "tools.yml", vars...)
	assert.Contains(t, tools, "WOULD-MIGRATE postgres://nova_config@localhost:5432/nova")
	assert.Contains(t, tools, seat+" --only NOVA_REDIS_COORDINATOR_PASSWORD --require=NOVA_REDIS_COORDINATOR_PASSWORD -- "+fake+"/nova-redis fn check --addr localhost:6380 --user coordinator --password-env NOVA_REDIS_COORDINATOR_PASSWORD")
}

func TestToolsPlayRefusesAnEmptyFleetDSN(t *testing.T) {
	t.Parallel()
	r := newFleetPlayRig(t, "deployer-fixture.yml")
	out, err := r.playResult(t, "tools.yml", "--check", "--limit", "store_deployer", "-e", "nova_pg_dsn=",
		"-e", "nova_home="+r.home, "-e", "nova_version=v0.0.0-check",
		"-e", "nova_source="+r.root, "-e", "nova_dogfood_receipts="+filepath.Join(r.dir, "dogfood"), "-e", "nova_release_out="+filepath.Join(r.dir, "release"))
	require.Error(t, err)
	assert.Contains(t, out, "the applied fleet row needs redis_port and pg_dsn")
	assert.Contains(t, out, "nova-config migrate")
	assert.Contains(t, out, "nova-config fleet set --redis_port <port> --pg_dsn")
	assert.Contains(t, out, "nova-config apply --kind fleet")
}

// TestToolsPlayNamesTheDogfoodReceipts: the build's gate reads the receipts
// the operator names, never a directory the build guesses, so a run that
// names none is refused before anything is built, and a waiver that replaces
// the gate's flags whole needs none.
func TestToolsPlayNamesTheDogfoodReceipts(t *testing.T) {
	t.Parallel()
	r := newFleetPlayRig(t, "check-fixture.yml")
	vars := []string{"--check", "--limit", "localhost", "-e", "nova_home=" + r.home, "-e", "nova_version=v0.0.0-check",
		"-e", "nova_source=" + r.root, "-e", "nova_release_out=" + filepath.Join(r.dir, "release"), "-e", "nova_sops=/usr/bin/sops-of-the-fixture"}
	out, err := r.playResult(t, "tools.yml", vars...)
	require.Error(t, err)
	assert.Contains(t, out, "-e nova_dogfood_receipts=<the dogfood receipts directory>")
	assert.NotContains(t, out, "WOULD-BUILD")
	out = r.play(t, "tools.yml", append(vars, "-e", `{"nova_release_gate_args": ["--no-dogfood-gate", "--reason", "the test"]}`)...)
	assert.Contains(t, out, "WOULD-BUILD version=v0.0.0-check")
}

// tlaRig is the tla play on the machine running the test: the tla fixture
// (localhost a record machine), a jar directory, a java and the facts saying
// Linux, all under the test's own temp dir.
type tlaRig struct {
	*fleetPlayRig
	jarDir, jar, java string
}

func newTLARig(t *testing.T) *tlaRig {
	t.Helper()
	r := &tlaRig{fleetPlayRig: newFleetPlayRig(t, "tla-fixture.yml")}
	r.jarDir = filepath.Join(r.dir, "opt-tla")
	require.NoError(t, os.MkdirAll(r.jarDir, 0o755))
	r.jar = filepath.Join(r.jarDir, "tla2tools.jar")
	r.java = filepath.Join(r.dir, "java")
	require.NoError(t, os.WriteFile(r.java, []byte("#!/bin/sh\necho 'openjdk version \"21.0.12.1\" 2026-08-18 LTS' >&2\n"), 0o755))
	return r
}

// vars are the play's -e for this rig, the sum file the repository's unless
// one is given.
func (r *tlaRig) vars(sum string, extra ...string) []string {
	java, err := json.Marshal(map[string][]string{"nova_tla_java_candidates": {filepath.Join(r.dir, "no-java"), r.java}})
	if err != nil {
		panic(err) // a map of strings always marshals
	}
	v := []string{"--tags", "tla", "-e", "ansible_system=Linux", "-e", "nova_home=" + r.home, "-e", "nova_tla_dir=" + r.jarDir, "-e", string(java)}
	if sum != "" {
		v = append(v, "-e", "nova_tla_sha256_file="+sum)
	}
	return append(v, extra...)
}

// repoSum is the SHA-256 the repository pins the TLC jar to.
func repoSum(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "tla", "tla2tools.sha256"))
	require.NoError(t, err)
	f := strings.Fields(string(b))
	require.NotEmpty(t, f)
	require.Regexp(t, `^[0-9a-f]{64}$`, f[0])
	return f[0]
}

// TestTLAPlayHoldsTheJarToTheRepositorysSum runs the tools play's tla play on
// a record machine: its --check rendering names the repository's sum
// (tla/tla2tools.sha256); a missing jar and a jar whose SHA-256 differs are
// refused, naming the path, both sums and how to place the jar; the pinned
// jar (here a jar the test pins with a sum file of its own) passes, owned by
// the login, with the java found echoed.
func TestTLAPlayHoldsTheJarToTheRepositorysSum(t *testing.T) {
	t.Parallel()
	r := newTLARig(t)
	want := repoSum(t, r.root)

	check, err := r.playResult(t, "tools.yml", r.vars("", "--check")...)
	require.Error(t, err, "--check with no jar passed:\n%s", check)
	assert.Contains(t, check, "TLA REFUSED host=localhost jar="+r.jar+" want="+want+" got=none")
	assert.Contains(t, check, "scp a tla2tools.jar whose sha256sum is "+want+" to localhost:"+r.jar)

	require.NoError(t, os.WriteFile(r.jar, []byte("not the pinned jar\n"), 0o644))
	got := sha256.Sum256([]byte("not the pinned jar\n"))
	out, err := r.playResult(t, "tools.yml", r.vars("")...)
	require.Error(t, err, "a jar whose sum differs passed:\n%s", out)
	assert.Contains(t, out, "TLA REFUSED host=localhost jar="+r.jar+" want="+want+" got="+hex.EncodeToString(got[:]))

	sum := filepath.Join(r.dir, "tla2tools.sha256")
	require.NoError(t, os.WriteFile(sum, []byte(hex.EncodeToString(got[:])+"  tla2tools.jar\n"), 0o644))
	out = r.play(t, "tools.yml", r.vars(sum)...)
	assert.Contains(t, out, "TLA OK host=localhost jar="+r.jar+" sha256="+hex.EncodeToString(got[:])+" java="+r.java+" version=21.0.12.1")
	assert.Regexp(t, `localhost\s+: ok=\d+\s+changed=0 `, out, "a converged record machine changes nothing")
}

// TestLoopsPlayRecordFilter: nova_loop_only names the records a run renders and
// restarts (nova-tools#5096 item 24, the readers-only pass of 2026-10-02); every
// other unit on the machine is left as it is, and none is retired, not even a
// marked unit no record names. A name no record on the run's machines carries is
// refused before anything is written.
func TestLoopsPlayRecordFilter(t *testing.T) {
	t.Parallel()
	r := newFleetPlayRig(t, "check-fixture.yml")
	units := filepath.Join(r.home, ".config", "systemd", "user")
	require.NoError(t, os.MkdirAll(units, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(units, "nova-loop-old.service"), []byte("# written by fleet/loops.yml from the loop record old\n[Service]\n"), 0o644))
	check := []string{"--check", "--diff", "-e", "ansible_system=Linux", "-e", "nova_home=" + r.home, "-e", "nova_sops=/usr/bin/sops-of-the-fixture"}

	plain := r.play(t, "loops.yml", check...)
	assert.Contains(t, plain, "WOULD-RETIRE old on localhost")
	assert.Contains(t, plain, "nova-loop-tick-local.timer")

	only := r.play(t, "loops.yml", append(check, "-e", "nova_loop_only=member-local")...)
	assert.Contains(t, only, "nova-loop-member-local.service")
	assert.NotContains(t, only, "nova-loop-tick-local", "a record not named is not rendered")
	assert.NotContains(t, only, "WOULD-RETIRE", "a filtered run retires nothing")
	assert.Contains(t, only, "WOULD-RESTART member-local on localhost")
	assert.Contains(t, only, "LOOPS host=localhost place="+units+" records=1 enabled=1 written=1 retired=0 only=member-local (check: nothing changed)")
	assert.False(t, strings.Contains(only, "FAILED!"), "a task failed")

	list := r.play(t, "loops.yml", append(check, "-e", `{"nova_loop_only": ["member-local", "tick-local"]}`)...)
	assert.Contains(t, list, "records=2 enabled=1 written=")
	assert.Contains(t, list, "only=member-local,tick-local")

	out, err := r.playResult(t, "loops.yml", append(check, "-e", "nova_loop_only=member-locl")...)
	require.Error(t, err)
	assert.Contains(t, out, "nova_loop_only names member-locl, which no loop record of this run's machines is")
	assert.NotContains(t, out, "LOOPS host=")
}

// TestToolsPlaySendsOnlyTheFilesTheInstalledBuildLacks runs tools.yml for
// real twice, the second time at a version an --incremental build would
// leave: nova-update the first version's bytes (reused), nova-extra rebuilt.
// The new version's directory is seeded on the machine from the installed
// build's, only nova-extra and the checksum files are sent, and the install
// leaves the identical nova-update in place (the same file), so the loop
// running it is not restarted.
func TestToolsPlaySendsOnlyTheFilesTheInstalledBuildLacks(t *testing.T) {
	t.Parallel()
	r := newFleetPlayRig(t, "check-fixture.yml")
	platform := runtime.GOOS + "-" + runtime.GOARCH
	out := filepath.Join(r.dir, "release")
	stage := func(version, extra string, update []byte) {
		dir := filepath.Join(out, version, platform)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "nova-update"), update, 0o755))
		script := []byte("#!/bin/sh\necho nova-extra " + extra + "\n")
		require.NoError(t, os.WriteFile(filepath.Join(dir, "nova-extra"), script, 0o755))
		var sums strings.Builder
		for _, f := range []struct {
			name string
			body []byte
		}{{"nova-extra", script}, {"nova-update", update}} {
			s := sha256.Sum256(f.body)
			sums.WriteString(hex.EncodeToString(s[:]) + "  " + f.name + "\n")
		}
		require.NoError(t, os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums.String()), 0o644))
		d := sha256.Sum256([]byte(sums.String()))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "SUMS.digest"), []byte(hex.EncodeToString(d[:])+"\n"), 0o644))
	}
	built := filepath.Join(r.dir, "built")
	r.build(t, filepath.Join(built, "nova-update"), "v0.0.0-one", "./cmd/nova-update")
	update, err := os.ReadFile(filepath.Join(built, "nova-update"))
	require.NoError(t, err)
	stage("v0.0.0-one", "v0.0.0-one", update)
	stage("v0.0.0-two", "v0.0.0-two", update)
	vars := func(version string) []string {
		return []string{"-e", "nova_home=" + r.home, "-e", "nova_version=" + version, "-e", "nova_source=" + r.root,
			"-e", "nova_dogfood_receipts=" + filepath.Join(r.dir, "dogfood"), "-e", "nova_release_out=" + out}
	}

	first := r.play(t, "tools.yml", vars("v0.0.0-one")...)
	assert.Contains(t, first, "RELEASE INSTALLED version=v0.0.0-one tools=2 skipped=0 ")
	installed := filepath.Join(r.home, ".local", "bin", "nova-update")
	before, err := os.Stat(installed)
	require.NoError(t, err)

	second := r.play(t, "tools.yml", vars("v0.0.0-two")...)
	assert.Regexp(t, `TASK \[the stage seeded from the installed build's directory, on the machine\][^\n]*\n(?:[^\n]*\n)?changed: \[localhost\]`, second)
	assert.Contains(t, second, "(item=nova-extra)")
	assert.Contains(t, second, "(item=SHA256SUMS)")
	assert.NotContains(t, second, "(item=nova-update)", "a file the machine already held was sent")
	assert.Contains(t, second, "was=v0.0.0-one removed=0 INSTALLED RELEASE INSTALLED version=v0.0.0-two tools=1 skipped=1 ")
	after, err := os.Stat(installed)
	require.NoError(t, err)
	assert.True(t, os.SameFile(before, after), "the identical nova-update was replaced")
	extra, err := exec.Command(filepath.Join(r.home, ".local", "bin", "nova-extra")).Output()
	require.NoError(t, err)
	assert.Equal(t, "nova-extra v0.0.0-two\n", string(extra))

	check := r.play(t, "tools.yml", append(vars("v0.0.0-two"), "--check")...)
	assert.Contains(t, check, "was=v0.0.0-two removed=0 UP-TO-DATE", "a reused nova-update answers the earlier version; the build fact says the install is done")
}

// TestToolsPlaySendsEveryStagedFileWhoseBytesDiffer is BenchStage.tla's
// ReusedByteIdentical on the real play: the seed copies the installed build's
// directory on the machine unverified, so the files sent are those whose
// bytes in the stage differ from the release's SHA256SUMS, measured after the
// seed (one listing with sha256), never those whose SHA256SUMS line differs.
// A seeded file corrupted on the machine whose line matches the release's,
// the binary the play runs or any other, is sent again in the same run, the
// install takes the rebuilt tool and skips the rest, and an intact reused
// file is not sent.
func TestToolsPlaySendsEveryStagedFileWhoseBytesDiffer(t *testing.T) {
	t.Parallel()
	built := ""
	var update []byte
	for _, corrupt := range []string{"nova-update", "nova-same"} {
		t.Run(corrupt, func(t *testing.T) {
			r := newFleetPlayRig(t, "check-fixture.yml")
			platform := runtime.GOOS + "-" + runtime.GOARCH
			out := filepath.Join(r.dir, "release")
			if built == "" {
				built = filepath.Join(r.dir, "built")
				r.build(t, filepath.Join(built, "nova-update"), "v0.0.0-one", "./cmd/nova-update")
				var err error
				update, err = os.ReadFile(filepath.Join(built, "nova-update"))
				require.NoError(t, err)
			}
			for _, version := range []string{"v0.0.0-one", "v0.0.0-two"} {
				dir := filepath.Join(out, version, platform)
				require.NoError(t, os.MkdirAll(dir, 0o755))
				var sums strings.Builder
				for _, f := range []struct {
					name string
					body []byte
				}{
					{"nova-extra", []byte("#!/bin/sh\necho nova-extra " + version + "\n")},
					{"nova-same", []byte("#!/bin/sh\necho nova-same\n")},
					{"nova-update", update},
				} {
					require.NoError(t, os.WriteFile(filepath.Join(dir, f.name), f.body, 0o755))
					s := sha256.Sum256(f.body)
					sums.WriteString(hex.EncodeToString(s[:]) + "  " + f.name + "\n")
				}
				require.NoError(t, os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums.String()), 0o644))
			}
			vars := func(version string) []string {
				return []string{"-e", "nova_home=" + r.home, "-e", "nova_version=" + version, "-e", "nova_source=" + r.root,
					"-e", "nova_dogfood_receipts=" + filepath.Join(r.dir, "dogfood"), "-e", "nova_release_out=" + out}
			}
			r.play(t, "tools.yml", vars("v0.0.0-one")...)
			seed := filepath.Join(r.home, "nova-bench", "release", "v0.0.0-one", platform, corrupt)
			require.NoError(t, os.WriteFile(seed, []byte("corrupt on the machine\n"), 0o755))
			extraNow := func() string {
				b, err := exec.Command(filepath.Join(r.home, ".local", "bin", "nova-extra")).Output()
				require.NoError(t, err)
				return string(b)
			}

			second := r.play(t, "tools.yml", vars("v0.0.0-two")...)
			assert.Contains(t, second, "changed: [localhost] => (item="+corrupt+")", "the corrupt seeded file was not sent again")
			assert.Contains(t, second, "changed: [localhost] => (item=nova-extra)")
			for _, intact := range []string{"nova-update", "nova-same"} {
				if intact != corrupt {
					assert.NotContains(t, second, "(item="+intact+")", "an intact reused file was sent")
				}
			}
			assert.Contains(t, second, "INSTALLED RELEASE INSTALLED version=v0.0.0-two tools=1 skipped=2 ")
			assert.Equal(t, "nova-extra v0.0.0-two\n", extraNow())
			staged, err := os.ReadFile(filepath.Join(r.home, "nova-bench", "release", "v0.0.0-two", platform, corrupt))
			require.NoError(t, err)
			want, err := os.ReadFile(filepath.Join(out, "v0.0.0-two", platform, corrupt))
			require.NoError(t, err)
			assert.Equal(t, want, staged, "the stage holds bytes the release does not")
		})
	}
}
