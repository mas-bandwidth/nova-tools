// Package plan routes a CI run's shards onto the self-hosted fleet while the
// fleet has idle capacity and spills the remainder onto GitHub-hosted runners.
//
// It is the arithmetic behind the plan job at the head of
// .github/workflows/ci.yml. GitHub cannot move a job to a different runner once
// it is queued -- a job's runs-on is fixed when the job is created -- so the
// routing happens before the test jobs exist: the plan job reads the
// repository's runners through the Actions API, counts online-and-idle runners
// per pool, holds back a reserve so a card sharing the bench can still find a
// runner, and writes one runs-on value per shard. A pool with idle capacity
// takes its shards; a pool that is short hands the rest to the hosted image for
// that OS.
package plan

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// Runner is one Actions runner as GET /repos/{owner}/{repo}/actions/runners
// reports it: online and idle is Status "online" with Busy false.
type Runner struct {
	Name   string
	Status string // "online" or "offline"
	Busy   bool
	Labels []string
}

// Label is the API's label shape: an object with a name, not a bare string.
type Label struct {
	Name string `json:"name"`
}

// apiRunner is the wire shape of one runner in the API's response.
type apiRunner struct {
	Name   string  `json:"name"`
	Status string  `json:"status"`
	Busy   bool    `json:"busy"`
	Labels []Label `json:"labels"`
}

// RunnerPage is one page of the runners endpoint.
type RunnerPage struct {
	TotalCount int      `json:"total_count"`
	Runners    []Runner `json:"-"`
}

// ParseRunnerPage reads one page of the runners endpoint, accepting either the
// API's object ({"total_count":N,"runners":[...]}) or a bare array of runners,
// which is what a fixture of runner states looks like.
func ParseRunnerPage(raw []byte) (RunnerPage, error) {
	trimmed := bytes.TrimSpace(raw)
	var page RunnerPage
	if len(trimmed) > 0 && trimmed[0] == '[' {
		list, err := decodeRunners(trimmed)
		if err != nil {
			return RunnerPage{}, err
		}
		page.Runners = list
		page.TotalCount = len(list)
		return page, nil
	}
	var env struct {
		TotalCount int         `json:"total_count"`
		Runners    []apiRunner `json:"runners"`
	}
	if err := json.Unmarshal(trimmed, &env); err != nil {
		return RunnerPage{}, err
	}
	page.TotalCount = env.TotalCount
	page.Runners = fromAPI(env.Runners)
	return page, nil
}

// ParseRunners reads a fixture or response into runners only.
func ParseRunners(raw []byte) ([]Runner, error) {
	page, err := ParseRunnerPage(raw)
	if err != nil {
		return nil, err
	}
	return page.Runners, nil
}

func decodeRunners(raw []byte) ([]Runner, error) {
	var list []apiRunner
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	return fromAPI(list), nil
}

func fromAPI(in []apiRunner) []Runner {
	out := make([]Runner, 0, len(in))
	for _, r := range in {
		labels := make([]string, 0, len(r.Labels))
		for _, l := range r.Labels {
			labels = append(labels, l.Name)
		}
		out = append(out, Runner{Name: r.Name, Status: r.Status, Busy: r.Busy, Labels: labels})
	}
	return out
}

// Pool is a self-hosted runner pool: the group label that selects it, the full
// runs-on label array a job carries to land on it, and the hosted image that
// catches the shards the pool cannot take.
type Pool struct {
	Group  string
	Labels []string
	Hosted string
}

// Pools is the fleet as four pools, one per group label. The card names them
// (2026-09-17): studio 16, space 16, hulk 24, vision 20, 76 runners in all.
var Pools = map[string]Pool{
	"studio": {Group: "studio", Labels: []string{"self-hosted", "macOS", "ARM64", "studio"}, Hosted: "macos-latest"},
	"space":  {Group: "space", Labels: []string{"self-hosted", "linux", "x64", "space"}, Hosted: "ubuntu-latest"},
	"hulk":   {Group: "hulk", Labels: []string{"self-hosted", "linux", "x64", "hulk"}, Hosted: "ubuntu-latest"},
	"vision": {Group: "vision", Labels: []string{"self-hosted", "linux", "x64", "vision"}, Hosted: "ubuntu-latest"},
}

