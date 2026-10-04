package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// stdoutMu synchronizes native subtests that read or swap os.Stdout.
var stdoutMu sync.Mutex

// verbTokens lists the verb and line tokens that lead status lines in nova-swarm.
var verbTokens = []string{
	"nova-swarm template",
	"nova-swarm lint",
	"nova-swarm member",
	"nova-swarm native",
	"nova-swarm worker",
	"nova-swarm verify",
	"nova-swarm doctor",
	"nova-swarm profile",
	"nova-swarm slots",
	"nova-swarm version",
	"nova-swarm",
	"LINT",
	"MEMBER",
	"NATIVE",
	"STAGE",
	"CARD",
	"WORKER",
	"RESULT",
	"DOCTOR",
	"SLOTS",
}

// statusWords lists the status grammar words that lead a typed outcome.
var statusWords = map[string]bool{
	"OK":      true,
	"REFUSED": true,
	"FAILED":  true,
	"FAIL":    true,
	"DRIFT":   true,
}

// statusAfter returns the first status word after token in out, and whether the
// token was found on any line: the status word that leads a typed line (STANDARD §2).
func statusAfter(out, token string) (string, bool) {
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, token); ok && strings.HasPrefix(rest, " ") {
			rest = strings.TrimLeft(rest, " ")
			word, _, _ := strings.Cut(rest, " ")
			word = strings.TrimRight(word, ":")
			if word == strings.ToUpper(word) && word != "" {
				return word, true
			}
		}
	}
	return "", false
}

// captureStageOutput captures the first STAGE line printed to os.Stdout during fn.
func captureStageOutput(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err, "os.Pipe")
	orig := os.Stdout
	os.Stdout = w
	lineCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Fprintln(orig, line)
			if strings.HasPrefix(line, "STAGE FAIL") && strings.Contains(line, "missing-mirror.git") {
				lineCh <- line
			}
		}
		close(lineCh)
	}()

	fn()

	os.Stdout = orig
	w.Close()
	defer r.Close()
	line, ok := <-lineCh
	require.True(t, ok, "printed no STAGE line")
	return line
}

