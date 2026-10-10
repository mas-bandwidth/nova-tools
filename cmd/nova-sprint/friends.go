package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
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
// (friendcards.go). A friend's nova-friend daemon beats with friend beat, and
// nothing else does (docs/SPEC-FRIEND.md, "The beat comes from the daemon"); the
// coordinator holds one with friend down and releases it with friend up. The
// status is derived where it is shown (store.FriendRows), by
// sprint.FriendStatus (held; up only on her session's evidence, a wake ping her
// session answered within sprint.FriendPongWindow or a card of hers finished within
// sprint.FriendFinishWindow; else down, her beat never counting: the owner,
// 2026-10-05, "there is no value in things that are answered just by the daemon";
// 2026-10-03 8:04 AM ET: "Please change 'asleep' to 'down' so we have consistency
// across all tables") and in the fleet's order.

// friendWords is how a friend's row comes about, in nova-sprint help and
// nova-sprint help friend.
func friendWords() string {
	return strings.TrimSpace(`
The friends: the friends table is nova-config's friend rows, copied into the
store by friend sync (--pg, else NOVA_PG_DSN, as nova-config takes it; with
--every <d> it syncs again each time d passes until interrupted, acting as the
seat each pass, the friend sync loop row's verb, docs/FRIENDS.md; friend sync
install --every <d> runs that loop as this machine's own service, a launchd agent
or a systemd user unit, and friend sync uninstall removes it): a friend
the store lacks is added, one nova-config no longer has is taken off with her
beat, and a friend that stays keeps her hold. The same sync writes each friend's width,
the jobs she works at once: her friend row's width (nova-config friend set
<friend> --width <n>, at least 1), `+fmt.Sprint(config.DefaultFriendWidth)+` when the row names none; and her delivery mode (mode:
batch|one-shot, default batch); a row whose width is below 1 is refused with nothing changed. where counts the cards: ready, working,
width, done (ok and failed), ok% (ok over done, pooled in the footer) and
status, all from the friend's sprint cards — the cards dealt to her fleet row
friend.<name>, their states and their finish verdicts — never from her
inbox/outbox directories (those are only the transport of her cards, below). A friend says she is there with
nova-sprint friend beat <friend>, which her nova-friend daemon runs every `+sprint.FriendBeatEvery.String()+`
while it runs (nova-friend install; docs/SPEC-FRIEND.md), and nothing else
beats for her. Her beat may carry --daemon-version <stamp>, the binary's build stamp, kept on the beat record and read by nova-sprint seat. The beat is recorded and shown, and it never makes her up: her
status is up only on evidence from her own session, a wake ping her session
answered (friend health --state up) within `+sprint.FriendPongWindow.String()+` or a card of hers
finished within `+sprint.FriendFinishWindow.String()+`, down otherwise with the missing evidence
named on her row, and held while friend down holds her whatever she does.
friend up releases the hold and is no evidence: a friend released with none
in its window is down until her session gives some. A friend down shows
working 0: her cards stay on her row and count again when she is up; ready
and done are as they were. where draws the
friends between work and fleet in its default frame, which draws no merge
table; the friends table is drawn after merge only under where --all, up
first, then held, then down, each by name, with no load column.

WHO: friend <name> prefers a known friend while she is up with room.
WHO: only friend <name> waits for that friend alone. Other work, including
cards with no WHO line, goes first to subscription friends whose tiers cover
it, then to the fleet. Among eligible friends, most free room wins and name
breaks ties. In batch mode her room is twice her width: she works at width and
queues the rest. In one-shot mode she holds one card, and the next only after
the last one finished. nova-sprint unpin <id>... --reason <text> removes an
unstarted card's stored WHO choice without editing its brief; --stream <s>
selects a stream, and --dry-run only previews it. Work assigned to a friend
uses her fleet row friend.<name>; presence never takes it back. friend take
takes back what she has not started, and friend down takes back every card.
friend sync writes it as inbox/<job>/BRIEF.md in her working directory (her
nova-config row's dir, else <root>/<friend>-working, said once), the job
directory <card> at epoch 0 and <card>~<epoch> after a clear (its STATUS line
names the card, the branch to push and the report), and finishes it from
outbox/<job>/REPORT.md: Verdict: LAND with Head: <full sha> goes to review at
origin's tip of that branch when the tip is that Head (one git ls-remote), and
is refused, the card left working, when the tip is another sha (both named),
when origin has no branch of that name, when the tip cannot be read, or when
the card names no REPO: line; Verdict: HOLD, FAIL, FAILED or BROKEN is work
that came back failed, but one whose first paragraph names a brief defect (the
base lacks a PATHS file, a duplicate of landed work, a decision delivered, or
the label "brief defect") is the brief's: it counts on the stream and in
neither done nor ok%, and its judgment asks to re-cut the brief, never
rework; a LAND without a full sha, an empty report and any other verdict
finish the card failed too, each with the report's first paragraph. card
prints who=; where counts it on her friends row.

friend reconcile <friend> settles her cards when her own account and the table
disagree: it reads inbox/QUEUE.json in her working directory, {"tasks":[{"id":"<card>",
"state":"queued|working|done"}]} (an id is the card or its job directory), and
her outbox, and for each card working on her row collects it when
outbox/<job>/REPORT.md is there (as friend sync does), keeps it while her queue
says queued or working, and returns it to ready (its work card retired, no
failed-work judgment, the tick deals it again at its next attempt) when no
report is there and her queue says done, or does not hold it though it was
written after the card's deal (a card dealt since is kept: she has not had
it to account for). An id of her queue that is no card working on her row is
named and left alone. It exits 1 when a collect or the return was refused.
--dry-run says each card's settlement and writes nothing.`) + "\n"
}

