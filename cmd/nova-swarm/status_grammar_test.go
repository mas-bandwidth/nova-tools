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

// TestStatusGrammar pins the one status grammar across the verbs (docs/STANDARD.md
// section 2, "The status word leads every line", and its exit table: 0 done, 1 the verb
// ran and said no, 2 usage or a store that did not answer). Every row drives a verb
// through the tool's run function -- member through cmdMember, whose send parameter is
// the package's fake seam -- with the fixtures the package's own tests use, and asserts
// the status line's leading words and the exit code together, so neither moves without
// the other. A row's wantLine is the status line's prefix: the verb's token, then the
// status word. Two OK rows carry no wantLine: template prints a verbatim document and
// version prints the one buildinfo line, and neither is a status line.
func TestStatusGrammar(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		drive    func(t *testing.T) (exit int, stdout, stderr string)
		wantExit int
		wantLine string
	}{
		{"template_ok", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "template", "--name", "card")
		}, 0, ""},
		{"template_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "template", "--nope")
		}, 2, "nova-swarm template REFUSED:"},
		{"lint_ok", func(t *testing.T) (int, string, string) {
			card, err := swarm.Template("card")
			require.NoError(t, err)
			return runSwarm(t, "lint", "--card", writeLintCard(t, "card.md", card), "--child-rules")
		}, 0, "LINT OK card=card.md"},
		{"lint_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "lint", "--nope")
		}, 2, "nova-swarm lint REFUSED:"},
		{"lint_failed", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "lint", "--card", writeLintCard(t, "empty.md", ""))
		}, 1, "LINT DRIFT card=empty.md"},
		{"member_ok", func(t *testing.T) (int, string, string) {
			args := append(memberWithoutOnce(t.TempDir()), "--ticks", "2", "--every", "1ms")
			var out, errb bytes.Buffer
			code := cmdMember(args[1:], &out, &errb, noServer)
			return code, out.String(), errb.String()
		}, 0, "MEMBER OK as=m1 ticks=2 running=0"},
		{"member_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "member", "--nope")
		}, 2, "nova-swarm member REFUSED:"},
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
		}, 0, "NATIVE OK label=lbl"},
		{"native_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "native", "--nope")
		}, 2, "nova-swarm native REFUSED:"},
		{"worker_ok", func(t *testing.T) (int, string, string) {
			return runWorkerCheck(workerCheckFixture(t, nil))
		}, 0, "WORKER OK check-1"},
		{"worker_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "worker", "check", filepath.Join(t.TempDir(), "absent.json"))
		}, 2, "nova-swarm worker check REFUSED:"},
		{"worker_failed", func(t *testing.T) (int, string, string) {
			bad := filepath.Join(t.TempDir(), "w.json")
			require.NoError(t, os.WriteFile(bad, []byte("{}\n"), 0o600))
			return runWorkerCheck(bad)
		}, 1, "WORKER DRIFT name:"},
		{"verify_ok", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "verify", "--result", aResult(t, "RESULT: lbl sha=abc123\nverdict: ok\n"),
				"--contract", "RESULT: lbl sha=abc123", "--label", "lbl")
		}, 0, "RESULT OK"},
		{"verify_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "verify", "--nope")
		}, 2, "nova-swarm verify REFUSED:"},
		{"verify_failed", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "verify", "--result", aResult(t, "SOMETHING ELSE\nverdict: ok\n"),
				"--contract", "RESULT: lbl sha=abc123", "--label", "lbl")
		}, 1, "RESULT REFUSED"},
		{"doctor_ok", func(t *testing.T) (int, string, string) {
			bin := filepath.Join(t.TempDir(), "nova-swarm")
			require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\necho nova-swarm v0.0.0-test sha=00000000\n"), 0o755))
			return runSwarm(t, "doctor", "--path", bin, "--local", bin)
		}, 0, "DOCTOR OK"},
		{"doctor_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "doctor", "--nope")
		}, 2, "nova-swarm doctor REFUSED:"},
		{"profile_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "profile", "--nope")
		}, 2, "nova-swarm profile REFUSED:"},
		{"slots_ok", func(t *testing.T) (int, string, string) {
			store := slotShares(t, "capacity\t1\nreserve\t0\nalice\t1\n")
			return runSwarm(t, "slots", "list", "--store", store)
		}, 0, "SLOTS OK store="},
		{"slots_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "slots", "list", "--nope")
		}, 2, "nova-swarm slots list REFUSED:"},
		{"version_ok", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "version")
		}, 0, ""},
		{"version_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "version", "extra")
		}, 2, "nova-swarm version REFUSED:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			exit, stdout, stderr := tc.drive(t)
			assert.Equal(t, tc.wantExit, exit, "stdout:\n%s\nstderr:\n%s", stdout, stderr)
			if tc.wantLine == "" {
				return
			}
			for _, out := range []string{stdout, stderr} {
				for _, line := range strings.Split(out, "\n") {
					if strings.HasPrefix(line, tc.wantLine) {
						return
					}
				}
			}
			t.Errorf("no line opens with %q\nstdout:\n%s\nstderr:\n%s", tc.wantLine, stdout, stderr)
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
