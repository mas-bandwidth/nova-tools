// The registry of minds: the ladder nova-decide routes a unit of work over
// (Glenn 2026-09-18).
//
// A rung is a HEIGHT, not a name: Flash and Pro at the bottom on the DeepSeek
// lineage, then the child rungs -- Opus (Rowan's) and Sol (Stella's) -- at ONE
// height in two lineages, so a failure moves SIDEWAYS before it moves up; then
// the friends, each a mind with an owned lane; then Astra and Fable as the top
// pair, all friends at once above them, and Glenn above that.
//
// Two rungs are chosen by KIND and not by height: a guard, secrets, the
// sandbox, sudo, deploy keys or the network is Johnny's always, and so is a
// fresh take -- the rungs below failed, or a design with one author. The `kinds`
// column is that designation and nothing else.
//
// A designation on a RESERVED mind is a READ, never the work. Johnny is
// reserved -- for a different view, and for the STOP a security read can call --
// so the security kind attaches him as the READER and the work goes to the rung
// the evidence supports. A designation that hands a reserved mind the work
// itself is the routing bug of 2026-09-18: seven units the work set owns as
// rowan-child were routed to a mind that does not take work.
//
// The registry is a DATA FILE. A path names one; an empty path is the default
// embedded below, so the loop runs on a bench with no file of its own. Every
// row is validated on the way in: a bad row is a refusal, never a guess.
//
// Beside the minds it holds two MEASURED tables, data for the same reason the
// ladder is data:
//
//   - floors: the confidence floor PER KIND. One floor for every kind is one
//     number standing in for ten different questions, and on 2026-09-18 it made
//     13 of 13 provider answers escalate -- a 100% escalation rate against
//     tune's own 0.7 cap. Each row carries the measurement behind it in `from`;
//     a kind with no row keeps DefaultFloor, which is a floor with no rows
//     behind it, and the log line says so.
//   - rates: what one model's tokens cost, per million, so a usage row carries
//     usd instead of a dash. A model with no rate is a dash and a NOTE naming
//     the model and the remedy -- never a guessed price.
package decide

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

//go:embed registry.json
var defaultRegistryJSON []byte

// How a rung is asked. A mind is reached one of three ways and the row says
// which, because the caller has to hand the unit over after the decision.
const (
	// AskBus is a note on the bus: a friend, the top pair, all friends, Glenn.
	AskBus = "bus"
	// AskCard is a swarm card: the DeepSeek rungs take work as cards.
	AskCard = "card"
	// AskChild is a child of the coordinator that owns that lineage.
	AskChild = "child"
)

// A mind's availability. A reserved mind is OFF the height ladder and reached
// only by a kind designation -- which is exactly Johnny: reserved for a
// different view, and security's rung regardless.
const (
	AvailabilityAvailable = "available"
	AvailabilityAsleep    = "asleep"
	AvailabilityReserved  = "reserved"
)

// DesignationFreshTake is the kind designation for a fresh take: the rungs
// below failed, or a design with one author.
const DesignationFreshTake = "fresh-take"

// Mind is one rung of the ladder: its name, the lineage it thinks in, its
// height, the kinds it is designated for, the lanes it owns, its availability
// and how it is asked.
type Mind struct {
	Name         string   `json:"name"`
	Lineage      string   `json:"lineage"`
	Height       int      `json:"height"`
	Kinds        []string `json:"kinds"`
	Lanes        []string `json:"lanes"`
	Availability string   `json:"availability"`
	Ask          string   `json:"ask"`
}

// Usable reports whether the height ladder may pick this mind at all. A
// reserved or asleep mind is not on the ladder; a reserved one is still
// reachable by a designation.
func (m Mind) Usable() bool { return m.Availability == AvailabilityAvailable }

