// Package digest is nova-sprint digest (nova-tools#3158): what landed, which
// holds were routed and which reads were scored in a window [since, until),
// read from three Redis sources only (THE BOUNDARY, 2026-09-25):
//
//	ws:log               stream every move receipt (id stream from to by why at)
//	land:<repo>:events   stream the unit lander's transitions (event LANDED ...)
//	pr:<name>:<n>        hash   the PR record (internal/nsprint/prkey): merge_sha, reads
//
// The window is on stream ids (ms): an entry at since is in, one at until is
// out. Nothing is SCANned: ws:log is read by id range, the event streams are
// the --repo list plus every repo a ws:log landing in the window names, and
// the PR records are the ones the window's receipts name. Every XRANGE carries
// COUNT 1000 and all streams page together, so a digest is a few round trips:
// one for ws:log and the named event streams, one for the PR records and the
// derived event streams, and one more per further page.
//
// A stream whose max-deleted-entry-id (XINFO STREAM) is at or after since has
// lost entries of the window: its sections print a TRIMMED line and never
// "none", so a trimmed past is never reported as an empty one.
package digest

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
)

// Page is the COUNT of every stream read.
const Page = 1000

// WSLog is the move receipt stream.
const WSLog = "ws:log"

// EventsKey is the unit lander's event stream of repo (as the lander keys it).
func EventsKey(repo string) string { return "land:" + repo + ":events" }

// Options is one digest's window and the repos whose event streams are read
// besides those a landing in the window names.
type Options struct {
	Since, Until time.Time
	Repos        []string
}

// Landing is a stream landing: the ws:log receipt land:<repo>:<slug> ->
// landed that stream.SaveLanded writes, with the stream PR's merge_sha from
// its record, and Tasks the receipts "landed with <name>#<pr> (...)" in the
// window.
type Landing struct {
	At       int64
	Repo     string
	PR       int
	MergeSHA string
	Members  int
	Tasks    int
	By       string
}

// Batch is one LANDED event of the unit lander.
type Batch struct {
	At                    int64
	Repo, Base, ID, Train string
}

// Close is the tasks a person's CLOSE line landed (why "landed: CLOSE by
// <who> on <name>#<n>"), grouped by line; At is the first.
type Close struct {
	At    int64
	Ref   string
	By    string
	Tasks int
}

// Hold is a hold routed to its answerer: the ws:log receipt of a fix, close
// or recut task (route_duty.lua's route pr <kind>), with the holder and
// whether the holder has answered it, from the PR record's typed lines.
type Hold struct {
	At                  int64
	Repo                string
	PR                  int
	Head, Holder, Route string
	State               string // open, answered or ? (no record or no HOLD line at head)
}

// Scored is a scored read: the ws:log receipt "read: SCORE by <who> at
// <head8>" of a read task, with the score from the PR record's SCORE line.
type Scored struct {
	At               int64
	Repo             string
	PR               int
	Head, Who, Score string
}

// Trim is a stream that lost entries at or after since. Mark is
// max-deleted=<id> (an XDEL at or after since) or first=<id> (entries were
// trimmed off the front and the oldest one left is after since, so the
// trimmed ones may have been in the window).
type Trim struct{ Stream, Mark string }

// trimmed reads XINFO STREAM against the window's first id lo. XDEL moves
// max-deleted-entry-id; XTRIM and MAXLEN do not, but leave entries-added
// above length and recorded-first-entry-id at the oldest survivor.
func trimmed(key string, in *redis.XInfoStream, lo string) *Trim {
	if d := in.MaxDeletedEntryID; d != "" && d != "0-0" && !idBefore(d, lo) {
		return &Trim{Stream: key, Mark: "max-deleted=" + d}
	}
	if f := in.RecordedFirstEntryID; in.EntriesAdded > in.Length && f != "" && idBefore(lo, f) {
		return &Trim{Stream: key, Mark: "first=" + f}
	}
	return nil
}

// Digest is everything one window printed.
type Digest struct {
	Since, Until time.Time
	Repos        []string // the event streams read, sorted
	Landings     []Landing
	Batches      []Batch
	Closes       []Close
	Holds        []Hold
	Reads        []Scored
	LogTrim      *Trim  // ws:log
	EventTrims   []Trim // land:<repo>:events
}

