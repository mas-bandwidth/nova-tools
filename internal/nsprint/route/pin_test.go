package route

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// TestOpenRouterRequestPinsProvider is #3151's DONE-WHEN, half one: the
// request body of every or* route that can run carries the provider block
// with the route's pin as the only provider and allow_fallbacks false, and
// parse refuses an openrouter route that can run without a pin.
func TestOpenRouterRequestPinsProvider(t *testing.T) {
	tab, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	pinned := 0
	for _, r := range tab.Rows() {
		raw, ok := r.HarnessConfig()
		if r.Via != ViaOpenRouter {
			if ok {
				t.Fatalf("route %s via %s has an OpenRouter harness config", r.Route, r.Via)
			}
			continue
		}
		if r.State == Dropped && r.Pin == "" {
			continue
		}
		if !ok {
			t.Fatalf("openrouter route %s (%s) has no harness config", r.Route, r.State)
		}
		var cfg struct {
			Provider map[string]struct {
				Options map[string]any `json:"options"`
				Models  map[string]struct {
					Options struct {
						Provider map[string]any `json:"provider"`
					} `json:"options"`
				} `json:"models"`
			} `json:"provider"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatalf("route %s: config is not JSON: %v", r.Route, err)
		}
		or := cfg.Provider["openrouter"]
		if or.Options["apiKey"] != "{env:OPENROUTER_API_KEY}" {
			t.Fatalf("route %s: apiKey %v, want the variable's name only", r.Route, or.Options["apiKey"])
		}
		block := or.Models[r.Model].Options.Provider
		if block == nil {
			t.Fatalf("route %s: model %s carries no provider block:\n%s", r.Route, r.Model, raw)
		}
		if fb, ok := block["allow_fallbacks"].(bool); !ok || fb {
			t.Fatalf("route %s: allow_fallbacks %v, want false", r.Route, block["allow_fallbacks"])
		}
		order, _ := block["order"].([]any)
		if len(order) != 1 || order[0] != r.Pin {
			t.Fatalf("route %s: order %v, want [%s]", r.Route, block["order"], r.Pin)
		}
		pinned++
	}
	if pinned == 0 {
		t.Fatal("no openrouter route in the table carries a pin")
	}

	base := "routes:\n  - route: a\n    rung: flash\n    model: m/a\n    via: %s\n    state: held\n    why: \"x\"\n"
	for name, tc := range map[string]struct{ src, want string }{
		"held without pin":   {strings.Replace(base, "%s", "openrouter", 1), "needs pin"},
		"pin off openrouter": {strings.Replace(base, "%s", "opencode", 1) + "    pin: alibaba\n", "only an openrouter route"},
		"two providers":      {strings.Replace(base, "%s", "openrouter", 1) + "    pin: \"a b\"\n", "one provider slug"},
	} {
		if _, err := Parse([]byte(tc.src)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err %v, want %q", name, err, tc.want)
		}
	}
	dropped := strings.Replace(strings.Replace(base, "%s", "openrouter", 1), "state: held", "state: dropped", 1)
	if _, err := Parse([]byte(dropped)); err != nil {
		t.Fatalf("a dropped openrouter route never runs and needs no pin: %v", err)
	}
}

// TestUsageRowCarriesProvider is #3151's DONE-WHEN, half two: the usage row
// of an openrouter call names the upstream that served it, read from the
// route's own harness config, and the writer refuses a row that names none.
func TestUsageRowCarriesProvider(t *testing.T) {
	tab, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	var r Row
	for _, row := range tab.Rows() {
		if row.Via == ViaOpenRouter && row.State == Allowed {
			r = row
			break
		}
	}
	raw, ok := r.HarnessConfig()
	if !ok {
		t.Fatalf("allowed openrouter route %q has no harness config", r.Route)
	}
	if got := swarm.Upstream(raw, "openrouter", r.Model); got != r.Pin {
		t.Fatalf("upstream from %s's config = %q, want its pin %q", r.Route, got, r.Pin)
	}
	if got := swarm.Upstream(nil, "openrouter", r.Model); got != swarm.Unpinned {
		t.Fatalf("upstream with no config = %q, want %q", got, swarm.Unpinned)
	}
	if got := swarm.Upstream(nil, "deepseek", "deepseek-flash"); got != "deepseek" {
		t.Fatalf("a direct provider's upstream = %q, want itself", got)
	}

	dir := t.TempDir()
	row := swarm.UsageRow{"job": "c1", "attempt": "1", "provider": "openrouter", "model": r.Model}
	for _, write := range []func(string, swarm.UsageRow) error{swarm.AppendCardUsage, swarm.WriteCardUsage} {
		path := filepath.Join(dir, "refused.tsv")
		if err := write(path, row); err == nil || !strings.Contains(err.Error(), "no upstream") {
			t.Fatalf("an openrouter row with no upstream was not refused: %v", err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("the refused row left a file: %v", err)
		}
	}

	row["upstream"] = swarm.Upstream(raw, "openrouter", r.Model)
	path := filepath.Join(dir, "usage.tsv")
	if err := swarm.AppendCardUsage(path, row); err != nil {
		t.Fatal(err)
	}
	if err := swarm.AppendCardUsage(path, swarm.UsageRow{"job": "c2", "provider": "deepseek", "model": "deepseek-flash"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	head := strings.Split(lines[0], "\t")
	col := -1
	for i, h := range head {
		if h == "upstream" {
			col = i
		}
	}
	if col < 0 || len(lines) != 3 {
		t.Fatalf("usage.tsv has no upstream column or not two rows:\n%s", data)
	}
	if got := strings.Split(lines[1], "\t")[col]; got != r.Pin {
		t.Fatalf("openrouter row upstream %q, want %q", got, r.Pin)
	}
	if got := strings.Split(lines[2], "\t")[col]; got != "deepseek" {
		t.Fatalf("direct row upstream %q, want deepseek", got)
	}
}
