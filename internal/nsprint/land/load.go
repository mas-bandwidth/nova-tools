package land

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/civerdict"
)

// ErrNoRecord is returned when s:<S>:u:<unit> does not exist, or when
// s:<S>:prunit:<repo>:<n> names no unit (7.7: exit 1).
var ErrNoRecord = errors.New("no unit record")

const (
	defaultAbsentAfter = 60 * time.Minute
	// defaultLandBar is the score an APPROVE needs to count (3.3 (2)).
	defaultLandBar = 8
)

// policy reads the base policy land:<repo>:<base>:policy (2.2). readers
// defaults to 0: the owner ruling in 3.3 (2) (Glenn 6:45 PM ET) puts no read
// gate at landing, and every fleet/land/<repo>.yml sets readers: 0.
func policy(m map[string]string) (readers, bar int, after time.Duration) {
	bar = defaultLandBar
	if n, err := strconv.Atoi(m["readers"]); err == nil && n > 0 {
		readers = n
	}
	if n, err := strconv.Atoi(m["land_bar"]); err == nil && n > 0 {
		bar = n
	}
	after = defaultAbsentAfter
	if d, err := time.ParseDuration(m["absent_after"]); err == nil && d > 0 {
		after = d
	}
	return readers, bar, after
}

// ResolvePR returns the unit s:<S>:prunit:<repo>:<n> names: the PR form of
// `why` resolves to the unit without a scan (7.7).
func ResolvePR(ctx context.Context, c redis.Cmdable, sprint string, id ID) (string, error) {
	key := PRUnitKey(sprint, id.Repo, id.N)
	unit, err := c.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) || (err == nil && strings.TrimSpace(unit) == "") {
		return "", fmt.Errorf("%w: MISSING %s", ErrNoRecord, key)
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", key, err)
	}
	return strings.TrimSpace(unit), nil
}

// LoadPR is `why <repo>#<n>`: it resolves the PR through s:<S>:prunit to its
// unit and loads that unit. The retired s:<S>:pr:<repo>:<n> is never read.
func LoadPR(ctx context.Context, c *redis.Client, sprint string, id ID) (*Unit, error) {
	unit, err := ResolvePR(ctx, c, sprint, id)
	if err != nil {
		return nil, err
	}
	u, err := LoadUnit(ctx, c, sprint, unit)
	if err != nil {
		return nil, err
	}
	u.ID = id
	return u, nil
}

