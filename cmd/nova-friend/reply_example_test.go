package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// replyExampleWorld keeps the inspected local envelopes away from host files,
// bus connections and native processes. A fixture capability grants no send.
func replyExampleWorld(t *testing.T) (world, *friend.MemFS) {
	t.Helper()
	w := newRig(t, "bob").world()
	fs := friend.NewMemFS()
	w.settings = fs
	w.open = func(context.Context, string) (bus.Store, func(), error) {
		require.FailNow(t, "local envelope inspection must not open the bus")
		return nil, nil, nil
	}
	w.exec = func(context.Context, string, string, []string, string) (string, int, error) {
		require.FailNow(t, "local envelope inspection must not execute a native process")
		return "", 0, nil
	}
	return w, fs
}

func TestNativeEnvelopeExamplesMatchWholeTranscript(t *testing.T) {
	t.Parallel()
	for _, command := range []string{
		"nova-friend local-pong --as bob --dir /work/bob --session ses_123 --nonce abc123 --capability aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa --dry-run",
		"nova-friend reply --as bob --dir /work/bob --session ses_123 --re 01M4F39QAT9N7621G6XKC3R9AA --capability aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa --subject 'Review answer' --body 'Reviewed' --dry-run",
	} {
		argv, err := onboarding.SplitShell(command)
		require.NoError(t, err)
		t.Run(argv[1], func(t *testing.T) {
			t.Parallel()
			w, fs := replyExampleWorld(t)
			got := cliOf(w).Do(t, argv[1:]...).Exit(0)
			raw := testkit.ReadFile(t, filepath.Join("..", "..", "docs", "TESTS.md"))
			lines, err := onboarding.FirstRun(raw, "nova-friend")
			require.NoError(t, err)
			steps, err := onboarding.Steps("nova-friend", lines)
			require.NoError(t, err)
			var selected []onboarding.Step
			for _, step := range steps {
				if step.Line == "$ "+command {
					selected = append(selected, step)
				}
			}
			require.Len(t, selected, 1, "the exact local example belongs in the first-run sitting")
			for _, p := range onboarding.CompareTranscript(selected, []onboarding.Result{{Code: got.Code, Stdout: got.Stdout, Stderr: got.Stderr}}, nil) {
				assert.Failf(t, "documented local envelope transcript differs", "docs/TESTS.md: %s", p)
			}
			entry, err := fs.Lstat("/work/bob")
			require.NoError(t, err)
			assert.Equal(t, friend.KindMissing, entry.Kind, "dry-run does not require or create control/profile files")
		})
	}
}
