package pulse

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// contractSHA12 binds a card's line 1 to every line below it: the literal <sha12> token in
// line 1 is replaced with the sha-12 hex digest of the body, so the contract line cannot
// drift from the steps it names. It is shared by the validated-template cut and `cut --kind`,
// and it is a no-op on a card whose line 1 carries no <sha12> token.
func contractSHA12(card string) string {
	lines := strings.Split(card, "\n")
	body := strings.Join(lines[1:], "\n")
	sum := sha256.Sum256([]byte(body))
	lines[0] = strings.ReplaceAll(lines[0], "<sha12>", hex.EncodeToString(sum[:])[:12])
	return strings.Join(lines, "\n")
}
