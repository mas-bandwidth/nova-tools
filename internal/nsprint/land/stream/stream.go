// Package stream is the stream lander (nova-tools#3598; the PR records of
// #3611-#3613): the flow Rowan lands by hand, as verbs with all state in
// Redis. Members of one work stream land oldest first onto one stream branch
// off the base, the batch is tested once, a red batch is bisected to the one
// member that turns it red and that member is parked, ONE stream PR goes to
// the base, and on green CI it merges: every task naming a member PR or an
// issue it closes moves to landed with the member's CLOSE line on its record,
// one fenced nova_sprint call per member (ns_land_member, nova-tools#3779).
//
// Keys (specs/ws-index.md in rowan-new for the ws sets; the rest here):
//
//	pr:<name>:<n>          hash  repo, n, head, base, base_sha, state (open|parked|landed|merged|closed),
//	                             ci (pending|green|red), mergeable (true|false|""), stream, task, kind,
//	                             reads (typed SCORE/DISPOSITION/HOLD lines and Jev's JEV line, newline-joined;
//	                             the lander also reads pr:<name>:<n>:lines, read post's list),
//	                             created_at, updated_at; closes (issues the body closes, "-" none);
//	                             park, landed_with, close, closed_at on moves
//	land:<repo>:<slug>     hash  streams, slug, base, base_sha, branch, head, members (<n>@<head> ...),
//	                             tasks (ids, same order), parked (<n>:<why> ...), pr, state
//	                             (conflict|base-red|empty|pushed|open|merged), tests, at, workdir
//	land:<repo>:streams    set   every slug with a land hash
//	cfg:land               hash  min_score, min_score:<repo>, remote:<repo>, jev (off|gate|require),
//	                             jev_passes (the JEV passes that gate, comma list)
//	cfg:land:test:<repo>   string the repo's batch test command (bash -c)
//
// Every write goes through land_stream.lua (one EVAL, atomic) or, for a
// member's task moves, the nova_sprint library. Reads are pipelined: one
// round trip per batch, never a SCAN.
package stream

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/jev"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleetbuild"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

//go:embed land_stream.lua
var luaSource string

var script = redis.NewScript(luaSource)

// PRKey is the per-PR record the lander reads: the one PR record key,
// pr:<name>:<n> with the bare repository name (internal/nsprint/prkey), so
// the lander's owner/name and read's and ci's bare name hit the same hash.
// land_stream.lua's prkey mirrors it.
func PRKey(repo string, n int) string { return prkey.Key(repo, n) }

// LinesKey is the record's typed-line list: `read post` appends each line
// it stores there (internal/nsprint/read, LinesKey), where the record's
// reads field never sees it. The lander reads both (nova-tools #3898).
func LinesKey(repo string, n int) string { return PRKey(repo, n) + ":lines" }

// LandKey is one stream landing.
func LandKey(repo, slug string) string { return "land:" + repo + ":" + slug }

// IndexKey lists every slug with a land hash for the repo.
func IndexKey(repo string) string { return "land:" + repo + ":streams" }

// WSKey is one ws-index set of a stream.
func WSKey(stream, state string) string { return "ws:" + stream + ":" + state }

// reserved slugs would collide with the lander's own land:<repo>:<word> keys.
var reserved = map[string]bool{"streams": true, "gates": true, "events": true, "tok": true}

// Slug is a stream name as a branch- and key-safe word: "swarm: cards" is
// swarm-cards. Several streams landing as one branch (a strict up-to-date
// base) join their slugs with "+".
func Slug(streams ...string) (string, error) {
	parts := make([]string, 0, len(streams))
	for _, s := range streams {
		var b strings.Builder
		dash := false
		for _, r := range strings.ToLower(s) {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
				b.WriteRune(r)
				dash = false
			default:
				if b.Len() > 0 && !dash {
					b.WriteByte('-')
					dash = true
				}
			}
		}
		w := strings.TrimRight(b.String(), "-")
		if w == "" {
			return "", fmt.Errorf("stream %q has no letters or digits for a slug", s)
		}
		parts = append(parts, w)
	}
	if len(parts) == 0 {
		return "", errors.New("no stream named")
	}
	slug := strings.Join(parts, "+")
	if reserved[slug] {
		return "", fmt.Errorf("slug %q is a reserved land:<repo>:%s key", slug, slug)
	}
	return slug, nil
}

