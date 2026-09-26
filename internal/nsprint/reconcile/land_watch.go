package reconcile

// The land watch (nova-tools #4324): merging -> landed as a duty, with the
// stuck and too-long alarms in the structure and not in the coordinator's
// attention.
//
// Glenn 2026-09-26 ~11:30 AM ET: "The core problem with merging is that it
// is so slow, and you don't watch (as coordinator) and see when something is
// stuck or taking too long, or going one at a time." ~11:40 AM: "you
// frequently get stuck on it, and I have to prod and poke you for hours".
//
// Every reconciler pass (one second, no GitHub, two pipelines):
//
//   - merging_at: the first pass that sees a task in ws:<stream>:merging
//     stamps it on the watch's own record, land:merging:<stream> (a hash,
//     id -> ms, HSETNX). The task record has one writer (the Lua move,
//     #3778); when that move writes merging_at on the task the watch reads
//     it and it wins.
//   - LAND-SLOW: a stream whose oldest merging member is past cfg:land slow
//     (seconds, default 600) prints `LAND-SLOW <stream> oldest=<id> age=<d>
//     max=<d>` when the word or the oldest member changes (not every 1 s
//     pass), keeps land:slow:<stream> (word, oldest, oldest_at, age_ms,
//     stalled) current every pass, and sends ONE wake note per episode (the
//     episode is oldest@oldest_at) to the coordinator's bus channel and the
//     declared notify channel (cfg:land notify), through friend:outbox as a
//     notice. The progress duty (#4319) reads stalled into its status; the
//     table's own LAND-SLOW line is a seam (see the PR).
//   - LAND-WALL: past cfg:land wall (default 1800) the line is LAND-WALL,
//     land:slow:<stream> stalled=1 (the stream counts as stalled), and one
//     more wake note for the episode.
//   - Merge card per stream: a stream with members in merging and no live
//     merge task gets ONE (kind merge, to the first friend advertising the
//     frontier type on its desired record, else the coordinator), its title
//     in the task grammar and its body the brief: the members in work order
//     (the ws ZSET score, #4342) with PR and merging age, the rules, the
//     stream's and the sprint's MERGE-NOTEs, at land:brief:<card> (card
//     render folds it into the friend brief; the task record has one
//     writer). The brief is refreshed while the card is open.
//     land:merge:<stream> names the card (task, to, cut_at, members; its seq
//     field survives an episode so ids never repeat). When merging empties
//     (the landing moved the members) the card fields, the brief and the
//     slow record go, and the card's claim is released.
//   - One writer per stream: land:merge:<stream> owner is the atomic claim
//     (stream.Claim, land_stream.lua) every writer of stream/<slug> takes
//     first. The watch claims card:<id> before it pushes a card; the land
//     duty claims duty:<repo>:<token> before it builds; nova-sprint land
//     claims (--card <id> for the card's child). A claim held live by
//     another is refused, so the watch cuts nothing while the duty builds
//     and the duty builds nothing while a card is open.
//   - Escalation typed, once per closed card: a merge card closed with a
//     reason naming cross-stream (the merge child could not land inside its
//     stream's PATHS) cuts one escalation to the coordinator (kind work,
//     LAND-CROSS) and the #4318 sentinel edge: the streams whose live
//     cards' PATHS hold the named paths=<files> are written as after=<slug>:
//     sentinel on land:merge:<stream>, and until each of those sentinels
//     lands only the escalation card claims the stream (no merge card, no
//     duty). A merge card closed any other way with the same members still
//     in merging cuts one escalation too (LAND-STUCK), never a new frontier
//     card every close.
//
// Nothing here sleeps; the clock is injected.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/friend"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/note"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const (
	// DefaultLandSlow is cfg:land slow when unset: the age of the oldest
	// merging member that raises LAND-SLOW.
	DefaultLandSlow = 10 * time.Minute
	// DefaultLandWall is cfg:land wall when unset: the age past which a
	// stream landing counts as stalled (LAND-WALL).
	DefaultLandWall = 30 * time.Minute
	// DefaultLandNotify is the channel the LAND-SLOW note goes to besides
	// the coordinator's own (cfg:land notify).
	DefaultLandNotify = "bus:To:glenn"
	// LandMergeKind is the merge card's task kind.
	LandMergeKind = task.KindMerge
)