var (
	landedRx   = regexp.MustCompile(`^LANDED (\S+)#(\d+) \S+ at \S+ merge=(\S+) members=(\d+)$`)
	withRx     = regexp.MustCompile(`^landed with (\S+#\d+) \(`)
	closeRx    = regexp.MustCompile(`^landed: CLOSE by (\S+) on (\S+#\d+)$`)
	scoreWhyRx = regexp.MustCompile(`^read: SCORE by (\S+) at ([0-9a-f]+)$`)
	// route_duty.lua pr_task_id: <kind>-<pr>-<head8>, -<name> when not nova-tools.
	taskIDRx = regexp.MustCompile(`^(read|fix|close|recut)-(\d+)-([0-9a-f]{8})(?:-(\S+))?$`)
	scoreRx  = regexp.MustCompile(`^(\d+)(?:/10)?$`)
)

// defaultPRRepo is the repo a PR task id without a -<name> suffix names
// (route_duty.lua pr_task_id).
const defaultPRRepo = "nova-tools"

// stream is one stream being paged through the window.
type stream struct {
	key     string
	msgs    []redis.XMessage
	info    *redis.XInfoStreamCmd
	page    *redis.XMessageSliceCmd
	more    bool
	trimmed *Trim
}

func msOf(t time.Time) int64 { return t.UnixMilli() }

// idMs is the ms part of a stream id.
func idMs(id string) int64 {
	ms, _, _ := strings.Cut(id, "-")
	n, _ := strconv.ParseInt(ms, 10, 64)
	return n
}

// idBefore is a < b for stream ids.
func idBefore(a, b string) bool {
	am, as, _ := strings.Cut(a, "-")
	bm, bs, _ := strings.Cut(b, "-")
	an, _ := strconv.ParseUint(am, 10, 64)
	bn, _ := strconv.ParseUint(bm, 10, 64)
	if an != bn {
		return an < bn
	}
	ax, _ := strconv.ParseUint(as, 10, 64)
	bx, _ := strconv.ParseUint(bs, 10, 64)
	return ax < bx
}

func noSuchKey(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such key")
}

// queue adds the stream's next read to pipe: XINFO and the first page, or the
// page after the last id.
func (s *stream) queue(ctx context.Context, pipe redis.Pipeliner, lo, hi string) {
	start := lo
	if s.info == nil {
		s.info = pipe.XInfoStream(ctx, s.key)
	} else {
		start = "(" + s.msgs[len(s.msgs)-1].ID
	}
	s.page = pipe.XRangeN(ctx, s.key, start, hi, Page)
}

// take reads the answers queue asked for.
func (s *stream) take(lo string) error {
	if s.info != nil && s.trimmed == nil && len(s.msgs) == 0 {
		if err := s.info.Err(); err != nil && !noSuchKey(err) {
			return fmt.Errorf("XINFO STREAM %s: %w", s.key, err)
		} else if err == nil {
			s.trimmed = trimmed(s.key, s.info.Val(), lo+"-0")
		}
	}
	msgs, err := s.page.Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("XRANGE %s: %w", s.key, err)
	}
	s.msgs = append(s.msgs, msgs...)
	s.more = len(msgs) == Page
	return nil
}

// fetch pages every stream through the window: one pipeline for the first
// pages (with extra's commands), then one per further page of any of them.
func fetch(ctx context.Context, c redis.Cmdable, ss []*stream, lo, hi string, extra func(redis.Pipeliner)) error {
	todo := ss
	first := true
	for len(todo) > 0 || (first && extra != nil) {
		pipe := c.Pipeline()
		for _, s := range todo {
			s.queue(ctx, pipe, lo, hi)
		}
		if first && extra != nil {
			extra(pipe)
		}
		first = false
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) && !noSuchKey(err) {
			return err
		}
		var next []*stream
		for _, s := range todo {
			if err := s.take(lo); err != nil {
				return err
			}
			if s.more {
				next = append(next, s)
			}
		}
		todo = next
	}
	return nil
}

func field(m redis.XMessage, k string) string {
	v, _ := m.Values[k].(string)
	return v
}

// prRef is one PR record the window's receipts name.
type prRef struct {
	name string
	n    int
}

func (r prRef) key() string { return prkey.Key(r.name, r.n) }

