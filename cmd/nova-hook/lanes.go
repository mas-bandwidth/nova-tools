package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/sprintwire"
	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// The friend lane client in the supervised hook service (internal/friend; the card
// the-friend-lane-is-a-client-with-no-local-state). A friend's service (nova-hook hook
// --role friend under nova-hook.client.<name>) asks the sprint server every LaneEvery
// (a third of the server's lane lease) what each of the friend's lanes holds, taking a
// ready card into each free lane, the fleet's own lane protocol. While the model proves,
// the asks renew the leases; when it stops, the asks stop, and the server's lease returns her cards
// (tla/FriendLaneClient.tla). New holds make ONE bus message to her own name, which the
// hook loop folds into the one pending notice like any other (pkg/harness/notice): "N
// cards taken; briefs at <paths>; work each in a child agent to width". The model works
// them and finishes through the server's verbs. Nothing here is a daemon of its own, and
// no local file decides: the brief cache is rebuilt from the server's answers.

const (
	// sprintServerEnv names the sprint server, host:port; DefaultSprintServer when unset.
	sprintServerEnv     = "NOVA_SPRINT_SERVER"
	DefaultSprintServer = "127.0.0.1:6390"
	// ServerLaneLease is the sprint server's lane lease: a lane unheard this long is gone and
	// its card returned ready (nova-sprint's LaneGoneAfter, in its lane_hold.go). The
	// client's renewal is paced against it, never against the hook's challenge cadence.
	ServerLaneLease = 10 * time.Minute
	// LaneEvery is how often the lane client asks, and so renews: a third of the lease, so a
	// round late or failed twice still renews inside it (tla/FriendLaneClient.tla RenewTicks <
	// Bound, LiveHoldNeverExpires; nova-tools#5798 at 23aec479 paced it at the 30m challenge
	// interval against the 10m lease, and live cards would have been returned mid-work).
	LaneEvery = ServerLaneLease / 3
	// laneRoundWait bounds one whole round's one request, so a sprint server answering in
	// 30 s is waited out; it stays well inside the renewal margin (LaneEvery plus this bound
	// against the server's 10 m lease, tla/FriendLaneClient.tla RenewTicks < Bound).
	laneRoundWait = 35 * time.Second
	// laneAskWait bounds one single-verb ask where a caller has no batch (a fallback, never the
	// production round).
	laneAskWait = 5 * time.Second
	// configWait bounds one read of her canonical nova-config row (friend.ConfigEvery apart),
	// so a slow store never holds the loop's tick much past a second.
	configWait = 2 * time.Second
)

// sprintAsk is one verb sent to the sprint server: what it printed, or its refusal.
// sprintBatchAsk sends a round's verbs in one request: one reply a verb, its stdout or its
// refusal. An error is a request the server did not answer at all. batch is the world's
// sprintBatch seam: the one batch of verbs to the sprint server, real or a test's.
func sprintBatchAsk(batch func(context.Context, string, ...[]string) ([]sprintwire.Result, error), ctx context.Context, addr string, argvs [][]string) ([]friend.Reply, error) {
	res, err := batch(ctx, addr, argvs...)
	if err != nil {
		return nil, err
	}
	replies := make([]friend.Reply, len(res))
	for i, r := range res {
		if r.Code != 0 {
			replies[i].Err = fmt.Errorf("%s refused: %s", argvVerb(argvs[i]), strings.TrimSpace(r.Stderr+" "+r.Stdout))
			continue
		}
		replies[i].Out = r.Stdout
	}
	return replies, nil
}

// argvVerb is a verb's name for an error.
func argvVerb(argv []string) string {
	if len(argv) == 0 {
		return "the verb"
	}
	return argv[0]
}

func sprintAsk(ctx context.Context, addr string, argv []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, laneAskWait)
	defer cancel()
	res, err := sprintwire.Client{Addr: addr}.Do(ctx, argv)
	if err != nil {
		return "", err
	}
	if len(res) != 1 {
		return "", fmt.Errorf("%s: the server answered %d results, want 1", argv[0], len(res))
	}
	if res[0].Code != 0 {
		return "", fmt.Errorf("%s refused: %s", argv[0], strings.TrimSpace(res[0].Stderr+" "+res[0].Stdout))
	}
	return res[0].Stdout, nil
}

