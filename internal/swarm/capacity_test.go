package swarm

import (
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
