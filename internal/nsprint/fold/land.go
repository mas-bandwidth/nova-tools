package fold

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

// LandCost is the lander's cost line (nova-tools #3139 rev 7 section 9, build
// B17): five numbers, each from the gate receipts the sprint's window holds.
// The window is s:<S> opened_at..closed_at (ms, the ids of land:<repo>:events);
// the repos and bases are the ones the sprint's units (s:<S>:units) name.
// Nothing is SCANned: the GATE events index the receipts (2.2).
type LandCost struct {
	Receipts int     // gate receipts written in the window
	CoreS    float64 // their summed core_s: voided, bisect, reruns, singles and tip gates
	// CoreUnmeasured counts receipts whose core_s does not parse; they add
	// nothing to CoreS, so a nonzero count makes CoreS a lower bound.
	CoreUnmeasured int
	Landed         int // units of the sprint with a landing receipt landed:<repo>:<unit>:<head>
	Voided         int // receipts whose batch is void (chain voided behind a red batch)
	// Bisect counts attribution gates (6.1): a receipt whose batch's members
	// are a strict, non-empty subset of an earlier RED receipt's batch on the
	// same from_tip (the singles, the prefixes, and a green prefix re-gated).
	Bisect int
	// SelectionMisses counts RED full tip gates (class full, no members, the
	// #3495 shape) of a tip that a GREEN selected gate (checks=selected)
	// produced as its train_head: the selection passed what the full set fails.
	SelectionMisses int
	SelectedGreen   int // GREEN receipts of selected gates, the misses' denominator
	// GitOps counts steps named git or git-* in receipts' steps
	// (<step>:<wall_s>:<cpu_s>:<rc> joined by ;); GitUnmeasured counts
	// receipts with no steps at all, whose git ops are unknown, never zero.
	GitOps        int
	GitUnmeasured int
}

// gateReceipt is one receipt with its batch's fields.
type gateReceipt struct {
	repo, base, batch string
	rec, bat          map[string]string
}

// readLand reads the lander's receipts for the sprint.
func readLand(ctx context.Context, client *redis.Client, sprint string, head map[string]string) (LandCost, error) {
	var c LandCost
	key := "s:" + sprint
	units, err := client.SMembers(ctx, key+":units").Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return c, fmt.Errorf("read %s:units: %w", key, err)
	}
	sort.Strings(units)
	ufields := []string{"repo", "base", "head", "landed_head"}
	pipe := client.Pipeline()
	ucmds := make([]*redis.SliceCmd, len(units))
	for i, u := range units {
		ucmds[i] = pipe.HMGet(ctx, key+":u:"+u, ufields...)
	}
	if len(units) > 0 {
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return c, fmt.Errorf("read the units of %s: %w", key, err)
		}
	}
	bases := map[string]map[string]bool{} // repo -> base
	var repos []string
	pipe = client.Pipeline()
	var lcmds []*redis.IntCmd
	for _, cmd := range ucmds {
		f := make([]string, len(ufields))
		for j, v := range cmd.Val() {
			f[j], _ = v.(string)
		}
		repo, base, h := f[0], f[1], f[3]
		if h == "" {
			h = f[2]
		}
		if repo == "" {
			continue
		}
		if bases[repo] == nil {
			bases[repo] = map[string]bool{}
			repos = append(repos, repo)
		}
		if base != "" {
			bases[repo][base] = true
		}
		if h != "" {
			lcmds = append(lcmds, pipe.Exists(ctx, "landed:"+repo+":"+unitOf(cmd)+":"+h))
		}
	}
	if len(lcmds) > 0 {
		if _, err := pipe.Exec(ctx); err != nil {
			return c, fmt.Errorf("read the landing receipts of %s: %w", key, err)
		}
		for _, cmd := range lcmds {
			if cmd.Val() == 1 {
				c.Landed++
			}
		}
	}
	sort.Strings(repos)

	lo, hi := "-", "+"
	if v := head["opened_at"]; v != "" {
		lo = v
	}
	if v := head["closed_at"]; v != "" {
		hi = v
	}
	var gates []gateReceipt
	for _, repo := range repos {
		stream := "land:" + repo + ":events"
		start := lo
		for {
			msgs, err := client.XRangeN(ctx, stream, start, hi, 1000).Result()
			if err != nil && !errors.Is(err, redis.Nil) {
				return c, fmt.Errorf("read %s: %w", stream, err)
			}
			for _, m := range msgs {
				if sv(m.Values["event"]) != "GATE" {
					continue
				}
				base, batch, attempt := sv(m.Values["base"]), sv(m.Values["batch"]), sv(m.Values["attempt"])
				if !bases[repo][base] || batch == "" || attempt == "" {
					continue
				}
				gates = append(gates, gateReceipt{repo: repo, base: base, batch: batch + ":" + attempt})
			}
			if len(msgs) < 1000 {
				break
			}
			start = "(" + msgs[len(msgs)-1].ID
		}
	}
	if len(gates) == 0 {
		return c, nil
	}
	pipe = client.Pipeline()
	rcmds := make([]*redis.MapStringStringCmd, len(gates))
	bcmds := make([]*redis.MapStringStringCmd, len(gates))
	for i, g := range gates {
		batch, _, _ := strings.Cut(g.batch, ":")
		rcmds[i] = pipe.HGetAll(ctx, "land:"+g.repo+":receipt:"+g.batch)
		bcmds[i] = pipe.HGetAll(ctx, "land:"+g.repo+":"+g.base+":batch:"+batch)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return c, fmt.Errorf("read the gate receipts of %s: %w", key, err)
	}
	for i := range gates {
		gates[i].rec, gates[i].bat = rcmds[i].Val(), bcmds[i].Val()
	}
	tallyLand(&c, gates)
	return c, nil
}

