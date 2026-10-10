package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/provbalance"
)

// The providers check: each nova-config route names a provider and model, and the friends
// and the native runs spend through them (internal/sprint/provider_funds.go: a provider
// out of funds is never a mystery failure). Per enabled route the check holds three
// facts: the provider's key is in the seat's secrets store by name, the provider answers
// a no-cost call (its models list) through a fakeable client, and the funds the provider
// reports, where it reports them, are over zero. A disabled route is listed with its note
// and is never checked (docs/SPEC-DOCTOR.md, "providers").
func init() {
	Default.Register(Check{
		Name:       "providers",
		Dependency: "the model providers and their routes",
		Fleet:      true,
		Run:        checkProviders,
	})
}

// providersTimeout bounds one route's exec and one provider's call, so a provider that
// does not answer leaves the route judged, never the check hung.
const providersTimeout = 15 * time.Second

// providerKeys is the environment variable NAME each provider's key is sealed under in
// the seat's secrets store (docs/MODELS.md, "The routes"; pkg/provbalance.KeyEnv for
// openrouter). A provider not named here keeps its key elsewhere (opencode's login file)
// or needs none (ollama on the loopback), so the check does not hold it to a sealed name.
var providerKeys = map[string]string{
	"openrouter": "OPENROUTER_API_KEY",
	"deepseek":   "DEEPSEEK_API_KEY",
}

// modelsURL is the no-cost models list each provider answers, read with the key when one
// is sealed. A provider not named here has no known endpoint: the check lists it and
// checks no further (the harness's own login answers it).
var modelsURL = map[string]string{
	"openrouter": "https://openrouter.ai/api/v1/models",
	"deepseek":   "https://api.deepseek.com/models",
	"ollama":     "http://127.0.0.1:11434/api/tags",
}

// errNoModels says a provider has no models endpoint the check knows, so no call is made.
var errNoModels = errors.New("no models endpoint is known for this provider")

// ProviderProbe is the outside call the providers check makes: the provider's no-cost
// models list and the funds it reports. Env may carry one (a test's fake); otherwise the
// check builds the real HTTP probe, so no test opens a socket.
type ProviderProbe interface {
	// Models asks the provider for its models list with key, a call that costs nothing.
	// errNoModels says the provider has no endpoint the probe knows.
	Models(ctx context.Context, provider, key string) error
	// Funds is the dollars the provider reports left, and whether it reports any; a
	// provider that reports none is not judged on its funds.
	Funds(ctx context.Context, provider, key string) (dollars float64, known bool, err error)
}

// providerProbeFor is the probe a check uses: the one Env carries, or the real HTTP probe.
func providerProbeFor(env Env) ProviderProbe {
	if p, ok := env.(ProviderProbe); ok {
		return p
	}
	return httpProbe{}
}

// httpProbe is the real provider client: the models list over HTTP and the balance
// through pkg/provbalance, the sprint's own balance read.
type httpProbe struct{}

func (p httpProbe) Models(ctx context.Context, provider, key string) error {
	url, ok := modelsURL[provider]
	if !ok {
		return errNoModels
	}
	ctx, cancel := context.WithTimeout(ctx, providersTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }() // ignored: the status is all the caller reads
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GET %s answered %d", url, resp.StatusCode)
	}
	return nil
}

func (p httpProbe) Funds(ctx context.Context, provider, key string) (float64, bool, error) {
	r := provbalance.Read(ctx, nil, provider, func(string) string { return key })
	if !r.Known {
		return 0, false, nil
	}
	return r.Balance, true, nil
}

// providerRoute is one nova-config route row as the check reads it.
type providerRoute struct {
	Name     string
	Provider string
	Model    string
	Note     string
	Enabled  bool
}

