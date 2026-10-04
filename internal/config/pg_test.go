package config

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOpenPGRefusalCarriesNoPasswordByte pins openPGWithin's refusal of a DSN
// the Postgres parser rejects (docs/SPEC-CONFIG.md: "A refusal never
// reproduces a password from the input"): pgconn's own message runs the
// connection string through a best-effort redactor that malformed input
// defeats -- an unterminated quote beside a space in the password leaves the
// marker's bytes in the quoted text -- so the returned error names the
// parser's error type and carries no byte of the password. No Postgres is
// dialed: the refusal happens before the ping.
func TestOpenPGRefusalCarriesNoPasswordByte(t *testing.T) {
	t.Parallel()

	const password = `'x s3cr3tMARKER`
	_, err := openPGWithin(context.Background(), `host=h user=u password=`+password, time.Second)
	require.Error(t, err, "an unparsable DSN is refused")
	assert.Contains(t, err.Error(), "could not be parsed", "the refusal names the parse failure, not the parser's message")
	assert.NotContains(t, err.Error(), "s3cr3tMARKER", "the refusal never carries the marker")
	assert.NotContains(t, err.Error(), password, "the refusal never carries any run of the password's bytes")
}