// LandSlowKey is a stream's slow record; LandMergeKey names its merge card;
// LandMergingKey is the watch's first-seen stamp per merging member (id ->
// ms); LandBriefKey holds a merge card's brief.
func LandSlowKey(stream string) string    { return "land:slow:" + stream }
func LandMergeKey(s string) string        { return stream.OwnerKey(s) }
func LandMergingKey(stream string) string { return "land:merging:" + stream }
func LandBriefKey(card string) string     { return "land:brief:" + card }

// MergeMember is one member of a merge card's brief.
type MergeMember struct {
	Task      string
	PR        string
	Order     float64 // the ws:<stream>:merging score: the work order
	MergingAt time.Time
}

// MergeCard is the card the watch cuts: Kind merge for a stream's landing,
// or work for an escalation to the coordinator (Reason set; Escalate cross
// for a cross-stream end, whose After is the sentinels it waits on, or
// stuck for a card closed with the same members in merging).
type MergeCard struct {
	ID, Kind, Stream, Slug, Repo, Base, To, Sprint, Paths string
	Members                                               []MergeMember
	Notes                                                 []string
	Reason, Escalate                                      string
	After                                                 []string
	Now                                                   time.Time
}

// LandWake is the one wake note of a LAND-SLOW episode.
type LandWake struct {
	Stream, Oldest string
	Age, Max       time.Duration
	Wall           bool
	Line           string
}

// LandWatch is the duty. Its Run is a Duty.
type LandWatch struct {
	Client *redis.Client
	// Now is the clock; nil is time.Now.
	Now func() time.Time
	// Out receives the lines; nil prints nothing.
	Out io.Writer
	// Repo is the repo the merge cards land; "" reads cfg:land repos
	// (first), else DefaultLandRepo. Base is "dev" when empty.
	Repo, Base string
	// Slow and Wall override cfg:land slow and wall when set.
	Slow, Wall time.Duration
	// Push pushes a merge or escalation card and returns its id; nil is
	// task.Push into the first sprint of sprint:order.
	Push func(ctx context.Context, m MergeCard) (string, error)
	// Notify sends the wake note; nil writes friend:outbox notices.
	Notify func(ctx context.Context, w LandWake) error
	// Coordinator names the coordinator; nil reads the friends' roles.
	Coordinator func(ctx context.Context) (string, error)
	// Frontier names the friend a merge card goes to; nil picks the first
	// friend advertising frontier on its desired record that no held state
	// blocks, else the coordinator.
	Frontier func(ctx context.Context) (string, error)
}

func (w *LandWatch) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *LandWatch) printf(format string, a ...any) {
	if w.Out != nil {
		fmt.Fprintf(w.Out, format+"\n", a...)
	}
}

type watchStream struct {
	name, slug string
	members    []redis.Z
	slow       map[string]string
	merge      map[string]string
	seen       map[string]string // land:merging:<stream>: id -> first-seen ms
	notes      []string          // ws:<stream>:notes
}

