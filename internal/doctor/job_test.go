package doctor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
)

const (
	testRedis = "127.0.0.1:6390"
	testHome  = "/home/ada"
)

// testNow is the fake clock every world reads: no test reads real time.
var testNow = time.Date(2026, 10, 6, 8, 10, 0, 0, time.UTC)

// world is a machine as the job steps see it through the tools: each field one
// dependency, true when it is there. The fake tools answer from it, and the fake shell
// (world.run) changes it only by the exact fix commands nova-doctor prints. Both read
// every nova command through parseArgv (argv_test.go) first, so neither accepts a line
// the real tool refuses. The session's evidence is nova-friend check's: the age of the
// last session pong ever recorded ("-" none), the age of the newest card finished, and
// the messages back; its verdict is judged over the --since the doctor passed.
type world struct {
	t                                                    *testing.T
	root                                                 string
	redisUp, loginOK, storeLogin, schema, applied, aclOK bool
	fnLoaded, swarmAgrees                                bool
	daemon, broken, delivered                            bool
	pongAge                                              string
	finishAge                                            time.Duration
	back, outbox                                         int
	calls                                                int // tool calls the doctor made
}

var allTools = []string{"nova-bus", "nova-config", "nova-friend", "nova-redis", "nova-sprint", "nova-swarm"}

func healthyWorld(t *testing.T) *world {
	w := &world{t: t, root: t.TempDir(), redisUp: true, loginOK: true, storeLogin: true, schema: true, applied: true,
		aclOK: true, fnLoaded: true, swarmAgrees: true, daemon: true, delivered: true,
		pongAge: "20s", finishAge: 8 * time.Minute, back: 2, outbox: 2}
	require.NoError(t, os.MkdirAll(filepath.Join(w.root, "bin"), 0o755))
	for _, n := range allTools {
		w.install(n)
	}
	return w
}

func (w *world) install(tool string) {
	require.NoError(w.t, os.WriteFile(filepath.Join(w.root, "bin", tool), nil, 0o755))
}

func (w *world) uninstall(tool string) {
	require.NoError(w.t, os.Remove(filepath.Join(w.root, "bin", tool)))
}

// deaf is a session that answered nothing in its windows: its last pong pongAge ago ("-"
// none), its newest card finished two hours ago, and nothing back on the bus.
func (w *world) deaf(pongAge string) {
	w.pongAge, w.finishAge, w.back = pongAge, 2*time.Hour, 0
}

func (w *world) installed(tool string) bool {
	_, err := os.Stat(filepath.Join(w.root, "bin", tool))
	return err == nil
}

// exitErr is a tool's exit: its code and what it said on stderr.
type exitErr struct {
	code   int
	stderr string
}

func (e exitErr) Error() string { return e.stderr }
func (e exitErr) ExitCode() int { return e.code }

func (w *world) env() fakeEnv {
	return fakeEnv{
		env:   map[string]string{"PATH": "bin", "HOME": testHome, "NOVA_REDIS_ADDR": testRedis, "NOVA_SPRINT_ACTOR": "ada"},
		root:  w.root,
		clock: testNow,
		dial: func(addr string) error {
			if addr != testRedis || !w.redisUp {
				return errors.New("connection refused")
			}
			return nil
		},
		exec: w.exec,
	}
}

