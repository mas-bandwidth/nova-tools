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
// Every reconciler pass (one second, no GitHub, one call: ns_land_watch_pass
// reads, stamps, cleans and keeps the slow record; a second trip only for a
// note or a card, nova-tools #4449; the model is rowan-new
// specs/tla/LandWatch.tla, findings L1 and L2):
//
//   - merging_at: the first pass that sees a task in ws:<stream>:merging
//     stamps it on the watch's own record, land:merging:<stream> (a hash,
//     id -> ms, HSETNX). The task record has one writer (the Lua move,
//     #3778); when that move writes merging_at on the task the watch reads
//     it and it wins. The note's episode is the member's stay, keyed on
//     the watch's own first sight (L1: the move's stamp can arrive later
//     and older); the words noted follow the member in land:noted:<stream>
//     (L2: one record per stream re-noted when the oldest changed and
//     changed back).
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

	"github.com/mas-bandwidth/nova-tools/internal/civerdict"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/friend"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
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
// ms); LandNotedKey is the words noted per member for its stay (id ->
// LAND-SLOW[,LAND-WALL]).
func LandSlowKey(stream string) string    { return "land:slow:" + stream }
func LandMergeKey(s string) string        { return stream.OwnerKey(s) }
func LandMergingKey(stream string) string { return "land:merging:" + stream }
func LandNotedKey(stream string) string   { return "land:noted:" + stream }
func LandGenKey(stream string) string     { return "land:gen:" + stream } // the move's generation the watch saw with its first sight: a generation it has not seen is a new stay

// MergeMember is one member of a merge card's brief.
type MergeMember struct {
	Task      string
	PR        string
	Head      string
	CI        string
	Behind    string
	Paths     string  // the record's PATHS
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
	members    []MergeMember
	merge      map[string]string // land:merge:<stream>
	cardState  string            // the merge card's state, reason, dest (when named)
	cardReason string
	escState   string          // the escalation's state (when named)
	stops      map[string]bool // after= sentinel -> landed
	notes      []string        // ws:<stream>:notes
	slow       *slowRow        // the record the function kept, when the oldest is past slow
}

// slowRow is the function's slow row: the word and the oldest, whether
// either changed this pass, and whether a note is due for the oldest and
// the word in this stay.
type slowRow struct {
	word, oldest  string
	at            time.Time
	age, max      time.Duration
	changed, note bool
	noted         string // the words already noted for the oldest
}

// landCfg is cfg:land as the function resolved it.
type landCfg struct {
	slow, wall    time.Duration
	repos, notify string
}

// Run is one pass: one call of ns_land_watch_pass (the read, the stamps,
// the empty episode's cleanup, the slow record), then the lines, the note
// when one is due, and the cards: the rare work, on a pipeline that is
// empty on the steady tick, so no second trip.
func (w *LandWatch) Run(ctx context.Context, l *Lease) (Counts, error) {
	c := w.Client
	now := w.now()
	sts, cfg, sprint, sprintNoteLines, err := landWatchPass(ctx, c, now, w.Slow, w.Wall)
	if err != nil {
		return Counts{}, fmt.Errorf("land watch: %w", err)
	}
	repo := w.Repo
	if repo == "" {
		if s := strings.TrimSpace(cfg.repos); s != "" {
			repo = strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })[0]
		} else {
			repo = DefaultLandRepo
		}
	}
	pipe := c.Pipeline()
	var errs []string
	for _, st := range sts {
		if len(st.members) == 0 {
			continue // the function cleaned the episode's records
		}
		slug, serr := stream.Slug(st.name)
		if serr != nil {
			errs = append(errs, st.name+": "+serr.Error())
			continue
		}
		st.slug = slug
		var paths []string
		seenPath := map[string]bool{}
		for _, m := range st.members {
			for _, p := range strings.Fields(m.Paths) {
				if !seenPath[p] {
					seenPath[p] = true
					paths = append(paths, p)
				}
			}
		}
		w.watchSlow(ctx, pipe, st, cfg.notify, &errs)
		if err := w.watchCard(ctx, pipe, st, repo, sprint, paths, sprintNoteLines, now); err != nil {
			errs = append(errs, st.name+": "+err.Error())
		}
	}
	// (the steady tick queues nothing here, and an empty pipeline is no trip)
	if err := execPipe(ctx, pipe); err != nil {
		return Counts{}, fmt.Errorf("land watch: %w", err)
	}
	if len(errs) > 0 {
		return Counts{}, errors.New("land watch: " + strings.Join(errs, "; "))
	}
	return Counts{}, nil
}

