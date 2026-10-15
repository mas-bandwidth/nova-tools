// nova-hook hooks an identity in: the one record, in the bus Redis, of the
// pushes its role needs (the bus, the sprint, its work), each live while its
// holder renews a lease and proven while a proof stands (docs/SPEC-HOOK.md;
// the module is hook/, the machine tla/Hook.tla). Tools gate the acting
// identity; some bus mailbox proofs are advisory unless --require-push is set
// or NOVA_BUS_REQUIRE_PUSH=1.
// The verbs are hook, prove, unhook, status, friends and serve (the heartbeat server);
// the dispatch, the banner, the help, the version verb, the refusals and the
// output envelope are pkg/tool's, and the rules are the hook module's.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/codexdesktop"
	"github.com/mas-bandwidth/nova-tools/pkg/dshdesktop"
	"github.com/mas-bandwidth/nova-tools/pkg/harness/notice"
	"github.com/mas-bandwidth/nova-tools/pkg/hookgate"
	"github.com/mas-bandwidth/nova-tools/pkg/redisconn"
	"github.com/mas-bandwidth/nova-tools/pkg/sprintwire"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
	"github.com/mas-bandwidth/nova-tools/pkg/units"
)

var version string

// Tick is how long one blocking read of the hook's loop waits: the bus
// stream is read, and the record looked at, once a tick.
const Tick = time.Second

// world is what the tool reaches outside itself: the environment, the store
// it opens, the clock, the signals the loop stops on and the nonce source.
// main passes the real one; a test passes its own over hooktest's store.
type world struct {
	codexLock          func(string) (func() error, error)
	codexValidate      func(string, string, string) error
	codexDeliver       func(context.Context, string, string, string, string) (string, error)
	codexChats         func(home string) ([]codexdesktop.Chat, error) // the desktop chats a Codex home holds
	dshValidate        func(dshdesktop.Route) error
	dshDeliver         func(dshdesktop.Route, string, string) (string, error)
	dshHeld            dshdesktop.HeldFunc // which session.lock files a process holds open
	antigravityExec    antigravityExecFunc
	antigravityHome    func() (string, error)
	antigravityInstall func(*antigravityRoute) error
	antigravityDeliver func(context.Context, *antigravityRoute, string, string) (string, error)
	claudeInstall      func(bin, as, role, state, settings string) error // the Claude Code Stop hook
	files              notice.Files                                      // the saved notice files
	getenv             func(string) string
	open               func(ctx context.Context, addr string) (st bus.Store, login string, closeStore func(), err error)
	fleetBus           func(ctx context.Context, addr string) (string, error)
	lookup             bus.Lookup
	now                func() time.Time
	signals            func(ctx context.Context) (context.Context, context.CancelFunc)
	nonce              func() string
	after              func(time.Duration) <-chan time.Time                                    // the heartbeat server's wait between passes
	wait               func(context.Context, time.Duration)                                    // the hook loop's pause or retry backoff wait
	deliver            func(*bus.Bus, bus.Message) (bus.Message, error)                        // the heartbeat server's one send: Bus.Send under a call's deadline
	holder             string                                                                  // who holds the pushes this process holds, as the lease shows it
	tick               time.Duration                                                           // one blocking read of the loop
	service            serviceManager                                                          // the one supervised hook service per friend
	sprintBatch        func(context.Context, string, ...[]string) ([]sprintwire.Result, error) // one authoritative batch of friend rows
	sprint             func(ctx context.Context, addr string, argv []string) (string, error)   // one verb to the sprint server (the lane client's ask)
	home               func() (string, error)                                                  // the user's home, where the lane client caches briefs
}

func realWorld() world {
	host, err := os.Hostname()
	if err != nil {
		host = "unknown-host" // ignored: the holder's name is a label on the lease, never a key
	}
	w := world{
		codexLock:          notice.Lock,
		codexValidate:      notice.ValidateCodex,
		codexDeliver:       codexdesktop.DeliverSaved,
		codexChats:         func(home string) ([]codexdesktop.Chat, error) { return codexdesktop.DesktopChats(os.DirFS(home)) },
		dshValidate:        notice.ValidateDsh,
		dshDeliver:         dshdesktop.Deliver,
		dshHeld:            dshdesktop.LsofHeld,
		antigravityExec:    realAntigravityExec,
		antigravityHome:    os.UserHomeDir,
		antigravityInstall: installAntigravityRoute,
		antigravityDeliver: deliverAntigravitySaved,
		claudeInstall:      notice.InstallClaude,
		files:              notice.Disk{},
		getenv:             os.Getenv,
		now:                time.Now,
		tick:               Tick,
		after:              time.After,
		deliver:            deliver,
		sprint:             sprintAsk,
		sprintBatch: func(ctx context.Context, addr string, verbs ...[]string) ([]sprintwire.Result, error) {
			return (sprintwire.Client{Addr: addr}).Do(ctx, verbs...)
		},
		home:   userHome,
		holder: fmt.Sprintf("nova-hook %s pid %d", host, os.Getpid()),
		lookup: func(ctx context.Context, h string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", h)
		},
		signals: func(ctx context.Context) (context.Context, context.CancelFunc) {
			return signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		},
		nonce: func() string {
			var raw [8]byte
			if _, err := rand.Read(raw[:]); err != nil {
				return fmt.Sprint(time.Now().UnixNano()) // ignored: no random bytes; the clock serves a nonce the stream alone carries
			}
			return hex.EncodeToString(raw[:])
		},
	}
	w.open = func(ctx context.Context, addr string) (bus.Store, string, func(), error) {
		st, login, closeStore, err := bus.OpenRedis(ctx, addr, w.getenv)
		if err != nil {
			return nil, "", nil, err
		}
		return st, login, closeStore, nil
	}
	w.fleetBus = func(ctx context.Context, addr string) (string, error) { return bus.ReadFleetBus(ctx, addr, w.getenv) }
	if os.Getenv("NOVA_TEST_NO_HOST") == "" {
		w.service = newOSServiceManager(runtime.GOOS, os.UserHomeDir, os.Executable, w.getenv, units.Load)
	}
	return w
}

// deliver is one bus message under one call's deadline.
func deliver(b *bus.Bus, m bus.Message) (bus.Message, error) {
	ctx, cancel := call()
	defer cancel()
	return b.Send(ctx, m)
}

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, realWorld())) }

// run is the entry point apart from the process.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, w world) int {
	return hookTool(w).Run(args, stdin, stdout, stderr)
}

func roleNames() string {
	var out []string
	for _, r := range hook.Roles {
		out = append(out, string(r)+" ("+pushList(hook.PushesOf(r))+")")
	}
	return strings.Join(out, ", ")
}

func pushList(ps []hook.Push) string {
	var out []string
	for _, p := range ps {
		out = append(out, string(p))
	}
	return strings.Join(out, ",")
}

