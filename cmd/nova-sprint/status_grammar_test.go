package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStatusGrammar pins the one status grammar docs/STANDARD.md section 2
// holds for every tool: after the verb token the first word is OK, REFUSED or
// FAILED, and the exit code tells the same truth (0 done, 1 the verb ran and
// said no, 2 usage or a store that did not answer). Each row drives one
// outcome of one verb of this tool through run and asserts that word and that
// exit together, so a word that moves and leaves its exit behind, or an exit
// that moves and leaves its word, turns this test red. A refusal is the one
// line `nova-sprint[ <verb>] REFUSED: <what>; run: <remedy>` on stderr with
// nothing on stdout (main.go refuse). The step verbs' own FAIL printers
// (check, repair, tick, accept --group) are also driven to their FAILED word
// by TestCheckRepairTickAndGroupFailsExitNonZero and
// TestEveryVerbThatPrintsFailExitsNonZero; this test holds the words of the
// verbs that print a summary line and a refusal.
func TestStatusGrammar(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		// setup builds the fixture and returns the command line to run.
		setup func(t *testing.T) *testApp
		line  string
		// wantToken opens the status line of the wanted outcome.
		wantToken string
		// wantWord is the first word after it: OK, REFUSED or FAILED.
		wantWord string
		// wantMarker is the refusal shape a REFUSED row carries on stderr.
		wantMarker string
		wantExit   int
	}{
		{
			name: "add ok",
			setup: func(t *testing.T) *testApp {
				ta := newTestApp(t)
				ta.ok("init --readers reader-a,reader-b --members m1")
				return ta
			},
			line: "add --stream s1 --count 1", wantToken: "ADD", wantWord: "OK", wantExit: 0,
		},
		{
			name: "add refused with no stream",
			setup: func(t *testing.T) *testApp {
				ta := newTestApp(t)
				ta.ok("init --readers reader-a,reader-b --members m1")
				return ta
			},
			line: "add", wantToken: "nova-sprint add", wantWord: "REFUSED",
			wantMarker: "; run: nova-sprint add -h", wantExit: 2,
		},
		{
			name: "resolve failed over an unknown card",
			setup: func(t *testing.T) *testApp {
				ta := newTestApp(t)
				ta.ok("init --readers reader-a,reader-b --members m1")
				ta.ok("add --stream s1 --count 1")
				return ta
			},
			line: "resolve s1-nope", wantToken: "RESOLVE", wantWord: "FAILED", wantExit: 1,
		},
		{
			name: "take ok",
			setup: func(t *testing.T) *testApp {
				ta := newTestApp(t)
				ta.ok("init --readers reader-a,reader-b --members m1")
				ta.ok("add --stream s1 --count 1")
				ta.deal(1)
				return ta
			},
			line: "take --as m1 s1-1.w1@1", wantToken: "TAKE-BY-ID", wantWord: "OK", wantExit: 0,
		},
		{
			name: "take refused with no member",
			setup: func(t *testing.T) *testApp {
				ta := newTestApp(t)
				ta.ok("init --readers reader-a,reader-b --members m1")
				return ta
			},
			line: "take", wantToken: "nova-sprint take", wantWord: "REFUSED",
			wantMarker: "; run: nova-sprint take -h", wantExit: 2,
		},
		{
			name: "check ok",
			setup: func(t *testing.T) *testApp {
				ta := newTestApp(t)
				ta.ok("init --readers reader-a,reader-b --members m1")
				return ta
			},
			line: "check", wantToken: "CHECK", wantWord: "OK", wantExit: 0,
		},
		{
			name: "tick ok",
			setup: func(t *testing.T) *testApp {
				ta := newTestApp(t)
				ta.ok("init --readers reader-a,reader-b --members m1")
				return ta
			},
			line: "tick", wantToken: "TICK", wantWord: "OK", wantExit: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ta := tc.setup(t)
			code, out, errs := ta.do(tc.line)
			require.Equal(t, tc.wantExit, code, "exit = %d, want %d\nstdout: %q\nstderr: %q", code, tc.wantExit, out, errs)
			if tc.wantWord == "REFUSED" {
				assert.Empty(t, out, "a refusal prints nothing on stdout, got %q", out)
				first := strings.Split(strings.TrimRight(errs, "\n"), "\n")[0]
				assert.True(t, strings.HasPrefix(first, tc.wantToken+" REFUSED:"), "the refusal must open with %q REFUSED:, got %q", tc.wantToken, first)
				assert.Contains(t, errs, tc.wantMarker, "the refusal must carry its remedy, got %q", errs)
				return
			}
			var status string
			for _, l := range strings.Split(strings.TrimRight(out+errs, "\n"), "\n") {
				if strings.HasPrefix(l, tc.wantToken+" ") {
					status = l
					break
				}
			}
			fields := strings.Fields(status)
			require.GreaterOrEqual(t, len(fields), 2, "the status line must open with a verb token and a status word, got %q in\n%s%s", status, out, errs)
			assert.Equal(t, tc.wantToken, fields[0], "the verb token is %q, want %q in %q", fields[0], tc.wantToken, status)
			assert.Equal(t, tc.wantWord, fields[1], "the word after the verb token is %q, want %q in %q", fields[1], tc.wantWord, status)
		})
	}
}