// DesignatedFor reports whether this mind is the rung for that kind by
// designation -- by KIND, never by height.
func (m Mind) DesignatedFor(kind string) bool {
	for _, k := range m.Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// Owns reports whether this mind owns the named lane. An empty lane is owned by
// nobody, so a unit that names none has no owner to prefer.
func (m Mind) Owns(lane string) bool {
	lane = strings.TrimSpace(lane)
	if lane == "" {
		return false
	}
	if strings.EqualFold(lane, m.Name) {
		return true
	}
	for _, l := range m.Lanes {
		if strings.EqualFold(l, lane) {
			return true
		}
	}
	return false
}

// KindFloor is one kind's confidence floor and the measurement behind it. From
// is not decoration: a floor nobody can trace is a feeling with a number on it,
// and rule 8 says a floor is tuned from rows or it is not tuned at all.
type KindFloor struct {
	Kind  string  `json:"kind"`
	Floor float64 `json:"floor"`
	From  string  `json:"from,omitempty"`
}

// Rate is what one model's tokens cost, in US dollars per MILLION tokens, and
// where the number was published. Per million rather than per token because
// that is how a provider quotes it, and a rate copied from a page should read
// like the page it was copied from.
type Rate struct {
	Model     string  `json:"model"`
	Provider  string  `json:"provider,omitempty"`
	InputUSD  float64 `json:"input_usd_per_mtok"`
	OutputUSD float64 `json:"output_usd_per_mtok"`
	From      string  `json:"from,omitempty"`
}

// Registry is the ladder: every mind, in height then name order, and the two
// measured tables beside it -- the floor per kind, and the rate per model.
type Registry struct {
	Minds  []Mind      `json:"minds"`
	Floors []KindFloor `json:"floors,omitempty"`
	Rates  []Rate      `json:"rates,omitempty"`
}

// DefaultRegistryJSON is the embedded ladder's own bytes, for a caller that has
// to merge into the document rather than into the parsed ladder -- the floor
// proposal writing a registry on a bench that has never had a file of its own.
// A copy, because the embedded bytes are the package's.
func DefaultRegistryJSON() []byte {
	return append([]byte(nil), defaultRegistryJSON...)
}

// DefaultRegistry is the embedded ladder, the one a bench with no registry file
// runs on.
func DefaultRegistry() (*Registry, error) {
	reg, err := ParseRegistry(defaultRegistryJSON)
	if err != nil {
		return nil, fmt.Errorf("decide: the embedded registry is broken: %w", err)
	}
	return reg, nil
}

// LoadRegistry reads the registry a path names. An empty path is the embedded
// default; an unreadable or invalid file is a refusal naming the path.
func LoadRegistry(path string) (*Registry, error) {
	if strings.TrimSpace(path) == "" {
		return DefaultRegistry()
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("decide: cannot read the registry %s: %w", path, err)
	}
	reg, err := ParseRegistry(raw)
	if err != nil {
		return nil, fmt.Errorf("decide: registry %s: %w", path, err)
	}
	return reg, nil
}

// ParseRegistry parses and validates a registry. Every row must name a mind, a
// lineage, a height at or above zero, an availability of the three, and an ask
// of the three (a row naming none is asked on the bus). A duplicate name, an
// empty table or anything else is a refusal.
func ParseRegistry(data []byte) (*Registry, error) {
	var reg Registry
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("decide: bad registry: not a JSON object: %w", err)
	}
	if len(reg.Minds) == 0 {
		return nil, fmt.Errorf("decide: bad registry: no minds; a ladder with no rungs is not a ladder")
	}
	seen := make(map[string]bool, len(reg.Minds))
	for i := range reg.Minds {
		m := &reg.Minds[i]
		m.Name = strings.TrimSpace(m.Name)
		m.Lineage = strings.TrimSpace(m.Lineage)
		m.Availability = strings.TrimSpace(m.Availability)
		m.Ask = strings.TrimSpace(m.Ask)
		switch {
		case m.Name == "":
			return nil, fmt.Errorf("decide: bad registry: row %d has no name", i+1)
		case m.Lineage == "":
			return nil, fmt.Errorf("decide: bad registry: %s names no lineage; sideways before up needs one", m.Name)
		case m.Height < 0:
			return nil, fmt.Errorf("decide: bad registry: %s has height %d; a rung is at or above zero", m.Name, m.Height)
		case seen[m.Name]:
			return nil, fmt.Errorf("decide: bad registry: %s is named twice", m.Name)
		}
		seen[m.Name] = true
		if m.Availability == "" {
			return nil, fmt.Errorf("decide: bad registry: %s names no availability (%s, %s or %s)", m.Name, AvailabilityAvailable, AvailabilityAsleep, AvailabilityReserved)
		}
		switch m.Availability {
		case AvailabilityAvailable, AvailabilityAsleep, AvailabilityReserved:
		default:
			return nil, fmt.Errorf("decide: bad registry: %s has availability %q, want %s, %s or %s", m.Name, m.Availability, AvailabilityAvailable, AvailabilityAsleep, AvailabilityReserved)
		}
		if m.Ask == "" {
			m.Ask = AskBus
		}
		switch m.Ask {
		case AskBus, AskCard, AskChild:
		default:
			return nil, fmt.Errorf("decide: bad registry: %s is asked by %q, want %s, %s or %s", m.Name, m.Ask, AskBus, AskCard, AskChild)
		}
	}
	if err := validateFloors(reg.Floors); err != nil {
		return nil, err
	}
	if err := validateRates(reg.Rates); err != nil {
		return nil, err
	}
	sort.SliceStable(reg.Minds, func(i, j int) bool {
		if reg.Minds[i].Height != reg.Minds[j].Height {
			return reg.Minds[i].Height < reg.Minds[j].Height
		}
		return reg.Minds[i].Name < reg.Minds[j].Name
	})
	return &reg, nil
}

