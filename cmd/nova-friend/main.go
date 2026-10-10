// nova-friend is what a friend runs to be part of the team (docs/SPEC-FRIEND.md;
// the model is tla/Friend.tla): one launchd agent per friend that parks on the
// friend's nova-bus stream and pushes each message into the running session
// as a turn, beats to the sprint server while it does, answers the
// coordinator's pings at once and pushes them in so the session answers as
// its own turn, and tells the session when the coordinator goes silent; and,
// on the coordinator's side, the ping loop that pings every friend each
// second. The verbs are run, beat, install, uninstall, check, status, pong,
// ping, wait-pong, watch, host, serve and reach; the
// dispatch, the banner, the help, the refusals and the output envelope are
// internal/tool's, and the rules are internal/friend's.
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/redisauth"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

var version string

// The environment: the bus store (nova-bus's variable) and the sprint
// server (nova-sprint's), with the server's default beside it.
const (
	RedisEnv      = "NOVA_BUS_REDIS"
	ServerEnv     = "NOVA_SPRINT_SERVER"
	DefaultServer = "127.0.0.1:6390"
)

// WaitPongEvery is how often wait-pong reads the log.
const WaitPongEvery = time.Second

// OpenRetryMax caps the wait between two tries of the daemon to open a store
// that does not answer.
const OpenRetryMax = 30 * time.Second

// world is what the tool reaches outside itself; main passes the real one,
// a test its own over internal/bus's Fake, a fake harness and its own
// clock, so no test opens a socket or reads the real time.
type world struct {
	getenv         func(string) string
	open           func(ctx context.Context, addr string) (bus.Store, func(), error)
	exec           friend.Exec
	wall           func(wl friend.Wall, run friend.Exec) friend.Exec                                                                      // a lane's child inside its wall; the real world's is Wall.Exec, nil walls nothing (a test's fake harness)
	beat           func(ctx context.Context, server, friend string, active time.Time, proof friend.BeatWords) (answer string, err error)  // the FRIEND-BEAT line, which carries the friend's row
	beatDown       func(ctx context.Context, server, friend string, active, until time.Time, reason string, proof friend.BeatWords) error // her beat while she is down (friend beat --until --reason); nil holds the beat back
	progress       func(ctx context.Context, server string, argv []string) error                                                          // one progress verb to the sprint server (friend.ProgressArgv)
	finish         func(ctx context.Context, server string, argv []string) error                                                          // one finish verb to the sprint server (friend.FinishArgv: a lane's card whose run ended with no report)
	down           func(ctx context.Context, server string, argv []string) error                                                          // one down verb to the sprint server (DownArgv: a credit refusal's friend down --reason --until); nil names none (a test's)
	sqlite         friend.Exec                                                                                                            // reads opencode's database (the sqlite3 CLI); nil reads none: no card cost, no token cap
	cards          func(ctx context.Context, server string, argv []string) (string, error)                                                // the cards on her row, asked of the sprint server (friend.FriendCardsArgv); nil asks none
	friends        func(ctx context.Context, server string) (rows []friend.WakeRow, seat string, err error)                               // the friends table and the seat's holder, from the sprint server's coordinator view (GET /api/view/coordinator?all=1)
	holders        func(ctx context.Context, server string) (map[string]string, error)                                                    // current card holders from GET /api/view/cards; nil in a world that reads none
	view           func(ctx context.Context, server, friend string) (string, error)                                                       // the sprint server's worker view of her (GET /api/view/worker), while friend cards is refused; nil reads none
	stage          func(dir string) *friend.Stager                                                                                        // stages a held card's job under her working directory and prunes the finished ones (friend.Stager, with the daemon's git credentials); nil stages none (a test's)
	tip            func(ctx context.Context, repo, branch string) (string, error)                                                         // origin's tip of a card's branch (friend.Stager.Tip, one git ls-remote): a report's LAND finishes only there; nil reads none (a test's)
	launchctl      friend.Launchctl
	now            func() time.Time
	sleep          func(ctx context.Context, d time.Duration)
	signals        func(ctx context.Context) (context.Context, context.CancelFunc)
	uid            int
	home           string
	binary         func() (string, error)
	copy           friend.CopyFile              // places a removable-volume binary under home; nil refuses it
	lookPath       func(string) (string, error) // a program on PATH by absolute path, for the agent's secrets wrap
	random         func() string
	alive          friend.Aliver                       // the harness check, when set (a test's fake harness); nil watches the adapter
	launch         []string                            // host: the launch command after "--"
	settings       friend.SettingsFS                   // where a harness's own settings are read and written (install, check --settings)
	argv           []string                            // this run's arguments after the program's name: what the plist drift is read against
	wake           *wakeFS                             // watch: the wake file's reads; nil reads the disk
	stepBeat       bool                                // deterministic fake clock in CLI tests; never set by realWorld
	checkGo        func(func())                        // optional test scheduler for a fake-clock session check
	reachPermitted func(context.Context) (bool, error) // reach window permission; nil checks the platform without prompting
}

// readPlist is the installed plist at path, empty when there is none or it
// cannot be read: no plist is no drift.
func (w world) readPlist(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "" // ignored: a daemon run by hand, or never installed, has no plist to differ from
	}
	return string(b)
}

// sprintVerb sends one worker verb (progress, finish) to the sprint server and answers its
// refusal as an error.
func sprintVerb(ctx context.Context, server string, argv []string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, err := sprintwire.Client{Addr: server}.Do(ctx, argv)
	if err != nil {
		return err
	}
	if len(res) != 1 {
		return fmt.Errorf("%s: the server answered %d results, want 1", argv[0], len(res))
	}
	if res[0].Code != 0 {
		return fmt.Errorf("%s refused: %s", argv[0], strings.TrimSpace(res[0].Stderr))
	}
	return nil
}

// sprintAsk sends one worker verb to the sprint server and answers what it printed, or its
// refusal as an error.
func sprintAsk(ctx context.Context, server string, argv []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, err := sprintwire.Client{Addr: server}.Do(ctx, argv)
	if err != nil {
		return "", err
	}
	if len(res) != 1 {
		return "", fmt.Errorf("%s: the server answered %d results, want 1", strings.Join(argv[:min(2, len(argv))], " "), len(res))
	}
	if res[0].Code != 0 {
		return "", &friend.Refused{Why: fmt.Sprintf("%s refused: %s", strings.Join(argv[:min(2, len(argv))], " "), strings.TrimSpace(res[0].Stderr))}
	}
	return res[0].Stdout, nil
}

// sprintBeat sends one friend beat to the sprint server and answers its FRIEND-BEAT line,
// or its refusal as an error.
func sprintBeat(ctx context.Context, server string, argv []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, err := sprintwire.Client{Addr: server}.Do(ctx, argv)
	if err != nil {
		return "", err
	}
	if len(res) != 1 {
		return "", fmt.Errorf("friend beat: the server answered %d results, want 1", len(res))
	}
	if res[0].Code != 0 {
		return "", fmt.Errorf("friend beat refused: %s", strings.TrimSpace(res[0].Stderr))
	}
	return res[0].Stdout, nil
}

// proofArgs are a beat's proof words as flags: the check her daemon asked and the
// check her session answered, each with the daemon's run (friend.BeatWords;
// sprint.ProveBeat on the server); none when there is nothing to say.
func proofArgs(w friend.BeatWords) []string {
	var args []string
	if w.Check != "" {
		args = append(args, "--check", w.Check)
	}
	if w.Pong != "" {
		args = append(args, "--pong", w.Pong)
	}
	if len(args) > 0 && w.Run != "" {
		args = append(args, "--run", w.Run)
	}
	if w.StopReturns > 0 {
		args = append(args, "--stop-returns", strconv.Itoa(w.StopReturns))
	}
	return args
}

// maxView bounds the worker view the daemon reads: her cards and her results not landed.
const maxView = 4 << 20

// sprintView reads the sprint server's worker view of a friend (nova-sprint serve, GET
// /api/view/worker?as=<friend>), the JSON document whole.
func sprintView(ctx context.Context, server, name string) (string, error) {
	return readSprintView(ctx, server, "/api/view/worker?as="+url.QueryEscape(name))
}

// readSprintView is the bounded GET shared by the worker and holder views.
func readSprintView(ctx context.Context, server, route string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+server+route, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("the sprint server at %s did not answer: %w", server, err)
	}
	defer resp.Body.Close() // ignored: a read-only body
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxView+1))
	switch {
	case err != nil:
		return "", fmt.Errorf("the sprint server at %s: its view was cut: %w", server, err)
	case len(raw) > maxView:
		return "", fmt.Errorf("the sprint server at %s: its view is over %d bytes", server, maxView)
	case resp.StatusCode != http.StatusOK:
		return "", fmt.Errorf("the sprint server at %s refused the view (%s): %s", server, resp.Status, oneline.Cap(strings.TrimSpace(string(raw)), 300))
	}
	return string(raw), nil
}

// sprintHolders reads current ownership from the existing cards view, without
// asking another friend to run or answer anything.
func sprintHolders(ctx context.Context, server string) (map[string]string, error) {
	raw, err := readSprintView(ctx, server, "/api/view/cards")
	if err != nil {
		return nil, err
	}
	return parseHolders(raw)
}

func realWorld() world {
	w := world{getenv: os.Getenv, exec: friend.RealExec, sqlite: friend.RealExec, wall: friend.Wall.Exec, now: time.Now, uid: os.Getuid(), home: os.Getenv("HOME"),
		sleep: func(ctx context.Context, d time.Duration) {
			select {
			case <-ctx.Done():
			case <-time.After(d):
			}
		},
		signals: func(ctx context.Context) (context.Context, context.CancelFunc) {
			return signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		},
		launchctl: func(ctx context.Context, args ...string) (string, error) {
			cmd, cancel := subproc.Command(ctx, subproc.Tool, "launchctl", args...)
			defer cancel()
			out, err := cmd.CombinedOutput()
			return string(out), err
		},
		beat: func(ctx context.Context, server, name string, active time.Time, proof friend.BeatWords) (string, error) {
			args := []string{"friend", "beat", name}
			if !active.IsZero() {
				args = append(args, "--active", active.UTC().Format(time.RFC3339))
			}
			args = append(args, "--daemon-version", buildinfo.Version(version))
			return sprintBeat(ctx, server, append(args, proofArgs(proof)...))
		},
		beatDown: func(ctx context.Context, server, name string, active, until time.Time, reason string, proof friend.BeatWords) error {
			args := []string{"friend", "beat", name, "--until", until.UTC().Format(time.RFC3339), "--reason", reason}
			if !active.IsZero() {
				args = append(args, "--active", active.UTC().Format(time.RFC3339))
			}
			args = append(args, "--daemon-version", buildinfo.Version(version))
			_, err := sprintBeat(ctx, server, append(args, proofArgs(proof)...))
			return err
		},
		progress: sprintVerb,
		finish:   sprintVerb,
		down:     sprintVerb,
		cards:    sprintAsk,
		view:     sprintView,
		friends:  coordinatorFriends,
		holders:  sprintHolders,
		stage:    func(dir string) *friend.Stager { return &friend.Stager{Dir: dir} },
		tip:      (&friend.Stager{}).Tip,
		lookPath: exec.LookPath,
		copy:     friend.CopyExecutable,
		settings: friend.OSFS{},
		binary: func() (string, error) {
			p, err := os.Executable()
			if err != nil {
				return "", err
			}
			return filepath.EvalSymlinks(p)
		},
		random: func() string {
			const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
			var raw [6]byte
			if _, err := rand.Read(raw[:]); err != nil {
				return fmt.Sprint(time.Now().UnixNano())[9:15] // ignored: no random bytes; the clock's low digits serve a nonce
			}
			for i := range raw {
				raw[i] = alphabet[int(raw[i])%len(alphabet)]
			}
			return string(raw[:])
		},
	}
	w.open = w.openRedis
	return w
}

func main() {
	args := os.Args[1:]
	if name, ok := friend.GoShimName(os.Args[0]); ok {
		// run as go or gofmt through a lane's shim directory (friend.GoShims): the refuse-go verb says no
		args = []string{"refuse-go", "--name", name}
	}
	os.Exit(run(args, os.Stdin, os.Stdout, os.Stderr, realWorld()))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, w world) int {
	if len(args) > 0 && args[0] == "hook" {
		if len(args) > 1 && (args[1] == "-h" || args[1] == "--help") {
			return friendTool(w).Run(args, stdin, stdout, stderr)
		}
		return runClaudeHook(args[1:], stdin, stdout, stderr)
	}
	if len(args) > 0 && args[0] == friend.WallVerb {
		// the lane's wall around one command: its argv follows "--", which the verb table
		// does not carry, so it is dispatched here (internal/friend RunWall)
		return friend.RunWall(args[1:], os.Environ(), stdin, stdout, stderr)
	}
	w.argv = args
	if len(args) > 0 && args[0] == "host" {
		// the launch command follows "--", which the verb table does not carry (only the default
		// verb takes operands): it is split off here and the verb reads it from the world
		if i := slices.Index(args, "--"); i >= 0 {
			w.launch, args = args[i+1:], args[:i]
		}
	}
	return friendTool(w).Run(args, stdin, stdout, stderr)
}

// openRedis dials the bus store the way nova-bus does (internal/redisconn,
// the fleet's login from the environment, the password never on the line).
func (w world) openRedis(ctx context.Context, addr string) (bus.Store, func(), error) {
	o := redisconn.Options{Addr: addr, Env: redisconn.Env{User: redisauth.UserEnv}}
	if w.getenv(redisauth.UserEnv) != "" {
		o.Env.PasswordEnv = redisauth.PasswordEnvEnv
		if w.getenv(redisauth.PasswordEnvEnv) == "" {
			o.PasswordEnv = redisauth.DefaultPasswordEnv
		}
	}
	conn, err := redisconn.Open(ctx, o, w.getenv)
	if err != nil {
		return nil, nil, err
	}
	return bus.Redis{C: conn.Client()}, func() { conn.Close() }, nil // ignored: closing the store connection at exit, nothing is left to report it to
}

// stateDir is where a reader finds the state files of the friend --as names:
// --state-dir, else under dir when her daemon writes there, else the home
// directory's (friend.FindStateDir). dir is the verb's --dir, empty for a verb
// with none.
func (w world) stateDir(c *tool.Call, dir string) string {
	if s := c.Str("state-dir"); s != "" {
		return s
	}
	return friend.FindStateDir(w.home, dir, c.Str("as"))
}

// daemonStateDir is where the daemon keeps its files: --state-dir, else
// <dir>/.nova-friend, made here, so the session's pong lands inside the
// directory it may write; when that directory refuses it, the home
// directory's, and why says the refusal (friend.DaemonStateDir).
func (w world) daemonStateDir(c *tool.Call) (state, why string) {
	if s := c.Str("state-dir"); s != "" {
		return s, ""
	}
	return friend.DaemonStateDir(w.home, c.Str("dir"), c.Str("as"), func(d string) error { return os.MkdirAll(d, 0o755) })
}

func (w world) server() string {
	if s := w.getenv(ServerEnv); s != "" {
		return s
	}
	return DefaultServer
}