// checkProviders reads the routes (nova-config route list --json) and the seat's secrets
// store names, then holds every enabled route to its key, its models call and its funds.
// The evidence names every route that would fail; a disabled route is listed with its note.
func checkProviders(ctx context.Context, env Env) Result {
	store, seat := env.Getenv("NOVA_SECRETS_STORE"), env.Getenv("NOVA_SECRETS_SEAT")
	names, err := secretNames(env)
	if err != nil {
		return Result{Status: Fail,
			Evidence: "the seat's secrets store could not be read: " + err.Error(),
			Fix:      "nova-secrets names --store <dir> --as <seat> (nova-up --local writes the seat's store and its environment)"}
	}
	routes, err := providersRoutes(ctx, env)
	if err != nil {
		return Result{Status: Fail,
			Evidence: "the routes could not be read: " + err.Error(),
			Fix:      "nova-config route list --json"}
	}
	if len(routes) == 0 {
		return Result{Status: OK, Evidence: "no route names a provider; nothing to check"}
	}
	probe := providerProbeFor(env)
	var problems, notes []string
	firstFix := ""
	checked := 0
	for _, r := range routes {
		if !r.Enabled {
			notes = append(notes, "disabled "+r.Name+": "+orNoNote(r.Note))
			continue
		}
		checked++
		keyName := providerKeys[r.Provider]
		key := ""
		if keyName != "" {
			if !names[keyName] {
				firstFix = orFix(firstFix, "nova-secrets seal --store "+store+" --as "+seat+" --name "+keyName)
				problems = append(problems, "route "+r.Name+": provider "+r.Provider+"'s key "+keyName+" is not in the seat's secrets store")
				continue
			}
			key = env.Getenv(keyName)
		}
		switch err := probe.Models(ctx, r.Provider, key); {
		case errors.Is(err, errNoModels):
			notes = append(notes, "provider "+r.Provider+" has no known models endpoint; route "+r.Name+" is listed, not checked")
			continue
		case err != nil:
			firstFix = orFix(firstFix, "check the network to "+r.Provider+", or run nova-doctor under the seat's secrets when the provider reads a sealed key (nova-secrets exec --only <NAME> -- nova-doctor run)")
			problems = append(problems, "route "+r.Name+": provider "+r.Provider+" did not answer its models list: "+err.Error())
			continue
		}
		switch dollars, known, err := probe.Funds(ctx, r.Provider, key); {
		case err != nil:
			firstFix = orFix(firstFix, "nova-sprint funded "+r.Provider+" --reason '<the payment>'")
			problems = append(problems, "route "+r.Name+": provider "+r.Provider+"'s funds could not be read: "+err.Error())
		case known && dollars <= 0:
			firstFix = orFix(firstFix, "nova-sprint funded "+r.Provider+" --reason '<the payment>'")
			problems = append(problems, fmt.Sprintf("route %s: provider %s is out of funds ($%.2f)", r.Name, r.Provider, dollars))
		}
	}
	if len(problems) == 0 {
		ev := fmt.Sprintf("%d enabled route(s) ok", checked)
		if len(notes) > 0 {
			ev += "; " + strings.Join(notes, "; ")
		}
		return Result{Status: OK, Evidence: ev}
	}
	ev := strings.Join(problems, "; ")
	if len(notes) > 0 {
		ev += "; " + strings.Join(notes, "; ")
	}
	return Result{Status: Fail, Evidence: ev, Fix: firstFix}
}

// providersRoutes reads nova-config's route rows: `nova-config route list --json`, the
// rows with their note (a disabled route carries one), through the one exec seam.
func providersRoutes(ctx context.Context, env Env) ([]providerRoute, error) {
	ctx, cancel := context.WithTimeout(ctx, providersTimeout)
	defer cancel()
	out, err := env.Exec(ctx, "nova-config", "route", "list", "--json")
	if err != nil {
		return nil, err
	}
	// Every field of a `route list --json` item is a string (pkg/tool's envelope
	// carries the config row's map[string]string: `"enabled":"true"`), so enabled is read
	// as the word the store holds and compared, never as a JSON boolean.
	var wire struct {
		Items []struct {
			Kind   string `json:"kind"`
			Fields struct {
				Name     string `json:"name"`
				Provider string `json:"provider"`
				Model    string `json:"model"`
				Note     string `json:"note"`
				Enabled  string `json:"enabled"`
			} `json:"fields"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &wire); err != nil {
		return nil, err
	}
	routes := make([]providerRoute, 0, len(wire.Items))
	for _, it := range wire.Items {
		if it.Kind != "" && it.Kind != "route" {
			continue
		}
		f := it.Fields
		routes = append(routes, providerRoute{Name: f.Name, Provider: f.Provider, Model: f.Model, Note: f.Note, Enabled: f.Enabled == "true"})
	}
	return routes, nil
}

// secretNames reads the top-level key names of the seat's file in the secrets store and
// never a value (pkg/secrets, names). It reads the file through Env, so a test roots
// it in t.TempDir().
func secretNames(env Env) (map[string]bool, error) {
	store, seat := env.Getenv("NOVA_SECRETS_STORE"), env.Getenv("NOVA_SECRETS_SEAT")
	if store == "" || seat == "" {
		return nil, errors.New("NOVA_SECRETS_STORE and NOVA_SECRETS_SEAT do not name the seat's store")
	}
	b, err := env.ReadFile(strings.TrimSuffix(store, "/") + "/" + seat + ".yaml")
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		if line == "" || line[0] == ' ' || line[0] == '\t' || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "sops:") {
			break
		}
		name, _, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if name = strings.TrimSpace(name); name != "" {
			names[name] = true
		}
	}
	return names, nil
}

// orNoNote is a route's note, or the words a disabled route with none prints.
func orNoNote(note string) string {
	if note == "" {
		return "no note"
	}
	return note
}

// orFix keeps the first fix line: the one remedy a fail prints for its first problem.
func orFix(fix, set string) string {
	if fix == "" {
		return set
	}
	return fix
}