// validateFloors checks the floor table: one row per kind, a kind the ladder
// knows, and a floor that is a confidence. A floor for a kind nobody routes is
// a typo that would sit in the file being silently ignored, which is how a
// floor stops meaning anything.
func validateFloors(floors []KindFloor) error {
	seen := make(map[string]bool, len(floors))
	for i := range floors {
		f := &floors[i]
		f.Kind = strings.TrimSpace(f.Kind)
		if f.Kind == "" {
			return fmt.Errorf("decide: bad registry: floor row %d names no kind; it wants one of %s", i+1, strings.Join(Kinds, ", "))
		}
		if !KnownKind(f.Kind) {
			return fmt.Errorf("decide: bad registry: floor row %d names kind %q, which is not one of %s", i+1, f.Kind, strings.Join(Kinds, ", "))
		}
		if seen[f.Kind] {
			return fmt.Errorf("decide: bad registry: kind %s has two floors; one kind, one floor", f.Kind)
		}
		seen[f.Kind] = true
		if err := ValidFloor(f.Floor); err != nil {
			return fmt.Errorf("decide: bad registry: kind %s: %w", f.Kind, err)
		}
	}
	return nil
}

// validateRates checks the rate table: one row per model, and a price that is a
// price. A negative rate is not a discount, and a model named twice is two
// answers to one question.
func validateRates(rates []Rate) error {
	seen := make(map[string]bool, len(rates))
	for i := range rates {
		r := &rates[i]
		r.Model = strings.TrimSpace(r.Model)
		r.Provider = strings.TrimSpace(r.Provider)
		if r.Model == "" {
			return fmt.Errorf("decide: bad registry: rate row %d names no model", i+1)
		}
		if seen[r.Model] {
			return fmt.Errorf("decide: bad registry: model %s has two rates; one model, one rate", r.Model)
		}
		seen[r.Model] = true
		if r.InputUSD < 0 || r.OutputUSD < 0 {
			return fmt.Errorf("decide: bad registry: model %s has a negative rate (in %g, out %g per Mtok); a price is at or above zero", r.Model, r.InputUSD, r.OutputUSD)
		}
	}
	return nil
}

