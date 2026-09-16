package swarm

// ONE CALL, NO TOOLS, NO TRANSCRIPT (issue #856).
//
// A model step is one HTTP request to the route's OpenAI-compatible chat-completions
// endpoint and one answer. There is no session, no tool list and no message history: the
// request carries the step's own input and nothing else, which is the whole point -- "no
// memory between calls".
//
// THE KEY IS READ AS DATA AND NEVER PRINTED. It comes out of the same files the harness
// already reads -- the provider block of `~/.config/opencode/opencode.json` for an
// OpenCode route, `~/.config/deepseek/env` for the direct DeepSeek route -- and lives in
// the Route struct alone. Every error this file makes names the FILE the key came from and
// never the key, and the request's own headers are never logged (rule: the argv log
// redacts by name; here there is no argv at all).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// zenBaseURL is the OpenCode endpoint a provider block names no baseURL for: the `opencode`
// provider is Zen and Go, whose base the harness knows by name.
const zenBaseURL = "https://opencode.ai/zen/v1"

// chatCompletionsPath is the one route this client speaks.
const chatCompletionsPath = "/chat/completions"

// providerKeyFiles are the key files this bench keeps per provider, read as DATA by the
// harness -- never sourced, which is how a key leaked once (kin-freddy). A file holding a
// bare key is one line; a file holding `NAME=value` lines is read for the value.
var providerKeyFiles = map[string]string{
	"deepseek":  ".config/deepseek/env",
	"inception": ".config/freddy/env",
}

// RouteFiles are the places a route is resolved FROM. Every field is optional: with none of
// them the resolution is the bench's own -- the provider config under $HOME and the
// provider's own key file.
type RouteFiles struct {
	ConfigPath string // the opencode.json holding the provider blocks
	KeyFile    string // a key file that overrides the provider's own
	Endpoint   string // a base URL that overrides the provider's own (a bench or a test)
	Home       string // the home the two defaults hang under; "" is the real one
}

// Route is one resolved model route: where the call goes, what it is called there, and the
// key it is made with. THE KEY IS NEVER PRINTED; KeySource -- the path it was read from --
// is what a log or a refusal may say.
type Route struct {
	Provider  string
	Model     string // the model id as the endpoint knows it, with no provider prefix
	Endpoint  string // the full chat-completions URL
	Key       string
	KeySource string
	PriceIn   float64 // usd per million input tokens, 0 when the config names no price
	PriceOut  float64 // usd per million output tokens
}

// ResolveRoute turns `provider/model` into the endpoint, the model id and the key.
func ResolveRoute(model string, files RouteFiles) (Route, error) {
	provider, id, ok := strings.Cut(model, "/")
	if !ok || provider == "" || id == "" {
		return Route{}, fmt.Errorf("the model %q has no provider prefix (a route is provider/model, one slash, both sides nonempty)", model)
	}
	home := files.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	route := Route{Provider: provider, Model: id}
	cfgPath := files.ConfigPath
	if cfgPath == "" && home != "" {
		cfgPath = filepath.Join(home, ".config", "opencode", "opencode.json")
	}
	base, key, keySrc, priceIn, priceOut := providerFromConfig(cfgPath, provider, id)
	route.PriceIn, route.PriceOut = priceIn, priceOut
	if base == "" && provider == "opencode" {
		base = zenBaseURL
	}
	if files.Endpoint != "" {
		base = files.Endpoint
	}
	if base == "" {
		return Route{}, fmt.Errorf("the provider %q names no baseURL in %s and this tool knows no default for it; name one with --endpoint",
			provider, dashPath(cfgPath))
	}
	route.Endpoint = strings.TrimSuffix(base, "/")
	if !strings.HasSuffix(route.Endpoint, chatCompletionsPath) {
		route.Endpoint += chatCompletionsPath
	}
	// THE KEY. The file the caller named wins; then the provider's own key file; then the
	// value the config carried (an `{env:NAME}` reference resolves from the environment).
	switch {
	case files.KeyFile != "":
		k, err := readKeyFile(files.KeyFile)
		if err != nil {
			return Route{}, err
		}
		route.Key, route.KeySource = k, files.KeyFile
	default:
		if rel, known := providerKeyFiles[provider]; known && home != "" {
			path := filepath.Join(home, filepath.FromSlash(rel))
			if k, err := readKeyFile(path); err == nil && k != "" {
				route.Key, route.KeySource = k, path
			}
		}
		if route.Key == "" && key != "" {
			route.Key, route.KeySource = key, keySrc
		}
	}
	if route.Key == "" && !strings.Contains(route.Endpoint, "127.0.0.1") && !strings.Contains(route.Endpoint, "localhost") {
		return Route{}, fmt.Errorf("no key for provider %q: neither %s nor the provider's own key file holds one",
			provider, dashPath(cfgPath))
	}
	return route, nil
}

