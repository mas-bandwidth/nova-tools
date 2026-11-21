package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// providers is a store with a machine, an openai provider p1 and an anthropic provider p2.
func providers(t *testing.T) *harness {
	h := newHarness()
	h.ok(t, "machine", "add", "bench-a", "--user", "nova", "--actor", "t")
	h.ok(t, "provider", "add", "p1", "--url", "https://api.example.com", "--api", "openai", "--key", "P1_API_KEY",
		"--models_url", "https://api.example.com/models", "--concurrency", "4", "--actor", "t")
	h.ok(t, "provider", "add", "p2", "--url", "http://bench-a.tail:8080/v1", "--api", "anthropic", "--actor", "t")
	return h
}

func TestProviderRows(t *testing.T) {
	t.Parallel()
	h := providers(t)
	assert.Equal(t, "PROVIDER name=p1 url=https://api.example.com api=openai key=P1_API_KEY models_url=https://api.example.com/models balance_url=- concurrency=4 note=- enabled=true\n"+
		"PROVIDER name=p2 url=http://bench-a.tail:8080/v1 api=anthropic key=- models_url=- balance_url=- concurrency=0 note=- enabled=true\n"+
		"CONFIG LIST kind=provider rows=2\n", h.ok(t, "provider", "list"), "no key and no cap are the defaults")
	assert.Contains(t, h.ok(t, "provider", "show", "p2", "--json"), `"fields":{"name":"p2","url":"http://bench-a.tail:8080/v1","api":"anthropic","key":"","models_url":"","balance_url":"","concurrency":0,"note":"","enabled":true,"created":`)
	assert.Equal(t, "CONFIG SET kind=provider name=p1 rev=4 changed=balance_url,enabled\n", h.ok(t, "provider", "set", "p1", "--balance_url", "https://api.example.com/user/balance", "--enabled", "false", "--actor", "a1"))
	assert.Equal(t, "https://api.example.com/user/balance", h.st.history[3].after["balance_url"])
	assert.Contains(t, h.ok(t, "history", "provider", "p1"), "  balance_url: \"\" -> \"https://api.example.com/user/balance\"\n")
	assert.Equal(t, "CONFIG DRY-RUN op=add kind=provider name=p3 actor=a1 wrote=nothing\n  url: \"https://p3.example.com\"\n  api: \"openai\"\n  key: \"\"\n  models_url: \"\"\n  balance_url: \"\"\n  concurrency: 0\n  note: \"\"\n  enabled: true\n",
		h.ok(t, "provider", "add", "p3", "--url", "https://p3.example.com", "--api", "openai", "--actor", "a1", "--dry-run"))
	assert.NotContains(t, h.st.rows["provider"], "p3")
}

// A model is named <provider>/<model id>, the id as its harness names it; its harness defaults to opencode; a price
// left out is unpriced (null), never 0, and default unsets it again.
func TestModelRows(t *testing.T) {
	t.Parallel()
	h := providers(t)
	assert.Equal(t, "CONFIG ADD kind=model name=p1/org/m-1.5:q4 rev=4\n", h.ok(t, "model", "add", "p1/org/m-1.5:q4", "--actor", "t"))
	h.ok(t, "model", "add", "p2/m_2", "--harness", "claude", "--price_in", "3000000", "--price_out", "0", "--machine", "bench-a", "--note", "local", "--actor", "t")
	assert.Equal(t, "MODEL name=p1/org/m-1.5:q4 harness=opencode price_in=- price_out=- machine=- note=- enabled=true\n"+
		`MODEL name=p2/m_2 harness=claude price_in=3000000 price_out=0 machine=bench-a note="local" enabled=true`+"\n"+
		"CONFIG LIST kind=model rows=2\n", h.ok(t, "model", "list"))
	assert.Contains(t, h.ok(t, "model", "list", "--json"), `{"name":"p1/org/m-1.5:q4","harness":"opencode","price_in":null,"price_out":null,"machine":"","note":"","enabled":true}`)
	assert.Contains(t, h.ok(t, "model", "show", "p2/m_2", "--json"), `"price_in":3000000,"price_out":0,"machine":"bench-a"`, "0 is free, and prints as a number")
	h.ok(t, "model", "set", "p2/m_2", "--price_in", "default", "--machine", "", "--actor", "t")
	assert.Equal(t, "", h.st.rows["model"]["p2/m_2"].F["price_in"], "default unsets a price")
	assert.Equal(t, "", h.st.rows["model"]["p2/m_2"].F["machine"])
	assert.Contains(t, h.ok(t, "history", "model", "p2/m_2"), "kind=model name=p2/m_2 op=set")
	assert.Equal(t, "CONFIG REMOVE kind=model name=p2/m_2 rev=7\n", h.ok(t, "model", "remove", "p2/m_2", "--actor", "t"))
	assert.Contains(t, h.ok(t, "model", "add", "-h"), "[--harness <opencode|claude|codex>] [--price_in <micro-usd-per-mtok|default>]")
}

