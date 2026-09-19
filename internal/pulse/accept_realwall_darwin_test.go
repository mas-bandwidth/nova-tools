//go:build darwin

package pulse

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	hyg "github.com/mas-bandwidth/nova-tools/internal/hygiene"
)

// The one end-to-end run under the REAL wall (cold read of f927bccc, MEDIUM 6): the
// installed nova-sandbox, sandbox-exec behind it, the good fix, ACCEPT OK -- and the
// worker's-copy test red, because the wall admits nothing under the swarm root. Skipped
// where no nova-sandbox is installed; on the Studio it is.
func TestAcceptUnderTheRealWallDarwin(t *testing.T) {
	real, err := exec.LookPath("nova-sandbox")
	if err != nil {
		t.Skip("no nova-sandbox on PATH; the real-wall run needs the installed binary")
	}
	if _, err := os.Stat("/usr/bin/sandbox-exec"); err != nil {
		t.Skip("no sandbox-exec on this darwin")
	}
	l := newAcceptLab(t)
	l.goodFix(t)
	card := l.card(t, fixRedHeader)
	var out, errb strings.Builder
	code := Accept(AcceptInput{
		Job: l.job, Card: card, Base: acceptGit(t, l.job, nil, "rev-parse", "main"), Bench: "lab", Cert: l.cert,
		Identities: []hyg.Identity{{Name: "Rowan", Email: "rowan@example.com"}},
		Sandbox:    real, Timeout: 5 * time.Minute, Max: 0, Stdout: &out, Stderr: &errb, Now: time.Now,
	})
	if code != 0 || !strings.Contains(out.String(), "ACCEPT OK ") {
		t.Fatalf("under the real wall: exit %d\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	// The worker's copy is unreachable through the real wall.
	acceptWrite(t, l.job, "sign/sign_test.go", strings.Replace(baseTest, "import \"testing\"", "import (\n\t\"os\"\n\t\"path/filepath\"\n\t\"testing\"\n)", 1)+"\nfunc TestSignZero(t *testing.T) {\n\tif _, err := os.ReadFile(filepath.Join(\"..\", \"..\", \"..\", \"..\", \"jobs\", \"CARD-7\", \"sign\", \"testdata\", \"answer.txt\")); err != nil {\n\t\tt.Fatal(err)\n\t}\n\tif Sign(0) != 0 {\n\t\tt.Fatal(\"zero\")\n\t}\n}\n")
	l.commit(t, "reaches for the worker's copy")
	acceptWrite(t, l.job, "sign/testdata/answer.txt", "42\n")
	out.Reset()
	errb.Reset()
	code = Accept(AcceptInput{
		Job: l.job, Card: card, Base: acceptGit(t, l.job, nil, "rev-parse", "main"), Bench: "lab", Cert: l.cert,
		Identities: []hyg.Identity{{Name: "Rowan", Email: "rowan@example.com"}},
		Sandbox:    real, Timeout: 5 * time.Minute, Max: 0, Stdout: &out, Stderr: &errb, Now: time.Now,
	})
	if code != 1 || !strings.Contains(out.String(), " reason=red-at-head at=TestSignZero ") {
		t.Fatalf("the real wall admitted the worker's copy: exit %d\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if entries, _ := os.ReadDir(filepath.Join(l.slot, "accept")); len(entries) > 1 {
		t.Fatalf("the accept directory holds more than the gate's cache after the run: %d entries", len(entries))
	}
}