// exec is every tool the job steps call, answering as the real one does from the world.
func (w *world) exec(name string, args ...string) (string, error) {
	tool := filepath.Base(name)
	if !w.installed(tool) {
		return "", &exec.Error{Name: tool, Err: exec.ErrNotFound}
	}
	if len(args) == 1 && args[0] == "version" { // the self check, which runs through the frame's Env
		return buildinfo.Line(tool, "v1.0.0"), nil
	}
	w.calls++
	cmd := tool + " " + strings.Join(args, " ")
	a, refused := parseArgv(cmd)
	if refused != "" {
		w.t.Fatalf("the doctor ran a command the real tool refuses: %q: %s", cmd, refused)
	}
	switch a.verb {
	case "nova-redis acl check", "nova-redis fn check":
		if a.flags["addr"] != testRedis {
			break
		}
		if a.verb == "nova-redis fn check" {
			if !w.fnLoaded {
				return "", exitErr{1, "FN CHECK MISSING library=nova"}
			}
			return "FN CHECK OK library=nova\n", nil
		}
		switch {
		case !w.redisUp:
			return "", exitErr{2, "ACL CHECK FAILED store=" + testRedis + " err=\"connection refused\""}
		case !w.loginOK:
			return "", exitErr{2, "ACL CHECK FAILED store=" + testRedis + " err=\"WRONGPASS\""}
		case !w.aclOK:
			// As the real tool: the drift is on stdout at exit 1, its remedy a command and then prose.
			return "NOTE ACL DEFAULT on=false nopass=false\nACL CHECK DRIFT users=4 differ=1 library=abc store=" + testRedis +
				" remedy=\"nova-redis acl apply --addr " + testRedis + " sets the users that differ\"\n", exitErr{1, ""}
		}
		return "NOTE ACL DEFAULT on=false nopass=false\nACL CHECK OK users=4 library=abc store=" + testRedis + "\n", nil
	case "nova-config login":
		if !w.storeLogin {
			return "", exitErr{2, "nova-config login REFUSED: no login is recorded at /x; run: nova-config login --store <dir> --as <seat> --key <file> --secret <NAME> --dsn <dsn> --friend <actor>"}
		}
		return "LOGIN file=/x dsn=postgres://h/db resolves=yes\n", nil
	case "nova-config status":
		if !w.schema {
			return "CONFIG STATUS pg=h schema=0 redis=-\n", exitErr{1, "nova-config status REFUSED: schema config is not there yet; run: nova-config migrate"}
		}
		return "CONFIG STATUS pg=h schema=3\n", nil
	case "nova-config apply":
		if !a.has("check") || a.flags["redis"] != testRedis {
			break
		}
		rev := "7"
		if !w.applied {
			rev = "6"
		}
		return "CONFIG CHECK kind=machine add=0 set=0 remove=0 rev=7 applied=7\nCONFIG CHECK kind=friend add=0 set=0 remove=0 rev=7 applied=" + rev + "\n", nil
	case "nova-swarm doctor":
		if !w.swarmAgrees {
			return "", exitErr{2, "DOCTOR DRIFT path=/usr/bin/nova-swarm\nDOCTOR REFUSED /usr/bin/nova-swarm shadows ~/.local/bin/nova-swarm"}
		}
		return "DOCTOR OK stamp=v1.0.0\n", nil
	case "nova-friend check":
		// The health check: never the delivery check, which the doctor only prints.
		if a.has("harness") || !a.has("json") || a.flags["redis"] != testRedis {
			break
		}
		since, err := time.ParseDuration(a.flags["since"])
		require.NoError(w.t, err, "the doctor passes the window: %q", cmd)
		if !slices.Equal(a.args, []string{"bob"}) && !(len(a.args) == 0 && a.flags["as"] == "ada") {
			break
		}
		rep := w.friendReport(since)
		b, err := json.Marshal(rep)
		require.NoError(w.t, err)
		if rep.Friends[0].Verdict.Verdict != "ok" {
			return string(b), exitErr{1, ""}
		}
		return string(b), nil
	}
	w.t.Fatalf("the doctor called a tool the fake does not know: %s", cmd)
	return "", nil
}