func friendTool(w world) *tool.Tool {
	redis := func(f *tool.Flags) {
		f.String("redis", w.getenv(RedisEnv), "the bus store's Redis address, host:port (default: "+RedisEnv+")")
	}
	stateDir := func(f *tool.Flags) {
		f.String("state-dir", "", "where the state files live (default: <dir>/.nova-friend where the daemon wrote there, else ~/.nova-friend/<me>)")
	}
	settingFlags := func(f *tool.Flags) {
		f.String("config-dir", w.getenv("CLAUDE_CONFIG_DIR"), "harness claude: the friend's own config directory, made and named in the agent (default: CLAUDE_CONFIG_DIR)")
		f.String("model", "", "harness opencode: the model, provider/model, written into <dir>/opencode.json (default: left as it is)")
	}
	daemonFlags := func(f *tool.Flags) {
		f.Required("as", "your name, a nova-config friend row")
		f.Required("harness", "the harness the session runs in: "+strings.Join(friend.Harnesses, ", "))
		f.Required("dir", "the friend's working directory: the session's, and where the state files live")
		f.String("session", "", "the session to deliver into (default: the harness's newest session in --dir; harness tmux: the tmux session, default: the one host saved, else friend-<me>)")
		f.String("adapter", "", "delivery route: folder for an existing watched Codex session (default: the harness adapter)")
		f.String("delivery-dir", "", "existing folder watched by that Codex session when --adapter folder")
		f.String("server", w.server(), "the sprint server, host:port (default: "+ServerEnv+", else "+DefaultServer+")")
		f.Int("width", 0, "the friend's width, from the nova-config friend row; 0 is unknown")
		f.Duration("silent-stop", friend.DefaultSilentStop, "stop a turn that has printed nothing for this long; a turn that prints runs on")
		f.Int("broken-after", friend.DefaultBrokenAfter, "turns in a row the provider refuses the same way before the session is broken")
		f.Duration("limit-rest", friend.DefaultLimitWait, "how long the friend is down when its harness's usage limit or empty balance names no reset")
		f.Bool("notifications-only", false, "deliver filtered notifications through one receiver; no sprint beats, proof, claims, jobs, staging, pruning or finishes")
		f.String("notify-kinds", "request,blocker,report", "message kinds that wake the model, comma-separated; requests/blockers always retained; ack/status are audited by default")
		f.Duration("notify-window", friend.NotificationWindow, "global card-delivery burst window and minimum wake interval; urgent messages bypass it")
		f.String("coordinator", "", "who is told of a broken session when no ping has named the seat")
		f.Check(func(c *tool.Call) {
			if err := friend.CheckFolderRoute(c.Str("harness"), c.Str("session"), c.Str("adapter"), c.Str("delivery-dir")); err != nil {
				c.Problem(err.Error())
			}
			if c.Bool("notifications-only") && c.Str("adapter") != "" {
				c.Problem("--notifications-only uses the Codex app queue; omit --adapter folder for this separate receiver")
			}
			if c.Bool("notifications-only") && c.Str("harness") != "codex" {
				c.Problem("--notifications-only wants --harness codex: queued notifications into the existing app")
			}
			if why := bus.CheckKinds(commaList(c.Str("notify-kinds"))...); why != "" {
				c.Problem(why)
			}
			if c.Dur("notify-window") <= 0 {
				c.Problem("--notify-window wants a positive duration")
			}
		})
		stateDir(f)
		redis(f)
		f.Check(func(c *tool.Call) {
			if h := c.Str("harness"); h != "" && !friend.Known(h) {
				c.Problem(fmt.Sprintf("--harness %q is no harness; it wants one of %s", h, strings.Join(friend.Harnesses, ", ")))
			}
		})
	}
	return &tool.Tool{
		Name:    "nova-friend",
		Default: "check",
		What:    "what a friend runs to be part of the team: the wake loop, the beat, and the proof of life, as one daemon",
		Stamp:   version,
		How: `one launchd agent per friend (install) runs the daemon (run): it parks on the friend's
nova-bus stream and, when the session is free, pushes every waiting message in as one turn (the
harness's deliver command), beats to the sprint server while the session answers, answers the
coordinator PING at once (daemon-pong); presence is the session's word on the bus, never a process.
state: <dir>/.nova-friend/ (--state-dir moves it), the queue: <dir>/inbox/QUEUE.json.`,
		ExitTable: "0 done, 1 the verb ran and said no (wait-pong: no pong in time; status: no daemon; check: the session did not answer), 2 could not run (a flag, an input, a store or a server that did not answer).",
		Words:     []string{"NONE", "FAIL", "DRIFT", "DRY-RUN"},
		Verbs: []tool.Verb{
			{
				Name:   "hook",
				Usage:  "hook --harness claude",
				Effect: tool.Inspection + ": reads one PreToolUse JSON event from stdin and prints only Claude hook protocol JSON",
				Detail: "An opt-in project hook for interactive Claude Code sessions (docs/CLAUDE-ASYNC-BASH-CANDIDATE.md). No settings are installed by this verb. Bash input is changed to run_in_background=true without approving the command; malformed input is denied.",
				Flags: func(f *tool.Flags) {
					f.Required("harness", "claude: the harness whose PreToolUse JSON is on stdin")
				},
				Run: func(*tool.Call) *tool.Out { return tool.Refuse("hook requires the raw PreToolUse entrypoint") },
			},
			{
				Name:    "run",
				Usage:   "run --as <me> --harness <h> --dir <d> [--session <id>] [--adapter folder --delivery-dir <watched-dir>] [--server <addr>] [--width <n>] [--silent-stop <d>] [--broken-after <n>] [--coordinator <seat>] [--state-dir <d>] [--redis <addr>] [--profile <p>] [--config-dir <d>] [--deny-self <d,...>] [--wall-jobs <d,...>] [--wall-reads <d,...>] [--model <provider/model>] [--db <opencode.db>] [--lane-tiers <t,...>] [--lane-streams <p,...>] [--token-cap <n>] [--load-max <n>] [--load-width <n>] [--pause-on funds|any] [--refuse-go] [--dry-run]",
				Example: "", // a daemon: the example block has no line that runs for ever
				Effect:  tool.Delivery + ": the daemon; messages go into the session, beats and pongs go out, until a signal",
				DryRun:  true,
				Detail: `The loop launchd runs (install writes it). A harness with no deliver command (the surveyed ones) is
refused at once, exit 2, the adapter card its remedy. Otherwise the daemon starts with its push unproven
(status push=unproven, check proof=pending): its first SESSION CHECK goes in through the harness at once,
and it delivers nothing into the session until the session answers it (or writes on the bus), then turns
live without a restart; unanswered within ` + friend.SessionBound.String() + `, one "push proof: unproven" line names the check's nonce, and
the check is asked again with the same nonce, every ` + friend.SessionQuiet.String() + ` once the session has read the last, else after
` + friend.ReaskAfter.String() + ` (a check the last run queued and never saw answered keeps its nonce). A session the adapter
cannot drive is one "presence: REFUSED" line with the remedy (a dsh session under an agent preset: start a
session in <dir> with no agent preset and name it with --session <id>). The beat after a check goes in
says it (--check <nonce> --run <run>), and the beat after the session answers names it (--pong <nonce>
--run <run>): the sprint server counts only an answer to a check this run asked, once, as her session's
evidence while her beats go on, so a check goes in every ` + friend.ProveEvery.String() + ` while she is up; a per-card harness
(claude) says neither. While the session is down the beat says so (--until, --reason: the push unproven,
or no session answer, with the check's nonce). The present comes first, the backlog never does: on the
daemon's start (once the first beat says her row's mode), after ` + friend.StaleAfter.String() + ` with no turn taken, and when she
sends herself a message with the subject present, the next turn is one PRESENT turn (her live queue from her
row or inbox/QUEUE.json, each card's column and BRIEF.md, the seat, the newest coordinator note, and one line
Skipped: n deals, n pings, n notes); every older message is acked "superseded by the present at <time>", said
on the record and to the seat on the bus, and a PING past the ` + friend.Window.String() + ` challenge window is dropped, never
answered. A report on a card no longer on her row is never finished; the record names who holds it now.
Each second, when the session is free: every waiting
message is read off the stream and pushed in as ONE turn, one envelope, oldest first, each message under a line
[i/n] <id> from=<f> at=<RFC3339> age=<m>m subject=<s>, capped at the harness's text limit
(` + fmt.Sprint(friend.BatchBytes) + ` bytes unless it names its own; the first message always goes in) with the rest named under
and <n> more: nova-bus recv --as <me> --all; exit 0 acks every message the envelope carried, a failure acks none. A turn that fails leaves them pending, handed in
again when their claims open, and the third failure acks a message, given_up=true on the record. A
PING is answered at once with a daemon-pong and acked, never a turn; while a challenge is open the
pong line rides at the head of the next turn. No ping for ` + friend.Window.String() + `: "coordinator silent", and
"coordinator back" when pings resume, collapsed to the latest and said only inside a turn that
carries messages. Presence is the session's, never the daemon's: ` + friend.ProveEvery.String() + ` after the last check went in
(the session's own bus messages keep her up meanwhile; the daemon's never count), a SESSION CHECK <nonce> goes in through the harness
as a turn of its own, once no turn is under way (on the friend's own stream for a harness with no
deliver command), and only the session's pong carrying that nonce answers it; none within ` + friend.SessionBound.String() + `
and the friend is down, "no session answer", her beat saying down until the next answer brings it up; it
starts down until the first answer. The check waits while a turn is at the gate (from before the limit
gate's wait to its end) or, on a headless harness (dsh, gemini: a one-shot process per turn), while the
adapter's own record says a turn runs, and a check owed ` + friend.SessionQuiet.String() + ` that has not
gone in is one "presence: REFUSED" line naming why. A turn runs as long as it prints; one
silent past --silent-stop is stopped with its process group, the reason on the record. The same provider refusal (an invalid_request_error)
on --broken-after turns in a row marks the session broken: nothing more is delivered, every message
stays pending, status says session=broken, and the seat (else --coordinator) is told once on the
bus; a restart clears it. A turn whose harness says it is out of credits or at a usage limit (a Claude
Code rate_limit_event rejected, "Insufficient AI Credits ... will refresh 6:52 PM", "usage limit ...
try again at") makes the friend down until the reset: the presence file says down with the limit,
her beat says down with the reset and the reason (nova-sprint friend beat --until --reason; her row reads
down), nothing is delivered, and the seat (else
--coordinator) is told once with the line that shows it on her row (nova-sprint friend down <me>
--reason <its words> --until <the reset>); after the reset a wake turn must be answered with its
nonce from inside the session before she beats again, and the seat is told she is back. Each harness's own
wording is read (claude, codex, opencode, grok, antigravity, dsh, gemini), its kind (limit or credits) and its
reset when it names one (a clock time in the zone it names, "resets Oct 10 at 5am (America/New_York)"), else
--limit-rest, and the seat is told once, as a judgment, the text it could not read; status says session=limited limit_kind= limit_until= while it stands. The friend
row's mode and width come with each beat's answer (row_mode=, row_width=, row_config_dir=). In one-shot mode width lanes run, each its own session seeded from the friend's AGENTS.md and
memory/, kept in lanes.json; each lane hands one card a turn from <dir>/inbox/QUEUE.json (its BRIEF.md, the
REPORT.md and RESULT.md to write, one bus line to send), the waiting messages riding along, and hands the
next only when the turn ends; a card with no RESULT.md after two turns is set aside and reported. A claude
lane is a process per card instead (env CLAUDE_CONFIG_DIR=<config_dir> claude -p <the brief>, stdin
/dev/null, inside the lane wall with the row's config_dir as its --config-dir), its result read from the
card's outbox; a claude row in one-shot mode with no config_dir (nor --config-dir) is refused on the
record with the remedy, and no lane runs. Each claude run is priced from its stream-json and its
rate_limit_event read, an opencode run from its session's record (opencode export), and each beat says
what the lanes have cost and the limit they last read, one record line when it changed (spend: harness=
runs= cost_usd= five_hour= seven_day= ..._resets=, or limited_until=). A lane
turn the provider rate-limits (429, "rate limit reached", "too many requests", "input token limit
exceeded") keeps its card and pauses new lanes for a backoff (30s doubling to 10m), lowers the live lane
cap by a quarter and raises it one lane per clean 10m, no hold; three lowerings in an hour are one
blocker to the seat. Out of funds (402, insufficient balance) holds the lanes until a restart, told once. With a sprint server the daemon also serves the friend's reader row (reader-<friend>): every 10s it asks queue --as reader-<friend> --json,
begins each asked read up to the row's read slots (row_read_slots=, 2 until the beat says; read slots are in addition to width, never taken by cards and never lent to them), writes <dir>/reads/<card>/{READ.md,BRIEF.md,WORKER-REPORT.txt},
runs it as a one-shot of the harness (a claude account's model by the read's tier) inside the lane wall, and records read --ok|--broken --finding --usage from the RESULT.md, or read --return --reason --usage when it names no verdict or the provider's usage limit stops it. Every
lane child (the harness's session open and each card's turn) runs inside the wall profile the row names
(row_profile=), else --profile: as nova-friend wall --profile <p> --dir <d> -- <harness>, writes only to
--dir, --wall-jobs and --config-dir, never to the coordinator's self (--deny-self; a lane wall that
denies nothing is refused), the network TCP 443 and 22 (docs/SPEC-SANDBOX.md). Prints
one RUN line per delivery on stdout; stops on SIGINT or SIGTERM, a delivery under way left pending.
--dry-run checks the flags and the harness and prints the daemon it would run (RUN DRY-RUN as= harness=
dir= state= redis=): no store is opened and nothing is written.
One-shot lanes do what a friend's card runner script did (docs/SPEC-FRIEND.md, one-shot lanes at parity), each
set on the friend row as the beat answers it (row_tiers=, row_streams=, row_token_cap=, row_load_max=, row_load_width=,
row_pause_on=, row_refuse_go=) with the flags as the defaults the row overrides: a card filter (--lane-tiers: a dealt
card of another tier that no lane has begun is never run, and the coordinator is asked once by a bus request to take it
back with the exact nova-sprint friend take line, the server serving no friend's take-back; --lane-streams: patterns a card's stream or id
must match, else it is skipped); at most the row's width at once, held to --load-width (3) while the machine's one-minute
load is above --load-max; a per-card token cap (--token-cap) that writes a HOLD REPORT.md naming the cap and stops the
lane; a provider failure (out of funds, 402; with --pause-on any a rate limit too) that stops every lane under way (each
card kept, counted toward nothing), writes ` + friend.PauseFile + ` in the state directory with the provider's exact message, and while it stands
beats the friend down with it (friend beat --until --reason), nothing resuming until a person runs nova-friend resume; each finished card's tokens read from opencode's own database (--db, the run's
session and its children) and priced by the store's route row for --model, rounded up to the cent, unpriced with its
reason when there is none, published as a Cost: line on REPORT.md and tokens:/cost: lines on RESULT.md; --refuse-go puts
go and gofmt that refuse (this binary, by symlink) first on the lane's PATH; and one bus note to the coordinator at each finish.`,
				Flags: func(f *tool.Flags) {
					daemonFlags(f)
					f.String("model", "", "the friend's model as provider/model, to price a card by the store's route row (default: none, cards are unpriced)")
					f.String("db", filepath.Join(w.home, ".local", "share", "opencode", "opencode.db"), "opencode's own database, where a card's tokens are read")
					f.String("lane-tiers", "", "the tiers the lanes work, comma-separated; a dealt card of another tier is never run and the coordinator is asked to take it back (default: the row's row_tiers, else every tier)")
					f.String("lane-streams", "", "patterns a card's stream or id must match, comma-separated (default: the row's row_streams, else every card)")
					f.Int("token-cap", 0, "tokens one card may spend, all kinds, before its lane is stopped with a HOLD report; 0 none (default: the row's row_token_cap)")
					f.Int("load-max", 0, "the machine's one-minute load above which lanes are held to --load-width; 0 none (default: the row's row_load_max)")
					f.Int("load-width", friend.DefaultLoadWidth, "the lanes that run while the load is above --load-max")
					f.String("pause-on", "", "funds or any: any holds the lanes and the friend down on a rate limit too (default: funds, a rate limit backs off)")
					f.Bool("refuse-go", false, "put go and gofmt that refuse first on every lane's PATH, and GOROOT nowhere (default: the row's row_refuse_go)")
					f.String("mode", "", "override the friend row's delivery mode, batch or one-shot, for a test (default: the row's, read from each beat)")
					f.String("profile", sandbox.ProfileFriend, "the wall profile every lane child runs inside when the friend row names none (row_profile=): "+strings.Join(sandbox.LaneProfiles, ", "))
					f.String("config-dir", "", "the friend's config directory, writable inside the lane's wall and its HOME there, and a claude one-shot lane's CLAUDE_CONFIG_DIR, an absolute path (default: the row's config_dir, read from each beat as row_config_dir=, else CLAUDE_CONFIG_DIR)")
					f.String("deny-self", w.getenv("NOVA_FRIEND_DENY_SELF"), "the coordinator's self, never written inside a lane's wall, comma-separated; ~/ is the wall's HOME; a lane wall with none is refused (default: NOVA_FRIEND_DENY_SELF)")
					f.String("wall-jobs", "", "job directories outside --dir that are writable inside the lane's wall, comma-separated")
					f.String("wall-reads", "", "directories the harness reads inside the lane's wall beyond the system roots and its own, comma-separated")
					f.Check(func(c *tool.Call) {
						if m := c.Str("mode"); m != "" && m != friend.ModeBatch && m != friend.ModeOneShot {
							c.Problem(fmt.Sprintf("--mode %q wants batch or one-shot", m))
						}
						if d := c.Str("config-dir"); d != "" && !filepath.IsAbs(d) {
							c.Problem(fmt.Sprintf("--config-dir %q wants an absolute path: CLAUDE_CONFIG_DIR is read as given, never expanded", d))
						}
					})
					f.Prints()
				},
				Run: w.run,
			},
			{
				Name:    "beat",
				Usage:   "beat --as <me> [--server <addr>]",
				Example: "", // the daemon's own act; the example block's first run has no beat line
				Effect:  tool.Delivery + ": one beat to the sprint server, the same beat the daemon's loop sends while its session is alive; --dry-run sends nothing",
				Detail: `The daemon's beat on its own (docs/SPEC-FRIEND.md, the loop): one "friend beat <me>" to
the sprint server, what keeps the friend up in the sprint's friends table. The agent install writes
runs the daemon, and the daemon beats already while its session is alive, so the beat needs no
agent of its own and no hand plist: this verb is the canary, run by hand. A server that does not
answer is exit 2. --dry-run says the beat it would send, and to which server, and sends nothing.
example: nova-friend beat --as bob --server 127.0.0.1:6390`,
				DryRun: true,
				Flags: func(f *tool.Flags) {
					f.Required("as", "your name, a nova-config friend row")
					f.String("server", w.server(), "the sprint server, host:port (default: "+ServerEnv+", else "+DefaultServer+")")
				},
				Run: w.beatVerb,
			},
			{
				Name:    "install",
				Usage:   "install --as <me> --harness <h> --dir <d> [--session <id>] [--adapter folder --delivery-dir <watched-dir>] [--server <addr>] [--width <n>] [--silent-stop <d>] [--broken-after <n>] [--coordinator <seat>] [--state-dir <d>] [--redis <addr>] [--config-dir <d>] [--model <provider/model>] [--secrets NAME[,NAME] --seat <seat>] [--launchd-log <file>] [--dry-run]",
				Example: "install --as bob --harness opencode --dir ./bob --dry-run",
				Effect:  tool.LocalWrite + ": writes the harness's settings and the launchd agent com.nova.friend-<me>, and loads it",
				Detail: `First writes the settings the friend's harness needs in its own config (docs/SPEC-FRIEND.md, Harness
settings), each merged into what the file holds and read back, one INSTALL WROTE line each: codex,
the friend's directory in CODEX_HOME/config.toml [sandbox_workspace_write] writable_roots; dsh, the
agent preset registry's default and selectedDefault "` + friend.DSHPreset + `" in DSH_HOME/profiles/desktop/cordis.patch.yml
(a session keeps the preset it was opened under); grok, the wake file (--session, else
~/.nova-friend/<me>/<me>.wake), made empty, and named in the agent; claude, --config-dir (default
CLAUDE_CONFIG_DIR) made private and named in the agent; opencode, the friend's directory allowed in
<dir>/opencode.json and --model there when given. The friend's directory, the writable root, the wake
file's directory and the config directory must each be a real directory: a symlink (or a config file
that is one) is refused and nothing is written or loaded. nova-friend check --settings names drift.
Then writes ~/Library/LaunchAgents/com.nova.friend-<me>.plist (RunAtLoad, KeepAlive: started at login,
restarted when it dies, pending messages redelivered first), boots out whatever that label runs,
and bootstraps the new one; running it again replaces the agent. launchd's own log goes under
~/Library/Logs (launchd cannot open one on a network volume). The daemon's state files and record
go under <dir>/.nova-friend, inside the directory the session may write, so a sandboxed session's
pong lands where the daemon reads it; a directory that refuses them (a background process may not
touch a removable volume without the person's permission) puts them under ~/.nova-friend/<me>, said
on the record; --state-dir moves them. A daemon whose arguments differ from the installed plist says
"plist drift" on start: a launchctl kickstart keeps the arguments launchd loaded, so after an edit
run install again. A binary on a removable volume (/Volumes) is copied to
~/.nova-friend/bin/nova-friend before the plist is written, and the plist names the copy; a copy that
cannot be made is refused and the agent is not loaded. --secrets NAME[,NAME] wraps the daemon in nova-secrets
exec as the machine's --seat (its store under ~/nova-bench/secrets, its key under ~/.config/nova-secrets),
opening exactly those names to the harness and refusing to start without every one; nova-secrets
and sops are found on PATH at install and written by absolute path. --dry-run prints the plan and
writes nothing. For harness claude, a NOTE prints the one line the open session runs as a background task, ` + friend.ClaudeWaitLine("<me>", "<file>.wake") + `
(the session's own blocking read, re-run with the cursor it printed each time it returns; the daemon is passive for claude, answers
the coordinator's ping, and appends one line per message to that wake file). For harness grok, a NOTE prints the one line the open session runs, ` + friend.GrokMonitorLine("") + `
(--session names the wake file in place of <file>.wake): one command in the session, not a flag, an
environment variable or a wrapper at app start. While no such monitor runs, a delivery is deferred
(the message stays pending and is tried again), never failed and never dropped. Once the agent is
loaded, install runs the delivery check once (nova-friend check, --within) and says its line in a
NOTE: a CHECK FAIL is a NOTE, except a session the harness cannot drive (dsh under an agent preset), which
is refused at exit 2 with its remedy and the agent booted out again. A harness with no deliver command
(claude, the surveyed ones) is refused before anything is written, the adapter card its remedy.`,
				DryRun: true,
				Flags: func(f *tool.Flags) {
					daemonFlags(f)
					f.String("secrets", "", "the names of the secrets the session needs, comma-separated (never values); wraps the daemon in nova-secrets exec")
					f.String("seat", "", "the machine's nova-secrets seat the secrets are opened as (nova-config machine show <self>: seat); wanted with --secrets")
					f.String("launchd-log", "", "launchd's stdout and stderr file (default: ~/Library/Logs/nova-friend-<me>.log)")
					f.Duration("within", friend.DefaultCheckWithin, "how long the delivery check after loading waits for the session's pong")
					settingFlags(f)
					f.Check(func(c *tool.Call) {
						if c.Str("secrets") != "" && c.Str("seat") == "" {
							c.Problem("--secrets wants --seat <seat>: the seat the secrets are opened as")
						}
						for _, name := range secretNames(c.Str("secrets")) {
							if !secretNameRe.MatchString(name) {
								c.Problem(fmt.Sprintf("--secrets names a secret by its variable name, letters, digits and underscores: %q is none", name))
							}
						}
					})
				},
				Run: w.install,
			},
			{
				Name:    "uninstall",
				Usage:   "uninstall --as <me> [--dry-run]",
				Example: "uninstall --as bob --dry-run",
				Effect:  tool.LocalWrite + ": boots the agent out and removes its plist",
				DryRun:  true,
				Flags: func(f *tool.Flags) {
					f.Required("as", "your name, the friend the agent was installed for")
				},
				Run: w.uninstall,
			},
			{
				Name:    "check",
				Usage:   "check [--as <coordinator>] [<friend>...] [--since <duration>] [--shown <file|->] [--harness codex --dir <d> --session <id> --adapter folder --delivery-dir <watched-dir>] [--json]",
				Example: "", // the banner's example block runs nothing that reads a fleet's state; -h carries the example
				Effect:  tool.Delivery + ": without --harness it only reads (the health check); with --harness it delivers one session check into the live session (the delivery check)",
				DryRun:  true,
				Detail: `The health check: is each friend's row true. The friends are the arguments, else every friend with a
state directory under ~/.nova-friend (or --state-dir) or on the bus. Everything is judged over the --since
window (default 24h): deliveries, deferrals, real messages and the session pong. Per friend, five lines in
this order:
CHECK DAEMON friend=<f> agent=<loaded|not-loaded|none> pid=<n|-> status=<ok|stale|none> connection=<..> challenge=<..> pong_age=<age|-> presence=<up|asleep|down> seen_age=<age|-> proof=<pending|sent|none> proof_age=<age|->
CHECK HARNESS friend=<f> harness=<h> route=<push|mailbox|queue|passive> last=<RFC3339|-> last_exit=<n|-> failed_of_last20=<n> deferred=<n> broken=<RFC3339|-> reason=<line|-> session_live=<conversation|-> queued=<n|->
CHECK BUS friend=<f> real_since=<n> last_real=<RFC3339|->   (real: not ping, pong, daemon-pong or keepalive)
CHECK WORK friend=<f> inbox=<n> outbox=<n> newest_outbox=<name|-> newest_at=<RFC3339|->   (under the friend's directory)
CHECK VERDICT friend=<f> verdict=<ok|broken|silent|deaf|down|untrue> shown=<state/working|-> why=<one line>
then one summary line: CHECK OK friends=<n> ok=<n> broken=<n> deaf=<n> silent=<n> down=<n> untrue=<n>.
The verdict is a function of those facts, the first rule that holds: broken when the session is marked
broken or every delivery in the window failed (at least one, and all of them); deaf when a delivery in
the window succeeded and neither a session pong nor a real message came back in the window; silent when
no delivery was due in the window and nothing came back; down by presence; else ok. --shown is what a
consumer shows of each friend, JSON {"<friend>":{"state":"up|asleep|down","working":<n>}} from a file or
- for stdin; when it says up or working and the verdict is not ok, the verdict stays and the why leads
with "untrue: shown <state>/<working>, ", and when the facts are ok but the friend is asleep or its agent
is not loaded the verdict is untrue. --json prints one object instead of the lines: friends[] each with
friend and daemon{friend, agent, pid, status, connection, challenge, pong_age, presence, seen_age, proof, proof_age},
harness{friend, harness, route, last, last_exit, failed_of_last20, deferred, delivered, failed, broken,
reason, session_live, queued}, bus{friend, real_since, last_real}, work{friend, inbox, outbox, newest_outbox, newest_at},
verdict{friend, verdict, shown, why}, and summary{friends, ok, broken, deaf, silent, down, untrue}.
Exit 0 when every verdict is ok, 1 when any is not (the check found something), 2 when it could not run
(a refused flag, an unreadable --shown).
With --harness the verb is the delivery check instead, which
proves the live session takes a delivery: a SESSION CHECK <nonce> goes in through the harness's deliver
command, the session runs the exact nova-friend pong line it carries, and a pong with that nonce from
--as is on the bus within --within. It prints CHECK OK harness= took=, or CHECK FAIL harness=
stage=<deliver|act|reply> why= at exit 1: deliver, the adapter did not take it (a harness with no
deliver command says its reason); act, the session never ran the line; reply, the line ran and no pong
reached the bus. The pong goes to --to, else the seat the daemon's status names, else --as itself. Run it
once a night as a nova-config loop record (docs/TESTING.md). --dry-run checks the flags and the harness
and prints the line the session would run: nothing is delivered and no store is opened.
With --settings (and --as, --harness, --dir, and the flags install took: --session, --config-dir,
--model, --state-dir) the verb compares the harness's settings with what install would write and
writes nothing: CHECK OK harness= settings=<n> drift=0, or CHECK DRIFT harness= settings=<n> drift=<n>
at exit 1 with one CHECK DRIFT line per setting, harness= file= name= want= have= (a symlink where a
real directory belongs is have="symlink to <target>"); install again writes them.
example: nova-friend check --as ada bob`,
				Flags: func(f *tool.Flags) {
					f.String("as", "", "your name, the coordinator (the health check); the friend itself with --harness")
					f.String("harness", "", "the harness the session runs in (delivery check): "+strings.Join(friend.Harnesses, ", "))
					f.String("dir", "", "the friend's working directory")
					f.String("session", "", "the session to deliver into (delivery check); for grok the wake file (--settings)")
					f.String("adapter", "", "delivery route: folder for an existing watched Codex session")
					f.String("delivery-dir", "", "existing folder watched by that Codex session when --adapter folder")
					f.Bool("settings", false, "compare the harness's settings with what install would write; nothing is delivered or written")
					settingFlags(f)
					f.Duration("within", friend.DefaultCheckWithin, "how long to wait for the session's pong (delivery check)")
					f.String("to", "", "who the pong goes to (default: the seat the daemon's status names, else --as)")
					f.Duration("since", 24*time.Hour, "the window every fact is judged over: deliveries, deferrals, real messages, the session pong")
					f.String("shown", "", "path to shown state file, or - for stdin")
					stateDir(f)
					redis(f)
					f.Check(func(c *tool.Call) {
						if err := friend.CheckFolderRoute(c.Str("harness"), c.Str("session"), c.Str("adapter"), c.Str("delivery-dir")); err != nil {
							c.Problem(err.Error())
						}
						callArgs.Store(c, f.Args())
						if c.Bool("settings") && c.Str("harness") == "" {
							c.Problem("--settings wants --harness, --as and --dir: the friend whose harness settings to compare")
						}
						if h := c.Str("harness"); h != "" {
							if c.Str("as") == "" {
								c.Problem("--as is required")
							}
							if c.Str("dir") == "" {
								c.Problem("--dir is required")
							}
							if !friend.Known(h) {
								c.Problem(fmt.Sprintf("--harness %q is no harness; it wants one of %s", h, strings.Join(friend.Harnesses, ", ")))
							}
							if c.Dur("within") <= 0 {
								c.Problem("--within wants a positive duration, such as 5m")
							}
						} else {
							if c.Dur("since") <= 0 {
								c.Problem("--since wants a positive duration, such as 24h")
							}
						}
					})
				},
				Run: w.check,
			},
			{
				Name:    "host",
				Usage:   "host --as <me> --harness <h> --dir <d> [--prompt <regexp>] [--state-dir <d>] [--dry-run] [--json] -- <launch command...>",
				Example: "host --as bob --harness aider --dir ./bob --dry-run -- aider",
				Effect:  tool.LocalWrite + ": starts the launch command in a new detached tmux session friend-<me> and saves the session and prompt in the state directory",
				DryRun:  true,
				Detail: `Hosts a terminal harness (OpenCode, Grok, Aider, any TUI) in tmux, so the friend's session is the TUI in
the pane: the daemon types into it as a person would, and a person can attach and watch. Runs
tmux new-session -d -s friend-<me> -c <dir> -- <launch command...>; refuses when friend-<me> exists.
--harness names the harness whose idle prompt pattern is used (` + strings.Join(hostHarnesses(), ", ") + `);
--prompt <regexp> overrides it and is wanted for any other harness: the pattern the last non-empty line
of the pane matches while the harness waits for input. The session name and the pattern are saved in
<state-dir>/` + friend.HostFile + ` (--state-dir, else <dir>/.nova-friend, as run), so run and install need no flag beyond
--harness tmux: with that harness a delivery captures the pane (tmux capture-pane -p -t friend-<me>);
when its last non-empty line matches the idle prompt it types the text on one line, each newline shown
as " ⏎ " (tmux send-keys -l), then Enter as a second call, and is accepted once the prompt line has gone,
polled each half second for up to a minute. While the prompt is absent a turn runs, the delivery is
deferred and nothing is typed, so no second turn lands beside one. A missing session is deferred with
the line to host it again, never a failure. Hosting is opt-in: a TUI started outside tmux keeps its
own harness. To watch: tmux attach -t friend-<me>.
Output: HOST OK session=friend-<name> dir=<d> attach="tmux attach -t friend-<name>"; or
HOST REFUSED: friend-<name> runs already; run: tmux attach -t friend-<name>; --dry-run prints
HOST DRY-RUN session= dir= command= (the tmux command) and starts and saves nothing. JSON fields:
session, dir, attach (command on a dry run). Exit 0 started, 1 refused, 2 could not run (no launch
command, no prompt pattern for the harness, tmux missing or failing).`,
				Flags: func(f *tool.Flags) {
					f.Required("as", "your name, a nova-config friend row")
					f.Required("harness", "the harness the TUI is, for its idle prompt pattern: "+strings.Join(hostHarnesses(), ", ")+" (other: name --prompt)")
					f.Required("dir", "the friend's working directory: the TUI's, and where the state files live")
					f.String("prompt", "", "the idle prompt, a regular expression the last non-empty line of the pane matches (default: the harness's)")
					stateDir(f)
					f.Check(func(c *tool.Call) {
						if len(w.launch) == 0 {
							c.Problem("the launch command is wanted after --: host --as <me> --harness <h> --dir <d> -- <launch command...>")
						}
						if h := c.Str("harness"); h != "" {
							if _, _, err := friend.HostPrompt(h, c.Str("prompt")); err != nil {
								c.Problem(err.Error())
							}
						}
					})
				},
				Run: w.host,
			},
			w.reachVerb(),
			{
				Name:    "ping",
				Usage:   "ping --as <coordinator> (--to <friend> | --wake --to-friends [--every <d>] [--within <d>] [--never-wake <f,...>] [--server <addr>]) [--nonce <n>] [--since <RFC3339>] [--redis <addr>] [--dry-run]",
				Example: "ping --as ada --to bob --nonce abc123",
				Effect:  tool.Delivery + ": one PING on the friend's stream, as the coordinator",
				DryRun:  true,
				Detail: `Sends "PING <nonce>" with the seat line (seat=<me> since=<RFC3339>) and the pong command the
session runs; the nonce is six random characters unless --nonce names one. Prints PING OK
nonce= id= to=. The daemon answers daemon-pong at once and acks it; the session answers pong at the head of its next turn.
--wake makes it a wake check (a wake=1 line in the body): the daemon still answers at once, and, the session being free,
pushes the pong line in as its own turn, so an idle session is asked too; only the session's pong ends it (wait-pong).
--dry-run checks the PING as send checks it and sends nothing.

--wake --to-friends is the wake loop (docs/SPEC-FRIEND.md, "The wake ping loop"): it reads the friends table from the sprint
server (--server) and sends a wake PING to every friend whose status is up, never one held or down, never --as, never one in
--never-wake; waits up to --within for each session's pong (a daemon-pong never counts); and sends the coordinator (the seat's
holder, else --as) one blocker note naming the friends whose session did not answer, "wake: deaf: <f,...>", once per change
of that set. With --every <d> it does this each d until interrupted, one line per pass (WAKE OK pass= pinged= answered=
deaf=) and one WAKE DEAF friends= at= per change; without it, one pass. ping-install runs that loop as a launchd agent.`,
				Flags: func(f *tool.Flags) {
					f.Required("as", "your name, the coordinator")
					f.String("to", "", "the friend to ping (required without --to-friends)")
					f.Bool("to-friends", false, "ping every friend the friends table holds up: wake pings, with --wake")
					f.Duration("every", 0, "with --to-friends: pass again each d until interrupted (default: one pass)")
					f.Duration("within", friend.Window, "with --to-friends: how long each pass waits for the sessions' pongs")
					f.String("never-wake", "", "with --to-friends: friends never wake-pinged, comma-separated")
					f.String("server", w.server(), "with --to-friends: the sprint server, host:port, whose coordinator view holds the friends table (default: "+ServerEnv+", else "+DefaultServer+")")
					f.Check(func(c *tool.Call) { wakeFlagProblems(c) })
					f.String("nonce", "", "the nonce to carry (default: six random characters)")
					f.String("since", "", "since when you hold the seat, RFC3339 (default: now)")
					f.Bool("wake", false, "a wake check: the session is pushed the pong line as its own turn when it is free")
					redis(f)
				},
				Run: w.ping,
			},
			{
				Name:    "ping-install",
				Usage:   "ping-install --as <coordinator> --every <d> [--within <d>] [--never-wake <f,...>] [--server <addr>] [--redis <addr>] [--launchd-log <file>] [--dry-run]",
				Example: "", // writes a launchd agent: the example block has no line a test may run for real
				Effect:  tool.Delivery + ": a launchd agent that runs ping --wake --to-friends --every, started at login and restarted when it dies",
				DryRun:  true,
				Detail: `Writes the agent com.nova.friend-wake-ping-<as> under ~/Library/LaunchAgents and loads it, so running it again replaces
the agent with the same result; the agent runs: nova-friend ping --as <as> --wake --to-friends --every <d> --within <d> with
the other flags as given. --dry-run prints the plist path and the commands and writes nothing. ping-uninstall boots it out and
removes its plist.`,
				Flags: func(f *tool.Flags) {
					f.Required("as", "your name, the coordinator")
					f.Duration("every", 10*time.Minute, "how often a pass runs")
					f.Duration("within", friend.Window, "how long each pass waits for the sessions' pongs")
					f.String("never-wake", "", "friends never wake-pinged, comma-separated")
					f.String("server", w.server(), "the sprint server, host:port (default: "+ServerEnv+", else "+DefaultServer+")")
					f.String("launchd-log", "", "launchd's own log (default: ~/Library/Logs/nova-friend-wake-ping-<as>.log)")
					redis(f)
				},
				Run: w.pingInstall,
			},
			{
				Name:    "ping-uninstall",
				Usage:   "ping-uninstall --as <coordinator> [--dry-run]",
				Example: "", // removes a launchd agent: no example line
				Effect:  tool.Delivery + ": boots the wake ping agent out and removes its plist",
				DryRun:  true,
				Detail:  `Removes what ping-install wrote; an agent that is not there is fine.`,
				Flags: func(f *tool.Flags) {
					f.Required("as", "your name, the coordinator")
				},
				Run: w.pingUninstall,
			},
			{
				Name:    "pong",
				Usage:   "pong --as <me> --nonce <n> [--to <coordinator>] [--dir <work-dir>] [--queue <n>] [--working <n>] [--width <n>] [--state-dir <d>] [--redis <addr>] [--dry-run]",
				Example: "pong --as bob --nonce abc123 --to ada --queue 2 --working 1 --width 4",
				Effect:  tool.Delivery + ": the session's answer to a PING, one note on the bus to the coordinator, and the pong file",
				DryRun:  true,
				Detail: `What the session runs when a PING <nonce> arrives, first and before anything else: sends
"pong <nonce> queue=<n> working=<n> width=<n>" to the coordinator (--to, else the seat the last
ping named, read from the status file) and records it in the state directory (--state-dir, as the
check's line names the daemon's; else ~/.nova-friend/<me>), where the daemon reads it. The note on
the bus is the answer: a pong file that cannot be written is said and the answer stands. The name is the daemon's: a
--as that is not the friend whose state is there is refused. --dry-run checks the note as send checks it and prints
the line it would send; nothing is sent and no pong file is written.`,
				Flags: func(f *tool.Flags) {
					f.Required("as", "your name, the friend the daemon in --dir runs as")
					f.Required("nonce", "the nonce the PING carried")
					f.String("to", "", "the coordinator (default: the seat the last ping named)")
					f.String("dir", "", "the friend's working directory; when given, omitted queue and working counts are read from its inbox/QUEUE.json")
					f.Int("queue", 0, "tasks queued, from your own task list")
					f.Int("working", 0, "tasks working, from your own task list")
					f.Int("width", 0, "your width, from the nova-config friend row")
					stateDir(f)
					redis(f)
				},
				Run: w.pong,
			},
			{
				Name:    "wait-pong",
				Usage:   "wait-pong --from <friend> --nonce <n> [--timeout <d>] [--redis <addr>]",
				Example: "wait-pong --from bob --nonce abc123 --timeout 2s",
				Effect:  tool.Inspection,
				Detail: `Reads the bus log every second until a pong for the nonce from that friend's own stream (the
message's from, never its body) is there, or --timeout (default ` + friend.Window.String() + `) runs out. The log is read
from --timeout before the wait began (the store's clock), so a pong older than the wait is not
looked for and a long log is never read from its start. Prints WAIT-PONG OK
nonce= from= at= queue= working= width= daemon=<true|false> (whether the daemon-pong came too),
or WAIT-PONG NONE at exit 1.`,
				Flags: func(f *tool.Flags) {
					f.Required("from", "the friend whose pong to wait for")
					f.Required("nonce", "the nonce the ping carried")
					f.Duration("timeout", friend.Window, "how long to wait")
					redis(f)
				},
				Run: w.waitPong,
			},
			{
				Name:    "watch",
				Usage:   "watch --as <coordinator> [--timeout <duration>] [--state-dir <d>] [--redis <addr>] [--json]",
				Example: "", // waits until a wake or --timeout: the example block has no line that blocks for minutes; -h carries the example
				Effect:  tool.Inspection + ": the cursor file in the state directory is rewritten",
				ExitTable: "0 a wake came: WATCH OK; 1 WATCH NONE, --timeout ran out; 2 could not run (a flag, a name the roster lacks, " +
					"a store that did not answer, a cursor file that cannot be read or saved).",
				Detail: `The coordinator's wake, one run. A session that runs this in the background is re-invoked when it exits, so
run it again each time it returns; it needs no flag between runs. It waits on your stream, on your wake file
(<state-dir>/<me>.wake, where the claude adapter appends one line per message) and on events, and returns
on the first wake with one line per wake, at most 5, the wake file's lines first:
WATCH MESSAGE id=<id> from=<name> subject=<s>   a bus message for you
WATCH EVENT id=<id> from=<name> subject=<s>     a bus message whose subject starts event: (any tool may send one, e.g. event: machine stopped unasked)
WATCH WAKE line=<text>                          a line appended to the wake file
then WATCH OK after=<cursor> at exit 0. Subjects and wake lines are quoted. Your own messages and the subjects
ping, pong, daemon-pong and keepalive (matched without case) are skipped and never wake you. Past --timeout
(a Go duration; 0, the default, is for ever) it prints WATCH NONE waited=<duration> on standard error at exit 1.
The cursor (the last stream entry id seen and the wake file's offset) is saved in <state-dir>/watch.json, written
whole and renamed, after every run, so the next run misses nothing; the first run starts at the stream's end
and the wake file's end. The watch takes nothing: a later recv still delivers what it saw. --json prints one
object when the watch ends: {"status":"ok","word":"OK|NONE","after":<cursor>,"waited":<duration, NONE only>,
"wakes":[{"kind":"MESSAGE|EVENT|WAKE","id":<id>,"from":<name>,"subject":<s>,"line":<text>}]} (id, from and
subject are left out of a WAKE, line out of the others). Exit 2 when a flag is wrong, the name is not on the
roster, or the store does not answer.
example: nova-friend watch --as ada --timeout 10m`,
				Flags: func(f *tool.Flags) {
					f.Required("as", "your name, the coordinator whose stream and wake file are watched")
					f.Duration("timeout", 0, "how long to wait before WATCH NONE, a Go duration (1s, 10m); 0 is for ever")
					stateDir(f)
					redis(f)
					f.Check(func(c *tool.Call) {
						if c.Dur("timeout") < 0 {
							c.Problem("--timeout wants a duration of at least 0, 0 for ever (a negative watch is no watch)")
						}
					})
				},
				Run: w.watch,
			},
			{
				Name:    "status",
				Usage:   "status --as <me> --dir <d> [--state-dir <d>] | status --all",
				Example: "status --as bob --dir ./bob",
				Effect:  tool.Inspection,
				Detail: `Prints STATUS OK daemon=<up|down> harness= connection=<connected|silent> seat= last_ping= challenge=<quiet|challenged|deaf>
last_pong= session_pong_age= daemon_pong_age= pongs= queue= working= width= beats= delivered= envelope= envelope_bytes= session=<ok|broken|-> mode=<batch|one-shot|-> presence=<up|down>
(once a daemon has written it; last_session=, and when down presence_reason=, "no session answer" or "no daemon") (broken: session_id= broken_at= reason=; one-shot: lanes=)
status=<up|down> why= evidence=, and for harness grok route=<push|defer>, for harness claude route=passive,
from the daemon's status file (up while it is under ` + friend.DaemonStale.String() + ` old), the session's pong file and the queue file;
session_pong_age is the session's own pong (the pong file), daemon_pong_age the daemon's answer to the last ping (status.json
last_daemon_pong), two facts: a daemon that pongs says nothing of the session
(<dir>/inbox/QUEUE.json). route=push when a tail of a .wake file runs under the open window's pid; route=defer, with a NOTE of
` + friend.GrokMonitorLine("") + `, when none does. For harness claude route=passive (the daemon takes nothing off the stream; the
session's own wait reads it), with a NOTE of the line install prints. JSON carries route as a string (push, defer or passive) and
that NOTE in notes; other harnesses omit route. status is the friend's, decided from evidence in order (docs/SPEC-FRIEND.md); the harness's process is
shown and never decides (harness_seen=running|not-seen|-, the daemon's check of the process table: a session run from its command
line has no app to see); at a limit (the state directory's
` + friend.LimitFile + `) is down until the reset; no session answer (the pong file, or the daemon's last word from the session on the bus) under ` + friend.AnswerBound.String() + ` is down; a bus that cannot
deliver (the daemon down, the session broken, the store failing) is down; otherwise up. The daemon's beat never makes it up. why
is the rule that decided it, as a person reads it ("no session answer 12m", "limit until Mon 1:00 PM"); evidence is every piece,
"; "-separated: the harness, the session answer, the limit, the messages waiting on the stream (counted with --redis), the last
turn's end and exit from the log. envelope is how many messages the last turn's envelope carried and envelope_bytes its size
(at most the harness's text limit, ` + fmt.Sprint(friend.BatchBytes) + ` bytes unless it names its own; the first message always goes in), 0 before the first. STATUS NONE at
exit 1 when no daemon ever ran as --as (no status file in the state directory). daemon_version= is the daemon's build stamp and
last_beat_age= the age of its last beat that the sprint server answered (- for none); binary= the path it runs from. status --all
lists every friend daemon agent installed for this login (~/Library/LaunchAgents/com.nova.friend-<name>.plist that runs the
daemon), one AGENT line each: name= daemon=<up|down|none> daemon_version= last_beat= last_beat_age=, read from the status
file in the agent's state directory (up while it is under ` + friend.DaemonStale.String() + ` old; none: no status file).`,
				Flags: func(f *tool.Flags) {
					f.String("as", "", "your name (required without --all)")
					f.String("dir", "", "the friend's working directory, where the queue file lives (required without --all)")
					f.Bool("all", false, "one line per friend daemon agent installed for this login (com.nova.friend-*): name, daemon up or down, daemon_version, last_beat_age")
					f.Check(func(c *tool.Call) {
						if !c.Bool("all") {
							c.Want("as", "your name")
							c.Want("dir", "the friend's working directory, where the queue file lives")
						}
					})
					stateDir(f)
					redis(f)
				},
				Run: w.status,
			},
			{
				Name:    "refuse-go",
				Usage:   "refuse-go --name go|gofmt",
				Example: "", // refuses, by design: the example block has no line that runs
				Effect:  tool.Inspection,
				Detail: `What a lane's go and gofmt shims run: nova-friend run --refuse-go makes a directory of symlinks named go and gofmt to
this binary and puts it first on every lane child's PATH (GOROOT pointing nowhere), so a go command a lane runs reaches
this verb through the name it was run by (no shell script) and is refused at exit 2, with the way to run it on a bench.`,
				ExitTable: "2 always: refused.",
				Flags: func(f *tool.Flags) {
					f.Required("name", "the command that was run: go or gofmt")
				},
				Run: func(c *tool.Call) *tool.Out { return tool.Refuse(friend.GoRefusal(c.Str("name"))) },
			},
			{
				Name:    "resume",
				Usage:   "resume --as <me> [--dir <d>] [--state-dir <d>] [--dry-run]",
				Example: "", // clears a pause that only a real provider failure writes
				Effect:  tool.LocalWrite + ": removes the lanes' pause marker " + friend.PauseFile + " from the state directory",
				DryRun:  true,
				Detail: `A provider failure the lanes met (out of funds, 402; a rate limit too when the row says pause_on=any) stops every
lane and writes ` + friend.PauseFile + ` in the state directory with the provider's exact message; while it stands her daemon beats her down
with that message (friend beat --until --reason). Nothing resumes until a person has dealt with the provider and runs this
verb: it prints RESUME OK cleared= with the message the marker held (RESUME OK cleared=none when there was no pause), after
which a running daemon lifts its hold at its next step, runs the kept cards again, and its next beat withdraws the down.
A friend a person held with nova-sprint friend down stays held until nova-sprint friend up.`,
				ExitTable: "0 done, 2 could not run (the marker cannot be removed).",
				Flags: func(f *tool.Flags) {
					f.Required("as", "your name")
					f.String("dir", "", "the friend's working directory, whose state directory holds the marker (default: found by --as)")
					stateDir(f)
				},
				Run: w.resume,
			},
			{
				Name:    "serve",
				Usage:   "serve --as <coordinator> [--redis <addr>] [--dry-run]",
				Example: "", // a loop: the example block has no line that runs for ever
				Effect:  tool.Delivery + ": the coordinator's ping loop; a PING to every friend each second, until a signal",
				DryRun:  true,
				Detail: `The coordinator's side of the connection, run as a nova-config loop row. Each ` + friend.PingEvery.String() + `: the pongs on
the coordinator's own stream are read (a daemon-pong or a session pong, the sender the message's from,
never its body; only a nonce sent to that friend in the last ` + friend.DownAfter.String() + ` answers, once), each friend whose
state changed is said, and every friend row but --as gets a PING with a fresh nonce. A friend is up on
a pong and down after ` + friend.DownAfter.String() + ` without one (from the start for a friend never answered). The friend rows
are nova-config's as the bus store holds them (the set friends), read at the start and again each
` + friend.RowsEvery.String() + `: a row added is pinged, a row removed is forgotten. Prints SERVE OK friends= every= down_after= once, then one line per state change, never one
per ping: SERVE UP friend= at=, SERVE DOWN friend= at= last_pong=<RFC3339|never> reason=; a store or rows
read that fails is one SERVE NOTE until it changes or clears. Stops on SIGINT or SIGTERM: SERVE STOP.
--dry-run reads the friend rows and prints SERVE OK friends= every= down_after=: nothing is sent.`,
				Flags: func(f *tool.Flags) {
					f.Required("as", "your name, the coordinator: the pings come from it and the pongs come to it")
					redis(f)
					f.Prints()
				},
				Run: w.serve,
			},
		},
	}
}

