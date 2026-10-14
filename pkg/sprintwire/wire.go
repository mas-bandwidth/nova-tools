// Package sprintwire is how a worker talks to the sprint's server: the run
// loop on the coordinator's machine, the one writer of the sprint, which runs a
// worker's verbs for it, one at a time, beside the store. The server is single
// threaded and runs each request's batch in order, a plain client/server pair
// carries the verbs rather than a shared store, and both ends are Go. A request
// is the worker's verbs, each
// the argument list it would give nova-sprint, in the order to run them; the
// reply is each verb's exit code and what it printed. One request is one
// exchange whatever it carries, from beside the server or from 100 ms away.
package sprintwire

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Path is the server's one endpoint.
const Path = "/verbs"

// MaxRequest bounds a request's body, and MaxVerbs the verbs of one batch.
const (
	MaxRequest = 1 << 20
	MaxVerbs   = 256
)

// Request is a worker's verbs, in the order to run them.
type Request struct {
	Verbs [][]string `json:"verbs"`
}

// Result is one verb's answer: nova-sprint's exit code, and what it printed.
// Outcome tells the member what the verb did, apart from its exit code, which
// does not separate a definite refusal from an unknown outcome: "accepted"
// when the verb ran and its work is in the table, "refused" when it was
// refused (usage, or before any write), "unknown" when it is not known
// whether it happened (a cut, a store that did not confirm, or a clear that
// closed the step as it finished). Changed says whether the verb wrote:
// "yes", "no", or "unknown".
type Result struct {
	Code    int    `json:"code"`
	Stdout  string `json:"stdout"`
	Stderr  string `json:"stderr"`
	Outcome string `json:"outcome,omitempty"` // accepted | refused | unknown
	Changed string `json:"changed,omitempty"` // yes | no | unknown
}

// Response is the answers, one a verb, in the request's order.
type Response struct {
	Results []Result `json:"results"`
}

// Client sends verbs to a sprint server.
type Client struct {
	Addr string       // host:port
	HTTP *http.Client // nil is a client that waits Timeout for an answer
}

// Timeout is how long a client waits for the server's answer by default.
const Timeout = 2 * time.Minute

// Do sends the verbs in one request and returns their answers, one a verb. An
// error is a server that did not answer, or answered something else: nothing
// is known of what ran.
func (c Client) Do(ctx context.Context, verbs ...[]string) ([]Result, error) {
	body, err := json.Marshal(Request{Verbs: verbs})
	if err != nil {
		return nil, err
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: Timeout}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+c.Addr+Path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the sprint server at %s did not answer: %w", c.Addr, err)
	}
	defer func() { _ = resp.Body.Close() }() // ignored: the answer is read whole below, or not at all
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("the sprint server at %s: its answer was cut: %w", c.Addr, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the sprint server at %s refused the request (%s): %s", c.Addr, resp.Status, bytes.TrimSpace(raw))
	}
	// every result is read strictly: an answer with no exit code, or one that is not a
	// result at all, is no answer, never exit 0
	var got struct {
		Results []*struct {
			Code    *int    `json:"code"`
			Stdout  *string `json:"stdout"`
			Stderr  *string `json:"stderr"`
			Outcome string  `json:"outcome"`
			Changed string  `json:"changed"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &got); err != nil || len(got.Results) != len(verbs) {
		return nil, fmt.Errorf("the sprint server at %s answered %d results for %d verbs", c.Addr, len(got.Results), len(verbs))
	}
	out := make([]Result, len(got.Results))
	for i, r := range got.Results {
		if r == nil || r.Code == nil || r.Stdout == nil || r.Stderr == nil {
			return nil, fmt.Errorf("the sprint server at %s: result %d of %d names no exit code or output: nothing is known of what ran", c.Addr, i+1, len(verbs))
		}
		out[i] = Result{Code: *r.Code, Stdout: *r.Stdout, Stderr: *r.Stderr, Outcome: r.Outcome, Changed: r.Changed}
	}
	return out, nil
}
