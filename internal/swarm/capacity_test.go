package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE CAPACITY TEMPLATE IS #176'S NEAR-TERM ENDPOINT: a manual census and routing log
// published, reviewed and matched against ready work, without double-counting shared
// pools. The issue says no scheduler ships today and a friend's own choices fill the
// form, so this is the stable artifact the template carries -- the offer's named
// fields (friend, instance, bench, model identity and basis, harness, supported task
// types, demonstrated strengths and limits, permitted scope, current availability,
// concurrency, expected queue/latency and shared-limit pool references), the
// coordinator-side routing log (ready work, compatible offers, incompatibility reason,
// shared pool count once, stale-offer exclusion), the four acknowledgement rows the
// issue names (idle compatible pool receiving ready work, incompatible offer skipped,
// shared capacity counted once, stale offer excluded), the unknown/measured split
// (missing contact is unknown and stale capacity is not proof of failure and not
// proof of consent), and the four execution capability groups the SPEC-WORK friend
// section distinguishes (coordinator, direct worker, one-shot, swarm and local).
// And the negative half, which is the issue's own hard line on credentials: no API
// key, no credential value, no private host detail, no live bench path is ever
// published, names/models/harnesses/benches stay placeholders, and WrapTemplate
// refuses it the way `setup` is refused because wrapping a task inside a capacity
// form would produce a prompt that is neither.
func TestTheCapacityTemplateIsTheCensusAndRoutingLogNotAScheduler(t *testing.T) {
	body, err := Template("capacity")
	if err != nil {
		t.Fatalf("template capacity: %v", err)
	}
	for _, want := range []string{
		"capacity — one friend's offered capacity and the manual routing log (#176)",
		"offered by: <",
		"reviewed with: <",
		"# The offer (the friend's own half)",
		"expires: <",
		"friend: <",
		"instance: <",
		"bench: <",
		"model identity: <",
		"basis: <",
		"harness: <",
		"supported task types: <",
		"demonstrated strengths: <",
		"demonstrated limits: <",
		"permitted scope: <",
		"current availability: <",
		"concurrency: <",
		"expected queue/latency: <",
		"shared-limit pools: <",
		"# The routing log (the coordinator's half)",
		"ready work: <",
		"compatible offers: <",
		"incompatible offers: <",
		"shared pool share: <",
		"stale offers excluded: <",
		"idle compatible pool receiving ready work",
		"an incompatible offer being skipped",
		"shared capacity counted once",
		"a stale offer excluded",
		"missing contact is unknown",
		"stale capacity is not proof of failure and not proof of consent",
		"coordinator capacity",
		"direct worker capacity",
		"one-shot capacity",
		"swarm capacity",
		"local capacity",
		"no key",
		"no token",
		"no private host detail",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the capacity template does not carry %q", want)
		}
	}
	lower := strings.ToLower(body)
	for _, private := range []string{"rowan", "freddy", "stella", "emma", "johnny", "glenn", "deepseek", "age1", "sk-", "ghp_", "mas-bandwidth", "/users/", "/opt/homebrew"} {
		if strings.Contains(lower, private) {
			t.Errorf("the capacity template publishes a private or live detail: %q", private)
		}
	}
	// THE BANNER AND THE REFUSAL NAME THE SAME SET. A name Template answers to that
	// the list omits is the drift that lost `worker` once, and `capacity` lands today
	// in the same place the setup form of #184 did.
	if names := strings.Join(TemplateNames(), ","); !strings.Contains(names, "capacity") {
		t.Errorf("TemplateNames must carry capacity so the banner and the refusal name the same set: %s", names)
	}
	// It is a form, not a task's conditions: `add --template capacity` is refused the
	// way `result` and `setup` are, because wrapping a task inside an offer/routing
	// form produces a prompt that is neither a task nor a form a person and a friend
	// fill together.
	if _, err := WrapTemplate("capacity", 3, []byte("a task")); err == nil {
		t.Error("`capacity` is the issue #176 census-and-routing-log form and not a task template; add --template capacity must be refused the way ` result` and `setup` are")
	}
}

// Red test: pull-never-exceeds-the-capacity-line (docs/SPEC-JOBS.md section 7).
//
// The capacity line min(cores*1.5 - load1, (free_gb-25)/2, memfree_gb/2) bounds how many
// workers nova-swarm pull may start on a bench. A full bench stops pulling and leaves every
// card in queue/.
func TestPullNeverExceedsTheCapacityLine(t *testing.T) {
	// The line is the smallest arm, floored at zero: CPU headroom, disk above the 25G
	// floor, and memory.
	for _, c := range []struct {
		cores, load1, freeGB, memFreeGB, want int
	}{
		{8, 4, 100, 32, 8}, // cores*3/2 - load1 = 8 is the smallest
		{16, 0, 30, 8, 2},  // (30-25)/2 = 2 is the smallest
		{16, 0, 100, 8, 4}, // 8/2 = 4 is the smallest
		{2, 10, 30, 2, 0},  // every arm is at or below zero: the line floors at 0
		{0, 0, 0, 0, 0},    // no cores is no admission
	} {
		if got := AdmissionLine(c.cores, c.load1, c.freeGB, c.memFreeGB); got != c.want {
			t.Errorf("AdmissionLine(%d,%d,%d,%d) = %d, want %d",
				c.cores, c.load1, c.freeGB, c.memFreeGB, got, c.want)
		}
	}

	// Admission is the line minus the workers already on it, never more than the queue
	// holds, and never negative.
	if got := Admission(8, 8, 3); got != 0 {
		t.Fatalf("a full bench admits %d, want 0", got)
	}
	if got := Admission(8, 2, 3); got != 3 {
		t.Fatalf("a bench with 6 free admits %d of 3 queued, want 3", got)
	}
	if got := Admission(4, 1, 10); got != 3 {
		t.Fatalf("Admission(4,1,10) = %d, want 3 (the line, not the queue)", got)
	}

	bench := t.TempDir()
	queue := filepath.Join(bench, "queue")
	taken := filepath.Join(bench, "taken")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.card", "b.card", "c.card"} {
		if err := os.WriteFile(filepath.Join(queue, name), []byte("card\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// A full bench admits nothing and leaves every card in queue/.
	line := AdmissionLine(8, 4, 100, 32) // 8
	if got := Admission(line, line, 3); got != 0 {
		t.Fatalf("full bench admission = %d, want 0", got)
	}
	names, err := PullQueue(queue, taken, "w1", Admission(line, line, 3))
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 0 {
		t.Fatalf("a full bench took %v, want none", names)
	}
	if got := countCards(t, queue); got != 3 {
		t.Fatalf("a full bench left %d cards in queue/, want 3", got)
	}

	// Two workers on an 8-line bench may still take the three cards, and no more than the
	// line allows. A pull that would exceed the line takes only up to the line.
	names, err = PullQueue(queue, taken, "w1", Admission(line, 2, 3))
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 3 {
		t.Fatalf("two workers took %v, want the 3 queued cards", names)
	}
	if got := countCards(t, queue); got != 0 {
		t.Fatalf("after the pull queue/ holds %d cards, want 0", got)
	}
}

func countCards(t *testing.T, dir string) int {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.card"))
	if err != nil {
		t.Fatal(err)
	}
	return len(matches)
}
