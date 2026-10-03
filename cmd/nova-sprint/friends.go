package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// The friends table (docs/SPEC-SPRINT.md section 1; the owner, 2026-10-02: "add a
// friends table, above fleet and below merge. friends | status for now.
// up/down/held"; "friends should be configured in nova-config"; "you should use
// heartbeats from each friend to track their state, and sort them alphabetically,
// and then by status, like with fleet"; and the same day: "please give friends in
// the friends table the same ready, working, width, done, ok%, status that we have
// for machines, but no load, since they don't correspond to a machine (at the
// moment...)". The roster is nova-config's friend rows alone, copied into the store by
// friend sync, which also writes each friend's width. A friend's counts are her
// sprint cards' (the cards dealt to her fleet row friend.<name>, their states
// and their finish verdicts), read from the fleet table by where — never from
// her inbox/outbox directories, which are only the transport of her cards
// (friendcards.go). A friend's machinery beats with friend beat; the
// coordinator holds one with friend down and releases it with friend up. The
// status is derived where it is shown (store.FriendRows), by
// sprint.FriendStatus (up, held, or down after sprint.FriendDownAfter without
// a beat; the owner, 2026-10-02 9:46 PM ET: "or every 1sec if you really want,
// then after 15 sec. asleep. better.", the word then asleep; 2026-10-03 8:04 AM
// ET: "Please change 'asleep' to 'down' so we have consistency across all
// tables") and in the fleet's order.

// friendWords is how a friend's row comes about, in nova-sprint help and
// nova-sprint help friend.
func friendWords() string {
	return strings.TrimSpace(`
The friends: the friends table is nova-config's friend rows, copied into the
store by friend sync (--pg, else NOVA_PG_DSN, as nova-config takes it): a friend
the store lacks is added, one nova-config no longer has is taken off with her
beat, and a friend that stays keeps her hold. The same sync writes each friend's width,
the jobs she works at once: her friend row's width (nova-config friend set
<friend> --width <n>, at least 1), `+fmt.Sprint(config.DefaultFriendWidth)+` when the row names none; a row whose width is
below 1 is refused with nothing changed. where counts the cards: ready, working,
width, done (ok and failed), ok% (ok over done, pooled in the footer) and
status, all from the friend's sprint cards — the cards dealt to her fleet row
friend.<name>, their states and their finish verdicts — never from her
inbox/outbox directories (those are only the transport of her cards, below). A friend says she is there with
nova-sprint friend beat <friend>, which her own machinery runs every `+sprint.FriendBeatEvery.String()+`
beside the friend's harness, for example in the wrapper that starts it
  while :; do nova-sprint friend beat <friend> >/dev/null 2>&1; sleep 1; done &
  trap 'kill $!' EXIT
and her status is up while her last beat is under `+sprint.FriendDownAfter.String()+` old, down once
she has gone `+sprint.FriendDownAfter.String()+` without a beat or when she has never beaten (a beat
wakes her at once), held while friend down holds her whatever she beats.
friend up releases the hold and is not a beat: a friend released with no beat
in the last `+sprint.FriendDownAfter.String()+` is down until she beats. A friend down shows
working 0: her cards stay on her row and count again when she beats; ready
and done are as they were. where shows the
friends after merge and before fleet, up first, then held, then down, each by
name, with no load column.

A friend's card: a card whose brief says WHO: friend (any friend) or
WHO: friend <name> (a row of the friends table; add and brief refuse any other)
is dealt by the tick to a friend up below her width, the one it names or the
one with the most free width, on her own fleet row friend.<name>, straight into
working; no machine is dealt it, and no presence or rebalance takes it back.
friend sync writes it as <friend>-working/inbox/<card>/BRIEF.md (its STATUS line
names the card, the branch to push and the report), and finishes it from
outbox/<card>/REPORT.md: Verdict: LAND with Head: <full sha> goes to review at
origin's tip of that branch when the tip is that Head (one git ls-remote), and
is refused naming both shas, the card left working, when it is not; Verdict: HOLD or FAIL is work that
came back failed, with the report's first paragraph. card prints who=; where counts it on her friends row.`) + "\n"
}

// friendsFn reads nova-config's friend rows (friend sync, friends clean), given
// the address of the config store (its --pg, else NOVA_PG_DSN).
type friendsFn func(ctx context.Context, pg string) ([]config.Row, error)

// readFriends is the real friendsFn: the friend rows of Postgres, by
// config.ResolveDSN, bounded.
func (a *app) readFriends(ctx context.Context, pg string) ([]config.Row, error) {
	var rows []config.Row
	err := a.withConfig(ctx, pg, func(ctx context.Context, st config.Store) error {
		var err error
		rows, err = st.List(ctx, config.KindFriend)
		return err
	})
	return rows, err
}

