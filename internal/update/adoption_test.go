package update

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The adoption matrix records each friend's own choice with provenance;
// declined, equivalent and unknown are answers, never failures.
func TestVoluntaryAdoptionMatrix(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "adoption.tsv")
	body := "tool\tfriend\tstate\tversion\tdetail\n" +
		"nova-bus\trowan\tadopted\t0.12.1\tfirst-use trial from public docs\n" +
		"nova-wake\trowan\tdeclined\t0.12.0\tdeclines upgrade: pins to stable pair\n" +
		"nova-tokens\trowan\tequivalent\t-\tuses own ledger script instead\n" +
		"nova-swarm\trowan\tunknown\t-\tunknown version: not installed here\n"
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		require.NoError(t, err)
	}
	var out, errs bytes.Buffer
	code := Run("nova-update", []string{"adoption", "--file", path}, "stamp", &out, &errs, Environment{})
	require.EqualValuesf(t, 0, code, "adoption with declined/equivalent/unknown = exit %d, want 0 (voluntary: none is failure):\n%s\n%s", code, out.String(), errs.String())
	got := out.String()
	for _, want := range []string{
		"ADOPTION CHOICE tool=nova-bus friend=rowan state=adopted",
		"ADOPTION CHOICE tool=nova-wake friend=rowan state=declined version=0.12.0: declines upgrade: pins to stable pair",
		"ADOPTION CHOICE tool=nova-tokens friend=rowan state=equivalent",
		"ADOPTION CHOICE tool=nova-swarm friend=rowan state=unknown",
		"ADOPTION OK entries=4 friends=1",
	} {
		assert.Containsf(t, got, want, "adoption output missing %q:\n%s", want, got)
	}
}
