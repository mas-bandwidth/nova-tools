package swarm

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/lanes"
)

// fakeQueue lays out a bench the way docs/SPEC-FLEET-KUBE.md Part 2 names it: cards wait in
// <bench>/queue/lanes/<lane>/<name>.card and a take lands in <bench>/taken/.
func fakeQueue(t *testing.T, cards map[string][]string) string {
	t.Helper()
	bench := t.TempDir()
	for lane, names := range cards {
		dir := filepath.Join(bench, "queue", lanes.LanesDir, lane)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range names {
			if err := os.WriteFile(filepath.Join(dir, name+CardExt), []byte("card "+name+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return bench
}

// fakeSubmitter records every Job the puller creates instead of reaching a cluster.
type fakeSubmitter struct {
	mu   sync.Mutex
	jobs []KubeJob
}

func (f *fakeSubmitter) submit(_ string, job KubeJob) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jobs = append(f.jobs, job)
	return nil
}

func submitInput(bench, worker string, cores int, load1 float64, sub *fakeSubmitter) PullSubmitInput {
	return PullSubmitInput{
		Bench:  bench,
		Worker: worker,
		Cores:  cores,
		Load1:  load1,
		Image:  "nova-card:test",
		Runner: "nova-card run",
		Submit: sub.submit,
	}
}

// 15. nova-swarm pull --submit (one replica) lists queue/lanes/, takes one card by
// rename(<name>.card, taken/<worker>-<name>.card), so two pullers cannot take one card
// because the rename decides.
func TestPullerTakesOneCardByAtomicRename(t *testing.T) {
	bench := fakeQueue(t, map[string][]string{lanes.Next: {"only"}})
	sub := &fakeSubmitter{}

	var wg sync.WaitGroup
	results := make([]PullSubmitResult, 2)
	errs := make([]error, 2)
	start := make(chan struct{})
	for i, worker := range []string{"w1", "w2"} {
		wg.Add(1)
		go func(i int, worker string) {
			defer wg.Done()
			<-start
			results[i], errs[i] = PullSubmit(submitInput(bench, worker, 8, 0, sub))
		}(i, worker)
	}
	close(start)
	wg.Wait()

	took := 0
	winner := ""
	for i, r := range results {
		if errs[i] != nil {
			t.Fatalf("puller %d: %v", i, errs[i])
		}
		if r.Card != "" {
			took++
			winner = r.Worker
		}
	}
	if took != 1 {
		t.Fatalf("%d pullers took the one card, want exactly 1 (results %+v)", took, results)
	}
	if _, err := os.Stat(filepath.Join(bench, "taken", winner+"-only"+CardExt)); err != nil {
		t.Fatalf("taken/%s-only.card missing: %v", winner, err)
	}
	if _, err := os.Stat(filepath.Join(bench, "queue", lanes.LanesDir, lanes.Next, "only"+CardExt)); !os.IsNotExist(err) {
		t.Fatalf("the card is still in its lane after the take: %v", err)
	}
	if len(sub.jobs) != 1 {
		t.Fatalf("%d Jobs created for one card, want 1", len(sub.jobs))
	}

	// The lanes are the order: a red card is taken before a next card.
	bench = fakeQueue(t, map[string][]string{lanes.Next: {"a"}, lanes.Red: {"z"}})
	r, err := PullSubmit(submitInput(bench, "w1", 8, 0, &fakeSubmitter{}))
	if err != nil {
		t.Fatal(err)
	}
	if r.Card != "z" || r.Lane != lanes.Red {
		t.Fatalf("took %q from %q, want z from red", r.Card, r.Lane)
	}
}

// 16. Each Job requests cpu 1, memory 2Gi, ephemeral-storage 2Gi and sets limits.memory
// 2Gi, and the node reserves the fixed 25 GiB floor, so the scheduler's admission of a 2 GiB
// request against allocatable is the two memory terms of the capacity line.
func TestJobRequestsAndLimitsAreTheCapacityLine(t *testing.T) {
	bench := fakeQueue(t, map[string][]string{lanes.Small: {"Card_One.v2"}})
	sub := &fakeSubmitter{}
	if _, err := PullSubmit(submitInput(bench, "w1", 8, 0, sub)); err != nil {
		t.Fatal(err)
	}
	if len(sub.jobs) != 1 {
		t.Fatalf("%d Jobs, want 1", len(sub.jobs))
	}
	type m = map[string]any
	job := sub.jobs[0]
	if job["kind"] != "Job" {
		t.Fatalf("kind = %v, want Job", job["kind"])
	}
	if name := job["metadata"].(m)["name"].(string); !validJobName(name) {
		t.Fatalf("Job name %q is not a DNS-1123 label", name)
	}
	cs := job["spec"].(m)["template"].(m)["spec"].(m)["containers"].([]m)
	if len(cs) != 1 {
		t.Fatalf("%d containers, want 1", len(cs))
	}
	res := cs[0]["resources"].(m)
	for k, v := range map[string]string{"cpu": "1", "memory": "2Gi", "ephemeral-storage": "2Gi"} {
		if got := res["requests"].(m)[k]; got != v {
			t.Fatalf("requests.%s = %v, want %s", k, got, v)
		}
	}
	if got := res["limits"].(m)["memory"]; got != "2Gi" {
		t.Fatalf("limits.memory = %v, want 2Gi", got)
	}
	if KubeReservedFloorGiB != 25 {
		t.Fatalf("reserved floor = %d GiB, want 25", KubeReservedFloorGiB)
	}
	// The scheduler admits floor(allocatable/request) Jobs per resource; with the 25 GiB
	// floor reserved that is exactly the disk and memory terms of the capacity line.
	for _, c := range []struct{ freeGB, memFreeGB int }{{100, 100}, {30, 64}, {200, 9}, {25, 50}, {10, 10}} {
		got := SchedulerAdmits(c.freeGB, c.memFreeGB)
		want := AdmissionLine(1<<20, 0, c.freeGB, c.memFreeGB)
		if got != want {
			t.Fatalf("free=%d memfree=%d: scheduler admits %d, capacity line memory terms %d", c.freeGB, c.memFreeGB, got, want)
		}
	}
}

// 17. The puller reads load1 and declines to submit while cores*1.5 - load1 <= 0.
func TestPullerDeclinesBelowTheLoadLine(t *testing.T) {
	bench := fakeQueue(t, map[string][]string{lanes.Next: {"a"}})
	sub := &fakeSubmitter{}

	// cores 4: the load line is 6.
	r, err := PullSubmit(submitInput(bench, "w1", 4, 6, sub))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Declined || r.Card != "" {
		t.Fatalf("at load1=6 on 4 cores the puller took %q (declined=%t), want a decline", r.Card, r.Declined)
	}
	if len(sub.jobs) != 0 {
		t.Fatalf("declined puller created %d Jobs", len(sub.jobs))
	}
	if _, err := os.Stat(filepath.Join(bench, "queue", lanes.LanesDir, lanes.Next, "a"+CardExt)); err != nil {
		t.Fatalf("a decline moved the card: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(bench, "taken")); len(entries) != 0 {
		t.Fatalf("a decline took %d cards", len(entries))
	}

	// Just under the line the puller submits.
	r, err = PullSubmit(submitInput(bench, "w1", 4, 5.9, sub))
	if err != nil {
		t.Fatal(err)
	}
	if r.Declined || r.Card != "a" || len(sub.jobs) != 1 {
		t.Fatalf("at load1=5.9 on 4 cores: card=%q declined=%t jobs=%d, want a taken and one Job", r.Card, r.Declined, len(sub.jobs))
	}
}

// validJobName reports whether s is a DNS-1123 label.
func validJobName(s string) bool {
	if s == "" || len(s) > 63 || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
			return false
		}
	}
	return true
}
