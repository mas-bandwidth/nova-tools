package release

// The spend gate's production readouts (spendcheck.go): the sprint's store over Redis, each
// paid provider's own count over net/http with its key from the environment (nova-secrets
// exec delivers it; it is never printed), and the subscription friends' harness receipts
// from a file. Each answers what it read, or an error saying why it could not, which the
// gate turns into a refusal naming it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/provbalance"
)

// ProductionSpend is the gate's sources from the cut's flags: the store at --spend-store,
// the providers' readouts (SpendReaders), the receipts at --spend-receipts.
func ProductionSpend(ctx context.Context, o options, w SpendWindow) (SpendSources, error) {
	if strings.TrimSpace(o.spendStore) == "" {
		return SpendSources{}, errors.New("--spend-store <addr> names no sprint store, so the recorded spend cannot be read")
	}
	if SpendStore == nil {
		return SpendSources{}, fmt.Errorf("this build reads no sprint store, so the recorded spend at %s cannot be read: the store's reader is nova-sprint's", o.spendStore)
	}
	s, err := SpendStore(ctx, o.spendStore, os.Getenv)
	if err != nil {
		return SpendSources{}, fmt.Errorf("cannot read the sprint store at %s: %w", o.spendStore, err)
	}
	src := SpendSources{Store: s, Providers: SpendReaders(nil, os.Getenv)}
	if o.spendReceipts != "" {
		src.Receipts = FileReceipts{Path: o.spendReceipts}
	}
	return src, nil
}

// SpendStore reads what the sprint's store at addr recorded, logged in from getenv. The
// store's tables are nova-sprint's, so this package cannot read them itself: a binary that
// links nova-sprint's store sets it (cmd/nova-sprint, spend_store.go), and where it is nil
// (nova-update, since nova-sprint left this repository) the gate is refused as unread,
// and --no-spend-gate --reason is the way past.
var SpendStore func(ctx context.Context, addr string, getenv func(string) string) (RecordedSpend, error)

// SpendReaders is the paid providers' readouts over the transport (nil is
// http.DefaultTransport), their keys read from getenv: openrouter's account activity, and
// the providers whose own count no endpoint answers, each an error naming why.
func SpendReaders(rt http.RoundTripper, getenv func(string) string) []ProviderSpend {
	return []ProviderSpend{
		OpenRouterSpend{RT: rt, Getenv: getenv},
		NoReadout{Name: "opencode", Why: "opencode Zen publishes no usage endpoint (anomalyco/opencode#44189, open): its console is the only readout of its spend"},
		NoReadout{Name: "inception", Why: "no usage endpoint of Inception's is known to this build: its dashboard is the only readout of its spend"},
	}
}

// NoReadout is a provider whose own count cannot be read: every window is unread, with why.
type NoReadout struct{ Name, Why string }

// Provider is the provider's name.
func (n NoReadout) Provider() string { return n.Name }

// Spend is always the error naming why.
func (n NoReadout) Spend(context.Context, SpendWindow) (float64, error) { return 0, errors.New(n.Why) }

// OpenRouter's readouts: the account's activity (GET with a provisioning key, the usage of
// each of the last completed UTC days, by model), and the key's own count of today (GET
// /key, data.usage_daily), the day the activity does not hold yet.
const (
	OpenRouterActivityURL = "https://openrouter.ai/api/v1/activity"
	OpenRouterActivityEnv = "OPENROUTER_PROVISIONING_KEY"
	// OpenRouterActivityDays is how far back the activity reaches.
	OpenRouterActivityDays = 30
)

// OpenRouterSpend is openrouter's own count of a window: the activity's days from the
// window's first through yesterday, and the key's count of today when the window reaches it.
type OpenRouterSpend struct {
	RT     http.RoundTripper
	Getenv func(string) string
}

// Provider is "openrouter".
func (OpenRouterSpend) Provider() string { return "openrouter" }

