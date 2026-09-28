package post

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Send releases a stored payload through the approve gate. The gate is checked
// before any socket is opened: a target outside the allowlist and an approval
// that is missing, for another hash, from another sender or stale are all
// refusals that make zero requests.
func Send(o Options) (SendResult, error) {
	if err := EnsureDrafts(o.Drafts); err != nil {
		return SendResult{}, err
	}
	if !validHash(o.Draft) {
		return SendResult{}, Refuse("bad-draft", fmt.Sprintf("--draft %s is not a payload hash; it wants the sixty-four lower-case hex digits `nova-post draft` prints", oneline.Field(o.Draft)), 2)
	}
	sentPath := filepath.Join(o.Drafts, o.Draft+".sent")
	if raw, err := os.ReadFile(sentPath); err == nil {
		return SendResult{Line: strings.TrimRight(string(raw), "\n")}, nil
	}
	stored, err := ReadDraft(o.Drafts, o.Draft)
	if err != nil {
		return SendResult{}, err
	}
	if err := RequireAllowlisted(o.Allowlist, Channel(stored.Meta.Channel), stored.Meta.Target); err != nil {
		return SendResult{}, err
	}
	if err := requireApproval(o.Bus, o.Approval, o.Draft, o.Now); err != nil {
		return SendResult{}, err
	}

	channel := Channel(stored.Meta.Channel)
	credName, err := CredentialName(channel, stored.Meta.Target)
	if err != nil {
		return SendResult{}, err
	}
	cred := os.Getenv(credName)
	if cred == "" {
		return SendResult{}, Refuse("no-credential", fmt.Sprintf("%s is absent from this process; run this under `nova-secrets exec --only %s -- nova-post send ...`", oneline.Field(credName), oneline.Field(credName)), 2)
	}

	id, url, err := deliver(o.Transport, channel, stored.Meta.Target, cred, stored.Payload, o.Now)
	if err != nil {
		return SendResult{}, err
	}
	if id == "" {
		id = "-"
	}
	if url == "" {
		url = "-"
	}
	line := fmt.Sprintf("POST OK channel=%s id=%s url=%s hash=%s approval=%s bytes=%d",
		oneline.Field(string(channel)), oneline.Field(id), oneline.Field(url),
		oneline.Field(o.Draft), oneline.Field(o.Approval), len(stored.Payload))
	if err := os.WriteFile(sentPath, []byte(line+"\n"), 0o644); err != nil {
		return SendResult{}, Refuse("write-sent", fmt.Sprintf("cannot record the send: %s", oneline.Err(err)), 2)
	}
	return SendResult{Line: line}, nil
}

// requireApproval is the whole gate: the receipt exists, its sender is Glenn
// and no other, its body names this hash, and it is under 24 hours old. Any
// failure is a one-line refusal with the remedy, exit 1.
func requireApproval(busDir, approval, hash string, now time.Time) error {
	if strings.TrimSpace(approval) == "" {
		return Refuse("missing-approval", "--approval is required; it names the receipt that approves this hash", 2)
	}
	note, ok := findNote(busDir, approval)
	if !ok {
		return Refuse("no-approval", fmt.Sprintf("no receipt %s in %s; have Glenn send `APPROVE nova-post sha256=%s` and pass its id", oneline.Field(approval), oneline.Escape(busDir), oneline.Field(hash)), 1)
	}
	if !isGlenn(note.Header.From) {
		return Refuse("approval-not-from-glenn", fmt.Sprintf("receipt %s is from %s, not Glenn; only Glenn's approval releases a send", oneline.Field(approval), oneline.Field(note.Header.From)), 1)
	}
	want := "APPROVE nova-post sha256=" + hash
	if !hasLine(note.Body, want) {
		return Refuse("approval-other-hash", fmt.Sprintf("receipt %s does not name this draft; it must carry the line `%s`", oneline.Field(approval), want), 1)
	}
	at, err := time.ParseInLocation(bus.DateLayout, note.Header.Date, time.UTC)
	if err != nil {
		return Refuse("approval-unreadable", fmt.Sprintf("receipt %s has no readable Date line; resend it with `nova-bus send`", oneline.Field(approval)), 1)
	}
	if now.Sub(at) >= 24*time.Hour {
		return Refuse("stale-approval", fmt.Sprintf("receipt %s is 24 hours old or more; ask Glenn for a fresh `APPROVE nova-post sha256=%s`", oneline.Field(approval), oneline.Field(hash)), 1)
	}
	return nil
}

