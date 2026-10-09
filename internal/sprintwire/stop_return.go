package sprintwire

import (
	"crypto/sha256"
	"fmt"
)

// StopReturnOp keeps retries on the existing immutable caller-operation path
// (SPEC-SPRINT, STOP acknowledgement). The reason is payload checked by the
// receipt, rather than part of identity.
func StopReturnOp(row, card string, gen int, epoch string) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%d:%s/%d:%s/%d/%d:%s", len(row), row, len(card), card, gen, len(epoch), epoch)))
	return fmt.Sprintf("stop-return-%x", h)
}