// Run is one pass.
func (w *LandWatch) Run(ctx context.Context, l *Lease) (Counts, error) {
	c := w.Client
	now := w.now()
	streams, err := c.ZRange(ctx, "ws:order", 0, -1).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return Counts{}, fmt.Errorf("land watch: ws:order: %w", err)
	}
	pipe := c.Pipeline()
	zs := make([]*redis.ZSliceCmd, len(streams))
	slows := make([]*redis.MapStringStringCmd, len(streams))
	merges := make([]*redis.MapStringStringCmd, len(streams))
	seens := make([]*redis.MapStringStringCmd, len(streams))
	for i, s := range streams {
		zs[i] = pipe.ZRangeWithScores(ctx, stream.WSKey(s, "merging"), 0, -1)
		slows[i] = pipe.HGetAll(ctx, LandSlowKey(s))
		merges[i] = pipe.HGetAll(ctx, LandMergeKey(s))
		seens[i] = pipe.HGetAll(ctx, LandMergingKey(s))
	}
	cfg := pipe.HMGet(ctx, "cfg:land", "slow", "wall", "repos", "notify")
	sprints := pipe.ZRange(ctx, "sprint:order", 0, 0)
	streamNotes := make([]*redis.StringSliceCmd, len(streams))
	for i, s := range streams {
		streamNotes[i] = pipe.LRange(ctx, note.StreamKey(s), 0, -1)
	}
	if err := execPipe(ctx, pipe); err != nil {
		return Counts{}, fmt.Errorf("land watch: %w", err)
	}
	slow, wall := w.Slow, w.Wall
	cv := cfg.Val()
	if slow <= 0 {
		slow = seconds(cv[0], DefaultLandSlow)
	}
	if wall <= 0 {
		wall = seconds(cv[1], DefaultLandWall)
	}
	repo := w.Repo
	if repo == "" {
		if s, _ := cv[2].(string); strings.TrimSpace(s) != "" {
			repo = strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })[0]
		} else {
			repo = DefaultLandRepo
		}
	}
	notify, _ := cv[3].(string)
	sprint := ""
	if v := sprints.Val(); len(v) > 0 {
		sprint = v[0]
	}
	var sts []watchStream
	for i, s := range streams {
		sts = append(sts, watchStream{name: s, members: zs[i].Val(), slow: slows[i].Val(), merge: merges[i].Val(), seen: seens[i].Val(),
			notes: streamNotes[i].Val()})
	}

	// Pipeline 2: stamp the first-seen ms on the watch's record and read
	// every member's merging_at (the move's, when written), pr and paths;
	// read each merge card's state and reason, and each sentinel a
	// cross-stream end waits on.
	pipe = c.Pipeline()
	reads := map[string]*redis.SliceCmd{}
	firsts := map[string]*redis.StringCmd{}
	cards := map[string]*redis.SliceCmd{}
	stops := map[string]*redis.SliceCmd{}
	nowMS := strconv.FormatInt(now.UnixMilli(), 10)
	for _, st := range sts {
		for _, z := range st.members {
			id := fmt.Sprint(z.Member)
			pipe.HSetNX(ctx, LandMergingKey(st.name), id, nowMS)
			firsts[id] = pipe.HGet(ctx, LandMergingKey(st.name), id)
			reads[id] = pipe.HMGet(ctx, "task:"+id, "merging_at", "pr", "paths")
		}
		if mid := st.merge["task"]; mid != "" {
			cards[mid] = pipe.HMGet(ctx, "task:"+mid, "state", "reason", "dest")
		}
		if esc := st.merge["escalation"]; esc != "" {
			cards[esc] = pipe.HMGet(ctx, "task:"+esc, "state", "reason", "dest")
		}
		for _, sid := range strings.Fields(st.merge["after"]) {
			stops[sid] = pipe.HMGet(ctx, "task:"+sid, "state", "where")
		}
	}
	var sprintNotes *redis.StringSliceCmd
	if sprint != "" {
		sprintNotes = pipe.LRange(ctx, note.SprintKey(sprint), 0, -1)
	}
	if err := execPipe(ctx, pipe); err != nil {
		return Counts{}, fmt.Errorf("land watch: %w", err)
	}
	var sprintNoteLines []string
	if sprintNotes != nil {
		sprintNoteLines = sprintNotes.Val()
	}

	pipe = c.Pipeline()
	var errs []string
	for _, st := range sts {
		if len(st.members) == 0 {
			if len(st.slow) > 0 {
				pipe.Del(ctx, LandSlowKey(st.name))
			}
			if len(st.merge) > 0 {
				// The card fields go; seq stays so the next episode's id is
				// new (task.Push answers CONFLICT or CLOSED to a repeated id).
				pipe.HDel(ctx, LandMergeKey(st.name), "task", "to", "cut_at", "members", "escalated", "escalation", "after")
				if mid := st.merge["task"]; mid != "" {
					pipe.Del(ctx, LandBriefKey(mid))
				}
				// A card's claim goes with its episode (compare and delete:
				// a claim another writer took since is kept).
				if o := st.merge["owner"]; strings.HasPrefix(o, "card:") {
					if _, err := stream.Release(ctx, c, []string{st.name}, o); err != nil {
						errs = append(errs, st.name+": release: "+err.Error())
					}
				}
			}
			if len(st.seen) > 0 {
				pipe.Del(ctx, LandMergingKey(st.name))
			}
			continue
		}
		slug, serr := stream.Slug(st.name)
		if serr != nil {
			errs = append(errs, st.name+": "+serr.Error())
			continue
		}
		st.slug = slug
		members := make([]MergeMember, 0, len(st.members))
		var paths []string
		seenPath := map[string]bool{}
		inMerging := map[string]bool{}
		for _, z := range st.members {
			id := fmt.Sprint(z.Member)
			inMerging[id] = true
			v := reads[id].Val()
			m := MergeMember{Task: id, Order: z.Score}
			// The move's stamp when it wrote one, else the watch's first
			// sight of the member in merging.
			at := firsts[id].Val()
			if s, ok := v[0].(string); ok && strings.TrimSpace(s) != "" {
				at = s
			}
			if ms, err := strconv.ParseInt(strings.TrimSpace(at), 10, 64); err == nil {
				m.MergingAt = time.UnixMilli(ms)
			} else {
				m.MergingAt = now
			}
			if s, ok := v[1].(string); ok {
				m.PR = s
			}
			if s, ok := v[2].(string); ok {
				for _, p := range strings.Fields(s) {
					if !seenPath[p] {
						seenPath[p] = true
						paths = append(paths, p)
					}
				}
			}
			members = append(members, m)
		}
		for id := range st.seen {
			if !inMerging[id] {
				pipe.HDel(ctx, LandMergingKey(st.name), id) // it left merging
			}
		}
		// The oldest by merging_at, the work order breaking ties.
		oldest := members[0]
		for _, m := range members[1:] {
			if m.MergingAt.Before(oldest.MergingAt) {
				oldest = m
			}
		}
		age := now.Sub(oldest.MergingAt)
		w.watchSlow(ctx, pipe, st, oldest, age, slow, wall, notify, &errs)
		if err := w.watchCard(ctx, pipe, st, cards, stops, repo, sprint, members, paths, sprintNoteLines, now); err != nil {
			errs = append(errs, st.name+": "+err.Error())
		}
	}
	if err := execPipe(ctx, pipe); err != nil {
		return Counts{}, fmt.Errorf("land watch: %w", err)
	}
	if len(errs) > 0 {
		return Counts{}, errors.New("land watch: " + strings.Join(errs, "; "))
	}
	return Counts{}, nil
}

