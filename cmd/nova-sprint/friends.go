package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The friends table (docs/SPEC-SPRINT.md section 1; the owner, 2026-10-02: "add a
// friends table, above fleet and below merge. friends | status for now.
// up/down/held"; "friends should be configured in nova-config"; "you should use
// heartbeats from each friend to track their state, and sort them alphabetically,
// and then by status, like with fleet"). The roster is nova-config's friend rows
// alone, copied into the store by friend sync; a friend's machinery beats with
// friend beat; the coordinator holds one with friend down and releases it with
// friend up. The status is derived where it is shown (store.FriendRows), by the
// fleet's rule and in the fleet's order.

// friendWords is how a friend's status comes about, in nova-sprint help and
// nova-sprint help friend.
func friendWords() string {
	return strings.TrimSpace(`
The friends: the friends table is nova-config's friend rows, copied into the
store by friend sync (--pg, else NOVA_PG_DSN, as nova-config takes it): a friend
the store lacks is added, one nova-config no longer has is taken off with its
beat, and a friend that stays keeps its hold. A friend says it is there with
nova-sprint friend beat <friend>, which its own machinery runs every few seconds
beside the friend's harness, for example in the wrapper that starts it
  while :; do nova-sprint friend beat <friend> >/dev/null 2>&1; sleep 5; done &
  trap 'kill $!' EXIT
and its status is the fleet's rule: up until it has missed `+fmt.Sprint(sprint.MissedBeatsDown)+` beat windows of
`+sprint.BeatDeadline.String()+` in a row, down past that or when it has never beaten, held while friend
down holds it whatever it beats (friend up releases the hold). where shows the
friends after merge and before fleet, up first, then held, then down, each by
name.`) + "\n"
}

// friendsFn reads nova-config's friend rows' names (friend sync), given the
// address of the config store (its --pg, else NOVA_PG_DSN).
type friendsFn func(ctx context.Context, pg string) ([]string, error)

// readFriends is the real friendsFn: the friend rows of Postgres, by
// config.ResolveDSN, bounded.
func (a *app) readFriends(ctx context.Context, pg string) ([]string, error) {
	var names []string
	err := a.withConfig(ctx, pg, func(ctx context.Context, st config.Store) error {
		rows, err := st.List(ctx, config.KindFriend)
		for _, r := range rows {
			names = append(names, r.Name)
		}
		return err
	})
	return names, err
}

func (a *app) cmdFriendSync(args []string, stdout, stderr io.Writer) int {
	const name = "friend sync"
	fs, c := a.verbSetup(name)
	pg := fs.String("pg", "", "the config store, Postgres postgres://user@host:port/db with no password (else NOVA_PG_DSN; the password from the variable NOVA_PG_PASSWORD_ENV names), as nova-config takes it")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if len(pos) > 0 {
		return refuse(stderr, name, "takes no words, found "+oneline.Escape(pos[0]))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	ctx := context.Background()
	names, err := a.friends(ctx, *pg)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: the config cannot be read: %s; nothing was changed\n", prog, name, oneline.WithRemedy(err.Error(), "nova-config friend list"))
		return exitCannotRead
	}
	if len(names) == 0 {
		fmt.Fprintf(stderr, "%s %s: the config holds no friend row, and syncing to none would take every friend off; is this the fleet's config? run: nova-config friend list; nothing was changed\n", prog, name)
		return exitCannotRead
	}
	for _, n := range names {
		if !sprint.ValidID(n) {
			fmt.Fprintf(stderr, "%s %s: a friend name wants letters, digits, _ and -: %s; fix the friend row in nova-config; nothing was changed\n", prog, name, oneline.Escape(n))
			return 1
		}
	}
	added, removed, err := st.SyncFriends(ctx, names)
	if err != nil {
		return a.readFailed(name, err, stderr)
	}
	line := fmt.Sprintf("FRIEND-SYNC OK added=%s removed=%s friends=%d", orDashStr(strings.Join(added, ","), "-"), orDashStr(strings.Join(removed, ","), "-"), len(names))
	if len(added)+len(removed) == 0 {
		line += ": nothing to do, the friends table already matches the config"
	}
	sayOK(stdout, c.json, name, line, map[string]any{"added": orEmpty(added), "removed": orEmpty(removed), "friends": len(names)})
	return 0
}

func (a *app) cmdFriendBeat(args []string, stdout, stderr io.Writer) int {
	const name = "friend beat"
	fs, c := a.verbSetup(name)
	friend, code := oneFriend(name, fs, args, stderr)
	if code != 0 {
		return code
	}
	c.orActor(friend)
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	b, err := st.FriendBeat(context.Background(), friend)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: %s\n", prog, name, oneline.Escape(err.Error()))
		return 1
	}
	sayOK(stdout, c.json, name, "FRIEND-BEAT OK "+friend+" at="+b.At.Format(time.RFC3339), map[string]any{"friend": friend, "at": b.At})
	return 0
}

// cmdFriendHold is friend down (held) and friend up (the hold released).
func (a *app) cmdFriendHold(held bool, args []string, stdout, stderr io.Writer) int {
	name := map[bool]string{true: "friend down", false: "friend up"}[held]
	fs, c := a.verbSetup(name)
	friend, code := oneFriend(name, fs, args, stderr)
	if code != 0 {
		return code
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if err := st.SetFriendHeld(context.Background(), friend, held, c.actor); err != nil {
		fmt.Fprintf(stderr, "%s %s: %s\n", prog, name, oneline.Escape(err.Error()))
		return 1
	}
	sayOK(stdout, c.json, name, token(name)+" OK "+friend+" held="+fmt.Sprint(held), map[string]any{"friend": friend, "held": held})
	return 0
}

// oneFriend is the one friend a verb names, or its refusal.
func oneFriend(verbName string, fs flagSet, args []string, stderr io.Writer) (string, int) {
	pos, err := parse(fs, args)
	if err != nil {
		return "", refuse(stderr, verbName, err.Error())
	}
	if len(pos) != 1 {
		return "", refuse(stderr, verbName, "wants one friend, a friend row of nova-config")
	}
	if !sprint.ValidID(pos[0]) {
		return "", refuse(stderr, verbName, "a friend name wants letters, digits, _ and -: "+pos[0])
	}
	return pos[0], 0
}

// orEmpty is the list, or an empty one in place of nil (JSON [] rather than null).
func orEmpty(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}
