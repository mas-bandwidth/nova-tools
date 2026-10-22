package tlc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// LoadCases reads tla/CASES.tsv under root and checks it against the tree: it
// must name every MC*.cfg exactly once and every module must exist. When the
// plan and the tree disagree the error names, in name order, every MC*.cfg the
// plan does not name, every config the plan names that has no file, and every
// row whose module is missing; it keeps its opening words so callers that match
// them still do.
func LoadCases(root string) ([]Case, error) {
	dir := filepath.Join(root, "tla")
	f, err := os.Open(filepath.Join(dir, CasesFile))
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %v", filepath.Join(dir, CasesFile), err)
	}
	defer func() { _ = f.Close() }() // ignored: the plan is opened only for reading
	cases, err := ParseCases(f)
	if err != nil {
		return nil, err
	}
	cfgs, err := filepath.Glob(filepath.Join(dir, "MC*.cfg"))
	if err != nil {
		return nil, err
	}
	var actual, named []string
	have := map[string]bool{}
	for _, p := range cfgs {
		b := filepath.Base(p)
		actual = append(actual, b)
		have[b] = true
	}
	seen := map[string]bool{}
	for _, c := range cases {
		named = append(named, c.Config)
		if seen[c.Config] {
			return nil, fmt.Errorf("%s must name every MC*.cfg exactly once: %s is named twice", CasesFile, c.Config)
		}
		seen[c.Config] = true
	}
	sort.Strings(actual)
	sort.Strings(named)
	var probs []string
	var unnamed []string
	for _, f := range actual {
		if !seen[f] {
			unnamed = append(unnamed, f)
		}
	}
	if len(unnamed) > 0 {
		probs = append(probs, "the plan does not name "+strings.Join(unnamed, ", "))
	}
	var absent []string
	for _, c := range named {
		if !have[c] {
			absent = append(absent, c)
		}
	}
	if len(absent) > 0 {
		probs = append(probs, "the plan names "+strings.Join(absent, ", ")+", which has no file")
	}
	var missing []string
	for _, c := range cases {
		if info, err := os.Stat(filepath.Join(dir, c.Module)); err != nil || !info.Mode().IsRegular() {
			missing = append(missing, c.Module)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		probs = append(probs, "missing instance module "+strings.Join(missing, ", "))
	}
	if len(probs) > 0 {
		return nil, fmt.Errorf("%s must name every MC*.cfg exactly once: %s", CasesFile, strings.Join(probs, "; "))
	}
	return cases, nil
}