func hookTool(w world) *tool.Tool {
	store := func(f *tool.Flags) {
		f.String("redis", w.getenv(bus.RedisEnv), "the bus Redis address, host:port, where the hook record lives (default: "+bus.RedisEnv+", else "+bus.SprintRedisEnv+"'s fleet:bus)")
	}
	as := func(f *tool.Flags) {
		f.String("as", "", "your name: the login user when there is one (then it may be left out)")
	}
	return &tool.Tool{
		Name:  "nova-hook",
		What:  "hooks an acting identity to its role's live, proven pushes; some bus mailbox checks are advisory",
		Stamp: version,
		How: `roles: coordinator bus+sprint; friend bus+work; comms bus; member/reader work.
strict gates require the acting name's role push to be live and proven.
nova-bus send/recv note unproven mailboxes unless --require-push or NOVA_BUS_REQUIRE_PUSH=1.
a hook proof is a protocol response only, not foreground presence or native idle-wake evidence.
the record is in bus Redis; serve checks every ` + hook.BeatEvery.String() + `; silence windows are ` + hook.BusyQuietAfter.String() + `/` + hook.QuietAfter.String() + `/` + hook.CoordinatorEvery.String() + `.`,
		ExitTable: "0 done (status: proven), 1 the verb ran and said no (not hooked in, a challenge not answered, unhooked under the loop), 2 could not run (a flag, an input, a store that did not answer).",
		Words:     []string{"ARMED", "CHALLENGE", "PUSH", "STATE", "STOP", "DRY-RUN"},
		UsageNote: "only the model answers a challenge, with nova-hook prove from inside its own turn; no daemon, loop or script may call prove.",
		Verbs: []tool.Verb{
			{
				Name:    "status",
				Usage:   "status [--as <name>] [--max <n>] [--redis <addr>]",
				Example: "status --as m1",
				Effect:  tool.Inspection,
				Detail: `With --as, one name's record: STATUS OK when it is proven (exit 0), else STATUS FAILED with what is not
live and the command that hooks it in (exit 1). Without it, every name that ever hooked in: STATUS OK count=<n>
proven=<n>, then one STATUS NAME name= state= role= epoch= <push>=<down|armed|proven> line each, at exit 0.
The states: none, unhooked, expired (no push live), partial, live (every push live, not every one proven), proven;
and down for a proven name with no evidence: no heartbeat server ever ran, none passed for two of its intervals
(hook server stale), or its activity and any outstanding challenge are past their deadlines.
Activity is an explicit model bus send or a nonce proof; quiet windows are role-specific and a pending challenge
has its full answer bound. Intentional silence between challenges is healthy while the server passes.
Without --as the first line carries serve_age=.
example: nova-hook status --as ada`,
				Flags: func(f *tool.Flags) {
					f.String("as", "", "the name whose record to read (default: every name that ever hooked in)")
					f.Max()
					store(f)
				},
				Run: w.status,
			},
			{
				Name:    "hook",
				Usage:   "hook --as <me> --role <role> [--once] [--harness <name>] [--redis <addr>] [--dry-run]",
				Example: "hook --as m1 --role member --once",
				Effect:  tool.Delivery + ": writes the hook record and the lease of every push it holds, and streams the bus push until a signal",
				DryRun:  true,
				Detail: `Hooks you in as --role (` + roleNames() + `): a record already hooked as that role is joined at
its epoch, otherwise a new epoch starts and every lease and proof of the old one is cleared. This process holds
the bus push when the role carries it: it renews the lease every ` + hook.RenewEvery.String() + ` and prints each message on your bus
stream as it lands, HOOK PUSH push=bus id= from= subject=. A push it does not hold (the sprint push, held by
nova-sprint run; the work push, held by the friend daemon or the member loop) is named with its holder on the
HOOK ARMED line's notes. A standalone loop asks at start and every ` + hook.ChallengeEvery.String() + `.
Codex, DeepSeek and Antigravity clients bootstrap once. The heartbeat server checks every hooked name whose
role carries the bus push every ` + hook.BeatEvery.String() + `; it challenges after ` + hook.BusyQuietAfter.String() + ` of silence for a friend
holding ready or working sprint cards, ` + hook.QuietAfter.String() + ` for an idle friend or comms name, and ` + hook.CoordinatorEvery.String() + ` for a coordinator.
Proof renewal starts ` + (hook.ProofTTL - hook.AnswerBound - hook.BeatEvery).String() + ` after the last proof, reserving the answer bound and two pass intervals. Run the line it carries,
nova-hook prove --as <me> --nonce <n>, within ` + hook.AnswerBound.String() + `, and the pushes it holds are proven. HOOK STATE state= line
each time the record changes. Run it under the session's Monitor: each line is a notification. It stops on SIGINT or
SIGTERM (HOOK STOP, exit 0; the leases lapse after ` + hook.LeaseTTL.String() + `), or when the record is unhooked under it (exit 1).
This proof records a protocol response to the challenge; it does not establish foreground session presence or native idle-wake behavior.
Codex desktop delivery uses --codex-socket, --codex-thread and --codex-events-dir together (or NOVA_HOOK_CODEX_SOCKET,
NOVA_HOOK_CODEX_THREAD, NOVA_HOOK_CODEX_EVENTS_DIR). The directory must exist, be private (0700), canonical and owned
by the current user. The route probes the native thread owner before arming. Each event is saved as external JSON
and one fixed native notice names that file; message body and subject never enter native turn text. Native acceptance
is not proof or acknowledgment: only the model runs nova-hook prove or answers with nova-bus send --re. A local failure
revokes route proof and stops renewal; unknown native outcomes are reported without declaring the model absent.
Do not blindly restart after accepted or unknown delivery: inspect the saved event and model answer first.
An explicit --codex-since UTC timestamp (or NOVA_HOOK_CODEX_SINCE) declares earlier ordinary messages archived history: work still needed must be resent. Persist that fixed timestamp in launch configuration; never reset it on restart. Current server challenge proof is independent of this boundary.
Codex, DeepSeek and Antigravity clients only bootstrap; the heartbeat server owns subsequent challenges. Other routes keep their existing cadence.
Muse clients use --harness Muse: before arming it installs the nova-hook plugin bundle (--muse-bundle, or
NOVA_HOOK_MUSE_BUNDLE, or muse-bundle beside the binary) with muse plugins install and approves its one hook
capability (pending-challenges, PostToolUse) with muse plugins approve. The hook only reads nova-bus peek and
prints pending lines; the model proves. --dry-run installs nothing.
Grok clients use --harness grok (docs/SPEC-GROK-HOOK.md): before arming it installs hooks into --grok-home
(or GROK_HOME, else the grok directory under the user home) that record busy and idle, and creates one wake
file. One monitor watches that file or this process, not both. At most one notice is pending; a burst folds
into "N more messages; read the bus". The message body stays on the bus. The model reads the bus. --dry-run
writes nothing. --once installs and arms, and does not stream.
Antigravity desktop delivery uses --harness antigravity with --antigravity-dir <workspace> or --antigravity-session <uuid>
(also --session, --antigravity-server, --antigravity-events-dir and --antigravity-since, or NOVA_HOOK_ANTIGRAVITY_*).
Before arming, it installs its own harness setup (<home>/.gemini/antigravity/bin/agentapi and the private 0700 events
directory), discovers the live Antigravity language_server and conversation, saves each event as bounded external JSON,
and delivers via agentapi send-message --title=nova-hook.
Claude Code clients use --harness claude with --claude-state and --claude-settings (existing directories): before arming
it installs a Stop hook that advances the generation file; the session's Monitor on this process's stdout is the delivery.
Every harness keeps one pending notice (pkg/harness/notice): a saved file rewritten in place with the newest valid
challenge and the message count; a new push waits until the model consumed the last one; only the model proves.
--once arms, renews once, sets the challenge and prints the record's state, at exit 0 when proven, else 1 with the
remedy: what a daemon's start does, and what a role whose holders are daemons runs to hook in again after an unhook.
--dry-run reads the record and prints the plan the real run would take (the role, the epoch, the pushes held and
each other push's holder) and writes nothing.
example: nova-hook hook --as ada --role coordinator`,
				Flags: func(f *tool.Flags) {
					as(f)
					f.Required("role", "the role you hook in as, one of "+roleNames())
					f.String("harness", w.getenv("NOVA_HOOK_HARNESS"), "install a harness adapter before arming: grok, muse, claude, deepseek (alias dsh), antigravity, or codex (the Codex desktop route: the friend's current chat, followed to a new one; the --codex-* flags override its defaults)")
					f.String("grok-home", w.getenv("GROK_HOME"), "absolute grok config directory the adapter installs into; empty uses GROK_HOME, else the grok directory under the user home")
					f.String("muse-bundle", w.getenv("NOVA_HOOK_MUSE_BUNDLE"), "the Muse plugin bundle to install (default: muse-bundle beside the nova-hook binary)")
					f.String("muse-bin", w.getenv("NOVA_HOOK_MUSE_BIN"), "the Muse CLI to run (default: muse on PATH)")
					f.String("codex-socket", w.getenv("NOVA_HOOK_CODEX_SOCKET"), "native Codex desktop socket (default: CODEX_HOME/ipc/ipc.sock)")
					f.String("codex-thread", w.getenv("NOVA_HOOK_CODEX_THREAD"), "native recipient thread ID; an override: by default the route finds the friend's current desktop chat (newest in a workspace named for the friend) and follows the friend to a new one")
					f.String("codex-events-dir", w.getenv("NOVA_HOOK_CODEX_EVENTS_DIR"), "private directory for bounded external event JSON (default: ~/.local/state/nova-hook/<name>-codex, created 0700)")
					f.String("codex-since", w.getenv("NOVA_HOOK_CODEX_SINCE"), "fixed cutover UTC timestamp (RFC3339 seconds); by default the store's time at the first hook, kept in the events directory's .cutover; earlier work must be resent")
					f.String("dsh-events-dir", w.getenv("NOVA_HOOK_DSH_EVENTS_DIR"), "private directory for bounded external event JSON and the native notification queue (default ~/.local/state/nova-hook/<as>-dsh, created 0700)")
					f.String("dsh-hooks-file", w.getenv("NOVA_HOOK_DSH_HOOKS_FILE"), "absolute native Cordis include .json path, mounted into the harness desktop profile (default ~/.dsh/<as>-hook/hooks.json)")
					f.String("dsh-session", w.getenv("NOVA_HOOK_DSH_SESSION"), "pin one existing harness session (default: the newest live session in the friend's workspace, followed when she starts another); never creates or resumes a session")
					f.String("dsh-since", w.getenv("NOVA_HOOK_DSH_SINCE"), "fixed cutover UTC timestamp (RFC3339 seconds); by default the store's time at the first hook, kept in the events directory's .cutover; earlier work must be resent")
					f.String("antigravity-dir", w.getenv("NOVA_HOOK_ANTIGRAVITY_DIR"), "Antigravity workspace directory for --harness antigravity")
					f.String("antigravity-session", w.getenv("NOVA_HOOK_ANTIGRAVITY_SESSION"), "Antigravity conversation UUID (default: newest root conversation for --antigravity-dir)")
					f.String("session", w.getenv("NOVA_HOOK_ANTIGRAVITY_SESSION"), "Antigravity conversation UUID alias for --antigravity-session")
					f.String("antigravity-server", w.getenv("NOVA_HOOK_ANTIGRAVITY_SERVER"), "optional Antigravity server host:port override")
					f.String("antigravity-events-dir", w.getenv("NOVA_HOOK_ANTIGRAVITY_EVENTS_DIR"), "private directory for Antigravity saved event JSON (default: ~/.gemini/antigravity/nova-hook/<as>)")
					f.String("antigravity-since", w.getenv("NOVA_HOOK_ANTIGRAVITY_SINCE"), "fixed cutover UTC timestamp (RFC3339 seconds) for Antigravity recovery")
					f.String("claude-state", w.getenv("NOVA_HOOK_CLAUDE_STATE"), "existing directory whose generation file marks a Claude Code turn boundary")
					f.String("claude-settings", w.getenv("NOVA_HOOK_CLAUDE_SETTINGS"), "existing directory where the Claude Code Stop hook is installed")
					f.Bool("claude-boundary", false, "advance the Claude Code generation file and exit; the installed Stop hook runs this")
					f.Bool("once", false, "arm, renew once, print the state and exit (0 proven, 1 not); no loop")
					laneFlags(w, f)
					store(f)
					f.Check(func(c *tool.Call) {
						if r := c.Str("role"); r != "" {
							if _, err := hook.ParseRole(r); err != nil {
								c.Problem(err.Error())
							}
						}
					})
				},
				Run: w.hook,
			},
			{
				Name:    "prove",
				Usage:   "prove --as <me> --nonce <n> [--redis <addr>] [--dry-run]",
				Example: "", // the nonce is the one a hook's stream printed: no example line can carry it
				Effect:  tool.Delivery + ": writes the proof of each push the challenge named",
				DryRun:  true,
				Detail: `Answers the challenge the hook printed (HOOK CHALLENGE nonce=<n>), or the heartbeat's (a bus message with the
subject HOOK CHALLENGE nonce=<n>): with that nonce, under the record's epoch, every push the challenge named whose
lease is live is proven for ` + hook.ProofTTL.String() + `, the challenge is spent and the heartbeat's misses are cleared.
A heartbeat nonce answers while it is inside the server's down-after window (serve --down-after), even after
newer challenges were issued: a harness that hands the model a challenge late still proves it, and the answer
counts at the time of the proof. Only the model answers, from inside its own turn: no daemon, loop or script may
call prove, since an answer is the proof that the model itself read the push. Prints PROVE OK proven=<n>, the
record's state, matched=newest or matched=<k>-back (the challenge the nonce answered, k before the newest) and
issued=<time> for a heartbeat challenge. A nonce the heartbeat never issued to this name, one from another epoch
or older than the window (the refusal names the window), a challenge answered or lapsed, or a record unhooked
since is FAILED at exit 1, with the reason.
--dry-run reads the record and prints what it would prove (the state, the role and how many live pushes a
proof would cover) and writes nothing.
example: nova-hook prove --as ada --nonce 9f2c4e1a0b3d5c7e`,
				Flags: func(f *tool.Flags) {
					as(f)
					f.Required("nonce", "the nonce the newest HOOK CHALLENGE line printed")
					store(f)
				},
				Run: w.prove,
			},
			{
				Name:    "unhook",
				Usage:   "unhook --as <me> [--redis <addr>] [--dry-run]",
				Example: "unhook --as m1",
				Effect:  tool.Delivery + ": unhooks the record: the epoch moves on, every lease, proof and challenge is cleared",
				DryRun:  true,
				Detail: `Unhooks you: from now every bus, friend and sprint tool refuses your name until you hook in again, and a
running holder (a hook loop, a friend daemon, a member loop) holds nothing more until someone hooks you in as
its role. Prints UNHOOK OK was=<hooked|unhooked|none> epoch=<n>. --dry-run prints what the record is and writes nothing.
example: nova-hook unhook --as ada`,
				Flags: func(f *tool.Flags) {
					as(f)
					store(f)
				},
				Run: w.unhook,
			},
			{
				Name:    "friends",
				Usage:   "friends [--json] [--as <me>] [--redis <addr>]",
				Example: "", // the listing needs a bus store with hook records: no example line can carry it
				Effect:  tool.Inspection,
				Detail: `Every name's state from the hook record alone, by the store's TIME: FRIENDS OK count= up= coordinator=
serve_alive= serve_age=, then one FRIENDS NAME line each: name, role, state (up: hooked and proven; down: not),
since, reason, last_challenge_at, last_answer_at (the heartbeat's) and misses. No sprint state (a hold is the
sprint's). The lines stop at ` + fmt.Sprint(friendsShown) + ` names with a MORE line; --json carries every name.`,
				Flags: func(f *tool.Flags) {
					f.String("as", "", "your name, optional: the listing is the same for every reader")
					store(f)
				},
				Run: w.friends,
			},
			{
				Name:    "serve",
				Usage:   "serve --as <me> [--every <d>] [--down-after <d>] [--judge-within <d>] [--once] [--redis <addr>]",
				Example: "", // a server needs a hooked name and a live bus store: no example line can carry it
				Effect:  tool.Delivery + ": challenges every hooked name on its bus, unhooks a name silent for --down-after, and expires overdue asks",
				Detail: `The heartbeat server (docs/SPEC-HOOK.md, the heartbeat; tla/HookBeat.tla). Every --every (default ` + hook.BeatEvery.String() + `),
in one atomic pass by the store's TIME, it checks every hooked coordinator, friend and comms name.
A friend holding ready or working sprint cards is challenged after ` + hook.BusyQuietAfter.String() + ` of silence;
an idle friend or comms name after ` + hook.QuietAfter.String() + `; a coordinator after ` + hook.CoordinatorEvery.String() + `.
The sprint server's friend cards answers choose busy or idle; an unavailable answer uses the shorter window
and prints a fault. Last activity is the latest nonce proof or explicit model bus send, with arming as the
initial boundary. Daemon traffic never counts. Proof renewal starts at proof TTL minus the answer bound and two pass intervals (default ` + (hook.ProofTTL - hook.AnswerBound - hook.BeatEvery).String() + ` after proof).
Only one challenge stands, delivered to the name's bus stream with subject HOOK CHALLENGE nonce=<n>.
Its answer is due within ` + hook.AnswerBound.String() + `. Past that deadline, a name whose activity is older than
--down-after (default ` + hook.DownAfter.String() + `) is unhooked. Passes never replace an outstanding nonce.
A bus send does not create or extend a proof; only the model answers with nova-hook prove.
Each real change is told once, coalesced per pass: the coordinator gets one judgment (bus kind judgment) naming
every FRIEND DOWN and FRIEND BACK, open until answered with nova-bus send --re <id> and pushed again as OVERDUE
once past --judge-within; every other live name gets one message with the changes and COORDINATOR IS <name>,
a judgment instead when the coordinator went down (COORDINATOR DOWN: who takes over). The pass also expires
overdue bus asks. Two servers take one pass per interval between them. Each line is SERVE STATE event=<challenge|
judgment|told|overdue|answered|expired|skip|fault>; a fault is event=fault and the loop goes on. --as must be hooked in as coordinator or comms.
--once runs one pass and exits 0. It stops on SIGINT or SIGTERM.
example: nova-hook serve --as ada --once`,
				Flags: func(f *tool.Flags) {
					as(f)
					f.Duration("every", hook.BeatEvery, "the interval between server passes; challenges use role silence and proof expiry")
					f.String("sprint-server", orDefault(w.getenv(sprintServerEnv), DefaultSprintServer), "the authoritative sprint server for ready and working friend cards, host:port")
					f.Duration("down-after", hook.DownAfter, "how old model activity must be before a challenge past its answer bound unhooks the name; at least --every")
					f.Duration("judge-within", 2*time.Minute, "how long a judgment waits for its answer before it is pushed again, once, as OVERDUE")
					f.Bool("once", false, "run one pass and exit 0; no loop")
					store(f)
				},
				Run: w.serve,
			},
			{
				Name:    "pending",
				Usage:   "pending [--as <name>]",
				Example: "", // the bundle's hook runs it; no hand run can carry its bus
				Effect:  tool.Inspection,
				Hidden:  true,
				Detail: `The Muse bundle's PostToolUse hook: discards stdin and prints the one pending notice line
the hook left for the name, so the newest challenge reaches the model inside its turn. It only reads: it
never proves, sends, acks or writes, and it always exits 0; no notice prints nothing.`,
				Flags: func(f *tool.Flags) {
					f.Prints()
					f.String("as", w.getenv("NOVA_HOOK_AS"), "the name whose pending notice to print (default: NOVA_HOOK_AS, else aurora)")
				},
				Run: w.pending,
			},
		},
	}
}

