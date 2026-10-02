package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// statusAfter returns the first status word after token in out, and whether the
// token was found on any line: the status word that leads a typed line (STANDARD §2).
func statusAfter(out, token string) (string, bool) {
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, token); ok && strings.HasPrefix(rest, " ") {
			rest = strings.TrimLeft(rest, " ")
			word, _, _ := strings.Cut(rest, " ")
			word = strings.TrimRight(word, ":")
			switch word {
			case "OK", "REFUSED", "FAILED", "DRIFT":
				return word, true
			}
		}
	}
	return "", false
}

// TestStatusGrammar pins the one status grammar across the verbs (docs/STANDARD.md
// section 2, "The status word leads every line", and its exit table: 0 done, 1 the verb
// ran and said no, 2 usage or a store that did not answer). Every row drives a verb
// through the tool's run function -- member through cmdMember, whose send parameter is
// the package's fake seam -- with the fixtures the package's own tests use, to one OK,
// one REFUSED and one FAILED outcome where the verb has each, and asserts the first
// word after the verb token and the exit code together, so neither moves without the other.
// One FAILED row's line is not the verb's own: native's public-class gate (the
// CARD-8390 gate, before any directory is made) refuses any card of a public-class
// worker whose root carries no public-repos.txt, with the card's CARD REFUSED line at
// exit 1. Three OK rows carry no status line: template prints a verbatim document,
// version prints the one buildinfo line and profile prints its PROFILE and PROFILE
// SUMMARY event lines, and none is a status line.
func TestStatusGrammar(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		drive    func(t *testing.T) (exit int, stdout, stderr string)
		wantExit int
		token    string
		wantWord string
	}{
		{"template_ok", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "template", "--name", "card")
		}, 0, "", ""},
		{"template_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "template", "--nope")
		}, 2, "nova-swarm template", "REFUSED"},
		{"lint_ok", func(t *testing.T) (int, string, string) {
			card, err := swarm.Template("card")
			require.NoError(t, err)
			return runSwarm(t, "lint", "--card", writeLintCard(t, "card.md", card), "--child-rules")
		}, 0, "LINT", "OK"},
		{"lint_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "lint", "--nope")
		}, 2, "nova-swarm lint", "REFUSED"},
		{"lint_failed", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "lint", "--card", writeLintCard(t, "empty.md", ""))
		}, 1, "LINT", "DRIFT"},
		{"member_ok", func(t *testing.T) (int, string, string) {
			args := append(memberWithoutOnce(t.TempDir()), "--ticks", "2", "--every", "1ms")
			var out, errb bytes.Buffer
			code := cmdMember(args[1:], &out, &errb, noServer)
			return code, out.String(), errb.String()
		}, 0, "MEMBER", "OK"},
		{"member_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "member", "--nope")
		}, 2, "nova-swarm member", "REFUSED"},
		{"native_ok", func(t *testing.T) (int, string, string) {
			windowsIsNotABench(t)
			bin := nativeHarness(t)
			root, slot := aSlot(t)
			cardPath := filepath.Join(root, "card.md")
			require.NoError(t, os.WriteFile(cardPath, []byte("a card\n"), 0o644))
			var out, errb bytes.Buffer
			code := run([]string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1",
				"--harness", bin, "--model", "fake/fake-model", "--label", "lbl", "--card", cardPath,
				"--slot", slot, "--root", root, "--deadline", "30s", "--no-wall"},
				strings.NewReader(""), &out, &errb, time.Now())
			return code, out.String(), errb.String()
		}, 0, "NATIVE", "OK"},
		{"native_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "native", "--nope")
		}, 2, "nova-swarm native", "REFUSED"},
		{"native_failed", func(t *testing.T) (int, string, string) {
			root, slot := aSlot(t)
			cardPath := filepath.Join(root, "card.md")
			require.NoError(t, os.WriteFile(cardPath, []byte("a card\n"), 0o644))
			return runSwarm(t, "native", "--tokens", "unmetered", "--harness", nativeHarness(t),
				"--card", cardPath, "--slot", slot, "--root", root, "--deadline", "30s", "--no-wall",
				"--worker", workerCheckFixture(t, func(d map[string]any) { d["class"] = "public" }))
		}, 1, "CARD", "REFUSED"},
		{"worker_ok", func(t *testing.T) (int, string, string) {
			return runWorkerCheck(workerCheckFixture(t, nil))
		}, 0, "WORKER", "OK"},
		{"worker_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "worker", "check", filepath.Join(t.TempDir(), "absent.json"))
		}, 2, "nova-swarm worker check", "REFUSED"},
		{"worker_failed", func(t *testing.T) (int, string, string) {
			bad := filepath.Join(t.TempDir(), "w.json")
			require.NoError(t, os.WriteFile(bad, []byte("{}\n"), 0o600))
			return runWorkerCheck(bad)
		}, 1, "WORKER", "DRIFT"},
		{"verify_ok", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "verify", "--result", aResult(t, "RESULT: lbl sha=abc123\nverdict: ok\n"),
				"--contract", "RESULT: lbl sha=abc123", "--label", "lbl")
		}, 0, "RESULT", "OK"},
		{"verify_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "verify", "--nope")
		}, 2, "nova-swarm verify", "REFUSED"},
		{"verify_failed", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "verify", "--result", aResult(t, "SOMETHING ELSE\nverdict: ok\n"),
				"--contract", "RESULT: lbl sha=abc123", "--label", "lbl")
		}, 1, "RESULT", "REFUSED"},
		{"doctor_ok", func(t *testing.T) (int, string, string) {
			bin := filepath.Join(t.TempDir(), "nova-swarm")
			require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\necho nova-swarm v0.0.0-test sha=00000000\n"), 0o755))
			return runSwarm(t, "doctor", "--path", bin, "--local", bin)
		}, 0, "DOCTOR", "OK"},
		{"doctor_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "doctor", "--nope")
		}, 2, "nova-swarm doctor", "REFUSED"},
		{"profile_ok", func(t *testing.T) (int, string, string) {
			root := t.TempDir()
			timelineFile(t, filepath.Join(root, "job-a"),
				"t_start\tt_end\ttool\twall_ms\tinput_tokens\toutput_tokens\n"+
					"2026-09-17T00:00:00Z\t2026-09-17T00:00:10Z\tmodel\t10000\t100\t20\n")
			return runSwarm(t, "profile", "--jobs", filepath.Join(root, "job-a"))
		}, 0, "", ""},
		{"profile_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "profile", "--nope")
		}, 2, "nova-swarm profile", "REFUSED"},
		{"slots_ok", func(t *testing.T) (int, string, string) {
			store := slotShares(t, "capacity\t1\nreserve\t0\nalice\t1\n")
			return runSwarm(t, "slots", "list", "--store", store)
		}, 0, "SLOTS", "OK"},
		{"slots_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "slots", "list", "--nope")
		}, 2, "nova-swarm slots list", "REFUSED"},
		{"version_ok", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "version")
		}, 0, "", ""},
		{"version_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "version", "extra")
		}, 2, "nova-swarm version", "REFUSED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			exit, stdout, stderr := tc.drive(t)
			assert.Equal(t, tc.wantExit, exit, "stdout:\n%s\nstderr:\n%s", stdout, stderr)
			if tc.token == "" {
				return
			}
			out := stdout + "\n" + stderr
			word, found := statusAfter(out, tc.token)
			require.True(t, found, "output lacks token %q on any line:\nstdout:\n%s\nstderr:\n%s", tc.token, stdout, stderr)
			assert.Equal(t, tc.wantWord, word, "first word after %q = %q, want %q (exit %d)\nstdout:\n%s\nstderr:\n%s", tc.token, word, tc.wantWord, exit, stdout, stderr)
		})
	}
}

// aResult writes a RESULT.md a verify row reads and returns its path.
func aResult(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "RESULT.md")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}
