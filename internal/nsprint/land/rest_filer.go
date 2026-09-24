package land

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type RESTFiler struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func (f RESTFiler) client() *http.Client {
	if f.HTTP != nil {
		return f.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (f RESTFiler) endpoint(repo, suffix string) (string, error) {
	owner, name, ok := strings.Cut(strings.Trim(repo, "/"), "/")
	if !ok {
		owner, name = "mas-bandwidth", strings.Trim(repo, "/")
	}
	if owner == "" || name == "" {
		return "", fmt.Errorf("land: invalid repo %q", repo)
	}
	base := strings.TrimRight(f.BaseURL, "/")
	if base == "" {
		base = "https://api.github.com"
	}
	return base + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/issues" + suffix, nil
}

func (f RESTFiler) do(ctx context.Context, method, endpoint string, body []byte) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if f.Token != "" {
		req.Header.Set("Authorization", "Bearer "+f.Token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := f.client().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, resp.StatusCode, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return b, resp.StatusCode, &HTTPStatusError{Code: resp.StatusCode, Body: strings.TrimSpace(string(b))}
	}
	return b, resp.StatusCode, nil
}

func (f RESTFiler) File(ctx context.Context, repo, title, body string) (int, error) {
	ep, err := f.endpoint(repo, "")
	if err != nil {
		return 0, err
	}
	payload, _ := json.Marshal(map[string]string{"title": title, "body": body})
	b, _, err := f.do(ctx, http.MethodPost, ep, payload)
	if err != nil {
		return 0, err
	}
	var v struct {
		Number int `json:"number"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return 0, err
	}
	if v.Number <= 0 {
		return 0, fmt.Errorf("land: issue response has number %d", v.Number)
	}
	return v.Number, nil
}

func (f RESTFiler) Find(ctx context.Context, repo, marker string, since time.Time) (int, bool, error) {
	q := url.Values{"state": {"all"}, "sort": {"created"}, "direction": {"desc"}, "since": {since.UTC().Format(time.RFC3339)}, "per_page": {"100"}}
	ep, err := f.endpoint(repo, "?"+q.Encode())
	if err != nil {
		return 0, false, err
	}
	b, _, err := f.do(ctx, http.MethodGet, ep, nil)
	if err != nil {
		return 0, false, err
	}
	var items []struct {
		Number      int             `json:"number"`
		Body        string          `json:"body"`
		PullRequest json.RawMessage `json:"pull_request"`
	}
	if err := json.Unmarshal(b, &items); err != nil {
		return 0, false, err
	}
	best := 0
	for _, item := range items {
		if len(item.PullRequest) > 0 && string(item.PullRequest) != "null" {
			continue
		}
		first := strings.SplitN(strings.ReplaceAll(item.Body, "\r\n", "\n"), "\n", 2)[0]
		if first == marker && (best == 0 || item.Number < best) {
			best = item.Number
		}
	}
	if best > 0 {
		return best, true, nil
	}
	if len(items) == 100 {
		return 0, false, fmt.Errorf("land: full issues page has no marker %s (per_page=%s)", marker, strconv.Itoa(len(items)))
	}
	return 0, false, nil
}