// PR is one pr:<repo>:<n> record.
type PR struct {
	Repo      string
	N         int
	Head      string
	Base      string
	BaseSHA   string
	State     string
	CI        string
	Mergeable string
	Stream    string
	Task      string
	Kind      string
	Reads     []string
	ClosedAt  string
	// Closes is the issues the PR's body closes, space-joined; "-" for
	// none, "" when nobody has recorded them (the lander reads the body).
	Closes string
	// CommitCloses is the issues its commit messages close (land stream).
	CommitCloses string
	// IssuesClosed is the issues the lander has closed for this PR.
	IssuesClosed string
	Exists       bool
}

func prFrom(repo string, n int, m map[string]string, list []string) PR {
	p := PR{Repo: repo, N: n, Exists: len(m) > 0,
		Head: m["head"], Base: m["base"], BaseSHA: m["base_sha"], State: m["state"], CI: m["ci"],
		Mergeable: m["mergeable"], Stream: m["stream"], Task: m["task"], Kind: m["kind"], ClosedAt: m["closed_at"],
		Closes: m["closes"], CommitCloses: m["commit_closes"], IssuesClosed: m["issues_closed"]}
	// The reads field's lines first, then every line of the lines list not
	// already there: a SCORE line `read post` stored counts the same as one
	// `pr lines --add` stored (nova-tools #3898: 19 read PRs sat in merging
	// with their SCORE lines only in the list).
	seen := map[string]bool{}
	for _, l := range append(strings.Split(m["reads"], "\n"), list...) {
		if l = strings.TrimSpace(l); l != "" && !seen[l] {
			seen[l] = true
			p.Reads = append(p.Reads, l)
		}
	}
	return p
}

// MergeableOK is the record's mergeable word read as a yes.
func (p PR) MergeableOK() bool {
	switch strings.ToLower(strings.TrimSpace(p.Mergeable)) {
	case "true", "yes", "mergeable", "clean":
		return true
	}
	return false
}

// LoadPRs reads records and their lines lists in one pipelined round trip.
func LoadPRs(ctx context.Context, c redis.Cmdable, repo string, ns []int) ([]PR, error) {
	pipe := c.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(ns))
	lists := make([]*redis.StringSliceCmd, len(ns))
	for i, n := range ns {
		cmds[i] = pipe.HGetAll(ctx, PRKey(repo, n))
		lists[i] = pipe.LRange(ctx, LinesKey(repo, n), 0, -1)
	}
	if len(ns) > 0 {
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
	}
	out := make([]PR, len(ns))
	for i, n := range ns {
		out[i] = prFrom(repo, n, cmds[i].Val(), lists[i].Val())
	}
	return out, nil
}

func now() string { return strconv.FormatInt(time.Now().UnixMilli(), 10) }

func eval(ctx context.Context, c redis.Scripter, op string, payload any) ([]string, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	res, err := script.Run(ctx, c, nil, op, string(b)).StringSlice()
	if err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return nil, fmt.Errorf("land_stream.lua %s: empty reply", op)
	}
	if res[0] == "REFUSED" {
		why := ""
		if len(res) > 1 {
			why = res[1]
		}
		return res, &RefusedError{Why: why}
	}
	return res, nil
}

// RefusedError is a REFUSED reply from land_stream.lua.
type RefusedError struct{ Why string }

func (e *RefusedError) Error() string { return "REFUSED " + e.Why }

// RecordFields are the settable fields of a pr record; empty means unchanged.
type RecordFields struct {
	Head, Base, BaseSHA, Stream, CI, Mergeable, State, Task, Kind string
	// Closes is the issues the PR's body closes (space-joined, "-" none).
	Closes string
	// BranchGone marks the PR's head branch gone (branch_gone): pr reap
	// (internal/nsprint/reap) closes such a PR.
	BranchGone string
}

func (f RecordFields) m() map[string]string {
	return map[string]string{"head": f.Head, "base": f.Base, "base_sha": f.BaseSHA, "stream": f.Stream,
		"ci": f.CI, "mergeable": f.Mergeable, "state": f.State, "task": f.Task, "kind": f.Kind, "closes": f.Closes,
		"branch_gone": f.BranchGone}
}

// RecordResult is the record after the write.
type RecordResult struct {
	Created                                  bool
	Head, Base, Stream, CI, Mergeable, State string
}

// Record creates or updates pr:<repo>:<n> in one call. A new record needs
// head, base and stream; a new head resets ci to pending and mergeable to
// unknown unless the call names them.
func Record(ctx context.Context, c redis.Scripter, repo string, n int, f RecordFields) (RecordResult, error) {
	res, err := eval(ctx, c, "record", map[string]any{"repo": repo, "n": strconv.Itoa(n), "now": now(), "fields": f.m()})
	if err != nil {
		return RecordResult{}, err
	}
	for len(res) < 8 {
		res = append(res, "")
	}
	return RecordResult{Created: res[1] == "1", Head: res[2], Base: res[3], Stream: res[4], CI: res[5], Mergeable: res[6], State: res[7]}, nil
}