// Read reads the digest of o's window.
func Read(ctx context.Context, c redis.Cmdable, o Options) (*Digest, error) {
	if !o.Since.Before(o.Until) {
		return nil, fmt.Errorf("since must be before until")
	}
	lo := strconv.FormatInt(msOf(o.Since), 10)
	hi := strconv.FormatInt(msOf(o.Until)-1, 10) // every id of the ms before until
	d := &Digest{Since: o.Since.UTC(), Until: o.Until.UTC()}

	repos := map[string]*stream{}
	var order []*stream
	addRepo := func(r string) *stream {
		if _, ok := repos[r]; ok {
			return nil
		}
		s := &stream{key: EventsKey(r)}
		repos[r] = s
		order = append(order, s)
		return s
	}
	log := &stream{key: WSLog}
	first := []*stream{log}
	for _, r := range o.Repos {
		if s := addRepo(r); s != nil {
			first = append(first, s)
		}
	}
	if err := fetch(ctx, c, first, lo, hi, nil); err != nil {
		return nil, err
	}
	d.LogTrim = log.trimmed

	// Receipts: landings name repos and PRs; holds and reads name PRs.
	landedWith := map[string]int{}
	closes := map[string]*Close{}
	var closeOrder []string
	refs := map[string]prRef{}
	var derived []*stream
	type landingAt struct {
		l   Landing
		ref prRef
	}
	var landings []landingAt
	var holds []Hold
	var reads []Scored
	for _, m := range log.msgs {
		at := idMs(m.ID)
		why, to := field(m, "why"), field(m, "to")
		switch {
		case to == "landed" && strings.HasPrefix(field(m, "id"), "land:"):
			g := landedRx.FindStringSubmatch(why)
			if g == nil {
				continue
			}
			pr, _ := strconv.Atoi(g[2])
			members, _ := strconv.Atoi(g[4])
			ref := prRef{prkey.Name(g[1]), pr}
			refs[ref.key()] = ref
			landings = append(landings, landingAt{Landing{At: at, Repo: g[1], PR: pr, MergeSHA: g[3], Members: members, By: field(m, "by")}, ref})
			if s := addRepo(g[1]); s != nil {
				derived = append(derived, s)
			}
		case to == "landed":
			if g := withRx.FindStringSubmatch(why); g != nil {
				landedWith[g[1]]++
			} else if g := closeRx.FindStringSubmatch(why); g != nil {
				k := g[2] + " " + g[1]
				if closes[k] == nil {
					closes[k] = &Close{At: at, Ref: g[2], By: g[1]}
					closeOrder = append(closeOrder, k)
				}
				closes[k].Tasks++
			}
		case field(m, "from") == "" && strings.HasPrefix(why, "route pr ") && why != "route pr read":
			g := taskIDRx.FindStringSubmatch(field(m, "id"))
			if g == nil || g[1] == "read" || "route pr "+g[1] != why {
				continue
			}
			ref := parseRef(g)
			refs[ref.key()] = ref
			holds = append(holds, Hold{At: at, Repo: ref.name, PR: ref.n, Head: g[3], Route: g[1]})
		case to == "merging":
			w := scoreWhyRx.FindStringSubmatch(why)
			g := taskIDRx.FindStringSubmatch(field(m, "id"))
			if w == nil || g == nil || g[1] != "read" {
				continue
			}
			ref := parseRef(g)
			refs[ref.key()] = ref
			reads = append(reads, Scored{At: at, Repo: ref.name, PR: ref.n, Head: w[2], Who: w[1]})
		}
	}

	keys := make([]string, 0, len(refs))
	for k := range refs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	recs := map[string]*redis.SliceCmd{}
	if len(keys) > 0 || len(derived) > 0 {
		if err := fetch(ctx, c, derived, lo, hi, func(pipe redis.Pipeliner) {
			for _, k := range keys {
				recs[k] = pipe.HMGet(ctx, k, "merge_sha", "reads")
			}
		}); err != nil {
			return nil, err
		}
	}
	rec := func(r prRef) (mergeSHA, lines string) {
		cmd := recs[r.key()]
		if cmd == nil {
			return "", ""
		}
		v := cmd.Val()
		if len(v) == 2 {
			mergeSHA, _ = v[0].(string)
			lines, _ = v[1].(string)
		}
		return mergeSHA, lines
	}

	for _, la := range landings {
		l := la.l
		if sha, _ := rec(la.ref); sha != "" {
			l.MergeSHA = sha
		}
		l.Tasks = landedWith[la.ref.name+"#"+strconv.Itoa(la.ref.n)]
		d.Landings = append(d.Landings, l)
	}
	for _, k := range closeOrder {
		d.Closes = append(d.Closes, *closes[k])
	}
	for _, h := range holds {
		_, lines := rec(prRef{h.Repo, h.PR})
		h.Holder, h.State = holder(lines, h.Head)
		d.Holds = append(d.Holds, h)
	}
	for _, r := range reads {
		_, lines := rec(prRef{r.Repo, r.PR})
		r.Score = score(lines, r.Who, r.Head)
		d.Reads = append(d.Reads, r)
	}
	for _, s := range order {
		if s.trimmed != nil {
			d.EventTrims = append(d.EventTrims, *s.trimmed)
		}
		repo := strings.TrimSuffix(strings.TrimPrefix(s.key, "land:"), ":events")
		d.Repos = append(d.Repos, repo)
		for _, m := range s.msgs {
			if field(m, "event") != "LANDED" {
				continue
			}
			d.Batches = append(d.Batches, Batch{At: idMs(m.ID), Repo: repo, Base: field(m, "base"),
				ID: field(m, "batch"), Train: field(m, "train_head")})
		}
	}
	sort.Strings(d.Repos)
	return d, nil
}