func (w *world) friendReport(since time.Duration) friend.CheckReport {
	f := friend.FriendCheck{Friend: "bob"}
	f.Daemon = friend.DaemonFacts{Friend: "bob", Agent: "loaded", PID: "42", Status: "ok", PongAge: w.pongAge, Presence: "up"}
	if !w.daemon {
		f.Daemon.Agent, f.Daemon.PID, f.Daemon.Status = "not-loaded", "-", "none"
	}
	f.Harness = friend.HarnessFacts{Friend: "bob", Harness: "claude", Route: "push", Last: "2026-10-06T08:00:00Z", LastExit: "0", Broken: "-", Reason: "-"}
	if w.delivered {
		f.Harness.Delivered = 3
	}
	if w.broken {
		f.Harness.Broken, f.Harness.Reason = "2026-10-06T07:00:00Z", "provider refused 3 turns"
	}
	f.Bus = friend.BusFacts{Friend: "bob", RealSince: w.back, LastReal: "-"}
	if w.back > 0 {
		f.Bus.LastReal = "2026-10-06T08:01:00Z"
	}
	f.Work = friend.WorkFacts{Friend: "bob", Outbox: w.outbox, NewestOutbox: "-", NewestAt: "-"}
	if w.outbox > 0 {
		f.Work.NewestOutbox, f.Work.NewestAt = "card-7", testNow.Add(-w.finishAge).Format(time.RFC3339)
	}
	// The verdict as nova-friend decides it over --since (docs/SPEC-FRIEND.md, "The
	// verdicts"): a delivery that succeeded with no session pong aged within the window
	// and no real message back is deaf.
	pong, err := time.ParseDuration(w.pongAge)
	verdict := "ok"
	switch {
	case w.broken:
		verdict = "broken"
	case w.delivered && w.back == 0 && (err != nil || pong > since):
		verdict = "deaf"
	case !w.delivered && w.back == 0:
		verdict = "silent"
	case !w.daemon:
		verdict = "down"
	}
	f.Verdict = friend.VerdictFacts{Friend: "bob", Verdict: verdict, Shown: "-", Why: verdict}
	s := friend.CheckSummary{Friends: 1}
	switch verdict {
	case "ok":
		s.OK = 1
	case "broken":
		s.Broken = 1
	case "deaf":
		s.Deaf = 1
	case "silent":
		s.Silent = 1
	default:
		s.Down = 1
	}
	return friend.CheckReport{Friends: []friend.FriendCheck{f}, Summary: s}
}

// installLine is a nova tool's install, from the module the doctor was built from.
var installLine = regexp.MustCompile(`^go install \S+/cmd/(nova-[a-z]+)@latest$`)

// run is the cold reader's shell: it runs a command exactly as printed, and knows only
// the commands a machine's owner has. A nova command goes through parseArgv first, so a
// line the real tool refuses (a flag after an argument, a required flag missing) fails
// here as it would there; a placeholder is a fix a stranger could not run.
func (w *world) run(cmd string) {
	if strings.Contains(cmd, "<") {
		w.t.Fatalf("the fix has a placeholder the cold reader cannot fill: %q", cmd)
	}
	if m := installLine.FindStringSubmatch(cmd); m != nil {
		w.install(m[1])
		return
	}
	if cmd == "install -m 0755 "+testHome+"/.local/bin/nova-swarm bin/nova-swarm" {
		w.swarmAgrees = true
		return
	}
	a, refused := parseArgv(cmd)
	if refused != "" {
		w.t.Fatalf("the fix is a command the real tool refuses: %q: %s", cmd, refused)
	}
	f := a.flags
	switch {
	case a.verb == "nova-redis serve" && f["bind"] == "127.0.0.1" && f["port"] == "6390" && f["dir"] == testHome+"/nova/stores/redis":
		w.redisUp = true
	case a.verb == "nova-redis fn load" && f["addr"] == testRedis:
		w.fnLoaded = true
	case a.verb == "nova-redis acl apply" && f["addr"] == testRedis:
		w.aclOK = true
	case a.verb == "nova-config migrate":
		w.schema = true
	case a.verb == "nova-config apply" && !a.has("check") && f["redis"] == testRedis:
		w.applied = true
	case a.verb == "nova-friend install" && f["as"] == "bob" && f["harness"] == "claude" && f["dir"] == "/home/bob" && f["redis"] == testRedis &&
		f["config-dir"] == "/home/bob/.claude":
		w.daemon, w.broken = true, false
	case a.verb == "nova-friend ping" && f["as"] == "ada" && f["to"] == "bob" && f["redis"] == testRedis:
		w.delivered = true
	case a.verb == "nova-friend check" && a.has("harness"):
		// The delivery check: --as is the friend itself, its pong goes to the coordinator.
		if f["as"] != "bob" || f["harness"] != "claude" || f["dir"] != "/home/bob" || f["redis"] != testRedis || f["to"] != "ada" || len(a.args) > 0 {
			w.t.Fatalf("the delivery check is not bob's own, to ada: %q", cmd)
		}
		w.pongAge = "5s"
	default:
		w.t.Fatalf("the fix is not a command the cold reader can run as printed: %q", cmd)
	}
}