// AddLine appends one typed line to the record's reads and returns the count.
func AddLine(ctx context.Context, c redis.Scripter, repo string, n int, line string) (int, error) {
	line = strings.TrimSpace(line)
	if line == "" || strings.ContainsAny(line, "\r\n") {
		return 0, errors.New("a typed line is one non-empty line")
	}
	res, err := eval(ctx, c, "line", map[string]any{"repo": repo, "n": strconv.Itoa(n), "now": now(), "line": line})
	if err != nil {
		return 0, err
	}
	k, _ := strconv.Atoi(res[1])
	return k, nil
}

// Read is the verdict of a record's typed lines at its head.
type Read struct {
	Who   string
	Score int
	Held  string // who holds it at head, "" when nobody
}

// ReadAt reads typed lines at head: SCORE who=<w> head=<sha> score=N[/10],
// DISPOSITION who=<w> head=<sha> verdict=APPROVE|HOLD score=N, and
// HOLD who=<w> head=<sha>. A line counts only at the record's head (a prefix
// of at least 7 hex digits). Per who the last line wins, so a re-read after a
// REPAIR releases that who's hold. Jev lines never count as reads.
func ReadAt(lines []string, head string) Read {
	type last struct {
		hold  bool
		score int
	}
	byWho := map[string]last{}
	var order []string
	atHead := func(who, h string) bool {
		return who != "" && !strings.HasPrefix(who, "jev") && len(h) >= 7 && strings.HasPrefix(strings.ToLower(head), h)
	}
	for _, l := range lines {
		var who string
		var cur last
		// DISPOSITION lines go through the one typed parser (#2506 part B):
		// a HOLD holds even when sloppily typed, an APPROVE counts only when
		// the parser finds it valid.
		if c, ok := typedrec.ParseDisposition(l); ok {
			who = c.Who
			if !atHead(who, c.Head) {
				continue
			}
			switch {
			case c.Verdict == "HOLD":
				cur.hold = true
			case c.Verdict == "APPROVE" && c.Valid:
				cur.score = parseScore(c.Score)
			default:
				continue
			}
		} else {
			f := strings.Fields(l)
			if len(f) == 0 {
				continue
			}
			kv := map[string]string{}
			for _, w := range f[1:] {
				if k, v, ok := strings.Cut(w, "="); ok {
					kv[k] = strings.TrimRight(v, ":,;")
				}
			}
			who = strings.ToLower(kv["who"])
			if !atHead(who, strings.ToLower(kv["head"])) {
				continue
			}
			switch f[0] {
			case "SCORE":
				cur.score = parseScore(kv["score"])
			case "HOLD":
				cur.hold = true
			default:
				continue
			}
		}
		if _, seen := byWho[who]; !seen {
			order = append(order, who)
		}
		byWho[who] = cur
	}
	r := Read{Score: -1}
	for _, who := range order {
		v := byWho[who]
		if v.hold {
			if r.Held == "" {
				r.Held = who
			}
			continue
		}
		if v.score > r.Score {
			r.Score, r.Who = v.score, who
		}
	}
	return r
}

func parseScore(s string) int {
	s, _, _ = strings.Cut(s, "/")
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 || n > 10 {
		return -1
	}
	return n
}

// Member is one PR of the stream that lands.
type Member struct {
	Task   string
	Stream string
	N      int
	Head   string
	// ReadyAt is the member's score in ws:<stream>:merging: the stream's
	// work order. Today the move writes the task's created_at there; #4342
	// writes the computed order (DEPENDS-ON, then PATHS overlap, then issue
	// number) as that score, and the lander orders by it unchanged: the
	// score is the one order, never read time.
	ReadyAt int64
	Who     string
	Score   int
}

// Skip is a task in merging that does not land in this batch, and why.
type Skip struct {
	Task string
	N    int
	Why  string
}

// Config is the land config for a repo.
type Config struct {
	MinScore int
	Remote   string
	Test     string
	// Partial is cfg:land partial (1|true|yes): the land duty opens a
	// stream PR without the members that cannot land, printing LAND-SERIAL
	// as allowed. Off, the duty refuses the stream with that line until
	// every merging member can land (nova-tools #4324).
	Partial bool
}

