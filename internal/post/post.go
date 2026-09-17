// Package post prepares, renders and releases the outward payloads of nova-post.
//
// It is the gate the specification draws: a draft is rendered once by a fixed
// deterministic function and stored verbatim, show writes those exact bytes to stdout,
// and send transmits the STORED payload and nothing freshly rendered. Release is never a
// decision of this package: send refuses unless a bus receipt, received under 24 hours
// ago and sent by Glenn, names the draft's own hash.
//
// Tests drive it against fakes only: the endpoint is caller-supplied, the clock is
// caller-supplied, and the approval is a throwaway bus note. Nothing here opens a socket
// on its own, reads a credential file, or prints any byte of a provider's transcript.
package post

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Channel is one of the four outward interfaces the spec names.
type Channel string

const (
	Ghost   Channel = "ghost"
	Bsky    Channel = "bsky"
	Email   Channel = "email"
	Discord Channel = "discord"
)

// ParseChannel reads the --channel token.
func ParseChannel(s string) (Channel, error) {
	switch Channel(s) {
	case Ghost, Bsky, Email, Discord:
		return Channel(s), nil
	}
	return "", &RefusalError{Code: 2, Reason: "bad-channel", Detail: fmt.Sprintf("unknown channel %q; the channels are ghost, bsky, email, discord", oneline.Field(s))}
}

// Request is the whole input of one render. The caller reads the files; Render is pure
// over their bytes so two renders of the same request are byte-for-byte equal.
type Request struct {
	Channel    Channel
	Target     string
	Body       string
	Title      string
	Link       string
	DigestDate string // YYYY-MM-DD; empty means an ordinary body
	CairnData  []byte
	FleetData  []byte
}

// Draft is the rendered payload and what identifies it.
type Draft struct {
	Hash    string
	Channel Channel
	Target  string
	Title   string
	Link    string
	Created time.Time
	Bytes   []byte
}

// meta is the one-line <hash>.meta: exactly channel, target, title, link, created, bytes.
type meta struct {
	Channel Channel `json:"channel"`
	Target  string  `json:"target"`
	Title   string  `json:"title"`
	Link    string  `json:"link"`
	Created string  `json:"created"`
	Bytes   int     `json:"bytes"`
}

// Render turns one request into the payload for its channel. It performs no network call
// and reads no credential; it is deterministic given the request and the clock.
func Render(req Request, now time.Time) (Draft, error) {
	ch, err := ParseChannel(string(req.Channel))
	if err != nil {
		return Draft{}, err
	}
	req.Channel = ch
	if strings.TrimSpace(req.Target) == "" {
		return Draft{}, &RefusalError{Code: 2, Reason: "no-target", Detail: "--target is required; refusing to guess the address"}
	}
	if ch == Discord && req.Target != "fleet" && req.Target != "friends" {
		return Draft{}, &RefusalError{Code: 2, Reason: "bad-target", Detail: fmt.Sprintf("discord --target is fleet or friends, got %q", oneline.Field(req.Target))}
	}
	body := req.Body
	if req.DigestDate != "" {
		if ch != Email {
			return Draft{}, &RefusalError{Code: 2, Reason: "digest-not-email", Detail: "--digest is email-only"}
		}
		if strings.TrimSpace(req.Body) != "" {
			return Draft{}, &RefusalError{Code: 2, Reason: "digest-and-file", Detail: "--digest and --file are mutually exclusive; name one body"}
		}
		if len(req.CairnData) == 0 {
			return Draft{}, &RefusalError{Code: 2, Reason: "digest-no-cairn", Detail: "--digest requires --cairn"}
		}
		if len(req.FleetData) == 0 {
			return Draft{}, &RefusalError{Code: 2, Reason: "digest-no-fleet", Detail: "--digest requires --fleet"}
		}
		rendered, err := RenderDigest(req.DigestDate, req.CairnData, req.FleetData)
		if err != nil {
			return Draft{}, err
		}
		body = rendered
	} else if strings.TrimSpace(req.Body) == "" {
		return Draft{}, &RefusalError{Code: 2, Reason: "no-body", Detail: "--file is required (or --digest for an email digest)"}
	}
	if line, shape, ok := SecretInBody(body); ok {
		return Draft{}, &RefusalError{Code: 2, Reason: "body-names-a-secret",
			Detail: fmt.Sprintf("line %d holds a %s; remove it before drafting (the matched text is not printed)", line, shape)}
	}
	payload, err := renderPayload(req, body, now)
	if err != nil {
		return Draft{}, err
	}
	sum := sha256.Sum256(payload)
	return Draft{
		Hash:    hex.EncodeToString(sum[:]),
		Channel: req.Channel,
		Target:  req.Target,
		Title:   req.Title,
		Link:    req.Link,
		Created: now.UTC(),
		Bytes:   payload,
	}, nil
}