// fixesParse is every fix line in a doctor's output that names a nova tool, each read
// as its tool would: none may be a line the real tool refuses.
func fixesParse(t *testing.T, lines []string) {
	t.Helper()
	for _, l := range lines {
		fix, ok := "", false
		if _, fix, ok = strings.Cut(l, " fix: "); !ok {
			_, fix, ok = strings.Cut(l, " next: ")
		}
		if !ok || !strings.HasPrefix(fix, "nova-") {
			continue
		}
		_, refused := parseArgv(fix)
		assert.Empty(t, refused, "a fix the real tool refuses: %q", fix)
	}
}

// doctor runs nova-doctor as a stranger would and returns the exit and the lines.
func (w *world) doctor(args ...string) (int, []string) {
	var out, errb bytes.Buffer
	code := Main(NewRegistry(), w.env(), "", args, strings.NewReader(""), &out, &errb)
	require.Empty(w.t, errb.String())
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	fixesParse(w.t, lines)
	return code, lines
}

// jobArgs are the flags each job runs with in these tests.
var jobArgs = map[string][]string{
	"local-notes": {"--job", "local-notes"},
	"messaging":   {"--job", "messaging"},
	"friend":      {"--job", "friend", "--as", "bob", "--dir", "/home/bob", "--config-dir", "/home/bob/.claude"},
	"worker":      {"--job", "worker"},
	"coordinator": {"--job", "coordinator", "--as", "ada"},
}

