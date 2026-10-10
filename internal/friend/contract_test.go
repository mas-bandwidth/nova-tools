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
	"github.com/mas-bandwidth/nova-tools/internal/cardgen"
)

// briefByRef is a brief by reference: its header lines and the Contract: line last.
const briefByRef = "RESULT: c sha=0123456789ab tier: pro\nREPO: o/r\nBASE: dev\nTEST: ./x TestY\nContract: docs/SPEC-CARD-CONTRACT.md v1\n"

// inboxCard writes brief as the inbox's c~1 job under a new directory and, with checkout,
// a staged checkout whose contract file holds v1.
func inboxCard(t *testing.T, brief string, checkout bool) (string, Card) {
	t.Helper()
	dir := t.TempDir()
	c := Card{ID: "c", Brief: filepath.Join(dir, "inbox", "c~1", "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", "c~1")}
	require.NoError(t, os.MkdirAll(filepath.Dir(c.Brief), 0o755))
	require.NoError(t, os.WriteFile(c.Brief, []byte(brief), 0o644))
	if checkout {
		doc := filepath.Join(JobDir(dir, "c~1"), "repo", filepath.FromSlash(cardgen.ContractPath))
		require.NoError(t, os.MkdirAll(filepath.Dir(doc), 0o755))
		require.NoError(t, os.WriteFile(doc, []byte("# contract\n\n<!-- contract v1 -->\nRULES.\nReport what was not done.\n<!-- end contract v1 -->\n"), 0o644))
	}
	return dir, c
}

// The daemon prepends the contract only when the harness cannot read the repository first
// (docs/SPEC-CARD-CONTRACT.md section 7): a brief by reference is left as it is when the
// staged checkout holds the contract at its version, given the text in place of the line
// when it does not and the daemon holds that version, and handed as it is, unread, when
// neither does; a brief that names no contract is none. Each says the brief's token count
// as handed.
func TestTheDaemonPrependsTheContractOnlyWhenTheCheckoutCannotBeRead(t *testing.T) {
	t.Parallel()
	const text = "RULES.\nReport what was not done."
	held := func(version string) (string, bool) { return text, version == "v1" }
	cases := []struct {
		name     string
		brief    string
		checkout bool
		mismatch bool
		contract func(string) (string, bool)
		how      string
		after    string
	}{
		{"the checkout holds it: left as it is", briefByRef, true, false, held, ContractCheckout, briefByRef},
		{"same version but truncated checkout block: held text is prepended", briefByRef, true, true, held, ContractPrepended, cardgen.WithContract(briefByRef, text)},
		{"no checkout: the text in place of the line", briefByRef, false, false, held, ContractPrepended, cardgen.WithContract(briefByRef, text)},
		{"a version the daemon does not hold: unread", strings.Replace(briefByRef, " v1\n", " v9\n", 1), false, false, held, ContractUnread, strings.Replace(briefByRef, " v1\n", " v9\n", 1)},
		{"a version the checkout does not hold is prepended", strings.Replace(briefByRef, " v1\n", " v9\n", 1), true, false, func(string) (string, bool) { return text, true }, ContractPrepended, cardgen.WithContract(strings.Replace(briefByRef, " v1\n", " v9\n", 1), text)},
		{"a checkout cannot be validated without held text", briefByRef, true, false, nil, ContractUnread, briefByRef},
		{"no text in hand: unread", briefByRef, false, false, nil, ContractUnread, briefByRef},
		{"no reference: none", "Do the thing.\n", false, false, held, ContractNone, "Do the thing.\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir, c := inboxCard(t, tc.brief, tc.checkout)
			if tc.mismatch {
				doc := filepath.Join(JobDir(dir, "c~1"), "repo", filepath.FromSlash(cardgen.ContractPath))
				require.NoError(t, os.WriteFile(doc, []byte("<!-- contract v1 -->\nRULES.\n<!-- end contract v1 -->\n"), 0o644))
			}
			how, tokens, err := HandBrief(dir, c, tc.contract)
			require.NoError(t, err)
			after, err := os.ReadFile(c.Brief)
			require.NoError(t, err)
			assert.Equal(t, tc.how, how)
			assert.Equal(t, tc.after, string(after))
			assert.Equal(t, card.Tokens(string(after)), tokens, "the count is of the brief as handed")
			assert.Equal(t, strings.SplitN(tc.brief, "\n", 2)[0], strings.SplitN(string(after), "\n", 2)[0], "line 1 says what it said")
		})
	}
}

// The daemon is given the contract text: nothing set, it holds this build's copy of the
// versioned block (cardgen.HeldContract), so a lane whose checkout cannot be read is handed
// the requested version whole. A batch session, which reads its briefs straight from the inbox, is handed each
// dealt brief as a lane's is, one record line per card with its token count.
func TestTheDaemonHoldsTheVersionedBlockWhenNothingElseSetsIt(t *testing.T) {
	t.Parallel()
	for _, version := range []string{"v1", cardgen.ContractVersion} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			brief := strings.Replace(briefByRef, " v1\n", " "+version+"\n", 1)
			dir, c := inboxCard(t, brief, false)
			var records []string
			d := &Daemon{Dir: dir, Record: func(line string) { records = append(records, line) }}
			held, ok := cardgen.HeldContract(version)
			require.True(t, ok)

			got, ok := dealtCard(dir, "inbox/c~1/BRIEF.md (card c, todo on her row)")
			require.True(t, ok)
			assert.Equal(t, c, got)
			d.hand(got, "batch", time.Unix(0, 0))
			after, err := os.ReadFile(c.Brief)
			require.NoError(t, err)
			assert.Equal(t, cardgen.WithContract(brief, held), string(after), "the held block, in place of the line")
			assert.Contains(t, string(after), "\nRULES.\n")
			require.Len(t, records, 1)
			assert.Equal(t, fmt.Sprintf("1970-01-01T00:00:00Z batch: card c handed: brief_tokens=%d contract=%s", card.Tokens(string(after)), ContractPrepended), records[0])

			for _, line := range []string{"", "inbox/c~1/README.md (card c, todo)", "inbox/../BRIEF.md (card c, todo)", "inbox/c~1/BRIEF.md", "inbox/c~1/BRIEF.md (card , todo)"} {
				_, ok := dealtCard(dir, line)
				assert.False(t, ok, "%q names no dealt card", line)
			}
		})
	}
}
