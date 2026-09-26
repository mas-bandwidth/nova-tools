package ws

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
)

// The stream's paths (nova-tools #4322): no path belongs to two open
// streams. Measured the week of 2026-09-21: #4346 (stream ci) squash-merged
// on a base that predated #4344 (stream work) and reverted it whole; both
// streams were open, both cards named PATHS, nothing compared them.
//
// PathsKey is the stream record's paths: a HASH, field <stream>, value the
// union of its live cards' PATHS (PathsField of each record in the stream's
// Live sets), sorted and comma-joined, "" when they name none. A stream
// that holds a live card and has no field is unbuilt: its paths are
// unknown, and every gated move is refused (PATHS unbuilt) until ws check
// --repair builds it. The one writer and the one check are
// 02_card_move.lua's SP: a card entering a live set of its stream is gated
// (SP.gate, in the same FCALL as the move, before any write) and adds its
// paths; one leaving recomputes the union. ws check recomputes it from the
// records (LivePaths). Gate is the same rule in Go, for card cut --from's
// check before it files any issue.
const (
	PathsKey   = "ws:paths"
	PathsField = "stream_paths"
)

// Live are the sets whose cards hold their paths: the stream line before
// landed. 02_card_move.lua's SP.LIVE is the same five.
var Live = []string{Waiting, Ready, Working, Review, Merging}

