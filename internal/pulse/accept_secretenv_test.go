package pulse

import (
	"context"
	"os"
	"strings"
	"testing"
)

// HOLD on #1721 (johnny-357c06749499): execWalled forwarded GOENV and any
// secret-shaped operator env name straight into the wall. The count is asserted,
// never the value (COMMON rule 3): this fixture carries names, not secrets.
func TestExecWalledDoesNotForwardGOENVOrASecretShapedNameToTheWall(t *testing.T) {
	names := []string{"GOENV", "NOVA_FAKE_API_TOKEN", "NOVA_FAKE_SECRET"}
	for _, n := range names {
		t.Setenv(n, "x")
	}
	present := 0
	for _, n := range names {
		if _, ok := os.LookupEnv(n); ok {
			present++
		}
	}
	if present != len(names) {
		t.Fatalf("fixture is vacuous: %d of %d fixture names are present in the operator env, want %d", present, len(names), len(names))
	}

	g := &acceptGate{sandbox: "/bin/true", home: t.TempDir(), gocache: t.TempDir()}
	cmd := g.execWalled(context.Background(), t.TempDir(), "go", "vet", "./...")

	crossed := 0
	for _, kv := range cmd.Env {
		n, _, _ := strings.Cut(kv, "=")
		for _, want := range names {
			if n == want {
				crossed++
			}
		}
	}
	if crossed != 0 {
		t.Fatalf("%d of the %d fixture names crossed into the wall's env, want 0", crossed, len(names))
	}
}

// The collapse-detector: an ordinary operator env name must still cross, so a fix
// that strips the whole environment cannot pass the test above by accident.
func TestExecWalledStillForwardsAnOrdinaryOperatorEnvNameToTheWall(t *testing.T) {
	t.Setenv("NOVA_FAKE_BENIGN_NAME", "x")
	g := &acceptGate{sandbox: "/bin/true", home: t.TempDir(), gocache: t.TempDir()}
	cmd := g.execWalled(context.Background(), t.TempDir(), "go", "vet", "./...")
	for _, kv := range cmd.Env {
		if n, _, _ := strings.Cut(kv, "="); n == "NOVA_FAKE_BENIGN_NAME" {
			return
		}
	}
	t.Fatalf("an ordinary operator env name did not cross into the wall's env; the filter is over-broad")
}