// session is one verb's open store: the bus store, its hook record and the
// identity the verb acts as.
type session struct {
	addr  string
	bus   bus.Store
	hooks hook.Store
	as    string
	close func()
}

// open opens the store a verb names and says who acts (bus.Address,
// bus.Identity), or the result that stops it. needAs false lets a verb run
// with no identity (status without --as).
func (w world) openSession(c *tool.Call, needAs bool) (*session, *tool.Out) {
	ctx, cancel := context.WithTimeout(context.Background(), redisconn.OpenTimeout)
	defer cancel()
	addr, refusal := bus.Address(ctx, c.Str("redis"), w.getenv, w.fleetBus)
	if refusal != "" {
		return nil, tool.Refuse(refusal)
	}
	if why := bus.CheckAddr(ctx, addr, w.lookup); why != "" {
		return nil, tool.Refuse(why)
	}
	st, login, closeStore, err := w.open(ctx, addr)
	if err != nil {
		return nil, tool.Refuse(err.Error())
	}
	hs, err := hookgate.Store(st)
	if err != nil {
		closeStore()
		return nil, tool.Refuse(err.Error())
	}
	s := &session{addr: addr, bus: st, hooks: hs, close: closeStore}
	if needAs || c.Str("as") != "" || login != "" {
		as, refusal := bus.Identity(c.Str("as"), login)
		if refusal != "" {
			closeStore()
			return nil, tool.Refuse(refusal)
		}
		s.as = as
	}
	return s, nil
}

