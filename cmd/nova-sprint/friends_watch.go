package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"io"
	"os"
	"strings"
)

func (a *app) cmdFriendsWatch(args []string, o, e io.Writer) int {
	return a.cmdPushObserver("friends watch", "friends", args, o, e)
}
func (a *app) cmdStatusWatch(args []string, o, e io.Writer) int {
	return a.cmdPushObserver("status watch", "transitions", args, o, e)
}

type pushObserverState struct {
	Actor    string `json:"actor"`
	Source   string `json:"source"`
	Snapshot string `json:"snapshot"`
}

// readPushObserverState retains only the last successfully delivered snapshot.
// An absent state starts with the present; an unreadable one proves nothing.
func readPushObserverState(path, actor, source string) (pushObserverState, error) {
	state := pushObserverState{Actor: actor, Source: source}
	if path == "" {
		return state, nil
	}
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if err = json.Unmarshal(body, &state); err != nil {
		return state, err
	}
	if state.Actor != actor || state.Source != source {
		return state, fmt.Errorf("observer state belongs to another actor or push; use a separate --state file")
	}
	return state, nil
}

// pushObserverPass proves a completed read and delivery, never merely a timer.
// The state advances after delivery succeeds, and its beat follows persistence.
func (a *app) pushObserverPass(ctx context.Context, c common, source, server, path string, state *pushObserverState) error {
	o, err := a.readPushObservation(ctx, c, source, server)
	if err != nil {
		return err
	}
	if o.Now.IsZero() || o.Set.Proven.After(o.Now) || !sprint.PushLive(o.Set.PushRecord, true, o.Now) {
		return fmt.Errorf("judgments push is not proven: %s", sprint.PushWhy(c.actor, o.Set.PushRecord, true, o.Now))
	}
	snapshot := string(o.State)
	if snapshot != state.Snapshot {
		if why := a.deliverPush(ctx, o.Set.PushRecord, "NOVA SPRINT "+strings.ToUpper(source)+"\n"+snapshot); why != "" {
			return fmt.Errorf("%s push failed: %s", source, why)
		}
		next := *state
		next.Snapshot = snapshot
		if path != "" {
			body, err := json.Marshal(next)
			if err != nil {
				return err
			}
			if err = atomicfile.Write(path, body, 0o600); err != nil {
				return err
			}
		}
		*state = next
	}
	return a.beatPushObserver(ctx, c, source, "", server, o)
}

// cmdPushObserver is a native loop with an owned interrupt and injected wait.
// A failed pass revokes its proof immediately; it never mints a session pong.
func (a *app) cmdPushObserver(name, source string, args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup(name)
	path := fs.String("state", "", "optional file holding the last successfully delivered snapshot; each actor and push has its own file")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, name, argErr("takes no words ", err, pos...))
	}
	if c.actor == "" {
		return refuse(stderr, name, "--actor <seat> is required: the observer belongs to that seat")
	}
	state, err := readPushObserverState(*path, c.actor, source)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	ctx, stop := a.notify(context.Background())
	defer stop()
	server := a.server(fs)
	period := sprint.SeatPushPeriods()[source]
	for ctx.Err() == nil {
		if err = a.pushObserverPass(ctx, *c, source, server, *path, &state); err != nil {
			if ctx.Err() != nil {
				return 0
			}
			if beatErr := a.beatPushObserver(ctx, *c, source, err.Error(), server); beatErr != nil {
				err = fmt.Errorf("%w; failure receipt could not be stored: %s", err, beatErr)
			}
			return refuse(stderr, name, err.Error())
		}
		if c.json {
			body, err := json.Marshal(map[string]any{"push": source, "name": c.actor, "checked": a.now(), "period": period.String()})
			if err != nil {
				return a.readFailed(name, err, stderr)
			}
			fmt.Fprintln(stdout, string(body))
		} else {
			fmt.Fprintf(stdout, "PUSH source=%s name=%s checked=%s period=%s\n", source, oneline.Field(c.actor), a.now().UTC().Format("2006-01-02T15:04:05Z07:00"), period)
		}
		select {
		case <-ctx.Done():
			return 0
		case <-a.after(period):
		}
	}
	return 0
}
