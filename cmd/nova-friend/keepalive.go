package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/friend/keepalive"
	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

const keepaliveCallTimeout = 900 * time.Millisecond

func (w world) sprintCall(ctx context.Context, server string, verbs ...[]string) ([]sprintwire.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, keepaliveCallTimeout)
	defer cancel()
	if w.sprint == nil {
		return nil, fmt.Errorf("keepalive needs the coordination client")
	}
	return w.sprint(ctx, server, verbs...)
}

func (w world) authority(ctx context.Context, server string) (keepalive.Seat, error) {
	res, err := w.sprintCall(ctx, server, []string{"seat", "--json"})
	if err != nil {
		return keepalive.Seat{}, err
	}
	if err := oneSprintResult("seat", res); err != nil {
		return keepalive.Seat{}, err
	}
	var seat keepalive.Seat
	if err := json.Unmarshal([]byte(res[0].Stdout), &seat); err != nil {
		return keepalive.Seat{}, fmt.Errorf("seat JSON: %w", err)
	}
	return seat, nil
}

func (w world) peers(ctx context.Context, server string) ([]string, error) {
	res, err := w.sprintCall(ctx, server, []string{"where", "--json"})
	if err != nil {
		return nil, err
	}
	if err := oneSprintResult("where", res); err != nil {
		return nil, err
	}
	var view struct {
		Friends []struct {
			Name string `json:"name"`
		} `json:"friends"`
	}
	if err := json.Unmarshal([]byte(res[0].Stdout), &view); err != nil {
		return nil, fmt.Errorf("where JSON: %w", err)
	}
	peers := make([]string, 0, len(view.Friends))
	for _, row := range view.Friends {
		if row.Name != "" {
			peers = append(peers, row.Name)
		}
	}
	sort.Strings(peers)
	return peers, nil
}

func (w world) healthBatch(ctx context.Context, server, actor string, observations []keepalive.Observation) error {
	verbs := make([][]string, 0, len(observations))
	for _, o := range observations {
		if o.Seat.Holder != actor || o.Seat.Generation == 0 || o.Seen.IsZero() || o.Evidence == 0 {
			return fmt.Errorf("health observation lacks its authority generation, ACK evidence, or proof time")
		}
		state := "down" // the health words of the coordination server: up, asleep, down
		if o.Asleep {
			state = "asleep"
		} else if o.Up {
			state = "up"
		}
		verbs = append(verbs, []string{"friend", "health", "--actor", actor, o.Friend,
			"--state", state, "--seen", o.Seen.UTC().Format(time.RFC3339Nano), "--generation", fmt.Sprint(o.Seat.Generation)})
	}
	if len(verbs) == 0 {
		return nil
	}
	res, err := w.sprintCall(ctx, server, verbs...)
	if err != nil {
		return err
	}
	if len(res) != len(verbs) {
		return fmt.Errorf("friend health answered %d results for %d observations", len(res), len(verbs))
	}
	for i := range res {
		if res[i].Code != 0 {
			return fmt.Errorf("friend health %s refused: %s", observations[i].Friend, strings.TrimSpace(res[i].Stderr+res[i].Stdout))
		}
	}
	return nil
}

func oneSprintResult(verb string, res []sprintwire.Result) error {
	if len(res) != 1 {
		return fmt.Errorf("%s answered %d results, wanted 1", verb, len(res))
	}
	if res[0].Code != 0 {
		return fmt.Errorf("%s refused: %s", verb, strings.TrimSpace(res[0].Stderr+res[0].Stdout))
	}
	return nil
}

func (w world) keepaliveLoop(ctx context.Context, name, role, server, redis, stateDir string, record func(string)) error {
	if w.openKeepalive == nil {
		return fmt.Errorf("keepalive needs its Redis opener")
	}
	store, closeStore, err := w.openKeepalive(ctx, redis)
	if err != nil {
		return fmt.Errorf("keepalive store: %w", err)
	}
	defer closeStore()
	if w.instance == nil {
		return fmt.Errorf("keepalive needs its instance factory")
	}
	l := &keepalive.Loop{
		Name: name, Role: role, Instance: w.instance(), Store: store, Now: w.now, Pause: w.sleep, Record: record,
		Authority: func(ctx context.Context) (keepalive.Seat, error) { return w.authority(ctx, server) },
	}
	if role == "coordinator" {
		l.Peers = func(ctx context.Context) ([]string, error) { return w.peers(ctx, server) }
		l.HealthBatch = func(ctx context.Context, observations []keepalive.Observation) error {
			return w.healthBatch(ctx, server, name, observations)
		}
	} else {
		l.Asleep = func(context.Context) (bool, error) {
			s, err := friend.ReadSessionState(stateDir)
			return s.Asleep, err
		}
	}
	return l.Run(ctx)
}

func (w world) coordinate(c *tool.Call) *tool.Out {
	if o := c.Refused(); o != nil {
		return o
	}
	addr := c.Want("redis", "the keepalive store's Redis address, host:port (or "+RedisEnv+")")
	if o := c.Refused(); o != nil {
		return o
	}
	name, server := c.Str("as"), c.Str("server")
	stateDir := c.Str("state-dir")
	if stateDir == "" {
		stateDir = friend.DefaultStateDir(w.home, "coordinator-"+name)
	}
	record := func(line string) {
		fmt.Fprintln(c.Stdout, "COORDINATE "+line)
		_ = friend.Record(stateDir, line) // ignored: the line remains on launchd's log
	}
	lock, err := friend.TakeDaemonLock(stateDir, "coordinator-"+name)
	if err != nil {
		fmt.Fprintln(c.Stderr, "COORDINATE FAIL: singleton: "+err.Error())
		return tool.Exit(1)
	}
	ctx, stop := w.signals(context.Background())
	defer stop()
	err = w.keepaliveLoop(ctx, name, "coordinator", server, addr, stateDir, record)
	err = errors.Join(err, lock.Unlock())
	if err != nil {
		fmt.Fprintln(c.Stderr, "COORDINATE FAIL: "+err.Error())
		return tool.Exit(1)
	}
	return tool.Exit(0)
}
