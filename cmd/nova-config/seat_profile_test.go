package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSeatFlagCarriesTheDSNAndThePasswordVariable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "nova-config")
	require.NoError(t, os.MkdirAll(cfgDir, 0o700))
	seatsPath := filepath.Join(cfgDir, "seats.tsv")
	seats := "studio\tpostgres://nova_config@127.0.0.1:5432/nova\tNOVA_PG_CONFIG_PASSWORD\n" +
		"bench\tpostgres://nova_config@127.0.0.1:5432/nova\tNOVA_PG_BENCH_PASSWORD\n"
	require.NoError(t, os.WriteFile(seatsPath, []byte(seats), 0o600))

	h := newHarness()
	h.env["XDG_CONFIG_HOME"] = dir
	h.env["NOVA_PG_CONFIG_PASSWORD"] = "secret-pass"
	h.machine(t, "m1", "4")

	// 1. Run a write through the in-memory twin with only --seat given.
	code, out, errs := h.run(t, "machine", "set", "m1", "--width", "8", "--seat", "studio")
	require.Equal(t, 0, code, "write exit code: %d, stdout: %q, stderr: %q", code, out, errs)
	assert.Contains(t, out, "CONFIG SET kind=machine name=m1 rev=2 changed=width", "write stdout: %q", out)
	assert.Empty(t, errs, "write stderr: %q", errs)

	// Assert the write lands in the store.
	row, ok, err := h.store.Get(context.Background(), config.KindMachine, "m1")
	require.NoError(t, err)
	require.True(t, ok, "m1 not found in store")
	assert.Equal(t, "8", row.Fields["width"], "width in store: %q", row.Fields["width"])

	// Assert the history recorded the write under the seat name.
	history, err := h.store.History(context.Background(), config.KindMachine, "m1")
	require.NoError(t, err)
	require.NotEmpty(t, history)
	assert.Equal(t, "studio", history[len(history)-1].Actor)

	// 2. Refusal for an unknown seat names the known ones.
	codeUnknown, outUnknown, errsUnknown := h.run(t, "machine", "set", "m1", "--width", "8", "--seat", "ghost")
	require.Equal(t, 2, codeUnknown, "unknown seat exit code: %d, stderr: %q", codeUnknown, errsUnknown)
	assert.Empty(t, outUnknown, "unknown seat stdout: %q", outUnknown)
	assert.Contains(t, errsUnknown, "unknown seat ghost", "stderr should name unknown seat: %q", errsUnknown)
	assert.Contains(t, errsUnknown, "studio", "stderr should name known seat studio: %q", errsUnknown)
	assert.Contains(t, errsUnknown, "bench", "stderr should name known seat bench: %q", errsUnknown)

	// 3. Exclusivity: --seat and --file are exclusive.
	codeExcl, _, errsExcl := h.run(t, "machine", "set", "m1", "--width", "8", "--seat", "studio", "--file", "try.json")
	require.Equal(t, 2, codeExcl)
	assert.Contains(t, errsExcl, "--seat and --file are exclusive")

	// 4. NOVA_SEAT environment variable is respected when --seat is unset.
	h2 := newHarness()
	h2.env["XDG_CONFIG_HOME"] = dir
	h2.env["NOVA_SEAT"] = "bench"
	h2.env["NOVA_PG_BENCH_PASSWORD"] = "bench-pass"
	h2.machine(t, "m1", "4")
	codeEnv, outEnv, errsEnv := h2.run(t, "machine", "set", "m1", "--width", "16")
	require.Equal(t, 0, codeEnv, "write with NOVA_SEAT: stdout: %q, stderr: %q", outEnv, errsEnv)
	assert.Contains(t, outEnv, "CONFIG SET kind=machine name=m1 rev=2 changed=width")
	hist2, err := h2.store.History(context.Background(), config.KindMachine, "m1")
	require.NoError(t, err)
	require.NotEmpty(t, hist2)
	assert.Equal(t, "bench", hist2[len(hist2)-1].Actor)
}
