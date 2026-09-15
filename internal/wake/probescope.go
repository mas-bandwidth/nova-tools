package wake

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
)

// ProbeScope binds a probe record to its transport.
//
// A NAME IS NOT A TRANSPORT (draft 6, K6). A state file copied to a second
// checkout, pointed at a second remote or branch, or run under a second caller
// would otherwise present another transport's ping as this one's -- a PINGED or
// an UNAVAILABLE about a note that was never sent on this lane at all. The
// scope is the first twelve hex characters of SHA-256 over the resolved bus
// path, --remote, --branch, --as and --line joined by NUL, and a call whose own
// scope differs from a standing record's is refused rather than acted on.
func ProbeScope(busDir, remote, branch, as, line string) string {
	resolved := busDir
	if abs, err := filepath.Abs(busDir); err == nil {
		resolved = abs
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{resolved, remote, branch, as, line}, "\x00")))
	return hex.EncodeToString(sum[:])[:12]
}