// The acceptance: one dependency is deliberately missing; nova-doctor --job names it as
// the first missing step, every step before it ok and every one after it blocked, and
// prints the one command that fixes it. A cold reader that knows nothing else runs that
// command as printed and nova-doctor is then ready. The test records the calls: doctor
// runs, repairs, and the tool calls the doctor itself made.
func TestDoctorNamesTheFirstMissingDependencyAndItsFix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		job, step, why string
		breakIt        func(w *world)
		extra          []string // flags after the job's own
	}{
		{"local-notes", "redis-reachable", "", func(w *world) { w.redisUp = false }, nil},
		{"local-notes", "binaries", "", func(w *world) { w.uninstall("nova-redis"); w.redisUp = true }, nil},
		{"messaging", "binaries", "", func(w *world) { w.uninstall("nova-bus") }, nil},
		{"worker", "redis-functions", "", func(w *world) { w.fnLoaded = false }, nil},
		{"worker", "swarm-binary", "", func(w *world) { w.swarmAgrees = false }, nil},
		{"coordinator", "config-schema", "", func(w *world) { w.schema = false }, nil},
		{"coordinator", "config-applied", "", func(w *world) { w.applied = false }, nil},
		{"coordinator", "redis-acl", "", func(w *world) { w.aclOK = false }, nil},
		{"friend", "daemon-running", "", func(w *world) { w.daemon = false }, nil},
		{"friend", "harness-responsive", "", func(w *world) { w.broken = true }, nil},
		{"friend", "message-delivered", "", func(w *world) { w.delivered = false }, nil},
		{"friend", "session-receipt", "", func(w *world) { w.deaf("-") }, nil},
		// The receipt is nova-friend's verdict over --since, never a pong of any age: a pong
		// 72 hours old is outside the default 24h window, and one 11 minutes old is outside
		// a 10m window the doctor is given and passes on.
		{"friend", "session-receipt", "a pong 72 hours old that nova-friend calls deaf", func(w *world) { w.deaf("72h0m0s") }, nil},
		{"friend", "session-receipt", "a pong 11 minutes old over --since 10m", func(w *world) { w.deaf("11m0s") }, []string{"--since", "10m"}},
	}
	for _, tc := range cases {
		name := tc.job + "/" + tc.step
		if tc.why != "" {
			name += "/" + tc.why
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := healthyWorld(t)
			tc.breakIt(w)
			if tc.step == "binaries" && tc.job == "local-notes" {
				// nova-redis gone: the login step calls it first, so the first missing is
				// named there, by the same install line.
				tc.step = "redis-login"
			}
			args := append(slices.Clone(jobArgs[tc.job]), tc.extra...)
			code, lines := w.doctor(args...)
			assert.Equal(t, 2, code, lines)
			names := stepNames(tc.job)
			at := slices.Index(names, tc.step)
			require.GreaterOrEqual(t, at, 0, "%s is a step of %s", tc.step, tc.job)
			require.Len(t, lines, len(names)+1, "one line per step and the summary: %q", lines)
			for i, n := range names {
				want := map[bool]string{true: "ok", false: "blocked"}[i < at]
				if i == at {
					want = "fail"
				}
				assert.True(t, strings.HasPrefix(lines[i], "DOCTOR "+n+" "+want+" "), "step %d: want %s %s, got %q", i, n, want, lines[i])
			}
			summary := lines[len(lines)-1]
			require.Contains(t, summary, "first_missing="+tc.step+" ", summary)

			// The cold reader: the next command, and nothing else, from the output.
			_, next, ok := strings.Cut(summary, " next: ")
			require.True(t, ok, summary)
			doctorRuns, repairs := 1, 0
			for code != 0 {
				require.Less(t, repairs, 3, "the doctor did not converge: %q", lines)
				w.run(next)
				repairs++
				code, lines = w.doctor(args...)
				doctorRuns++
				_, next, _ = strings.Cut(lines[len(lines)-1], " next: ")
			}
			assert.Equal(t, 1, repairs, "one missing dependency is one repair")
			assert.Equal(t, 2, doctorRuns)
			assert.Contains(t, lines[len(lines)-1], "DOCTOR job="+tc.job+" ready ")
			t.Logf("calls: job=%s missing=%s doctor_runs=%d repairs=%d tool_calls=%d", tc.job, tc.step, doctorRuns, repairs, w.calls)
			assert.LessOrEqual(t, w.calls, 2*len(names), "each tool call is made once per run")
		})
	}

	t.Run("two missing are fixed in dependency order", func(t *testing.T) {
		t.Parallel()
		w := healthyWorld(t)
		w.redisUp, w.fnLoaded = false, false
		var order []string
		for range 3 {
			code, lines := w.doctor(jobArgs["worker"]...)
			if code == 0 {
				break
			}
			summary := lines[len(lines)-1]
			step := strings.Fields(strings.SplitN(summary, "first_missing=", 2)[1])[0]
			order = append(order, step)
			_, next, _ := strings.Cut(summary, " next: ")
			w.run(next)
		}
		assert.Equal(t, []string{"redis-reachable", "redis-functions"}, order)
	})
}

func stepNames(job string) []string {
	var out []string
	for _, s := range JobSteps(job) {
		out = append(out, s.Name)
	}
	return out
}

