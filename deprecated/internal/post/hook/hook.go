// Package hook is the GitHub webhook receiver of nova-tools #2657: one signed
// delivery becomes one entry on the Redis stream ev:github.
//
// The decoding and the stream contract are internal/ghevent's (fields repo,
// kind, number, head, action, at, sender, comment_id); this package adds only
// what that one leaves out: the HTTP surface and the webhook secret. There is
// one GitHub event stream, and this is its one writer.
//
// Every delivery is authenticated before it is read: X-Hub-Signature-256 must
// be sha256=<hex HMAC-SHA256 of the raw body under the secret>, compared in
// constant time. A missing or wrong signature is 401 and writes nothing. A
// handler cannot be built without a secret, so an unauthenticated receiver is
// not a configuration this package can express.
//
// A ping is signature-checked like every other delivery and, once verified, is
// one kind=ping entry (#3177): the ping is how a new hook proves it reaches
// ev:github, so it is never dropped. The issues and workflow_run kinds the org
// hook subscribes to are carried as ghevent decodes them.
//
// Answers: 200 on an entry written (a ping included); 202 on a signed event the
// stream does not carry (GitHub marks the delivery good; nothing is written);
// 400 on a carried event whose payload does not decode; 401 on a bad
// signature; 405 on anything but POST; 413 over GitHub's 25 MB payload cap;
// 503 when Redis refuses the XADD, so GitHub's redeliver has something to redo.
package hook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
)

// MaxBody is GitHub's own cap on a webhook payload (25 MB).
const MaxBody = 25 << 20

// SignatureHeader is the header GitHub signs a delivery in.
const SignatureHeader = "X-Hub-Signature-256"

// Handler receives GitHub webhook deliveries and appends carried ones to
// ev:github.
type Handler struct {
	rdb    *redis.Client
	secret []byte
}

// NewHandler builds a receiver. Both the Redis client and a non-empty secret
// are required.
func NewHandler(rdb *redis.Client, secret []byte) (*Handler, error) {
	if rdb == nil {
		return nil, errors.New("hook: nil redis client")
	}
	if len(secret) == 0 {
		return nil, errors.New("hook: empty webhook secret; refusing to accept unsigned deliveries")
	}
	s := make([]byte, len(secret))
	copy(s, secret)
	return &Handler{rdb: rdb, secret: s}, nil
}

// Verify reports whether header is GitHub's sha256= signature of body under
// secret. The comparison is constant time over the decoded digest.
func Verify(secret, body []byte, header string) bool {
	if len(secret) == 0 {
		return false
	}
	hexSum, ok := strings.CutPrefix(strings.TrimSpace(header), "sha256=")
	if !ok {
		return false
	}
	got, err := hex.DecodeString(hexSum)
	if err != nil || len(got) != sha256.Size {
		return false
	}
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return hmac.Equal(got, m.Sum(nil))
}

// ServeHTTP authenticates, decodes and appends one delivery.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		answer(w, http.StatusMethodNotAllowed, "HOOK REFUSED reason=method want=POST")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			answer(w, http.StatusRequestEntityTooLarge, "HOOK REFUSED reason=too-large")
			return
		}
		answer(w, http.StatusBadRequest, "HOOK REFUSED reason=read")
		return
	}
	if !Verify(h.secret, body, r.Header.Get(SignatureHeader)) {
		answer(w, http.StatusUnauthorized, "HOOK REFUSED reason=signature want="+SignatureHeader+" sha256=<hmac of the body>")
		return
	}
	event := strings.TrimSpace(r.Header.Get("X-GitHub-Event"))
	delivery := field(r.Header.Get("X-GitHub-Delivery"))
	e, err := ghevent.Decode(event, body)
	if errors.Is(err, ghevent.ErrNotCarried) {
		answer(w, http.StatusAccepted, "HOOK SKIP kind="+field(event)+" delivery="+delivery)
		return
	}
	if err != nil {
		answer(w, http.StatusBadRequest, "HOOK REFUSED reason=payload kind="+field(event)+" delivery="+delivery)
		return
	}
	id, err := ghevent.Publish(r.Context(), h.rdb, e)
	if err != nil {
		answer(w, http.StatusServiceUnavailable, "HOOK FAILED reason=redis delivery="+delivery)
		return
	}
	if e.Kind == "ping" {
		answer(w, http.StatusOK, fmt.Sprintf("HOOK PONG id=%s repo=%s delivery=%s", id, field(e.Repo), delivery))
		return
	}
	answer(w, http.StatusOK, fmt.Sprintf("HOOK OK id=%s kind=%s repo=%s number=%s action=%s delivery=%s",
		id, field(e.Kind), field(e.Repo), field(e.Number), field(e.Action), delivery))
}

func answer(w http.ResponseWriter, code int, line string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(code)
	_, _ = io.WriteString(w, line+"\n")
}

// field keeps a header or payload value on one token of the answer line.
func field(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "-"
	}
	return oneline.Cap(oneline.Field(s), 128)
}