// providerFromConfig reads one provider block out of an opencode.json: its baseURL, its
// apiKey (resolved through `{env:NAME}` when it is a reference), and the model's own cost
// entry when the config carries one. Everything is best-effort: a config this side cannot
// read leaves the caller to --endpoint and a key file, and refuses nothing by itself.
func providerFromConfig(path, provider, model string) (base, key, keySrc string, priceIn, priceOut float64) {
	if path == "" {
		return "", "", "", 0, 0
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", "", "", 0, 0
	}
	var cfg struct {
		Provider map[string]struct {
			Options struct {
				BaseURL string `json:"baseURL"`
				APIKey  string `json:"apiKey"`
			} `json:"options"`
			Models map[string]struct {
				Cost struct {
					Input  float64 `json:"input"`
					Output float64 `json:"output"`
				} `json:"cost"`
			} `json:"models"`
		} `json:"provider"`
	}
	if json.Unmarshal(raw, &cfg) != nil {
		return "", "", "", 0, 0
	}
	entry, named := cfg.Provider[provider]
	if !named {
		return "", "", "", 0, 0
	}
	base = entry.Options.BaseURL
	if k := entry.Options.APIKey; k != "" {
		if name, isEnv := envReference(k); isEnv {
			if v := os.Getenv(name); v != "" {
				key, keySrc = v, "env:"+name
			}
		} else {
			key, keySrc = k, path
		}
	}
	if m, ok := entry.Models[model]; ok {
		priceIn, priceOut = m.Cost.Input, m.Cost.Output
	}
	return base, key, keySrc, priceIn, priceOut
}

// envReference reads the `{env:NAME}` spelling a config uses instead of an inline key.
func envReference(v string) (string, bool) {
	if !strings.HasPrefix(v, "{env:") || !strings.HasSuffix(v, "}") {
		return "", false
	}
	return strings.TrimSpace(v[len("{env:") : len(v)-1]), true
}

