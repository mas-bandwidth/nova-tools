package card

import (
	"context"
	"errors"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/cardhdr"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// QUACK: THE PER-BENCH END-TO-END PROBE (nova-tools#3648).
//
// One flash and one pro card per bench, each pinned to its bench (BENCH:),
// pushed by PushBatch into a fresh sprint; the probe then reads the card
// records only and prints one row per bench x tier with the seconds from T0
// (the batch's earliest cut_at) to each stage. It replaces the hand probe of
// 2026-09-24/25 (a bash generator, one card push per card and timings copied
// into a report by hand).

// QuackStages are the stages a quack row times, in pipeline order, and the
// record field each is read from: cut_at (Redis TIME at ns_card_push, whole
// seconds), dealt_at, launched_at, ended_at and harvested_at (ms), and the
// first read queued at the card's head (the created_at of the earliest task
// in s:<S>:reads:<repo>#<pr>@<head>, whole seconds).
var QuackStages = []string{"push", "deal", "launch", "end", "harvest", "read"}

var quackFields = []string{"cut_at", "dealt_at", "launched_at", "ended_at", "harvested_at",
	"where", "where_ok", "outcome", "reason", "repo", "pr", "head"}

// QuackBarsKey is the bars hash: field <stage> = the most seconds from T0 that
// stage may take. A field that is absent keeps its DefaultQuackBars value.
const QuackBarsKey = "cfg:quack"

// DefaultQuackBars are the bars when cfg:quack does not set one, from quack
// run #5 (sprint quack-0925e, 2026-09-25 06:57Z, reports/quack-v3-2026-09-25.md
// in rowan-new): all twelve launched by +27 s, ended by +88 s, harvested by
// +95 s, the first read queued 10 s after each PR; each bar is that measure
// with headroom, never tighter.
var DefaultQuackBars = map[string]int64{
	"push": 10, "deal": 30, "launch": 45, "end": 120, "harvest": 150, "read": 180,
}

// QuackInput is one probe card.
type QuackInput struct {
	Sprint, Bench, Tier string
	Repo, Base, BaseSHA string
	Stream              string
}

// quackDir is where a probe card writes its one file: a catalogued
// directory, so the new file stales no AGENTS map (quack run #3).
const quackDir = "docs/fixtures"

// QuackLabel is the card id for bench x tier.
func QuackLabel(bench, tier string) string { return "quack-" + bench + "-" + tier }

// QuackCard renders the probe card: a one-file change the model can do in one
// turn (the v4 quack card), pinned to its bench and tier.
func QuackCard(in QuackInput) (CardFile, error) {
	if !cardhdr.IsRoute(in.Tier) {
		return CardFile{}, fmt.Errorf("tier %q is not %s", in.Tier, cardhdr.RouteList)
	}
	if !idRE.MatchString(in.Bench) {
		return CardFile{}, fmt.Errorf("bench %q is not a bench name", in.Bench)
	}
	if !shaRE.MatchString(in.BaseSHA) {
		return CardFile{}, fmt.Errorf("base-sha %q is not 40 lowercase hex", in.BaseSHA)
	}
	label := QuackLabel(in.Bench, in.Tier)
	path := quackDir + "/quack-" + in.Sprint + "-" + in.Bench + "-" + in.Tier + ".txt"
	line := "quack " + in.Sprint + " " + label
	done := fmt.Sprintf("the file %s exists in the commit and its whole content is the single line %q; nothing else changes.", path, line)
	var b strings.Builder
	for _, kv := range [][2]string{
		{"RESULT", label + " sha=" + in.BaseSHA[:12]},
		{"KIND", "fix"}, {"TYPE", "code"}, {"REPO", in.Repo}, {"BASE", in.Base}, {"base-sha", in.BaseSHA},
		{"PATHS", path}, {"TEST", "none"}, {"DEPENDS-ON", "none"}, {"STREAM", in.Stream},
		{"PRIORITY", "100"}, {"WHO", "any"}, {"ROUTE", in.Tier}, {"BENCH", in.Bench}, {"EST", "2"},
		{"SOURCE", "quack"}, {"TASK", label}, {"DONE-WHEN", done},
		{"NO-SUBAGENTS", "work in this session only; do not spawn an Explore, Task or child agent."},
		{"UNATTENDED", "never ask a question and never offer to proceed; decide, and record the decision in RESULT.md."},
		{"DO", fmt.Sprintf("create %s containing exactly the line %q, commit it, write RESULT.md with CHECK: pass, RED: none, GREEN: none, and exit; do not run tests, do not read other files, do not explore.", path, line)},
	} {
		b.WriteString(kv[0] + ": " + kv[1] + "\n")
	}
	return CardFile{Name: label, Body: []byte(b.String())}, nil
}

// ReadQuackBars reads cfg:quack over the defaults. source is "cfg:quack" when
// the hash set any bar, else "default".
func ReadQuackBars(ctx context.Context, client *redis.Client) (map[string]int64, string, error) {
	bars := map[string]int64{}
	for k, v := range DefaultQuackBars {
		bars[k] = v
	}
	got, err := client.HGetAll(ctx, QuackBarsKey).Result()
	if err != nil {
		return nil, "", fmt.Errorf("read %s: %w", QuackBarsKey, err)
	}
	source := "default"
	for _, stage := range QuackStages {
		v, ok := got[stage]
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil || n <= 0 {
			return nil, "", fmt.Errorf("%s %s=%q is not a positive number of seconds", QuackBarsKey, stage, v)
		}
		bars[stage], source = n, QuackBarsKey
	}
	return bars, source, nil
}

// QuackProgress is one probe card as its record holds it: the ms each stage
// was reached (0 is not yet) and whether the card has ended failed.
type QuackProgress struct {
	Label, PR string
	At        map[string]int64
	Failed    string // the reason a card at done/fail gave, "" when it is not failed
}

// ReadQuack reads every probe card in at most three pipelines, whatever the
// count: the card records, then the read hash of each harvested card, then
// the created_at of each read task found.
func ReadQuack(ctx context.Context, client *redis.Client, sprint string, labels []string) ([]QuackProgress, error) {
	pipe := client.Pipeline()
	recs := make([]*redis.SliceCmd, len(labels))
	for i, l := range labels {
		recs[i] = pipe.HMGet(ctx, keyCard(sprint, l), quackFields...)
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("read quack cards: %w", err)
	}
	out := make([]QuackProgress, len(labels))
	reads := make([]*redis.StringSliceCmd, len(labels))
	pipe = client.Pipeline()
	queued := false
	for i, l := range labels {
		f := map[string]string{}
		for j, v := range recs[i].Val() {
			if s, ok := v.(string); ok {
				f[quackFields[j]] = s
			}
		}
		p := QuackProgress{Label: l, PR: f["pr"], At: map[string]int64{}}
		if n, err := strconv.ParseInt(f["cut_at"], 10, 64); err == nil && n > 0 {
			p.At["push"] = n * 1000
		}
		for stage, field := range map[string]string{"deal": "dealt_at", "launch": "launched_at", "end": "ended_at", "harvest": "harvested_at"} {
			if n, err := strconv.ParseInt(f[field], 10, 64); err == nil && n > 0 {
				p.At[stage] = n
			}
		}
		if f["where"] == "done" && f["where_ok"] == "fail" {
			p.Failed = "outcome=" + orNone(f["outcome"]) + " reason=" + orNone(f["reason"])
		}
		if p.At["harvest"] > 0 && f["pr"] != "" && f["head"] != "" {
			reads[i] = pipe.HVals(ctx, "s:"+sprint+":reads:"+f["repo"]+"#"+f["pr"]+"@"+f["head"])
			queued = true
		}
		out[i] = p
	}
	if !queued {
		return out, nil
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("read quack reads: %w", err)
	}
	pipe = client.Pipeline()
	tasks := make([][]*redis.StringCmd, len(labels))
	queued = false
	for i := range labels {
		if reads[i] == nil {
			continue
		}
		for _, id := range reads[i].Val() {
			tasks[i] = append(tasks[i], pipe.HGet(ctx, "task:"+id, "created_at"))
			queued = true
		}
	}
	if !queued {
		return out, nil
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("read quack read tasks: %w", err)
	}
	for i := range labels {
		for _, c := range tasks[i] {
			t, err := time.Parse(time.RFC3339, c.Val())
			if err != nil {
				continue
			}
			if ms := t.UnixMilli(); out[i].At["read"] == 0 || ms < out[i].At["read"] {
				out[i].At["read"] = ms
			}
		}
	}
	return out, nil
}

// Done reports whether the row can change no more: every stage reached, or
// the card ended failed.
func (p QuackProgress) Done() bool {
	return p.Failed != "" || p.At["read"] > 0
}

// QuackRow judges one card against the bars: the stage columns in whole
// seconds from t0 (ms), then PASS, or FAIL <stage> naming the first stage
// that is over its bar, or missing when the probe gave up (timedOut) or the
// card failed.
func QuackRow(p QuackProgress, t0 int64, bars map[string]int64, timedOut bool) (cols string, pass bool, verdict string) {
	var b strings.Builder
	fail := ""
	for _, stage := range QuackStages {
		at := p.At[stage]
		if at == 0 {
			b.WriteString(" " + stage + "=-")
			if fail == "" && (timedOut || p.Failed != "") {
				fail = stage
				if p.Failed != "" {
					fail += " " + p.Failed
				} else {
					fail += " missing"
				}
			}
			continue
		}
		s := (at - t0) / 1000
		if s < 0 {
			s = 0
		}
		fmt.Fprintf(&b, " %s=%d", stage, s)
		if fail == "" && s > bars[stage] {
			fail = fmt.Sprintf("%s over bar=%ds", stage, bars[stage])
		}
	}
	if fail != "" {
		return strings.TrimSpace(b.String()), false, "FAIL " + fail
	}
	if p.At["read"] == 0 {
		return strings.TrimSpace(b.String()), false, "WAIT"
	}
	return strings.TrimSpace(b.String()), true, "PASS"
}

func orNone(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