// watchSlow is one stream's LAND-SLOW / LAND-WALL: the line, the record,
// and one note per episode.
func (w *LandWatch) watchSlow(ctx context.Context, pipe redis.Pipeliner, st watchStream, oldest MergeMember, age, slow, wall time.Duration, notify string, errs *[]string) {
	if age < slow {
		if len(st.slow) > 0 {
			pipe.Del(ctx, LandSlowKey(st.name))
		}
		return
	}
	word, max, stalled := "LAND-SLOW", slow, "0"
	if age >= wall {
		word, max, stalled = "LAND-WALL", wall, "1"
	}
	line := fmt.Sprintf("%s %s oldest=%s age=%s max=%s", word, oneline.Field(st.name), oldest.Task, age.Truncate(time.Second), max.Truncate(time.Second))
	// The line prints on change (the word or the oldest member), never
	// every one-second pass; the record moves every pass.
	if st.slow["word"] != word || st.slow["oldest"] != oldest.Task {
		w.printf("%s", line)
	}
	episode := fmt.Sprintf("%s@%d", oldest.Task, oldest.MergingAt.UnixMilli())
	pipe.HSet(ctx, LandSlowKey(st.name), "word", word, "oldest", oldest.Task, "oldest_at", strconv.FormatInt(oldest.MergingAt.UnixMilli(), 10),
		"age_ms", strconv.FormatInt(age.Milliseconds(), 10), "stalled", stalled, "at", strconv.FormatInt(w.now().UnixMilli(), 10))
	// One note per episode for the slow word and one more for the wall,
	// each keyed on the episode so a restart never sends it twice.
	notedField := "noted"
	if stalled == "1" {
		notedField = "noted_wall"
	}
	if st.slow[notedField] == episode {
		return
	}
	wake := LandWake{Stream: st.name, Oldest: oldest.Task, Age: age, Max: max, Wall: stalled == "1", Line: line}
	err := w.notify(ctx, wake, notify)
	if err != nil {
		// A note that did not go is on the pass line; the next pass sends
		// it again.
		*errs = append(*errs, fmt.Sprintf("%s note: %v", st.name, err))
		return
	}
	pipe.HSet(ctx, LandSlowKey(st.name), notedField, episode)
	w.printf("LAND-NOTE %s %s oldest=%s age=%s sent=1", word, oneline.Field(st.name), oldest.Task, age.Truncate(time.Second))
}

func (w *LandWatch) notify(ctx context.Context, wake LandWake, notify string) error {
	if w.Notify != nil {
		return w.Notify(ctx, wake)
	}
	coord, err := w.coordinator(ctx)
	if err != nil {
		return err
	}
	if strings.TrimSpace(notify) == "" {
		notify = DefaultLandNotify
	}
	at := strconv.FormatInt(w.now().UnixMilli(), 10)
	idem := fmt.Sprintf("land-slow:%s:%s:%d", wake.Stream, wake.Oldest, wake.Age.Milliseconds())
	pipe := w.Client.Pipeline()
	channels := []string{notify}
	if coord != "" {
		channels = append(channels, "bus:To:"+coord)
	}
	for _, ch := range channels {
		pipe.XAdd(ctx, &redis.XAddArgs{Stream: friend.OutboxKey, MaxLen: 100000, Approx: true, Values: map[string]any{
			"friend": coord, "kind": "notice", "rung": "0", "channel": ch, "open": "1", "detail": wake.Line,
			"actor": LandActor, "idem": idem + ":" + ch, "at": at}})
	}
	return execPipe(ctx, pipe)
}

