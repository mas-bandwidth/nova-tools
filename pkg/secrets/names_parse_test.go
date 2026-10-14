package secrets

// ParseNameLine is the reader of the NAME line Lines prints (ported with it from
// nova-sprint, whose backup scan and doctor probe read a seat through it). Pure:
// strings in, a row out; nothing sleeps, reads the clock, opens a socket or starts
// a child.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseNameLineRoundTripsLines: every NAME line Lines prints reads back as the
// row it came from, so the reader cannot drift from the printer.
func TestParseNameLineRoundTripsLines(t *testing.T) {
	t.Parallel()

	rows := []NameRow{{Name: "API_KEY"}, {Name: "DB_URL", Clear: true}}
	_, lines, _ := NamesReport{As: "rowan", StoreDir: "/store", Rows: rows, Total: 2, Clear: 1, Sealed: 1}.Lines()
	require.Len(t, lines, len(rows))
	for i, line := range lines {
		got, ok := ParseNameLine(line)
		require.True(t, ok, "a NAME line Lines printed does not read back: %q", line)
		assert.Equal(t, rows[i], got)
	}
}

// TestParseNameLineRefusesEveryOtherLine is the reversed witness: the OK and MORE
// lines, a line with a field missing or misnamed, and an empty line are no NAME row.
func TestParseNameLineRefusesEveryOtherLine(t *testing.T) {
	t.Parallel()

	for _, line := range []string{
		"",
		"SECRETS NAMES OK as=rowan keys=1 shown=1 sealed=0 clear=1",
		"SECRETS NAMES MORE kind=key shown=2 total=3 run: nova-secrets names --store /s --as rowan --max 0",
		"SECRETS NAME key=API_KEY",
		"SECRETS NAME name=API_KEY clear=false",
		"SECRETS NAME key=API_KEY sealed=false",
		"SECRET NAME key=API_KEY clear=false",
		"SECRETS NAME key=API_KEY clear=false extra",
	} {
		_, ok := ParseNameLine(line)
		assert.False(t, ok, "%q read as a NAME row", line)
	}
}
