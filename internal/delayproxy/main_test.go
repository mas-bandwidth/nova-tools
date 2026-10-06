package delayproxy

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the package when a test leaves a goroutine running
// (docs/STANDARD.md section 7).
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