// bus opens the store --redis names, or says why not (exit 2).
func (w world) bus(c *tool.Call) (*bus.Bus, func(), *tool.Out) {
	addr := c.Want("redis", "the bus store's Redis address, host:port (or "+RedisEnv+")")
	if o := c.Refused(); o != nil {
		return nil, nil, o
	}
	ctx, cancel := context.WithTimeout(context.Background(), redisconn.OpenTimeout)
	defer cancel()
	st, closeStore, err := w.open(ctx, addr)
	if err != nil {
		return nil, nil, tool.Refuse(err.Error())
	}
	return &bus.Bus{Store: st}, closeStore, nil
}

func answer(err error) *tool.Out {
	var r *bus.Refusal
	if errors.As(err, &r) {
		return tool.Refuse(r.Problems...)
	}
	return tool.Refuse("the store did not answer: " + err.Error())
}

// openUntil opens the store, trying again after a wait that doubles from a
// second up to OpenRetryMax, until it answers or ctx ends (nil then): the loop
// tolerates a store that goes down, so one that is down at the start is no
// reason to exit; under launchd's KeepAlive that exit was a crash loop every
// five seconds. Each wait is said on the record.
func (w world) openUntil(ctx context.Context, addr string, record func(string)) (bus.Store, func()) {
	wait := time.Second
	for {
		open, cancel := context.WithTimeout(ctx, redisconn.OpenTimeout)
		st, closeStore, err := w.open(open, addr)
		cancel()
		if err == nil {
			return st, closeStore
		}
		record(w.now().UTC().Format(time.RFC3339) + " store: " + err.Error() + "; opening again in " + wait.String())
		w.sleep(ctx, wait)
		if ctx.Err() != nil {
			return nil, nil
		}
		wait = min(2*wait, OpenRetryMax)
	}
}

