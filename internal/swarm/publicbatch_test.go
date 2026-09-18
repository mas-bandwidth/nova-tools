package swarm

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The batch half of the public-class gate: a public-class worker's listed card
// runs, and its unlisted card is refused with CARD REFUSED and never starts.

func TestBatchPublicGateListedRunsUnlistedRefused(t *testing.T) {
	// The repo probe is pointed at an in-process 200 for every repository, so
	// the network never decides this test: checkRepos admits both cards and
	// the public-class gate is what refuses one of them.
	useProbeTransport(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "public-repos.txt"), []byte("acme/public\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	okCard := writeCard(t, dir, "ok.card", "RESULT: ok\nRESULT line two\n"+forgeClone("acme/public")+" repo\n")
	badCard := writeCard(t, dir, "bad.card", "RESULT: bad\nRESULT line two\n"+forgeClone("acme/secret")+" repo\n")
	tsv := filepath.Join(dir, "cards.tsv")
	tsvBody := "ok\t1\tmodel\t" + okCard + "\nbad\t2\tmodel\t" + badCard + "\n"
	if err := os.WriteFile(tsv, []byte(tsvBody), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := fakeRunner(t, dir)
	var out, errb bytes.Buffer
	code := Batch(BatchInput{
		ID: "B1", Deadline: 20 * time.Second, Cards: tsv, Root: root, Runner: runner,
		Worker: Worker{Name: "muse", Class: "public"},
		Stdout: &out, Stderr: &errb,
	})
	packet, errors := out.String(), errb.String()
	if !strings.Contains(errors, "CARD REFUSED reason=private-source repo=acme/secret class=public worker=muse") {
		t.Fatalf("the unlisted card is refused naming it on stderr:\n%s\npacket:\n%s", errors, packet)
	}
	if !strings.Contains(packet, "ABSTAIN reason=admission reason=private-source") {
		t.Fatalf("the refused card abstains on the packet:\n%s", packet)
	}
	if !strings.Contains(packet, "ok slot=1:") || strings.Contains(packet, "ok slot=1: ABSTAIN") {
		t.Fatalf("the listed card runs:\n%s", packet)
	}
	if code == 0 {
		t.Fatalf("a batch with a refused card does not exit 0:\n%s", packet)
	}
}
