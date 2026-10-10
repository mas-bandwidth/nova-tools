package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/stretchr/testify/require"
)

// The `log=` tail of a RUN DONE line is the field a reader is told to trust for the one
// diagnosis of a failed job, and its value is Cap(HarnessTail(jobDir), TailBytes). The
// audit (F5, 2026-09-11) put HarnessTail there to surface a word no verb printed -- the
// new user's `unauthorized` -- which lived in the LAST line of harness.log. The tail must
// be a TAIL: a harness.log led by a SANDBOX OK receipt and other header noise is the real
// shape, and the word that matters is at the END, past the 500-byte prefix Cap would keep
// if it were fed the whole file.
func TestHarnessTailCarriesTheLastWordsIntoTheRunDoneLog(t *testing.T) {
	t.Parallel()

	job := filepath.Join(t.TempDir(), "job")
	require.NoError(t, os.MkdirAll(job, 0o755))

	var header strings.Builder
	for i := 0; i < 40; i++ {
		header.WriteString("SANDBOX OK id=slot-7 status=ready receipt=accepted checksum=0f9e8d7c6b5a49382716\n")
	}
	logBody := header.String() + "401 unauthorized: token missing on this key\n"
	require.NoError(t, os.WriteFile(filepath.Join(job, "harness.log"), []byte(logBody), 0o644))

	tail := HarnessTail(job)
	said := ""
	if tail != "" {
		said = " log=" + oneline.Escape(oneline.Cap(tail, oneline.TailBytes))
	}
	require.Contains(t, said, "unauthorized", "the RUN DONE log= field dropped the last words of harness.log:\n%s", said)
}