// Spend is the account's dollars over the window.
func (r OpenRouterSpend) Spend(ctx context.Context, w SpendWindow) (float64, error) {
	today := w.To.UTC().Format(time.DateOnly)
	first := w.From.UTC().Format(time.DateOnly)
	if w.To.UTC().Sub(w.From.UTC()) > OpenRouterActivityDays*24*time.Hour {
		return 0, fmt.Errorf("openrouter's activity holds the last %d completed days and the window begins %s", OpenRouterActivityDays, first)
	}
	total := 0.0
	if first < today {
		key := r.Getenv(OpenRouterActivityEnv)
		if key == "" {
			return 0, fmt.Errorf("%s is not in this environment: run under nova-secrets exec --only %s", OpenRouterActivityEnv, OpenRouterActivityEnv)
		}
		var wire struct {
			Data []struct {
				Date  string   `json:"date"`
				Usage *float64 `json:"usage"`
			} `json:"data"`
		}
		if err := getJSON(ctx, r.RT, OpenRouterActivityURL, key, &wire); err != nil {
			return 0, err
		}
		for _, d := range wire.Data {
			if d.Usage == nil || len(d.Date) < len(time.DateOnly) {
				return 0, errors.New("GET " + OpenRouterActivityURL + " answered a row with no date or usage")
			}
			if day := d.Date[:len(time.DateOnly)]; day >= first && day < today {
				total += *d.Usage
			}
		}
	}
	key := r.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		return 0, errors.New("OPENROUTER_API_KEY is not in this environment: run under nova-secrets exec --only OPENROUTER_API_KEY")
	}
	var wire struct {
		Data struct {
			Daily *float64 `json:"usage_daily"`
		} `json:"data"`
	}
	keyURL := provbalance.OpenRouterKeyURL
	if err := getJSON(ctx, r.RT, keyURL, key, &wire); err != nil {
		return 0, err
	}
	if wire.Data.Daily == nil {
		return 0, errors.New("GET " + keyURL + " answered no data.usage_daily")
	}
	return total + *wire.Data.Daily, nil
}

// getJSON is one GET with the key as a bearer token, bounded, its answer decoded into v;
// the key is never in an error.
func getJSON(ctx context.Context, rt http.RoundTripper, url, key string, v any) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return errors.New("the request to " + url + " could not be made")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	if rt == nil {
		rt = http.DefaultTransport
	}
	resp, err := (&http.Client{Transport: rt}).Do(req)
	if err != nil {
		return fmt.Errorf("GET %s failed: %w", url, err)
	}
	defer resp.Body.Close() // ignored: the answer is read to its bound; a close error loses nothing read
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("GET %s: the answer could not be read: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s answered %d", url, resp.StatusCode)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("GET %s answered no JSON of its shape", url)
	}
	return nil
}

// ReceiptsKind is what a receipts file's "evidence" field says.
const ReceiptsKind = "spend-receipts"

// FileReceipts is the subscription friends' harness receipts as one file:
// {"evidence":"spend-receipts","from":<RFC3339>,"to":<RFC3339>,"friends":{"<friend>":<tokens>}},
// the tokens each friend's harness counted over [from, to). It is read only for a window
// it covers: from the window's start, to within an hour of its end.
type FileReceipts struct{ Path string }

// Tokens is the file's tokens by friend, or why they cannot be read for the window.
func (f FileReceipts) Tokens(_ context.Context, w SpendWindow) (map[string]int64, error) {
	raw, err := os.ReadFile(f.Path)
	if err != nil {
		return nil, fmt.Errorf("cannot read the receipts %s: %w", f.Path, err)
	}
	var file struct {
		Evidence string           `json:"evidence"`
		From     time.Time        `json:"from"`
		To       time.Time        `json:"to"`
		Friends  map[string]int64 `json:"friends"`
	}
	if err := json.Unmarshal(raw, &file); err != nil || file.Evidence != ReceiptsKind || file.Friends == nil {
		return nil, fmt.Errorf("%s is no %s file", f.Path, ReceiptsKind)
	}
	if !file.From.Equal(w.From) || file.To.Before(w.To.Add(-time.Hour)) || file.To.After(w.To) {
		return nil, fmt.Errorf("the receipts %s cover %s..%s, and the window is %s", f.Path,
			file.From.UTC().Format(time.RFC3339), file.To.UTC().Format(time.RFC3339), w)
	}
	return file.Friends, nil
}

// TagTime is when the tag's commit was made, as the forge says it: the start of the spend
// window since that tag.
func (g *GH) TagTime(ctx context.Context, repo, tag string) (time.Time, error) {
	out, err := g.api(ctx, "api", "repos/"+repo+"/commits/"+tag, "--jq", ".commit.committer.date")
	if err != nil {
		return time.Time{}, err
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(out))
	if err != nil {
		return time.Time{}, fmt.Errorf("gh answered no commit date for %s of %s", tag, repo)
	}
	return t, nil
}
