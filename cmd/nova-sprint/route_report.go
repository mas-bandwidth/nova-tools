package main

// nova-sprint route report prints the per-model outcome of one sprint's cards
// (nova-tools#3949), so a change to the route spread is measured, never
// guessed (swarm-0925a: kimi-k3 crashed 14 of 14 pro cards and left the
// spread on this count):
//
//	nova-sprint route report --sprint <S> [--redis <addr>]
//
// It reads the roster sprint:<S>:cards (one ZRANGE; no SCAN), then one
// pipeline of HMGETs over the card records it names (attempt, where,
// where_ok, reason, model, route, wall_ms) and one pipeline over each card's
// current attempt's result hash (w_model, w_route, w_reason, w_wall_ms: the
// wrapper's provider facts). The model is w_model, else the card's model,
// else "-". A card not in done is open; a done card is ok (where_ok ok), else
// counted by its reason (the card's, else w_reason): crash, refused, wall
// (wall or timeout) or fail (any other). One MODEL line per model, most cards
// first, then the ROUTE REPORT receipt with the totals. Exit 0 printed, 1
// REFUSED (the sprint has no cards; the remedy names the roster key), 2 usage
// or Redis.

import (
	"context"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/redis/go-redis/v9"
)

// modelOutcome is one MODEL line of route report.
type modelOutcome struct {
	Model                                       string
	Routes                                      []string
	Cards, OK, Crash, Refused, Wall, Fail, Open int
	wallSum                                     int64
	wallN                                       int
}

func (m modelOutcome) line() string {
	mean := "-"
	if m.wallN > 0 {
		mean = strconv.FormatInt(m.wallSum/int64(m.wallN)/1000, 10)
	}
	route := "-"
	if len(m.Routes) > 0 {
		route = strings.Join(m.Routes, ",")
	}
	return fmt.Sprintf("MODEL %s route=%s cards=%d ok=%d crash=%d refused=%d wall=%d fail=%d open=%d mean_wall_s=%s",
		m.Model, route, m.Cards, m.OK, m.Crash, m.Refused, m.Wall, m.Fail, m.Open, mean)
}

var reportCardFields = []string{"attempt", "where", "where_ok", "reason", "model", "route", "wall_ms"}
var reportResultFields = []string{"w_model", "w_route", "w_reason", "w_wall_ms"}

// routeReport reads the sprint's cards and folds them per model, most cards
// first (then by model name).
func routeReport(ctx context.Context, client redis.UniversalClient, sprint string) ([]modelOutcome, error) {
	ids, err := client.ZRange(ctx, card.RosterKey(sprint), 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", card.RosterKey(sprint), err)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	pipe := client.Pipeline()
	cards := make([]*redis.SliceCmd, len(ids))
	for i, id := range ids {
		cards[i] = pipe.HMGet(ctx, id, reportCardFields...)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("read card records: %w", err)
	}
	recs := make([]map[string]string, len(ids))
	pipe = client.Pipeline()
	results := make([]*redis.SliceCmd, len(ids))
	for i, id := range ids {
		recs[i] = hmgetMap(reportCardFields, cards[i].Val())
		if a := recs[i]["attempt"]; a != "" {
			results[i] = pipe.HMGet(ctx, id+":result:a"+a, reportResultFields...)
		}
	}
	if pipe.Len() > 0 {
		if _, err := pipe.Exec(ctx); err != nil {
			return nil, fmt.Errorf("read card results: %w", err)
		}
	}
	by := map[string]*modelOutcome{}
	for i, c := range recs {
		res := map[string]string{}
		if results[i] != nil {
			res = hmgetMap(reportResultFields, results[i].Val())
		}
		model := firstOf(res["w_model"], c["model"], "-")
		m := by[model]
		if m == nil {
			m = &modelOutcome{Model: model}
			by[model] = m
		}
		if r := firstOf(res["w_route"], c["route"], ""); r != "" && !slices.Contains(m.Routes, r) {
			m.Routes = append(m.Routes, r)
		}
		m.Cards++
		switch reason := firstOf(c["reason"], res["w_reason"], ""); {
		case c["where"] != "done":
			m.Open++
		case c["where_ok"] == "ok":
			m.OK++
		case reason == "crash":
			m.Crash++
		case reason == "refused":
			m.Refused++
		case reason == "wall" || reason == "timeout":
			m.Wall++
		default:
			m.Fail++
		}
		if ms, err := strconv.ParseInt(firstOf(res["w_wall_ms"], c["wall_ms"], ""), 10, 64); err == nil && ms > 0 {
			m.wallSum += ms
			m.wallN++
		}
	}
	out := make([]modelOutcome, 0, len(by))
	for _, m := range by {
		sort.Strings(m.Routes)
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Cards != out[j].Cards {
			return out[i].Cards > out[j].Cards
		}
		return out[i].Model < out[j].Model
	})
	return out, nil
}

func hmgetMap(fields []string, vals []interface{}) map[string]string {
	m := make(map[string]string, len(fields))
	for i, f := range fields {
		if i < len(vals) {
			if s, ok := vals[i].(string); ok {
				m[f] = s
			}
		}
	}
	return m
}

func firstOf(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func init() {
	register(Verb{
		Name:    "route",
		Summary: "route report --sprint <S> prints per-model ok/crash/refused/wall/fail/open and mean wall from the sprint's cards",
		Run: func(ctx context.Context, args []string, out, errOut io.Writer) int {
			if len(args) == 0 || args[0] != "report" {
				return refuse(errOut, "route", "want report --sprint <S>")
			}
			return runRouteReport(ctx, args[1:], out, errOut)
		},
	})
}

func runRouteReport(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("route report")
	sprint := fs.String("sprint", "", verbflag.HelpSprint)
	addr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 || *sprint == "" || *addr == "" {
		return refuse(errOut, "route", "report needs --sprint <S> and --redis <addr> (or NOVA_SPRINT_REDIS)")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client, code := openCardRedis(ctx, *addr, errOut)
	if code != 0 {
		return code
	}
	defer client.Close()
	rows, err := routeReport(ctx, client, *sprint)
	if err != nil {
		return refuse(errOut, "route", "report: "+err.Error())
	}
	if len(rows) == 0 {
		fmt.Fprintf(out, "REFUSED route report sprint=%s: %s is empty; check the sprint name (card fsck --sprint %s rebuilds the roster)\n",
			*sprint, card.RosterKey(*sprint), *sprint)
		return 1
	}
	var t modelOutcome
	for _, m := range rows {
		fmt.Fprintln(out, m.line())
		t.Cards += m.Cards
		t.OK += m.OK
		t.Crash += m.Crash
		t.Refused += m.Refused
		t.Wall += m.Wall
		t.Fail += m.Fail
		t.Open += m.Open
	}
	fmt.Fprintf(out, "ROUTE REPORT sprint=%s cards=%d models=%d ok=%d crash=%d refused=%d wall=%d fail=%d open=%d\n",
		*sprint, t.Cards, len(rows), t.OK, t.Crash, t.Refused, t.Wall, t.Fail, t.Open)
	return 0
}
