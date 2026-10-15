package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/mas-bandwidth/nova-tools/pkg/sprintwire"
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
	// its card returned ready (nova-sprint internal/sprint/lane_hold.go LaneGoneAfter). The
	// client's renewal is paced against it, never against the hook's challenge cadence.
	ServerLaneLease = 10 * time.Minute
	// LaneEvery is how often the lane client asks, and so renews: a third of the lease, so a
	// round late or failed twice still renews inside it (tla/FriendLaneClient.tla RenewTicks <
	// Bound, LiveHoldNeverExpires; nova-tools#5798 at 23aec479 paced it at the 30m challenge
	// interval against the 10m lease, and live cards would have been returned mid-work).
	LaneEvery = ServerLaneLease / 3
	// laneAskWait bounds one ask of the sprint server, and laneRoundWait one whole round, so
	// a hung sprint server never holds the hook loop past its own lease renewal (hook.RenewEvery).
	laneAskWait   = 5 * time.Second
	laneRoundWait = hook.RenewEvery / 2
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

// laneClient is the friend lane client a hook run carries: a friend's supervised service
// with lanes, else nil (a session's own hook, --once, a dry run, another role, --lanes 0).
func (w world) laneClient(c *tool.Call, as string, role hook.Role) *friend.Client {
	if role != hook.Friend || !w.isServiceRun() || c.Bool("once") || c.DryRun() || c.Int("lanes") <= 0 || w.sprint == nil {
		return nil
	}
	addr := c.Str("sprint-server")
	dir := ""
	if w.home != nil {
		if h, err := w.home(); err == nil && h != "" {
			dir = filepath.Join(h, ".local", "state", "nova-hook", as, "briefs")
		}
	}
	return &friend.Client{Name: as, Width: c.Int("lanes"), Dir: dir,
		Ask: func(ctx context.Context, argv []string) (string, error) { return w.sprint(ctx, addr, argv) }}
}

// proven says the record lets the lanes renew: proven, with its evidence standing.
func proven(st hook.Status) bool { return st.State == hook.StateProven && st.Down == "" }

// lanePass is one round of the lane client: every lane asked when the model proves, and one
// message to her own name when the round found holds she has not been told of. It answers
// the lines the loop prints: one STATE line a round that took or failed, nothing otherwise.
func lanePass(ctx context.Context, lc *friend.Client, ok bool, send func(context.Context, bus.Message) error) []*tool.Out {
	holds, fresh, errs := lc.Round(ctx, ok)
	var out []*tool.Out
	for _, err := range errs {
		out = append(out, tool.Done().As("STATE").Fact("state", "lane-ask-failed").Note(err.Error()+"; the server's record stands, and the lane is asked again next round"))
	}
	if len(fresh) == 0 {
		return out
	}
	subject, body := lc.Notice(fresh, holds)
	m := bus.Message{From: lc.Name, To: []string{lc.Name}, Subject: subject, Body: body}
	if err := send(ctx, m); err != nil {
		return append(out, tool.Done().As("STATE").Fact("state", "lane-notice-unsent").Note("the notice of "+fmt.Sprint(len(fresh))+" held cards was not sent: "+err.Error()+"; it is sent again next round"))
	}
	lc.Told(fresh)
	return append(out, tool.Done().As("STATE").Fact("state", "lanes-taken").Fact("taken", len(fresh)).Fact("held", len(holds)).Note("one notice sent: "+subject))
}

// laneSelf says a message on her own stream is the lane client's notice, which the loop
// hands to the notice core as any message (the loop passes over her own messages else).
func laneSelf(m bus.Message, as string) bool {
	return m.From == as && strings.HasPrefix(m.Subject, friend.Subject)
}

// laneFlags are the hook verb's lane client flags.
func laneFlags(w world, f *tool.Flags) {
	f.Int("lanes", config.DefaultFriendWidth, "a friend's supervised service: the lanes it asks the sprint server for, 1..n (the server's width for her row bounds what is taken); 0 takes nothing")
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