// friendVerbWords is what friend beat, friend down, and friend up say on -h.
// The friends section (friendWords) stays on nova-sprint help friend. A name
// the table lacks is refused and names friend sync; friend sync exits 3 when
// the config cannot be read or holds no friend row (docs/SPEC-SPRINT.md section 1).
func friendVerbWords(name string) string {
	every, pong, finish := sprint.FriendBeatEvery.String(), sprint.FriendPongWindow.String(), sprint.FriendFinishWindow.String()
	sync := "The name is one friend row of the friends table. friend sync copies those rows from nova-config; a name the table lacks is refused and the line names friend sync. friend sync exits 3 when the config cannot be read or holds no friend row. nova-sprint help friend says how the friends table is kept."
	switch name {
	case "friend beat":
		return "friend beat records that this friend is present, and --running the cards she is running now, which friend take and friend down leave with her. --working, --queue and --width are her own counts as her daemon keeps them, and --load her load as a percent, as fleet beat --load gives a machine's: her word, carried on where --json's friends beside the table's counts, which stay the sprint's. Her nova-friend daemon runs it every " + every + " while it runs, and nothing else beats for her: no loop beside the daemon. The beat is recorded, and its age shown on her row, and it never makes her up, whoever sends it: she is up only on evidence from her own session, a wake ping her session answered (friend health --state up) under " + pong + " old, her session's answer to a check her daemon asked under " + sprint.FriendProofLive.String() + " old while her beats go on (--check <nonce> when her daemon asks, --pong <nonce> when her session answers, each with --run, the daemon's run: an answer proves only to the run that asked it, once, within " + sprint.CheckAnswerWithin.String() + " of the ask; a time, a nonce never asked or one answered already is a beat with no proof, said on the line as no_proof=, but for the old --pong <time>, which counts for " + sprint.LegacyPongGrace.String() + " after the server starts; the verb trusts its caller as her, so a caller who asks and answers in one beat proves her), or a card of hers finished under " + finish + " old, and down otherwise, her row naming the evidence missing. --until <RFC3339> and --reason <text> are her daemon's word that she is down until then and why (her harness at its usage limit or out of credits): while her last beat says so she is down, whatever her session's evidence, with the pair shown in why she is down and on her report; a beat can say down, never up, and a beat without --until withdraws the word. --daemon-version <stamp> is this daemon's build stamp, kept on the beat record as daemon_version and read by nova-sprint seat. " + sync + "\n"
	case "friend down":
		return "friend down holds the named friend, as fleet down holds a machine: held is the coordinator's decision alone, whatever she beats or the coordinator's daemon observes; the tick deals her nothing, and where counts working as 0 while the friend is held. Every card dealt to her that she has not started goes back to ready, as friend take --all-unstarted takes it, and the next tick deals it to a friend up with room (a card whose WHO line names her waits for her); a card she has started (a push on its branch, her beat naming it running) stays with her and finishes, each named on a NOTE line. --reason <text> and --until <RFC3339> say why and when you expect her back, shown in her status cell. friend up releases the hold. friend down is hold <friend> in the old words, kept for one release: run nova-sprint hold <friend> --reason <text> (her cards finish; --return withdraws them), and unhold <friend>. " + sync + "\n"
	case "friend up":
		return "friend up releases a hold that friend down set. --width sets her width, the jobs she works at once (the deal holds her at twice that), as fleet up --width sets a machine's, until friend sync sets her nova-config row's again. It is no evidence: a friend released with no session pong in the last " + pong + " and no card finished in the last " + finish + " is down until her session gives some. friend up is unhold <friend> in the old words, kept for one release. " + sync + "\n"
	case "friend health":
		return "friend health is the coordinator's observation of the friend, written by the coordinator's daemon from its keepalive with hers: --state up (her session answered a wake ping), asleep (her daemon answered, her session did not) or down; --seen, when the proof was seen; --generation, the seat's generation the daemon read with nova-sprint seat. The seat's holder alone writes it, at the seat's generation now: an observation from a seat that moved is refused, as is a proof not newer than the row holds, and nothing is written; the same observation again is answered as recorded (replayed=true). An observation up, under the seat's generation now, with its proof under " + pong + " old, is her session's evidence and she is up on it (as on a card of hers finished under " + finish + " old); otherwise it is none (asleep is the daemon's word, kept on her row and shown as down; the table's words are up, held and down), with --reason and --until shown on her row; her beat never makes her up, and no observation holds her: held is the coordinator's friend down alone. --clear removes her observation (the seat's holder alone; --dry-run says what stood and writes nothing): her status is then her session's evidence alone, a card of hers finished under " + finish + " old, and FRIEND-HEALTH OK <friend> cleared=true was=<word|none> status=<up|held|down> says so; the stall ladder's release removes it the same way. " + sync + "\n"
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
	// install and uninstall put the --every loop in place as this machine's service
	// (friendsync_install.go)
	if len(args) > 0 && args[0] == "install" {
		return a.cmdFriendSyncInstall(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "uninstall" {
		return a.cmdFriendSyncUninstall(args[1:], stdout, stderr)
	}
	fs, c := a.verbSetup(name)
	pg := fs.String("pg", "", "the config store, Postgres postgres://user@host:port/db with no password (else NOVA_PG_DSN; the password from the variable NOVA_PG_PASSWORD_ENV names), as nova-config takes it")
	root := fs.String("root", "", "the directory holding <root>/<friend>-working for a friend whose nova-config row has no dir (else HOME); the sync delivers and collects each friend's sprint cards in her row's dir, else there, and never writes elsewhere")
	every := fs.Duration("every", 0, "sync now and again each time this passes, until interrupted, as the sprint's coordinator seat when no --actor is given, read again each pass (default: once); friend sync install --every <d> runs it as this machine's service and friend sync uninstall removes it (-h of each), or the friend sync loop row runs it (docs/FRIENDS.md)")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if len(pos) > 0 {
		return refuse(stderr, name, "takes no words, found "+oneline.Escape(pos[0]))
	}
	if *every < 0 {
		return refuse(stderr, name, "--every wants a duration above zero, or none for one pass, got "+every.String()+"; run: nova-sprint friend sync --every 15s")
	}
	if *every > 0 {
		return a.friendSyncLoop(*c, *pg, *root, *every, stdout, stderr)
	}
	code, _ := a.friendSyncPass(*c, *pg, *root, stdout, stderr)
	return code
}

// friendSyncPass is one pass of friend sync on a store it opens as c says: its exit
// code, and whether it changed anything (a friend added, taken off or updated, a card
// delivered or finished).
func (a *app) friendSyncPass(c common, pg, root string, stdout, stderr io.Writer) (int, bool) {
	const name = "friend sync"
	st, err := a.store(c)
	if err != nil {
		return refuse(stderr, name, err.Error()), false
	}
	ctx := context.Background()
	rows, err := a.friends(ctx, pg)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: the config cannot be read: %s; nothing was changed\n", prog, name, oneline.WithRemedy(err.Error(), "nova-config friend list"))
		return exitCannotRead, false
	}
	if len(rows) == 0 {
		fmt.Fprintf(stderr, "%s %s: the config holds no friend row, and syncing to none would take every friend off; is this the fleet's config? run: nova-config friend list; nothing was changed\n", prog, name)
		return exitCannotRead, false
	}
	if root == "" {
		root = a.getenv("HOME")
	}
	// the specs: the roster and each friend's width, nothing read of her
	// working directory (the cards, below, are delivered and collected there,
	// and where counts them from the fleet table, never from the directory)
	specs := make([]store.FriendSpec, 0, len(rows))
	for _, r := range rows {
		n, width := r.Name, config.FriendWidth(r)
		if !sprint.ValidID(n) {
			fmt.Fprintf(stderr, "%s %s: a friend name wants letters, digits, _ and -: %s; fix the friend row in nova-config; nothing was changed\n", prog, name, oneline.Escape(n))
			return 1, false
		}
		if width < 1 {
			fmt.Fprintf(stderr, "%s %s: friend %s has width %d, and a friend's width is at least 1; run: nova-config friend set %s --width <n>; nothing was changed\n", prog, name, n, width, n)
			return 1, false
		}
		if root == "" && config.FriendDir(r) == "" {
			return refuse(stderr, name, "wants --root <dir>, the directory holding <root>/"+n+"-working, for her nova-config row has no dir (HOME is not set); or run: nova-config friend set "+n+" --dir <her working directory>"), false
		}
		specs = append(specs, store.FriendSpec{Name: n, Width: width, Class: friendClass(r), Mode: config.FriendMode(r), ConfigDir: r.Fields["config_dir"], Dir: config.FriendDir(r), TokenCap: config.FriendTokenCap(r), TokenCapSet: true, Roles: friendRoles(r), Billing: r.Fields["billing"], Streams: r.Fields["streams"], Kinds: r.Fields["kinds"]})
	}
	added, removed, updated, err := st.SyncFriends(ctx, specs)
	if err != nil {
		return a.readFailed(name, err, stderr), false
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
		dir := a.friendDir(s.Name, s.Dir, root, stderr)
		// her starts first, so a card she began is working before its report is read
		if err := a.friendStartsOf(ctx, st, s.Name, dir, say); err != nil {
			fmt.Fprintf(stderr, "%s %s: the sprint cards of %s cannot be read for her starts: %s; the friends table is synced; run: nova-sprint friend sync\n", prog, name, s.Name, oneline.Escape(err.Error()))
			return 1, false
		}
		d, f, err := a.friendCardsOf(ctx, st, s.Name, dir, s.Dir, say)
		delivered, finished = delivered+d, finished+f
		if err != nil {
			fmt.Fprintf(stderr, "%s %s: the sprint cards of %s cannot be delivered or collected: %s; the friends table is synced; run: nova-sprint friend sync\n", prog, name, s.Name, oneline.Escape(err.Error()))
			return 1, false
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
	return 0, len(added)+len(removed)+len(updated)+delivered+finished > 0
}

// friendJobWalk bounds the walk of one job's directory for her first write: a checkout of
// this size is read whole, and a larger one up to it.
var friendJobWalk = friend.ActivityLimits{Files: 20000, Time: 250 * time.Millisecond}

// friendJobBegun is why friend sync reads her job as begun, "" while it is not: her report
// in outbox/<job>/REPORT.md, or a write under jobs/<job> after its staging (its JOB.md, which
// her daemon writes after the clone: friend.Stage). A job not staged is not begun.
func friendJobBegun(dir, job string, now func() time.Time) string {
	if fi, err := os.Lstat(filepath.Join(dir, "outbox", job, "REPORT.md")); err == nil && fi.Mode().IsRegular() {
		return "her report is in outbox/" + job
	}
	jobDir := filepath.Join(dir, friend.JobsDir, job)
	staged, err := os.Lstat(filepath.Join(jobDir, friend.JobFile))
	if err != nil || !staged.Mode().IsRegular() {
		return ""
	}
	if friend.NewestWrite(os.DirFS(jobDir), []string{"."}, now, friendJobWalk).After(staged.ModTime()) {
		return "a write under " + friend.JobsDir + "/" + job + " after its staging"
	}
	return ""
}

// friendStartsOf is friend sync's start receipt for one friend's cards (docs/SPEC-SPRINT.md
// section 1, a friend's card is working once she starts it): each card on her row, ready, or
// working with no start of hers (her finish's next or a take-back's next moved it there),
// whose job she has begun (friendJobBegun) is started (sprint.FriendStart, by id at its
// generation): working, its deadline from now, one line each. A start refused is said on a
// line and read again next sync. It runs before her cards are collected, so a report on a
// card she began and the deal held ready is read on a working card.
func (a *app) friendStartsOf(ctx context.Context, st *store.Store, name, dir string, say func(string)) error {
	row := sprint.FriendRow(name)
	cards, err := st.ReadCells(ctx, sprint.Fleet, row, sprint.Ready, sprint.Working)
	if err != nil || len(cards) == 0 {
		return err
	}
	cards = slices.DeleteFunc(cards, func(c *sprint.Card) bool {
		return c.F("kind") != "work" || c.Col == sprint.Working && c.F(sprint.FieldStarted) == strconv.Itoa(max(c.Int("gen"), 1))
	})
	if len(cards) == 0 {
		return nil
	}
	packets, err := st.Packets(ctx, cards)
	if err != nil {
		return err
	}
	for i, p := range packets {
		if p.Kind == "read" || !sprint.ValidCardID(p.Card) {
			continue
		}
		job := friendJobOf(p)
		why := friendJobBegun(dir, job, a.now)
		if why == "" {
			continue
		}
		c := cards[i]
		r := sprint.FriendStartReq{Friend: name, IDs: []string{c.ID}, Gens: map[string]int{c.ID: max(c.Int("gen"), 1)}, Why: map[string]string{c.ID: why}}
		step := store.FriendStartStep(r)
		step.Actor, step.Epoch = st.Actor, &p.Epoch
		res, err := st.Run(ctx, step)
		if err != nil {
			return err
		}
		if len(res.Refused) > 0 {
			say(fmt.Sprintf("FRIEND-CARD NOTE friend=%s card=%s job=%s: begun (%s), and not started: %s; the next sync reads it again", name, c.ID, oneline.Field(job), why, oneline.Escape(res.Refused[0].Why)))
			continue
		}
		say(fmt.Sprintf("FRIEND-CARD STARTED friend=%s card=%s job=%s: %s", name, c.ID, oneline.Field(job), why))
	}
	return nil
}

func (a *app) cmdFriendBeat(args []string, stdout, stderr io.Writer) int {
	return a.friendBeat(context.Background(), args, func(c common) (*store.Store, error) { return a.store(c) }, stdout, stderr)
}

// The server's allowlist of a friend's beat flags is built in serve.go before
// any init runs. --daemon-version is registered here, and the refusal's list
// of flags rebuilt, so the served beat accepts the stamp without that file
// changing (docs/SPEC-SPRINT.md, daemon-supervised-r-b.w7).
func init() {
	friendBeatFlags["--daemon-version"] = oneLineText
	keys := make([]string, 0, len(friendBeatFlags))
	for k := range friendBeatFlags {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	friendBeatServed = strings.Join(keys, ", ")
}

// friendBeat is friend beat on the store open gives it: the verb's (a.store), and the
// server's beat lane's (servelanes.go), which answer alike. It reads nothing of the app
// but its environment, so the beat lane runs it beside the line.
func (a *app) friendBeat(ctx context.Context, args []string, open func(common) (*store.Store, error), stdout, stderr io.Writer) int {
	const name = "friend beat"
	fs, c := a.verbSetup(name)
	build := fs.String("build", "", "the build her daemon runs (its version line's build): a friend come up is told to update when it is not the server's")
	started := fs.String("started", "", "when her daemon started, RFC3339: its generation; a start not seen before raises a status judgment")
	present := fs.String("present", "", "when her daemon sent her the present on its start, RFC3339: the snap-to-present step of a friend come up")
	running := fs.String("running", "", "the cards she is running now, comma separated (work card ids, her job names or primaries): friend take and friend down leave them with her")
	working := fs.String("working", "", "how many jobs she is working now, as her daemon counts them")
	stopReturns := fs.String("stop-returns", "", "how many stop-returns her lanes still owe after the machine's stop (section 14): start waits for zero")
	queue := fs.String("queue", "", "how many jobs she holds queued, as her daemon counts them")
	width := fs.String("width", "", "her width as her daemon has it (the deal's is the roster's: friend up --width)")
	load := fs.String("load", "", "her load as a percent, as fleet beat --load gives a machine's")
	active := fs.String("active", "", "the newest write under her working directory and outbox, as her daemon found it, RFC3339: her session's last activity")
	check := fs.String("check", "", "the nonce of the SESSION CHECK her daemon just put into her session: the server keeps it, so an answer naming it within "+sprint.CheckAnswerWithin.String()+" proves her session")
	pong := fs.String("pong", "", "the nonce of a check her session answered: her session's evidence for "+sprint.FriendProofLive.String()+" while her beat is fresh, only when this daemon's run asked it (--check) within "+sprint.CheckAnswerWithin.String()+", and once; anything else, a time included, is a beat with no proof")
	run := fs.String("run", "", "her daemon's run, its generation: a check proves only when its answer names the run that asked it")
	until := fs.String("until", "", "her daemon's word that she is down until then, RFC3339: her harness at its usage limit or out of credits")
	reason := fs.String("reason", "", "why she is down until --until, as her daemon read it")
	daemonVersion := fs.String("daemon-version", "", "this daemon's build stamp, kept on the beat record as daemon_version and read by nova-sprint seat")
	friend, code := oneFriend(name, fs, args, stderr)
	if code != 0 {
		return code
	}
	provided := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { provided[f.Name] = true })
	rep := sprint.FriendReport{}
	if provided["running"] {
		rep.Running = sprint.Split(*running)
		if rep.Running == nil {
			rep.Running = []string{} // a named empty list clears the stored list
		}
	}
	if b := strings.TrimSpace(*build); b != "" {
		rep.Build = oneline.Field(b)
	}
	for _, t := range []struct {
		flag, text string
		to         *time.Time
	}{{"--started", *started, &rep.Started}, {"--present", *present, &rep.Present}} {
		if t.text == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339, t.text)
		if err != nil {
			return refuse(stderr, name, t.flag+" wants an RFC3339 time, found "+oneline.Escape(t.text))
		}
		*t.to = at.UTC().Truncate(time.Second)
	}
	switch {
	case *until != "":
		at, err := time.Parse(time.RFC3339, *until)
		if err != nil {
			return refuse(stderr, name, "--until wants an RFC3339 time, found "+oneline.Escape(*until))
		}
		rep.Until, rep.Reason = at.UTC().Truncate(time.Second), oneline.Escape(*reason)
	case *reason != "":
		return refuse(stderr, name, "--reason says why she is down, and wants --until")
	}
	if *active != "" {
		at, err := time.Parse(time.RFC3339, *active)
		if err != nil {
			return refuse(stderr, name, "--active wants an RFC3339 time, found "+oneline.Escape(*active))
		}
		rep.Active = at.UTC().Truncate(time.Second)
	}
	if *check != "" && !sprint.ValidID(*check) {
		return refuse(stderr, name, "--check wants a nonce (letters, digits, _ and -), found "+oneline.Escape(*check))
	}
	if *run != "" && !sprint.ValidID(*run) {
		return refuse(stderr, name, "--run wants her daemon's run id (letters, digits, _ and -), found "+oneline.Escape(*run))
	}
	words := sprint.BeatWords{Run: *run, Check: *check, Pong: oneline.Escape(*pong)}
	if at, err := time.Parse(time.RFC3339, *pong); err == nil && !a.serveStarted.IsZero() && a.now().Sub(a.serveStarted) < sprint.LegacyPongGrace {
		// a daemon from before the nonces, within the server's first hour: counted as before
		words.Pong, words.Legacy = "", at
	}
	for _, n := range []struct {
		flag, text string
		min        int
		to         **int
	}{{"--working", *working, 0, &rep.Working}, {"--queue", *queue, 0, &rep.Queue}, {"--width", *width, 1, &rep.Width}, {"--stop-returns", *stopReturns, 0, &rep.StopReturns}} {
		if n.text == "" {
			if provided[strings.TrimPrefix(n.flag, "--")] {
				return refuse(stderr, name, n.flag+" wants a whole number, found an empty value")
			}
			continue
		}
		v, err := strconv.Atoi(n.text)
		if err != nil || v < n.min {
			return refuse(stderr, name, fmt.Sprintf("%s wants a whole number of at least %d, found %s", n.flag, n.min, oneline.Escape(n.text)))
		}
		*n.to = &v
	}
	if !rep.Until.IsZero() && rep.Working == nil {
		zero := 0
		rep.Working = &zero // a friend down works nothing, and the store keeps no report with no count
	}
	var given *float64
	if *load != "" {
		v, err := strconv.ParseFloat(strings.TrimSuffix(*load, "%"), 64)
		if err != nil || v < 0 {
			return refuse(stderr, name, "--load wants a percent, found "+oneline.Escape(*load))
		}
		given = &v
	}
	version := *daemonVersion
	if version != "" && !oneLineText(version) {
		return refuse(stderr, name, "--daemon-version wants one line of text, found "+oneline.Escape(version))
	}
	if version != "" {
		rep.DaemonVersion = oneline.Field(version)
	}
	c.orActor(friend)
	st, err := open(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	b, proof, err := st.FriendBeatProof(ctx, friend, rep, given, words)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: %s\n", prog, name, oneline.Escape(err.Error()))
		return 1
	}
	line, facts := "FRIEND-BEAT OK "+friend+" at="+b.At.Format(time.RFC3339)+" set="+proof.Set, map[string]any{"friend": friend, "at": b.At, "set": proof.Set}
	if words.Check != "" {
		line += " check=" + words.Check
		facts["check"] = words.Check
	}
	switch {
	case proof.Proved && !words.Legacy.IsZero():
		line += " proved=legacy"
		facts["proved"] = "legacy"
	case proof.Proved:
		line += " proved=" + words.Pong
		facts["proved"] = words.Pong
	case proof.NoProof != "":
		line += " no_proof=" + oneline.Quote(proof.NoProof)
		facts["no_proof"] = proof.NoProof
	}
	if !proof.Proof.IsZero() {
		line += " pong=" + proof.Proof.Format(time.RFC3339)
		facts["pong"] = proof.Proof
	}
	// her row as friend sync last wrote it, so her daemon reads her mode and width from its beat
	if spec, err := st.FriendSpecOf(ctx, friend); err == nil {
		mode := spec.Mode
		if mode == "" {
			mode = config.DefaultFriendMode
		}
		line += fmt.Sprintf(" row_mode=%s row_width=%d", mode, spec.Width)
		facts["row_mode"], facts["row_width"] = mode, spec.Width
		if spec.ConfigDir != "" {
			line += " row_config_dir=" + spec.ConfigDir
			facts["row_config_dir"] = spec.ConfigDir
		}
		// always, including 0: a missing word is the default cap, and 0 is none
		capN := spec.TokenCap
		if !spec.TokenCapSet {
			capN = config.DefaultFriendTokenCap
		}
		line += fmt.Sprintf(" row_token_cap=%d", capN)
		facts["row_token_cap"] = capN
	}
	for _, n := range []struct {
		key string
		v   *int
	}{{"working", rep.Working}, {"queue", rep.Queue}, {"width", rep.Width}, {"stop_returns", rep.StopReturns}} {
		if n.v != nil {
			line += fmt.Sprintf(" %s=%d", n.key, *n.v)
			facts[n.key] = *n.v
		}
	}
	if given != nil {
		line += fmt.Sprintf(" load=%.1f%%", *given)
		facts["load"] = *given
	}
	if version != "" {
		line += " daemon_version=" + oneline.Field(version)
		facts["daemon_version"] = version
	}
	if !rep.Active.IsZero() {
		line += " active=" + rep.Active.Format(time.RFC3339)
		facts["active"] = rep.Active
	}
	for _, d := range []struct {
		key string
		at  time.Time
	}{{"started", rep.Started}, {"present", rep.Present}} {
		if !d.at.IsZero() {
			line += " " + d.key + "=" + d.at.Format(time.RFC3339)
			facts[d.key] = d.at
		}
	}
	if rep.Build != "" {
		line += " build=" + rep.Build
		facts["build"] = rep.Build
	}
	if len(rep.Running) > 0 {
		line += " running=" + strings.Join(rep.Running, ",")
		facts["running"] = rep.Running
	}
	if !rep.Until.IsZero() {
		line += " down=true until=" + rep.Until.Format(time.RFC3339)
		facts["down"], facts["until"], facts["reason"] = true, rep.Until, rep.Reason
	}
	// the machine's word, so her daemon cancels its lanes on STOPPED and starts nothing
	// (docs/SPEC-SPRINT.md section 14, stop cancels jobs; internal/friend/stop.go)
	if word := machineWord(ctx, st); word != "" {
		line += " machine=" + word
		facts["machine"] = word
	}
	sayOK(stdout, c.json, name, line, facts)
	return 0
}

// cmdFriendHold is friend down (held) and friend up (the hold released, and her width set
// by --width, as fleet up --width sets a member's).
func (a *app) cmdFriendHold(held bool, args []string, stdout, stderr io.Writer) int {
	name := map[bool]string{true: "friend down", false: "friend up"}[held]
	fs, c := a.verbSetup(name)
	var width, reason, until *string
	if held {
		reason = fs.String("reason", "", "why she is held, shown on her row (her model allowance ran out)")
		until = fs.String("until", "", "when you expect her back, RFC3339, shown on her row")
	} else {
		width = fs.String("width", "", fmt.Sprintf("her width: the jobs she works at once; the deal holds her at %d times that, ready and working; 1 to %d (default: as it is; friend sync sets it to her nova-config row's again)", sprint.DealAhead, sprint.MaxWidth))
	}
	friend, code := oneFriend(name, fs, args, stderr)
	if code != 0 {
		return code
	}
	w := 0
	if width != nil && *width != "" {
		var err error
		if w, err = sprint.ParseWidth(*width); err != nil {
			return refuse(stderr, name, "--width: "+err.Error())
		}
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
	ctx := context.Background()
	if err := st.SetFriendHeld(ctx, friend, held, c.actor, why, back, w); err != nil {
		fmt.Fprintf(stderr, "%s %s: %s\n", prog, name, oneline.Escape(err.Error()))
		return 1
	}
	if held {
		// a held friend behaves as a held machine: what she has not started goes back to
		// ready for the friends' deal, what she has started stays with her and finishes
		started, err := a.friendStarted(ctx, st, friend)
		if err != nil {
			return a.readFailed(name, err, stderr)
		}
		say := "friend " + friend + " held"
		if why != "" {
			say += " reason=" + oneline.Field(why)
		}
		if !back.IsZero() {
			say += " until=" + back.UTC().Format(time.RFC3339)
		}
		c.says = []string{say} // a hold keeps no card, started or not (sprint.FriendTake)
		return a.runStep(name, *c, st, store.FriendTakeStep(sprint.FriendTakeReq{Friend: friend, All: true, Hold: true, Started: started, Who: c.actor}), stdout, stderr)
	}
	line, facts := token(name)+" OK "+friend+" held="+fmt.Sprint(held), map[string]any{"friend": friend, "held": held}
	if w > 0 {
		line += " width=" + strconv.Itoa(w)
		facts["width"] = w
	}
	sayOK(stdout, c.json, name, line, facts)
	return 0
}

// friendHealthClear is friend health --clear: the coordinator's observation of the friend
// removed (store.FriendHealthClear), so her status is her session's evidence alone; with dry
// the removal is checked and nothing is written.
func (a *app) friendHealthClear(c common, friend string, dry bool, stdout, stderr io.Writer) int {
	const name = "friend health"
	st, err := a.store(c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	prev, had, status, err := st.FriendHealthClear(context.Background(), friend, c.actor, dry, c.op)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: %s; nothing was changed\n", prog, name, oneline.Escape(err.Error()))
		return 1
	}
	was := "none"
	if had {
		was = orDashStr(prev.State, "unreadable")
	}
	if dry {
		fmt.Fprintf(stdout, "FRIEND-HEALTH DRY-RUN %s clear was=%s; nothing was changed\n", friend, was)
		return 0
	}
	sayOK(stdout, c.json, name, fmt.Sprintf("FRIEND-HEALTH OK %s cleared=true was=%s status=%s", friend, was, status),
		map[string]any{"friend": friend, "cleared": true, "was": was, "status": status})
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

// friendClass is a friend row's class: the tiers it says she can do, sorted and comma
// joined; "" when it names none.
func friendClass(r config.Row) string {
	tiers := sprint.Split(r.Fields["tiers"])
	slices.Sort(tiers)
	return strings.Join(slices.Compact(tiers), ",")
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
	dry := fs.Bool("dry-run", false, "check the observation and say what would be recorded; record nothing")
	clearObs := fs.Bool("clear", false, "remove her observation instead of recording one, so her status is her session's evidence alone (a card of hers finished, never her beat); takes no other flag but --dry-run")
	friend, code := oneFriend(name, fs, args, stderr)
	if code != 0 {
		return code
	}
	if *clearObs {
		if *state != "" || *seen != "" || *generation != 0 || *queue != 0 || *working != 0 || *width != 0 || *reason != "" || *until != "" {
			return refuse(stderr, name, "--clear removes her observation and takes no observation's flag (--state, --seen, --generation, --queue, --working, --width, --reason, --until)")
		}
		return a.friendHealthClear(*c, friend, *dry, stdout, stderr)
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
	if *dry {
		fmt.Fprintf(stdout, "FRIEND-HEALTH DRY-RUN %s state=%s seen=%s generation=%d; nothing was changed\n", friend, obs.State, obs.Seen.Format(time.RFC3339), obs.Generation)
		return 0
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

// friendRoles is the friend row's roles, sorted and comma joined: reader is the read
// cards' role (sprint read_cards.go; nova-config friend set <f> --roles builder,reader).
func friendRoles(r config.Row) string {
	words := sprint.Split(r.Fields["roles"])
	slices.Sort(words)
	return strings.Join(slices.Compact(words), ",")
}
