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
// Live sets), sorted and comma-joined; a stream with none has no field. The
// one writer is 02_card_move.lua's SP: a card entering a live set of its
// stream adds its paths, one leaving recomputes the union. Push reads the
// whole hash in one HGETALL and refuses a card whose PATHS overlap another
// stream's (Gate); ws check recomputes it from the records (LivePaths).
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
// wildcard segment (internal/b/** and internal/b/*.go hold internal/b). A
// glob at the root holds no one path and is dropped. Sorted, unique.
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
		if p := ParsePaths(csv); len(p) > 0 {
			sp[s] = p
		}
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

// PathsRefusal is a push refused because its PATHS overlap another open
// stream's: the stream and the overlapping paths of both sides.
type PathsRefusal struct {
	Stream string
	Paths  []string
}

// Receipt is the one refusal line; the remedy is the flag that pushes the
// card onto that stream instead.
func (r *PathsRefusal) Receipt() string {
	return fmt.Sprintf("REFUSED PATHS overlap stream=%s paths=%s remedy=%s",
		oneline.Field(r.Stream), oneline.Field(JoinPaths(r.Paths)), strconv.Quote("--join "+JoinArg(r.Stream)))
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
// naming the first overlapping stream (by name). join names a stream the
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
	for _, s := range sp.names() {
		if s == target {
			continue
		}
		if ov := OverlappingPaths(paths, sp[s]); ov != nil {
			return "", &PathsRefusal{Stream: s, Paths: ov}
		}
	}
	return target, nil
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
// ws:names, its record's paths field read by SplitPaths. It also returns the
// stored record (PathsKey) and the live cards. Three pipelined round trips.
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
		live.Add(cards[i].Stream, SplitPaths(cards[i].Raw))
	}
	return live, stored, cards, nil
}

// SamePaths says whether two path lists hold the same entries.
func SamePaths(a, b []string) bool {
	return JoinPaths(SplitPaths(JoinPaths(a))) == JoinPaths(SplitPaths(JoinPaths(b)))
}

// Stale is every stream whose stored paths (PathsKey) are not its live
// cards' paths, in name order.
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
		if !SamePaths(live[s], stored[s]) {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// RepairPaths writes what LivePaths found: each live card's PathsField from
// its record's PATHS, and each stale stream's field of PathsKey (removed
// when it holds none), in one pipeline. It returns the streams and records
// written. It is ws check --repair: the backfill of the records cut before
// #4322 and the fix of any drift since.
func RepairPaths(ctx context.Context, c redis.Cmdable, live, stored StreamPaths, cards []LiveCard) (streams, records int, err error) {
	pipe := c.Pipeline()
	for _, lc := range cards {
		want := JoinPaths(SplitPaths(lc.Raw))
		if want == lc.Stored {
			continue
		}
		if want == "" {
			pipe.HDel(ctx, lc.Key, PathsField)
		} else {
			pipe.HSet(ctx, lc.Key, PathsField, want)
		}
		records++
	}
	for _, s := range Stale(live, stored) {
		if len(live[s]) == 0 {
			pipe.HDel(ctx, PathsKey, s)
		} else {
			pipe.HSet(ctx, PathsKey, s, JoinPaths(live[s]))
		}
		streams++
	}
	if streams+records == 0 {
		return 0, 0, nil
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, 0, fmt.Errorf("repair %s: %w", PathsKey, err)
	}
	return streams, records, nil
}