// laneClient is the friend lane client a hook run carries: a friend's supervised service
// with lanes, else nil (a session's own hook, --once, a dry run, another role). A
// coordinator works her own friend row too, as nova-friend's daemon joins her hook. Its width is
// her canonical nova-config row's, fetched by the loop every friend.ConfigEvery (Apply).
// A service installed with an explicit --lanes 0 takes nothing: that install said no lanes,
// and only that value of the retired flag is still read.
func (w world) laneClient(c *tool.Call, as string, role hook.Role) *friend.Client {
	if !laneRole(role) || !w.isServiceRun() || c.Bool("once") || c.DryRun() || w.sprint == nil {
		return nil
	}
	if c.Given("lanes") && c.Int("lanes") == 0 {
		return nil
	}
	addr := c.Str("sprint-server")
	dir := ""
	if w.home != nil {
		if h, err := w.home(); err == nil && h != "" {
			dir = filepath.Join(h, ".local", "state", "nova-hook", as, "briefs")
		}
	}
	var fetch friend.Fetch
	if w.novaConfig != nil {
		fetch = func(ctx context.Context, name string) (string, error) {
			ctx, cancel := context.WithTimeout(ctx, configWait)
			defer cancel()
			return w.novaConfig(ctx, friend.ConfigArgv(name))
		}
	}
	return &friend.Client{Name: as, Dir: dir, Fetch: fetch,
		Ask: func(ctx context.Context, argvs [][]string) ([]friend.Reply, error) {
			if w.sprintBatch != nil {
				return sprintBatchAsk(w.sprintBatch, ctx, addr, argvs)
			}
			// a world without a batch (a test seam): ask each verb on its own
			replies := make([]friend.Reply, len(argvs))
			for i, argv := range argvs {
				replies[i].Out, replies[i].Err = w.sprint(ctx, addr, argv)
			}
			return replies, nil
		}}
}

// laneRole is whether a role's service carries the friend lanes: a friend's, and the
// coordinator's, who works her own friend row too (as nova-friend's daemon joins her hook).
func laneRole(role hook.Role) bool { return role == hook.Friend || role == hook.Coordinator }

// configPass is the loop's tick of the friend's configuration: the canonical row fetched and
// applied when due (friend.Client.Apply), and one STATE line when what the lanes run by
// changed; changed says the lanes should be asked on this pass, so a new width takes effect
// at once.
func configPass(ctx context.Context, lc *friend.Client, now time.Time) (*tool.Out, bool) {
	ch, changed := lc.Apply(ctx, now)
	if !changed {
		return nil, false
	}
	if !ch.OK {
		return tool.Done().As("STATE").Fact("state", "config-unread").Fact("intake", 0).Note("the friend's nova-config row did not read: " + ch.Why + "; no lane takes a card until it reads again, and every held card's lane is still renewed while friend cards answers"), true
	}
	return tool.Done().As("STATE").Fact("state", "config-applied").Fact("width", ch.Config.Width).Note("the friend's nova-config row applied: the lanes take to width " + fmt.Sprint(ch.Config.Width) + "; a lowered width drains, never returns a held card"), true
}

// novaConfigRun runs the canonical nova-config read, argv after the binary: the one beside
// this executable, else ~/.local/bin, else on PATH, with its own directory first on the
// child's PATH (it finds its seat login's helpers there; a launchd service's PATH is bare).
func novaConfigRun(ctx context.Context, argv []string) (string, error) {
	bin := ""
	var cands []string
	if exe, err := os.Executable(); err == nil {
		cands = append(cands, filepath.Join(filepath.Dir(exe), "nova-config"))
	}
	if h, err := userHome(); err == nil {
		cands = append(cands, filepath.Join(h, ".local", "bin", "nova-config"))
	}
	for _, p := range cands {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			bin = p
			break
		}
	}
	if bin == "" {
		p, err := exec.LookPath("nova-config")
		if err != nil {
			return "", errors.New("no nova-config binary beside nova-hook, in ~/.local/bin or on PATH")
		}
		bin = p
	}
	cmd := subproc.Context(ctx, bin, argv...)
	env := os.Environ()
	path := filepath.Dir(bin)
	if p := os.Getenv("PATH"); p != "" {
		path += string(os.PathListSeparator) + p
	}
	cmd.Env = append(env, "PATH="+path)
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return "", fmt.Errorf("%s %s: %w", bin, strings.Join(argv, " "), err)
	}
	// a refusal prints its JSON with the reason (exit 1 or 2): ParseConfig reads it
	return string(out), nil
}