func (w world) run(c *tool.Call) *tool.Out {
	if c.Bool("notifications-only") {
		return w.runNotifications(c)
	}
	addr := c.Want("redis", "the bus store's Redis address, host:port (or "+RedisEnv+")")
	if o := c.Refused(); o != nil {
		return o
	}
	name, dir, server := c.Str("as"), c.Str("dir"), c.Str("server")
	// every path a lane names is absolute, its working directory first: a model that reads a
	// path relative must never be handed one (friend.LaneJobOf, docs/SPEC-FRIEND.md)
	abs, err := filepath.Abs(dir)
	if err != nil || !filepath.IsAbs(abs) {
		return tool.Refuse(fmt.Sprintf("lane path not absolute: %s: %v; the daemon does not start", dir, err))
	}
	dir = abs
	state, stateWhy := w.daemonStateDir(c)
	// every lane child runs inside the wall of the profile her row names, else --profile
	// (docs/SPEC-FRIEND.md, buds-in-the-wall-r.w5); a batch turn runs as it did
	var rowProfile atomic.Pointer[string]
	var rowConfigDir atomic.Pointer[string] // her row's config_dir as her beat last answered; read by the lanes' runs and the wall
	var rowTokenCap atomic.Int64            // her row's per-card token cap as her beat last answered; DefaultTokenCap until it says, 0 none
	rowTokenCap.Store(friend.DefaultTokenCap)
	wall := friend.Wall{Dir: dir, Jobs: commaList(c.Str("wall-jobs")), Reads: commaList(c.Str("wall-reads")), Deny: commaList(c.Str("deny-self"))}
	self, selfErr := w.binary() // the daemon's binary, by path in its status
	if selfErr == nil {
		wall.Self = []string{self}
	} // else no Self: a lane's child is refused, never run outside the wall
	walledRaw := func(ctx context.Context, d, prog string, args []string, stdin string) (string, int, error) {
		if w.wall == nil {
			return w.exec(ctx, d, prog, args, stdin)
		}
		wl := wall
		// the wall's config directory: --config-dir, else her row's config_dir, else CLAUDE_CONFIG_DIR
		if wl.ConfigDir = c.Str("config-dir"); wl.ConfigDir == "" {
			if d := rowConfigDir.Load(); d != nil && *d != "" {
				wl.ConfigDir = *d
			} else {
				wl.ConfigDir = w.getenv("CLAUDE_CONFIG_DIR")
			}
		}
		wl.Profile = c.Str("profile")
		if p := rowProfile.Load(); p != nil {
			wl.Profile = *p
		}
		return w.wall(wl, w.exec)(ctx, d, prog, args, stdin)
	}
	// the lane rules: the flags are the defaults and her row, as her beat answers it, wins
	flagRules := friend.LaneRules{Tiers: commaList(c.Str("lane-tiers")), Streams: commaList(c.Str("lane-streams")), TokenCap: int64(c.Int("token-cap")),
		LoadMax: float64(c.Int("load-max")), LoadWidth: c.Int("load-width"), PauseOn: c.Str("pause-on"), RefuseGo: c.Bool("refuse-go")}
	var rowRules atomic.Pointer[friend.LaneRules]
	rules := func() friend.LaneRules {
		if r := rowRules.Load(); r != nil {
			return flagRules.Over(*r)
		}
		return flagRules
	}
	// the lane's go and gofmt refuse: this binary by symlink first on the lane's PATH (friend.GoShims)
	shimDir := filepath.Join(state, friend.ShimDirName)
	var shimOnce sync.Once
	var shimErr error
	shimmed := friend.ShimExec(walledRaw, shimDir, w.getenv("PATH"))
	walled := func(ctx context.Context, d, prog string, args []string, stdin string) (string, int, error) {
		if !rules().RefuseGo {
			return walledRaw(ctx, d, prog, args, stdin)
		}
		shimOnce.Do(func() {
			bin, err := w.binary()
			if err == nil {
				err = friend.GoShims(shimDir, bin)
			}
			shimErr = err
		})
		if shimErr != nil {
			return "", 0, fmt.Errorf("the go refusal shims cannot be made, so no lane runs: %w", shimErr)
		}
		return shimmed(ctx, d, prog, args, stdin)
	}
	// her harness's limit: every command's output read for it, her turns held while she is
	// down and a wake after the reset (friend.Limits); its hooks are set once record is
	fl := &friend.Limits{Now: w.now, Nonce: w.random, Harness: c.Str("harness"), Rest: c.Dur("limit-rest")}
	// every harness's credit and quota refusal, one table (limit.go): each lane's output and
	// the runner log are read, and a hit the harness's own wording did not name is handed to
	// the same limit path (fl.Refuse); three lanes failing alike with a wording no row knows
	// is one judgment (RefusalWatch). The harness's own wording is read first, so a hit both
	// readers know is one down.
	rw := &RefusalWatch{Harness: c.Str("harness"), Now: w.now}
	limitWatch := fl.Watch(walled)
	watched := func(ctx context.Context, d, prog string, args []string, stdin string) (string, int, error) {
		out, exit, err := limitWatch(ctx, d, prog, args, stdin)
		if exit != 0 || err != nil {
			if _, _, alreadyHeld := fl.Limited(); !alreadyHeld {
				rw.Observe(LaneText{Stdout: out, Log: friend.RunnerLog(dir)})
			}
		}
		return out, exit, err
	}
	deliver, err := friend.SelectDeliverer(name, c.Str("harness"), dir, c.Str("session"), c.Str("adapter"), c.Str("delivery-dir"), watched, c.Stdout)
	if err == nil {
		err = friend.TmuxFor(deliver, name, state) // harness tmux: the session and prompt host saved
	}
	if err != nil {
		return tool.Refuse(err.Error()) // the skeleton renders a refusal with the verb's token, on stderr
	}
	if friend.RunsCards(c.Str("harness")) {
		deliver = friend.NewClaude(name, dir, watched, c.Stdout) // a card a process: the adapter with a lane
	}
	// a harness whose session queues what is delivered (Antigravity's mailbox): every delivery
	// goes in at once, and the daemon follows the conversation that reads it (friend.Mailbox)
	var mailbox friend.Mailbox
	if ag, ok := deliver.(*friend.Antigravity); ok {
		ag.Now, ag.State = w.now, state // the ledger of every delivery lives in her state directory
		mailbox = ag
	}
	// a harness with its own queue (codex: the open chat's): its length on the status
	var queued func() (int, bool)
	if cx, ok := deliver.(*friend.Codex); ok {
		cx.Now, queued = w.now, cx.Queued
	}
	// a harness nothing pushes into is refused at the start (friend.PushProof), a dry run alike
	dry := c.DryRun()
	// a harness that runs each card as a process of its own (friend.CardRunner) has no session to
	// push a turn into: its session check goes in by the folder instead (friend.FolderCheck), and
	// its beat is never held back on the answer (docs/SPEC-FRIEND.md, The push proof)
	_, perCard := deliver.(friend.CardRunner)
	if o := undriven(c.Str("harness"), dir, "the daemon did not start"); o != nil && !perCard {
		return o
	}
	if dry {
		// the daemon it would run, its flags checked: no store opened, no beat, no record
		fmt.Fprintf(c.Stdout, "RUN DRY-RUN as=%s harness=%s dir=%s state=%s redis=%s; nothing was started\n", name, c.Str("harness"), dir, state, addr)
		return tool.Exit(0)
	}
	if oc, ok := deliver.(*friend.OpenCode); ok {
		// the installed opencode, read once: a run verb lacking a flag the adapter passes is a
		// refusal naming the version, never an exit 1 on every delivery (the finding of 2026-10-06)
		// (through the wall, outside the limit watch: a read of the CLI is no turn)
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := (&friend.OpenCode{Dir: oc.Dir, Program: oc.Program, Run: walled}).CheckRun(cctx)
		ccancel()
		if err != nil {
			return tool.Refuse(err.Error())
		}
		// the friend's directory as her tools name it: the symlink in the home directory too
		oc.Allow = []string{}
		if alias := filepath.Join(w.home, name+"-working"); fileThere(alias) {
			oc.Allow = append(oc.Allow, alias)
		}
		deliver = &friend.OpenCodePriced{OpenCode: oc, Friend: name, TokenCap: func() int64 { return rowTokenCap.Load() }} // every lane run priced from her own session record, and capped by her row's token_cap
	}
	// her row, as her beat last answered it (nova-sprint friend beat: row_mode, row_width, row_config_dir)
	rowMode, rowWidth := "", 0
	var rowMu sync.Mutex          // the cadence beat writes the row while the delivery loop reads it
	var rowReadSlots atomic.Int64 // her row's read slots as her beat last answered; friend.DefaultReadSlots until it says
	rowReadSlots.Store(friend.DefaultReadSlots)
	// her row's lane caps by tier as her beat last answered; friend.DefaultLaneCaps until it says
	var rowLaneCaps atomic.Pointer[map[string]time.Duration]
	if cl, ok := deliver.(*friend.Claude); ok {
		cl.Friend, cl.Now = name, w.now // every run's cost and limit on the record, its reset read on her clock
		cl.TokenCap = func() int64 { return rowTokenCap.Load() }
		cl.ConfigDir = func() string {
			if d := c.Str("config-dir"); d != "" {
				return d // the override
			}
			if d := rowConfigDir.Load(); d != nil {
				return *d
			}
			return ""
		}
	}
	var recordMu sync.Mutex // the beat and delivery loop share one ordered native record
	record := func(line string) {
		recordMu.Lock()
		defer recordMu.Unlock()
		fmt.Fprintln(c.Stdout, "RUN "+line)
		_ = friend.Record(state, line) // ignored: the line is on stdout (launchd's log) whatever the volume does
	}
	// when the sprint server last took her session's answer to a check as its proof (the up
	// beat's answer says proved=<nonce>): status proof_sent, check proof=sent
	var sent atomic.Pointer[time.Time]
	// the presence file says a limit while there is one, whatever the session check saw; the
	// harness's process never decides it (friend.HarnessWatch is advisory)
	writePresence := func(p friend.PresenceStatus) error {
		if until, reason, limited := fl.Limited(); limited {
			p.Presence, p.Reason = friend.PresenceDown, "harness limit until "+until.UTC().Format(time.RFC3339)+": "+reason
		}
		return friend.WritePresence(state, p)
	}
	// what her lanes have cost and the limit they last read (friend.Spender), said on the beat
	// when it changed since the last one said it (docs/SPEC-FRIEND.md, the Claude lanes)
	var spent atomic.Pointer[string]
	saySpend := func() {
		s, ok := deliver.(friend.Spender)
		if !ok {
			return
		}
		line := s.SpendLine()
		if last := spent.Load(); line == "" || (last != nil && *last == line) {
			return
		}
		spent.Store(&line)
		record(w.now().UTC().Format(time.RFC3339) + " " + line)
	}
	// the seat (else --coordinator) is told of each limit and each wake; set once the store is open
	tellSeat := func(subject, body string) {}
	fl.Down = func(until time.Time, reason string) {
		record(w.now().UTC().Format(time.RFC3339) + " limit: down until " + until.UTC().Format(time.RFC3339) + ": " + reason + "; turns and beats held until then, then a wake")
		if err := writePresence(friend.PresenceStatus{Friend: name, At: w.now()}); err != nil {
			record(w.now().UTC().Format(time.RFC3339) + " limit: the presence file: " + err.Error())
		}
		tellSeat(friend.LimitDownText(name, until, reason))
	}
	fl.Unread = func(text string) {
		record(w.now().UTC().Format(time.RFC3339) + " limit: the message names no reset I can read; held, a judgment to the seat: " + text)
		tellSeat(friend.LimitUnreadText(name, c.Dur("limit-rest"), text))
	}
	fl.Up = func(nonce string) {
		record(w.now().UTC().Format(time.RFC3339) + " limit: woken: the session answered " + nonce + " after the reset")
		tellSeat(friend.LimitUpText(name))
	}
	if stateWhy != "" {
		record(w.now().UTC().Format(time.RFC3339) + " state: " + friend.StateDirIn(dir) + " refused (" + stateWhy + "); the state is in " + state + ", outside the directory a sandboxed session may write: give --state-dir")
	}
	if line := friend.PlistDriftLine(w.readPlist(friend.Agent{Friend: name, Home: w.home}.PlistPath()), w.argv); line != "" {
		record(w.now().UTC().Format(time.RFC3339) + " " + line)
	}
	ctx, stop := w.signals(context.Background())
	defer stop()
	st, closeStore := w.openUntil(ctx, addr, record)
	if st == nil {
		return tool.Exit(0) // a signal while the store was down
	}
	defer closeStore()
	// the push proof is a state, never an exit (docs/SPEC-FRIEND.md, The push proof): the
	// daemon starts, its first SESSION CHECK goes in at once (the presence's own), and it
	// delivers nothing into the session until the session answers it; a check the last run
	// queued and never saw answered keeps its nonce, so the session's late answer proves it
	if perCard {
		record(w.now().UTC().Format(time.RFC3339) + " push proof: owed by the folder: " + c.Str("harness") + " runs each card as a process of its own, so the session check goes in as " + friend.SessionCheckFile(dir, "<nonce>") + "; a live session answers it with nova-friend pong --as " + name + " --nonce <nonce> --state-dir " + state + ", and the beat carries the answer; with no live session her cards' finishes are her presence, and nothing is held back")
	} else {
		record(w.now().UTC().Format(time.RFC3339) + " push proof: pending: the first session check goes into the " + c.Str("harness") + " session now; nothing is delivered until the session answers it")
	}
	// the seat the last ping named, from the daemon's status: whom the session check's answer goes to
	var seatMu sync.Mutex
	seat := ""
	answerTo := func() string {
		seatMu.Lock()
		defer seatMu.Unlock()
		if seat != "" {
			return seat
		}
		return c.Str("coordinator")
	}
	// presence is the session's, never the daemon's (docs/SPEC-FRIEND.md, presence); each
	// presence saved is also the bus's push proof, without which nova-bus refuses this name
	// as deaf (docs/SPEC-BUS.md, bus-requires-inbox-push-proof)
	prover := &friend.PushProver{Friend: name, Harness: c.Str("harness"), Store: st, Now: w.now, Record: record}
	keep := ""
	if pr, found, err := friend.ReadPresence(state); err == nil && found {
		keep = pr.Nonce // the last run's check, never answered: its answer still proves the push
	}
	sc := &friend.SessionCheck{
		Friend: name, Store: st, Now: w.now, Nonce: w.random, Record: record, Keep: keep,
		Run:  fmt.Sprintf("r%d", w.now().Unix()), // this run, its generation: an answer proves only to the run that asked
		Go:   w.checkGo,
		Save: prover.Save(writePresence),
		Text: func(nonce string) string {
			return friend.SessionCheckText(nonce, w.pongCommand(name, nonce, state, c.Str("redis"), dir), answerTo())
		},
	}
	laneDeliver := sc.Gate(fl.Gate(deliver)) // the daemon's: her turns, or her card runner
	sc.Deliver = laneDeliver
	if perCard {
		// the check goes in by the folder, ungated: her lanes hold no turn of the session
		sc.Deliver = &friend.FolderCheck{Friend: name, Dir: dir}
	}
	prover.Deliver = sc.Deliver
	tellSeat = func(subject, body string) {
		to := answerTo()
		if to == "" {
			record(w.now().UTC().Format(time.RFC3339) + " limit: no seat or coordinator to tell: " + subject)
			return
		}
		if _, err := (&bus.Bus{Store: sc.DaemonStore()}).Send(ctx, bus.Message{From: name, To: []string{to}, Subject: subject, Body: body}); err != nil {
			record(w.now().UTC().Format(time.RFC3339) + " limit: telling " + to + " failed: " + err.Error() + ": " + subject)
		}
	}
	// a refusal the row knows and the harness's own wording did not: the same limit path
	// (status, the gate, the down beat, the seat), and the down verb so her begun cards come
	// back; three lanes alike with a wording no row knows is one judgment to the seat
	rw.Down = func(r Refusal) {
		fl.Refuse(r.Kind, DownReason(r), r.Until)
		if w.down == nil {
			record(w.now().UTC().Format(time.RFC3339) + " credit refusal: no down verb to send: " + DownReason(r))
			return
		}
		if err := w.down(ctx, server, DownArgv(name, r)); err != nil {
			record(w.now().UTC().Format(time.RFC3339) + " credit refusal: friend down: " + err.Error())
			tellSeat("friend "+name+": the down verb was refused", DownReason(r)+"\nnova-sprint friend down was refused: "+err.Error()+"\n")
		}
	}
	rw.Judge = func(text string) {
		record(w.now().UTC().Format(time.RFC3339) + " limit: " + text)
		tellSeat(friend.LimitAlikeText(name, text))
	}
	// the down her lanes' harness faults owe her row (Daemon.FaultDown): her beat says it until it passes
	type faultHold struct {
		until  time.Time
		reason string
	}
	var faultDown atomic.Pointer[faultHold]
	stager := w.stager(dir)
	// the machine's word, read off each beat's answer (friend.ParseMachine), and the
	// stop-returns the lanes owe, carried on each beat (stop.go)
	var machineStopped atomic.Bool
	owedStopReturns := func() int { return 0 }
	d := &friend.Daemon{
		Friend: name, Harness: c.Str("harness"), Dir: dir, Width: c.Int("width"), Version: buildinfo.Version(version), Binary: self,
		MachineStopped: machineStopped.Load,
		StopReturn: func(ctx context.Context, argv []string) error {
			if w.finish == nil {
				return errors.New("this world sends no stop-return")
			}
			return w.finish(ctx, server, argv)
		},
		Store: sc.DaemonStore(), Deliver: laneDeliver, Now: w.now, Pause: w.sleep, StepBeatForTests: w.stepBeat,
		Sent: func() time.Time {
			if at := sent.Load(); at != nil {
				return *at
			}
			return time.Time{}
		},
		Limited: func() (string, time.Time, bool) {
			until, _, limited := fl.Limited()
			return fl.Kind(), until, limited
		},
		SilentStop: c.Dur("silent-stop"), BrokenAfter: c.Int("broken-after"), Coordinator: c.Str("coordinator"),
		Mailbox: mailbox,
		Session: func() string {
			if mailbox != nil {
				return mailbox.Live()
			}
			return c.Str("session")
		},
		Queued: queued,
		Activity: func() time.Time {
			return friend.NewestWrite(os.DirFS(dir), friend.ActivityRoots, w.now, friend.DefaultActivityLimits)
		},
		// the session check's and the limits' wrappers take a beat of ctx alone; the daemon's
		// beat carries the session's last activity, closed over here (fold of 2026-10-05)
		Beat: func(ctx context.Context, active time.Time) error {
			// up or down, held back or not: the beat's record says what the lanes cost
			saySpend()
			held := sc.Beat // the session's answer holds the beat back
			if perCard {
				// a per-card harness's beat is never held back: her cards' finishes are her presence
				// at the server; the check still steps (by the folder) and its answer rides the beat
				held = sc.BeatAlways
			}
			// the owner, 2026-10-05 ~9:30 AM ET: "there is no value in things that are answered
			// just by the daemon": the words are the check asked and the check the session
			// answered that no beat has said, for a per-card harness too (the folder's answer)
			words := sc.Words
			up := func(ctx context.Context) error {
				said := words()
				said.StopReturns = owedStopReturns()
				answer, err := w.beat(ctx, server, name, active, said)
				if err == nil {
					sc.Said(said)
					if said.Pong != "" && strings.Contains(answer, " proved="+said.Pong) {
						at := w.now()
						sent.Store(&at) // the server took her session's answer as its proof
					}
				}
				if st, ok := friend.ParseMachine(answer); err == nil && ok {
					machineStopped.Store(st == friend.MachineStoppedWord) // STOPPED cancels the lanes (stop.go)
				}
				if m, wd, ok := friend.ParseRow(answer); err == nil && ok {
					rowMu.Lock()
					rowMode, rowWidth = m, wd
					rowMu.Unlock()
					dir := friend.RowConfigDir(answer)
					rowConfigDir.Store(&dir)
				}
				if n, ok := friend.ParseReadSlots(answer); err == nil && ok {
					rowReadSlots.Store(int64(n))
				}
				if p, ok := friend.ParseProfile(answer); err == nil && ok {
					rowProfile.Store(&p)
				}
				if caps, ok := friend.ParseLaneCaps(answer); err == nil && ok {
					rowLaneCaps.Store(&caps)
				}
				if err == nil {
					if n, ok := friend.TokenCapOf(answer); ok {
						rowTokenCap.Store(n)
					}
					r := friend.LaneRulesOf(answer)
					rowRules.Store(&r)
				}
				return err
			}
			if w.beatDown == nil {
				return held(fl.Beat(up))(ctx) // no down beat: held back while she is at her limit
			}
			// while her harness is at its limit her beat says down with the until and the
			// reason (limits-mean-down-w-r5.w1~15), the session's check stepped as before,
			// ahead of the look; the inner check is the last before the up beat, so a limit
			// seen during the step is never beaten up
			down := func(ctx context.Context, until time.Time, reason string) error {
				said := words()
				err := w.beatDown(ctx, server, name, active, until, reason, said)
				if err == nil {
					sc.Said(said)
				}
				return err
			}
			if !perCard {
				// while her session is down her beat says so, with the check's nonce and why; a
				// limit seen during the step is the reason first, with its reset
				sessionDown := func(ctx context.Context, until time.Time, reason string) error {
					if u, r, limited := fl.Limited(); limited {
						until, reason = u, "harness limit: "+r
					}
					return down(ctx, until, reason)
				}
				held = func(beat func(context.Context) error) func(context.Context) error {
					return sc.BeatOr(beat, sessionDown)
				}
			}
			// a provider failure paused her lanes: her beat says her down with its exact
			// message until a person clears the marker (nova-friend resume; friend.PauseBeat)
			if marker := friend.ReadPause(state); marker != "" {
				until, reason := friend.PauseBeat(marker, c.Str("model"), w.now())
				return down(ctx, until, reason)
			}
			// her lanes hit the same harness fault three times in ten minutes: her beat says
			// her down with the fault until it passes (friend.FaultWatch), then up again
			if h := faultDown.Load(); h != nil && w.now().Before(h.until) {
				return down(ctx, h.until, h.reason)
			}
			if _, _, limited := fl.Limited(); limited && !perCard {
				sc.Step(ctx)
			}
			return fl.BeatOrDown(held(fl.BeatOrDown(up, down)), down)(ctx)
		},
		Row: func() (string, int) {
			rowMu.Lock()
			defer rowMu.Unlock()
			if m := c.Str("mode"); m != "" {
				return m, rowWidth // the override, for a test
			}
			return rowMode, rowWidth
		},
		LoadLanes: func() (friend.LaneState, error) { return friend.ReadLanes(state) },
		Sprint:    w.sprintAsk(server),
		Rules:     rules,
		Model:     c.Str("model"),
		Load:      w.load1(),
		LaneHold:  func() string { return friend.ReadPause(state) },
		LaneHoldDown: func(_ context.Context, message string) error {
			return friend.WritePause(state, message, w.now()) // her next beat says her down with it
		},
		FaultDown: func(until time.Time, reason string) {
			faultDown.Store(&faultHold{until: until, reason: reason}) // her next beat says her down with it
		},
		ReadSlots: func() int { return int(rowReadSlots.Load()) },
		LaneCaps: func() map[string]time.Duration {
			if caps := rowLaneCaps.Load(); caps != nil {
				return *caps
			}
			return nil
		},
		ReadModel: func(tier string) string {
			if c.Str("harness") == "claude" {
				return friend.ReadModels[tier] // a tier with none is the account's own model
			}
			return ""
		},
		Progress: func(ctx context.Context, cards []friend.Card) error {
			if w.progress == nil {
				return nil // a world that sends none (a test's)
			}
			for _, argv := range friend.ProgressArgv(name, cards) {
				if err := w.progress(ctx, server, argv); err != nil {
					return err
				}
			}
			return nil
		},
		SaveLanes:   func(s friend.LaneState) error { return friend.WriteLanes(state, s) },
		Held:        w.held(name, server),
		Seat:        w.seat(server),
		Stage:       stager.stage(),
		Prune:       stager.prune(),
		PruneLanded: w.pruneLanded(dir, server),
		Tip:         w.tip,
		Finish: func(ctx context.Context, argv []string) error {
			if w.finish == nil {
				return errors.New("this world sends no finish") // a test's: friend sync reads the lane's REPORT.md
			}
			return w.finish(ctx, server, argv)
		},
		CardDone: func(card, to string) string {
			busBin, err := w.lookPath("nova-bus")
			if err != nil {
				busBin = "nova-bus" // ignored: the name on PATH stands in when it is not found
			}
			if to == "" {
				to = "<the coordinator>"
			}
			return fmt.Sprintf(`%s send --as %s --to %s --subject "card %s done" --body "<the first line of your REPORT.md>" --redis %s`, busBin, name, to, card, c.Str("redis"))
		},
		Record: record,
		Pong:   func() (friend.Pong, bool, error) { return friend.ReadPong(state) },
		Status: func(s friend.Status) error {
			seatMu.Lock()
			seat = s.Seat
			seatMu.Unlock()
			return friend.WriteStatus(state, s)
		},
		PongCommand: func(nonce string) string {
			return w.pongCommand(name, nonce, state, c.Str("redis"), dir)
		},
	}
	owedStopReturns = d.OwedStopReturns
	if w.holders != nil {
		d.Holders = func(ctx context.Context) (map[string]string, error) { return w.holders(ctx, server) }
	}
	if !perCard {
		d.Proof = sc.Proof // nothing goes into the session until it answers its check
	}
	if c.Str("harness") == "opencode" {
		db := c.Str("db")
		if w.sqlite != nil {
			d.Tokens = func(ctx context.Context, session string) (friend.LaneTokens, error) {
				return friend.TokensFromOpenCode(ctx, w.sqlite, db, session)
			}
		}
		d.Route = w.route(server, c.Str("model"))
	}
	watch := friend.WatchHarness(d, deliver)
	if w.alive != nil {
		watch.Alive = w.alive
	}
	if err := d.Run(ctx); err != nil {
		fmt.Fprintln(c.Stderr, "RUN FAIL: "+err.Error())
		return tool.Exit(1)
	}
	return tool.Exit(0)
}

