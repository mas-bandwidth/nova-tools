package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNovaLocalDescriptionWrite pins the writer switch of the worker description (owner's
// rule S4, writers second): Description.Write writes the schema JSON text form of
// WorkerRecord, never binary or base64 and never the legacy JSON; what it writes reads back
// equal through nova-swarm's dual reader (LoadWorker), counted under the JSON form; the
// legacy fixture of the same record still reads during the switch; and a value the table
// cannot hold is refused by name before a byte is written.
func TestNovaLocalDescriptionWrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	require.NoError(t, os.Mkdir(home, 0o755))
	key := filepath.Join(dir, "local.key")
	require.NoError(t, os.WriteFile(key, []byte("local\n"), 0o600))

	d := Description{Name: "gemma", Provider: "ollama", Model: "gemma4-32k", BaseURL: "http://127.0.0.1:11434/v1",
		EnvVar: "OLLAMA_API_KEY", KeyFile: key, Usage: "opencode", Harness: "opencode",
		HarnessArgs: []string{"run", "--model", "ollama/{model}", "--", "{prompt}"}, WorkerDir: home, Deadline: "20m", Board: "gemma-board"}
	want := swarm.Worker{Name: d.Name, Provider: d.Provider, Model: d.Model, BaseURL: d.BaseURL, EnvVar: d.EnvVar, KeyFile: d.KeyFile,
		Usage: d.Usage, Harness: d.Harness, HarnessArgs: d.HarnessArgs, WorkerDir: d.WorkerDir, Deadline: d.Deadline, Board: d.Board}

	t.Run("the writer writes the schema JSON text and it reads back equal", func(t *testing.T) {
		for _, usage := range []string{"opencode", "none"} {
			d, want := d, want
			d.Usage, want.Usage = usage, usage
			out := filepath.Join(dir, "table-"+usage+".json")
			counts := swarm.SwarmReadCounts()["WORKER_RECORD"]
			jsonBefore := counts.Reads(schemaread.FormJSON)
			require.NoError(t, d.Write(out))
			raw, err := os.ReadFile(out)
			require.NoError(t, err)
			assert.Equal(t, byte('{'), raw[0], "the schema JSON text, never binary or base64")
			assert.Equal(t, byte('\n'), raw[len(raw)-1], "the trailing newline the file has always had")
			assert.Contains(t, string(raw), `"value"`, "harness_args is the table's list of Entry")
			assert.NotContains(t, string(raw), `"usage": "`, "usage is the table's ordinal, never the legacy word")
			w, problems := swarm.LoadWorker(out)
			require.Empty(t, problems)
			assert.Equal(t, want, w)
			assert.Greater(t, counts.Reads(schemaread.FormJSON), jsonBefore, "read under the schema JSON form")
		}
	})

	t.Run("the legacy fixture of the same record still reads", func(t *testing.T) {
		legacy := filepath.Join(dir, "legacy.json")
		require.NoError(t, os.WriteFile(legacy, []byte(`{
  "name": "gemma",
  "provider": "ollama",
  "model": "gemma4-32k",
  "base_url": "http://127.0.0.1:11434/v1",
  "env_var": "OLLAMA_API_KEY",
  "key_file": `+strconv.Quote(key)+`,
  "usage": "opencode",
  "harness": "opencode",
  "harness_args": [
    "run",
    "--model",
    "ollama/{model}",
    "--",
    "{prompt}"
  ],
  "worker_dir": `+strconv.Quote(home)+`,
  "deadline": "20m",
  "board": "gemma-board"
}
`), 0o644))
		w, problems := swarm.LoadWorker(legacy)
		require.Empty(t, problems)
		assert.Equal(t, want, w)
	})

	t.Run("a value the table cannot hold is refused by name before a byte is written", func(t *testing.T) {
		for name, c := range map[string]struct {
			mut           func(*Description)
			reason, field string
		}{
			"a usage the table has no ordinal for": {func(d *Description) { d.Usage = "sometimes" }, schemaread.ReasonClamped, "usage"},
			"no usage":                             {func(d *Description) { d.Usage = "" }, schemaread.ReasonAbsentRequired, "usage"},
			"a NUL in a text":                      {func(d *Description) { d.Model = "gemma\x00" }, schemaread.ReasonMalformed, "model"},
			"text that is not UTF-8":               {func(d *Description) { d.HarnessArgs = []string{"run", "\xff{model}"} }, schemaread.ReasonMalformed, "harness_args"},
		} {
			d := d
			c.mut(&d)
			out := filepath.Join(dir, "refused.json")
			err := d.Write(out)
			var r *schemaread.Refusal
			require.True(t, errors.As(err, &r), "%s: %v", name, err)
			assert.Equal(t, "WORKER_RECORD", r.Name, name)
			assert.Equal(t, c.reason, r.Reason, name)
			assert.Equal(t, c.field, r.Field, name)
			_, statErr := os.Stat(out)
			assert.True(t, errors.Is(statErr, os.ErrNotExist), "%s: nothing written, no legacy fallback", name)
		}
	})
}