// renderPayload is the fixed function per channel. The shape is the provider's, not the
// body's: a draft is exactly what will be transmitted.
func renderPayload(req Request, body string, now time.Time) ([]byte, error) {
	switch req.Channel {
	case Ghost:
		return json.Marshal(ghostDoc{Posts: []ghostPost{{Title: req.Title, HTML: body, Status: "published"}}})
	case Bsky:
		record := map[string]any{
			"$type":     "app.bsky.feed.post",
			"text":      body,
			"createdAt": now.UTC().Format(time.RFC3339),
		}
		if req.Link != "" {
			record["embed"] = map[string]any{
				"$type": "app.bsky.embed.external",
				"external": map[string]any{
					"uri":         req.Link,
					"title":       req.Title,
					"description": firstLine(body),
				},
			}
		}
		return json.Marshal(bskyDoc{Repo: req.Target, Collection: "app.bsky.feed.post", Record: record})
	case Email:
		return json.Marshal(emailDoc{To: req.Target, Subject: req.Title, Body: body})
	case Discord:
		if req.Title != "" || req.Link != "" {
			return nil, &RefusalError{Code: 2, Reason: "discord-prose", Detail: "--title and --link are not accepted on discord"}
		}
		return json.Marshal(discordDoc{Content: body})
	}
	return nil, &RefusalError{Code: 2, Reason: "bad-channel", Detail: "unknown channel"}
}

type ghostPost struct {
	Title  string `json:"title,omitempty"`
	HTML   string `json:"html"`
	Status string `json:"status"`
}

type ghostDoc struct {
	Posts []ghostPost `json:"posts"`
}

type bskyDoc struct {
	Repo       string         `json:"repo"`
	Collection string         `json:"collection"`
	Record     map[string]any `json:"record"`
}

type emailDoc struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

type discordDoc struct {
	Content string `json:"content"`
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// RenderDigest renders the day's cairn beats and the fleet numbers as the email body.
func RenderDigest(date string, cairn, fleet []byte) (string, error) {
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return "", &RefusalError{Code: 2, Reason: "bad-digest", Detail: fmt.Sprintf("--digest wants YYYY-MM-DD, got %q", oneline.Field(date))}
	}
	beats := beatsFor(string(cairn), date)
	if len(beats) == 0 {
		return "", &RefusalError{Code: 2, Reason: "no-beat", Detail: fmt.Sprintf("the cairn holds no beat for %s", date)}
	}
	fleetLines, err := fleetNumbers(fleet)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Nova digest %s\n", date)
	b.WriteString("beats:\n")
	for _, line := range beats {
		fmt.Fprintf(&b, "- %s\n", line)
	}
	b.WriteString("fleet:\n")
	for _, line := range fleetLines {
		fmt.Fprintf(&b, "- %s\n", line)
	}
	return b.String(), nil
}

// beatsFor collects every `## <stamp> <title>` section whose day matches date, condensing
// each section's fact lines into one line.
func beatsFor(cairn, date string) []string {
	var out []string
	var stamp, title string
	var fields []string
	flush := func() {
		if stamp == "" || !strings.HasPrefix(stamp, date) {
			return
		}
		line := stamp
		if title != "" {
			line += " " + title
		}
		if len(fields) > 0 {
			line += ": " + strings.Join(fields, " ")
		}
		out = append(out, line)
	}
	for _, raw := range strings.Split(cairn, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "## ") {
			flush()
			rest := strings.TrimSpace(strings.TrimPrefix(line, "## "))
			stamp, title = rest, ""
			if i := strings.IndexByte(rest, ' '); i >= 0 {
				stamp, title = rest[:i], strings.TrimSpace(rest[i+1:])
			}
			fields = nil
			continue
		}
		if line == "" || stamp == "" {
			continue
		}
		fields = append(fields, line)
	}
	flush()
	return out
}

// fleetNumbers reads one `name<TAB>number` per line and renders `name=number`.
func fleetNumbers(fleet []byte) ([]string, error) {
	var out []string
	for i, raw := range strings.Split(string(fleet), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, number, ok := strings.Cut(line, "\t")
		if !ok || strings.TrimSpace(name) == "" || strings.TrimSpace(number) == "" {
			return nil, &RefusalError{Code: 2, Reason: "bad-fleet", Detail: fmt.Sprintf("--fleet line %d wants name<TAB>number", i+1)}
		}
		out = append(out, strings.TrimSpace(name)+"="+strings.TrimSpace(number))
	}
	sort.Strings(out)
	return out, nil
}

// SecretError is the body-names-a-secret refusal: the line and the shape, never the text.
type SecretError struct {
	Line  int
	Shape string
}

func (e *SecretError) Error() string {
	return fmt.Sprintf("line %d holds a %s", e.Line, e.Shape)
}