// The friend job reports the five facts apart: the daemon running, the harness
// responsive, a message delivered, the session's receipt and a card completed, each its
// own step and line; a daemon that runs says nothing of the session.
func TestDoctorFriendJobKeepsTheFiveFactsApart(t *testing.T) {
	t.Parallel()
	w := healthyWorld(t)
	code, lines := w.doctor(jobArgs["friend"]...)
	assert.Equal(t, 0, code, lines)
	got := map[string]string{}
	for _, l := range lines[:len(lines)-1] {
		f := strings.Fields(l)
		got[f[1]] = strings.Join(f[3:], " ")
	}
	assert.Contains(t, got["daemon-running"], "the daemon is running")
	assert.Contains(t, got["harness-responsive"], "harness takes turns")
	assert.Contains(t, got["message-delivered"], "3 messages delivered")
	assert.Contains(t, got["session-receipt"], "the session answered")
	assert.Contains(t, got["card-completion"], "newest=card-7")
	assert.Equal(t, 2, w.calls, "the five friend steps read one nova-friend check, and the Redis steps one acl check")

	t.Run("a running daemon with a silent session is not ready", func(t *testing.T) {
		t.Parallel()
		w := healthyWorld(t)
		w.deaf("-")
		code, lines := w.doctor(jobArgs["friend"]...)
		assert.Equal(t, 2, code)
		assert.Contains(t, strings.Join(lines, "\n"), "DOCTOR daemon-running ok ")
		assert.Contains(t, strings.Join(lines, "\n"), "DOCTOR session-receipt fail deaf: nova-friend verdict=deaf over 24h0m0s")
	})
	// The session's receipt is nova-friend's own verdict over the --since window
	// (docs/SPEC-FRIEND.md, "The verdicts"), never a pong of any age judged here: a pong
	// inside the window or a real message back is a receipt, and past both it is deaf,
	// with the delivery check as the fix.
	for _, tc := range []struct {
		name, pongAge string
		back          int
		extra         []string
		want          string
	}{
		{"a pong 11 minutes old inside the default 24h", "11m0s", 0, nil, "ok the session answered: nova-friend verdict=ok over 24h0m0s"},
		{"a pong 11 minutes old outside --since 10m", "11m0s", 0, []string{"--since", "10m"}, "fail deaf: nova-friend verdict=deaf over 10m0s"},
		{"a pong 72 hours old and nothing back", "72h0m0s", 0, nil, "fail deaf: nova-friend verdict=deaf over 24h0m0s"},
		{"a pong 72 hours old with messages back", "72h0m0s", 2, nil, "ok the session answered: nova-friend verdict=ok"},
		{"no pong ever and nothing back", "-", 0, nil, "fail deaf: nova-friend verdict=deaf"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := healthyWorld(t)
			w.pongAge, w.back = tc.pongAge, tc.back
			code, lines := w.doctor(append(slices.Clone(jobArgs["friend"]), tc.extra...)...)
			out := strings.Join(lines, "\n")
			assert.Contains(t, out, "DOCTOR session-receipt "+tc.want, out)
			if strings.HasPrefix(tc.want, "fail") {
				assert.Equal(t, 2, code, out)
				assert.Contains(t, out, "fix: nova-friend check --as bob --harness claude --dir /home/bob --redis "+testRedis+" --to ada", out)
			} else {
				assert.Equal(t, 0, code, out)
			}
		})
	}
	t.Run("no card completed yet is said, and is not a failure", func(t *testing.T) {
		t.Parallel()
		w := healthyWorld(t)
		w.outbox = 0
		code, lines := w.doctor(jobArgs["friend"]...)
		assert.Equal(t, 0, code)
		assert.Contains(t, strings.Join(lines, "\n"), "DOCTOR card-completion ok no card completed yet")
	})
}