// proven says the record lets the lanes renew: proven, with its evidence standing.
func proven(st hook.Status) bool { return st.State == hook.StateProven && st.Down == "" }

// lanePass is one round of the lane client: every lane asked when the model proves, and one
// message to her own name when the round found holds she has not been told of. It answers
// the lines the loop prints: one STATE line a round that took or failed, nothing otherwise.
// lanePass answers the lines the loop prints and whether the server answered the round's one
// request. It is false only when the request did not answer at all (a transport error or the
// round bound): then every lane is unknown and the next round is due one RenewEvery away.
func lanePass(ctx context.Context, lc *friend.Client, ok bool, send func(context.Context, bus.Message) error) ([]*tool.Out, bool) {
	holds, fresh, errs := lc.Round(ctx, ok)
	var out []*tool.Out
	answered := true
	for _, err := range errs {
		var u *friend.Unanswered
		if errors.As(err, &u) {
			answered = false
			out = append(out, tool.Done().As("STATE").Fact("state", "lane-round-unanswered").Note(err.Error()+"; the server's record stands, and the round is asked again in "+hook.RenewEvery.String()))
			continue
		}
		out = append(out, tool.Done().As("STATE").Fact("state", "lane-ask-failed").Note(err.Error()+"; the server's record stands, and the lane is asked again next round"))
	}
	if len(fresh) == 0 {
		return out, answered
	}
	subject, body := lc.Notice(fresh, holds)
	m := bus.Message{From: lc.Name, To: []string{lc.Name}, Subject: subject, Body: body}
	if err := send(ctx, m); err != nil {
		return append(out, tool.Done().As("STATE").Fact("state", "lane-notice-unsent").Note("the notice of "+fmt.Sprint(len(fresh))+" held cards was not sent: "+err.Error()+"; it is sent again next round")), answered
	}
	lc.Told(fresh)
	return append(out, tool.Done().As("STATE").Fact("state", "lanes-taken").Fact("taken", len(fresh)).Fact("held", len(holds)).Note("one notice sent: "+subject)), answered
}

// laneNext is when the next lane round is due: LaneEvery after a round the server answered,
// RenewEvery after one that did not, so a slow server is asked again soon.
func laneNext(now time.Time, answered bool) time.Time {
	if !answered {
		return now.Add(hook.RenewEvery)
	}
	return now.Add(LaneEvery)
}

// laneSelf says a message on her own stream is the lane client's notice, which the loop
// hands to the notice core as any message (the loop passes over her own messages else).
func laneSelf(m bus.Message, as string) bool {
	return m.From == as && strings.HasPrefix(m.Subject, friend.Subject)
}

// laneFlags are the hook verb's lane client flags.
func laneFlags(w world, f *tool.Flags) {
	f.Int("lanes", 0, "retired: a service installed with an explicit --lanes 0 carries no lanes, any other value is accepted and ignored so a service installed with it still starts: a friend's lanes take to the friend's nova-config row's width, read every second (nova-config friend set <name> --width <n>); reinstall the service to drop it")
	f.String("sprint-server", orDefault(w.getenv(sprintServerEnv), DefaultSprintServer), "the sprint server the lanes ask, host:port (default: "+sprintServerEnv+", else "+DefaultSprintServer+")")
}

func orDefault(v, d string) string {
	if strings.TrimSpace(v) == "" {
		return d
	}
	return v
}

// sendSelf is the lane notice's send: one bus message under one call's deadline.
func sendSelf(s *session, w world) func(context.Context, bus.Message) error {
	return func(_ context.Context, m bus.Message) error {
		send := w.deliver
		if send == nil {
			send = deliver
		}
		_, err := send(&bus.Bus{Store: s.bus}, m)
		return err
	}
}

// errNoHome is a host with no home directory: the briefs are not cached.
var errNoHome = errors.New("no home directory")

func userHome() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil || h == "" {
		return "", errNoHome
	}
	return h, nil
}