func (w *LandWatch) coordinator(ctx context.Context) (string, error) {
	if w.Coordinator != nil {
		return w.Coordinator(ctx)
	}
	return (&RouteDuty{Client: w.Client}).coordinator(ctx)
}

// heldStates block new work (friend.go): no merge card goes to such a friend.
var heldStates = map[string]bool{friend.StateDown: true, friend.StateAway: true, friend.StateOutOfCredits: true,
	friend.StateOfflineModel: true, friend.StateWakeMissed: true}

// frontier is the friend a merge card goes to.
func (w *LandWatch) frontier(ctx context.Context) (string, error) {
	if w.Frontier != nil {
		return w.Frontier(ctx)
	}
	names, err := w.Client.SMembers(ctx, "friends").Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return "", err
	}
	sort.Strings(names)
	pipe := w.Client.Pipeline()
	tiers := make([]*redis.StringCmd, len(names))
	states := make([]*redis.StringCmd, len(names))
	for i, f := range names {
		tiers[i] = pipe.HGet(ctx, "friend:"+f+":desired", "tiers")
		states[i] = pipe.Get(ctx, friend.StateKey(f))
	}
	if err := execPipe(ctx, pipe); err != nil {
		return "", err
	}
	for i, f := range names {
		if heldStates[states[i].Val()] {
			continue
		}
		for _, t := range strings.Split(tiers[i].Val(), ",") {
			if strings.TrimSpace(t) == "frontier" {
				return f, nil
			}
		}
	}
	return w.coordinator(ctx)
}

// DefaultCardGrace is how long a card's claim is live before its task
// record exists (the push that follows the claim).
const DefaultCardGrace = time.Minute

// watchCard keeps one live merge card per stream with members in merging.
// A card that closed escalates once to the coordinator when it ended
// cross-stream (with the sentinel edge) or left the same members in
// merging; otherwise the next card is cut. Every cut claims the stream
// first: a live claim of another writer (the land duty building, a hand
// run) cuts nothing this pass.
func (w *LandWatch) watchCard(ctx context.Context, pipe redis.Pipeliner, st watchStream, cards, stops map[string]*redis.SliceCmd, repo, sprint string, members []MergeMember, paths []string, sprintNotes []string, now time.Time) error {
	mid := st.merge["task"]
	state, reason := "", ""
	if mid != "" {
		if cmd := cards[mid]; cmd != nil {
			v := cmd.Val()
			state, _ = v[0].(string)
			reason, _ = v[1].(string)
		}
	}
	notes := append(append([]string(nil), st.notes...), sprintNotes...)
	base := w.Base
	if base == "" {
		base = "dev"
	}
	card := MergeCard{Kind: string(LandMergeKind), Stream: st.name, Slug: st.slug, Repo: repo, Base: base, Sprint: sprint,
		Paths: strings.Join(paths, " "), Members: members, Notes: notes, Now: now}
	if mid != "" && state != "" && state != "closed" {
		// The brief follows the stream while the card waits for its friend.
		if state != "working" {
			card.ID = mid
			pipe.Set(ctx, LandBriefKey(mid), MergeBrief(card), 0)
		}
		return nil
	}
	if mid != "" && state == "closed" && st.merge["escalated"] != mid {
		why := ""
		switch {
		case strings.Contains(reason, "cross-stream"):
			why = "cross"
		case sameMembers(st.merge["members"], members):
			why = "stuck"
		}
		if why != "" {
			return w.escalate(ctx, pipe, st, card, mid, reason, why, now)
		}
	}
	if esc := st.merge["escalation"]; esc != "" && st.merge["escalated"] == mid {
		// The coordinator holds the escalation: no new merge card until it
		// closes, and after a cross-stream end until every sentinel it
		// waits on has landed (the claim refuses the same).
		if cmd := cards[esc]; cmd != nil {
			if es, _ := cmd.Val()[0].(string); es != "" && es != "closed" {
				return nil
			}
		}
		for _, sid := range strings.Fields(st.merge["after"]) {
			if !stopLanded(stops[sid]) {
				return nil
			}
		}
	}
	// No live card: claim the stream for the next one, then cut it.
	to, err := w.frontier(ctx)
	if err != nil {
		return err
	}
	if to == "" {
		return errors.New("merge card: no friend advertises frontier and there is no coordinator")
	}
	seq, _ := strconv.ParseInt(st.merge["seq"], 10, 64)
	seq++
	card.ID = fmt.Sprintf("merge-%s-%d", st.slug, seq)
	card.To = to
	if held, err := w.claim(ctx, st.name, card.ID, now); err != nil || held {
		return err
	}
	if mid != "" {
		pipe.Del(ctx, LandBriefKey(mid)) // the closed card's brief
	}
	id, err := w.push(ctx, card)
	if err != nil {
		return fmt.Errorf("merge card %s: %w", card.ID, err)
	}
	pipe.HSet(ctx, LandMergeKey(st.name), "seq", strconv.FormatInt(seq, 10), "task", id, "to", to,
		"cut_at", strconv.FormatInt(now.UnixMilli(), 10), "members", memberIDs(members))
	pipe.Set(ctx, LandBriefKey(id), MergeBrief(card), 0)
	w.printf("MERGE-CARD %s card=%s to=%s members=%d", oneline.Field(st.name), id, to, len(members))
	return nil
}

