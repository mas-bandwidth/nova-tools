package member

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A one-shot friend's identity prompt goes in front of every card's prompt, a blank line
// between, and the card follows byte for byte; a member with none hands the card as it is.
func TestTheIdentityPromptIsPrependedToTheBrief(t *testing.T) {
	t.Parallel()
	card := "RESULT: s1-1 sha=<head>\nREPO: mas-bandwidth/nova-tools\n\nDo the thing.\n"
	assert.Equal(t, "You are Fay (he/him).\n\n"+card, WithIdentity("You are Fay (he/him).\n", card))
	assert.Equal(t, card, WithIdentity("", card), "no identity: the card as it is")
	assert.Equal(t, card, WithIdentity(" \n\t", card), "an identity of space is none")
}

// The identity prompt file is read once and refused when it cannot carry an identity:
// unreadable, empty, or longer than an identity; no file named is no identity.
func TestReadIdentityPromptRefusesWhatCannotBeAnIdentity(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name, text string) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(text), 0o600))
		return p
	}
	got, err := ReadIdentityPrompt("")
	require.NoError(t, err)
	assert.Empty(t, got)

	got, err = ReadIdentityPrompt(write("fay.md", "\nYou are Fay (he/him).\n\n"))
	require.NoError(t, err)
	assert.Equal(t, "You are Fay (he/him).", got)

	_, err = ReadIdentityPrompt(filepath.Join(dir, "missing.md"))
	assert.ErrorContains(t, err, "readable identity prompt")
	_, err = ReadIdentityPrompt(write("empty.md", " \n"))
	assert.ErrorContains(t, err, "is empty")
	_, err = ReadIdentityPrompt(write("long.md", strings.Repeat("x", MaxIdentityPrompt+1)))
	assert.ErrorContains(t, err, "at most")
}