// Every job's steps are in dependency order and begin at connectivity; --json is the
// same report, bounded; a name that is no job, or a job flag without --job, is refused.
func TestDoctorJobShapeAndRefusals(t *testing.T) {
	t.Parallel()
	for _, j := range Jobs {
		s := JobSteps(j)
		require.NotEmpty(t, s, j)
		assert.Equal(t, Connectivity, s[0].Stage, j)
		assert.True(t, slices.IsSortedFunc(s, func(a, b Step) int { return int(a.Stage) - int(b.Stage) }), j)
	}
	for n, s := range steps {
		assert.Equal(t, n, s.Name)
		assert.NotEmpty(t, s.Stage.String(), n)
	}

	w := healthyWorld(t)
	w.redisUp = false
	var out, errb bytes.Buffer
	code := Main(NewRegistry(), w.env(), "", append(jobArgs["coordinator"], "--json"), strings.NewReader(""), &out, &errb)
	assert.Equal(t, 2, code)
	assert.Less(t, out.Len(), 4096, "the JSON is bounded")
	var rep JobReport
	require.NoError(t, json.Unmarshal(out.Bytes(), &rep), out.String())
	assert.Equal(t, "redis-reachable", rep.FirstMissing)
	assert.Equal(t, "nova-redis serve --bind 127.0.0.1 --port 6390 --dir "+testHome+"/nova/stores/redis", rep.Next)
	assert.False(t, rep.Ready)
	assert.Len(t, rep.Steps, len(JobSteps("coordinator")))
	assert.Equal(t, Blocked, rep.Steps[1].Status)

	for _, args := range [][]string{{"--job", "nope"}, {"--as", "bob"}, {"--since", "1h"}, {"--job", "friend", "--local"}} {
		out.Reset()
		errb.Reset()
		code := Main(NewRegistry(), w.env(), "", args, strings.NewReader(""), &out, &errb)
		assert.Equal(t, 2, code, args)
		assert.Empty(t, out.String(), args)
		assert.NotEmpty(t, errb.String(), args)
	}
}

// A down Redis named localhost is still loopback, but nova-redis serve refuses a
// bind that is not an IP (cmd/nova-redis/serve.go, validBinds). The fix binds
// 127.0.0.1. An address that is already a loopback IP is bound as that IP, so
// ::1 stays ::1. The dial is the fake's: nothing listens on 6390.
func TestDoctorServeFixBindsAnIPNotLocalhost(t *testing.T) {
	t.Parallel()
	w := healthyWorld(t)
	w.redisUp = false
	for _, tc := range []struct{ addr, bind string }{
		{"localhost:6390", "127.0.0.1"},
		{"[::1]:6390", "::1"},
	} {
		t.Run(tc.addr, func(t *testing.T) {
			code, lines := w.doctor("--job", "local-notes", "--redis", tc.addr)
			assert.Equal(t, 2, code, lines)
			want := "nova-redis serve --bind " + tc.bind + " --port 6390 --dir " + testHome + "/nova/stores/redis"
			require.Contains(t, lines[len(lines)-1], "next: "+want, lines)
			_, refused := parseArgv(want)
			assert.Empty(t, refused, want)
		})
	}
}

func TestRemedyAndClip(t *testing.T) {
	t.Parallel()
	for said, want := range map[string]string{
		`X FAILED remedy="nova-redis acl apply --addr a:1"`:                "nova-redis acl apply --addr a:1",
		"nova-config status REFUSED: no schema; run: nova-config migrate":  "nova-config migrate",
		"nova-config status REFUSED: bad flag; run: nova-config status -h": "def",
		"something else":  "def",
		"remedy=rm -rf /": "def",
	} {
		assert.Equal(t, want, remedy(said, "def"), said)
	}
	long := strings.Repeat("é", 300)
	c := clip(long, maxEvidence)
	assert.LessOrEqual(t, len(c), maxEvidence)
	assert.True(t, strings.HasSuffix(c, "..."))
	assert.Equal(t, "short", clip("short", 10))
	assert.Equal(t, fmt.Sprint(Blocked), "blocked")
}