var (
	pathsParenRx = regexp.MustCompile(`\([^)]*\)`)
	pathsSplitRx = regexp.MustCompile(`[\s,;]+`)
	pathsRepoRx  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*:`)
)

// SplitPaths is the one reading of a PATHS line as the stream compares it:
// tokens split on space, comma and semicolon; parentheticals, quotes, the
// markers "-" and "none", ~ exclusions and a repo: tag dropped; ./ and
// trailing slashes trimmed; a glob cut to the directory above its first
// wildcard segment (internal/nsprint/ws/** and internal/nsprint/ws/*.go hold
// internal/nsprint/ws). A glob at the root holds no one path and is dropped.
// Sorted, unique.
func SplitPaths(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, tok := range pathsSplitRx.Split(pathsParenRx.ReplaceAllString(text, " "), -1) {
		tok = strings.Trim(tok, "`'\"")
		tok = strings.TrimSuffix(tok, ".")
		if tok == "" || tok == "-" || strings.EqualFold(tok, "none") || strings.HasPrefix(tok, "~") {
			continue
		}
		if loc := pathsRepoRx.FindStringIndex(tok); loc != nil {
			tok = tok[loc[1]:]
		}
		tok = strings.TrimPrefix(tok, "./")
		tok = strings.TrimLeft(tok, "/")
		var keep []string
		for _, seg := range strings.Split(tok, "/") {
			if strings.ContainsAny(seg, "*?[") {
				break
			}
			if seg != "" && seg != "." {
				keep = append(keep, seg)
			}
		}
		p := strings.Join(keep, "/")
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// JoinPaths is the stored form: comma-joined (SplitPaths never yields a comma).
func JoinPaths(paths []string) string { return strings.Join(paths, ",") }

// ParsePaths reads the stored form back.
func ParsePaths(csv string) []string {
	var out []string
	for _, p := range strings.Split(csv, ",") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// PathsOverlap is the rule: equal, or one a prefix of the other at a /.
func PathsOverlap(a, b string) bool {
	return a == b || strings.HasPrefix(b, a+"/") || strings.HasPrefix(a, b+"/")
}

// OverlappingPaths is every entry of mine or theirs that overlaps an entry
// of the other side, sorted and unique; nil when the two are disjoint.
func OverlappingPaths(mine, theirs []string) []string {
	hit := map[string]bool{}
	for _, a := range mine {
		for _, b := range theirs {
			if PathsOverlap(a, b) {
				hit[a], hit[b] = true, true
			}
		}
	}
	if len(hit) == 0 {
		return nil
	}
	out := make([]string, 0, len(hit))
	for p := range hit {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// StreamPaths is the stream record's paths, stream -> paths.
type StreamPaths map[string][]string

// QueueStreamPaths queues the one read of every open stream's paths on pipe.
func QueueStreamPaths(ctx context.Context, pipe redis.Pipeliner) *redis.MapStringStringCmd {
	return pipe.HGetAll(ctx, PathsKey)
}

// StreamPathsOf reads a queued QueueStreamPaths.
func StreamPathsOf(cmd *redis.MapStringStringCmd) (StreamPaths, error) {
	m, err := cmd.Result()
	if err != nil && err != redis.Nil {
		return nil, fmt.Errorf("read %s: %w", PathsKey, err)
	}
	sp := StreamPaths{}
	for s, csv := range m {
		sp[s] = ParsePaths(csv) // a field of "" is present: built, no paths
	}
	return sp, nil
}

// ReadStreamPaths is QueueStreamPaths in its own round trip.
func ReadStreamPaths(ctx context.Context, c redis.Cmdable) (StreamPaths, error) {
	pipe := c.Pipeline()
	cmd := QueueStreamPaths(ctx, pipe)
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("read %s: %w", PathsKey, err)
	}
	return StreamPathsOf(cmd)
}

func (sp StreamPaths) names() []string {
	out := make([]string, 0, len(sp))
	for s := range sp {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// Add is a card's paths joining its stream, for the cards after it in one
// push batch.
func (sp StreamPaths) Add(stream string, paths []string) {
	if stream == "" || len(paths) == 0 {
		return
	}
	sp[stream] = SplitPaths(strings.Join(append(append([]string{}, sp[stream]...), paths...), ","))
}

// PathsRefusal is a push or move refused because its PATHS overlap other
// open streams' (Stream the first by name, Also the rest, and the
// overlapping paths of every side), or
// because a stream's paths are unbuilt (Unbuilt: the stream holds live
// cards and ws:paths has no field for it). Remedy, when set, replaces the
// overlap's default remedy (--join <stream>).
type PathsRefusal struct {
	Stream  string
	Also    []string
	Paths   []string
	Unbuilt bool
	// NotOpen: --join named a stream that holds no live card (parked,
	// landed or unknown); a card joins only an open stream.
	NotOpen bool
	Remedy  string
}

// RepairRemedy is the unbuilt refusal's remedy; StreamsRemedy the not-open
// join's (it lists the streams and their counts).
const (
	RepairRemedy  = "nova-sprint ws check --repair"
	StreamsRemedy = "nova-sprint stream ls"
)

// Receipt is the one refusal line; the overlap's remedy is the flag that
// pushes the card onto that stream instead, only when exactly one stream
// holds the overlap. A card overlapping two streams (a pair ws check
// --repair wrote overlapping), whichever one --join named, joins neither:
// the gate names both (also=) and the remedy parks the second, after which
// --join the first passes. It never suggests --join the other, which the
// gate refuses naming the first.
func (r *PathsRefusal) Receipt() string {
	if r.Unbuilt {
		return fmt.Sprintf("REFUSED PATHS unbuilt stream=%s remedy=%s", oneline.Field(r.Stream), strconv.Quote(RepairRemedy))
	}
	if r.NotOpen {
		return fmt.Sprintf("REFUSED PATHS notopen stream=%s remedy=%s", oneline.Field(r.Stream), strconv.Quote(StreamsRemedy))
	}
	remedy := r.Remedy
	if remedy == "" && len(r.Also) > 0 {
		remedy = "nova-sprint scope park --stream " + JoinArg(r.Also[0])
	} else if remedy == "" {
		remedy = "--join " + JoinArg(r.Stream)
	}
	also := ""
	if len(r.Also) > 0 {
		also = " also=" + oneline.Field(strings.Join(r.Also, "|"))
	}
	return fmt.Sprintf("REFUSED PATHS overlap stream=%s%s paths=%s remedy=%s",
		oneline.Field(r.Stream), also, oneline.Field(JoinPaths(r.Paths)), strconv.Quote(remedy))
}

// ParseRefusal reads SP.gate's typed refusal (02_card_move.lua), the reply
// of ns_card_push or the why of a REFUSED task move, push or unpark:
// "PATHS overlap paths=<a,b> stream=<s>[|<s2>...]" or "PATHS unbuilt
// stream=<s>" (the streams last: a name may hold a space, never a '|'). ok
// is false for any other why.
func ParseRefusal(why string) (*PathsRefusal, bool) {
	if rest, ok := strings.CutPrefix(why, "PATHS unbuilt stream="); ok && rest != "" {
		return &PathsRefusal{Stream: rest, Unbuilt: true}, true
	}
	if rest, ok := strings.CutPrefix(why, "PATHS notopen stream="); ok && rest != "" {
		return &PathsRefusal{Stream: rest, NotOpen: true}, true
	}
	rest, ok := strings.CutPrefix(why, "PATHS overlap paths=")
	if !ok {
		return nil, false
	}
	csv, stream, ok := strings.Cut(rest, " stream=")
	if !ok || csv == "" || stream == "" {
		return nil, false
	}
	streams := strings.Split(stream, "|")
	if streams[0] == "" {
		return nil, false
	}
	return &PathsRefusal{Stream: streams[0], Also: streams[1:], Paths: ParsePaths(csv)}, true
}

func (r *PathsRefusal) Error() string { return r.Receipt() }

// JoinArg is a stream name as the --join argument a person pastes: quoted
// when it holds a space.
func JoinArg(stream string) string {
	if strings.ContainsAny(stream, " \t'") {
		return strconv.Quote(stream)
	}
	return stream
}

// Gate is push's check of one card with its PATHS on stream against every
// OTHER open stream: the stream the card is pushed onto, or the refusal
// naming every overlapping stream (by name; SP.gate's rule). join names a stream the
// card may overlap: a card overlapping it is pushed onto it instead. A card
// with no paths, or with neither a stream nor a join, is not gated.
func (sp StreamPaths) Gate(stream string, paths []string, join string) (string, *PathsRefusal) {
	if len(paths) == 0 || (stream == "" && join == "") {
		return stream, nil
	}
	target := stream
	if join != "" && join != stream && OverlappingPaths(paths, sp[join]) != nil {
		target = join
	}
	var hit, ovs []string
	for _, s := range sp.names() {
		if s == target {
			continue
		}
		if ov := OverlappingPaths(paths, sp[s]); ov != nil {
			hit = append(hit, s)
			ovs = append(ovs, ov...)
		}
	}
	if len(hit) == 0 {
		return target, nil
	}
	// a --join target the card overlaps is named with the rest: the card
	// joins neither, and the remedy parks one (SP.gate's rule)
	if target != stream {
		hit = append(hit, target)
		ovs = append(ovs, OverlappingPaths(paths, sp[target])...)
		sort.Strings(hit)
	}
	no := &PathsRefusal{Stream: hit[0], Paths: SplitPaths(JoinPaths(ovs))}
	if len(hit) > 1 {
		no.Also = hit[1:]
	}
	return "", no
}

// GateView is what SP.gate reads, for a check in Go before a write that
// the Lua gate then makes atomic (card cut --from checks every row before
// it files any issue, and its --dry-run reports what would be refused):
// every stream's paths, the streams holding a live card, and the unbuilt
// ones (a live card, no field), in name order.
type GateView struct {
	Paths   StreamPaths
	Open    map[string]bool
	Unbuilt []string
}

// ReadGateView reads the view in two pipelined round trips: ws:paths and
// ws:names, then the first three members of each stream's Live sets.
func ReadGateView(ctx context.Context, c redis.Cmdable) (GateView, error) {
	pipe := c.Pipeline()
	rec := QueueStreamPaths(ctx, pipe)
	names := pipe.SMembers(ctx, "ws:names")
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return GateView{}, fmt.Errorf("read %s: %w", PathsKey, err)
	}
	v := GateView{Open: map[string]bool{}}
	var err error
	if v.Paths, err = StreamPathsOf(rec); err != nil {
		return GateView{}, err
	}
	streams := names.Val()
	sort.Strings(streams)
	if len(streams) == 0 {
		return v, nil
	}
	pipe = c.Pipeline()
	sets := make([][]*redis.StringSliceCmd, len(streams))
	for i, s := range streams {
		for _, w := range Live {
			sets[i] = append(sets[i], pipe.ZRange(ctx, Key(s, w), 0, 2))
		}
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return GateView{}, fmt.Errorf("read the live sets: %w", err)
	}
	for i, s := range streams {
		for _, cmd := range sets[i] {
			for _, m := range cmd.Val() {
				if m != SentinelID(s) {
					v.Open[s] = true
				}
			}
		}
		if _, built := v.Paths[s]; v.Open[s] && !built {
			v.Unbuilt = append(v.Unbuilt, s)
		}
	}
	return v, nil
}

// Gate is SP.gate's rule over the view: an unbuilt stream refuses every
// gated card, a --join naming a stream that is not open refuses, then
// StreamPaths.Gate. An accepted card's stream is open for the rows after it.
func (v GateView) Gate(stream string, paths []string, join string) (string, *PathsRefusal) {
	if len(paths) == 0 || (stream == "" && join == "") {
		return stream, nil
	}
	if len(v.Unbuilt) > 0 {
		return "", &PathsRefusal{Stream: v.Unbuilt[0], Unbuilt: true}
	}
	if join != "" && join != stream && !v.Open[join] {
		return "", &PathsRefusal{Stream: join, NotOpen: true}
	}
	to, no := v.Paths.Gate(stream, paths, join)
	if no == nil && to != "" && v.Open != nil {
		v.Open[to] = true
	}
	return to, no
}

// PathsPair is two open streams sharing paths.
type PathsPair struct {
	Stream, Other string
	Paths         []string
}

// Line is ws check's finding for the pair.
func (p PathsPair) Line() string {
	return fmt.Sprintf("PATHS OVERLAP stream=%s other=%s paths=%s", oneline.Field(p.Stream), oneline.Field(p.Other),
		oneline.Field(JoinPaths(p.Paths)))
}

// Overlaps is every pair of streams that share a path, in name order.
func (sp StreamPaths) Overlaps() []PathsPair {
	names := sp.names()
	var out []PathsPair
	for i, a := range names {
		for _, b := range names[i+1:] {
			if ov := OverlappingPaths(sp[a], sp[b]); ov != nil {
				out = append(out, PathsPair{Stream: a, Other: b, Paths: ov})
			}
		}
	}
	return out
}

// LiveCard is one live member's paths: its record key, the record's own
// PATHS (raw), and its stored PathsField.
type LiveCard struct {
	Stream, Key, Raw, Stored string
}

// RecordKey is the record of a ws set member: a card id is its own record,
// a task id is task:<id>.
func RecordKey(member string) string {
	if isCardID(member) {
		return member
	}
	return "task:" + member
}

// LivePaths recomputes every stream's paths from its records, the truth the
// stream record caches: every member of every Live set of every stream in
// ws:names, its record's stored PathsField (written only gated: the push,
// and the repair), else its PATHS read by SplitPaths (a record cut before
// #4322, which the repair backfills). It also returns the stored record
// (PathsKey) and the live cards. Three pipelined round trips.
func LivePaths(ctx context.Context, c redis.Cmdable) (live, stored StreamPaths, cards []LiveCard, err error) {
	pipe := c.Pipeline()
	names := pipe.SMembers(ctx, "ws:names")
	rec := QueueStreamPaths(ctx, pipe)
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, nil, nil, err
	}
	if stored, err = StreamPathsOf(rec); err != nil {
		return nil, nil, nil, err
	}
	streams := names.Val()
	sort.Strings(streams)
	type set struct {
		stream string
		cmd    *redis.StringSliceCmd
	}
	var sets []set
	pipe = c.Pipeline()
	for _, s := range streams {
		for _, w := range Live {
			sets = append(sets, set{s, pipe.ZRange(ctx, Key(s, w), 0, -1)})
		}
	}
	if len(sets) > 0 {
		if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
			return nil, nil, nil, err
		}
	}
	sentinel := map[string]bool{}
	for _, s := range streams {
		sentinel[SentinelID(s)] = true
	}
	for _, st := range sets {
		for _, m := range st.cmd.Val() {
			if sentinel[m] {
				continue
			}
			cards = append(cards, LiveCard{Stream: st.stream, Key: RecordKey(m)})
		}
	}
	pipe = c.Pipeline()
	hm := make([]*redis.SliceCmd, len(cards))
	for i, lc := range cards {
		hm[i] = pipe.HMGet(ctx, lc.Key, "paths", PathsField)
	}
	if len(cards) > 0 {
		if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
			return nil, nil, nil, err
		}
	}
	live = StreamPaths{}
	for i := range cards {
		v := hm[i].Val()
		cards[i].Raw, _ = v[0].(string)
		cards[i].Stored, _ = v[1].(string)
		if _, ok := live[cards[i].Stream]; !ok {
			live[cards[i].Stream] = nil // a stream with a live card is in live, paths or not
		}
		if cards[i].Stored != "" {
			live.Add(cards[i].Stream, ParsePaths(cards[i].Stored))
		} else {
			live.Add(cards[i].Stream, SplitPaths(cards[i].Raw))
		}
	}
	return live, stored, cards, nil
}

// SamePaths says whether two path lists hold the same entries.
func SamePaths(a, b []string) bool {
	return JoinPaths(SplitPaths(JoinPaths(a))) == JoinPaths(SplitPaths(JoinPaths(b)))
}

// Stale is every stream whose stored paths (PathsKey) are not its live
// cards' paths, or that holds a live card (a key of live) and has no field
// in stored (unbuilt), in name order.
func Stale(live, stored StreamPaths) []string {
	names := map[string]bool{}
	for s := range live {
		names[s] = true
	}
	for s := range stored {
		names[s] = true
	}
	var out []string
	for s := range names {
		_, holds := live[s]
		_, built := stored[s]
		if !SamePaths(live[s], stored[s]) || (holds && !built) {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// RepairFunction is ws check --repair's one call (02_card_move.lua
// SP.repair): the backfill and every stream's field, over the live sets as
// they are when it runs, so a push beside it loses nothing (#4322).
const RepairFunction = "ns_ws_paths_repair"

// Repair is what RepairPaths wrote: the streams whose field changed, the
// records backfilled (overlapping another stream's or not: ws check
// reports the pair), the streams left unbuilt, and one line per record
// found changed since the read (PATHS unread id=<member> in=<stream>).
type Repair struct {
	Streams, Records, Unbuilt int
	Refused                   []string
}

// RepairPaths is ws check --repair: one FCALL of ns_ws_paths_repair with
// the live cards LivePaths found holding PATHS and no PathsField, each as
// its record key, the PATHS read and their stored form. The Lua backfills
// each whose PATHS are still those read and writes every stream's field
// from the live sets in the same call. It is the
// backfill of the records cut before #4322 and the fix of any drift since.
func RepairPaths(ctx context.Context, c redis.Cmdable, cards []LiveCard) (Repair, error) {
	args := []any{"ws check --repair"}
	for _, lc := range cards {
		if lc.Stored != "" || lc.Raw == "" {
			continue
		}
		args = append(args, lc.Key, lc.Raw, JoinPaths(SplitPaths(lc.Raw)))
	}
	v, err := c.FCall(ctx, RepairFunction, nil, args...).StringSlice()
	if err != nil {
		return Repair{}, fmt.Errorf("repair %s: %w", PathsKey, err)
	}
	if len(v) < 4 || v[0] != "REPAIRED" {
		return Repair{}, fmt.Errorf("repair %s: reply %q", PathsKey, v)
	}
	var r Repair
	r.Streams, _ = strconv.Atoi(v[1])
	r.Records, _ = strconv.Atoi(v[2])
	r.Unbuilt, _ = strconv.Atoi(v[3])
	r.Refused = v[4:]
	return r, nil
}
