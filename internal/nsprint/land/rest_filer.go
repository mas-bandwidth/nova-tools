package land

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/gh"
	"github.com/redis/go-redis/v9"
)

// RESTFiler files the flaky issue and finds one by its marker through the
// one GitHub client (internal/gh, #4343). Redis, when set, counts the calls
// under Verb (default land-flaky).
type RESTFiler struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
	Verb    string
	Redis   redis.Cmdable
}

func (f RESTFiler) client() *gh.Client {
	verb := f.Verb
	if verb == "" {
		verb = "land flaky"
	}
	return &gh.Client{API: f.BaseURL, Token: f.Token, HTTP: f.HTTP, Verb: verb, Redis: f.Redis}
}

func (f RESTFiler) repo(repo string) (string, error) {
	owner, name, ok := strings.Cut(strings.Trim(repo, "/"), "/")
	if !ok {
		owner, name = "mas-bandwidth", strings.Trim(repo, "/")
	}
	if owner == "" || name == "" {
		return "", fmt.Errorf("land: invalid repo %q", repo)
	}
	return url.PathEscape(owner) + "/" + url.PathEscape(name), nil
}

// status maps the client's non-2xx error to the store's HTTPStatusError,
// which tells a definite rejection from an unknown outcome.
func status(err error) error {
	var herr *gh.HTTPError
	if errors.As(err, &herr) {
		return &HTTPStatusError{Code: herr.Status, Body: herr.Body}
	}
	return err
}

func (f RESTFiler) File(ctx context.Context, repo, title, body string) (int, error) {
	r, err := f.repo(repo)
	if err != nil {
		return 0, err
	}
	n, _, err := f.client().CreateIssue(ctx, r, title, body)
	if err != nil {
		return 0, status(err)
	}
	return n, nil
}

func (f RESTFiler) Find(ctx context.Context, repo, marker string, since time.Time) (int, bool, error) {
	r, err := f.repo(repo)
	if err != nil {
		return 0, false, err
	}
	q := url.Values{"state": {"all"}, "sort": {"created"}, "direction": {"desc"}, "since": {since.UTC().Format(time.RFC3339)}, "per_page": {"100"}}
	var items []struct {
		Number      int             `json:"number"`
		Body        string          `json:"body"`
		PullRequest json.RawMessage `json:"pull_request"`
	}
	if err := f.client().Issues(ctx, r, q, &items); err != nil {
		return 0, false, status(err)
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
