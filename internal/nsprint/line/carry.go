package line

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"encoding/json"

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
func Carry(ctx context.Context, c *redis.Client, sprint, instance string, id land.ID, dir, baseRef string) (Result, error) {
	unit, u, friends, err := loadUnit(ctx, c, sprint, id)
	if err != nil {
		return Result{Unit: unit}, err
	}
	res := Result{Unit: unit}
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
	return carryTo(ctx, c, sprint, instance, id, unit, u, friends, dir, baseRef, old, stored, head)
}

// CarryHead is the carry the pr-to-read head-change path runs on a `pr head`
// event from -> to (nova-tools #3806): the event names both heads, so the
// unit's head field is not consulted. The digest at from is the one recorded
// at read time when it was recorded at from; otherwise the wrapper computes
// it from dir, so a read taken without `read digest` still carries. The
// reads typed at from move to to when the diffs are identical; a changed
// diff is Refused naming the files, and the caller queues the re-reads.
func CarryHead(ctx context.Context, c *redis.Client, sprint, instance string, id land.ID, dir, baseRef, from, to string) (Result, error) {
	unit, u, friends, err := loadUnit(ctx, c, sprint, id)
	if err != nil {
		return Result{Unit: unit}, err
	}
	res := Result{Unit: unit, From: from, To: to}
	if from == "" || to == "" || from == to {
		res.Outcome = Nothing
		return res, nil
	}
	old, stored, ok := recorded(u)
	if !ok || old != from {
		stored, err = DiffDigest(ctx, dir, base(u, baseRef), from)
		if err != nil {
			return res, fmt.Errorf("digest at old head %s: %w", short(from), err)
		}
	}
	return carryTo(ctx, c, sprint, instance, id, unit, u, friends, dir, baseRef, from, stored, to)
}

// loadUnit resolves PR id to its unit and reads the unit record and the
// friends set in one pipeline.
func loadUnit(ctx context.Context, c *redis.Client, sprint string, id land.ID) (string, map[string]string, []string, error) {
	unit, err := land.ResolvePR(ctx, c, sprint, id)
	if err != nil {
		return "", nil, nil, err
	}
	ukey := land.UnitKey(sprint, unit)
	pipe := c.Pipeline()
	uCmd := pipe.HGetAll(ctx, ukey)
	friendsCmd := pipe.SMembers(ctx, "friends")
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return unit, nil, nil, fmt.Errorf("read %s: %w", ukey, err)
	}
	u := uCmd.Val()
	if len(u) == 0 {
		return unit, nil, nil, fmt.Errorf("%w: MISSING %s", land.ErrNoRecord, ukey)
	}
	friends := friendsCmd.Val()
	sort.Strings(friends)
	return unit, u, friends, nil
}

// base is the base ref a diff is read against: the flag, else the unit's
// base field, else dev.
func base(u map[string]string, baseRef string) string {
	if baseRef != "" {
		return baseRef
	}
	if u["base"] != "" {
		return u["base"]
	}
	return "dev"
}

// carryTo compares the digest at old (stored) with the diff at head and,
// when identical, moves every typed line at old to head with a carried_from
// receipt, the disp rows with them, and the digest to head.
func carryTo(ctx context.Context, c *redis.Client, sprint, instance string, id land.ID, unit string, u map[string]string, friends []string, dir, baseRef, old string, stored Digest, head string) (Result, error) {
	res := Result{Unit: unit, From: old, To: head}
	now, err := DiffDigest(ctx, dir, base(u, baseRef), head)
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
	filesJSON, err := json.Marshal(now.Files)
	if err != nil {
		return res, err
	}
	args := []any{
		sprint, instance, unit, id.Repo, id.N, old, head,
		now.SHA256, string(filesJSON), len(friends),
	}
	for _, f := range friends {
		args = append(args, f)
	}
	reply, err := c.FCall(ctx, "ns_read_carry", nil, args...).Slice()
	if err != nil {
		return res, fmt.Errorf("ns_read_carry on %s: %w", unit, err)
	}
	if len(reply) > 0 {
		if s, ok := reply[0].(string); ok && s == "LEASE" {
			return res, fmt.Errorf("lease held by %s", reply[1])
		}
	}
	if len(reply) > 1 {
		for i := 1; i < len(reply); i++ {
			if f, ok := reply[i].(string); ok {
				res.Who = append(res.Who, f)
			}
		}
	}
	if len(res.Who) == 0 {
		res.Outcome = Nothing
		return res, nil
	}
	res.Outcome = Carried
	return res, nil
}
