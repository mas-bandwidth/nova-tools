package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

func init() {
	verbClasses["handback"] = classWorker
	verbEffect["handback"] = "local write: hands the named unstarted cards back to the pool in the sprint's store and, with --root, retires each inbox brief; --dry-run writes nothing"
	verbExamples["handback"] = []string{"handback --all-unstarted --from friend.amy --reason 'eight ready cards are stranded'"}
}

// handbackWords is what handback says on -h. verbhelp.go's prose switch is
// outside this card, so -h carries the usage line, this effect, and the flags.
const handbackWords = "handback moves a dealt work card whose lane has not started back to ready in the pool: the attempt is unchanged, take_ended is not set, and the reason is logged on the card (handed_back). The deal skips that friend (taken_from) until friend give. A started lane is refused by name. --all-unstarted skips started cards and names each lane. A friend's own run omits --from. --root retires inbox/<job>/BRIEF.md once the withdraw has committed.\n"

func (a *app) cmdHandBack(args []string, stdout, stderr io.Writer) int {
	const name = "handback"
	fs, c := a.verbSetup(name)
	from := fs.String("from", "", "the friend, friend.<name> or her name; omitted when the actor is that friend")
	reason := fs.String("reason", "", "why the cards are handed back, kept on each card")
	all := fs.Bool("all-unstarted", false, "hand back every card of hers whose lane has not started")
	dry := fs.Bool("dry-run", false, "say which cards would be handed back and write nothing")
	root := fs.String("root", "", "the directory of the friends' working trees; retires each handed-back inbox brief")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	switch {
	case strings.TrimSpace(*reason) == "":
		return refuse(stderr, name, "wants --reason <text>")
	case *all == (len(pos) > 0):
		return refuse(stderr, name, "wants the cards to hand back, or --all-unstarted, and not both")
	case *from != "" && !func() bool { _, ok := sprint.HandBackFriend(*from); return ok }():
		return refuse(stderr, name, "a friend is friend.<name> or a name of letters, digits, _ and -: "+*from)
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	ctx := context.Background()
	names, err := st.FriendNames(ctx)
	if err != nil {
		return a.readFailed(name, err, stderr)
	}
	coord, err := st.B.Coordinator(ctx)
	if err != nil {
		return a.readFailed(name, err, stderr)
	}
	friendName := ""
	if *from != "" {
		friendName, _ = sprint.HandBackFriend(*from)
	}
	hers, actorIsFriend := handbackActorFriend(c.actor, names)
	switch {
	case c.actor == coord && coord != "":
		if friendName == "" {
			return refuse(stderr, name, "wants --from friend.<name>")
		}
	case actorIsFriend:
		if friendName == "" {
			friendName = hers
		}
		if friendName != hers {
			return refuse(stderr, name, "friend "+hers+" hands back only her own cards, and --from names "+friendName)
		}
	default:
		return refuse(stderr, name, "handback is the friend's or the coordinator's, and the actor is "+orDashStr(c.actor, "no one"))
	}
	if !slices.Contains(names, friendName) {
		fmt.Fprintf(stderr, "%s %s: no friend %s on the friends table (friends: %s); run: nova-sprint friend sync\n", prog, name, friendName, orDashStr(strings.Join(names, ","), "none"))
		return 1
	}
	if *dry {
		fmt.Fprintf(stdout, "HANDBACK DRY-RUN from=%s cards=%s all-unstarted=%t; nothing was changed\n", friendName, orDashStr(strings.Join(pos, ","), "-"), *all)
		return 0
	}
	lane, jobs, err := a.handbackLanes(ctx, st, friendName, *root)
	if err != nil {
		return a.readFailed(name, err, stderr)
	}
	step := store.Step{
		Verb: name, Args: store.ArgsOf(sprint.HandBackReq{From: friendName, IDs: pos, All: *all, Reason: *reason, Lane: lane, Who: c.actor}),
		Load: []string{sprint.Fleet, sprint.Work}, Mirrors: true, Named: false,
		Plan: func(s *sprint.Snapshot) sprint.Plan {
			return sprint.HandBack(s, sprint.HandBackReq{From: friendName, IDs: pos, All: *all, Reason: *reason, Lane: lane, Who: c.actor})
		},
	}
	if *root != "" {
		c.after = func(ctx context.Context, st *store.Store, res store.Result) []string {
			return handbackRetire(ctx, st, *root, friendName, jobs, res)
		}
	}
	_ = handbackWords
	return a.runStep(name, *c, st, step, stdout, stderr)
}

// handbackActorFriend is the friend the actor is, when the actor is on the roster.
func handbackActorFriend(actor string, roster []string) (string, bool) {
	if n, ok := sprint.FriendOfRow(actor); ok && sprint.ValidID(n) && slices.Contains(roster, n) {
		return n, true
	}
	if sprint.ValidID(actor) && slices.Contains(roster, actor) {
		return actor, true
	}
	return "", false
}

// handbackLanes is the lanes the caller can see have started, and the inbox
// job each card on her row was delivered as, before a hand-back bumps its generation.
func (a *app) handbackLanes(ctx context.Context, st *store.Store, friendName, root string) (map[string]string, map[string]string, error) {
	cards, err := st.ReadCells(ctx, sprint.Fleet, sprint.FriendRow(friendName), sprint.Working, sprint.Ready)
	if err != nil || len(cards) == 0 {
		return map[string]string{}, map[string]string{}, err
	}
	b, err := st.FriendBeatOf(ctx, friendName)
	if err != nil {
		return nil, nil, err
	}
	var running []string
	if b.Friend != nil {
		running = b.Friend.Running
	}
	packets, err := st.Packets(ctx, cards)
	if err != nil {
		return nil, nil, err
	}
	lane, jobs := map[string]string{}, map[string]string{}
	for _, p := range packets {
		job := friendJobOf(p)
		jobs[p.Card] = job
		if slices.Contains(running, p.Card) || slices.Contains(running, job) || slices.Contains(running, p.Primary) {
			lane[p.Card] = "friend." + friendName
		}
		if ev := handbackRootLane(root, friendName, job); ev != "" {
			lane[p.Card] = ev
		}
	}
	return lane, jobs, nil
}

// handbackRootLane is the lane evidence under --root: her jobs directory, or
// a report in her outbox. Neither is followed when it is a link.
func handbackRootLane(root, friendName, job string) string {
	if root == "" || job == "" {
		return ""
	}
	base := filepath.Join(root, friendName+"-working")
	jobs := filepath.Join(base, "jobs", job)
	if fi, err := os.Lstat(jobs); err == nil && fi.IsDir() && fi.Mode()&os.ModeSymlink == 0 {
		return "jobs/" + job
	}
	report := filepath.Join(base, "outbox", job, "REPORT.md")
	if fi, err := os.Lstat(report); err == nil && fi.Mode().IsRegular() {
		return "outbox/" + job
	}
	return ""
}

// handbackRetire moves each handed-back card's inbox brief. A card still on
// her row (the move is queued) keeps its brief for the daemon's next reconcile.
func handbackRetire(ctx context.Context, st *store.Store, root, friendName string, jobs map[string]string, res store.Result) []string {
	if res.Pending != "" {
		return []string{"inbox briefs stay until the hand-back commits"}
	}
	var notes []string
	seen := map[string]bool{}
	for _, line := range res.Moved {
		id, _, _ := strings.Cut(line, " ")
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		job := jobs[id]
		if job == "" {
			continue
		}
		info, err := st.CardOf(ctx, primaryOfWork(id))
		if err != nil {
			notes = append(notes, "inbox/"+job+" was not retired: "+err.Error())
			continue
		}
		var wc *sprint.Card
		for _, c := range info.Work {
			if c.ID == id {
				wc = c
			}
		}
		if wc == nil || wc.Col != sprint.Withdrawn || wc.F(sprint.FieldTakenFrom) != sprint.FriendRow(friendName) {
			continue
		}
		where, err := friend.RetireHandedBack(root, friendName, job, false, time.Now())
		switch {
		case err != nil:
			notes = append(notes, "inbox/"+job+" was not retired: "+err.Error())
		case where != "":
			notes = append(notes, "retired inbox/"+job+" to "+where)
		}
	}
	return notes
}

// primaryOfWork is the primary a work card id names (s1-1.w1, s1-1.w1 at a later epoch).
func primaryOfWork(id string) string {
	if i := strings.LastIndex(id, ".w"); i > 0 {
		return id[:i]
	}
	return id
}
