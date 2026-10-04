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
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
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
and done are as they were. where draws the
friends between work and fleet in its default frame, which draws no merge
table; the friends table is drawn after merge only under where --all, up
first, then held, then down, each by name, with no load column.

A friend's card: a card whose brief says WHO: friend (any friend) or
WHO: friend <name> (a row of the friends table; add and brief refuse any other)
is dealt by the tick to a friend up below her width, the one it names or the
one with the most free width, on her own fleet row friend.<name>, straight into
working; no machine is dealt it, and no presence or rebalance takes it back.
friend sync writes it as <friend>-working/inbox/<job>/BRIEF.md, the job
directory <card> at epoch 0 and <card>~<epoch> after a clear (its STATUS line
names the card, the branch to push and the report), and finishes it from
outbox/<job>/REPORT.md: Verdict: LAND with Head: <full sha> goes to review at
origin's tip of that branch when the tip is that Head (one git ls-remote), and
is refused, the card left working, when the tip is another sha (both named),
when origin has no branch of that name, when the tip cannot be read, or when
the card names no REPO: line; Verdict: HOLD, FAIL, FAILED or BROKEN is work
that came back failed, and a LAND without a full sha, an empty report and any
other verdict finish the card failed too, each with the report's first
paragraph. card prints who=; where counts it on her friends row.`) + "\n"
}

// friendVerbWords is what friend beat, friend down, friend up, friend health and friend take say on -h.
// The friends section (friendWords) stays on nova-sprint help friend. A name
// the table lacks is refused and names friend sync; friend sync exits 3 when
// the config cannot be read or holds no friend row (docs/SPEC-SPRINT.md section 1).
func friendVerbWords(name string) string {
	every, down := sprint.FriendBeatEvery.String(), sprint.FriendDownAfter.String()
	sync := "The name is one friend row of the friends table. friend sync copies those rows from nova-config; a name the table lacks is refused and the line names friend sync. friend sync exits 3 when the config cannot be read or holds no friend row. nova-sprint help friend says how the friends table is kept."
	switch name {
	case "friend beat":
		return "friend beat records that this friend is present. The friend's own machinery runs it every " + every + ". The friend is up while the last beat is under " + down + " old, and down once that long has passed with no beat, or when the friend has never beaten. A beat wakes the friend at once. Once the coordinator observes her (friend health), the observation decides her status and her beat no longer does. " + sync + "\n"
	case "friend down":
		return "friend down holds the named friend: held is the coordinator's decision alone, whatever she beats or the coordinator's daemon observes, and where counts working as 0 while the friend is held. --reason <text> and --until <RFC3339> say why and when you expect her back, shown in her status cell. friend up releases the hold. " + sync + "\n"
	case "friend up":
		return "friend up releases a hold that friend down set. It is not a beat: a friend released with no beat in the last " + down + " is down until the friend beats. " + sync + "\n"
	case "friend health":
		return "friend health is the coordinator's observation of the friend, written by the coordinator's daemon from its keepalive with hers: --state up (her session answered), asleep (her daemon answered, her session did not) or down; --seen, when the proof was seen; --generation, the seat's generation the daemon read with nova-sprint seat. The seat's holder alone writes it, at the seat's generation now: an observation from a seat that moved is refused, as is a proof not newer than the row holds, and nothing is written; the same observation again is answered as recorded (replayed=true). Once observed, the friend is up while the observation says up, under the seat's generation now, with its proof under " + sprint.FriendObservedDownAfter.String() + " old, and down otherwise (asleep is the daemon's word, kept on her row and shown as down; the table's words are up, held and down), with --reason and --until shown on her row; her own beat never makes her up again, and no observation holds her: held is the coordinator's friend down alone. " + sync + "\n"
	case "friend take":
		return "friend take takes cards dealt to the named friend, ready or working on her row and not started, back for the tick to deal again: each card's work card is retired and its primary goes working -> ready, one note on its story per take, and the next tick deals it as its next attempt (a card naming her goes to her again once she is up with room, so hold her first with friend down). A card is named by its work card (s1-4.w1) or its primary (s1-4). Not started means origin holds no push on the card's branch: the tip is read once per card, as friend sync reads a LAND's Head, and a card with a push is refused by name (it is her work, and her report finishes it), as is one whose tip cannot be read; one refusal refuses every card named, nothing written. --reason says why, on each card's story. " + sync + "\n"
	default:
		return ""
	}
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
	var reason, until *string
	if held {
		reason = fs.String("reason", "", "why she is held, shown on her row (her model allowance ran out)")
		until = fs.String("until", "", "when you expect her back, RFC3339, shown on her row")
	}
	friend, code := oneFriend(name, fs, args, stderr)
	if code != 0 {
		return code
	}
	var why string
	var back time.Time
	if held {
		why = strings.TrimSpace(*reason)
		if *until != "" {
			var err error
			if back, err = time.Parse(time.RFC3339, *until); err != nil {
				return refuse(stderr, name, "--until wants an RFC3339 time")
			}
		}
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if err := st.SetFriendHeld(context.Background(), friend, held, c.actor, why, back); err != nil {
		fmt.Fprintf(stderr, "%s %s: %s\n", prog, name, oneline.Escape(err.Error()))
		return 1
	}
	line := token(name) + " OK " + friend + " held=" + fmt.Sprint(held)
	if why != "" {
		line += " reason=" + oneline.Field(why)
	}
	if !back.IsZero() {
		line += " until=" + back.UTC().Format(time.RFC3339)
	}
	sayOK(stdout, c.json, name, line, map[string]any{"friend": friend, "held": held, "reason": why, "until": back.UTC()})
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

// cmdFriendHealth is the coordinator's observation of a friend (docs/SPEC-SPRINT.md
// section 1, "A friend's health"): the state word, the time of the proof it rests on,
// and the seat generation the daemon read the seat at, fenced by the seat in the step's
// own read (store.FriendHealth). The same observation again is answered as recorded,
// nothing written.
func (a *app) cmdFriendHealth(args []string, stdout, stderr io.Writer) int {
	const name = "friend health"
	fs, c := a.verbSetup(name)
	state := fs.String("state", "", "what the keepalive saw: up (her session answered), asleep (her daemon answered, her session did not) or down")
	seen := fs.String("seen", "", "when the proof this rests on was seen, RFC3339 (a session pong for up, a daemon pong for asleep, the judgment for down); a proof not newer than the row's, or dated after the server's clock, is refused")
	generation := fs.Uint64("generation", 0, "the seat's generation the daemon read (nova-sprint seat); any other than the seat's now is refused")
	queue := fs.Int("queue", 0, "what her pong said she has queued")
	working := fs.Int("working", 0, "what her pong said she is working")
	width := fs.Int("width", 0, "what her pong said her width is")
	reason := fs.String("reason", "", "why she is not up, shown on her row while the observation stands (her model allowance ran out)")
	until := fs.String("until", "", "when the daemon expects her back, RFC3339, shown on her row")
	friend, code := oneFriend(name, fs, args, stderr)
	if code != 0 {
		return code
	}
	if !slices.Contains(sprint.HealthStates, *state) {
		return refuse(stderr, name, "--state wants one of "+strings.Join(sprint.HealthStates, ", ")+", found "+orDashStr(*state, "none"))
	}
	at, err := time.Parse(time.RFC3339, *seen)
	if err != nil {
		return refuse(stderr, name, "--seen wants an RFC3339 time, the proof's")
	}
	if *generation == 0 {
		return refuse(stderr, name, "--generation wants the seat's generation the daemon read (nova-sprint seat prints it)")
	}
	if *queue < 0 || *working < 0 || *width < 0 {
		return refuse(stderr, name, "--queue, --working and --width are counts")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	obs := sprint.FriendHealth{State: *state, Seen: at.UTC(), Generation: *generation, Queue: *queue, Working: *working, Width: *width, Reason: strings.TrimSpace(*reason)}
	if *until != "" {
		if obs.Until, err = time.Parse(time.RFC3339, *until); err != nil {
			return refuse(stderr, name, "--until wants an RFC3339 time")
		}
		obs.Until = obs.Until.UTC()
	}
	h, status, replayed, err := st.FriendHealth(context.Background(), friend, c.actor, obs, c.op)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: %s; nothing was changed\n", prog, name, oneline.Escape(err.Error()))
		return 1
	}
	line := fmt.Sprintf("FRIEND-HEALTH OK %s state=%s seen=%s generation=%d status=%s", friend, h.State, h.Seen.Format(time.RFC3339), h.Generation, status)
	if replayed {
		line += " replayed=true"
	}
	sayOK(stdout, c.json, name, line, map[string]any{"friend": friend, "state": h.State, "seen": h.Seen, "generation": h.Generation,
		"queue": h.Queue, "working": h.Working, "width": h.Width, "status": status, "replayed": replayed})
	return 0
}

// cmdFriendTake is the coordinator taking a friend's dealt, unstarted cards back for the
// tick to deal again (docs/SPEC-SPRINT.md section 1, a friend's card taken back): each
// card named, ready or working on her row, has origin's tip of its branch read once (the
// tip friend sync reads a LAND's Head at), and the step (sprint.FriendTake) refuses by
// name a card with a push there, or one whose tip could not be read; one refusal
// refuses the step, nothing written.
func (a *app) cmdFriendTake(args []string, stdout, stderr io.Writer) int {
	const name = "friend take"
	fs, c := a.verbSetup(name)
	reason := fs.String("reason", "", "why the cards are taken back, on each card's story (she stalled)")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if len(pos) < 2 {
		return refuse(stderr, name, "wants a friend, a friend row of nova-config, and one or more of her cards, a work card (s1-4.w1) or its primary (s1-4)")
	}
	friend, ids := pos[0], pos[1:]
	if !sprint.ValidID(friend) {
		return refuse(stderr, name, "a friend name wants letters, digits, _ and -: "+friend)
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	ctx := context.Background()
	cards, err := st.ReadCells(ctx, sprint.Fleet, sprint.FriendRow(friend), sprint.Working, sprint.Ready)
	if err != nil {
		return a.readFailed(name, err, stderr)
	}
	packets, err := st.Packets(ctx, cards)
	if err != nil {
		return a.readFailed(name, err, stderr)
	}
	r := sprint.FriendTakeReq{Friend: friend, IDs: ids, Gens: map[string]int{}, Pushed: map[string]string{}, Unread: map[string]string{}, Reason: strings.TrimSpace(*reason), Who: c.actor}
	for _, p := range packets {
		if !slices.Contains(ids, p.Card) && !slices.Contains(ids, p.Primary) {
			continue
		}
		r.Gens[p.Card] = p.Gen
		repo := swarm.ReadCardBase([]byte(p.Brief)).Repo
		if repo == "" {
			r.Unread[p.Card] = "the card names no REPO: line"
			continue
		}
		switch at, err := a.tip(ctx, repo, p.Branch); {
		case err != nil:
			r.Unread[p.Card] = oneline.Escape(err.Error())
		case at != "":
			r.Pushed[p.Card] = at
		}
	}
	return a.runStep(name, *c, st, store.Step{Named: true, Args: store.ArgsOf(r), Verb: name, Load: []string{sprint.Fleet, sprint.Work}, Mirrors: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.FriendTake(s, r) }}, stdout, stderr)
}
