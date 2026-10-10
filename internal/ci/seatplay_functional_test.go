//go:build functional

package ci

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
// starts a plist's ProgramArguments on bootstrap, kills what it started on
// bootout, logs every verb but print, and refuses a bootstrap of a label
// with a <label>.fail file. It never reaches launchd.
const seatLaunchctl = `#!/bin/sh
S=%s; L=%s
case "$1" in
print) l=${2##*/}; [ -f "$S/$l.pid" ] && kill -0 "$(cat "$S/$l.pid")" 2>/dev/null || exit 113
  printf '\tstate = running\n\tpid = %%s\n' "$(cat "$S/$l.pid")"; exit 0;;
bootout) l=${2##*/}; echo "bootout $l" >> "$L"; [ -f "$S/$l.pid" ] || exit 3; kill "$(cat "$S/$l.pid")" 2>/dev/null; rm -f "$S/$l.pid"; exit 0;;
bootstrap) l=$(basename "$3" .plist); echo "bootstrap $l" >> "$L"; [ -f "$S/$l.fail" ] && exit 5; [ -f "$S/$l.pid" ] && exit 5
  set -- $(/usr/bin/plutil -extract ProgramArguments xml1 -o - "$3" | sed -n 's:.*<string>\(.*\)</string>.*:\1:p')
  nohup "$@" >/dev/null 2>&1 & echo $! > "$S/$l.pid"; exit 0;;
*) echo "unexpected $*" >> "$L"; exit 64;;
esac
`

// seatTool is the agents' binary and the fake nova-friend, one program built
// here with its directories and its build word stamped in (-X), so each
// build's bytes differ and a copy runs anywhere:
//   - named nova-sprint: run is the old server's stand-in, logging "server
//     up <build>" and running until it is stopped; every other verb is the
//     real nova-sprint's (exec);
//   - member <n>: a member's stand-in, sleeping n seconds;
//   - a last argument that is a number: sleep that long (a loop agent);
//   - run --as <f>: a friend daemon, beating into <dir>/<f>.beat every
//     second unless <dir>/<f>.nobeat was there when it started; while
//     <dir>/<f>.busy is there its lanes hold a card, and it puts the card
//     down 8 s after the launchctl log says the server step bootstrapped
//     com.nova.loop.srv, logging "drained <f>";
//   - status --as <f>: its last beat and its lanes;
//   - install --as <f> ... [--dry-run]: logs "install <f>", writes the
//     friend's plist naming this binary (or, with <dir>/<f>.noplist,
//     removes it), boots it out and bootstraps it through the launchctl.
const seatTool = `package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var dir, agents, launchctl, build, realBin string

func flag(args []string, name string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == name {
			return args[i+1]
		}
	}
	return ""
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func logLine(s string) {
	f, err := os.OpenFile(filepath.Join(dir, "log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		fmt.Fprintln(f, s)
		f.Close()
	}
}

func main() {
	args := os.Args[1:]
	if filepath.Base(os.Args[0]) == "nova-sprint" {
		if len(args) > 0 && args[0] == "run" {
			logLine("server up " + build)
			for {
				time.Sleep(time.Hour)
			}
		}
		syscall.Exec(realBin, append([]string{realBin}, args...), os.Environ())
		return
	}
	if n, err := strconv.Atoi(args[len(args)-1]); err == nil && (len(args) == 1 || args[0] == "member") {
		time.Sleep(time.Duration(n) * time.Second)
		return
	}
	who := flag(args, "--as")
	switch args[0] {
	case "run":
		beat := !exists(filepath.Join(dir, who+".nobeat"))
		var sawRestart time.Time
		for {
			if beat {
				os.WriteFile(filepath.Join(dir, who+".beat"), []byte(time.Now().UTC().Format(time.RFC3339)), 0o644)
			}
			if exists(filepath.Join(dir, who+".busy")) {
				b, _ := os.ReadFile(filepath.Join(dir, "log"))
				if sawRestart.IsZero() && strings.Contains(string(b), "bootstrap com.nova.loop.srv") {
					sawRestart = time.Now()
				}
				if !sawRestart.IsZero() && time.Since(sawRestart) > 8*time.Second { // wall-ok: the fake daemon holding its card, not a test bound
					os.Remove(filepath.Join(dir, who+".busy"))
					logLine("drained " + who)
				}
			}
			time.Sleep(time.Second)
		}
	case "status":
		lanes := "1:s1:-"
		if exists(filepath.Join(dir, who+".busy")) {
			lanes = "1:s1:card-1/1"
		}
		b, _ := os.ReadFile(filepath.Join(dir, who+".beat"))
		fmt.Printf("STATUS OK daemon=up lanes=%q last_beat=%s build=%s\n", lanes, strings.TrimSpace(string(b)), build)
	case "install":
		if args[len(args)-1] == "--dry-run" {
			fmt.Println("INSTALL PLAN " + who)
			return
		}
		logLine("install " + who)
		label := "com.nova.friend-" + who
		plist := filepath.Join(agents, label+".plist")
		self, _ := os.Executable()
		if exists(filepath.Join(dir, who+".noplist")) {
			os.Remove(plist)
		} else {
			var b strings.Builder
			b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?><plist version=\"1.0\"><dict><key>Label</key><string>" + label + "</string><key>ProgramArguments</key><array>")
			for _, a := range append([]string{self, "run"}, args[1:]...) {
				b.WriteString("<string>" + a + "</string>")
			}
			b.WriteString("</array><key>KeepAlive</key><true/></dict></plist>")
			os.WriteFile(plist, []byte(b.String()), 0o644)
		}
		run := func(a ...string) { p, err := os.StartProcess(launchctl, append([]string{launchctl}, a...), &os.ProcAttr{Files: []*os.File{nil, os.Stdout, os.Stderr}}); if err == nil { p.Wait() } }
		run("bootout", "gui/0/"+label)
		if exists(plist) {
			run("bootstrap", "gui/0", plist)
		}
	}
}
`

