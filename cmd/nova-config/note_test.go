package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The note of a route and of a machine through the one grammar
// (docs/SPEC-CONFIG.md, "The note"): set with --note on add and set, cleared
// with --note '', whole in show, cut to one line in list, in the history
// row, and in the view apply writes.

const sayWhy = "say why: --note '<the measured reason>'"

func TestTheNoteIsSetShownCutListedClearedAndRecordedForARoute(t *testing.T) {
	t.Parallel()

	h := loopHarness(t)
	long := strings.Repeat("ran to the deadline with no result; ", 6) + "END"
	steps := []struct {
		name string
		args []string
		code int
		out  string // a phrase stdout must hold
		not  string // a phrase stdout must not hold
		errs string // a phrase stderr must hold
	}{
		{name: "add with a note", args: []string{"route", "add", "r1", "--tier", "flash", "--provider", "p", "--model", "m", "--deadline", "60", "--note", "the cheap one"},
			out: "CONFIG ADD kind=route name=r1 rev=1\n"},
		{name: "show carries the note", args: []string{"route", "show", "r1"}, out: " note=the\\x20cheap\\x20one created="},
		{name: "set a long note", args: []string{"route", "set", "r1", "--note", long}, out: "CONFIG SET kind=route name=r1 rev=2 changed=note\n"},
		{name: "show keeps it whole", args: []string{"route", "show", "r1"}, out: "END created="},
		{name: "list cuts it to one line", args: []string{"route", "list"}, out: "...\n", not: "END"},
		{name: "json list keeps it whole", args: []string{"route", "list", "--json"}, out: "END"},
		{name: "disabling with no note is refused before the store opens", args: []string{"route", "set", "r1", "--enabled", "false"}, code: 2, errs: sayWhy},
		{name: "disabling with a note", args: []string{"route", "set", "r1", "--enabled", "false", "--note", "4 of 52 ok"}, out: "CONFIG SET kind=route name=r1 rev=3 changed=enabled,note\n"},
		{name: "clearing the note of a disabled route is refused", args: []string{"route", "set", "r1", "--note", ""}, code: 1, errs: sayWhy},
		{name: "enabling needs no note", args: []string{"route", "set", "r1", "--enabled", "true"}, out: "CONFIG SET kind=route name=r1 rev=4 changed=enabled\n"},
		{name: "a note is cleared with --note ''", args: []string{"route", "set", "r1", "--note", ""}, out: "CONFIG SET kind=route name=r1 rev=5 changed=note\n"},
		{name: "cleared, the list prints a dash", args: []string{"route", "list"}, out: " price_as_of=- note=-\n"},
		{name: "history shows who wrote which note when", args: []string{"route", "history", "r1"}, out: "op=set actor=a1 at="},
		{name: "adding a disabled route with no note is refused", args: []string{"route", "add", "r2", "--tier", "flash", "--provider", "p", "--model", "m", "--deadline", "60", "--enabled", "false"}, code: 2, errs: sayWhy},
	}
	for _, s := range steps {
		code, out, errs := h.run(t, s.args...)
		require.Equal(t, s.code, code, "%s: %v\nstdout: %s\nstderr: %s", s.name, s.args, out, errs)
		if s.out != "" {
			assert.Contains(t, out, s.out, s.name)
		}
		if s.not != "" {
			assert.NotContains(t, out, s.not, s.name)
		}
		if s.errs != "" {
			assert.Contains(t, errs, s.errs, s.name)
			assert.Equal(t, 1, strings.Count(errs, "\n"), "%s: one refusal line", s.name)
		}
	}
	_, out, _ := h.run(t, "route", "history", "r1")
	assert.Contains(t, out, `op=add actor=a1 `, "the add")
	assert.Contains(t, out, `enabled=true>false note=ran\x20to\x20the\x20deadline`, "the set that disabled it wrote the reason in the same row")
	assert.Contains(t, out, `>4\x20of\x2052\x20ok`, "the note before and after")
	assert.Contains(t, out, `note=4\x20of\x2052\x20ok>-`, "the clear")
	assert.Equal(t, 5, strings.Count(out, "HISTORY id="), "an add and four sets: %s", out)
}

func TestTheNoteOfAMachineIsSetShownCutListedAndClearedAsTheRoutesIs(t *testing.T) {
	t.Parallel()

	h := loopHarness(t)
	code, out, errs := h.run(t, "machine", "add", "superman", "--user", "u", "--seat", "s", "--slots", "8", "--width", "8")
	require.Equal(t, 0, code, errs)
	assert.NotContains(t, out, "note", "no note is asked for on add")
	long := "held 1:46 PM: reads kernel-bound, " + strings.Repeat("load 80 on 36 cores; ", 6) + "END"
	for _, note := range []string{"held 1:46 PM: reads kernel-bound, see nova-tools#5101", long} {
		code, out, errs = h.run(t, "machine", "set", "superman", "--note", note)
		require.Equal(t, 0, code, errs)
		assert.Contains(t, out, "changed=note")
	}
	code, out, _ = h.run(t, "machine", "show", "superman")
	require.Equal(t, 0, code)
	assert.Contains(t, out, "END", "show is whole")
	assert.Contains(t, out, " note=held\\x201:46\\x20PM:")
	code, out, _ = h.run(t, "machine", "list")
	require.Equal(t, 0, code)
	assert.NotContains(t, out, "END", "list is one cut line")
	assert.Contains(t, out, "...", "list marks the cut")
	assert.Equal(t, 2, strings.Count(out, "\n"), "one machine line and the count line: %q", out)
	code, out, _ = h.run(t, "machine", "history", "superman")
	require.Equal(t, 0, code)
	assert.Contains(t, out, `note=->held\x201:46\x20PM:\x20reads\x20kernel-bound,\x20see\x20nova-tools#5101`)
	code, out, errs = h.run(t, "machine", "set", "superman", "--note", "")
	require.Equal(t, 0, code, errs)
	assert.Equal(t, "CONFIG SET kind=machine name=superman rev=4 changed=note\n", out)
	code, out, _ = h.run(t, "machine", "list")
	require.Equal(t, 0, code)
	assert.Contains(t, out, " width=8 note=- ")
}

func TestApplyCarriesTheNoteIntoTheViewsTheSprintReads(t *testing.T) {
	t.Parallel()

	h := loopHarness(t)
	for _, args := range [][]string{
		{"route", "add", "r1", "--tier", "flash", "--provider", "p", "--model", "m", "--deadline", "60", "--enabled", "false", "--note", "4 of 52 ok"},
		{"machine", "add", "superman", "--user", "u", "--seat", "s", "--slots", "8", "--width", "8", "--note", "held 1:46 PM"},
	} {
		code, _, errs := h.run(t, args...)
		require.Equal(t, 0, code, errs)
	}
	for _, kind := range []string{"route", "machine"} {
		code, _, errs := h.run(t, "apply", "--kind", kind)
		require.Equal(t, 0, code, errs)
	}
	assert.Equal(t, "4 of 52 ok", h.redis.views["route"]["r1"]["note"], "the applied route carries its reason")
	assert.Equal(t, "held 1:46 PM", h.redis.views["machine"]["superman"]["note"])
}
