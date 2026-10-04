package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNativeLaunchGoesThroughTheOneLauncher is the call site #2646 left open (the
// hold): a native launch builds its harness argv with swarm.LaunchArgvFor, the providers
// table's one launcher, and the argv the child is handed is exactly the one it returned.
// A provider the table names launches with its own row; one it does not launches with the
// table's declared default row, never a literal argv in the caller. RED WITHOUT THE WIRING:
// native built `run --model <m> --title <l> -- <card>` inline and never asked the table.
func TestNativeLaunchGoesThroughTheOneLauncher(t *testing.T) {
	bin := nativeHarness(t)
	for _, tc := range []struct{ model, row string }{
		{"fake/fake-model", swarm.DefaultLaunchRow},
		{"opencode/deepseek-v4-flash", "opencode"},
	} {
		t.Run(tc.row, func(t *testing.T) {
			root, slot := aSlot(t)
			type call struct {
				provider string
				req      swarm.LaunchRequest
				argv     []string
			}
			var calls []call
			orig := launchArgvFor
			t.Cleanup(func() { launchArgvFor = orig })
			launchArgvFor = func(provider, goos string, req swarm.LaunchRequest) ([]string, error) {
				argv, err := orig(provider, goos, req)
				calls = append(calls, call{provider, req, argv})
				return argv, err
			}

			card := "a card\n"
			var errOut bytes.Buffer
			_, code := nativeRun(nativeRunConfig{
				binary: bin, model: tc.model, label: "launcher-lbl",
				card: []byte(card), slotDir: slot, root: root, deadline: 30 * time.Second, noWall: true,
			}, &errOut)
			require.Equal(t, 0, code, "the run exits 0, got %d:\n%s", code, errOut.String())
			require.Len(t, calls, 1, "the launch did not go through swarm.LaunchArgvFor: %d calls, want 1", len(calls))
			c := calls[0]
			assert.Equal(t, tc.row, c.provider, "the launch asked the table for row %q, want %q", c.provider, tc.row)
			assert.Equal(t, bin, c.req.Harness, "the launch request is %+v, want harness %s, model %s, title launcher-lbl and the card as prompt", c.req, bin, tc.model)
			assert.Equal(t, tc.model, c.req.Model, "the launch request is %+v, want harness %s, model %s, title launcher-lbl and the card as prompt", c.req, bin, tc.model)
			assert.Equal(t, "launcher-lbl", c.req.Title, "the launch request is %+v, want harness %s, model %s, title launcher-lbl and the card as prompt", c.req, bin, tc.model)
			assert.Equal(t, card, c.req.Prompt, "the launch request is %+v, want harness %s, model %s, title launcher-lbl and the card as prompt", c.req, bin, tc.model)
			raw, err := os.ReadFile(filepath.Join(slot, "native-argv.log"))
			require.NoError(t, err, "the run recorded no native-argv.log: %v", err)
			want := "argv: " + oneline.Escape(strings.Join(c.argv, " "))
			assert.Contains(t, string(raw), want+"\n", "the child was not handed the one launcher's argv; want line %q in:\n%s", want, raw)
		})
	}
}

// TestNativeLaunchCarriesTheResultFormat is nova-tools#3651, and #3689: a typed
// card's launch prompt is the card text followed by the RESULT-FORMAT paragraph,
// now the two-line contract (the wrapper writes every other field). RED WITHOUT
// swarm.CardPrompt: the prompt was the card text alone.
func TestNativeLaunchCarriesTheResultFormat(t *testing.T) {
	t.Parallel()

	card := "RESULT: c1 sha=0123456789ab nova-tools fix: a card\nKIND: fix\n"
	argv, err := nativeLaunchArgv("/bin/true", nativeRunConfig{model: "fake/fake-model", label: "fmt-lbl", card: []byte(card)}, "fake")
	require.NoError(t, err)
	prompt := argv[len(argv)-1]
	require.True(t, strings.HasPrefix(prompt, card), "the launch prompt does not carry the RESULT-FORMAT paragraph after the card:\n%s", prompt)
	require.Contains(t, prompt, "RESULT-FORMAT", "the launch prompt does not carry the RESULT-FORMAT paragraph after the card:\n%s", prompt)
	require.Contains(t, prompt, "`BLOCKED <why>`", "the launch prompt does not carry the RESULT-FORMAT paragraph after the card:\n%s", prompt)
	require.NotContains(t, prompt, "BRANCH:", "the launch prompt does not carry the RESULT-FORMAT paragraph after the card:\n%s", prompt)
}

// The argv log is written whole, a secret's value redacted, and a log that cannot be
// written is an error the run names (NATIVE NOTE argv log), never a silent return.
func TestTheArgvLogIsWrittenOrItsErrorReturned(t *testing.T) {
	t.Parallel()
	slot := t.TempDir()
	require.NoError(t, writeNativeArgvLog(slot, "/bin/harness", []string{"run", "a b"}, []string{"PATH=/bin", "API_KEY=sk-never"}))
	raw, err := os.ReadFile(filepath.Join(slot, "native-argv.log"))
	require.NoError(t, err)
	assert.Equal(t, "argv: /bin/harness run a b\nenv: PATH=/bin\nenv: API_KEY=<redacted>\n", string(raw))

	err = writeNativeArgvLog(filepath.Join(slot, "no-such-slot"), "/bin/harness", nil, nil)
	assert.ErrorIs(t, err, fs.ErrNotExist, "a log that cannot be written answered %v", err)
}
