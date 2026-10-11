package config

import (
	"encoding/json"
	"fmt"
	"math/big"
	"slices"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
)

// A route's prices are read from its provider's published list, never typed and
// left: nova-config route prices --refresh sets each enabled route's price fields
// from the list with price_as_of the day it was read and the list's URL as
// price_source (docs/SPEC-CONFIG.md, "route prices"). A flash route priced by hand
// on 2026-10-01 at a tenth of the list's input price went unread for days and the
// dashboard showed under half the real spend; the refresh is what keeps a row from
// being a stale hand-typed number.

// OpenRouterModelsURL is OpenRouter's public models endpoint: every model's
// pricing in USD per token, read with no key.
const OpenRouterModelsURL = "https://openrouter.ai/api/v1/models"

// ProviderOpenRouter and ProviderOpenCode are the provider words of the routes
// whose prices a published list gives.
const (
	ProviderOpenRouter = "openrouter"
	ProviderOpenCode   = "opencode"
)

// PriceList is where a provider's route prices are read: the list's URL, and
// whether the list is the provider's own or assumed from another's.
type PriceList struct {
	Provider string // the route rows' provider word
	URL      string
	Assumed  string // "" when the list is the provider's own; else whose list stands in
}

// PriceLists are the providers with a list to refresh from, in order. OpenCode
// publishes none, so its rows are priced from OpenRouter's and keep that note
// until it does.
var PriceLists = []PriceList{
	{Provider: ProviderOpenRouter, URL: OpenRouterModelsURL},
	{Provider: ProviderOpenCode, URL: OpenRouterModelsURL, Assumed: ProviderOpenRouter},
}

// PriceListOf is the list a provider's routes are refreshed from; ok is false
// when it has none.
func PriceListOf(provider string) (PriceList, bool) {
	for _, l := range PriceLists {
		if l.Provider == provider {
			return l, true
		}
	}
	return PriceList{}, false
}

// PriceListProviders names the providers with a list, for a refusal.
func PriceListProviders() string {
	var names []string
	for _, l := range PriceLists {
		n := l.Provider
		if l.Assumed != "" {
			n += " (assumed from " + l.Assumed + ")"
		}
		names = append(names, n)
	}
	return strings.Join(names, ", ")
}

// ListPrice is one model's prices as a list publishes them, each a canonical
// decimal in the route's units (USD per million tokens; the request fee USD per
// request), "" when the list carries none.
type ListPrice struct {
	Input, CacheRead, CacheWrite, Output, Request string
}

// fields is the list price by the route field it sets.
func (p ListPrice) fields() map[string]string {
	return map[string]string{cardcost.FieldInput: p.Input, cardcost.FieldCacheRead: p.CacheRead, cardcost.FieldCacheWrite: p.CacheWrite,
		cardcost.FieldOutput: p.Output, cardcost.FieldRequest: p.Request}
}

// listFields are the route fields a list sets, in the order a line names them.
var listFields = []string{cardcost.FieldInput, cardcost.FieldCacheRead, cardcost.FieldCacheWrite, cardcost.FieldOutput, cardcost.FieldRequest}

