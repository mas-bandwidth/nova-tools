package land

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// GID computes the 16-hex gate receipt identity (spec §2.2 / §3.7):
// sha256("kind=single", base, base_sha, required_set_id, policy_id, runner_id)[:16].
func GID(kind, base, baseSHA, requiredSetID, policyID, runnerID string) string {
	raw := fmt.Sprintf("kind=%s,%s,%s,%s,%s,%s", kind, base, baseSHA, requiredSetID, policyID, runnerID)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])[:16]
}

// Key helpers for the unit contract (§2.2).

func UnitKey(sprint, unit string) string {
	return fmt.Sprintf("s:%s:u:%s", sprint, unit)
}

func UnitsSetKey(sprint string) string {
	return fmt.Sprintf("s:%s:units", sprint)
}

func PRUnitKey(sprint, repo string, pr int) string {
	return fmt.Sprintf("s:%s:prunit:%s:%d", sprint, repo, pr)
}

func LandableKey(sprint, repo, base string) string {
	return fmt.Sprintf("s:%s:landable:%s:%s", sprint, repo, base)
}

func ReadKey(sprint, unit, who string) string {
	return fmt.Sprintf("s:%s:read:%s:%s", sprint, unit, who)
}

func HoldKey(sprint, unit, holder string) string {
	return fmt.Sprintf("s:%s:hold:%s:%s", sprint, unit, holder)
}

func PolicyKey(repo, base string) string {
	return fmt.Sprintf("land:%s:%s:policy", repo, base)
}

func CIKey(repo, head, gid string) string {
	return fmt.Sprintf("ci:%s:%s:%s", repo, head, gid)
}

func CITipKey(repo, base, tip, gid string) string {
	return fmt.Sprintf("ci:%s:%s:tip:%s:%s", repo, base, tip, gid)
}

func CIGIDsKey(repo, head string) string {
	return fmt.Sprintf("ci:%s:%s:gids", repo, head)
}

func BatchKey(repo, base, id string) string {
	return fmt.Sprintf("land:%s:%s:batch:%s", repo, base, id)
}

func ChainKey(repo, base string) string {
	return fmt.Sprintf("land:%s:%s:chain", repo, base)
}

func GatesStream(repo string) string {
	return fmt.Sprintf("land:%s:gates", repo)
}

func ReceiptKey(repo, batch string, attempt int) string {
	return fmt.Sprintf("land:%s:receipt:%s:%d", repo, batch, attempt)
}

func TipKey(repo, base string) string {
	return fmt.Sprintf("land:%s:%s:tip", repo, base)
}

func LeaseKey(repo, base string) string {
	return fmt.Sprintf("land:%s:%s:lease", repo, base)
}

func WriterKey(repo, base string) string {
	return fmt.Sprintf("land:%s:%s:writer", repo, base)
}

func PubBatchKey(repo, base, batch string) string {
	return fmt.Sprintf("land:%s:%s:pub:%s", repo, base, batch)
}

func LandedKey(repo, unit, head string) string {
	return fmt.Sprintf("landed:%s:%s:%s", repo, unit, head)
}

func EventsStream(repo string) string {
	return fmt.Sprintf("land:%s:events", repo)
}

func FreezeKey(repo, base string) string {
	return fmt.Sprintf("land:%s:%s:freeze", repo, base)
}

func DropKey(head string, recSeq int64) string {
	h8 := head
	if len(h8) > 8 {
		h8 = h8[:8]
	}
	return fmt.Sprintf("%s:%d", h8, recSeq)
}

// BenchLandKey is the bench's landing record (p99 step times per class, and
// the benched flag the reclaim sweep once set).
func BenchLandKey(bench string) string { return "bench:" + bench + ":land" }