// escalate cuts the one escalation of a closed card to the coordinator:
// LAND-CROSS with the sentinel edge (after=) for a cross-stream end,
// LAND-STUCK for a card that closed with the same members in merging.
func (w *LandWatch) escalate(ctx context.Context, pipe redis.Pipeliner, st watchStream, card MergeCard, mid, reason, why string, now time.Time) error {
	to, err := w.coordinator(ctx)
	if err != nil {
		return err
	}
	if to == "" {
		return errors.New(why + ": no coordinator to escalate to")
	}
	esc := card
	esc.Kind = string(task.KindWork)
	esc.To, esc.Reason, esc.Escalate = to, reason, why
	esc.ID = why + "-" + strings.TrimPrefix(mid, "merge-")
	if why == "cross" {
		if esc.After, err = w.afterStops(ctx, st.name, crossPaths(reason)); err != nil {
			return err
		}
	}
	if held, err := w.claim(ctx, st.name, esc.ID, now); err != nil || held {
		return err
	}
	id, err := w.push(ctx, esc)
	if err != nil {
		return fmt.Errorf("escalate %s: %w", mid, err)
	}
	pipe.HSet(ctx, LandMergeKey(st.name), "escalated", mid, "escalation", id, "after", strings.Join(esc.After, " "))
	word := "LAND-CROSS"
	if why == "stuck" {
		word = "LAND-STUCK"
	}
	w.printf("%s %s card=%s escalation=%s to=%s after=%s reason=%s", word, oneline.Field(st.name), mid, id, to,
		orDashStr(strings.Join(esc.After, ",")), oneline.Field(orDashStr(reason)))
	return nil
}

// claim takes the stream for card id; held is true (and nothing is cut)
// when another writer's claim is live.
func (w *LandWatch) claim(ctx context.Context, s, id string, now time.Time) (held bool, err error) {
	err = stream.Claim(ctx, w.Client, []string{s}, stream.CardOwner(id), now, now.Add(DefaultCardGrace))
	var owned *stream.OwnedError
	if errors.As(err, &owned) {
		return true, nil
	}
	return false, err
}

// sameMembers is whether the closed card's members (land:merge members,
// space-joined) are the members in merging now.
func sameMembers(was string, now []MergeMember) bool {
	old := strings.Fields(was)
	if len(old) == 0 || len(old) != len(now) {
		return false
	}
	in := map[string]bool{}
	for _, id := range old {
		in[id] = true
	}
	for _, m := range now {
		if !in[m.Task] {
			return false
		}
	}
	return true
}

func memberIDs(members []MergeMember) string {
	ids := make([]string, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.Task)
	}
	return strings.Join(ids, " ")
}

func stopLanded(cmd *redis.SliceCmd) bool {
	if cmd == nil {
		return false
	}
	v := cmd.Val()
	st, _ := v[0].(string)
	where, _ := v[1].(string)
	return st == "landed" || where == "landed"
}

// crossPaths is the files a cross-stream end names: paths=<a>,<b> (commas
// or spaces) in the card's reason.
func crossPaths(reason string) []string {
	_, rest, ok := strings.Cut(reason, "paths=")
	if !ok {
		return nil
	}
	var out []string
	for _, f := range strings.FieldsFunc(rest, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
		if strings.Contains(f, "=") {
			break // the next key=value of the reason
		}
		out = append(out, f)
	}
	return out
}

