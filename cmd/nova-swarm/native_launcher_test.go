package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
