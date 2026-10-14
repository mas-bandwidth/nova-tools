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

// oneSendRefusedLine is the one line a refused send prints on stdout: stdout
// holds exactly one line, in the form SEND REFUSED reason=<code>: <why>
// (docs/SPEC-BUS.md, send's refusal line).
func oneSendRefusedLine(t *testing.T, stdout, code string) string {
	t.Helper()
	require.True(t, strings.HasSuffix(stdout, "\n"), "stdout ends in one newline: %q", stdout)
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	require.Len(t, lines, 1, "stdout is exactly one line: %q", stdout)
	assert.True(t, strings.HasPrefix(lines[0], "SEND REFUSED reason="+code+": "), "the line names the code %s: %q", code, lines[0])
	return lines[0]
}

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

// TestARefusedSendPrintsSendRefusedOnStdout pins that every send that does not
// end in SEND OK prints exactly one SEND REFUSED reason=<code> line on stdout,
// the stderr headline unchanged, so a loop that reads only stdout sees every
// outcome of a send (docs/SPEC-BUS.md, send's refusal line).
func TestARefusedSendPrintsSendRefusedOnStdout(t *testing.T) {
	t.Parallel()

	t.Run("a gate that says never hooked is unhooked at exit 1", func(t *testing.T) {
		t.Parallel()
		r := newRig("ada", "bob")
		r.gate = neverHookedGate()
		res := r.cli().Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "b")
		assert.Equal(t, 1, res.Code, res.String())
		line := oneSendRefusedLine(t, res.Stdout, "unhooked")
		assert.Contains(t, line, "ada is not hooked in: no hook record")
		assert.Contains(t, res.Stderr, "SEND FAILED reason=unhooked: ada is not hooked in: no hook record", "the stderr headline is unchanged")
		assert.Contains(t, res.Stderr, "NOTE hook in as comms", "the SEND NOTE remedy stays on stderr")
	})

	t.Run("a gate that says a record lapsed is unhooked", func(t *testing.T) {
		t.Parallel()
		r := newRig("ada", "bob")
		r.gate = func(context.Context, bus.Store, hook.Want) error {
			return &hook.Refusal{
				Why:  "ada is not hooked in: unhooked at 2026-10-13T00:00:00Z; nothing on the bus, friend comms or the sprint works until it hooks in again",
				Hook: "nova-hook status --as ada",
			}
		}
		res := r.cli().Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "b")
		assert.Equal(t, 1, res.Code, res.String())
		oneSendRefusedLine(t, res.Stdout, "unhooked")
		assert.Contains(t, res.Stderr, "SEND FAILED reason=unhooked: ada is not hooked in: unhooked at")
	})

	t.Run("a gate whose record cannot be read is the store at exit 2", func(t *testing.T) {
		t.Parallel()
		r := newRig("ada", "bob")
		r.gate = func(context.Context, bus.Store, hook.Want) error { return errors.New("LOADING the store is loading") }
		res := r.cli().Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "b")
		assert.Equal(t, 2, res.Code, res.String())
		oneSendRefusedLine(t, res.Stdout, "store")
		assert.Contains(t, res.Stderr, "SEND REFUSED: the hook gate could not decide")
	})

	t.Run("a missing --subject is usage at exit 2", func(t *testing.T) {
		t.Parallel()
		r := newRig("ada", "bob")
		res := r.cli().Do(t, "send", "--as", "ada", "--to", "bob", "--body", "b")
		assert.Equal(t, 2, res.Code, res.String())
		line := oneSendRefusedLine(t, res.Stdout, "usage")
		assert.Contains(t, line, "--subject is required")
		assert.Contains(t, res.Stderr, "SEND REFUSED: --subject is required")
	})

	t.Run("a missing --redis is usage at exit 2", func(t *testing.T) {
		t.Parallel()
		r := newRig("ada", "bob")
		r.env = map[string]string{}
		res := r.cli().Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "b")
		assert.Equal(t, 2, res.Code, res.String())
		oneSendRefusedLine(t, res.Stdout, "usage")
		assert.Contains(t, res.Stderr, "--redis is required")
	})

	t.Run("a store that does not answer is the store", func(t *testing.T) {
		t.Parallel()
		r := newRig("ada", "bob")
		r.openErr = io.ErrUnexpectedEOF
		res := r.cli().Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "b")
		assert.Equal(t, 2, res.Code, res.String())
		oneSendRefusedLine(t, res.Stdout, "store")
		assert.Contains(t, res.Stderr, "unexpected EOF")
	})

	t.Run("a dry run that would be refused says so", func(t *testing.T) {
		t.Parallel()
		r := newRig("ada", "bob")
		res := r.cli().Do(t, "send", "--as", "ada", "--to", "bob", "--body", "b", "--dry-run")
		assert.Equal(t, 2, res.Code, res.String())
		line := oneSendRefusedLine(t, res.Stdout, "usage")
		assert.True(t, strings.HasSuffix(line, " (dry run)"), "a refused dry run is marked: %q", line)
	})

	t.Run("a success prints SEND OK and no SEND REFUSED", func(t *testing.T) {
		t.Parallel()
		res := newRig("ada", "bob").cli().Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "b")
		assert.Equal(t, 0, res.Code, res.String())
		assert.Contains(t, res.Stdout, "SEND OK id=")
		assert.NotContains(t, res.Stdout, "SEND REFUSED")
	})

	t.Run("--json carries the line in result.line", func(t *testing.T) {
		t.Parallel()
		r := newRig("ada", "bob")
		r.gate = neverHookedGate()
		res := r.cli().Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "b", "--json")
		assert.Equal(t, 1, res.Code, res.String())
		assert.Equal(t, 1, strings.Count(strings.TrimSuffix(res.Stdout, "\n"), "\n")+1, "stdout is one JSON object: %q", res.Stdout)
		var got struct {
			Result struct {
				Verb   string `json:"verb"`
				Status string `json:"status"`
				Exit   int    `json:"exit"`
				Remedy string `json:"remedy"`
				Line   string `json:"line"`
			} `json:"result"`
		}
		require.NoError(t, json.Unmarshal([]byte(res.Stdout), &got), res.Stdout)
		assert.Equal(t, "send", got.Result.Verb, "the refusal keeps the verb")
		assert.Equal(t, 1, got.Result.Exit)
		assert.Equal(t, "failed", got.Result.Status)
		assert.Equal(t, "nova-hook status --as ada", got.Result.Remedy, "the refusal keeps its remedy")
		assert.True(t, strings.HasPrefix(got.Result.Line, "SEND REFUSED reason=unhooked: "), got.Result.Line)
	})
}

