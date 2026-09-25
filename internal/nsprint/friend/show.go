package friend

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// Row is `friend show`: one friend's state, rung and counts, read only.
type Row struct {
	Friend   string
	Up       bool
	State    string
	Rung     int
	Since    time.Time
	Until    time.Time
	Reason   string
	Wake     WakePath
	Starting int
	Living   int
	Open     int
	Done     int
}

// Line prints the row. A human wake path at rung 2 or more is `waiting on
// human since <t>`; an unknown value prints ?, never a carried number. The
// oldest open age prints ? until a task push records its time.
func (r Row) Line() string {
	var b strings.Builder
	fmt.Fprintf(&b, "friend=%s beat=%s state=%s", r.Friend, map[bool]string{true: "up", false: "down"}[r.Up], r.State)
	if r.State == StateIdle || r.State == StateUnderfull {
		fmt.Fprintf(&b, " rung=%d", r.Rung)
	}
	if !r.Since.IsZero() {
		b.WriteString(" since=" + r.Since.UTC().Format(time.RFC3339))
	}
	if !r.Until.IsZero() {
		b.WriteString(" until=" + r.Until.UTC().Format(time.RFC3339))
	}
	fmt.Fprintf(&b, " wake=%s starting=%d living=%d open=%d done=%d oldest=?", r.Wake, r.Starting, r.Living, r.Open, r.Done)
	if r.Wake.Kind == "human" && r.Rung >= 2 && !r.Since.IsZero() {
		b.WriteString(" | waiting on human since " + r.Since.UTC().Format(time.RFC3339))
	}
	if r.Wake.Kind == "" {
		b.WriteString(" | RED no declared wake path")
	}
	return b.String()
}

// Show reads one friend, or every registered friend when f is "".
func Show(ctx context.Context, st *store.Store, f string) ([]Row, error) {
	if st == nil {
		return nil, fmt.Errorf("friend show: nil store")
	}
	client := st.Client()
	friends := []string{f}
	if f == "" {
		var err error
		friends, err = client.SMembers(ctx, "friends").Result()
		if err != nil {
			return nil, fmt.Errorf("friend show: registry: %w", err)
		}
	} else if ok, err := client.SIsMember(ctx, "friends", f).Result(); err != nil {
		return nil, fmt.Errorf("friend show: registry: %w", err)
	} else if !ok {
		return nil, fmt.Errorf("friend show: %s is not a registered friend", f)
	}
	readings, err := read(ctx, st)
	if err != nil {
		return nil, err
	}
	sprints, err := openSprints(ctx, client)
	if err != nil {
		return nil, err
	}
	byName := map[string]reading{}
	for _, r := range readings {
		byName[r.friend] = r
	}
	type cmds struct {
		state    *redis.SliceCmd
		wake     *redis.SliceCmd
		starting *redis.IntCmd
		living   *redis.IntCmd
		done     []*redis.IntCmd
	}
	pipe := client.Pipeline()
	all := make([]cmds, len(friends))
	for i, name := range friends {
		c := cmds{
			state:    pipe.HMGet(ctx, StateKey(name), "state", "rung", "since", "until", "reason"),
			wake:     pipe.HMGet(ctx, WakePathKey(name), "kind", "unit", "host", "notify"),
			starting: pipe.ZCard(ctx, "friend:"+name+":starting"),
			living:   pipe.ZCard(ctx, "friend:"+name+":living"),
		}
		for _, s := range sprints {
			c.done = append(c.done, pipe.SCard(ctx, "s:"+s+":done:"+name))
		}
		all[i] = c
	}
	if len(friends) > 0 {
		if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
			return nil, fmt.Errorf("friend show: %w", err)
		}
	}
	str := func(v any) string { s, _ := v.(string); return s }
	ms := func(v any) time.Time {
		n, err := strconv.ParseInt(str(v), 10, 64)
		if err != nil || n == 0 {
			return time.Time{}
		}
		return time.UnixMilli(n)
	}
	rows := make([]Row, 0, len(friends))
	for i, name := range friends {
		c := all[i]
		sv, wv := c.state.Val(), c.wake.Val()
		row := Row{Friend: name, Up: byName[name].up, State: StateUp, Open: byName[name].open,
			Starting: int(c.starting.Val()), Living: int(c.living.Val())}
		if s := str(sv[0]); s != "" {
			row.State = s
			row.Rung, _ = strconv.Atoi(str(sv[1]))
			row.Since, row.Until, row.Reason = ms(sv[2]), ms(sv[3]), str(sv[4])
		}
		row.Wake = WakePath{Kind: str(wv[0]), Unit: str(wv[1]), Host: str(wv[2]), Notify: str(wv[3])}
		for _, d := range c.done {
			row.Done += int(d.Val())
		}
		rows = append(rows, row)
	}
	sortRows(rows)
	return rows, nil
}

func sortRows(rows []Row) {
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j].Friend < rows[j-1].Friend; j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
}
