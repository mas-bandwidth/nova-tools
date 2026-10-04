// nova-friend is what a friend runs to be part of the team (docs/SPEC-FRIEND.md;
// the model is tla/Friend.tla): one launchd agent per friend that parks on the
// friend's nova-bus2 stream and pushes each message into the running session
// as a turn, beats to the sprint server while it does, answers the
// coordinator's pings at once and pushes them in so the session answers as
// its own turn, and tells the session when the coordinator goes silent. The
// verbs are run, install, uninstall, status, sleep, wake, pong, ping and wait-pong; the
// dispatch, the banner, the help, the refusals and the output envelope are
// internal/tool's, and the rules are internal/friend's.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus2"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/redisauth"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

var version string

// The environment: the bus store (nova-bus2's variable) and the sprint
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
// a test its own over internal/bus2's Fake, a fake harness and its own
// clock, so no test opens a socket or reads the real time.
type world struct {
	getenv    func(string) string
	open      func(ctx context.Context, addr string) (bus2.Store, func(), error)
	exec      friend.Exec
	beat      func(ctx context.Context, server, friend string, asleep bool) error
	launchctl friend.Launchctl
	now       func() time.Time
	sleep     func(ctx context.Context, d time.Duration)
	signals   func(ctx context.Context) (context.Context, context.CancelFunc)
	uid       int
	home      string
	binary    func() (string, error)
	random    func() string
}