// liveWheres are the ws sets of a live card.
var liveWheres = []string{"waiting", "ready", "working", "review", "merging"}

// afterStops is the sentinel ids (<slug>:sentinel, nova-tools #4318) of the
// other streams whose live cards' PATHS hold one of files: the streams a
// cross-stream end waits on. Two round trips, once per escalation.
func (w *LandWatch) afterStops(ctx context.Context, self string, files []string) ([]string, error) {
	if len(files) == 0 {
		return nil, nil
	}
	c := w.Client
	streams, err := c.ZRange(ctx, "ws:order", 0, -1).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	pipe := c.Pipeline()
	type set struct {
		stream string
		cmd    *redis.StringSliceCmd
	}
	var sets []set
	for _, s := range streams {
		if s == self {
			continue
		}
		for _, wh := range liveWheres {
			sets = append(sets, set{s, pipe.ZRange(ctx, stream.WSKey(s, wh), 0, -1)})
		}
	}
	if len(sets) == 0 {
		return nil, nil
	}
	if err := execPipe(ctx, pipe); err != nil {
		return nil, err
	}
	pipe = c.Pipeline()
	type card struct {
		stream string
		paths  *redis.StringCmd
	}
	var cs []card
	for _, st := range sets {
		for _, id := range st.cmd.Val() {
			cs = append(cs, card{st.stream, pipe.HGet(ctx, "task:"+id, "paths")})
		}
	}
	if len(cs) == 0 {
		return nil, nil
	}
	if err := execPipe(ctx, pipe); err != nil {
		return nil, err
	}
	var out []string
	seen := map[string]bool{}
	for _, cd := range cs {
		if seen[cd.stream] || !pathsHold(strings.Fields(cd.paths.Val()), files) {
			continue
		}
		seen[cd.stream] = true
		if sid := ws.SentinelID(cd.stream); sid != "" {
			out = append(out, sid)
		}
	}
	sort.Strings(out)
	return out, nil
}

// pathsHold is whether a PATHS list holds one of files: the path itself, a
// directory above it, or a glob matching it.
func pathsHold(paths, files []string) bool {
	for _, p := range paths {
		dir := strings.TrimSuffix(p, "/") + "/"
		for _, f := range files {
			if f == p || strings.HasPrefix(f, dir) {
				return true
			}
			if ok, _ := path.Match(p, f); ok {
				return true
			}
		}
	}
	return false
}

func (w *LandWatch) push(ctx context.Context, m MergeCard) (string, error) {
	if w.Push != nil {
		return w.Push(ctx, m)
	}
	return pushMergeCard(ctx, w.Client, m)
}

// MergeTitle is the card's title in the task grammar: the stream, the work,
// PATHS, BASE and the DONE-WHEN the friend's brief renders.
func MergeTitle(m MergeCard) string {
	paths := m.Paths
	if paths == "" {
		paths = "-"
	}
	land := fmt.Sprintf("nova-sprint land --repo %s --stream %q --card %s", m.Repo, m.Stream, m.ID)
	if m.Escalate == "stuck" {
		return fmt.Sprintf("STREAM: %s | stuck landing of %s: its merge card closed with the same %d members in merging (%s) | PATHS: %s | BASE: %s | DONE-WHEN: %s prints LANDED n=%d and ws:%s:merging is empty",
			m.Stream, m.Stream, len(m.Members), strings.Join(strings.Fields(orDashStr(m.Reason)), " "), paths, m.Base, land, len(m.Members), m.Stream)
	}
	if m.Reason != "" {
		after := "none found"
		if len(m.After) > 0 {
			after = strings.Join(m.After, ", ")
		}
		return fmt.Sprintf("STREAM: %s | cross-stream landing of %s: %s | PATHS: %s | BASE: %s | AFTER: %s (the stream lands after these sentinels, #4318; only this card may land it before) | DONE-WHEN: one %s that builds and passes with every stream's DONE-WHEN; the stream heads are re-based on it and %s lands (%s prints LANDED)",
			m.Stream, m.Stream, strings.Join(strings.Fields(m.Reason), " "), paths, m.Base, after, m.Base, m.Stream, land)
	}
	return fmt.Sprintf("STREAM: %s | land stream %s: %d members in work order into %s as one PR | PATHS: %s | BASE: %s | DONE-WHEN: %s prints LANDED n=%d and ws:%s:merging is empty; a conflict or red whose fix needs files outside PATHS ends BLOCKED cross-stream paths=<files>",
		m.Stream, m.Stream, len(m.Members), m.Base, paths, m.Base, land, len(m.Members), m.Stream)
}