// watchSlow is one stream's LAND-SLOW / LAND-WALL: the line when the word
// or the oldest changed (never every one-second pass), and the note once
// per member and word for one stay: the function says note=1 until the
// word is marked noted here, after the note went; a note that did not go
// is on the pass line and the next pass sends it again.
func (w *LandWatch) watchSlow(ctx context.Context, pipe redis.Pipeliner, st watchStream, notify string, errs *[]string) {
	s := st.slow
	if s == nil {
		return
	}
	line := fmt.Sprintf("%s %s oldest=%s age=%s max=%s", s.word, oneline.Field(st.name), s.oldest, s.age.Truncate(time.Second), s.max.Truncate(time.Second))
	if s.changed {
		w.printf("%s", line)
	}
	if !s.note {
		return
	}
	wake := LandWake{Stream: st.name, Oldest: s.oldest, Age: s.age, Max: s.max, Wall: s.word == "LAND-WALL", Line: line}
	if err := w.notify(ctx, wake, notify); err != nil {
		*errs = append(*errs, fmt.Sprintf("%s note: %v", st.name, err))
		return
	}
	words := s.word
	if s.noted != "" {
		words = s.noted + "," + s.word
	}
	pipe.HSet(ctx, LandNotedKey(st.name), s.oldest, words)
	w.printf("LAND-NOTE %s %s oldest=%s age=%s sent=1", s.word, oneline.Field(st.name), s.oldest, s.age.Truncate(time.Second))
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
func (w *LandWatch) watchCard(ctx context.Context, pipe redis.Pipeliner, st watchStream, repo, sprint string, paths []string, sprintNotes []string, now time.Time) error {
	mid := st.merge["task"]
	state, reason := "", ""
	if mid != "" {
		state, reason = st.cardState, st.cardReason
	}
	members := st.members
	notes := append(append([]string(nil), st.notes...), sprintNotes...)
	base := w.Base
	if base == "" {
		base = "dev"
	}
	card := MergeCard{Kind: string(LandMergeKind), Stream: st.name, Slug: st.slug, Repo: repo, Base: base, Sprint: sprint,
		Paths: strings.Join(paths, " "), Members: members, Notes: notes, Now: now}
	if mid != "" && state != "" && state != "closed" {
		// A live card: nothing to cut. Its brief is a view (MergeBriefFor),
		// rendered when the card is read, so the steady tick writes nothing.
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
		if st.escState != "" && st.escState != "closed" {
			return nil
		}
		for _, sid := range strings.Fields(st.merge["after"]) {
			if !st.stops[sid] {
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
	id, err := w.push(ctx, card)
	if err != nil {
		return fmt.Errorf("merge card %s: %w", card.ID, err)
	}
	pipe.HSet(ctx, LandMergeKey(st.name), "seq", strconv.FormatInt(seq, 10), "task", id, "to", to,
		"cut_at", strconv.FormatInt(now.UnixMilli(), 10), "members", memberIDs(members))
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
	streams, epoch, err := orderAndEpoch(ctx, c)
	if err != nil {
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
			sets = append(sets, set{s, pipe.ZRange(ctx, stream.WSKeyAt(epoch, s, wh), 0, -1)})
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
		return fmt.Sprintf("STREAM: %s | stuck landing of %s: its merge card closed with the same %d members in merging (%s) | PATHS: %s | BASE: %s | DONE-WHEN: %s prints LANDED n=%d and the stream's merging set is empty",
			m.Stream, m.Stream, len(m.Members), strings.Join(strings.Fields(orDashStr(m.Reason)), " "), paths, m.Base, land, len(m.Members))
	}
	if m.Reason != "" {
		after := "none found"
		if len(m.After) > 0 {
			after = strings.Join(m.After, ", ")
		}
		return fmt.Sprintf("STREAM: %s | cross-stream landing of %s: %s | PATHS: %s | BASE: %s | AFTER: %s (the stream lands after these sentinels, #4318; only this card may land it before) | DONE-WHEN: one %s that builds and passes with every stream's DONE-WHEN; the stream heads are re-based on it and %s lands (%s prints LANDED)",
			m.Stream, m.Stream, strings.Join(strings.Fields(m.Reason), " "), paths, m.Base, after, m.Base, m.Stream, land)
	}
	return fmt.Sprintf("STREAM: %s | land stream %s: %d members in work order into %s as one PR | PATHS: %s | BASE: %s | DONE-WHEN: %s prints LANDED n=%d and the stream's merging set is empty; a conflict or red whose fix needs files outside PATHS ends BLOCKED cross-stream paths=<files>",
		m.Stream, m.Stream, len(m.Members), m.Base, paths, m.Base, land, len(m.Members))
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
		head := short8(mm.Head)
		if head == "" {
			head = "-"
		}
		ci := mm.CI
		if ci == "" {
			ci = "-"
		}
		behind := mm.Behind
		if behind == "" {
			behind = "-"
		}
		age := "-"
		if !mm.MergingAt.IsZero() && !m.Now.IsZero() {
			age = m.Now.Sub(mm.MergingAt).Truncate(time.Second).String()
		}
		fmt.Fprintf(&b, "  %d. %s pr=%s head=%s ci=%s behind=%s order=%.0f merging_for=%s\n", i+1, mm.Task, pr, head, ci, behind, mm.Order, age)
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
// orderAndEpoch reads ws:order and the sprint epoch (nova-tools#4238) in one
// round trip: the watch's stream sets are keyed by the epoch it read.
// orderAndEpoch reads ws:order and the sprint epoch in one round (the
// cross-stream stops read after a landing, afterStops; the pass itself reads
// through landWatchRead).
func orderAndEpoch(ctx context.Context, c redis.Cmdable) ([]string, uint64, error) {
	pipe := c.Pipeline()
	order := pipe.ZRange(ctx, "ws:order", 0, -1)
	epochCmd := pipe.HGet(ctx, ws.EpochKey, ws.EpochField)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, 0, fmt.Errorf("ws:order: %w", err)
	}
	epoch, err := ws.ParseEpoch(epochCmd.Val())
	if err != nil {
		return nil, 0, err
	}
	return order.Val(), epoch, nil
}

// landWatchPass is the pass's one call, ns_land_watch_pass: every stream of
// ws:order with its merging members (each with its at, pr and paths), its
// merge record, the card, escalation and stop states it names, its notes,
// and the slow row the function kept; cfg:land as resolved (slow and wall
// overridden by the watch's own when set); the first sprint of sprint:order
// and its notes.
func landWatchPass(ctx context.Context, c redis.Cmdable, now time.Time, slow, wall time.Duration) (sts []watchStream, cfg landCfg, sprint string, sprintNotes []string, err error) {
	args := []any{strconv.FormatInt(now.UnixMilli(), 10), "", ""}
	if slow > 0 {
		args[1] = strconv.FormatInt(slow.Milliseconds(), 10)
	}
	if wall > 0 {
		args[2] = strconv.FormatInt(wall.Milliseconds(), 10)
	}
	reply, err := c.FCall(ctx, "ns_land_watch_pass", nil, args...).StringSlice()
	if err != nil {
		return nil, cfg, "", nil, fmt.Errorf("ns_land_watch_pass: %w", err)
	}
	r := &rows{name: "ns_land_watch_pass", reply: reply}
	var cur *watchStream
	for r.more() {
		switch k := r.kind(); k {
		case "epoch":
			_, err = r.take(1)
		case "cfg":
			var v []string
			if v, err = r.take(4); err == nil {
				cfg.slow = msDuration(v[0])
				cfg.wall = msDuration(v[1])
				cfg.repos, cfg.notify = v[2], v[3]
			}
		case "sprint":
			var v []string
			if v, err = r.take(1); err == nil {
				sprint = v[0]
			}
		case "sprintnotes":
			sprintNotes, err = r.list()
		case "stream":
			var v []string
			if v, err = r.take(2); err == nil {
				sts = append(sts, watchStream{name: v[0], merge: map[string]string{}, stops: map[string]bool{}})
				cur = &sts[len(sts)-1]
			}
		case "member", "merge", "card", "escalation", "stop", "notes", "slow":
			if cur == nil {
				return nil, cfg, "", nil, fmt.Errorf("ns_land_watch_pass: %s row before any stream at %d", k, r.i)
			}
			err = cur.decode(r, k)
		default:
			return nil, cfg, "", nil, fmt.Errorf("ns_land_watch_pass: unexpected row %q at %d", k, r.i)
		}
		if err != nil {
			return nil, cfg, "", nil, err
		}
	}
	return sts, cfg, sprint, sprintNotes, nil
}

// decode reads one of a stream's rows.
func (st *watchStream) decode(r *rows, k string) error {
	switch k {
	case "member":
		v, err := r.take(5)
		if err != nil {
			return err
		}
		score, _ := strconv.ParseFloat(v[1], 64)
		at, _ := strconv.ParseInt(v[2], 10, 64)
		st.members = append(st.members, MergeMember{Task: v[0], Order: score, MergingAt: time.UnixMilli(at), PR: v[3], Paths: v[4]})
	case "merge":
		h, err := r.hash()
		if err != nil {
			return err
		}
		st.merge = h
	case "card":
		v, err := r.take(4)
		if err != nil {
			return err
		}
		st.cardState, st.cardReason = v[1], v[2]
	case "escalation":
		v, err := r.take(2)
		if err != nil {
			return err
		}
		st.escState = v[1]
	case "stop":
		v, err := r.take(3)
		if err != nil {
			return err
		}
		st.stops[v[0]] = v[1] == "landed" || v[2] == "landed"
	case "notes":
		l, err := r.list()
		if err != nil {
			return err
		}
		st.notes = l
	case "slow":
		v, err := r.take(8)
		if err != nil {
			return err
		}
		at, _ := strconv.ParseInt(v[2], 10, 64)
		st.slow = &slowRow{word: v[0], oldest: v[1], at: time.UnixMilli(at), age: msDuration(v[3]),
			changed: v[4] == "1", note: v[5] == "1", max: msDuration(v[6]), noted: v[7]}
	}
	return nil
}

// rows walks a function's flat reply: a row kind, then its fields.
type rows struct {
	name  string
	reply []string
	i     int
}

func (r *rows) more() bool   { return r.i < len(r.reply) }
func (r *rows) kind() string { return r.reply[r.i] }

// take reads n fields after the kind and moves past them.
func (r *rows) take(n int) ([]string, error) {
	if r.i+1+n > len(r.reply) {
		return nil, fmt.Errorf("%s: reply is short at %d (%s)", r.name, r.i, r.reply[r.i])
	}
	v := r.reply[r.i+1 : r.i+1+n]
	r.i += 1 + n
	return v, nil
}

// list reads <n> then n lines.
func (r *rows) list() ([]string, error) {
	c, err := r.take(1)
	if err != nil {
		return nil, err
	}
	n, _ := strconv.Atoi(c[0])
	if r.i+n > len(r.reply) {
		return nil, fmt.Errorf("%s: list is short at %d", r.name, r.i)
	}
	out := append([]string{}, r.reply[r.i:r.i+n]...)
	r.i += n
	return out, nil
}

// hash reads <n> then n field, value pairs.
func (r *rows) hash() (map[string]string, error) {
	c, err := r.take(1)
	if err != nil {
		return nil, err
	}
	n, _ := strconv.Atoi(c[0])
	if r.i+2*n > len(r.reply) {
		return nil, fmt.Errorf("%s: hash is short at %d", r.name, r.i)
	}
	h := make(map[string]string, n)
	for j := 0; j < n; j++ {
		h[r.reply[r.i+2*j]] = r.reply[r.i+2*j+1]
	}
	r.i += 2 * n
	return h, nil
}

func msDuration(s string) time.Duration {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return time.Duration(n) * time.Millisecond
}

// MergeBriefFor is a merge card's brief as a view of its stream now
// (nova-tools #4449: the watch writes no text every second): the merging
// members in work order with PR and merging age, the rules, the stream's
// and the sprint's MERGE-NOTEs, from one read-only call
// (ns_land_brief_read). rec is the card's record (stream, repo, ref as the
// base). An empty merging set is no brief: "", nil, and the card renders
// from its title alone.
func MergeBriefFor(ctx context.Context, c redis.Cmdable, id string, rec map[string]string, now time.Time) (string, error) {
	streamName := rec["stream"]
	if streamName == "" {
		if rest, ok := strings.CutPrefix(rec["title"], "STREAM: "); ok {
			streamName, _, _ = strings.Cut(rest, " |")
		}
	}
	if streamName == "" {
		return "", fmt.Errorf("merge brief: task:%s names no stream", id)
	}
	reply, err := c.FCall(ctx, "ns_land_brief_read", nil, streamName, strconv.FormatInt(now.UnixMilli(), 10)).StringSlice()
	if err != nil {
		return "", fmt.Errorf("ns_land_brief_read: %w", err)
	}
	r := &rows{name: "ns_land_brief_read", reply: reply}
	st := watchStream{name: streamName, stops: map[string]bool{}}
	var repos, sprint string
	var sprintNotes []string
	for r.more() {
		switch k := r.kind(); k {
		case "cfg":
			v, err := r.take(1)
			if err != nil {
				return "", err
			}
			repos = v[0]
		case "sprint":
			v, err := r.take(1)
			if err != nil {
				return "", err
			}
			sprint = v[0]
		case "sprintnotes":
			if sprintNotes, err = r.list(); err != nil {
				return "", err
			}
		case "member", "notes":
			if err := st.decode(r, k); err != nil {
				return "", err
			}
		default:
			return "", fmt.Errorf("ns_land_brief_read: unexpected row %q at %d", k, r.i)
		}
	}
	if len(st.members) == 0 {
		return "", nil
	}
	slug, err := stream.Slug(streamName)
	if err != nil {
		return "", err
	}
	repo := rec["repo"]
	if repo == "" {
		if s := strings.TrimSpace(repos); s != "" {
			repo = strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })[0]
		} else {
			repo = DefaultLandRepo
		}
	}
	base := rec["ref"]
	if base == "" {
		base = "dev"
	}
	var paths []string
	seenPath := map[string]bool{}
	for _, m := range st.members {
		for _, p := range strings.Fields(m.Paths) {
			if !seenPath[p] {
				seenPath[p] = true
				paths = append(paths, p)
			}
		}
	}

	// Read PR records for head, ci, behind and stream records for behind.
	pipe := c.Pipeline()
	refreshCmd1 := pipe.HGetAll(ctx, "stream-refresh:"+streamName)
	refreshCmd2 := pipe.HGetAll(ctx, "stream-refresh:"+repo+":"+slug)
	refreshCmd4 := pipe.HGetAll(ctx, "stream-refresh:"+prkey.Name(repo)+":"+slug)
	refreshCmd3 := pipe.HGetAll(ctx, "stream:refresh:"+streamName)
	landStreamCmd := pipe.HGetAll(ctx, "land:"+streamName)
	landRepoSlugCmd := pipe.HGetAll(ctx, "land:"+repo+":"+slug)
	landBareSlugCmd := pipe.HGetAll(ctx, "land:"+prkey.Name(repo)+":"+slug)
	tipCmd1 := pipe.HGet(ctx, civerdict.TipKey(repo, base), "sha")
	tipCmd2 := pipe.HGet(ctx, civerdict.TipKey(prkey.Name(repo), base), "sha")
	procCICmd := pipe.HGetAll(ctx, "proc:ci")
	ciPoolCmd := pipe.ZRevRange(ctx, "ci:pool", 0, 10)

	type memberPRInfo struct {
		repo string
		num  string
		cmd  *redis.SliceCmd
	}
	prCmds := make([]memberPRInfo, len(st.members))
	for i, m := range st.members {
		mRepo, mNum, ok := parseMemberPR(m.PR, repo)
		if ok {
			prCmds[i] = memberPRInfo{
				repo: mRepo,
				num:  mNum,
				cmd:  pipe.HMGet(ctx, prkey.KeyText(mRepo, mNum), "head", "ci", "ci_sha", "base_sha", "behind"),
			}
		}
	}
	_ = execPipe(ctx, pipe)

	var streamBehind, streamBaseSHA string
	readStreamFields := func(m map[string]string) {
		if streamBehind == "" {
			if b := strings.TrimSpace(m["behind"]); b != "" {
				streamBehind = b
			} else if b := strings.TrimSpace(m["commits_behind"]); b != "" {
				streamBehind = b
			}
		}
		if streamBaseSHA == "" {
			if s := strings.TrimSpace(m["base_sha"]); s != "" {
				streamBaseSHA = s
			}
		}
	}
	readStreamFields(refreshCmd1.Val())
	readStreamFields(refreshCmd2.Val())
	readStreamFields(refreshCmd4.Val())
	readStreamFields(refreshCmd3.Val())
	readStreamFields(landStreamCmd.Val())
	readStreamFields(landRepoSlugCmd.Val())
	readStreamFields(landBareSlugCmd.Val())

	devTip := strings.TrimSpace(tipCmd1.Val())
	if devTip == "" {
		devTip = strings.TrimSpace(tipCmd2.Val())
	}
	if devTip == "" {
		procCI := procCICmd.Val()
		if s := strings.TrimSpace(procCI["tip"]); s != "" {
			devTip = s
		} else if s := strings.TrimSpace(procCI["sha"]); s != "" {
			devTip = s
		} else if s := strings.TrimSpace(procCI["head"]); s != "" {
			devTip = s
		}
	}
	if devTip == "" {
		for _, item := range ciPoolCmd.Val() {
			if r, sha, ok := strings.Cut(item, ":"); ok {
				if prkey.Name(r) == prkey.Name(repo) && sha != "" {
					devTip = sha
					break
				}
			}
		}
	}
	if devTip == "" {
		for _, info := range prCmds {
			if info.cmd != nil {
				vals := info.cmd.Val()
				if len(vals) > 3 && vals[3] != nil {
					if s := strings.TrimSpace(vals[3].(string)); s != "" {
						devTip = s
						break
					}
				}
			}
		}
	}

	type resolvedMember struct {
		head    string
		ci      string
		behind  string
		needsCI bool
		ciRepo  string
	}
	resMembers := make([]resolvedMember, len(st.members))
	for i := range st.members {
		info := prCmds[i]
		if info.cmd == nil {
			b := streamBehind
			if b == "" && streamBaseSHA != "" && devTip != "" && streamBaseSHA == devTip {
				b = "0"
			}
			if b == "" {
				b = "-"
			}
			resMembers[i] = resolvedMember{head: "-", ci: "-", behind: b}
			continue
		}
		vals := info.cmd.Val()
		var head, ci, ciSHA, baseSHA, behind string
		if len(vals) > 0 && vals[0] != nil {
			head, _ = vals[0].(string)
		}
		if len(vals) > 1 && vals[1] != nil {
			ci, _ = vals[1].(string)
		}
		if len(vals) > 2 && vals[2] != nil {
			ciSHA, _ = vals[2].(string)
		}
		if len(vals) > 3 && vals[3] != nil {
			baseSHA, _ = vals[3].(string)
		}
		if len(vals) > 4 && vals[4] != nil {
			behind, _ = vals[4].(string)
		}

		head = strings.TrimSpace(head)
		ci = strings.TrimSpace(ci)
		ciSHA = strings.TrimSpace(ciSHA)
		baseSHA = strings.TrimSpace(baseSHA)
		behind = strings.TrimSpace(behind)

		if behind == "" {
			if streamBehind != "" {
				behind = streamBehind
			} else {
				bSHA := baseSHA
				if bSHA == "" {
					bSHA = streamBaseSHA
				}
				if bSHA != "" && devTip != "" && bSHA == devTip {
					behind = "0"
				}
			}
		}
		if behind == "" {
			behind = "-"
		}

		if ciSHA != "" && head != "" && ciSHA != head {
			ci = ""
		}

		needsCI := false
		if ci == "" && head != "" && head != "-" {
			needsCI = true
		}
		if head == "" {
			head = "-"
		}

		resMembers[i] = resolvedMember{
			head:    head,
			ci:      ci,
			behind:  behind,
			needsCI: needsCI,
			ciRepo:  info.repo,
		}
	}

	var anyNeedsCI bool
	for _, rm := range resMembers {
		if rm.needsCI {
			anyNeedsCI = true
			break
		}
	}
	if anyNeedsCI {
		pipe2 := c.Pipeline()
		type ciReq struct {
			idx int
			cmd *redis.MapStringStringCmd
		}
		var ciReqs []ciReq
		for i, rm := range resMembers {
			if rm.needsCI {
				ciReqs = append(ciReqs, ciReq{
					idx: i,
					cmd: pipe2.HGetAll(ctx, "ci:"+rm.ciRepo+":"+rm.head),
				})
				if prkey.Name(rm.ciRepo) != rm.ciRepo {
					ciReqs = append(ciReqs, ciReq{
						idx: i,
						cmd: pipe2.HGetAll(ctx, "ci:"+prkey.Name(rm.ciRepo)+":"+rm.head),
					})
				}
			}
		}
		_ = execPipe(ctx, pipe2)
		for _, req := range ciReqs {
			if resMembers[req.idx].ci != "" && resMembers[req.idx].ci != "-" {
				continue
			}
			ciMap := req.cmd.Val()
			if len(ciMap) > 0 {
				if w := strings.TrimSpace(ciMap["ci"]); w != "" {
					resMembers[req.idx].ci = w
				} else if v := strings.ToUpper(strings.TrimSpace(ciMap["verdict"])); v == "OK" {
					resMembers[req.idx].ci = "green"
				} else if v := strings.ToUpper(strings.TrimSpace(ciMap["final"])); v == "OK" {
					resMembers[req.idx].ci = "green"
				} else if v == "FAIL" {
					resMembers[req.idx].ci = "red"
				}
			}
		}
	}

	for i := range resMembers {
		if resMembers[i].ci == "" {
			resMembers[i].ci = "-"
		}
		st.members[i].Head = resMembers[i].head
		st.members[i].CI = resMembers[i].ci
		st.members[i].Behind = resMembers[i].behind
	}

	card := MergeCard{ID: id, Kind: string(LandMergeKind), Stream: streamName, Slug: slug, Repo: repo, Base: base, Sprint: sprint,
		Paths: strings.Join(paths, " "), Members: st.members, Notes: append(append([]string(nil), st.notes...), sprintNotes...), Now: now}
	return MergeBrief(card), nil
}

func parseMemberPR(pr, defaultRepo string) (repo, num string, ok bool) {
	pr = strings.TrimSpace(pr)
	if pr == "" || pr == "no-pr" {
		return "", "", false
	}
	repo = defaultRepo
	if i := strings.LastIndex(pr, "/pull/"); i >= 0 {
		parts := strings.Split(strings.Trim(pr[:i], "/"), "/")
		if len(parts) >= 2 {
			repo = parts[len(parts)-2] + "/" + parts[len(parts)-1]
		}
		num = strings.Trim(pr[i+len("/pull/"):], "/")
	} else if left, right, cut := strings.Cut(pr, "#"); cut {
		if left != "" {
			repo = left
		}
		num = right
	} else {
		num = pr
	}
	num = strings.TrimSpace(num)
	if num == "" {
		return "", "", false
	}
	if _, err := strconv.Atoi(num); err != nil {
		return "", "", false
	}
	return prkey.Name(repo), num, true
}

func execPipe(ctx context.Context, pipe redis.Pipeliner) error {
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return err
	}
	return nil
}