// LoadConfig reads cfg:land (min_score:<repo>, min_score, remote:<repo>,
// partial) and cfg:land:test:<repo> in one round trip. The default score
// floor is 8.
func LoadConfig(ctx context.Context, c redis.Cmdable, repo string) (Config, error) {
	pipe := c.Pipeline()
	h := pipe.HMGet(ctx, "cfg:land", "min_score:"+repo, "min_score", "remote:"+repo, "partial")
	t := pipe.Get(ctx, "cfg:land:test:"+repo)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return Config{}, err
	}
	cfg := Config{MinScore: 8, Test: strings.TrimSpace(t.Val())}
	vals := h.Val()
	for _, v := range vals[:2] {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
				cfg.MinScore = n
				break
			}
		}
	}
	if s, ok := vals[2].(string); ok {
		cfg.Remote = strings.TrimSpace(s)
	}
	if s, ok := vals[3].(string); ok {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "1", "true", "yes", "on":
			cfg.Partial = true
		}
	}
	return cfg, nil
}

// prNumber reads a task's pr field (123, #123, <repo>#123, or a pulls URL)
// and reports whether it names this repo (a bare number does).
func prNumber(field, repo string) (int, bool) {
	field = strings.TrimSpace(field)
	if field == "" {
		return 0, false
	}
	if i := strings.LastIndex(field, "/pull/"); i >= 0 {
		prefix := field[:i]
		if !strings.HasSuffix(prefix, "/"+repo) && !strings.HasSuffix(prefix, repo) {
			return 0, false
		}
		n, err := strconv.Atoi(strings.Trim(field[i+len("/pull/"):], "/"))
		return n, err == nil && n > 0
	}
	if pre, num, ok := strings.Cut(field, "#"); ok {
		if pre != "" && pre != repo && !strings.HasSuffix(repo, "/"+pre) {
			return 0, false
		}
		field = num
	}
	n, err := strconv.Atoi(field)
	return n, err == nil && n > 0
}

// Members is ws:<stream>:merging intersected with the repo's pr records that
// carry a read at head >= minScore, no hold at head and no failing JEV line
// at head, oldest pr_ready_at first, for each stream in the order given.
// Three pipelined round trips.
//
// The JEV line (internal/jev, nova-tools#3631) is Jev's mechanical passes
// over the PR, never a read: cfg:land jev is off, gate (the default: a
// gating pass that failed at head skips the PR as jev:<passes>) or require
// (a PR Jev has not passed at head skips as no-jev-at-head too), and
// cfg:land jev_passes names the passes that gate (empty: every pass).
func Members(ctx context.Context, c redis.Cmdable, repo string, streams []string, minScore int) ([]Member, []Skip, error) {
	pipe := c.Pipeline()
	zs := make([]*redis.ZSliceCmd, len(streams))
	for i, s := range streams {
		zs[i] = pipe.ZRangeWithScores(ctx, WSKey(s, "merging"), 0, -1)
	}
	jevCfg := pipe.HMGet(ctx, "cfg:land", "jev", "jev_passes")
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, nil, err
	}
	jevMode, jevGating := jev.ModeGate, map[string]bool(nil)
	if v := jevCfg.Val(); len(v) == 2 {
		s0, _ := v[0].(string)
		s1, _ := v[1].(string)
		jevMode, jevGating = jev.ParseMode(s0), jev.ParseGating(s1)
	}
	type cand struct {
		task, stream string
		at           int64
	}
	var cands []cand
	for i, s := range streams {
		for _, z := range zs[i].Val() {
			cands = append(cands, cand{task: fmt.Sprint(z.Member), stream: s, at: int64(z.Score)})
		}
	}
	if len(cands) == 0 {
		return nil, nil, nil
	}
	pipe = c.Pipeline()
	tc := make([]*redis.StringCmd, len(cands))
	for i, cd := range cands {
		tc[i] = pipe.HGet(ctx, "task:"+cd.task, "pr")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, nil, err
	}
	var skips []Skip
	ns := make([]int, len(cands))
	var want []int
	for i, cd := range cands {
		n, ok := prNumber(tc[i].Val(), repo)
		if !ok {
			if tc[i].Val() == "" {
				skips = append(skips, Skip{Task: cd.task, Why: "no-pr"})
			}
			continue // another repo's PR is not a skip here
		}
		ns[i] = n
		want = append(want, n)
	}
	recs, err := LoadPRs(ctx, c, repo, want)
	if err != nil {
		return nil, nil, err
	}
	byN := map[int]PR{}
	for _, r := range recs {
		byN[r.N] = r
	}
	var out []Member
	for i, cd := range cands {
		n := ns[i]
		if n == 0 {
			continue
		}
		r := byN[n]
		why := ""
		read := ReadAt(r.Reads, r.Head)
		jevWhy := jev.Skip(r.Reads, r.Head, jevMode, jevGating)
		switch {
		case !r.Exists:
			why = "no-record"
		case r.Stream != "" && r.Stream != cd.stream:
			why = "stream:" + r.Stream
		case r.State != "" && r.State != "open":
			why = "state:" + r.State
		case r.Head == "":
			why = "no-head"
		case read.Held != "":
			why = "hold:" + read.Held
		case jevWhy != "":
			why = jevWhy
		case read.Score < 0:
			why = "no-read-at-head"
		case read.Score < minScore:
			why = fmt.Sprintf("score:%d<%d", read.Score, minScore)
		}
		if why != "" {
			skips = append(skips, Skip{Task: cd.task, N: n, Why: why})
			continue
		}
		out = append(out, Member{Task: cd.task, Stream: cd.stream, N: n, Head: r.Head, ReadyAt: cd.at, Who: read.Who, Score: read.Score})
	}
	// Streams in the order named, the ws ZSET score first within each (the
	// work order, #4342), equal scores by PR number.
	rank := map[string]int{}
	for i, s := range streams {
		if _, ok := rank[s]; !ok {
			rank[s] = i
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if rank[a.Stream] != rank[b.Stream] {
			return rank[a.Stream] < rank[b.Stream]
		}
		if a.ReadyAt != b.ReadyAt {
			return a.ReadyAt < b.ReadyAt
		}
		return a.N < b.N
	})
	return out, skips, nil
}