// seatRedis is a nova-redis for the test: its library digest is stamped
// in; fn load puts it in the store's state file, fn check compares.
const seatRedis = `#!/bin/sh
D=%s; S=%s
case "$2" in
load) was=$(cat "$S"); echo "$D" > "$S"; if [ "$was" = "$D" ]; then echo "UNCHANGED nova_sprint sha=$D store=twin"; else echo "REPLACED nova_sprint sha=$D was=$was store=twin"; fi;;
check) l=$(cat "$S"); if [ "$l" = "$D" ]; then echo "OK nova_sprint sha=$D loaded=$l want=$D store=twin"; else echo "STALE nova_sprint sha=$D loaded=$l want=$D store=twin"; exit 1; fi;;
esac
`

// seatUpdate is the stage's nova-update: release install puts every file of
// the stage whose bytes differ into the bin directory on a fresh inode, as
// the real one does, and skips the rest.
const seatUpdate = `#!/bin/sh
stage=$(dirname "$0"); bin=""; v=""
while [ "$#" -gt 0 ]; do case "$1" in --bin) bin=$2; shift;; --version) v=$2; shift;; esac; shift; done
n=0; k=0
for f in "$stage"/*; do b=$(basename "$f")
  if cmp -s "$f" "$bin/$b"; then k=$((k+1)); else cp "$f" "$bin/$b.tmp" && mv "$bin/$b.tmp" "$bin/$b"; n=$((n+1)); fi
done
echo "RELEASE INSTALLED version=$v tools=$n skipped=$k"
`