// call is a context bounded by one call's deadline.
func call() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), bus.CallTimeout)
}

// line prints one line of a verb that prints as it goes.
func line(c *tool.Call, o *tool.Out) {
	o.Verb = "hook"
	o.Render(c.Stdout, false)
}

// stateLine is the record's state as one HOOK STATE line, with the breadcrumb
// as its notes when it is not proven.
func stateLine(st hook.Status, role hook.Role) *tool.Out {
	o := tool.Done().As("STATE").Fact("state", st.State).Fact("role", string(st.Role)).Fact("epoch", st.Epoch)
	for _, p := range st.Pushes {
		o.Fact(string(p.Push), p.State)
	}
	if st.State != hook.StateProven {
		if r := st.Gate(hook.Want{Name: st.Name, Role: role}); r != nil {
			for _, l := range r.Lines {
				o.Note(l)
			}
		}
	}
	return o
}

// holds is the pushes this process holds for a role: the bus push, when the
// role carries it; every other push has its own holder (hook.HeldBy).
func holds(r hook.Role) []hook.Push {
	if slices.Contains(hook.PushesOf(r), hook.Bus) {
		return []hook.Push{hook.Bus}
	}
	return nil
}

// hook is the verb: arm, hold, challenge, then stream until a signal or an unhook
// (docs/SPEC-HOOK.md, the hook verb; tla/Hook.tla, Arm, Renew and Challenge).
func (w world) hook(c *tool.Call) *tool.Out {
	native, nativeErr := w.codexConfig(c)
	if nativeErr != nil {
		return tool.Refuse(nativeErr.Error())
	}
	var dsh *dshRoute
	var muse *museRoute
	var grokR *grokRoute
	var claude *claudeRoute
	switch strings.ToLower(c.Str("harness")) {
	case "muse":
		var err error
		muse, err = w.museConfig(c)
		if err != nil {
			return tool.Refuse(err.Error())
		}
	case hookHarnessGrok:
		var err error
		grokR, err = w.grokConfig(c)
		if err != nil {
			return tool.Refuse(err.Error())
		}
	case "claude":
		var err error
		claude, err = claudeConfig(c, native)
		if err != nil {
			return tool.Refuse(err.Error())
		}
		if claude.boundary {
			if c.DryRun() {
				return tool.Refuse("claude boundary writes the generation file; omit --dry-run")
			}
			n, err := notice.ClaudeBoundary(claude.state)
			if err != nil {
				return tool.Refuse("claude generation could not be written: " + err.Error())
			}
			return tool.Done().As("STATE").Fact("state", "boundary").Fact("generation", n)
		}
	case "", "deepseek", "dsh":
		var err error
		dsh, err = w.dshConfig(c)
		if err != nil {
			return tool.Refuse(err.Error())
		}
	case "antigravity", "codex":
	default:
		return tool.Refuse("--harness wants muse or antigravity (or grok, claude, deepseek, dsh); the Codex desktop route uses --codex-* flags")
	}

	if (muse != nil || grokR != nil || claude != nil) && (c.Str("dsh-events-dir") != "" || c.Str("dsh-hooks-file") != "" || c.Str("dsh-since") != "" || c.Str("dsh-session") != "") {
		return tool.Refuse("harness delivery routes are exclusive; configure one harness route")
	}
	if native != nil && (dsh != nil || muse != nil) {
		return tool.Refuse("native harness delivery routes are exclusive; configure one harness route")
	}
	if grokR != nil && (native != nil || muse != nil) {
		return tool.Refuse("one harness at a time: --harness grok, --harness muse, or the Codex desktop route")
	}
	if muse != nil && !c.DryRun() {
		if out := w.museInstall(c, muse); out != nil {
			return out
		}
	}
	s, refused := w.openHookSession(c)
	if refused != nil {
		return refused
	}
	defer s.close()
	role, err := hook.ParseRole(c.Str("role"))
	if err != nil {
		return tool.Refuse(err.Error())
	}
	if grokR != nil && !slices.Contains(holds(role), hook.Bus) {
		o := tool.Refuse("grok delivery wants a role that holds the bus push")
		o.Remedy = grokRemedy(grokR)
		return o
	}
	if c.DryRun() {
		ctx, cancel := call()
		defer cancel()
		o := w.hookPlan(ctx, s, role)
		if grokR != nil && o.Status == tool.OK {
			o.Note("would install the grok harness into " + grokR.home + " and write nothing")
		}
		return o
	}
	held := holds(role)
	if native != nil && !slices.Contains(held, hook.Bus) {
		return tool.Refuse(native.label() + " delivery wants a role with the bus push")
	}
	if (dsh != nil || claude != nil || muse != nil) && !slices.Contains(held, hook.Bus) {
		return tool.Refuse("harness delivery wants a role with the bus push")
	}
	if !w.isServiceRun() {
		hctx, hcancel := call()
		svc, heldByService := w.heldService(hctx, s)
		hcancel()
		if heldByService {
			return tool.Refuse(fmt.Sprintf("%s is already held by supervised service %s; run nova-hook unhook --as %s first", s.as, svc, s.as))
		}
	}
	if w.service != nil && !c.Bool("once") && !w.isServiceRun() && (strings.TrimSpace(c.Str("harness")) != "" || native != nil || dsh != nil || claude != nil) {
		dshRestart := false
		if dsh != nil {
			var err error
			if dshRestart, err = dshSetup(dsh); err != nil {
				return tool.Refuse("dsh harness-side setup failed: " + err.Error())
			}
		}
		if grokR != nil {
			grokR.as = s.as
			grokR.role = role
			if out := w.installGrok(c, grokR); out != nil {
				return out
			}
		}
		if claude != nil {
			if out, _ := w.installClaude(c, claude, string(role)); out != nil {
				return out
			}
		}
		svc, err := w.service.Install(s.as, hookServiceArgs(c, s.as, role, s.addr))
		if err != nil {
			return tool.Refuse("supervised service could not be installed: " + err.Error())
		}
		sctx, scancel := call()
		recErr := w.recordService(sctx, s, svc)
		scancel()
		if recErr != nil {
			if rmErr := w.service.Remove(s.as); rmErr != nil {
				return tool.Refuse("the hook record could not be written: " + recErr.Error() + " (and service rollback failed: " + rmErr.Error() + ")")
			}
			return tool.Refuse("the hook record could not be written: " + recErr.Error())
		}
		o := tool.Done().Fact("as", s.as).Fact("role", string(role)).Fact("service", svc)
		o.Note("supervised hook service installed and started; only nova-hook unhook --as " + s.as + " stops it")
		if dsh != nil {
			o.Fact("dsh_session", dsh.route.SessionID)
			if dshRestart {
				o.Note(dshRestartNote)
			}
		}
		return o
	}
	var shim notice.Shim
	locked := ""
	switch {
	case dsh != nil:
		locked = dsh.route.EventsDir
	case native != nil:
		locked = native.directory
	}
	if locked != "" {
		release, err := w.codexLock(locked)
		if err != nil {
			return tool.Refuse("the harness route is not exclusively held: " + err.Error())
		}
		defer func() {
			if err := release(); err != nil {
				line(c, tool.Done().As("STATE").Fact("state", "unlock-failed").Note(err.Error()))
			}
		}()
	}
	switch {
	case dsh != nil:
		restart, err := dshSetup(dsh)
		if err != nil {
			return tool.Refuse("dsh harness-side setup failed: " + err.Error())
		}
		st := tool.Done().As("STATE").Fact("state", "native-include-mounted").Fact("path", dsh.route.HooksFile).Fact("session", dsh.route.SessionID)
		if restart {
			st.Note(dshRestartNote)
		}
		line(c, st)
		shim = &notice.Dsh{Route: dsh.route, Deliver: w.dshDeliver, Discover: dsh.discover}
	case grokR != nil:
		grokR.as = s.as
		grokR.role = role
		if out := w.installGrok(c, grokR); out != nil {
			return out
		}
		shim = &notice.Grok{Home: grokR.home, Wake: grokR.wake, Notices: grokR.notices}
	case claude != nil:
		out, sh := w.installClaude(c, claude, string(role))
		if out != nil {
			return out
		}
		shim = sh
	case muse != nil:
		shim = &notice.Muse{Notices: muse.notices}
	case native != nil:
		if native.antigravity != nil {
			if out := w.probeAntigravity(c, native.antigravity); out != nil {
				return out
			}
		}
		shim = w.nativeShim(native)
	}
	var recovery codexRecovery
	// Capture the boundary before publishing any lease, then cover everything
	// through that boundary before streaming strictly after it.
	cursor, refused := w.cursor(s, held)
	if refused != nil {
		return refused
	}
	if native != nil {
		var err error
		if native.since.IsZero() && native.cutoverFile != "" {
			if native.since, err = codexCutover(s, native.cutoverFile); err != nil {
				return tool.Refuse(err.Error())
			}
		}
		recovery, err = codexBacklog(s, cursor, native.since)
		if err != nil {
			return tool.Refuse(err.Error())
		}
	}
	// the identity's own word (hook.Store.HookAs): it joins a record hooked as role,
	// and otherwise arms a new epoch: after an unhook, or over a record hooked as
	// another role, which it takes over (a holder's start never does either)
	ctx, cancel := call()
	took := ""
	if st, _, rerr := s.hooks.Read(ctx, s.as); rerr == nil && st[0].Role != role && st[0].State != hook.StateNone && st[0].State != hook.StateUnhooked {
		took = string(st[0].Role)
	}
	epoch, joined, err := s.hooks.HookAs(ctx, s.as, role, w.holder)
	if err == nil && w.isServiceRun() {
		err = w.recordService(ctx, s, defaultServiceName(s.as))
	}
	cancel()
	if err != nil {
		return tool.Refuse("the hook record could not be written: " + err.Error())
	}
	if native != nil {
		recovery.afterEpoch(epoch)
	}
	if err := w.renew(s, role, held, epoch); err != nil {
		return tool.Refuse("the lease could not be written: " + err.Error())
	}
	var core *notice.Core
	if shim != nil {
		core = w.newCore(c, s, role, epoch, shim)
	}
	// serverOwned: the native routes only bootstrap; the heartbeat server owns
	// every later challenge. Other routes keep their own cadence.
	serverOwned := native != nil || dsh != nil

	armed := tool.Done().As("ARMED").Fact("as", s.as).Fact("role", string(role)).Fact("epoch", epoch).Fact("joined", joined).Fact("hold", pushList(held))
	if took != "" {
		armed.Note("the record was hooked as " + took + ": it is " + string(role) + " from now, and the holders of " + took + " hold nothing until it is hooked as " + took + " again")
	}
	for _, p := range hook.PushesOf(role) {
		if !slices.Contains(held, p) {
			armed.Note(fmt.Sprintf("the %s push is held by: %s", p, hook.HeldBy(s.as, role, p)))
		}
	}
	if native != nil && native.antigravity != nil && !c.Bool("once") {
		line(c, tool.Done().As("STATE").Fact("state", "antigravity-installed").Fact("conversation", native.antigravity.conversation).Fact("port", native.antigravity.port).Fact("events_dir", native.directory))
	}
	cutover := time.Time{}
	if native != nil {
		cutover = native.since
		if cutover.IsZero() && !recovery.cutover.IsZero() {
			cutover = recovery.cutover
		}
	}
	if native != nil && !cutover.IsZero() {
		cutoverLine := tool.Done().As("STATE").Fact("state", "recovery-cutover").Fact("since", cutover.Format(time.RFC3339))
		if recovery.archived > 0 {
			cutoverLine.Fact("archived", recovery.archived)
		}
		line(c, cutoverLine.Note("earlier ordinary messages are archived history; work still needed must be resent; retain this fixed boundary across restarts"))
	}
	if native != nil && recovery.coverageUnknown {
		line(c, tool.Done().As("STATE").Fact("state", "recovery-coverage-unknown").Fact("oldest", recovery.oldest).Note("24h coverage before oldest retained entry is unverified; coordinator must inspect retention before cutover"))
	}
	once := c.Bool("once")
	if !once {
		line(c, armed)
	}
	challenge := func(epoch int64) *tool.Out {
		if len(held) == 0 {
			return nil
		}
		nonce := w.nonce()
		ctx, cancel := call()
		defer cancel()
		created, err := s.hooks.Challenge(ctx, s.as, role, epoch, held, nonce)
		if err != nil {
			return tool.Refuse("the challenge could not be written: " + err.Error())
		}
		if core != nil && serverOwned {
			// The bootstrap is the Core's first notice: held as pending like any other.
			if native != nil {
				r := &notice.Recovery{Messages: recovery.messages}
				if !cutover.IsZero() {
					r.Since = cutover.Format(time.RFC3339)
				}
				if recovery.coverageUnknown {
					r.Coverage = "24h history before oldest retained entry " + recovery.oldest + " is unverified; stream creation and trimming are indistinguishable"
				}
				core.Recover(r)
				recovery = codexRecovery{}
			}
			var boot []bus.Message
			if created {
				boot = append(boot, bus.Message{ID: "bootstrap-" + nonce, From: s.as, Subject: "HOOK CHALLENGE nonce=" + nonce, Body: notice.ChallengeText(s.as, nonce, hook.ChallengeEvery)})
			}
			if err := core.Offer(ctx, boot); err != nil {
				return core.Fail(ctx, err)
			}
			if err := core.Step(ctx); err != nil {
				return core.Fail(ctx, err)
			}
		}
		if !created {
			return nil
		}

		o := tool.Done().As("CHALLENGE").Fact("nonce", nonce).Fact("push", pushList(held)).Fact("within", hook.AnswerBound.String())
		o.Note("run: nova-hook prove --as " + s.as + " --nonce " + nonce)
		return o
	}
	ch := challenge(epoch)
	if ch != nil && ch.Status != tool.OK {
		return ch
	}
	if once {
		return w.onceResult(s, role, held, armed, ch)
	}
	if ch != nil {
		line(c, ch)
	}
	return w.loop(c, s, role, held, epoch, cursor, challenge, core, serverOwned, w.laneClient(c, s.as, role))
}