// Parked is a member the batch test bisected out, or a conflict.
type Parked struct {
	Member
	Why string
}

// Landing is one land:<repo>:<slug> hash.
type Landing struct {
	Repo, Slug, Streams, Base, BaseSHA, Branch, Head string
	Members                                          []Member
	Parked                                           []Parked
	PR                                               int
	State                                            string
	Tests                                            int
	Workdir                                          string
	At                                               string
	MergeSHA                                         string
	// CommitCloses is, per kept member, the issues its commit messages
	// close; SaveBuilt stores it on the member's record (commit_closes).
	CommitCloses map[int]string
	// Serial is the LAND-SERIAL line of a landing that carries fewer
	// members than the stream has in merging (nova-tools #4324); PartialBy
	// and PartialAt say who allowed it (--partial) and when. State serial
	// is a build the line refused after the parks.
	Serial, PartialBy, PartialAt string
	// SerialLeft is the members a serial record left out (stored as
	// serial_left, <n>:<task> each): the next run refuses while any of them
	// is live outside merging (NotBack).
	SerialLeft []Skip
}

func (l Landing) fields() map[string]string {
	var mem, tasks, streams, parked []string
	for _, m := range l.Members {
		mem = append(mem, fmt.Sprintf("%d@%s", m.N, m.Head))
		tasks = append(tasks, m.Task)
		streams = append(streams, m.Stream)
	}
	for _, p := range l.Parked {
		parked = append(parked, fmt.Sprintf("%d:%s", p.N, p.Why))
	}
	f := map[string]string{
		"repo": l.Repo, "slug": l.Slug, "streams": l.Streams, "base": l.Base, "base_sha": l.BaseSHA,
		"branch": l.Branch, "head": l.Head, "members": strings.Join(mem, " "), "tasks": strings.Join(tasks, " "),
		"member_streams": strings.Join(streams, "\n"), "parked": strings.Join(parked, " "),
		"state": l.State, "tests": strconv.Itoa(l.Tests), "workdir": l.Workdir, "at": now(),
	}
	if l.PR > 0 {
		f["pr"] = strconv.Itoa(l.PR)
	}
	if l.Serial != "" {
		f["serial"], f["partial_by"], f["partial_at"] = l.Serial, l.PartialBy, l.PartialAt
	}
	if len(l.SerialLeft) > 0 {
		left := make([]string, 0, len(l.SerialLeft))
		for _, sk := range l.SerialLeft {
			left = append(left, fmt.Sprintf("%d:%s", sk.N, sk.Task))
		}
		f["serial_left"] = strings.Join(left, " ")
	}
	return f
}

