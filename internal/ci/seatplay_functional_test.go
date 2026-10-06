//go:build functional

package ci

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

// seatLaunchctl is a launchctl for the seat play's test: it holds the
// agents of the test's home in a state directory (one <label>.pid each),
// starts a plist's first two ProgramArguments on bootstrap, kills what it
// started on bootout, logs every verb, and refuses a bootstrap of a label
// with a <label>.fail file. It never reaches launchd.
const seatLaunchctl = `#!/bin/sh
S=%s
case "$1" in
print) l=${2##*/}; [ -f "$S/$l.pid" ] && kill -0 "$(cat "$S/$l.pid")" 2>/dev/null || exit 113
  printf '\tstate = running\n\tpid = %%s\n' "$(cat "$S/$l.pid")"; exit 0;;
bootout) l=${2##*/}; echo "bootout $l" >> "$S/log"; [ -f "$S/$l.pid" ] || exit 3; kill "$(cat "$S/$l.pid")" 2>/dev/null; rm -f "$S/$l.pid"; exit 0;;
bootstrap) l=$(basename "$3" .plist); echo "bootstrap $l" >> "$S/log"; [ -f "$S/$l.fail" ] && exit 5; [ -f "$S/$l.pid" ] && exit 5
  bin=$(/usr/bin/plutil -extract ProgramArguments.0 raw -o - "$3"); a1=$(/usr/bin/plutil -extract ProgramArguments.1 raw -o - "$3")
  nohup "$bin" "$a1" >/dev/null 2>&1 & echo $! > "$S/$l.pid"; exit 0;;
*) echo "unexpected $*" >> "$S/log"; exit 64;;
esac
`

