package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSessiontraceMainCoverSeq(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		xs   []string
		want string
	}{
		{"none", nil, "<<>>"},
		{"two words", []string{"a", "b"}, "<<a, b>>"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := seq(tc.xs)
			if got != tc.want {
				t.Fatalf("seq(%v) = %q, want %q", tc.xs, got, tc.want)
			}
		})
	}
}

func TestSessiontraceMainCoverModule(t *testing.T) {
	t.Parallel()
	// Trace 1: ends at EOF with an ok step (Code 2) and a usage step
	tr1 := trace{
		Seed: 1, Keep: true,
		Actions: []action{
			{Kind: "ok", Command: "create", Write: true},
			{Kind: "usage", Command: "help", Lost: false},
		},
		Steps: []step{
			{Action: action{Kind: "ok", Write: true}, Code: 2, Stdout: "output\n", Receipts: []receipt{{ID: "1-0", Values: map[string]any{"rev_before": "0", "rev_after": "1", "actor": "trace"}}}},
			{Action: action{Kind: "usage"}, Code: 2, Stdout: "usage\n"},
		},
		Exit: 0, EOF: true,
	}
	// Trace 2: last step is quit
	tr2 := trace{
		Seed: 2, Keep: false,
		Actions: []action{
			{Kind: "ok", Command: "create", Write: true},
			{Kind: "quit", Command: "exit", Lost: false},
		},
		Steps: []step{
			{Action: action{Kind: "ok", Write: true}, Code: 0, Stdout: "output\n", Receipts: []receipt{{ID: "2-0", Values: map[string]any{"rev_before": "0", "rev_after": "1", "actor": "trace"}}}},
			{Action: action{Kind: "quit"}, Code: 0, Stdout: "quit\n"},
		},
		Exit: 1, EOF: false,
	}
	// Trace 3: ends neither way (fail)
	tr3 := trace{
		Seed: 3, Keep: true,
		Actions: []action{
			{Kind: "ok", Command: "create", Write: true},
		},
		Steps: []step{
			{Action: action{Kind: "ok", Write: true}, Code: 0, Stdout: "output\n", Receipts: []receipt{{ID: "3-0", Values: map[string]any{"rev_before": "0", "rev_after": "1", "actor": "trace"}}}},
		},
		Exit: 2, EOF: false,
	}
	traces := []trace{tr1, tr2, tr3}
	out := module(traces)
	require.Contains(t, out, "conn' = \"dead\"")
	require.Contains(t, out, "lineCode' = 2")
	require.Contains(t, out, "traceCase \\in 1..3")
	require.Contains(t, out, "ObservedKeep == <<TRUE, FALSE, TRUE>>")
	require.Contains(t, out, "ObservedEnd == <<\"eof\", \"quit\", \"fail\">>")
}

func TestSessiontraceMainCoverConfiguration(t *testing.T) {
	t.Parallel()
	// Trace with 2 actions
	tr2act := trace{
		Seed: 1, Keep: true,
		Actions: []action{{Kind: "ok", Write: true}, {Kind: "no"}},
		Steps: []step{
			{Action: action{Kind: "ok", Write: true}, Code: 0, Stdout: "out\n", Receipts: []receipt{{ID: "1-0", Values: map[string]any{"rev_before": "0", "rev_after": "1", "actor": "trace"}}}},
			{Action: action{Kind: "no"}, Code: 0, Stdout: "out\n"},
		},
		Exit: 0, EOF: true,
	}
	// Trace with 5 actions
	tr5act := trace{
		Seed: 2, Keep: false,
		Actions: []action{
			{Kind: "ok", Write: true}, {Kind: "ok"}, {Kind: "ok"}, {Kind: "no"}, {Kind: "no"},
		},
		Steps: []step{
			{Action: action{Kind: "ok", Write: true}, Code: 0, Stdout: "out\n", Receipts: []receipt{{ID: "1-0", Values: map[string]any{"rev_before": "0", "rev_after": "1", "actor": "trace"}}}},
			{Action: action{Kind: "ok"}, Code: 0, Stdout: "out\n"},
			{Action: action{Kind: "ok"}, Code: 0, Stdout: "out\n"},
			{Action: action{Kind: "no"}, Code: 0, Stdout: "out\n"},
			{Action: action{Kind: "no"}, Code: 0, Stdout: "out\n"},
		},
		Exit: 0, EOF: true,
	}
	out2 := configuration([]trace{tr2act})
	require.Contains(t, out2, "MaxLines = 2")
	require.Contains(t, out2, "SPECIFICATION TraceSpec")
	require.Contains(t, out2, "INVARIANTS")
	require.Contains(t, out2, "TraceObserved")
	require.Contains(t, out2, "TraceEnd")
	out5 := configuration([]trace{tr5act})
	require.Contains(t, out5, "MaxLines = 5")
}

func TestSessiontraceMainCoverWriteJSON(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()
	path := filepath.Join(tmpdir, "test.json")
	val := map[string]any{"key": "value", "num": 42}
	require.NoError(t, writeJSON(path, val))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(string(data), "\n"))
	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))
	require.Equal(t, val["key"], parsed["key"])
	require.Equal(t, float64(42), parsed["num"])
	// Refuse to marshal a channel
	ch := make(chan struct{})
	path2 := filepath.Join(tmpdir, "fail.json")
	require.Error(t, writeJSON(path2, ch))
	_, err = os.Stat(path2)
	require.True(t, os.IsNotExist(err))
	// Refuse path in missing directory
	misspath := filepath.Join(tmpdir, "missing", "file.json")
	require.Error(t, writeJSON(misspath, val))
}

func TestSessiontraceMainCoverRepoRoot(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmpdir, "tla"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpdir, "tla", "TableSession.tla"), []byte("MODULE S"), 0o644))
	origwd, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(origwd)
	require.NoError(t, os.Chdir(tmpdir))
	root, err := repoRoot()
	require.NoError(t, err)
	require.Equal(t, tmpdir, root)
}

func TestSessiontraceMainCoverCommand(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	tmpdir := t.TempDir()
	path := filepath.Join(tmpdir, "nothere.jar")
	code := command([]string{"--jar", path, "--out", tmpdir}, nil, &stderr)
	require.Equal(t, 1, code)
	require.Contains(t, stderr.String(), "no capture started")
	outpath := filepath.Join(tmpdir, "out.json")
	_, err := os.Stat(outpath)
	require.True(t, os.IsNotExist(err))
	dir := filepath.Join(tmpdir, "adir")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	stderr.Reset()
	code = command([]string{"--jar", dir, "--out", tmpdir}, nil, &stderr)
	require.Equal(t, 1, code)
	require.Contains(t, stderr.String(), "no capture started")
}

func TestSessiontraceMainCoverRunTLC(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()
	outdir := filepath.Join(tmpdir, "out")
	_, _, err := runTLC(t.Context(), tmpdir, "fake.jar", outdir, []trace{})
	require.Error(t, err)
	require.ErrorIs(t, err, os.ErrNotExist)
	outfiles, err := os.ReadDir(outdir)
	require.NoError(t, err)
	require.Empty(t, outfiles)
}
