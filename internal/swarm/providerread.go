package swarm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// THE PROVIDER SOCKET IS NOT THE CARD'S IDLE WINDOW.
//
// A response that has sent its headers and then no body bytes for
// ProviderBodySilence is UNKNOWN. The timer is readWithinSilence, on the
// localhost proxy this process owns (providerproxy.go). The card is not
// launched again. A body that arrives inside the gap is success.
//
// headerTimeout and chunkTimeout are still written into the job's own
// opencode.json. They are not this deadline. OpenCode 1.18.20 was measured
// at one POST /v1/responses with both set to 2000 and was still running at
// 50.5s with no timeout line. --idle stays 300s: a silent go test whose CPU
// is moving, and a model turn that is only thinking, are not this socket.
//
// A known failure before the provider begins the work stays on the existing
// grace retry (ProviderLaunchFailure). stella-6b51d37c8d7d.

// ProviderHeaderTimeout is written into the job config as headerTimeout, in
// milliseconds. It is not the body-read deadline. It stays under 90s so a
// harness that does honor it cannot outlive the body-read budget.
const ProviderHeaderTimeout = 45 * time.Second

// ProviderChunkTimeout is written into the job config as chunkTimeout, in
// milliseconds. Same limit as the header option, and the same non-claim:
// setting it was measured not to end the stall.
const ProviderChunkTimeout = 45 * time.Second

// ProviderBodySilence is how long the response body may sit with no bytes
// after headers have arrived. The proxy enforces it. A stream that keeps
// sending is not cut off. Silence past this is UNKNOWN and the card is not
// launched again. It is under the 90s a black-holed attempt may cost.
const ProviderBodySilence = 45 * time.Second

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

// AcceptanceUnknown reports that this job's provider read may have been
// accepted and then lost. The marker file is the record. A marker that cannot
// be read is the same hold: a missing record must not become an ordinary
// retry. The runner's own verdict line is the second record, for the run
// whose marker could not be written.
func AcceptanceUnknown(job string) bool {
	if job == "" {
		return false
	}
	_, err := os.ReadFile(filepath.Join(job, "provider-acceptance"))
	if err == nil {
		// Any present marker is a hold. A word in the file, including
		// "reconciled", is not reconciliation. A reconciler removes the
		// marker. Empty and unrecognized text stay holds.
		return true
	}
	if !os.IsNotExist(err) {
		return true
	}
	for _, name := range []string{"harness.log", "harness-output.log"} {
		logRaw, lerr := os.ReadFile(filepath.Join(job, name))
		if lerr != nil {
			continue
		}
		if strings.Contains(string(logRaw), "why=unknown-acceptance") {
			return true
		}
	}
	return false
}

// ApplyProviderReadDeadline returns the job config with the model's provider
// carrying headerTimeout and chunkTimeout, in milliseconds. Those options are
// not the body-read deadline; see ProviderBodySilence. A body that is not a
// JSON object is returned unchanged. An absent provider entry is created with
// only those two options, so a built-in provider still receives them. An
// apiKey already in the options is left as it was.
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
