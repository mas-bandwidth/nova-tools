package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// readSeatPushes is name's push document, watch proofs included.
// ok is false when there is none.
func readSeatPushes(ctx context.Context, st *store.Store, name string) (sprint.SeatPushes, bool, error) {
	var file sprint.SeatPushes
	kv, ok := st.B.(store.KV)
	if !ok || name == "" {
		return file, false, nil
	}
	raw, ok, err := kv.GetKey(ctx, keySeatPush(name))
	if err != nil || !ok {
		return file, false, err
	}
	file, err = sprint.DecodeSeatPushes(raw)
	if err != nil {
		return file, false, fmt.Errorf("the push record of %s is not JSON: %w", name, err)
	}
	return file, true, nil
}

// putSeatPushes writes file and, the first time, the name teardown deletes by.
func putSeatPushes(ctx context.Context, kv store.KV, file sprint.SeatPushes) error {
	if file.Name == "" {
		return errors.New("a push record wants a name")
	}
	b, err := json.Marshal(file)
	if err != nil {
		return err
	}
	if err := kv.SetKey(ctx, keySeatPush(file.Name), string(b)); err != nil {
		return err
	}
	var names []string
	raw, ok, err := kv.GetKey(ctx, store.KeySeatPushers)
	if err != nil {
		return err
	}
	if ok {
		_ = json.Unmarshal([]byte(raw), &names) // ignored: an unreadable list is written again whole
	}
	if slices.Contains(names, file.Name) {
		return nil
	}
	l, err := json.Marshal(append(names, file.Name))
	if err != nil {
		return err
	}
	return kv.SetKey(ctx, store.KeySeatPushers, string(l))
}

// beatWatch stamps one watch proof on name's push document, keeping the rest.
func beatWatch(ctx context.Context, st *store.Store, name, field string, now time.Time) error {
	kv, ok := st.B.(store.KV)
	if !ok {
		return errors.New("this store keeps no keys, and the push record is one")
	}
	file, _, err := readSeatPushes(ctx, st, name)
	if err != nil {
		return err
	}
	if file.Name == "" {
		file.Name = name
	}
	if err := file.Beat(field, now); err != nil {
		return err
	}
	return putSeatPushes(ctx, kv, file)
}

// pushCommon is the store flags a who-works wrapper needs before the verb
// parses its own. Unknown flags are the verb's.
func pushCommon(verb string, args []string, getenv func(string) string) common {
	c := common{verb: verb}
	if getenv != nil {
		c.redis = firstEnv(getenv, "NOVA_SPRINT_REDIS", "NOVA_REDIS_ADDR")
		c.actor = getenv("NOVA_SPRINT_ACTOR")
	}
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--json":
			c.json = true
		case args[i] == "--redis" && i+1 < len(args):
			i++
			c.redis = args[i]
		case strings.HasPrefix(args[i], "--redis="):
			c.redis = strings.TrimPrefix(args[i], "--redis=")
		case args[i] == "--actor" && i+1 < len(args):
			i++
			c.actor = args[i]
		case strings.HasPrefix(args[i], "--actor="):
			c.actor = strings.TrimPrefix(args[i], "--actor=")
		}
	}
	return c
}

// guardWhoWorks runs next after the seat's four pushes are fresh, when the
// actor's proof is armed. Unarmed (the test binary, except names a test
// registers) calls next as the verb always did.
func (a *app) guardWhoWorks(verb string, args []string, stdout, stderr io.Writer, next func(*app, []string, io.Writer, io.Writer) int) int {
	c := pushCommon(verb, args, a.getenv)
	if !pushArmed(c.actor) {
		return next(a, args, stdout, stderr)
	}
	st, err := a.store(c)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if code := a.pushesBeforeWrite(st, verb, c.json, stdout, stderr); code != 0 {
		return code
	}
	return next(a, args, stdout, stderr)
}

