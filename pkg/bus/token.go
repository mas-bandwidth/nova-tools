package bus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// A send's token (SPEC-BUS.md, a-lost-send-response-is-safe-to-retry.w1):
// the caller's word for one logical send, the same on every retry of it. The
// send writes a record of it at SentOf(from, token) in the transaction that
// writes the message, and a send that finds the record writes nothing and
// answers the message the record names, so a write that committed and whose
// response was lost is retried without a second message. The record holds the
// fingerprint of the send's arguments: the same token with other arguments is
// refused. The model is tla/BusSendOnce.tla.

// SentPrefix is the record of a send's token: one string key per sender and
// token, its value the record's JSON, expiring at the token's cleanup.
const SentPrefix = "bus2:sent:"

// SentOf is the key of the record of from's token.
func SentOf(from, token string) string { return SentPrefix + from + ":" + token }

// MaxToken is the bytes of a token at most.
const MaxToken = 128

// The token's settings, when the Bus names none (Bus.TokenLife, TokenCleanup).
const (
	// DefaultTokenLife is how long a retry under a token answers the
	// original: a day, longer than any sender's retry loop runs.
	DefaultTokenLife = 24 * time.Hour
	// DefaultTokenCleanup is when the store drops the record (its key
	// expires): a week. Between the life and the cleanup a retry is refused,
	// naming the message that went, never sent again.
	DefaultTokenCleanup = 7 * 24 * time.Hour
)

var tokenRe = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)

// CheckToken says why s is no token, "" when it is one; the empty token is
// a send with none.
func CheckToken(s string) string {
	switch {
	case s == "":
		return ""
	case len(s) > MaxToken:
		return fmt.Sprintf("the token is %d bytes, at most %d", len(s), MaxToken)
	case !tokenRe.MatchString(s):
		return fmt.Sprintf("the token %q is not letters, digits, '.', '_', ':' and '-'", s)
	}
	return ""
}

func (b *Bus) tokenLife() time.Duration {
	if b.TokenLife > 0 {
		return b.TokenLife
	}
	return DefaultTokenLife
}

// tokenCleanup is never before the life ends: a record is kept as long as a
// retry under it is honoured.
func (b *Bus) tokenCleanup() time.Duration {
	c := b.TokenCleanup
	if c <= 0 {
		c = DefaultTokenCleanup
	}
	return max(c, b.tokenLife())
}

// sentRecord is what the store keeps of a send under a token.
type sentRecord struct {
	Fingerprint string    `json:"fingerprint"`
	ID          string    `json:"id"`
	At          time.Time `json:"at"`
}

// fingerprint is the send's arguments as check normalised them (recipients
// sorted and once each, the kind spelled out), so a retry that names them in
// another order is the same send. Each field is length-prefixed: no two
// argument lists have one encoding.
func fingerprint(m Message) string {
	h := sha256.New()
	for _, f := range []string{m.From, strings.Join(m.To, ","), strings.Join(m.CC, ","), m.Subject, m.Re, m.KindName(), m.Body} {
		h.Write([]byte(strconv.Itoa(len(f)) + ":" + f + ";"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// sendOnce writes m under its token, or answers the message an earlier send
// under it wrote. now is the store's time the send was checked at.
func (b *Bus) sendOnce(ctx context.Context, m Message, now time.Time, streams []string, marks []Mark) (Message, error) {
	fp := fingerprint(m)
	raw, err := json.Marshal(sentRecord{Fingerprint: fp, ID: m.ID, At: m.At})
	if err != nil {
		return Message{}, err
	}
	prior, found, err := b.Store.AddOnce(ctx, SentOf(m.From, m.Token), string(raw), b.tokenCleanup(), streams, m.Fields(), marks...)
	if err != nil {
		return Message{}, err
	}
	if !found {
		return m, nil
	}
	return b.replay(m, fp, prior, now)
}

// replay is the answer to a send whose token has a record: the message it
// names when the arguments are the same and the token is inside its life,
// else a refusal saying which message went.
func (b *Bus) replay(m Message, fp, prior string, now time.Time) (Message, error) {
	var rec sentRecord
	if err := json.Unmarshal([]byte(prior), &rec); err != nil || rec.ID == "" {
		return Message{}, &Refusal{[]string{fmt.Sprintf("the token %q has a record that is not one (%s holds %q); a new message wants a new token", m.Token, SentOf(m.From, m.Token), prior)}}
	}
	at := rec.At.UTC().Format(time.RFC3339)
	if rec.Fingerprint != fp {
		return Message{}, &Refusal{[]string{fmt.Sprintf("the token %q already sent %s at %s with other arguments; a retry repeats them exactly, and a new message wants a new token", m.Token, rec.ID, at)}}
	}
	if now.Sub(rec.At) >= b.tokenLife() { // honoured while now < at + life: the record outlives it (tla/BusSendOnce.tla Step)
		return Message{}, &Refusal{[]string{fmt.Sprintf("the token %q sent %s at %s, past its life of %s: the message went, and is not sent again; a new message wants a new token", m.Token, rec.ID, at, b.tokenLife())}}
	}
	m.ID, m.At = rec.ID, rec.At.UTC()
	return m, nil
}