// beatVerb is the daemon's beat on its own: one "friend beat <me>" to the
// sprint server, the same call world.beat makes for the daemon's loop each
// time round (docs/SPEC-FRIEND.md, the loop). The agent install writes
// runs the daemon, and the daemon beats already while its session is alive,
// so no agent of the beat's own is written (the hand plists are retired);
// the verb is the canary, and a server that does not answer is exit 2.
func (w world) beatVerb(c *tool.Call) *tool.Out {
	name, server := c.Str("as"), c.Str("server")
	if c.DryRun() {
		return tool.Done().Fact("as", name).Fact("server", server).Fact("dry_run", true).Note("dry run: no beat sent; it would send friend beat " + name + " to " + server)
	}
	if _, err := w.beat(context.Background(), server, name, time.Time{}, friend.BeatWords{}); err != nil {
		return tool.Refuse("the beat was not taken: " + err.Error())
	}
	return tool.Done().Fact("as", name).Fact("server", server)
}

// load1 is the machine's one-minute load, read at most every 5 seconds: /proc/loadavg, else
// sysctl vm.loadavg; 0 (no load rule can fire) when neither answers.
func (w world) load1() func() float64 {
	var mu sync.Mutex
	var at time.Time
	var last float64
	return func() float64 {
		mu.Lock()
		defer mu.Unlock()
		if now := w.now(); at.IsZero() || now.Sub(at) >= 5*time.Second {
			at = now
			out := ""
			if raw, err := os.ReadFile("/proc/loadavg"); err == nil {
				out = string(raw)
			} else if o, exit, err := w.exec(context.Background(), "/", "sysctl", []string{"-n", "vm.loadavg"}, ""); err == nil && exit == 0 {
				out = o
			}
			last, _ = friend.Load1Of(out)
		}
		return last
	}
}