// pushesBeforeWrite is the four-push gate of a verb that changes who works.
// "" holder, or a seat whose proof is not armed, is not gated. A fresh set
// prints one PUSH line per proof, except under --json, where stdout stays
// the verb's one object.
func (a *app) pushesBeforeWrite(st *store.Store, verb string, asJSON bool, stdout, stderr io.Writer) int {
	ctx := context.Background()
	holder, err := st.B.Coordinator(ctx)
	if err != nil {
		return a.readFailed(verb, err, stderr)
	}
	if holder == "" || !pushArmed(holder) {
		return 0
	}
	file, ok, err := readSeatPushes(ctx, st, holder)
	if err != nil {
		return a.readFailed(verb, err, stderr)
	}
	if why := pushSetDown(holder, file, ok, a.now()); why != "" {
		return refuse(stderr, verb, why)
	}
	if asJSON {
		return 0
	}
	fmt.Fprintln(stdout, formatPushSet(file, ok, a.now()))
	return 0
}

// pushSetDown is the one refusal while any of the four proofs is stale, ""
// when every one is fresh. The line carries each arm command after "; run: ".
func pushSetDown(name string, file sprint.SeatPushes, ok bool, now time.Time) string {
	var why, arm []string
	if !sprint.PushLive(file.PushRecord, ok, now) {
		why = append(why, "judgments: "+sprint.PushWhy(name, file.PushRecord, ok, now))
		arm = append(arm, sprint.PushSetup(name, file.PushRecord, ok))
	}
	for _, field := range []string{sprint.PushFieldBus, sprint.PushFieldFriends, sprint.PushFieldTransitions} {
		at, every, known := file.At(field)
		if !known || !sprint.WatchStale(at, every, now) {
			continue
		}
		why = append(why, watchWhy(fieldLabel(field), at, every, now))
		arm = append(arm, watchArm(field, name))
	}
	if len(why) == 0 {
		return ""
	}
	return "PUSH DOWN: " + strings.Join(why, "; ") + "; nothing was changed; run: " + strings.Join(arm, "; ")
}

func fieldLabel(field string) string {
	switch field {
	case sprint.PushFieldBus:
		return "bus"
	case sprint.PushFieldFriends:
		return "friends check"
	case sprint.PushFieldTransitions:
		return "transitions"
	}
	return field
}

func watchArm(field, name string) string {
	switch field {
	case sprint.PushFieldBus:
		return "nova-bus recv --as " + name + " --forever --exec '<deliver into the session>'"
	case sprint.PushFieldFriends:
		return "nova-sprint friends watch"
	case sprint.PushFieldTransitions:
		return "nova-sprint status watch"
	}
	return "nova-sprint " + field
}

func watchWhy(label string, at time.Time, every time.Duration, now time.Time) string {
	if at.IsZero() {
		return label + " has no proof: the seat is blind to it"
	}
	age := now.Sub(at).Truncate(time.Second)
	limit := every * sprint.PushSetLives
	return label + " last proved " + age.String() + " ago, past " + limit.String() + " (its period " + every.String() + " times three)"
}

// formatPushSet is the four PUSH lines, proven=<RFC3339> or DOWN.
func formatPushSet(file sprint.SeatPushes, ok bool, now time.Time) string {
	lines := []string{
		pushLine("judgments", file.Proven, sprint.PushLive(file.PushRecord, ok, now)),
	}
	for _, field := range []string{sprint.PushFieldBus, sprint.PushFieldFriends, sprint.PushFieldTransitions} {
		at, every, _ := file.At(field)
		lines = append(lines, pushLine(fieldLabel(field), at, !sprint.WatchStale(at, every, now)))
	}
	return strings.Join(lines, "\n")
}

func pushLine(name string, at time.Time, live bool) string {
	if !live || at.IsZero() {
		return "PUSH " + name + " DOWN"
	}
	return "PUSH " + name + " proven=" + at.UTC().Format(time.RFC3339)
}
