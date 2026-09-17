// Package post is the outbound engine behind nova-post: it renders one payload
// per channel deterministically, stores it under the drafts directory, and
// releases it only through the approve gate documented in docs/SPEC-OUTBOUND.md.
//
// Nothing here reaches the network at draft time. A credential arrives only
// through the environment (nova-secrets exec), is read by name at send time,
// and is never printed, quoted or measured.
package post

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Channel is one of the four outward channels this tool speaks.
type Channel string

const (
	Ghost   Channel = "ghost"
	Bsky    Channel = "bsky"
	Email   Channel = "email"
	Discord Channel = "discord"
)

// Mailer delivers one email payload. It is an interface so tests can record the
// exact bytes without opening a socket.
type Mailer interface {
	Send(to string, payload []byte) error
}

// Transport carries the seams a test replaces: the HTTP client, the per-channel
// base URLs, and the mailer. The zero value plus DefaultTransport is production.
type Transport struct {
	HTTP      *http.Client
	GhostBase string
	BskyBase  string
	Mail      Mailer
}

// DefaultTransport is the production transport.
func DefaultTransport() Transport {
	return Transport{HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// Options is one verb's whole input. Every path comes from a flag.
type Options struct {
	Channel   Channel
	Target    string
	Draft     string
	Drafts    string
	Allowlist string
	Bus       string
	Approval  string
	File      string
	Title     string
	Link      string
	Digest    string
	Cairn     string
	Fleet     string
	Now       time.Time
	Transport Transport
}

// Meta is the one-line sidecar written beside each payload.
type Meta struct {
	Channel string `json:"channel"`
	Target  string `json:"target"`
	Title   string `json:"title"`
	Link    string `json:"link"`
	Created string `json:"created"`
	Bytes   int    `json:"bytes"`
}

// Refusal is a one-line refusal with its remedy and exit code: 2 when the
// invocation could not run, 1 when the gate or a provider said NO.
type Refusal struct {
	Reason string
	Remedy string
	Exit   int
}

func (r *Refusal) Error() string { return r.Reason + "; " + r.Remedy }

// Refuse builds a refusal.
func Refuse(reason, remedy string, exit int) error {
	return &Refusal{Reason: reason, Remedy: remedy, Exit: exit}
}

// ExitCode is the exit a caller owes an error: 2 for a refusal that does not
// name one, which is the "could not run" default.
func ExitCode(err error) int {
	var r *Refusal
	if errors.As(err, &r) {
		return r.Exit
	}
	return 2
}

// ParseChannel validates the --channel value.
func ParseChannel(s string) (Channel, error) {
	c := Channel(s)
	switch c {
	case Ghost, Bsky, Email, Discord:
		return c, nil
	}
	return "", Refuse("bad-channel", fmt.Sprintf("--channel %q is not ghost, bsky, email or discord; give one of the four", oneline.Field(s)), 2)
}

// CredentialName is the environment variable a channel needs, by name only.
func CredentialName(c Channel, target string) (string, error) {
	switch c {
	case Ghost:
		return "GHOST_ADMIN_KEY", nil
	case Bsky:
		return "BSKY_APP_PASSWORD", nil
	case Email:
		return "SMTP_PASSWORD", nil
	case Discord:
		switch target {
		case "fleet":
			return "DISCORD_FLEET_WEBHOOK", nil
		case "friends":
			return "DISCORD_FRIENDS_WEBHOOK", nil
		}
		return "", Refuse("bad-target", fmt.Sprintf("--target %q for discord is not fleet or friends; give one of the two", oneline.Field(target)), 2)
	}
	return "", Refuse("bad-channel", "give --channel ghost, bsky, email or discord", 2)
}

// DraftResult is what draft wrote.
type DraftResult struct {
	Hash    string
	Meta    Meta
	Payload []byte
}

// ShowResult is what show read.
type ShowResult struct {
	Payload []byte
	Meta    Meta
}

// SendResult is the one line send printed.
type SendResult struct {
	Line string
}

// validHash is the payload hash's shape: sixty-four lower-case hex digits,
// which also keeps a caller-supplied draft name from reaching outside the store.
func validHash(h string) bool {
	if len(h) != 64 {
		return false
	}
	for _, r := range h {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			continue
		}
		return false
	}
	return true
}

// EnsureDrafts refuses a --drafts that is missing or is not a directory: the
// tool creates no directory and guesses none.
func EnsureDrafts(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return Refuse("bad-drafts", fmt.Sprintf("--drafts %s is not a directory; create it first, refusing to guess a store", oneline.Escape(dir)), 2)
	}
	if !info.IsDir() {
		return Refuse("bad-drafts", fmt.Sprintf("--drafts %s is not a directory; create it first, refusing to guess a store", oneline.Escape(dir)), 2)
	}
	return nil
}

