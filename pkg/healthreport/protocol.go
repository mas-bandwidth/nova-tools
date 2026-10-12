// Package healthreport validates a separately enrolled own-friend bus-fault
// capability. It grants no bus or sprint-verb authority. See the reviewed
// CAPABILITY-DESIGN.md in jobs/revoked-bus-alarm-20261012; durable acceptance,
// replay and alarm delivery belong to the receiver, not this package.
package healthreport

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
)

const (
	Path       = "/health/v1/bus-fault"
	MaxEvent   = 4096
	MaxReceipt = 512
)

var word = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
var envName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// Enrollment is public owner configuration. SecretEnv names a variable;
// neither this record nor an Event contains its value.
type Enrollment struct {
	Version     int    `json:"version"`
	ID          string `json:"id"`
	Epoch       uint64 `json:"epoch"`
	Friend      string `json:"friend"`
	Store       string `json:"store"`
	User        string `json:"user"`
	Coordinator string `json:"coordinator"`
	Audience    string `json:"audience"`
	SecretEnv   string `json:"secret_env"`
}

// Validate refuses incomplete or ambiguous grants, without quoting inputs
// that might contain accidentally supplied secrets. Activation/epoch rollback
// fencing is the receiver's durable owner operation, not validation.
func (e Enrollment) Validate() error {
	if e.Version != 1 || e.Epoch == 0 || !word.MatchString(e.ID) {
		return errors.New("invalid health capability version, identifier or epoch")
	}
	if bus.CheckName(e.Friend) != "" || bus.CheckName(e.Coordinator) != "" {
		return errors.New("invalid health capability friend or coordinator")
	}
	if !plain(e.User, 128) || !envName.MatchString(e.SecretEnv) {
		return errors.New("invalid health capability login or secret variable name")
	}
	if !plain(e.Store, 256) {
		return errors.New("invalid health capability store")
	}
	if strings.HasPrefix(e.Store, "/") {
		if filepath.Clean(e.Store) != e.Store {
			return errors.New("invalid health capability store")
		}
	} else {
		host, port, err := net.SplitHostPort(e.Store)
		n, portErr := strconv.Atoi(port)
		if err != nil || host == "" || portErr != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return errors.New("invalid health capability store")
		}
	}
	u, err := url.Parse(e.Audience)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != Path || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || !plain(e.Audience, 512) {
		return errors.New("health capability audience must be an exact HTTPS fault endpoint")
	}
	return nil
}

func plain(s string, max int) bool {
	if len(s) > max || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}

// ParseEnrollment reads a bounded, strict public descriptor.
func ParseEnrollment(raw []byte) (Enrollment, error) {
	var e Enrollment
	if err := decode(raw, MaxEvent, &e); err != nil {
		return e, errors.New("invalid health capability descriptor")
	}
	return e, e.Validate()
}

// Event is one immutable send observation. Sequence/episode survive capability
// rotation. A receiver must bind it to the authenticated Enrollment and commit
// its replay fence and outbox before returning a Receipt.
type Event struct {
	Sequence    uint64    `json:"sequence"`
	Episode     string    `json:"episode"`
	Transition  string    `json:"transition"`
	Friend      string    `json:"friend"`
	Store       string    `json:"store"`
	User        string    `json:"user"`
	Coordinator string    `json:"coordinator"`
	Audience    string    `json:"audience"`
	FailedAt    time.Time `json:"failed_at"`
	Class       string    `json:"class"`
	Reason      string    `json:"reason"`
	RecoveredAt time.Time `json:"recovered_at,omitzero"`
}

// Validate binds all body claims to the grant, not a request-selected identity.
func (v Event) Validate(e Enrollment) error {
	if err := e.Validate(); err != nil {
		return err
	}
	if v.Friend != e.Friend || v.Store != e.Store || v.User != e.User || v.Coordinator != e.Coordinator || v.Audience != e.Audience {
		return errors.New("health event does not match its enrollment")
	}
	if v.Sequence == 0 || !word.MatchString(v.Episode) || v.FailedAt.IsZero() || (v.Class != bus.AlarmAuth && v.Class != bus.AlarmConnection) || v.Reason == "" || !plain(v.Reason, 200) {
		return errors.New("invalid health fault observation")
	}
	switch v.Transition {
	case "failed":
		if !v.RecoveredAt.IsZero() {
			return errors.New("failed health event names a recovery")
		}
	case "recovered":
		if v.RecoveredAt.IsZero() || v.RecoveredAt.Before(v.FailedAt) {
			return errors.New("invalid health recovery time")
		}
	default:
		return errors.New("invalid health transition")
	}
	return nil
}

// ParseEvent preserves the original bytes for digesting; it never canonicalizes
// a retry into a different accepted event. Nested/duplicate/unknown JSON fields
// and extra top-level values cannot select a second meaning.
func ParseEvent(raw []byte, e Enrollment) (Event, error) {
	var v Event
	if err := decode(raw, MaxEvent, &v); err != nil {
		return v, errors.New("invalid health event encoding")
	}
	return v, v.Validate(e)
}

// Receipt proves only durable receiver acceptance, never alarm delivery.
type Receipt struct {
	Sequence uint64 `json:"sequence"`
	Episode  string `json:"episode"`
	Digest   string `json:"digest"`
}

// ReceiptFor is the exact receipt a durable receiver may return after commit.
func ReceiptFor(v Event, raw []byte) Receipt {
	sum := sha256.Sum256(raw)
	return Receipt{Sequence: v.Sequence, Episode: v.Episode, Digest: hex.EncodeToString(sum[:])}
}

func parseReceipt(raw []byte, want Receipt) (Receipt, error) {
	var got Receipt
	if err := decode(raw, MaxReceipt, &got); err != nil || got != want {
		return Receipt{}, errors.New("health receiver answered no matching acceptance receipt")
	}
	return got, nil
}

// decode requires one flat JSON object. DisallowUnknownFields alone does not
// reject duplicate keys, so read field names first before decoding the schema.
func decode(raw []byte, limit int, dst any) error {
	if len(raw) == 0 || len(raw) > limit {
		return errors.New("invalid body size")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	open, err := d.Token()
	if err != nil || open != json.Delim('{') {
		return errors.New("body is not an object")
	}
	seen := map[string]bool{}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return err
		}
		k, ok := key.(string)
		if !ok || seen[k] {
			return errors.New("duplicate or invalid field")
		}
		seen[k] = true
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return err
		}
		if bytes.Equal(value, []byte("null")) || (len(value) > 0 && (value[0] == '{' || value[0] == '[')) {
			return errors.New("field is not a scalar")
		}
	}
	if _, err := d.Token(); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("extra body value")
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(dst)
}