// SecretInBody scans the body for the four denied shapes and returns the 1-based line and
// the shape name. It never returns the matched text, so a refusal cannot quote a secret
// back into a log.
func SecretInBody(body string) (int, string, bool) {
	for i, raw := range strings.Split(body, "\n") {
		if shape, ok := secretShape(raw); ok {
			return i + 1, shape, true
		}
	}
	return 0, "", false
}

var credentialNames = []string{"GHOST_ADMIN_KEY", "SMTP_PASSWORD", "BSKY_APP_PASSWORD", "DISCORD_BOT_TOKEN"}

func secretShape(line string) (string, bool) {
	for _, name := range credentialNames {
		if strings.Contains(line, name) {
			return "credential-name", true
		}
	}
	if strings.Contains(line, "DISCORD_") && strings.Contains(line, "_WEBHOOK") {
		return "credential-name", true
	}
	for _, field := range strings.FieldsFunc(line, func(r rune) bool {
		switch r {
		case ' ', '\t', '"', '\'', '`', ',', ')', '(', ';', '<', '>', ']', '[':
			return true
		}
		return false
	}) {
		switch {
		case strings.HasSuffix(field, ".key"):
			return "key-file", true
		case strings.HasSuffix(field, "recovery.pub"):
			return "recovery-key", true
		case strings.Contains(field, "secrets/"):
			return "secrets-store-path", true
		}
	}
	return "", false
}

// RefusalError is one refusal: the exit code, the one-word reason and the remedy sentence.
type RefusalError struct {
	Code   int
	Reason string
	Detail string
}

func (e *RefusalError) Error() string { return e.Detail }

// Save writes <hash>.post (the payload verbatim) and <hash>.meta (one JSON line) under dir.
func Save(dir string, d Draft) error {
	if err := os.WriteFile(filepath.Join(dir, d.Hash+".post"), d.Bytes, 0o644); err != nil {
		return &RefusalError{Code: 2, Reason: "draft-write", Detail: "cannot write the payload: " + oneline.Err(err)}
	}
	raw, err := json.Marshal(meta{Channel: d.Channel, Target: d.Target, Title: d.Title, Link: d.Link, Created: d.Created.UTC().Format(time.RFC3339), Bytes: len(d.Bytes)})
	if err != nil {
		return &RefusalError{Code: 2, Reason: "draft-write", Detail: "cannot encode the meta line: " + oneline.Err(err)}
	}
	if err := os.WriteFile(filepath.Join(dir, d.Hash+".meta"), append(raw, '\n'), 0o644); err != nil {
		return &RefusalError{Code: 2, Reason: "draft-write", Detail: "cannot write the meta line: " + oneline.Err(err)}
	}
	return nil
}

// Load reads a stored draft and re-checks that the bytes still hash to the name it is
// stored under. A draft whose bytes changed under it is refused, not sent.
func Load(dir, hash string) (Draft, error) {
	if !validHash(hash) {
		return Draft{}, &RefusalError{Code: 2, Reason: "bad-hash", Detail: fmt.Sprintf("--draft wants a lower-case sha256, got %q", oneline.Field(hash))}
	}
	body, err := os.ReadFile(filepath.Join(dir, hash+".post"))
	if err != nil {
		return Draft{}, &RefusalError{Code: 2, Reason: "no-draft", Detail: "cannot read the draft payload: " + oneline.Err(err)}
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != hash {
		return Draft{}, &RefusalError{Code: 2, Reason: "draft-changed", Detail: "the stored payload no longer hashes to " + hash + "; re-draft it"}
	}
	raw, err := os.ReadFile(filepath.Join(dir, hash+".meta"))
	if err != nil {
		return Draft{}, &RefusalError{Code: 2, Reason: "no-draft", Detail: "cannot read the draft meta: " + oneline.Err(err)}
	}
	var m meta
	if err := json.Unmarshal(raw, &m); err != nil {
		return Draft{}, &RefusalError{Code: 2, Reason: "bad-draft", Detail: "the meta line will not parse: " + oneline.Err(err)}
	}
	created, _ := time.Parse(time.RFC3339, m.Created)
	return Draft{Hash: hash, Channel: m.Channel, Target: m.Target, Title: m.Title, Link: m.Link, Created: created, Bytes: body}, nil
}

func validHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// Allowed reports whether the allowlist names channel and target. A missing file is an
// error, because a finite set that cannot be read is not a finite set.
func Allowed(path string, ch Channel, target string) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, &RefusalError{Code: 1, Reason: "no-allowlist", Detail: "cannot read --allowlist " + oneline.Field(path) + ": " + oneline.Err(err)}
	}
	for _, line := range strings.Split(string(raw), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		parts := strings.Split(t, "\t")
		if len(parts) < 2 {
			continue
		}
		if strings.TrimSpace(parts[0]) == string(ch) && strings.TrimSpace(parts[1]) == target {
			return true, nil
		}
	}
	return false, nil
}
