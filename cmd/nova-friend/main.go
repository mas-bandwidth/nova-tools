// nova-friend is what a friend runs to be part of the team (docs/SPEC-FRIEND.md;
// the model is tla/Friend.tla): one launchd agent per friend that parks on the
// friend's nova-bus stream and pushes each message into the running session
// as a turn, beats to the sprint server while it does, answers the
// coordinator's pings at once and pushes them in so the session answers as
// its own turn, and tells the session when the coordinator goes silent; and,
// on the coordinator's side, the ping loop that pings every friend each
// second. The verbs are run, install, uninstall, check, status, pong, ping,
// wait-pong and serve; the
// dispatch, the banner, the help, the refusals and the output envelope are
// internal/tool's, and the rules are internal/friend's.
package main

import (
	"context"
	"crypto/rand"
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
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/redisauth"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
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
	getenv    func(string) string
	open      func(ctx context.Context, addr string) (bus.Store, func(), error)
	exec      friend.Exec
	wall      func(wl friend.Wall, run friend.Exec) friend.Exec                                                   // a lane's child inside its wall; the real world's is Wall.Exec, nil walls nothing (a test's fake harness)
	beat      func(ctx context.Context, server, friend string, active, pong time.Time) (answer string, err error) // the FRIEND-BEAT line, which carries the friend's row
	beatDown  func(ctx context.Context, server, friend string, active, until time.Time, reason string) error      // her beat while her harness is at its limit (friend beat --until --reason); nil holds the beat back
	progress  func(ctx context.Context, server string, argv []string) error                                       // one progress verb to the sprint server (friend.ProgressArgv)
	finish    func(ctx context.Context, server string, argv []string) error                                       // one finish verb to the sprint server (friend.FinishArgv: a lane's card whose run ended with no report)
	cards     func(ctx context.Context, server string, argv []string) (string, error)                             // the cards on her row, asked of the sprint server (friend.FriendCardsArgv); nil asks none
	friends   func(ctx context.Context, server string) (rows []friend.WakeRow, seat string, err error)            // the friends table and the seat's holder, from the sprint server's coordinator view (GET /api/view/coordinator?all=1)
	view      func(ctx context.Context, server, friend string) (string, error)                                    // the sprint server's worker view of her (GET /api/view/worker), while friend cards is refused; nil reads none
	stage     func(dir string) func(ctx context.Context, p friend.Packet) (string, error)                         // stages a held card's job under her working directory (friend.Stager, with the daemon's git credentials); nil stages none (a test's)
	launchctl friend.Launchctl
	now       func() time.Time
	sleep     func(ctx context.Context, d time.Duration)
	signals   func(ctx context.Context) (context.Context, context.CancelFunc)
	uid       int
	home      string
	binary    func() (string, error)
	copy      friend.CopyFile              // places a removable-volume binary under home; nil refuses it
	lookPath  func(string) (string, error) // a program on PATH by absolute path, for the agent's secrets wrap
	random    func() string
	alive     friend.Aliver     // the harness check, when set (a test's fake harness); nil watches the adapter
	settings  friend.SettingsFS // where a harness's own settings are read and written (install, check --settings)
	argv      []string          // this run's arguments after the program's name: what the plist drift is read against
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

// maxView bounds the worker view the daemon reads: her cards and her results not landed.
const maxView = 4 << 20

// sprintView reads the sprint server's worker view of a friend (nova-sprint serve, GET
// /api/view/worker?as=<friend>), the JSON document whole.
func sprintView(ctx context.Context, server, name string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+server+"/api/view/worker?as="+url.QueryEscape(name), nil)
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

func realWorld() world {
	w := world{getenv: os.Getenv, exec: friend.RealExec, wall: friend.Wall.Exec, now: time.Now, uid: os.Getuid(), home: os.Getenv("HOME"),
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
		beat: func(ctx context.Context, server, name string, active, pong time.Time) (string, error) {
			args := []string{"friend", "beat", name}
			if !active.IsZero() {
				args = append(args, "--active", active.UTC().Format(time.RFC3339))
			}
			if !pong.IsZero() {
				args = append(args, "--pong", pong.UTC().Format(time.RFC3339))
			}
			return sprintBeat(ctx, server, args)
		},
		beatDown: func(ctx context.Context, server, name string, active, until time.Time, reason string) error {
			args := []string{"friend", "beat", name, "--until", until.UTC().Format(time.RFC3339), "--reason", reason}
			if !active.IsZero() {
				args = append(args, "--active", active.UTC().Format(time.RFC3339))
			}
			_, err := sprintBeat(ctx, server, args)
			return err
		},
		progress: sprintVerb,
		finish:   sprintVerb,
		cards:    sprintAsk,
		view:     sprintView,
		friends:  coordinatorFriends,
		stage: func(dir string) func(ctx context.Context, p friend.Packet) (string, error) {
			return (&friend.Stager{Dir: dir}).Stage
		},
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

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, realWorld())) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, w world) int {
	if len(args) > 0 && args[0] == friend.WallVerb {
		// the lane's wall around one command: its argv follows "--", which the verb table
		// does not carry, so it is dispatched here (internal/friend RunWall)
		return friend.RunWall(args[1:], os.Environ(), stdin, stdout, stderr)
	}
	w.argv = args
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
		f.String("session", "", "the session to deliver into (default: the harness's newest session in --dir)")
		f.String("server", w.server(), "the sprint server, host:port (default: "+ServerEnv+", else "+DefaultServer+")")
		f.Int("width", 0, "the friend's width, from the nova-config friend row; 0 is unknown")
		f.Duration("silent-stop", friend.DefaultSilentStop, "stop a turn that has printed nothing for this long; a turn that prints runs on")
		f.Int("broken-after", friend.DefaultBrokenAfter, "turns in a row the provider refuses the same way before the session is broken")
		f.Duration("limit-rest", friend.DefaultLimitWait, "how long the friend is down when its harness's usage limit or empty balance names no reset")
		f.String("coordinator", "", "who is told of a broken session when no ping has named the seat")
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
		Words:     []string{"NONE", "FAIL", "DRIFT"},
		Verbs: []tool.Verb{
			{
				Name:    "run",
				Usage:   "run --as <me> --harness <h> --dir <d> [--session <id>] [--server <addr>] [--width <n>] [--silent-stop <d>] [--broken-after <n>] [--coordinator <seat>] [--state-dir <d>] [--redis <addr>] [--profile <p>] [--config-dir <d>] [--deny-self <d,...>] [--wall-jobs <d,...>] [--wall-reads <d,...>] [--dry-run]",
				Example: "", // a daemon: the example block has no line that runs for ever
				Effect:  tool.Delivery + ": the daemon; messages go into the session, beats and pongs go out, until a signal",
				DryRun:  true,
				Detail: `The loop launchd runs (install writes it). It starts only on a push proof: a harness with no deliver
command (claude, the surveyed ones) is refused at once, exit 2, the adapter card its remedy; then one SESSION
CHECK goes in through the harness and its pong must reach the bus within ` + friend.ProofWithin.String() + `, else exit 2 with the remedy
(a dsh session under an agent preset: start a session in <dir> with no agent preset and name it with
--session <id>). Every beat carries the session's last proof (--pong), and the sprint deals nothing to a
friend whose proof has lapsed. Each second, when the session is free: every waiting
message read off the stream and pushed in as ONE turn, oldest first (at most ` + fmt.Sprint(friend.MaxBatch) + `; the rest is the next
turn), acked together when the turn ends at exit 0; a turn that fails leaves them pending, handed in
again when their claims open, and the third failure acks a message, given_up=true on the record. A
PING is answered at once with a daemon-pong and acked, never a turn; while a challenge is open the
pong line rides at the head of the next turn. No ping for ` + friend.Window.String() + `: "coordinator silent", and
"coordinator back" when pings resume, collapsed to the latest and said only inside a turn that
carries messages. Presence is the session's, never the daemon's: after ` + friend.SessionQuiet.String() + ` with no bus message from
the session (the daemon's own sends never count), a SESSION CHECK <nonce> goes in through the harness
as a turn of its own, once no turn is under way (on the friend's own stream for a harness with no
deliver command), and only the session's pong carrying that nonce answers it; none within ` + friend.SessionBound.String() + `
and the friend is down, "no session answer", the beat to the sprint server held back until the next
answer brings it up; it starts down until the first answer. A turn runs as long as it prints; one
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
reset when it names one, else --limit-rest; status says session=limited limit_kind= limit_until= while it stands. The friend
row's mode and width come with each beat's answer (row_mode=, row_width=, row_config_dir=). In one-shot mode width lanes run, each its own session seeded from the friend's AGENTS.md and
memory/, kept in lanes.json; each lane hands one card a turn from <dir>/inbox/QUEUE.json (its BRIEF.md, the
REPORT.md and RESULT.md to write, one bus line to send), the waiting messages riding along, and hands the
next only when the turn ends; a card with no RESULT.md after two turns is set aside and reported. A claude
lane is a process per card instead (env CLAUDE_CONFIG_DIR=<config_dir> claude -p <the brief>, stdin
/dev/null, inside the lane wall with the row's config_dir as its --config-dir), its result read from the
card's outbox; a claude row in one-shot mode with no config_dir (nor --config-dir) is refused on the
record with the remedy, and no lane runs. A lane
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
dir= state= redis=): no store is opened and nothing is written.`,
				Flags: func(f *tool.Flags) {
					daemonFlags(f)
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
				Name:    "install",
				Usage:   "install --as <me> --harness <h> --dir <d> [--session <id>] [--server <addr>] [--width <n>] [--silent-stop <d>] [--broken-after <n>] [--coordinator <seat>] [--state-dir <d>] [--redis <addr>] [--config-dir <d>] [--model <provider/model>] [--secrets NAME[,NAME] --seat <seat>] [--launchd-log <file>] [--dry-run]",
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
writes nothing. For harness grok, a NOTE prints the one line the open session runs, ` + friend.GrokMonitorLine("") + `
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
				Usage:   "check [--as <coordinator>] [<friend>...] [--since <duration>] [--shown <file|->] [--json]",
				Example: "", // the banner's example block runs nothing that reads a fleet's state; -h carries the example
				Effect:  tool.Delivery + ": without --harness it only reads (the health check); with --harness it delivers one session check into the live session (the delivery check)",
				DryRun:  true,
				Detail: `The health check: is each friend's row true. The friends are the arguments, else every friend with a
state directory under ~/.nova-friend (or --state-dir) or on the bus. Everything is judged over the --since
window (default 24h): deliveries, deferrals, real messages and the session pong. Per friend, five lines in
this order:
CHECK DAEMON friend=<f> agent=<loaded|not-loaded|none> pid=<n|-> status=<ok|stale|none> connection=<..> challenge=<..> pong_age=<age|-> presence=<up|asleep|down> seen_age=<age|->
CHECK HARNESS friend=<f> harness=<h> route=<push|defer|passive> last=<RFC3339|-> last_exit=<n|-> failed_of_last20=<n> deferred=<n> broken=<RFC3339|-> reason=<line|->
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
friend and daemon{friend, agent, pid, status, connection, challenge, pong_age, presence, seen_age},
harness{friend, harness, route, last, last_exit, failed_of_last20, deferred, delivered, failed, broken,
reason}, bus{friend, real_since, last_real}, work{friend, inbox, outbox, newest_outbox, newest_at},
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
					f.Bool("settings", false, "compare the harness's settings with what install would write; nothing is delivered or written")
					settingFlags(f)
					f.Duration("within", friend.DefaultCheckWithin, "how long to wait for the session's pong (delivery check)")
					f.String("to", "", "who the pong goes to (default: the seat the daemon's status names, else --as)")
					f.Duration("since", 24*time.Hour, "the window every fact is judged over: deliveries, deferrals, real messages, the session pong")
					f.String("shown", "", "path to shown state file, or - for stdin")
					stateDir(f)
					redis(f)
					f.Check(func(c *tool.Call) {
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
				Usage:   "pong --as <me> --nonce <n> [--to <coordinator>] [--queue <n>] [--working <n>] [--width <n>] [--state-dir <d>] [--redis <addr>] [--dry-run]",
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
				Name:    "status",
				Usage:   "status --as <me> --dir <d> [--state-dir <d>]",
				Example: "status --as bob --dir ./bob",
				Effect:  tool.Inspection,
				Detail: `Prints STATUS OK daemon=<up|down> harness= connection=<connected|silent> seat= last_ping= challenge=<quiet|challenged|deaf>
last_pong= session_pong_age= daemon_pong_age= pongs= queue= working= width= beats= delivered= session=<ok|broken|-> mode=<batch|one-shot|-> presence=<up|down>
(once a daemon has written it; last_session=, and when down presence_reason=, "no session answer" or "no daemon") (broken: session_id= broken_at= reason=; one-shot: lanes=)
status=<up|down> why= evidence=, and for harness grok route=<push|defer>,
from the daemon's status file (up while it is under ` + friend.DaemonStale.String() + ` old), the session's pong file and the queue file;
session_pong_age is the session's own pong (the pong file), daemon_pong_age the daemon's answer to the last ping (status.json
last_daemon_pong), two facts: a daemon that pongs says nothing of the session
(<dir>/inbox/QUEUE.json). route=push when a tail of a .wake file runs under the open window's pid; route=defer, with a NOTE of
` + friend.GrokMonitorLine("") + `, when none does. JSON carries route as a string (push or defer) and that NOTE in notes; other
harnesses omit route. status is the friend's, decided from evidence in order (docs/SPEC-FRIEND.md); the harness's process is
shown and never decides (harness_seen=running|not-seen|-, the daemon's check of the process table: a session run from its command
line has no app to see); at a limit (the state directory's
` + friend.LimitFile + `) is down until the reset; no session answer (the pong file, or the daemon's last word from the session on the bus) under ` + friend.AnswerBound.String() + ` is down; a bus that cannot
deliver (the daemon down, the session broken, the store failing) is down; otherwise up. The daemon's beat never makes it up. why
is the rule that decided it, as a person reads it ("no session answer 12m", "limit until Mon 1:00 PM"); evidence is every piece,
"; "-separated: the harness, the session answer, the limit, the messages waiting on the stream (counted with --redis), the last
turn's end and exit from the log. STATUS NONE at
exit 1 when no daemon ever ran as --as (no status file in the state directory).`,
				Flags: func(f *tool.Flags) {
					f.Required("as", "your name")
					f.Required("dir", "the friend's working directory, where the queue file lives")
					stateDir(f)
					redis(f)
				},
				Run: w.status,
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
	addr := c.Want("redis", "the bus store's Redis address, host:port (or "+RedisEnv+")")
	if o := c.Refused(); o != nil {
		return o
	}
	name, dir, server := c.Str("as"), c.Str("dir"), c.Str("server")
	state, stateWhy := w.daemonStateDir(c)
	// every lane child runs inside the wall of the profile her row names, else --profile
	// (docs/SPEC-FRIEND.md, buds-in-the-wall-r.w5); a batch turn runs as it did
	var rowProfile atomic.Pointer[string]
	var rowConfigDir atomic.Pointer[string] // her row's config_dir as her beat last answered; read by the lanes' runs and the wall
	wall := friend.Wall{Dir: dir, Jobs: commaList(c.Str("wall-jobs")), Reads: commaList(c.Str("wall-reads")), Deny: commaList(c.Str("deny-self"))}
	if bin, err := w.binary(); err == nil {
		wall.Self = []string{bin}
	} // else no Self: a lane's child is refused, never run outside the wall
	walled := func(ctx context.Context, d, prog string, args []string, stdin string) (string, int, error) {
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
	// her harness's limit: every command's output read for it, her turns held while she is
	// down and a wake after the reset (friend.Limits); its hooks are set once record is
	fl := &friend.Limits{Now: w.now, Nonce: w.random, Harness: c.Str("harness"), Rest: c.Dur("limit-rest")}
	deliver, err := friend.NewDeliverer(c.Str("harness"), dir, c.Str("session"), fl.Watch(walled), c.Stdout)
	if err != nil {
		return tool.Refuse(err.Error()) // the skeleton renders a refusal with the verb's token, on stderr
	}
	if friend.RunsCards(c.Str("harness")) {
		deliver = friend.NewClaude(name, dir, fl.Watch(walled), c.Stdout) // a card a process: the adapter with a lane
	}
	// a harness nothing pushes into is refused at the start (friend.PushProof), a dry run alike
	dry := c.DryRun()
	// a harness that runs each card as a process of its own (friend.CardRunner) has no session to
	// push into: no push proof and no session check stand for it (docs/SPEC-FRIEND.md, one-shot lanes)
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
		// the friend's directory as her tools name it: the symlink in the home directory too
		oc.Allow = []string{}
		if alias := filepath.Join(w.home, name+"-working"); fileThere(alias) {
			oc.Allow = append(oc.Allow, alias)
		}
		deliver = &friend.OpenCodePriced{OpenCode: oc} // every lane run priced from her own session record
	}
	// her row, as her beat last answered it (nova-sprint friend beat: row_mode, row_width, row_config_dir)
	rowMode, rowWidth := "", 0
	var rowReadSlots atomic.Int64 // her row's read slots as her beat last answered; friend.DefaultReadSlots until it says
	rowReadSlots.Store(friend.DefaultReadSlots)
	if cl, ok := deliver.(*friend.Claude); ok {
		cl.Friend, cl.Now = name, w.now // every run's cost and limit on the record, its reset read on her clock
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
	record := func(line string) {
		fmt.Fprintln(c.Stdout, "RUN "+line)
		_ = friend.Record(state, line) // ignored: the line is on stdout (launchd's log) whatever the volume does
	}
	// the session's last proof (its answer or its own bus message), as the presence file
	// last said it: every beat carries it (--pong), so the sprint deals only to a friend
	// whose proof is live and tells the coordinator when it lapses
	var proved atomic.Pointer[time.Time]
	// the presence file says a limit while there is one, whatever the session check saw; the
	// harness's process never decides it (friend.HarnessWatch is advisory)
	writePresence := func(p friend.PresenceStatus) error {
		if !p.LastHeard.IsZero() {
			at := p.LastHeard
			proved.Store(&at)
		}
		if until, reason, limited := fl.Limited(); limited {
			p.Presence, p.Reason = friend.PresenceDown, "harness limit until "+until.UTC().Format(time.RFC3339)+": "+reason
		}
		return friend.WritePresence(state, p)
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
	// the push proof: the first SESSION CHECK round trip, before the loop; no pong within
	// friend.ProofWithin and the daemon does not start (docs/SPEC-FRIEND.md, The push proof)
	var proof friend.CheckResult
	remedy := ""
	if !perCard {
		proof, remedy, _ = friend.PushProof(ctx, w.conformance(c, name, c.Str("harness"), state, c.Str("coordinator"), friend.ProofWithin, deliver, st))
	}
	if proof.Stage != "" {
		if ctx.Err() != nil {
			return tool.Exit(0) // a signal during the proof
		}
		if remedy == "" {
			remedy = fmt.Sprintf("open the friend's %s session in %s, then prove it answers: nova-friend check --as %s --harness %s --dir %s", c.Str("harness"), dir, name, c.Str("harness"), dir)
		}
		o := tool.Refuse("no push proof: " + proof.Line() + "; the daemon did not start")
		o.Remedy = remedy
		return o
	}
	if perCard {
		record(w.now().UTC().Format(time.RFC3339) + " push proof: none owed: " + c.Str("harness") + " runs each card as a process of its own, no session to push into")
	} else {
		record(w.now().UTC().Format(time.RFC3339) + " push proof: " + proof.Line())
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
	sc := &friend.SessionCheck{
		Friend: name, Store: st, Now: w.now, Nonce: w.random, Record: record,
		Save: prover.Save(writePresence),
		Text: func(nonce string) string {
			bin, err := w.binary()
			if err != nil {
				bin = "nova-friend" // ignored: the name on PATH stands in when this binary's path is unknown
			}
			return friend.SessionCheckText(nonce, fmt.Sprintf("%s pong --as %s --nonce %s --state-dir %s --redis %s", bin, name, nonce, state, c.Str("redis")), answerTo())
		},
	}
	sc.Deliver = sc.Gate(fl.Gate(deliver))
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
	d := &friend.Daemon{
		Friend: name, Harness: c.Str("harness"), Dir: dir, Width: c.Int("width"),
		Store: sc.DaemonStore(), Deliver: sc.Deliver, Now: w.now, Pause: w.sleep,
		Limited: func() (string, time.Time, bool) {
			until, _, limited := fl.Limited()
			return fl.Kind(), until, limited
		},
		SilentStop: c.Dur("silent-stop"), BrokenAfter: c.Int("broken-after"), Coordinator: c.Str("coordinator"),
		Activity: func() time.Time {
			return friend.NewestWrite(os.DirFS(dir), friend.ActivityRoots, w.now, friend.DefaultActivityLimits)
		},
		// the session check's and the limits' wrappers take a beat of ctx alone; the daemon's
		// beat carries the session's last activity, closed over here (fold of 2026-10-05)
		Beat: func(ctx context.Context, active time.Time) error {
			held := sc.Beat // the session's answer holds the beat back; a per-card harness has no session, its process is the daemon
			if perCard {
				held = func(beat func(context.Context) error) func(context.Context) error { return beat }
			}
			up := func(ctx context.Context) error {
				var pong time.Time
				if at := proved.Load(); at != nil {
					pong = *at
				}
				if perCard {
					pong = w.now() // the daemon beating is the proof: nothing else can be asked of a process per card
				}
				answer, err := w.beat(ctx, server, name, active, pong)
				if m, wd, ok := friend.ParseRow(answer); err == nil && ok {
					rowMode, rowWidth = m, wd
					dir := friend.RowConfigDir(answer)
					rowConfigDir.Store(&dir)
				}
				if n, ok := friend.ParseReadSlots(answer); err == nil && ok {
					rowReadSlots.Store(int64(n))
				}
				if p, ok := friend.ParseProfile(answer); err == nil && ok {
					rowProfile.Store(&p)
				}
				return err
			}
			if w.beatDown == nil {
				return held(fl.Beat(up))(ctx) // no down beat: held back while she is at her limit
			}
			// while her harness is at its limit her beat says down with the until and the
			// reason (limits-mean-down-w-r5.w1~15), the session's check stepped as before; the
			// inner check is the last before the up beat, so a limit seen during the step is
			// never beaten up
			down := func(ctx context.Context, until time.Time, reason string) error {
				return w.beatDown(ctx, server, name, active, until, reason)
			}
			return fl.BeatOrDown(held(fl.BeatOrDown(up, down)), func(ctx context.Context, until time.Time, reason string) error {
				if !perCard {
					sc.Step(ctx)
				}
				return down(ctx, until, reason)
			})(ctx)
		},
		Row: func() (string, int) {
			if m := c.Str("mode"); m != "" {
				return m, rowWidth // the override, for a test
			}
			return rowMode, rowWidth
		},
		LoadLanes: func() (friend.LaneState, error) { return friend.ReadLanes(state) },
		Sprint:    w.sprintAsk(server),
		ReadSlots: func() int { return int(rowReadSlots.Load()) },
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
		SaveLanes: func(s friend.LaneState) error { return friend.WriteLanes(state, s) },
		Held:      w.held(name, server),
		Stage:     w.stager(dir),
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
			bin, err := w.binary()
			if err != nil {
				bin = "nova-friend" // ignored: the name on PATH stands in when this binary's path is unknown
			}
			return fmt.Sprintf("%s pong --as %s --nonce %s --state-dir %s --redis %s --width %d --queue <tasks queued> --working <tasks working>", bin, name, nonce, state, c.Str("redis"), c.Int("width"))
		},
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

// stager is the daemon's Stage: every held work card's job staged under her working directory
// (jobs/<job>/repo and its JOB.md); nil in a world that stages none or asks no held cards.
func (w world) stager(dir string) func(ctx context.Context, p friend.Packet) (string, error) {
	if w.stage == nil || w.cards == nil || dir == "" {
		return nil
	}
	return w.stage(dir)
}

func (w world) agent(c *tool.Call) (friend.Agent, error) {
	bin, err := w.binary()
	if err != nil {
		return friend.Agent{}, fmt.Errorf("this binary's path: %v", err)
	}
	name := c.Str("as")
	log := c.Str("launchd-log")
	if log == "" {
		log = filepath.Join(w.home, "Library", "Logs", "nova-friend-"+name+".log")
	}
	a := friend.Agent{
		Friend: name, Harness: c.Str("harness"), Dir: c.Str("dir"), Session: c.Str("session"), StateDir: c.Str("state-dir"), Width: c.Int("width"),
		Binary: bin, Copy: w.copy, Redis: c.Str("redis"), Server: c.Str("server"), Home: w.home, Path: w.getenv("PATH"), LaunchdLog: log,
		Secrets: secretNames(c.Str("secrets")), Seat: c.Str("seat"),
		Coordinator: c.Str("coordinator"), SilentStop: c.Dur("silent-stop"), BrokenAfter: c.Int("broken-after"),
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
	// runs each card as a process of its own (claude) has no session to push into and is not
	if !friend.RunsCards(c.Str("harness")) {
		if o := undriven(c.Str("harness"), c.Str("dir"), "nothing was written or loaded"); o != nil {
			return o
		}
	}
	a, err := w.agent(c)
	if err != nil {
		return tool.Refuse(err.Error())
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
		return noteGrokMonitor(o, a.Harness, a.Session)
	}
	wrote, err := h.Write()
	if err != nil {
		return tool.Refuse("the harness's settings: " + err.Error())
	}
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
		return noteGrokMonitor(tool.Fail(err.Error()).Fact("plist", path), a.Harness, a.Session)
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
	return noteGrokMonitor(o.Note("check it: nova-friend status --as "+a.Friend+" --dir "+a.Dir), a.Harness, a.Session)
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
func (w world) pongCommand(name, nonce, state, redis string) string {
	bin, err := w.binary()
	if err != nil {
		bin = "nova-friend" // ignored: the name on PATH stands in when this binary's path is unknown
	}
	return fmt.Sprintf("%s pong --as %s --nonce %s --state-dir %s --redis %s", bin, name, nonce, state, redis)
}

// deliveryCheck runs the push proof once against the live session
// (friend.PushProof over friend.Conformance); the refusal is set when it
// could not run (no harness, no store), and remedy, with undriven, when the
// adapter cannot drive the session at all.
func (w world) deliveryCheck(c *tool.Call, name, harness, dir, session, state, to string, within time.Duration) (res friend.CheckResult, remedy string, undriven bool, refusal string) {
	deliver, err := friend.NewDeliverer(harness, dir, session, w.exec, nil)
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
			return friend.SessionCheckText(nonce, w.pongCommand(name, nonce, state, c.Str("redis")), to)
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

func (w world) status(c *tool.Call) *tool.Out {
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
		Fact("queue", queue).Fact("working", working).Fact("width", width).Fact("beats", s.Beats).Fact("last_beat", stamp(s.LastBeat)).Fact("delivered", s.Delivered).Fact("session", dash(s.Session)).Fact("mode", dash(s.Mode))
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
	b, closeStore, refused := w.bus(c)
	if refused != nil {
		return refused
	}
	defer closeStore()
	line := friend.PongLine(nonce, c.Int("queue"), c.Int("working"), c.Int("width"))
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
	p := friend.Pong{Nonce: nonce, At: m.At, To: to, Queue: c.Int("queue"), Working: c.Int("working"), Width: c.Int("width")}
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