// MergeBrief is the card's body: the members in work order with PR and
// merging age, the rules, and the current MERGE-NOTEs.
func MergeBrief(m MergeCard) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Merge card for stream %s: land its %d merging members into %s as ONE pull request from %s.\n\n",
		m.Stream, len(m.Members), m.Base, stream.DefaultBranch(m.Slug, 1))
	b.WriteString("Members in work order (the ws:<stream>:merging score; the lander merges in this order and no other):\n")
	for i, mm := range m.Members {
		pr := mm.PR
		if pr == "" {
			pr = "no-pr"
		}
		age := "-"
		if !mm.MergingAt.IsZero() && !m.Now.IsZero() {
			age = m.Now.Sub(mm.MergingAt).Truncate(time.Second).String()
		}
		fmt.Fprintf(&b, "  %d. %s pr=%s order=%.0f merging_for=%s\n", i+1, mm.Task, pr, mm.Order, age)
	}
	b.WriteString("\nRules (all hard; nova-tools #4324):\n")
	b.WriteString("- One branch per work stream, members in work order, ONE PR into " + m.Base + ", never one member at a time and never a member merged by hand: `gh pr merge` on a member is refused; members land only through nova-sprint land stream.\n")
	fmt.Fprintf(&b, "- First move, always: `nova-sprint land stream --repo %s --stream %q --dry-run` prints the plan (PLAN and ORDER lines). Then `nova-sprint land --repo %s --stream %q --card %s` runs the whole landing and prints one line per step with its wall (REBASED, PUSHED, PR opened, CI, MERGED, LANDED). This card owns the stream (land:merge:<stream> owner): the land duty and every other run are refused while it is open, and a run without --card %s is refused too.\n", m.Repo, m.Stream, m.Repo, m.Stream, m.ID, m.ID)
	b.WriteString("- LAND-SERIAL is a refusal: a member that cannot land (unread, held, no PR) is read, parked or released first; --partial only when the coordinator says so.\n")
	b.WriteString("- Never behind " + m.Base + ": a conflict inside PATHS is resolved on the stream branch keeping both sides' intent. A conflict or red whose fix needs files outside PATHS ends this card BLOCKED cross-stream paths=<files>: the coordinator takes it from there.\n")
	b.WriteString("- End with MERGE-NOTE lines for what the tree now expects (`nova-sprint note post --stream <s> --by <this card> \"<one line>\"`): a conflict you resolved, a wrong pattern you saw, an API that moved. They reach every copy dealt after you.\n")
	if len(m.Notes) > 0 {
		b.WriteString("\nMERGE-NOTES now (this stream's, then the sprint's):\n")
		for _, n := range m.Notes {
			b.WriteString("  " + n + "\n")
		}
	}
	return b.String()
}

// pushMergeCard is the default Push: task.Push into the first sprint of
// sprint:order, at the front of the friend's queue.
func pushMergeCard(ctx context.Context, c *redis.Client, m MergeCard) (string, error) {
	sprint := m.Sprint
	if sprint == "" {
		sprints, err := c.ZRange(ctx, "sprint:order", 0, 0).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return "", err
		}
		if len(sprints) == 0 {
			return "", task.ErrNoSprint
		}
		sprint = sprints[0]
	}
	kind := task.Kind(m.Kind)
	if kind == "" {
		kind = LandMergeKind
	}
	res, err := task.Push(ctx, store.New(c), task.PushRequest{
		Sprint: sprint, ID: m.ID, Kind: kind, Title: MergeTitle(m), Effects: task.EffectsExternal,
		Repo: m.Repo, Ref: m.Base, To: m.To, Front: true, Actor: LandActor, ErrOut: io.Discard,
	})
	if err != nil {
		return "", err
	}
	switch res {
	case task.PushCreated, task.PushExists:
		return m.ID, nil
	}
	return "", fmt.Errorf("task push %s: %s", m.ID, res)
}

// seconds reads a cfg:land seconds value; def when unset or not a number.
func seconds(v any, def time.Duration) time.Duration {
	s, _ := v.(string)
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return def
	}
	return time.Duration(n) * time.Second
}

// execPipe runs a pipeline; a Nil reply inside it is not an error.
func execPipe(ctx context.Context, pipe redis.Pipeliner) error {
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return err
	}
	return nil
}
