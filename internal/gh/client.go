// Package gh is the one GitHub client (nova-tools #4343): every REST call a
// nova-sprint verb makes goes through Client.Do, which counts the call per
// verb and endpoint in Redis, records X-RateLimit-Remaining, paces writes
// through one per-second writer, and retries a 403 or 429 secondary limit
// after its Retry-After instead of surfacing it. Never GraphQL. The class
// tests in this package hold the boundary: no other package shells out to
// gh, imports go-github or names the REST root.
//
// Measured 2026-09-26 (Glenn ~11:42 AM ET): the hand landing scripts polled
// gh 60-80 times per landing, children each ran `gh pr checks --watch`, the
// reconciler and lander read PR state by REST where the webhook ingest had
// already written it to ev:github, and no rate header was ever read.
//
// Keys (no TTL, #keys-do-not-expire; the budget read prunes buckets older
// than a day):
//
//	gh:calls                          SET   "<verb> <endpoint>" (the index; no SCAN)
//	gh:calls:<verb>:<endpoint>        HASH  <unix minute> -> calls in that minute
//	gh:rate                           HASH  remaining, limit, reset, at, verb, endpoint
//	gh:writer                         HASH  <unix second> -> writes made in it
package gh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// DefaultAPI is the REST root.
const DefaultAPI = "https://api.github.com"

// DefaultRate is the writes per second the writer paces to. GitHub's
// secondary limit is 80 content-generating requests a minute; one a second
// keeps a burst of 100 cut cards under it.
const DefaultRate = 1

// DefaultRetries is how many times a secondary limit is retried.
const DefaultRetries = 3

// DefaultRetryAfter is the wait when a secondary-limit reply carries no
// Retry-After (GitHub's own advice: at least one minute).
const DefaultRetryAfter = time.Minute

// MaxBody is the most of a reply body that is read.
const MaxBody = 8 << 20

// ErrBudget is returned once the client's per-run budget is spent.
var ErrBudget = errors.New("REST budget spent")

// ErrQuota is the primary quota at zero: the call is refused, not retried,
// and the refusal prints the reset time.
var ErrQuota = errors.New("GitHub primary quota exhausted")

// Client is the one GitHub client. A zero API is DefaultAPI; a client from
// New reads an empty Token once from the environment on its first call, a
// literal one sends no Authorization (a test's fake forge); a nil HTTP is a
// 30 s client; a nil Redis counts nothing and paces in-process (a test
// without a store: every production client has one,
// TestEveryProductionClientHasAStore); nil Now and Sleep are the wall
// clock; a nil Log is stderr; a nil Env is os.Getenv.
type Client struct {
	API   string
	Token string
	HTTP  *http.Client
	// Verb is what the calls are counted under (land-stream, file, ...).
	Verb string
	// Redis is where the counts, the rate record and the writer's pace live.
	Redis redis.Cmdable
	// Budget caps the requests one run makes; 0 is unlimited. Calls is the
	// requests made so far, retries included.
	Budget int
	Calls  int
	// Retries is the secondary-limit retries made so far.
	Retries int
	// Rate is writes per second; 0 is DefaultRate. MaxRetries is the retries
	// of one call; 0 is DefaultRetries.
	Rate       int
	MaxRetries int
	// Now and Sleep are the clock; a test injects both.
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error
	// Log takes the retry and refusal lines; nil is stderr.
	Log io.Writer
	// Env reads the token environment; nil is os.Getenv.
	Env func(string) string

	mu     sync.Mutex
	local  map[int64]int // the writer's pace when Redis is nil
	lazy   bool          // New: read an empty Token once from Env
	tokErr error         // that read failed once; every call after refuses the same way
}

// New is the production client of one verb over the store: its token is
// read once from the environment (Token) on the first call.
func New(verb string, rdb redis.Cmdable) *Client {
	return &Client{Verb: verb, Redis: rdb, lazy: true}
}

// Response is one reply. Remaining is X-RateLimit-Remaining, -1 when the
// reply carried none.
type Response struct {
	Status    int
	Body      []byte
	Remaining int
}

// HTTPError is a non-2xx reply.
type HTTPError struct {
	Method  string
	Path    string
	Status  int
	Body    string
	Message string // the reply's message field when it has one
}

func (e *HTTPError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = e.Body
	}
	return fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.Path, e.Status, msg)
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (c *Client) log() io.Writer {
	if c.Log != nil {
		return c.Log
	}
	return os.Stderr
}

// token is the bearer token, read once from the environment when the
// client came from New without one.
func (c *Client) token() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Token != "" || !c.lazy {
		return c.Token, nil
	}
	if c.tokErr != nil {
		return "", c.tokErr
	}
	env := c.Env
	if env == nil {
		env = os.Getenv
	}
	tok, err := TokenFrom(env)
	if err != nil {
		c.tokErr = err
		fmt.Fprintf(c.log(), "gh: REFUSED %s: %v\n", c.verb(), err)
		return "", err
	}
	c.Token = tok
	return tok, nil
}

