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
// The registry is a DATA FILE. A path names one; an empty path is the default
// embedded below, so the loop runs on a bench with no file of its own. Every
// row is validated on the way in: a bad row is a refusal, never a guess.
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

	// Model is the model id a unit dispatched to this mind RUNS with, where
	// this mind is a model at all. It is registry data and never a constant
	// here: the ladder comes from the file, and so do the ids on it. A mind
	// asked on the bus or as a child is a person or a coordinator and carries
	// none, so a caller that can only dispatch a model keeps today's model and
	// says so.
	Model string `json:"model,omitempty"`
}

// Dispatchable reports whether this mind can be handed a unit as a MODEL: it
// is asked as a card and the registry gives the model id to run it with.
func (m Mind) Dispatchable() bool {
	return m.Ask == AskCard && strings.TrimSpace(m.Model) != ""
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

// Registry is the ladder: every mind, in height then name order.
type Registry struct {
	Minds []Mind `json:"minds"`
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
		m.Model = strings.TrimSpace(m.Model)
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
	sort.SliceStable(reg.Minds, func(i, j int) bool {
		if reg.Minds[i].Height != reg.Minds[j].Height {
			return reg.Minds[i].Height < reg.Minds[j].Height
		}
		return reg.Minds[i].Name < reg.Minds[j].Name
	})
	return &reg, nil
}

// ModelFor is the model id a rung runs a unit with, and whether the registry
// gives it one at all. A rung with no model is not a refusal: it is a mind a
// card cannot be dispatched to, and the caller keeps today's model.
func (r *Registry) ModelFor(name string) (string, bool) {
	m, ok := r.ByName(name)
	if !ok || !m.Dispatchable() {
		return "", false
	}
	return m.Model, true
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
