package post

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// SendInput is everything the release needs. Endpoint, APIKey and HTTP are supplied by
// the caller so a test points one channel at its own httptest server and no test opens a
// socket to a real provider.
type SendInput struct {
	Drafts    string
	Hash      string
	Approval  string
	BusDir    string
	Allowlist string
	Endpoint  string
	APIKey    string
	HTTP      *http.Client
	Now       time.Time
}

// Result is one release: the provider's own id and url, and the one line to print.
type Result struct {
	ID   string
	URL  string
	Line string
}

// Send releases one stored draft. It refuses unless the allowlist names the target, the
// receipt exists, is Glenn's, names this hash and is under 24 hours old; then it
// transmits the STORED bytes, writes <hash>.sent and is idempotent.
func Send(in SendInput) (Result, error) {
	d, err := Load(in.Drafts, in.Hash)
	if err != nil {
		return Result{}, err
	}
	sentPath := filepath.Join(in.Drafts, d.Hash+".sent")
	if raw, err := os.ReadFile(sentPath); err == nil {
		line := strings.TrimSpace(string(raw))
		if line == "" {
			line = renderOK(d, "-", "-", in.Approval)
		}
		return Result{ID: "-", URL: "-", Line: line}, nil
	}
	allowed, err := Allowed(in.Allowlist, d.Channel, d.Target)
	if err != nil {
		return Result{}, err
	}
	if !allowed {
		return Result{}, &RefusalError{Code: 1, Reason: "target-not-allowed",
			Detail: fmt.Sprintf("channel=%s target=%s is not in --allowlist %s; add the line %s<TAB>%s", oneline.Field(string(d.Channel)), oneline.Field(d.Target), oneline.Field(in.Allowlist), oneline.Field(string(d.Channel)), oneline.Field(d.Target))}
	}
	if _, err := ReadApproval(in.BusDir, in.Approval, d.Hash, in.Now); err != nil {
		return Result{}, err
	}
	id, url, err := transmit(in, d)
	if err != nil {
		return Result{}, err
	}
	line := renderOK(d, id, url, in.Approval)
	if err := os.WriteFile(sentPath, []byte(line+"\n"), 0o644); err != nil {
		return Result{}, &RefusalError{Code: 1, Reason: "sent-write", Detail: "the provider accepted it but the .sent record could not be written: " + oneline.Err(err)}
	}
	return Result{ID: id, URL: url, Line: line}, nil
}

// transmit posts the stored payload and reads only the provider's identifiers back.
func transmit(in SendInput, d Draft) (string, string, error) {
	if strings.TrimSpace(in.Endpoint) == "" {
		return "", "", &RefusalError{Code: 2, Reason: "no-endpoint", Detail: "the channel has no endpoint; refusing to guess"}
	}
	client := in.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequest(http.MethodPost, in.Endpoint, strings.NewReader(string(d.Bytes)))
	if err != nil {
		return "", "", &RefusalError{Code: 2, Reason: "bad-endpoint", Detail: "the endpoint is not a URL: " + oneline.Err(err)}
	}
	req.Header.Set("Content-Type", "application/json")
	switch d.Channel {
	case Ghost, Bsky:
		if in.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+in.APIKey)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", &RefusalError{Code: 1, Reason: "provider-unreachable", Detail: "the provider answers no better than this: " + oneline.Err(err)}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		detail := fmt.Sprintf("the provider answered %d", resp.StatusCode)
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			detail += " Retry-After=" + oneline.Field(ra)
		}
		detail += "; nothing was retried"
		return "", "", &RefusalError{Code: 1, Reason: "provider-limit", Detail: detail}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", &RefusalError{Code: 1, Reason: "provider-refused", Detail: fmt.Sprintf("the provider answered %d; nothing was retried", resp.StatusCode)}
	}
	id, url := providerIDs(raw)
	return id, url, nil
}

// providerIDs reads the provider's own identifiers and nothing else; email returns
// neither and prints id=- url=-.
func providerIDs(raw []byte) (string, string) {
	var doc struct {
		ID    string `json:"id"`
		URL   string `json:"url"`
		URI   string `json:"uri"`
		Posts []struct {
			ID  string `json:"id"`
			URL string `json:"url"`
		} `json:"posts"`
	}
	_ = json.Unmarshal(raw, &doc)
	id, url := doc.ID, doc.URL
	if len(doc.Posts) > 0 {
		if doc.Posts[0].ID != "" {
			id = doc.Posts[0].ID
		}
		if doc.Posts[0].URL != "" {
			url = doc.Posts[0].URL
		}
	}
	if id == "" && doc.URI != "" {
		id = doc.URI
	}
	if id == "" {
		id = "-"
	}
	if url == "" {
		url = "-"
	}
	return id, url
}

func renderOK(d Draft, id, url, approval string) string {
	return fmt.Sprintf("POST OK channel=%s id=%s url=%s hash=%s approval=%s bytes=%d",
		oneline.Field(string(d.Channel)), oneline.Field(id), oneline.Field(url),
		oneline.Field(d.Hash), oneline.Field(approval), len(d.Bytes))
}
