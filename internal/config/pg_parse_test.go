package config

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAnUnparsableDSNNeverEchoesItsPassword pins OpenPG's parse
// refusal (docs/nova-config/README.md, "Connecting"): a DSN the parser
// refuses is named by its defect class, never by the parser's text, whose
// password masking is best effort, so a password with an unterminated quote
// never reaches the error or a line that prints it. The parse fails before
// any connection, so no server is needed.
func TestAnUnparsableDSNNeverEchoesItsPassword(t *testing.T) {
	t.Parallel()

	const password = "s3cr3t f4ke-pw"
	cases := []struct {
		name string
		dsn  string
	}{
		{name: "url form", dsn: "postgres://u:'" + password + "@127.0.0.1:1/db"},
		{name: "keyword form", dsn: "host=127.0.0.1 user=u password='" + password + " dbname=db"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			pg, err := OpenPG(context.Background(), c.dsn)
			require.Error(t, err)
			assert.Nil(t, pg)
			assert.Contains(t, err.Error(), "postgres dsn could not be parsed", "the parse refuses before any connection")
			for _, part := range append(strings.Fields(password), password) {
				assert.NotContains(t, err.Error(), part)
			}
		})
	}
}