// LoadUnit is `why <unit>`: the unit hash, its reads and holds, the base
// policy, its landable rank, stack parent, batch and CI receipt, in three
// pipelined round trips. Reads and holds are keyed per friend
// (s:<S>:read:<unit>:<f>, s:<S>:hold:<unit>:<f>), enumerated from the
// `friends` set the record functions resolve against (hold.lua), never SCANned.
func LoadUnit(ctx context.Context, c *redis.Client, sprint, unit string) (*Unit, error) {
	ukey := UnitKey(sprint, unit)
	pipe := c.Pipeline()
	uCmd := pipe.HGetAll(ctx, ukey)
	friendsCmd := pipe.SMembers(ctx, "friends")
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("read %s: %w", ukey, err)
	}
	fields := uCmd.Val()
	if len(fields) == 0 {
		return nil, fmt.Errorf("%w: MISSING %s", ErrNoRecord, ukey)
	}
	friends := friendsCmd.Val()
	sort.Strings(friends)
	repo, base := fields["repo"], fields["base"]
	if base == "" {
		base = "dev"
	}
	u := &Unit{
		Unit: unit, Sprint: sprint, Repo: repo, Base: base, Fields: fields,
		Friends: map[string]FriendState{}, Rank: -1,
	}
	if n, err := strconv.Atoi(fields["pr"]); err == nil && n > 0 && repo != "" {
		u.ID = ID{Repo: repo, N: n}
	}

	pipe = c.Pipeline()
	readCmds := make([]*redis.MapStringStringCmd, len(friends))
	holdCmds := make([]*redis.MapStringStringCmd, len(friends))
	for i, f := range friends {
		readCmds[i] = pipe.HGetAll(ctx, ReadKey(sprint, unit, f))
		holdCmds[i] = pipe.HGetAll(ctx, HoldKey(sprint, unit, f))
	}
	polCmd := pipe.HGetAll(ctx, civerdict.PolicyKey(repo, base))
	rankCmd := pipe.ZRank(ctx, LandableKey(sprint, repo, base), unit)
	cardCmd := pipe.ZCard(ctx, LandableKey(sprint, repo, base))
	var tipCmd *redis.StringCmd
	if fields["base_sha"] == "" {
		tipCmd = pipe.HGet(ctx, civerdict.TipKey(repo, base), "sha")
	}
	var parentCmd, batchCmd *redis.MapStringStringCmd
	if sp := parentUnit(fields["stack_parent"]); sp != "" {
		u.Parent = sp
		parentCmd = pipe.HGetAll(ctx, UnitKey(sprint, sp))
	}
	if b := fields["batch"]; b != "" {
		batchCmd = pipe.HGetAll(ctx, BatchKey(repo, base, b))
	}
	var gidsCmd *redis.StringSliceCmd
	head := fields["head"]
	if head != "" {
		gidsCmd = pipe.SMembers(ctx, civerdict.GIDsKey(repo, head))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("read %s: %w", unit, err)
	}
	for i, f := range friends {
		if r, seq, ok := parseUnitRead(f, readCmds[i].Val()); ok {
			u.Reads = append(u.Reads, r)
			u.MaxSeq = max(u.MaxSeq, seq)
		}
		if h, seq, ok := parseUnitHold(f, holdCmds[i].Val()); ok {
			u.Holds = append(u.Holds, h)
			u.MaxSeq = max(u.MaxSeq, seq)
		}
	}
	pol := polCmd.Val()
	u.Readers, u.LandBar, u.AbsentAfter = policy(pol)
	if r, err := rankCmd.Result(); err == nil {
		u.Rank = r
	}
	u.Landable = cardCmd.Val()
	if parentCmd != nil {
		u.ParentRec = parentCmd.Val()
	}
	if batchCmd != nil {
		u.Batch = batchCmd.Val()
	}
	if gidsCmd != nil {
		u.CIGIDs = gidsCmd.Val()
	}

	baseSHA := fields["base_sha"]
	if tipCmd != nil {
		baseSHA = tipCmd.Val()
	}
	pipe = c.Pipeline()
	var ciCmd *redis.MapStringStringCmd
	if head != "" {
		gid, err := civerdict.ExpectedFrom(base, baseSHA, pol["policy_id"], pol["required_set_id"], pol["runner_id"])
		switch {
		case errors.Is(err, civerdict.ErrNoPolicy):
			u.NoPolicy = true
		case err == nil:
			ciCmd = pipe.HGetAll(ctx, civerdict.Key(repo, head, gid))
		}
	}
	friendCmds := map[string]*redis.MapStringStringCmd{}
	for _, h := range u.Holds {
		if _, ok := friendCmds[h.Holder]; !ok && h.Open() {
			friendCmds[h.Holder] = pipe.HGetAll(ctx, "friend:"+h.Holder+":state")
		}
	}
	if ciCmd == nil && len(friendCmds) == 0 {
		return u, nil
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("read %s: %w", unit, err)
	}
	if ciCmd != nil {
		u.CI = ciCmd.Val()
	}
	for f, cmd := range friendCmds {
		u.Friends[f] = parseFriend(cmd.Val())
	}
	return u, nil
}

// parentUnit is the stack parent a unit names: the sexp edge, "" for none.
func parentUnit(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == "none" {
		return ""
	}
	return v
}

// parseUnitRead reads s:<S>:read:<unit>:<who> (ns_read, hold.lua ingest).
func parseUnitRead(who string, m map[string]string) (Read, int64, bool) {
	if len(m) == 0 {
		return Read{}, 0, false
	}
	r := Read{Friend: who, Head: m["head"], Verdict: strings.ToUpper(strings.TrimSpace(m["verdict"]))}
	if n, err := strconv.Atoi(m["score"]); err == nil {
		r.Score, r.HasScore = n, true
	}
	seq, _ := strconv.ParseInt(m["seq"], 10, 64)
	return r, seq, true
}

// parseUnitHold reads s:<S>:hold:<unit>:<holder> (ns_hold, ns_release,
// hold.lua). Its id is h<seq>, the record's own rec:seq stamp.
func parseUnitHold(holder string, m map[string]string) (Hold, int64, bool) {
	if len(m) == 0 {
		return Hold{}, 0, false
	}
	seq, _ := strconv.ParseInt(m["seq"], 10, 64)
	h := Hold{
		ID: "h" + m["seq"], Holder: holder, Head: m["head"], Kind: m["kind"], Reason: m["reason"],
		URL: m["url"], At: m["at"], ReleasedBy: m["released_by"], ReleaseKind: m["release_kind"],
		ReleaseURL: m["release_url"], ReleasedAt: m["released_at"],
	}
	if rs, err := strconv.ParseInt(m["release_seq"], 10, 64); err == nil {
		seq = max(seq, rs)
	}
	return h, seq, true
}

// Snapshot is everything `land status` reads, loaded by LoadStatus.
type Snapshot struct {
	Sprint   string
	Units    map[string]map[string]string // unit -> s:<S>:u:<unit>
	Bases    []RepoBase                   // every repo and base a unit names, sorted
	Landable map[RepoBase][]string        // priority order
	Chain    map[RepoBase][]map[string]string
	Freeze   map[RepoBase]map[string]string
	Workers  map[string]bool   // worker:<bench>:<slot> -> alive, for every gating batch
	Benches  map[string]string // bench -> benched reason, for benches in `benches` marked benched
}

