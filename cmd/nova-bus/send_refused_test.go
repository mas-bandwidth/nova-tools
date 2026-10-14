package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// neverHookedGate is the gate a name that was never hooked in meets: the hook
// module's Refusal with the never-hooked why and the breadcrumb.
func neverHookedGate() func(context.Context, bus.Store, hook.Want) error {
	return func(context.Context, bus.Store, hook.Want) error {
		return &hook.Refusal{
			Why:  "ada is not hooked in: no hook record (never hooked); nothing on the bus, friend comms or the sprint works until it is",
			Hook: "nova-hook status --as ada",
			Lines: []string{
				"hook in as comms (on the bus, taking no sprint work): nova-hook hook --as ada --role comms",
			},
		}
	}
}

// lapsedGate is the gate a name whose hook record exists but lapsed meets.
func lapsedGate() func(context.Context, bus.Store, hook.Want) error {
	return func(context.Context, bus.Store, hook.Want) error {
		return &hook.Refusal{
			Why:  "ada is not hooked in: unhooked at 2026-10-13T00:00:00Z; nothing on the bus, friend comms or the sprint works until it hooks in again",
			Hook: "nova-hook status --as ada",
		}
	}
}

// unreadableGate is the gate a store whose hook record cannot be read meets:
// it fails closed (pkg/hookgate), refusing at exit 2.
func unreadableGate() func(context.Context, bus.Store, hook.Want) error {
	return func(context.Context, bus.Store, hook.Want) error { return errors.New("LOADING the store is loading") }
}

// sendObject is the one object a send prints on stdout under --json: the
// headline the text form prints under result.line and, on every outcome but
// OK, the code the headline names as the object's reason field
// (docs/SPEC-BUS.md, send's refusal line).
type sendObject struct {
	Result struct {
		Verb   string   `json:"verb"`
		Status string   `json:"status"`
		Exit   int      `json:"exit"`
		Remedy string   `json:"remedy"`
		Why    []string `json:"why"`
		Line   string   `json:"line"`
	} `json:"result"`
	Reason string         `json:"reason"`
	Facts  map[string]any `json:"facts"`
	Notes  []string       `json:"notes"`
}

// oneSendObject is stdout as exactly one JSON object, the one stream a script
// reads to see every outcome of a send (docs/SPEC-BUS.md, send's refusal line).
func oneSendObject(t *testing.T, stdout string) sendObject {
	t.Helper()
	require.True(t, strings.HasSuffix(stdout, "\n"), "stdout ends in one newline: %q", stdout)
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	require.Len(t, lines, 1, "stdout is exactly one JSON object: %q", stdout)
	var o sendObject
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &o), lines[0])
	return o
}

// headlineNamesCode pins the stderr headline of a send that did not end in
// SEND OK: its first line carries reason=<code> before the colon
// (docs/SPEC-BUS.md, send's refusal line), and it is the line result.line
// carries under --json.
func headlineNamesCode(t *testing.T, stderr, code string) string {
	t.Helper()
	require.NotEmpty(t, stderr, "the refusal is on stderr")
	line := strings.SplitN(stderr, "\n", 2)[0]
	assert.Contains(t, line, " reason="+code+":", "the stderr headline names the code %s: %q", code, line)
	return line
}