// unitOf recovers the unit id from its HMGET (the key's last segment).
func unitOf(cmd *redis.SliceCmd) string {
	args := cmd.Args()
	if len(args) < 2 {
		return ""
	}
	k, _ := args[1].(string)
	return k[strings.LastIndex(k, ":u:")+3:]
}

// tallyLand counts the five numbers over receipts in event order.
func tallyLand(c *LandCost, gates []gateReceipt) {
	type red struct {
		repo, base, fromTip string
		members             map[string]bool
	}
	var reds []red
	selectedTrain := map[string]bool{} // repo base train_head of a GREEN selected gate
	var tips []gateReceipt
	for _, g := range gates {
		if len(g.rec) == 0 {
			continue // the event outlived its receipt: nothing to count
		}
		c.Receipts++
		if n, err := strconv.ParseFloat(g.rec["core_s"], 64); err == nil && n >= 0 {
			c.CoreS += n
		} else {
			c.CoreUnmeasured++
		}
		if g.bat["state"] == "void" {
			c.Voided++
		}
		members := memberSet(g.bat["members"])
		fromTip := g.rec["from_tip"]
		for _, r := range reds {
			if r.repo == g.repo && r.base == g.base && r.fromTip == fromTip && strictSubset(members, r.members) {
				c.Bisect++
				break
			}
		}
		verdict := g.rec["verdict"]
		if verdict == "RED" && len(members) > 0 {
			reds = append(reds, red{g.repo, g.base, fromTip, members})
		}
		if verdict == "GREEN" && strings.HasPrefix(g.rec["selection"], "checks=selected") {
			c.SelectedGreen++
			if th := g.rec["train_head"]; th != "" {
				selectedTrain[g.repo+" "+g.base+" "+th] = true
			}
		}
		if g.bat["class"] == "full" && len(members) == 0 && verdict == "RED" {
			tips = append(tips, g)
		}
		steps := strings.TrimSpace(g.rec["steps"])
		if steps == "" {
			c.GitUnmeasured++
			continue
		}
		for _, s := range strings.Split(steps, ";") {
			name, _, _ := strings.Cut(strings.TrimSpace(s), ":")
			if name == "git" || strings.HasPrefix(name, "git-") {
				c.GitOps++
			}
		}
	}
	for _, g := range tips {
		if selectedTrain[g.repo+" "+g.base+" "+g.rec["from_tip"]] {
			c.SelectionMisses++
		}
	}
}

func memberSet(csv string) map[string]bool {
	m := map[string]bool{}
	for _, p := range strings.Split(csv, ",") {
		if p = strings.TrimSpace(p); p != "" {
			m[p] = true
		}
	}
	return m
}

// strictSubset: a is non-empty, every member of a is in b, and b has more.
func strictSubset(a, b map[string]bool) bool {
	if len(a) == 0 || len(a) >= len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// share prints n of d as a percent to one place, or - when d is 0.
func share(n, d int) string {
	if d == 0 {
		return "-"
	}
	return strconv.FormatFloat(100*float64(n)/float64(d), 'f', 1, 64) + "%"
}

func (c LandCost) corePerLanded() string {
	if c.Landed == 0 || c.Receipts == 0 {
		return "-"
	}
	return strconv.FormatFloat(c.CoreS/float64(c.Landed), 'f', 1, 64)
}

func (c LandCost) gitOps() string {
	if c.Receipts-c.GitUnmeasured == 0 {
		return "-"
	}
	return strconv.Itoa(c.GitOps)
}

// printLand prints the land line: the five numbers first, then their counts.
func printLand(out io.Writer, sprint string, c LandCost) {
	fmt.Fprintf(out, "FOLD LAND sprint=%s core_s_per_landed=%s voided_share=%s bisect_share=%s selection_misses=%d git_ops=%s receipts=%d core_s=%s landed=%d voided=%d bisect=%d selected_green=%d git_unmeasured=%d core_unmeasured=%d\n",
		sprint, c.corePerLanded(), share(c.Voided, c.Receipts), share(c.Bisect, c.Receipts), c.SelectionMisses, c.gitOps(),
		c.Receipts, strconv.FormatFloat(c.CoreS, 'f', 1, 64), c.Landed, c.Voided, c.Bisect, c.SelectedGreen, c.GitUnmeasured, c.CoreUnmeasured)
}
