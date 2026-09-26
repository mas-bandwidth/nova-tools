// Package card is one nova-sprint card attempt.
//
// The attempt identity is fixed: <sprint>/<label>/<base sha8>/<bench>/<attempt>.
// Reconciliation matches that identity. A branch name is not an identity.
package card

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
)

const (
	// EndRecordName is the only file in a results directory that can end a card.
	EndRecordName = "end.record"
)

var (
	sprintRE   = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)
	labelRE    = regexp.MustCompile(`^[a-z0-9-]{1,80}$`)
	baseRE     = regexp.MustCompile(`^[0-9a-f]{8}$`)
	benchRE    = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)
	tokenSHARe = regexp.MustCompile(`^[0-9a-f]{12}$`)
	pushedRE   = regexp.MustCompile(`^(-|[0-9a-f]{7,64})$`)
)

// Identity is one card attempt. The parts are the whole identity: a second
// attempt, a second cut, or a second bench is a different value.
type Identity struct {
	Sprint  string
	Label   string
	BaseSHA string
	Bench   string
	Attempt int
}

func (id Identity) String() string {
	return fmt.Sprintf("%s/%s/%s/%s/%d", id.Sprint, id.Label, id.BaseSHA, id.Bench, id.Attempt)
}

// ParseIdentity accepts only the canonical form String produces.
func ParseIdentity(s string) (Identity, error) {
	parts := strings.Split(s, "/")
	if len(parts) != 5 {
		return Identity{}, fmt.Errorf("identity %q is not <sprint>/<label>/<base sha8>/<bench>/<attempt>", s)
	}
	attempt, err := strconv.Atoi(parts[4])
	if err != nil || attempt < 1 || strconv.Itoa(attempt) != parts[4] {
		return Identity{}, fmt.Errorf("identity %q has no attempt", s)
	}
	id := Identity{Sprint: parts[0], Label: parts[1], BaseSHA: parts[2], Bench: parts[3], Attempt: attempt}
	if !sprintRE.MatchString(id.Sprint) || !labelRE.MatchString(id.Label) || !baseRE.MatchString(id.BaseSHA) || !benchRE.MatchString(id.Bench) {
		return Identity{}, fmt.Errorf("identity %q is not canonical", s)
	}
	return id, nil
}

func validSprintLabel(sprint, label string) bool {
	return sprintRE.MatchString(sprint) && labelRE.MatchString(label)
}

// TokenSHA is the first 12 hex characters of SHA-256(token). Receipts store
// this and never the token. 128 random bits are the issuer's job; this card
// only compares the token it was given.
func TokenSHA(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:12]
}

func validTokenSHA(s string) bool { return tokenSHARe.MatchString(s) }

func validPushedSHA(s string) bool { return pushedRE.MatchString(s) }

func validAt(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if r <= ' ' || r == '|' {
			return false
		}
	}
	return true
}

// reasonOK is the wrapper's reason table (nova-sprint 3.3) plus done for a
// card that finished. The same pairs are checked again inside card_run.lua.
func reasonOK(outcome, reason string) bool {
	switch reason {
	case "done":
		return outcome == "DONE"
	case "crash", "timeout", "wall", "idle-killed", "tests-red", "refused", "no-commit":
		return outcome == "FAILED"
	case "env", "base-moved", "deps", "spec", "access":
		return outcome == "BLOCKED"
	case "scope":
		return outcome == "ABSTAIN"
	case "other":
		return outcome == "ABSTAIN" || outcome == "BLOCKED" || outcome == "FAILED"
	default:
		return false
	}
}

func CardKey(sprint, label string) string { return "s:" + sprint + ":card:" + label }

func LogKey(sprint string) string { return "s:" + sprint + ":log" }

func IdemKey(sprint string) string { return "s:" + sprint + ":idem" }

func IdxKey(sprint, state string) string { return "s:" + sprint + ":idx:card:" + state }

// BenchWorkingKeyAt is the bench's one lease ledger (#3998): its dealt,
// launched and running sprint cards and its working copies, under epoch e
// (nova-tools#4238; a reader keys by ws.Epoch).

func BenchWorkingKeyAt(e uint64, bench string) string {
	return ws.ConsumerKeyAt(e, "bench:"+bench, "working")
}

func BenchEndedKey(sprint, bench string) string {
	return "s:" + sprint + ":bench:" + bench + ":ended"
}