// reportValue is the value of a report's first line whose key, after any markdown marks
// (#, *, -, _, spaces), is one of keys in any case: the rest of the line after the colon;
// false when no line has one.
func reportValue(report string, keys ...string) (string, bool) {
	for _, line := range strings.Split(report, "\n") {
		key, rest, ok := strings.Cut(strings.TrimLeft(line, "#*-_ \t"), ":")
		if ok && slices.Contains(keys, strings.ToLower(strings.TrimSpace(key))) {
			return strings.TrimSpace(rest), true
		}
	}
	return "", false
}

func (a *app) cmdFriendSync(args []string, stdout, stderr io.Writer) int {
	const name = "friend sync"
	fs, c := a.verbSetup(name)
	pg := fs.String("pg", "", "the config store, Postgres postgres://user@host:port/db with no password (else NOVA_PG_DSN; the password from the variable NOVA_PG_PASSWORD_ENV names), as nova-config takes it")
	root := fs.String("root", "", "the directory the friends' working directories are under, <root>/<friend>-working (else HOME); the sync delivers and collects each friend's sprint cards there and never writes elsewhere")
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
	rows, err := a.friends(ctx, *pg)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: the config cannot be read: %s; nothing was changed\n", prog, name, oneline.WithRemedy(err.Error(), "nova-config friend list"))
		return exitCannotRead
	}
	if len(rows) == 0 {
		fmt.Fprintf(stderr, "%s %s: the config holds no friend row, and syncing to none would take every friend off; is this the fleet's config? run: nova-config friend list; nothing was changed\n", prog, name)
		return exitCannotRead
	}
	if *root == "" {
		*root = a.getenv("HOME")
	}
	if *root == "" {
		return refuse(stderr, name, "wants --root <dir>, the directory the friends' working directories are under (HOME is not set)")
	}
	// the specs: the roster and each friend's width, nothing read of her
	// working directory (the cards, below, are delivered and collected there,
	// and where counts them from the fleet table, never from the directory)
	specs := make([]store.FriendSpec, 0, len(rows))
	for _, r := range rows {
		n, width := r.Name, config.FriendWidth(r)
		if !sprint.ValidID(n) {
			fmt.Fprintf(stderr, "%s %s: a friend name wants letters, digits, _ and -: %s; fix the friend row in nova-config; nothing was changed\n", prog, name, oneline.Escape(n))
			return 1
		}
		if width < 1 {
			fmt.Fprintf(stderr, "%s %s: friend %s has width %d, and a friend's width is at least 1; run: nova-config friend set %s --width <n>; nothing was changed\n", prog, name, n, width, n)
			return 1
		}
		specs = append(specs, store.FriendSpec{Name: n, Width: width})
	}
	added, removed, updated, err := st.SyncFriends(ctx, specs)
	if err != nil {
		return a.readFailed(name, err, stderr)
	}
	// the friends' sprint cards: each one dealt to her delivered into her inbox, each one
	// she reported on finished from her outbox (friendcards.go)
	delivered, finished := 0, 0
	var said []string
	say := func(l string) {
		said = append(said, l)
		if !c.json {
			fmt.Fprintln(stdout, l)
		}
	}
	for _, s := range specs {
		d, f, err := a.friendCardsOf(ctx, st, s.Name, filepath.Join(*root, s.Name+"-working"), say)
		delivered, finished = delivered+d, finished+f
		if err != nil {
			fmt.Fprintf(stderr, "%s %s: the sprint cards of %s cannot be delivered or collected: %s; the friends table is synced; run: nova-sprint friend sync\n", prog, name, s.Name, oneline.Escape(err.Error()))
			return 1
		}
	}
	line := fmt.Sprintf("FRIEND-SYNC OK added=%s removed=%s updated=%s friends=%d", orDashStr(strings.Join(added, ","), "-"), orDashStr(strings.Join(removed, ","), "-"), orDashStr(strings.Join(updated, ","), "-"), len(rows))
	if delivered+finished > 0 {
		line += fmt.Sprintf(" delivered=%d finished=%d", delivered, finished)
	}
	if len(added)+len(removed)+len(updated)+delivered+finished == 0 {
		line += ": nothing to do, the friends table already matches the config"
	}
	sayOK(stdout, c.json, name, line, map[string]any{"added": orEmpty(added), "removed": orEmpty(removed), "updated": orEmpty(updated), "friends": len(rows),
		"delivered": delivered, "finished": finished, "cards": orEmpty(said)})
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
