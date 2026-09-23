package pulse

import (
	"errors"
	"os/exec"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
)

// acceptWallMark is a line the wall or a missing toolchain prints. A card can print the
// same words; the scorer must not take the words alone as the bench speaking (#1806).
var acceptWallMark = regexp.MustCompile(`SANDBOX REFUSED|SANDBOX DENIED|\bWALL\b|Operation not permitted|[Nn]o space left on device|executable file not found|command not found|cannot find GOROOT|toolchain not available`)

// acceptFirstLine is the first line of a command's output that is not a notice: not
// empty, not go's downloading chatter, not a package header, not a test's own PASS.
func acceptFirstLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		t := strings.TrimSpace(line)
		switch {
		case t == "", strings.HasPrefix(t, "go: downloading"), strings.HasPrefix(t, "go: finding"),
			strings.HasPrefix(t, "go: extracting"), strings.HasPrefix(t, "# "), strings.HasPrefix(t, "=== "),
			strings.HasPrefix(t, "--- PASS"), t == "PASS", strings.HasPrefix(t, "ok "), strings.HasPrefix(t, "?"):
			continue
		}
		return oneline.Cap(t, oneline.TailBytes)
	}
	return "-"
}

// acceptToolchainRed: the red is the bench's when the command could not be started at
// all, or when the wall itself answered (nova-sandbox's exits 125, 126, 127).
//
// cardRuns is true when the failed command ran the card's code (`go test`). Output a
// card's TestMain or init can print is never the bench's voice: a wall marker before the
// first `=== RUN` used to score ABSTAIN toolchain and let a card dodge REJECT (#1806).
func acceptToolchainRed(out string, err error, cardRuns bool) bool {
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		return true
	}
	if ee != nil {
		switch ee.ExitCode() {
		case sandbox.ExitRefused, sandbox.ExitNotExecuted, sandbox.ExitNotFound:
			return true
		}
	}
	prefix := out
	if i := strings.Index(out, "=== RUN"); i >= 0 {
		prefix = out[:i]
	}
	// Compiler text is the card's, whatever words it holds: `undefined: WALL` is a
	// build error naming WALL, not the wall.
	for _, line := range strings.Split(prefix, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "# ") || strings.Contains(t, "[build failed]") || strings.Contains(t, "[setup failed]") {
			return false
		}
	}
	if cardRuns {
		return false
	}
	return acceptWallMark.MatchString(acceptFirstLine(prefix))
}
