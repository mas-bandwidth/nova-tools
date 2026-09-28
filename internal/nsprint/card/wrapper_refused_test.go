package card_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// The refusing fake's own switches (the wrapper strips NOVA_CARD_* from the
// harness's environment, so they carry names of their own).
const (
	fakeRefusalEnv = "WRAPPER_FAKE_REFUSAL" // refuse: the reason the fake native prints
	fakePoolEnv    = "WRAPPER_FAKE_POOL"    // refuse-identity: a pool root with no identity.tsv
)

// fakeRefusal is the bench harness with a native that refuses: native's
// stderr is kept at out/native.err, as the fleet's harness keeps it, and the
// harness exits with native's 2. refuse prints `NATIVE REFUSED: <reason>`;
// refuse-identity runs the real pool identity check against a pool root with
// no identity.tsv and prints its refusal exactly as native does.
func fakeRefusal(mode string) int {
	reason := os.Getenv(fakeRefusalEnv)
	if mode == "refuse-identity" {
		_, err := swarm.LoadPoolIdentity(os.Getenv(fakePoolEnv))
		if err == nil {
			fmt.Println("fake harness: the pool has an identity; nothing to refuse")
			return 9
		}
		reason = err.Error()
	}
	out := os.Getenv("NOVA_CARD_OUT")
	line := "NATIVE REFUSED: " + oneline.Escape(reason) + "\n"
	if err := os.WriteFile(filepath.Join(out, "native.err"), []byte(line), 0o644); err != nil {
		fmt.Println("fake harness:", err)
		return 9
	}
	fmt.Println("2026-09-24T00:00:00Z END native rc=2 wall_s=0")
	return 2
}

// TestNativeRefusalReadsTheLineFromTheMark holds NativeRefusal to its
// sources: a stamped log line is read from the mark on, a job with only other
// output holds no refusal, and a later source is read when the first is silent.
func TestNativeRefusalReadsTheLineFromTheMark(t *testing.T) {
	t.Parallel()

	job := t.TempDir()
	if err := os.MkdirAll(filepath.Join(job, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(job, filepath.FromSlash(rel)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("harness.log", "START\nEND native rc=1\n")
	write("out/native.out", "NATIVE INCOMPLETE nothing\n")
	if line, ok := card.NativeRefusal(job); ok {
		t.Fatalf("no refusal was printed, got %q", line)
	}
	write("out/harness.log", "2026-09-24T00:00:00Z NATIVE REFUSED: pool /p has no identity row  \n")
	if line, ok := card.NativeRefusal(job); !ok || line != "NATIVE REFUSED: pool /p has no identity row" {
		t.Fatalf("got %q %t, want the line from the mark, trimmed", line, ok)
	}
}

// TestHarnessRefusalIsReadFromItsOwnLogOnly is #4234's evidence rule on the
// harness's own refusals: the bench harness's stamped `REFUSED <why> for
// <card>` in out/harness.log and the in-process receipt `REFUSED card run ...`
// in harness.log are refusal lines, read from the mark on; the same word in
// native's output, which may be the model's, is not.
func TestHarnessRefusalIsReadFromItsOwnLogOnly(t *testing.T) {
	t.Parallel()

	job := t.TempDir()
	if err := os.MkdirAll(filepath.Join(job, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(job, filepath.FromSlash(rel)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("harness.log", "START\nEND native rc=2\n")
	write("out/native.out", "the tool REFUSED the edit; trying again\nNATIVE INCOMPLETE nothing\n")
	write("out/native.err", "warning: REFUSED is a word\n")
	if line, ok := card.NativeRefusal(job); ok {
		t.Fatalf("native's output is not the harness's refusal, got %q", line)
	}
	const bench = "REFUSED no payload_sha on s:copies:card:quack-001-c1 for copies/quack-001-c1/1"
	write("out/harness.log", "2026-09-26T13:18:07Z "+bench+"\n")
	if line, ok := card.NativeRefusal(job); !ok || line != bench {
		t.Fatalf("got %q %t, want the bench harness's line from the mark", line, ok)
	}
	const receipt = "REFUSED card run copies/quack-001-c1/1 code=2 why=\"no copy task:quack-001~1\""
	write("harness.log", receipt+"\n")
	if line, ok := card.NativeRefusal(job); !ok || line != receipt {
		t.Fatalf("got %q %t, want the in-process receipt first", line, ok)
	}
	write("harness.log", "2026-09-26T13:18:07Z START copies/quack-001-c1/1 bench=b: nothing REFUSED here\n")
	write("out/harness.log", "")
	if line, ok := card.NativeRefusal(job); ok {
		t.Fatalf("a mark mid-line after words is not a refusal, got %q", line)
	}
}
