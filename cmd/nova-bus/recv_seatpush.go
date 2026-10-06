package main

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// proveCoordinatorBus stamps the seat's bus-push proof when as is the
// coordinator. get and set speak Redis keys (sprint.Names.Key), the keys a
// raw client reads: sprint:coordinator holds the holder's name, and the
// proof is bus_at on sprint:seat-push:<name>. A caller who is not the
// coordinator writes nothing.
//
// recv --forever owes this once a pass. The loop is cmd/nova-bus/main.go,
// the for that waits ForeverBlock, and this card does not edit that file.
func proveCoordinatorBus(ctx context.Context, get func(context.Context, string) (string, bool, error), set func(context.Context, string, string) error, as string, now time.Time) error {
	if as == "" {
		return nil
	}
	names := sprint.Names{}
	holder, ok, err := get(ctx, names.Key("coordinator"))
	if err != nil || !ok || holder != as {
		return err
	}
	key := names.Key(store.SeatPushKey(as))
	var file sprint.SeatPushes
	raw, ok, err := get(ctx, key)
	if err != nil {
		return err
	}
	if ok && raw != "" {
		file, err = sprint.DecodeSeatPushes(raw)
		if err != nil {
			return err
		}
	}
	if file.Name == "" {
		file.Name = as
	}
	if err := file.Beat(sprint.PushFieldBus, now); err != nil {
		return err
	}
	b, err := json.Marshal(file)
	if err != nil {
		return err
	}
	if err := set(ctx, key, string(b)); err != nil {
		return err
	}
	return rememberBusPusher(ctx, get, set, names, as)
}

// rememberBusPusher adds as to sprint:seat-pushers when it is not there, so
// teardown still deletes the push record.
func rememberBusPusher(ctx context.Context, get func(context.Context, string) (string, bool, error), set func(context.Context, string, string) error, names sprint.Names, as string) error {
	key := names.Key(store.KeySeatPushers)
	var have []string
	raw, ok, err := get(ctx, key)
	if err != nil {
		return err
	}
	if ok && raw != "" {
		if err := json.Unmarshal([]byte(raw), &have); err != nil {
			return err
		}
	}
	if slices.Contains(have, as) {
		return nil
	}
	b, err := json.Marshal(append(have, as))
	if err != nil {
		return err
	}
	return set(ctx, key, string(b))
}