// TestSeatPlayRestartsWhatIsStaleAndRefusesAFailedRestart runs the seat play
// for real (tools.yml --tags seat) on a coordinator fixture, its home and its
// launchd the test's own: an agent whose process runs a binary the install
// replaced, one launchd does not hold, one whose plist names a copy, and a
// disabled one. The run boots out and bootstraps the three stale ones (the
// copy's plist pointed at the bin directory first), never kickstarts, and
// leaves every one fresh; a second run changes nothing; a run whose
// bootstrap fails bootstraps each again and refuses the step, naming it.
func TestSeatPlayRestartsWhatIsStaleAndRefusesAFailedRestart(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" {
		t.Skip("the fake launchctl reads plists with plutil, a darwin tool")
	}
	r := newFleetPlayRig(t, "seat-fixture.yml")
	home := filepath.Join(r.dir, "seat-home")
	bin := filepath.Join(home, ".local", "bin")
	agents := filepath.Join(home, "Library", "LaunchAgents")
	stage := filepath.Join(home, "nova-bench", "release", "v0.0.0-seat", runtime.GOOS+"-"+runtime.GOARCH)
	state := filepath.Join(r.dir, "launchd")
	copyDir := filepath.Join(home, "copy")
	for _, d := range []string{bin, agents, stage, state, copyDir} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	r.build(t, bin+string(filepath.Separator), "", "./cmd/nova-sprint")
	r.build(t, stage+string(filepath.Separator), "", "./cmd/nova-sprint")
	write := func(path, body string) {
		t.Helper()
		require.NoError(t, os.WriteFile(path, []byte(body), 0o755))
	}
	write(filepath.Join(bin, "nova-secrets"), "#!/bin/sh\nwhile [ \"$#\" -gt 0 ] && [ \"$1\" != \"--\" ]; do shift; done; shift; exec \"$@\"\n")
	write(filepath.Join(bin, "nova-redis"), "#!/bin/sh\necho \"OK nova_sprint sha=abc loaded=abc want=abc store=twin\"\n")
	launchctl := filepath.Join(r.dir, "launchctl")
	write(launchctl, fmt.Sprintf(seatLaunchctl, state))
	t.Cleanup(func() {
		pids, _ := filepath.Glob(filepath.Join(state, "*.pid")) // ignored: a pattern that is always valid
		for _, f := range pids {
			b, _ := os.ReadFile(f) // ignored: a pid file the fake wrote; a missing one has nothing to stop
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				_ = syscall.Kill(pid, syscall.SIGTERM) // ignored: a process this test started that has already ended
			}
		}
	})

	// the agents' binary: a sleeper built here, so a copy of it runs anywhere
	src := filepath.Join(r.dir, "sleeper")
	require.NoError(t, os.MkdirAll(src, 0o755))
	write(filepath.Join(src, "go.mod"), "module sleeper\n\ngo 1.22\n")
	write(filepath.Join(src, "main.go"), "package main\n\nimport (\n\t\"os\"\n\t\"strconv\"\n\t\"time\"\n)\n\nfunc main() {\n\tn, _ := strconv.Atoi(os.Args[len(os.Args)-1])\n\ttime.Sleep(time.Duration(n) * time.Second)\n}\n")
	sleeper := filepath.Join(r.dir, "sleeper-bin")
	build := exec.Command("go", "build", "-o", sleeper, ".")
	build.Dir = src
	build.Env = append(goenv.Clean(os.Environ()), "GOFLAGS=")
	out, err := build.CombinedOutput()
	require.NoError(t, err, string(out))
	install := func(dst string) {
		t.Helper()
		b, err := os.ReadFile(sleeper)
		require.NoError(t, err)
		write(dst+".new", string(b))
		require.NoError(t, os.Rename(dst+".new", dst)) // a fresh inode, as release install makes one
	}
	install(filepath.Join(bin, "nova-swarm"))
	install(filepath.Join(copyDir, "nova-swarm"))
	plist := func(label, binary, arg, extra string) string {
		p := filepath.Join(agents, label+".plist")
		write(p, fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>Label</key><string>%s</string><key>ProgramArguments</key><array><string>%s</string><string>%s</string></array>%s</dict></plist>`, label, binary, arg, extra))
		return p
	}
	t1 := plist("com.nova.loop.t1", filepath.Join(bin, "nova-swarm"), "1001", "")
	plist("com.nova.loop.t2", filepath.Join(bin, "nova-swarm"), "1002", "")
	t3 := plist("com.nova.loop.t3", filepath.Join(copyDir, "nova-swarm"), "1003", "")
	plist("com.nova.loop.t4", filepath.Join(bin, "nova-swarm"), "1004", "<key>Disabled</key><true/>")
	for _, p := range []string{t1, t3} {
		out, err := exec.Command(launchctl, "bootstrap", "gui/0", p).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	// the agents' processes run the binary they were started from before the install replaces it
	fresh := func(labels ...string) bool {
		out, err := exec.Command(filepath.Join(bin, "nova-sprint"), "live", "--json", "--bin-dir", bin, "--agents-dir", agents, "--launchctl", launchctl).Output()
		if err != nil {
			return false
		}
		var m struct {
			Agents []struct {
				Label string `json:"label"`
				Fresh bool   `json:"fresh"`
				PID   int    `json:"pid"`
			} `json:"agents"`
		}
		if json.Unmarshal(out, &m) != nil {
			return false
		}
		n := 0
		for _, a := range m.Agents {
			for _, l := range labels {
				if a.Label == l && a.Fresh && a.PID > 0 {
					n++
				}
			}
		}
		return n == len(labels)
	}
	wait := 30 * time.Second
	if v := os.Getenv("NOVA_TEST_WAIT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			wait = d
		}
	}
	deadline := time.After(wait)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for started := false; !started; {
		select {
		case <-deadline:
			t.Fatal("the agents' processes never ran their binaries")
		case <-tick.C:
			started = fresh("com.nova.loop.t1", "com.nova.loop.t3")
		}
	}
	install(filepath.Join(bin, "nova-swarm")) // the install: t1 now runs a replaced binary

	twin := filepath.Join(r.dir, "twin")
	seed := exec.Command(filepath.Join(bin, "nova-sprint"), "init", "--redis", "mem:"+twin, "--readers", "reader-a,reader-b", "--members", "m1", "--actor", "tester")
	out, err = seed.CombinedOutput()
	require.NoError(t, err, string(out))

	vars := []string{"--tags", "seat", "--limit", "localhost", "-e", "nova_home=" + home, "-e", "nova_version=v0.0.0-seat",
		"-e", "nova_redis_addr=mem:" + twin, "-e", "nova_launchctl=" + launchctl, "-e", "nova_sops=/usr/bin/true",
		"-e", "nova_member_stop_timeout=5", "-e", "nova_seat_beat_within=10"}
	log := func() string {
		b, _ := os.ReadFile(filepath.Join(state, "log")) // ignored: no log is no launchctl verb run
		require.NoError(t, os.WriteFile(filepath.Join(state, "log"), nil, 0o644))
		return string(b)
	}
	log()
	first := r.play(t, "tools.yml", vars...)
	assert.Contains(t, first, `"RESTART com.nova.loop.t1 on localhost: runs a binary the install replaced"`)
	assert.Contains(t, first, `"RESTART com.nova.loop.t2 on localhost: not loaded"`)
	assert.Contains(t, first, `"RESTART com.nova.loop.t3 on localhost: names a copy, not `+filepath.Join(bin, "nova-swarm")+`"`)
	assert.Regexp(t, `ADOPT step=server host=localhost before=\S+ after=\S+ restarted=com.nova.loop.t1,com.nova.loop.t2,com.nova.loop.t3 CHANGED`, first)
	for _, step := range []string{"store", "dashboard", "friends"} {
		assert.Regexp(t, `ADOPT step=`+step+` host=localhost .* UNCHANGED`, first)
	}
	ran := log()
	assert.NotContains(t, ran, "kickstart")
	for _, l := range []string{"t1", "t3"} {
		assert.Contains(t, ran, "bootout com.nova.loop."+l+"\n")
	}
	for _, l := range []string{"t1", "t2", "t3"} {
		assert.Contains(t, ran, "bootstrap com.nova.loop."+l+"\n")
	}
	assert.NotContains(t, ran, "com.nova.loop.t4", "a disabled agent is left alone")
	b, err := os.ReadFile(t3)
	require.NoError(t, err)
	assert.Contains(t, string(b), "<string>"+filepath.Join(bin, "nova-swarm")+"</string>", "the copy's plist names the bin directory's tool")

	live := exec.Command(filepath.Join(bin, "nova-sprint"), "live", "--json", "--bin-dir", bin, "--agents-dir", agents, "--launchctl", launchctl)
	out, err = live.Output()
	require.NoError(t, err)
	var m struct {
		Agents []struct {
			Label string `json:"label"`
			Stale bool   `json:"stale"`
			Fresh bool   `json:"fresh"`
		} `json:"agents"`
	}
	require.NoError(t, json.Unmarshal(out, &m))
	require.Len(t, m.Agents, 4)
	for _, a := range m.Agents {
		assert.False(t, a.Stale, a.Label)
	}

	second := r.play(t, "tools.yml", vars...)
	assert.Contains(t, second, "restarted=none UNCHANGED", "a second run finds nothing stale")
	assert.Empty(t, log(), "and runs no launchctl verb but print")

	// the install again, and t1's bootstrap fails: each is bootstrapped again, the step is refused naming it
	install(filepath.Join(bin, "nova-swarm"))
	write(filepath.Join(state, "com.nova.loop.t1.fail"), "")
	third, err := r.playResult(t, "tools.yml", vars...)
	require.Error(t, err, third)
	assert.Contains(t, third, "ADOPT REFUSED step=server host=localhost: server: bootstrap each from its plist failed for com.nova.loop.t1;")
	assert.Contains(t, third, "the store library was not changed by this run")
	ran = log()
	assert.Equal(t, 2, strings.Count(ran, "bootstrap com.nova.loop.t1\n"), "bootstrapped, then bootstrapped again:\n%s", ran)
}