// TestEveryRefusedSendPrintsOneStdoutLine is the table over send's refusal
// paths: whenever the exit code is not 0, stdout is exactly one SEND REFUSED
// reason=<code> line, never empty (docs/SPEC-BUS.md, send's refusal line).
func TestEveryRefusedSendPrintsOneStdoutLine(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		code  string
		setup func(*rig)
		args  []string
		exit  int
	}{
		{"never hooked", "unhooked", func(r *rig) { r.gate = neverHookedGate() },
			[]string{"send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "b"}, 1},
		{"record cannot be read", "store", func(r *rig) {
			r.gate = func(context.Context, bus.Store, hook.Want) error { return errors.New("LOADING the store is loading") }
		}, []string{"send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "b"}, 2},
		{"missing --subject", "usage", nil, []string{"send", "--as", "ada", "--to", "bob", "--body", "b"}, 2},
		{"missing --to", "usage", nil, []string{"send", "--as", "ada", "--subject", "s", "--body", "b"}, 2},
		{"both bodies", "usage", nil, []string{"send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "b", "--stdin"}, 2},
		{"no store named", "usage", func(r *rig) { r.env = map[string]string{} },
			[]string{"send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "b"}, 2},
		{"the store does not answer", "store", func(r *rig) { r.openErr = io.ErrUnexpectedEOF },
			[]string{"send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "b"}, 2},
		{"unknown recipient", "refused-by-bus", nil,
			[]string{"send", "--as", "ada", "--to", "zed", "--subject", "s", "--body", "b"}, 2},
		{"unknown flag", "usage", nil,
			[]string{"send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "b", "--nope"}, 2},
		{"invalid flag value", "usage", nil,
			[]string{"send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "b", "--timeout", "xyz"}, 2},
		{"positional argument", "usage", nil,
			[]string{"send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "b", "extra"}, 2},
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
			require.NotEmpty(t, strings.TrimSpace(res.Stdout), "stdout is never empty when the exit is not 0: %v", res)
			oneSendRefusedLine(t, res.Stdout, c.code)
		})
	}
}