// TestARefusedSendPrintsSendRefusedOnStdout pins the shape of every send that
// does not end in SEND OK (docs/SPEC-BUS.md, send's refusal line): the
// refusal is the grammar's on stderr, its headline naming its cause as
// reason=<code>, and stdout stays empty, so a loop reading only stdout sees
// nothing false; with --json the one object on stdout is the one stream that
// carries every outcome, result.line holding the headline and reason the code.
func TestARefusedSendPrintsSendRefusedOnStdout(t *testing.T) {
	t.Parallel()

	t.Run("a gate that says never hooked is unhooked at exit 1", func(t *testing.T) {
		t.Parallel()
		r := newRig("ada", "bob")
		r.gate = neverHookedGate()
		res := r.cli().Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "b")
		assert.Equal(t, 1, res.Code, res.String())
		assert.Empty(t, res.Stdout, "stdout stays empty for every non-OK send without --json")
		line := headlineNamesCode(t, res.Stderr, "unhooked")
		assert.True(t, strings.HasPrefix(line, "SEND FAILED reason=unhooked: "), "the gate's FAILED headline keeps its word and exit: %q", line)
		assert.Contains(t, line, "ada is not hooked in: no hook record")
		assert.Contains(t, res.Stderr, "NOTE hook in as comms", "the SEND NOTE remedy stays on stderr")
	})

	t.Run("a gate that says a record lapsed is unhooked", func(t *testing.T) {
		t.Parallel()
		r := newRig("ada", "bob")
		r.gate = lapsedGate()
		res := r.cli().Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "b")
		assert.Equal(t, 1, res.Code, res.String())
		assert.Empty(t, res.Stdout)
		line := headlineNamesCode(t, res.Stderr, "unhooked")
		assert.True(t, strings.HasPrefix(line, "SEND FAILED reason=unhooked: "), "%q", line)
		assert.Contains(t, line, "unhooked at 2026-10-13T00:00:00Z")
	})

	t.Run("a gate whose record cannot be read is the store at exit 2", func(t *testing.T) {
		t.Parallel()
		r := newRig("ada", "bob")
		r.gate = unreadableGate()
		res := r.cli().Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "b")
		assert.Equal(t, 2, res.Code, res.String())
		assert.Empty(t, res.Stdout)
		line := headlineNamesCode(t, res.Stderr, "store")
		assert.True(t, strings.HasPrefix(line, "SEND REFUSED reason=store: the hook gate could not decide"), "%q", line)
	})

	t.Run("a missing --subject is usage at exit 2 in the grammar", func(t *testing.T) {
		t.Parallel()
		r := newRig("ada", "bob")
		res := r.cli().Do(t, "send", "--as", "ada", "--to", "bob", "--body", "b")
		assert.Equal(t, 2, res.Code, res.String())
		assert.Empty(t, res.Stdout)
		line := headlineNamesCode(t, res.Stderr, "usage")
		assert.Contains(t, line, "--subject is required")
		assert.Contains(t, line, "; run: ", "a usage refusal keeps the grammar")
	})

	t.Run("an off-loopback --redis is usage at exit 2", func(t *testing.T) {
		t.Parallel()
		r := newRig("ada", "bob")
		res := r.cli().Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "b", "--redis", "far.test:6381")
		assert.Equal(t, 2, res.Code, res.String())
		assert.Empty(t, res.Stdout)
		line := headlineNamesCode(t, res.Stderr, "usage")
		assert.Contains(t, line, "reaches a store over loopback")
		assert.Equal(t, 0, r.opened, "an address the tool refuses is refused before the dial")
	})

	t.Run("a store that does not answer is the store at exit 2", func(t *testing.T) {
		t.Parallel()
		r := newRig("ada", "bob")
		r.openErr = io.ErrUnexpectedEOF
		res := r.cli().Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "b")
		assert.Equal(t, 2, res.Code, res.String())
		assert.Empty(t, res.Stdout)
		headlineNamesCode(t, res.Stderr, "store")
		assert.Contains(t, res.Stderr, "unexpected EOF")
	})

	t.Run("a dry run that would be refused says so the same way, its line marked (dry run)", func(t *testing.T) {
		t.Parallel()
		r := newRig("ada", "bob")
		res := r.cli().Do(t, "send", "--as", "ada", "--to", "bob", "--body", "b", "--dry-run")
		assert.Equal(t, 2, res.Code, res.String())
		assert.Empty(t, res.Stdout)
		line := headlineNamesCode(t, res.Stderr, "usage")
		assert.True(t, strings.HasSuffix(line, " (dry run)"), "a refused dry run's line is marked: %q", line)
	})

	t.Run("with --json a refusal is one object on stdout naming the code", func(t *testing.T) {
		t.Parallel()
		r := newRig("ada", "bob")
		r.gate = neverHookedGate()
		res := r.cli().Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "b", "--json")
		assert.Equal(t, 1, res.Code, res.String())
		o := oneSendObject(t, res.Stdout)
		assert.Equal(t, "send", o.Result.Verb, "the refusal keeps the verb")
		assert.Equal(t, "failed", o.Result.Status)
		assert.Equal(t, 1, o.Result.Exit)
		assert.Equal(t, "unhooked", o.Reason, "the object carries the code as its reason field")
		assert.True(t, strings.HasPrefix(o.Result.Line, "SEND FAILED reason=unhooked: "), "result.line holds the stderr headline: %q", o.Result.Line)
		assert.Contains(t, o.Result.Line, "ada is not hooked in: no hook record")
		assert.Equal(t, "nova-hook status --as ada", o.Result.Remedy, "the refusal keeps its remedy")
		assert.NotEmpty(t, o.Notes, "the SEND NOTE breadcrumb rides the object as notes")
	})

	t.Run("with --json a usage refusal is one object naming usage", func(t *testing.T) {
		t.Parallel()
		r := newRig("ada", "bob")
		res := r.cli().Do(t, "send", "--as", "ada", "--to", "bob", "--body", "b", "--json")
		assert.Equal(t, 2, res.Code, res.String())
		o := oneSendObject(t, res.Stdout)
		assert.Equal(t, "refused", o.Result.Status)
		assert.Equal(t, 2, o.Result.Exit)
		assert.Equal(t, "usage", o.Reason)
		assert.True(t, strings.HasPrefix(o.Result.Line, "SEND REFUSED reason=usage: --subject is required"), "%q", o.Result.Line)
	})

	t.Run("a success prints SEND OK on stdout and no reason", func(t *testing.T) {
		t.Parallel()
		res := newRig("ada", "bob").cli().Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "b")
		assert.Equal(t, 0, res.Code, res.String())
		assert.Contains(t, res.Stdout, "SEND OK id=")
		assert.NotContains(t, res.Stdout, "REFUSED")
		assert.NotContains(t, res.Stdout, "reason=")
	})

	t.Run("with --json a success is one object with result.line and no reason", func(t *testing.T) {
		t.Parallel()
		r := newRig("ada", "bob")
		res := r.cli().Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "b", "--json")
		assert.Equal(t, 0, res.Code, res.String())
		o := oneSendObject(t, res.Stdout)
		assert.Equal(t, "ok", o.Result.Status)
		assert.Empty(t, o.Reason, "the reason field is absent on OK")
		assert.True(t, strings.HasPrefix(o.Result.Line, "SEND OK id="), "result.line holds the SEND OK headline: %q", o.Result.Line)
		assert.Contains(t, o.Result.Line, "to=bob")
		assert.Equal(t, "bob", o.Facts["to"])
	})

	t.Run("with --json a dry run that would be refused marks its line", func(t *testing.T) {
		t.Parallel()
		r := newRig("ada", "bob")
		res := r.cli().Do(t, "send", "--as", "ada", "--to", "bob", "--body", "b", "--dry-run", "--json")
		assert.Equal(t, 2, res.Code, res.String())
		o := oneSendObject(t, res.Stdout)
		assert.Equal(t, "usage", o.Reason)
		assert.True(t, strings.HasSuffix(o.Result.Line, " (dry run)"), "result.line is the marked line: %q", o.Result.Line)
	})
}

