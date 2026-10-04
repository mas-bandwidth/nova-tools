package main

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// dryField is what separates a dry run's OK line from a real one: the field for a dry run
// and nothing at all for a real one. Both renderings are pinned so a run cannot grow a
// dry_run=true on a real write.
func TestMainCoverDryField(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		dry  bool
		want string
	}{
		{name: "dry run carries the field", dry: true, want: " dry_run=true"},
		{name: "real run carries nothing", dry: false, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, dryField(tc.dry))
		})
	}
}

// sha8 names a commit's two ends on the WAIT ADVANCED line: eight characters for a real
// commit, the whole thing when it is already shorter, and the grammar's "-" for none.
func TestMainCoverSha8(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "full sha is cut to eight", in: "0123456789abcdef0123456789abcdef01234567", want: "01234567"},
		{name: "exactly eight is kept", in: "01234567", want: "01234567"},
		{name: "shorter is left whole", in: "0123456", want: "0123456"},
		{name: "empty is the grammar's dash", in: "", want: "-"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, sha8(tc.in))
		})
	}
}

// printTranscript prints git's own output verbatim below the one escaped event line. It
// prints a carried transcript, and stays silent for an error that carries none.
func TestMainCoverPrintTranscript(t *testing.T) {
	t.Parallel()
	t.Run("a carried transcript is printed verbatim", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		printTranscript(&out, &bus.ConflictError{
			Reason: "SEND FAILED: the rebase conflicted",
			Output: "CONFLICT (content): Merge conflict in from-ada/INDEX\n",
		})
		assert.Equal(t, "CONFLICT (content): Merge conflict in from-ada/INDEX\n", out.String())
	})
	t.Run("an error with no transcript prints nothing", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		printTranscript(&out, errors.New("plain refusal"))
		assert.Empty(t, out.String())
	})
	t.Run("an empty transcript prints nothing", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		printTranscript(&out, &bus.ConflictError{Reason: "refused"})
		assert.Empty(t, out.String())
	})
}
