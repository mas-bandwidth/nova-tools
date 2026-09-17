package update

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CARD-8531 nova-tools #182: voluntary tool adoption and upgrade awareness.
// The adoption matrix records each friend's own choice with provenance;
// declined, equivalent and unknown are answers, never failures.
func TestVoluntaryAdoptionMatrix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "adoption.tsv")
	body := "tool\tfriend\tstate\tversion\tdetail\n" +
		"nova-bus\trowan\tadopted\t0.12.1\tfirst-use trial from public docs\n" +
		"nova-wake\trowan\tdeclined\t0.12.0\tdeclines upgrade: pins to stable pair\n" +
		"nova-tokens\trowan\tequivalent\t-\tuses own ledger script instead\n" +
		"nova-swarm\trowan\tunknown\t-\tunknown version: not installed here\n"
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	code := Run("nova-update", []string{"adoption", "--file", path}, "stamp", &out, &errs, Environment{})
	if code != 0 {
		t.Fatalf("adoption with declined/equivalent/unknown = exit %d, want 0 (voluntary: none is failure):\n%s\n%s", code, out.String(), errs.String())
	}
	got := out.String()
	for _, want := range []string{
		"ADOPTION tool=nova-bus friend=rowan state=adopted",
		"ADOPTION tool=nova-wake friend=rowan state=declined",
		"ADOPTION tool=nova-tokens friend=rowan state=equivalent",
		"ADOPTION tool=nova-swarm friend=rowan state=unknown",
		"ADOPTION OK",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("adoption output missing %q:\n%s", want, got)
		}
	}
}
