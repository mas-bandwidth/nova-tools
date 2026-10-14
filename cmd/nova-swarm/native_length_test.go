package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/member"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestALengthFinishIsAHarnessFault(t *testing.T) {
	t.Parallel()
	row, err := os.ReadFile("testdata/length-finish/last-message.tsv")
	require.NoError(t, err)
	words, ok := parseLengthFinish(string(row))
	require.True(t, ok)
	assert.Equal(t, "finish length; output 0, reasoning 32000", words)
	raw, err := os.ReadFile("testdata/length-finish/native.log")
	require.NoError(t, err)
	r := nativeResult(t, string(raw), "cutoff")
	assert.Equal(t, member.EndStaging, r.End)
	assert.Empty(t, r.Usage, "a harness fault records no card cost")
	assert.Empty(t, r.Provider)
	_, why := member.Judge(r, member.Push{})
	assert.True(t, strings.HasPrefix(why, "staging refused: harness fault:"), why)
	assert.NotContains(t, why, "no result:")
	assert.NotContains(t, why, "provider failure")
}

func TestNativeSetsTheOutputTokenMax(t *testing.T) {
	t.Parallel()
	const name = "OPENCODE_EXPERIMENTAL_OUTPUT_TOKEN_MAX="
	for _, n := range []int{128000, 64000} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			t.Parallel()
			env := withOutputTokenMax([]string{name + "32000", "KEEP=yes", name + "8000"}, n)
			var found []string
			for _, e := range env {
				if strings.HasPrefix(e, name) {
					found = append(found, e)
				}
			}
			assert.Equal(t, []string{name + strconv.Itoa(n)}, found)
			assert.Contains(t, env, "KEEP=yes")
		})
	}
	f, nf := nativeFlagSet()
	require.True(t, f.parse([]string{"--output-token-max", "64000"}, &bytes.Buffer{}, &bytes.Buffer{}))
	assert.Equal(t, 64000, *nf.outputTokenMax)
	_, defaults := nativeFlagSet()
	assert.Equal(t, 128000, *defaults.outputTokenMax)
	assert.Equal(t, 128000, (nativeRunConfig{}).outputMax(), "omitted fields retain the default")
	for _, n := range []string{"0", "-1"} {
		var out, errs bytes.Buffer
		assert.Equal(t, 2, cmdNative([]string{"--output-token-max", n}, &out, &errs))
		assert.Contains(t, errs.String(), "--output-token-max wants positive tokens")
	}
}

func TestAStopFinishStaysNoResult(t *testing.T) {
	t.Parallel()
	words, ok := parseLengthFinish("assistant\tstop\t0\t32000")
	assert.False(t, ok)
	assert.Empty(t, words)
	endedTool, words, ok := parseRunEnd("1\tassistant\tstop\t\t\t0\t0")
	assert.True(t, ok)
	assert.False(t, endedTool)
	assert.Empty(t, words)
	r := nativeResult(t, "NATIVE INCOMPLETE label=c1 job=j tmp=t rc=0 wall=none harness=ok budget=10/1000 why=no-result\n", "stopped cleanly")
	_, why := member.Judge(r, member.Push{})
	assert.Contains(t, why, "no result:")
}

// Exercise report itself so moving the fault behind provider/asked fallback
// turns this red, even when a midturn cutoff is followed by another finish.
func TestAMidturnCutoffPrecedesFallbackReportsAndCost(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, capture, fixture string
		rc                     int
	}{
		{"later question", "Would you like me to continue?\n", "midturn-stop.tsv", 0},
		{"later tool", "tool result\n", "midturn-tool.tsv", 0},
		{"nonzero provider error", "level=ERROR message=\"stream error\" error=\"HTTP 503 overloaded\"\n", "midturn-tool.tsv", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			row, err := os.ReadFile(filepath.Join("testdata", "length-finish", tc.fixture))
			require.NoError(t, err)
			job := t.TempDir()
			cfg := nativeRunConfig{binary: "opencode", label: "c1", model: "provider/model", resultsRoot: filepath.Join(job, "published"), lengthRead: func(string, time.Time) (string, bool) {
				_, words, ok := parseRunEnd(string(row))
				return words, ok && words != ""
			}}
			log, err := os.Create(filepath.Join(job, "native.log"))
			require.NoError(t, err)
			capture := filepath.Join(job, "harness-output.log")
			require.NoError(t, os.WriteFile(capture, []byte(tc.capture), 0600))
			harnessOut, err := os.OpenFile(capture, os.O_APPEND|os.O_WRONLY, 0600)
			require.NoError(t, err)
			state := &nativeRunState{prep: &nativePrepared{cfg: cfg, jobDir: job, dataHome: job, tmpDir: job, stepArgv: []string{"test"}}, wal: &nativeWalled{}, log: log, harnessOut: harnessOut, outLog: capture, sampler: &liveSampler{stop: make(chan struct{})}, jobObserved: true, timeline: swarm.NewTimeline(), denials: swarm.NewShellDenialReader(), lastAttempt: 1, runStart: time.Unix(0, 0), res: nativeRunResult{rc: tc.rc, wallSeconds: 1}}
			var out bytes.Buffer
			_, code := report(state, &out)
			require.Zero(t, code, out.String())
			assert.Contains(t, out.String(), "NATIVE HARNESS-FAULT")
			assert.NotContains(t, out.String(), "NATIVE PROVIDER-")
			assert.NotContains(t, out.String(), "asked report")
			_, published := swarm.FindCardResult(job)
			assert.False(t, published, "no fallback may synthesize a result before this fault")
			r := nativeResult(t, "NATIVE OK label=c1 job=j rc=0 wall=1.00s harness=ok budget=unmetered spend=input:10,output:0,cost:0.25,model:provider/model\n"+out.String(), tc.capture)
			assert.Equal(t, member.EndStaging, r.End)
			assert.Empty(t, r.Usage)
		})
	}
}

func TestPublishedResultsAndHeadlessHarnessesIgnoreLengthMetadata(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"published", "headless"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			job := t.TempDir()
			calls := 0
			cfg := nativeRunConfig{binary: "opencode", lengthRead: func(string, time.Time) (string, bool) {
				calls++
				return "finish length; output 0, reasoning 32000", true
			}}
			if name == "published" {
				require.NoError(t, os.WriteFile(filepath.Join(job, "RESULT.md"), []byte("the child's own report"), 0600))
			} else {
				cfg.binary = "codex"
			}
			why, fault := nativeLengthFault(cfg, job, job, time.Unix(0, 0))
			assert.False(t, fault)
			assert.Empty(t, why)
			assert.Zero(t, calls)
		})
	}
}
