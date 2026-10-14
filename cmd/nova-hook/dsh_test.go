package main

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/dshdesktop"
	"github.com/stretchr/testify/assert"
)

var dshArgs = []string{"hook", "--as", "ada", "--role", "comms", "--harness", "deepseek", "--dsh-events-dir", "/private/events", "--dsh-hooks-file", "/private/hooks.json", "--dsh-session", "session-1"}

func fakeDshWorld(r *rig) world {
	w := r.world()
	w.codexLock = func(string) (func() error, error) { return func() error { return nil }, nil }
	w.dshValidate = func(dshdesktop.Route) error { return nil }
	w.dshDeliver = func(dshdesktop.Route, string, string) (string, error) { return "notice", nil }
	return w
}

func TestDshRouteRefusesBeforeArming(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"partial events-dir only", []string{"hook", "--as", "ada", "--role", "comms", "--harness", "deepseek", "--dsh-events-dir", "/d"}},
		{"once", append(append([]string{}, dshArgs...), "--once")},
		{"unknown harness", []string{"hook", "--as", "ada", "--role", "comms", "--harness", "muse", "--dsh-events-dir", "/d", "--dsh-hooks-file", "/d/hooks.json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			w := fakeDshWorld(r)
			var out bytes.Buffer
			code := run(tc.args, strings.NewReader(""), &out, io.Discard, w)
			assert.Equal(t, 2, code, out.String())
			assert.Equal(t, hook.StateNone, r.h.State(t, "ada").State)
		})
	}
}
