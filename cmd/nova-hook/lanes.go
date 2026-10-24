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

// The friend lane client in a friend's hook (internal/friend; the card
// the-friend-lane-is-a-client-with-no-local-state). A friend's hook (nova-hook hook
// --role friend, her service under nova-hook.client.<name> or her session's own) asks the
// sprint server every LaneEvery (45 s, tla/FriendAutoTake.tla) what each of her lanes holds, taking a
// ready card into each free lane, the fleet's own lane protocol. While the model proves,
// the asks renew the leases; when it stops, the asks stop, and the server's lease returns her cards
// (tla/FriendLaneClient.tla). New holds make ONE bus message to her own name, which the
// hook loop folds into the one pending notice like any other (pkg/harness/notice): "N
// cards taken; briefs at <paths>; work each in a child agent to width". The model works
// them and finishes through the server's verbs. A hook with no harness route sends no bus
// notice: its stream is the route, one HOOK TAKEN line per card (takenLine). Nothing here is a daemon of its own, and
// no local file decides: the brief cache is rebuilt from the server's answers.

const (
	// sprintServerEnv names the sprint server, host:port; DefaultSprintServer when unset.
	sprintServerEnv     = "NOVA_SPRINT_SERVER"
	DefaultSprintServer = "127.0.0.1:6390"
	// ServerLaneLease is the sprint server's lane lease: a lane unheard this long is gone and
	// its card returned ready (nova-sprint's LaneGoneAfter, in its lane_hold.go). The
	// client's renewal is paced against it, never against the hook's challenge cadence.
	ServerLaneLease = 10 * time.Minute
	// LaneEvery is how often the lane client asks, and so takes and renews: 45 s, so a free
	// lane with a ready card on her row is taken within one interval (tla/FriendAutoTake.tla
	// TakenWithinInterval; 2026-10-24, a friend sat on 9 ready cards with 3 working when the
	// rounds ran every third of the lease, and not at all in a session's own hook), and well
	// inside the lease, so a round late or failed many times still renews inside it
	// (tla/FriendLaneClient.tla RenewTicks < Bound, LiveHoldNeverExpires; nova-tools#5798 at
	// 23aec479 paced it at the 30m challenge interval against the 10m lease).
	LaneEvery = 45 * time.Second
	// laneAskWait bounds one ask of the sprint server, and laneRoundWait one whole round, so
	// a hung sprint server never holds the hook loop past its own lease renewal (hook.RenewEvery).
	laneAskWait   = 5 * time.Second
	laneRoundWait = hook.RenewEvery / 2
	// configWait bounds one read of her canonical nova-config row (friend.ConfigEvery apart),
	// so a slow store never holds the loop's tick much past a second.
	configWait = 2 * time.Second
)

// sprintAsk is one verb sent to the sprint server: what it printed, or its refusal.
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

// laneClient is the friend lane client a hook run carries: a friend's hook, her supervised
// service or her session's own foreground hook (nova-hook hook --as <name> --role friend
// under the session's Monitor), else nil (--once, a dry run, another role). Before
// 2026-10-24 only the service carried lanes, so a friend hooked in the foreground (two of
// them that day) took nothing: the friend daemons that took cards were retired on 10-17 and nothing
// took their place. A coordinator's service works her own friend row too, as nova-friend's
// daemon joins her hook; her session's own hook does not (the coordinator deals, she does
// not take from the foreground). Its width is
// her canonical nova-config row's, fetched by the loop every friend.ConfigEvery (Apply).
// A service installed with an explicit --lanes 0 takes nothing: that install said no lanes,
// and only that value of the retired flag is still read.
func (w world) laneClient(c *tool.Call, as string, role hook.Role) *friend.Client {
	if !laneRole(role) || c.Bool("once") || c.DryRun() || w.sprint == nil {
		return nil
	}
	if !w.isServiceRun() && role != hook.Friend {
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
		Ask: func(ctx context.Context, argv []string) (string, error) { return w.sprint(ctx, addr, argv) }}
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

// lanePass is one round of the lane client: every lane asked when the model proves, and the
// round's new holds told once. It answers the lines the loop prints: one STATE line a round
// that took or failed, and one HOOK TAKEN line per card newly held (takenLine), nothing
// otherwise. send is the bus notice to her own name, which a harness route (the notice core)
// delivers; nil in a hook with no harness route, whose stream is the route: the TAKEN lines wake the
// session, and nothing else is sent. The hook only takes and tells: the card's work is the
// model's, in a child agent, and its finish the server's verbs.
func lanePass(ctx context.Context, lc *friend.Client, ok bool, send func(context.Context, bus.Message) error) []*tool.Out {
	holds, fresh, errs := lc.Round(ctx, ok)
	var out []*tool.Out
	for _, err := range errs {
		out = append(out, tool.Done().As("STATE").Fact("state", "lane-ask-failed").Note(err.Error()+"; the server's record stands, and the lane is asked again next round"))
	}
	if len(fresh) == 0 {
		return out
	}
	if send == nil {
		lc.Told(fresh)
		for _, h := range fresh {
			out = append(out, takenLine(lc, h))
		}
		return out
	}
	subject, body := lc.Notice(fresh, holds)
	m := bus.Message{From: lc.Name, To: []string{lc.Name}, Subject: subject, Body: body}
	if err := send(ctx, m); err != nil {
		return append(out, tool.Done().As("STATE").Fact("state", "lane-notice-unsent").Note("the notice of "+fmt.Sprint(len(fresh))+" held cards was not sent: "+err.Error()+"; it is sent again next round"))
	}
	lc.Told(fresh)
	for _, h := range fresh {
		out = append(out, takenLine(lc, h))
	}
	return append(out, tool.Done().As("STATE").Fact("state", "lanes-taken").Fact("taken", len(fresh)).Fact("held", len(holds)).Note("one notice sent: "+subject))
}

// takenLine is one card a lane newly holds, as the hook's stream says it: HOOK TAKEN
// card=<card>@<gen> brief=<path> lane=<n> epoch=<e> as=friend.<name>, with the verb that
// finishes it. A brief the server sent none of is named by the verb that prints it.
func takenLine(lc *friend.Client, h friend.Hold) *tool.Out {
	brief := h.Brief
	if brief == "" {
		brief = "nova-sprint card " + h.Card
	}
	at := fmt.Sprintf("%s@%d --epoch %d --lane %d", h.Card, h.Gen, h.Epoch, h.Lane)
	finish := "nova-sprint finish --as " + lc.Row() + " " + at + " (--head <commit> | --failed) --report <text>"
	if h.Kind == "read" {
		finish = fmt.Sprintf("nova-sprint read --as %s (--ok | --broken) %s --epoch %d --lane %d", lc.Row(), h.Card, h.Epoch, h.Lane)
	}
	return tool.Done().As("TAKEN").Fact("card", fmt.Sprintf("%s@%d", h.Card, h.Gen)).Fact("brief", brief).Fact("lane", h.Lane).Fact("epoch", h.Epoch).Fact("as", lc.Row()).
		Note("work it in a child agent (the hook does not); finish: " + finish)
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
