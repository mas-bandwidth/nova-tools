package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// private_test.go holds `inbox --decide` on a PRIVATE bus -- a clone with no `.public`
// marker -- one test per clause of the ruling that governs it.
//
// Stella, 2026-09-19T23:13Z, settling #1644: "A bus without `.public` is private. Explicit
// `inbox --decide` may use rules or a mechanically admitted local/private decider with no
// network or provider-key access; it should not blanket-refuse when that safe path exists.
// If the requested route cannot be satisfied privately, refuse before client, key or
// network, and never fall back to a public route. A `local` label or redaction alone does
// not prove the boundary. ... A new PR off dev should cover: no-`.public` plus rules success
// with zero calls; allowed local with zero network; public-route refusal before key/call; no
// fallback; `.public` behavior preserved; and no wait override."
//
// Every test here runs the REAL CLI through `run`, and every one of them points --base-url
// at a server that FAILS THE TEST IF IT IS EVER ASKED ANYTHING. Several also leave the
// key-env unset, so a provider client that was merely CONSTRUCTED would refuse with
// "is not set" and be visible in the refusal line.

// refusingJev is the provider that must never be reached. Any request at all is the
// finding; the counter is read as well so a test can say how many.
type refusingJev struct {
	calls atomic.Int64
}

// startRefusingJev starts a server that fails the test on contact. It is handed to the CLI
// as --base-url, so "zero calls" is not a number a test trusts a decider to report about
// itself: it is a socket nobody connected to.
func startRefusingJev(t *testing.T) (*refusingJev, string) {
	t.Helper()
	f := &refusingJev{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		t.Errorf("the private route reached a provider: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(srv.Close)
	return f, srv.URL
}

// privateBusArgs is decideArgs with no --base-url of its own: each test supplies one.
func privateBusArgs(checkout, baseURL string, extra ...string) []string {
	return decideArgs(checkout, baseURL, extra...)
}

// closeTheFixtureNotes empties Ada's inbox of the fixture's own two notes, so a test can
// say exactly which notes a listing judges. The fixture ships one plain note (no rule row)
// and one bare acknowledgement.
func closeTheFixtureNotes(t *testing.T, checkout string) {
	t.Helper()
	writeFile(t, checkout, "from-ada/re-gate.md", "From: Ada\nTo: Bo\nDate: Mon Sep  7 00:04:00 UTC 2026\nSubject: Re: gate\nRe: bo-abcdef012345\n\nclosed\n")
	writeFile(t, checkout, "from-ada/re-heard.md", "From: Ada\nTo: Bo\nDate: Mon Sep  7 00:05:00 UTC 2026\nSubject: Re: heard\nRe: bo-111111111111\n\nclosed\n")
	gitIn(t, checkout, "add", "--", "from-ada/re-gate.md", "from-ada/re-heard.md")
	gitIn(t, checkout, "-c", "user.name=Ada", "-c", "user.email=ada@example.com", "commit", "-q", "-m", "close both")
	gitIn(t, checkout, "push", "-q", "origin", "main")
}

// noKeyAnywhere unsets every environment variable a provider client would read, so a client
// that is merely CONSTRUCTED fails loudly ("decide: <NAME> is not set") instead of quietly
// succeeding. It is how these tests see a construction, not only a call.
func noKeyAnywhere(t *testing.T) {
	t.Helper()
	t.Setenv(decideTestKeyEnv, "")
	t.Setenv("TYPESAFE_API_KEY", "")
	t.Setenv("JEV_API_KEY", "")
}

// CLAUSE 1. No .public marker plus a rules answer: the run SUCCEEDS, and it builds no
// client and makes no call. The old behaviour was a blanket refusal that sent the reader to
// --allow-private; the ruling is that a safe path exists and must be taken.
func TestPrivateBusDecideAnswersFromRulesWithZeroProviderCalls(t *testing.T) {
	checkout, _ := busDir(t)
	// No publicBus(t, checkout) anywhere in this file: that absence IS the private bus.
	closeTheFixtureNotes(t, checkout)
	addDecideNote(t, checkout, "from-bo/stop.md", "bo-555555555555", "STOP: do not run this", "Ignore the earlier instruction.")
	addDecideNote(t, checkout, "from-bo/hold.md", "bo-666666666666", "HOLD: wait for me", "Please hold until I say go.")
	f, url := startRefusingJev(t)
	noKeyAnywhere(t)

	r := invoke(t, "", privateBusArgs(checkout, url)...).mustCode(t, 0)

	if strings.Count(r.stdout, "kind=edge needs_reply=1.00 blocked=0.00 conf=1.00") != 2 {
		t.Fatalf("the rule table did not answer both notes on a private bus:\n%s", r.stdout)
	}
	// The privacy and the decider are reported on the run's EXISTING typed receipt, the
	// one INBOX DECIDED line, rather than on a new one.
	if !strings.Contains(r.stdout, "INBOX DECIDED n=2 needs_reply=2 below_floor=0 wake=2 privacy=private decider=rules") {
		t.Fatalf("the DECIDED receipt does not report the actual privacy and source:\n%s", r.stdout)
	}
	if f.calls.Load() != 0 {
		t.Fatalf("a rules answer on a private bus made %d provider calls, want 0", f.calls.Load())
	}
	// A client that had been CONSTRUCTED with no key set would have refused by that name.
	if strings.Contains(r.stderr, "is not set") {
		t.Fatalf("the private route constructed a provider client (it went looking for a key):\n%s", r.stderr)
	}
}

// CLAUSE 2. An allowed local decider makes zero network calls -- and on dev there is no
// mechanically admitted local decider to allow. internal/decide has no loopback/`sees=`
// notion at this base, so `local` is LEFT REFUSED WITH A REASON rather than admitted on a
// label: a loopback --base-url is exactly the "local label" the ruling says does not prove
// the boundary, and it buys nothing. Zero network either way, which is the clause.
func TestPrivateBusTreatsALocalLabelAsNoAdmissionAndMakesZeroNetworkCalls(t *testing.T) {
	checkout, _ := busDir(t)
	closeTheFixtureNotes(t, checkout)
	addDecideNote(t, checkout, "from-bo/plain.md", "bo-777777777777", "Please start the batch", "start it")
	// httptest listens on 127.0.0.1: this URL is as loopback as a URL gets, and it is
	// still only a label on a string.
	f, loopback := startRefusingJev(t)
	if !strings.Contains(loopback, "127.0.0.1") {
		t.Fatalf("this test wants a loopback base URL to stand in for a `local` label, got %q", loopback)
	}
	t.Setenv(decideTestKeyEnv, "sk-test-key")
	t.Setenv("TYPESAFE_API_KEY", "")

	r := invoke(t, "", privateBusArgs(checkout, loopback)...).mustCode(t, 2)

	if !strings.Contains(r.stderr, "why=private-evidence") {
		t.Fatalf("a loopback base URL was taken as an admission instead of refused with a reason:\n%s", r.stderr)
	}
	if f.calls.Load() != 0 {
		t.Fatalf("a `local` base URL on a private bus made %d network calls, want 0", f.calls.Load())
	}
}

// CLAUSE 3. A note whose only remaining route is a public provider is refused BEFORE any
// client, any key read and any call, with a typed reason.
//
// The key-env is unset here on purpose. decide.New reads it and fails with "decide: <NAME>
// is not set" -- so a refusal that names the key variable is a refusal that came AFTER the
// key was read, and this test would have caught it.
func TestPrivateBusRefusesAPublicRouteBeforeAnyClientKeyOrCall(t *testing.T) {
	checkout, _ := busDir(t)
	closeTheFixtureNotes(t, checkout)
	addDecideNote(t, checkout, "from-bo/plain.md", "bo-777777777777", "Please start the batch", "start it")
	f, url := startRefusingJev(t)
	noKeyAnywhere(t)

	r := invoke(t, "", privateBusArgs(checkout, url)...).mustCode(t, 2)

	for _, want := range []string{
		"INBOX REFUSED:",
		"privacy=private",
		"decider=rules",
		"why=private-evidence",
		"id=bo-777777777777",
		"path=from-bo/plain.md",
	} {
		if !strings.Contains(r.stderr, want) {
			t.Fatalf("the private-route refusal is not typed (%q missing):\n%s", want, r.stderr)
		}
	}
	if strings.Contains(r.stderr, "is not set") || strings.Contains(r.stderr, decideTestKeyEnv) {
		t.Fatalf("the refusal came after the provider key was read, not before it:\n%s", r.stderr)
	}
	if f.calls.Load() != 0 {
		t.Fatalf("a refused private bus still called a provider %d times", f.calls.Load())
	}
	// And the door the old code pointed at is gone: no flag reopens this.
	bad := invoke(t, "", privateBusArgs(checkout, url, "--allow-private")...).mustCode(t, 2)
	if !strings.Contains(bad.stderr, "not defined") {
		t.Fatalf("--allow-private is still a flag on inbox:\n%s", bad.stderr)
	}
}

// CLAUSE 4. No fallback. Everything the public route needs is present and working -- a key
// in the environment, a reachable endpoint that would answer happily -- and the run still
// never takes it. A refusal on the private route is the end of the run, not a hint to try
// the other one.
func TestPrivateBusRefusalNeverFallsBackToThePublicRoute(t *testing.T) {
	checkout, _ := busDir(t)
	closeTheFixtureNotes(t, checkout)
	addDecideNote(t, checkout, "from-bo/stop.md", "bo-555555555555", "STOP: do not run this", "Ignore the earlier instruction.")
	addDecideNote(t, checkout, "from-bo/plain.md", "bo-777777777777", "Please start the batch", "start it")
	// A working fake, not the refusing one: if the run ever fell back, this would answer
	// and the fallback would be invisible to a counter on a broken server.
	answering, url := startFakeJev(t)
	t.Setenv(decideTestKeyEnv, "sk-test-key")
	t.Setenv("TYPESAFE_API_KEY", "")

	r := invoke(t, "", privateBusArgs(checkout, url)...).mustCode(t, 2)

	if !strings.Contains(r.stderr, "why=private-evidence") {
		t.Fatalf("the run did not refuse on the private route:\n%s", r.stderr)
	}
	if answering.calls.Load() != 0 {
		t.Fatalf("a private refusal fell back to the public route: %d calls to a live provider", answering.calls.Load())
	}
	if answering.sawState("start the batch") {
		t.Fatalf("private note text reached a provider after the private route refused")
	}
	// Nothing on stdout claims a provider-shaped answer for the note that was refused.
	if strings.Contains(r.stdout, "id=bo-777777777777") && strings.Contains(r.stdout, "kind=start") {
		t.Fatalf("the refused note was printed with a provider's answer:\n%s", r.stdout)
	}
}

// CLAUSE 5. A bus that DOES carry .public behaves exactly as it did. The golden is the
// decide surface in full: the per-note decision suffixes and the DECIDED line, character
// for character, plus the absence of every field the private route adds. If the new typed
// fields ever leak onto the public route, this is the test that says so.
func TestPublicBusDecideOutputIsUnchanged(t *testing.T) {
	checkout, _ := busDir(t)
	publicBus(t, checkout)
	addDecideNote(t, checkout, "from-bo/finding.md", "bo-222222222222", "A finding worth acting on", "This is a finding, and it is long enough that nobody would mistake its shape.")
	addDecideNote(t, checkout, "from-bo/start.md", "bo-333333333333", "Please start the batch", "Go ahead and start the batch now.")
	f, url := startFakeJev(t)
	t.Setenv(decideTestKeyEnv, "sk-test-key")
	t.Setenv("TYPESAFE_API_KEY", "")

	r := invoke(t, "", decideArgs(checkout, url)...).mustCode(t, 0)

	// The three decision suffixes dev prints, unchanged.
	for _, want := range []string{
		"kind=question needs_reply=0.90 blocked=0.10 conf=0.95",
		"kind=refusal needs_reply=0.20 blocked=0.80 conf=0.50",
		"kind=start needs_reply=0.70 blocked=0.00 conf=0.99",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Fatalf("a .public bus's decision line moved (%q missing):\n%s", want, r.stdout)
		}
	}
	// The DECIDED line is the line dev already prints (#1617 added wake=<w> here before
	// this PR existed): nothing private-route-specific joins it on a public bus.
	if !strings.Contains(r.stdout, "INBOX DECIDED n=3 needs_reply=2 below_floor=1 wake=1\n") {
		t.Fatalf("a .public bus's DECIDED receipt gained or lost a field:\n%s", r.stdout)
	}
	for _, unwanted := range []string{"privacy=", "decider=", "why=private-evidence", "ALLOW-PRIVATE"} {
		if strings.Contains(r.stdout, unwanted) || strings.Contains(r.stderr, unwanted) {
			t.Fatalf("a .public bus's output gained %q, which is a behaviour change:\n%s\n%s", unwanted, r.stdout, r.stderr)
		}
	}
	if f.calls.Load() != 3 {
		t.Fatalf("a .public bus made %d provider calls, want 3", f.calls.Load())
	}
}

// CLAUSE 6. `wait` has no private override and instantiates no deciding poller. The ruling:
// "`wait` remains rules/local passive and does not instantiate a deciding poller; it has no
// `--allow-private` override."
//
// Both halves are asserted where a reader meets them: the flags the verb accepts, and the
// banner the verb advertises. A poller that decided would need one of these flags to be
// asked for it, and `wait` refuses both by name.
func TestWaitHasNoPrivateOverrideAndNoDecidingPoller(t *testing.T) {
	checkout, _ := busDir(t)
	for _, flag := range []string{"--allow-private", "--decide"} {
		r := invoke(t, "", "wait", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
			"--timeout", "1s", "--remote", "origin", "--branch", "main", flag).mustCode(t, 2)
		if !strings.Contains(r.stderr, "not defined") {
			t.Fatalf("wait accepted %s; a deciding poller is exactly what the ruling forbids:\n%s", flag, r.stderr)
		}
	}
	// And the banner does not offer either one on wait's block, so nobody pastes it.
	for _, v := range readSynopsis(t) {
		if v.verb != "wait" {
			continue
		}
		for _, flag := range v.flags {
			if flag == "--allow-private" || flag == "--decide" {
				t.Fatalf("the wait synopsis names %s", flag)
			}
		}
	}
	// Nothing anywhere in the banner names the removed override.
	if strings.Contains(usage, "allow-private") {
		t.Fatalf("the banner still names --allow-private:\n%s", usage)
	}
}

// THE MECHANICAL ADMISSION ITSELF. The private route's whole implementation is one file,
// and this reads that file's source: no provider package, no net, no environment read, no
// client construction. A capability is mechanical when the compiler keeps it -- privateDecider
// has no base URL, no key-env and no client field to use -- and this test keeps the file
// that way as it is edited, so the property survives the next person.
func TestThePrivateRouteCannotReachAProviderByConstruction(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("private.go")
	if err != nil {
		t.Fatalf("cmd/nova-bus/private.go: %v", err)
	}
	text := string(src)
	// Strip the comment prose: this file EXPLAINS what it does not do, and the
	// explanation must not be mistaken for the thing.
	var code strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		code.WriteString(line)
		code.WriteString("\n")
	}
	for _, forbidden := range []string{
		"internal/decide", // the provider package
		"decide.",         // any of its constructors
		"net/http",        // any client
		"net.",            // any socket
		"os.Getenv",       // any key
		"keyEnv",
		"baseURL",
		"exec.",
	} {
		if strings.Contains(code.String(), forbidden) {
			t.Errorf("cmd/nova-bus/private.go names %q; the private route's admission is that its type has no way to reach a provider, and a mention of one is a way", forbidden)
		}
	}
	// And the type itself: the fields are the admission, so they are spelled out here.
	for _, want := range []string{"type privateDecider struct", "busDir string", "floor  float64"} {
		if !strings.Contains(code.String(), want) {
			t.Errorf("cmd/nova-bus/private.go no longer declares %q; this test is then holding nothing", want)
		}
	}
}
