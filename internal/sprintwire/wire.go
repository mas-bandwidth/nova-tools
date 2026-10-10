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
	"os"
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
type Result struct {
	Code       int    `json:"code"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	Restarting bool   `json:"restarting,omitempty"`
}

// Response is the answers, one a verb, in the request's order.
type Response struct {
	Results []Result `json:"results"`
}

// Client sends verbs to a sprint server.
type Client struct {
	Addr   string        // host:port
	HTTP   *http.Client  // nil is a client that waits Timeout for an answer
	Stderr io.Writer     // optional stderr for waiting output
	Bound  time.Duration // max time to wait for server restart (default 4 minutes)
}

// Timeout is how long a client waits for the server's answer by default.
const Timeout = 2 * time.Minute

// RestartStep is the step a client waits between resends while a server switch
// is under way, and DefaultRestartBound how long it waits in all
// (docs/SPEC-SPRINT.md, section "Server Switch Restarting and Bounded Wait").
const (
	RestartStep         = 500 * time.Millisecond
	DefaultRestartBound = 4 * time.Minute
)

// Do sends the verbs in one request and returns their answers, one a verb. An
// error is a server that did not answer, or answered something else: nothing
// is known of what ran, and that request is never sent again. A typed
// Restarting result instead means a server switch is under way: Do waits one
// step, prints one WAITING line, and sends the same request again, up to its
// bound, then fails naming the switch
// (docs/SPEC-SPRINT.md, section "Server Switch Restarting and Bounded Wait").
func (c Client) Do(ctx context.Context, verbs ...[]string) ([]Result, error) {
	bound := c.Bound
	if bound <= 0 {
		bound = DefaultRestartBound
	}
	stderr := c.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	body, err := json.Marshal(Request{Verbs: verbs})
	if err != nil {
		return nil, err
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: Timeout}
	}

	start := time.Now()
	waited := false

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+c.Addr+Path, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := hc.Do(req)
		if err != nil {
			return nil, fmt.Errorf("the sprint server at %s did not answer: %w", c.Addr, err)
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		// ignored: close response body
		_ = resp.Body.Close() // ignored: response body closed after reading
		if err != nil {
			return nil, fmt.Errorf("the sprint server at %s: its answer was cut: %w", c.Addr, err)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("the sprint server at %s refused the request (%s): %s", c.Addr, resp.Status, bytes.TrimSpace(raw))
		}
		var got struct {
			Results []*struct {
				Code       *int    `json:"code"`
				Stdout     *string `json:"stdout"`
				Stderr     *string `json:"stderr"`
				Restarting *bool   `json:"restarting"`
			} `json:"results"`
		}
		if err := json.Unmarshal(raw, &got); err != nil || len(got.Results) != len(verbs) {
			return nil, fmt.Errorf("the sprint server at %s answered %d results for %d verbs", c.Addr, len(got.Results), len(verbs))
		}

		restarting := false
		for _, r := range got.Results {
			if r != nil && r.Restarting != nil && *r.Restarting {
				restarting = true
				break
			}
		}

		if restarting {
			if !waited {
				// ignored: write waiting message
				_, _ = io.WriteString(stderr, "WAITING\n") // ignored: write waiting message
				waited = true
			}
			if time.Since(start) >= bound {
				return nil, fmt.Errorf("the sprint server at %s: server switch timed out waiting for restart", c.Addr)
			}
			if err := waitStep(ctx, RestartStep); err != nil {
				return nil, err
			}
			continue
		}

		out := make([]Result, len(got.Results))
		for i, r := range got.Results {
			if r == nil || r.Code == nil || r.Stdout == nil || r.Stderr == nil {
				return nil, fmt.Errorf("the sprint server at %s: result %d of %d names no exit code or output: nothing is known of what ran", c.Addr, i+1, len(verbs))
			}
			out[i] = Result{Code: *r.Code, Stdout: *r.Stdout, Stderr: *r.Stderr}
		}
		return out, nil
	}
}

// waitStep waits one restart step or until ctx is done: the one-shot clock the
// client's resend loop waits on between an answer that names a restart
// (docs/SPEC-SPRINT.md, section "Server Switch Restarting and Bounded Wait").
func waitStep(ctx context.Context, step time.Duration) error {
	t := time.NewTimer(step)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
