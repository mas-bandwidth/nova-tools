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

const testRedis = "127.0.0.1:6390"

// testNow is the fake clock every world reads: no test reads real time.
var testNow = time.Date(2026, 10, 6, 8, 10, 0, 0, time.UTC)

// world is a machine as the job steps see it through the tools: each field one
// dependency, true when it is there. The fake tools answer from it, and the fake shell
// (world.run) changes it only by the exact fix commands nova-doctor prints. The session's
// evidence is nova-friend check's: the age of the last session pong ever recorded ("-"
// none), the age of the newest card finished, and the messages back.
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
		env:   map[string]string{"PATH": "bin", "NOVA_REDIS_ADDR": testRedis, "NOVA_SPRINT_ACTOR": "ada"},
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
	switch cmd {
	case "nova-redis acl check --addr " + testRedis:
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
	case "nova-redis fn check --addr " + testRedis:
		if !w.fnLoaded {
			return "", exitErr{1, "FN CHECK MISSING library=nova"}
		}
		return "FN CHECK OK library=nova\n", nil
	case "nova-config login --check":
		if !w.storeLogin {
			return "", exitErr{2, "nova-config login REFUSED: no login is recorded at /x; run: nova-config login --store <dir> --as <seat> --key <file> --secret <NAME> --dsn <dsn> --friend <actor>"}
		}
		return "LOGIN file=/x dsn=postgres://h/db resolves=yes\n", nil
	case "nova-config status":
		if !w.schema {
			return "CONFIG STATUS pg=h schema=0 redis=-\n", exitErr{1, "nova-config status REFUSED: schema config is not there yet; run: nova-config migrate"}
		}
		return "CONFIG STATUS pg=h schema=3\n", nil
	case "nova-config apply --check":
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
	case "nova-friend check bob --json", "nova-friend check --as ada --json":
		b, err := json.Marshal(w.friendReport())
		require.NoError(w.t, err)
		if w.friendReport().Friends[0].Verdict.Verdict != "ok" {
			return string(b), exitErr{1, ""}
		}
		return string(b), nil
	}
	w.t.Fatalf("the doctor called a tool the fake does not know: %s", cmd)
	return "", nil
}

func (w *world) friendReport() friend.CheckReport {
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
	// The verdict as nova-friend decides it, over its default 24h window: a pong of
	// any age inside it, or a message back, is "came back".
	pong, err := time.ParseDuration(w.pongAge)
	verdict := "ok"
	switch {
	case !w.daemon || w.broken:
		verdict = "down"
	case w.back == 0 && (err != nil || pong > 24*time.Hour):
		verdict = "deaf"
	}
	f.Verdict = friend.VerdictFacts{Friend: "bob", Verdict: verdict, Shown: "-", Why: verdict}
	s := friend.CheckSummary{Friends: 1}
	if verdict == "ok" {
		s.OK = 1
	} else {
		s.Down = 1
	}
	return friend.CheckReport{Friends: []friend.FriendCheck{f}, Summary: s}
}

// installLine is a nova tool's install, from the module (or a checkout) the doctor was built from.
var installLine = regexp.MustCompile(`^go install (?:\S+/cmd/(nova-[a-z]+)@latest|\./cmd/(nova-[a-z]+) \(in a checkout of the nova-tools repository\))$`)

// run is the cold reader's shell: it runs a command exactly as printed, and knows only
// the commands a machine's owner has. Anything else is a fix a stranger could not run.
func (w *world) run(cmd string) {
	install := installLine.FindStringSubmatch(cmd)
	switch {
	case cmd == "nova-redis serve --bind 127.0.0.1 --port 6390 --dir ~/nova/stores/redis":
		w.redisUp = true
	case cmd == "nova-redis fn load --addr "+testRedis:
		w.fnLoaded = true
	case cmd == "nova-redis acl apply --addr "+testRedis:
		w.aclOK = true
	case cmd == "nova-config migrate":
		w.schema = true
	case cmd == "nova-config apply":
		w.applied = true
	case install != nil:
		w.install(install[1] + install[2])
	case cmd == "nova-friend install --as bob --harness claude --dir /home/bob":
		w.daemon, w.broken = true, false
	case cmd == "nova-friend ping --as ada --to bob":
		w.delivered = true
	case cmd == "nova-friend check --as bob --harness claude --dir /home/bob": // the delivery check: --as is the friend itself
		w.pongAge = "5s"
	default:
		w.t.Fatalf("the fix is not a command the cold reader can run as printed: %q", cmd)
	}
}

// doctor runs nova-doctor as a stranger would and returns the exit and the lines.
func (w *world) doctor(args ...string) (int, []string) {
	var out, errb bytes.Buffer
	code := Main(NewRegistry(), w.env(), "", args, strings.NewReader(""), &out, &errb)
	require.Empty(w.t, errb.String())
	return code, strings.Split(strings.TrimSpace(out.String()), "\n")
}