// Where an effective floor came from. Every decision line carries it, because a
// floor measured from rows and a floor nobody has ever measured are two
// different claims and a reader cannot tell them apart from the number.
const (
	// FloorFromFlag is a floor the caller named on the command line. An
	// explicit number beats a table: the person asking is looking at something
	// the table does not know.
	FloorFromFlag = "flag"
	// FloorFromKind is the registry's floor for this unit's kind, measured.
	FloorFromKind = "kind"
	// FloorFromBuiltIn is DefaultFloor: no row, no measurement, a floor with
	// nothing behind it.
	FloorFromBuiltIn = "built-in"
)

// FloorFor is the registry's floor for one kind, and whether it holds one at
// all. A kind with no row is not a kind with a floor of zero.
func (r *Registry) FloorFor(kind string) (KindFloor, bool) {
	kind = strings.TrimSpace(kind)
	if r == nil || kind == "" {
		return KindFloor{}, false
	}
	for _, f := range r.Floors {
		if f.Kind == kind {
			return f, true
		}
	}
	return KindFloor{}, false
}

// ResolveFloor answers the floor one decision is gated on and says where it
// came from: the caller's flag where they gave one, else this kind's measured
// floor, else the built-in default.
func ResolveFloor(reg *Registry, kind string, flagFloor float64, flagGiven bool) (float64, string) {
	if flagGiven {
		return flagFloor, FloorFromFlag
	}
	if f, ok := reg.FloorFor(kind); ok {
		return f.Floor, FloorFromKind
	}
	return DefaultFloor, FloorFromBuiltIn
}

// RateFor is the rate table's row for one model. A model with no row is a dash
// on the usage line and a NOTE, never a price somebody made up.
func (r *Registry) RateFor(model string) (Rate, bool) {
	model = strings.TrimSpace(model)
	if r == nil || model == "" {
		return Rate{}, false
	}
	for _, rate := range r.Rates {
		if rate.Model == model {
			return rate, true
		}
	}
	return Rate{}, false
}

// USD is what a call of this many tokens cost at this rate. The counters are
// per million, so the arithmetic is done once, here, and never at a call site.
func (r Rate) USD(inputTokens, outputTokens int) float64 {
	return float64(inputTokens)/1e6*r.InputUSD + float64(outputTokens)/1e6*r.OutputUSD
}

// ByName finds one mind.
func (r *Registry) ByName(name string) (Mind, bool) {
	for _, m := range r.Minds {
		if m.Name == name {
			return m, true
		}
	}
	return Mind{}, false
}

// AtHeight is every mind on one rung, in name order.
func (r *Registry) AtHeight(h int) []Mind {
	out := make([]Mind, 0, 2)
	for _, m := range r.Minds {
		if m.Height == h {
			out = append(out, m)
		}
	}
	return out
}

// Heights is every rung height the registry holds, ascending.
func (r *Registry) Heights() []int {
	seen := map[int]bool{}
	out := make([]int, 0, len(r.Minds))
	for _, m := range r.Minds {
		if !seen[m.Height] {
			seen[m.Height] = true
			out = append(out, m.Height)
		}
	}
	sort.Ints(out)
	return out
}

// TopHeight is the highest rung: above it there is nothing to escalate to.
func (r *Registry) TopHeight() int {
	top := 0
	for _, m := range r.Minds {
		if m.Height > top {
			top = m.Height
		}
	}
	return top
}

// DesignatedFor is every mind designated for a kind, in height then name order.
func (r *Registry) DesignatedFor(kind string) []Mind {
	out := make([]Mind, 0, 1)
	for _, m := range r.Minds {
		if m.DesignatedFor(kind) {
			out = append(out, m)
		}
	}
	return out
}

// RungName names a height: one mind, or every mind on that rung joined by "/"
// -- the two lineages of one rung are one rung.
func (r *Registry) RungName(h int) string {
	at := r.AtHeight(h)
	names := make([]string, 0, len(at))
	for _, m := range at {
		names = append(names, m.Name)
	}
	if len(names) == 0 {
		return "-"
	}
	return strings.Join(names, "/")
}
