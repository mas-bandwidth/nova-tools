package friend

import (
	"slices"
	"sort"
	"strings"
	"time"
)

// A friend whose provider key is not in her environment must read down with the reason,
// never up with lanes that burn turns into the provider's refusal, and never a silent
// restart loop (Glenn 2026-10-08: Alex's key is not in the secrets store; he must read
// truthfully DOWN with the reason "no key sealed", never a fake up). The names the daemon
// needs are `run --needs-env NAME[,NAME]`, else the provider's key as ProviderKeyEnv names
// it for `--model provider/model`; MissingEnv is the ones unset at a step, and while any is,
// the lanes open nothing and start nothing, and her beat says her down with the reason
// (NeedsEnvReason), withdrawn on the beat after the key appears (docs/SPEC-FRIEND.md,
// one-shot lanes, "no key sealed").

// providerKeyEnv is the environment variable each provider's key lives in, by the provider
// of a provider/model name; a provider not here needs --needs-env to be named.
var providerKeyEnv = map[string]string{
	"inception":  "INCEPTION_API_KEY",
	"deepseek":   "DEEPSEEK_API_KEY",
	"openai":     "OPENAI_API_KEY",
	"anthropic":  "ANTHROPIC_API_KEY",
	"google":     "GEMINI_API_KEY",
	"openrouter": "OPENROUTER_API_KEY",
	"xai":        "XAI_API_KEY",
}

// ProviderKeyEnv is the environment variable the key of model's provider lives in
// (model is provider/model), "" when the provider is unknown or model names none.
func ProviderKeyEnv(model string) string {
	provider, _, found := strings.Cut(model, "/")
	if !found {
		return ""
	}
	return providerKeyEnv[strings.ToLower(provider)]
}

// NeedsEnvOf is the names a daemon needs in its environment: the --needs-env flag's
// (comma-separated), else the provider's key for --model; sorted, no duplicates, no blanks.
func NeedsEnvOf(flag, model string) []string {
	var names []string
	for _, n := range strings.Split(flag, ",") {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		if k := ProviderKeyEnv(model); k != "" {
			names = append(names, k)
		}
	}
	sort.Strings(names)
	return slices.Compact(names)
}

// MissingEnv is the names of needs that getenv answers empty for, in order.
func MissingEnv(needs []string, getenv func(string) string) []string {
	var missing []string
	for _, n := range needs {
		if getenv == nil || getenv(n) == "" {
			missing = append(missing, n)
		}
	}
	return missing
}

// NeedsEnvReason is the beat's reason while names are missing: "no key sealed: NAME" (every
// name, comma-separated), the words the owner asked for.
func NeedsEnvReason(missing []string) string {
	return "no key sealed: " + strings.Join(missing, ",")
}

// NeedsEnvBeatAhead is how far ahead the down beat sets its --until while a key is missing:
// sent again each beat, so it never lapses while the key is missing and lapses within it
// once the key appears (the daemon re-reads its environment each step).
const NeedsEnvBeatAhead = 3 * BeatEvery

// NeedsEnvHoldText is the one judgment the seat is told when a friend's key is missing.
func NeedsEnvHoldText(friend string, missing []string, at time.Time) (subject, body string) {
	return "friend " + friend + " down: " + NeedsEnvReason(missing),
		"The daemon of " + friend + " started at " + at.UTC().Format(time.RFC3339) + " without " + strings.Join(missing, ", ") + " in its environment: no key is sealed for her provider. Her lanes open nothing and start nothing, and her beat says her down with this reason until the key is sealed and the daemon sees it (nova-secrets seal, then the agent's wrap opens it; the daemon re-reads its environment every step).\n"
}
