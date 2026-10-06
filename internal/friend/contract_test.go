package friend

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/card"
)

// The daemon hands a brief by reference as the lane can read it (docs/SPEC-CARD-CONTRACT.md
// section 7): left as it is when the staged checkout holds the contract at its version,
// the text in place of the line when it does not and the daemon holds that version, and
// as it is, unread, when neither does; a brief that names no contract is none. Each says
// the brief's token count as handed.
func TestTheDaemonPrependsTheContractOnlyWhenTheCheckoutCannotBeRead(t *testing.T) {
	t.Parallel()
	const text = "RULES.\nReport what was not done."
	ref := "RESULT: c sha=0123456789ab tier: pro\nREPO: o/r\nBASE: dev\nTEST: ./x TestY\n" + card.ContractLine() + "\n"
	held := func(version string) (string, bool) { return text, version == card.ContractVersion }
	hand := func(t *testing.T, brief string, checkout bool, contract func(string) (string, bool)) (string, int, string) {
		dir := t.TempDir()
		c := Card{ID: "c", Brief: filepath.Join(dir, "inbox", "c~1", "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", "c~1")}
		require.NoError(t, os.MkdirAll(filepath.Dir(c.Brief), 0o755))
		require.NoError(t, os.WriteFile(c.Brief, []byte(brief), 0o644))
		if checkout {
			doc := filepath.Join(JobDir(dir, "c~1"), "repo", filepath.FromSlash(card.ContractPath))
			require.NoError(t, os.MkdirAll(filepath.Dir(doc), 0o755))
			require.NoError(t, os.WriteFile(doc, []byte("# contract\n\n<!-- contract v1 -->\n"+text+"\n<!-- end contract v1 -->\n"), 0o644))
		}
		how, tokens, err := HandBrief(dir, c, contract)
		require.NoError(t, err)
		after, err := os.ReadFile(c.Brief)
		require.NoError(t, err)
		assert.Equal(t, card.Tokens(string(after)), tokens, "the count is of the brief as handed")
		return how, tokens, string(after)
	}

	how, _, after := hand(t, ref, true, held)
	assert.Equal(t, ContractCheckout, how)
	assert.Equal(t, ref, after, "the lane reads the repository first: the brief is left as it is")

	how, _, after = hand(t, ref, false, held)
	assert.Equal(t, ContractPrepended, how)
	assert.Equal(t, card.WithContract(ref, text), after)
	assert.True(t, strings.HasPrefix(after, "RESULT: c sha="), "line 1 says what it said")

	how, _, after = hand(t, strings.Replace(ref, " v1\n", " v9\n", 1), false, held)
	assert.Equal(t, ContractUnread, how, "a version the daemon does not hold is unread")
	assert.Contains(t, after, " v9\n")
	how, _, _ = hand(t, ref, false, nil)
	assert.Equal(t, ContractUnread, how)

	how, _, after = hand(t, "Do the thing.\n", false, held)
	assert.Equal(t, ContractNone, how)
	assert.Equal(t, "Do the thing.\n", after)
}

// A batch session reads the briefs dealt to it straight from the inbox, so the daemon hands
// each dealt brief as a lane's is handed (startDealt): the contract in place of the line
// when the checkout cannot be read, and one record line per card with its token count.
func TestABatchSessionIsHandedTheContractAsALaneIs(t *testing.T) {
	t.Parallel()
	const text = "RULES.\nReport what was not done."
	dir := t.TempDir()
	ref := "RESULT: c sha=0123456789ab tier: pro\nREPO: o/r\nBASE: dev\nTEST: ./x TestY\n" + card.ContractLine() + "\n"
	brief := filepath.Join(dir, "inbox", "c~1", "BRIEF.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(brief), 0o755))
	require.NoError(t, os.WriteFile(brief, []byte(ref), 0o644))
	var records []string
	d := &Daemon{Dir: dir, Record: func(line string) { records = append(records, line) },
		Contract: func(version string) (string, bool) { return text, version == card.ContractVersion }}

	c, ok := dealtCard(dir, "inbox/c~1/BRIEF.md (card c, todo on her row)")
	require.True(t, ok)
	assert.Equal(t, Card{ID: "c", Brief: brief, Outbox: filepath.Join(dir, "outbox", "c~1")}, c)
	d.hand(c, "batch", time.Unix(0, 0))
	after, err := os.ReadFile(brief)
	require.NoError(t, err)
	assert.Equal(t, card.WithContract(ref, text), string(after))
	require.Len(t, records, 1)
	assert.Equal(t, fmt.Sprintf("1970-01-01T00:00:00Z batch: card c handed: brief_tokens=%d contract=%s", card.Tokens(string(after)), ContractPrepended), records[0])

	for _, line := range []string{"", "inbox/c~1/README.md (card c, todo)", "inbox/../BRIEF.md (card c, todo)", "inbox/c~1/BRIEF.md", "inbox/c~1/BRIEF.md (card , todo)"} {
		_, ok := dealtCard(dir, line)
		assert.False(t, ok, "%q names no dealt card", line)
	}
}
