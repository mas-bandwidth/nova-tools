package fold

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

// CICost is the sprint's ci cost line (#2756 10.4 item 5, 10.3 item 4,
// control 34; nova-tools #3046). ci cards are script cards with zero model
// calls, so they stay off the route and sprint lines and the model cost per
// landed card is the same with or without them; their cost is bench time,
// charged here for every attempt: failures, crashes and reruns included.
type CICost struct {
	Heads      int   // unique required head-and-base pairs the sprint cut ci cards for
	OK         int   // of those, the record says OK for that base
	Attempts   int   // every ended attempt, one per `ci end` receipt
	Unmeasured int   // attempts whose wall was never recorded: an absence, never a zero
	BenchS     int64 // bench seconds over the measured attempts
	LandedPRs  int   // unique repo and PR the sprint's model cards landed
	WallS      int64 // cut -> final verdict, summed over the landed PRs' heads
	WallPRs    int   // landed PRs whose landed head has a ci cut and end on its record
}

var ciCardFields = []string{"ci_repo", "ci_pr", "ci_head", "base"}

var ciRecordFields = []string{"verdict", "base", "attempt", "card", "wall_s", "cut_at", "end_at"}

// readCI builds the ci cost from the cards fold.Read already holds, the ci
// cards' head binding and records (one pipeline each) and the `ci end`
// receipts in the sprint log, which is never trimmed before fold (10.3 item
// 2), so every attempt is there even after the record moved to the latest.
func readCI(ctx context.Context, client *redis.Client, sprint string, labels []string, cards []map[string]string) (CICost, error) {
	key := "s:" + sprint
	var c CICost
	type landed struct{ repo, pr, head string }
	var landedPRs []landed
	seenPR := map[string]bool{}
	var ciLabels []string
	for i, card := range cards {
		if card["ci_for"] != "" {
			ciLabels = append(ciLabels, labels[i])
			continue
		}
		if card["state"] == "landed" && card["repo"] != "" && card["pr"] != "" && !seenPR[card["repo"]+":"+card["pr"]] {
			seenPR[card["repo"]+":"+card["pr"]] = true
			landedPRs = append(landedPRs, landed{card["repo"], card["pr"], card["head"]})
		}
	}
	c.LandedPRs = len(landedPRs)

	type ciCard struct{ label, repo, pr, head, base string }
	var ci []ciCard
	if len(ciLabels) > 0 {
		pipe := client.Pipeline()
		cmds := make([]*redis.SliceCmd, len(ciLabels))
		for i, l := range ciLabels {
			cmds[i] = pipe.HMGet(ctx, key+":card:"+l, ciCardFields...)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return c, fmt.Errorf("read the ci cards of %s: %w", key, err)
		}
		for i, cmd := range cmds {
			v := cmd.Val()
			ci = append(ci, ciCard{ciLabels[i], sv(v[0]), sv(v[1]), sv(v[2]), sv(v[3])})
		}
	}

	// One record per head: the key is ci:<repo>:<head> (10.3).
	records := map[string]map[string]string{}
	var heads []string
	for _, k := range ci {
		if k.repo == "" || k.head == "" {
			continue
		}
		rk := "ci:" + k.repo + ":" + k.head
		if _, ok := records[rk]; !ok {
			records[rk] = nil
			heads = append(heads, rk)
		}
	}
	if len(heads) > 0 {
		pipe := client.Pipeline()
		cmds := make([]*redis.SliceCmd, len(heads))
		for i, rk := range heads {
			cmds[i] = pipe.HMGet(ctx, rk, ciRecordFields...)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return c, fmt.Errorf("read the ci records of %s: %w", key, err)
		}
		for i, cmd := range cmds {
			rec := map[string]string{}
			for j, v := range cmd.Val() {
				if s, ok := v.(string); ok {
					rec[ciRecordFields[j]] = s
				}
			}
			records[heads[i]] = rec
		}
	}

	// x/y: unique head-and-base pairs, OK only for the pair's own base.
	type pair struct{ repo, head, base string }
	seen := map[pair]bool{}
	byLabel := map[string]ciCard{}
	for _, k := range ci {
		byLabel[k.label] = k
		if k.repo == "" || k.head == "" {
			continue
		}
		p := pair{k.repo, k.head, k.base}
		if seen[p] {
			continue
		}
		seen[p] = true
		c.Heads++
		rec := records["ci:"+k.repo+":"+k.head]
		if rec["verdict"] == "OK" && rec["base"] == k.base {
			c.OK++
		}
	}

	// Every attempt, from its `ci end` receipt.
	if len(byLabel) > 0 {
		start := "-"
		for {
			msgs, err := client.XRangeN(ctx, key+":log", start, "+", 1000).Result()
			if err != nil {
				return c, fmt.Errorf("read %s:log: %w", key, err)
			}
			for _, m := range msgs {
				if fmt.Sprint(m.Values["kind"]) != "ci end" {
					continue
				}
				k, ok := byLabel[fmt.Sprint(m.Values["id"])]
				if !ok {
					continue
				}
				c.Attempts++
				if w, ok := attemptWall(m.Values, records["ci:"+k.repo+":"+k.head], sprint+"/"+k.label); ok {
					c.BenchS += w
				} else {
					c.Unmeasured++
				}
			}
			if len(msgs) < 1000 {
				break
			}
			start = "(" + msgs[len(msgs)-1].ID
		}
	}

	// Wall per landed PR: the landed head's record, cut -> final verdict.
	for _, p := range landedPRs {
		for _, k := range ci {
			if k.repo != p.repo || k.pr != p.pr || !sameSHA(k.head, p.head) {
				continue
			}
			rec := records["ci:"+k.repo+":"+k.head]
			cut, err1 := strconv.ParseInt(rec["cut_at"], 10, 64)
			end, err2 := strconv.ParseInt(rec["end_at"], 10, 64)
			if err1 == nil && err2 == nil && end >= cut && cut > 0 {
				c.WallS += (end - cut) / 1000
				c.WallPRs++
			}
			break
		}
	}
	return c, nil
}

