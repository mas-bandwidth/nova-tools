package card

import (
	"crypto/sha256"
	"encoding/hex"
)

// Digest is a SHA-256 in 64 lower-case hexadecimal characters: a definition
// digest, a brief digest, a request hash, a receipt identity.
type Digest string

// Valid reports whether the digest is 64 lower-case hexadecimal characters.
func (d Digest) Valid() bool { return len(d) == 64 && isLowerHex(string(d)) }

// Sum is the Digest of b.
func Sum(b []byte) Digest {
	h := sha256.Sum256(b)
	return Digest(hex.EncodeToString(h[:]))
}

// ValidObjectID reports whether s is a git object id or a code head: 40 (SHA-1)
// or 64 (SHA-256) lower-case hexadecimal characters. It is the one rule for a
// commit, an object id and a head, in a card's admission, an event and a piece
// of evidence.
func ValidObjectID(s string) bool { return (len(s) == 40 || len(s) == 64) && isLowerHex(s) }

func isLowerHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return len(s) > 0
}
