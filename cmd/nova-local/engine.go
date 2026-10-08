// The engines (docs/SPEC-LOCAL.md, rule 2): the adapter every engine is, and the one HTTP
// client they speak through. Nothing here prints, and nothing here opens a socket of its
// own: every request goes through the Client's Do, which main fills with a real transport
// and a test with a fake one.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Do sends one request and returns its response: http.Client.Do in the command, a
// function over a fake engine in a test (no socket, no real time).
type Do func(*http.Request) (*http.Response, error)

// Client is one engine's endpoint: its base URL (the OpenAI-compatible /v1 a harness
// calls), the transport, and the budget each request is given (status's --timeout).
type Client struct {
	Base    string
	Do      Do
	Timeout time.Duration
}

// Root is the base URL without its /v1: the engine's own API lives there.
func (c Client) Root() string { return strings.TrimSuffix(strings.TrimSuffix(c.Base, "/"), "/v1") }

// call sends one JSON request (in nil sends no body) and decodes a 2xx reply into out
// (nil decodes nothing); the HTTP status is returned whatever it was.
func (c Client) call(ctx context.Context, method, path string, in, out any) (int, error) {
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Root()+path, body)
	if err != nil {
		return 0, err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close() // ignored: the reply was read in full or its read error returned; a close error adds nothing
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxReply))
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode/100 != 2 {
		return resp.StatusCode, fmt.Errorf("%s %s answered %d", method, path, resp.StatusCode)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("%s %s: the reply is not the JSON this adapter reads: %v", method, path, err)
		}
	}
	return resp.StatusCode, nil
}

// maxReply bounds what one engine reply may be: a listing of every tag a store holds
// is kilobytes, and a reply past this is not one this tool reads.
const maxReply = 16 << 20

// Model is one model an engine advertises, as status prints it: its reference, the
// digest the engine reports ("" when it reports none), the bytes of its weights, whether
// it is loaded, and the context it is loaded at (0 when not loaded or not reported).
type Model struct {
	Ref     string
	Digest  string
	Weights int64
	Loaded  bool
	NumCtx  int
}

// Refusal is a verb's no with the command that satisfies it: Code 1 when the caller can
// retry differently, 2 when an input could not be used at all (docs/SPEC-LOCAL.md, the
// verbs).
type Refusal struct {
	Why, Remedy string
	Code        int
}

func (r *Refusal) Error() string { return r.Why }

// ServeRequest is what serve asks an adapter for: one model at a context, kept loaded
// for KeepAlive, with Seed baked in when set (rule 6).
type ServeRequest struct {
	Model     string
	NumCtx    int
	Seed      string // "" is unset
	KeepAlive string
	DryRun    bool
}

// Served is what serve made: the name a harness calls (rule 6), the parent's digest,
// what was baked in, whether the tag was created now, and the warm-up's wall time
// (rule 7), measured on the clock serve was given.
type Served struct {
	ServeAs     string
	Digest      string
	Temperature string // "0" baked in, "" unset
	Seed        string
	Created     bool
	Load        time.Duration
}

// Adapter is one engine (rule 2): adding one is one file and no change to a verb. It
// declares its name, its default base URL, its health check, how it lists models, how
// it serves one at a context and how it stops one, where its weights live, and the
// provider id a harness names it by ("" when it publishes none).
type Adapter interface {
	Name() string
	DefaultBase() string
	Provider() string
	// Health is the HTTP status of the health path; up is a 2xx inside the budget.
	Health(ctx context.Context, c Client) (int, error)
	// Models is every model the engine advertises, with Loaded and NumCtx filled.
	Models(ctx context.Context, c Client) ([]Model, error)
	// Serve makes req.Model served at req.NumCtx and warms it, timing the warm-up on now.
	Serve(ctx context.Context, c Client, req ServeRequest, now func() time.Time) (Served, error)
	// Stop unloads the served model; already is true when it was not loaded.
	Stop(ctx context.Context, c Client, model string) (already bool, err error)
	// Store is the directory the model's weights are read from, unresolved, or "" and why
	// it could not be read.
	Store(ctx context.Context, c Client, model string) (dir, why string)
}

// Adapters is the engines this build knows, by name, in the order status reports them.
func Adapters() []Adapter { return []Adapter{Ollama{}} }