func landingFrom(repo string, m map[string]string) Landing {
	l := Landing{Repo: repo, Slug: m["slug"], Streams: m["streams"], Base: m["base"], BaseSHA: m["base_sha"],
		Branch: m["branch"], Head: m["head"], State: m["state"], Workdir: m["workdir"], At: m["at"],
		MergeSHA: m["merge_sha"], Serial: m["serial"], PartialBy: m["partial_by"], PartialAt: m["partial_at"]}
	l.PR, _ = strconv.Atoi(m["pr"])
	l.Tests, _ = strconv.Atoi(m["tests"])
	mem := strings.Fields(m["members"])
	tasks := strings.Fields(m["tasks"])
	streams := strings.Split(m["member_streams"], "\n")
	for i, w := range mem {
		ns, head, _ := strings.Cut(w, "@")
		n, _ := strconv.Atoi(ns)
		mb := Member{N: n, Head: head}
		if i < len(tasks) {
			mb.Task = tasks[i]
		}
		if i < len(streams) {
			mb.Stream = streams[i]
		}
		l.Members = append(l.Members, mb)
	}
	for _, w := range strings.Fields(m["serial_left"]) {
		ns, task, _ := strings.Cut(w, ":")
		n, _ := strconv.Atoi(ns)
		l.SerialLeft = append(l.SerialLeft, Skip{Task: task, N: n})
	}
	for _, w := range strings.Fields(m["parked"]) {
		ns, why, _ := strings.Cut(w, ":")
		n, _ := strconv.Atoi(ns)
		l.Parked = append(l.Parked, Parked{Member: Member{N: n}, Why: why})
	}
	return l
}

// LoadLanding reads one land hash; ok is false when there is none.
func LoadLanding(ctx context.Context, c redis.Cmdable, repo, slug string) (Landing, bool, error) {
	m, err := c.HGetAll(ctx, LandKey(repo, slug)).Result()
	if err != nil {
		return Landing{}, false, err
	}
	if len(m) == 0 {
		return Landing{}, false, nil
	}
	return landingFrom(repo, m), true, nil
}

// LoadLandings reads every land hash of the repo from the index: two round trips.
func LoadLandings(ctx context.Context, c redis.Cmdable, repo string) ([]Landing, error) {
	slugs, err := c.SMembers(ctx, IndexKey(repo)).Result()
	if err != nil {
		return nil, err
	}
	sort.Strings(slugs)
	pipe := c.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(slugs))
	for i, s := range slugs {
		cmds[i] = pipe.HGetAll(ctx, LandKey(repo, s))
	}
	if len(slugs) > 0 {
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
	}
	var out []Landing
	for _, cmd := range cmds {
		if m := cmd.Val(); len(m) > 0 {
			out = append(out, landingFrom(repo, m))
		}
	}
	return out, nil
}

