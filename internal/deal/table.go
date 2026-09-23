package deal

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/fleet"
)

// THE CONSUMERS TABLE is not a new file. A bench's capabilities are its row in the machines
// registry (internal/fleet), written as `key=value` tokens in the row's notes field the way
// `allow-shared=` and `certified=` already are; a friend's are their entry in the bus's
// participants.json, with the fixed width of four. Nothing about a consumer lives in two
// places, and a bench that is not in the registry is not a consumer.
//
// The tokens a bench row may carry, all optional:
//
//	legs=go,c+cpp,python   the toolchain legs installed (every bench runs any card is a
//	                       POLICY: when it holds, every row carries every leg)
//	locality=datacenter    house | datacenter
//	wall=180               the longest task this bench's idle bound tolerates, in minutes
//	iso=net,nonet          the isolation modes it can offer
//	kinds=card,script      the task kinds it takes; empty means all
//	routes=flash,pro       the model routes reachable from it; empty means all
//	width=12               how deep its queue is kept; empty means the ramp's width
//	mirror=o/n@dev         a warm mirror it holds; repeatable
//
// An unknown token is ignored, not refused: the registry is shared with verbs that do not
// know this vocabulary, and a typo here must not stop a card being placed elsewhere. A
// malformed VALUE for a token this package does know (`wall=soon`) IS refused by name.
const (
	tokenLegs     = "legs="
	tokenLocality = "locality="
	tokenWall     = "wall="
	tokenIso      = "iso="
	tokenKinds    = "kinds="
	tokenRoutes   = "routes="
	tokenWidth    = "width="
	tokenMirror   = "mirror="
)

// Benches reads the machines registry and returns one Capabilities row per machine that may
// take work. A machine the registry refuses (a runner host, a services host) is not a
// consumer and is silently absent: the registry has already said why, on its own line.
func Benches(registryPath string) ([]Capabilities, error) {
	reg, err := fleet.ReadRegistry(registryPath)
	if err != nil {
		return nil, err
	}
	var out []Capabilities
	for _, m := range reg.Machines() {
		if err := reg.RequireBench(m.Name); err != nil {
			continue
		}
		c, err := benchCapabilities(m)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func benchCapabilities(m fleet.Machine) (Capabilities, error) {
	c := Capabilities{Name: m.Name, Kind: KindBench}
	for _, tok := range strings.Fields(m.Notes) {
		switch {
		case strings.HasPrefix(tok, tokenLegs):
			c.Legs = splitList(tok[len(tokenLegs):])
		case strings.HasPrefix(tok, tokenLocality):
			c.Locality = strings.ToLower(tok[len(tokenLocality):])
		case strings.HasPrefix(tok, tokenWall):
			n, err := strconv.Atoi(tok[len(tokenWall):])
			if err != nil || n < 0 {
				return Capabilities{}, fmt.Errorf("machine %s: %s wants whole minutes, got %q", m.Name, strings.TrimSuffix(tokenWall, "="), tok[len(tokenWall):])
			}
			c.MaxWallMinutes = n
		case strings.HasPrefix(tok, tokenIso):
			c.Isolation = splitList(tok[len(tokenIso):])
		case strings.HasPrefix(tok, tokenKinds):
			c.Kinds = splitList(tok[len(tokenKinds):])
		case strings.HasPrefix(tok, tokenRoutes):
			c.Routes = splitList(tok[len(tokenRoutes):])
		case strings.HasPrefix(tok, tokenWidth):
			n, err := strconv.Atoi(tok[len(tokenWidth):])
			if err != nil || n < 0 {
				return Capabilities{}, fmt.Errorf("machine %s: %s wants a whole number, got %q", m.Name, strings.TrimSuffix(tokenWidth, "="), tok[len(tokenWidth):])
			}
			c.Width = n
		case strings.HasPrefix(tok, tokenMirror):
			c.Mirrors = append(c.Mirrors, tok[len(tokenMirror):])
		}
	}
	if c.Locality == "" {
		c.Locality = LocalityAny
	}
	return c, nil
}

// Friends reads the bus roster and returns one Capabilities row per participant: a friend is
// a consumer with a width of four, every leg (a person is not a toolchain), any locality (a
// person's window is wherever it is), and the judgement kinds. Rowan is the coordinator and
// is a consumer like any other -- the coordinator's own lane shows on the wall.
func Friends(busDir string) ([]Capabilities, error) {
	cfg, err := bus.LoadConfig(busDir)
	if err != nil {
		return nil, err
	}
	var out []Capabilities
	for _, p := range cfg.Participants {
		out = append(out, Capabilities{
			Name:     strings.ToLower(strings.TrimSpace(p.Name)),
			Kind:     KindFriend,
			Locality: LocalityAny,
			Width:    FriendWidth,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// FriendsFromNames is the same row for a caller that already holds the names (a test, or a
// coordinator whose bus clone is not at hand).
func FriendsFromNames(names []string) []Capabilities {
	var out []Capabilities
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == "" {
			continue
		}
		out = append(out, Capabilities{Name: n, Kind: KindFriend, Locality: LocalityAny, Width: FriendWidth})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ApplyPresence stamps the measured heartbeat onto the table: present[name] is true when
// `friend:<name>` or `bench:<name>` is live in the store. A name the map does not carry is
// NOT present -- no evidence is not negative evidence about the WORK, but about presence the
// key IS the evidence, and an absent key is exactly what AWAY means.
func ApplyPresence(cs []Capabilities, present map[string]bool) []Capabilities {
	out := make([]Capabilities, 0, len(cs))
	for _, c := range cs {
		c.Present = present[strings.ToLower(c.Name)]
		out = append(out, c)
	}
	return out
}

// LoadTable is the two halves together, for the common caller.
func LoadTable(registryPath, busDir string, friendNames []string) ([]Capabilities, error) {
	var out []Capabilities
	if strings.TrimSpace(registryPath) != "" {
		benches, err := Benches(registryPath)
		if err != nil {
			return nil, err
		}
		out = append(out, benches...)
	}
	switch {
	case strings.TrimSpace(busDir) != "":
		friends, err := Friends(busDir)
		if err != nil {
			return nil, err
		}
		out = append(out, friends...)
	case len(friendNames) > 0:
		out = append(out, FriendsFromNames(friendNames)...)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the consumers table is empty; it wants a machines registry (--machines) or a bus clone (--bus), and refuses to guess")
	}
	return out, nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ReadFileList is a small helper for a caller holding a newline list of names.
func ReadFileList(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	return out, nil
}
