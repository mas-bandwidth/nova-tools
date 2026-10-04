package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAdapterInputIsOneBoundedTypedValue catches malformed, trailing and
// oversized configuration without invoking a harness or opening a socket.
func TestAdapterInputIsOneBoundedTypedValue(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, body string
		valid      bool
	}{
		{"valid", `{"argv":["bridge","--stdio"]}`, true},
		{"unknown", `{"argv":["bridge"],"shell":"anything"}`, false},
		{"trailing", `{"argv":["bridge"]} {}`, false},
		{"malformed", `{"argv":`, false},
		{"oversized", strings.Repeat(" ", 64*1024+1), false},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "adapter.json")
			require.NoError(t, os.WriteFile(path, []byte(row.body), 0600))
			var cfg friend.CommandConfig
			err := readAdapter(path, &cfg)
			if row.valid {
				require.NoError(t, err)
				assert.Equal(t, []string{"bridge", "--stdio"}, cfg.Argv)
			} else {
				assert.Error(t, err)
			}
		})
	}
}