// nativeShim is the Codex desktop route's shim, or the Antigravity route's.
func (w world) nativeShim(r *codexRoute) notice.Shim {
	if ag := r.antigravity; ag != nil {
		return &notice.Antigravity{Directory: r.directory, Token: func() string { return ag.csrfToken },
			Deliver: func(ctx context.Context, id, dir string) (string, error) {
				return w.antigravityDeliver(ctx, ag, id, dir)
			}}
	}
	return &notice.Codex{Socket: r.socket, Thread: r.thread, Directory: r.directory, Deliver: w.codexDeliver, Discover: r.discover}
}

// newCore wires one friend's pending notice: the store, the shim, the saved
// files, and the one PUSH line a push prints.
func (w world) newCore(c *tool.Call, s *session, role hook.Role, epoch int64, shim notice.Shim) *notice.Core {
	return &notice.Core{As: s.as, Role: role, Epoch: epoch, Redis: s.addr, Store: notice.Redis{Hooks: s.hooks, Bus: s.bus}, Shim: shim, Files: w.files,
		NewID: func() string { return "notice-" + w.nonce() }, Say: func(o *tool.Out) { line(c, o) }}
}

// cursor is the bus stream's last entry id when this process holds the bus
// push ("0-0" for an empty stream): the hook's pushes are the entries after it.
func (w world) cursor(s *session, held []hook.Push) (string, *tool.Out) {
	waiter, ok := s.bus.(bus.Waiter)
	if !ok || !slices.Contains(held, hook.Bus) {
		return "0-0", nil
	}
	ctx, cancel := call()
	defer cancel()
	last, exists, err := waiter.Tail(ctx, bus.StreamOf(s.as))
	if err != nil {
		return "", tool.Refuse("the bus stream could not be read: " + err.Error())
	}
	if !exists || last == "" {
		return "0-0", nil
	}
	return last, nil
}

