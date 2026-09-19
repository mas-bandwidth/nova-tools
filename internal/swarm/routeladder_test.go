// Red tests for the swarm fill/launch route (Glenn 2026-09-19: "Are we all
// using Jev yet when selecting which model to send work to? Because that is
// important.").
//
// Before a card is assigned a model the batch asks the ladder which MIND does
// this unit of work, in process through internal/decide -- the same seam
// triage --decide already uses -- and dispatches the card with THAT rung's
// model id from the registry. The key absent, a provider error, a confidence
// under the floor or a rung the registry gives no model for all fall back to
// today's model, and the card's receipt line says which happened:
//
//	ROUTE jev=<rung|fallback> conf=<x> rung=<name> model=<id>
//
// No test here dials a provider: the first drives the whole path through an
// httptest fake that speaks the Jev body-and-response shape of SPEC-DECIDE
// rule 2 and refuses a malformed question with 400, the way the real endpoint
// does; the rest drive the same seam with no socket at all.
package swarm

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

// routeTestRegistry is a ladder of its own -- nothing about this test is our
// own registry -- with model ids on the two rungs that take cards.
const routeTestRegistry = `{
  "minds": [
    {"name": "low", "lineage": "vendor", "height": 0, "kinds": [], "lanes": [], "availability": "available", "ask": "card", "model": "vendor/low-1"},
    {"name": "high", "lineage": "vendor", "height": 1, "kinds": [], "lanes": [], "availability": "available", "ask": "card", "model": "vendor/high-1"},
    {"name": "child", "lineage": "house", "height": 2, "kinds": [], "lanes": [], "availability": "available", "ask": "child"},
    {"name": "guardian", "lineage": "house", "height": 3, "kinds": ["guard", "fresh-take"], "lanes": ["security"], "availability": "reserved", "ask": "bus"}
  ]
}`

func routeTestReg(t *testing.T) *decide.Registry {
	t.Helper()
	reg, err := decide.ParseRegistry([]byte(routeTestRegistry))
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func routeTestNow() time.Time { return time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC) }

// routeTestUnit is one mechanical unit of work: the kind starts at the bottom
// rung, so the offered set is the two card rungs and there is a decision to
// make.
func routeTestUnit(id string) decide.Unit {
	return decide.Unit{ID: id, Kind: decide.KindRebase, Files: 4, Packages: 1, Lanes: 1}
}

// jevFake is the provider, strict the way the real endpoint is: a POST with a
// bearer key, a body carrying state, model and typed questions, 400 on a
// malformed question, and the real answer shape {choice, probabilities,
// confidence} beside a usage object.
func jevFake(t *testing.T, choose func(options []string) (string, float64)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body struct {
			State     string `json:"state"`
			Model     string `json:"model"`
			Questions map[string]struct {
				Type         string            `json:"type"`
				Instructions string            `json:"instructions"`
				Criteria     map[string]string `json:"criteria"`
			} `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"bad body"}`, http.StatusBadRequest)
			return
		}
		q, ok := body.Questions[decide.RungQuestion]
		// A malformed question is a 400, not an answer: the fake is as strict
		// as the endpoint it stands in for.
		if !ok || q.Type != "choice" || len(q.Criteria) < 2 || strings.TrimSpace(q.Instructions) == "" || body.Model == "" {
			http.Error(w, `{"error":"malformed question"}`, http.StatusBadRequest)
			return
		}
		options := make([]string, 0, len(q.Criteria))
		for name := range q.Criteria {
			options = append(options, name)
		}
		choice, conf := choose(options)
		probabilities := map[string]float64{}
		for _, o := range options {
			probabilities[o] = (1 - conf) / float64(len(options))
		}
		probabilities[choice] = conf
		_ = json.NewEncoder(w).Encode(map[string]any{
			"answers": map[string]any{
				decide.RungQuestion: map[string]any{
					"type": "choice", "choice": choice,
					"probabilities": probabilities, "confidence": conf,
				},
			},
			"usage": map[string]any{"input_tokens": 812, "output_tokens": 0},
		})
	}))
}

