package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeConfig is nova-config as the program sees it: it records every call and
// answers `route show` from the state of each route (absent: no row).
type fakeConfig struct {
	state    map[string]string // route -> "enabled" | "disabled"
	calls    [][]string
	failSet  int  // exit code of a write, 0: ok
	noBinary bool // the binary cannot start
}

func (f *fakeConfig) run(_ context.Context, _ string, args []string, stdout, stderr io.Writer) (int, error) {
	f.calls = append(f.calls, args)
	if f.noBinary {
		return 0, errors.New("executable file not found")
	}
	if args[0] == "route" && args[1] == "show" {
		switch f.state[args[2]] {
		case "disabled":
			io.WriteString(stdout, "ROUTE name="+args[2]+" enabled=false note=-\n")
		case "enabled":
			io.WriteString(stdout, "ROUTE name="+args[2]+" enabled=true note=-\n")
		default:
			io.WriteString(stderr, "nova-config route show REFUSED: route "+args[2]+" not found\n")
			return 1, nil
		}
		return 0, nil
	}
	return f.failSet, nil
}

func (f *fakeConfig) writes() [][]string {
	var out [][]string
	for _, c := range f.calls {
		if c[1] != "show" || c[0] != "route" {
			out = append(out, c)
		}
	}
	return out
}

func drive(f *fakeConfig, env map[string]string, args ...string) (string, string, int) {
	var out, errs bytes.Buffer
	code := run(context.Background(), args, func(k string) string { return env[k] }, &out, &errs, f.run)
	return out.String(), errs.String(), code
}

func allDisabled() map[string]string {
	s := map[string]string{}
	for _, rn := range routeNotes {
		s[rn.route] = "disabled"
	}
	return s
}

func TestNotesWritesEveryDisabledRouteAndTheMachineAndNothingElse(t *testing.T) {
	t.Parallel()

	f := &fakeConfig{state: allDisabled()}
	out, errs, code := drive(f, map[string]string{"NOTES_AS": "a1", "NOTES_CONN": "--pg postgres://h/db", "NOVA_CONFIG": "nc"}, "m7")
	require.Equal(t, 0, code, errs)
	assert.Equal(t, "NOTES DONE routes_written=8 routes_skipped=0 routes_missing=0 machine=m7 dry_run=false\n", out)
	w := f.writes()
	require.Len(t, w, 9, "eight routes and the machine")
	for i, c := range w[:8] {
		assert.Equal(t, []string{"route", "set", routeNotes[i].route, "--note", routeNotes[i].note, "--as", "a1", "--pg", "postgres://h/db"}, c)
		assert.Contains(t, c[4], "nova-tools#5101")
		assert.NotContains(t, c, "--enabled", "the program enables and disables nothing")
	}
	assert.Equal(t, []string{"machine", "set", "m7", "--note", machineNote, "--as", "a1", "--pg", "postgres://h/db"}, w[8])
	assert.Equal(t, []string{"route", "show", routeNotes[0].route, "--pg", "postgres://h/db"}, f.calls[0], "the store flags ride a show")
}

func TestNotesDryRunPassesTheFlagToEveryWrite(t *testing.T) {
	t.Parallel()

	f := &fakeConfig{state: allDisabled()}
	out, _, code := drive(f, nil, "m7", "--dry-run")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "machine=m7 dry_run=true")
	for _, c := range f.writes() {
		assert.Equal(t, []string{"--dry-run"}, c[5:], "%v", c)
	}
	assert.NotContains(t, f.calls[0], "--dry-run", "a show is a read")
}

func TestNotesSkipsAnEnabledRouteAndCountsAMissingOneAndEndsOne(t *testing.T) {
	t.Parallel()

	s := allDisabled()
	s["flash-mimo26-openrouter"] = "enabled"
	delete(s, "flash-luna56-opencode")
	f := &fakeConfig{state: s}
	out, errs, code := drive(f, nil, "m7")
	assert.Equal(t, 1, code)
	assert.Contains(t, out, "NOTES SKIP route=flash-mimo26-openrouter reason=enabled\n")
	assert.Contains(t, out, "NOTES DONE routes_written=6 routes_skipped=1 routes_missing=1 machine=m7 dry_run=false\n")
	assert.Contains(t, errs, "NOTES MISSING route=flash-luna56-opencode\n")
	assert.Len(t, f.writes(), 7, "six routes and the machine")
}

func TestNotesRefusesWithoutTheMachineOrWithAnUnknownArgumentAndCallsNothing(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, "want the held machine's name first"},
		{[]string{"--dry-run"}, "want the held machine's name first"},
		{[]string{""}, "want the held machine's name first"},
		{[]string{"m7", "--now"}, "REFUSED: unknown argument --now"},
	} {
		f := &fakeConfig{state: allDisabled()}
		out, errs, code := drive(f, nil, c.args...)
		assert.Equal(t, 2, code, "%v", c.args)
		assert.Contains(t, errs, c.want)
		assert.Empty(t, out)
		assert.Empty(t, f.calls, "a refused run calls nothing")
	}
}

func TestNotesAFailedWriteEndsTheRunWithItsCode(t *testing.T) {
	t.Parallel()

	f := &fakeConfig{state: allDisabled(), failSet: 3}
	out, _, code := drive(f, nil, "m7")
	assert.Equal(t, 3, code)
	assert.NotContains(t, out, "NOTES DONE")
	assert.Len(t, f.writes(), 1, "the first failed write stops the run")
}

func TestNotesABinaryThatCannotStartIsEveryRouteMissing(t *testing.T) {
	t.Parallel()

	f := &fakeConfig{noBinary: true}
	_, errs, code := drive(f, nil, "m7")
	assert.Equal(t, 1, code, "the machine write cannot start either: %s", errs)
	assert.Equal(t, len(routeNotes), strings.Count(errs, "NOTES MISSING"))
	assert.Contains(t, errs, "notes-2026-10-02 FAILED: nova-config machine set")
}
