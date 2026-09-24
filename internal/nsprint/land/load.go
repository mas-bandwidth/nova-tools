package land

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/civerdict"
)

// ErrNoRecord is returned when s:<S>:pr:<repo>:<n> does not exist.
var ErrNoRecord = errors.New("no PR record")

const defaultAbsentAfter = 60 * time.Minute

func policy(m map[string]string) (int, time.Duration) {
	readers := 1
	if n, err := strconv.Atoi(m["readers"]); err == nil && n > 0 {
		readers = n
	}
	after := defaultAbsentAfter
	if d, err := time.ParseDuration(m["absent_after"]); err == nil && d > 0 {
		after = d
	}
	return readers, after
}

// LoadPR reads one PR's records in two pipelined round trips.
func LoadPR(ctx context.Context, c *redis.Client, sprint string, id ID) (*PR, error) {
	s := "s:" + sprint + ":"
	sfx := id.Repo + ":" + strconv.Itoa(id.N)
	pipe := c.Pipeline()
	prCmd := pipe.HGetAll(ctx, id.Key(sprint))
	dispCmd := pipe.HGetAll(ctx, s+"disp:"+sfx)
	holdCmd := pipe.HGetAll(ctx, s+"hold:"+sfx)
	polCmd := pipe.HGetAll(ctx, s+"policy")
	rankCmd := pipe.ZRank(ctx, s+"landable", id.String())
	cardCmd := pipe.ZCard(ctx, s+"landable")
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("read %s: %w", id, err)
	}
	fields := prCmd.Val()
	if len(fields) == 0 {
		return nil, fmt.Errorf("%w: MISSING %s", ErrNoRecord, id.Key(sprint))
	}
	holds, err := parseHolds(holdCmd.Val())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", id, err)
	}
	p := &PR{
		ID: id, Sprint: sprint, Fields: fields,
		Reads: parseReads(dispCmd.Val()), Holds: holds,
		Friends: map[string]FriendState{}, Rank: -1, Landable: cardCmd.Val(),
	}
	p.Readers, p.AbsentAfter = policy(polCmd.Val())
	if r, err := rankCmd.Result(); err == nil {
		p.Rank = r
	}
	parent, has, err := parentID(id, fields["stack_parent"])
	if err != nil {
		return nil, fmt.Errorf("%s stack_parent: %w", id, err)
	}
	if has {
		p.Parent = &parent
	}

	pipe = c.Pipeline()
	var ciCmd, parentCmd *redis.MapStringStringCmd
	var gidsCmd *redis.StringSliceCmd
	if head := fields["head"]; head != "" {
		base := fields["base"]
		if base == "" {
			base = "dev"
		}
		baseSHA := fields["base_sha"]
		if baseSHA == "" {
			baseSHA, _ = c.HGet(ctx, "land:"+id.Repo+":"+base+":tip", "sha").Result()
		}
		gid, _ := civerdict.Expected(ctx, c, id.Repo, base, baseSHA)
		if gid != "" {
			ciCmd = pipe.HGetAll(ctx, civerdict.Key(id.Repo, head, gid))
		}
		gidsCmd = pipe.SMembers(ctx, civerdict.GIDsKey(id.Repo, head))
	}
	if p.Parent != nil {
		parentCmd = pipe.HGetAll(ctx, p.Parent.Key(sprint))
	}
	friendCmds := map[string]*redis.MapStringStringCmd{}
	for _, h := range holds {
		if _, ok := friendCmds[h.Holder]; !ok && h.Open() {
			friendCmds[h.Holder] = pipe.HGetAll(ctx, "friend:"+h.Holder+":state")
		}
	}
	if ciCmd == nil && gidsCmd == nil && parentCmd == nil && len(friendCmds) == 0 {
		return p, nil
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("read %s: %w", id, err)
	}
	if ciCmd != nil {
		p.CI = ciCmd.Val()
	}
	if gidsCmd != nil {
		p.CIGIDs = gidsCmd.Val()
	}
	if parentCmd != nil {
		p.ParentRec = parentCmd.Val()
	}
	for f, cmd := range friendCmds {
		p.Friends[f] = parseFriend(cmd.Val())
	}
	return p, nil
}

// Snapshot is everything `land status` reads, loaded by LoadStatus.
type Snapshot struct {
	Sprint   string
	PRs      map[string]map[string]string // <repo>#<n> -> PR hash
	Holds    map[string][]Hold            // <repo>#<n> -> hold records
	Landable []string                     // priority order
	Friends  map[string]FriendState
}

// LoadStatus reads the sprint's PR index, every PR hash and hold record,
// and the holders' states in three pipelined round trips.
func LoadStatus(ctx context.Context, c *redis.Client, sprint string) (*Snapshot, error) {
	s := "s:" + sprint + ":"
	pipe := c.Pipeline()
	idsCmd := pipe.SMembers(ctx, s+"prs")
	landCmd := pipe.ZRange(ctx, s+"landable", 0, -1)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("read %sprs: %w", s, err)
	}
	snap := &Snapshot{
		Sprint: sprint, PRs: map[string]map[string]string{}, Holds: map[string][]Hold{},
		Landable: landCmd.Val(), Friends: map[string]FriendState{},
	}
	ids := idsCmd.Val()
	sort.Strings(ids)
	if len(ids) == 0 {
		return snap, nil
	}
	pipe = c.Pipeline()
	prCmds := make(map[string]*redis.MapStringStringCmd, len(ids))
	holdCmds := make(map[string]*redis.MapStringStringCmd, len(ids))
	for _, raw := range ids {
		id, err := ParseID(raw)
		if err != nil {
			return nil, fmt.Errorf("%sprs: %w", s, err)
		}
		prCmds[raw] = pipe.HGetAll(ctx, id.Key(sprint))
		holdCmds[raw] = pipe.HGetAll(ctx, s+"hold:"+id.Repo+":"+strconv.Itoa(id.N))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("read PR records: %w", err)
	}
	holders := map[string]bool{}
	for _, raw := range ids {
		snap.PRs[raw] = prCmds[raw].Val()
		holds, err := parseHolds(holdCmds[raw].Val())
		if err != nil {
			return nil, fmt.Errorf("%s: %w", raw, err)
		}
		snap.Holds[raw] = holds
		for _, h := range holds {
			if h.Open() {
				holders[h.Holder] = true
			}
		}
	}
	if len(holders) == 0 {
		return snap, nil
	}
	pipe = c.Pipeline()
	fCmds := map[string]*redis.MapStringStringCmd{}
	for f := range holders {
		fCmds[f] = pipe.HGetAll(ctx, "friend:"+f+":state")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("read holder states: %w", err)
	}
	for f, cmd := range fCmds {
		snap.Friends[f] = parseFriend(cmd.Val())
	}
	return snap, nil
}