func parseRef(g []string) prRef {
	n, _ := strconv.Atoi(g[2])
	name := g[4]
	if name == "" {
		name = defaultPRRepo
	}
	return prRef{name, n}
}

// typedLine is one typed line of a PR record: its kind and key=value words
// (trailing :,; stripped, as route_duty.lua pr_lines reads them).
type typedLine struct {
	kind string
	kv   map[string]string
}

func parseLines(lines string) []typedLine {
	var out []typedLine
	for _, line := range strings.Split(lines, "\n") {
		words := strings.Fields(line)
		if len(words) == 0 {
			continue
		}
		t := typedLine{kind: words[0], kv: map[string]string{}}
		for _, w := range words[1:] {
			if k, v, ok := strings.Cut(w, "="); ok {
				t.kv[k] = strings.TrimRight(v, ":,;")
			}
		}
		out = append(out, t)
	}
	return out
}

// atHead: the line's head and the receipt's head8 name one commit (both at
// least 7 hex, one a prefix of the other).
func atHead(lineHead, head8 string) bool {
	a, b := strings.ToLower(lineHead), strings.ToLower(head8)
	if len(a) < 7 || len(b) < 7 {
		return false
	}
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

func isHold(t typedLine) bool {
	return t.kind == "HOLD" || (t.kind == "DISPOSITION" && strings.EqualFold(t.kv["verdict"], "HOLD"))
}

func counts(who string) bool {
	return who != "" && !strings.HasPrefix(who, "jev") && !strings.HasPrefix(who, "cold")
}

// holder is the hold at head8: the first holder by name among the counting
// whos with a HOLD line at that head, and answered when that who has a later
// SCORE or non-HOLD DISPOSITION line on the record.
func holder(lines, head8 string) (who, state string) {
	ts := parseLines(lines)
	last := map[string]int{}
	for i, t := range ts {
		w := strings.ToLower(t.kv["who"])
		if isHold(t) && counts(w) && atHead(t.kv["head"], head8) {
			last[w] = i
		}
	}
	if len(last) == 0 {
		return "?", "?"
	}
	whos := make([]string, 0, len(last))
	for w := range last {
		whos = append(whos, w)
	}
	sort.Strings(whos)
	who = whos[0]
	for _, t := range ts[last[who]+1:] {
		if strings.ToLower(t.kv["who"]) == who && (t.kind == "SCORE" || (t.kind == "DISPOSITION" && !isHold(t))) {
			return who, "answered"
		}
	}
	return who, "open"
}

// score is the score of who's last SCORE line at head8, or ?.
func score(lines, who, head8 string) string {
	out := "?"
	for _, t := range parseLines(lines) {
		if t.kind == "SCORE" && strings.EqualFold(t.kv["who"], who) && atHead(t.kv["head"], head8) {
			if g := scoreRx.FindStringSubmatch(t.kv["score"]); g != nil {
				out = g[1]
			}
		}
	}
	return out
}