// RequiredGroups returns the groups that hold a required case, sorted: the
// matrix a CI run derives.
func RequiredGroups(cases []Case) []string {
	set := map[string]bool{}
	for _, c := range cases {
		if c.Gate == "required" {
			set[c.Group] = true
		}
	}
	out := make([]string, 0, len(set))
	for g := range set {
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

// Select returns the cases one run covers: a group, or shard number shard of
// shards (every shards'th case from shard) of the whole plan or, with a group,
// of that group's cases. A group's shards of its own size are its cases one
// at a time: run --bench measures a group so, a load trough before each case.
func Select(cases []Case, group string, shards, shard int) ([]Case, error) {
	if shards < 1 || shard < 0 || shard >= shards {
		return nil, errors.New("use a positive shard count and a zero-based shard below it")
	}
	pool := cases
	if group != "" {
		pool = nil
		for _, c := range cases {
			if c.Group == group {
				pool = append(pool, c)
			}
		}
		if len(pool) == 0 {
			return nil, fmt.Errorf("unknown group: %s", group)
		}
		if shards > len(pool) {
			return nil, fmt.Errorf("shard count exceeds the %d cases of group %s", len(pool), group)
		}
	}
	if shards > len(pool) {
		return nil, errors.New("shard count exceeds the number of declared cases")
	}
	var chosen []Case
	for i := shard; i < len(pool); i += shards {
		chosen = append(chosen, pool[i])
	}
	return chosen, nil
}

// Selection is a choice of cases from the plan, as Select takes it, or with
// Balanced as SelectBalanced takes it over Records (the records the deal was
// read from, so the suite deals the same shard again). It lives here, beside
// the choosers, and not in suite.go: which cases a run takes decides no result,
// so a new way to choose them stales no record (fingerprint.go). The zero value
// selects every case.
type Selection struct {
	Group         string
	Shards, Shard int
	Balanced      bool
	Records       []Record
}

// Choose is the selection's cases from the plan, in plan order. Shards 0 is
// one shard.
func (s Selection) Choose(cases []Case) ([]Case, error) {
	shards := s.Shards
	if shards == 0 {
		shards = 1
	}
	if s.Balanced {
		return SelectBalanced(cases, s.Records, s.Group, shards, s.Shard)
	}
	return Select(cases, s.Group, shards, s.Shard)
}

// UnrecordedSeconds is the time a case with no usable record is dealt at: the
// whole bounded budget, so it never shares a shard on a guess.
const UnrecordedSeconds = float64(BoundedCap / time.Second)

// recordedSeconds maps each config to its recorded seconds.
func recordedSeconds(records []Record) map[string]float64 {
	seconds := map[string]float64{}
	for _, r := range records {
		if s, err := strconv.ParseFloat(r.Seconds, 64); err == nil && s >= 0 {
			seconds[r.Config] = s
		}
	}
	return seconds
}

// dealBalanced deals group's cases into shards by recorded time, longest
// first, each to the shard with the least time so far (the lower shard on a
// tie; equal times in plan order), and returns each shard's cases in plan
// order and its total seconds. The deal is a function of the plan and the
// records alone, so the planner and every shard's run agree on it.
func dealBalanced(cases []Case, records []Record, group string, shards int) ([][]Case, []float64, error) {
	pool, err := Select(cases, group, 1, 0)
	if err != nil {
		return nil, nil, err
	}
	if shards < 1 || shards > len(pool) {
		return nil, nil, fmt.Errorf("use a shard count from 1 to the %d cases of group %s", len(pool), group)
	}
	seconds := recordedSeconds(records)
	cost := make([]float64, len(pool))
	order := make([]int, len(pool))
	for i, c := range pool {
		s, ok := seconds[c.Config]
		if !ok {
			s = UnrecordedSeconds
		}
		cost[i], order[i] = s, i
	}
	sort.SliceStable(order, func(a, b int) bool { return cost[order[a]] > cost[order[b]] })
	owner := make([]int, len(pool))
	sums := make([]float64, shards)
	for _, i := range order {
		best := 0
		for k := 1; k < shards; k++ {
			if sums[k] < sums[best] {
				best = k
			}
		}
		owner[i] = best
		sums[best] += cost[i]
	}
	dealt := make([][]Case, shards)
	for i, c := range pool {
		dealt[owner[i]] = append(dealt[owner[i]], c)
	}
	return dealt, sums, nil
}

// SelectBalanced is shard number shard of shards of group's cases, dealt by
// recorded time (see GroupShards) rather than Select's every-shards'th case.
func SelectBalanced(cases []Case, records []Record, group string, shards, shard int) ([]Case, error) {
	if shard < 0 || shard >= shards {
		return nil, errors.New("use a positive shard count and a zero-based shard below it")
	}
	dealt, _, err := dealBalanced(cases, records, group, shards)
	if err != nil {
		return nil, err
	}
	return dealt[shard], nil
}

// GroupShards is the fewest shards of group's cases, dealt by SelectBalanced,
// that keep each shard within maxSeconds of recorded TLC time, so a bounded run
// of every shard fits its budget where the group as a whole does not. A case
// with no usable record counts as UnrecordedSeconds. A shard of one case fits
// whatever its time: a case cannot be split further, and each case is held to
// the bounded budget on its own by its record (internal/ci: the TLC records
// rule).
func GroupShards(cases []Case, records []Record, group string, maxSeconds float64) (int, error) {
	if !(maxSeconds > 0) {
		return 0, errors.New("the seconds a shard may hold must be positive")
	}
	pool, err := Select(cases, group, 1, 0)
	if err != nil {
		return 0, err
	}
	for shards := 1; shards < len(pool); shards++ {
		dealt, sums, err := dealBalanced(cases, records, group, shards)
		if err != nil {
			return 0, err
		}
		fits := true
		for k := range dealt {
			if sums[k] > maxSeconds && len(dealt[k]) > 1 {
				fits = false
				break
			}
		}
		if fits {
			return shards, nil
		}
	}
	return len(pool), nil
}