func (c *Client) verb() string {
	v := strings.TrimSpace(c.Verb)
	if v == "" {
		return "unnamed"
	}
	return strings.ReplaceAll(v, " ", "-")
}

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Client) root() string {
	api := strings.TrimRight(c.API, "/")
	if api == "" {
		return DefaultAPI
	}
	return api
}

func isWrite(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// Do makes one call: path is the REST path (/repos/o/r/pulls), body is
// marshalled as JSON when not nil, out is decoded from a 2xx reply when not
// nil. A write is paced by the writer; a 403 or 429 secondary limit is
// retried after Retry-After; a non-2xx reply is an *HTTPError with the
// body; the budget spent is ErrBudget. Every request made is counted.
func (c *Client) Do(ctx context.Context, method, path string, body any, out any) (Response, error) {
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return Response{Remaining: -1}, err
		}
		payload = b
	}
	endpoint := Endpoint(method, path)
	retries := c.MaxRetries
	if retries <= 0 {
		retries = DefaultRetries
	}
	if _, err := c.token(); err != nil {
		return Response{Remaining: -1}, err
	}
	for attempt := 0; ; attempt++ {
		// The budget is spent by calls, not by the retries of one: a call
		// that waits out a secondary limit is still one call of the run.
		if attempt == 0 && c.Budget > 0 && c.Calls-c.Retries >= c.Budget {
			return Response{Remaining: -1}, ErrBudget
		}
		if isWrite(method) {
			if err := c.pace(ctx); err != nil {
				return Response{Remaining: -1}, err
			}
		}
		c.Calls++
		resp, err := c.once(ctx, method, path, payload)
		c.count(ctx, endpoint, resp)
		if err != nil {
			return resp.Response, err
		}
		if resp.Status >= 200 && resp.Status < 300 {
			if out != nil && len(resp.Body) > 0 {
				if err := json.Unmarshal(resp.Body, out); err != nil {
					return resp.Response, fmt.Errorf("%s %s: %v", method, path, err)
				}
			}
			return resp.Response, nil
		}
		herr := &HTTPError{Method: method, Path: path, Status: resp.Status, Body: strings.TrimSpace(string(resp.Body))}
		var m struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(resp.Body, &m) == nil {
			herr.Message = m.Message
		}
		if resp.Status != http.StatusForbidden && resp.Status != http.StatusTooManyRequests {
			return resp.Response, herr
		}
		if resp.Remaining == 0 {
			reset := resp.reset
			fmt.Fprintf(c.log(), "gh: REFUSED %s %s: %v; resets at %s\n", c.verb(), endpoint, ErrQuota, reset)
			return resp.Response, fmt.Errorf("%s %s: %w; resets at %s", method, path, ErrQuota, reset)
		}
		if !resp.secondary() {
			return resp.Response, herr
		}
		if attempt >= retries {
			fmt.Fprintf(c.log(), "gh: REFUSED %s %s: secondary limit after %d retries\n", c.verb(), endpoint, attempt)
			return resp.Response, herr
		}
		wait := resp.retryAfter
		if wait <= 0 {
			wait = DefaultRetryAfter
		}
		c.Retries++
		fmt.Fprintf(c.log(), "gh: RETRY %s %s HTTP %d secondary limit; waiting %s (%d/%d)\n", c.verb(), endpoint, resp.Status, wait, attempt+1, retries)
		if err := c.sleep(ctx, wait); err != nil {
			return resp.Response, err
		}
	}
}

// reply is one HTTP exchange's headers read.
type reply struct {
	Response
	retryAfter time.Duration
	reset      string
	limit      string
}

func (r reply) secondary() bool {
	if r.retryAfter > 0 {
		return true
	}
	b := strings.ToLower(string(r.Body))
	return strings.Contains(b, "secondary rate limit") || strings.Contains(b, "abuse detection")
}

func (c *Client) once(ctx context.Context, method, path string, payload []byte) (reply, error) {
	r := reply{Response: Response{Remaining: -1}}
	var rd io.Reader
	if payload != nil {
		rd = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.root()+path, rd)
	if err != nil {
		return r, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return r, err
	}
	defer resp.Body.Close()
	r.Status = resp.StatusCode
	if v := resp.Header.Get("X-RateLimit-Remaining"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			r.Remaining = n
		}
	}
	r.limit = resp.Header.Get("X-RateLimit-Limit")
	r.reset = resp.Header.Get("X-RateLimit-Reset")
	if v := resp.Header.Get("Retry-After"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			r.retryAfter = time.Duration(n) * time.Second
		}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody))
	r.Body = b
	if err != nil {
		return r, err
	}
	return r, nil
}