// isGlenn reports whether a From line names Glenn. The parenthetical and any
// address suffix are presentation; the first name is the fact.
func isGlenn(from string) bool {
	name := strings.TrimSpace(from)
	if i := strings.IndexAny(name, " \t,("); i >= 0 {
		name = name[:i]
	}
	return strings.EqualFold(name, "glenn")
}

// hasLine reports whether the body carries the wanted line, trimmed.
func hasLine(body, want string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

// deliver transmits the stored payload and returns the provider's own id and
// url. A 429 or a 5xx is a named failure and is never retried.
func deliver(t Transport, c Channel, target, cred string, payload []byte, now time.Time) (string, string, error) {
	switch c {
	case Ghost:
		url := ghostBase(t, target) + "/ghost/api/admin/posts/?source=html"
		token, err := ghostToken(cred, now)
		if err != nil {
			return "", "", err
		}
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return "", "", Refuse("provider-error", oneline.Err(err), 1)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Ghost "+token)
		body, err := doHTTP(t, req)
		if err != nil {
			return "", "", err
		}
		var out struct {
			Posts []struct {
				ID  string `json:"id"`
				URL string `json:"url"`
			} `json:"posts"`
		}
		_ = json.Unmarshal(body, &out)
		if len(out.Posts) > 0 {
			return out.Posts[0].ID, out.Posts[0].URL, nil
		}
		return "", "", nil
	case Bsky:
		url := bskyBase(t) + "/xrpc/com.atproto.repo.createRecord"
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return "", "", Refuse("provider-error", oneline.Err(err), 1)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+cred)
		body, err := doHTTP(t, req)
		if err != nil {
			return "", "", err
		}
		var out struct {
			URI string `json:"uri"`
			CID string `json:"cid"`
		}
		_ = json.Unmarshal(body, &out)
		return out.URI, out.URI, nil
	case Discord:
		req, err := http.NewRequest(http.MethodPost, cred, bytes.NewReader(payload))
		if err != nil {
			return "", "", Refuse("provider-error", oneline.Err(err), 1)
		}
		req.Header.Set("Content-Type", "application/json")
		body, err := doHTTP(t, req)
		if err != nil {
			return "", "", err
		}
		var out struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(body, &out)
		return out.ID, "", nil
	case Email:
		mailer := t.Mail
		if mailer == nil {
			mailer = smtpSender{}
		}
		if err := mailer.Send(target, payload); err != nil {
			return "", "", Refuse("provider-error", oneline.Err(err), 1)
		}
		return "", "", nil
	}
	return "", "", Refuse("bad-channel", "give --channel ghost, bsky, email or discord", 2)
}

// doHTTP runs one request and refuses a 429 or any non-2xx as a named failure,
// with the Retry-After when the provider gave one and the promise that nothing
// was retried.
func doHTTP(t Transport, req *http.Request) ([]byte, error) {
	client := t.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, Refuse("provider-error", fmt.Sprintf("%s answered nothing; nothing was retried", oneline.Err(err)), 1)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		retry := ""
		if v := resp.Header.Get("Retry-After"); v != "" {
			retry = " retry-after=" + oneline.Field(v)
		}
		return nil, Refuse("provider-refused", fmt.Sprintf("status=%d%s nothing was retried; stop and read the provider's transcript", resp.StatusCode, retry), 1)
	}
	return body, nil
}

// smtpSender is the production email transport, reading the host and sender
// from the environment rather than a flag or a secret file.
type smtpSender struct{}

func (smtpSender) Send(to string, payload []byte) error {
	host := os.Getenv("SMTP_HOST")
	if host == "" {
		return fmt.Errorf("SMTP_HOST is absent from this process; set the host or run under nova-secrets exec")
	}
	from := os.Getenv("SMTP_FROM")
	if from == "" {
		from = to
	}
	password := os.Getenv("SMTP_PASSWORD")
	auth := smtp.PlainAuth("", from, password, host)
	return smtp.SendMail(host, auth, from, []string{to}, payload)
}