// Assignment is one runs-on slot the workflow will look up by Key. Pool is the
// self-hosted pool the shard prefers; an empty Pool is a leg that is always
// hosted (the linux and windows legs of the merge gate), and Hosted is its
// image. Shards is how many runners the leg needs at once: the test matrix asks
// for one per shard, the merge gate's darwin leg asks for its six together.
type Assignment struct {
	Key    string
	Pool   string
	Shards int
	Hosted string
}

// Reserve is the runners held back from every pool: the card says a reserve of
// two, so a card on the same bench can still find a runner while CI runs.
const Reserve = 2

// FleetUnconditional reports whether the event keeps the whole fleet no matter
// what the API says: a push to dev or main and the nightly schedule.
func FleetUnconditional(event string) bool {
	return event == "push" || event == "schedule"
}

// Assignments is the shard demand the workflow will have on each event, in the
// fixed order the plan assigns them. The counts mirror test-packages and the
// merge gate in .github/workflows/ci.yml; keeping them here makes the routing
// testable without a runner.
func Assignments(event string) []Assignment {
	space, studio := 8, 16
	switch event {
	case "pull_request":
		space, studio = 4, 4
	case "merge_group":
		space, studio = 8, 2
	}
	var out []Assignment
	for i := 1; i <= space; i++ {
		out = append(out, Assignment{Key: "space-" + strconv.Itoa(i), Pool: "space", Shards: 1})
	}
	for i := 1; i <= studio; i++ {
		out = append(out, Assignment{Key: "studio-" + strconv.Itoa(i), Pool: "studio", Shards: 1})
	}
	if event == "merge_group" {
		out = append(out,
			Assignment{Key: "merge-linux", Hosted: "ubuntu-latest", Shards: 6},
			Assignment{Key: "merge-darwin", Pool: "studio", Shards: 6},
			Assignment{Key: "merge-windows", Hosted: "windows-latest", Shards: 6},
		)
	}
	return out
}

// Result is the plan's verdict: RunsOn is the JSON object the workflow indexes,
// Spilled names the keys that went to hosted runners, and Available is the idle
// count left to each pool after the reserve.
type Result struct {
	RunsOn    map[string]any
	Spilled   []string
	Available map[string]int
}

// Route decides every assignment, in order, so that two legs drawing on one
// pool share a single dwindling count instead of each seeing the pool's whole
// idle capacity. reserve runners are held back from every pool. When fleet is
// true (a push or the nightly schedule) every self-hosted leg keeps the fleet
// unconditionally and no idle count is consulted.
func Route(runners []Runner, assignments []Assignment, reserve int, fleet bool) Result {
	available := make(map[string]int, len(Pools))
	for name, pool := range Pools {
		n := 0
		if !fleet {
			for _, r := range runners {
				if r.Status != "online" || r.Busy {
					continue
				}
				if hasLabel(r, pool.Group) {
					n++
				}
			}
			n -= reserve
			if n < 0 {
				n = 0
			}
		}
		available[name] = n
	}

	res := Result{
		RunsOn:    make(map[string]any, len(assignments)),
		Available: available,
	}
	for _, a := range assignments {
		pool, isPool := Pools[a.Pool]
		need := a.Shards
		if need < 1 {
			need = 1
		}
		if !isPool {
			res.RunsOn[a.Key] = hostedImage(a.Hosted)
			continue
		}
		if fleet || available[a.Pool] >= need {
			res.RunsOn[a.Key] = pool.Labels
			if !fleet {
				available[a.Pool] -= need
			}
			continue
		}
		res.RunsOn[a.Key] = pool.Hosted
		res.Spilled = append(res.Spilled, a.Key)
	}
	return res
}

func hostedImage(img string) string {
	if img == "" {
		return "ubuntu-latest"
	}
	return img
}

func hasLabel(r Runner, label string) bool {
	for _, l := range r.Labels {
		if l == label {
			return true
		}
	}
	return false
}
