/*
Package messagebus is the machinery under nova-message-bus: the table's participant
config, the note header, the id, the answered rule, the receipts file, and the push
protocol. The binary in cmd/nova-message-bus is flags, dispatch and output grammar over
this package.

The table it works on is a git repository holding one lane directory per sender and one
Markdown file per note. This package never invents a path: every entry point takes the
table root from its caller, and the caller took it from a flag.

Nothing here enforces the covenant that everything read on a table is data and no note is
a grant. That sentence is in SPEC.md, where a person reads it, because a tool cannot
enforce it and should not pretend to.
*/
package messagebus

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ConfigName is the file, at the table root, that lists who is at the table. The name is
// fixed rather than a flag because it is a property of the table, not of an invocation:
// two lines running this tool over one table must read one roster, and a --config flag
// would let them disagree about who exists. The table root itself is always a flag.
const ConfigName = "participants.json"

// Participant is one name at the table.
//
// Lane may be empty. A person who is written TO and never writes -- Glenn on the table
// this was built for -- is a participant with no lane: addressable, and refused as a
// sender, because a sender with no lane has nowhere for a note to go.
type Participant struct {
	Name     string   `json:"name"`
	Lane     string   `json:"lane,omitempty"`
	Aliases  []string `json:"aliases,omitempty"`
	GitName  string   `json:"git_name,omitempty"`
	GitEmail string   `json:"git_email,omitempty"`
}

// Group is one name that stands for several participants, so that a table which really
// does address "Everybody at the table" can say so and still be checkable. A group is
// addressable and is never a sender.
type Group struct {
	Name    string   `json:"name"`
	Members []string `json:"members"`
}

// Config is the whole roster.
type Config struct {
	Participants []Participant `json:"participants"`
	Groups       []Group       `json:"groups,omitempty"`

	// byName maps a lower-cased name or alias to the participant index it names, and
	// groups maps the same to a group index. Built by validate, never decoded.
	byName  map[string]int
	byGroup map[string]int
}

// LoadConfig reads and validates the roster at <table>/participants.json.
//
// Decoding is strict: an unknown field is an error rather than a silently ignored line,
// because the failure mode this guards is a roster whose "alias" key was typed "aliass"
// and whose owner believed a name was known.
func LoadConfig(table string) (*Config, error) {
	if table == "" {
		return nil, errors.New("no table root given")
	}
	path := filepath.Join(table, ConfigName)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ConfigName, err)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var c Config
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", ConfigName, err)
	}
	// A second top-level value would otherwise be silently ignored, and a roster whose
	// real content sits in the second object is a roster nobody is reading.
	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: more than one JSON value in the file", ConfigName)
	}
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", ConfigName, err)
	}
	return &c, nil
}

