package main

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

// THE CLASS RULE: A LINE A COLD SESSION TYPES FROM WHAT nova-sprint PRINTS
// REACHES THE STORE (nova-tools#4352 A).
//
// Measured on dev by the coordinator, 2026-09-26 13:27-13:45 EDT: five of
// its first twelve calls were refused before they read anything, each
// ending "run: nova-sprint help". --sprint on three verbs where a sprint is
// meaningful and was not taken; --n for a pull request on ci status, where
// pr lines had taught it; "redis address is required" on friend show, width
// and census with NOVA_SPRINT_REDIS set, which every other verb read; and
// pr lines printing its usage instead of the lines. Each line below is one
// of those, verbatim but for the store address, and each must get past its
// flags and its address to the store: a grammar refusal is red here with
// the line and what it printed.
//
// The store is miniredis (no FCALL, so a verb that calls the function
// library fails after the dial; that is past the grammar, which is all this
// holds). Each line runs in a child (this test binary as nova-sprint) whose
// environment names the store in NOVA_SPRINT_REDIS alone, as the
// coordinator's session did, so the one resolver (seat.go) is on trial too.
func TestGrammarTheColdSessionLinesReachTheStore(t *testing.T) {
	t.Parallel()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	head := "0123456789abcdef0123456789abcdef01234567"
	// refusedBefore are what a line refused before the store says.
	refusedBefore := []string{"not defined", "is required", "needs --", "wants --", "want ws"}
	for _, c := range []struct {
		line string
		want string // on stdout, when the line has something to show
	}{
		{"doctor --redis {addr} --sprint quack-0926", "DOCTOR"},
		{"card render --ids console-grammar~1 --sprint quack-0926", ""},
		{"ws counts --sprint quack-0926", ""},
		{"ci status --repo nova-tools --n 4371", "ci:nova-tools:" + head + " green"},
		{"friend show", ""},
		{"width", ""},
		{"census --sprint quack-0926", ""},
		{"pr lines --repo nova-tools --n 4373", "SCORE who=emma head=abcdef1 score=9/10\nPR LINES pr:nova-tools:4373 lines=1"},
		// #4399 item 10: the verbs that read NOVA_REDIS_ADDR themselves
		// (line.go, read.go, spec.go, land_writer.go) now read the one
		// resolver, so NOVA_SPRINT_REDIS alone reaches the store
		{"line list --repo nova-tools --n 4373", ""},
		{"read brief --repo nova-tools --n 4371", ""},
		{"spec list", ""},
		{"land writer --repo nova-tools --base dev", ""},
	} {
		t.Run(c.line, func(t *testing.T) {
			t.Parallel()
			mr := miniredis.RunT(t)
			// What pr lines lists and ci status --n reads: PR 4373's typed
			// line, PR 4371's head and that head's ci record.
			mr.HSet("pr:nova-tools:4371", "head", head, "base", "dev", "stream", "console")
			mr.HSet("ci:nova-tools:"+head, "ci", "green", "bench", "hulk", "attempt", "1", "pr", "4371")
			if _, err := mr.RPush("pr:nova-tools:4373:lines", "SCORE who=emma head=abcdef1 score=9/10"); err != nil {
				t.Fatal(err)
			}
			// The session's environment: the address in NOVA_SPRINT_REDIS
			// alone, no seat, the friend the harness exports.
			cmd := exec.Command(self, strings.Fields(strings.ReplaceAll(c.line, "{addr}", mr.Addr()))...)
			cmd.Env = []string{grammarAsToolEnv + "=1", "NOVA_SPRINT_REDIS=" + mr.Addr(), seatEnv + "=rowan"}
			for _, kv := range os.Environ() {
				// every other NOVA_ variable stays out (a seat, another
				// address, a login); the host guard stays in
				if !strings.HasPrefix(kv, "NOVA_") || strings.HasPrefix(kv, "NOVA_TEST_NO_HOST=") {
					cmd.Env = append(cmd.Env, kv)
				}
			}
			var out, errOut strings.Builder
			cmd.Stdout, cmd.Stderr = &out, &errOut
			_ = cmd.Run()
			code := cmd.ProcessState.ExitCode()
			said := strings.TrimSpace(out.String() + errOut.String())
			why := ""
			for _, r := range refusedBefore {
				if why == "" && strings.Contains(said, r) {
					why = "refused before the store"
				}
			}
			switch {
			case why != "":
			case mr.TotalConnectionCount() == 0:
				why = "never dialled the store at NOVA_SPRINT_REDIS"
			case c.want != "" && !strings.Contains(out.String(), c.want):
				why = "stdout lacks " + strconv.Quote(c.want)
			}
			if why != "" {
				t.Errorf("nova-sprint %s: %s, exit %d: %s", c.line, why, code, said)
			}
		})
	}
}

// grammarAsToolEnv makes the test binary run as nova-sprint (TestMain), so a
// line runs in a child with its own environment: the test stays parallel.
const grammarAsToolEnv = "NOVA_SPRINT_AS_TOOL"

// TestMain: with grammarAsToolEnv set this binary is nova-sprint itself, on
// the process's real stdout and stderr, the way nova-tokens' tests run it.
func TestMain(m *testing.M) {
	if os.Getenv(grammarAsToolEnv) != "" {
		os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}