// TestStatusGrammar pins the one status grammar across the verbs (docs/STANDARD.md
// section 2, "The status word leads every line", and its exit table: 0 done, 1 the verb
// ran and said no, 2 usage or a store that did not answer). Every row drives a verb
// through the tool's run function -- member through cmdMember, whose send parameter is
// the package's fake seam -- with the fixtures the package's own tests use, to its OK,
// REFUSED, and non-zero outcomes (DRIFT for lint and worker, RESULT REFUSED for verify,
// CARD REFUSED for native's public-class gate, and STAGE FAIL for native staging where
// the rename stays held), and asserts the line prefix, the first word after the verb
// token, and the exit code together, so none moves without the others. Four OK rows
// carry no status line: help prints the banner, template prints a verbatim document,
// version prints the one buildinfo line and profile prints its PROFILE and PROFILE
// SUMMARY event lines, and none is a status line.
func TestStatusGrammar(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		drive    func(t *testing.T) (exit int, stdout, stderr string)
		wantExit int
		wantLine string
		token    string
		wantWord string
	}{
		{"help_ok", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "help")
		}, 0, "", "", ""},
		{"template_ok", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "template", "--name", "card")
		}, 0, "", "", ""},
		{"template_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "template", "--nope")
		}, 2, "nova-swarm template REFUSED:", "nova-swarm template", "REFUSED"},
		{"lint_ok", func(t *testing.T) (int, string, string) {
			card, err := swarm.Template("card")
			require.NoError(t, err)
			return runSwarm(t, "lint", "--card", writeLintCard(t, "card.md", card), "--child-rules")
		}, 0, "LINT OK card=card.md", "LINT", "OK"},
		{"lint_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "lint", "--nope")
		}, 2, "nova-swarm lint REFUSED:", "nova-swarm lint", "REFUSED"},
		{"lint_drift", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "lint", "--card", writeLintCard(t, "empty.md", ""))
		}, 1, "LINT DRIFT card=empty.md", "LINT", "DRIFT"},
		{"member_ok", func(t *testing.T) (int, string, string) {
			args := append(memberWithoutOnce(t.TempDir()), "--ticks", "2", "--every", "1ms")
			var out, errb bytes.Buffer
			code := cmdMember(args[1:], &out, &errb, noServer)
			return code, out.String(), errb.String()
		}, 0, "MEMBER OK as=m1 ticks=2 running=0", "MEMBER", "OK"},
		{"member_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "member", "--nope")
		}, 2, "nova-swarm member REFUSED:", "nova-swarm member", "REFUSED"},
		{"native_ok", func(t *testing.T) (int, string, string) {
			windowsIsNotABench(t)
			bin := nativeHarness(t)
			root, slot := aSlot(t)
			cardPath := filepath.Join(root, "card.md")
			require.NoError(t, os.WriteFile(cardPath, []byte("a card\n"), 0o644))
			stdoutMu.Lock()
			defer stdoutMu.Unlock()
			var out, errb bytes.Buffer
			code := run([]string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1",
				"--harness", bin, "--model", "fake/fake-model", "--label", "lbl", "--card", cardPath,
				"--slot", slot, "--root", root, "--deadline", "30s", "--no-wall"},
				strings.NewReader(""), &out, &errb, time.Now())
			return code, out.String(), errb.String()
		}, 0, "NATIVE OK label=lbl", "NATIVE", "OK"},
		{"native_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "native", "--nope")
		}, 2, "nova-swarm native REFUSED:", "nova-swarm native", "REFUSED"},
		{"native_public_class_refused", func(t *testing.T) (int, string, string) {
			root, slot := aSlot(t)
			cardPath := filepath.Join(root, "card.md")
			require.NoError(t, os.WriteFile(cardPath, []byte("a card\n"), 0o644))
			return runSwarm(t, "native", "--tokens", "unmetered", "--harness", nativeHarness(t),
				"--card", cardPath, "--slot", slot, "--root", root, "--deadline", "30s", "--no-wall",
				"--worker", workerCheckFixture(t, func(d map[string]any) { d["class"] = "public" }))
		}, 1, "CARD REFUSED reason=private-source repo=- class=public worker=check-1", "CARD", "REFUSED"},
		{"native_stage_fail", func(t *testing.T) (int, string, string) {
			windowsIsNotABench(t)
			bin := nativeHarness(t)
			root, slot := aSlot(t)
			cardPath := filepath.Join(root, "card.md")
			cardText := "RESULT: card-stage-fail sha=123456789012\nREPO: https://example.com/mas-bandwidth/missing-mirror.git\nBASE: dev\n"
			require.NoError(t, os.WriteFile(cardPath, []byte(cardText), 0o644))
			stdoutMu.Lock()
			defer stdoutMu.Unlock()
			var out, errb bytes.Buffer
			var code int
			line := captureStageOutput(t, func() {
				code = run([]string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1",
					"--harness", bin, "--model", "fake/fake-model", "--label", "card-stage-fail", "--card", cardPath,
					"--slot", slot, "--root", root, "--deadline", "30s", "--no-wall"},
					strings.NewReader(""), &out, &errb, time.Now())
			})
			return code, line + "\n" + out.String(), errb.String()
		}, 2, "STAGE FAIL bench=", "STAGE", "FAIL"},
		{"worker_ok", func(t *testing.T) (int, string, string) {
			return runWorkerCheck(workerCheckFixture(t, nil))
		}, 0, "WORKER OK check-1", "WORKER", "OK"},
		{"worker_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "worker", "check", filepath.Join(t.TempDir(), "absent.json"))
		}, 2, "nova-swarm worker check REFUSED:", "nova-swarm worker check", "REFUSED"},
		{"worker_drift", func(t *testing.T) (int, string, string) {
			bad := filepath.Join(t.TempDir(), "w.json")
			require.NoError(t, os.WriteFile(bad, []byte("{}\n"), 0o600))
			return runWorkerCheck(bad)
		}, 1, "WORKER DRIFT name:", "WORKER", "DRIFT"},
		{"verify_ok", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "verify", "--result", aResult(t, "RESULT: lbl sha=abc123\nverdict: ok\n"),
				"--contract", "RESULT: lbl sha=abc123", "--label", "lbl")
		}, 0, "RESULT OK", "RESULT", "OK"},
		{"verify_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "verify", "--nope")
		}, 2, "nova-swarm verify REFUSED:", "nova-swarm verify", "REFUSED"},
		{"verify_result_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "verify", "--result", aResult(t, "SOMETHING ELSE\nverdict: ok\n"),
				"--contract", "RESULT: lbl sha=abc123", "--label", "lbl")
		}, 1, "RESULT REFUSED", "RESULT", "REFUSED"},
		{"doctor_ok", func(t *testing.T) (int, string, string) {
			bin := filepath.Join(t.TempDir(), "nova-swarm")
			require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\necho nova-swarm v0.0.0-test sha=00000000\n"), 0o755))
			return runSwarm(t, "doctor", "--path", bin, "--local", bin)
		}, 0, "DOCTOR OK", "DOCTOR", "OK"},
		{"doctor_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "doctor", "--nope")
		}, 2, "nova-swarm doctor REFUSED:", "nova-swarm doctor", "REFUSED"},
		{"profile_ok", func(t *testing.T) (int, string, string) {
			root := t.TempDir()
			timelineFile(t, filepath.Join(root, "job-a"),
				"t_start\tt_end\ttool\twall_ms\tinput_tokens\toutput_tokens\n"+
					"2026-09-17T00:00:00Z\t2026-09-17T00:00:10Z\tmodel\t10000\t100\t20\n")
			return runSwarm(t, "profile", "--jobs", filepath.Join(root, "job-a"))
		}, 0, "", "", ""},
		{"profile_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "profile", "--nope")
		}, 2, "nova-swarm profile REFUSED:", "nova-swarm profile", "REFUSED"},
		{"slots_ok", func(t *testing.T) (int, string, string) {
			store := slotShares(t, "capacity\t1\nreserve\t0\nalice\t1\n")
			return runSwarm(t, "slots", "list", "--store", store)
		}, 0, "SLOTS OK store=", "SLOTS", "OK"},
		{"slots_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "slots", "list", "--nope")
		}, 2, "nova-swarm slots list REFUSED:", "nova-swarm slots list", "REFUSED"},
		{"version_ok", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "version")
		}, 0, "", "", ""},
		{"version_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "version", "extra")
		}, 2, "nova-swarm version REFUSED:", "nova-swarm version", "REFUSED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			exit, stdout, stderr := tc.drive(t)
			assert.Equal(t, tc.wantExit, exit, "stdout:\n%s\nstderr:\n%s", stdout, stderr)
			if tc.wantLine != "" {
				foundPrefix := false
				for _, out := range []string{stdout, stderr} {
					for _, line := range strings.Split(out, "\n") {
						if strings.HasPrefix(line, tc.wantLine) {
							foundPrefix = true
							break
						}
					}
					if foundPrefix {
						break
					}
				}
				require.True(t, foundPrefix, "no line opens with %q\nstdout:\n%s\nstderr:\n%s", tc.wantLine, stdout, stderr)
			}
			if tc.token == "" {
				for _, out := range []string{stdout, stderr} {
					for _, line := range strings.Split(out, "\n") {
						for _, tok := range verbTokens {
							if rest, ok := strings.CutPrefix(line, tok); ok && strings.HasPrefix(rest, " ") {
								rest = strings.TrimLeft(rest, " ")
								word, _, _ := strings.Cut(rest, " ")
								word = strings.TrimRight(word, ":")
								assert.False(t, statusWords[word], "line has status word %q after verb token %q: %q", word, tok, line)
							}
						}
					}
				}
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