func realWorld() world {
	w := world{getenv: os.Getenv, exec: friend.RealExec, now: time.Now, uid: os.Getuid(), home: os.Getenv("HOME"),
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
		beat: func(ctx context.Context, server, name string, asleep bool) error {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			argv := []string{"friend", "beat"}
			if asleep {
				argv = append(argv, "--asleep")
			}
			argv = append(argv, name)
			res, err := sprintwire.Client{Addr: server}.Do(ctx, argv)
			if err != nil {
				return err
			}
			if len(res) != 1 || res[0].Code != 0 {
				return fmt.Errorf("friend beat refused: %s", strings.TrimSpace(res[0].Stderr))
			}
			return nil
		},
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
	return friendTool(w).Run(args, stdin, stdout, stderr)
}

// openRedis dials the bus store the way nova-bus2 does (internal/redisconn,
// the fleet's login from the environment, the password never on the line).
func (w world) openRedis(ctx context.Context, addr string) (bus2.Store, func(), error) {
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
	return bus2.Redis{C: conn.Client()}, func() { conn.Close() }, nil
}

// stateDir is where the state files of the friend --as names live: --state-dir,
// else the default under the home directory.
func (w world) stateDir(c *tool.Call) string {
	if s := c.Str("state-dir"); s != "" {
		return s
	}
	return friend.DefaultStateDir(w.home, c.Str("as"))
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
		f.String("state-dir", "", "where the state files live (default: ~/.nova-friend/<me>)")
	}
	daemonFlags := func(f *tool.Flags) {
		f.Required("as", "your name, a nova-config friend row")
		f.Required("harness", "the harness the session runs in: "+strings.Join(friend.Harnesses, ", "))
		f.Required("dir", "the friend's working directory: the session's, and where the state files live")
		f.String("session", "", "the session to deliver into (default: the harness's newest session in --dir)")
		f.String("server", w.server(), "the sprint server, host:port (default: "+ServerEnv+", else "+DefaultServer+")")
		f.String("coordinator", "", "the coordinator's name for this session (default: the saved session state)")
		f.Int("width", 0, "the friend's width, from the nova-config friend row; 0 is unknown")
		stateDir(f)
		redis(f)
		f.Check(func(c *tool.Call) {
			if h := c.Str("harness"); h != "" && !friend.Known(h) {
				c.Problem(fmt.Sprintf("--harness %q is no harness; it wants one of %s", h, strings.Join(friend.Harnesses, ", ")))
			}
			if name := c.Str("as"); name != "" && bus2.CheckName(name) != "" {
				c.Problem("--as " + bus2.CheckName(name))
			}
			if coordinator := c.Str("coordinator"); coordinator != "" && bus2.CheckName(coordinator) != "" {
				c.Problem("--coordinator " + bus2.CheckName(coordinator))
			}
		})
	}
	return &tool.Tool{
		Name:  "nova-friend",
		What:  "what a friend runs to be part of the team: the wake loop, the beat, and the proof of life, as one daemon",
		Stamp: version,
		How: `one launchd agent per friend (install) runs the daemon (run): it parks on the friend's
nova-bus2 stream and pushes each message into the running session as a turn (the harness's
deliver command), beats to the sprint server while the loop runs, answers the coordinator's
PING at once (daemon-pong) and pushes it in; the session's own pong --nonce alone makes it up.
state: ~/.nova-friend/<me>/ (or --state-dir), the queue: <dir>/inbox/QUEUE.json.`,
		ExitTable: "0 done, 1 the verb ran and said no (wait-pong: no pong in time; status: no daemon), 2 could not run (a flag, an input, a store or a server that did not answer).",
		Words:     []string{"NONE"},
		Verbs: []tool.Verb{
			{
				Name:    "run",
				Usage:   "run --as <me> --harness <h> --dir <d> [--session <id>] [--server <addr>] [--coordinator <name>] [--width <n>] [--state-dir <d>] [--redis <addr>]",
				Example: "", // a daemon: the example block has no line that runs for ever
				Effect:  tool.Delivery + ": the daemon; messages go into the session, beats and pongs go out, until a signal",
				Detail: `The loop launchd runs (install writes it). Each second: one read of the stream (a message is
pushed into the session as a turn and acked when the turn ends at exit 0; a turn that fails leaves it
pending, handed in again when its claim opens, and the third failure acks it, given_up=true on the
record; a PING is answered at once with a daemon-pong and pushed in), one beat to the sprint server, the session's pong file
read while a challenge is open, the status file written. No ping for ` + friend.Window.String() + `: the session
is told "coordinator silent" once, and "coordinator back" when pings resume. Prints one RUN
line per delivery on stdout; stops on SIGINT or SIGTERM, a delivery under way left pending.`,
				Flags: func(f *tool.Flags) { daemonFlags(f); f.Prints() },
				Run:   w.run,
			},
			{
				Name:    "install",
				Usage:   "install --as <me> --harness <h> --dir <d> [--session <id>] [--server <addr>] [--coordinator <name>] [--width <n>] [--state-dir <d>] [--redis <addr>] [--launchd-log <file>] [--dry-run]",
				Example: "install --as bob --harness opencode --dir ./bob --dry-run",
				Effect:  tool.LocalWrite + ": writes the launchd agent com.nova.friend-<me> and loads it",
				Detail: `Writes ~/Library/LaunchAgents/com.nova.friend-<me>.plist (RunAtLoad, KeepAlive: started at login,
restarted when it dies, pending messages redelivered first), boots out whatever that label runs,
and bootstraps the new one; running it again replaces the agent. launchd's own log goes under
~/Library/Logs (launchd cannot open one on a network volume), and the daemon's state files and
record under ~/.nova-friend/<me> (a background process may not touch a removable volume without
the person's permission); --state-dir moves them. --coordinator is carried into the launch agent;
the run command persists it for later sleep/wake commands. --dry-run prints the plan and writes nothing.`,
				DryRun: true,
				Flags: func(f *tool.Flags) {
					daemonFlags(f)
					f.String("launchd-log", "", "launchd's stdout and stderr file (default: ~/Library/Logs/nova-friend-<me>.log)")
				},
				Run: w.install,
			},
			{
				Name:    "sleep",
				Usage:   "sleep --as <me> [--coordinator <name>] [--state-dir <d>]",
				Example: "sleep --as bob --coordinator ada",
				Effect:  tool.LocalWrite + ": records a contactable sleeping session for the daemon's next beat",
				Detail: `Stores asleep=true in the session state. The running daemon reports it on its next beat;
this command does not wake or suspend the harness, send a network message, or prove that the session
can be woken. A coordinator is required, either here or already saved in this state directory.`,
				DryRun: true,
				Flags: func(f *tool.Flags) {
					f.Required("as", "your name, the friend this state directory belongs to")
					f.String("coordinator", "", "the coordinator's name (default: the saved session state)")
					stateDir(f)
					f.Check(func(c *tool.Call) {
						if name := c.Str("as"); name != "" && bus2.CheckName(name) != "" {
							c.Problem("--as " + bus2.CheckName(name))
						}
						if coordinator := c.Str("coordinator"); coordinator != "" && bus2.CheckName(coordinator) != "" {
							c.Problem("--coordinator " + bus2.CheckName(coordinator))
						}
					})
				},
				Run: w.sleepFriend,
			},
			{
				Name:    "wake",
				Usage:   "wake --as <me> [--state-dir <d>]",
				Example: "wake --as bob",
				Effect:  tool.LocalWrite + ": clears the saved sleeping-session marker",
				Detail: `Clears asleep in the local session state. This is an explicit operator override and needs no
coordinator, so it can recover a saved sleep state whose coordinator is missing. It does not send a
network message or wake a harness; a running daemon reports the cleared state on its next beat.`,
				DryRun: true,
				Flags: func(f *tool.Flags) {
					f.Required("as", "your name, the friend this state directory belongs to")
					stateDir(f)
					f.Check(func(c *tool.Call) {
						if name := c.Str("as"); name != "" && bus2.CheckName(name) != "" {
							c.Problem("--as " + bus2.CheckName(name))
						}
					})
				},
				Run: w.wakeFriend,
			},
			{
				Name:    "uninstall",
				Usage:   "uninstall --as <me> [--dry-run]",
				Example: "uninstall --as bob --dry-run",
				Effect:  tool.LocalWrite + ": boots the agent out and removes its plist",
				DryRun:  true,
				Flags: func(f *tool.Flags) {
					f.Required("as", "your name, the friend the agent was installed for")
					f.Check(func(c *tool.Call) {
						if name := c.Str("as"); name != "" && bus2.CheckName(name) != "" {
							c.Problem("--as " + bus2.CheckName(name))
						}
					})
				},
				Run: w.uninstall,
			},
			{
				Name:    "ping",
				Usage:   "ping --as <coordinator> --to <friend> [--nonce <n>] [--since <RFC3339>] [--redis <addr>]",
				Example: "ping --as ada --to bob --nonce abc123",
				Effect:  tool.Delivery + ": one PING on the friend's stream, as the coordinator",
				Detail: `Sends "PING <nonce>" with the seat line (seat=<me> since=<RFC3339>) and the pong command the
session runs; the nonce is six random characters unless --nonce names one. Prints PING OK
nonce= id= to=. The daemon answers daemon-pong at once; the session answers pong as a turn.`,
				Flags: func(f *tool.Flags) {
					f.Required("as", "your name, the coordinator")
					f.Required("to", "the friend to ping")
					f.String("nonce", "", "the nonce to carry (default: six random characters)")
					f.String("since", "", "since when you hold the seat, RFC3339 (default: now)")
					redis(f)
				},
				Run: w.ping,
			},
			{
				Name:    "pong",
				Usage:   "pong --as <me> --nonce <n> [--to <coordinator>] [--queue <n>] [--working <n>] [--width <n>] [--state-dir <d>] [--redis <addr>]",
				Example: "pong --as bob --nonce abc123 --to ada --queue 2 --working 1 --width 4",
				Effect:  tool.Delivery + ": the session's answer to a PING, one note on the bus to the coordinator, and the pong file",
				Detail: `What the session runs when a PING <nonce> arrives, first and before anything else: sends
"pong <nonce> queue=<n> working=<n> width=<n>" to the coordinator (--to, else the seat the last
ping named, read from the status file) and records it in the state directory (~/.nova-friend/<me>,
or --state-dir as the daemon runs with), where the daemon reads it. The name is the daemon's: a
--as that is not the friend whose state is there is refused.`,
				Flags: func(f *tool.Flags) {
					f.Required("as", "your name, the friend the daemon in --dir runs as")
					f.Required("nonce", "the nonce the PING carried")
					f.String("to", "", "the coordinator (default: the seat the last ping named)")
					f.Int("queue", 0, "tasks queued, from your own task list")
					f.Int("working", 0, "tasks working, from your own task list")
					f.Int("width", 0, "your width, from the nova-config friend row")
					stateDir(f)
					f.Check(func(c *tool.Call) {
						if name := c.Str("as"); name != "" && bus2.CheckName(name) != "" {
							c.Problem("--as " + bus2.CheckName(name))
						}
					})
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
last_pong= pongs= queue= working= width= beats= delivered= sleep_requested= reported_asleep= coordinator=, from the daemon's status file and local session state (sleep_requested is saved locally; reported_asleep may lag until the daemon's next beat; daemon is up while it is
under ` + friend.DaemonStale.String() + ` old), the session's pong file and the queue file (<dir>/inbox/QUEUE.json); STATUS NONE at
exit 1 when no daemon ever ran as --as (no status file in the state directory).`,
				Flags: func(f *tool.Flags) {
					f.Required("as", "your name")
					f.Required("dir", "the friend's working directory, where the queue file lives")
					stateDir(f)
					f.Check(func(c *tool.Call) {
						if name := c.Str("as"); name != "" && bus2.CheckName(name) != "" {
							c.Problem("--as " + bus2.CheckName(name))
						}
					})
				},
				Run: w.status,
			},
		},
	}
}

// bus opens the store --redis names, or says why not (exit 2).
func (w world) bus(c *tool.Call) (*bus2.Bus, func(), *tool.Out) {
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
	return &bus2.Bus{Store: st}, closeStore, nil
}

func answer(err error) *tool.Out {
	var r *bus2.Refusal
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
func (w world) openUntil(ctx context.Context, addr string, record func(string)) (bus2.Store, func()) {
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
	if o := c.Refused(); o != nil {
		return o
	}
	name, dir, server, state := c.Str("as"), c.Str("dir"), c.Str("server"), w.stateDir(c)
	if err := w.verifyStateOwner(state, name); err != nil {
		return tool.Refuse(err.Error())
	}
	if _, err := w.readSessionConfig(state, name, c.Str("coordinator")); err != nil {
		return tool.Refuse(err.Error())
	}
	addr := c.Want("redis", "the bus store's Redis address, host:port (or "+RedisEnv+")")
	if o := c.Refused(); o != nil {
		return o
	}
	deliver, err := friend.NewDeliverer(c.Str("harness"), dir, c.Str("session"), w.exec, c.Stdout)
	if err != nil {
		o := tool.Refuse(err.Error())
		o.Render(c.Stderr, c.Bool("json"))
		return tool.Exit(2)
	}
	record := func(line string) {
		fmt.Fprintln(c.Stdout, "RUN "+line)
		_ = friend.Record(state, line) // ignored: the line is on stdout (launchd's log) whatever the volume does
	}
	ctx, stop := w.signals(context.Background())
	defer stop()
	st, closeStore := w.openUntil(ctx, addr, record)
	if st == nil {
		return tool.Exit(0) // a signal while the store was down
	}
	defer closeStore()
	d := &friend.Daemon{
		Friend: name, Harness: c.Str("harness"), Dir: dir, Width: c.Int("width"),
		StateDir: state, Coordinator: c.Str("coordinator"),
		Store: st, Deliver: deliver, Now: w.now, Pause: w.sleep,
		Beat:   func(ctx context.Context, asleep bool) error { return w.beat(ctx, server, name, asleep) },
		Record: record,
		Pong:   func() (friend.Pong, bool, error) { return friend.ReadPong(state) },
		Status: func(s friend.Status) error { return friend.WriteStatus(state, s) },
		PongCommand: func(nonce string) string {
			bin, err := w.binary()
			if err != nil {
				bin = "nova-friend" // ignored: the name on PATH stands in when this binary's path is unknown
			}
			return fmt.Sprintf("%s pong --as %s --nonce %s --state-dir %s --redis %s --width %d --queue <tasks queued> --working <tasks working>", bin, name, nonce, state, c.Str("redis"), c.Int("width"))
		},
	}
	if err := d.Run(ctx); err != nil {
		fmt.Fprintln(c.Stderr, "RUN FAIL: "+err.Error())
		return tool.Exit(1)
	}
	return tool.Exit(0)
}

func (w world) readSessionConfig(stateDir, name, coordinator string) (friend.SessionState, error) {
	s, err := friend.ReadSessionState(stateDir)
	if err != nil {
		return friend.SessionState{}, fmt.Errorf("the session state cannot be read: %w", err)
	}
	effective := coordinator
	if effective == "" {
		effective = s.Coordinator
	}
	if effective != "" && !validCoordinator(effective) {
		return friend.SessionState{}, fmt.Errorf("coordinator %q is not a valid bus name: %s", effective, bus2.CheckName(effective))
	}
	if s.Asleep && effective == "" {
		return friend.SessionState{}, fmt.Errorf("the session is asleep but has no coordinator; run: nova-friend wake --as %s --state-dir %s, or set --coordinator and start again", name, stateDir)
	}
	s.Coordinator = effective
	return s, nil
}

func validCoordinator(name string) bool { return bus2.CheckName(name) == "" }

func (w world) verifyStateOwner(stateDir, name string) error {
	status, found, err := friend.ReadStatus(stateDir)
	if err != nil {
		return fmt.Errorf("the status file cannot be read: %w", err)
	}
	if found && status.Friend != name {
		return fmt.Errorf("the daemon whose state is in %s runs as %s, not %s", stateDir, status.Friend, name)
	}
	return nil
}

func (w world) sleepFriend(c *tool.Call) *tool.Out {
	name, stateDir, coordinator := c.Str("as"), w.stateDir(c), c.Str("coordinator")
	if err := w.verifyStateOwner(stateDir, name); err != nil {
		return tool.Refuse(err.Error())
	}
	state, err := friend.ReadSessionState(stateDir)
	if err != nil {
		return tool.Refuse("the session state cannot be read: " + err.Error())
	}
	if coordinator != "" {
		state.Coordinator = coordinator
	}
	if state.Coordinator == "" {
		return tool.Refuse("sleep needs a coordinator; pass --coordinator <name> or save one with nova-friend run")
	}
	if !validCoordinator(state.Coordinator) {
		return tool.Refuse("coordinator " + fmt.Sprintf("%q", state.Coordinator) + " is not a valid name; pass a valid --coordinator")
	}
	if c.DryRun() {
		return tool.Done().Fact("asleep", true).Fact("coordinator", state.Coordinator).Fact("dry_run", true).
			Note("plan only; the session state was not changed")
	}
	state, err = friend.UpdateSessionState(stateDir, func(s *friend.SessionState) error {
		if coordinator != "" {
			s.Coordinator = coordinator
		}
		if s.Coordinator == "" {
			return fmt.Errorf("sleep needs a coordinator; pass --coordinator <name> or save one with nova-friend run")
		}
		if !validCoordinator(s.Coordinator) {
			return fmt.Errorf("saved coordinator %q is not a valid name; pass a valid --coordinator", s.Coordinator)
		}
		s.Asleep = true
		return nil
	})
	if err != nil {
		return tool.Refuse("the session state update failed: " + err.Error())
	}
	return tool.Done().Fact("asleep", true).Fact("coordinator", state.Coordinator).
		Note("the daemon's next beat reports asleep; this command does not wake or suspend the harness")
}

func (w world) wakeFriend(c *tool.Call) *tool.Out {
	name, stateDir := c.Str("as"), w.stateDir(c)
	if err := w.verifyStateOwner(stateDir, name); err != nil {
		return tool.Refuse(err.Error())
	}
	if c.DryRun() {
		state, err := friend.ReadSessionState(stateDir)
		if err != nil {
			return tool.Refuse("the session state cannot be read: " + err.Error())
		}
		return tool.Done().Fact("asleep", false).Fact("was_asleep", state.Asleep).Fact("dry_run", true).
			Note("plan only; the session state was not changed")
	}
	_, err := friend.UpdateSessionState(stateDir, func(s *friend.SessionState) error {
		s.Asleep = false
		return nil
	})
	if err != nil {
		return tool.Refuse("the session state update failed: " + err.Error())
	}
	return tool.Done().Fact("asleep", false).
		Note("the daemon's next beat reports awake; this command does not wake the harness")
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
	return friend.Agent{
		Friend: name, Harness: c.Str("harness"), Dir: c.Str("dir"), Session: c.Str("session"), StateDir: c.Str("state-dir"), Width: c.Int("width"), Coordinator: c.Str("coordinator"),
		Binary: bin, Redis: c.Str("redis"), Server: c.Str("server"), Home: w.home, Path: w.getenv("PATH"), LaunchdLog: log,
	}, nil
}

func (w world) install(c *tool.Call) *tool.Out {
	dry := c.DryRun()
	c.Want("redis", "the bus store's Redis address, host:port (or "+RedisEnv+"), written into the agent")
	if o := c.Refused(); o != nil {
		return o
	}
	a, err := w.agent(c)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	if dry {
		return tool.Done().Fact("label", a.Label()).Fact("plist", a.PlistPath()).Fact("launchd_log", a.LaunchdLog).
			Item("plan", "command", tool.Text("write "+a.PlistPath())).
			Item("plan", "command", tool.Text(fmt.Sprintf("launchctl bootout gui/%d/%s", w.uid, a.Label()))).
			Item("plan", "command", tool.Text(fmt.Sprintf("launchctl bootstrap gui/%d %s", w.uid, a.PlistPath()))).
			Note("the agent runs: nova-friend run --as " + a.Friend + " --harness " + a.Harness + " --dir " + a.Dir + " --width " + fmt.Sprint(a.Width) + ", with --redis, --server and --coordinator as given here")
	}
	path, ran, err := friend.Install(context.Background(), a, w.uid, w.launchctl, func(p string, data []byte) error {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		return os.WriteFile(p, data, 0o644)
	}, func() { w.sleep(context.Background(), time.Second) })
	o := tool.Done().Fact("label", a.Label()).Fact("plist", path).Fact("launchd_log", a.LaunchdLog)
	for _, r := range ran {
		o.Item("ran", "command", tool.Text(r))
	}
	if err != nil {
		return tool.Fail(err.Error()).Fact("plist", path)
	}
	return o.Note("check it: nova-friend status --as " + a.Friend + " --dir " + a.Dir)
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
	dir, name, state := c.Str("dir"), c.Str("as"), w.stateDir(c)
	s, found, err := friend.ReadStatus(state)
	if err != nil {
		return tool.Refuse("the status file cannot be read: " + err.Error())
	}
	session, sessionErr := friend.ReadSessionState(state)
	if !found {
		o := tool.Fail("no daemon has run as "+name+" (no status file in "+state+"); run: nova-friend install --as "+name+" --harness <h> --dir "+dir).
			As("NONE").Fact("reported_asleep", "unknown")
		if sessionErr != nil {
			o.Note("the session state: " + sessionErr.Error())
		} else {
			o.Fact("sleep_requested", session.Asleep).Fact("coordinator", dash(session.Coordinator))
		}
		return o
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
		Fact("challenge", s.Challenge).Fact("nonce", dash(s.Nonce)).Fact("last_pong", stamp(p.At)).Fact("pong_age", age(now, p.At)).Fact("pongs", s.Pongs).
		Fact("queue", queue).Fact("working", working).Fact("width", width).Fact("beats", s.Beats).Fact("last_beat", stamp(s.LastBeat)).Fact("delivered", s.Delivered).
		Fact("reported_asleep", s.Asleep)
	if sessionErr != nil {
		o.Note("the session state: " + sessionErr.Error())
	} else {
		o.Fact("sleep_requested", session.Asleep).Fact("coordinator", dash(session.Coordinator))
	}
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
	return o
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func (w world) pong(c *tool.Call) *tool.Out {
	name, nonce, state := c.Str("as"), c.Str("nonce"), w.stateDir(c)
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
	m, err := b.Send(context.Background(), bus2.Message{From: name, To: []string{to}, Subject: friend.PongSubject, Body: line + "\n"})
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
	m, err := b.Send(context.Background(), bus2.Message{From: me, To: []string{to}, Subject: friend.PingPrefix + nonce, Body: body + "\n"})
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
	floor := bus2.IDAt(storeNow.Add(-timeout)) // the log from the wait's own window back, never from its start
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