// RepoBase is one landing chain: a repo and a base (2.1).
type RepoBase struct{ Repo, Base string }

func (rb RepoBase) String() string { return rb.Repo + "/" + rb.Base }

// LoadStatus reads the sprint's unit index, every unit hash, then per repo
// and base the landable zset, the chain and its batch hashes, the freeze,
// and every gating batch's worker key, in four pipelined round trips.
func LoadStatus(ctx context.Context, c *redis.Client, sprint string) (*Snapshot, error) {
	pipe := c.Pipeline()
	idsCmd := pipe.SMembers(ctx, UnitsSetKey(sprint))
	benchCmd := pipe.SMembers(ctx, "benches")
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("read %s: %w", UnitsSetKey(sprint), err)
	}
	snap := &Snapshot{
		Sprint: sprint, Units: map[string]map[string]string{}, Landable: map[RepoBase][]string{},
		Chain: map[RepoBase][]map[string]string{}, Freeze: map[RepoBase]map[string]string{},
		Workers: map[string]bool{}, Benches: map[string]string{},
	}
	ids := idsCmd.Val()
	sort.Strings(ids)
	benches := benchCmd.Val()
	sort.Strings(benches)

	pipe = c.Pipeline()
	uCmds := make([]*redis.MapStringStringCmd, len(ids))
	for i, id := range ids {
		uCmds[i] = pipe.HGetAll(ctx, UnitKey(sprint, id))
	}
	bCmds := make([]*redis.MapStringStringCmd, len(benches))
	for i, b := range benches {
		bCmds[i] = pipe.HGetAll(ctx, BenchLandKey(b))
	}
	if len(ids)+len(benches) > 0 {
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return nil, fmt.Errorf("read unit records: %w", err)
		}
	}
	seen := map[RepoBase]bool{}
	for i, id := range ids {
		f := uCmds[i].Val()
		snap.Units[id] = f
		if f["repo"] == "" {
			continue
		}
		rb := RepoBase{f["repo"], f["base"]}
		if rb.Base == "" {
			rb.Base = "dev"
		}
		if !seen[rb] {
			seen[rb] = true
			snap.Bases = append(snap.Bases, rb)
		}
	}
	for i, b := range benches {
		if m := bCmds[i].Val(); m["benched"] != "" && m["benched"] != "0" {
			snap.Benches[b] = m["benched"]
		}
	}
	sort.Slice(snap.Bases, func(i, j int) bool { return snap.Bases[i].String() < snap.Bases[j].String() })
	if len(snap.Bases) == 0 {
		return snap, nil
	}

	pipe = c.Pipeline()
	landCmds := map[RepoBase]*redis.StringSliceCmd{}
	chainCmds := map[RepoBase]*redis.StringSliceCmd{}
	freezeCmds := map[RepoBase]*redis.MapStringStringCmd{}
	for _, rb := range snap.Bases {
		landCmds[rb] = pipe.ZRange(ctx, LandableKey(sprint, rb.Repo, rb.Base), 0, -1)
		chainCmds[rb] = pipe.ZRange(ctx, ChainKey(rb.Repo, rb.Base), 0, -1)
		freezeCmds[rb] = pipe.HGetAll(ctx, FreezeKey(rb.Repo, rb.Base))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("read chains: %w", err)
	}
	pipe = c.Pipeline()
	batchCmds := map[RepoBase][]*redis.MapStringStringCmd{}
	chainIDs := map[RepoBase][]string{}
	for _, rb := range snap.Bases {
		snap.Landable[rb] = landCmds[rb].Val()
		if fz := freezeCmds[rb].Val(); len(fz) > 0 {
			snap.Freeze[rb] = fz
		}
		chainIDs[rb] = chainCmds[rb].Val()
		for _, b := range chainIDs[rb] {
			batchCmds[rb] = append(batchCmds[rb], pipe.HGetAll(ctx, BatchKey(rb.Repo, rb.Base, b)))
		}
	}
	if len(batchCmds) == 0 {
		return snap, nil
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("read batches: %w", err)
	}
	pipe = c.Pipeline()
	workerCmds := map[string]*redis.IntCmd{}
	for _, rb := range snap.Bases {
		for i, cmd := range batchCmds[rb] {
			b := cmd.Val()
			if len(b) == 0 {
				b = map[string]string{"state": "MISSING"}
			}
			b["id"] = chainIDs[rb][i]
			snap.Chain[rb] = append(snap.Chain[rb], b)
			if b["state"] == "gating" && b["bench"] != "" && b["slot"] != "" {
				wk := "worker:" + b["bench"] + ":" + b["slot"]
				if _, ok := workerCmds[wk]; !ok {
					workerCmds[wk] = pipe.Exists(ctx, wk)
				}
			}
		}
	}
	if len(workerCmds) == 0 {
		return snap, nil
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("read workers: %w", err)
	}
	for wk, cmd := range workerCmds {
		snap.Workers[wk] = cmd.Val() == 1
	}
	return snap, nil
}