// ParseOpenRouterList reads OpenRouter's models endpoint: {"data":[{"id",
// "pricing":{"prompt","completion","input_cache_read","input_cache_write",
// "request"}}]}, each price a decimal string in USD per token (per request for
// the fee). A model whose price is not a non-negative decimal (a router's -1) is
// left out; a list with no model at all is an error, so an empty answer never
// reads as every route missing.
func ParseOpenRouterList(b []byte) (map[string]ListPrice, error) {
	var doc struct {
		Data []struct {
			ID      string            `json:"id"`
			Pricing map[string]string `json:"pricing"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("the models list is not the JSON OpenRouter publishes: %v", err)
	}
	million := big.NewRat(1_000_000, 1)
	out := map[string]ListPrice{}
	for _, m := range doc.Data {
		if m.ID == "" || m.Pricing == nil {
			continue
		}
		var p ListPrice
		ok := true
		for _, f := range []struct {
			key   string
			dst   *string
			scale *big.Rat
		}{
			{"prompt", &p.Input, million}, {"input_cache_read", &p.CacheRead, million}, {"input_cache_write", &p.CacheWrite, million},
			{"completion", &p.Output, million}, {"request", &p.Request, nil},
		} {
			v, has := m.Pricing[f.key]
			if !has || v == "" {
				continue
			}
			r, err := cardcost.Decimal(v)
			if err != nil {
				ok = false
				break
			}
			if f.scale != nil {
				r = new(big.Rat).Mul(r, f.scale)
			}
			if f.key == "request" && r.Sign() == 0 {
				continue // no fee is no field, as a route with none keeps it empty
			}
			*f.dst = cardcost.Text(r)
		}
		if ok && (p.Input != "" || p.Output != "") {
			out[m.ID] = p
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the models list holds no priced model")
	}
	return out, nil
}

// listModel finds a route's model on the list: its own id, else the one id whose
// last part is the model's last part (an OpenCode model named without its
// vendor). found is false when no id, or more than one, answers.
func listModel(list map[string]ListPrice, model string) (id string, found bool) {
	if _, ok := list[model]; ok {
		return model, true
	}
	tail := model[strings.LastIndex(model, "/")+1:]
	var hits []string
	for id := range list {
		if id[strings.LastIndex(id, "/")+1:] == tail {
			hits = append(hits, id)
		}
	}
	if len(hits) != 1 {
		return "", false
	}
	return hits[0], true
}

// PriceJumpFactor is how far a price may move between two reads and still be set
// by the refresh: a list price above twice or below half of the row's is a
// judgment, set by hand after a reader has looked, never silently.
const PriceJumpFactor = 2

// PriceStalePercent is how far a row's price may differ from the list and still
// read as current: past it the row is stale.
const PriceStalePercent = 10

// PriceJump says a price moved past PriceJumpFactor either way from have to
// list. A row with no price (have "") is never a jump: its first price is the
// list's. A price of 0 becoming one above 0 is.
func PriceJump(have, list string) bool {
	h, err1 := cardcost.Decimal(have)
	l, err2 := cardcost.Decimal(list)
	if err1 != nil || err2 != nil {
		return false
	}
	factor := big.NewRat(PriceJumpFactor, 1)
	return l.Cmp(new(big.Rat).Mul(h, factor)) > 0 || h.Cmp(new(big.Rat).Mul(l, factor)) > 0
}

// PriceStale says a row's price differs from the list by more than
// PriceStalePercent of the list's: the row is a number nobody re-read. A price
// on one side only is stale; on neither, not.
func PriceStale(have, list string) bool {
	if have == "" || list == "" {
		return have != list
	}
	h, err1 := cardcost.Decimal(have)
	l, err2 := cardcost.Decimal(list)
	if err1 != nil || err2 != nil {
		return true
	}
	diff := new(big.Rat).Sub(h, l)
	diff.Abs(diff)
	// diff*100 > list*percent, exactly
	return new(big.Rat).Mul(diff, big.NewRat(100, 1)).Cmp(new(big.Rat).Mul(l, big.NewRat(PriceStalePercent, 1))) > 0
}

// PriceJudgment is a price the refresh would not set, or a price the row still
// holds stale: it moved past PriceJumpFactor since the row's last read, or it
// differs from the list by more than PriceStalePercent.
type PriceJudgment struct {
	Field, Have, List string
}

// PriceRefresh is what a refresh does to one route: the fields it sets, the
// prices it refuses as judgments, the prices it names stale, or why the list
// says nothing of it.
type PriceRefresh struct {
	Route     string
	Provider  string
	Model     string
	ListID    string            // the model's id on the list; "" when missing
	Assumed   string            // whose list stands in for the provider's own
	Changes   map[string]string // the fields the write sets, price_as_of and price_source among them; nil when none
	Moved     []string          // the price fields whose value changes, in listFields order
	Judgments []PriceJudgment
	Stale     []PriceJudgment // the fields whose row differed from the list by more than PriceStalePercent, in listFields order
	Missing   bool            // the model is not on the list: nothing is set
}

// PlanPriceRefresh is the refresh of the enabled routes of the given providers
// ("" every provider with a list) against a list read from source on today
// (YYYY-MM-DD), in name order. A price the list carries is set unless it moved
// past PriceJumpFactor (a judgment, left as it is); a field the list does not
// carry is left as it is. A route the refresh reaches gets price_as_of today and
// price_source the list's URL, unless no price is set from the list (a missing
// model; every price a judgment) or the row already says both. A field whose
// stored price differs from the list by more than PriceStalePercent is named
// stale, whether or not the refresh sets it.
func PlanPriceRefresh(routes []Row, list map[string]ListPrice, provider, source, today string) []PriceRefresh {
	var out []PriceRefresh
	sorted := slices.Clone(routes)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	for _, r := range sorted {
		f := r.Fields
		if f["enabled"] == "false" || (provider != "" && f["provider"] != provider) {
			continue
		}
		pl, ok := PriceListOf(f["provider"])
		if !ok || pl.URL != source {
			continue
		}
		pr := PriceRefresh{Route: r.Name, Provider: f["provider"], Model: f["model"], Assumed: pl.Assumed}
		id, found := listModel(list, f["model"])
		if !found {
			pr.Missing = true
			out = append(out, pr)
			continue
		}
		pr.ListID = id
		changes := map[string]string{}
		set := 0
		for _, field := range listFields {
			want := list[id].fields()[field]
			if want == "" {
				continue
			}
			have := f[field]
			if have != "" {
				if c, err := cardcost.Canonical(have); err == nil {
					have = c
				}
			}
			if have != "" && PriceStale(have, want) {
				pr.Stale = append(pr.Stale, PriceJudgment{Field: field, Have: have, List: want})
			}
			if PriceJump(have, want) {
				pr.Judgments = append(pr.Judgments, PriceJudgment{Field: field, Have: have, List: want})
				continue
			}
			set++
			if have != want {
				changes[field] = want
				pr.Moved = append(pr.Moved, field)
			}
		}
		if set > 0 {
			if f[cardcost.FieldAsOf] != today {
				changes[cardcost.FieldAsOf] = today
			}
			if f[cardcost.FieldSource] != source {
				changes[cardcost.FieldSource] = source
			}
		}
		if len(changes) > 0 {
			pr.Changes = changes
		}
		out = append(out, pr)
	}
	return out
}