// jobArgs are the flags each job runs with in these tests.
var jobArgs = map[string][]string{
	"local-notes": {"--job", "local-notes"},
	"messaging":   {"--job", "messaging"},
	"friend":      {"--job", "friend", "--as", "bob", "--dir", "/home/bob"},
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
	}{
		{"local-notes", "redis-reachable", "", func(w *world) { w.redisUp = false }},
		{"local-notes", "binaries", "", func(w *world) { w.uninstall("nova-redis"); w.redisUp = true }},
		{"messaging", "binaries", "", func(w *world) { w.uninstall("nova-bus") }},
		{"worker", "redis-functions", "", func(w *world) { w.fnLoaded = false }},
		{"coordinator", "config-schema", "", func(w *world) { w.schema = false }},
		{"coordinator", "config-applied", "", func(w *world) { w.applied = false }},
		{"coordinator", "redis-acl", "", func(w *world) { w.aclOK = false }},
		{"friend", "daemon-running", "", func(w *world) { w.daemon = false }},
		{"friend", "harness-responsive", "", func(w *world) { w.broken = true }},
		{"friend", "message-delivered", "", func(w *world) { w.delivered = false }},
		{"friend", "session-receipt", "", func(w *world) { w.deaf("-") }},
		// nova-friend check reports the age of the last pong ever recorded: one 11 minutes
		// old is past the session pong window, and with no card finished within its window
		// the session is deaf, whatever else is on record.
		{"friend", "session-receipt", "the only pong 11 minutes old", func(w *world) { w.deaf("11m0s") }},
		{"friend", "session-receipt", "a pong 72 hours old that nova-friend calls deaf", func(w *world) { w.deaf("72h0m0s") }},
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
			code, lines := w.doctor(jobArgs[tc.job]...)
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
				code, lines = w.doctor(jobArgs[tc.job]...)
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
		assert.Contains(t, strings.Join(lines, "\n"), "DOCTOR session-receipt fail deaf: no session pong within 10m0s, no card finished within 30m0s (last 2h0m0s ago)")
	})
	// The session's evidence is nova-friend status's (docs/SPEC-FRIEND.md, "Presence is her
	// session's evidence"): a session pong under ten minutes old or a card finished under
	// thirty, at the fake clock. A pong past its window is deaf with its age; messages back
	// are shown and decide nothing.
	for _, tc := range []struct {
		name, pongAge string
		finishAge     time.Duration
		back          int
		want          string
	}{
		{"a pong 9m59s old is a receipt", "9m59s", 2 * time.Hour, 0, "ok the session answered: session pong 9m59s ago; messages_back=0"},
		{"the only pong 11 minutes old is deaf", "11m0s", 2 * time.Hour, 0,
			"fail deaf: no session pong within 10m0s (last 11m0s ago), no card finished within 30m0s (last 2h0m0s ago); messages_back=0"},
		{"a pong 72 hours old is deaf though messages came back", "72h0m0s", 2 * time.Hour, 2,
			"fail deaf: no session pong within 10m0s (last 72h0m0s ago)"},
		{"a card finished 29 minutes ago is a receipt", "72h0m0s", 29 * time.Minute, 0, "ok the session answered: finished card-7 29m0s ago"},
		{"a card finished 31 minutes ago is not", "-", 31 * time.Minute, 0,
			"fail deaf: no session pong within 10m0s, no card finished within 30m0s (last 31m0s ago)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := healthyWorld(t)
			w.pongAge, w.finishAge, w.back = tc.pongAge, tc.finishAge, tc.back
			code, lines := w.doctor(jobArgs["friend"]...)
			out := strings.Join(lines, "\n")
			assert.Contains(t, out, "DOCTOR session-receipt "+tc.want, out)
			if strings.HasPrefix(tc.want, "fail") {
				assert.Equal(t, 2, code, out)
				assert.Contains(t, out, "fix: nova-friend check --as bob --harness claude --dir /home/bob", out)
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
	assert.Equal(t, "nova-redis serve --bind 127.0.0.1 --port 6390 --dir ~/nova/stores/redis", rep.Next)
	assert.False(t, rep.Ready)
	assert.Len(t, rep.Steps, len(JobSteps("coordinator")))
	assert.Equal(t, Blocked, rep.Steps[1].Status)

	for _, args := range [][]string{{"--job", "nope"}, {"--as", "bob"}, {"--job", "friend", "--local"}} {
		out.Reset()
		errb.Reset()
		code := Main(NewRegistry(), w.env(), "", args, strings.NewReader(""), &out, &errb)
		assert.Equal(t, 2, code, args)
		assert.Empty(t, out.String(), args)
		assert.NotEmpty(t, errb.String(), args)
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