// validate builds the lookup tables and refuses a roster that cannot be used
// unambiguously. Every refusal here is a roster a table could otherwise run on for weeks
// before two names collided at the wrong moment.
func (c *Config) validate() error {
	if len(c.Participants) == 0 {
		return errors.New("no participants")
	}
	c.byName = make(map[string]int)
	c.byGroup = make(map[string]int)
	lanes := make(map[string]string)
	for i := range c.Participants {
		p := &c.Participants[i]
		p.Name = strings.TrimSpace(p.Name)
		if p.Name == "" {
			return fmt.Errorf("participant %d: empty name", i)
		}
		for _, n := range append([]string{p.Name}, p.Aliases...) {
			n = strings.TrimSpace(n)
			if n == "" {
				return fmt.Errorf("participant %q: empty alias", p.Name)
			}
			key := fold(n)
			if prev, dup := c.byName[key]; dup {
				return fmt.Errorf("name %q names both %q and %q", n, c.Participants[prev].Name, p.Name)
			}
			c.byName[key] = i
		}
		if p.Lane == "" {
			// Addressable, never a sender. GitName and GitEmail are meaningless without
			// a lane, and accepting them would suggest this line can send.
			if p.GitName != "" || p.GitEmail != "" {
				return fmt.Errorf("participant %q: has a git identity but no lane", p.Name)
			}
			continue
		}
		if err := validLane(p.Lane); err != nil {
			return fmt.Errorf("participant %q: %w", p.Name, err)
		}
		if prev, dup := lanes[p.Lane]; dup {
			return fmt.Errorf("lane %q belongs to both %q and %q", p.Lane, prev, p.Name)
		}
		lanes[p.Lane] = p.Name
		if strings.TrimSpace(p.GitName) == "" || strings.TrimSpace(p.GitEmail) == "" {
			return fmt.Errorf("participant %q: a lane needs git_name and git_email (the commit identity, passed with git -c)", p.Name)
		}
	}
	for i := range c.Groups {
		g := &c.Groups[i]
		g.Name = strings.TrimSpace(g.Name)
		if g.Name == "" {
			return fmt.Errorf("group %d: empty name", i)
		}
		key := fold(g.Name)
		if prev, dup := c.byName[key]; dup {
			return fmt.Errorf("group %q is also participant %q", g.Name, c.Participants[prev].Name)
		}
		if prev, dup := c.byGroup[key]; dup {
			return fmt.Errorf("group %q is declared twice (also %q)", g.Name, c.Groups[prev].Name)
		}
		c.byGroup[key] = i
		if len(g.Members) == 0 {
			return fmt.Errorf("group %q: no members", g.Name)
		}
		for _, m := range g.Members {
			if _, ok := c.byName[fold(strings.TrimSpace(m))]; !ok {
				return fmt.Errorf("group %q: member %q is not a participant", g.Name, m)
			}
		}
	}
	return nil
}

// validLane holds the one shape a lane may have. It is not decoration: the lane is joined
// to the table root and written into, so a lane of "../.." or an absolute path would put
// a note outside the table, and a lane whose name differs from its id prefix would make
// two notes with one id.
func validLane(lane string) error {
	rest, ok := strings.CutPrefix(lane, "from-")
	if !ok {
		return fmt.Errorf("lane %q: a lane is from-<slug>", lane)
	}
	if rest == "" {
		return fmt.Errorf("lane %q: empty slug", lane)
	}
	for _, r := range rest {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return fmt.Errorf("lane %q: a slug is lower-case letters, digits and hyphens", lane)
	}
	return nil
}

// Slug is the lane's own name without the from- prefix, and the first half of every id
// this participant is assigned. Empty for a participant with no lane.
func (p Participant) Slug() string {
	rest, _ := strings.CutPrefix(p.Lane, "from-")
	return rest
}

// Lookup resolves one name or alias, exactly, to a participant. It is the strict form:
// no prefix matching, no parentheticals. Used for --as, where the caller is naming
// themselves and a near miss should be refused rather than guessed at.
func (c *Config) Lookup(name string) (Participant, bool) {
	i, ok := c.byName[fold(strings.TrimSpace(name))]
	if !ok {
		return Participant{}, false
	}
	return c.Participants[i], true
}

// Senders lists every participant that has a lane, by name.
func (c *Config) Senders() []string {
	var out []string
	for _, p := range c.Participants {
		if p.Lane != "" {
			out = append(out, p.Name)
		}
	}
	sort.Strings(out)
	return out
}

// KnownNames lists every name, alias and group the roster knows, for a refusal that tells
// the caller what it could have said instead.
func (c *Config) KnownNames() []string {
	var out []string
	for _, p := range c.Participants {
		out = append(out, p.Name)
		out = append(out, p.Aliases...)
	}
	for _, g := range c.Groups {
		out = append(out, g.Name)
	}
	sort.Strings(out)
	return out
}

// LaneOwner answers which participant owns a lane directory, so a note found on disk can
// be attributed to the lane it sits in rather than to the name it claims in its own
// From line.
func (c *Config) LaneOwner(lane string) (Participant, bool) {
	for _, p := range c.Participants {
		if p.Lane != "" && p.Lane == lane {
			return p, true
		}
	}
	return Participant{}, false
}

// Lanes lists every lane directory the roster declares, sorted.
func (c *Config) Lanes() []string {
	var out []string
	for _, p := range c.Participants {
		if p.Lane != "" {
			out = append(out, p.Lane)
		}
	}
	sort.Strings(out)
	return out
}

func fold(s string) string { return strings.ToLower(s) }
