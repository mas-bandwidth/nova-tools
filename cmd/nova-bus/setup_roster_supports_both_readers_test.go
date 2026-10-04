package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Onboarding points 2 and 6: the printed setup creates a roster both readers can use.
func TestStandaloneSetupRosterSupportsBothReaders(t *testing.T) {
	t.Parallel()
	marker := "  printf '%s\\n' '"
	_, tail, ok := strings.Cut(usage, marker)
	require.True(t, ok, "standalone setup must print its roster")
	roster, _, ok := strings.Cut(tail, "' > bus/participants.json")
	require.True(t, ok)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, bus.ConfigName), []byte(roster), 0600))
	config, err := bus.LoadConfig(root)
	require.NoError(t, err)
	assert.Equal(t, []string{"Ada", "Bo"}, config.Senders())
	assert.Less(t, strings.Index(usage, "exit codes:"), strings.Index(usage, "usage:"))
	assert.NotContains(t, usage, `{"name":"Bo"}`)
}