// attemptWall is one attempt's bench seconds: `wall_s=<n>` on its `ci end`
// receipt, else the record's wall_s when the record's latest pointer is this
// very attempt of this card. Anything else is unmeasured.
func attemptWall(v map[string]any, rec map[string]string, card string) (int64, bool) {
	for _, f := range strings.Fields(fmt.Sprint(v["evidence"])) {
		if w, ok := strings.CutPrefix(f, "wall_s="); ok {
			n, err := strconv.ParseInt(w, 10, 64)
			return n, err == nil && n >= 0
		}
	}
	if rec["card"] != card || rec["attempt"] != fmt.Sprint(v["attempt"]) {
		return 0, false
	}
	n, err := strconv.ParseInt(rec["wall_s"], 10, 64)
	return n, err == nil && n >= 0
}

func sv(v any) string {
	s, _ := v.(string)
	return s
}

// minutes prints seconds/n as minutes to two places, or - when n is 0.
func minutes(s int64, n int) string {
	if n == 0 {
		return "-"
	}
	m := strconv.FormatFloat(float64(s)/60/float64(n), 'f', 2, 64)
	m = strings.TrimRight(strings.TrimRight(m, "0"), ".")
	return m
}

func (c CICost) measured() int { return c.Attempts - c.Unmeasured }

func (c CICost) benchPerLanded() string {
	if c.measured() == 0 {
		return "-"
	}
	return minutes(c.BenchS, c.LandedPRs)
}

func (c CICost) benchMin() string {
	if c.measured() == 0 {
		return "-"
	}
	return minutes(c.BenchS, 1)
}

// printCI prints the ci cost line.
func printCI(out io.Writer, sprint string, c CICost) {
	fmt.Fprintf(out, "FOLD CI COST sprint=%s heads=%d ok=%d attempts=%d unmeasured=%d bench_min=%s landed_prs=%d bench_min_per_landed=%s wall_min_per_landed=%s wall_unmeasured=%d\n",
		sprint, c.Heads, c.OK, c.Attempts, c.Unmeasured, c.benchMin(), c.LandedPRs,
		c.benchPerLanded(), minutes(c.WallS, c.WallPRs), c.LandedPRs-c.WallPRs)
}

// ciSexp is the ci cost as the fold record carries it into nova-work.
func ciSexp(c CICost) string {
	return fmt.Sprintf("(:heads %d :ok %d :attempts %d :unmeasured %d :bench-min %s :landed-prs %d :bench-min-per-landed %s :wall-min-per-landed %s :wall-unmeasured %d)",
		c.Heads, c.OK, c.Attempts, c.Unmeasured, q(c.benchMin()), c.LandedPRs,
		q(c.benchPerLanded()), q(minutes(c.WallS, c.WallPRs)), c.LandedPRs-c.WallPRs)
}