// renew renews the lease of every push held.
func (w world) renew(s *session, role hook.Role, held []hook.Push, epoch int64) error {
	for _, p := range held {
		ctx, cancel := call()
		err := s.hooks.Renew(ctx, s.as, role, p, epoch, w.holder)
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

// onceResult is --once's one result: the armed facts, the challenge and the
// state, OK when proven and FAILED with the remedy when not.
func (w world) onceResult(s *session, role hook.Role, held []hook.Push, armed, ch *tool.Out) *tool.Out {
	ctx, cancel := call()
	st, _, err := s.hooks.Read(ctx, s.as)
	cancel()
	if err != nil {
		return tool.Refuse("the hook record could not be read: " + err.Error())
	}
	o := tool.Done()
	if st[0].State != hook.StateProven {
		o = tool.Fail(fmt.Sprintf("%s is armed as %s and not proven: %s", s.as, role, st[0].Words()))
		o.Reasons = []string{hookgate.Reason}
		o.Remedy = "nova-hook status --as " + s.as // a role whose holders are daemons: they hook in at their start (the notes name them)
		if len(held) > 0 {
			o.Remedy = hook.HookCommand(s.as, role) + " under the session's Monitor" // this process held a push: hold it for good
		}
	}
	o.Facts = append(o.Facts, armed.Facts...)
	o.Fact("state", st[0].State)
	if ch != nil {
		o.Fact("nonce", ch.Facts[0].V)
		o.Note("prove: nova-hook prove --as " + s.as + " --nonce " + fmt.Sprint(ch.Facts[0].V) + " within " + hook.AnswerBound.String())
	}
	o.Notes = append(o.Notes, armed.Notes...)
	return o
}

// loop holds the bus push and streams it until a signal or an unhook: one
// blocking read of the stream a tick, the lease renewed every RenewEvery, a
// new challenge every ChallengeEvery, and a HOOK STATE line on each change.
// A stream that does not answer is said once while it stays the same fault,
// and the loop waits a tick before it reads again: a store that is down never
// floods the Monitor. A record lost under the loop (none: deleted, or the
// store flushed) is armed again, the session's hook being the identity's word;
// an unhook ends the loop.
func (w world) loop(c *tool.Call, s *session, role hook.Role, held []hook.Push, epoch int64, cursor string, challenge func(int64) *tool.Out, core *notice.Core, serverOwned bool, lanes *friend.Client) *tool.Out {
	ctx, stop := w.signals(context.Background())
	defer stop()
	stream := bus.StreamOf(s.as)
	waiter, _ := s.bus.(bus.Waiter) // ignored: a store with no wait reads holds no bus push here; its loop only renews
	nextRenew, nextChallenge := w.now().Add(hook.RenewEvery), w.now().Add(hook.ChallengeEvery)
	nextLanes := w.now() // the lane client's first round is at once: a restart resumes from the server
	lastState, unread := "", ""
	renewAttempts := 0
	var lk link
	pause := func() {
		w.sleep(ctx, w.tick)
	}
	for {
		if ctx.Err() != nil {
			line(c, tool.Done().As("STOP").Fact("as", s.as).Fact("lapse", hook.LeaseTTL.String()))
			return tool.Exit(0)
		}
		if waiter != nil && slices.Contains(held, hook.Bus) {
			got, err := waiter.BlockRead(ctx, stream, cursor, w.tick, bus.WaitRead)
			switch {
			case err != nil && ctx.Err() == nil:
				if why := err.Error(); why != unread {
					unread = why
					line(c, tool.Done().As("STATE").Fact("state", "unread").Note("the bus stream did not answer: "+why+"; the loop reads it again every "+w.tick.String()+", and says so again only when the fault changes"))
				}
				pause()
			case err == nil && unread != "":
				unread = ""
				line(c, tool.Done().As("STATE").Fact("state", "read").Note("the bus stream answers again"))
			}
			var batch []bus.Message
			for _, e := range got {
				cursor = e.Entry
				if m := e.Message(); m.From != s.as || strings.HasPrefix(m.Subject, "HOOK CHALLENGE ") || laneSelf(m, s.as) { // a server may challenge its own name; the lane client tells its own
					if core != nil {
						batch = append(batch, m)
						continue
					}
					line(c, tool.Done().As("PUSH").Fact("push", "bus").Fact("id", m.ID).Fact("from", m.From).Fact("subject", tool.Text(m.Subject)))
				}
			}
			if core != nil {
				if err := core.Offer(ctx, batch); err != nil {
					return core.Fail(ctx, err)
				}
			}
		} else {
			pause()
		}
		if ctx.Err() != nil {
			continue
		}
		if core != nil {
			if err := core.Step(ctx); err != nil {
				return core.Fail(ctx, err)
			}
		}
		if !w.now().Before(nextRenew) {
			nextRenew = w.now().Add(hook.RenewEvery)
			w.watchServer(c, s, role, &lk)
			err := w.renew(s, role, held, epoch)
			if errors.Is(err, hook.ErrNotHooked) {
				if e, ok := w.rearm(s, role, held); ok {
					// the record was lost (none), not unhooked: armed again, a new challenge
					epoch, err = e, nil
					line(c, tool.Done().As("ARMED").Fact("as", s.as).Fact("role", string(role)).Fact("epoch", epoch).Fact("joined", false).Fact("hold", pushList(held)).Note("the hook record was lost under this hook (no record: deleted, or the store flushed); armed again"))
					nextChallenge = w.now()
					if core != nil {
						core.Rearm(epoch)
					}
					if serverOwned {
						if ch := challenge(epoch); ch != nil {
							if ch.Status != tool.OK {
								return ch
							}
							line(c, ch)
						}
					}
				}
			}
			if errors.Is(err, hook.ErrNotHooked) {
				o := tool.Fail(s.as + " was unhooked under this hook (nova-hook unhook, or a hook as another role); nothing is held from now")
				o.Reasons = []string{hookgate.Reason}
				o.Remedy = hook.HookCommand(s.as, role)
				return o
			} else if err != nil {
				nextRenew = w.now().Add(w.retryBackoff(renewAttempts))
				renewAttempts++
				line(c, tool.Done().As("STATE").Fact("state", "unrenewed").Note("the lease could not be renewed: "+err.Error()+"; it is tried again at the next renewal, inside the lease"))
			} else {
				renewAttempts = 0
			}
		}
		if !serverOwned && !w.now().Before(nextChallenge) {
			nextChallenge = w.now().Add(hook.ChallengeEvery)
			if ch := challenge(epoch); ch != nil {
				line(c, ch)
			}
		}
		cctx, cancel := call()
		st, _, err := s.hooks.Read(cctx, s.as)
		cancel()
		if err == nil && st[0].Words() != lastState {
			lastState = st[0].Words()
			line(c, stateLine(st[0], role))
		}
		if lanes != nil && err == nil && !w.now().Before(nextLanes) {
			// the lane client's round: asks (and so renews) only while the model proves
			// (tla/FriendLaneClient.tla DeadModelReleases)
			nextLanes = w.now().Add(LaneEvery)
			lctx, lcancel := context.WithTimeout(ctx, laneRoundWait)
			for _, o := range lanePass(lctx, lanes, proven(st[0]), sendSelf(s, w)) {
				line(c, o)
			}
			lcancel()
		}
	}
}

// rearm arms the record again when it is gone (none) or unhooked by heartbeat
// misses (Reason != "") rather than explicit nova-hook unhook (Reason == ""),
// and renews what the loop holds.
func (w world) rearm(s *session, role hook.Role, held []hook.Push) (int64, bool) {
	ctx, cancel := call()
	st, _, err := s.hooks.Read(ctx, s.as)
	cancel()
	if err != nil {
		return 0, false
	}
	if st[0].State != hook.StateNone && !(st[0].State == hook.StateUnhooked && st[0].Reason != "") {
		return 0, false
	}
	ctx, cancel = call()
	epoch, _, err := s.hooks.HookAs(ctx, s.as, role, w.holder)
	if err == nil && w.isServiceRun() {
		err = w.recordService(ctx, s, defaultServiceName(s.as))
	}
	cancel()
	if err != nil || w.renew(s, role, held, epoch) != nil {
		return 0, false
	}
	return epoch, true
}

// prove is the verb: the answer to the hook's challenge (tla/Hook.tla, Answer).
func (w world) prove(c *tool.Call) *tool.Out {
	s, refused := w.openSession(c, true)
	if refused != nil {
		return refused
	}
	defer s.close()
	ctx, cancel := call()
	defer cancel()
	if c.DryRun() {
		return w.provePlan(ctx, s)
	}
	pr, err := s.hooks.AnswerProof(ctx, s.as, c.Str("nonce"), "session")
	switch {
	case errors.Is(err, hook.ErrNoChallenge), errors.Is(err, hook.ErrWrongNonce), errors.Is(err, hook.ErrExpired):
		o := tool.Fail(err.Error())
		o.Remedy = "nova-hook status --as " + s.as
		return o
	case errors.Is(err, hook.ErrNotHooked):
		o := tool.Fail(s.as + " was unhooked or hooked again since the challenge; its hook prints a new one")
		o.Remedy = "nova-hook status --as " + s.as
		return o
	case err != nil:
		return tool.Refuse("the proof could not be written: " + err.Error())
	}
	st, _, err := s.hooks.Read(ctx, s.as)
	if err != nil {
		return tool.Refuse("the hook record could not be read: " + err.Error())
	}
	o := tool.Done().Fact("proven", pr.Proven).Fact("state", st[0].State).Fact("role", string(st[0].Role)).Fact("matched", matched(pr.Back))
	if !pr.Issued.IsZero() {
		o.Fact("issued", pr.Issued.Format(time.RFC3339))
	}
	return o
}

// matched is which challenge a proof answered, as prove's reply says it:
// "newest", or "<k>-back" for one k challenges before the newest (the proof
// window, tla/HookBeat.tla AnswerIssued).
func matched(back int) string {
	if back == 0 {
		return "newest"
	}
	return fmt.Sprintf("%d-back", back)
}

// provePlan is prove --dry-run's result: it reads the record and prints what
// the answer would prove -- each push of the record's role whose lease is live
// (the pushes a challenge can name) -- and writes nothing.
func (w world) provePlan(ctx context.Context, s *session) *tool.Out {
	st, _, err := s.hooks.Read(ctx, s.as)
	if err != nil {
		return tool.Refuse("the hook record could not be read: " + err.Error())
	}
	live := 0
	for _, p := range st[0].Pushes {
		if p.State != hook.PushDown {
			live++
		}
	}
	return tool.Done().As("DRY-RUN").Fact("as", s.as).Fact("state", st[0].State).Fact("role", string(st[0].Role)).Fact("would-prove", live)
}

// hookPlan is hook --dry-run's result: it reads the record and prints the plan
// the real run would take -- the role, the epoch (joined at the record's, else
// the next), the pushes this process would hold, and each other push's holder
// -- and writes nothing.
func (w world) hookPlan(ctx context.Context, s *session, role hook.Role) *tool.Out {
	st, _, err := s.hooks.Read(ctx, s.as)
	if err != nil {
		return tool.Refuse("the hook record could not be read: " + err.Error())
	}
	cur := st[0]
	joined := cur.Role == role && cur.State != hook.StateNone && cur.State != hook.StateUnhooked
	epoch := cur.Epoch + 1
	if joined {
		epoch = cur.Epoch
	}
	if epoch < 1 {
		epoch = 1
	}
	held := holds(role)
	o := tool.Done().As("DRY-RUN").Fact("as", s.as).Fact("role", string(role)).Fact("epoch", epoch).Fact("joined", joined).Fact("hold", pushList(held))
	for _, p := range hook.PushesOf(role) {
		if !slices.Contains(held, p) {
			o.Note(fmt.Sprintf("the %s push is held by: %s", p, hook.HeldBy(s.as, role, p)))
		}
	}
	return o
}

// unhook is the verb (tla/Hook.tla, Unhook).
func (w world) unhook(c *tool.Call) *tool.Out {
	s, refused := w.openSession(c, true)
	if refused != nil {
		return refused
	}
	defer s.close()
	ctx, cancel := call()
	defer cancel()
	if c.DryRun() {
		st, _, err := s.hooks.Read(ctx, s.as)
		if err != nil {
			return tool.Refuse("the hook record could not be read: " + err.Error())
		}
		return tool.Done().As("DRY-RUN").Fact("as", s.as).Fact("state", st[0].State).Fact("epoch", st[0].Epoch)
	}
	if w.service != nil {
		if err := w.service.Remove(s.as); err != nil {
			return tool.Refuse("the supervised service could not be removed: " + err.Error())
		}
	}
	if err := w.clearService(ctx, s); err != nil {
		return tool.Refuse("the hook record could not be written: " + err.Error())
	}
	was, err := s.hooks.Unhook(ctx, s.as)
	if err != nil {
		return tool.Refuse("the hook record could not be written: " + err.Error())
	}
	st, _, err := s.hooks.Read(ctx, s.as)
	if err != nil {
		return tool.Refuse("the hook record could not be read: " + err.Error())
	}
	o := tool.Done().Fact("as", s.as).Fact("was", was).Fact("epoch", st[0].Epoch)
	if was != hook.StateNone {
		o.Note("hook in again: nova-hook hook --as " + s.as + " --role " + string(st[0].Role))
	}
	return o
}

// status is the verb: one name's record as a check, or every name's.
func (w world) status(c *tool.Call) *tool.Out {
	s, refused := w.openSession(c, false)
	if refused != nil {
		return refused
	}
	defer s.close()
	ctx, cancel := call()
	defer cancel()
	if s.as != "" {
		st, _, err := s.hooks.Read(ctx, s.as)
		if err != nil {
			return tool.Refuse("the hook record could not be read: " + err.Error())
		}
		if r := st[0].Gate(hook.Want{Name: s.as, Role: st[0].Role}); r != nil {
			o := hookgate.Out(r)
			o.Facts = statusFacts(st[0])
			return o
		}
		o := tool.Done()
		o.Facts = statusFacts(st[0])
		sv, err := s.hooks.Serve(ctx)
		if err != nil {
			return tool.Refuse("the heartbeat server's stamp could not be read: " + err.Error())
		}
		if age, lost := noChallenge(st[0], sv); lost { // the gate refuses a record with no evidence first (Status.Down)
			o = tool.Fail(fmt.Sprintf("NO CHALLENGE FOR %s: the heartbeat server last passed %s ago (every %s), so nothing checks this link", age, sv.Now.Sub(sv.At).Round(time.Second), sv.Every))
			o.Remedy = "nova-hook serve --as <a coordinator or comms name>"
			o.Facts = statusFacts(st[0])
		}
		return o
	}
	names, err := s.hooks.Names(ctx)
	if err != nil {
		return tool.Refuse("the hook names could not be read: " + err.Error())
	}
	sts, _, err := s.hooks.Read(ctx, names...)
	if err != nil {
		return tool.Refuse("the hook record could not be read: " + err.Error())
	}
	sv, err := s.hooks.Serve(ctx)
	if err != nil {
		return tool.Refuse("the heartbeat server's stamp could not be read: " + err.Error())
	}
	proven := 0
	o := tool.Done()
	for _, st := range sts {
		if st.Down != "" && st.State == hook.StateProven {
			st.State = "down" // no evidence (Served.Down): the last written state is not trusted
		}
		if st.State == hook.StateProven {
			proven++
		}
		o.Item("name", fieldsOf(statusFacts(st))...)
	}
	o.Fact("count", len(sts)).Fact("proven", proven)
	if sv.OK {
		o.Fact("serve_age", sv.Now.Sub(sv.At).Round(time.Second).String()).Fact("serve_stale", sv.Stale())
	}
	return o
}

// noChallenge detects a stale server row (tla/HookBeat.tla, StaleServerMeansDown).
// Intentional silence between challenges never implies a lost server.
// No server ever passed: no timeout (nothing was lost).
func noChallenge(st hook.Status, sv hook.Served) (time.Duration, bool) {
	if !sv.OK || st.State == hook.StateNone || st.State == hook.StateUnhooked {
		return 0, false
	}
	if _, ok := hook.Challenged[st.Role]; !ok {
		return 0, false
	}
	age := sv.Now.Sub(sv.At).Round(time.Second)
	return age, sv.Stale()
}

// statusFacts is a record's facts: name, state, role, epoch and each push.
func statusFacts(st hook.Status) tool.Fields {
	o := tool.Done().Fact("name", st.Name).Fact("state", st.State).Fact("role", string(st.Role)).Fact("epoch", st.Epoch)
	for _, p := range st.Pushes {
		o.Fact(string(p.Push), p.State)
	}
	return o.Facts
}

func fieldsOf(fs tool.Fields) []any {
	var kv []any
	for _, f := range fs {
		kv = append(kv, f.K, f.V)
	}
	return kv
}