// NotBack is the members a serial landing record left out that are live
// outside merging now (waiting, ready, working, review or parked): each is
// a Skip with why not-back:<where>. A member in merging again (it is in
// members or skips), landed, done or gone is back. One round trip, none
// when prev is not a serial record.
func NotBack(ctx context.Context, c redis.Cmdable, prev Landing, members []Member, skips []Skip) ([]Skip, error) {
	if prev.State != "serial" || len(prev.SerialLeft) == 0 {
		return nil, nil
	}
	here := map[string]bool{}
	for _, m := range members {
		here[m.Task] = true
	}
	for _, sk := range skips {
		here[sk.Task] = true
	}
	var ask []Skip
	pipe := c.Pipeline()
	var cmds []*redis.StringCmd
	for _, sk := range prev.SerialLeft {
		if sk.Task == "" || here[sk.Task] {
			continue
		}
		ask = append(ask, sk)
		cmds = append(cmds, pipe.HGet(ctx, "task:"+sk.Task, "state"))
	}
	if len(ask) == 0 {
		return nil, nil
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	var out []Skip
	for i, sk := range ask {
		switch st := cmds[i].Val(); st {
		case "waiting", "ready", "working", "review", "parked":
			out = append(out, Skip{Task: sk.Task, N: sk.N, Why: "not-back:" + st})
		}
	}
	return out, nil
}

// SaveBuilt writes the landing hash, the stream PR's own record (when it has
// a PR) and parks every bisected member (merging -> working, ws:log receipt,
// its pr record state=parked) in one Lua call. It returns how many parked
// tasks moved.
func SaveBuilt(ctx context.Context, c redis.Scripter, l Landing, by string) (int, error) {
	var parked []map[string]string
	for _, p := range l.Parked {
		if p.Task == "" {
			continue
		}
		parked = append(parked, map[string]string{"task": p.Task, "stream": p.Stream, "n": strconv.Itoa(p.N),
			"why": fmt.Sprintf("PARKED %s#%d %s in %s at %s", l.Repo, p.N, p.Why, l.Branch, short(l.Head))})
	}
	payload := map[string]any{"repo": l.Repo, "slug": l.Slug, "now": now(), "by": by, "land": l.fields(), "parked": parked}
	if parked == nil {
		payload["parked"] = []any{}
	}
	commits := []any{}
	for _, m := range l.Members {
		if cl, ok := l.CommitCloses[m.N]; ok {
			commits = append(commits, map[string]string{"n": strconv.Itoa(m.N), "closes": cl})
		}
	}
	payload["commit_closes"] = commits
	// The stream PR's record follows an open landing's pushed head only: a
	// serial record keeps the PR number but pushed nothing.
	if l.PR > 0 && l.State == "open" {
		payload["stream_pr"] = map[string]any{"n": strconv.Itoa(l.PR), "fields": map[string]string{
			"repo": l.Repo, "n": strconv.Itoa(l.PR), "head": l.Head, "base": l.Base, "base_sha": l.BaseSHA,
			"stream": l.Streams, "kind": "stream", "state": "open", "ci": "pending", "mergeable": "",
			"slug": l.Slug, "updated_at": now()}}
	}
	res, err := eval(ctx, c, "built", payload)
	if err != nil {
		return 0, err
	}
	n, _ := strconv.Atoi(res[1])
	return n, nil
}

// LanderWho is the who= of the lander's own typed lines.
const LanderWho = "lander"

// CloseLine is a member's CLOSE typed line (nova-tools#3779): on its pr
// record, as its GitHub comment and in its close field. head is the member's
// head; branch and streamHead the stream branch it landed in; repo#pr the
// stream PR and mergeSHA its merge commit.
func CloseLine(head, branch, streamHead, repo string, pr int, mergeSHA string) string {
	return fmt.Sprintf("CLOSE who=%s head=%s landed: in %s at %s with %s (%s)", LanderWho, short(head), branch, short(streamHead), LandedWith(repo, pr), short(mergeSHA))
}

// LandedWith is repo#pr with the bare repository name, as the CLOSE line
// and every landed task's why name the stream PR.
func LandedWith(repo string, pr int) string {
	return fmt.Sprintf("%s#%d", prkey.Name(repo), pr)
}

// SaveLanded marks the land hash and the stream PR merged, stamps every
// member record landed with its CLOSE line and logs the landing, in one Lua
// call; it moves no task (LandMembers does, per member). already is true
// when it was merged before.
func SaveLanded(ctx context.Context, c redis.Scripter, l Landing, by, mergeSHA string) (already bool, err error) {
	already, _, err = saveLanded(ctx, c, l, by, mergeSHA)
	return already, err
}

// ReleasesOn says whether a landing of repo into base names a fleet release:
// nova-tools into dev (#4050), with a full merge sha.
func ReleasesOn(repo, base, mergeSHA string) bool {
	return prkey.Name(repo) == fleetbuild.ReleaseRepo && base == fleetbuild.ReleaseBase && fullSHA.MatchString(mergeSHA)
}

var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// saveLanded is SaveLanded, returning the release version it wrote to
// fleet:release ("" when the landing names none or was merged before).
func saveLanded(ctx context.Context, c redis.Scripter, l Landing, by, mergeSHA string) (already bool, release string, err error) {
	members := make([]map[string]string, 0, len(l.Members))
	for _, m := range l.Members {
		members = append(members, map[string]string{"task": m.Task, "stream": m.Stream, "n": strconv.Itoa(m.N),
			"close": CloseLine(m.Head, l.Branch, l.Head, l.Repo, l.PR, mergeSHA)})
	}
	var mem any = members
	if len(members) == 0 {
		mem = []any{}
	}
	why := fmt.Sprintf("LANDED %s#%d %s at %s merge=%s members=%d", l.Repo, l.PR, l.Branch, short(l.Head), short(mergeSHA), len(l.Members))
	payload := map[string]any{"repo": l.Repo, "slug": l.Slug, "now": now(), "by": by,
		"merge_sha": mergeSHA, "pr": strconv.Itoa(l.PR), "why": why, "members": mem}
	if ReleasesOn(l.Repo, l.Base, mergeSHA) {
		payload["release"] = map[string]string{"key": fleetbuild.ConfigKey, "train": fleetbuild.DefaultTrain,
			"landed": LandedWith(l.Repo, l.PR)}
	}
	res, err := eval(ctx, c, "landed", payload)
	if err != nil {
		return false, "", err
	}
	if res[0] == "ALREADY" {
		return true, "", nil
	}
	if len(res) > 1 {
		release = res[1]
	}
	return false, release, nil
}

// FunctionLandMember is the nova_sprint library function that lands one
// member (internal/nsprint/fn/lua/03_task_event.lua).
const FunctionLandMember = "ns_land_member"

// Landed is what LandMembers moved.
type Landed struct {
	Moved   int      // tasks moved to landed
	Missing int      // members no task names
	Lines   int      // CLOSE lines added to member records
	Skipped []string // "id: why" for tasks the move refused
}

// LandMembers runs ns_land_member for every member of a merged landing in
// one pipeline (one fenced Lua call per member): the CLOSE line on its
// record, and every task naming the member PR or an issue it closes moved to
// landed with why "landed with <repo>#<pr> (<merge sha8>)". closes maps a
// member to the issues its body closes ("-" none); a member missing from it
// keeps its record's closes field. A re-run adds and moves nothing twice.
func LandMembers(ctx context.Context, c redis.Cmdable, l Landing, by, mergeSHA string, closes map[int]string) (Landed, error) {
	var out Landed
	if len(l.Members) == 0 {
		return out, nil
	}
	why := fmt.Sprintf("landed with %s (%s)", LandedWith(l.Repo, l.PR), short(mergeSHA))
	pipe := c.Pipeline()
	cmds := make([]*redis.Cmd, len(l.Members))
	for i, m := range l.Members {
		cmds[i] = pipe.FCall(ctx, FunctionLandMember, nil, l.Repo, l.Slug, mergeSHA, strconv.Itoa(m.N), m.Task,
			CloseLine(m.Head, l.Branch, l.Head, l.Repo, l.PR, mergeSHA), by, why, closes[m.N])
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return out, fmt.Errorf("%s: %w", FunctionLandMember, err)
	}
	for i, m := range l.Members {
		res, err := cmds[i].StringSlice()
		if err != nil {
			return out, fmt.Errorf("%s #%d: %w", FunctionLandMember, m.N, err)
		}
		if len(res) < 6 || res[0] != "OK" {
			return out, fmt.Errorf("%s #%d: %s", FunctionLandMember, m.N, strings.Join(res, " "))
		}
		moved, _ := strconv.Atoi(res[1])
		matched, _ := strconv.Atoi(res[4])
		lines, _ := strconv.Atoi(res[5])
		out.Moved += moved
		out.Lines += lines
		if matched == 0 {
			out.Missing++
		}
		out.Skipped = append(out.Skipped, res[6:]...)
	}
	return out, nil
}

// closesRx is GitHub's closing keywords on a same-repository issue.
var closesRx = regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\s*:?\s+#([0-9]+)\b`)

// UnionCloses joins two closes values (numbers space-joined, "-" none, ""
// unknown): the numbers of both, each once, in order; "-" when both say
// none; "" when a is unknown and b names none.
func UnionCloses(a, b string) string {
	var out []string
	seen := map[string]bool{}
	for _, x := range append(strings.Fields(a), strings.Fields(b)...) {
		if x != "-" && !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	switch {
	case len(out) > 0:
		return strings.Join(out, " ")
	case a == "":
		return ""
	}
	return "-"
}

// ParseCloses is the issues a PR body closes, space-joined in order, each
// once; "-" when it closes none.
func ParseCloses(body string) string {
	var out []string
	seen := map[string]bool{}
	for _, m := range closesRx.FindAllStringSubmatch(body, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	if len(out) == 0 {
		return "-"
	}
	return strings.Join(out, " ")
}

// IssueCloseLine is the comment the lander closes an issue with: the
// member's CLOSE line (which names the merge sha) and the member that closes
// it.
func IssueCloseLine(head, branch, streamHead, repo string, pr int, mergeSHA string, member int) string {
	return fmt.Sprintf("%s; closes this via %s", CloseLine(head, branch, streamHead, repo, pr, mergeSHA), LandedWith(repo, member))
}

// MarkIssuesClosed adds the issues whose close went through to each member
// record's issues_closed, so a re-run closes none twice.
func MarkIssuesClosed(ctx context.Context, c redis.Cmdable, repo string, closed map[int][]int, recs []PR) error {
	if len(closed) == 0 {
		return nil
	}
	pipe := c.Pipeline()
	for _, r := range recs {
		is := closed[r.N]
		if len(is) == 0 {
			continue
		}
		all := strings.Fields(r.IssuesClosed)
		for _, i := range is {
			all = append(all, strconv.Itoa(i))
		}
		pipe.HSet(ctx, PRKey(repo, r.N), "issues_closed", strings.Join(all, " "))
	}
	_, err := pipe.Exec(ctx)
	return err
}

// MarkClosed stamps closed_at on member records whose close went through.
func MarkClosed(ctx context.Context, c redis.Cmdable, repo string, ns []int) error {
	if len(ns) == 0 {
		return nil
	}
	pipe := c.Pipeline()
	at := now()
	for _, n := range ns {
		pipe.HSet(ctx, PRKey(repo, n), "closed_at", at)
	}
	_, err := pipe.Exec(ctx)
	return err
}

func short(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// Short is the 8-character form of a sha.
func Short(s string) string { return short(s) }