// route is the store's route row for the friend's provider/model, asked of the sprint server
// once it answers (nova-sprint routes --json); not found with no --model or no server.
func (w world) route(server, model string) func() friend.RoutePrice {
	var mu sync.Mutex
	var rp friend.RoutePrice
	provider, id, _ := strings.Cut(model, "/")
	return func() friend.RoutePrice {
		mu.Lock()
		defer mu.Unlock()
		if rp.Found || w.cards == nil || id == "" {
			return rp
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if out, err := w.cards(ctx, server, []string{"routes", "--json"}); err == nil {
			rp = friend.RoutePriceOf(out, provider, id)
		}
		return rp
	}
}

// sprintAsk is the daemon's Sprint: one verb to the sprint server, what it printed; nil in a
// world that asks none (a test's), which runs no reads.
func (w world) sprintAsk(server string) func(context.Context, []string) (string, error) {
	if w.cards == nil {
		return nil
	}
	return func(ctx context.Context, argv []string) (string, error) { return w.cards(ctx, server, argv) }
}

// held is the daemon's Held: the cards on her row, asked of the sprint server each loop, its
// worker view while it refuses friend cards (friend.HeldVia); nil in a world that asks none (a
// test's), which leaves her inbox to friend sync.
func (w world) held(name, server string) func(context.Context) (friend.Row, error) {
	if w.cards == nil {
		return nil
	}
	var view func(ctx context.Context) (string, error)
	if w.view != nil {
		view = func(ctx context.Context) (string, error) { return w.view(ctx, server, name) }
	}
	now := w.now
	if now == nil {
		now = time.Now
	}
	return friend.HeldVia(name, func(ctx context.Context, argv []string) (string, error) { return w.cards(ctx, server, argv) }, view, now)
}

// seat is the daemon's Seat: the coordinator seat holder as the sprint server's coordinator
// view says it (coordinatorFriends); nil in a world that reads none (a test's), which leaves
// the seat unknown, so every message is delivered quoted.
func (w world) seat(server string) func(context.Context) (string, error) {
	if w.friends == nil {
		return nil
	}
	return func(ctx context.Context) (string, error) {
		_, seat, err := w.friends(ctx, server)
		return seat, err
	}
}

// stager is the daemon's one Stager: every held work card's job staged under her working
// directory (jobs/<job>/repo, a worktree of the repository's mirror, and its JOB.md), and the
// finished jobs past friend.FinishedJobsKept pruned, both under the mirror's one lock; nil in a
// world that stages none or asks no held cards.
func (w world) stager(dir string) daemonStager {
	if w.stage == nil || w.cards == nil || dir == "" {
		return daemonStager{}
	}
	return daemonStager{w.stage(dir)}
}

// daemonStager is a Stager as the daemon's Stage and Prune, nil for none.
type daemonStager struct{ s *friend.Stager }

func (d daemonStager) stage() func(ctx context.Context, p friend.Packet) (string, error) {
	if d.s == nil {
		return nil
	}
	return d.s.Stage
}

func (d daemonStager) prune() func(ctx context.Context, live map[string]bool) ([]string, error) {
	if d.s == nil {
		return nil
	}
	return func(ctx context.Context, live map[string]bool) ([]string, error) {
		return d.s.Prune(ctx, live, friend.FinishedJobsKept)
	}
}

// landedStandingFor is how long one card's standing is reused by the landed removal. The
// cleanup runs every loop (a second); a card that lands or drops is found within this bound,
// far inside the one-hour grace, and an open card costs the sprint server no read each second.
const landedStandingFor = time.Minute

// pruneLanded is the daemon's PruneLanded: gc's class landed over her own jobs/, so a landed
// or dropped card's job is removed whole whatever its git state, a clone or a worktree
// (sprint.PruneLanded, docs/SPEC-FRIEND.md, the prune pass; the daemon's prune pass and the
// one-shot reap are the one cleanup). The daemon is a store client and reads no card's state
// itself: standing asks the sprint server for one card's record, once per id a pass, and a
// job that is live is answered open without a read. Nil in a world that asks no server.
func (w world) pruneLanded(dir, server string) func(context.Context, map[string]bool) ([]string, error) {
	if w.cards == nil || dir == "" {
		return nil
	}
	jobs := filepath.Join(dir, friend.JobsDir)
	now := w.now
	if now == nil {
		now = time.Now
	}
	type standingAt struct {
		card *sprint.Card
		at   time.Time
	}
	cache := map[string]standingAt{}
	return func(ctx context.Context, live map[string]bool) ([]string, error) {
		tick := now()
		liveIDs := map[string]bool{}
		for job := range live {
			if id, _, _ := strings.Cut(job, "~"); id != "" {
				liveIDs[id] = true
			}
		}
		standing := func(id string) *sprint.Card {
			if liveIDs[id] {
				return &sprint.Card{ID: id, Col: sprint.Working}
			}
			if c, ok := cache[id]; ok && tick.Sub(c.at) < landedStandingFor {
				return c.card
			}
			c := w.cardStanding(ctx, server, id)
			cache[id] = standingAt{card: c, at: tick}
			return c
		}
		res := sprint.PruneLanded(jobs, tick, false, standing)
		out := make([]string, 0, len(res.Removed))
		for _, p := range res.Removed {
			out = append(out, filepath.Base(p))
		}
		if res.Failed > 0 {
			return out, fmt.Errorf("the landed removal failed on %d paths", res.Failed)
		}
		return out, nil
	}
}

// cardStanding is one card's record as the sprint server answers card <id> --json: its
// primary card, nil when the server knows none or does not answer. A dropped card is a
// record the server's card read may not see; gc, which reads the sprint's store, takes that
// one (docs/SPEC-SPRINT.md, the disk guard's volumes).
func (w world) cardStanding(ctx context.Context, server, id string) *sprint.Card {
	out, err := w.cards(ctx, server, []string{"card", id, "--json"})
	if err != nil {
		return nil
	}
	var view struct {
		Primary *sprint.Card
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &view); err != nil {
		return nil
	}
	return view.Primary
}

func (w world) agent(c *tool.Call) (friend.Agent, error) {
	bin, err := w.binary()
	if err != nil {
		return friend.Agent{}, fmt.Errorf("this binary's path: %v", err)
	}
	name := c.Str("as")
	log := c.Str("launchd-log")
	if log == "" {
		prefix := "nova-friend-"
		if c.Bool("notifications-only") {
			prefix = "nova-friend-notifications-"
		}
		log = filepath.Join(w.home, "Library", "Logs", prefix+name+".log")
	}
	a := friend.Agent{
		Friend: name, Harness: c.Str("harness"), Dir: c.Str("dir"), Session: c.Str("session"), Adapter: c.Str("adapter"), DeliveryDir: c.Str("delivery-dir"), StateDir: c.Str("state-dir"), Width: c.Int("width"),
		Binary: bin, Copy: w.copy, Redis: c.Str("redis"), Server: c.Str("server"), Home: w.home, Path: w.getenv("PATH"), LaunchdLog: log,
		Secrets: secretNames(c.Str("secrets")), Seat: c.Str("seat"),
		Coordinator: c.Str("coordinator"), SilentStop: c.Dur("silent-stop"), BrokenAfter: c.Int("broken-after"),
		NotificationsOnly: c.Bool("notifications-only"), NotifyKinds: c.Str("notify-kinds"), NotifyWindow: c.Dur("notify-window"),
	}
	if a.Harness == "claude" {
		a.ConfigDir = c.Str("config-dir")
	}
	if len(a.Secrets) > 0 {
		for _, p := range []struct {
			name string
			to   *string
		}{{"nova-secrets", &a.SecretsTool}, {"sops", &a.Sops}} {
			found, err := w.lookPath(p.name)
			if err != nil {
				return friend.Agent{}, fmt.Errorf("--secrets wraps the daemon in nova-secrets exec, and %s is not on PATH: %v", p.name, err)
			}
			*p.to = found
		}
	}
	return a, nil
}

func (w world) install(c *tool.Call) *tool.Out {
	dry := c.DryRun()
	c.Want("redis", "the bus store's Redis address, host:port (or "+RedisEnv+"), written into the agent")
	if o := c.Refused(); o != nil {
		return o
	}
	// a harness nothing pushes into is refused before anything is written (friend.PushProof); one that
	// runs each card as a process of its own (claude) proves by the folder (friend.FolderCheck) and is not
	if !friend.RunsCards(c.Str("harness")) {
		if o := undriven(c.Str("harness"), c.Str("dir"), "nothing was written or loaded"); o != nil {
			return o
		}
	}
	a, err := w.agent(c)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	if a.NotificationsOnly {
		return w.installNotifications(c, a)
	}
	h := w.harnessSettings(c)
	if a.Harness == "grok" {
		a.Session = h.WakePath() // the wake file install made is the one the agent names
	}
	src := a.Binary
	placed, copy, err := friend.PlanBinary(src, a.Home)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	if dry {
		plan, err := h.Plan()
		if err != nil {
			return tool.Refuse(err.Error())
		}
		a.Binary = placed
		o := tool.Done().Fact("label", a.Label()).Fact("plist", a.PlistPath()).Fact("launchd_log", a.LaunchdLog)
		for _, s := range plan {
			o.Item("plan", "command", tool.Text("write "+s.File+" "+s.Name+"="+s.Want))
		}
		if copy {
			o.Item("plan", "command", tool.Text("copy "+src+" "+placed))
		}
		o.Item("plan", "command", tool.Text("write "+a.PlistPath())).
			Item("plan", "command", tool.Text(fmt.Sprintf("launchctl bootout gui/%d/%s", w.uid, a.Label()))).
			Item("plan", "command", tool.Text(fmt.Sprintf("launchctl bootstrap gui/%d %s", w.uid, a.PlistPath()))).
			Note("the agent runs: " + a.Said())
		return noteClaudeWait(noteGrokMonitor(o, a.Harness, a.Session), a.Harness, a.Friend, w.claudeWake(c, a.Friend))
	}
	wrote, err := h.Write()
	if err != nil {
		return tool.Refuse("the harness's settings: " + err.Error())
	}
	a.Sleep = func(d time.Duration) { w.sleep(context.Background(), d) }
	path, ran, err := friend.Install(context.Background(), a, w.uid, w.launchctl, func(p string, data []byte) error {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		return os.WriteFile(p, data, 0o644)
	}, func() { w.sleep(context.Background(), time.Second) })
	if err != nil && errors.Is(err, friend.ErrBinaryOnRemovableVolume) {
		return tool.Refuse(err.Error())
	}
	o := tool.Done().Fact("label", a.Label()).Fact("plist", path).Fact("launchd_log", a.LaunchdLog)
	for _, s := range wrote {
		o.Item("wrote", "harness", s.Harness, "file", s.File, "name", s.Name, "value", tool.Text(s.Want))
	}
	for _, r := range ran {
		o.Item("ran", "command", tool.Text(r))
	}
	if err != nil {
		return noteClaudeWait(noteGrokMonitor(tool.Fail(err.Error()).Fact("plist", path), a.Harness, a.Session), a.Harness, a.Friend, w.claudeWake(c, a.Friend))
	}
	// the delivery check, once, against the session the agent now serves; a fail is said, never undone
	state := c.Str("state-dir") // where the agent just started keeps its files, as its run will choose them
	if state == "" {
		state, _ = friend.DaemonStateDir(w.home, a.Dir, a.Friend, func(d string) error { return dirThere(filepath.Dir(d)) })
	}
	res, remedy, cannot, refusal := w.deliveryCheck(c, a.Friend, a.Harness, a.Dir, a.Session, state, "", c.Dur("within"))
	if cannot {
		// a session the adapter cannot drive: the agent would refuse at every start, so it goes again
		undone, uerr := friend.Uninstall(context.Background(), a, w.uid, w.launchctl, os.Remove)
		why := res.Why + "; the agent was booted out and its plist removed (" + strings.Join(undone, "; ") + ")"
		if uerr != nil {
			why = res.Why + "; booting the agent out failed: " + uerr.Error()
		}
		r := tool.Refuse(why)
		r.Remedy = remedy
		return r
	}
	if refusal != "" {
		o.Note("check: not run: " + refusal)
	} else {
		o.Note("check: " + res.Line())
	}
	o = noteClaudeWait(noteGrokMonitor(o.Note("check it: nova-friend status --as "+a.Friend+" --dir "+a.Dir), a.Harness, a.Session), a.Harness, a.Friend, w.claudeWake(c, a.Friend))
	if friend.RunsCards(a.Harness) {
		o.Note(friend.FolderCheckLine(a.Friend, a.Dir, state)) // where her session check lands, and the answer
	}
	return o
}

// undriven is the refusal of a harness whose adapter has no deliver command
// (friend.Undriven), with the adapter card as its remedy and what was left
// undone; nil for a harness that has one, or an unknown one (the flag check
// names it).
func undriven(harness, dir, undone string) *tool.Out {
	d, err := friend.NewDeliverer(harness, dir, "", nil, nil)
	if err != nil {
		return nil
	}
	why, remedy, ok := friend.Undriven(d, harness)
	if !ok {
		return nil
	}
	o := tool.Refuse(why + "; " + undone)
	o.Remedy = remedy
	return o
}

// harnessSettings is what install writes into the friend's harness and
// check --settings compares (internal/friend/settings.go).
func (w world) harnessSettings(c *tool.Call) friend.HarnessSettings {
	h := friend.HarnessSettings{Harness: c.Str("harness"), Friend: c.Str("as"), Dir: c.Str("dir"), Home: w.home, StateDir: c.Str("state-dir"),
		ConfigDir: c.Str("config-dir"), Model: c.Str("model"), CodexHome: w.getenv("CODEX_HOME"), DSHHome: w.getenv("DSH_HOME"), FS: w.settings}
	if h.Harness == "grok" {
		h.Wake = c.Str("session") // for grok, --session names the wake file
	}
	return h
}

// checkTo is whom the check's pong goes to: to, else the seat the daemon's
// status names, else the friend itself.
func (w world) checkTo(to, name, state string) string {
	if to != "" {
		return to
	}
	if s, found, err := friend.ReadStatus(state); err == nil && found && s.Seat != "" {
		return s.Seat
	}
	return name
}

// pongCommand is the pong line a session check carries, as the daemon's
// own check carries it: this binary's pong verb, the friend's state
// directory and store.
func (w world) pongCommand(name, nonce, state, redis, dir string) string {
	bin, err := w.binary()
	if err != nil {
		bin = "nova-friend" // ignored: the name on PATH stands in when this binary's path is unknown
	}
	return fmt.Sprintf("%s pong --as %s --nonce %s --state-dir %s --redis %s --dir %s",
		oneline.ShellWord(bin), oneline.ShellWord(name), oneline.ShellWord(nonce),
		oneline.ShellWord(state), oneline.ShellWord(redis), oneline.ShellWord(dir))
}

// deliveryCheck runs the push proof once against the live session
// (friend.PushProof over friend.Conformance); the refusal is set when it
// could not run (no harness, no store), and remedy, with undriven, when the
// adapter cannot drive the session at all.
func (w world) deliveryCheck(c *tool.Call, name, harness, dir, session, state, to string, within time.Duration) (res friend.CheckResult, remedy string, undriven bool, refusal string) {
	if harness == "claude" { // the claude adapter's target is the state directory, where the wake file is
		dir = state
		if session == "" {
			session = name
		}
	}
	deliver, err := friend.SelectDeliverer(name, harness, dir, session, c.Str("adapter"), c.Str("delivery-dir"), w.exec, nil)
	if err == nil {
		err = friend.TmuxFor(deliver, name, state) // harness tmux: the session and prompt host saved
	}
	if err != nil {
		return friend.CheckResult{}, "", false, err.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), redisconn.OpenTimeout)
	st, closeStore, err := w.open(ctx, c.Str("redis"))
	cancel()
	if err != nil {
		return friend.CheckResult{}, "", false, "the store did not answer: " + err.Error()
	}
	defer closeStore()
	res, remedy, undriven = friend.PushProof(context.Background(), w.conformance(c, name, harness, state, to, within, deliver, st))
	return res, remedy, undriven, ""
}

