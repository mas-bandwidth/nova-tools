package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

// configDeclKey is nova-config's stamp hash; configRevField its friend
// revision, the applied configuration every list and show line ends with.
const (
	configDeclKey  = "config:decl"
	configRevField = "rev:friend"
)

// upWithin is how old a beat may be and still read up: the consumer
// table's minute (keys do not expire; readers judge age).
const upWithin = 60 * time.Second

// friendView is one friend as the store holds it, read in one pipeline.
type friendView struct {
	Name string
	// The configuration nova-config applied (friend:<f>:desired, :roles).
	Slots   string // "" when friend:<f>:desired has none
	Tiers   string
	Roles   string
	Working int64
	// The beat (friend:<f>:beat): what the friend reports through here.
	Host, Harness, Session, Load, Models string
	BeatAt                               time.Time // zero: no beat
	// Away is friend:<f>:down's reason ("" when not away; "down" when the
	// flag carries none).
	Away   string
	IsAway bool
}

// state is up (a beat under upWithin old and no away flag), away (the
// flag) or down.
func (v friendView) state(now time.Time) string {
	switch {
	case v.IsAway:
		return "away"
	case !v.BeatAt.IsZero() && now.Sub(v.BeatAt) <= upWithin:
		return "up"
	}
	return "down"
}

// beatAge is how long ago the beat was, "-" with none.
func (v friendView) beatAge(now time.Time) string {
	if v.BeatAt.IsZero() {
		return "-"
	}
	age := now.Sub(v.BeatAt)
	if age < 0 {
		age = 0
	}
	return age.Truncate(time.Second).String()
}

// listLine is the list row.
func (v friendView) listLine(now time.Time, rev string) string {
	return fmt.Sprintf("FRIEND name=%s state=%s slots=%s tiers=%s roles=%s host=%s working=%d rev=%s",
		v.Name, v.state(now), dash(v.Slots), dash(v.Tiers), dash(v.Roles), dash(v.Host), v.Working, dash(rev))
}

// showLine is the show row: the list row and the session's facts.
func (v friendView) showLine(now time.Time, rev string) string {
	return fmt.Sprintf("FRIEND name=%s state=%s slots=%s tiers=%s roles=%s host=%s working=%d session=%s harness=%s load=%s models=%s beat=%s away=%s rev=%s",
		v.Name, v.state(now), dash(v.Slots), dash(v.Tiers), dash(v.Roles), dash(v.Host), v.Working, dash(v.Session), quoteField(v.Harness),
		dash(v.Load), dash(v.Models), v.beatAge(now), quoteField(v.Away), dash(rev))
}

// readFriends reads the named friends (every registered one when names is
// nil) and the applied configuration revision, in two pipelines.
func readFriends(ctx context.Context, c redis.Cmdable, names []string) ([]friendView, string, error) {
	pipe := c.Pipeline()
	revCmd := pipe.HGet(ctx, configDeclKey, configRevField)
	var members *redis.StringSliceCmd
	if names == nil {
		members = pipe.SMembers(ctx, "friends")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, "", fmt.Errorf("read friends: %w", err)
	}
	rev := revCmd.Val()
	if members != nil {
		names = members.Val()
	}
	sort.Strings(names)
	pipe = c.Pipeline()
	desired := make([]*redis.SliceCmd, len(names))
	beats := make([]*redis.MapStringStringCmd, len(names))
	downs := make([]*redis.MapStringStringCmd, len(names))
	roles := make([]*redis.StringCmd, len(names))
	working := make([]*redis.Cmd, len(names))
	for i, n := range names {
		k := taskcard.Consumer{Kind: "friend", Name: n}
		desired[i] = pipe.HMGet(ctx, k.DesiredKey(), "slots", "tiers")
		beats[i] = pipe.HGetAll(ctx, k.BeatKey())
		downs[i] = pipe.HGetAll(ctx, k.DownKey())
		roles[i] = pipe.HGet(ctx, "friend:"+n+":roles", "roles")
		working[i] = ws.CellCard(ctx, pipe, k.String(), "working")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, "", fmt.Errorf("read friends: %w", err)
	}
	out := make([]friendView, len(names))
	for i, n := range names {
		v := friendView{Name: n}
		if d := desired[i].Val(); len(d) == 2 {
			v.Slots, _ = d[0].(string)
			v.Tiers, _ = d[1].(string)
		}
		b := beats[i].Val()
		v.Host, v.Harness, v.Session, v.Load, v.Models = b["host"], b["harness"], b["session"], b["load1"], b["models"]
		if ms, err := strconv.ParseInt(b["at"], 10, 64); err == nil && ms > 0 {
			v.BeatAt = time.UnixMilli(ms)
		}
		if d := downs[i].Val(); len(d) > 0 {
			v.IsAway = true
			v.Away = d["reason"]
			if v.Away == "" {
				v.Away = "down"
			}
		}
		v.Roles = roles[i].Val()
		count, err := working[i].Int64()
		if err != nil {
			return nil, "", fmt.Errorf("read friend:%s working: %w (is the function library loaded? run: nova-sprint fn check)", n, err)
		}
		v.Working = count
		out[i] = v
	}
	return out, rev, nil
}

// runList is `list`: one line per registered friend, sorted by name.
func runList(ctx context.Context, e env, args []string, out, errOut io.Writer) int {
	const verb = "list"
	fs := newFlags(verb)
	redisAddr := fs.String("redis", "", redisHelp)
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, verb, "takes flags, not positional arguments")
	}
	st, err := openStore(ctx, e.redis(*redisAddr))
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer func() { _ = st.Close() }()
	views, rev, err := readFriends(ctx, st.Client(), nil)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	now := time.Now()
	for _, v := range views {
		fmt.Fprintln(out, v.listLine(now, rev))
	}
	fmt.Fprintf(out, "FRIEND LIST friends=%d rev=%s\n", len(views), dash(rev))
	return 0
}

// runShow is `show <name>`: the one friend's line with its session's facts.
func runShow(ctx context.Context, e env, args []string, out, errOut io.Writer) int {
	const verb = "show"
	fs := newFlags(verb)
	redisAddr := fs.String("redis", "", redisHelp)
	name, err := parseNamed(fs, args)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	st, err := openStore(ctx, e.redis(*redisAddr))
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer func() { _ = st.Close() }()
	ok, err := registered(ctx, st.Client(), name)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if !ok {
		return refused(errOut, verb, unregistered(name))
	}
	views, rev, err := readFriends(ctx, st.Client(), []string{name})
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if len(views) != 1 {
		return refuse(errOut, verb, "read "+name+": no view")
	}
	fmt.Fprintln(out, strings.TrimSpace(views[0].showLine(time.Now(), rev)))
	return 0
}
