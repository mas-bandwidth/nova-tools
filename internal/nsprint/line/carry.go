package line

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
)

// Outcome of one carry.
const (
	// Carried: every typed line at the old head now stands at the new head
	// with a carried_from receipt.
	Carried = "CARRIED"
	// Refused: the diff changed; the changed files are named and a re-read
	// is the only remedy.
	Refused = "REFUSED"
	// Nothing: the recorded digest is already at the unit's head, or no
	// read stood at the old head.
	Nothing = "NOTHING"
)

// Result is one carry's receipt.
type Result struct {
	Outcome string
	Unit    string
	From    string // the head the lines were typed at
	To      string // the unit's head now
	Who     []string
	Changed []string
}

// Line is the one receipt line the verb prints.
func (r Result) Line(id land.ID) string {
	parts := []string{r.Outcome, id.String()}
	if r.From != "" || r.To != "" {
		parts = append(parts, short(r.From)+"->"+short(r.To))
	}
	switch r.Outcome {
	case Carried:
		parts = append(parts, "reads="+strconv.Itoa(len(r.Who)), "who="+strings.Join(r.Who, ","))
	case Refused:
		parts = append(parts, "changed="+strings.Join(r.Changed, ","), "remedy=re-read at "+short(r.To))
	}
	return strings.Join(parts, " ")
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	if sha == "" {
		return "-"
	}
	return sha
}

// Carry moves every typed line of PR id from the head its diff digest was
// recorded at to the unit's head now, when the two diffs are identical.
// dir is where the new head's diff is read (the bench mirror); baseRef is
// the base ref that dir resolves ("" is the unit's base field). Four round
// trips: the unit and the friends set, the reads, the writes, in pipelines.
func Carry(ctx context.Context, c *redis.Client, sprint string, id land.ID, dir, baseRef string) (Result, error) {
	unit, err := land.ResolvePR(ctx, c, sprint, id)
	if err != nil {
		return Result{}, err
	}
	res := Result{Unit: unit}
	ukey := land.UnitKey(sprint, unit)
	pipe := c.Pipeline()
	uCmd := pipe.HGetAll(ctx, ukey)
	friendsCmd := pipe.SMembers(ctx, "friends")
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return res, fmt.Errorf("read %s: %w", ukey, err)
	}
	u := uCmd.Val()
	if len(u) == 0 {
		return res, fmt.Errorf("%w: MISSING %s", land.ErrNoRecord, ukey)
	}
	head := u["head"]
	old, stored, ok := recorded(u)
	if !ok {
		return res, fmt.Errorf("%w on %s; run: nova-sprint read digest --repo %s --n %d --head <the head the line was typed at>", ErrNoDigest, unit, id.Repo, id.N)
	}
	res.From, res.To = old, head
	if head == "" {
		return res, fmt.Errorf("unit %s has no head", unit)
	}
	if old == head {
		res.Outcome = Nothing
		return res, nil
	}
	if baseRef == "" {
		baseRef = u["base"]
	}
	if baseRef == "" {
		baseRef = "dev"
	}
	now, err := DiffDigest(ctx, dir, baseRef, head)
	if err != nil {
		return res, err
	}
	if now.SHA256 != stored.SHA256 {
		res.Outcome = Refused
		res.Changed = Changed(stored, now)
		if len(res.Changed) == 0 {
			res.Changed = []string{"(diff)"}
		}
		return res, nil
	}
	friends := friendsCmd.Val()
	sort.Strings(friends)
	pipe = c.Pipeline()
	readCmds := make([]*redis.MapStringStringCmd, len(friends))
	for i, f := range friends {
		readCmds[i] = pipe.HGetAll(ctx, land.ReadKey(sprint, unit, f))
	}
	dispKey := fmt.Sprintf("s:%s:disp:%s:%d", sprint, id.Repo, id.N)
	dispCmd := pipe.HGetAll(ctx, dispKey)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return res, fmt.Errorf("read reads of %s: %w", unit, err)
	}
	disp := dispCmd.Val()
	at := strconv.FormatInt(time.Now().UnixMilli(), 10)
	pipe = c.Pipeline()
	for i, f := range friends {
		r := readCmds[i].Val()
		if len(r) == 0 || r["head"] != old || r["verdict"] == "" {
			continue
		}
		res.Who = append(res.Who, f)
		pipe.HSet(ctx, land.ReadKey(sprint, unit, f), "head", head, FieldCarriedFrom, old, FieldCarriedAt, at)
		if v, ok := disp[f+"@"+old]; ok {
			pipe.HSet(ctx, dispKey, f+"@"+head, v)
		}
	}
	pipe.HSet(ctx, ukey, FieldHead, head)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return res, fmt.Errorf("carry on %s: %w", unit, err)
	}
	if len(res.Who) == 0 {
		res.Outcome = Nothing
		return res, nil
	}
	res.Outcome = Carried
	return res, nil
}
