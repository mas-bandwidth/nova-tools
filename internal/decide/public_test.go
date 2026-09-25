package decide

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Stella's re-review of #1327 at 833514e1: R1, R3 and R5.

// privateRegistry is a registry whose every string is a synthetic private
// marker: the names, the lineages and the lanes. Nothing in it is public, and
// registry validation does not make it so -- a lane copied out of a private
// project is still private when it is configured locally.
func privateRegistry(t *testing.T) *Registry {
	t.Helper()
	reg, err := ParseRegistry([]byte(`{"minds":[
	  {"name":"codename-zeus","lineage":"acme-internal","height":0,"lanes":["project-manhattan"],"availability":"available","ask":"card"},
	  {"name":"codename-hera","lineage":"acme-internal","height":2,"lanes":["kepler-prime"],"availability":"available","ask":"child"},
	  {"name":"codename-ares","lineage":"umbrella-corp","height":2,"lanes":["blacksite-9"],"availability":"available","ask":"child"},
	  {"name":"codename-hades","lineage":"tyrell-division","height":3,"lanes":["nostromo"],"availability":"available","ask":"bus"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// privateMarkers is every string that registry holds.
var privateMarkers = []string{
	"codename", "zeus", "hera", "ares", "hades",
	"acme-internal", "umbrella-corp", "tyrell-division",
	"project-manhattan", "kepler-prime", "blacksite-9", "nostromo",
}

// payload is everything one Decide call would put on the wire: the state text
// and every string in the questions -- names, instructions and criteria alike.
type payload struct {
	State     string              `json:"state"`
	Questions map[string]Question `json:"questions"`
}

// recorder captures the whole payload and answers with the option at index pick.
type recorder struct {
	payloads []payload
	pick     int
	conf     float64
	usage    Usage
	err      error
	calls    int
}

func (r *recorder) Decide(_ context.Context, state string, qs map[string]Question) (map[string]Answer, Usage, error) {
	r.calls++
	r.payloads = append(r.payloads, payload{State: state, Questions: qs})
	if r.err != nil {
		return nil, Usage{}, r.err
	}
	q := qs[RungQuestion]
	names := make([]string, 0, len(q.Choice))
	for name := range q.Choice {
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil, Usage{}, errNoRungQuestion
	}
	// The options are opaque ids, so pick by their own order.
	choice := ""
	for _, name := range names {
		if choice == "" || name < choice {
			choice = name
		}
	}
	if r.pick > 0 {
		sorted := append([]string(nil), names...)
		for i := range sorted {
			for j := i + 1; j < len(sorted); j++ {
				if sorted[j] < sorted[i] {
					sorted[i], sorted[j] = sorted[j], sorted[i]
				}
			}
		}
		if r.pick < len(sorted) {
			choice = sorted[r.pick]
		}
	}
	return map[string]Answer{RungQuestion: {Type: "choice", Choice: choice, Confidence: r.conf}}, r.usage, nil
}

// text is everything the recorder was sent, as one searchable blob.
func (r *recorder) text(t *testing.T) string {
	t.Helper()
	raw, err := json.Marshal(r.payloads)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// (R1) No registry string crosses the public-input boundary. The state carries
// enumerated tokens only, and the questions carry OPAQUE option ids -- not a
// mind's name, not its lineage, not the lane it owns.
func TestNoRegistryStringReachesTheProvider(t *testing.T) {
	reg := privateRegistry(t)
	u := Unit{ID: "u-1", Kind: KindNewVerb, Files: 3, Packages: 1, Lanes: 1, LaneOwner: "kepler-prime"}
	rec := &recorder{conf: 0.95}
	res, err := RouteJev(context.Background(), rec, reg, u, DefaultFloor)
	if err != nil {
		t.Fatal(err)
	}
	if rec.calls != 1 {
		t.Fatalf("the provider was called %d times, want 1", rec.calls)
	}
	blob := strings.ToLower(rec.text(t))
	for _, marker := range privateMarkers {
		if strings.Contains(blob, strings.ToLower(marker)) {
			t.Errorf("the provider payload carries the registry string %q:\n%s", marker, rec.text(t))
		}
	}
	// The options are opaque, and the answer still maps back to a real mind.
	opaque := regexp.MustCompile(`^rung-\d+$`)
	for name := range rec.payloads[0].Questions[RungQuestion].Choice {
		if !opaque.MatchString(name) {
			t.Errorf("the option %q is not an opaque id", name)
		}
	}
	if res.Rung.Name == "" {
		t.Error("the opaque answer did not map back to a mind")
	}
	if res.Source != SourceJev {
		t.Errorf("source = %q, want %q: the provider's choice must still be honoured", res.Source, SourceJev)
	}
}

// The provider's opaque choice picks the mind at that position, and nothing
// else: option n is the nth eligible rung.
func TestAnOpaqueChoiceMapsBackToItsMind(t *testing.T) {
	reg := privateRegistry(t)
	u := Unit{ID: "u-2", Kind: KindNewVerb, Files: 3, Packages: 1, Lanes: 1}
	first := &recorder{conf: 0.95, pick: 0}
	low, err := RouteJev(context.Background(), first, reg, u, DefaultFloor)
	if err != nil {
		t.Fatal(err)
	}
	second := &recorder{conf: 0.95, pick: 1}
	high, err := RouteJev(context.Background(), second, reg, u, DefaultFloor)
	if err != nil {
		t.Fatal(err)
	}
	if low.Rung.Name == high.Rung.Name {
		t.Fatalf("two different options answered the same mind (%s)", low.Rung.Name)
	}
	for _, m := range []Mind{low.Rung, high.Rung} {
		if _, ok := reg.ByName(m.Name); !ok {
			t.Errorf("the answer %q is not a mind of this registry", m.Name)
		}
	}
}

// Public is the boundary itself: an allowlist of enumerated values, and a value
// outside it never leaves.
func TestPublicIsAnAllowlist(t *testing.T) {
	reg := privateRegistry(t)
	shape, err := Unit{ID: "u", Kind: KindRebase, Files: 2, LaneOwner: "project-manhattan"}.Public(reg)
	if err != nil {
		t.Fatalf("a well-formed unit has a public projection: %v", err)
	}
	for field, value := range shape {
		allowed, ok := publicAllowlist[field]
		if !ok {
			t.Errorf("the projection carries the field %q, which is not on the allowlist", field)
			continue
		}
		found := false
		for _, a := range allowed {
			if a == value {
				found = true
			}
		}
		if !found {
			t.Errorf("the field %s carries %q, which is not one of %v", field, value, allowed)
		}
	}
	if lane := shape["lane"]; lane == "project-manhattan" {
		t.Error("the lane's own spelling crossed the boundary")
	}
	if err := checkPublic(map[string]string{"lane": "project-manhattan"}); err == nil {
		t.Error("a value outside the allowlist passed the boundary check")
	}
	if err := checkPublic(map[string]string{"nickname": "none"}); err == nil {
		t.Error("a field outside the allowlist passed the boundary check")
	}
}

// (R3) A wait is typed, and it survives to the line: same rung means WAIT, and
// a wait is never permission to retry.
func TestTheWaitIsTypedOnTheLine(t *testing.T) {
	reg := testRegistry(t)
	res := mustRoute(t, reg, Unit{ID: "w", Kind: KindFixWithRedTest, Files: 3, Packages: 1,
		Attempts: []Attempt{{Rung: "sol", Outcome: OutcomeTimeout}}}, DefaultFloor)
	if res.Wait != WaitAwaitingTermination {
		t.Fatalf("wait = %q, want %q (%s)", res.Wait, WaitAwaitingTermination, res.Reason)
	}
	if !strings.Contains(res.Line(), "wait=awaiting_termination") {
		t.Errorf("the line must carry the typed wait: %s", res.Line())
	}
	if !strings.Contains(strings.ToLower(res.Reason), "not permission to retry") {
		t.Errorf("the same rung is not permission to retry, and the reason must say so: %q", res.Reason)
	}
	moving := mustRoute(t, reg, Unit{ID: "m", Kind: KindRebase, Files: 2, Packages: 1}, DefaultFloor)
	if moving.Wait != WaitNone {
		t.Errorf("a decision that is not a wait carries %q", moving.Wait)
	}
	if !strings.Contains(moving.Line(), "wait=-") {
		t.Errorf("every line carries the field, an absence as a dash: %s", moving.Line())
	}
}

// (R3) Security and an unresolved timeout are both true at once: the security
// READ is attached AND the work waits. Neither answer may hide the other.
//
// The first fact used to be "the designated owner stands", meaning rung=johnny.
// Since 2026-09-18 the designation is a READ -- a reserved mind takes no work --
// so what must survive the wait is the reader, and the rung is the one the
// attempt left occupied.
func TestSecurityAndWaitTogether(t *testing.T) {
	reg := testRegistry(t)
	for name, u := range map[string]Unit{
		"guard kind":  {ID: "s1", Kind: KindGuard, Files: 1, Attempts: []Attempt{{Rung: "johnny", Outcome: OutcomeTimeout}}},
		"guard touch": {ID: "s2", Kind: KindFleetChore, Files: 1, Guard: true, Attempts: []Attempt{{Rung: "johnny", Outcome: OutcomeTimeout}}},
		"deploy keys": {ID: "s3", Kind: KindFleetChore, Files: 1, Touches: []string{TouchDeployKeys}, Attempts: []Attempt{{Rung: "johnny", Outcome: OutcomeTimeout}}},
		"on another":  {ID: "s4", Kind: KindFleetChore, Files: 1, Secrets: true, Attempts: []Attempt{{Rung: "opus", Outcome: OutcomeTimeout}}},
		"terminated ok": {ID: "s5", Kind: KindGuard, Files: 1,
			Attempts: []Attempt{{Rung: "johnny", Outcome: OutcomeTimeout, Terminated: true}}},
	} {
		res := mustRoute(t, reg, u, DefaultFloor)
		if res.ReadField() != "johnny" {
			t.Errorf("%s: the security read stands through a wait, got read=%s", name, res.ReadField())
		}
		wantWait := name != "terminated ok"
		if got := res.Wait == WaitAwaitingTermination; got != wantWait {
			t.Errorf("%s: wait = %q, want awaiting=%v (%s)", name, res.Wait, wantWait, res.Reason)
		}
	}
}

// (R5) The provider's usage is kept, not dropped: the call count, the tokens,
// and -- for a call that failed -- the fact that its cost is UNKNOWN rather
// than zero.
func TestProviderUsageIsKept(t *testing.T) {
	reg := testRegistry(t)
	u := Unit{ID: "u", Kind: KindNewVerb, Files: 3, Packages: 1, Lanes: 1}

	rec := &recorder{conf: 0.95, usage: Usage{InputTokens: 937, HasInput: true, OutputTokens: 12, HasOutput: true}}
	res, err := RouteJev(context.Background(), rec, reg, u, DefaultFloor)
	if err != nil {
		t.Fatal(err)
	}
	if res.Usage.Calls != 1 || res.Usage.InputTokens != 937 || res.Usage.OutputTokens != 12 || !res.Usage.Known() {
		t.Errorf("usage = %+v, want 1 call of 937/12 known", res.Usage)
	}

	failed := &recorder{err: errors.New("provider down")}
	res, err = RouteJev(context.Background(), failed, reg, u, DefaultFloor)
	if err != nil {
		t.Fatal(err)
	}
	if res.Usage.Calls != 1 || res.Usage.Known() || !res.Usage.Failed {
		t.Errorf("a failed call is evidence too: %+v", res.Usage)
	}
	if res.Usage.InputTokens != 0 || res.Usage.OutputTokens != 0 {
		t.Errorf("a failed call reports no tokens, and unknown is not zero: %+v", res.Usage)
	}

	rules := mustRoute(t, reg, u, DefaultFloor)
	if rules.Usage.Calls != 0 || rules.Usage.Known() {
		t.Errorf("the rules alone spend nothing: %+v", rules.Usage)
	}
	guard := mustRoute(t, reg, Unit{ID: "g", Kind: KindGuard, Files: 1}, DefaultFloor)
	if guard.Usage.Calls != 0 {
		t.Errorf("a designation spends nothing: %+v", guard.Usage)
	}
}

// The log row carries the usage and the wait, so the JSONL is a record of what
// was spent as well as what was decided.
func TestTheLogRowCarriesUsageAndWait(t *testing.T) {
	reg := testRegistry(t)
	rec := &recorder{conf: 0.95, usage: Usage{InputTokens: 100, HasInput: true, OutputTokens: 5, HasOutput: true}}
	res, err := RouteJev(context.Background(), rec, reg, Unit{ID: "u", Kind: KindNewVerb, Files: 3, Packages: 1, Lanes: 1}, DefaultFloor)
	if err != nil {
		t.Fatal(err)
	}
	e := EntryFor(res, Unit{ID: "u", Kind: KindNewVerb}, time.Unix(0, 0).UTC())
	if e.TokensIn == nil || *e.TokensIn != 100 || e.TokensOut == nil || *e.TokensOut != 5 {
		t.Errorf("the row lost the usage: %+v", e)
	}
	if e.Calls != 1 {
		t.Errorf("the row lost the call count: %+v", e)
	}
	if e.Wait != WaitNone {
		t.Errorf("the row lost the wait: %q", e.Wait)
	}

	waiting := mustRoute(t, reg, Unit{ID: "w", Kind: KindNewVerb, Files: 3, Attempts: []Attempt{{Rung: "opus", Outcome: OutcomeTimeout}}}, DefaultFloor)
	we := EntryFor(waiting, Unit{ID: "w", Kind: KindNewVerb}, time.Unix(0, 0).UTC())
	if we.Wait != WaitAwaitingTermination || !we.AwaitingTermination {
		t.Errorf("the row lost the wait: %+v", we)
	}
	if we.TokensIn != nil || we.TokensOut != nil {
		t.Errorf("no call was made, so there are no tokens to report -- an absence, not a zero: %+v", we)
	}
}