// conformance is the delivery check through deliver over st: the session
// check carries this binary's pong line, sent to to (checkTo).
func (w world) conformance(c *tool.Call, name, harness, state, to string, within time.Duration, deliver friend.Deliverer, st bus.Store) friend.Conformance {
	to = w.checkTo(to, name, state)
	return friend.Conformance{
		Friend: name, Harness: harness, Deliver: deliver, Store: st, Within: within, Now: w.now, Nonce: w.random,
		Wait: func(ctx context.Context) bool { w.sleep(ctx, friend.CheckPoll); return ctx.Err() == nil },
		Text: func(nonce string) string {
			return friend.SessionCheckText(nonce, w.pongCommand(name, nonce, state, c.Str("redis"), c.Str("dir")), to)
		},
		Pong: func() (friend.Pong, bool, error) { return friend.ReadPong(state) },
	}
}

// noteGrokMonitor appends the one line a grok session runs, when harness is grok.
func noteGrokMonitor(o *tool.Out, harness, session string) *tool.Out {
	if line := friend.GrokInstallLine(harness, session); line != "" {
		o.Note(line)
	}
	return o
}

// claudeWake is the wake file the claude session's wait names: in the state
// directory the daemon will keep (--state-dir, else <dir>/.nova-friend, else
// the home directory's), named and never made here.
func (w world) claudeWake(c *tool.Call, name string) string {
	state := c.Str("state-dir")
	if state == "" && c.Str("dir") != "" {
		state = friend.StateDirIn(c.Str("dir"))
	}
	if state == "" {
		state = friend.DefaultStateDir(w.home, name)
	}
	return friend.ClaudeWakePath(state, name)
}

// noteClaudeWait appends the one line a claude session runs, when harness is claude.
func noteClaudeWait(o *tool.Out, harness, friendName, wake string) *tool.Out {
	if line := friend.ClaudeInstallLine(harness, friendName, wake); line != "" {
		o.Note(line)
	}
	return o
}

func (w world) uninstall(c *tool.Call) *tool.Out {
	a := friend.Agent{Friend: c.Str("as"), Home: w.home}
	if c.DryRun() {
		return tool.Done().Fact("label", a.Label()).Fact("plist", a.PlistPath()).Item("plan", "command", tool.Text(fmt.Sprintf("launchctl bootout gui/%d/%s", w.uid, a.Label()))).Item("plan", "command", tool.Text("rm "+a.PlistPath()))
	}
	ran, err := friend.Uninstall(context.Background(), a, w.uid, w.launchctl, os.Remove)
	o := tool.Done().Fact("label", a.Label()).Fact("plist", a.PlistPath())
	for _, r := range ran {
		o.Item("ran", "command", tool.Text(r))
	}
	if err != nil {
		return tool.Fail(err.Error())
	}
	return o
}