// RequireAllowlisted refuses a target the allowlist does not name. It is exit 1:
// the gate said NO, and no socket is opened before it passes.
func RequireAllowlisted(path string, c Channel, target string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Refuse("allowlist-absent", fmt.Sprintf("--allowlist %s is unreadable; create it with a `%s<TAB>%s` line", oneline.Escape(path), c, oneline.Field(target)), 1)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.TrimSpace(parts[0]) == string(c) && strings.TrimSpace(parts[1]) == target {
			return nil
		}
	}
	return Refuse("target-not-allowlisted", fmt.Sprintf("add `%s<TAB>%s` to %s; the allowlist is the finite set this tool may address", c, oneline.Field(target), oneline.Escape(path)), 1)
}

// The shapes a body may not carry. A matched secret is named only by its shape
// and line, never quoted back.
var (
	keyFileRe   = regexp.MustCompile(`[A-Za-z0-9_./-]+\.key`)
	credNames   = []string{"GHOST_ADMIN_KEY", "SMTP_PASSWORD", "SMTP_PASSWORD_BACKUP", "BSKY_APP_PASSWORD", "DISCORD_FLEET_WEBHOOK", "DISCORD_FRIENDS_WEBHOOK", "DISCORD_BOT_TOKEN"}
	secretShape = func(line string) string {
		switch {
		case strings.Contains(line, "recovery.pub"):
			return "recovery-pub"
		case strings.Contains(line, "/secrets/") || strings.Contains(line, "secrets/"):
			return "secret-store-path"
		case keyFileRe.MatchString(line):
			return "key-file"
		}
		for _, name := range credNames {
			if strings.Contains(line, name) {
				return "credential-name"
			}
		}
		return ""
	}
)

// ScanBody refuses a body that names a secret, printing the line number and the
// shape, never the matched text.
func ScanBody(body string) error {
	for i, line := range strings.Split(body, "\n") {
		if shape := secretShape(line); shape != "" {
			return Refuse("body-names-secret", fmt.Sprintf("line=%d shape=%s; remove the secret from the body, a credential reaches this tool only through nova-secrets exec", i+1, oneline.Field(shape)), 2)
		}
	}
	return nil
}

// hashOf is the lower-case SHA-256 of the payload bytes.
func hashOf(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// bskyAltRe matches a markdown image: the alt text is capture 1.
var bskyAltRe = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)

// requireBskyAlt refuses an image with empty alt text for bsky.
func requireBskyAlt(body string) error {
	for _, m := range bskyAltRe.FindAllStringSubmatch(body, -1) {
		if strings.TrimSpace(m[1]) == "" {
			return Refuse("empty-image-alt", "an image in the body has empty alt text; give it alt text, the alt field is how a reader who cannot see the image gets the post", 2)
		}
	}
	return nil
}

// ghostToken builds the Ghost Admin API JWT from an `id:secret` key. The key is
// used and never printed.
func ghostToken(key string, now time.Time) (string, error) {
	id, secret, ok := strings.Cut(key, ":")
	if !ok || id == "" || secret == "" {
		return "", Refuse("bad-credential", "GHOST_ADMIN_KEY is not the Admin API key shape `id:secret`; fix the secret, refusing to guess", 2)
	}
	b64 := func(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
	header := b64([]byte(`{"alg":"HS256","typ":"JWT","kid":"` + id + `"}`))
	claims, err := json.Marshal(map[string]any{
		"iat": now.Unix(),
		"exp": now.Add(5 * time.Minute).Unix(),
		"aud": "/admin/",
	})
	if err != nil {
		return "", err
	}
	signing := b64([]byte(header)) + "." + b64(claims)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signing))
	return signing + "." + b64(mac.Sum(nil)), nil
}

// trimSlash removes a trailing slash from a base URL.
func trimSlash(s string) string { return strings.TrimRight(s, "/") }

// ghostBase is the site the ghost post goes to.
func ghostBase(t Transport, target string) string {
	if t.GhostBase != "" {
		return trimSlash(t.GhostBase)
	}
	if strings.Contains(target, "://") {
		return trimSlash(target)
	}
	return "https://" + trimSlash(target)
}

// bskyBase is the PDS base URL.
func bskyBase(t Transport) string {
	if t.BskyBase != "" {
		return trimSlash(t.BskyBase)
	}
	return "https://bsky.social"
}

// firstLine is the body's first non-empty line, trimmed, for a link card.
func firstLine(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

// heading is the body's first markdown heading, or its first line.
func heading(body string) string {
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "#") {
			return strings.TrimSpace(strings.TrimLeft(t, "#"))
		}
	}
	return firstLine(body)
}
