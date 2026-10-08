//go:build slow || functional

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// ISSUE #644, THE TOP FAILURE CLASS OF 2026-09-16: the harness's own permission fence
// auto-rejected paths the CARD was told to use -- its own `../scratch` beside `repo/`, a
// read-only /sys path on a bench with no wall -- the model stopped, and the batch scored
// eight of thirty cards `no-result`, the token for a model that chose to publish nothing.
//
// Two things are proved here, and each is red without its half of the fix: the config the
// job runs under NAMES THE JOB'S OWN DIRECTORIES, and a rejection that still happens is
// REPORTED on the NATIVE OK line instead of vanishing into an absent result.

// externalDirectoryRules reads the external_directory rules out of the config the child saw.
func externalDirectoryRules(t *testing.T, slot string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(slot, "data", ".config", "opencode", "opencode.json"))
	require.NoError(t, err, "every native run writes the harness config the job's fence reads")
	var cfg struct {
		Permission struct {
			External map[string]string `json:"external_directory"`
		} `json:"permission"`
	}
	err = json.Unmarshal(raw, &cfg)
	require.NoError(t, err, "the config the child saw is not readable JSON:\n%s", raw)
	return cfg.Permission.External
}
