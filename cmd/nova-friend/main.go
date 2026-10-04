// nova-friend is what a friend runs to be part of the team (docs/SPEC-FRIEND.md;
// the model is tla/Friend.tla): one launchd agent per friend that parks on the
// friend's nova-bus2 stream and pushes each message into the running session
// as a turn, beats to the sprint server while it does, answers the
// coordinator's pings at once and pushes them in so the session answers as
// its own turn, and tells the session when the coordinator goes silent. The
// verbs are run, install, uninstall, status, pong, ping and wait-pong; the
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
	"os/exec"
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

// world is what the tool reaches outside itself; main passes the real one,
// a test its own over internal/bus2's Fake, a fake harness and its own
// clock, so no test opens a socket or reads the real time.
type world struct {
	getenv    func(string) string
	open      func(ctx context.Context, addr string) (bus2.Store, func(), error)
	exec      friend.Exec
	beat      func(ctx context.Context, server, friend string) error
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
			out, err := exec.CommandContext(ctx, "launchctl", args...).CombinedOutput()
			return string(out), err
		},
		beat: func(ctx context.Context, server, name string) error {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			res, err := sprintwire.Client{Addr: server}.Do(ctx, []string{"friend", "beat", name})
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
	daemonFlags := func(f *tool.Flags) {
		f.Required("as", "your name, a nova-config friend row")
		f.Required("harness", "the harness the session runs in: "+strings.Join(friend.Harnesses, ", "))
		f.Required("dir", "the friend's working directory: the session's, and where the state files live")
		f.String("session", "", "the session to deliver into (default: the harness's newest session in --dir)")
		f.String("server", w.server(), "the sprint server, host:port (default: "+ServerEnv+", else "+DefaultServer+")")
		f.Int("width", 0, "the friend's width, from the nova-config friend row; 0 is unknown")
		redis(f)
		f.Check(func(c *tool.Call) {
			if h := c.Str("harness"); h != "" && !friend.Known(h) {
				c.Problem(fmt.Sprintf("--harness %q is no harness; it wants one of %s", h, strings.Join(friend.Harnesses, ", ")))
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
state: <dir>/.nova-friend/{status,pong}.json and deliver.log; the queue: <dir>/inbox/QUEUE.json.`,
		ExitTable: "0 done, 1 the verb ran and said no (wait-pong: no pong in time; status: no daemon), 2 could not run (a flag, an input, a store or a server that did not answer).",
		Words:     []string{"NONE"},
		Verbs: []tool.Verb{
			{
				Name:    "run",
				Usage:   "run --as <me> --harness <h> --dir <d> [--session <id>] [--server <addr>] [--width <n>] [--redis <addr>]",
				Example: "", // a daemon: the example block has no line that runs for ever
				Effect:  tool.Delivery + ": the daemon; messages go into the session, beats and pongs go out, until a signal",
				Detail: `The loop launchd runs (install writes it). Each second: one read of the stream (a message is
pushed into the session as a turn and acked when the turn ends at exit 0; a PING is answered at
once with a daemon-pong and pushed in), one beat to the sprint server, the session's pong file
read while a challenge is open, the status file written. No ping for ` + friend.Window.String() + `: the session
is told "coordinator silent" once, and "coordinator back" when pings resume. Prints one RUN
line per delivery on stdout; stops on SIGINT or SIGTERM, a delivery under way left pending.`,
				Flags: func(f *tool.Flags) { daemonFlags(f); f.Prints() },
				Run:   w.run,
			},
			{
				Name:    "install",
				Usage:   "install --as <me> --harness <h> --dir <d> [--session <id>] [--server <addr>] [--width <n>] [--redis <addr>] [--launchd-log <file>] [--dry-run]",
				Example: "install --as bob --harness opencode --dir ./bob --dry-run",
				Effect:  tool.LocalWrite + ": writes the launchd agent com.nova.friend-<me> and loads it",
				Detail: `Writes ~/Library/LaunchAgents/com.nova.friend-<me>.plist (RunAtLoad, KeepAlive: started at login,
restarted when it dies, pending messages redelivered first), boots out whatever that label runs,
and bootstraps the new one; running it again replaces the agent. launchd's own log goes under
~/Library/Logs (launchd cannot open one on a network volume); the daemon's record is
<dir>/.nova-friend/deliver.log. --dry-run prints the plan and writes nothing.`,
				DryRun: true,
				Flags: func(f *tool.Flags) {
					daemonFlags(f)
					f.String("launchd-log", "", "launchd's stdout and stderr file (default: ~/Library/Logs/nova-friend-<me>.log)")
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
				Usage:   "pong --as <me> --nonce <n> [--dir <d>] [--to <coordinator>] [--queue <n>] [--working <n>] [--width <n>] [--redis <addr>]",
				Example: "pong --as bob --nonce abc123 --dir ./bob --to ada --queue 2 --working 1 --width 4",
				Effect:  tool.Delivery + ": the session's answer to a PING, one note on the bus to the coordinator, and the pong file",
				Detail: `What the session runs when a PING <nonce> arrives, first and before anything else: sends
"pong <nonce> queue=<n> working=<n> width=<n>" to the coordinator (--to, else the seat the last
ping named, read from the status file) and records it in <dir>/.nova-friend/pong.json, where the
daemon reads it. --dir is the current directory when not given. The name is the daemon's: a --as
that is not the friend the daemon in --dir runs as is refused.`,
				Flags: func(f *tool.Flags) {
					f.Required("as", "your name, the friend the daemon in --dir runs as")
					f.Required("nonce", "the nonce the PING carried")
					f.String("dir", ".", "the friend's working directory, where the state files live")
					f.String("to", "", "the coordinator (default: the seat the last ping named)")
					f.Int("queue", 0, "tasks queued, from your own task list")
					f.Int("working", 0, "tasks working, from your own task list")
					f.Int("width", 0, "your width, from the nova-config friend row")
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
				Usage:   "status --as <me> --dir <d>",
				Example: "status --as bob --dir ./bob",
				Effect:  tool.Inspection,
				Detail: `Prints STATUS OK daemon=<up|down> harness= connection=<connected|silent> seat= last_ping= challenge=<quiet|challenged|deaf>
last_pong= pongs= queue= working= width= beats= delivered=, from the daemon's status file (up while it is
under ` + friend.DaemonStale.String() + ` old), the session's pong file and the queue file; STATUS NONE at exit 1 when no daemon
ever ran in --dir.`,
				Flags: func(f *tool.Flags) {
					f.Required("as", "your name")
					f.Required("dir", "the friend's working directory, where the state files live")
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

func (w world) run(c *tool.Call) *tool.Out {
	b, closeStore, refused := w.bus(c)
	if refused != nil {
		return refused
	}
	defer closeStore()
	name, dir, server := c.Str("as"), c.Str("dir"), c.Str("server")
	deliver, err := friend.NewDeliverer(c.Str("harness"), dir, c.Str("session"), w.exec, c.Stdout)
	if err != nil {
		o := tool.Refuse(err.Error())
		o.Render(c.Stderr, c.Bool("json"))
		return tool.Exit(2)
	}
	ctx, stop := w.signals(context.Background())
	defer stop()
	d := &friend.Daemon{
		Friend: name, Harness: c.Str("harness"), Dir: dir, Width: c.Int("width"),
		Store: b.Store, Deliver: deliver, Now: w.now, Pause: w.sleep,
		Beat: func(ctx context.Context) error { return w.beat(ctx, server, name) },
		Record: func(line string) {
			fmt.Fprintln(c.Stdout, "RUN "+line)
			_ = friend.Record(dir, line) // ignored: the line is on stdout (launchd's log) whatever the volume does
		},
		Pong:   func() (friend.Pong, bool, error) { return friend.ReadPong(dir) },
		Status: func(s friend.Status) error { return friend.WriteStatus(dir, s) },
		PongCommand: func(nonce string) string {
			bin, err := w.binary()
			if err != nil {
				bin = "nova-friend" // ignored: the name on PATH stands in when this binary's path is unknown
			}
			return fmt.Sprintf("%s pong --as %s --nonce %s --dir %s --redis %s --width %d --queue <tasks queued> --working <tasks working>", bin, name, nonce, dir, c.Str("redis"), c.Int("width"))
		},
	}
	if err := d.Run(ctx); err != nil {
		fmt.Fprintln(c.Stderr, "RUN FAIL: "+err.Error())
		return tool.Exit(1)
	}
	return tool.Exit(0)
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
		Friend: name, Harness: c.Str("harness"), Dir: c.Str("dir"), Session: c.Str("session"), Width: c.Int("width"),
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
			Note("the agent runs: nova-friend run --as " + a.Friend + " --harness " + a.Harness + " --dir " + a.Dir + " --width " + fmt.Sprint(a.Width) + ", with --redis and --server as given here")
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
	dir, name := c.Str("dir"), c.Str("as")
	s, found, err := friend.ReadStatus(dir)
	if err != nil {
		return tool.Refuse("the status file cannot be read: " + err.Error())
	}
	if !found {
		return tool.Fail("no daemon has run in " + dir + "; run: nova-friend install --as " + name + " --harness <h> --dir " + dir).As("NONE")
	}
	if s.Friend != name {
		return tool.Refuse(fmt.Sprintf("the daemon in %s runs as %s, not %s", dir, s.Friend, name))
	}
	now := w.now()
	daemon := "down"
	if now.Sub(s.At) < friend.DaemonStale {
		daemon = "up"
	}
	p, _, perr := friend.ReadPong(dir)
	queue, working, qerr := friend.ReadQueue(dir)
	width := s.Width
	if p.Width > 0 {
		width = p.Width
	}
	o := tool.Done().Fact("daemon", daemon).Fact("harness", s.Harness).Fact("status_age", age(now, s.At)).
		Fact("connection", s.Connection).Fact("seat", dash(s.Seat)).Fact("last_ping", stamp(s.LastPing)).Fact("ping_age", age(now, s.LastPing)).
		Fact("challenge", s.Challenge).Fact("nonce", dash(s.Nonce)).Fact("last_pong", stamp(p.At)).Fact("pong_age", age(now, p.At)).Fact("pongs", s.Pongs).
		Fact("queue", queue).Fact("working", working).Fact("width", width).Fact("beats", s.Beats).Fact("last_beat", stamp(s.LastBeat)).Fact("delivered", s.Delivered)
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
	dir, name, nonce := c.Str("dir"), c.Str("as"), c.Str("nonce")
	s, found, err := friend.ReadStatus(dir)
	if err != nil {
		return tool.Refuse("the status file cannot be read: " + err.Error())
	}
	if found && s.Friend != name {
		return tool.Refuse(fmt.Sprintf("the daemon in %s runs as %s, not %s: a pong carries the daemon's name", dir, s.Friend, name))
	}
	to := c.Str("to")
	if to == "" {
		to = s.Seat
	}
	if to == "" {
		return tool.Refuse("--to is required: no ping has named a seat yet in " + dir + "; it wants the coordinator's name")
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
	if err := friend.WritePong(dir, p); err != nil {
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
