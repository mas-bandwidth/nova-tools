// Package stream is the stream lander (nova-tools#3598; the PR records of
// #3611-#3613): the flow Rowan lands by hand, as verbs with all state in
// Redis. Members of one work stream land oldest first onto one stream branch
// off the base, the batch is tested once, a red batch is bisected to the one
// member that turns it red and that member is parked, ONE stream PR goes to
// the base, and on green CI it merges and every member moves from merging to
// landed in one Lua call.
//
// Keys (specs/ws-index.md in rowan-new for the ws sets; the rest here):
//
//	pr:<name>:<n>          hash  repo, n, head, base, base_sha, state (open|parked|landed|merged|closed),
//	                             ci (pending|green|red), mergeable (true|false|""), stream, task, kind,
//	                             reads (typed SCORE/DISPOSITION/HOLD lines, newline-joined),
//	                             created_at, updated_at; park, landed_with, close, closed_at on moves
//	land:<repo>:<slug>     hash  streams, slug, base, base_sha, branch, head, members (<n>@<head> ...),
//	                             tasks (ids, same order), parked (<n>:<why> ...), pr, state
//	                             (conflict|base-red|empty|pushed|open|merged), tests, at, workdir
//	land:<repo>:streams    set   every slug with a land hash
//	cfg:land               hash  min_score, min_score:<repo>, remote:<repo>
//	cfg:land:test:<repo>   string the repo's batch test command (bash -c)
//
// Every write goes through land_stream.lua (one EVAL, atomic). Reads are
// pipelined: one round trip per batch, never a SCAN.
package stream

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

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
	Exists    bool
}

func prFrom(repo string, n int, m map[string]string) PR {
	p := PR{Repo: repo, N: n, Exists: len(m) > 0,
		Head: m["head"], Base: m["base"], BaseSHA: m["base_sha"], State: m["state"], CI: m["ci"],
		Mergeable: m["mergeable"], Stream: m["stream"], Task: m["task"], Kind: m["kind"], ClosedAt: m["closed_at"]}
	for _, l := range strings.Split(m["reads"], "\n") {
		if l = strings.TrimSpace(l); l != "" {
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

// LoadPRs reads records in one pipelined round trip.
func LoadPRs(ctx context.Context, c redis.Cmdable, repo string, ns []int) ([]PR, error) {
	pipe := c.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(ns))
	for i, n := range ns {
		cmds[i] = pipe.HGetAll(ctx, PRKey(repo, n))
	}
	if len(ns) > 0 {
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
	}
	out := make([]PR, len(ns))
	for i, n := range ns {
		out[i] = prFrom(repo, n, cmds[i].Val())
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
}

func (f RecordFields) m() map[string]string {
	return map[string]string{"head": f.Head, "base": f.Base, "base_sha": f.BaseSHA, "stream": f.Stream,
		"ci": f.CI, "mergeable": f.Mergeable, "state": f.State, "task": f.Task, "kind": f.Kind}
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
	Task    string
	Stream  string
	N       int
	Head    string
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
}

// LoadConfig reads cfg:land (min_score:<repo>, min_score, remote:<repo>) and
// cfg:land:test:<repo> in one round trip. The default score floor is 8.
func LoadConfig(ctx context.Context, c redis.Cmdable, repo string) (Config, error) {
	pipe := c.Pipeline()
	h := pipe.HMGet(ctx, "cfg:land", "min_score:"+repo, "min_score", "remote:"+repo)
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
// carry a read at head >= minScore and no hold at head, oldest pr_ready_at
// first, for each stream in the order given. Three pipelined round trips.
func Members(ctx context.Context, c redis.Cmdable, repo string, streams []string, minScore int) ([]Member, []Skip, error) {
	pipe := c.Pipeline()
	zs := make([]*redis.ZSliceCmd, len(streams))
	for i, s := range streams {
		zs[i] = pipe.ZRangeWithScores(ctx, WSKey(s, "merging"), 0, -1)
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, nil, err
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
	// Streams in the order named, oldest pr_ready_at first within each,
	// equal times by PR number.
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
	return f
}

func landingFrom(repo string, m map[string]string) Landing {
	l := Landing{Repo: repo, Slug: m["slug"], Streams: m["streams"], Base: m["base"], BaseSHA: m["base_sha"],
		Branch: m["branch"], Head: m["head"], State: m["state"], Workdir: m["workdir"], At: m["at"],
		MergeSHA: m["merge_sha"]}
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
	if l.PR > 0 {
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

// CloseLine is the member close receipt.
func CloseLine(by, branch, head, repo string, pr int) string {
	return fmt.Sprintf("CLOSE who=%s: in %s at %s; landed with %s#%d", by, branch, short(head), repo, pr)
}

// SaveLanded moves every member of the landing from merging to landed with
// its CLOSE receipt, marks the land hash and the stream PR merged and logs
// the landing, in one Lua call. already is true when it was merged before.
func SaveLanded(ctx context.Context, c redis.Scripter, l Landing, by, mergeSHA string) (moved, missing int, already bool, err error) {
	members := make([]map[string]string, 0, len(l.Members))
	for _, m := range l.Members {
		members = append(members, map[string]string{"task": m.Task, "stream": m.Stream, "n": strconv.Itoa(m.N),
			"close": CloseLine(by, l.Branch, l.Head, l.Repo, l.PR)})
	}
	var mem any = members
	if len(members) == 0 {
		mem = []any{}
	}
	why := fmt.Sprintf("LANDED %s#%d %s at %s merge=%s members=%d", l.Repo, l.PR, l.Branch, short(l.Head), short(mergeSHA), len(l.Members))
	res, err := eval(ctx, c, "landed", map[string]any{"repo": l.Repo, "slug": l.Slug, "now": now(), "by": by,
		"merge_sha": mergeSHA, "pr": strconv.Itoa(l.PR), "why": why, "members": mem})
	if err != nil {
		return 0, 0, false, err
	}
	if res[0] == "ALREADY" {
		return 0, 0, true, nil
	}
	moved, _ = strconv.Atoi(res[1])
	missing, _ = strconv.Atoi(res[2])
	return moved, missing, false, nil
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
