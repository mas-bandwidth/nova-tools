//go:build functional

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInboxCarriesSameMessagesWithOrWithoutPublicMarker(t *testing.T) {
	t.Parallel()
	checkout, _ := busDir(t)
	args := []string{"inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "12", "--full"}
	private := invoke(t, "", args...).mustCode(t, 0)
	require.NoError(t, os.WriteFile(filepath.Join(checkout, ".public"), []byte("public\n"), 0600))
	public := invoke(t, "", args...).mustCode(t, 0)
	require.Equal(t, private.stdout, public.stdout, "public marker changed ordinary message listing")
	require.Equal(t, private.stderr, public.stderr, "public marker changed ordinary message listing")
	for _, unexpected := range []string{"INBOX DECIDED", "needs_reply=", "decider=", "conf="} {
		assert.NotContainsf(t, public.stdout, unexpected, "inbox classified notes: %s", unexpected)
	}
	require.Contains(t, public.stdout, "A question about the gate", "ordinary note disappeared")
}