// A model names a provider that exists, a harness that speaks the provider's api, and a machine that exists; a
// provider keeps the api its models' harnesses speak and goes, like a machine serving a model, only once no model
// names it. Each refusal names the fix and writes nothing.
func TestModelAndProviderRefusals(t *testing.T) {
	t.Parallel()
	h := providers(t)
	h.ok(t, "model", "add", "p1/m-1", "--machine", "bench-a", "--actor", "t")
	n := len(h.st.history)
	for _, c := range []struct {
		code int
		args []string
		want string
	}{
		{1, []string{"model", "add", "p9/m-1"}, "nova-config model add: model p9/m-1: no provider p9; the providers are p1,p2; run: nova-config provider add p9 --url <url> --api <openai|anthropic> --actor <you>\n"},
		{1, []string{"model", "add", "p1/m-2", "--harness", "claude"}, "nova-config model add: model p1/m-2: harness claude speaks anthropic and provider p1 speaks openai; give --harness opencode or codex; run: nova-config model add p1/m-2 --harness opencode --actor <you>\n"},
		{1, []string{"model", "add", "p2/m-2"}, "model p2/m-2: harness opencode speaks openai and provider p2 speaks anthropic; give --harness claude"},
		{1, []string{"model", "set", "p1/m-1", "--harness", "claude"}, "model p1/m-1: harness claude speaks anthropic and provider p1 speaks openai"},
		{1, []string{"model", "add", "p1/m-3", "--machine", "bench-z"}, "nova-config model add: model p1/m-3: no machine bench-z; the machines are bench-a; run: nova-config machine add bench-z --user <user>\n"},
		{1, []string{"provider", "remove", "p1"}, "nova-config provider remove: provider p1 is still named by models p1/m-1; remove them first; run: nova-config model remove p1/m-1 --actor <you>\n"},
		{1, []string{"provider", "set", "p1", "--api", "anthropic"}, "nova-config provider set: provider p1 keeps api openai while models p1/m-1 run on it with a harness that speaks openai; remove them first; run: nova-config model remove p1/m-1 --actor <you>\n"},
		{1, []string{"machine", "remove", "bench-a"}, "nova-config machine remove: machine bench-a still serves models p1/m-1; clear their machine first; run: nova-config model set p1/m-1 --machine '' --actor <you>\n"},
		{1, []string{"model", "add", "p1/m-1"}, "model p1/m-1 exists; use: nova-config model set p1/m-1"},
		{1, []string{"model", "set", "p1/m-9", "--note", "n"}, "model p1/m-9 not found; run: nova-config model list"},
		{2, []string{"model", "add", "P1/m"}, `name "P1/m": want <provider>/<model id>`},
		{2, []string{"model", "add", "p1"}, `name "p1": want <provider>/<model id>`},
		{2, []string{"model", "add", "p1/"}, `name "p1/": want <provider>/<model id>`},
		{2, []string{"model", "add", "p1//m"}, `name "p1//m": want <provider>/<model id>`},
		{2, []string{"model", "add", "p1/m@x"}, `name "p1/m@x": want <provider>/<model id>`},
		{2, []string{"model", "add", "p1/m-4", "--harness", "aider"}, `--harness "aider": want one of opencode|claude|codex`},
		{2, []string{"model", "set", "p1/m-1", "--harness", ""}, `--harness: want one of opencode|claude|codex`},
		{2, []string{"model", "add", "p1/m-4", "--price_in", "-1"}, `--price_in "-1": want a non-negative integer`},
		{2, []string{"model", "add", "p1/m-4", "--price_out", "1.5"}, `--price_out "1.5": want a non-negative integer`},
		{2, []string{"provider", "add", "p3", "--url", "https://p3.example.com"}, "missing required flag --api; provider fields: name, url, api, key, models_url, balance_url, concurrency, note, enabled; run: nova-config provider add -h"},
		{2, []string{"provider", "add", "p3", "--url", "https://p3.example.com", "--api", "grpc"}, `--api "grpc": want one of openai|anthropic`},
		{2, []string{"provider", "add", "P_3", "--url", "https://p3.example.com", "--api", "openai"}, "want lower-case letters, digits and dashes"},
		// a refused url or key is never quoted back: it may carry a password or be a key's value
		{2, []string{"provider", "add", "p3", "--url", "ftp://p3.example.com", "--api", "openai"}, `--url: want an http or https URL with a host and no user, query or fragment, such as https://api.example.com`},
		{2, []string{"provider", "add", "p3", "--url", "api.example.com", "--api", "openai"}, `--url: want an http or https URL`},
		{2, []string{"provider", "add", "p3", "--url", "https://u:sk-live-pw@p3.example.com", "--api", "openai"}, `--url: want an http or https URL with a host and no user`},
		{2, []string{"provider", "set", "p1", "--models_url", "https://api.example.com/models?key=sk-live-q"}, `--models_url: want an http or https URL`},
		{2, []string{"provider", "set", "p1", "--balance_url", "https://api.example.com/a b"}, `--balance_url: want an http or https URL`},
		{2, []string{"provider", "set", "p1", "--url", ""}, "--url: want a value"},
		{2, []string{"provider", "set", "p1", "--key", "sk-live-1234"}, `--key: want the name of the secret that holds it (letters, digits and underscores, not starting with a digit), never its value`},
		{2, []string{"provider", "set", "p1", "--concurrency", "-2"}, `--concurrency "-2": want a non-negative integer`},
	} {
		code, out, errs := h.run(t, append(c.args, "--actor", "a")...)
		assert.Equal(t, c.code, code, "%v: %s", c.args, errs)
		assert.Empty(t, out, "%v", c.args)
		assert.Contains(t, errs, c.want, "%v", c.args)
		assert.NotContains(t, errs, "sk-live", "%v: a refusal never echoes what may be a secret", c.args)
	}
	assert.Len(t, h.st.history, n, "no refused write was recorded")
	assert.NotContains(t, h.ok(t, "provider", "list"), "sk-live", "a refused key value is never stored")

	h.ok(t, "model", "set", "p1/m-1", "--harness", "codex", "--machine", "", "--actor", "t")
	h.ok(t, "machine", "remove", "bench-a", "--actor", "t")
	h.ok(t, "model", "remove", "p1/m-1", "--actor", "t")
	h.ok(t, "provider", "set", "p1", "--api", "anthropic", "--actor", "t")
	h.ok(t, "provider", "remove", "p1", "--actor", "t")
	require.Empty(t, h.st.rows["model"])
}

// A machine's routes are the devices or tools it has that a piece of work may need: a sorted comma list of names.
func TestMachineRoutes(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.ok(t, "machine", "add", "bench-a", "--user", "nova", "--routes", "usb-hub, gpu,gpu", "--actor", "t")
	assert.Equal(t, "gpu,usb-hub", h.st.rows["machine"]["bench-a"].F["routes"])
	assert.Contains(t, h.ok(t, "machine", "show", "bench-a"), " roles=bench routes=gpu,usb-hub runners=0 ")
	assert.Contains(t, h.ok(t, "machine", "show", "bench-a", "--json"), `"roles":"bench","routes":"gpu,usb-hub","runners":0`)
	code, _, errs := h.run(t, "machine", "set", "bench-a", "--routes", "gpu,route:quest", "--actor", "t")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, `--routes "route:quest": want a comma list of words matching ^[a-z0-9][a-z0-9-]*$`)
	h.ok(t, "machine", "set", "bench-a", "--routes", "", "--actor", "t")
	assert.Equal(t, "", h.st.rows["machine"]["bench-a"].F["routes"], "'' is none")
	assert.NotContains(t, h.ok(t, "inventory", "--host", "bench-a"), "routes", "no play reads routes")
}