// TestEveryRefusedSendNamesItsCodeOnStderrAndInOneObjectOnStdout is the table
// over every refusal path of send (docs/SPEC-BUS.md, send's refusal line):
// without --json stdout is empty and the stderr headline names a code; with
// --json stdout is exactly one object naming the same code, so both streams
// agree and a script reading --json sees every outcome.
func TestEveryRefusedSendNamesItsCodeOnStderrAndInOneObjectOnStdout(t *testing.T) {
	t.Parallel()
	send := []string{"send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "b"}
	cases := []struct {
		name  string
		code  string
		exit  int
		setup func(*rig)
		args  []string
	}{
		{"never hooked", "unhooked", 1, func(r *rig) { r.gate = neverHookedGate() }, send},
		{"a hook that lapsed", "unhooked", 1, func(r *rig) { r.gate = lapsedGate() }, send},
		{"a record that cannot be read", "store", 2, func(r *rig) { r.gate = unreadableGate() }, send},
		{"a missing --subject", "usage", 2, nil, []string{"send", "--as", "ada", "--to", "bob", "--body", "b"}},
		{"a missing --to", "usage", 2, nil, []string{"send", "--as", "ada", "--subject", "s", "--body", "b"}},
		{"both bodies", "usage", 2, nil, append(append([]string{}, send...), "--stdin")},
		{"no store named", "usage", 2, func(r *rig) { r.env = map[string]string{} }, send},
		{"an off-loopback --redis", "usage", 2, nil, append(append([]string{}, send...), "--redis", "far.test:6381")},
		{"the store does not answer", "store", 2, func(r *rig) { r.openErr = io.ErrUnexpectedEOF }, send},
		{"an unknown recipient", "refused-by-bus", 2, nil,
			[]string{"send", "--as", "ada", "--to", "zed", "--subject", "s", "--body", "b"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newRig("ada", "bob")
			if c.setup != nil {
				c.setup(r)
			}
			res := r.cli().Run(c.args...)
			require.Equal(t, c.exit, res.Code, "%+v", res)
			require.Empty(t, res.Stdout, "stdout stays empty for every non-OK send without --json: %q", res.Stdout)
			headlineNamesCode(t, res.Stderr, c.code)

			r2 := newRig("ada", "bob")
			if c.setup != nil {
				c.setup(r2)
			}
			asJSON := append(append([]string{}, c.args...), "--json")
			res2 := r2.cli().Run(asJSON...)
			require.Equal(t, c.exit, res2.Code, "%+v", res2)
			o := oneSendObject(t, res2.Stdout)
			assert.Equal(t, c.code, o.Reason, "the object names the same code as the stderr headline")
			assert.Contains(t, o.Result.Line, " reason="+c.code+":", "result.line holds the headline: %q", o.Result.Line)
		})
	}
}