// readKeyFile reads a key file AS DATA: a bare key on one line, or the value of the first
// `NAME=value` line. It is never sourced and never printed; a failure names the PATH.
func readKeyFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("the key file %s could not be read: %w", path, err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if _, v, ok := strings.Cut(line, "="); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`), nil
		}
		return line, nil
	}
	return "", fmt.Errorf("the key file %s holds no key", path)
}

func dashPath(p string) string {
	if strings.TrimSpace(p) == "" {
		return Dash
	}
	return p
}

// CallResult is what one model call returned and what it cost, as the ENDPOINT reported it.
// A number the endpoint did not report stays zero and the row prints a dash for it.
type CallResult struct {
	Text       string
	PromptIn   int64
	Completion int64
	Cached     int64
	USD        float64
	Reported   map[string]bool // which numbers the endpoint actually reported
}

// chatTimeout bounds one call. A step is one turn of work, not a session; a call that has
// not answered in this long is a route that is not answering.
const chatTimeout = 10 * time.Minute

// Complete makes ONE chat completion: the system demand, the step's input, no tools, no
// history. The key rides the Authorization header and appears in no log this tool writes.
func (r Route) Complete(ctx context.Context, hc *http.Client, system, user string) (CallResult, error) {
	body, err := json.Marshal(map[string]any{
		"model": r.Model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"stream":      false,
		"temperature": 0,
	})
	if err != nil {
		return CallResult{}, err
	}
	if hc == nil {
		hc = &http.Client{Timeout: chatTimeout}
	}
	ctx, cancel := context.WithTimeout(ctx, chatTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.Endpoint, bytes.NewReader(body))
	if err != nil {
		return CallResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if r.Key != "" {
		req.Header.Set("Authorization", "Bearer "+r.Key)
	}
	resp, err := hc.Do(req)
	if err != nil {
		// The error carries the URL, which carries no key: the key is a header.
		return CallResult{}, fmt.Errorf("the route %s/%s did not answer: %w", r.Provider, r.Model, err)
	}
	defer resp.Body.Close()
	raw, err := readAllBounded(resp.Body, maxAnswerBytes)
	if err != nil {
		return CallResult{}, fmt.Errorf("the route %s/%s answered unreadably: %w", r.Provider, r.Model, err)
	}
	if resp.StatusCode != http.StatusOK {
		return CallResult{}, fmt.Errorf("the route %s/%s answered HTTP %d: %s", r.Provider, r.Model, resp.StatusCode, oneLine(head(string(raw), 200)))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens        *int64 `json:"prompt_tokens"`
			CompletionTokens    *int64 `json:"completion_tokens"`
			PromptTokensDetails *struct {
				CachedTokens *int64 `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			PromptCacheHitTokens *int64   `json:"prompt_cache_hit_tokens"`
			Cost                 *float64 `json:"cost"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return CallResult{}, fmt.Errorf("the route %s/%s answered with no JSON body this tool can read", r.Provider, r.Model)
	}
	if len(out.Choices) == 0 {
		return CallResult{}, fmt.Errorf("the route %s/%s answered with no choices", r.Provider, r.Model)
	}
	res := CallResult{Text: out.Choices[0].Message.Content, Reported: map[string]bool{}}
	if v := out.Usage.PromptTokens; v != nil {
		res.PromptIn, res.Reported["tokens_in"] = *v, true
	}
	if v := out.Usage.CompletionTokens; v != nil {
		res.Completion, res.Reported["tokens_out"] = *v, true
	}
	switch {
	case out.Usage.PromptTokensDetails != nil && out.Usage.PromptTokensDetails.CachedTokens != nil:
		res.Cached, res.Reported["cache_read"] = *out.Usage.PromptTokensDetails.CachedTokens, true
	case out.Usage.PromptCacheHitTokens != nil:
		res.Cached, res.Reported["cache_read"] = *out.Usage.PromptCacheHitTokens, true
	}
	switch {
	case out.Usage.Cost != nil:
		res.USD, res.Reported["usd"] = *out.Usage.Cost, true
	case r.PriceIn > 0 || r.PriceOut > 0:
		res.USD = (float64(res.PromptIn)*r.PriceIn + float64(res.Completion)*r.PriceOut) / 1e6
		res.Reported["usd"] = true
	}
	return res, nil
}

// maxAnswerBytes bounds one answer: a step's artifact is a patch or a result, and a body
// larger than this is a route that is not answering the question asked.
const maxAnswerBytes = 8 << 20

// readAllBounded reads at most n bytes and says so rather than growing without a ceiling.
func readAllBounded(r interface{ Read([]byte) (int, error) }, n int64) ([]byte, error) {
	var buf bytes.Buffer
	_, err := buf.ReadFrom(&boundedReader{r: r, left: n})
	return buf.Bytes(), err
}

type boundedReader struct {
	r    interface{ Read([]byte) (int, error) }
	left int64
}

func (b *boundedReader) Read(p []byte) (int, error) {
	if b.left <= 0 {
		return 0, fmt.Errorf("the answer is larger than %d bytes", maxAnswerBytes)
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.r.Read(p)
	b.left -= int64(n)
	return n, err
}

func head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
