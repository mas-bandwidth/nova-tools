package harness

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeMuse writes a fake muse program: it records argv and env, reads the
// prompt file and the key on stdin, and prints recorded JSONL.
func fakeMuse(t *testing.T, dir string) string {
	t.Helper()
	script := `#!/bin/sh
printf '%s\n' "$@" > "$FAKE_MUSE_ARGV"
env > "$FAKE_MUSE_ENV"
cat > "$FAKE_MUSE_KEY"
prev=""
prompt=""
for a in "$@"; do
  if [ "$prev" = "--prompt-file" ]; then prompt="$a"; fi
  prev="$a"
done
cat "$prompt" > "$FAKE_MUSE_PROMPT"
if [ "$FAKE_MUSE_MODE" = "refused" ]; then
  printf '%s\n' '{"type":"error","message":"provider refused: 401 Unauthorized, invalid api key"}'
  exit 3
fi
printf '%s\n' '{"payload_type":"run.lifecycle.started","payload":{"kind":"run_started"}}'
printf '%s\n' '{"type":"result","result":"card done","model":"muse-wonder-1.0","usage":{"input_tokens":10,"cache_creation_input_tokens":2,"cache_read_input_tokens":3,"output_tokens":5,"reasoning_tokens":1}}'
`
	p := filepath.Join(dir, "muse")
	require.NoError(t, os.WriteFile(p, []byte(script), 0o755))
	return p
}

func TestMuseRunsACardHeadless(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	work := filepath.Join(dir, "work")
	require.NoError(t, os.MkdirAll(work, 0o755))
	prompt := filepath.Join(dir, "prompt.txt")
	require.NoError(t, os.WriteFile(prompt, []byte("do the card"), 0o644))
	bin := fakeMuse(t, dir)
	const key = "test-provider-key-secret"
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"FAKE_MUSE_ARGV=" + filepath.Join(dir, "argv"),
		"FAKE_MUSE_ENV=" + filepath.Join(dir, "env"),
		"FAKE_MUSE_KEY=" + filepath.Join(dir, "key"),
		"FAKE_MUSE_PROMPT=" + filepath.Join(dir, "prompt-seen"),
	}
	res, err := RunMuse(context.Background(), bin, work, prompt, "muse-wonder-1.0", key, env)
	require.NoError(t, err)
	assert.Equal(t, "card done", res.Text)
	assert.True(t, res.Usage.Observed)
	assert.Equal(t, map[string]string{
		"tokens_in": "10", "tokens_out": "5", "cache_write": "2", "cache_read": "3", "reasoning": "1",
		"provider": "muse", "model": "muse-wonder-1.0",
	}, res.Usage.Values)

	argv, _ := os.ReadFile(filepath.Join(dir, "argv"))
	assert.Equal(t, "exec\n--prompt-file\n"+prompt+"\n--json\n--model\nmuse-wonder-1.0\n--api-key-stdin\n", string(argv))
	assert.NotContains(t, string(argv), key)
	childEnv, _ := os.ReadFile(filepath.Join(dir, "env"))
	assert.NotContains(t, string(childEnv), key, "key is on stdin only, never the environment")
	gotKey, _ := os.ReadFile(filepath.Join(dir, "key"))
	assert.Equal(t, key, string(gotKey))
	seen, _ := os.ReadFile(filepath.Join(dir, "prompt-seen"))
	assert.Equal(t, "do the card", string(seen))

	// A refused provider maps to the provider-refused failure kind.
	envRefused := append([]string{}, env...)
	envRefused = append(envRefused, "FAKE_MUSE_MODE=refused")
	_, err = RunMuse(context.Background(), bin, work, prompt, "muse-wonder-1.0", key, envRefused)
	require.Error(t, err)
	var merr *MuseError
	require.ErrorAs(t, err, &merr)
	assert.Equal(t, FailureProviderRefused, merr.Kind)
	assert.Equal(t, 3, merr.ExitCode)
	assert.True(t, strings.Contains(merr.Error(), "provider refused"))
}
