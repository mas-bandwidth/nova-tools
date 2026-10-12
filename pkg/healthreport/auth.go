package healthreport

import (
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

const (
	HeaderID    = "X-Nova-Health-Capability"
	HeaderEpoch = "X-Nova-Health-Epoch"
)

// Capability keeps the separately provisioned secret out of public descriptors,
// events, receipts and formatted diagnostics. It authenticates only Enrollment.
type Capability struct {
	enrollment Enrollment
	secret     [32]byte
}

// NewCapability accepts a canonical unpadded base64url encoding of 32 random
// bytes. Provisioning/randomness is an explicit owner step, not done here.
func NewCapability(e Enrollment, encoded string) (*Capability, error) {
	if len(encoded) != 43 {
		return nil, errors.New("health capability secret is not a canonical 32-byte value")
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(raw) != 32 || base64.RawURLEncoding.EncodeToString(raw) != encoded {
		return nil, errors.New("health capability secret is not a canonical 32-byte value")
	}
	if err := e.Validate(); err != nil {
		return nil, err
	}
	var empty [32]byte
	c := &Capability{enrollment: e}
	copy(c.secret[:], raw)
	if c.secret == empty {
		return nil, errors.New("health capability secret is empty")
	}
	return c, nil
}

func (c *Capability) String() string   { return "health capability (secret redacted)" }
func (c *Capability) GoString() string { return c.String() }

// Principal is a successfully authenticated owner grant. Request bodies cannot
// construct one; its zero value is not authorized. The receiver still checks
// its durable activated epoch at the atomic acceptance boundary.
type Principal struct {
	enrollment Enrollment
	authorized bool
}

// Enrollment returns a copy of the authenticated public grant.
func (p Principal) Enrollment() Enrollment { return p.enrollment }

// ParseEvent refuses an uninitialized principal before binding body claims.
func (p Principal) ParseEvent(raw []byte) (Event, error) {
	if !p.authorized {
		return Event{}, errors.New("health principal is not authorized")
	}
	return ParseEvent(raw, p.enrollment)
}

// Authenticate verifies one unambiguous set of headers. Its return value is
// owner enrollment, never a name selected from the request body. Durable epoch
// activation/replay fences remain mandatory at the receiver's commit boundary.
func (c *Capability) Authenticate(h http.Header) (Principal, error) {
	denied := errors.New("health capability refused")
	if c == nil {
		return Principal{}, denied
	}
	id, epoch, auth := h.Values(HeaderID), h.Values(HeaderEpoch), h.Values("Authorization")
	if len(id) != 1 || len(epoch) != 1 || len(auth) != 1 || id[0] != c.enrollment.ID || epoch[0] != strconv.FormatUint(c.enrollment.Epoch, 10) {
		return Principal{}, denied
	}
	token, ok := strings.CutPrefix(auth[0], "Bearer ")
	if !ok || len(token) != 43 {
		return Principal{}, denied
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(raw) != len(c.secret) || base64.RawURLEncoding.EncodeToString(raw) != token || subtle.ConstantTimeCompare(raw, c.secret[:]) != 1 {
		return Principal{}, denied
	}
	return Principal{enrollment: c.enrollment, authorized: true}, nil
}

func (c *Capability) headers(h http.Header) {
	h.Set(HeaderID, c.enrollment.ID)
	h.Set(HeaderEpoch, strconv.FormatUint(c.enrollment.Epoch, 10))
	h.Set("Authorization", "Bearer "+base64.RawURLEncoding.EncodeToString(c.secret[:]))
}