// TestSeatPlayAdoptsInOrderAndRefusesEachHalfMove runs the seat's part of
// tools.yml for real (--tags seat: the candidate checks, the install, the
// migration, the library and the seat play) on a coordinator fixture whose
// home, launchd, store and friend are the test's own, in the order a real
// adoption meets them: every agent is fresh until the play's own install
// replaces its binary. Then:
//  1. the run restarts the loop agent, reinstalls the friend after its lanes
//     put their card down, sees a beat newer than the reinstall, and loads
//     the new library;
//  2. a second run changes nothing;
//  3. a new build refused at the dashboard step loads the library of before
//     again with the nova-redis saved before the install, and puts back only
//     the tools this adoption kept: a file an older adoption left in the
//     rollback directory stays out of the bin directory;
//  4. a new friend daemon that never beats is refused although the old one
//     beat during the drain;
//  5. a reinstall that leaves no plist is refused naming it;
//  6. a bootstrap that fails is bootstrapped again and refused naming it.
func TestSeatPlayAdoptsInOrderAndRefusesEachHalfMove(t *testing.T) {
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
	fdir := filepath.Join(r.dir, "friend")
	redisState := filepath.Join(r.dir, "redis-loaded")
	for _, d := range []string{bin, agents, stage, state, fdir, filepath.Join(r.dir, "dir-a")} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	write := func(path, body string, mode os.FileMode) {
		t.Helper()
		require.NoError(t, os.WriteFile(path+".tmp", []byte(body), mode))
		require.NoError(t, os.Rename(path+".tmp", path)) // a fresh inode, as release install makes one
	}
	launchctl := filepath.Join(r.dir, "launchctl")
	write(launchctl, fmt.Sprintf(seatLaunchctl, state, filepath.Join(fdir, "log")), 0o755)
	t.Cleanup(func() {
		pids, _ := filepath.Glob(filepath.Join(state, "*.pid")) // ignored: a pattern that is always valid
		for _, f := range pids {
			b, _ := os.ReadFile(f) // ignored: a pid file the fake wrote; a missing one has nothing to stop
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				_ = syscall.Kill(pid, syscall.SIGTERM) // ignored: a process this test started that has already ended
			}
		}
	})

	// the real nova-sprint, which the stand-in named nova-sprint execs for every verb but run
	realDir := filepath.Join(r.dir, "real")
	require.NoError(t, os.MkdirAll(realDir, 0o755))
	r.build(t, realDir+string(filepath.Separator), "", "./cmd/nova-sprint")
	realSprint := filepath.Join(realDir, "nova-sprint")
	write(filepath.Join(bin, "nova-secrets"), "#!/bin/sh\nwhile [ \"$#\" -gt 0 ] && [ \"$1\" != \"--\" ]; do shift; done; shift; exec \"$@\"\n", 0o755)
	write(filepath.Join(stage, "nova-secrets"), "#!/bin/sh\nwhile [ \"$#\" -gt 0 ] && [ \"$1\" != \"--\" ]; do shift; done; shift; exec \"$@\"\n", 0o755)
	// the migration logs how many old servers and members still run while it does
	config := fmt.Sprintf("#!/bin/sh\nn=$(pgrep -f '%s/nova-sprint run|%s/nova-worker member' | wc -l | tr -d ' ')\necho \"migrate holds=$n\" >> %s\necho \"MIGRATE OK applied=0\"\n", bin, bin, filepath.Join(fdir, "log"))
	write(filepath.Join(bin, "nova-config"), config, 0o755)
	write(filepath.Join(stage, "nova-config"), config, 0o755)
	write(filepath.Join(stage, "nova-update"), seatUpdate, 0o755)
	write(redisState, "lib-old\n", 0o644)

	src := filepath.Join(r.dir, "tool-src")
	require.NoError(t, os.MkdirAll(src, 0o755))
	write(filepath.Join(src, "go.mod"), "module seattool\n\ngo 1.22\n", 0o644)
	write(filepath.Join(src, "main.go"), seatTool, 0o644)
	// tool builds the agents' binary stamped with word, into the bin directory
	// (the build installed before) or the stage (the new one), with the
	// nova-redis of a library of the same word
	tool := func(word, dst string) {
		t.Helper()
		out := filepath.Join(r.dir, "tool-"+word)
		build := exec.Command("go", "build", "-o", out, "-ldflags", fmt.Sprintf("-X main.dir=%s -X main.agents=%s -X main.launchctl=%s -X main.build=%s -X main.realBin=%s", fdir, agents, launchctl, word, realSprint), ".")
		build.Dir = src
		build.Env = append(goenv.Clean(os.Environ()), "GOFLAGS=")
		b, err := build.CombinedOutput()
		require.NoError(t, err, string(b))
		b, err = os.ReadFile(out)
		require.NoError(t, err)
		write(filepath.Join(dst, "nova-worker"), string(b), 0o755)
		write(filepath.Join(dst, "nova-sprint"), string(b), 0o755)
		write(filepath.Join(dst, "nova-friend"), string(b), 0o755)
		write(filepath.Join(dst, "nova-redis"), fmt.Sprintf(seatRedis, "lib-"+word, redisState), 0o755)
	}
	tool("old", bin)
	tool("new", stage)

	plist := func(label string, args ...string) string {
		var b strings.Builder
		for _, a := range args {
			b.WriteString("<string>" + a + "</string>")
		}
		p := filepath.Join(agents, label+".plist")
		write(p, `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>Label</key><string>`+label+`</string><key>ProgramArguments</key><array>`+b.String()+`</array></dict></plist>`, 0o644)
		return p
	}
	friendArgs := []string{"--as", "fa", "--harness", "opencode", "--dir", filepath.Join(r.dir, "dir-a"), "--width", "1"}
	start := []string{
		plist("com.nova.loop.srv", filepath.Join(bin, "nova-sprint"), "run", "--stand-in"),
		plist("com.nova.loop.mem", filepath.Join(bin, "nova-worker"), "member", "100000"),
		plist("com.nova.friend-fa", append([]string{filepath.Join(bin, "nova-friend"), "run"}, friendArgs...)...),
	}
	plist("com.nova.loop.t4", filepath.Join(bin, "nova-worker"), "1004") // never loaded: see Disabled below
	write(filepath.Join(agents, "com.nova.loop.t4.plist"), strings.Replace(string(must(os.ReadFile(filepath.Join(agents, "com.nova.loop.t4.plist")))), "</dict>", "<key>Disabled</key><true/></dict>", 1), 0o644)
	for _, p := range start {
		out, err := exec.Command(launchctl, "bootstrap", "gui/0", p).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	twin := filepath.Join(r.dir, "twin")
	out, err := exec.Command(filepath.Join(bin, "nova-sprint"), "init", "--redis", "mem:"+twin, "--readers", "reader-a,reader-b", "--members", "m1", "--actor", "tester").CombinedOutput()
	require.NoError(t, err, string(out))

	// manifest is the installed nova-sprint's live
	manifest := func() map[string]map[string]any {
		out, err := exec.Command(filepath.Join(bin, "nova-sprint"), "live", "--json", "--bin-dir", bin, "--agents-dir", agents, "--launchctl", launchctl).Output()
		if err != nil {
			return nil
		}
		var m struct {
			Agents []map[string]any `json:"agents"`
		}
		if json.Unmarshal(out, &m) != nil {
			return nil
		}
		by := map[string]map[string]any{}
		for _, a := range m.Agents {
			by[a["label"].(string)] = a
		}
		return by
	}
	// before the play, every agent runs the binary installed: none is stale
	wait := 30 * time.Second
	if v := os.Getenv("NOVA_TEST_WAIT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			wait = d
		}
	}
	deadline := time.After(wait)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for ready := false; !ready; {
		select {
		case <-deadline:
			t.Fatalf("the agents never ran fresh before the play: %v", manifest())
		case <-tick.C:
			m := manifest()
			ready = m != nil && m["com.nova.friend-fa"]["beat"] != ""
			for _, l := range []string{"com.nova.loop.srv", "com.nova.loop.mem", "com.nova.friend-fa"} {
				ready = ready && m[l]["fresh"] == true && m[l]["stale"] == false
			}
		}
	}
	write(filepath.Join(fdir, "fa.busy"), "", 0o644) // the old daemon holds a card when the play starts

	vars := []string{"--tags", "seat", "--limit", "localhost", "-e", "nova_home=" + home, "-e", "nova_version=v0.0.0-seat",
		"-e", "nova_redis_addr=mem:" + twin, "-e", "nova_launchctl=" + launchctl, "-e", "nova_sops=/usr/bin/true",
		"-e", "nova_member_stop_timeout=5", "-e", "nova_seat_beat_within=15", "-e", "nova_seat_friend_drain=60"}
	logs := func() string {
		var all string
		for _, f := range []string{filepath.Join(fdir, "log")} {
			b, _ := os.ReadFile(f) // ignored: no log is nothing logged
			all += string(b)
			require.NoError(t, os.WriteFile(f, nil, 0o644))
		}
		return all
	}
	logs()

	// 1. the whole sequence
	first := r.play(t, "tools.yml", vars...)
	assert.Contains(t, first, "ADOPT step=store host=localhost before=lib-old after=lib-new CHANGED")
	assert.Contains(t, first, "WINDOW host=localhost replaces=")
	assert.Contains(t, first, `"RESTART com.nova.loop.srv on localhost: the window stopped it"`)
	assert.Regexp(t, `ADOPT step=server host=localhost before=\S+ after=\S+ restarted=com.nova.loop.mem,com.nova.loop.srv CHANGED`, first)
	assert.Contains(t, first, "ADOPT step=friends host=localhost before=")
	assert.Contains(t, first, "reinstalled=fa CHANGED")
	assert.Contains(t, first, "FAILED - RETRYING: [localhost]: friends: fa's lanes have no card in hand", "the drain waited")
	ran := logs()
	assert.NotContains(t, ran, "kickstart")
	// the window: the old server and member stopped before the migration, nothing old running during it
	migrate := strings.Index(ran, "migrate holds=")
	require.Positive(t, migrate, ran)
	assert.Contains(t, ran, "migrate holds=0\n", "no old server or member ran while it migrated:\n%s", ran)
	assert.Less(t, strings.Index(ran, "bootout com.nova.loop.srv"), migrate)
	assert.Less(t, strings.Index(ran, "bootout com.nova.loop.mem"), migrate)
	assert.Less(t, migrate, strings.Index(ran, "bootstrap com.nova.loop.srv"))
	assert.Less(t, migrate, strings.Index(ran, "server up new\n"), "the new server starts after the migration")
	assert.Less(t, strings.Index(ran, "drained fa"), strings.Index(ran, "install fa"), "the reinstall waits for the card:\n%s", ran)
	assert.NotContains(t, ran, "com.nova.loop.t4", "a disabled agent is left alone")
	m := manifest()
	for _, l := range []string{"com.nova.loop.srv", "com.nova.loop.mem", "com.nova.friend-fa", "com.nova.loop.t4"} {
		assert.Equal(t, false, m[l]["stale"], l)
	}
	assert.Equal(t, "lib-new", strings.TrimSpace(string(must(os.ReadFile(redisState)))))

	// 2. a second run of the whole sequence changes nothing
	second := r.play(t, "tools.yml", vars...)
	for _, step := range []string{"store", "server", "dashboard", "friends"} {
		assert.Regexp(t, `ADOPT step=`+step+` host=localhost .*UNCHANGED"`, second)
	}
	assert.Regexp(t, `localhost\s+: ok=\d+\s+changed=0\s`, second, "nothing installed, loaded, restarted or written")
	assert.Empty(t, logs(), "no bootout, bootstrap or install")

	// 3. a new build refused at the dashboard, after the migration: the tools of before
	// are put back, their library loaded and read back, and the old server started again
	dash := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	defer dash.Close()
	tool("new3", stage)
	// what an older adoption left in the rollback directory: a nova-config this
	// one does not replace, and a tool the bin directory no longer has
	kept := filepath.Join(home, "nova-bench", "release", "adopt-rollback", "bin")
	require.NoError(t, os.MkdirAll(kept, 0o755))
	write(filepath.Join(kept, "nova-config"), "#!/bin/sh\necho an older adoption's nova-config\n", 0o755)
	write(filepath.Join(kept, "nova-ancient"), "#!/bin/sh\n", 0o755)
	third, err := r.playResult(t, "tools.yml", append(vars, "-e", "nova_seat_dashboard_url="+dash.URL)...)
	require.Error(t, err, third)
	assert.Contains(t, third, "ADOPT REFUSED step=dashboard host=localhost: "+dash.URL+" answered 500 with no summary;")
	assert.Contains(t, third, "library lib-new read back, the one of before")
	assert.Contains(t, third, "started again: com.nova.loop.mem,com.nova.loop.srv")
	assert.Equal(t, "lib-new", strings.TrimSpace(string(must(os.ReadFile(redisState)))), "the restore loaded the old library")
	assert.Equal(t, config, string(must(os.ReadFile(filepath.Join(bin, "nova-config")))), "an older adoption's nova-config is not put back")
	assert.NoFileExists(t, filepath.Join(bin, "nova-ancient"), "a tool an older adoption kept is not put back")
	ran = logs()
	assert.Less(t, strings.Index(ran, "migrate holds=0"), strings.Index(ran, "server up new3\n"), ran)
	assert.True(t, strings.HasPrefix(ran[strings.LastIndex(ran, "server up"):], "server up new\n"), "the server last started is the old one:\n%s", ran)
	m = manifest()
	assert.Equal(t, false, m["com.nova.loop.srv"]["stale"], "the old server runs the old binary, put back")

	// 4. the new daemon never beats; the old one beat through the drain
	write(filepath.Join(fdir, "fa.nobeat"), "", 0o644)
	tool("new4", stage)
	fourth, err := r.playResult(t, "tools.yml", vars...)
	require.Error(t, err, fourth)
	assert.Contains(t, fourth, "ADOPT REFUSED step=friends host=localhost: fa has not beaten since its reinstall within 15 s")
	require.NoError(t, os.Remove(filepath.Join(fdir, "fa.nobeat")))
	logs()

	// 5. a reinstall that leaves no plist
	write(filepath.Join(fdir, "fa.noplist"), "", 0o644)
	tool("new5", stage)
	fifth, err := r.playResult(t, "tools.yml", vars...)
	require.Error(t, err, fifth)
	assert.Contains(t, fifth, "ADOPT REFUSED step=friends host=localhost: fa has no plist in "+agents+" after its reinstall")
	require.NoError(t, os.Remove(filepath.Join(fdir, "fa.noplist")))
	logs()

	// 6. the member's bootstrap fails: each is bootstrapped again, the step refused naming
	// it, and the rollback says it could not start it on the tools of before either
	write(filepath.Join(state, "com.nova.loop.mem.fail"), "", 0o644)
	tool("new6", stage)
	sixth, err := r.playResult(t, "tools.yml", vars...)
	require.Error(t, err, sixth)
	assert.Contains(t, sixth, "ADOPT REFUSED step=server host=localhost: server: bootstrap each from its plist failed for com.nova.loop.mem;")
	assert.Contains(t, sixth, "not started: com.nova.loop.mem")
	assert.Equal(t, 3, strings.Count(logs(), "bootstrap com.nova.loop.mem\n"), "bootstrapped, again, and by the rollback")
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