func age(now, t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return now.Sub(t).Round(time.Second).String()
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

func (w world) resume(c *tool.Call) *tool.Out {
	state := w.stateDir(c, c.Str("dir"))
	msg := friend.ReadPause(state)
	if msg == "" {
		return tool.Done().Fact("cleared", "none")
	}
	if c.DryRun() {
		return tool.Done().Fact("cleared", "would").Fact("pause", msg)
	}
	if _, err := friend.ClearPause(state); err != nil {
		return tool.Refuse("the pause marker cannot be removed: " + err.Error())
	}
	return tool.Done().Fact("cleared", "yes").Fact("pause", msg)
}

// statusAll is status --all: one line per friend daemon agent installed under
// the home's LaunchAgents, its daemon up or down by its status file's age
// (friend.DaemonStale), its version and its last beat (docs/SPEC-FRIEND.md,
// daemon-supervised-r-b.w7).
func (w world) statusAll() *tool.Out {
	agents, err := friend.InstalledAgents(w.home)
	if err != nil {
		return tool.Refuse("the installed agents cannot be read: " + err.Error())
	}
	now := w.now()
	o := tool.Done().Fact("agents", len(agents))
	for _, a := range agents {
		s, found, err := friend.ReadStatus(a.StateDir)
		daemon := "none"
		switch {
		case err != nil:
			o.Note(a.Friend + ": the status file cannot be read: " + err.Error())
		case found && now.Sub(s.At) < friend.DaemonStale:
			daemon = "up"
		case found:
			daemon = "down"
		}
		o.Item("agent", "name", a.Friend, "daemon", daemon, "daemon_version", dash(s.DaemonVersion),
			"last_beat", stamp(s.LastBeat), "last_beat_age", age(now, s.LastBeat), "state_dir", a.StateDir)
	}
	if len(agents) == 0 {
		o.Note("no friend daemon agent is installed for this login; run: nova-friend install --as <me> --harness <h> --dir <d>")
	}
	return o
}

func (w world) status(c *tool.Call) *tool.Out {
	if c.Bool("all") {
		return w.statusAll()
	}
	dir, name := c.Str("dir"), c.Str("as")
	state := w.stateDir(c, dir)
	s, found, err := friend.ReadStatus(state)
	if err != nil {
		return tool.Refuse("the status file cannot be read: " + err.Error())
	}
	if !found {
		return tool.Fail("no daemon has run as " + name + " (no status file in " + state + "); run: nova-friend install --as " + name + " --harness <h> --dir " + dir).As("NONE")
	}
	if s.Friend != name {
		return tool.Refuse(fmt.Sprintf("the daemon whose state is in %s runs as %s, not %s", state, s.Friend, name))
	}
	now := w.now()
	daemon := "down"
	if now.Sub(s.At) < friend.DaemonStale {
		daemon = "up"
	}
	p, _, perr := friend.ReadPong(state)
	queue, working, qerr := friend.ReadQueue(dir)
	width := s.Width
	if p.Width > 0 {
		width = p.Width
	}
	o := tool.Done().Fact("daemon", daemon).Fact("harness", s.Harness).Fact("status_age", age(now, s.At)).
		Fact("connection", s.Connection).Fact("seat", dash(s.Seat)).Fact("last_ping", stamp(s.LastPing)).Fact("ping_age", age(now, s.LastPing)).
		Fact("challenge", s.Challenge).Fact("nonce", dash(s.Nonce)).Fact("last_pong", stamp(p.At)).Fact("session_pong_age", age(now, p.At)).Fact("daemon_pong_age", age(now, s.LastDaemonPong)).Fact("pongs", s.Pongs).
		Fact("queue", queue).Fact("working", working).Fact("width", width).Fact("beats", s.Beats).Fact("last_beat", stamp(s.LastBeat)).Fact("last_beat_age", age(now, s.LastBeat)).
		Fact("daemon_version", dash(s.DaemonVersion)).Fact("binary", dash(s.Binary)).Fact("delivered", s.Delivered).Fact("envelope", s.Envelope).Fact("envelope_bytes", s.EnvelopeBytes).Fact("session", dash(s.Session)).Fact("mode", dash(s.Mode))
	if s.Lanes != "" {
		o.Fact("lanes", tool.Text(s.Lanes))
	}
	// her row against her inbox, as the daemon's last reconcile found them (friend.SyncInbox)
	if s.HeldKnown {
		o.Fact("held", s.Held).Fact("inbox", s.InboxJobs).Fact("missing", s.Missing)
	} else {
		o.Fact("held", "-").Fact("inbox", "-").Fact("missing", "-")
	}
	if s.InboxError != "" {
		o.Note("the inbox: " + s.InboxError)
	}
	if s.HeldKnown && s.HeldFrom == friend.FromView {
		o.Note("the inbox: her row is read from " + friend.FromView + ": " + friend.ViewWhy + "; a missing card's BRIEF.md is friend sync's to write")
	}
	var route, routeLine string
	var routeErr error
	if s.Harness == "grok" {
		route, routeLine, routeErr = (&friend.Grok{Dir: dir, Run: w.exec, Home: filepath.Join(w.home, ".grok")}).Route(context.Background())
	}
	v := friend.FriendStatus(w.evidence(c, s, p, now, daemon == "up", route, o), now, friend.AnswerBound, time.Local)
	if s.Session == friend.SessionBroken {
		o.Fact("session_id", dash(s.SessionID)).Fact("reason", tool.Text(s.SessionReason)).Fact("broken_at", stamp(s.BrokenAt))
		o.Note("the session is broken: the provider refused the same way turn after turn; the daemon delivers nothing into it, every message stays pending; renew the session, then restart the daemon (install again)")
	}
	pr, prFound, prErr := friend.ReadPresence(state)
	switch {
	case !prFound || prErr != nil: // a daemon from before presence: no word
	case daemon == "down":
		o.Fact("presence", friend.PresenceDown).Fact("presence_reason", tool.Text("no daemon"))
	default:
		o.Fact("presence", pr.Presence).Fact("last_session", stamp(pr.LastHeard))
		if pr.Presence != friend.PresenceUp {
			o.Fact("presence_reason", tool.Text(pr.Reason))
			o.Note("the daemon is up and the session is not (" + pr.Reason + "): the friend is down, and no beat goes to the sprint server until the session answers a session check")
		}
	}
	if prErr != nil {
		o.Note("the presence file: " + prErr.Error())
	}
	o.Fact("status", v.Status).Fact("why", tool.Text(v.Reason)).Fact("evidence", tool.Text(strings.Join(v.Evidence, "; "))).
		Fact("harness_seen", tool.Text(dash(s.HarnessSeen))) // advisory, after the verdict it does not decide
	if s.BeatError != "" {
		o.Note("the last beat failed: " + s.BeatError)
	}
	if s.StoreError != "" {
		o.Note("the store: " + s.StoreError)
	}
	if perr != nil {
		o.Note("the pong file: " + perr.Error())
	}
	if qerr != nil {
		o.Note("the queue file: " + qerr.Error())
	}
	if s.Harness == "grok" {
		line, rerr := routeLine, routeErr
		if route == "" {
			route = "defer"
		}
		o.Fact("route", route)
		if route != "push" {
			if line == "" {
				line = friend.GrokMonitorLine("")
			}
			o.Note(line)
			if rerr != nil {
				o.Note("grok route: " + rerr.Error())
			}
		}
	}
	if s.Harness == "claude" {
		o.Fact("route", "passive").Note(friend.ClaudeWaitLine(c.Str("as"), friend.ClaudeWakePath(state, c.Str("as"))))
	}
	return o
}

// evidence is what the friend's status is decided from (docs/SPEC-FRIEND.md,
// "A friend's status, from evidence"): the harness, the session's last
// answer, the limit file, the messages waiting on her stream (counted when
// --redis names the store), and the last turn's line of the log. A daemon
// that is down, a broken session and a store that does not answer are a bus
// that cannot deliver to her. What cannot be read is a NOTE on o.
func (w world) evidence(c *tool.Call, s friend.Status, p friend.Pong, now time.Time, daemonUp bool, route string, o *tool.Out) friend.Evidence {
	state := w.stateDir(c, c.Str("dir"))
	e := friend.Evidence{DaemonUp: daemonUp, LastAnswer: p.At, Undelivered: -1}
	e.Harness = s.HarnessSeen // the daemon's harness check: advisory, shown and never deciding
	if route == "push" {
		e.Harness = friend.HarnessRunning // a tail runs under the open window's pid
	}
	if pr, found, err := friend.ReadPresence(state); daemonUp && err == nil && found && pr.Presence == friend.PresenceUp && pr.LastHeard.After(e.LastAnswer) {
		e.LastAnswer = pr.LastHeard // the session's last word the daemon read on the bus, when the pong file is older or was never written
	}
	l, _, err := friend.ReadLimitFile(state)
	if err != nil {
		o.Note("the limit file: " + err.Error())
	}
	e.Limit, e.LimitUntil = l.Reason, l.Until
	if e.LastResult, e.LastExit, err = friend.LastResult(state); err != nil {
		o.Note("the log: " + err.Error())
	}
	switch {
	case !daemonUp:
		e.BusBlocked = "daemon down " + friend.Ago(now.Sub(s.At))
	case s.Session == friend.SessionBroken:
		e.BusBlocked = "session broken"
	case s.StoreError != "":
		e.BusBlocked = "the store: " + s.StoreError
	}
	if addr := c.Str("redis"); addr != "" {
		ctx, cancel := context.WithTimeout(context.Background(), redisconn.OpenTimeout)
		defer cancel()
		st, closeStore, err := w.open(ctx, addr)
		if err == nil {
			defer closeStore()
			var pending, fresh []bus.Entry
			if pending, fresh, err = (&bus.Bus{Store: st}).Peek(ctx, c.Str("as")); err == nil {
				e.Undelivered = len(pending) + len(fresh)
			}
		}
		if err != nil && e.BusBlocked == "" {
			e.BusBlocked = "the store: " + err.Error()
		}
	}
	return e
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func (w world) pong(c *tool.Call) *tool.Out {
	name, nonce, state := c.Str("as"), c.Str("nonce"), w.stateDir(c, "")
	s, found, err := friend.ReadStatus(state)
	if err != nil {
		return tool.Refuse("the status file cannot be read: " + err.Error())
	}
	if found && s.Friend != name {
		return tool.Refuse(fmt.Sprintf("the daemon whose state is in %s runs as %s, not %s: a pong carries the daemon's name", state, s.Friend, name))
	}
	to := c.Str("to")
	if to == "" {
		to = s.Seat
	}
	if to == "" {
		return tool.Refuse("--to is required: no ping has named a seat yet (no status file in " + state + "); it wants the coordinator's name")
	}
	previous, _, err := friend.ReadPong(state)
	if err != nil {
		return tool.Refuse("the previous pong file cannot be read: " + err.Error())
	}
	queue, working, width := previous.Queue, previous.Working, previous.Width
	if s.Width > 0 {
		width = s.Width
	}
	if dir := c.Str("dir"); dir != "" && (!c.Given("queue") || !c.Given("working")) {
		q, wk, err := friend.ReadQueue(dir)
		if err != nil {
			return tool.Refuse("the working directory's queue cannot be read: " + err.Error())
		}
		queue, working = q, wk
	}
	if c.Given("queue") {
		queue = c.Int("queue")
	}
	if c.Given("working") {
		working = c.Int("working")
	}
	if c.Given("width") {
		width = c.Int("width")
	}
	b, closeStore, refused := w.bus(c)
	if refused != nil {
		return refused
	}
	defer closeStore()
	line := friend.PongLine(nonce, queue, working, width)
	pong := bus.Message{From: name, To: []string{to}, Subject: friend.PongSubject, Body: line + "\n"}
	if c.DryRun() {
		// the note checked as send checks it; nothing sent, no pong file
		if _, err := b.Check(context.Background(), pong); err != nil {
			return answer(err)
		}
		return tool.Done().Fact("nonce", nonce).Fact("to", to).Fact("line", tool.Text(line))
	}
	m, err := b.Send(context.Background(), pong)
	if err != nil {
		return answer(err)
	}
	p := friend.Pong{Nonce: nonce, At: m.At, To: to, Queue: queue, Working: working, Width: width}
	if err := friend.WritePong(state, p); err != nil {
		return tool.Fail("sent, but the pong file was not written: "+err.Error()).Fact("id", m.ID)
	}
	return tool.Done().Fact("nonce", nonce).Fact("to", to).Fact("id", m.ID).Fact("at", m.At.Format(time.RFC3339))
}

func (w world) ping(c *tool.Call) *tool.Out {
	if c.Bool("to-friends") {
		return w.wakeFriends(c)
	}
	nonce := c.Str("nonce")
	if nonce == "" {
		nonce = w.random()
	}
	since := w.now()
	if s := c.Str("since"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return tool.Refuse("--since wants an RFC3339 instant: " + err.Error())
		}
		since = t
	}
	b, closeStore, refused := w.bus(c)
	if refused != nil {
		return refused
	}
	defer closeStore()
	me, to := c.Str("as"), c.Str("to")
	body := friend.PingText(me, since, nonce)
	if c.Bool("wake") { // a wake check: answered by the session, never by the daemon (docs/SPEC-FRIEND.md, session-pong.w1)
		body = friend.WakePingText(me, since, nonce)
	}
	ping := bus.Message{From: me, To: []string{to}, Subject: friend.PingPrefix + nonce, Body: body + "\n"}
	if c.DryRun() {
		// the PING checked as send checks it; nothing sent
		if _, err := b.Check(context.Background(), ping); err != nil {
			return answer(err)
		}
		return tool.Done().Fact("nonce", nonce).Fact("to", to)
	}
	m, err := b.Send(context.Background(), ping)
	if err != nil {
		return answer(err)
	}
	return tool.Done().Fact("nonce", nonce).Fact("id", m.ID).Fact("to", to).Fact("at", m.At.Format(time.RFC3339)).
		Note("wait for it: nova-friend wait-pong --from " + to + " --nonce " + nonce)
}

func (w world) waitPong(c *tool.Call) *tool.Out {
	b, closeStore, refused := w.bus(c)
	if refused != nil {
		return refused
	}
	defer closeStore()
	from, nonce, timeout := c.Str("from"), c.Str("nonce"), c.Dur("timeout")
	ctx := context.Background()
	start := w.now()
	_, storeNow, err := b.Store.Roster(ctx)
	if err != nil {
		return answer(err)
	}
	floor := bus.IDAt(storeNow.Add(-timeout)) // the log from the wait's own window back, never from its start
	for {
		got, err := b.Log(ctx, floor)
		if err != nil {
			return answer(err)
		}
		daemon := false
		for _, e := range got {
			m := e.Message()
			if m.From != from { // the friend's own stream, never the body
				continue
			}
			if m.Subject == friend.DaemonPongSubject && strings.TrimSpace(m.Body) == "daemon-pong "+nonce {
				daemon = true
			}
			if n, q, wk, wd, ok := friend.ParsePong(strings.TrimSpace(m.Body)); ok && n == nonce {
				return tool.Done().Fact("nonce", nonce).Fact("from", from).Fact("at", m.At.Format(time.RFC3339)).Fact("took", w.now().Sub(start).Round(time.Millisecond).String()).
					Fact("queue", q).Fact("working", wk).Fact("width", wd).Fact("daemon", daemon)
			}
		}
		if w.now().Sub(start) >= timeout {
			o := tool.Fail("no pong "+nonce+" from "+from+" within "+timeout.String()).As("NONE").Fact("daemon", daemon)
			if daemon {
				return o.Note("the daemon answered and the session did not: deaf")
			}
			return o
		}
		w.sleep(ctx, WaitPongEvery)
	}
}

// secretNameRe is a secret's name: an environment variable's.
var secretNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// secretNames splits --secrets, dropping empty words.
func secretNames(csv string) []string {
	var out []string
	for _, w := range strings.Split(csv, ",") {
		if w = strings.TrimSpace(w); w != "" {
			out = append(out, w)
		}
	}
	return out
}

// commaList is a comma-separated flag's values, blanks dropped.
func commaList(csv string) []string {
	var out []string
	for _, v := range strings.Split(csv, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// fileThere says whether path is there, a symlink counting as itself.
func fileThere(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// dirThere is nil when path is a directory.
func dirThere(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	return nil
}

// hostHarnesses are the harnesses host knows an idle prompt for, sorted.
func hostHarnesses() []string {
	names := make([]string, 0, len(friend.HostPrompts))
	for h := range friend.HostPrompts {
		names = append(names, h)
	}
	slices.Sort(names)
	return names
}

// host starts the launch command in a tmux session (friend.Host) and saves
// the session and prompt for run and install (docs/SPEC-FRIEND.md, "Hosted in tmux").
func (w world) host(c *tool.Call) *tool.Out {
	command := w.launch
	name, dir := c.Str("as"), c.Str("dir")
	_, prompt, err := friend.HostPrompt(c.Str("harness"), c.Str("prompt"))
	if err != nil {
		return tool.Refuse(err.Error())
	}
	res, err := friend.Host(context.Background(), w.exec, friend.HostSpec{Name: name, Dir: dir, Command: command, DryRun: c.DryRun()})
	var refused friend.HostRefused
	switch {
	case errors.As(err, &refused):
		return &tool.Out{Status: tool.Refused, Exit: 1, Why: []string{refused.Session + " runs already"}, Remedy: res.Attach} // the verb ran and said no: REFUSED at exit 1
	case err != nil:
		return tool.Refuse(err.Error())
	case c.DryRun():
		return tool.Done().As("DRY-RUN").Fact("session", res.Session).Fact("dir", dir).Fact("command", tool.Text(res.Line))
	}
	state, _ := w.daemonStateDir(c) // where run keeps its state and TmuxFor reads it; a refused <dir> falls back as run's does
	if err := friend.WriteHost(state, friend.Hosted{Session: res.Session, Harness: c.Str("harness"), Prompt: prompt}); err != nil {
		return tool.Refuse("the session " + res.Session + " runs, and its state could not be saved: " + err.Error())
	}
	return tool.Done().Fact("session", res.Session).Fact("dir", dir).Fact("attach", tool.Text(res.Attach))
}

// parseHolders reads the server's view cards document, schema 1. Empty holders
// mean no working fleet row holds the card; malformed or mismatched documents
// never supply an ownership name.
func parseHolders(out string) (map[string]string, error) {
	var v struct {
		View   string `json:"view"`
		Schema int    `json:"schema"`
		Cards  []struct {
			ID     string `json:"id"`
			Holder string `json:"holder"`
		} `json:"cards"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return nil, errors.New("card holders: the view is not valid JSON")
	}
	if v.View != "cards" || v.Schema != 1 {
		return nil, errors.New("card holders: expected view cards schema 1")
	}
	holders, seen := map[string]string{}, map[string]bool{}
	for _, c := range v.Cards {
		if c.ID == "" || seen[c.ID] {
			return nil, errors.New("card holders: empty or duplicate card id")
		}
		seen[c.ID] = true
		if c.Holder != "" {
			holders[c.ID] = c.Holder
		}
	}
	return holders, nil
}