// canListen reports whether this sandbox allows a listening socket, the way
// internal/decide's own suite asks.
func canListen(t *testing.T) bool {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// TestRouteCardFollowsTheAnswer: the model a card is dispatched with is the
// one the ladder's answer names, not the one the fill script wrote in the TSV.
func TestRouteCardFollowsTheAnswer(t *testing.T) {
	if !canListen(t) {
		t.Skip("this sandbox forbids listening sockets")
	}
	// The fake picks the HIGHER of the two offered rungs, which is never the
	// fallback model: if the card ends up on vendor/high-1 the answer decided
	// it, and if it ends up on vendor/low-1 nothing did.
	srv := jevFake(t, func(options []string) (string, float64) { return highestOption(options), 0.97 })
	defer srv.Close()
	t.Setenv("ROUTE_TEST_KEY", "sk-not-a-real-key")
	client, err := decide.New(srv.URL, "ROUTE_TEST_KEY")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	in := RouteInput{
		Registry: routeTestReg(t),
		Decide:   client.Decide,
		Floor:    0.9,
		Log:      filepath.Join(dir, "decide.jsonl"),
		Usage:    filepath.Join(dir, "usage.tsv"),
		Now:      routeTestNow,
	}
	got := RouteCard(context.Background(), in, routeTestUnit("card-1"), "vendor/low-1")
	if got.Model != "vendor/high-1" {
		t.Fatalf("the card was dispatched with %q; the answer named the high rung, so the model must follow it", got.Model)
	}
	if got.Rung != "high" || got.Source != decide.SourceJev {
		t.Fatalf("rung=%q source=%q, want the rung the answer named from jev", got.Rung, got.Source)
	}
	if want := "ROUTE jev=high conf=0.97"; !strings.HasPrefix(got.Receipt, want) {
		t.Fatalf("receipt %q, want it to begin %q", got.Receipt, want)
	}
	if !strings.Contains(got.Receipt, "model=vendor/high-1") {
		t.Fatalf("receipt %q does not name the model the card runs with", got.Receipt)
	}
	// Accounting is not optional (SPEC-DECIDE): the decision is logged and the
	// call it made is a row of the fleet's own usage TSV.
	logRaw, err := os.ReadFile(in.Log)
	if err != nil {
		t.Fatalf("the decision was not logged: %v", err)
	}
	if !strings.Contains(string(logRaw), `"rung_tried":"high"`) {
		t.Fatalf("log row %q does not carry the rung", logRaw)
	}
	usageRaw, err := os.ReadFile(in.Usage)
	if err != nil {
		t.Fatalf("the provider call was not accounted for: %v", err)
	}
	if !strings.Contains(string(usageRaw), "812") {
		t.Fatalf("usage row %q does not carry what the call reported", usageRaw)
	}
}

// TestRouteCardBelowTheFloorKeepsTodaysModel: a confidence under the floor is
// a suggestion, so the card keeps the model it came with and the receipt says
// the route fell back.
func TestRouteCardBelowTheFloorKeepsTodaysModel(t *testing.T) {
	if !canListen(t) {
		t.Skip("this sandbox forbids listening sockets")
	}
	srv := jevFake(t, func(options []string) (string, float64) { return highestOption(options), 0.42 })
	defer srv.Close()
	t.Setenv("ROUTE_TEST_KEY", "sk-not-a-real-key")
	client, err := decide.New(srv.URL, "ROUTE_TEST_KEY")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	got := RouteCard(context.Background(), RouteInput{
		Registry: routeTestReg(t),
		Decide:   client.Decide,
		Floor:    0.9,
		Log:      filepath.Join(dir, "decide.jsonl"),
		Usage:    filepath.Join(dir, "usage.tsv"),
		Now:      routeTestNow,
	}, routeTestUnit("card-2"), "vendor/low-1")
	if got.Model != "vendor/low-1" {
		t.Fatalf("model %q; below the floor the card keeps today's model", got.Model)
	}
	if !strings.HasPrefix(got.Receipt, "ROUTE jev=fallback conf=0.42") {
		t.Fatalf("receipt %q, want it to say the route fell back at the confidence it saw", got.Receipt)
	}
}

// TestRouteCardWithNoKeyKeepsTodaysModel: no key is no call, and today's
// behaviour is the fallback -- the loop runs on a bench with no API at all.
func TestRouteCardWithNoKeyKeepsTodaysModel(t *testing.T) {
	dir := t.TempDir()
	got := RouteCard(context.Background(), RouteInput{
		Registry: routeTestReg(t),
		Decide:   nil,
		Floor:    0.9,
		Log:      filepath.Join(dir, "decide.jsonl"),
		Now:      routeTestNow,
	}, routeTestUnit("card-3"), "vendor/low-1")
	if got.Model != "vendor/low-1" {
		t.Fatalf("model %q; with no key the card keeps today's model", got.Model)
	}
	if !strings.HasPrefix(got.Receipt, "ROUTE jev=fallback") {
		t.Fatalf("receipt %q, want jev=fallback", got.Receipt)
	}
	if _, err := os.Stat(filepath.Join(dir, "usage.tsv")); err == nil {
		t.Fatal("a decision that made no call wrote a usage row; an empty row is a claim that a call was made")
	}
}

// TestRouteCardKeepsTodaysModelWhenTheProviderRefuses: a 400 from the endpoint
// leaves the rules' answer standing, the card keeps today's model, and the
// call is STILL accounted for -- a refusal cannot unspend it.
func TestRouteCardKeepsTodaysModelWhenTheProviderRefuses(t *testing.T) {
	if !canListen(t) {
		t.Skip("this sandbox forbids listening sockets")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"malformed question"}`, http.StatusBadRequest)
	}))
	defer srv.Close()
	t.Setenv("ROUTE_TEST_KEY", "sk-not-a-real-key")
	client, err := decide.New(srv.URL, "ROUTE_TEST_KEY")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	usagePath := filepath.Join(dir, "usage.tsv")
	got := RouteCard(context.Background(), RouteInput{
		Registry: routeTestReg(t),
		Decide:   client.Decide,
		Floor:    0.9,
		Log:      filepath.Join(dir, "decide.jsonl"),
		Usage:    usagePath,
		Now:      routeTestNow,
	}, routeTestUnit("card-4"), "vendor/low-1")
	if got.Model != "vendor/low-1" {
		t.Fatalf("model %q; a provider refusal leaves today's model standing", got.Model)
	}
	raw, err := os.ReadFile(usagePath)
	if err != nil {
		t.Fatalf("the failed call was not accounted for: %v", err)
	}
	if !strings.Contains(string(raw), "\t2\t") && !strings.Contains(string(raw), "typesafe") {
		t.Fatalf("usage row %q does not record the failed call", raw)
	}
}

// TestRouteCardAfterGateFailureNeverReturnsTheSameRung: a card that fails its
// gate re-enters the ladder carrying that failure, and the answer is never the
// rung that just failed -- the ladder IS the retry policy, and a retry on the
// same rung is not one.
func TestRouteCardAfterGateFailureNeverReturnsTheSameRung(t *testing.T) {
	reg := routeTestReg(t)
	unit := routeTestUnit("card-5")
	first := RouteCard(context.Background(), RouteInput{Registry: reg, Floor: 0.9, Now: routeTestNow}, unit, "vendor/low-1")
	if first.Rung != "low" {
		t.Fatalf("the first answer is %q; a mechanical unit starts at the bottom rung", first.Rung)
	}
	next := AfterGateFailure(unit, first.Rung, "the gate went red")
	if len(next.Attempts) != 1 || next.Attempts[0].Outcome != decide.OutcomeFailed || !next.Attempts[0].Failed() {
		t.Fatalf("the re-entry carries %v; a gate failure is a CONFIRMED failure the ladder may step past", next.Attempts)
	}
	if len(unit.Attempts) != 0 {
		t.Fatal("AfterGateFailure mutated the unit it was given; the re-entry is a new unit, not an edit of the old one")
	}
	firstMind, _ := reg.ByName(first.Rung)
	// Whatever the provider answers among the rungs it is OFFERED, the rung
	// that just failed is not one of them: the answer is another lineage at
	// that height where there is one and the rung above where there is not,
	// and it is never a retry on the rung that failed.
	for _, pick := range []struct {
		name   string
		choose func([]string) (string, float64)
	}{
		{"the provider takes the lowest rung offered", func(o []string) (string, float64) { return lowestOption(o), 0.98 }},
		{"the provider takes the highest rung offered", func(o []string) (string, float64) { return highestOption(o), 0.98 }},
		{"the provider is never asked at all", nil},
	} {
		t.Run(pick.name, func(t *testing.T) {
			dir := t.TempDir()
			in := RouteInput{Registry: reg, Floor: 0.9, Now: routeTestNow,
				Log: filepath.Join(dir, "decide.jsonl"), Usage: filepath.Join(dir, "usage.tsv")}
			if pick.choose != nil {
				in.Decide = func(ctx context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error) {
					q, ok := qs[decide.RungQuestion]
					if !ok || q.Choice == nil {
						return nil, decide.Usage{}, fmt.Errorf("the rung decision carries no rung question")
					}
					options := make([]string, 0, len(q.Choice))
					for name := range q.Choice {
						options = append(options, name)
					}
					choice, conf := pick.choose(options)
					return map[string]decide.Answer{decide.RungQuestion: {Type: "choice", Choice: choice, Confidence: conf}},
						decide.Usage{InputTokens: 900, HasInput: true, OutputTokens: 0, HasOutput: true}, nil
				}
			}
			second := RouteCard(context.Background(), in, next, "vendor/low-1")
			if second.Rung == first.Rung {
				t.Fatalf("the gate failed on %s and the ladder answered %s again; a failed unit never re-enters on the same rung", first.Rung, second.Rung)
			}
			secondMind, _ := reg.ByName(second.Rung)
			if secondMind.Height <= firstMind.Height {
				t.Fatalf("the gate failed at height %d and the ladder answered height %d; the ladder moves sideways or up, never down and never the same mind",
					firstMind.Height, secondMind.Height)
			}
			if second.Model == "vendor/low-1" && !second.Fallback {
				t.Fatalf("the escalated card was dispatched with the failed rung's own model %q", second.Model)
			}
		})
	}
}

// TestRouteCardWithNoModelOnTheRungKeepsTodaysModel: the rungs that are asked
// on the bus or as a child are not models, and a card cannot be dispatched to
// one. The card keeps today's model, and the receipt names the rung anyway, so
// the log says out loud what should have gone to a person.
func TestRouteCardWithNoModelOnTheRungKeepsTodaysModel(t *testing.T) {
	reg := routeTestReg(t)
	dir := t.TempDir()
	// A judgment kind starts above the card rungs, on the child rung, which
	// the registry gives no model id: it is ASKED, not run.
	unit := decide.Unit{ID: "card-6", Kind: decide.KindFixWithRedTest, Files: 4, Packages: 1}
	seam := func(ctx context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error) {
		return nil, decide.Usage{}, fmt.Errorf("no provider should be asked when one rung is eligible")
	}
	got := RouteCard(context.Background(), RouteInput{
		Registry: reg, Decide: seam, Floor: 0.9,
		Log: filepath.Join(dir, "decide.jsonl"), Usage: filepath.Join(dir, "usage.tsv"), Now: routeTestNow,
	}, unit, "vendor/low-1")
	if got.Rung != "child" {
		t.Fatalf("rung %q; a fix with a red test is judgment work and starts on the child rung", got.Rung)
	}
	if got.Model != "vendor/low-1" {
		t.Fatalf("model %q; a rung with no model id cannot take a card, so today's model stands", got.Model)
	}
	if !got.Fallback || got.Why != RouteFallbackNoModel {
		t.Fatalf("fallback=%v why=%q, want the enumerated reason that the rung is asked and not run", got.Fallback, got.Why)
	}
	if !strings.Contains(got.Receipt, "rung=child") {
		t.Fatalf("receipt %q must still name the rung the ladder answered", got.Receipt)
	}
}

// lowestOption is the first opaque option id the question offered: the rungs
// are offered lowest first, so this is the lowest rung still eligible.
func lowestOption(options []string) string {
	best := ""
	for _, o := range options {
		if best == "" || o < best {
			best = o
		}
	}
	return best
}

// highestOption is the last opaque option id the question offered: the rungs
// are offered lowest first, so this is always the rung ABOVE the fallback.
func highestOption(options []string) string {
	best := ""
	for _, o := range options {
		if best == "" || o > best {
			best = o
		}
	}
	return best
}

// THE FILL/LAUNCH PATH ITSELF. The seam above is the decision; this is the
// place it is made -- readCards then routeCards, which is what runs between a
// batch reading its TSV and the first runner process starting. Before this
// existed, the model column of that TSV was the last word on every card.
func TestRouteCardsOnTheFillPathOverridesTheTSVModel(t *testing.T) {
	dir := t.TempDir()
	body := "RESULT: CARD-1 nova-tools #1 rebased onto dev\nKIND: rebase\nFILES: 4\nPACKAGES: 1\nLANES: 1\nSTEP 1. do the thing\n"
	cardPath := filepath.Join(dir, "card-1.card")
	if err := os.WriteFile(cardPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// A second card states no kind at all, and its contract line reads as none
	// of them: it is never routed, and its line says so.
	untyped := filepath.Join(dir, "card-2.card")
	if err := os.WriteFile(untyped, []byte("RESULT: CARD-2 something nobody typed\nSTEP 1. do the thing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tsv := filepath.Join(dir, "cards.tsv")
	rows := "card-1\t1\tvendor/low-1\t" + cardPath + "\ncard-2\t2\tvendor/low-1\t" + untyped + "\n"
	if err := os.WriteFile(tsv, []byte(rows), 0o644); err != nil {
		t.Fatal(err)
	}
	cards, err := readCards(tsv)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 2 {
		t.Fatalf("read %d cards, want 2", len(cards))
	}
	if !cards[0].routable || cards[0].unit.Kind != decide.KindRebase || cards[0].unit.Files != 4 {
		t.Fatalf("the card's stated evidence was not read: %+v", cards[0].unit)
	}
	if cards[1].routable {
		t.Fatalf("a card naming no kind was typed anyway: %+v", cards[1].unit)
	}
	var stderr strings.Builder
	in := BatchInput{
		Stderr: &stderr,
		Route: &RouteInput{
			Registry: routeTestReg(t), Floor: 0.9, Now: routeTestNow,
			Log: filepath.Join(dir, "decide.jsonl"), Usage: filepath.Join(dir, "usage.tsv"),
			Decide: func(ctx context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error) {
				// What crosses the boundary is the bucketed projection and
				// never the card: no title, no path, no branch name.
				for _, secret := range []string{"CARD-1", "nova-tools", cardPath, "STEP 1"} {
					if strings.Contains(state, secret) {
						return nil, decide.Usage{}, fmt.Errorf("the card's own text reached the provider: %q", secret)
					}
				}
				q := qs[decide.RungQuestion]
				options := make([]string, 0, len(q.Choice))
				for name := range q.Choice {
					options = append(options, name)
				}
				return map[string]decide.Answer{decide.RungQuestion: {
					Type: "choice", Choice: highestOption(options), Confidence: 0.96,
				}}, decide.Usage{InputTokens: 900, HasInput: true}, nil
			},
		},
	}
	routeCards(in, cards)
	if cards[0].model != "vendor/high-1" {
		t.Fatalf("the card was dispatched with %q; the TSV's model is the FALLBACK and the ladder's answer is the model", cards[0].model)
	}
	if !strings.Contains(cards[0].receipt, "jev=high") {
		t.Fatalf("receipt %q does not say the ladder chose the rung", cards[0].receipt)
	}
	if cards[1].model != "vendor/low-1" || !strings.Contains(cards[1].receipt, "why=card-names-no-kind") {
		t.Fatalf("an untyped card must keep today's model and say why: model=%q receipt=%q", cards[1].model, cards[1].receipt)
	}
	for _, want := range []string{"ROUTE card-1 ", "ROUTE card-2 "} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("every card says its route line once; stderr is:\n%s", stderr.String())
		}
	}
}
