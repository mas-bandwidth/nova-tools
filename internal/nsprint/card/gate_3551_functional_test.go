//go:build functional

package card_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestDoneWhen3551 is the executable DONE-WHEN of #3551: it runs this test
// binary again on exactly the two controls, verbose, and requires checkGate.
func TestDoneWhen3551(t *testing.T) {
	t.Parallel()

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, "-test.v", "-test.count=1", "-test.run", "^("+strings.Join(gate3551, "|")+")$")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("controls exited %v:\n%s", err, out)
	}
	if err := checkGate(string(out), gate3551); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
