package swarm

import (
	"encoding/json"
	"regexp"
	"time"
)

// THE PROVIDER SOCKET IS NOT THE CARD'S IDLE WINDOW.
//
// OpenCode's fetch sets timeout: false. headerTimeout bounds time-to-headers.
// chunkTimeout bounds silence between SSE chunks, and it is armed only when the
// job's config sets it. With it unset, a socket that has already accepted the
// request and then sends nothing blocks until nova-swarm's --idle (300s) kills
// the card. That is the 18-of-55 hang (#2535).
//
// These two deadlines sit on the job's own opencode.json, the copy native writes
// for the child. The person's config is not edited. --idle stays 300s: a silent
// go test whose CPU is moving, and a model turn that is only thinking, are not
// this socket.
//
// A read that ends after the request may have been accepted is UNKNOWN. It is
// not a launch failure and it does not earn another launch. A known failure
// before the provider begins the work stays on the existing grace retry
// (ProviderLaunchFailure). stella-6b51d37c8d7d.

// ProviderHeaderTimeout is how long the socket may sit before response headers.
// It is under the 90s a black-holed attempt may cost the card.
const ProviderHeaderTimeout = 45 * time.Second

// ProviderChunkTimeout is how long the socket may sit between SSE chunks.
// Same bound: one attempt ends in under 90s. A second launch is not started
// from this timeout.
const ProviderChunkTimeout = 45 * time.Second

// providerReadBudget is the card's allowance for one black-holed attempt.
const providerReadBudget = 90 * time.Second

// lostResponseRE is the harness's own words for a read that died after the
// request was submitted. OpenCode prints these; a card's prose is not asked
// to avoid the words, and the classifier is applied only to the attempt's
// own new output.
var lostResponseRE = regexp.MustCompile(`(?i)SSE read timed out|headers timed out|Headers Timeout Error|HeadersTimeoutError`)

// LostResponse reports that this attempt's output is a provider read that
// ended after the request may have been accepted. The caller must not launch
// the card again on that signal.
func LostResponse(tail []byte) bool {
	return lostResponseRE.Match(tail)
}

// ApplyProviderReadDeadline returns the job config with the model's provider
// carrying headerTimeout and chunkTimeout, in milliseconds. A body that is
// not a JSON object is returned unchanged. An absent provider entry is
// created with only those two options, so a built-in provider still receives
// the deadline. An apiKey already in the options is left as it was.
func ApplyProviderReadDeadline(raw []byte, provider string) []byte {
	if provider == "" || ProviderHeaderTimeout >= providerReadBudget || ProviderChunkTimeout >= providerReadBudget {
		return raw
	}
	cfg := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return raw
		}
	}
	providers, _ := cfg["provider"].(map[string]any)
	if providers == nil {
		providers = map[string]any{}
	}
	entry, _ := providers[provider].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
	}
	opts, _ := entry["options"].(map[string]any)
	if opts == nil {
		opts = map[string]any{}
	}
	opts["headerTimeout"] = int(ProviderHeaderTimeout / time.Millisecond)
	opts["chunkTimeout"] = int(ProviderChunkTimeout / time.Millisecond)
	entry["options"] = opts
	providers[provider] = entry
	cfg["provider"] = providers
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return raw
	}
	return append(out, '\n')
}
